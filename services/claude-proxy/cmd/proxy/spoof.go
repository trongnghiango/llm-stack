package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
	"time"

	"claude-proxy/internal/logger"
)

// ─────────────────────────────────────────────
// Low-level helpers
// ─────────────────────────────────────────────

// sanitizeJSONString escapes unescaped control characters (\n \r \t) that
// appear inside JSON string values, making otherwise-invalid JSON parseable.
func sanitizeJSONString(s string) string {
	var sb strings.Builder
	sb.Grow(len(s) + 64)
	inString := false
	escaped := false

	for _, r := range s {
		if inString {
			if escaped {
				sb.WriteRune(r)
				escaped = false
				continue
			}
			if r == '\\' {
				escaped = true
				sb.WriteRune(r)
				continue
			}
			if r == '"' {
				inString = false
				sb.WriteRune(r)
				continue
			}
			switch r {
			case '\n':
				sb.WriteString(`\n`)
			case '\r':
				sb.WriteString(`\r`)
			case '\t':
				sb.WriteString(`\t`)
			default:
				sb.WriteRune(r)
			}
		} else {
			if r == '"' {
				inString = true
			}
			sb.WriteRune(r)
		}
	}
	return sb.String()
}

// cleanJSONFences strips surrounding Markdown code fences from s.
func cleanJSONFences(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	return strings.TrimSpace(s)
}

// chunkStringByRunes splits s into rune-aware chunks of at most chunkSize runes.
func chunkStringByRunes(s string, chunkSize int) []string {
	if s == "" {
		return nil
	}
	runes := []rune(s)
	var chunks []string
	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[i:end]))
	}
	return chunks
}

// ─────────────────────────────────────────────
// Tool-call JSON parsing
// ─────────────────────────────────────────────

// nameRe / argsRe are used as regex fallbacks when JSON.Unmarshal fails.
var (
	nameRe  = regexp.MustCompile(`"name"\s*:\s*"([^"]+)"`)
	argsRe  = regexp.MustCompile(`(?s)"(?:arguments|input|parameters)"\s*:\s*(\{.*?\}|\[.*?\])`)
	fenceRe = regexp.MustCompile("(?s)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")
)

// parseToolJSON attempts to extract (name, args, ok) from a raw JSON string
// that is expected to represent a tool-call object:
//
//	{ "name": "...", "arguments"|"input"|"parameters": {...} }
//
// Falls back to regex extraction when JSON.Unmarshal fails (e.g. unescaped
// newlines in command strings).
func parseToolJSON(raw string) (string, interface{}, bool) {
	raw = cleanJSONFences(raw)
	sanitized := sanitizeJSONString(raw)

	// Fast path: standard JSON decode
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(sanitized), &parsed); err == nil {
		if name, ok := parsed["name"].(string); ok && name != "" {
			for _, key := range []string{"arguments", "input", "parameters"} {
				if v, ok := parsed[key]; ok {
					return name, v, true
				}
			}
			return name, map[string]interface{}{}, true
		}
	}

	// Slow path: regex fallback for malformed JSON (e.g. raw newlines in bash commands)
	nameMatch := nameRe.FindStringSubmatch(sanitized)
	if len(nameMatch) < 2 || nameMatch[1] == "" {
		return "", nil, false
	}
	name := nameMatch[1]
	argsStr := "{}"
	if argsMatch := argsRe.FindStringSubmatch(sanitized); len(argsMatch) >= 2 {
		argsStr = argsMatch[1]
	}
	logger.Warnf("[Spoof] Regex fallback for malformed JSON: tool=%s args=%.80s", name, argsStr)
	return name, argsStr, true
}

// findToolJSONBlock scans s for the first JSON object that looks like a tool-call
// (has both "name" and one of "arguments"/"input"/"parameters") and returns
// [startIdx, endIdx] or nil if not found.
func findToolJSONBlock(s string) []int {
	n := len(s)
	for i := 0; i < n; i++ {
		if s[i] != '{' {
			continue
		}
		depth, inString, escape := 0, false, false
		for j := i; j < n; j++ {
			c := s[j]
			if escape {
				escape = false
				continue
			}
			if c == '\\' && inString {
				escape = true
				continue
			}
			if c == '"' {
				inString = !inString
				continue
			}
			if inString {
				continue
			}
			if c == '{' {
				depth++
			} else if c == '}' {
				depth--
				if depth == 0 {
					candidate := s[i : j+1]
					if strings.Contains(candidate, `"name"`) &&
						(strings.Contains(candidate, `"arguments"`) ||
							strings.Contains(candidate, `"input"`) ||
							strings.Contains(candidate, `"parameters"`)) {
						return []int{i, j + 1}
					}
					break
				}
			}
		}
	}
	return nil
}

// ─────────────────────────────────────────────
// Multi-format tool-call detection
// ─────────────────────────────────────────────

