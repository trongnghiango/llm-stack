package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"claude-proxy/internal/config"
	"claude-proxy/internal/logger"
	"claude-proxy/internal/router"
)

// testStateUpdater mutates global state atomically inside test functions.
type testStateUpdater func(mutate func(*router.RouterState))

// setupProxyTest initializes global state for proxy handler tests.
func setupProxyTest(t *testing.T) testStateUpdater {
	t.Helper()

	t.Setenv("LOG_INFO", "0")
	t.Setenv("LOG_DEBUG", "0")

	// Create temporary logger to swallow log output.
	logger.SetupTestLogger(t)

	// Reset global state.
	router.ClearCache()
	cfg := config.Config{
		UpstreamURL:    "http://placeholder",
		Port:           0,
		UseLLMRouter:   false,
		UpstreamAPIKey: "",
	}
	r := router.NewModelRouter(cfg)
	router.SetRouterState(&router.RouterState{
		Config:           cfg,
		SemanticRuleMap:  make(map[string][]config.SemanticRule),
		DecisionModelMap: make(map[string]string),
		ModelSettingsMap: make(map[string]config.ModelSetting),
		ModelRouter:      r,
	})

	return func(mutate func(*router.RouterState)) {
		s := router.GetState()
		mutate(s)
		router.SetRouterState(s)
	}
}

func TestProxyMetricsEndpoint(t *testing.T) {
	setupProxyTest(t)

	req := httptest.NewRequest("GET", "/debug/metrics", nil)
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(body, []byte("router_cache_hits_total")) {
		t.Fatalf("metrics response missing prometheus output: %q", body[:100])
	}
}

func TestProxyEmptyBodyForwards(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"upstream":"ok"}`)
	}))
	defer upstream.Close()

	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
	})

	req := httptest.NewRequest("POST", "/v1/messages", nil)
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if !bytes.Contains(body, []byte(`"upstream":"ok"`)) {
		t.Fatalf("unexpected response: %s", body)
	}
}

func TestProxyInvalidJSONForwards(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"forwarded":true}`)
	}))
	defer upstream.Close()

	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
	})

	bodyReader := bytes.NewReader([]byte(`not json at all`))
	req := httptest.NewRequest("POST", "/v1/messages", bodyReader)
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if !bytes.Contains(body, []byte(`"forwarded":true`)) {
		t.Fatalf("unexpected response: %s", body)
	}
}

func TestProxyNoModelFieldForwards(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"forwarded":true}`)
	}))
	defer upstream.Close()

	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
	})

	payload, _ := json.Marshal(map[string]string{"not_model": "value"})
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if !bytes.Contains(body, []byte(`"forwarded":true`)) {
		t.Fatalf("unexpected response: %s", body)
	}
}

func TestProxyModelRewrite(t *testing.T) {
	var capturedModel string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		var parsed struct {
			Model string `json:"model"`
		}
		json.Unmarshal(body, &parsed)
		capturedModel = parsed.Model

		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{"done":true}`)
	}))
	defer upstream.Close()

	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
		s.SemanticRuleMap["swe.architect"] = []config.SemanticRule{
			{TriggerModel: "swe.architect", TargetModel: "nvidia/openai/gpt-oss-120b"},
		}
		s.ModelRouter = router.NewModelRouter(s.Config)
	})

	payload, _ := json.Marshal(map[string]string{
		"model": "swe.architect",
	})
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}
	if capturedModel != "nvidia/openai/gpt-oss-120b" {
		t.Fatalf("expected model rewrite to %q, got %q", "nvidia/openai/gpt-oss-120b", capturedModel)
	}
}

func TestProxyUpstreamUnreachable(t *testing.T) {
	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = "http://127.0.0.1:1"
	})

	payload, _ := json.Marshal(map[string]string{"model": "swe.engineer"})
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d: %s", resp.StatusCode, string(body))
	}
}

func TestProxyStreamingResponse(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher, ok := w.(http.Flusher)
		if !ok {
			t.Fatal("test server does not support flushing")
		}
		fmt.Fprint(w, "data: {\"type\":\"ping\"}\n\n")
		flusher.Flush()
		fmt.Fprint(w, "data: {\"type\":\"done\"}\n\n")
		flusher.Flush()
	}))
	defer upstream.Close()

	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
	})

	payload, _ := json.Marshal(map[string]string{"model": "swe.engineer"})
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !strings.Contains(string(body), "data: {\"type\":\"ping\"}") {
		t.Fatalf("expected SSE ping, got: %s", body)
	}
	if !strings.Contains(string(body), "data: {\"type\":\"done\"}") {
		t.Fatalf("expected SSE done, got: %s", body)
	}
	if resp.Header.Get("Cache-Control") != "no-cache" {
		t.Fatalf("expected Cache-Control: no-cache for SSE, got: %s", resp.Header.Get("Cache-Control"))
	}
}

