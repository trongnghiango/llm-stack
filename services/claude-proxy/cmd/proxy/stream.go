package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"

	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"
	"claude-proxy/internal/utils"
)

// ─────────────────────────────────────────────
// SSE / JSON text extraction
// ─────────────────────────────────────────────

// extractTextFromSSE reads a full SSE body and concatenates all text/thinking
// delta values (Anthropic format) or delta.content/reasoning_content values
// (OpenAI format) into a single string.
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
			logger.Debugf("[SSE Extract] Skipping malformed chunk: %.80s err=%v", jsonStr, err)
			continue
		}
		// Anthropic content_block_delta / thinking_delta
		if evtType, _ := evt["type"].(string); evtType == "content_block_delta" || evtType == "thinking_delta" {
			if delta, ok := evt["delta"].(map[string]interface{}); ok {
				if txt, ok := delta["text"].(string); ok {
					fullText.WriteString(txt)
				} else if thinking, ok := delta["thinking"].(string); ok {
					fullText.WriteString(thinking)
				}
			}
		}
		// OpenAI delta.content / delta.reasoning_content / delta.reasoning
		if choices, ok := evt["choices"].([]interface{}); ok && len(choices) > 0 {
			if choice, ok := choices[0].(map[string]interface{}); ok {
				if delta, ok := choice["delta"].(map[string]interface{}); ok {
					if content, ok := delta["content"].(string); ok {
						fullText.WriteString(content)
					} else if r, ok := delta["reasoning_content"].(string); ok {
						fullText.WriteString(r)
					} else if r, ok := delta["reasoning"].(string); ok {
						fullText.WriteString(r)
					}
				}
			}
		}
	}
	return fullText.String()
}

// extractTextFromJSON extracts text from a single JSON response body.
// Handles both Anthropic (content[].text / thinking) and OpenAI
// (choices[0].message.content / reasoning_content) formats.
func extractTextFromJSON(bodyBytes []byte) string {
	var generic map[string]interface{}
	if err := json.Unmarshal(bodyBytes, &generic); err != nil {
		return string(bodyBytes)
	}

	// Anthropic: content[].text or content[].thinking
	if content, ok := generic["content"].([]interface{}); ok && len(content) > 0 {
		var sb strings.Builder
		for _, item := range content {
			if block, ok := item.(map[string]interface{}); ok {
				if txt, ok := block["text"].(string); ok {
					sb.WriteString(txt)
				} else if thinking, ok := block["thinking"].(string); ok {
					sb.WriteString(thinking)
				}
			}
		}
		if sb.Len() > 0 {
			return sb.String()
		}
	}

	// OpenAI: choices[0].message.{content,reasoning_content,reasoning}
	if choices, ok := generic["choices"].([]interface{}); ok && len(choices) > 0 {
		if choice, ok := choices[0].(map[string]interface{}); ok {
			if msg, ok := choice["message"].(map[string]interface{}); ok {
				if txt, ok := msg["content"].(string); ok && txt != "" {
					return txt
				}
				if r, ok := msg["reasoning_content"].(string); ok && r != "" {
					return r
				}
				if r, ok := msg["reasoning"].(string); ok && r != "" {
					return r
				}
			}
		}
	}

	return string(bodyBytes)
}

// ─────────────────────────────────────────────
// SSE stream forwarding with JSON validation
// ─────────────────────────────────────────────

// setSseHeaders sets the standard response headers for an SSE stream.
func setSseHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Type", contentTypeSSE)
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
}

