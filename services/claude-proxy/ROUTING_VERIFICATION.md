# Routing proxy verification

## Goal
Confirm `router.go` actually calls external LLM router when handling `swe.utility` model.

## Prereqs
- Go toolchain installed.
- Repository built (`go build ./...`).
- Server binary available (`./proxy`).
- Access to logs (`logs/info-*.log`).

## Steps
1. **Start server**
   ```bash
   ./proxy &
   SERVER_PID=$!
   ```
2. **Send test request** targeting utility model. Example (replace endpoint if different):
   ```bash
   curl -s -X POST http://127.0.0.1:20129/v1/complete \
        -H "Content-Type: application/json" \
        -d '{"model":"swe.utility","prompt":"Explain routing."}'
   ```
   - Use any prompt; key is `model":"swe.utility"`.
3. **Check logs** for two entries:
   - `"[Router] Resolve called"` with `originalModel=swe.utility`.
   - `"[Cache] Miss for model=swe.utility"` (first call) **or** `"[Cache] Hit for model=swe.utility"` (subsequent calls).
   - Look for line `"[Router] Resolve called"` followed by `"[Cache] Store for model=swe.utility"` indicating LLM or keyword resolution stored.
4. **Validate external call** – In `routing_fsm.go` a successful response results in a decision stored in cache. Presence of `Store for model=swe.utility decision=...` confirms the routing was successfully processed and cached.
5. **Optional** – Force cache miss by clearing the cache (restart server) and repeat step 2; you should see a `Miss for model=swe.utility` entry then a `Store for model=swe.utility` entry.
6. **Stop server**
   ```bash
   kill $SERVER_PID
   ```

## Expected outcome
- Log contains `Resolve called` with `originalModel=swe.utility`.
- Immediately after, a `Miss for model=swe.utility` line appears (first run) followed by `Store for model=swe.utility`.
- The HTTP response includes a `model` field chosen by router (e.g., `nvidia/minimaxai/minimax-m3` or `ds/deepseek-v4-flash`).

If logs only show `originalModel=swe.engineer` and no cache entries, routing is bypassing LLM.

---
*Document created to aid verification of routing proxy.*