func TestProxyPanicRecovery(t *testing.T) {
	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = "http://127.0.0.1:1"
		s.ModelRouter = &panicRouter{}
	})

	payload, _ := json.Marshal(map[string]string{"model": "panic"})
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("expected 500 after panic, got %d: %s", resp.StatusCode, string(body))
	}
}

// panicRouter implements ModelRouter by panicking on every call.
type panicRouter struct{}

func (r *panicRouter) Resolve(_, _ string) string {
	panic("test panic")
}

func TestProxyAPIKeyInjection(t *testing.T) {
	var capturedAuth, capturedXAPIKey string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedAuth = r.Header.Get("Authorization")
		capturedXAPIKey = r.Header.Get("X-API-Key")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
		s.Config.UpstreamAPIKey = "sk-test-key-123"
	})

	payload, _ := json.Marshal(map[string]string{"model": "swe.engineer"})
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if capturedAuth != "Bearer sk-test-key-123" {
		t.Fatalf("expected Authorization Bearer, got: %s", capturedAuth)
	}
	if capturedXAPIKey != "sk-test-key-123" {
		t.Fatalf("expected X-API-Key, got: %s", capturedXAPIKey)
	}
}

func TestProxyHeaderForwarding(t *testing.T) {
	var capturedXRequestID string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		capturedXRequestID = r.Header.Get("X-Request-ID")
		w.Header().Set("X-Upstream-ID", "upstream-456")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, `{}`)
	}))
	defer upstream.Close()

	upd := setupProxyTest(t)
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
		s.Config.UpstreamAPIKey = ""
	})

	payload, _ := json.Marshal(map[string]string{"model": "swe.engineer"})
	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	req.Header.Set("X-Request-ID", "req-789")
	w := httptest.NewRecorder()
	handleProxy(w, req)

	resp := w.Result()
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if capturedXRequestID != "req-789" {
		t.Fatalf("expected X-Request-ID header forwarded as req-789, got: %s", capturedXRequestID)
	}
	if resp.Header.Get("X-Upstream-ID") != "upstream-456" {
		t.Fatalf("expected X-Upstream-ID response header, got: %s", resp.Header.Get("X-Upstream-ID"))
	}
}

func TestHealthEndpoint(t *testing.T) {
	setupProxyTest(t)

	req := httptest.NewRequest("GET", "/health", nil)
	w := httptest.NewRecorder()
	HealthHandler(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte(`"ok"`)) {
		t.Fatalf("unexpected body: %s", body)
	}
}

func TestReadyzEndpoint(t *testing.T) {
	upd := setupProxyTest(t)

	// Test not-ready path.
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = ""
		s.ModelRouter = nil
	})

	req := httptest.NewRequest("GET", "/readyz", nil)
	w := httptest.NewRecorder()
	ReadyzHandler(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("expected 503, got %d", resp.StatusCode)
	}
	if !bytes.Contains(body, []byte(`"not ready"`)) {
		t.Fatalf("unexpected body: %s", body)
	}

	// Restore state — should return 200.
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = "http://placeholder"
		s.ModelRouter = router.NewModelRouter(s.Config)
	})
	w2 := httptest.NewRecorder()
	ReadyzHandler(w2, req)
	resp2 := w2.Result()
	body2, _ := io.ReadAll(resp2.Body)
	resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 after setup, got %d", resp2.StatusCode)
	}
	if !bytes.Contains(body2, []byte(`"ok"`)) {
		t.Fatalf("unexpected body: %s", body2)
	}
}

