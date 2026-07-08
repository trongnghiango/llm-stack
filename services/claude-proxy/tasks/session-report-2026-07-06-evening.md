# Session Report — 2026-07-06 (Evening)

**Thời gian:** 2026-07-06T21:01 → 21:15 ICT  
**Bối cảnh:** Nhận bàn giao từ commit `662aa27` (atomic.Value refactor)

---

## 1. Phân tích (Analysis)

Sau khi đọc handoff document và review toàn bộ codebase post-662aa27, phát hiện **6 vấn đề** chưa được xử lý:

### Correctness (🔴)

| ID | Vị trí | Vấn đề |
|----|--------|--------|
| C1 | `routing_fsm.go:100,108` | **Cache poisoning**: kết quả keyword/fallback được cache vĩnh viễn sau khi LLM timeout/error → lần sau không retry LLM |
| C2 | `router.go:37`, `routing_fsm.go:20` | **cfg/maps bất đối xứng**: `llmRouter.cfg` copy lúc startup; `getState()` trả maps từ snapshot mới nhất → trong cửa sổ rất hẹp sau SIGHUP, 2 nguồn dữ liệu từ thế hệ config khác nhau |

### Operability (🟡)

| ID | Vị trí | Vấn đề |
|----|--------|--------|
| O1 | `utils.go:21` | **Cache unbounded**: `sync.Map` grow vô hạn, không có TTL/eviction → leak RAM với unique prompts |
| O2 | `middleware.go:13` | **No request tracing**: log từ concurrent requests không có ID chung → khó debug routing sai |

### Test coverage (🟢)

| ID | Vị trí | Vấn đề |
|----|--------|--------|
| T1 | `config_loader_test.go` | Không có test cho `reloadConfig()` (SIGHUP path) |
| T2 | `router_test.go` | `callLLMRouter()` chỉ được test gián tiếp; 7 response shapes chưa có unit test riêng |

---

## 2. Kế hoạch (Plan)

Xem implementation plan đã được user approve:  
`/home/ka/.gemini/antigravity/brain/6a4ca0ed-5503-4316-ae27-3cabd7ab6a76/implementation_plan.md`

**Thứ tự thực hiện:** C1 → C2 → O1 → O2 → T1 → T2

**Nguyên tắc thiết kế:**
- Không thêm thư viện ngoài
- Backward-compatible với tất cả tests hiện có
- Configurable qua env var (không hardcode)

---

## 3. Thống nhất (Agreement)

User đã **approve** implementation plan lúc 21:06 ICT.

---

## 4. Tasks

```
[x] Fix C1: Bỏ cache.Store ở stateKeywordMatch và stateFallbackRule
[x] Fix C2: Snapshot cfg từ getState() tại đầu resolveDynamic, bỏ tham số cfg
[x] Fix O1: Tạo cache.go với ttlCache (TTL 30 phút, evict 5 phút)
[x] Fix O2: Generate/forward X-Request-ID trong middleware + main
[x] Fix T1: TestReloadConfig_Valid + TestReloadConfig_InvalidFile
[x] Fix T2: TestCallLLMRouter (7 cases) + TestCallLLMRouter_Timeout
[x] Verify: build + vet + test -race
[x] Commit + handoff
```

---

## 5. Triển khai (Implementation)

### Fix C1 — `routing_fsm.go`

```diff
 case stateKeywordMatch:
     if resolvedModel != "" {
-        decisionCache.Store(cacheKey, resolvedModel)
+        // Do not cache keyword-match results: if LLM previously timed out/errored,
+        // we want subsequent requests to retry the LLM, not persist the degraded result.
         cur = stateDone
     }
 case stateFallbackRule:
     resolvedModel = getStaticTarget(originalModel)
-    decisionCache.Store(cacheKey, resolvedModel)
+    // Do not cache static fallback — same reasoning as keyword match above.
     cur = stateDone
```

### Fix C2 — `router.go` + `routing_fsm.go`

- Bỏ tham số `cfg Config` khỏi `resolveRecursive(cfg, ...)` → `resolveRecursive(...)`
- Bỏ tham số `cfg Config` khỏi `resolveDynamic(cfg, ...)` → `resolveDynamic(...)`
- Đầu `resolveDynamic`: `st := getState(); cfg := st.config` — cả config lẫn maps từ cùng 1 atomic snapshot

### Fix O1 — `cache.go` (file mới)

```go
type ttlCache struct {
    mu  sync.RWMutex
    m   map[string]cacheEntry
    ttl time.Duration
}
// Load(key) (string, bool)  — trả về trực tiếp, không cần type assertion
// Store(key, value string)  — set entry với expiresAt = now + ttl
// evictLoop(interval)       — goroutine background, evict stale entries
```

