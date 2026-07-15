package router

import (
	"fmt"
	"strings"
	"time"
	"regexp"

	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"
)

type resolveState int

const (
	stateIdle resolveState = iota
	stateCacheLookup
	stateCacheMiss
	stateCallLLM
	stateKeywordMatch
	stateFallbackRule
	stateDone
)

func resolveDynamic(originalModel, prompt string, useLLM bool) string {
	if !isDynamicModel(originalModel) {
		target := getStaticTarget(originalModel)
		logger.Debugf("[Router] Static route model=%s -> %s", originalModel, target)
		return target
	}

	cur := stateIdle
	var resolvedModel string
	cacheKey := originalModel + ":" + prompt
	// Snapshot state once: guarantees cfg and maps are from the same atomic generation.
	st := GetState()
	cfg := st.Config

	// Heuristic pre‑filter: apply selection rules before any cache or LLM call.
	tokenCount := len(strings.Fields(prompt))
	matched := false
	for _, rule := range st.SelectionRules {
		if rule.Pattern != "" {
			re := regexp.MustCompile(rule.Pattern)
			if re.MatchString(prompt) {
				resolvedModel = rule.TargetModel
				metrics.HeuristicMatchesTotal.Inc()
				logger.Infof("[Router] Vì %s nên định tuyến (heuristic) đến %s", rule.Description, resolvedModel)
				matched = true
				break
			}
		}
		if rule.MinTokens != 0 || rule.MaxTokens != 0 {
			minOK := rule.MinTokens == 0 || tokenCount >= rule.MinTokens
			maxOK := rule.MaxTokens == 0 || tokenCount <= rule.MaxTokens
			if minOK && maxOK {
				resolvedModel = rule.TargetModel
				metrics.HeuristicMatchesTotal.Inc()
				logger.Infof("[Router] Vì %s nên định tuyến (heuristic) đến %s", rule.Description, resolvedModel)
				matched = true
				break
			}
		}
	}
	if matched {
		return resolvedModel
	}
	metrics.HeuristicMissesTotal.Inc()

	for cur != stateDone {
		switch cur {
		case stateIdle:
			cur = stateCacheLookup
		case stateCacheLookup:
			startCache := time.Now()
			if d, ok := decisionCache.Load(cacheKey); ok {
				metrics.CacheHits.Inc()
				logger.Infof("[Cache] Vì tìm thấy trong cache nên định tuyến model=%s -> %s", originalModel, d)
				resolvedModel = d
				cur = stateDone
				metrics.RoutingDuration.WithLabelValues("cache").Observe(time.Since(startCache).Seconds())
				break
			}
			metrics.CacheMisses.Inc()
			logger.Infof("[Cache] Miss for model=%s", originalModel)
			cur = stateCacheMiss
			metrics.RoutingDuration.WithLabelValues("cache").Observe(time.Since(startCache).Seconds())
		case stateCacheMiss:
			if useLLM {
				cur = stateCallLLM
			} else {
				cur = stateKeywordMatch
			}
		case stateCallLLM:
			// Check circuit breaker before making the external call.
			if !LlmCircuitBreaker.Allow() {
				logger.Infof("[CircuitBreaker] State=%s, skipping LLM call, routing via keyword", LlmCircuitBreaker.State())
				metrics.LlmErrorsTotal.Inc()
				cur = stateKeywordMatch
				break
			}

			var systemPrompt string
			if setting, ok := st.ModelSettingsMap[originalModel]; ok {
				systemPrompt = setting.SystemPrompt
			}

			startLLM := time.Now()
			decision := callLLMRouter(cfg, originalModel, prompt, systemPrompt)
			metrics.RoutingDuration.WithLabelValues("llm").Observe(time.Since(startLLM).Seconds())

			if decision == "FALLBACK" || decision == "" {
				LlmCircuitBreaker.RecordFailure()
				metrics.LlmErrorsTotal.Inc()
				cur = stateKeywordMatch
			} else {
				resolvedModel = resolveDecision(originalModel, decision)
				if resolvedModel == "FALLBACK" {
					LlmCircuitBreaker.RecordFailure()
					metrics.LlmErrorsTotal.Inc()
					logger.Debugf("[Router] Decision '%s' not mapped for model=%s, falling back to keyword match", decision, originalModel)
					cur = stateKeywordMatch
				} else {
					LlmCircuitBreaker.RecordSuccess()
					decisionCache.Store(cacheKey, resolvedModel)
					explanation := fmt.Sprintf("LLM Router phân loại thuộc nhóm %s", decision)
					if decision == "MINIMAX" {
						explanation = "yêu cầu liên quan đến tài liệu hoặc giải thích mã nguồn (LLM Router: MINIMAX)"
					} else if decision == "DEEPSEEK" {
						explanation = "yêu cầu liên quan đến lập trình, thuật toán hoặc unit test (LLM Router: DEEPSEEK)"
					}
					logger.Infof("[Router] Vì %s nên định tuyến đến %s", explanation, resolvedModel)
					cur = stateDone
				}
			}
		case stateKeywordMatch:
			startKeyword := time.Now()
			lower := strings.ToLower(prompt)
			var matchedKw string
			if rules, ok := st.SemanticRuleMap[originalModel]; ok {
				for _, rule := range rules {
					if len(rule.Keywords) > 0 {
						for _, kw := range rule.Keywords {
							if strings.Contains(lower, strings.ToLower(kw)) {
								resolvedModel = rule.TargetModel
								matchedKw = kw
								break
							}
						}
					}
					if resolvedModel != "" {
						break
					}
				}
			}
			metrics.RoutingDuration.WithLabelValues("keyword").Observe(time.Since(startKeyword).Seconds())
			if resolvedModel != "" {
				// Do not cache keyword-match results: if LLM previously timed out/errored,
				// we want subsequent requests to retry the LLM, not persist the degraded result.
				logger.Infof("[Router] Vì yêu cầu khớp từ khóa '%s' nên định tuyến đến %s", matchedKw, resolvedModel)
				cur = stateDone
			} else {
				cur = stateFallbackRule
			}
		case stateFallbackRule:
			startFallback := time.Now()
			resolvedModel = getStaticTarget(originalModel)
			metrics.FallbackTotal.Inc()
			metrics.RoutingDuration.WithLabelValues("fallback").Observe(time.Since(startFallback).Seconds())
			// Do not cache static fallback — same reasoning as keyword match above.
			logger.Infof("[Router] Vì không khớp bất kỳ luật phân loại nào nên định tuyến fallback đến %s", resolvedModel)
			cur = stateDone
		}
	}
	return resolvedModel
}

