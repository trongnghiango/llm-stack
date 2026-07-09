package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

// ============================================================================
// MAIN CONTROLLER / PROXY HANDLER
// ============================================================================

// ProxyHandler chứa logic trung chuyển yêu cầu HTTP, chuyển đổi giao thức
// và gọi trực tiếp tới Cloudflare Workers AI API.
type ProxyHandler struct {
	sm     *SessionManager
	client *http.Client
}

// NewProxyHandler khởi tạo một ProxyHandler mới với timeout client mạng là 120s.
func NewProxyHandler(sm *SessionManager) *ProxyHandler {
	return &ProxyHandler{
		sm:     sm,
		client: &http.Client{Timeout: 120 * time.Second},
	}
}

// HandleChatCompletion xử lý luồng gọi OpenAI-compatible từ client.
func (h *ProxyHandler) HandleChatCompletion(c *gin.Context) {
	var req OpenAIRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[⚠️ Bind Error] OpenAI binding failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
		return
	}

	sessionID := req.User
	if sessionID == "" {
		sessionID = c.ClientIP() + "_" + req.Model
	}

	var resp *http.Response
	var err error
	var account CFAccount

	// Vòng lặp Failover khẩn cấp chuyển đổi tài khoản khi dính lỗi
	for i := 0; i < 3; i++ {
		var ok bool
		account, ok = h.sm.GetAccount(sessionID)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Tất cả tài khoản hệ thống đã đạt ngưỡng giới hạn an toàn!"})
			return
		}

		// Gọi chuyển tiếp đến Cloudflare
		resp, err = h.forwardToCloudflare(account, req.Model, req)

		if err != nil {
			h.sm.Penalize(account.AccountID, 5*time.Minute)
			h.sm.BreakSession(sessionID)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			h.sm.Penalize(account.AccountID, 12*time.Hour) // Phạt 24 tiếng nếu cạn kiệt Neurons ngày
			h.sm.BreakSession(sessionID)
			continue
		}
		break
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("[⚠️ Cloudflare Error Response] Status: %d, Body: %s", resp.StatusCode, string(bodyBytes))
		c.JSON(resp.StatusCode, gin.H{"error": fmt.Sprintf("Cloudflare API error %d: %s", resp.StatusCode, string(bodyBytes))})
		return
	}

	defer resp.Body.Close()

	stream := false
	if req.Stream != nil {
		stream = *req.Stream
	}

	if stream {
		h.handleStream(c, resp.Body, account.AccountID, req.Model)
	} else {
		h.handleStandard(c, resp.Body, account.AccountID, req.Model)
	}
}

