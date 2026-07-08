package router

import (
	"sync/atomic"
	"time"

	"claude-proxy/internal/config"
	"claude-proxy/internal/logger"
)

// RouterState holds all mutable global configuration state.
// Swapped atomically via globalState — readers see a consistent snapshot.
type RouterState struct {
	Config           config.Config
	SemanticRuleMap  map[string][]config.SemanticRule
	DecisionModelMap map[string]string
	ModelSettingsMap map[string]config.ModelSetting
	ModelRouter      ModelRouter
}

// globalState is the single source of truth for routing config.
// Written during startup and SIGHUP reload. Read by every request.
var globalState atomic.Value

// GetState returns the current router state snapshot, safe for concurrent use.
func GetState() *RouterState {
	return globalState.Load().(*RouterState)
}

// SetRouterState atomically stores the given router state.
func SetRouterState(st *RouterState) {
	globalState.Store(st)
}

// SetState builds fast lookup maps from Config and atomically stores the state.
func SetState(cfg config.Config) {
	sm := make(map[string][]config.SemanticRule)
	for _, r := range cfg.SemanticRules {
		sm[r.TriggerModel] = append(sm[r.TriggerModel], r)
	}
	dm := make(map[string]string)
	for _, d := range cfg.DecisionMap {
		dm[d.Decision] = d.TargetModel
	}
	ms := make(map[string]config.ModelSetting)
	for k, v := range cfg.ModelSettings {
		ms[k] = v
	}

	router := NewModelRouter(cfg)

	// Propagate log config to logger package
	logger.SetRedactSensitivePayloads(cfg.RedactSensitivePayloads)
	logger.SetPayloadLogRetentionDays(cfg.PayloadLogRetentionDays)

	globalState.Store(&RouterState{
		Config:           cfg,
		SemanticRuleMap:  sm,
		DecisionModelMap: dm,
		ModelSettingsMap: ms,
		ModelRouter:      router,
	})
}

// ReloadConfig re-reads config file and atomically swaps global state.
// Intended for SIGHUP signal handling.
func ReloadConfig(configPath string) {
	// After reloading config, recreate the LLM circuit breaker so that env changes take effect.
	// Use same env parsing logic as newCircuitBreaker.
	LlmCircuitBreaker = newCircuitBreaker(3, 30*time.Second)

	path := configPath
	if path == "" {
		path = "config.json"
	}
	loader := &config.JSONConfigLoader{Path: path}
	cfg, err := loader.Load()
	if err != nil {
		logger.Errorf("[Reload] Lỗi tải lại cấu hình: %v", err)
		return
	}
	SetState(cfg)
	logger.Infof("[Reload] Cấu hình đã được tải lại thành công")
}

func init() {
	// Seed with empty state so first Load() safely replaces it.
	globalState.Store(&RouterState{
		SemanticRuleMap:  make(map[string][]config.SemanticRule),
		DecisionModelMap: make(map[string]string),
		ModelSettingsMap: make(map[string]config.ModelSetting),
	})
}
