package router

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"claude-proxy/internal/config"
	"claude-proxy/internal/logger"
)

// mockRouterServer returns an httptest server that replies with the given decision.
func mockRouterServer(decision string) *httptest.Server {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}{
			Content: []struct {
				Text string `json:"text"`
			}{{Text: decision}},
		}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, string(data))
	})
	return httptest.NewServer(handler)
}

// storeTestState populates globalState atomically for a router test.
func storeTestState(cfg config.Config, sm map[string][]config.SemanticRule, dm map[string]string, ms map[string]config.ModelSetting) {
	router := NewModelRouter(cfg)
	globalState.Store(&RouterState{
		Config:           cfg,
		SemanticRuleMap:  sm,
		DecisionModelMap: dm,
		ModelSettingsMap: ms,
		ModelRouter:      router,
	})
}

func TestRouterCacheMissAndHit(t *testing.T) {
	t.Setenv("LOG_INFO", "1")
	t.Setenv("LOG_DEBUG", "1")

	ts := mockRouterServer("MINIMAX")
	defer ts.Close()

	cfg := config.Config{UpstreamURL: ts.URL, RouterModel: "test-model", UseLLMRouter: true}
	decisionCache = newTTLCache(time.Minute, time.Minute)
	infoPath := logger.SetupTestLogger(t)
	storeTestState(cfg, nil, nil, nil)

	prompt := "unique test prompt"
	got := GetState().ModelRouter.Resolve("swe.utility", prompt)
	expected := "nvidia/minimaxai/minimax-m3"
	if got != expected {
		t.Fatalf("first resolve returned %q, want %q", got, expected)
	}

	got2 := GetState().ModelRouter.Resolve("swe.utility", prompt)
	if got2 != expected {
		t.Fatalf("second resolve returned %q, want %q", got2, expected)
	}

	logger.CloseLogger()

	data, err := os.ReadFile(infoPath)
	if err != nil {
		t.Fatalf("read info log: %v", err)
	}
	logContent := string(data)
	if !strings.Contains(logContent, "Miss for model=swe.utility") {
		t.Fatalf("info log missing cache miss entry: %q", logContent)
	}
	if !strings.Contains(logContent, "Vì tìm thấy trong cache nên định tuyến model=swe.utility") {
		t.Fatalf("info log missing cache hit entry: %q", logContent)
	}
}

func TestRouterDecisionMapping(t *testing.T) {
	t.Setenv("LOG_INFO", "1")
	t.Setenv("LOG_DEBUG", "1")

	t.Run("CustomDecisionMapping", func(t *testing.T) {
		ts := mockRouterServer("CUSTOM")
		defer ts.Close()
		cfg := config.Config{UpstreamURL: ts.URL, RouterModel: "test-model", UseLLMRouter: true}
		decisionCache = newTTLCache(time.Minute, time.Minute)
		storeTestState(cfg, nil, map[string]string{"CUSTOM": "custom/model"}, nil)
		got := GetState().ModelRouter.Resolve("swe.utility", "any prompt")
		if got != "custom/model" {
			t.Fatalf("custom mapping returned %q, want %q", got, "custom/model")
		}
	})

	t.Run("BuiltinFallback", func(t *testing.T) {
		ts := mockRouterServer("FALLBACK")
		defer ts.Close()
		cfg := config.Config{UpstreamURL: ts.URL, RouterModel: "test-model", UseLLMRouter: true}
		decisionCache = newTTLCache(time.Minute, time.Minute)
		storeTestState(cfg, nil, map[string]string{}, nil)
		got := GetState().ModelRouter.Resolve("swe.utility", "any prompt")
		if got != "nvidia/stepfun-ai/step-3.7-flash" {
			t.Fatalf("builtin fallback returned %q, want %q", got, "nvidia/stepfun-ai/step-3.7-flash")
		}
	})
}

func TestRouterOpenAIFormat(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": "MINIMAX"}}]}`)
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	cfg := config.Config{UpstreamURL: ts.URL, RouterModel: "test-model", UseLLMRouter: true}
	decisionCache = newTTLCache(time.Minute, time.Minute)
	storeTestState(cfg, nil, nil, nil)
	got := GetState().ModelRouter.Resolve("swe.utility", "any prompt")
	if got != "nvidia/minimaxai/minimax-m3" {
		t.Fatalf("OpenAI format resolve returned %q, want %q", got, "nvidia/minimaxai/minimax-m3")
	}
}