// HandleAnthropicCompletion tiếp nhận và biên dịch luồng gọi Anthropic-compatible (từ Claude Code).
func (h *ProxyHandler) HandleAnthropicCompletion(c *gin.Context) {
	var req AnthropicRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		log.Printf("[⚠️ Bind Error] Anthropic binding failed: %v", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid request payload: " + err.Error()})
		return
	}

	// 1. Chuyển đổi tin nhắn hệ thống (System prompt) và các block tin nhắn
	var openAIMessages []interface{}
	var systemPrompt string
	if req.System != nil {
		switch s := req.System.(type) {
		case string:
			systemPrompt = s
		case []interface{}:
			var systemBlocks []string
			for _, block := range s {
				if blockMap, ok := block.(map[string]interface{}); ok {
					if blockMap["type"] == "text" {
						if text, ok := blockMap["text"].(string); ok {
							systemBlocks = append(systemBlocks, text)
						}
					}
				} else if blockStr, ok := block.(string); ok {
					systemBlocks = append(systemBlocks, blockStr)
				}
			}
			systemPrompt = strings.Join(systemBlocks, "")
		}
	}

	// Tự động thêm chỉ thị hệ thống để ép Qwen/DeepSeek gọi tool qua JSON thô chuẩn xác
	toolSystemInstruction := ""
	if req.Tools != nil {
		toolSystemInstruction = "\n\n[CRITICAL SYSTEM INSTRUCTION FOR TOOL USE]\nWhen you decide to call a tool, you MUST output a raw JSON block with 'name' and 'arguments' fields. Example:\n{\"name\": \"Read\", \"arguments\": {\"file_path\": \"/path/to/file\"}}\nDO NOT write any explanation, introduction, markdown blocks, or text before or after the JSON. Output only the raw JSON string so the parser can execute it immediately."
	}

	if systemPrompt != "" {
		openAIMessages = append(openAIMessages, map[string]string{
			"role":    "system",
			"content": systemPrompt + toolSystemInstruction,
		})
	} else if toolSystemInstruction != "" {
		openAIMessages = append(openAIMessages, map[string]string{
			"role":    "system",
			"content": "[CRITICAL SYSTEM INSTRUCTION FOR TOOL USE]\nWhen you decide to call a tool, you MUST output a raw JSON block with 'name' and 'arguments' fields. Example:\n{\"name\": \"Read\", \"arguments\": {\"file_path\": \"/path/to/file\"}}\nDO NOT write any explanation, introduction, markdown blocks, or text before or after the JSON. Output only the raw JSON string so the parser can execute it immediately.",
		})
	}

	for _, msg := range req.Messages {
		switch v := msg.Content.(type) {
		case string:
			openAIMessages = append(openAIMessages, map[string]interface{}{
				"role":    msg.Role,
				"content": v,
			})
		case []interface{}:
			hasToolUse := false
			hasToolResult := false
			var textParts []string
			var toolCalls []interface{}

			for _, block := range v {
				blockMap, ok := block.(map[string]interface{})
				if !ok {
					continue
				}

				bType, _ := blockMap["type"].(string)
				switch bType {
				case "text":
					if txt, ok := blockMap["text"].(string); ok {
						textParts = append(textParts, txt)
					}
				case "tool_use":
					hasToolUse = true
					id, _ := blockMap["id"].(string)
					name, _ := blockMap["name"].(string)
					input := blockMap["input"]
					
					inputBytes, _ := json.Marshal(input)
					
					toolCalls = append(toolCalls, map[string]interface{}{
						"id":   id,
						"type": "function",
						"function": map[string]interface{}{
							"name":      name,
							"arguments": string(inputBytes),
						},
					})
				case "tool_result":
					hasToolResult = true
					toolUseID, _ := blockMap["tool_use_id"].(string)
					
					var resultStr string
					rawContent := blockMap["content"]
					switch rc := rawContent.(type) {
					case string:
						resultStr = rc
					case []interface{}:
						var innerTexts []string
						for _, b := range rc {
							if bm, ok := b.(map[string]interface{}); ok {
								if t, ok := bm["text"].(string); ok {
									innerTexts = append(innerTexts, t)
								}
							}
						}
						resultStr = strings.Join(innerTexts, "")
					default:
						bBytes, _ := json.Marshal(rawContent)
						resultStr = string(bBytes)
					}

					openAIMessages = append(openAIMessages, map[string]interface{}{
						"role":         "tool",
						"tool_call_id": toolUseID,
						"content":      resultStr,
					})
				}
			}

			if !hasToolResult {
				contentVal := strings.Join(textParts, "")
				newMsg := map[string]interface{}{
					"role":    msg.Role,
					"content": contentVal,
				}
				if hasToolUse {
					newMsg["tool_calls"] = toolCalls
				}
				openAIMessages = append(openAIMessages, newMsg)
			}
		}
	}

	// 2. Định danh SessionID
	sessionID := req.User
	if sessionID == "" {
		sessionID = c.ClientIP() + "_" + req.Model
	}

	var resp *http.Response
	var err error
	var account CFAccount

	// 3. Khởi dựng OpenAIRequest giả lập để chuyển tiếp sang API Cloudflare
	openAIReq := OpenAIRequest{
		Model:       req.Model,
		Messages:    openAIMessages,
		Tools:       translateAnthropicTools(req.Tools),
		ToolChoice:  translateAnthropicToolChoice(req.ToolChoice),
		Stream:      req.Stream,
		MaxTokens:   req.MaxTokens,
		Temperature: req.Temperature,
		TopP:        req.TopP,
		Stop:        req.StopSequences,
		User:        req.User,
	}

	// Vòng lặp Failover khẩn cấp chuyển đổi tài khoản khi dính lỗi
	for i := 0; i < 3; i++ {
		var ok bool
		account, ok = h.sm.GetAccount(sessionID)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Tất cả tài khoản hệ thống đã đạt ngưỡng giới hạn an toàn!"})
			return
		}

		resp, err = h.forwardToCloudflare(account, req.Model, openAIReq)

		if err != nil {
			h.sm.Penalize(account.AccountID, 5*time.Minute)
			h.sm.BreakSession(sessionID)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			h.sm.Penalize(account.AccountID, 12*time.Hour)
			h.sm.BreakSession(sessionID)
			continue
		}
		break
	}

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		log.Printf("[⚠️ Cloudflare Error Response] Status: %d, Body: %s", resp.StatusCode, string(bodyBytes))
		c.JSON(resp.StatusCode, gin.H{"error": fmt.Sprintf("Cloudflare API error %d: %s", resp.StatusCode, string(bodyBytes))})
		return
	}

	defer resp.Body.Close()

	stream := false
	if req.Stream != nil {
		stream = *req.Stream
	}

	promptTokens := estimatePromptTokens(openAIMessages, openAIReq.Tools)

	if stream {
		h.handleAnthropicStream(c, resp.Body, account.AccountID, req.Model, int64(promptTokens))
	} else {
		h.handleAnthropicStandard(c, resp.Body, account.AccountID, req.Model, int64(promptTokens))
	}
}

