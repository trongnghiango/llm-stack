package main

import "time"

// ============================================================================
// HẰNG SỐ CẤU HÌNH HỆ THỐNG
// ============================================================================

const (
	// MaxNeuronsPerAccount là hạn mức tối đa của 1 tài khoản Cloudflare Free trong ngày (10,000 Neurons).
	MaxNeuronsPerAccount = 10000

	// HandoffThreshold là ngưỡng chặn trên (95%) để kích hoạt cơ chế Handoff (bàn giao)
	// chủ động chuyển giao session sang tài khoản mới một cách an toàn.
	HandoffThreshold = 9500
)

// ============================================================================
// DOMAIN MODELS (CẤU TRÚC DỮ LIỆU THỰC THỂ)
// ============================================================================

// CFAccount lưu trữ cấu hình và trạng thái động của một tài khoản Cloudflare trong RAM.
type CFAccount struct {
	AccountID          string    `json:"account_id"`
	APIToken           string    `json:"-"`
	IsActive           bool      `json:"is_active"`
	CurrentNeuronsUsed int64     `json:"current_neurons_used"`
	NextRetry          time.Time `json:"next_retry"`
}

// OpenAIRequest định nghĩa cấu trúc payload của OpenAI Chat Completion Request.
type OpenAIRequest struct {
	Model       string        `json:"model"`
	Messages    []interface{} `json:"messages"`
	Tools       []interface{} `json:"tools,omitempty"`       // Hỗ trợ Function/Tool Calling
	ToolChoice  interface{}   `json:"tool_choice,omitempty"` // Hỗ trợ cấu hình bắt buộc/tự chọn tool
	Stream      *bool         `json:"stream,omitempty"`
	MaxTokens   *int          `json:"max_tokens,omitempty"`
	Temperature *float64      `json:"temperature,omitempty"` // Hỗ trợ cấu hình độ sáng tạo
	TopP        *float64      `json:"top_p,omitempty"`
	Stop        []string      `json:"stop,omitempty"` // Hỗ trợ chuỗi dừng sinh
	User        string        `json:"user"`
}

// AnthropicContentBlock đại diện cho một khối nội dung dạng text trong request của Anthropic.
type AnthropicContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

// AnthropicMessage định nghĩa cấu trúc một tin nhắn trong hội thoại của Anthropic.
type AnthropicMessage struct {
	Role    string      `json:"role"`
	Content interface{} `json:"content"` // Có thể là chuỗi hoặc mảng các AnthropicContentBlock/tool_use/tool_result
}

// AnthropicRequest định nghĩa cấu trúc payload của Anthropic Messages Request.
type AnthropicRequest struct {
	Model         string             `json:"model"`
	Messages      []AnthropicMessage `json:"messages"`
	System        interface{}        `json:"system,omitempty"`
	Tools         interface{}        `json:"tools,omitempty"`       // Hỗ trợ Function/Tool Calling
	ToolChoice    interface{}        `json:"tool_choice,omitempty"` // Hỗ trợ bắt buộc/tự chọn tool
	MaxTokens     *int               `json:"max_tokens,omitempty"`
	Temperature   *float64           `json:"temperature,omitempty"` // Độ sáng tạo
	TopP          *float64           `json:"top_p,omitempty"`
	TopK          *int               `json:"top_k,omitempty"`
	StopSequences []string           `json:"stop_sequences,omitempty"` // Chuỗi dừng sinh
	Stream        *bool              `json:"stream,omitempty"`
	User          string             `json:"user,omitempty"`
}
