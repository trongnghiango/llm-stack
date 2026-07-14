package config

// SemanticRule defines routing rule for a trigger model.
type SemanticRule struct {
	TriggerModel string   `json:"trigger_model"`
	Keywords     []string `json:"keywords"`
	TargetModel  string   `json:"target_model"`
	Description  string   `json:"description"`
}

// Config aggregates proxy configuration.
type DecisionMapping struct {
	Decision    string `json:"decision"`
	TargetModel string `json:"target_model"`
}

type ModelSetting struct {
	SystemPrompt string            `json:"system_prompt,omitempty"`
	DecisionMap  []DecisionMapping `json:"decision_map,omitempty"`
}

type SelectionRule struct {
    Pattern    string `json:"pattern,omitempty"`
    MinTokens int    `json:"min_tokens,omitempty"`
    MaxTokens int    `json:"max_tokens,omitempty"`
    TargetModel string `json:"target_model"`
    Description string `json:"description,omitempty"`
}

// Config aggregates proxy configuration.

type Config struct {
    // Existing fields ...
    // SelectionRules defines heuristic rules applied before LLM routing.
    // Each rule can specify a regex pattern, a token length range, and the target model.
    // Rules are evaluated in order; the first matching rule wins.
    SelectionRules []SelectionRule `json:"selection_rules,omitempty"`
	UpstreamURL             string                  `json:"upstream_url"`
	Port                    int                     `json:"port"`
	UseLLMRouter            bool                    `json:"use_llm_router"`
	RouterModel             string                  `json:"router_model"`
	UpstreamAPIKey          string                  `json:"upstream_api_key"`
	SemanticRules           []SemanticRule          `json:"semantic_rules"`
	DecisionMap             []DecisionMapping       `json:"decision_map"`
	ModelSettings           map[string]ModelSetting `json:"model_settings,omitempty"`
	PayloadLogRetentionDays int                     `json:"payload_log_retention_days,omitempty"`
	RedactSensitivePayloads bool                    `json:"redact_sensitive_payloads,omitempty"`
}

// ConfigPath stores the --config flag value for SIGHUP reload.
var ConfigPath string