// handleAnthropicStandard định dạng kết quả phản hồi thường (Non-stream) chuẩn Anthropic.
func (h *ProxyHandler) handleAnthropicStandard(c *gin.Context, cfBody io.Reader, accountID, model string, promptTokens int64) {
	bodyBytes, _ := io.ReadAll(cfBody)
	log.Printf("[Cloudflare Raw Response (Anthropic)]: %s", string(bodyBytes))

	var cfResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &cfResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed decode"})
		return
	}

	result, ok := cfResponse["result"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Cloudflare API không trả về trường result hợp lệ"})
		return
	}

	aiResponse, _ := result["response"].(string)

	if aiResponse == "" {
		log.Println("[⚠️ WARNING] Cloudflare trả về chuỗi rỗng! Tự động điền text dự phòng.")
		aiResponse = "Cloudflare Proxy kết nối thành công, nhưng mô hình trả về dữ liệu trống."
	}

	var content []map[string]interface{}
	stopReason := "end_turn"

	if toolCalls, exists := result["tool_calls"].([]interface{}); exists && len(toolCalls) > 0 {
		stopReason = "tool_use"
		for _, tc := range toolCalls {
			if tcMap, ok := tc.(map[string]interface{}); ok {
				id, _ := tcMap["id"].(string)
				name, _ := tcMap["name"].(string)
				args := tcMap["arguments"]

				var argsMap map[string]interface{}
				if argsStr, ok := args.(string); ok {
					json.Unmarshal([]byte(argsStr), &argsMap)
				} else if am, ok := args.(map[string]interface{}); ok {
					argsMap = am
				}

				// Gửi tool_use block (để client biết tool nào đã được gọi và tự thực thi)
				content = append(content, map[string]interface{}{
					"type":  "tool_use",
					"id":    id,
					"name":  name,
					"input": argsMap,
				})
			}
		}
	} else if xmlToolCalls, textOutside, parsed := parseXMLToolCalls(aiResponse); parsed {
		stopReason = "tool_use"
		if textOutside != "" {
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": textOutside,
			})
		}
		for _, tc := range xmlToolCalls {
			funcMap, _ := tc["function"].(map[string]interface{})
			name, _ := funcMap["name"].(string)
			args := funcMap["arguments"]
			id, _ := tc["id"].(string)

			var argsMap map[string]interface{}
			if argsStr, ok := args.(string); ok {
				json.Unmarshal([]byte(argsStr), &argsMap)
			} else if am, ok := args.(map[string]interface{}); ok {
				argsMap = am
			}

			content = append(content, map[string]interface{}{
				"type":  "tool_use",
				"id":    id,
				"name":  name,
				"input": argsMap,
			})
		}
	} else if jsonToolCalls, textOutside, parsed := parseRawJSONToolCall(aiResponse); parsed {
		stopReason = "tool_use"
		if textOutside != "" {
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": textOutside,
			})
		}
		for _, tc := range jsonToolCalls {
			funcMap, _ := tc["function"].(map[string]interface{})
			name, _ := funcMap["name"].(string)
			args := funcMap["arguments"]
			id, _ := tc["id"].(string)

			var argsMap map[string]interface{}
			if argsStr, ok := args.(string); ok {
				json.Unmarshal([]byte(argsStr), &argsMap)
			} else if am, ok := args.(map[string]interface{}); ok {
				argsMap = am
			}

			content = append(content, map[string]interface{}{
				"type":  "tool_use",
				"id":    id,
				"name":  name,
				"input": argsMap,
			})
		}
	} else {
		content = append(content, map[string]interface{}{
			"type": "text",
			"text": aiResponse,
		})
	}

	outputTokens := int64(len(aiResponse) / 4)
	if outputTokens == 0 {
		outputTokens = 10
	}

	anthropicResponse := map[string]interface{}{
		"id":            fmt.Sprintf("msg_%d", time.Now().UnixNano()),
		"type":          "message",
		"role":          "assistant",
		"content":       content,
		"model":         model,
		"stop_reason":   stopReason,
		"stop_sequence": nil,
		"usage": map[string]interface{}{
			"input_tokens":  promptTokens,
			"output_tokens": outputTokens,
		},
	}
	c.JSON(http.StatusOK, anthropicResponse)

	estimatedTokens := int64(len(aiResponse) / 4)
	estimatedNeurons := int64(float64(estimatedTokens) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 50
	}
	h.sm.TrackUsage(accountID, estimatedNeurons)
}

