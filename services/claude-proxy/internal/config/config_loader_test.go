package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestJSONConfigLoader_Success(t *testing.T) {
	cfg := `{
        "upstream_url": "http://example.com",
        "port": 1234,
        "use_llm_router": false,
        "router_model": "test-model",
        "upstream_api_key": "",
        "semantic_rules": [],
        "decision_map": [{"decision":"CUSTOM","target_model":"custom/model"}],
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

	loader := &JSONConfigLoader{Path: cfgPath}
	loadedCfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	if loadedCfg.UpstreamURL != "http://example.com" {
		t.Errorf("UpstreamURL = %q, want http://example.com", loadedCfg.UpstreamURL)
	}
	if loadedCfg.Port != 1234 {
		t.Errorf("Port = %d, want 1234", loadedCfg.Port)
	}
	if len(loadedCfg.DecisionMap) != 1 || loadedCfg.DecisionMap[0].Decision != "CUSTOM" {
		t.Errorf("DecisionMap = %+v, expected 1 item with CUSTOM", loadedCfg.DecisionMap)
	}
	setting, ok := loadedCfg.ModelSettings["swe.engineer"]
	if !ok {
		t.Fatalf("ModelSettings 'swe.engineer' not loaded")
	}
	if setting.SystemPrompt != "classify engineer tasks" {
		t.Errorf("SystemPrompt = %q, want 'classify engineer tasks'", setting.SystemPrompt)
	}
	if loadedCfg.PayloadLogRetentionDays != 7 {
		t.Errorf("PayloadLogRetentionDays = %d, want default 7", loadedCfg.PayloadLogRetentionDays)
	}
	if !loadedCfg.RedactSensitivePayloads {
		t.Errorf("RedactSensitivePayloads = %v, want default true", loadedCfg.RedactSensitivePayloads)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		cfg     Config
		wantErr bool
	}{
		{
			name: "Valid minimal config",
			cfg: Config{
				UpstreamURL: "http://localhost:8080",
				Port:        1234,
			},
			wantErr: false,
		},
		{
			name: "Missing upstream_url",
			cfg: Config{
				Port: 1234,
			},
			wantErr: true,
		},
		{
			name: "Invalid upstream_url",
			cfg: Config{
				UpstreamURL: "::not-a-url",
				Port:        1234,
			},
			wantErr: true,
		},
		{
			name: "Port out of range too low",
			cfg: Config{
				UpstreamURL: "http://localhost",
				Port:        -1,
			},
			wantErr: true,
		},
		{
			name: "Port 0 is valid",
			cfg: Config{
				UpstreamURL: "http://localhost",
				Port:        0,
			},
			wantErr: false,
		},
		{
			name: "Port out of range too high",
			cfg: Config{
				UpstreamURL: "http://localhost",
				Port:        65536,
			},
			wantErr: true,
		},
		{
			name: "LLM router missing model",
			cfg: Config{
				UpstreamURL:  "http://localhost",
				Port:         1234,
				UseLLMRouter: true,
				RouterModel:  "",
			},
			wantErr: true,
		},
		{
			name: "LLM router with model",
			cfg: Config{
				UpstreamURL:  "http://localhost",
				Port:         1234,
				UseLLMRouter: true,
				RouterModel:  "some-model",
			},
			wantErr: false,
		},
		{
			name: "Semantic rule missing trigger_model",
			cfg: Config{
				UpstreamURL: "http://localhost",
				Port:        1234,
				SemanticRules: []SemanticRule{
					{TriggerModel: "", TargetModel: "target"},
				},
			},
			wantErr: true,
		},
		{
			name: "Semantic rule missing target_model",
			cfg: Config{
				UpstreamURL: "http://localhost",
				Port:        1234,
				SemanticRules: []SemanticRule{
					{TriggerModel: "trigger", TargetModel: ""},
				},
			},
			wantErr: true,
		},
		{
			name: "Negative payload log retention days",
			cfg: Config{
				UpstreamURL:             "http://localhost",
				Port:                    1234,
				PayloadLogRetentionDays: -5,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateConfig(tt.cfg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func FuzzConfigLoader(f *testing.F) {
	// Seed with a valid config
	f.Add([]byte(`{"upstream_url":"http://127.0.0.1:8080","port":1234,"use_llm_router":false}`))
	f.Add([]byte(`{invalid json`))

	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		cfgPath := filepath.Join(dir, "fuzz_config.json")
		if err := os.WriteFile(cfgPath, data, 0644); err != nil {
			return
		}
		loader := &JSONConfigLoader{Path: cfgPath}
		_, _ = loader.Load() // Must not panic
	})
}

func TestConfigEnvOverrides(t *testing.T) {
	cfg := `{"upstream_url": "http://example.com", "port": 1234}`
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.json")
	if err := os.WriteFile(cfgPath, []byte(cfg), 0644); err != nil {
		t.Fatalf("write temp config failed: %v", err)
	}

	t.Setenv("PAYLOAD_LOG_RETENTION_DAYS", "14")
	t.Setenv("REDACT_SENSITIVE_PAYLOADS", "false")

	loader := &JSONConfigLoader{Path: cfgPath}
	loadedCfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load config failed: %v", err)
	}

	if loadedCfg.PayloadLogRetentionDays != 14 {
		t.Errorf("PayloadLogRetentionDays = %d, want 14", loadedCfg.PayloadLogRetentionDays)
	}
	if loadedCfg.RedactSensitivePayloads {
		t.Errorf("RedactSensitivePayloads = %v, want false", loadedCfg.RedactSensitivePayloads)
	}
}
