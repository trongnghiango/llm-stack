package utils

import (
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"claude-proxy/internal/logger"
)

// loggingMiddleware logs every request with method, path, status code, and duration.
// It also ensures every request carries an X-Request-ID for cross-cutting trace correlation.
type loggingMiddleware struct {
	handler http.Handler
}

// NewLoggingMiddleware creates a new loggingMiddleware.
func NewLoggingMiddleware(next http.Handler) http.Handler {
	return &loggingMiddleware{handler: next}
}

func (m *loggingMiddleware) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	// Ensure request carries a trace ID. Generate one if client did not supply it.
	reqID := r.Header.Get("X-Request-ID")
	if reqID == "" {
		reqID = fmt.Sprintf("%016x", rand.Uint64())
		r.Header.Set("X-Request-ID", reqID)
	}
	// Echo the ID back so callers can correlate client↔proxy↔upstream.
	w.Header().Set("X-Request-ID", reqID)

	start := time.Now()
	lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
	m.handler.ServeHTTP(lrw, r)
	duration := time.Since(start)
	logger.Infof("[Request] %s %s -> %d (%dms) req=%s", r.Method, r.URL.Path, lrw.statusCode, duration.Milliseconds(), reqID)
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}

func (lrw *loggingResponseWriter) Flush() {
	if f, ok := lrw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (lrw *loggingResponseWriter) Unwrap() http.ResponseWriter {
	return lrw.ResponseWriter
}
