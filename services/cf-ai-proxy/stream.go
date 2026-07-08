package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// ============================================================================
// COMPONENT: STREAM CONVERTER
// ============================================================================

// StreamConverter đóng vai trò là một máy trạng thái (State Machine) bóc tách dữ liệu thô
// text/event-stream từ Cloudflare Workers AI để tái cấu trúc về dạng chunk chuẩn OpenAI.
type StreamConverter struct {
	model        string
	isFirstChunk bool
	tokenCount   int64 // Đếm số lượng token đã stream thành công để quy đổi Neurons
}

// NewStreamConverter khởi tạo một StreamConverter mới cho model được yêu cầu.
func NewStreamConverter(model string) *StreamConverter {
	return &StreamConverter{
		model:        model,
		isFirstChunk: true,
		tokenCount:   0,
	}
}

// ConvertLine phân tích từng dòng trong stream từ Cloudflare và chuyển đổi sang chunk format của OpenAI.
// Trả về chuỗi dữ liệu định dạng SSE và một cờ boolean chỉ định có tiếp tục đọc stream nữa không.
func (sc *StreamConverter) ConvertLine(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || !strings.HasPrefix(line, "data:") {
		return "", true
	}

	dataStr := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
	if dataStr == "[DONE]" {
		return "data: [DONE]\n\n", false
	}

	var cfJSON map[string]interface{}
	if err := json.Unmarshal([]byte(dataStr), &cfJSON); err != nil {
		return "", true
	}

	token, _ := cfJSON["response"].(string)
	sc.tokenCount++

	openAIChunk := map[string]interface{}{
		"id":      fmt.Sprintf("chatcmpl-%d", time.Now().UnixNano()),
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   sc.model,
		"choices": []map[string]interface{}{
			{
				"index": 0,
				"delta": func() map[string]interface{} {
					if sc.isFirstChunk {
						sc.isFirstChunk = false
						return map[string]interface{}{"role": "assistant", "content": token}
					}
					return map[string]interface{}{"content": token}
				}(),
				"finish_reason": nil,
			},
		},
	}

	chunkBytes, _ := json.Marshal(openAIChunk)
	return fmt.Sprintf("data: %s\n\n", string(chunkBytes)), true
}
