package main

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"claude-proxy/internal/router"
	"claude-proxy/internal/utils"
)

// HealthHandler returns a simple liveness check.
func HealthHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// ReadyzHandler returns 200 if the proxy is ready, 503 otherwise.
func ReadyzHandler(w http.ResponseWriter, r *http.Request) {
	s := router.GetState()
	if s.Config.UpstreamURL == "" || s.ModelRouter == nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"not ready"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"ok"}`))
}

// DebugHealthHandler checks upstream connectivity and circuit breaker availability.
func DebugHealthHandler(w http.ResponseWriter, r *http.Request) {
	s := router.GetState()
	if s.Config.UpstreamURL == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"unhealthy","reason":"upstream_url is empty"}`))
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", s.Config.UpstreamURL, nil)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(`{"status":"unhealthy","reason":"failed to create check request"}`))
		return
	}

	resp, err := utils.HTTPClient.Do(req)
	if err != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusServiceUnavailable)
		w.Write([]byte(fmt.Sprintf(`{"status":"unhealthy","reason":"upstream connection failed: %v"}`, err)))
		return
	}
	resp.Body.Close()

	if s.Config.UseLLMRouter {
		if router.IsCircuitBreakerOpen() {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			w.Write([]byte(`{"status":"unhealthy","reason":"llm router circuit breaker is open"}`))
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"status":"healthy"}`))
}
