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

	for i := 0; i < len(s); i++ {
		c := s[i]
		if inString {
			if escaped {
				sb.WriteByte(c)
				escaped = false
				continue
			}
			if c == '\\' {
				escaped = true
				sb.WriteByte(c)
				continue
			}
			if c == '"' {
				inString = false
				sb.WriteByte(c)
				continue
			}
			switch c {
			case '\n':
				sb.WriteString(`\n`)
			case '\r':
				sb.WriteString(`\r`)
			case '\t':
				sb.WriteString(`\t`)
			default:
				sb.WriteByte(c)
			}
		} else {
			if c == '"' {
				inString = true
			}
			sb.WriteByte(c)
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

// parseToolJSON attempts to extract (name, args, ok) from a raw JSON string
// that is expected to represent a tool-call object:
//
//	{ "name": "...", "arguments"|"input"|"parameters": {...} }
func parseToolJSON(raw string) (string, interface{}, bool) {
	raw = cleanJSONFences(raw)
	sanitized := sanitizeJSONString(raw)
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
	return "", nil, false
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

// parseToolFromText inspects text for a tool-call in any of the supported formats
// and returns (textBeforeTool, toolName, argsJSON, found).
//
// Detection order (first match wins):
//  1. XML tags: <tools>, <tool_call>, <tool>
//  2. Special LLM tokens: <|message|>...<|call|>
//  3. Markdown fenced JSON: ```json { "name": ... } ```
//  4. Raw JSON object anywhere in the text
func parseToolFromText(text string) (textToStream, toolName, argsJSON string, hasTool bool) {
	textToStream = text
	argsJSON = "{}"

	setTool := func(name string, args interface{}, prefix string) bool {
		if name == "" {
			return false
		}
		toolName = name
		hasTool = true
		textToStream = strings.TrimSpace(prefix)
		if args != nil {
			if s, ok := args.(string); ok {
				argsJSON = s
			} else {
				b, _ := json.Marshal(args)
				argsJSON = string(b)
			}
		}
		if argsJSON == "" || argsJSON == "null" {
			argsJSON = "{}"
		}
		return true
	}

	// 1. XML tag pairs
	for _, pair := range [][2]string{
		{"<tools>", "</tools>"},
		{"<tool_call>", "</tool_call>"},
		{"<tool>", "</tool>"},
	} {
		startTag, endTag := pair[0], pair[1]
		si := strings.Index(text, startTag)
		if si < 0 {
			continue
		}
		ei := strings.Index(text, endTag)
		var raw string
		if ei > si {
			raw = strings.TrimSpace(text[si+len(startTag) : ei])
		} else {
			raw = strings.TrimSpace(text[si+len(startTag):])
		}
		if n, a, ok := parseToolJSON(raw); ok && setTool(n, a, text[:si]) {
			return
		}
	}

	// 2. Special LLM tokens: <|message|>...<|call|>
	if mi := strings.Index(text, "<|message|>"); mi >= 0 {
		raw := text[mi+len("<|message|>"):]
		if ci := strings.Index(raw, "<|call|>"); ci >= 0 {
			raw = raw[:ci]
		}
		if n, a, ok := parseToolJSON(raw); ok {
			prefix := text[:mi]
			if si := strings.LastIndex(prefix, "<|start|>"); si >= 0 {
				prefix = prefix[:si]
			}
			if setTool(n, a, prefix) {
				return
			}
		}
	}

	// 3. Markdown fenced JSON
	fenceRe := regexp.MustCompile("(?s)```(?:json)?\\s*(\\{[\\s\\S]*?\\})\\s*```")
	for _, m := range fenceRe.FindAllStringSubmatchIndex(text, -1) {
		raw := text[m[2]:m[3]]
		if n, a, ok := parseToolJSON(raw); ok && setTool(n, a, text[:m[0]]) {
			return
		}
	}

	// 4. Raw JSON block
	if loc := findToolJSONBlock(text); loc != nil {
		raw := text[loc[0]:loc[1]]
		if n, a, ok := parseToolJSON(raw); ok && setTool(n, a, text[:loc[0]]) {
			return
		}
	}

	return
}

// ─────────────────────────────────────────────
// Spoofed Anthropic SSE emission
// ─────────────────────────────────────────────

// emitSpoofedAnthropicStream converts a plain-text model response (which may
// contain a tool-call in any supported format) into a well-formed Anthropic SSE
// stream and writes it to w.
func emitSpoofedAnthropicStream(w http.ResponseWriter, text string) {
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	textToStream, toolName, argsJSON, hasTool := parseToolFromText(text)
	if hasTool {
		logger.Infof("[Spoof-Stream] Intercepted tool call: %s args=%.150s", toolName, argsJSON)
	}

	msgID := fmt.Sprintf("msg_spoof_%d", time.Now().UnixNano())

	// 1. message_start
	fmt.Fprintf(w, "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":%q,\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3-7-sonnet-20250219\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n", msgID)
	flush()

	blockIdx := 0

	// 2. Text block (if any prefix text, or if no tool)
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

	// 3. Tool-use block (if tool detected)
	if hasTool {
		toolID := fmt.Sprintf("toolu_spoof_%d", time.Now().UnixNano())
		fmt.Fprintf(w, "event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"tool_use\",\"id\":%q,\"name\":%q,\"input\":{}}}\n\n", blockIdx, toolID, toolName)
		for _, chunk := range chunkStringByRunes(argsJSON, 40) {
			chunkBytes, _ := json.Marshal(chunk)
			fmt.Fprintf(w, "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\n", blockIdx, string(chunkBytes))
			flush()
		}
		fmt.Fprintf(w, "event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", blockIdx)
		flush()
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
func writeSseBody(w http.ResponseWriter, fullText string) {
	setSseHeaders(w)
	w.WriteHeader(http.StatusOK)
	emitSpoofedAnthropicStream(w, fullText)
}
