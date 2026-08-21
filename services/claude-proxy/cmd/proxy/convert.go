package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ─────────────────────────────────────────────
// Types shared across all proxy files
// ─────────────────────────────────────────────

// MessageBlock represents a single content block inside an Anthropic message.
type MessageBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Id        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseId string          `json:"tool_use_id,omitempty"`
	Content   json.RawMessage `json:"content,omitempty"`
}

// Message is a single turn in an Anthropic conversation.
type Message struct {
	Role    string          `json:"role"`
	Content json.RawMessage `json:"content"`
}

// AnthropicTool represents a tool definition requested by the client.
type AnthropicTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

// AnthropicRequest is the subset of request fields the proxy needs to inspect.
type AnthropicRequest struct {
	Model      string          `json:"model"`
	System     json.RawMessage `json:"system,omitempty"`
	Messages   []Message       `json:"messages,omitempty"`
	Tools      json.RawMessage `json:"tools,omitempty"`
	ToolChoice json.RawMessage `json:"tool_choice,omitempty"`
	Stream     *bool           `json:"stream,omitempty"`
}

// ─────────────────────────────────────────────
// Schema-driven argument mapping
// ─────────────────────────────────────────────

// toolInputSchema mirrors the Anthropic JSON Schema for a tool's input_schema field.
type toolInputSchema struct {
	Type       string                     `json:"type"`
	Properties map[string]toolSchemaProp  `json:"properties"`
	Required   []string                   `json:"required"`
}

// toolSchemaProp represents a single property entry in an input_schema.
type toolSchemaProp struct {
	Type        string `json:"type"`
	Description string `json:"description,omitempty"`
}

// mapRawTextToSchema tries to map raw model output into a proper JSON argument
// object using the tool's input_schema. The strategy is:
//  1. If raw is already valid JSON → return as-is (model did the right thing).
//  2. Otherwise, find the first required string property in the schema and wrap
//     the raw text into {"<first_required_string_prop>": "<raw>"}.
//  3. If no schema or no matching property is found → return "{}".
func mapRawTextToSchema(raw string, schema json.RawMessage) string {
	raw = strings.TrimSpace(raw)

	// 1. Already valid JSON — use it directly.
	if json.Valid([]byte(raw)) && (strings.HasPrefix(raw, "{") || strings.HasPrefix(raw, "[")) {
		return raw
	}

	// 2. Parse the input_schema.
	if len(schema) == 0 {
		return "{}"
	}
	var s toolInputSchema
	if err := json.Unmarshal(schema, &s); err != nil || len(s.Properties) == 0 {
		return "{}"
	}

	// Find first required string property.
	for _, reqField := range s.Required {
		if prop, ok := s.Properties[reqField]; ok && prop.Type == "string" {
			b, _ := json.Marshal(map[string]string{reqField: raw})
			return string(b)
		}
	}

	// Fall back to first string property in iteration order (map is random but
	// acceptable as a last resort).
	for field, prop := range s.Properties {
		if prop.Type == "string" {
			b, _ := json.Marshal(map[string]string{field: raw})
			return string(b)
		}
	}

	return "{}"
}


// ─────────────────────────────────────────────
// Tool-spoofing system prompt
// ─────────────────────────────────────────────

const toolSpoofSystemInstruction = "[CRITICAL SYSTEM INSTRUCTION FOR TOOL USE]\n" +
	"You are an AI assistant equipped with specific tools.\n" +
	"When you need to call a tool, you can output a single XML block using the EXACT tool name as the tag (e.g., `<Bash>{\"command\": \"ls\"}</Bash>`) OR use the standard format `<tools>{\"name\": \"exact_tool_name\", \"arguments\": {\"param\": \"value\"}}</tools>`.\n" +
	"Do not write any explanation, markdown, or text before or after the XML block when calling a tool.\n" +
	"When you receive a [Tool Result] in the conversation history, the tool has already been executed. " +
	"Use the result to continue or finish answering the user in plain text without repeating the tool call."

// buildSpoofedSystemPrompt merges existing system content with tool schemas and
// the XML-tool-call instruction so OSS models know how to emit tool calls.
func buildSpoofedSystemPrompt(rawSystem json.RawMessage, tools []AnthropicTool) string {
	var systemStr string

	if len(rawSystem) > 0 {
		if err := json.Unmarshal(rawSystem, &systemStr); err != nil {
			// May be an array of content blocks
			var blocks []MessageBlock
			if err := json.Unmarshal(rawSystem, &blocks); err == nil {
				var sb strings.Builder
				for _, b := range blocks {
					if b.Type == "text" {
						sb.WriteString(b.Text + "\n")
					}
				}
				systemStr = sb.String()
			}
		}
	}

	if systemStr != "" {
		systemStr += "\n\n" + toolSpoofSystemInstruction
	} else {
		systemStr = toolSpoofSystemInstruction
	}

	toolsBytes, _ := json.Marshal(tools)
	systemStr += "\n\nAVAILABLE TOOLS:\n" + string(toolsBytes)
	return systemStr
}

// convertMessagesForSpoofing rewrites messages so that:
//   - tool_use blocks become <tools>{...}</tools> XML text
//   - tool_result blocks become [Tool Result]: ... text
//
// This allows OSS models that do not support native tool-use to read the
// conversation history correctly.
func convertMessagesForSpoofing(messages []Message) []Message {
	converted := make([]Message, 0, len(messages))
	for _, msg := range messages {
		// Plain string content — pass through unchanged.
		var strContent string
		if err := json.Unmarshal(msg.Content, &strContent); err == nil {
			converted = append(converted, msg)
			continue
		}

		// Content block array — flatten to a single string.
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
					sb.WriteString("[Tool Result]:\n" + extractToolResultText(b.Content))
				}
			}
			newBytes, _ := json.Marshal(sb.String())
			converted = append(converted, Message{Role: msg.Role, Content: newBytes})
			continue
		}

		converted = append(converted, msg)
	}
	return converted
}

// extractToolResultText pulls the plain text out of a tool_result content field,
// which may be a string, an array of text blocks, or raw bytes.
func extractToolResultText(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	var blocks []MessageBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		var sb strings.Builder
		for _, b := range blocks {
			if b.Type == "text" {
				sb.WriteString(b.Text + "\n")
			}
		}
		return strings.TrimSpace(sb.String())
	}
	return string(raw)
}
