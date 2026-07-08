package main

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============================================================================
// COMPONENT: MEMORY STORAGE & SESSION MANAGER
// ============================================================================

// SessionManager quản lý danh sách tài khoản trong RAM, điều phối Round-Robin,
// bám dính session (Sticky Session) và quản lý Circuit Breaker (Blacklist 24h).
type SessionManager struct {
	mu           sync.RWMutex
	pool         []*CFAccount
	poolIndex    uint64
	sessionToAcc map[string]*CFAccount
	penalized    map[string]time.Time // Quản lý án phạt tập trung
	modelMap     map[string]string    // Ánh xạ từ alias sang target model
	modelsList   []string             // Lưu danh sách alias để xuất ra v1/models
	rStore       *RedisStore          // Tích hợp Redis làm kho lưu trữ session/quota
}

// NewSessionManager khởi tạo một SessionManager mới với các map rỗng.
func NewSessionManager(rStore *RedisStore) *SessionManager {
	return &SessionManager{
		sessionToAcc: make(map[string]*CFAccount),
		penalized:    make(map[string]time.Time),
		modelMap:     make(map[string]string),
		rStore:       rStore,
	}
}

// LoadAccountsFromCSV đọc và nạp danh sách tài khoản từ file CSV có đường dẫn filePath.
func (sm *SessionManager) LoadAccountsFromCSV(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comment = '#'
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.pool = nil // Reset lại pool tài khoản
	for i, record := range records {
		// Bỏ qua dòng tiêu đề nếu có
		if i == 0 && (strings.Contains(record[0], "id") || strings.Contains(record[1], "token")) {
			continue
		}
		if len(record) < 2 {
			continue
		}

		accID := strings.TrimSpace(record[0])
		token := strings.TrimSpace(record[1])

		if accID != "" && token != "" {
			sm.pool = append(sm.pool, &CFAccount{
				AccountID:          accID,
				APIToken:           token,
				IsActive:           true,
				CurrentNeuronsUsed: 0,
			})
		}
	}
	log.Printf("[Pool Initializer] Đã nạp thành công %d tài khoản từ file CSV.", len(sm.pool))
	return nil
}