// handleAnthropicStream chuyển đổi và phát dòng chảy sự kiện SSE chuẩn Anthropic từ dữ liệu thô Cloudflare.
func (h *ProxyHandler) handleAnthropicStream(c *gin.Context, cfBody io.Reader, accountID, model string, promptTokens int64) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	reader := bufio.NewReader(cfBody)
	msgID := fmt.Sprintf("msg_%d", time.Now().UnixNano())

	// Khởi tạo các mảng dữ liệu cấu trúc SSE sự kiện bắt đầu của Anthropic
	messageStart := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":            msgID,
			"type":          "message",
			"role":          "assistant",
			"content":       []interface{}{},
			"model":         model,
			"stop_reason":   nil,
			"stop_sequence": nil,
			"usage": map[string]interface{}{
				"input_tokens":  promptTokens,
				"output_tokens": 0,
			},
		},
	}
	msgStartBytes, _ := json.Marshal(messageStart)

	contentBlockStart := map[string]interface{}{
		"type":  "content_block_start",
		"index": 0,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	}
	blockStartBytes, _ := json.Marshal(contentBlockStart)

	var tokenCount int64 = 0
	var started bool = false
	var ended bool = false
	var hasToolUse bool = false

	var streamBuffer strings.Builder
	var flushedBuffer bool = false

	sendStartEvents := func(w io.Writer) {
		if started {
			return
		}
		started = true
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", string(msgStartBytes))
		fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(blockStartBytes))
	}

	sendEndEvents := func(w io.Writer) {
		if ended {
			return
		}
		ended = true

		// Xử lý nốt buffer nếu chưa được flush
		if !flushedBuffer {
			bufStr := streamBuffer.String()
			if xmlToolCalls, textOutside, parsed := parseXMLToolCalls(bufStr); parsed {
				hasToolUse = true
				if textOutside != "" {
					contentBlockDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]interface{}{
							"type": "text_delta",
							"text": textOutside,
						},
					}
					deltaBytes, _ := json.Marshal(contentBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
				}

				for _, tc := range xmlToolCalls {
					funcMap, _ := tc["function"].(map[string]interface{})
					name, _ := funcMap["name"].(string)
					args := funcMap["arguments"]
					id, _ := tc["id"].(string)

					argsBytes, _ := json.Marshal(args)
					argsStr := string(argsBytes)

					toolBlockStart := map[string]interface{}{
						"type":  "content_block_start",
						"index": 1,
						"content_block": map[string]interface{}{
							"type": "tool_use",
							"id":   id,
							"name": name,
						},
					}
					tbsBytes, _ := json.Marshal(toolBlockStart)
					fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(tbsBytes))

					toolBlockDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": 1,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": argsStr,
						},
					}
					tbdBytes, _ := json.Marshal(toolBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))

					toolBlockStop := map[string]interface{}{
						"type":  "content_block_stop",
						"index": 1,
					}
					tbstBytes, _ := json.Marshal(toolBlockStop)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(tbstBytes))
				}
			} else if jsonToolCalls, textOutside, parsed := parseRawJSONToolCall(bufStr); parsed {
				hasToolUse = true
				if textOutside != "" {
					contentBlockDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]interface{}{
							"type": "text_delta",
							"text": textOutside,
						},
					}
					deltaBytes, _ := json.Marshal(contentBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
				}

				for _, tc := range jsonToolCalls {
					funcMap, _ := tc["function"].(map[string]interface{})
					name, _ := funcMap["name"].(string)
					args := funcMap["arguments"]
					id, _ := tc["id"].(string)

					argsBytes, _ := json.Marshal(args)
					argsStr := string(argsBytes)

					toolBlockStart := map[string]interface{}{
						"type":  "content_block_start",
						"index": 1,
						"content_block": map[string]interface{}{
							"type": "tool_use",
							"id":   id,
							"name": name,
						},
					}
					tbsBytes, _ := json.Marshal(toolBlockStart)
					fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(tbsBytes))

					toolBlockDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": 1,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": argsStr,
						},
					}
					tbdBytes, _ := json.Marshal(toolBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))

					toolBlockStop := map[string]interface{}{
						"type":  "content_block_stop",
						"index": 1,
					}
					tbstBytes, _ := json.Marshal(toolBlockStop)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(tbstBytes))
				}
			} else {
				if bufStr != "" {
					contentBlockDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": 0,
						"delta": map[string]interface{}{
							"type": "text_delta",
							"text": bufStr,
						},
					}
					deltaBytes, _ := json.Marshal(contentBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
				}
			}
			flushedBuffer = true
		}

		contentBlockStop := map[string]interface{}{
			"type":  "content_block_stop",
			"index": 0,
		}
		blockStopBytes, _ := json.Marshal(contentBlockStop)
		fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(blockStopBytes))

		stopReason := "end_turn"
		if hasToolUse {
			stopReason = "tool_use"
		}

		messageDelta := map[string]interface{}{
			"type": "message_delta",
			"delta": map[string]interface{}{
				"stop_reason":   stopReason,
				"stop_sequence": nil,
			},
			"usage": map[string]interface{}{
				"output_tokens": tokenCount,
			},
		}
		msgDeltaBytes, _ := json.Marshal(messageDelta)
		fmt.Fprintf(w, "event: message_delta\ndata: %s\n\n", string(msgDeltaBytes))

		messageStop := map[string]interface{}{
			"type": "message_stop",
		}
		msgStopBytes, _ := json.Marshal(messageStop)
		fmt.Fprintf(w, "event: message_stop\ndata: %s\n\n", string(msgStopBytes))
	}

	c.Stream(func(w io.Writer) bool {
		sendStartEvents(w)

		line, err := reader.ReadString('\n')
		if err != nil {
			sendEndEvents(w)
			return false
		}

		line = strings.TrimSpace(line)
		if line != "" {
			log.Printf("[Stream Chunk] Read line: %s", line)
		}
		if line == "" {
			return true
		}
		if !strings.HasPrefix(line, "data:") {
			return true
		}

		dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if dataStr == "[DONE]" {
			sendEndEvents(w)
			return false
		}

		var cfJSON map[string]interface{}
		if err := json.Unmarshal([]byte(dataStr), &cfJSON); err != nil {
			return true
		}

		// 1. Kiểm tra phát hiện Tool Calls trong luồng Stream
		if toolCalls, exists := cfJSON["tool_calls"].([]interface{}); exists && len(toolCalls) > 0 {
			hasToolUse = true
			for idx, tc := range toolCalls {
				if tcMap, ok := tc.(map[string]interface{}); ok {
					id, _ := tcMap["id"].(string)
					name, _ := tcMap["name"].(string)
					args := tcMap["arguments"]

					var argsMap map[string]interface{}
					if argsStr2, ok2 := args.(string); ok2 {
						json.Unmarshal([]byte(argsStr2), &argsMap)
					} else if am, ok2 := args.(map[string]interface{}); ok2 {
						argsMap = am
					}
					argsBytes, _ := json.Marshal(argsMap)
					argsStr := string(argsBytes)

					// --- Gửi tool_use block ---
					toolBlockStart := map[string]interface{}{
						"type":  "content_block_start",
						"index": idx + 1,
						"content_block": map[string]interface{}{
							"type": "tool_use",
							"id":   id,
							"name": name,
						},
					}
					tbsBytes, _ := json.Marshal(toolBlockStart)
					fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(tbsBytes))

					toolBlockDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": idx + 1,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": argsStr,
						},
					}
					tbdBytes, _ := json.Marshal(toolBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))

					toolBlockStop := map[string]interface{}{
						"type":  "content_block_stop",
						"index": idx + 1,
					}
					tbstBytes, _ := json.Marshal(toolBlockStop)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(tbstBytes))

				}
			}
		}

		// 2. Xử lý phản hồi text thông thường
		token, _ := cfJSON["response"].(string)
		if token != "" {
			tokenCount++

			if !flushedBuffer {
				streamBuffer.WriteString(token)
				bufStr := streamBuffer.String()
				trimmedBuf := strings.TrimSpace(bufStr)

				// Nếu buffer bắt đầu bằng "<tools", "<tool_use" hoặc "{"
				if strings.HasPrefix(trimmedBuf, "<tools") || strings.HasPrefix(trimmedBuf, "<tool_use") || strings.HasPrefix(trimmedBuf, "{") {
					isXML := strings.HasPrefix(trimmedBuf, "<tools") || strings.HasPrefix(trimmedBuf, "<tool_use")
					hasEnded := false
					if isXML {
						hasEnded = strings.Contains(trimmedBuf, "</tools>") || strings.Contains(trimmedBuf, "</tool_use>")
					} else {
						hasEnded = strings.HasSuffix(trimmedBuf, "}")
					}

					if hasEnded {
						var parsed bool
						var toolCalls []map[string]interface{}
						var textOutside string

						if isXML {
							toolCalls, textOutside, parsed = parseXMLToolCalls(bufStr)
						} else {
							toolCalls, textOutside, parsed = parseRawJSONToolCall(bufStr)
						}

						if parsed {
							hasToolUse = true
							flushedBuffer = true

							if textOutside != "" {
								contentBlockDelta := map[string]interface{}{
									"type":  "content_block_delta",
									"index": 0,
									"delta": map[string]interface{}{
										"type": "text_delta",
										"text": textOutside,
									},
								}
								deltaBytes, _ := json.Marshal(contentBlockDelta)
								fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
							}

							for xmlIdx, tc := range toolCalls {
								funcMap, _ := tc["function"].(map[string]interface{})
								name, _ := funcMap["name"].(string)
								args := funcMap["arguments"]
								id, _ := tc["id"].(string)

								var argsMap map[string]interface{}
								if am, ok := args.(map[string]interface{}); ok {
									argsMap = am
								}
								argsBytes, _ := json.Marshal(argsMap)
								argsStr := string(argsBytes)

								blkIdx := xmlIdx + 1

								toolBlockStart := map[string]interface{}{
									"type":  "content_block_start",
									"index": blkIdx,
									"content_block": map[string]interface{}{
										"type": "tool_use",
										"id":   id,
										"name": name,
									},
								}
								tbsBytes, _ := json.Marshal(toolBlockStart)
								fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(tbsBytes))

								toolBlockDelta := map[string]interface{}{
									"type":  "content_block_delta",
									"index": 1,
									"delta": map[string]interface{}{
										"type":         "input_json_delta",
										"partial_json": argsStr,
									},
								}
								tbdBytes, _ := json.Marshal(toolBlockDelta)
								fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))

								toolBlockStop := map[string]interface{}{
									"type":  "content_block_stop",
									"index": 1,
								}
								tbstBytes, _ := json.Marshal(toolBlockStop)
								fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(tbstBytes))
							}
						}
					}
				} else {
					// Nếu có ký tự lạ không khớp hoặc độ dài buffer đã lớn (> 15 ký tự), flush
					if len(trimmedBuf) >= 15 || strings.Contains(bufStr, "\n") {
						flushedBuffer = true
						contentBlockDelta := map[string]interface{}{
							"type":  "content_block_delta",
							"index": 0,
							"delta": map[string]interface{}{
								"type": "text_delta",
								"text": bufStr,
							},
						}
						deltaBytes, _ := json.Marshal(contentBlockDelta)
						fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
					}
				}
			} else {
				contentBlockDelta := map[string]interface{}{
					"type":  "content_block_delta",
					"index": 0,
					"delta": map[string]interface{}{
						"type": "text_delta",
						"text": token,
					},
				}
				deltaBytes, _ := json.Marshal(contentBlockDelta)
				fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
			}
		}

		return true
	})

	estimatedNeurons := int64(float64(tokenCount) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 10
	}
	h.sm.TrackUsage(accountID, estimatedNeurons)
}

