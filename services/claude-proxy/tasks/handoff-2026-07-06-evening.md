# Handoff — Git Commit `94c247b`

## Tổng quan

Session tối 2026-07-06: review post-662aa27, phát hiện và fix 6 vấn đề (2 correctness, 2 operability, 2 test coverage), tăng test count 22→30.

---

## Kiến trúc thay đổi

### `cache.go` (mới) — TTL-based routing cache

```go
type ttlCache struct {
    mu  sync.RWMutex
    m   map[string]cacheEntry   // key = originalModel + ":" + prompt
    ttl time.Duration           // default 30 min
}
// Load(key) (string, bool)
// Store(key, value string)
// evictLoop(5 min)           // goroutine background
```

- Thay thế `sync.Map` unbounded cũ trong `utils.go`
- Configurable: `ROUTER_CACHE_TTL_MINUTES` (default 30)
- **Quan trọng:** Chỉ LLM-confirmed decisions được cache. Keyword match và static fallback KHÔNG được cache (tránh poisoning sau LLM timeout).

### `routing_fsm.go` — cfg/maps snapshot nhất quán

```go
func resolveDynamic(originalModel, prompt string, useLLM bool) string {
    st := getState()
    cfg := st.config    // cfg và maps từ cùng 1 atomic snapshot
    ...
}
```

`resolveRecursive` và `resolveDynamic` không còn nhận tham số `cfg Config` — lấy trực tiếp từ `getState()`.

### `middleware.go` — X-Request-ID tracing

```
client → proxy (generate reqID nếu không có) → upstream
         ↓ log: [Yêu cầu] POST /v1/messages -> 200 (12ms) req=<id>
         ↓ header response: X-Request-ID: <id>
```

---

## File thay đổi (10 files)

| File | Thay đổi |
|------|---------|
| `cache.go` (mới) | `ttlCache` + `initDecisionCache()` + background eviction |
| `utils.go` | Xóa `decisionCache sync.Map` và `sync` import |
| `routing_fsm.go` | Bỏ tham số cfg, snapshot từ getState(), bỏ cache.Store ở keyword/fallback |
| `router.go` | Bỏ tham số cfg khỏi `resolveRecursive` và callers |
| `middleware.go` | X-Request-ID generation + log reqID |
| `main.go` | Forward X-Request-ID lên upstream |
| `config_loader_test.go` | +TestReloadConfig_Valid, +TestReloadConfig_InvalidFile |
| `router_test.go` | +TestCallLLMRouter (7 subtests), +TestCallLLMRouter_Timeout |
| `proxy_test.go` | Cập nhật cache reset → `newTTLCache(time.Minute, time.Minute)` |
| `tasks/session-report-2026-07-06-evening.md` | Session report đầy đủ |

---

## Build & Test

```sh
go build -o proxy .              # OK
go test -race -count=1 ./...    # 30 tests PASS, race detector clean
go vet ./...                     # Clean
```

Binary: `proxy` (16MB ELF 64-bit)  
Git commit: `94c247b` (main branch)

---

## Còn lại

| Item | Trạng thái | Ghi chú |
|------|-----------|---------|
| Real-world validation với `use_llm_router: true` | ⚠️ Chưa test | Cần upstream thật để test full LLM routing path |
| Benchmark `ttlCache` | ⏸️ Skip | Không cần thiết; atomic.Value + RWMutex rất rẻ |
| `/debug/cache` inspect endpoint | 💡 Optional | Có thể thêm: `GET /debug/cache` → JSON stats (size, hits, misses) |
| Integration test với SIGHUP thực tế | ⚠️ Chưa | Test hiện tại chỉ call `reloadConfig()` trực tiếp |

---

## Điểm cần lưu ý

### 1. Cache chỉ store LLM-confirmed results
Keyword match và static fallback không được cache. Lý do: nếu LLM timeout → fallback → cache kết quả tệ → lần sau không retry LLM. Behavior này là chủ đích.

### 2. Env var mới: `ROUTER_CACHE_TTL_MINUTES`
Default 30 phút. Set về thấp hơn nếu muốn LLM được gọi lại thường xuyên hơn sau config thay đổi.

### 3. X-Request-ID dùng `math/rand` (không phải `crypto/rand`)
Đủ cho correlation ID trong internal service. Không cần uniqueness tuyệt đối.

### 4. `TestCallLLMRouter_Timeout` dùng port 1 (refused)
Không dùng hang server để tránh goroutine leak với race detector. Test verify connection-refused path → FALLBACK.
