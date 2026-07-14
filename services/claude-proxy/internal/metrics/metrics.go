package metrics

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"net/http"
)

// Define Prometheus metrics for router behavior.
var (
	CacheHits = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_cache_hits_total",
		Help: "Total number of cache hits for routing decisions",
	})
	CacheMisses = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_cache_misses_total",
		Help: "Total number of cache misses for routing decisions",
	})
	ExternalCalls = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_external_calls_total",
		Help: "Total number of external router service calls",
	})
	ExternalLatency = prometheus.NewHistogram(prometheus.HistogramOpts{
		Name:    "router_external_call_duration_seconds",
		Help:    "Latency of external router calls",
		Buckets: prometheus.ExponentialBuckets(0.01, 2, 10), // 10ms to ~10s
	})

	// New metrics (added)
    HeuristicMatchesTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "router_heuristic_matches_total",
        Help: "Number of routing decisions made via heuristic selection rules",
    })
    HeuristicMissesTotal = prometheus.NewCounter(prometheus.CounterOpts{
        Name: "router_heuristic_misses_total",
        Help: "Number of routing requests that did not match any heuristic rule",
    })
	LlmErrorsTotal = prometheus.NewCounter(prometheus.CounterOpts{
    // Existing counters ...

		Name: "router_llm_errors_total",
		Help: "Total number of LLM classifier errors or FALLBACK decisions",
	})
	FallbackTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_fallback_total",
		Help: "Total number of requests routed via static fallback rule",
	})
	InvalidModelTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_invalid_model_total",
		Help: "Total number of requests with unrecognized swe.* model",
	})
	CacheEvictions = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_cache_evictions_total",
		Help: "Total number of TTL cache entries evicted",
	})
	RoutingDuration = prometheus.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "router_routing_duration_seconds",
		Help:    "Latency of each routing stage",
		Buckets: prometheus.ExponentialBuckets(0.001, 2, 10), // 1ms to ~1s
	}, []string{"stage"})

	// Payload‑too‑large metric
	PayloadTooLargeTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "router_payload_too_large_total",
		Help: "Requests rejected because payload exceeds configured limit",
	})

	// Circuit‑breaker state gauge (0=closed,1=open,2=half‑open)
	CircuitBreakerState = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "router_circuit_breaker_state",
		Help: "LLM circuit breaker state: 0=closed, 1=open, 2=half-open",
	})
)

func init() {
	// Register metrics with Prometheus default registry.
	prometheus.MustRegister(
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
		HeuristicMatchesTotal,
		HeuristicMissesTotal,
	)
}

// ExposeMetrics registers HTTP handler for Prometheus metrics on the given mux.
func ExposeMetrics(mux *http.ServeMux) {
	mux.Handle("/debug/metrics", promhttp.Handler())
}