func TestRouterOpenAIReasoningFormat(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices": [{"message": {"content": null, "reasoning_content": "We think DEEPSEEK is the best classifier choice."}}]}`)
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	cfg := config.Config{UpstreamURL: ts.URL, RouterModel: "test-model", UseLLMRouter: true}
	decisionCache = newTTLCache(time.Minute, time.Minute)
	storeTestState(cfg, nil, nil, nil)
	got := GetState().ModelRouter.Resolve("swe.utility", "any prompt")
	if got != "ds/deepseek-v4-flash" {
		t.Fatalf("OpenAI reasoning format resolve returned %q, want %q", got, "ds/deepseek-v4-flash")
	}
}

func TestCacheKeyIsolation(t *testing.T) {
	decisionCache = newTTLCache(time.Minute, time.Minute)
	// Directly pre-populate cache for one model and verify it does not bleed to another model with the same prompt.
	decisionCache.Store("swe.utility:test prompt", "swe.utility-resolved")

	cfg := config.Config{UseLLMRouter: false} // Ensure FSM routing fallback runs without LLM call
	// Set up semantic rule fallback for swe.architect to return a different model
	storeTestState(cfg,
		map[string][]config.SemanticRule{
			"swe.architect": {
				{TriggerModel: "swe.architect", TargetModel: "architect-fallback"},
			},
		},
		nil, nil)

	got := GetState().ModelRouter.Resolve("swe.architect", "test prompt")
	if got == "swe.utility-resolved" {
		t.Fatalf("cache collision: architect resolved to utility's cache entry")
	}
	if got != "architect-fallback" {
		t.Fatalf("architect resolved to %q, want %q", got, "architect-fallback")
	}
}

func TestCustomModelSettings(t *testing.T) {
	var capturedSystemPrompt string
	var mu sync.Mutex

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		var reqBody struct {
			Messages []struct {
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.Unmarshal(body, &reqBody); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		mu.Lock()
		if len(reqBody.Messages) > 0 {
			capturedSystemPrompt = reqBody.Messages[0].Content
		}
		mu.Unlock()

		resp := struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
		}{
			Content: []struct {
				Text string `json:"text"`
			}{{Text: "CUSTOM_DECISION"}},
		}
		data, _ := json.Marshal(resp)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, string(data))
	})
	ts := httptest.NewServer(handler)
	defer ts.Close()

	// Config setup with custom system prompt and custom decision maps for swe.engineer
	cfg := config.Config{
		UpstreamURL:  ts.URL,
		RouterModel:  "custom-router",
		UseLLMRouter: true,
		ModelSettings: map[string]config.ModelSetting{
			"swe.engineer": {
				SystemPrompt: "Custom System Prompt for Engineer Classifier",
				DecisionMap: []config.DecisionMapping{
					{Decision: "CUSTOM_DECISION", TargetModel: "resolved/custom-engineer-model"},
				},
			},
		},
	}

	decisionCache = newTTLCache(time.Minute, time.Minute)
	storeTestState(cfg, nil, nil, cfg.ModelSettings)

	got := GetState().ModelRouter.Resolve("swe.engineer", "optimize this loop")

	mu.Lock()
	sysPrompt := capturedSystemPrompt
	mu.Unlock()

	if sysPrompt != "Custom System Prompt for Engineer Classifier" {
		t.Fatalf("expected system prompt %q, got %q", "Custom System Prompt for Engineer Classifier", sysPrompt)
	}

	if got != "resolved/custom-engineer-model" {
		t.Fatalf("expected resolved model %q, got %q", "resolved/custom-engineer-model", got)
	}
}

func TestIsDynamicModel(t *testing.T) {
	st := &RouterState{
		SemanticRuleMap: map[string][]config.SemanticRule{
			"swe.utility": {
				{TriggerModel: "swe.utility", Keywords: []string{"doc"}, TargetModel: "minimax"},
			},
			"swe.architect": {
				{TriggerModel: "swe.architect", TargetModel: "gpt-oss"},
			},
		},
		ModelSettingsMap: map[string]config.ModelSetting{
			"custom.dynamic": {
				SystemPrompt: "Custom Prompt",
			},
		},
	}
	globalState.Store(st)

	tests := []struct {
		model string
		want  bool
	}{
		{"swe.utility", true},
		{"custom.dynamic", true},
		{"swe.architect", false},
		{"unknown.model", false},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := isDynamicModel(tt.model); got != tt.want {
				t.Errorf("isDynamicModel(%q) = %v, want %v", tt.model, got, tt.want)
			}
		})
	}
}

