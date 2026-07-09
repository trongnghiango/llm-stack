package main

import (
	"log"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
)

// safeShortID rút trích ngắn ID tài khoản một cách an toàn để in log, tránh lỗi index out of bounds.
func safeShortID(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

// ============================================================================
// ĐIỂM KHỞI CHẠY HỆ THỐNG (APPLICATION INITIALIZATION)
// ============================================================================

func main() {
	// Khởi tạo RedisStore tùy chọn
	rStore := NewRedisStore()

	// Khởi tạo bộ quản lý session kèm RedisStore
	sm := NewSessionManager(rStore)
	
	// Nạp danh sách Account ID và API Token của Cloudflare từ file CSV
	if err := sm.LoadAccountsFromCSV("accounts.csv"); err != nil {
		log.Fatalf("❌ Lỗi nghiêm trọng không thể đọc file accounts.csv: %v", err)
	}

	// Nạp danh sách ánh xạ model từ file CSV
	if err := sm.LoadModelsFromCSV("models.csv"); err != nil {
		log.Printf("⚠️ Cảnh báo: Không thể đọc file models.csv (%v). Sử dụng cấu hình mặc định.", err)
	}

	// Khởi chạy File Watcher (Hot Reload) cho accounts.csv và models.csv
	go func() {
		getModTime := func(filepath string) time.Time {
			info, err := os.Stat(filepath)
			if err != nil {
				return time.Time{}
			}
			return info.ModTime()
		}

		lastAccountsTime := getModTime("accounts.csv")
		lastModelsTime := getModTime("models.csv")

		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()

		for range ticker.C {
			// Check accounts.csv
			accTime := getModTime("accounts.csv")
			if !accTime.IsZero() && !accTime.Equal(lastAccountsTime) {
				log.Println("🔥 [Hot Reload] Phát hiện thay đổi trong accounts.csv! Đang nạp lại tài khoản...")
				if err := sm.LoadAccountsFromCSV("accounts.csv"); err != nil {
					log.Printf("❌ [Hot Reload] Lỗi nạp lại accounts.csv: %v", err)
				} else {
					log.Println("✅ [Hot Reload] Nạp lại accounts.csv thành công!")
					// Đồng bộ lại Neurons của các tài khoản mới ngay lập tức
					go sm.SyncNeuronsFromCloudflare()
				}
				lastAccountsTime = accTime
			}

			// Check models.csv
			modTime := getModTime("models.csv")
			if !modTime.IsZero() && !modTime.Equal(lastModelsTime) {
				log.Println("🔥 [Hot Reload] Phát hiện thay đổi trong models.csv! Đang nạp lại models mapping...")
				if err := sm.LoadModelsFromCSV("models.csv"); err != nil {
					log.Printf("❌ [Hot Reload] Lỗi nạp lại models.csv: %v", err)
				} else {
					log.Println("✅ [Hot Reload] Nạp lại models.csv thành công!")
				}
				lastModelsTime = modTime
			}
		}
	}()

	// Khởi chạy đồng bộ Neurons từ Cloudflare GraphQL API
	go func() {
		// Chờ 1 giây để server khởi động hoàn tất trước khi đồng bộ lần đầu
		time.Sleep(1 * time.Second)
		log.Println("🔄 Đang đồng bộ Neurons ban đầu từ Cloudflare GraphQL API...")
		sm.SyncNeuronsFromCloudflare()

		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for range ticker.C {
			log.Println("🔄 Đang đồng bộ định kỳ Neurons từ Cloudflare GraphQL API...")
			sm.SyncNeuronsFromCloudflare()
		}
	}()

	handler := NewProxyHandler(sm)

	gin.SetMode(gin.ReleaseMode)
	r := gin.Default()

	// OpenAI Endpoints
	r.POST("/v1/chat/completions", handler.HandleChatCompletion)
	r.POST("/v1/v1/chat/completions", handler.HandleChatCompletion)
	r.GET("/v1/models", handler.HandleListModels)
	r.GET("/v1/v1/models", handler.HandleListModels)

	// ANTHROPIC ENDPOINT: Tiếp nhận yêu cầu trực tiếp từ Claude Code (Anthropic Compatible)
	r.POST("/v1/messages", handler.HandleAnthropicCompletion)
	r.POST("/v1/v1/messages", handler.HandleAnthropicCompletion)

	// API Quản trị để giám sát trạng thái và số lượng Neurons tiêu thụ của pool tài khoản
	r.GET("/admin/metrics", func(c *gin.Context) {
		sm.mu.RLock()
		defer sm.mu.RUnlock()

		now := time.Now()
		var accounts []map[string]interface{}
		var totalNeurons int64 = 0
		var activeCount = 0

		for _, acc := range sm.pool {
			isPenalized := false
			secondsRemaining := 0.0
			neuronsUsed := acc.CurrentNeuronsUsed

			if sm.rStore != nil && sm.rStore.active {
				// Đọc neurons từ Redis
				nVal, errVal := sm.rStore.client.Get(sm.rStore.ctx, "neurons:"+acc.AccountID).Result()
				if errVal == nil {
					neuronsUsed, _ = strconv.ParseInt(nVal, 10, 64)
				}
				// Đọc thời gian phạt còn lại từ Redis
				ttlVal, errTtl := sm.rStore.client.TTL(sm.rStore.ctx, "penalty:"+acc.AccountID).Result()
				if errTtl == nil && ttlVal > 0 {
					isPenalized = true
					secondsRemaining = ttlVal.Seconds()
				}
			} else {
				if penTime, ok := sm.penalized[acc.AccountID]; ok {
					if penTime.After(now) {
						isPenalized = true
						secondsRemaining = penTime.Sub(now).Seconds()
					}
				}
			}

			if acc.IsActive && !isPenalized {
				activeCount++
			}
			totalNeurons += neuronsUsed

			accounts = append(accounts, map[string]interface{}{
				"account_id":           acc.AccountID,
				"is_active":            acc.IsActive,
				"current_neurons_used": neuronsUsed,
				"is_penalized":         isPenalized,
				"seconds_remaining":    int(secondsRemaining),
			})
		}

		c.JSON(200, gin.H{
			"total_accounts":     len(sm.pool),
			"active_accounts":    activeCount,
			"total_neurons_used": totalNeurons,
			"accounts":           accounts,
		})
	})

	// Bảng điều khiển Web UI trực quan
	r.GET("/admin/dashboard", func(c *gin.Context) {
		c.Data(200, "text/html; charset=utf-8", []byte(dashboardHTML))
	})

	log.Println("⚡ [Cloudflare Pool Proxy Engine] Running cleanly on 0.0.0.0:20127...")
	r.Run("0.0.0.0:20127")
}