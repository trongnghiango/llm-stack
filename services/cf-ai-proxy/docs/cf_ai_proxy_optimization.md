# Optimization Proposal: Low-Latency Stream Peeking & Fault-Tolerant Tool Parsing

## 1. Objectives
- **Minimize Latency (TTFT)**: Eliminate the 15-character buffering delay for standard text completions in SSE streams.
- **Fault-Tolerant Tool Parsing**: Support parsing and execution of truncated (incomplete) tool calls resulting from `max_tokens` exhaustion or abrupt connection terminations.
- **Graceful Error Recovery**: Recover from JSON or XML validation failures gracefully during SSE stream termination.

---

## 2. Proposed Changes

### A. Heuristic Stream Peeking
Instead of blindly buffering the first 15 characters, the stream reader will peek at the first non-empty token:
1. If the trimmed buffer starts with `<` (potential XML tag) or `{` (potential JSON object), continue buffering to capture the complete tool invocation.
2. If it starts with any other character, immediately flush the buffer and bypass future buffering (direct pass-through).

### B. JSON Repair Engine
A lightweight, linear-time scanner to balance unclosed quotes, braces `{`, and brackets `[`:
- Count active open braces and quotes.
- Append missing closing quotes and brackets from inside out to restore syntax validity.

### C. Tolerant XML Parser
Enhance `parseXMLToolCalls` to process partial contents if `</tools>` or `</tool_use>` is missing (e.g., due to truncation):
- Fallback to parsing up to the end of the string.
- Apply the JSON Repair Engine on the nested arguments string before unmarshaling.

---

## 3. Implementation Plan
1. Implement `repairJSON` helper in `handler.go`.
2. Update `parseXMLToolCalls` and `parseRawJSONToolCall` to use `repairJSON` and support partial/truncated parses.
3. Update `handleAnthropicStream` to perform **Heuristic Peeking** on the first token.
4. Verify correctness via automated tests.