// isDynamicModel returns true if a model requires dynamic classifier routing.
func isDynamicModel(model string) bool {
	if model == "swe.utility" {
		return true
	}
	st := GetState()
	if rules, ok := st.SemanticRuleMap[model]; ok {
		for _, rule := range rules {
			if len(rule.Keywords) > 0 {
				return true
			}
		}
	}
	if setting, ok := st.ModelSettingsMap[model]; ok {
		if setting.SystemPrompt != "" || len(setting.DecisionMap) > 0 {
			return true
		}
	}
	return false
}

// getStaticTarget returns the target model for static routing mappings.
func getStaticTarget(model string) string {
	st := GetState()
	if rules, ok := st.SemanticRuleMap[model]; ok {
		for _, rule := range rules {
			if len(rule.Keywords) == 0 {
				return rule.TargetModel
			}
		}
	}
	if model == "swe.utility" {
		return "ka.simple"
	}
	return model
}

// resolveDecision searches maps from specific settings to global and built-in rules.
func resolveDecision(model, decision string) string {
	st := GetState()

	// 1. Check model specific decision map
	if setting, ok := st.ModelSettingsMap[model]; ok {
		for _, dm := range setting.DecisionMap {
			if dm.Decision == decision {
				return dm.TargetModel
			}
		}
	}
	// 2. Check global decision map configuration
	if m, ok := st.DecisionModelMap[decision]; ok {
		return m
	}
	// 3. Check built-in fallback decision map
	if m, ok := builtinDecisionMap[decision]; ok {
		return m
	}
	// 4. Return FALLBACK if no mapping matches — avoids using garbage as model name
	return "FALLBACK"
}