// forwardStreamWithValidation pipes an SSE response from upstream to the client,
// skipping (or terminating on) malformed JSON chunks depending on STRICT_SCHEMA_VALIDATION.
func forwardStreamWithValidation(w http.ResponseWriter, r *http.Request, resp *http.Response) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		_, _ = io.Copy(w, resp.Body)
		return
	}

	scanner := bufio.NewScanner(resp.Body)
	buf := make([]byte, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	strictMode := os.Getenv("STRICT_SCHEMA_VALIDATION") == "true"
	ctx := r.Context()

	for scanner.Scan() {
		// Check if the client disconnected (e.g. Claude Code closed or user pressed Ctrl+C)
		if err := ctx.Err(); err != nil {
			logger.Warnf("[Stream] Client disconnected mid-stream: %v", err)
			_, _ = io.Copy(io.Discard, resp.Body)
			return
		}

		line := scanner.Bytes()
		shouldForward := true

		if dIdx := bytes.Index(line, []byte("data:")); dIdx >= 0 {
			jsonPayload := bytes.TrimSpace(line[dIdx+5:])
			if len(jsonPayload) > 0 && string(jsonPayload) != "[DONE]" && !json.Valid(jsonPayload) {
				metrics.SseValidationErrorsTotal.Inc()
				chunk := string(jsonPayload)
				if len(chunk) > 150 {
					chunk = chunk[:150] + "..."
				}

				if strictMode {
					logger.Errorf("[Stream] STRICT: invalid JSON chunk, closing: %s", chunk)
					w.Write([]byte("event: error\ndata: {\"error\":{\"type\":\"stream_parse_error\",\"message\":\"Strict mode terminated connection due to invalid schema structure from Upstream.\"}}\n\n"))
					flusher.Flush()
					_, _ = io.Copy(io.Discard, resp.Body) // Drain body to allow connection reuse
					return
				}

				logger.Warnf("[Stream] Skipping malformed JSON chunk: %s", chunk)
				if _, err := w.Write([]byte("event: ping\ndata: {\"warning\":\"malformed_json_skipped_gracefully\"}\n\n")); err != nil {
					_, _ = io.Copy(io.Discard, resp.Body)
					return
				}
				shouldForward = false
			}
		}

		if shouldForward {
			if _, err := w.Write(line); err != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				return
			}
			if _, err := w.Write([]byte("\n")); err != nil {
				_, _ = io.Copy(io.Discard, resp.Body)
				return
			}
		}
		flusher.Flush()
	}

	if err := scanner.Err(); err != nil {
		if err != io.EOF && !errors.Is(err, context.Canceled) {
			logger.Errorf("[Stream] Scanner error: %v", err)
		}
	}
}

// ─────────────────────────────────────────────
// Header utilities
// ─────────────────────────────────────────────

// droppedHeaders lists HTTP headers that must not be forwarded to upstream.
var droppedHeaders = map[string]bool{
	"content-length":    true,
	"content-encoding":  true,
	"transfer-encoding": true,
}

// copySafeHeaders copies response headers to w, excluding hop-by-hop headers.
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

// isSSEResponse returns true when the response Content-Type is text/event-stream.
func isSSEResponse(resp *http.Response) bool {
	for _, v := range resp.Header.Values("Content-Type") {
		if strings.HasPrefix(v, "text/event-stream") {
			return true
		}
	}
	return false
}

// writeUpstreamResponseHeaders copies safe upstream headers to w and returns
// whether the upstream response is an SSE stream.
func writeUpstreamResponseHeaders(w http.ResponseWriter, resp *http.Response) (isStream bool) {
	for key, values := range resp.Header {
		low := strings.ToLower(key)
		switch low {
		case "content-length", "content-encoding", "transfer-encoding", "connection", "keep-alive":
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
	return
}

// readBodyOrError reads the full response body with a max size limit to prevent OOM,
// writing a 502 error to w on failure.
func readBodyOrError(w http.ResponseWriter, resp *http.Response, label string) ([]byte, bool) {
	// Use a 15MB limit when buffering responses in memory for spoofing/fake-streaming.
	const maxMemoryResponseBytes = 15 << 20
	limitedReader := io.LimitReader(resp.Body, maxMemoryResponseBytes)

	b, err := io.ReadAll(limitedReader)
	if err != nil {
		logger.Errorf("[%s] Failed to read upstream body: %v", label, err)
		http.Error(w, "Failed to read upstream response", http.StatusBadGateway)
		_, _ = io.Copy(io.Discard, resp.Body) // Ensure connection is drained
		return nil, false
	}

	// Check if we hit the limit without reaching EOF
	if int64(len(b)) >= maxMemoryResponseBytes {
		logger.Errorf("[%s] Upstream payload exceeds %d bytes limit, dropping to prevent OOM.", label, maxMemoryResponseBytes)
		http.Error(w, "Upstream response payload too large", http.StatusBadGateway)
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil, false
	}

	return b, true
}

// ─────────────────────────────────────────────
// Upstream request building
// ─────────────────────────────────────────────

// buildUpstreamRequest constructs and returns the HTTP request to send to the
// upstream model server, injecting API-key and Anthropic-Version headers.
func buildUpstreamRequest(r *http.Request, targetURL string, payload []byte) (*http.Request, error) {
	upstreamReq, err := http.NewRequestWithContext(r.Context(), "POST", targetURL, bytes.NewBuffer(payload))
	if err != nil {
		return nil, err
	}

	copySafeHeaders(upstreamReq.Header, r.Header)

	upstreamReq.ContentLength = int64(len(payload))
	upstreamReq.Header.Set("Content-Length", utils.IntToStr(len(payload)))
	upstreamReq.Header.Set("Anthropic-Version", anthropicVersion)
	upstreamReq.Header.Set("Content-Type", contentTypeJSON)

	if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
		upstreamReq.Header.Set("X-Request-ID", reqID)
	}

	return upstreamReq, nil
}
