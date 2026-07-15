# Multi-LLM SWE Protocol — Go Proxy

A reverse proxy that intercepts Anthropic‑compatible API requests, rewrites the `model` field based on prompt content, and forwards them to an upstream LLM endpoint. Routes requests across multiple LLM backends using keyword matching or an external classifier.

## Quick Start

```sh
go build -o proxy .
./proxy                    # listens on 127.0.0.1:20129 (default)
./proxy --port 8080        # override port via CLI flag
./proxy --help             # show all flags
```

## CLI Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--config` | `config.json` | Path to configuration file |
| `--port` | from config | Override proxy port |
| `--upstream` | from config | Override upstream URL |
| `--api-key` | from config | Override upstream API key |
| `--llm-router` | from config | Override use_llm_router (`true`/`false`) |
| `--help` | — | Show usage |

## Endpoints

| Path | Description |
|------|-------------|
| `/` | Proxy — forwards to upstream after model rewrite |
| `/health` | Liveness probe — always returns `{"status":"ok"}` |
| `/readyz` | Readiness probe — 503 if uninitialized, 200 otherwise |
| `/debug/health` | Upstream connectivity and circuit breaker health check |
| `/debug/metrics` | Prometheus metrics (cache hits/misses, latency, errors, circuit breaker state) |


## Routing

Two modes controlled by `use_llm_router` in `config.json` (or `--llm-router` flag):

- **Keyword router** (default): matches prompt against `keywords` arrays in `semantic_rules`. Zero external calls.
- **LLM router**: sends prompt to an external classifier model. Falls back to keyword matching if classification fails.

Resolution flow: static route → cache lookup → LLM classifier call or keyword match → fallback.

## Configuration

`config.json` defines abstract roles mapped to physical model names:

| Role | Physical Model |
|------|---------------|
| `swe.architect` | `ka.reason` |
| `swe.engineer` | `ka.base` |
| `swe.subagent` | `ka.base` |
| `swe.utility` | Dynamic (doc keyword → ka.docs, else ka.simple) |
| `swe.knowledge` | `ka.docs` |

Client‑side mapping (`config/model_mappings.json`) maps conceptual model names to Claude API model IDs.

### Config Schema Validation

On startup or SIGHUP configuration reload, `config.json` is validated. The process terminates or ignores reload with errors if:
- `upstream_url` is missing or invalid.
- `port` is out of range [1, 65535].
- `router_model` is missing when `use_llm_router` is enabled.
- Any semantic rule is missing its `trigger_model` or `target_model`.

### Cache TTL & Capacity Limits

To prevent memory bloat, routing decisions are cached in a capacity-limited TTL cache:
- **Cache TTL**: Default 30 minutes. Configure via `ROUTER_CACHE_TTL_MINUTES`.
- **Capacity Limits (LRU equivalent)**: Default max 10,000 entries. Configure via `ROUTER_CACHE_MAX_ENTRIES`. When the cache exceeds this size, expired entries are cleaned up first; if none are expired, the oldest or random entries are evicted to maintain a safe memory footprint.
- **Cache Policy**: Only LLM-confirmed resolved decisions are cached. Keywords and fallback routing paths bypass cache to allow automatic self-recovery once external LLM connectivity is restored.


### Runtime reload & Graceful Shutdown

Send `SIGHUP` to the process to reload `config.json` without restarting:
```sh
kill -HUP <pid>
```
If the new configuration file contains invalid syntax or fails validation, the reload is aborted and the active configuration is kept unchanged. During a SIGINT or SIGTERM shutdown sequence, any incoming reload request is safely ignored to prevent race conditions. The shutdown blocks the main thread until all active in-flight requests have completed.

## Logging

Dual structured logging is supported. Logs are printed in human-readable text format and additionally mirrored in structured JSON lines format to `logs/json/info-<date>.jsonl` for machine analysis.

Controlled by environment variables:

- `LOG_INFO` (default `1`): set `0` to silence info logs.
- `LOG_DEBUG` (default `0`): set `1` to enable debug logs.
- `LOG_BUF_SIZE` (default `5000`): channel buffer for async logger.
- `LOG_INFO_PATH`, `LOG_ERROR_PATH`, `LOG_DEBUG_PATH`: override text log paths.
- `LOG_JSON_PATH` (default `logs/json/info-<date>.jsonl`): override JSON lines log path.

Structured JSON log format example:
```json
{"time":"2026-07-06 15:04:05","level":"INFO","msg":"[Router] Resolved swe.utility -> ds/deepseek-v4-flash","request_id":"c651fd27b68593a"}
```

## Circuit Breaker & Security Hardening

### Circuit Breaker

Cascading failures to the external LLM classifier are prevented using a zero-dependency circuit breaker. When the classifier fails or times out for `CIRCUIT_BREAKER_THRESHOLD` consecutive times (default `3`), the circuit transitions to **Open** state, immediately bypassing the LLM classifier and routing via keywords. After `CIRCUIT_BREAKER_RESET_SECONDS` (default `30` seconds), a single probe request is allowed. A success closes the circuit, while a failure re-opens it.

Configuration environment variables:
- `CIRCUIT_BREAKER_THRESHOLD` (default `3`): consecutive failures before opening.
- `CIRCUIT_BREAKER_RESET_SECONDS` (default `30`): cooldown period before probing.

### Security Hardening

- **Payload Size Limit**: Every request body is limited to `MAX_PAYLOAD_BYTES` (default `5<<20` = 5 MiB). Payloads larger than this limit are immediately rejected with `400 Bad Request`.
- **Model Whitelist Warning**: If a request carries a model name starting with `swe.` that has no matching semantic rule configured, a warning log is recorded at level `[Security]` and the `router_invalid_model_total` metric is incremented.

## Prometheus Metrics Examples

Prometheus metrics are served at `/debug/metrics`.

### PromQL Examples

- **Cache Hit Ratio**:
  ```promql
  sum(rate(router_cache_hits_total[5m])) / (sum(rate(router_cache_hits_total[5m])) + sum(rate(router_cache_misses_total[5m])))
  ```
- **LLM Classifier Error Rate**:
  ```promql
  rate(router_llm_errors_total[5m]) / rate(router_external_calls_total[5m])
  ```
- **Average Routing Latency per Stage**:
  ```promql
  sum(rate(router_routing_duration_seconds_sum[5m])) by (stage) / sum(rate(router_routing_duration_seconds_count[5m])) by (stage)
  ```


## Development

```sh
go build -o proxy .        # build binary
go test -v ./...            # run all tests
go test -v -run TestName    # run a single test
go vet ./...                # static analysis
```

## Source Files

| File | Role |
|------|------|
| `main.go` | HTTP server, proxy handler, graceful shutdown |
| `router.go` | `ModelRouter` interface + keyword/LLM router implementations |
| `routing_fsm.go` | FSM for dynamic routing (cache → LLM → keyword → fallback) |
| `config.go` | Config structs + global lookup maps |
| `config_loader.go` | JSON config loader with env var override |
| `logger.go` | Buffered async logger to separate files |
| `metrics.go` | Prometheus counters/histograms |
| `health.go` | `/health` and `/readyz` handlers |
| `flags.go` | CLI flag parsing |
| `middleware.go` | Request logging middleware |

## Repository Structure

```
├── config/
│   ├── model_mappings.json      # Client-side model → API mapping
│   └── phase_routing.json       # Phase-to-model mapping
├── docs/
│   ├── architecture/             # Workflow and proxy architecture
│   ├── policy/                   # Routing policy and optimization
│   ├── adr/                      # Architectural Decision Records
│   └── handoff/                  # Session handoff documents
├── *.go                          # Proxy source (package main)
├── config.json                   # Proxy configuration
└── proxy                         # Built binary
```