// estimatePromptTokens ước lượng số lượng tokens của prompt đầu vào bao gồm cả tin nhắn và định nghĩa công cụ.
func estimatePromptTokens(messages []interface{}, tools []interface{}) int {
	totalChars := 0
	for _, m := range messages {
		switch msg := m.(type) {
		case map[string]string:
			totalChars += len(msg["content"])
		case map[string]interface{}:
			if contentStr, ok := msg["content"].(string); ok {
				totalChars += len(contentStr)
			}
		}
	}

	if tools != nil && len(tools) > 0 {
		toolsBytes, _ := json.Marshal(tools)
		totalChars += len(toolsBytes)
	}

	// 1 token ≈ 1.1 ký tự đối với mã nguồn, JSON schema và XML tags trong tool calling
	return int(float64(totalChars) / 1.1)
}

// forwardToCloudflare chuẩn hóa tên model (đảm bảo chứa @cf/) và gửi HTTP Request trực tiếp tới Cloudflare Workers AI.
func (h *ProxyHandler) forwardToCloudflare(acc CFAccount, targetModel string, req OpenAIRequest) (*http.Response, error) {
	// Giải quyết và chuẩn hóa tên model sang đường dẫn ID của Cloudflare
	cleanModel := h.sm.ResolveModel(targetModel)

	maxTokens := 1024
	if req.MaxTokens != nil {
		maxTokens = *req.MaxTokens
	}

	// Tự động tối ưu hóa max_tokens để tránh lỗi vượt ngưỡng Context Length (32768 tokens)
	promptTokens := estimatePromptTokens(req.Messages, req.Tools)
	maxContextLimit := 32768
	if strings.Contains(cleanModel, "llama-3.2") || strings.Contains(cleanModel, "llama-3-8b") {
		maxContextLimit = 8192
	}

	maxAllowedOutput := maxContextLimit - promptTokens - 500 // Dự phòng 500 tokens buffer
	if maxAllowedOutput < 1024 {
		maxAllowedOutput = 1024
	}

	if maxTokens > maxAllowedOutput {
		log.Printf("[🔧 Token Adjust] Capped max_tokens từ %d xuống %d để tránh quá tải context (Prompt tokens: ~%d)", maxTokens, maxAllowedOutput, promptTokens)
		maxTokens = maxAllowedOutput
	}

	stream := false
	if req.Stream != nil {
		stream = *req.Stream
	}
	payload := map[string]interface{}{
		"messages":   req.Messages,
		"stream":     stream,
		"max_tokens": maxTokens,
	}
	if req.Tools != nil && len(req.Tools) > 0 {
		payload["tools"] = req.Tools
	}
	if req.ToolChoice != nil {
		payload["tool_choice"] = req.ToolChoice
	}
	if req.Temperature != nil {
		payload["temperature"] = *req.Temperature
	}
	if req.TopP != nil {
		payload["top_p"] = *req.TopP
	}
	if req.Stop != nil && len(req.Stop) > 0 {
		payload["stop"] = req.Stop
	}
	bodyBytes, _ := json.Marshal(payload)

	url := fmt.Sprintf("https://api.cloudflare.com/client/v4/accounts/%s/ai/run/%s", strings.TrimSpace(acc.AccountID), cleanModel)
	log.Printf("[🎯 Cloudflare Pipe] URL: %s", url)

	cfReq, _ := http.NewRequest("POST", url, bytes.NewBuffer(bodyBytes))
	cfReq.Header.Set("Authorization", "Bearer "+strings.TrimSpace(acc.APIToken))
	cfReq.Header.Set("Content-Type", "application/json")

	return h.client.Do(cfReq)
}