// GetAccount lấy ra tài khoản được bám dính (Sticky) cho Session hiện tại
// hoặc cấp phát một tài khoản mới theo giải thuật Round-Robin nếu chưa có hoặc tài khoản cũ đã bị cạn quota.
func (sm *SessionManager) GetAccount(sessionID string) (CFAccount, bool) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now()

	// 1. Nếu có Redis, dùng Redis làm kho lưu trữ phân tán
	if sm.rStore != nil && sm.rStore.active {
		ctx := sm.rStore.ctx
		
		// A. Tra cứu session bám dính trong Redis
		cachedAccID, err := sm.rStore.client.Get(ctx, "session:"+sessionID).Result()
		if err != nil && err != redis.Nil {
			sm.handleRedisError(err)
		}
		
		if err == nil && cachedAccID != "" {
			penExistsVal, penErr := sm.rStore.client.Exists(ctx, "penalty:"+cachedAccID).Result()
			if penErr != nil {
				sm.handleRedisError(penErr)
			}
			penExists := penExistsVal > 0
			
			var neuronsUsed int64 = 0
			nVal, errVal := sm.rStore.client.Get(ctx, "neurons:"+cachedAccID).Result()
			if errVal != nil && errVal != redis.Nil {
				sm.handleRedisError(errVal)
			}
			if errVal == nil {
				neuronsUsed, _ = strconv.ParseInt(nVal, 10, 64)
			}

			if !penExists && neuronsUsed < HandoffThreshold {
				for _, acc := range sm.pool {
					if acc.AccountID == cachedAccID {
						return *acc, true
					}
				}
			}
			
			if sm.rStore.active {
				sm.rStore.client.Del(ctx, "session:"+sessionID)
			}
		}

		// B. Cấp phát Round-Robin với Redis
		total := len(sm.pool)
		if total == 0 {
			return CFAccount{}, false
		}

		for i := 0; i < total; i++ {
			idx := atomic.AddUint64(&sm.poolIndex, 1)
			acc := sm.pool[idx%uint64(total)]

			penExistsVal, penErr := sm.rStore.client.Exists(ctx, "penalty:"+acc.AccountID).Result()
			if penErr != nil {
				sm.handleRedisError(penErr)
			}
			penExists := penExistsVal > 0
			
			var neuronsUsed int64 = 0
			nVal, errVal := sm.rStore.client.Get(ctx, "neurons:"+acc.AccountID).Result()
			if errVal != nil && errVal != redis.Nil {
				sm.handleRedisError(errVal)
			}
			if errVal == nil {
				neuronsUsed, _ = strconv.ParseInt(nVal, 10, 64)
			}

			if !penExists && neuronsUsed < HandoffThreshold {
				if sm.rStore.active {
					errSet := sm.rStore.client.Set(ctx, "session:"+sessionID, acc.AccountID, 24*time.Hour).Err()
					if errSet != nil {
						sm.handleRedisError(errSet)
					}
				}
				return *acc, true
			}
		}
		
		// Fallback xuống RAM cục bộ nếu trong lúc chạy Redis bị mất kết nối và active chuyển sang false
		if !sm.rStore.active {
			log.Println("🔄 Tự động fallback xuống RAM trong cuộc gọi GetAccount hiện tại do lỗi Redis.")
		} else {
			return CFAccount{}, false
		}
	}

	// 2. Chế độ dự phòng: Lưu trữ trong RAM cục bộ (không có Redis hoặc khi Redis sập)
	for accID, penTime := range sm.penalized {
		if now.After(penTime) {
			delete(sm.penalized, accID)
			log.Printf("[Circuit Breaker] Hết hạn phạt, mở khóa tài khoản: %s", safeShortID(accID))
			for _, acc := range sm.pool {
				if acc.AccountID == accID {
					acc.IsActive = true
					atomic.StoreInt64(&acc.CurrentNeuronsUsed, 0)
					break
				}
			}
		}
	}

	if acc, exists := sm.sessionToAcc[sessionID]; exists {
		_, isPenalized := sm.penalized[acc.AccountID]
		used := atomic.LoadInt64(&acc.CurrentNeuronsUsed)
		if !isPenalized && acc.IsActive && used < HandoffThreshold {
			return *acc, true
		}
		delete(sm.sessionToAcc, sessionID)
	}

	total := len(sm.pool)
	if total == 0 {
		return CFAccount{}, false
	}

	for i := 0; i < total; i++ {
		idx := atomic.AddUint64(&sm.poolIndex, 1)
		acc := sm.pool[idx%uint64(total)]

		_, isPenalized := sm.penalized[acc.AccountID]
		used := atomic.LoadInt64(&acc.CurrentNeuronsUsed)
		if !isPenalized && acc.IsActive && used < HandoffThreshold {
			sm.sessionToAcc[sessionID] = acc
			return *acc, true
		}
	}

	return CFAccount{}, false
}

