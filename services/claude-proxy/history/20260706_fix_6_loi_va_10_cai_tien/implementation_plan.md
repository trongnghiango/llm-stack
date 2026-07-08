# Implementation Plan — 10 Improvement Proposals

## Phân tích & Phân loại

Sau khi review toàn bộ codebase (commit `04c80da`), tôi phân loại 10 đề xuất theo **giá trị thực / rủi ro / phù hợp kiến trúc**:

---

## 🟢 TIER A — Implement (rõ ràng, không tranh cãi)

### #1 · Config Schema Validation

**Hiện trạng:** `JSONConfigLoader.Load()` chấp nhận bất kỳ JSON hợp lệ nào. Nếu thiếu `upstream_url` hoặc `port = 0`, proxy chạy nhưng mọi request đều fail.

**Quyết định:** Validation bằng Go struct logic — **không dùng JSON Schema library**. Lý do: Go struct validation tự nhiên, zero external deps, dễ test, output error message rõ ràng.

**Thay đổi:**

#### [MODIFY] [config_loader.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/config_loader.go)
Thêm hàm `validateConfig(cfg Config) error` kiểm tra:
- `upstream_url` không rỗng và parse được
- `port` trong khoảng [1, 65535]
- `router_model` không rỗng khi `use_llm_router = true`
- Không có `SemanticRule` nào có `trigger_model` hoặc `target_model` rỗng

Gọi `validateConfig` trong `Load()` **trước khi** store vào `globalState`. Nếu fail → trả về `error`, caller (`main()` và `reloadConfig()`) xử lý phù hợp.

#### [MODIFY] [config_loader_test.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/config_loader_test.go)
Thêm `TestValidateConfig` với table-driven cases (missing upstream, invalid port, missing router_model when LLM enabled, empty trigger_model in semantic rule, etc.).

---

### #5 · Bổ sung Prometheus Metrics

**Hiện trạng:** Có `cache_hits_total`, `cache_misses_total`, `external_calls_total`, `external_call_duration_seconds`. Thiếu: latency theo từng routing stage, error counters, fallback counter, cache evictions.

**Thay đổi:**

#### [MODIFY] [metrics.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/metrics.go)
Thêm:
```go
llmErrorsTotal   Counter   // LLM classifier returned error/FALLBACK
fallbackTotal    Counter   // requests routed via static fallback  
invalidModelTotal Counter  // requests with unrecognized model field
cacheEvictions   Counter   // entries evicted from ttlCache
routingDuration  HistogramVec{label: "stage"}  // latency per stage
```

#### [MODIFY] [routing_fsm.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/routing_fsm.go)
Instrument `resolveDynamic()` per stage, `routingDuration.WithLabelValues("cache|llm|keyword|fallback").Observe(...)`.

#### [MODIFY] [cache.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/cache.go)
Thêm `cacheEvictions.Inc()` trong `evict()` mỗi lần xóa entry.

#### [MODIFY] [main.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/main.go)
Tăng `invalidModelTotal` khi model parse fail hoặc model là `swe.*` không có rule.

---

### #7 · Graceful Shutdown cải tiến

**Hiện trạng:** SIGHUP và SIGTERM/SIGINT dùng chung 1 goroutine select loop — nếu cả hai đến gần nhau, `reloadConfig()` có thể chạy sau khi server đã vào Shutdown sequence.

**Thay đổi:**

#### [MODIFY] [main.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/main.go)
- Tách SIGHUP handler và SIGTERM/SIGINT handler vào 2 goroutine độc lập với 1 `shutdownOnce sync.Once` cườ lại
- SIGHUP goroutine kiểm tra `shutdownOnce` trước khi gọi `reloadConfig()`
- Thêm log rõ ràng khi bắt đầu drain in-flight requests

---

### #8 · Security Hardening

**Hiện trạng:** Không có giới hạn payload size. Model field được accept bất kỳ string nào.

**Thay đổi:**

#### [MODIFY] [main.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/main.go)
- `http.MaxBytesReader` wrap `r.Body` với limit configurable: `MAX_PAYLOAD_BYTES` env var (default `5<<20` = 5 MiB)
- Nếu model là `swe.*` nhưng không có rule → log `[Security]` warning + tăng `invalidModelTotal`
- Payload > limit: trả `400 Bad Request` với body `{"error":"request too large"}`