// handleStream chuyển đổi và stream phản hồi định dạng SSE chuẩn OpenAI.
func (h *ProxyHandler) handleStream(c *gin.Context, cfBody io.Reader, accountID, model string) {
	c.Header("Content-Type", "text/event-stream")
	c.Header("Cache-Control", "no-cache")
	c.Header("Connection", "keep-alive")
	c.Header("Transfer-Encoding", "chunked")

	converter := NewStreamConverter(model)
	reader := bufio.NewReader(cfBody)

	c.Stream(func(w io.Writer) bool {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err == io.EOF {
				fmt.Fprint(w, "data: [DONE]\n\n")
			}
			return false
		}

		openAILine, keepGoing := converter.ConvertLine(line)
		if openAILine != "" {
			fmt.Fprint(w, openAILine)
		}
		return keepGoing
	})

	estimatedNeurons := int64(float64(converter.tokenCount) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 10
	}
	h.sm.TrackUsage(accountID, estimatedNeurons)
}

// handleStandard định dạng phản hồi JSON thường (Non-stream) chuẩn OpenAI.
func (h *ProxyHandler) handleStandard(c *gin.Context, cfBody io.Reader, accountID, model string) {
	bodyBytes, _ := io.ReadAll(cfBody)
	log.Printf("[Cloudflare Raw Response]: %s", string(bodyBytes))

	var cfResponse map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &cfResponse); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed decode"})
		return
	}

	result, ok := cfResponse["result"].(map[string]interface{})
	if !ok {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "Cloudflare API không trả về trường result hợp lệ"})
		return
	}

	aiResponse, _ := result["response"].(string)

	if aiResponse == "" {
		log.Println("[⚠️ WARNING] Cloudflare trả về chuỗi rỗng! Tự động điền text dự phòng.")
		aiResponse = "Cloudflare Proxy kết nối thành công, nhưng mô hình trả về dữ liệu trống."
	}

	openAIResponse := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().Unix()),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]interface{}{
			{"index": 0, "message": map[string]interface{}{"role": "assistant", "content": aiResponse}, "finish_reason": "stop"},
		},
	}
	c.JSON(http.StatusOK, openAIResponse)

	estimatedTokens := int64(len(aiResponse) / 4)
	estimatedNeurons := int64(float64(estimatedTokens) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 50
	}
	h.sm.TrackUsage(accountID, estimatedNeurons)
}

// HandleListModels trả về danh sách các model đang hoạt động theo chuẩn OpenAI.
func (h *ProxyHandler) HandleListModels(c *gin.Context) {
	h.sm.mu.RLock()
	defer h.sm.mu.RUnlock()

	var data []map[string]interface{}
	// Thêm CHỈ các mô hình từ models.csv
	for _, alias := range h.sm.modelsList {
		data = append(data, map[string]interface{}{
			"id":       alias,
			"object":   "model",
			"created":  1717190400,
			"owned_by": "cloudflare",
		})
	}

	c.JSON(http.StatusOK, map[string]interface{}{
		"object": "list",
		"data":   data,
	})
}

// translateAnthropicTools chuyển đổi khai báo công cụ của Anthropic sang cấu trúc OpenAI Function tương thích.
func translateAnthropicTools(tools interface{}) []interface{} {
	if tools == nil {
		return nil
	}

	rawTools, ok := tools.([]interface{})
	if !ok {
		return nil
	}

	var openAITools []interface{}
	for _, t := range rawTools {
		toolMap, ok := t.(map[string]interface{})
		if !ok {
			continue
		}

		name, _ := toolMap["name"].(string)
		desc, _ := toolMap["description"].(string)
		inputSchema := toolMap["input_schema"]

		openAITools = append(openAITools, map[string]interface{}{
			"type": "function",
			"function": map[string]interface{}{
				"name":        name,
				"description": desc,
				"parameters":  inputSchema,
			},
		})
	}
	return openAITools
}

// translateAnthropicToolChoice chuyển đổi tham số tool_choice từ Anthropic sang định dạng OpenAI tương thích.
func translateAnthropicToolChoice(toolChoice interface{}) interface{} {
	if toolChoice == nil {
		return nil
	}
	tcMap, ok := toolChoice.(map[string]interface{})
	if !ok {
		if str, ok := toolChoice.(string); ok {
			return str
		}
		return nil
	}
	tcType, _ := tcMap["type"].(string)
	switch tcType {
	case "auto":
		return "auto"
	case "any":
		return "required"
	case "tool":
		name, _ := tcMap["name"].(string)
		if name != "" {
			return map[string]interface{}{
				"type": "function",
				"function": map[string]interface{}{
					"name": name,
				},
			}
		}
	}
	return nil
}