// TrackUsage cộng dồn lượng Neurons tiêu thụ sau mỗi request thành công và khóa bảo vệ nếu chạm ngưỡng.
func (sm *SessionManager) TrackUsage(accountID string, estimatedNeurons int64) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	// 1. Tích lũy neurons trong Redis nếu kích hoạt
	if sm.rStore != nil && sm.rStore.active {
		ctx := sm.rStore.ctx
		neuronsKey := "neurons:" + accountID
		newUsed, err := sm.rStore.client.IncrBy(ctx, neuronsKey, estimatedNeurons).Result()
		if err != nil {
			sm.handleRedisError(err)
		} else {
			if newUsed == estimatedNeurons {
				sm.rStore.client.Expire(ctx, neuronsKey, 24*time.Hour)
			}
			log.Printf("[Usage Tracker - Redis] Account %s tiêu thụ thêm ~%d Neurons (Tổng: %d/%d)", 
				safeShortID(accountID), estimatedNeurons, newUsed, MaxNeuronsPerAccount)
			
			if newUsed >= HandoffThreshold {
				errSet := sm.rStore.client.Set(ctx, "penalty:"+accountID, "1", 24*time.Hour).Err()
				if errSet != nil {
					sm.handleRedisError(errSet)
				}
				log.Printf("[ALERT - Redis] Account %s đã chạm ngưỡng chặn trên (%d). Khóa bảo vệ 24 giờ trong Redis!", safeShortID(accountID), HandoffThreshold)
			}
		}
	}

	// 2. Luôn đồng bộ dữ liệu cục bộ trong RAM để phục vụ ghi log/metrics
	for _, acc := range sm.pool {
		if acc.AccountID == accountID {
			newUsed := atomic.AddInt64(&acc.CurrentNeuronsUsed, estimatedNeurons)
			if sm.rStore == nil || !sm.rStore.active {
				log.Printf("[Usage Tracker - RAM] Account %s tiêu thụ thêm ~%d Neurons (Tổng: %d/%d)", 
					safeShortID(accountID), estimatedNeurons, newUsed, MaxNeuronsPerAccount)
				if newUsed >= HandoffThreshold {
					acc.IsActive = false
					sm.penalized[accountID] = time.Now().Add(24 * time.Hour)
					log.Printf("[ALERT - RAM] Account %s đã chạm ngưỡng chặn trên (%d). Khóa bảo vệ 24 giờ!", safeShortID(accountID), HandoffThreshold)
				}
			} else {
				// Đồng bộ số lượng để hiển thị metrics chính xác
				atomic.StoreInt64(&acc.CurrentNeuronsUsed, newUsed)
			}
			break
		}
	}
}

// Penalize đưa tài khoản vào danh sách đen (penalized) trong 24 giờ khi dính lỗi 429 hoặc 401.
func (sm *SessionManager) Penalize(accountID string, duration time.Duration) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	actualDuration := duration
	if duration >= 12*time.Hour {
		actualDuration = 24 * time.Hour
	}

	if sm.rStore != nil && sm.rStore.active {
		err := sm.rStore.client.Set(sm.rStore.ctx, "penalty:"+accountID, "1", actualDuration).Err()
		if err != nil {
			sm.handleRedisError(err)
		}
	}

	sm.penalized[accountID] = time.Now().Add(actualDuration)
	for _, acc := range sm.pool {
		if acc.AccountID == accountID {
			acc.IsActive = false
			break
		}
	}
	log.Printf("[Circuit Breaker] Tài khoản %s dính lỗi. Phạt tạm dừng %v (Redis/RAM)", safeShortID(accountID), actualDuration)
}

// BreakSession bẻ gãy liên kết bám dính của Session để buộc hệ thống cấp tài khoản mới ở request sau.
func (sm *SessionManager) BreakSession(sessionID string) {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	if sm.rStore != nil && sm.rStore.active {
		err := sm.rStore.client.Del(sm.rStore.ctx, "session:"+sessionID).Err()
		if err != nil {
			sm.handleRedisError(err)
		}
	}

	delete(sm.sessionToAcc, sessionID)
}

// LoadModelsFromCSV đọc và nạp danh sách ánh xạ model từ file CSV.
func (sm *SessionManager) LoadModelsFromCSV(filePath string) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()

	reader := csv.NewReader(file)
	reader.Comment = '#'
	records, err := reader.ReadAll()
	if err != nil {
		return err
	}

	sm.mu.Lock()
	defer sm.mu.Unlock()

	sm.modelMap = make(map[string]string)
	sm.modelsList = nil

	for i, record := range records {
		// Bỏ qua dòng tiêu đề nếu có
		if i == 0 && (strings.Contains(record[0], "alias") || strings.Contains(record[1], "target")) {
			continue
		}
		if len(record) < 2 {
			continue
		}

		alias := strings.TrimSpace(record[0])
		target := strings.TrimSpace(record[1])

		if alias != "" && target != "" {
			sm.modelMap[strings.ToLower(alias)] = target
			sm.modelsList = append(sm.modelsList, alias)
		}
	}
	log.Printf("[Model Config] Đã nạp thành công %d ánh xạ model từ file CSV.", len(sm.modelMap))
	return nil
}