// toolCall holds one extracted tool invocation.
type toolCall struct {
	Name string
	Args string // JSON string
}



// extractAllToolCalls finds every tool call embedded in text and returns them
// in order along with any plain text that precedes the first call.
func extractAllToolCalls(text string, requestedTools []AnthropicTool) (calls []toolCall, prefixText string) {
	prefixText = text
	working := text
	firstPrefix := true

	for {
		call, pre, rest, found := extractOneToolCall(working, requestedTools)
		if !found {
			break
		}
		if firstPrefix {
			prefixText = pre
			firstPrefix = false
		}
		calls = append(calls, call)
		if rest == "" {
			break
		}
		working = rest
	}
	return
}

// extractOneToolCall finds the first tool call in text and returns:
//
//	call       – the extracted tool
//	prefixText – text before the tool call
//	remainder  – text after the tool call (for chaining)
//	found      – whether a tool call was detected
func extractOneToolCall(text string, requestedTools []AnthropicTool) (call toolCall, prefixText, remainder string, found bool) {
	mkArgs := func(args interface{}) string {
		if args == nil {
			return "{}"
		}
		switch v := args.(type) {
		case string:
			if v == "" || v == "null" {
				return "{}"
			}
			return v
		default:
			b, _ := json.Marshal(args)
			return string(b)
		}
	}

	// 1. Dynamic XML tag pairs based on requested tools
	tagPairs := [][2]string{
		{"<tools>", "</tools>"},
		{"<tool_call>", "</tool_call>"},
		{"<tool>", "</tool>"},
	}
	for _, t := range requestedTools {
		tagPairs = append(tagPairs, [2]string{fmt.Sprintf("<%s>", t.Name), fmt.Sprintf("</%s>", t.Name)})
		if t.Name == "Bash" {
			tagPairs = append(tagPairs, [2]string{"<run_command>", "</run_command>"})
		}
	}

	for _, pair := range tagPairs {
		startTag, endTag := pair[0], pair[1]
		si := strings.Index(text, startTag)
		if si < 0 {
			continue
		}
		ei := strings.Index(text, endTag)
		var raw, after string
		if ei > si {
			raw = strings.TrimSpace(text[si+len(startTag) : ei])
			after = text[ei+len(endTag):]
		} else {
			raw = strings.TrimSpace(text[si+len(startTag):])
			after = ""
		}
		name, args, ok := parseToolJSON(raw)
		if !ok {
			if m := nameRe.FindStringSubmatch(raw); len(m) >= 2 {
				// raw contains JSON with a name field — extract it
				name = m[1]
				ok = true
				args = raw
			} else {
				// raw is plain text — derive tool name from tag and map via schema
				toolName := strings.Trim(startTag, "<>")
				if toolName == "run_command" {
					toolName = "Bash"
				}
				for _, t := range requestedTools {
					if t.Name == toolName {
						name = t.Name
						ok = true
						args = mapRawTextToSchema(raw, t.InputSchema)
						break
					}
				}
			}
		}
		if ok && name != "" {
			return toolCall{Name: name, Args: mkArgs(args)}, text[:si], after, true
		}
	}

	// 2. Special LLM tokens: <|message|>...<|call|>
	if mi := strings.Index(text, "<|message|>"); mi >= 0 {
		raw := text[mi+len("<|message|>"):]
		after := ""
		if ci := strings.Index(raw, "<|call|>"); ci >= 0 {
			after = raw[ci+len("<|call|>"):]
			raw = raw[:ci]
		}
		if name, args, ok := parseToolJSON(raw); ok && name != "" {
			prefix := text[:mi]
			if si := strings.LastIndex(prefix, "<|start|>"); si >= 0 {
				prefix = prefix[:si]
			}
			return toolCall{Name: name, Args: mkArgs(args)}, prefix, after, true
		}
	}

	// 2.5. Paired <tool_name> + <tool_arguments> tags (common hallucination format)
	if ni := strings.Index(text, "<tool_name>"); ni >= 0 {
		niEnd := strings.Index(text, "</tool_name>")
		if niEnd > ni {
			toolName := strings.TrimSpace(text[ni+len("<tool_name>") : niEnd])
			rest := text[niEnd+len("</tool_name>"):]
			rawArgs := "{}"
			after := rest
			if ai := strings.Index(rest, "<tool_arguments>"); ai >= 0 {
				aiEnd := strings.Index(rest, "</tool_arguments>")
				if aiEnd > ai {
					rawArgs = strings.TrimSpace(rest[ai+len("<tool_arguments>") : aiEnd])
					after = rest[aiEnd+len("</tool_arguments>"):]
				}
			}
			if toolName != "" {
				// Map args through the tool's schema if raw text isn't JSON
				for _, t := range requestedTools {
					if t.Name == toolName {
						rawArgs = mapRawTextToSchema(rawArgs, t.InputSchema)
						break
					}
				}
				return toolCall{Name: toolName, Args: mkArgs(rawArgs)}, text[:ni], after, true
			}
		}
	}

	// 3. Markdown fenced JSON
	for _, m := range fenceRe.FindAllStringSubmatchIndex(text, -1) {
		raw := text[m[2]:m[3]]
		if name, args, ok := parseToolJSON(raw); ok && name != "" {
			return toolCall{Name: name, Args: mkArgs(args)}, text[:m[0]], text[m[1]:], true
		}
	}

	// 4. Raw JSON block
	if loc := findToolJSONBlock(text); loc != nil {
		raw := text[loc[0]:loc[1]]
		if name, args, ok := parseToolJSON(raw); ok && name != "" {
			return toolCall{Name: name, Args: mkArgs(args)}, text[:loc[0]], text[loc[1]:], true
		}
	}

	return toolCall{}, text, "", false
}

