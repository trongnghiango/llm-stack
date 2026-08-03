package main

import (
	"bytes"
	"context"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"

	"claude-proxy/internal/config"
	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"
	"claude-proxy/internal/router"
	"claude-proxy/internal/utils"
)

// Message structures
type MessageBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Id        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseId string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

type AnthropicRequest struct {
	Model      string          `json:"model"`
	System     json.RawMessage `json:"system,omitempty"`
	Messages   []Message       `json:"messages,omitempty"`
	Tools      json.RawMessage `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	Stream     *bool           `json:"stream,omitempty"`
}

func convertMessagesForSpoofing(messages []Message) []Message {
	converted := make([]Message, 0, len(messages))
	for _, msg := range messages {
		// Try string content
		var strContent string
		if err := json.Unmarshal(msg.Content, &strContent); err == nil {
			converted = append(converted, msg)
			continue
		}

		// Try []MessageBlock
		var blocks []MessageBlock
		if err := json.Unmarshal(msg.Content, &blocks); err == nil {
			var sb strings.Builder
			for _, b := range blocks {
				switch b.Type {
				case "text":
					if b.Text != "" {
						if sb.Len() > 0 {
							sb.WriteString("\n")
						}
						sb.WriteString(b.Text)
					}
				case "tool_use":
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					argsStr := "{}"
					if len(b.Input) > 0 {
						argsStr = string(b.Input)
					}
					sb.WriteString(fmt.Sprintf("<tools>{\"name\": %q, \"arguments\": %s}</tools>", b.Name, argsStr))
				case "tool_result":
					if sb.Len() > 0 {
						sb.WriteString("\n")
					}
					var resultText string
					if len(b.Content) > 0 {
						if err := json.Unmarshal(b.Content, &resultText); err != nil {
							var innerBlocks []MessageBlock
							if err := json.Unmarshal(b.Content, &innerBlocks); err == nil {
								var innerSb strings.Builder
								for _, ib := range innerBlocks {
									if ib.Type == "text" {
										innerSb.WriteString(ib.Text + "\n")
									}
								}
								resultText = strings.TrimSpace(innerSb.String())
							} else {
								resultText = string(b.Content)
							}
						}
					}
					sb.WriteString(fmt.Sprintf("[Tool Result]:\n%s", resultText))
				}
			}
			newBytes, _ := json.Marshal(sb.String())
			converted = append(converted, Message{
				Role:    msg.Role,
				Content: newBytes,
			})
			continue
		}

		converted = append(converted, msg)
	}
	return converted
}

func handleProxy(w http.ResponseWriter, r *http.Request) {
	defer func() {
		if recoveryErr := recover(); recoveryErr != nil {
			logger.Errorf("[LỖI HỆ THỐNG CRASH] Phát hiện sự cố nghiêm trọng được khôi phục: %v", recoveryErr)
			http.Error(w, "Internal Proxy Server Recovery Error", http.StatusInternalServerError)
		}
	}()

	// Serve metrics endpoint directly (bypasses mux for direct handler testing).
	if r.URL.Path == "/debug/metrics" {
		promhttp.Handler().ServeHTTP(w, r)
		return
	}

	maxBytes := int64(5 << 20) // 5 MiB default
	if v := os.Getenv("MAX_PAYLOAD_BYTES"); v != "" {
		if n, err := strconv.ParseInt(v, 10, 64); err == nil && n > 0 {
			maxBytes = n
		}
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)

	bodyBytes, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			metrics.PayloadTooLargeTotal.Inc()
			logger.Errorf("[Security] Request payload too large (limit %d bytes)", maxBytes)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusRequestEntityTooLarge)
			w.Write([]byte(`{"error":"request too large"}`))
			return
		}
		http.Error(w, "Lỗi đọc luồng dữ liệu yêu cầu", http.StatusBadRequest)
		return
	}
	defer r.Body.Close()

	if len(bodyBytes) == 0 {
		forwardRequest(w, r, bodyBytes, false, false)
		return
	}

	var rawData map[string]json.RawMessage
	if err := json.Unmarshal(bodyBytes, &rawData); err != nil {
		if r.URL.Path == "/v1/messages" {
			logger.Errorf("[Chẩn đoán] Gói tin gửi tới /v1/messages sai cấu trúc JSON: %v\n", err)
		}
		forwardRequest(w, r, bodyBytes, false, false)
		return
	}

	var originalModel string
	if modelBytes, exists := rawData["model"]; exists {
		if err := json.Unmarshal(modelBytes, &originalModel); err != nil {
			logger.Errorf("[Router] Failed to parse model field: %v", err)
			forwardRequest(w, r, bodyBytes, false, false)
			return
		}
	} else {
		forwardRequest(w, r, bodyBytes, false, false)
		return
	}

	if strings.HasPrefix(originalModel, "swe.") {
		st := router.GetState()
		if _, hasRule := st.SemanticRuleMap[originalModel]; !hasRule {
			logger.Errorf("[Security] Unrecognized swe model: %s", originalModel)
			metrics.InvalidModelTotal.Inc()
		}
	}

	// Extract prompt text for routing decisions
	var textReq AnthropicRequest
	if err := json.Unmarshal(bodyBytes, &textReq); err != nil {
		logger.Debugf("[Router] Unable to parse request for prompt text: %v", err)
	}
	var textBuilder strings.Builder
	for _, msg := range textReq.Messages {
		if msg.Role != "user" {
			continue
		}
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil {
			textBuilder.WriteString(" " + s)
			continue
		}
		var blocks []MessageBlock
		if err := json.Unmarshal(msg.Content, &blocks); err == nil {
			for _, b := range blocks {
				if b.Type == "text" {
					textBuilder.WriteString(" " + b.Text)
				}
			}
		}
	}
	promptText := textBuilder.String()

	// Resolve target model via router implementation (atomic state snapshot)
	st := router.GetState()
	targetModel := st.ModelRouter.Resolve(originalModel, promptText)

	// Check if we need to spoof tools
	modelSettings, hasSettings := st.Config.ModelSettings[targetModel]
	isSpoofingTools := hasSettings && modelSettings.SpoofToolsXML

	if isSpoofingTools {
		// Convert message history (tool_use -> <tools> XML, tool_result -> [Tool Result])
		convertedMsgs := convertMessagesForSpoofing(textReq.Messages)
		msgsBytes, _ := json.Marshal(convertedMsgs)
		rawData["messages"] = msgsBytes

		if len(textReq.Tools) > 0 {
			logger.Infof("[Router] Spoofing Tools -> XML for targetModel: %s", targetModel)
			toolSystemInstruction := "[CRITICAL SYSTEM INSTRUCTION FOR TOOL USE]\n" +
				"You are an AI assistant equipped with specific tools.\n" +
				"When you need to call a tool, output ONLY a single XML block formatted exactly like this:\n" +
				"<tools>{\"name\": \"exact_tool_name\", \"arguments\": {\"param\": \"value\"}}</tools>\n" +
				"Do not write any explanation, markdown, or text before or after the XML block when calling a tool.\n" +
				"When you receive a [Tool Result] in the conversation history, the tool has already been executed. Use the result to continue or finish answering the user in plain text without repeating the tool call."
			
			// Read existing system prompt if any
			var systemStr string
			if len(textReq.System) > 0 {
				if err := json.Unmarshal(textReq.System, &systemStr); err != nil {
					var systemBlocks []MessageBlock
					if err := json.Unmarshal(textReq.System, &systemBlocks); err == nil {
						var sb strings.Builder
						for _, block := range systemBlocks {
							if block.Type == "text" {
								sb.WriteString(block.Text + "\n")
							}
						}
						systemStr = sb.String()
					}
				}
			}

			if systemStr != "" {
				systemStr += "\n\n" + toolSystemInstruction
			} else {
				systemStr = toolSystemInstruction
			}

			// Inject tool schemas into system prompt
			toolsBytes, _ := json.Marshal(textReq.Tools)
			systemStr += "\n\nAVAILABLE TOOLS:\n" + string(toolsBytes)

			// Update System in rawData
			sysBytes, _ := json.Marshal(systemStr)
			rawData["system"] = sysBytes

			// Remove native tools to avoid confusing upstream
			delete(rawData, "tools")
			delete(rawData, "tool_choice")
		}
	}

	// Overwrite model field preserving other payload data
	rawData["model"] = json.RawMessage(fmt.Sprintf("%q", targetModel))
	modifiedBody, err := json.Marshal(rawData)
	if err != nil {
		http.Error(w, "Lỗi tái tạo JSON payload", http.StatusInternalServerError)
		return
	}
	reqID := r.Header.Get("X-Request-ID")
	logger.LogPayload(reqID, modifiedBody)
	
	isFakeStream := hasSettings && modelSettings.FakeStream && textReq.Stream != nil && *textReq.Stream
	forwardRequest(w, r, modifiedBody, isSpoofingTools, isFakeStream)
}

// Headers to drop before forwarding
var droppedHeaders = map[string]bool{
	"content-length":    true,
	"content-encoding":  true,
	"transfer-encoding": true,
}

func copySafeHeaders(dst, src http.Header) {
	for name, values := range src {
		if droppedHeaders[strings.ToLower(name)] {
			continue
		}
		for _, v := range values {
			dst.Add(name, v)
		}
	}
}

func forwardRequest(w http.ResponseWriter, r *http.Request, payload []byte, isSpoofingTools bool, isFakeStream bool) {
	s := router.GetState()
	upstreamURL, err := url.Parse(s.Config.UpstreamURL)
	if err != nil {
		logger.Errorf("[Lỗi Upstream] Đường dẫn upstream không hợp lệ: %v", err)
		http.Error(w, "Invalid Upstream Endpoint Configuration", http.StatusBadGateway)
		return
	}
	upstreamURL.Path = r.URL.Path

	upstreamReq, err := http.NewRequestWithContext(r.Context(), "POST", upstreamURL.String(), bytes.NewBuffer(payload))
	if err != nil {
		http.Error(w, "Fail to initialize upstream tunnel", http.StatusInternalServerError)
		return
	}

	copySafeHeaders(upstreamReq.Header, r.Header)

	// Set correct Content-Length for modified payload
	upstreamReq.ContentLength = int64(len(payload))
	upstreamReq.Header.Set("Content-Length", fmt.Sprintf("%d", len(payload)))

	// Inject API key headers if configured
	if s.Config.UpstreamAPIKey != "" {
		upstreamReq.Header.Del("Authorization")
		upstreamReq.Header.Del("X-API-Key")
		upstreamReq.Header.Del("X-Api-Key")
		upstreamReq.Header.Del("x-api-key")
		upstreamReq.Header.Set("x-api-key", s.Config.UpstreamAPIKey)
		upstreamReq.Header.Set("Authorization", "Bearer "+s.Config.UpstreamAPIKey)
	}

	upstreamReq.Header.Set("Anthropic-Version", "2023-06-01")
	upstreamReq.Header.Set("Content-Type", "application/json")
	
	// Forward trace ID to upstream for end-to-end correlation.
	if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
		upstreamReq.Header.Set("X-Request-ID", reqID)
	}

	resp, err := utils.HTTPClient.Do(upstreamReq)
	if err != nil {
		logger.Errorf("[Lỗi kết nối] Không thể kết nối tới Local 9router (%s): %v", s.Config.UpstreamURL, err)
		http.Error(w, "Unable to establish connection to Local 9router", http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Forward response headers, omit streaming‑specific ones
	isStream := false
	for key, values := range resp.Header {
		low := strings.ToLower(key)
		if low == "content-length" || low == "content-encoding" || low == "transfer-encoding" || low == "connection" || low == "keep-alive" {
			continue
		}
		if low == "content-type" {
			for _, v := range values {
				if strings.HasPrefix(v, "text/event-stream") {
					isStream = true
				}
			}
		}
		for _, v := range values {
			w.Header().Add(key, v)
		}
	}

	logger.Infof("[Response] isFakeStream=%v isStream=%v isSpoofingTools=%v status=%d contentType=%s", isFakeStream, isStream, isSpoofingTools, resp.StatusCode, resp.Header.Get("Content-Type"))

	// Pass through upstream error responses directly
	if resp.StatusCode >= 400 {
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	// Case 1: Tool Spoofing (both streaming and non-streaming responses)
	if isSpoofingTools {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Errorf("[Spoof-Stream] Failed to read response body: %v", err)
			http.Error(w, "Failed to read upstream response", http.StatusBadGateway)
			return
		}

		var fullText string
		if isStream {
			fullText = extractTextFromSSE(string(bodyBytes))
		} else {
			fullText = extractTextFromJSON(bodyBytes)
		}

		logger.Infof("[Spoof-Stream] Extracted text (%d chars): %.200s", len(fullText), fullText)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		emitSpoofedAnthropicStream(w, fullText)
		return
	}

	// Case 2: Fake Stream (upstream is non-streaming JSON, client requested stream)
	if isFakeStream && !isStream {
		bodyBytes, err := io.ReadAll(resp.Body)
		if err != nil {
			logger.Errorf("[FakeStream] Failed to read response body: %v", err)
			http.Error(w, "Failed to read upstream response", http.StatusBadGateway)
			return
		}
		fullText := extractTextFromJSON(bodyBytes)

		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(http.StatusOK)

		emitSpoofedAnthropicStream(w, fullText)
		return
	}

	// Case 3: Transparent Stream Forwarding
	if isStream {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.Header().Set("X-Accel-Buffering", "no")
		w.WriteHeader(resp.StatusCode)
		forwardStreamWithValidation(w, resp)
		return
	}

	// Case 4: Transparent Non-Streaming Response
	logger.Infof("[Response] -> raw copy path")
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

func extractTextFromSSE(bodyStr string) string {
	var fullText strings.Builder
	for _, line := range strings.Split(bodyStr, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		jsonStr := strings.TrimSpace(line[5:])
		if jsonStr == "" || jsonStr == "[DONE]" {
			continue
		}
		var evt map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &evt); err != nil {
			continue
		}
		// Anthropic content_block_delta
		if evtType, _ := evt["type"].(string); evtType == "content_block_delta" {
			if delta, ok := evt["delta"].(map[string]interface{}); ok {
				if txt, ok := delta["text"].(string); ok {
					fullText.WriteString(txt)
				}
			}
		}
		// OpenAI delta.content
		if choices, ok := evt["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if delta, ok := choice["delta"].(map[string]interface{}); ok {
					if content, ok := delta["content"].(string); ok {
						fullText.WriteString(content)
					}
				}
			}
		}
	}
	return fullText.String()
}

func extractTextFromJSON(bodyBytes []byte) string {
	var generic map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &generic); err != nil {
		return string(bodyBytes)
	}
	// Anthropic content[0].text
	if content, ok := generic["content"].([]interface{}); ok && len(content) > 0 {
		var sb strings.Builder
		for _, item := range content {
			if block, ok := item.(map[string]interface{}); ok {
				if txt, ok := block["text"].(string); ok {
					sb.WriteString(txt)
				}
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}
	// OpenAI choices[0].message.content
	if choices, ok := generic["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if txt, ok := msg["content"].(string); ok {
					return txt
				}
			}
		}
	}
	return string(bodyBytes)
}

func emitSpoofedAnthropicStream(w http.ResponseWriter, text string) {
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	var textToStream = text
	var toolRawJSON string

	// Look for <tools>...</tools> or <tool_call>...</tool_call>
	startTag := "<tools>"
	endTag := "</tools>"
	startIdx := strings.Index(text, startTag)
	endIdx := strings.Index(text, endTag)

	if startIdx < 0 {
		startTag = "<tool_call>"
		endTag = "</tool_call>"
		startIdx = strings.Index(text, startTag)
		endIdx = strings.Index(text, endTag)
	}

	if startIdx >= 0 && endIdx > startIdx {
		textToStream = strings.TrimSpace(text[:startIdx])
		toolRawJSON = strings.TrimSpace(text[startIdx+len(startTag) : endIdx])
	} else if startIdx >= 0 {
		textToStream = strings.TrimSpace(text[:startIdx])
		toolRawJSON = strings.TrimSpace(text[startIdx+len(startTag):])
	}

	// Clean code fence blocks if model wrapped JSON in ```json ... ```
	toolRawJSON = strings.TrimPrefix(toolRawJSON, "```json")
	toolRawJSON = strings.TrimPrefix(toolRawJSON, "```")
	toolRawJSON = strings.TrimSuffix(toolRawJSON, "```")
	toolRawJSON = strings.TrimSpace(toolRawJSON)

	var hasTool bool
	var toolName string
	var argsJSON string = "{}"

	if toolRawJSON != "" {
		var parsedTool map[string]interface{}
		if err := json.Unmarshal([]byte(toolRawJSON), &parsedTool); err == nil {
			if n, ok := parsedTool["name"].(string); ok && n != "" {
				toolName = n
				hasTool = true
				if args, ok := parsedTool["arguments"]; ok {
					if argsStr, ok := args.(string); ok {
						argsJSON = argsStr
					} else {
						b, _ := json.Marshal(args)
						argsJSON = string(b)
					}
				} else if input, ok := parsedTool["input"]; ok {
					if inputStr, ok := input.(string); ok {
						argsJSON = inputStr
					} else {
						b, _ := json.Marshal(input)
						argsJSON = string(b)
					}
				}
				logger.Infof("[Spoof-Stream] Intercepted tool: %s args=%.150s", toolName, argsJSON)
			}
		} else {
			logger.Warnf("[Spoof-Stream] Failed to parse tool JSON: %s err=%v", toolRawJSON[:min(len(toolRawJSON), 100)], err)
		}
	}

	msgID := fmt.Sprintf("msg_spoof_%d", time.Now().UnixNano())

	// 1. message_start
	w.Write([]byte(fmt.Sprintf("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"%s\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"claude-3-7-sonnet-20250219\",\"stop_reason\":null,\"stop_sequence\":null,\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n", msgID)))
	flush()

	blockIndex := 0

	// 2. Text block (if text exists before tool or if no tool)
	if textToStream != "" || !hasTool {
		w.Write([]byte(fmt.Sprintf("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n", blockIndex)))
		
		chunkSize := 40
		for i := 0; i < len(textToStream); i += chunkSize {
			end := i + chunkSize
			if end > len(textToStream) {
				end = len(textToStream)
			}
			chunk := textToStream[i:end]
			chunkBytes, _ := json.Marshal(chunk)
			w.Write([]byte(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"text_delta\",\"text\":%s}}\n\n", blockIndex, string(chunkBytes))))
			flush()
		}
		
		w.Write([]byte(fmt.Sprintf("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", blockIndex)))
		flush()
		blockIndex++
	}

	// 3. Tool block (if tool found)
	if hasTool {
		toolID := fmt.Sprintf("toolu_spoof_%d", time.Now().UnixNano())
		w.Write([]byte(fmt.Sprintf("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":%d,\"content_block\":{\"type\":\"tool_use\",\"id\":\"%s\",\"name\":\"%s\",\"input\":{}}}\n\n", blockIndex, toolID, toolName)))
		
		chunkSize := 40
		for i := 0; i < len(argsJSON); i += chunkSize {
			end := i + chunkSize
			if end > len(argsJSON) {
				end = len(argsJSON)
			}
			chunk := argsJSON[i:end]
			chunkBytes, _ := json.Marshal(chunk)
			w.Write([]byte(fmt.Sprintf("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":%d,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":%s}}\n\n", blockIndex, string(chunkBytes))))
			flush()
		}
		
		w.Write([]byte(fmt.Sprintf("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":%d}\n\n", blockIndex)))
		flush()
	}

	// 4. message_delta
	stopReason := "end_turn"
	if hasTool {
		stopReason = "tool_use"
	}
	w.Write([]byte(fmt.Sprintf("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"%s\",\"stop_sequence\":null},\"usage\":{\"output_tokens\":0}}\n\n", stopReason)))

	// 5. message_stop
	w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	flush()
}

func forwardStreamWithValidation(w http.ResponseWriter, resp *http.Response) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		_, _ = io.Copy(w, resp.Body)
		return
	}
	
	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	strictMode := os.Getenv("STRICT_SCHEMA_VALIDATION") == "true"

	for scanner.Scan() {
		line := scanner.Bytes()
		payloadToWrite := line
		var shouldForward = true

		if dIdx := bytes.Index(line, []byte("data:")); dIdx >= 0 {
			jsonPayload := bytes.TrimSpace(line[dIdx+5:])
			if len(jsonPayload) > 0 && string(jsonPayload) != "[DONE]" {
				if !json.Valid(jsonPayload) {
					metrics.SseValidationErrorsTotal.Inc()
					chunkTruncated := string(jsonPayload)
					if len(chunkTruncated) > 150 {
						chunkTruncated = chunkTruncated[:150] + "..."
					}
					
					if strictMode {
						logger.Errorf("[Stream Parse Error] STRICT MODE: JSON hỏng. Ngắt kết nối. Chunk: %s", chunkTruncated)
						shouldForward = false
						errEvent := "event: error\ndata: {\"error\": {\"type\": \"stream_parse_error\", \"message\": \"Strict mode terminated connection due to invalid schema structure from Upstream.\"}}\n\n"
						w.Write([]byte(errEvent))
						flusher.Flush()
						return
					} else {
						logger.Warnf("[Stream Parse] Bỏ qua JSON hỏng. Chunk: %s", chunkTruncated)
						shouldForward = false
						warnEvent := "event: ping\ndata: {\"warning\": \"malformed_json_skipped_gracefully\"}\n\n"
						w.Write([]byte(warnEvent))
					}
				}
			}
		}

		if shouldForward {
			w.Write(payloadToWrite)
			w.Write([]byte("\n"))
		}
		flusher.Flush()
	}
	
	if err := scanner.Err(); err != nil {
		if err != io.EOF && !errors.Is(err, context.Canceled) {
			logger.Errorf("[Lỗi đọc luồng] Kết thúc luồng đọc bất thường từ Upstream (Scanner): %v", err)
		}
	}
}

