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
// Tool-spoofing system prompt
// ─────────────────────────────────────────────

const toolSpoofSystemInstruction = "[CRITICAL SYSTEM INSTRUCTION FOR TOOL USE]\n" +
	"You are an AI assistant equipped with specific tools.\n" +
	"When you need to call a tool, output ONLY a single XML block formatted exactly like this:\n" +
	"<tools>{\"name\": \"exact_tool_name\", \"arguments\": {\"param\": \"value\"}}</tools>\n" +
	"Do not write any explanation, markdown, or text before or after the XML block when calling a tool.\n" +
	"When you receive a [Tool Result] in the conversation history, the tool has already been executed. " +
	"Use the result to continue or finish answering the user in plain text without repeating the tool call."

// buildSpoofedSystemPrompt merges existing system content with tool schemas and
// the XML-tool-call instruction so OSS models know how to emit tool calls.
func buildSpoofedSystemPrompt(rawSystem json.RawMessage, rawTools json.RawMessage) string {
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

	toolsBytes, _ := json.Marshal(rawTools)
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
