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
	var account CFAccount

	// Vòng lặp Failover khẩn cấp chuyển đổi tài khoản khi dính lỗi
	for i := 0; i < 3; i++ {
		var ok bool
		account, ok = h.sm.GetAccount(c.Request.Context(), sessionID)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Tất cả tài khoản hệ thống đã đạt ngưỡng giới hạn an toàn!"})
			return
		}

		// Gọi chuyển tiếp đến Cloudflare
		var err error
		resp, err = h.forwardToCloudflare(account, req.Model, req)

		if err != nil || resp == nil {
			h.sm.Penalize(c.Request.Context(), account.AccountID, 5*time.Minute)
			h.sm.BreakSession(c.Request.Context(), sessionID)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			h.sm.Penalize(c.Request.Context(), account.AccountID, 12*time.Hour) // Phạt 24 tiếng nếu cạn kiệt Neurons ngày
			h.sm.BreakSession(c.Request.Context(), sessionID)
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			h.sm.Penalize(c.Request.Context(), account.AccountID, 5*time.Minute)
			h.sm.BreakSession(c.Request.Context(), sessionID)
			continue
		}
		break
	}

	if resp == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "All attempts to forward request to Cloudflare failed"})
		return
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

	// Log danh sách các tools nhận từ client
	if req.Tools != nil {
		log.Printf("[🔧 Client Tools Metadata] Tools: %+v", req.Tools)
	}
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
		toolSystemInstruction = "[CRITICAL SYSTEM INSTRUCTION FOR TOOL USE]\nWhen you decide to call a tool, you MUST use the EXACT tool name and arguments schema defined in the provided tools list (e.g., 'Read', 'Write', 'Edit', 'Bash'). Output your tool call as a raw JSON block with 'name' and 'arguments' fields. Example:\n{\"name\": \"tool_name_from_provided_list\", \"arguments\": {\"param1\": \"value1\"}}\nDO NOT write any explanation, introduction, markdown blocks, or text before or after the JSON. Output only the raw JSON string so the parser can execute it immediately."
	}

	if systemPrompt != "" {
		content := systemPrompt
		if toolSystemInstruction != "" {
			content += "\n\n" + toolSystemInstruction
		}
		openAIMessages = append(openAIMessages, map[string]string{
			"role":    "system",
			"content": content,
		})
	} else if toolSystemInstruction != "" {
		openAIMessages = append(openAIMessages, map[string]string{
			"role":    "system",
			"content": toolSystemInstruction,
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
			hasToolResult := false
			var textParts []string

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
				case "thinking":
					if thinkingText, ok := blockMap["thinking"].(string); ok {
						textParts = append(textParts, fmt.Sprintf("<think>\n%s\n</think>\n", thinkingText))
					}
				case "tool_use":
					name, _ := blockMap["name"].(string)
					input := blockMap["input"]

					inputBytes, _ := json.Marshal(input)
					// Tái cấu trúc thành dạng JSON thô gọi tool của mô hình
					toolCallText := fmt.Sprintf("\n{\"name\": \"%s\", \"arguments\": %s}", name, string(inputBytes))
					textParts = append(textParts, toolCallText)
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

					// Gửi tool_result dưới dạng user message thông thường để bypass lỗi 400 của Cloudflare
					openAIMessages = append(openAIMessages, map[string]interface{}{
						"role":    "user",
						"content": fmt.Sprintf("[Tool Result for tool_use_id: %s]\n%s", toolUseID, resultStr),
					})
				}
			}

			if !hasToolResult {
				contentVal := strings.Join(textParts, "")
				newMsg := map[string]interface{}{
					"role":    msg.Role,
					"content": contentVal,
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
	var account CFAccount

	// 3. Khởi dựng OpenAIRequest giả lập để chuyển tiếp sang API Cloudflare
	openAIReq := OpenAIRequest{
		Model:               req.Model,
		Messages:            openAIMessages,
		Tools:               translateAnthropicTools(req.Tools),
		ToolChoice:          translateAnthropicToolChoice(req.ToolChoice),
		Stream:              req.Stream,
		MaxTokens:           req.MaxTokens,
		MaxCompletionTokens: req.MaxTokens, // Gán song song cho các mô hình reasoning
		Temperature:         req.Temperature,
		TopP:                req.TopP,
		Stop:                req.StopSequences,
		User:                req.User,
	}

	// Ánh xạ mức nỗ lực suy nghĩ (Reasoning Effort)
	if req.Effort != "" {
		openAIReq.ReasoningEffort = req.Effort
	} else if req.Thinking != nil {
		if tm, ok := req.Thinking.(map[string]interface{}); ok {
			if effortVal, ok := tm["effort"].(string); ok {
				openAIReq.ReasoningEffort = effortVal
			}
		}
	}

	// Vòng lặp Failover khẩn cấp chuyển đổi tài khoản khi dính lỗi
	for i := 0; i < 3; i++ {
		var ok bool
		account, ok = h.sm.GetAccount(c.Request.Context(), sessionID)
		if !ok {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Tất cả tài khoản hệ thống đã đạt ngưỡng giới hạn an toàn!"})
			return
		}

		var err error
		resp, err = h.forwardToCloudflare(account, req.Model, openAIReq)

		if err != nil || resp == nil {
			h.sm.Penalize(c.Request.Context(), account.AccountID, 5*time.Minute)
			h.sm.BreakSession(c.Request.Context(), sessionID)
			continue
		}

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusUnauthorized {
			resp.Body.Close()
			h.sm.Penalize(c.Request.Context(), account.AccountID, 12*time.Hour)
			h.sm.BreakSession(c.Request.Context(), sessionID)
			continue
		}

		if resp.StatusCode >= 500 {
			resp.Body.Close()
			h.sm.Penalize(c.Request.Context(), account.AccountID, 5*time.Minute)
			h.sm.BreakSession(c.Request.Context(), sessionID)
			continue
		}
		break
	}

	if resp == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "All attempts to forward request to Cloudflare failed"})
		return
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

	// Guard empty response
	if len(bodyBytes) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Empty response from Cloudflare"})
		return
	}

	var cfResponse map[string]interface{}
	// Attempt decode; if malformed, repair and retry
	if err := json.Unmarshal(bodyBytes, &cfResponse); err != nil {
		repaired := repairJSON(string(bodyBytes))
		if err2 := json.Unmarshal([]byte(repaired), &cfResponse); err2 != nil {
			log.Printf("[⚠️ Parse Error] original: %v, repaired: %v, raw: %s", err, err2, string(bodyBytes))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed decode Cloudflare response"})
			return
		}
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

	// Tách biệt phần suy nghĩ (thinking) nếu có thẻ <think>
	thinkingPart, mainText := parseThinkingTags(aiResponse)
	if thinkingPart != "" {
		content = append(content, map[string]interface{}{
			"type":     "thinking",
			"thinking": thinkingPart,
		})
		aiResponse = mainText
	}

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
		if aiResponse != "" || len(content) == 0 {
			content = append(content, map[string]interface{}{
				"type": "text",
				"text": aiResponse,
			})
		}
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

	estimatedNeurons := int64(float64(len(aiResponse)/4) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 50
	}
	h.sm.TrackUsage(c.Request.Context(), accountID, estimatedNeurons)
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

	var tokenCount int64 = 0
	var started bool = false
	var ended bool = false
	var hasToolUse bool = false

	// Map theo dõi trạng thái gửi tool_use chuẩn SSE
	sentToolStart := make(map[int]bool)
	toolIDs := make(map[int]string)
	toolNames := make(map[int]string)

	var inToolCallBuf bool
	var toolCallBuf strings.Builder

	// Máy trạng thái cho streaming suy nghĩ (thinking) và text
	var activeBlockType string // "thinking" or "text"
	var activeBlockIndex int   // 0, 1...
	var activeBlockStarted bool
	var thinkingEnded bool

	sendStartBlock := func(w io.Writer, bType string, idx int) {
		blockStartEvent := map[string]interface{}{
			"type":  "content_block_start",
			"index": idx,
			"content_block": map[string]interface{}{
				"type": bType,
			},
		}
		if bType == "text" {
			blockStartEvent["content_block"].(map[string]interface{})["text"] = ""
		}
		bsBytes, _ := json.Marshal(blockStartEvent)
		fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(bsBytes))
	}

	sendDeltaBlock := func(w io.Writer, bType string, idx int, text string) {
		var deltaType string
		if bType == "thinking" {
			deltaType = "thinking_delta"
		} else {
			deltaType = "text_delta"
		}
		blockDeltaEvent := map[string]interface{}{
			"type":  "content_block_delta",
			"index": idx,
			"delta": map[string]interface{}{
				"type": deltaType,
			},
		}
		if bType == "thinking" {
			blockDeltaEvent["delta"].(map[string]interface{})["thinking"] = text
		} else {
			blockDeltaEvent["delta"].(map[string]interface{})["text"] = text
		}
		bdBytes, _ := json.Marshal(blockDeltaEvent)
		fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(bdBytes))
	}

	sendStopBlock := func(w io.Writer, idx int) {
		blockStopEvent := map[string]interface{}{
			"type":  "content_block_stop",
			"index": idx,
		}
		bstBytes, _ := json.Marshal(blockStopEvent)
		fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(bstBytes))
	}

	sendStartEvents := func(w io.Writer) {
		if started {
			return
		}
		started = true
		fmt.Fprintf(w, "event: message_start\ndata: %s\n\n", string(msgStartBytes))
	}

	sendEndEvents := func(w io.Writer) {
		if ended {
			return
		}
		ended = true

		// Xử lý nốt buffer nếu chưa được flush
		if inToolCallBuf {
			bufStr := toolCallBuf.String()
			var parsed bool
			var toolCalls []map[string]interface{}
			var textOutside string

			isXML := strings.Contains(bufStr, "<tools>") || strings.Contains(bufStr, "<tool_use>")
			if isXML {
				toolCalls, textOutside, parsed = parseXMLToolCalls(bufStr)
			} else {
				toolCalls, textOutside, parsed = parseRawJSONToolCall(bufStr)
			}

			if parsed {
				hasToolUse = true
				if textOutside != "" {
					sendDeltaBlock(w, "text", activeBlockIndex, textOutside)
				}

				toolBlockIndexOffset := activeBlockIndex + 1
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
					argsStrVal := string(argsBytes)

					blkIdx := xmlIdx + toolBlockIndexOffset
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
						"index": blkIdx,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": argsStrVal,
						},
					}
					tbdBytes, _ := json.Marshal(toolBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))

					toolBlockStop := map[string]interface{}{
						"type":  "content_block_stop",
						"index": blkIdx,
					}
					tbstBytes, _ := json.Marshal(toolBlockStop)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(tbstBytes))
				}
			} else {
				if bufStr != "" {
					if activeBlockType == "" {
						activeBlockType = "text"
						activeBlockIndex = 0
						sendStartBlock(w, "text", 0)
						activeBlockStarted = true
						thinkingEnded = true
					}
					sendDeltaBlock(w, "text", activeBlockIndex, bufStr)
				}
			}
			inToolCallBuf = false
			toolCallBuf.Reset()
		}

		if activeBlockType == "" {
			activeBlockType = "text"
			activeBlockIndex = 0
			sendStartBlock(w, "text", 0)
			activeBlockStarted = true
			thinkingEnded = true
		}

		// Gửi content_block_stop cho block đang active
		if activeBlockStarted {
			sendStopBlock(w, activeBlockIndex)
		}

		// Gửi content_block_stop cho toàn bộ các tool blocks đã khởi động
		if hasToolUse {
			toolBlockIndexOffset := activeBlockIndex + 1
			for tIdx, sent := range sentToolStart {
				if sent {
					toolStopBlk := map[string]interface{}{
						"type":  "content_block_stop",
						"index": tIdx + toolBlockIndexOffset,
					}
					stopBytes, _ := json.Marshal(toolStopBlk)
					fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(stopBytes))
				}
			}
		}

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
		if err != nil && line == "" {
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

		// 1. Kiểm tra phát hiện Tool Calls chuẩn OpenAI trong choices[0].delta.tool_calls hoặc cấp cao nhất
		var openAIToolCalls []interface{}
		if choices, ok := cfJSON["choices"].([]interface{}); ok && len(choices) > 0 {
			if choiceMap, ok := choices[0].(map[string]interface{}); ok {
				if deltaMap, ok := choiceMap["delta"].(map[string]interface{}); ok {
					if tcList, ok := deltaMap["tool_calls"].([]interface{}); ok {
						openAIToolCalls = tcList
					}
				}
			}
		}
		if len(openAIToolCalls) == 0 {
			if tcList, ok := cfJSON["tool_calls"].([]interface{}); ok {
				openAIToolCalls = tcList
			}
		}

		if len(openAIToolCalls) > 0 {
			hasToolUse = true
			toolBlockIndexOffset := activeBlockIndex + 1
			for _, tc := range openAIToolCalls {
				tcMap, ok := tc.(map[string]interface{})
				if !ok {
					continue
				}

				tIdx := 0
				if val, ok := tcMap["index"].(float64); ok {
					tIdx = int(val)
				} else if val, ok := tcMap["index"].(int); ok {
					tIdx = val
				}

				id, _ := tcMap["id"].(string)
				var name string
				if funcMap, ok := tcMap["function"].(map[string]interface{}); ok {
					name, _ = funcMap["name"].(string)
				}

				if id != "" {
					toolIDs[tIdx] = id
				}
				if name != "" {
					toolNames[tIdx] = name
				}

				var argsDelta string
				if funcMap, ok := tcMap["function"].(map[string]interface{}); ok {
					if argsStr2, ok2 := funcMap["arguments"].(string); ok2 {
						argsDelta = argsStr2
					} else if argsObj, ok2 := funcMap["arguments"].(map[string]interface{}); ok2 {
						argsBytes, _ := json.Marshal(argsObj)
						argsDelta = string(argsBytes)
					}
				}

				// Gửi content_block_start một lần duy nhất khi nhận đủ ID và Name của tool
				if !sentToolStart[tIdx] && toolIDs[tIdx] != "" && toolNames[tIdx] != "" {
					toolBlockStart := map[string]interface{}{
						"type":  "content_block_start",
						"index": tIdx + toolBlockIndexOffset,
						"content_block": map[string]interface{}{
							"type": "tool_use",
							"id":   toolIDs[tIdx],
							"name": toolNames[tIdx],
						},
					}
					tbsBytes, _ := json.Marshal(toolBlockStart)
					fmt.Fprintf(w, "event: content_block_start\ndata: %s\n\n", string(tbsBytes))
					sentToolStart[tIdx] = true
				}

				// Chỉ gửi content_block_delta nếu start block đã được phát
				if argsDelta != "" && sentToolStart[tIdx] {
					toolBlockDelta := map[string]interface{}{
						"type":  "content_block_delta",
						"index": tIdx + toolBlockIndexOffset,
						"delta": map[string]interface{}{
							"type":         "input_json_delta",
							"partial_json": argsDelta,
						},
					}
					tbdBytes, _ := json.Marshal(toolBlockDelta)
					fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))
				}
			}
		}

		// 2. Xử lý phản hồi text thông thường
		token, _ := cfJSON["response"].(string)
		isReasoningToken := false
		if token == "" {
			if choices, ok := cfJSON["choices"].([]interface{}); ok && len(choices) > 0 {
				if choiceMap, ok := choices[0].(map[string]interface{}); ok {
					if deltaMap, ok := choiceMap["delta"].(map[string]interface{}); ok {
						if content, ok := deltaMap["content"].(string); ok && content != "" {
							token = content
						} else if reasoning, ok := deltaMap["reasoning_content"].(string); ok && reasoning != "" {
							token = reasoning
							isReasoningToken = true
						} else if reasoning2, ok := deltaMap["reasoning"].(string); ok && reasoning2 != "" {
							token = reasoning2
							isReasoningToken = true
						}
					}
				}
			}
		}

		if token != "" {
			tokenCount++

			// Lọc và chuyển đổi theo máy trạng thái suy nghĩ
			// Phát hiện bắt đầu suy nghĩ
			if (isReasoningToken || strings.Contains(token, "<think>")) && !thinkingEnded {
				if activeBlockType == "" {
					activeBlockType = "thinking"
					activeBlockIndex = 0
					sendStartBlock(w, "thinking", 0)
					activeBlockStarted = true
				}
			}

			// Nếu token chứa thẻ <think>, bóc tách
			if strings.Contains(token, "<think>") {
				parts := strings.Split(token, "<think>")
				token = ""
				if len(parts) > 1 {
					token = parts[1]
				}
			}

			// Phát hiện kết thúc suy nghĩ
			if strings.Contains(token, "</think>") && !thinkingEnded {
				parts := strings.Split(token, "</think>")
				thinkingPart := parts[0]
				textPart := ""
				if len(parts) > 1 {
					textPart = parts[1]
				}

				if thinkingPart != "" && activeBlockType == "thinking" {
					sendDeltaBlock(w, "thinking", activeBlockIndex, thinkingPart)
				}

				// Kết thúc block suy nghĩ
				sendStopBlock(w, activeBlockIndex)

				// Khởi chạy block text tiếp theo
				activeBlockType = "text"
				activeBlockIndex++
				sendStartBlock(w, "text", activeBlockIndex)
				thinkingEnded = true
				activeBlockStarted = true

				token = textPart
			}

			if token != "" {
				if activeBlockType == "thinking" {
					sendDeltaBlock(w, "thinking", activeBlockIndex, token)
				} else {
					// Nếu chưa khởi chạy block text nào, khởi chạy ngay
					if activeBlockType == "" {
						activeBlockType = "text"
						activeBlockIndex = 0
						sendStartBlock(w, "text", 0)
						activeBlockStarted = true
						thinkingEnded = true
					}

					if inToolCallBuf {
						toolCallBuf.WriteString(token)
						bufStr := toolCallBuf.String()
						
						// Check if the tool call has ended
						hasEnded := false
						isXML := strings.Contains(bufStr, "<tools>") || strings.Contains(bufStr, "<tool_use>")
						if isXML {
							hasEnded = strings.Contains(bufStr, "</tools>") || strings.Contains(bufStr, "</tool_use>")
						} else {
							hasEnded = strings.HasSuffix(strings.TrimSpace(bufStr), "}")
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
								inToolCallBuf = false
								toolCallBuf.Reset()
								
								if textOutside != "" {
									sendDeltaBlock(w, "text", activeBlockIndex, textOutside)
								}
								
								toolBlockIndexOffset := activeBlockIndex + 1
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
									
									blkIdx := xmlIdx + toolBlockIndexOffset
									
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
										"index": blkIdx,
										"delta": map[string]interface{}{
											"type":         "input_json_delta",
											"partial_json": argsStr,
										},
									}
									tbdBytes, _ := json.Marshal(toolBlockDelta)
									fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))
									
									toolBlockStop := map[string]interface{}{
										"type":  "content_block_stop",
										"index": blkIdx,
									}
									tbstBytes, _ := json.Marshal(toolBlockStop)
									fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(tbstBytes))
								}
							}
						}
					} else {
						// Check if the token contains the start of a tool call
						startIdx := -1
						isXML := false
						
						if idx := strings.Index(token, "<tools>"); idx != -1 {
							startIdx = idx
							isXML = true
						} else if idx := strings.Index(token, "<tool_use>"); idx != -1 {
							startIdx = idx
							isXML = true
						} else if idx := strings.Index(token, "{\"name\":"); idx != -1 {
							startIdx = idx
							isXML = false
						} else if idx := strings.Index(token, "{\"name\""); idx != -1 {
							startIdx = idx
							isXML = false
						}
						
						if startIdx != -1 {
							// Stream the text before the tool call
							textBefore := token[:startIdx]
							if textBefore != "" {
								sendDeltaBlock(w, "text", activeBlockIndex, textBefore)
							}
							
							// Start buffering
							inToolCallBuf = true
							toolCallBuf.Reset()
							toolCallBuf.WriteString(token[startIdx:])
							
							// Check if it also has ended in this same token
							bufStr := toolCallBuf.String()
							hasEnded := false
							if isXML {
								hasEnded = strings.Contains(bufStr, "</tools>") || strings.Contains(bufStr, "</tool_use>")
							} else {
								hasEnded = strings.HasSuffix(strings.TrimSpace(bufStr), "}")
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
									inToolCallBuf = false
									toolCallBuf.Reset()
									
									if textOutside != "" {
										sendDeltaBlock(w, "text", activeBlockIndex, textOutside)
									}
									
									toolBlockIndexOffset := activeBlockIndex + 1
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
										
										blkIdx := xmlIdx + toolBlockIndexOffset
										
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
											"index": blkIdx,
											"delta": map[string]interface{}{
												"type":         "input_json_delta",
												"partial_json": argsStr,
											},
										}
										tbdBytes, _ := json.Marshal(toolBlockDelta)
										fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(tbdBytes))
										
										toolBlockStop := map[string]interface{}{
											"type":  "content_block_stop",
											"index": blkIdx,
										}
										tbstBytes, _ := json.Marshal(toolBlockStop)
										fmt.Fprintf(w, "event: content_block_stop\ndata: %s\n\n", string(tbstBytes))
									}
								}
							}
						} else {
							// No tool call start, send token directly
							sendDeltaBlock(w, "text", activeBlockIndex, token)
						}
					}
				}
			}
		}

		if err == io.EOF {
			sendEndEvents(w)
			return false
		}
		return true
	})

	estimatedNeurons := int64(float64(tokenCount) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 10
	}
	h.sm.TrackUsage(c.Request.Context(), accountID, estimatedNeurons)
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
		if err != nil && line == "" {
			if err == io.EOF {
				fmt.Fprint(w, "data: [DONE]\n\n")
			}
			return false
		}

		openAILine, keepGoing := converter.ConvertLine(line)
		if openAILine != "" {
			fmt.Fprint(w, openAILine)
		}

		if err == io.EOF {
			fmt.Fprint(w, "data: [DONE]\n\n")
			return false
		}
		return keepGoing
	})

	estimatedNeurons := int64(float64(converter.tokenCount) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 10
	}
	h.sm.TrackUsage(c.Request.Context(), accountID, estimatedNeurons)
}

