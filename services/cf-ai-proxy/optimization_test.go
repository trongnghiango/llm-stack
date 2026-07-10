package main

import (
	"testing"
)

func TestRepairJSON(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "JSON hoàn chỉnh",
			input:    `{"name": "Read", "arguments": {"file_path": "/path/to/file.md"}}`,
			expected: `{"name": "Read", "arguments": {"file_path": "/path/to/file.md"}}`,
		},
		{
			name:     "JSON bị cắt cụt giữa chừng ở value string",
			input:    `{"name": "Read", "arguments": {"file_path": "/path/to/fi`,
			expected: `{"name": "Read", "arguments": {"file_path": "/path/to/fi"}}`,
		},
		{
			name:     "JSON bị cắt cụt ở ngoặc nhọn arguments",
			input:    `{"name": "Read", "arguments": {"file_path": "/path/to/file.md"`,
			expected: `{"name": "Read", "arguments": {"file_path": "/path/to/file.md"}}`,
		},
		{
			name:     "JSON mảng bị cắt cụt",
			input:    `[{"name": "Read"`,
			expected: `[{"name": "Read"}]`,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := repairJSON(tc.input)
			if got != tc.expected {
				t.Errorf("Expected: %s, Got: %s", tc.expected, got)
			}
		})
	}
}

func TestIsPotentialToolCall(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		expected bool
	}{
		{"Bắt đầu bằng JSON", "{}", true},
		{"Bắt đầu bằng JSON lửng", "{\"name", true},
		{"Bắt đầu bằng XML tag đầy đủ", "<tools>", true},
		{"Bắt đầu bằng XML tag lửng ngắn", "<t", true},
		{"Bắt đầu bằng XML tag lửng dài", "<tool_u", true},
		{"Ký tự thường", "Here is", false},
		{"Ký tự tag lạ không liên quan", "<some_tag>", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isPotentialToolCall(tc.input)
			if got != tc.expected {
				t.Errorf("For %s, expected %v, got %v", tc.input, tc.expected, got)
			}
		})
	}
}

func TestTolerantParseRawJSONToolCall(t *testing.T) {
	input := `{"name": "Read", "arguments": {"file_path": "/path/to/file`
	res, _, ok := parseRawJSONToolCall(input)
	if !ok {
		t.Fatalf("Expected parseRawJSONToolCall to succeed on truncated JSON")
	}
	if len(res) == 0 {
		t.Fatalf("Expected tool call to be returned")
	}
	funcMap := res[0]["function"].(map[string]interface{})
	if funcMap["name"] != "Read" {
		t.Errorf("Expected tool name 'Read', got %v", funcMap["name"])
	}
	args := funcMap["arguments"].(map[string]interface{})
	if args["file_path"] != "/path/to/file" {
		t.Errorf("Expected file_path '/path/to/file', got %v", args["file_path"])
	}
}

func TestTolerantParseXMLToolCalls(t *testing.T) {
	input := `<tools>{"name": "Write", "arguments": {"file_path": "/path/to/new.txt"`
	res, _, ok := parseXMLToolCalls(input)
	if !ok {
		t.Fatalf("Expected parseXMLToolCalls to succeed on truncated XML/JSON")
	}
	if len(res) == 0 {
		t.Fatalf("Expected tool call to be returned")
	}
	funcMap := res[0]["function"].(map[string]interface{})
	if funcMap["name"] != "Write" {
		t.Errorf("Expected tool name 'Write', got %v", funcMap["name"])
	}
	args := funcMap["arguments"].(map[string]interface{})
	if args["file_path"] != "/path/to/new.txt" {
		t.Errorf("Expected file_path '/path/to/new.txt', got %v", args["file_path"])
	}
}
