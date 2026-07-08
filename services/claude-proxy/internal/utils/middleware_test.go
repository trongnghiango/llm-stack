package utils

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"claude-proxy/internal/logger"
)

func TestLoggingMiddleware(t *testing.T) {
	logger.SetupTestLogger(t)

	// Setup a simple handler to be wrapped
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		w.Write([]byte("ok"))
	})

	mw := &loggingMiddleware{handler: handler}

	// 1. Check generating X-Request-ID
	req := httptest.NewRequest("POST", "/test", bytes.NewBufferString("test body"))
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	resp := w.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusAccepted {
		t.Errorf("expected 202, got %d", resp.StatusCode)
	}

	reqID := resp.Header.Get("X-Request-ID")
	if reqID == "" {
		t.Errorf("expected generated X-Request-ID in response header")
	}

	// 2. Check forwarding existing X-Request-ID
	req2 := httptest.NewRequest("POST", "/test", bytes.NewBufferString("test body"))
	req2.Header.Set("X-Request-ID", "custom-id-123")
	w2 := httptest.NewRecorder()

	mw.ServeHTTP(w2, req2)
	resp2 := w2.Result()
	defer resp2.Body.Close()

	if resp2.Header.Get("X-Request-ID") != "custom-id-123" {
		t.Errorf("expected custom-id-123, got %s", resp2.Header.Get("X-Request-ID"))
	}
}

func TestLoggingMiddleware_PayloadLimit(t *testing.T) {
	logger.SetupTestLogger(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	mw := &loggingMiddleware{handler: handler}

	// Set limit to 5 bytes
	t.Setenv("MAX_PAYLOAD_BYTES", "5")

	req := httptest.NewRequest("POST", "/test", strings.NewReader("longer than 5 bytes"))
	w := httptest.NewRecorder()

	mw.ServeHTTP(w, req)

	// Since we wrap r.Body inside ServeHTTP, let's try reading the body in the handler
	var readErr error
	handlerWithRead := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, 100)
		_, readErr = r.Body.Read(buf)
	})
	mwWithRead := &loggingMiddleware{handler: handlerWithRead}

	mwWithRead.ServeHTTP(w, req)
	if readErr == nil {
		t.Errorf("expected error reading body when size limit exceeded")
	}
}