// ─────────────────────────────────────────────
// Spoofed Anthropic SSE emission
// ─────────────────────────────────────────────

// emitSpoofedAnthropicStream converts a plain-text model response (which may
// contain one or more tool-calls in any supported format) into a well-formed
// Anthropic SSE stream and writes it to w.
func emitSpoofedAnthropicStream(w http.ResponseWriter, text string, requestedTools []AnthropicTool) {
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	calls, prefixText := extractAllToolCalls(text, requestedTools)
	hasTool := len(calls) > 0
	if hasTool {
		for _, c := range calls {
			logger.Infof("[Spoof-Stream] Tool call: %s args=%.150s", c.Name, c.Args)
		}
	}

	msgID := fmt.Sprintf("msg_spoof_%d", time.Now().UnixNano())

	// 1. message_start
	fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3-7-sonnet-20250219\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n", msgID)
	flush()

	blockIdx := 0

	// 2. Text block (prefix text or plain response with no tool)
	textToStream := strings.TrimSpace(prefixText)
	if textToStream != "" || !hasTool {
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n", blockIdx)
		for _, chunk := range chunkStringByRunes(textToStream, 40) {
			chunkBytes, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n", blockIdx, string(chunkBytes))
			flush()
		}
		fmt.Fprintf(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", blockIdx)
		flush()
		blockIdx++
	}

	// 3. Tool-use blocks (one per detected call)
	for _, tc := range calls {
		toolID := fmt.Sprintf("toolu_spoof_%d", time.Now().UnixNano())
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q,\"input\":{}}}\n\n", blockIdx, toolID, tc.Name)
		for _, chunk := range chunkStringByRunes(tc.Args, 40) {
			chunkBytes, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\n", blockIdx, string(chunkBytes))
			flush()
		}
		fmt.Fprintf(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", blockIdx)
		flush()
		blockIdx++
	}

	// 4. message_delta + message_stop
	stopReason := "end_turn"
	if hasTool {
		stopReason = "tool_use"
	}
	fmt.Fprintf(w, "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":%q,\"stop_sequence\":null},\"usage\":{\"output_tokens\":0}}\n\n", stopReason)
	fmt.Fprintf(w, "event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n")
	flush()
}

// writeSseBody is a helper used by forwardRequest when it needs to emit a
// spoofed SSE body: sets headers, writes 200, then calls emitSpoofedAnthropicStream.
func writeSseBody(w http.ResponseWriter, fullText string, requestedTools []AnthropicTool) {
	setSseHeaders(w)
	w.WriteHeader(http.StatusOK)
	emitSpoofedAnthropicStream(w, fullText, requestedTools)
}

// writeJsonBody is used when the client requested a JSON response instead of a stream
func writeJsonBody(w http.ResponseWriter, fullText string, requestedTools []AnthropicTool) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)

	calls, prefixText := extractAllToolCalls(fullText, requestedTools)
	hasTool := len(calls) > 0

	content := []map[string]interface{}{}
	textToStream := strings.TrimSpace(prefixText)
	if textToStream != "" || !hasTool {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": textToStream,
		})
	}
	for _, tc := range calls {
		toolID := fmt.Sprintf("toolu_spoof_%d", time.Now().UnixNano())
		
		var input map[string]interface{}
		if err := json.Unmarshal([]byte(tc.Args), &input); err != nil {
			input = map[string]interface{}{}
		}
		
		content = append(content, map[string]interface{}{
			"type":  "tool_use",
			"id":    toolID,
			"name":  tc.Name,
			"input": input,
		})
	}

	stopReason := "end_turn"
	if hasTool {
		stopReason = "tool_use"
	}
	
	msgID := fmt.Sprintf("msg_spoof_%d", time.Now().UnixNano())

	resp := map[string]interface{}{
		"id":            msgID,
		"type":          "message",
		"role":          "assistant",
		"model":         "claude-3-7-sonnet-20250219",
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":  0,
			"output_tokens": 0,
		},
		"content": content,
	}
	
	json.NewEncoder(w).Encode(resp)
}
