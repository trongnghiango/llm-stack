package config

import (
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"claude-proxy/internal/logger"
)

// ConfigProvider loads configuration for the proxy.
type ConfigProvider interface {
	Load() (Config, error)
}

// JSONConfigLoader reads a JSON configuration file.
type JSONConfigLoader struct {
	Path string
}

func validateConfig(cfg Config) error {
	if cfg.UpstreamURL == "" {
		return fmt.Errorf("upstream_url is required")
	}
	u, err := url.Parse(cfg.UpstreamURL)
	if err != nil || u.Scheme == "" || u.Host == "" {
		return fmt.Errorf("upstream_url is invalid: %s", cfg.UpstreamURL)
	}
	if cfg.Port < 0 || cfg.Port > 65535 {
		return fmt.Errorf("port must be in range [0, 65535], got %d", cfg.Port)
	}
	if cfg.UseLLMRouter && cfg.RouterModel == "" {
		return fmt.Errorf("router_model is required when use_llm_router is true")
	}
	if cfg.PayloadLogRetentionDays < 0 {
		return fmt.Errorf("payload_log_retention_days must be non-negative, got %d", cfg.PayloadLogRetentionDays)
	}
	for i, r := range cfg.SemanticRules {
		if r.TriggerModel == "" {
			return fmt.Errorf("semantic_rules[%d]: trigger_model is required", i)
		}
		if r.TargetModel == "" {
			return fmt.Errorf("semantic_rules[%d]: target_model is required", i)
		}
	}
	return nil
}

// Load parses the JSON file, populates configuration.
func (j *JSONConfigLoader) Load() (Config, error) {
	f, err := os.Open(j.Path)
	if err != nil {
		return Config{}, err
	}
	defer f.Close()

	cfg := Config{
		PayloadLogRetentionDays: 7,
		RedactSensitivePayloads: true,
	}
	if err := json.NewDecoder(f).Decode(&cfg); err != nil {
		return Config{}, err
	}

	// Environment variable overrides for UseLLMRouter
	if val := os.Getenv("USE_LLM_ROUTER"); val != "" {
		lower := strings.ToLower(val)
		if lower == "1" || lower == "true" {
			cfg.UseLLMRouter = true
		} else if lower == "0" || lower == "false" {
			cfg.UseLLMRouter = false
		}
	}

	// Environment variable overrides for PayloadLogRetentionDays
	if val := os.Getenv("PAYLOAD_LOG_RETENTION_DAYS"); val != "" {
		if days, err := strconv.Atoi(val); err == nil && days >= 0 {
			cfg.PayloadLogRetentionDays = days
		}
	}

	// Environment variable overrides for RedactSensitivePayloads
	if val := os.Getenv("REDACT_SENSITIVE_PAYLOADS"); val != "" {
		lower := strings.ToLower(val)
		if lower == "1" || lower == "true" {
			cfg.RedactSensitivePayloads = true
		} else if lower == "0" || lower == "false" {
			cfg.RedactSensitivePayloads = false
		}
	}

	// Environment variable overrides for RedisURL
	if val := os.Getenv("REDIS_URL"); val != "" {
		cfg.RedisURL = val
	}

	if err := validateConfig(cfg); err != nil {
		return Config{}, fmt.Errorf("validation failed: %w", err)
	}

	logger.Infof("[Config] Loaded %s | port %d | router %v", cfg.UpstreamURL, cfg.Port, cfg.UseLLMRouter)
	return cfg, nil
}