> [!IMPORTANT]
> **Không block model ngoài whitelist** — proxy phải transparent. Chỉ log warning và forward.

---

### #9 · Mở rộng Test Coverage

#### [MODIFY] [config_loader_test.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/config_loader_test.go)
Fuzz test native Go (`testing.F`):
```go
func FuzzConfigLoader(f *testing.F) {
    f.Add([]byte(`{"upstream_url":"http://x","port":1}`))
    f.Fuzz(func(t *testing.T, data []byte) {
        // must not panic regardless of input
    })
}
```

#### [NEW] [circuit_breaker_test.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/circuit_breaker_test.go)
Table-driven tests cho 3 states: Closed, Open, HalfOpen transitions (phụ thuộc #3).

---

### #10 · Cập nhật README

#### [MODIFY] [README.md](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/README.md)
Bổ sung:
- Cache behavior (TTL, what gets cached/not)
- Circuit breaker states và fallback behavior
- JSON log format và path
- Prometheus PromQL examples cho cache hit rate, LLM error rate
- Security: payload limit, model warning
- Env vars mới: `MAX_PAYLOAD_BYTES`, `CIRCUIT_BREAKER_THRESHOLD`, `CIRCUIT_BREAKER_RESET_SECONDS`, `LOG_JSON_PATH`

---

## 🟡 TIER B — Implement với điều kiện

### #3 · Circuit Breaker (zero-dependency)

> [!WARNING]
> **Không dùng `sony/gobreaker`** — thêm external dep không cần thiết. Codebase chủ định zero non-prometheus deps. Implementation đơn giản ~60 LOC là đủ.

#### [NEW] [circuit_breaker.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/circuit_breaker.go)
```
states: Closed → Open (sau N failures) → HalfOpen (sau resetTimeout) → Closed/Open

threshold:    CIRCUIT_BREAKER_THRESHOLD env (default 3)
resetTimeout: CIRCUIT_BREAKER_RESET_SECONDS env (default 30)
```

#### [MODIFY] [router.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/router.go)
Wrap `callLLMRouter` với circuit breaker. Nếu Open → return `"FALLBACK"` ngay, không gọi HTTP.

---

### #4 · JSON Structured Logging (dual-mode)

> [!WARNING]
> **Không đổi hoàn toàn sang JSON** — text logs hiện tại dễ đọc khi dev. Dual-mode: cùng 1 message → ghi cả text và JSON.

#### [MODIFY] [logger.go](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/logger.go)
- Thêm `jsonTarget *logTarget` (file `logs/json/info-<date>.jsonl`)
- Trong `run()`, mỗi info/error message → encode JSON line song song
- JSON format: `{"time":"2006-01-02T15:04:05Z","level":"INFO","msg":"...","request_id":"abc"}`
- `request_id` extract từ `req=<id>` nếu có trong message
- `LOG_JSON_PATH` env var override path

> [!NOTE]
> Chỉ info và error được JSON-ified. Debug logs giữ text-only.

---

## 🔴 TIER C — Không thực hiện

### #2 · LRU Cache (hashicorp/golang-lru)

**Lý do từ chối:**
- Vừa implement `ttlCache` (commit `94c247b`) — clean, zero-dep, tested
- TTL eviction đủ cho use case này; prompt unique nên LRU và TTL không khác biệt đáng kể
- Thêm `hashicorp/golang-lru` chỉ để replace 100 LOC đang hoạt động tốt = tech debt

**Thay thế:** Thêm `cache_evictions_total` counter vào `ttlCache` hiện có (thuộc #5).

### #6 · OpenAPI / Swagger

**Lý do từ chối:**
- Proxy pass-through Anthropic API — client biết format từ Anthropic docs rồi
- Không có routes mới đáng document ngoài `/health`, `/readyz`, `/debug/metrics`
- Auto-generate OpenAPI từ Go handlers không trivial và tạo maintenance burden
- Giá trị thực gần như = 0 cho use case internal proxy

---

## Thứ tự thực hiện

```
Group 1 — Foundation:    #1 (validation) → #8 (security)
Group 2 — Observability: #5 (metrics)    → #4 (JSON logs)
Group 3 — Resilience:    #3 (circuit breaker) → #9 (CB tests)
Group 4 — Polish:        #7 (shutdown)   → #9 (fuzz test) → #10 (docs)
```

~8 files modified, 2 files mới (`circuit_breaker.go`, `circuit_breaker_test.go`)

---

## Open Questions

> [!IMPORTANT]
> **#8 Security model:** Khi model không trong whitelist `swe.*`, proxy forward thẳng. Bạn muốn:
> - **A (recommended)** Chỉ log warning, forward bình thường (transparent proxy)
> - **B** Block + trả 400 nếu model không có rule
> - **C** Block chỉ các model `swe.*` không có rule (cho phép forward raw model names)

> [!IMPORTANT]
> **#8 Payload limit:** 5 MiB có phù hợp không? Nếu forward ảnh (base64), có thể cần lớn hơn. Đề xuất: configurable qua `MAX_PAYLOAD_BYTES`, default 5 MiB.

> [!NOTE]
> **#4 JSON log request_id:** Chỉ có trong middleware access log. Router decisions sẽ không có `request_id` trong JSON. Acceptable?

---

## Verification Plan

### Automated Tests
```sh
go build -o proxy .
go test -race -count=1 ./...          # 30 → ~42+ tests
go vet ./...
go test -fuzz=FuzzConfigLoader -fuzztime=10s ./...
```

### Manual Checks
- Start proxy, send request → verify JSON log xuất hiện song song với text log
- Simulate 3 LLM failures → verify circuit breaker opens → requests route via keyword
- Send payload > 5 MiB → verify 400 với message rõ ràng
- Send SIGHUP sau SIGTERM đến nhanh → verify không crash


## Bối cảnh

Sau refactor lớn (commit `662aa27`), codebase đang clean (22 tests pass, race-free, vet OK).
6 vấn đề mới được phát hiện qua review: 2 correctness bugs, 2 operability gaps, 2 test coverage gaps.

---

## Proposed Changes

### Fix 1 — Cache poisoning từ LLM fallback timeout/error

**File:** [`routing_fsm.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/routing_fsm.go#L55-L80)

**Vấn đề:** Khi `callLLMRouter` timeout hoặc error → trả về `"FALLBACK"` → FSM fallthrough sang `stateKeywordMatch` → kết quả keyword/fallback được **cache vĩnh viễn**. Lần sau cùng prompt không thử lại LLM nữa.

**Fix:** Chỉ `decisionCache.Store()` khi decision đến từ LLM thành công. Kết quả keyword match và static fallback **không được cache** (chúng đã O(1) và không cần LLM call).

```diff
 case stateCallLLM:
     decision := callLLMRouter(cfg, originalModel, prompt, systemPrompt)
     if decision == "FALLBACK" || decision == "" {
         cur = stateKeywordMatch
     } else {
         resolvedModel = resolveDecision(originalModel, decision)
         if resolvedModel == "FALLBACK" {
             cur = stateKeywordMatch
         } else {
-            decisionCache.Store(cacheKey, resolvedModel)
+            decisionCache.Store(cacheKey, resolvedModel) // ✅ chỉ cache khi LLM thành công
             cur = stateDone
         }
     }
 case stateKeywordMatch:
     ...
     if resolvedModel != "" {
-        decisionCache.Store(cacheKey, resolvedModel)
+        // ❌ Không cache kết quả keyword match — LLM timeout không nên tạo permanent cache
         cur = stateDone
     }
 case stateFallbackRule:
     resolvedModel = getStaticTarget(originalModel)
-    decisionCache.Store(cacheKey, resolvedModel)
+    // ❌ Không cache static fallback
     cur = stateDone
```

> [!IMPORTANT]
> Cache hiện tại là `sync.Map` (unbounded). Sau fix này, chỉ LLM-confirmed decisions được persist. Cache key vẫn là `originalModel + ":" + fullPrompt` — nếu về sau thêm TTL, chỉ cần thay đổi ở đây.

---

### Fix 2 — `cfg` bất đối xứng sau SIGHUP reload

**File:** [`router.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/router.go#L20-L34), [`routing_fsm.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/routing_fsm.go#L20)

**Vấn đề:** `llmRouter.cfg` và `keywordRouter.cfg` được copy tại thời điểm `NewModelRouter()`. Sau SIGHUP, `config_loader.go` tạo `NewModelRouter(newCfg)` rồi store vào `globalState` — router instance mới, nhưng **`resolveRecursive` nhận `cfg` từ lúc gọi**, không phải từ snapshot hiện tại. Điều này đúng — vấn đề thực sự là `callLLMRouter` dùng `cfg.UpstreamURL`, `cfg.RouterModel`, `cfg.UpstreamAPIKey` từ lúc `Resolve()` được gọi, còn `resolveDynamic` gọi `getState()` để lấy maps — cả hai đều từ cùng một snapshot nên thực ra **nhất quán**.

**Phân tích thêm:** Sau khi trace kỹ, `llmRouter.Resolve()` → `resolveRecursive(r.cfg, ...)` → `resolveDynamic(cfg, ...)` sử dụng `cfg` được truyền vào cho `callLLMRouter`, trong khi `getState()` trong `resolveDynamic` đọc maps từ snapshot mới nhất. Bất đối xứng xảy ra **giữa 2 SIGHUP** nếu một request đang xử lý khi reload xảy ra giữa chừng. Tuy nhiên, Go's `atomic.Value` đảm bảo reader thấy một snapshot nhất quán — `r.cfg` và `getState()` maps có thể từ 2 thế hệ config khác nhau nếu reload xảy ra giữa request.

**Fix đúng nhất:** Thay vì truyền `cfg` qua stack, lấy `getState().config` tại đầu `resolveDynamic` → cả config lẫn maps từ cùng 1 snapshot.

```diff
 func resolveDynamic(cfg Config, originalModel, prompt string, useLLM bool) string {
+    // Snapshot toàn bộ state một lần để cfg và maps nhất quán
+    st := getState()
+    cfg = st.config
     if !isDynamicModel(originalModel) {
         ...
     }
-    st := getState()
     ...
```

**Đồng thời** bỏ tham số `cfg Config` ra khỏi `resolveRecursive` và `resolveDynamic` vì không cần nữa, thay bằng lấy trực tiếp từ snapshot.

---

### Fix 3 — `decisionCache` không có TTL / bounded size

**File:** [`routing_fsm.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/routing_fsm.go), file mới `cache.go`

**Vấn đề:** `sync.Map` grow vô hạn. Prompt unique → leak RAM dần theo thời gian. Không có cách evict.

**Fix:** Implement simple **time-based expiry** trong wrapper `ttlCache`:

```go
// cache.go
type cacheEntry struct {
    value     string
    expiresAt time.Time
}

type ttlCache struct {
    mu  sync.RWMutex
    m   map[string]cacheEntry
    ttl time.Duration
}

func (c *ttlCache) Load(key string) (string, bool) { ... }
func (c *ttlCache) Store(key, value string)        { ... }
func (c *ttlCache) StartEviction(interval time.Duration) { ... } // goroutine evict entries
```

- Default TTL: **30 phút** (configurable qua `ROUTER_CACHE_TTL_MINUTES` env var)
- Eviction chạy mỗi 5 phút trong goroutine background
- `decisionCache` đổi từ `sync.Map` sang `*ttlCache`

> [!NOTE]
> Không dùng thư viện ngoài để tránh dependency. Implementation đơn giản ~50 LOC, đủ cho use case này.

---

### Fix 4 — Request-ID tracing

**Files:** [`middleware.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/middleware.go), [`main.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/main.go)

**Vấn đề:** Mỗi request không có ID nhận diện xuyên suốt. Log từ nhiều concurrent requests trộn lẫn, khó trace khi routing sai.

**Fix:**
1. Trong `loggingMiddleware.ServeHTTP`: nếu `X-Request-ID` header rỗng thì generate `fmt.Sprintf("%x", rand.Uint64())`.
2. Forward `X-Request-ID` lên upstream trong `forwardRequest`.
3. Log `requestID` ở đầu `handleProxy` (Infof level).

```go
// middleware.go — thêm vào ServeHTTP
reqID := r.Header.Get("X-Request-ID")
if reqID == "" {
    reqID = fmt.Sprintf("%016x", rand.Uint64())
    r.Header.Set("X-Request-ID", reqID)
}
w.Header().Set("X-Request-ID", reqID)
```

> [!NOTE]
> `rand.Uint64()` (non-crypto) đủ cho request correlation, không cần `crypto/rand`.

---

### Fix 5 — Test SIGHUP reload path

**File:** [`config_loader_test.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/config_loader_test.go)

**Thêm 2 test cases:**

```go
func TestReloadConfig_Valid(t *testing.T) {
    // Write config A → Load → verify state A
    // Write config B (khác port/model) → reloadConfig() → verify state B
}

func TestReloadConfig_InvalidFile(t *testing.T) {
    // Load valid config → set configPath = invalid file → reloadConfig()
    // Verify: state không đổi (vẫn là config cũ), không panic
}
```

---

### Fix 6 — Unit test `callLLMRouter` response shapes

**File:** [`router_test.go`](file:///home/ka/Repos/github.com/trongnghiango/multi-llm-swe-protocol/router_test.go)

**Thêm table-driven test `TestCallLLMRouter`** với mock HTTP server:

| Case | Response Shape | Expected Decision |
|------|---------------|-------------------|
| Anthropic `content[0].text` | `{"content":[{"text":"MINIMAX"}]}` | `"MINIMAX"` |
| OpenAI `choices[0].message.content` | `{"choices":[{"message":{"content":"DEEPSEEK"}}]}` | `"DEEPSEEK"` |
| Reasoning fallback `reasoning_content` | `{"choices":[{"message":{"content":null,"reasoning_content":"...MINIMAX..."}}]}` | `"MINIMAX"` |
| Empty content | `{"content":[{"text":""}]}` | `"FALLBACK"` |
| HTTP 500 | status 500 | `"FALLBACK"` |
| Timeout (server hangs 10s) | — | `"FALLBACK"` (8s timeout) |
| Malformed JSON | `not-json` | `"FALLBACK"` |

---

## Thứ tự thực hiện

```
Fix 1 → Fix 2 → cache.go (Fix 3) → Fix 4 → Fix 5 → Fix 6
```

Fix 1 và 2 không phụ thuộc nhau, có thể làm song song. Fix 3 yêu cầu đổi kiểu `decisionCache` — làm sau Fix 1/2 để tránh merge conflict.

---

## Verification Plan

### Automated Tests
```sh
go build -o proxy .               # Build OK
go test -race -count=1 ./...      # Tất cả tests pass (bao gồm tests mới)
go vet ./...                      # Clean
```

### Regression Checks
- `TestRouterCacheMissAndHit` vẫn pass (cache hit path chỉ xảy ra với LLM-confirmed results)
- `TestRouterDecisionMapping/BuiltinFallback` vẫn pass (FALLBACK decision → builtin map → cached)
- Race detector clean với `go test -race`

### Metrics
- Cache hit/miss counters (`router_cache_hits_total`, `router_cache_misses_total`) vẫn hoạt động đúng sau khi đổi sang `ttlCache`

---

## Open Questions

> [!IMPORTANT]
> **TTL mặc định cache:** Đề xuất 30 phút. Cùng prompt+model trong 30 phút sẽ không gọi LLM lại. Bạn muốn giá trị khác không?

> [!NOTE]
> **Fix 2 scope:** Sau phân tích kỹ, vấn đề bất đối xứng chỉ xảy ra trong khoảng thời gian rất ngắn (một request đang giữa `Resolve()` khi SIGHUP xảy ra). Atomic snapshot vẫn nhất quán ở mức từng đọc — chỉ có thể có chênh lệch nếu `r.cfg` (từ router cũ) và `getState()` (maps mới). Fix vẫn nên làm vì đơn giản hóa code và đảm bảo 100% nhất quán.