func TestGetStaticTarget(t *testing.T) {
	globalState.Store(&RouterState{
		SemanticRuleMap: map[string][]config.SemanticRule{
			"swe.architect": {
				{TriggerModel: "swe.architect", TargetModel: "gpt-oss"},
			},
		},
	})

	tests := []struct {
		model string
		want  string
	}{
		{"swe.architect", "gpt-oss"},
		{"swe.utility", "nvidia/stepfun-ai/step-3.7-flash"},
		{"unknown.model", "unknown.model"},
	}

	for _, tt := range tests {
		t.Run(tt.model, func(t *testing.T) {
			if got := getStaticTarget(tt.model); got != tt.want {
				t.Errorf("getStaticTarget(%q) = %q, want %q", tt.model, got, tt.want)
			}
		})
	}
}

func TestConfigEnvOverride(t *testing.T) {
	tests := []struct {
		name        string
		baseValue   bool
		envValue    string
		expectValue bool
		hasEnv      bool
	}{
		{
			name:        "Override false to true with true",
			baseValue:   false,
			envValue:    "true",
			expectValue: true,
			hasEnv:      true,
		},
		{
			name:        "Override false to true with 1",
			baseValue:   false,
			envValue:    "1",
			expectValue: true,
			hasEnv:      true,
		},
		{
			name:        "Override true to false with false",
			baseValue:   true,
			envValue:    "false",
			expectValue: false,
			hasEnv:      true,
		},
		{
			name:        "Override true to false with 0",
			baseValue:   true,
			envValue:    "0",
			expectValue: false,
			hasEnv:      true,
		},
		{
			name:        "Keep true when env not set",
			baseValue:   true,
			envValue:    "",
			expectValue: true,
			hasEnv:      false,
		},
		{
			name:        "Keep false when env not set",
			baseValue:   false,
			envValue:    "",
			expectValue: false,
			hasEnv:      false,
		},
		{
			name:        "Keep true when env is invalid",
			baseValue:   true,
			envValue:    "invalid",
			expectValue: true,
			hasEnv:      true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create a temporary configuration file.
			cfgJSON := fmt.Sprintf(`{"upstream_url": "http://example.com", "port": 1234, "use_llm_router": %v, "router_model": "test-router-model"}`, tc.baseValue)
			dir := t.TempDir()
			cfgPath := filepath.Join(dir, "config.json")
			if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0644); err != nil {
				t.Fatalf("failed to write temp config: %v", err)
			}

			// Save existing environment variable value.
			origEnv, wasSet := os.LookupEnv("USE_LLM_ROUTER")
			defer func() {
				if wasSet {
					os.Setenv("USE_LLM_ROUTER", origEnv)
				} else {
					os.Unsetenv("USE_LLM_ROUTER")
				}
			}()

			// Set or unset environment variable.
			if tc.hasEnv {
				os.Setenv("USE_LLM_ROUTER", tc.envValue)
			} else {
				os.Unsetenv("USE_LLM_ROUTER")
			}

			loader := &config.JSONConfigLoader{Path: cfgPath}
			loadedCfg, err := loader.Load()
			if err != nil {
				t.Fatalf("failed to load config: %v", err)
			}

			if loadedCfg.UseLLMRouter != tc.expectValue {
				t.Errorf("expected UseLLMRouter to be %v, got %v", tc.expectValue, loadedCfg.UseLLMRouter)
			}
		})
	}
}

