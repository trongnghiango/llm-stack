# Tasks — 10 Proposals & 6 Fixes Implementation

## Group 1 — Foundation
- `[x]` #1: validateConfig() in config_loader.go + TestValidateConfig
- `[x]` #8: MaxBytesReader payload limit + unrecognized model warning in middleware.go & main.go

## Group 2 — Observability
- `[x]` #5a: New metrics (llmErrorsTotal, fallbackTotal, invalidModelTotal, cacheEvictions, routingDuration, payloadTooLargeTotal, circuitBreakerState) in metrics.go
- `[x]` #5b: Instrument routing_fsm.go per stage latency + cacheEvictions in cache.go
- `[x]` #4: Dual structured JSON logging in logger.go using standard library JSON format

## Group 3 — Resilience
- `[x]` #3a: zero-dependency circuit_breaker.go (new file)
- `[x]` #3b: Integrate circuit breaker checks and records inside resolveDynamic in routing_fsm.go
- `[x]` #9a: circuit_breaker_test.go (new file)

## Group 4 — Polish & Verification
- `[x]` #7: Graceful shutdown & isolated SIGHUP/SIGTERM signal handlers in main.go
- `[x]` #9b: FuzzConfigLoader in config_loader_test.go
- `[x]` #10: README.md update covering configuration schema, cache, circuit breaker, logging, security, and PromQL queries.

## Bug Fixes
- `[x]` Fix 1: Do not cache keyword/fallback results in routing FSM.
- `[x]` Fix 2: Thread-safety atomic configuration snapshot in resolveDynamic.
- `[x]` Fix 3: Upgraded cache to `hashicorp/golang-lru/v2/expirable` for strict LRU + TTL eviction.
- `[x]` Fix 4: Generate, trace, and forward X-Request-ID in loggingMiddleware and upstream client.
- `[x]` Fix 5: Write unit tests for SIGHUP config reload path in config_loader_test.go.
- `[x]` Fix 6: Write table-driven unit tests for callLLMRouter in router_test.go.

## Total Coverage Target
- `[x]` Achieved **86.0%** statement coverage with unit tests passing successfully.
