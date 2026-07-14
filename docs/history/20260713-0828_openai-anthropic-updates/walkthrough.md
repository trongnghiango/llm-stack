# Walkthrough - OpenAI/Anthropic API updates & Reasoning block conversions

We have completed the implementation of the OpenAI and Anthropic format upgrades in `cf-ai-proxy`. The proxy now fully supports modern parameter specifications (including `thinking`, `effort`, `max_completion_tokens`, and `reasoning_effort`) and natively parses chain-of-thought outputs (like the `<think>` tag) into proper Anthropic `"thinking"` content blocks for downstream client rendering (e.g. inside Claude Code).

## Changes Made

### 1. Data Model Upgrades
Updated [models.go](file:///home/ka/Repos/github.com/trongnghiango/llm-stack/services/cf-ai-proxy/models.go) to include new parameters:
- Added `MaxCompletionTokens`, `ReasoningEffort`, `Store`, and `ResponseFormat` to `OpenAIRequest`.
- Added `Thinking` and `Effort` to `AnthropicRequest`.

### 2. Request & History Conversion
Updated [handler.go](file:///home/ka/Repos/github.com/trongnghiango/llm-stack/services/cf-ai-proxy/handler.go) in the `HandleAnthropicCompletion` entrypoint:
- Mapped Anthropic request parameter `Effort` (or `Thinking`'s effort setting) directly to OpenAI `ReasoningEffort`.
- Mapped `MaxTokens` dynamically to `MaxCompletionTokens` for parallel reasoning capability.
- Added history context conversion: parsed incoming `"thinking"` type block messages in the conversation history and wrapped their text in `<think>\n...\n</think>\n` before passing it upstream.

### 3. Response Conversion (Standard & Stream)
Updated [handler.go](file:///home/ka/Repos/github.com/trongnghiango/llm-stack/services/cf-ai-proxy/handler.go) to handle reasoning models (like `deepseek-r1`):
- Added `parseThinkingTags` utility to extract string fragments within `<think>` and `</think>` tags.
- **Non-stream**: Updated `handleAnthropicStandard` to separate thinking segments and return them as native Anthropic content blocks of type `"thinking"`.
- **Stream**: Replaced `handleAnthropicStream` with a robust state machine that detects incoming reasoning tokens (via OpenAI `reasoning_content` or raw `<think>` tag streams), issues a proper `"thinking"` type block start and deltas, closes it on `</think>`, and transitions to a `"text"` block. It also dynamically updates downstream tool indexes if a thinking block is prepended.

### 4. Added Unit Tests
Added test cases in [main_test.go](file:///home/ka/Repos/github.com/trongnghiango/llm-stack/services/cf-ai-proxy/main_test.go) (`TestParseThinkingTags`) to verify the parsing of `<think>` and `</think>` tags with various inputs.

---

## Verification Results

### Automated Tests
Ran the entire proxy test suite, and all 19 tests passed successfully:
```bash
$ go test -v ./...
=== RUN   TestModelNameNormalization
--- PASS: TestModelNameNormalization (0.00s)
=== RUN   TestAccountLifecycleAndPenalization
--- PASS: TestAccountLifecycleAndPenalization (0.00s)
=== RUN   TestAnthropicStandardResponse
--- PASS: TestAnthropicStandardResponse (0.00s)
=== RUN   TestAnthropicStreamResponse
--- PASS: TestAnthropicStreamResponse (0.00s)
...
=== RUN   TestParseThinkingTags
=== RUN   TestParseThinkingTags/Case-0
=== RUN   TestParseThinkingTags/Case-1
=== RUN   TestParseThinkingTags/Case-2
=== RUN   TestParseThinkingTags/Case-3
--- PASS: TestParseThinkingTags (0.00s)
...
PASS
ok  	cf-ai-proxy	1.023s
```