func TestCallLLMRouter(t *testing.T) {
	logger.SetupTestLogger(t)

	tests := []struct {
		name    string
		handler http.HandlerFunc
		want    string
	}{
		{
			name: "Anthropic content array",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"content":[{"text":"MINIMAX"}]}`)
			},
			want: "MINIMAX",
		},
		{
			name: "OpenAI choices message content",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":"DEEPSEEK"}}]}`)
			},
			want: "DEEPSEEK",
		},
		{
			name: "OpenAI reasoning_content fallback",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":null,"reasoning_content":"The task looks like MINIMAX territory."}}]}`)
			},
			want: "MINIMAX",
		},
		{
			name: "OpenAI reasoning field fallback",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"choices":[{"message":{"content":null,"reasoning":"I think DEEPSEEK is best here."}}]}`)
			},
			want: "DEEPSEEK",
		},
		{
			name: "Empty content returns FALLBACK",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{"content":[{"text":""}]}`)
			},
			want: "FALLBACK",
		},
		{
			name: "HTTP 500 returns FALLBACK",
			handler: func(w http.ResponseWriter, r *http.Request) {
				http.Error(w, "internal server error", http.StatusInternalServerError)
			},
			want: "FALLBACK",
		},
		{
			name: "Malformed JSON returns FALLBACK",
			handler: func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, `{not valid json`)
			},
			want: "FALLBACK",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ts := httptest.NewServer(tc.handler)
			defer ts.Close()

			cfg := config.Config{
				UpstreamURL:  ts.URL,
				RouterModel:  "test-router-model",
				UseLLMRouter: true,
			}
			storeTestState(cfg, nil, nil, nil)

			got := callLLMRouter(cfg, "swe.utility", "some prompt", "")
			if got != tc.want {
				t.Errorf("callLLMRouter() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestCallLLMRouter_Timeout(t *testing.T) {
	logger.SetupTestLogger(t)

	// Port 1 is always refused — simulates an unreachable upstream without goroutine leaks.
	cfg := config.Config{
		UpstreamURL:  "http://127.0.0.1:1",
		RouterModel:  "test-model",
		UseLLMRouter: true,
	}
	storeTestState(cfg, nil, nil, nil)

	got := callLLMRouter(cfg, "swe.utility", "some prompt", "")
	if got != "FALLBACK" {
		t.Errorf("expected FALLBACK on unreachable upstream, got %q", got)
	}
}

func TestRouter_Resolve_CircuitBreakerOpen(t *testing.T) {
	logger.SetupTestLogger(t)

	cfg := config.Config{
		UpstreamURL:  "http://127.0.0.1:1",
		RouterModel:  "test-model",
		UseLLMRouter: true,
	}

	// Dynamic model rules
	rules := map[string][]config.SemanticRule{
		"swe.utility": {
			{
				Keywords:    []string{"documentation"},
				TargetModel: "minimax-model",
			},
		},
	}
	storeTestState(cfg, rules, nil, nil)

	// Force circuit breaker open
	LlmCircuitBreaker.mu.Lock()
	LlmCircuitBreaker.state = cbOpen
	LlmCircuitBreaker.failures = 3
	LlmCircuitBreaker.lastFailure = time.Now()
	LlmCircuitBreaker.mu.Unlock()

	defer func() {
		// Reset circuit breaker
		LlmCircuitBreaker.RecordSuccess()
	}()

	router := NewModelRouter(cfg)

	// Resolve a dynamic model. Since the circuit breaker is open, it should skip the LLM call
	// and fall back to keyword matching!
	// We pass "documentation prompt" to trigger the keyword "documentation".
	resolved := router.Resolve("swe.utility", "documentation prompt")

	if resolved != "minimax-model" {
		t.Errorf("expected resolved model to be minimax-model (keyword match fallback), got %q", resolved)
	}
}

// TestDecisionMapLoading verifies that JSONConfigLoader populates the decision map from config.json.
func TestDecisionMapLoading(t *testing.T) {
	cfg := `{
        "upstream_url": "http://example.com",
        "port": 1234,
        "use_llm_router": false,
        "router_model": "test-model",
        "upstream_api_key": "",
        "semantic_rules": [],
        "decision_map": [{"decision":"CUSTOM","target_model":"custom/model"}]
    }`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write temp config failed: %v", err)
	}

	loader := &config.JSONConfigLoader{Path: cfgPath}
	loadedCfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	SetState(loadedCfg)

	st := GetState()
	if got, ok := st.DecisionModelMap["CUSTOM"]; !ok || got != "custom/model" {
		t.Fatalf("DecisionModelMap[CUSTOM] = %q, ok=%v, want custom/model", got, ok)
	}
}