func main() {
	// Initialise logger - will be closed on exit.
	defer logger.CloseLogger()

	// Parse CLI flags (before config load so --config can override path).
	cfgPath := parseFlags()
	config.ConfigPath = cfgPath

	// Load configuration via JSONConfigLoader
	cp := &config.JSONConfigLoader{Path: cfgPath}
	cfg, err := cp.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "FATAL: Config load error: %v\n", err)
		log.Fatalf("[Init] Config load error: %v", err)
	}
	router.SetState(cfg)

	// Start payload log cleanup worker running every hour
	stopCleanup := logger.StartPayloadLogCleanupWorker(1 * time.Hour)
	defer close(stopCleanup)

	// Apply CLI flag overrides on top of loaded config.
	applyFlagOverrides()
	// Register handlers on dedicated mux.
	mux := http.NewServeMux()
	metrics.ExposeMetrics(mux)
	mux.HandleFunc("/health", HealthHandler)
	mux.HandleFunc("/readyz", ReadyzHandler)
	mux.HandleFunc("/debug/health", DebugHealthHandler)
	mux.HandleFunc("/", handleProxy)
	// Wrap mux with logging middleware.
	var handler http.Handler = mux
	handler = utils.NewLoggingMiddleware(handler)
	bindAddr := os.Getenv("BIND_ADDR")
	if bindAddr == "" {
		bindAddr = "127.0.0.1"
	}
	addr := fmt.Sprintf("%s:%d", bindAddr, router.GetState().Config.Port)
	logger.Infof("[Sẵn sàng] Khởi chạy Local Proxy thành công tại http://%s", addr)
	server := &http.Server{Addr: addr, Handler: handler}

	// Start server in background goroutine to allow main thread to block on shutdown
	go func() {
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			fmt.Fprintf(os.Stderr, "FATAL: ListenAndServe error: %v\n", err)
			log.Fatalf("[Thất bại] Không thể khởi chạy cổng %d: %v", router.GetState().Config.Port, err)
		}
	}()

	// Signal handling: shutdown (SIGINT/SIGTERM) and config reload (SIGHUP).
	quit := make(chan os.Signal, 1)
	reload := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	signal.Notify(reload, syscall.SIGHUP)

	var isShuttingDown atomic.Bool

	// SIGHUP reload goroutine
	go func() {
		for range reload {
			if isShuttingDown.Load() {
				logger.Infof("[Reload] Bỏ qua reload cấu hình vì server đang tắt")
				continue
			}
			router.ReloadConfig(config.ConfigPath)
		}
	}()

	// Block main goroutine waiting for shutdown signal
	sig := <-quit
	isShuttingDown.Store(true)
	logger.Infof("[Tắt máy] Nhận tín hiệu %v, đang tắt dần...", sig)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := server.Shutdown(ctx); err != nil {
		logger.Errorf("[Tắt máy] Lỗi khi tắt server: %v", err)
	}
	logger.Infof("[Tắt máy] Shutdown hoàn tất.")
}
