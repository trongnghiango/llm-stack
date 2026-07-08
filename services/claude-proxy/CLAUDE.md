# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```sh
go build -o proxy ./cmd/proxy     # Build to ./proxy binary
go test -v ./...                  # Run all tests
go test -v -run TestName ./cmd/proxy  # Run a single test (e.g. TestRouterCacheMissAndHit)
go vet ./...                      # Static analysis
```

## High‑Level Architecture

This is a **Go reverse proxy** (`module claude-proxy`) that intercepts Anthropic‑compatible API requests, rewrites the `model` field, and forwards them to an upstream endpoint. Its purpose is to route requests across multiple LLM backends based on prompt content.

### Package Layout & Source Files

| Package / Folder | Role & Key Files |
|------------------|------------------|
| `cmd/proxy/` | Entry point of the proxy. Runs HTTP server, graceful shutdown, health endpoints (`health.go`), CLI flags (`flags.go`), and proxy handlers. |
| `internal/config/` | Config validation and JSON loading (`config_loader.go` and `config.go`). |
| `internal/router/` | Core routing FSM logic (`routing_fsm.go`), Cache (`cache.go`), Circuit Breaker (`circuit_breaker.go`), State (`state.go`), and models. |
| `internal/logger/` | Asynchronous, level-aware logger (`logger.go`). Writes logs in Vietnamese using "Vì...nên" format. |
| `internal/metrics/` | Prometheus metrics definitions and HTTP exposition handler (`metrics.go`). |
| `internal/utils/` | Shared HTTPClient and Logging middleware (`utils.go` and `middleware.go`). |

### Two router modes

Controlled by `use_llm_router` in `config.json` (overridable via `USE_LLM_ROUTER` env var):

- **Keyword router** (default, `false`): matches prompt against `keywords` arrays in `semantic_rules`. Zero external calls.
- **LLM router** (`true`): sends prompt to an external classifier model (`router_model`). Falls back to keyword matching if the LLM classifier fails or returns `FALLBACK`.

### FSM resolution flow (`internal/router/routing_fsm.go`)

1. **Static route** — non‑`swe.*` models pass through unchanged, `swe.*` models without dynamic rules resolve to their `target_model` directly.
2. **Cache lookup** — if the same `model:prompt` pair was resolved before, return cached target.
3. **LLM call** (if LLM router enabled) — classifier returns `MINIMAX`, `DEEPSEEK`, or a custom decision → mapped through `decision_map` → `builtinDecisionMap`.
4. **Keyword match** — scans `semantic_rules[model].keywords` for prompt substring matches.
5. **Fallback** — returns the keyword‑less static target for that model.

### Two‑layer model mapping

- **Layer 1 (proxy, `config.json`)**: abstract roles (`swe.architect`, `swe.engineer`, `swe.utility`, `swe.knowledge`) → physical upstream model names (e.g. `nvidia/openai/gpt-oss-120b`, `ds/deepseek-v4-flash`).
- **Layer 2 (client, `config/model_mappings.json`)**: framework conceptual models (`GPT-OSS-120B`, `GLM-5.2`, `DeepSeek`, `MiniMax`) → Claude API model IDs.

### Logging

Controlled by environment variables:

- `LOG_INFO` (default `1`): set `0` to silence info logs.
- `LOG_DEBUG` (default `0`): set `1` to enable debug logs.
- `LOG_BUF_SIZE` (default `5000`): channel buffer size for async writer.
- `LOG_INFO_PATH`, `LOG_ERROR_PATH`, `LOG_DEBUG_PATH`: override log file paths (default: `logs/<level>-<date>.log`).

Routing decisions are logged in Vietnamese with a `Vì...nên` (Because...therefore) structure.

### Metrics

Prometheus metrics at `/debug/metrics` on the proxy port.

### Tests

Tests are localized under each package directory. They use `t.Setenv()`, `t.TempDir()`, and `httptest` for isolation.
- `cmd/proxy/` tests: End-to-end proxy behavior, CLI overrides, server boot/shutdown.
- `internal/router/` tests: Routing FSM states, cache eviction/expiry, circuit breaker behavior.
- `internal/config/` tests: Config loaders and validation.
- `internal/logger/` tests: Logging output formats.
- `internal/metrics/` tests: Metric registration.
- `internal/utils/` tests: Payload limit checks and middleware behavior.