// parseXMLToolCalls phân tích phản hồi dạng XML <tools>...</tools> của Qwen/DeepSeek và chuyển đổi sang OpenAI ToolCall.
func parseXMLToolCalls(text string) ([]map[string]interface{}, string, bool) {
	trimmed := strings.TrimSpace(text)

	var startTag, endTag string
	if strings.Contains(trimmed, "<tools>") && strings.Contains(trimmed, "</tools>") {
		startTag = "<tools>"
		endTag = "</tools>"
	} else if strings.Contains(trimmed, "<tool_use>") && strings.Contains(trimmed, "</tool_use>") {
		startTag = "<tool_use>"
		endTag = "</tool_use>"
	} else {
		return nil, text, false
	}

	startIdx := strings.Index(trimmed, startTag)
	endIdx := strings.Index(trimmed, endTag)

	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return nil, text, false
	}

	toolsContent := trimmed[startIdx+len(startTag) : endIdx]
	toolsContent = strings.TrimSpace(toolsContent)

	textOutside := trimmed[:startIdx] + trimmed[endIdx+len(endTag):]
	textOutside = strings.TrimSpace(textOutside)

	// Thử parse JSON object đơn lẻ
	var singleCall map[string]interface{}
	if err := json.Unmarshal([]byte(toolsContent), &singleCall); err == nil {
		name, _ := singleCall["name"].(string)
		arguments := singleCall["arguments"]
		if name != "" {
			id := fmt.Sprintf("call_%d", time.Now().UnixNano())
			return []map[string]interface{}{
				{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
					},
				},
			}, textOutside, true
		}
	}

	// Thử parse mảng JSON array
	var multiCalls []map[string]interface{}
	if err := json.Unmarshal([]byte(toolsContent), &multiCalls); err == nil {
		var result []map[string]interface{}
		for _, tc := range multiCalls {
			name, _ := tc["name"].(string)
			arguments := tc["arguments"]
			if name != "" {
				id := fmt.Sprintf("call_%d", time.Now().UnixNano())
				result = append(result, map[string]interface{}{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
					},
				})
			}
		}
		if len(result) > 0 {
			return result, textOutside, true
		}
	}

	// Thử tách theo dòng (nhiều JSON Object đơn lẻ nối tiếp nhau)
	lines := strings.Split(toolsContent, "\n")
	var result []map[string]interface{}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var item map[string]interface{}
		if err := json.Unmarshal([]byte(line), &item); err == nil {
			name, _ := item["name"].(string)
			arguments := item["arguments"]
			if name != "" {
				id := fmt.Sprintf("call_%d", time.Now().UnixNano())
				result = append(result, map[string]interface{}{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": arguments,
					},
				})
			}
		}
	}
	if len(result) > 0 {
		return result, textOutside, true
	}

	return nil, text, false
}

// ============================================================================
// LOCAL TOOL EXECUTOR
// ============================================================================

// runLocalTool thực thi công cụ nội bộ của proxy.
// Hiện tại hỗ trợ: Write, Update, Read.
// Trả về chuỗi kết quả (thành công) hoặc thông báo lỗi.
func (h *ProxyHandler) runLocalTool(name string, args map[string]interface{}) (string, error) {
	switch name {
	case "Write":
		return localToolWrite(args)
	case "Update":
		return localToolUpdate(args)
	case "Read":
		return localToolRead(args)
	case "Bash":
		return localToolBash(args)
	case "NotebookRead":
		// Không thực thi Notebook trực tiếp, trả về thông báo
		return "NotebookRead không được hỗ trợ trong proxy mode", nil
	default:
		// Với tool không rõ: trả về thông báo (không lỗi) để model tiếp tục
		log.Printf("[Tool Dispatcher] Tool '%s' chưa được triển khai, bỏ qua.", name)
		return fmt.Sprintf("Tool '%s' executed (no local handler, please implement if needed)", name), nil
	}
}

// localToolWrite ghi toàn bộ nội dung vào file (tạo mới nếu chưa tồn tại, ghi đè nếu đã tồn tại).
// args: { "file_path": "...", "content": "..." }
func localToolWrite(args map[string]interface{}) (string, error) {
	filePath, ok1 := args["file_path"].(string)
	content, ok2 := args["content"].(string)
	if !ok1 || filePath == "" {
		return "", fmt.Errorf("Write: thiếu hoặc sai tham số 'file_path'")
	}
	if !ok2 {
		return "", fmt.Errorf("Write: thiếu hoặc sai tham số 'content'")
	}

	// Đảm bảo thư mục tồn tại
	dir := filepath.Dir(filePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("Write: không tạo được thư mục '%s': %w", dir, err)
	}

	// Ghi file
	if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
		return "", fmt.Errorf("Write: không ghi được file '%s': %w", filePath, err)
	}

	log.Printf("[Tool-Write] ✅ Đã ghi %d byte vào %s", len(content), filePath)
	return fmt.Sprintf("Đã ghi thành công %d byte vào file: %s", len(content), filePath), nil
}

// localToolUpdate cập nhật một phần nội dung trong file hiện có.
// args: { "file_path": "...", "old_str": "...", "new_str": "..." }
func localToolUpdate(args map[string]interface{}) (string, error) {
	filePath, ok1 := args["file_path"].(string)
	oldStr, ok2 := args["old_str"].(string)
	newStr, ok3 := args["new_str"].(string)
	if !ok1 || filePath == "" {
		return "", fmt.Errorf("Update: thiếu hoặc sai tham số 'file_path'")
	}
	if !ok2 {
		return "", fmt.Errorf("Update: thiếu hoặc sai tham số 'old_str'")
	}
	if !ok3 {
		return "", fmt.Errorf("Update: thiếu hoặc sai tham số 'new_str'")
	}

	// Đọc file hiện có
	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("Update: không đọc được file '%s': %w", filePath, err)
	}

	currentContent := string(data)
	if !strings.Contains(currentContent, oldStr) {
		return "", fmt.Errorf("Update: không tìm thấy chuỗi old_str trong file '%s'", filePath)
	}

	updatedContent := strings.Replace(currentContent, oldStr, newStr, 1)

	if err := os.WriteFile(filePath, []byte(updatedContent), 0o644); err != nil {
		return "", fmt.Errorf("Update: không ghi được file '%s': %w", filePath, err)
	}

	log.Printf("[Tool-Update] ✅ Đã cập nhật file %s", filePath)
	return fmt.Sprintf("Đã cập nhật thành công file: %s", filePath), nil
}

