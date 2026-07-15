package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"claude-proxy/internal/config"
	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"
	"claude-proxy/internal/router"
	"claude-proxy/internal/utils"
)

// Message structures
type MessageBlock struct {
	Type string `json:"type"`
	Text string `json:"text"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicRequest struct {
	Model    string          `json:"model"`
	System   json.RawMessage `json:"system,omitempty"`
	Messages []Message       `json:"messages,omitempty"`
}

func handleProxy(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recoveryErr := recover(); recoveryErr != nil {
			logger.Errorf("[LỖI HỆ THỐNG CRASH] Phát hiện sự cố nghiêm trọng được khôi phục: %v", recoveryErr)
			http.Error(w, "Internal Proxy Server Recovery Error", http.StatusInternalServerError)
		}
	}()

	// Serve metrics endpoint directly (bypasses mux for direct handler testing).
	if r.URL.Path == "/debug/metrics" {
		promhttp.Handler().ServeHTTP(w, r)
		return
	}

	maxBytes := int64(5 << 20) // 5 MiB default
	if v := os.Getenv("MAX_PAYLOAD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			metrics.PayloadTooLargeTotal.Inc()
			logger.Errorf("[Security] Request payload too large (limit %d bytes)", maxBytes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			w.Write([]byte(`{"error":"request too large"}`))
			return
		}
		http.Error(w, "Lỗi đọc luồng dữ liệu yêu cầu", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(bodyBytes) == 0 {
		forwardRequest(w, r, bodyBytes)
		return
	}

	var rawData map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &rawData); err != nil {
		if r.URL.Path == "/v1/messages" {
			logger.Errorf("[Chẩn đoán] Gói tin gửi tới /v1/messages sai cấu trúc JSON: %v\n", err)
		}
		forwardRequest(w, r, bodyBytes)
		return
	}

	var originalModel string
	if modelBytes, exists := rawData["model"]; exists {
		if err := json.Unmarshal(modelBytes, &originalModel); err != nil {
			logger.Errorf("[Router] Failed to parse model field: %v", err)
			forwardRequest(w, r, bodyBytes)
			return
		}
	} else {
		forwardRequest(w, r, bodyBytes)
		return
	}

	if strings.HasPrefix(originalModel, "swe.") {
		st := router.GetState()
		if _, hasRule := st.SemanticRuleMap[originalModel]; !hasRule {
			logger.Errorf("[Security] Unrecognized swe model: %s", originalModel)
			metrics.InvalidModelTotal.Inc()
		}
	}

	// Extract prompt text for routing decisions
	var textReq AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &textReq); err != nil {
		logger.Debugf("[Router] Unable to parse request for prompt text: %v", err)
	}
	var textBuilder strings.Builder
	if len(textReq.System) > 0 {
		var systemStr string
		if err := json.Unmarshal(textReq.System, &systemStr); err == nil {
			textBuilder.WriteString(systemStr)
		} else {
			var systemBlocks []MessageBlock
			if err := json.Unmarshal(textReq.System, &systemBlocks); err == nil {
				for _, block := range systemBlocks {
					if block.Type == "text" {
						textBuilder.WriteString(" " + block.Text)
					}
				}
			}
		}
	}
	for _, msg := range textReq.Messages {
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil {
			textBuilder.WriteString(" " + s)
			continue
		}
		var blocks []MessageBlock
		if err := json.Unmarshal(msg.Content, &blocks); err == nil {
			for _, b := range blocks {
				if b.Type == "text" {
					textBuilder.WriteString(" " + b.Text)
				}
			}
		}
	}
	promptText := textBuilder.String()

	// Resolve target model via router implementation (atomic state snapshot)
	targetModel := router.GetState().ModelRouter.Resolve(originalModel, promptText)

	// Overwrite model field preserving other payload data
	rawData["model"] = json.RawMessage(fmt.Sprintf("%q", targetModel))
	modifiedBody, err := json.Marshal(rawData)
	if err != nil {
		http.Error(w, "Lỗi tái tạo JSON payload", http.StatusInternalServerError)
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	logger.LogPayload(reqID, modifiedBody)
	forwardRequest(w, r, modifiedBody)
}

func forwardRequest(w http.ResponseWriter, r *http.Request, payload []byte) {
	s := router.GetState()
	upstreamURL, err := url.Parse(s.Config.UpstreamURL)
	if err != nil {
		logger.Errorf("[Lỗi Upstream] Đường dẫn upstream không hợp lệ: %v\n", err)
		http.Error(w, "Invalid Upstream Endpoint Configuration", http.StatusBadGateway)
		return
	}
	upstreamURL.Path = r.URL.Path

	upstreamReq, err := http.NewRequestWithContext(r.Context(), "POST", upstreamURL.String(), bytes.NewBuffer(payload))
	if err != nil {
		http.Error(w, "Fail to initialize upstream tunnel", http.StatusInternalServerError)
		return
	}

	// Copy original headers, drop length/encoding ones
	for name, values := range r.Header {
		low := strings.ToLower(name)
		if low == "content-length" || low == "content-encoding" || low == "transfer-encoding" {
			continue
		}
		for _, v := range values {
			upstreamReq.Header.Add(name, v)
		}
	}

	// Set correct Content-Length for modified payload
	upstreamReq.ContentLength = int64(len(payload))
	upstreamReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(payload)))

	// Inject API key headers if configured
	if s.Config.UpstreamAPIKey != "" {
		upstreamReq.Header.Del("Authorization")
		upstreamReq.Header.Del("X-API-Key")
		upstreamReq.Header.Del("X-Api-Key")
		upstreamReq.Header.Del("x-api-key")
		upstreamReq.Header.Set("x-api-key", s.Config.UpstreamAPIKey)
		upstreamReq.Header.Set("Authorization", "Bearer "+s.Config.UpstreamAPIKey)
	}

	upstreamReq.Header.Set("Anthropic-Version", "2023-06-01")
	upstreamReq.Header.Set("Content-Type", "application/json")
	// Forward trace ID to upstream for end-to-end correlation.
	if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
		upstreamReq.Header.Set("X-Request-ID", reqID)
	}

	resp, err := utils.HTTPClient.Do(upstreamReq)
	if err != nil {
		logger.Errorf("[Lỗi kết nối] Không thể kết nối tới Local OmniRoute (%s): %v\n", s.Config.UpstreamURL, err)
		http.Error(w, "Unable to establish connection to Local OmniRoute", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward response headers, omit streaming‑specific ones
	isStream := false
	for key, values := range resp.Header {
		low := strings.ToLower(key)
		if low == "content-length" || low == "content-encoding" || low == "transfer-encoding" || low == "connection" || low == "keep-alive" {
			continue
		}
		if low == "content-type" {
			for _, v := range values {
				if strings.HasPrefix(v, "text/event-stream") {
					isStream = true
				}
			}
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
	}

	w.WriteHeader(resp.StatusCode)
	flusher, ok := w.(http.Flusher)
	if !ok {
		_, _ = io.Copy(w, resp.Body)
		return
	}
	buf := make([]byte, 4096)
	for {
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, writeErr := w.Write(buf[:n]); writeErr != nil {
				return
			}
			flusher.Flush()
		}
		if readErr != nil {
			if readErr != io.EOF {
				logger.Errorf("[Lỗi đọc luồng] Kết thúc luồng đọc bất thường từ Upstream: %v\n", readErr)
			}
			break
		}
	}
}

func main() {
	// Initialise logger – will be closed on exit.
	defer logger.CloseLogger()

	// Parse CLI flags (before config load so --config can override path).
	cfgPath := parseFlags()
	config.ConfigPath = cfgPath

	// Load configuration via JSONConfigLoader
	cp := &config.JSONConfigLoader{Path: cfgPath}
	cfg, err := cp.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Config load error: %v\n", err)
		log.Fatalf("[Init] Config load error: %v", err)
	}
	router.SetState(cfg)

	// Start payload log cleanup worker running every hour
	stopCleanup := logger.StartPayloadLogCleanupWorker(1 * time.Hour)
	defer close(stopCleanup)

	// Apply CLI flag overrides on top of loaded config.
	applyFlagOverrides()
	// Register handlers on dedicated mux.
	mux := http.NewServeMux()
	metrics.ExposeMetrics(mux)
	mux.HandleFunc("/health", HealthHandler)
	mux.HandleFunc("/readyz", ReadyzHandler)
	mux.HandleFunc("/debug/health", DebugHealthHandler)
	mux.HandleFunc("/", handleProxy)
	// Wrap mux with logging middleware.
	var handler http.Handler = mux
	handler = utils.NewLoggingMiddleware(handler)
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", bindAddr, router.GetState().Config.Port)
	logger.Infof("[Sẵn sàng] Khởi chạy Local Proxy thành công tại http://%s", addr)
	server := &http.Server{Addr: addr, Handler: handler}

	// Start server in background goroutine to allow main thread to block on shutdown
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "FATAL: ListenAndServe error: %v\n", err)
			log.Fatalf("[Thất bại] Không thể khởi chạy cổng %d: %v", router.GetState().Config.Port, err)
		}
	}()

	// Signal handling: shutdown (SIGINT/SIGTERM) and config reload (SIGHUP).
	quit := make(chan os.Signal, 1)
	reload := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(reload, syscall.SIGHUP)

	var isShuttingDown atomic.Bool

	// SIGHUP reload goroutine
	go func() {
		for range reload {
			if isShuttingDown.Load() {
				logger.Infof("[Reload] Bỏ qua reload cấu hình vì server đang tắt")
				continue
			}
			router.ReloadConfig(config.ConfigPath)
		}
	}()

	// Block main goroutine waiting for shutdown signal
	sig := <-quit
	isShuttingDown.Store(true)
	logger.Infof("[Tắt máy] Nhận tín hiệu %v, đang tắt dần...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Errorf("[Tắt máy] Lỗi khi tắt server: %v", err)
	}
	logger.Infof("[Tắt máy] Shutdown hoàn tất.")
}
