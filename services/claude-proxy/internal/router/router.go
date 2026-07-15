package router

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"claude-proxy/internal/config"
	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"
	"claude-proxy/internal/utils"
)

// ModelRouter decides target model based on original model and prompt.
type ModelRouter interface {
	Resolve(originalModel, prompt string) string
}

// llmRouter uses external router model with caching.
type llmRouter struct {
	cfg config.Config
}

// keywordRouter uses only keyword matching as fallback.
type keywordRouter struct {
	cfg config.Config
}

// NewModelRouter returns appropriate router implementation.
func NewModelRouter(cfg config.Config) ModelRouter {
	if cfg.UseLLMRouter {
		return &llmRouter{cfg: cfg}
	}
	return &keywordRouter{cfg: cfg}
}

func resolveRecursive(originalModel, prompt string, useLLM bool) string {
	visited := make(map[string]bool)
	currentModel := originalModel
	for strings.HasPrefix(currentModel, "swe.") {
		if visited[currentModel] {
			logger.Errorf("[Router] Cycle detected in recursive resolution: %s, falling back to %s", currentModel, originalModel)
			return originalModel
		}
		visited[currentModel] = true
		nextModel := resolveDynamic(currentModel, prompt, useLLM)
		if nextModel == currentModel {
			break
		}
		logger.Infof("[Router] Resolved %s -> %s", currentModel, nextModel)
		currentModel = nextModel
	}
	return currentModel
}

// Resolve implements routing for llmRouter.
func (r *llmRouter) Resolve(originalModel, prompt string) string {
	snippet := prompt
	if len(snippet) > 100 {
		snippet = snippet[:100] + "..."
	}
	logger.Infof("[Router] Resolve called: originalModel=%s, promptSnippet=%q", originalModel, snippet)
	return resolveRecursive(originalModel, prompt, true)
}

// Resolve implements routing for keywordRouter (no external call).
func (r *keywordRouter) Resolve(originalModel, prompt string) string {
	snippet := prompt
	if len(snippet) > 100 {
		snippet = snippet[:100] + "..."
	}
	logger.Infof("[Router] Resolve called (keyword only): originalModel=%s, promptSnippet=%q", originalModel, snippet)
	return resolveRecursive(originalModel, prompt, false)
}

// Built-in decision mappings (for backward compatibility when config is empty)
var builtinDecisionMap = map[string]string{
	"MINIMAX":  "ka.docs",
	"DEEPSEEK": "ka.simple",
	"FALLBACK": "ka.simple",
}

type genericResponse struct {
	Content []struct {
		Text string `json:"text"`
	} `json:"content"`
	Choices []struct {
		Message struct {
			Content          *string `json:"content"`
			Reasoning        *string `json:"reasoning"`
			ReasoningContent *string `json:"reasoning_content"`
		} `json:"message"`
	} `json:"choices"`
}

// callLLMRouter invokes external LLM router service to classify the task
func callLLMRouter(cfg config.Config, originalModel, prompt, systemPrompt string) string {
	metrics.ExternalCalls.Inc()
	start := time.Now()
	defer func() {
		metrics.ExternalLatency.Observe(time.Since(start).Seconds())
	}()

	// Truncate long prompts to avoid exceeding router token limit.
	if len(prompt) > 1500 {
		prompt = prompt[:1500] + "... [truncated]"
	}

	if systemPrompt == "" {
		systemPrompt = `You are an API Gateway Router. Analyze the user's task and classify it.
Respond with ONLY one of the following exact words. Do not write anything else, no explanations, no markdown formatting.

- "MINIMAX": If the task is writing documentation, explaining code, writing READMEs, summarizing, or editing markdown files.
- "DEEPSEEK": If the task is writing algorithms, unit tests, code optimization, helper functions, or repetitive boilerplate code.`
	}

	messages := []map[string]string{
		{"role": "system", "content": systemPrompt},
		{"role": "user", "content": prompt},
	}

	routerReqBody := map[string]interface{}{
		"model":      cfg.RouterModel,
		"messages":   messages,
		"max_tokens": 1024,
		"stream":     false,
	}

	jsonData, err := json.Marshal(routerReqBody)
	if err != nil {
		logger.Errorf("[Router Exception] JSON marshal error: %v", err)
		return "FALLBACK"
	}

	// Build upstream URL.
	upstreamURL := fmt.Sprintf("%s/v1/messages", strings.TrimSuffix(cfg.UpstreamURL, "/"))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "POST", upstreamURL, bytes.NewBuffer(jsonData))
	if err != nil {
		logger.Errorf("[Router Exception] NewRequest error: %v", err)
		return "FALLBACK"
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.UpstreamAPIKey != "" {
		req.Header.Set("x-api-key", cfg.UpstreamAPIKey)
		req.Header.Set("Authorization", "Bearer "+cfg.UpstreamAPIKey)
	}

	resp, err := utils.HTTPClient.Do(req)
	if err != nil {
		logger.Errorf("[Router Exception] HTTP call failed: %v", err)
		return "FALLBACK"
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		logger.Errorf("[Router Exception] Read body error: %v", err)
		return "FALLBACK"
	}

	if resp.StatusCode != http.StatusOK {
		logger.Errorf("[Router Exception] HTTP %d: %s", resp.StatusCode, string(body))
		return "FALLBACK"
	}

	var routerRes genericResponse
	if err := json.Unmarshal(body, &routerRes); err != nil {
		logger.Errorf("[Router Exception] Unmarshal error: %v", err)
		return "FALLBACK"
	}

	var rawDecision string
	if len(routerRes.Choices) > 0 && routerRes.Choices[0].Message.Content != nil {
		rawDecision = *routerRes.Choices[0].Message.Content
	} else if len(routerRes.Content) > 0 {
		rawDecision = routerRes.Content[0].Text
	}

	decision := strings.TrimSpace(strings.ToUpper(rawDecision))
	if decision != "" {
		return decision
	}

	// Fallback to check reasoning fields
	var reasoningText string
	if len(routerRes.Choices) > 0 {
		msg := routerRes.Choices[0].Message
		if msg.ReasoningContent != nil {
			reasoningText = *msg.ReasoningContent
		} else if msg.Reasoning != nil {
			reasoningText = *msg.Reasoning
		}
	}

	if reasoningText != "" {
		st := GetState()
		uReasoning := strings.ToUpper(reasoningText)
		if setting, ok := st.ModelSettingsMap[originalModel]; ok {
			for _, dm := range setting.DecisionMap {
				if strings.Contains(uReasoning, strings.ToUpper(dm.Decision)) {
					return dm.Decision
				}
			}
		}
		for k := range st.DecisionModelMap {
			if strings.Contains(uReasoning, strings.ToUpper(k)) {
				return k
			}
		}
		for k := range builtinDecisionMap {
			if strings.Contains(uReasoning, strings.ToUpper(k)) {
				return k
			}
		}
	}

	return "FALLBACK"
}

func init() {
	LlmCircuitBreaker = newCircuitBreaker(3, 30*time.Second)
}
