package main

import (
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
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"claude-proxy/internal/config"
	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"
	"claude-proxy/internal/router"
	"claude-proxy/internal/utils"
)

// ─────────────────────────────────────────────
// Constants
// ─────────────────────────────────────────────

const (
	anthropicVersion  = "2023-06-01"
	contentTypeJSON   = "application/json"
	contentTypeSSE    = "text/event-stream"
	defaultMaxPayload = int64(5 << 20) // 5 MiB
	sseChunkRunes     = 40
)

// systemReminderRegex matches <system-reminder>…</system-reminder> blocks that
// Claude Code injects and that should not influence routing decisions.
var systemReminderRegex = regexp.MustCompile(`(?s)<system-reminder>.*?</system-reminder>`)

func cleanPromptForRouting(s string) string {
	return systemReminderRegex.ReplaceAllString(s, "")
}

// ─────────────────────────────────────────────
// HTTP proxy handler
// ─────────────────────────────────────────────

func handleProxy(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if rec := recover(); rec != nil {
			logger.Errorf("[Proxy] Panic recovered: %v", rec)
			http.Error(w, "Internal Proxy Server Recovery Error", http.StatusInternalServerError)
		}
	}()



	// ── 1. Read and size-limit the request body ──────────────────────────────
	maxBytes := defaultMaxPayload
	if v := os.Getenv("MAX_PAYLOAD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			metrics.PayloadTooLargeTotal.Inc()
			logger.Errorf("[Security] Payload too large (limit %d bytes)", maxBytes)
			w.Header().Set("Content-Type", contentTypeJSON)
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			w.Write([]byte(`{"error":"request too large"}`))
			return
		}
		http.Error(w, "Failed to read request body", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(bodyBytes) == 0 {
		forwardRequest(w, r, bodyBytes, false, false)
		return
	}

	// ── 2. Parse raw JSON so we can rewrite fields without losing unknowns ───
	var rawData map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &rawData); err != nil {
		if r.URL.Path == "/v1/messages" {
			logger.Errorf("[Proxy] Malformed JSON on /v1/messages: %v", err)
		}
		forwardRequest(w, r, bodyBytes, false, false)
		return
	}

	// ── 3. Resolve original model ────────────────────────────────────────────
	var originalModel string
	if modelBytes, ok := rawData["model"]; ok {
		if err := json.Unmarshal(modelBytes, &originalModel); err != nil {
			logger.Errorf("[Router] Cannot parse model field: %v", err)
			forwardRequest(w, r, bodyBytes, false, false)
			return
		}
	} else {
		forwardRequest(w, r, bodyBytes, false, false)
		return
	}

	// Warn on unrecognised proxy-configured models.
	if strings.HasPrefix(originalModel, "claude-") || strings.HasPrefix(originalModel, "swe.") {
		st := router.GetState()
		if _, known := st.SemanticRuleMap[originalModel]; !known {
			logger.Errorf("[Security] Unrecognised routed model: %s", originalModel)
			metrics.InvalidModelTotal.Inc()
		}
	}

	// ── 4. Build prompt snippet for routing ──────────────────────────────────
	var textReq AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &textReq); err != nil {
		logger.Debugf("[Router] Cannot parse request for routing: %v", err)
	}
	promptText := extractPromptText(textReq.Messages)

	// ── 5. Route to target model ─────────────────────────────────────────────
	st := router.GetState()
	targetModel := st.ModelRouter.Resolve(originalModel, promptText)
	modelSettings, hasSettings := st.Config.ModelSettings[targetModel]

	// ── 6. Tool spoofing (XML injection for OSS models) ──────────────────────
	isSpoofingTools := hasSettings && modelSettings.SpoofToolsXML && len(textReq.Tools) > 0
	if isSpoofingTools {
		convertedMsgs := convertMessagesForSpoofing(textReq.Messages)
		msgsBytes, _ := json.Marshal(convertedMsgs)
		rawData["messages"] = msgsBytes

		logger.Infof("[Router] Spoofing Tools -> XML for model: %s", targetModel)
		systemStr := buildSpoofedSystemPrompt(textReq.System, textReq.Tools)
		sysBytes, _ := json.Marshal(systemStr)
		rawData["system"] = sysBytes

		delete(rawData, "tools")
		delete(rawData, "tool_choice")
	}

	// ── 7. Rewrite model field and forward ───────────────────────────────────
	rawData["model"] = json.RawMessage(fmt.Sprintf("%q", targetModel))
	modifiedBody, err := json.Marshal(rawData)
	if err != nil {
		http.Error(w, "Failed to rebuild JSON payload", http.StatusInternalServerError)
		return
	}

	reqID := r.Header.Get("X-Request-ID")
	logger.LogPayload(reqID, modifiedBody)

	isFakeStream := hasSettings && modelSettings.FakeStream && textReq.Stream != nil && *textReq.Stream
	forwardRequest(w, r, modifiedBody, isSpoofingTools, isFakeStream)
}

// extractPromptText concatenates text from all user-role messages for routing.
func extractPromptText(messages []Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		if msg.Role != "user" {
			continue
		}
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil {
			sb.WriteString(" " + cleanPromptForRouting(s))
			continue
		}
		var blocks []MessageBlock
		if err := json.Unmarshal(msg.Content, &blocks); err == nil {
			for _, b := range blocks {
				if b.Type == "text" {
				sb.WriteString(" " + cleanPromptForRouting(b.Text))
				}
			}
		}
	}
	return strings.TrimSpace(sb.String())
}