// localToolRead đọc nội dung file và trả về dưới dạng chuỗi.
// args: { "file_path": "..." }
func localToolRead(args map[string]interface{}) (string, error) {
	filePath, ok := args["file_path"].(string)
	if !ok || filePath == "" {
		return "", fmt.Errorf("Read: thiếu hoặc sai tham số 'file_path'")
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("Read: không đọc được file '%s': %w", filePath, err)
	}

	log.Printf("[Tool-Read] ✅ Đọc %d byte từ %s", len(data), filePath)
	return string(data), nil
}

// localToolBash thực thi một lệnh shell tuỳ ý trong bash.
// args:
//
//	"command"  (string, bắt buộc) – lệnh cần chạy (ví dụ: "ls -la /tmp")
//	"timeout"  (number, tuỳ chọn) – timeout tính bằng giây, mặc định 30s, tối đa 120s
//	"cwd"      (string, tuỳ chọn) – thư mục làm việc, mặc định là thư mục hiện hành
//
// Trả về stdout + stderr gộp chung (tối đa 32 KB).
// Nếu lệnh exit với mã ≠ 0, hàm vẫn trả về output kèm thông báo exit code (không lỗi hard),
// để model có thể đọc stderr và tự sửa.
func localToolBash(args map[string]interface{}) (string, error) {
	const maxOutputBytes = 32 * 1024 // 32 KB
	const defaultTimeout = 30        // giây
	const maxTimeout = 120           // giây

	// --- Lấy tham số command (bắt buộc) ---
	command, ok := args["command"].(string)
	if !ok || strings.TrimSpace(command) == "" {
		return "", fmt.Errorf("Bash: thiếu hoặc sai tham số 'command'")
	}

	// --- Timeout ---
	timeoutSec := defaultTimeout
	switch v := args["timeout"].(type) {
	case float64:
		timeoutSec = int(v)
	case int:
		timeoutSec = v
	}
	if timeoutSec <= 0 {
		timeoutSec = defaultTimeout
	}
	if timeoutSec > maxTimeout {
		timeoutSec = maxTimeout
	}

	// --- Working directory ---
	cwd, _ := args["cwd"].(string)
	if cwd == "" {
		// Mặc định: thư mục chứa binary proxy
		if exe, err := os.Executable(); err == nil {
			cwd = filepath.Dir(exe)
		} else {
			cwd = "."
		}
	}

	log.Printf("[Tool-Bash] 🐚 Chạy lệnh (timeout=%ds, cwd=%s): %s", timeoutSec, cwd, command)

	// --- Tạo context với timeout ---
	ctx, cancel := context.WithTimeout(context.Background(), time.Duration(timeoutSec)*time.Second)
	defer cancel()

	// --- Tạo command ---
	cmd := exec.CommandContext(ctx, "bash", "-c", command)
	cmd.Dir = cwd

	// Kế thừa biến môi trường của process cha (PATH, HOME, v.v.)
	cmd.Env = os.Environ()

	// Gộp stdout + stderr vào một buffer
	var outBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &outBuf

	// --- Chạy ---
	runErr := cmd.Run()

	// --- Kiểm tra timeout ---
	if ctx.Err() == context.DeadlineExceeded {
		log.Printf("[Tool-Bash] ⏰ Timeout sau %ds: %s", timeoutSec, command)
		output := outBuf.String()
		if len(output) > maxOutputBytes {
			output = output[:maxOutputBytes] + "\n... [output truncated]"
		}
		return fmt.Sprintf("⏰ Command timed out after %ds.\n--- Partial output ---\n%s", timeoutSec, output), nil
	}

	// --- Lấy output, cắt nếu quá dài ---
	output := outBuf.String()
	truncated := false
	if len(output) > maxOutputBytes {
		output = output[:maxOutputBytes]
		truncated = true
	}

	// --- Xử lý exit code ---
	if runErr != nil {
		exitCode := -1
		if exitErr, ok := runErr.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		}
		log.Printf("[Tool-Bash] ⚠️ Exit code %d: %s", exitCode, command)
		suffix := ""
		if truncated {
			suffix = "\n... [output truncated at 32KB]"
		}
		// Trả về kết quả kèm exit code, KHÔNG trả lỗi hard để model tự xử lý
		return fmt.Sprintf("Exit code: %d\n--- Output ---\n%s%s", exitCode, output, suffix), nil
	}

	log.Printf("[Tool-Bash] ✅ Thành công (exit 0): %s", command)
	suffix := ""
	if truncated {
		suffix = "\n... [output truncated at 32KB]"
	}
	return output + suffix, nil
}

// parseRawJSONToolCall phân tích phản hồi dạng JSON thô {"name": "...", "arguments": {...}} của Qwen/DeepSeek.
func parseRawJSONToolCall(text string) ([]map[string]interface{}, string, bool) {
	trimmed := strings.TrimSpace(text)

	// Tìm vị trí của dấu { đầu tiên và dấu } cuối cùng
	startIdx := strings.Index(trimmed, "{")
	endIdx := strings.LastIndex(trimmed, "}")

	if startIdx == -1 || endIdx == -1 || endIdx <= startIdx {
		return nil, text, false
	}

	jsonContent := trimmed[startIdx : endIdx+1]

	var item map[string]interface{}
	if err := json.Unmarshal([]byte(jsonContent), &item); err == nil {
		name, _ := item["name"].(string)
		args := item["arguments"]
		if name != "" && args != nil {
			id := fmt.Sprintf("call_%d", time.Now().UnixNano())
			textOutside := trimmed[:startIdx] + trimmed[endIdx+1:]
			textOutside = strings.TrimSpace(textOutside)

			// Trả về theo định dạng OpenAI ToolCall tương thích
			return []map[string]interface{}{
				{
					"id":   id,
					"type": "function",
					"function": map[string]interface{}{
						"name":      name,
						"arguments": args,
					},
				},
			}, textOutside, true
		}
	}
	return nil, text, false
}