// ResolveModel ánh xạ và chuẩn hóa tên mô hình đầu vào sang đúng ID mà Cloudflare Workers AI hỗ trợ.
func (sm *SessionManager) ResolveModel(model string) string {
	sm.mu.RLock()
	defer sm.mu.RUnlock()

	m := strings.ToLower(strings.TrimSpace(model))
	m = strings.TrimPrefix(m, "@")
	m = strings.TrimPrefix(m, "cf/")
	m = strings.TrimPrefix(m, "/cf/")

	// 1. Thử khớp trực tiếp alias
	if target, exists := sm.modelMap[m]; exists {
		return target
	}

	// 2. Thử tìm xem m có chứa key nào trong map không (khớp tương đối)
	for alias, target := range sm.modelMap {
		if strings.Contains(m, alias) {
			return target
		}
	}

	// 3. Dự phòng nếu không tìm thấy: luôn thêm @cf/
	if !strings.HasPrefix(m, "cf/") && !strings.HasPrefix(m, "@cf/") {
		return "@cf/" + m
	}
	return "@cf/" + strings.TrimPrefix(m, "cf/")
}

// handleRedisError phát hiện lỗi kết nối mạng của Redis và chuyển sang chế độ RAM tạm thời.
func (sm *SessionManager) handleRedisError(err error) {
	if err == nil || err == redis.Nil {
		return
	}

	errStr := err.Error()
	if strings.Contains(errStr, "connection refused") || 
	   strings.Contains(errStr, "timeout") || 
	   strings.Contains(errStr, "dial") || 
	   strings.Contains(errStr, "broken pipe") ||
	   strings.Contains(errStr, "EOF") {
		
		if sm.rStore.active {
			sm.rStore.active = false
			log.Printf("⚠️ Lỗi kết nối Redis phát hiện tại thời điểm chạy: %v. Tự động chuyển sang chế độ dự phòng RAM cục bộ!", err)
			
			// Khởi chạy tiến trình tự động kết nối lại ở chế độ nền
			go sm.autoReconnectRedis()
		}
	}
}

// autoReconnectRedis chạy ngầm thử kết nối lại Redis mỗi 10 giây.
func (sm *SessionManager) autoReconnectRedis() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for range ticker.C {
		// Nếu ứng dụng đã khôi phục kết nối thành công trước đó (hoặc trạng thái được sửa ngoài), dừng vòng lặp
		sm.mu.RLock()
		active := sm.rStore.active
		sm.mu.RUnlock()
		if active {
			return
		}

		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		err := sm.rStore.client.Ping(ctx).Err()
		cancel()

		if err == nil {
			sm.mu.Lock()
			sm.rStore.active = true
			log.Println("🔌 Khôi phục kết nối Redis thành công! Đã tự động chuyển lại sang chế độ lưu trữ phân tán.")
			
			// Đồng bộ trạng thái từ RAM lên Redis khi kết nối lại
			ctxSync := sm.rStore.ctx
			for _, acc := range sm.pool {
				if acc.CurrentNeuronsUsed > 0 {
					sm.rStore.client.Set(ctxSync, "neurons:"+acc.AccountID, acc.CurrentNeuronsUsed, 24*time.Hour)
				}
			}
			sm.mu.Unlock()
			return
		}
		log.Printf("🔄 Đang thử kết nối lại Redis ở chế độ nền (lỗi: %v)...", err)
	}
}

