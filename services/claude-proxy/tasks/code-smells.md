# Code Smell Report — multi-llm-swe-protocol

## NGHIÊM TRỌNG

### S1. Data race — global state không đồng bộ

**Files:** `config.go:33-38`, `config_loader.go:43-59`, `main.go:39`  
**Issue:** `config`, `semanticRuleMap`, `decisionModelMap`, `modelSettingsMap`, `modelRouter` là global variable ghi bởi `reloadConfig()` (SIGHUP goroutine) và đọc bởi request handler goroutines — không có `sync.Mutex`/`sync.RWMutex`/`atomic.Value` nào bảo vệ. Race condition dẫn đến panic hoặc routing sai.  
**Fix:** Dùng `atomic.Value` cho config hoặc `sync.RWMutex` cho các maps.

### S2. `reloadConfig()` hardcode path `"config.json"`, bỏ qua `--config` flag

**File:** `config_loader.go:69`  
**Issue:**
```go
loader := &JSONConfigLoader{Path: "config.json"}  // --config flag bị ignored
```
`parseFlags()` trả về đường dẫn tùy chỉnh nhưng không được lưu để dùng trong signal handler.  
**Fix:** Lưu `cfgPath` vào package-level variable, dùng trong `reloadConfig()`.

### S3. Early return `/debug/metrics` trong `handleProxy` là dead code

**File:** `main.go:49-52`  
**Issue:** `handleProxy` check path và serve metrics handler. Nhưng `exposeMetrics()` đã register handler trên mux (`mux.Handle("/debug/metrics", ...)`), và `ServeMux` route ưu tiên path cụ thể hơn → request tới `/debug/metrics` không bao giờ tới `handleProxy`.  
**Fix:** Xoá block dead code trong `handleProxy`.

---

## TRUNG BÌNH

### M1. Ignored error khi parse model và prompt text

**File:** `main.go:76,84`  
**Issue:**
```go
_ = json.Unmarshal(modelBytes, &originalModel)  // dòng 76
_ = json.Unmarshal(bodyBytes, &textReq)          // dòng 84
```
- Nếu field `model` không phải string → `originalModel = ""` → request forward với model rỗng.
- Nếu parse `textReq` lỗi → `promptText = ""` → routing decision mù.  
**Fix:** Kiểm tra error và fallback hợp lý.

### M2. Channel buffer size hardcode, không dùng `logBufSize`

**File:** `logger.go:248`  
**Issue:**
```go
ch: make(chan []byte, 5000), // hardcode 5000
```
`logBufSize` đã được khởi tạo từ `initLogConfig()` (dòng 53, 118-122) với giá trị mặc định 5000 hoặc từ env `LOG_BUF_SIZE`, nhưng không được dùng ở đây.  
**Fix:** `make(chan []byte, logBufSize)`

### M3. LLM router gửi system prompt vào user message, không dùng `system` role

**File:** `router.go:115-121`  
**Issue:**
```go
combinedContent := fmt.Sprintf("%s\n\nUser Task: %q", systemPrompt, prompt)
"messages": []map[string]string{{"role": "user", "content": combinedContent}}
```
System prompt bị nhồi vào user message thay vì gửi riêng với `role: "system"`. Router model không thấy được structured system instruction.  
**Fix:** Gửi system prompt ở đúng role riêng biệt.

### M4. Unrecognized LLM decision dùng thẳng làm model name

**File:** `routing_fsm.go:164-165`  
**Issue:**
```go
// Return original decision name if no mapping matches
return decision
```
Nếu LLM router trả về garbage (output format sai, hallucination), garbage đó được dùng làm model name → request fail không rõ nguyên nhân.  
**Fix:** Fallback về `"FALLBACK"` nếu không match mapping nào.

### M5. `isDynamicModel` bỏ qua `DecisionMap`

**File:** `routing_fsm.go:112-129`  
**Issue:** Chỉ check `SystemPrompt != ""` nhưng không check `len(setting.DecisionMap) > 0`. Model có custom decision mapping nhưng không có keywords/system prompt sẽ bị coi là non-dynamic → không bao giờ gọi LLM classifier.  
**Fix:** Thêm check `len(setting.DecisionMap) > 0`.

---

## THẤP

### L1. Header API key set redundant case variants

**Files:** `router.go:141-145`, `main.go:162-170`  
**Issue:** Dùng raw map assignment (`req.Header["x-api-key"] = ...`) và `Header.Set(...)` cho cùng giá trị. Go `http.Header` canonicalize key sang `X-Api-Key` — set cả lowercase và capitalized là vô ích.  
**Fix:** Chỉ dùng `Header.Set("X-Api-Key", ...)` hoặc `Header.Set("x-api-key", ...)` — một là đủ.

### L2. `getLogPath` hardcode relative path prefix

**File:** `logger.go:21`  
**Issue:** `fmt.Sprintf("logs/%s-%s.log", base, date)` — path relative. Hoạt động vì `init()` tạo thư mục `logs/` ở CWD. Sẽ fail nếu process CWD khác.

### L3. Cycle detection trong `resolveRecursive` chỉ log, không xử lý

**File:** `router.go:42-43`  
**Issue:** Khi phát hiện cycle, chỉ log rồi `break` — trả về model hiện tại đang gây cycle thay vì fallback về target mặc định.

### L4. Comment code atomic counters bị comment-out

**File:** `logger.go:145,155`  
**Issue:**
```go
// atomic.AddInt64(&l.total, 1) // Uncomment if atomic import added.
// atomic.AddInt64(&l.dropped, 1)
```
Code để theo dõi stats bị comment-out, không import `sync/atomic`.

---

**Mức độ ưu tiên sửa:** S1 > S2 > S3 > M1 > M2 > M3 > M4 > M5 > L1–L4

---

## Trạng thái

| Smell | Status | Ghi chú |
|-------|--------|---------|
| S1 — Data race | ✅ Fixed | `state.go` + `atomic.Value` thay 5 globals |
| S2 — reloadConfig path | ✅ Fixed | `configPath` lưu từ `--config`, dùng trong reload |
| S3 — /debug/metrics guard | ✅ Giữ lại | Không dead code — bypass mux trong test |
| M1 — Ignored errors | ✅ Fixed | Parse model + promptText kiểm tra error |
| M2 — Buffer hardcode | ✅ Fixed | `make(chan ..., logBufSize)` |
| M3 — System prompt role | ✅ Fixed | Gửi `role:"system"` riêng thay vì nhồi vào user |
| M4 — Unknown decision | ✅ Fixed | `resolveDecision` trả về `"FALLBACK"`, caller fallthrough |
| M5 — isDynamicModel | ✅ Fixed | Thêm check `len(DecisionMap) > 0` |
| L1 — Header case dupe | ✅ Fixed | `Header.Set` thay raw map assignment |
| L2 — Log path relative | ⏸️ Bỏ qua | Env override (`LOG_INFO_PATH`) đã đủ, không scope creep |
| L3 — Cycle detection | ✅ Fixed | Fallback về `originalModel` thay vì trả cycle model |
| L4 — Atomic counters | ✅ Fixed | `atomic.Uint64` + uncomment increment calls |