// handleStandard định dạng phản hồi JSON thường (Non-stream) chuẩn OpenAI.
func (h *ProxyHandler) handleStandard(c *gin.Context, cfBody io.Reader, accountID, model string) {
	bodyBytes, _ := io.ReadAll(cfBody)
	log.Printf("[Cloudflare Raw Response]: %s", string(bodyBytes))

	// Guard empty response
	if len(bodyBytes) == 0 {
		c.JSON(http.StatusBadGateway, gin.H{"error": "Empty response from Cloudflare"})
		return
	}

	var cfResponse map[string]interface{}
	// Attempt decode; if malformed, repair and retry
	if err := json.Unmarshal(bodyBytes, &cfResponse); err != nil {
		repaired := repairJSON(string(bodyBytes))
		if err2 := json.Unmarshal([]byte(repaired), &cfResponse); err2 != nil {
			log.Printf("[⚠️ Parse Error] original: %v, repaired: %v, raw: %s", err, err2, string(bodyBytes))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "Failed decode Cloudflare response"})
			return
		}
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

	estimatedNeurons := int64(float64(len(aiResponse)/4) * 1.5)
	if estimatedNeurons == 0 {
		estimatedNeurons = 50
	}
	h.sm.TrackUsage(c.Request.Context(), accountID, estimatedNeurons)
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
	var hasStartTag bool
	if strings.Contains(trimmed, "<tools>") {
		startTag = "<tools>"
		endTag = "</tools>"
		hasStartTag = true
	} else if strings.Contains(trimmed, "<tool_use>") {
		startTag = "<tool_use>"
		endTag = "</tool_use>"
		hasStartTag = true
	}

	if !hasStartTag {
		return nil, text, false
	}

	startIdx := strings.Index(trimmed, startTag)
	endIdx := strings.Index(trimmed, endTag)

	var toolsContent string
	var textOutside string

	if startIdx == -1 {
		return nil, text, false
	}

	if endIdx == -1 || endIdx <= startIdx {
		// Bị cụt mất thẻ đóng, parse phần nội dung đã nhận từ thẻ mở đến hết
		toolsContent = trimmed[startIdx+len(startTag):]
		textOutside = trimmed[:startIdx]
		// Sửa lỗi JSON bị cắt cụt bên trong thẻ XML
		toolsContent = repairJSON(toolsContent)
	} else {
		toolsContent = trimmed[startIdx+len(startTag) : endIdx]
		textOutside = trimmed[:startIdx] + trimmed[endIdx+len(endTag):]
	}
	toolsContent = strings.TrimSpace(toolsContent)
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
		// Cố gắng sửa lỗi dòng đơn lẻ bị cụt trước khi unmarshal
		repairedLine := repairJSON(line)
		var item map[string]interface{}
		if err := json.Unmarshal([]byte(repairedLine), &item); err == nil {
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

	// Tìm vị trí của dấu { đầu tiên
	startIdx := strings.Index(trimmed, "{")
	if startIdx == -1 {
		return nil, text, false
	}

	endIdx := strings.LastIndex(trimmed, "}")
	var jsonContent string
	var textOutside string

	if endIdx == -1 || endIdx <= startIdx {
		// Bị cụt mất ngoặc đóng cuối, sử dụng repairJSON
		jsonContent = trimmed[startIdx:]
		jsonContent = repairJSON(jsonContent)
		textOutside = trimmed[:startIdx]
	} else {
		jsonContent = trimmed[startIdx : endIdx+1]
		textOutside = trimmed[:startIdx] + trimmed[endIdx+1:]
	}
	textOutside = strings.TrimSpace(textOutside)

	var item map[string]interface{}
	if err := json.Unmarshal([]byte(jsonContent), &item); err == nil {
		name, _ := item["name"].(string)
		args := item["arguments"]
		if name != "" {
			if args == nil {
				args = map[string]interface{}{}
			}
			id := fmt.Sprintf("call_%d", time.Now().UnixNano())
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

// isPotentialToolCall kiểm tra xem token đầu vào có khả năng bắt đầu cuộc gọi tool hay không.
func isPotentialToolCall(s string) bool {
	if strings.HasPrefix(s, "{") {
		return true
	}
	if strings.HasPrefix(s, "<") {
		if len(s) < len("<tool_use>") {
			return strings.HasPrefix("<tools>", s) || strings.HasPrefix("<tool_use>", s)
		}
		return strings.HasPrefix(s, "<tools") || strings.HasPrefix(s, "<tool_use")
	}
	return false
}

// sendTextDelta gửi sự kiện content_block_delta cho client với text delta tương ứng.
func sendTextDelta(w io.Writer, text string) {
	contentBlockDelta := map[string]interface{}{
		"type":  "content_block_delta",
		"index": 0,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}
	deltaBytes, _ := json.Marshal(contentBlockDelta)
	fmt.Fprintf(w, "event: content_block_delta\ndata: %s\n\n", string(deltaBytes))
}

// repairJSON cố gắng sửa các chuỗi JSON bị cắt cụt bằng cách đóng các dấu ngoặc nháy kép, ngoặc nhọn, ngoặc vuông còn thiếu.
func repairJSON(jsonStr string) string {
	jsonStr = strings.TrimSpace(jsonStr)
	if jsonStr == "" {
		return jsonStr
	}

	var braces []rune
	inQuote := false
	escape := false

	for _, r := range jsonStr {
		if escape {
			escape = false
			continue
		}
		if r == '\\' {
			escape = true
			continue
		}
		if r == '"' {
			inQuote = !inQuote
			continue
		}
		if !inQuote {
			if r == '{' || r == '[' {
				braces = append(braces, r)
			} else if r == '}' || r == ']' {
				if len(braces) > 0 {
					last := braces[len(braces)-1]
					if (r == '}' && last == '{') || (r == ']' && last == '[') {
						braces = braces[:len(braces)-1]
					}
				}
			}
		}
	}

	if inQuote {
		jsonStr += "\""
	}

	for i := len(braces) - 1; i >= 0; i-- {
		if braces[i] == '{' {
			jsonStr += "}"
		} else if braces[i] == '[' {
			jsonStr += "]"
		}
	}

	return jsonStr
}

// parseThinkingTags bóc tách thẻ <think>...</think> ra khỏi văn bản.
func parseThinkingTags(text string) (string, string) {
	startIdx := strings.Index(text, "<think>")
	if startIdx == -1 {
		return "", text
	}
	endIdx := strings.Index(text, "</think>")
	if endIdx == -1 {
		thinking := text[startIdx+len("<think>"):]
		return strings.TrimSpace(thinking), ""
	}
	thinking := text[startIdx+len("<think>"):endIdx]
	content := text[:startIdx] + text[endIdx+len("</think>"): ]
	return strings.TrimSpace(thinking), strings.TrimSpace(content)
}