// SyncNeuronsFromCloudflare gọi Cloudflare GraphQL API để đồng bộ lượng Neurons thực tế đã dùng trong ngày.
func (sm *SessionManager) SyncNeuronsFromCloudflare() {
	sm.mu.Lock()
	defer sm.mu.Unlock()

	now := time.Now().UTC()
	// Mốc thời gian bắt đầu ngày hôm nay (00:00:00 UTC)
	todayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC).Format(time.RFC3339)

	client := &http.Client{Timeout: 6 * time.Second}

	for _, acc := range sm.pool {
		query := fmt.Sprintf(`{"query": "query { viewer { accounts(filter: {accountTag: \"%s\"}) { aiInferenceAdaptiveGroups(filter: {datetime_geq: \"%s\"} limit: 1) { sum { totalNeurons } } } } }"}`, acc.AccountID, todayStart)

		req, err := http.NewRequest("POST", "https://api.cloudflare.com/client/v4/graphql", strings.NewReader(query))
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+acc.APIToken)
		req.Header.Set("Content-Type", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[Sync] Không thể kết nối Cloudflare GraphQL API cho tài khoản %s: %v", safeShortID(acc.AccountID), err)
			continue
		}
		
		body, errRead := io.ReadAll(resp.Body)
		resp.Body.Close()
		if errRead != nil || resp.StatusCode != http.StatusOK {
			log.Printf("[Sync] Tài khoản %s phản hồi lỗi HTTP %d hoặc lỗi đọc body", safeShortID(acc.AccountID), resp.StatusCode)
			continue
		}

		var gqlResp struct {
			Data struct {
				Viewer struct {
					Accounts []struct {
						AiInferenceAdaptiveGroups []struct {
							Sum struct {
								TotalNeurons float64 `json:"totalNeurons"`
							} `json:"sum"`
						} `json:"aiInferenceAdaptiveGroups"`
					} `json:"accounts"`
				} `json:"viewer"`
			} `json:"data"`
			Errors []struct {
				Message string `json:"message"`
			} `json:"errors"`
		}

		if err := json.Unmarshal(body, &gqlResp); err == nil {
			if len(gqlResp.Errors) > 0 {
				log.Printf("[Sync] Cloudflare trả về lỗi cho tài khoản %s: %s (Có thể do chưa phân quyền Account Analytics: Read)", safeShortID(acc.AccountID), gqlResp.Errors[0].Message)
				continue
			}
			
			if len(gqlResp.Data.Viewer.Accounts) > 0 {
				accData := gqlResp.Data.Viewer.Accounts[0]
				var neuronsUsed int64 = 0
				
				if len(accData.AiInferenceAdaptiveGroups) > 0 {
					neuronsFloat := accData.AiInferenceAdaptiveGroups[0].Sum.TotalNeurons
					neuronsUsed = int64(neuronsFloat)
				}
				
				// Đồng bộ vào RAM cục bộ
				atomic.StoreInt64(&acc.CurrentNeuronsUsed, neuronsUsed)
				
				// Đồng bộ vào Redis nếu có
				if sm.rStore != nil && sm.rStore.active {
					ctx := sm.rStore.ctx
					sm.rStore.client.Set(ctx, "neurons:"+acc.AccountID, neuronsUsed, 24*time.Hour)
				}
				
				log.Printf("[Sync - Cloudflare] Đồng bộ thành công %s: %d Neurons đã dùng hôm nay", safeShortID(acc.AccountID), neuronsUsed)

				// Khóa/Phạt nếu chạm ngưỡng
				if neuronsUsed >= HandoffThreshold {
					acc.IsActive = false
					if sm.rStore != nil && sm.rStore.active {
						sm.rStore.client.Set(sm.rStore.ctx, "penalty:"+acc.AccountID, "1", 24*time.Hour)
					} else {
						sm.penalized[acc.AccountID] = time.Now().Add(24 * time.Hour)
					}
					log.Printf("[ALERT] Tài khoản %s đã dùng quá ngưỡng (%d/%d). Bị khóa!", safeShortID(acc.AccountID), neuronsUsed, HandoffThreshold)
				} else {
					// Mở khóa nếu tài khoản còn hạn mức
					acc.IsActive = true
					if sm.rStore != nil && sm.rStore.active {
						sm.rStore.client.Del(sm.rStore.ctx, "penalty:"+acc.AccountID)
					} else {
						delete(sm.penalized, acc.AccountID)
					}
				}
			}
		} else {
			log.Printf("[Sync] Lỗi decode phản hồi GraphQL của account %s: %v", safeShortID(acc.AccountID), err)
		}
	}
}

