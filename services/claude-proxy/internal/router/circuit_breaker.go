package router

import (
	"os"
	"strconv"
	"sync"
	"time"

	"claude-proxy/internal/logger"
	"claude-proxy/internal/metrics"
)

// cbState represents the circuit breaker state.
type cbState int

const (
	cbClosed   cbState = iota // normal operation — calls allowed
	cbOpen                    // failing — calls blocked, return FALLBACK immediately
	cbHalfOpen                // recovering — one probe call allowed
)

// LlmCircuitBreaker protects the LLM classifier from cascading failures.
var LlmCircuitBreaker *circuitBreaker

// IsCircuitBreakerOpen reports whether the LLM circuit breaker is currently open.
func IsCircuitBreakerOpen() bool {
	if LlmCircuitBreaker == nil {
		return false
	}
	return LlmCircuitBreaker.State() == "open"
}

// circuitBreaker protects the LLM classifier from cascading failures.
// When the classifier fails threshold times consecutively, the circuit opens
// and all calls immediately return FALLBACK. After resetTimeout, one probe
// call is allowed; success closes the circuit, failure re-opens it.
type circuitBreaker struct {
	mu           sync.Mutex
	state        cbState
	failures     int
	threshold    int
	resetTimeout time.Duration
	lastFailure  time.Time
}

// newCircuitBreaker creates a circuit breaker with the given settings.
// Reads CIRCUIT_BREAKER_THRESHOLD and CIRCUIT_BREAKER_RESET_SECONDS env vars
// to allow runtime configuration.
func newCircuitBreaker(threshold int, resetTimeout time.Duration) *circuitBreaker {
	if v := os.Getenv("CIRCUIT_BREAKER_THRESHOLD"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			threshold = n
		}
	}
	if v := os.Getenv("CIRCUIT_BREAKER_RESET_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			resetTimeout = time.Duration(n) * time.Second
		}
	}
	return &circuitBreaker{
		state:        cbClosed,
		threshold:    threshold,
		resetTimeout: resetTimeout,
	}
}

// Allow reports whether the caller should attempt the protected operation.
// In Open state, it transitions to HalfOpen after resetTimeout and allows
// one probe; otherwise it blocks.
func (cb *circuitBreaker) Allow() bool {
	// Update Prometheus gauge based on state before allowing.
	// 0=closed, 1=open, 2=half-open
	switch cb.state {
	case cbClosed:
		metrics.CircuitBreakerState.Set(0)
	case cbOpen:
		metrics.CircuitBreakerState.Set(1)
	case cbHalfOpen:
		metrics.CircuitBreakerState.Set(2)
	}

	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case cbClosed:
		return true
	case cbOpen:
		if time.Since(cb.lastFailure) > cb.resetTimeout {
			cb.state = cbHalfOpen
			return true // probe call
		}
		return false
	case cbHalfOpen:
		return false // only one probe at a time
	}
	return false
}

// RecordSuccess resets the circuit breaker to Closed state.
func (cb *circuitBreaker) RecordSuccess() {
	// Success → closed state
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.state = cbClosed
	cb.failures = 0
	// Update Prometheus gauge to closed (0)
	metrics.CircuitBreakerState.Set(0)
}

// RecordFailure increments the failure counter and opens the circuit if
// the threshold is reached.
func (cb *circuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.failures++
	cb.lastFailure = time.Now()
	if cb.failures >= cb.threshold {
		cb.state = cbOpen
		logger.Errorf("[CircuitBreaker] Opened after %d consecutive failures", cb.failures)
	}
}

// State returns the current circuit breaker state as a string (for logging/metrics).
func (cb *circuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	switch cb.state {
	case cbClosed:
		return "closed"
	case cbOpen:
		return "open"
	case cbHalfOpen:
		return "half-open"
	}
	return "unknown"
}