// TestModelSettingsLoading verifies that JSONConfigLoader loads model_settings configurations.
func TestModelSettingsLoading(t *testing.T) {
	cfg := `{
        "upstream_url": "http://example.com",
        "port": 1234,
        "use_llm_router": true,
        "router_model": "test-model",
        "model_settings": {
            "swe.engineer": {
                "system_prompt": "classify engineer tasks",
                "decision_map": [
                    {"decision": "EXECUTE", "target_model": "concrete-model"}
                ]
            }
        }
    }`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write temp config failed: %v", err)
	}

	loader := &config.JSONConfigLoader{Path: cfgPath}
	loadedCfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}
	SetState(loadedCfg)

	st := GetState()
	setting, ok := st.ModelSettingsMap["swe.engineer"]
	if !ok {
		t.Fatalf("modelSettingsMap does not contain 'swe.engineer'")
	}
	if setting.SystemPrompt != "classify engineer tasks" {
		t.Fatalf("expected system prompt %q, got %q", "classify engineer tasks", setting.SystemPrompt)
	}
	if len(setting.DecisionMap) != 1 || setting.DecisionMap[0].Decision != "EXECUTE" || setting.DecisionMap[0].TargetModel != "concrete-model" {
		t.Fatalf("loaded DecisionMap is invalid: %+v", setting.DecisionMap)
	}
}

// TestReloadConfig_Valid verifies that ReloadConfig() atomically swaps state from config A to config B.
func TestReloadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")

	// Load initial config A.
	cfgA := `{"upstream_url":"http://host-a.example.com","port":8080,"use_llm_router":false,"decision_map":[]}`
	if err := os.WriteFile(cfgPath, []byte(cfgA), 0644); err != nil {
		t.Fatalf("write config A: %v", err)
	}
	loader := &config.JSONConfigLoader{Path: cfgPath}
	loadedCfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load config A: %v", err)
	}
	SetState(loadedCfg)

	if got := GetState().Config.UpstreamURL; got != "http://host-a.example.com" {
		t.Fatalf("after load A: UpstreamURL = %q, want %q", got, "http://host-a.example.com")
	}

	// Write config B and trigger SIGHUP-equivalent reload.
	cfgB := `{"upstream_url":"http://host-b.example.com","port":9090,"use_llm_router":true,"router_model":"test-model","decision_map":[]}`
	if err := os.WriteFile(cfgPath, []byte(cfgB), 0644); err != nil {
		t.Fatalf("write config B: %v", err)
	}
	config.ConfigPath = cfgPath
	ReloadConfig(cfgPath)

	st := GetState()
	if st.Config.UpstreamURL != "http://host-b.example.com" {
		t.Fatalf("after reload: UpstreamURL = %q, want %q", st.Config.UpstreamURL, "http://host-b.example.com")
	}
	if st.Config.Port != 9090 {
		t.Fatalf("after reload: Port = %d, want 9090", st.Config.Port)
	}
	if !st.Config.UseLLMRouter {
		t.Fatalf("after reload: UseLLMRouter should be true")
	}
}

// TestReloadConfig_InvalidFile verifies that ReloadConfig() leaves global state unchanged when the
// config file is missing or contains invalid JSON.
func TestReloadConfig_InvalidFile(t *testing.T) {
	// Load a valid baseline config first.
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	cfgGood := `{"upstream_url":"http://good.example.com","port":1111,"use_llm_router":false,"decision_map":[]}`
	if err := os.WriteFile(cfgPath, []byte(cfgGood), 0644); err != nil {
		t.Fatalf("write good config: %v", err)
	}
	loader := &config.JSONConfigLoader{Path: cfgPath}
	loadedCfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load good config: %v", err)
	}
	SetState(loadedCfg)
	beforeURL := GetState().Config.UpstreamURL

	// Point ReloadConfig to a non-existent file.
	config.ConfigPath = filepath.Join(dir, "nonexistent.json")
	ReloadConfig(config.ConfigPath) // must not panic

	if got := GetState().Config.UpstreamURL; got != beforeURL {
		t.Fatalf("state changed after failed reload: got %q, want %q", got, beforeURL)
	}

	// Point ReloadConfig to a file with invalid JSON.
	badPath := filepath.Join(dir, "bad.json")
	if err := os.WriteFile(badPath, []byte("{not valid json"), 0644); err != nil {
		t.Fatalf("write bad config: %v", err)
	}
	config.ConfigPath = badPath
	ReloadConfig(config.ConfigPath) // must not panic

	if got := GetState().Config.UpstreamURL; got != beforeURL {
		t.Fatalf("state changed after bad JSON reload: got %q, want %q", got, beforeURL)
	}
}