func TestDebugHealthEndpoint(t *testing.T) {
	upd := setupProxyTest(t)

	// Mock server that returns 200 for health check
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer ts.Close()

	req := httptest.NewRequest("GET", "/debug/health", nil)

	// 1. Upstream empty
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = ""
	})
	w := httptest.NewRecorder()
	DebugHealthHandler(w, req)
	if w.Result().StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for empty upstream, got %d", w.Result().StatusCode)
	}

	// 2. Upstream reachable
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = ts.URL
		s.Config.UseLLMRouter = false
	})
	w2 := httptest.NewRecorder()
	DebugHealthHandler(w2, req)
	if w2.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 for reachable upstream, got %d", w2.Result().StatusCode)
	}

	// 3. Upstream unreachable
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = "http://127.0.0.1:1" // connection refused
	})
	w3 := httptest.NewRecorder()
	DebugHealthHandler(w3, req)
	if w3.Result().StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 for unreachable upstream, got %d", w3.Result().StatusCode)
	}

	// 4. Circuit breaker open
	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = ts.URL
		s.Config.UseLLMRouter = true
	})
	// Force circuit breaker open
	router.LlmCircuitBreaker.RecordFailure()
	router.LlmCircuitBreaker.RecordFailure()
	router.LlmCircuitBreaker.RecordFailure()

	w4 := httptest.NewRecorder()
	DebugHealthHandler(w4, req)
	if w4.Result().StatusCode != http.StatusServiceUnavailable {
		t.Errorf("expected 503 when circuit breaker open, got %d", w4.Result().StatusCode)
	}

	// Reset circuit breaker
	router.LlmCircuitBreaker.RecordSuccess()
}

func TestHandleProxy_MetricsDirect(t *testing.T) {
	req := httptest.NewRequest("GET", "/debug/metrics", nil)
	w := httptest.NewRecorder()
	handleProxy(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestHandleProxy_UnrecognizedSweModelWarning(t *testing.T) {
	upd := setupProxyTest(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = ts.URL
	})

	body := `{"model":"swe.nonexistent-model"}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleProxy(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestHandleProxy_InvalidJsonV1Messages(t *testing.T) {
	upd := setupProxyTest(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = ts.URL
	})

	body := `{invalid json`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleProxy(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200 for forwarded invalid json, got %d", w.Result().StatusCode)
	}
}

func TestHandleProxy_SystemBlocksAndMessageBlocks(t *testing.T) {
	upd := setupProxyTest(t)
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{}`))
	}))
	defer ts.Close()

	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = ts.URL
	})

	body := `{
		"model": "swe.utility",
		"system": [{"type": "text", "text": "system instruction"}],
		"messages": [
			{"role": "user", "content": [{"type": "text", "text": "user message block"}]}
		]
	}`
	req := httptest.NewRequest("POST", "/v1/messages", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	handleProxy(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestProxyPayloadLogging(t *testing.T) {
	t.Setenv("LOG_PAYLOADS", "1")
	upd := setupProxyTest(t)

	// Mock upstream server
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		r.Body.Close()
		w.WriteHeader(http.StatusOK)
		w.Write(body)
	}))
	defer upstream.Close()

	upd(func(s *router.RouterState) {
		s.Config.UpstreamURL = upstream.URL
	})

	payload, _ := json.Marshal(map[string]interface{}{
		"model": "swe.utility",
		"messages": []map[string]string{
			{"role": "user", "content": "hello proxy"},
		},
	})

	req := httptest.NewRequest("POST", "/v1/messages", bytes.NewReader(payload))
	req.Header.Set("X-Request-ID", "test-trace-id-999")
	w := httptest.NewRecorder()

	handleProxy(w, req)

	resp := w.Result()
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", resp.StatusCode, string(body))
	}

	// Verify that the payload log is written to the payloads directory
	payloadsDir := logger.GetPayloadsDir()
	logFilePath := filepath.Join(payloadsDir, "req-test-trace-id-999.json")

	// Wait for goroutine to finish writing
	var data []byte
	var readErr error
	for i := 0; i < 20; i++ {
		time.Sleep(5 * time.Millisecond)
		data, readErr = os.ReadFile(logFilePath)
		if readErr == nil {
			break
		}
	}
	if readErr != nil {
		t.Fatalf("failed to read logged payload from proxy handler: %v", readErr)
	}

	var parsed struct {
		Model    string `json:"model"`
		Messages []struct {
			Role    string `json:"role"`
			Content string `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		t.Fatalf("logged payload is not valid JSON: %v", err)
	}

	if parsed.Model != "nvidia/stepfun-ai/step-3.7-flash" { // fallback model for swe.utility
		t.Errorf("expected logged model to be rewritten fallback model, got: %q", parsed.Model)
	}

	if len(parsed.Messages) == 0 || parsed.Messages[0].Content != "hello proxy" {
		t.Errorf("expected logged messages to contain the correct prompt content, got: %q", string(data))
	}
}
