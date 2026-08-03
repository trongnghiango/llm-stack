package main

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestExtractTextFromSSE(t *testing.T) {
	sseInput := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"id\":\"123\"}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"<tools>{\\\"name\\\": \\\"Write\\\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\", \\\"arguments\\\": {\\\"file_path\\\": \\\"/temp.md\\\"}}</tools>\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	extracted := extractTextFromSSE(sseInput)
	expected := `<tools>{"name": "Write", "arguments": {"file_path": "/temp.md"}}</tools>`
	if extracted != expected {
		t.Fatalf("expected %q, got %q", expected, extracted)
	}
}

func TestExtractTextFromJSON(t *testing.T) {
	// Test Anthropic format
	anthropicJSON := []byte(`{"content":[{"type":"text","text":"hello world"}]}`)
	if txt := extractTextFromJSON(anthropicJSON); txt != "hello world" {
		t.Fatalf("expected 'hello world', got %q", txt)
	}

	// Test OpenAI format
	openaiJSON := []byte(`{"choices":[{"message":{"content":"hello from openai"}}]}`)
	if txt := extractTextFromJSON(openaiJSON); txt != "hello from openai" {
		t.Fatalf("expected 'hello from openai', got %q", txt)
	}
}

func TestEmitSpoofedAnthropicStream_WithTool(t *testing.T) {
	rec := httptest.NewRecorder()
	input := `<tools>{"name": "Write", "arguments": {"file_path": "/temp.md", "content": "hello\n"}}</tools>`

	emitSpoofedAnthropicStream(rec, input)
	body := rec.Body.String()

	if !strings.Contains(body, "event: message_start") {
		t.Error("missing message_start event")
	}
	if !strings.Contains(body, `"type":"tool_use"`) {
		t.Error("missing tool_use content block")
	}
	if !strings.Contains(body, `"name":"Write"`) {
		t.Error("missing tool name Write")
	}
	if !strings.Contains(body, `"type":"input_json_delta"`) {
		t.Error("missing input_json_delta")
	}
	if !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Error("missing stop_reason tool_use")
	}
	if !strings.Contains(body, "event: message_stop") {
		t.Error("missing message_stop event")
	}
	// Verify raw <tools> tag does not appear in the streamed output
	if strings.Contains(body, "<tools>") || strings.Contains(body, "</tools>") {
		t.Error("raw XML tags leaked into output stream")
	}
}

func TestEmitSpoofedAnthropicStream_WithMarkdownAndText(t *testing.T) {
	rec := httptest.NewRecorder()
	input := "Tôi sẽ tạo file.\n<tools>\n```json\n{\"name\": \"Write\", \"arguments\": {\"file_path\": \"/temp.md\", \"content\": \"hello\"}}\n```\n</tools>"

	emitSpoofedAnthropicStream(rec, input)
	body := rec.Body.String()

	if !strings.Contains(body, "Tôi sẽ tạo file.") {
		t.Error("missing leading text")
	}
	if !strings.Contains(body, `"name":"Write"`) {
		t.Error("missing tool name Write")
	}
	if !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Error("missing stop_reason tool_use")
	}
	if strings.Contains(body, "<tools>") || strings.Contains(body, "</tools>") {
		t.Error("raw XML tags leaked into output stream")
	}
}

func TestEmitSpoofedAnthropicStream_NoTool(t *testing.T) {
	rec := httptest.NewRecorder()
	input := "Xin chào, tôi là trợ lý AI."

	emitSpoofedAnthropicStream(rec, input)
	body := rec.Body.String()

	if !strings.Contains(body, "Xin chào, tôi là trợ lý AI.") {
		t.Error("missing text")
	}
	if strings.Contains(body, `"type":"tool_use"`) {
		t.Error("should not contain tool_use")
	}
	if !strings.Contains(body, `"stop_reason":"end_turn"`) {
		t.Error("stop_reason should be end_turn")
	}
}

func TestConvertMessagesForSpoofing(t *testing.T) {
	rawInput := `[
		{"role":"user","content":"Tạo file temp.md"},
		{"role":"assistant","content":[{"type":"text","text":"Tôi sẽ tạo file"},{"type":"tool_use","name":"Write","input":{"file_path":"temp.md","content":"hello"}}]},
		{"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_123","content":"File created successfully"}]}
	]`

	var msgs []Message
	if err := json.Unmarshal([]byte(rawInput), &msgs); err != nil {
		t.Fatalf("failed to unmarshal test messages: %v", err)
	}

	converted := convertMessagesForSpoofing(msgs)
	if len(converted) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(converted))
	}

	// Message 1: plain user string
	var msg1 string
	_ = json.Unmarshal(converted[0].Content, &msg1)
	if msg1 != "Tạo file temp.md" {
		t.Errorf("expected 'Tạo file temp.md', got %q", msg1)
	}

	// Message 2: assistant tool_use converted to <tools> XML
	var msg2 string
	_ = json.Unmarshal(converted[1].Content, &msg2)
	if !strings.Contains(msg2, "<tools>") || !strings.Contains(msg2, `"name": "Write"`) {
		t.Errorf("expected <tools> XML in assistant message, got %q", msg2)
	}

	// Message 3: user tool_result converted to [Tool Result]
	var msg3 string
	_ = json.Unmarshal(converted[2].Content, &msg3)
	if !strings.Contains(msg3, "[Tool Result]") || !strings.Contains(msg3, "File created successfully") {
		t.Errorf("expected [Tool Result] in user message, got %q", msg3)
	}
}

func TestEmitSpoofedAnthropicStream_WithRawNewlinesInJSON(t *testing.T) {
	rec := httptest.NewRecorder()
	// Raw literal newline inside the description string
	input := "Tôi sẽ review code.\n<tools>{\"name\": \"Agent\", \"arguments\": {\"description\": \"Review code\nLine 2\", \"prompt\": \"Task details\"}}</tools>"

	emitSpoofedAnthropicStream(rec, input)
	body := rec.Body.String()

	if !strings.Contains(body, `"name":"Agent"`) {
		t.Errorf("expected tool name Agent in output, got: %s", body)
	}
	if !strings.Contains(body, `"stop_reason":"tool_use"`) {
		t.Errorf("expected stop_reason tool_use, got: %s", body)
	}
}

func TestCleanPromptForRouting(t *testing.T) {
	raw := "<system-reminder>\n# claudeMd documentation\nSome doc rules\n</system-reminder>\nReview clean code of proxy"
	cleaned := strings.TrimSpace(cleanPromptForRouting(raw))
	if cleaned != "Review clean code of proxy" {
		t.Fatalf("expected 'Review clean code of proxy', got %q", cleaned)
	}
}