// ─────────────────────────────────────────────
// Upstream forwarding
// ─────────────────────────────────────────────

func forwardRequest(w http.ResponseWriter, r *http.Request, payload []byte, isSpoofingTools, isFakeStream bool) {
	s := router.GetState()
	upstreamURL, err := url.Parse(s.Config.UpstreamURL)
	if err != nil {
		logger.Errorf("[Upstream] Invalid upstream URL: %v", err)
		http.Error(w, "Invalid Upstream Endpoint Configuration", http.StatusBadGateway)
		return
	}
	upstreamURL.Path = r.URL.Path

	upstreamReq, err := buildUpstreamRequest(r, upstreamURL.String(), payload)
	if err != nil {
		http.Error(w, "Failed to initialise upstream request", http.StatusInternalServerError)
		return
	}

	// Inject API key if configured.
	if s.Config.UpstreamAPIKey != "" {
		for _, h := range []string{"Authorization", "X-API-Key", "X-Api-Key", "x-api-key"} {
			upstreamReq.Header.Del(h)
		}
		upstreamReq.Header.Set("x-api-key", s.Config.UpstreamAPIKey)
		upstreamReq.Header.Set("Authorization", "Bearer "+s.Config.UpstreamAPIKey)
	}

	resp, err := utils.HTTPClient.Do(upstreamReq)
	if err != nil {
		logger.Errorf("[Upstream] Connection failed (%s): %v", s.Config.UpstreamURL, err)
		http.Error(w, "Unable to establish connection to upstream", http.StatusBadGateway)
		return
	}
	// Bắt buộc luôn xả và đóng stream
	defer resp.Body.Close()

	isStream := writeUpstreamResponseHeaders(w, resp)
	logger.Infof("[Response] isFakeStream=%v isStream=%v isSpoofingTools=%v status=%d",
		isFakeStream, isStream, isSpoofingTools, resp.StatusCode)

	// Pass upstream error responses directly.
	if resp.StatusCode >= 400 {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	// Case 1 — Tool spoofing: buffer full body, extract text, emit spoofed SSE.
	if isSpoofingTools {
		bodyBytes, ok := readBodyOrError(w, resp, "Spoof-Stream")
		if !ok {
			return
		}
		var fullText string
		if isStream {
			fullText = extractTextFromSSE(string(bodyBytes))
		} else {
			fullText = extractTextFromJSON(bodyBytes)
		}
		logger.Infof("[Spoof-Stream] Extracted %d chars: %.200s", len(fullText), fullText)
		writeSseBody(w, fullText)
		return
	}

	// Case 2 — Fake stream: upstream returned JSON but client wants SSE.
	if isFakeStream && !isStream {
		bodyBytes, ok := readBodyOrError(w, resp, "FakeStream")
		if !ok {
			return
		}
		writeSseBody(w, extractTextFromJSON(bodyBytes))
		return
	}

	// Case 3 — Transparent SSE forwarding.
	if isStream {
		setSseHeaders(w)
		w.WriteHeader(resp.StatusCode)
		// UPDATED: Now passes `r` to enable context listening
		forwardStreamWithValidation(w, r, resp)
		return
	}

	// Case 4 — Transparent non-streaming response.
	logger.Infof("[Response] -> raw copy path")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// ─────────────────────────────────────────────
// Server bootstrap
// ─────────────────────────────────────────────

func main() {
	defer logger.CloseLogger()

	cfgPath := parseFlags()
	config.ConfigPath = cfgPath

	cp := &config.JSONConfigLoader{Path: cfgPath}
	cfg, err := cp.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Config load error: %v\n", err)
		log.Fatalf("[Init] Config load error: %v", err)
	}
	router.SetState(cfg)

	stopCleanup := logger.StartPayloadLogCleanupWorker(1 * time.Hour)
	defer close(stopCleanup)

	applyFlagOverrides()

	mux := http.NewServeMux()
	metrics.ExposeMetrics(mux)
	mux.HandleFunc("/health", HealthHandler)
	mux.HandleFunc("/readyz", ReadyzHandler)
	mux.HandleFunc("/debug/health", DebugHealthHandler)
	mux.HandleFunc("/", handleProxy)

	var handler http.Handler = mux
	handler = utils.NewLoggingMiddleware(handler)

	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", bindAddr, router.GetState().Config.Port)
	logger.Infof("[Server] Listening on %s", addr)

	server := &http.Server{Addr: addr, Handler: handler}
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "FATAL: ListenAndServe: %v\n", err)
			log.Fatalf("[Server] ListenAndServe failed: %v", err)
		}
	}()

	quit := make(chan os.Signal, 1)
	reload := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(reload, syscall.SIGHUP)

	var isShuttingDown atomic.Bool
	go func() {
		for range reload {
			if isShuttingDown.Load() {
				logger.Infof("[Reload] Skipped — server is shutting down")
				continue
			}
			router.ReloadConfig(config.ConfigPath)
		}
	}()

	sig := <-quit
	isShuttingDown.Store(true)
	logger.Infof("[Shutdown] Signal %v received, shutting down…", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		logger.Errorf("[Shutdown] Error: %v", err)
	}
	logger.Infof("[Shutdown] Done.")
}
