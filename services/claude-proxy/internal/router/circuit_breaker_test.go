package router

import (
	"testing"
	"time"
)

func TestCircuitBreaker_ClosedToOpen(t *testing.T) {
	cb := newCircuitBreaker(3, 30*time.Second)

	// Initially closed — all calls allowed
	for i := 0; i < 3; i++ {
		if !cb.Allow() {
			t.Fatalf("expected Allow()=true in closed state (attempt %d)", i)
		}
		cb.RecordFailure()
	}

	// After threshold failures, circuit should be open
	if cb.State() != "open" {
		t.Fatalf("expected state=open after threshold failures, got %q", cb.State())
	}
	if cb.Allow() {
		t.Fatal("expected Allow()=false in open state")
	}
}

func TestCircuitBreaker_OpenToHalfOpen(t *testing.T) {
	cb := newCircuitBreaker(1, 10*time.Millisecond) // very short reset for test
	cb.Allow()
	cb.RecordFailure() // open the circuit

	if cb.State() != "open" {
		t.Fatalf("expected open state, got %q", cb.State())
	}

	// Wait for reset timeout
	time.Sleep(20 * time.Millisecond)

	// First Allow() after timeout should transition to half-open and return true
	if !cb.Allow() {
		t.Fatal("expected Allow()=true after reset timeout (half-open probe)")
	}
	if cb.State() != "half-open" {
		t.Fatalf("expected half-open state, got %q", cb.State())
	}
	// Second Allow() while half-open should be blocked
	if cb.Allow() {
		t.Fatal("expected Allow()=false for second call in half-open state")
	}
}

func TestCircuitBreaker_HalfOpenSuccess(t *testing.T) {
	cb := newCircuitBreaker(1, 10*time.Millisecond)
	cb.Allow()
	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // probe — transitions to half-open
	cb.RecordSuccess()

	if cb.State() != "closed" {
		t.Fatalf("expected closed after successful probe, got %q", cb.State())
	}
	if !cb.Allow() {
		t.Fatal("expected Allow()=true in closed state after recovery")
	}
}

func TestCircuitBreaker_HalfOpenFailure(t *testing.T) {
	cb := newCircuitBreaker(1, 10*time.Millisecond)
	cb.Allow()
	cb.RecordFailure()
	time.Sleep(20 * time.Millisecond)
	cb.Allow() // probe
	cb.RecordFailure() // probe fails — re-open

	if cb.State() != "open" {
		t.Fatalf("expected open after probe failure, got %q", cb.State())
	}
}

func TestCircuitBreaker_SuccessResetsCounter(t *testing.T) {
	cb := newCircuitBreaker(3, 30*time.Second)
	cb.Allow()
	cb.RecordFailure() // 1 failure
	cb.Allow()
	cb.RecordFailure() // 2 failures
	cb.Allow()
	cb.RecordSuccess() // success — should reset
	cb.Allow()
	cb.RecordFailure() // 1 failure again
	cb.Allow()
	cb.RecordFailure() // 2 failures

	// Still closed — hasn't reached threshold since last reset
	if cb.State() != "closed" {
		t.Fatalf("expected closed state, got %q", cb.State())
	}
}

func TestCircuitBreaker_EnvConfig(t *testing.T) {
	t.Setenv("CIRCUIT_BREAKER_THRESHOLD", "2")
	t.Setenv("CIRCUIT_BREAKER_RESET_SECONDS", "60")
	cb := newCircuitBreaker(5, 10*time.Second) // defaults overridden by env
	if cb.threshold != 2 {
		t.Fatalf("expected threshold=2 from env, got %d", cb.threshold)
	}
	if cb.resetTimeout != 60*time.Second {
		t.Fatalf("expected resetTimeout=60s from env, got %v", cb.resetTimeout)
	}
}