- Default TTL: `30 min`, configurable: `ROUTER_CACHE_TTL_MINUTES`
- Eviction interval: `5 min`
- Xóa `var decisionCache sync.Map` khỏi `utils.go`

### Fix O2 — `middleware.go` + `main.go`

```go
// middleware.go — ServeHTTP
reqID := r.Header.Get("X-Request-ID")
if reqID == "" {
    reqID = fmt.Sprintf("%016x", rand.Uint64())
    r.Header.Set("X-Request-ID", reqID)
}
w.Header().Set("X-Request-ID", reqID)
logger.Infof("... req=%s", reqID)

// main.go — forwardRequest
if reqID := r.Header.Get("X-Request-ID"); reqID != "" {
    upstreamReq.Header.Set("X-Request-ID", reqID)
}
```

### Fix T1 — `config_loader_test.go`

- `TestReloadConfig_Valid`: Load config A → ghi config B → `reloadConfig()` → verify state B
- `TestReloadConfig_InvalidFile`: Load config tốt → `reloadConfig()` với file không tồn tại và file JSON lỗi → verify state không đổi, không panic

### Fix T2 — `router_test.go`

`TestCallLLMRouter` — 7 table-driven subtests:

| Case | Expected |
|------|----------|
| Anthropic `content[0].text` = `"MINIMAX"` | `"MINIMAX"` |
| OpenAI `choices[0].message.content` = `"DEEPSEEK"` | `"DEEPSEEK"` |
| OpenAI `reasoning_content` contains `"MINIMAX"` | `"MINIMAX"` |
| OpenAI `reasoning` contains `"DEEPSEEK"` | `"DEEPSEEK"` |
| Empty content `""` | `"FALLBACK"` |
| HTTP 500 | `"FALLBACK"` |
| Malformed JSON | `"FALLBACK"` |

`TestCallLLMRouter_Timeout` — upstream unreachable (port 1 refused) → `"FALLBACK"`

---

## 6. Testing

```
go build -o proxy .     → OK
go vet ./...            → OK (clean)
go test -race -count=1 ./...  → ok  claude-proxy  1.076s
```

**Test count:** 22 (trước) → **30** (sau, +8 tests mới)

| Test mới | Kết quả |
|----------|---------|
| `TestReloadConfig_Valid` | ✅ PASS |
| `TestReloadConfig_InvalidFile` | ✅ PASS |
| `TestCallLLMRouter/Anthropic_content_array` | ✅ PASS |
| `TestCallLLMRouter/OpenAI_choices_message_content` | ✅ PASS |
| `TestCallLLMRouter/OpenAI_reasoning_content_fallback` | ✅ PASS |
| `TestCallLLMRouter/OpenAI_reasoning_field_fallback` | ✅ PASS |
| `TestCallLLMRouter/Empty_content_returns_FALLBACK` | ✅ PASS |
| `TestCallLLMRouter/HTTP_500_returns_FALLBACK` | ✅ PASS |
| `TestCallLLMRouter/Malformed_JSON_returns_FALLBACK` | ✅ PASS |
| `TestCallLLMRouter_Timeout` | ✅ PASS |

Race detector: **clean** (không có race condition mới)

---

## 7. Files thay đổi

| File | Loại | Thay đổi |
|------|------|---------|
| `cache.go` | **[NEW]** | `ttlCache` implementation (~110 LOC) |
| `routing_fsm.go` | MODIFY | Bỏ 2× `decisionCache.Store`, snapshot cfg, đơn giản hóa Load call |
| `router.go` | MODIFY | Bỏ tham số `cfg` khỏi `resolveRecursive` và callers |
| `utils.go` | MODIFY | Xóa `decisionCache sync.Map` và import `sync` |
| `middleware.go` | MODIFY | Thêm X-Request-ID generation + log |
| `main.go` | MODIFY | Forward X-Request-ID lên upstream |
| `config_loader_test.go` | MODIFY | +2 tests: reload valid/invalid |
| `router_test.go` | MODIFY | +8 tests: callLLMRouter + timeout |
| `proxy_test.go` | MODIFY | Cập nhật cache reset từ `sync.Map{}` → `ttlCache` |

---

## 8. Commit

```
fix: cache poisoning, cfg consistency, ttl cache, request tracing, test coverage
```

**Git hash:** (xem commit sau khi push)

---

## Còn lại

| Item | Trạng thái | Ghi chú |
|------|-----------|---------|
| Real-world validation với `use_llm_router: true` | ⚠️ Chưa | Cần upstream thật để test LLM routing path |
| Benchmark `ttlCache` vs `sync.Map` | ⏸️ Skip | Không cần thiết hiện tại |
| `/debug/cache` endpoint để inspect cache | 💡 Optional | Nice-to-have cho observability |
