package metrics

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestExposeMetrics(t *testing.T) {
	mux := http.NewServeMux()
	ExposeMetrics(mux)
	req := httptest.NewRequest("GET", "/debug/metrics", nil)
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Errorf("expected 200 from metrics endpoint, got %d", w.Code)
	}
}

func TestMetricsRegistration(t *testing.T) {
	// Verify that our custom metrics are registered
	metrics := []prometheus.Collector{
		CacheHits,
		CacheMisses,
		ExternalCalls,
		ExternalLatency,
		LlmErrorsTotal,
		FallbackTotal,
		InvalidModelTotal,
		CacheEvictions,
		RoutingDuration,
		PayloadTooLargeTotal,
		CircuitBreakerState,
	}

	for _, m := range metrics {
		err := prometheus.Register(m)
		if err == nil {
			t.Errorf("Expected metric to be already registered, but got no error")
			prometheus.Unregister(m) // clean up if it wasn't registered (unlikely in test)
		} else {
			if _, ok := err.(prometheus.AlreadyRegisteredError); !ok {
				t.Errorf("Expected AlreadyRegisteredError, got: %v", err)
			}
		}
	}
}
