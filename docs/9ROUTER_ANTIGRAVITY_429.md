# 🔧 9router × Antigravity (ag/*) — Xử lý 429 & Runbook patch

> Ghi chú vận hành cho sự cố **`ag/gemini-*` bị 429 RESOURCE_EXHAUSTED rồi "chết" ~15 phút** trên 9router.
> Áp dụng cho image `decolua/9router:latest` **v0.5.116** (build 2026-11-09). Cập nhật lần cuối: **2026-11-16**.

---

## 1. TL;DR

| | |
|---|---|
| **Triệu chứng** | Gọi `ag/gemini-3.8-flash-high(high)` → `429 RESOURCE_EXHAUSTED`, sau đó mọi model antigravity đều lỗi: `all 1 accounts locked` |
| **Nguyên nhân 1** | Google chặn phía upstream (rate/capacity) — quota API vẫn báo còn 100% ⇒ **không phải hết hạn mức ngày** |
| **Nguyên nhân 2** | 9router tự retry 429 **3 lần**; luật nội bộ: **3× 429 trong 60s → `CACHE_BLOCK 15m`** ⇒ tự khoá 15 phút |
| **Nguyên nhân 3** | Chỉ có **1 account antigravity đang bật** ⇒ không có fallback |
| **Đã sửa** | Giảm `retry["429"].attempts` từ `3` → `2` (bind-mount `patch/antigravity.js`) |
| **Không nên** | Bật MITM (rủi ro ToS/ban cao nhất) |

---

## 2. Bằng chứng từ log

```text
[11:47:24] ▶ POST ag/gemini-3.8-flash-high(high) → antigravity · STREAM · 9 MSG · 32 TOOL · THINK:high
[11:47:40] ✗ ERROR 429 · antigravity/gemini-3.8-flash-high(high) · 16472ms
    URL: https://daily-cloudcode-pa.googleapis.com/v1internal:streamGenerateContent?alt=sse
    [429]: "Resource has been exhausted (e.g. check quota)." — RESOURCE_EXHAUSTED
[11:47:42] 🔒 nghiango.cnx@gmail.com locked modelLock_gemini-3.8-flash-high(high) for 16s [429]
[11:47:42] ⚠️ antigravity | all 1 accounts locked for gemini-3.8-flash-high(high)
```

Cơ chế tự kích hoạt (retry 3 lần = đủ 3 strike):

```text
[10:37:03] 429   ← lần thử 1
[10:37:25] 429   ← lần thử 2 (retry nội bộ)
[10:37:46] 429   ← lần thử 3 (retry nội bộ)
[10:37:47] ⚠️ [AG_QUOTA] STRIKE_429 gemini-3.8-flash-high(high) — 3x 429 (quota 100%); CACHE_BLOCK 15m
```

Nguồn: `docker logs llm-9router`. Code liên quan: `.next/server/chunks/238.js` (hàm `r()` — strike/cache-block) và `open-sse/executors/base.js` (retry loop).

---

## 3. Thay đổi đã áp dụng (2026-11-16)

### 3.1. File patch

`patch/antigravity.js` — bản copy của `open-sse/providers/registry/antigravity.js`, chỉ sửa 1 chỗ:

```js
    retry: {
      "429": {
        // 2 thay vì 3: tránh tự kích hoạt STRIKE_429 (3x 429 trong 60s)
        // khiến 9router tự khoá model 15 phút (CACHE_BLOCK).
        attempts: 2,
      },
```

Với `attempts: 2`, router chỉ tạo tối đa 2 strike/60s ⇒ **không bao giờ chạm ngưỡng 3** ⇒ không còn cảnh tự khoá 15 phút. 429 vẫn trả về cho client, client thử lại sau 20–30s.

### 3.2. Mount trong `docker-compose.yml`

```yaml
  9router:
    volumes:
      - ./data/9router:/app/data
      - ./config/nim_accounts.csv:/app/NIM_accounts.csv:ro
      # Patch: giảm retry 429 (3→2) để không tự kích hoạt CACHE_BLOCK 15 phút
      - ./patch/antigravity.js:/app/open-sse/providers/registry/antigravity.js:ro
```

### 3.3. Bật account thứ 2 (2026-11-16 12:19)

Trước đó chỉ có **1 account active** ⇒ mỗi lần 429 là `all 1 accounts locked` (không có fallback).
Đã bật `nghia.ewsn@gmail.com` (id `c61851c7…`, priority 1) và gán `projectId = aicode-consumers` (giống account 1) để tránh router tự sinh projectId ngẫu nhiên.

| Account | Gemini weekly | Gemini 5h | Argon (`gemini-4-argon`) |
|---|---|---|---|
| `nghiango.cnx@gmail.com` (dddefc72, priority 2) | 94.8% | 97.3% | ❌ 404 |
| `nghia.ewsn@gmail.com` (c61851c7, priority 1) | 99.99% | 100% | ✅ 99.3% |

Kiểm tra quota thật (đọc trực tiếp từ Google, không đoán qua log):

```bash
cd data/9router/db
TOK=$(sqlite3 data.sqlite "select json_extract(data,'\$.accessToken') from providerConnections where provider='antigravity' limit 1;")
curl -s -X POST "https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary" \
  -H "Authorization: Bearer $TOK" -H "Content-Type: application/json" \
  -H "User-Agent: antigravity/hub/2.24.0 darwin/arm64" \
  -d '{"project":"aicode-consumers"}'
```

> Kết quả ngày 16/11: `gemini-5h` còn **97%**, `gemini-weekly` còn **94.8%** ⇒ **429 không phải do hết quota**, mà do Google throttle theo nhịp/khối lượng. Cùng một payload lúc 200 lúc 429.

Sau khi bật account 2, request tự chuyển sang `ACC:nghia.ewsn@gmail.com` và trả 200 (xác nhận trong log 12:19:53).

Áp dụng:

```bash
docker compose up -d --no-deps 9router   # --no-deps: không đụng cf-ai-proxy/redis
```

> `docker restart` **không đủ** — phải recreate để mount mới có hiệu lực.
> Env (`JWT_SECRET`, `API_KEY_SECRET`) lấy từ `.env` nên API key cũ vẫn hợp lệ.

---

## 4. Kiểm chứng

```bash
# 1) Patch đã vào container
docker exec llm-9router sed -n '27,37p' /app/open-sse/providers/registry/antigravity.js
#    → "attempts: 2" trong block "429"

# 2) Mount đúng
docker inspect llm-9router --format '{{range .Mounts}}{{.Source}} -> {{.Destination}}{{println}}{{end}}'

# 3) Container khoẻ
docker inspect llm-9router --format '{{.State.Status}}'
docker logs llm-9router --since 3m | grep -vE '^\s' | tail

# 4) Smoke test (dùng API key "DSH" trong DB)
KEY=$(sqlite3 data/9router/db/data.sqlite "select key from apiKeys where name='DSH' limit 1;")
curl -s -o /dev/null -w '%{http_code}\n' -H "Authorization: Bearer $KEY" http://127.0.0.1:20128/v1/models
curl -s -H "Authorization: Bearer $KEY" -H 'Content-Type: application/json' \
  -X POST http://127.0.0.1:20128/v1/chat/completions \
  -d '{"model":"ag/gemini-3.8-flash-high","messages":[{"role":"user","content":"say OK"}],"max_tokens":12}' \
  | head -c 200
```

Kỳ vọng: `200` + `{"choices":[... "content":"OK" ...]}`.

---

## 5. Vận hành hằng ngày (quy tắc chống 429)

1. **Gặp 429 → đợi 20–30s** rồi thử lại. Đừng bấm liên tiếp 3 lần (đủ 3 strike là bị khoá 15 phút).
2. **Đổi model variant là thoát block ngay**: `CACHE_BLOCK` tính theo cặp `(account, model)`. `high` bị chặn thì `medium` vẫn chạy.
   - `ag/gemini-3.8-flash-high` → `-medium` → `-low` → `gemini-3.8-flash`
3. **Không chạy nhiều client nặng song song** trên cùng account (DSH + Claude Code + Cursor…).
4. **Giữ request gọn**: bớt tool không cần, chia nhỏ context. Request kiểu 9 message + 32 tool + `THINK:high` là combo dễ 429 nhất.
5. **Không bật MITM** (Dashboard → MITM Proxy). Đây là thứ dễ bị Google flag/ban nhất.
6. **Ưu tiên kiểm tra**: Dashboard → *Quota Tracker* trước khi kết luận "hỏng".

### Cấu hình nên có trên Dashboard (`http://127.0.0.1:20128`)

| Mục | Giá trị khuyến nghị | Lý do |
|---|---|---|
| Providers → Antigravity | bật **≥ 2 account** | Hết cảnh "all 1 accounts locked" |
| Settings → thinking antigravity | `medium` (thay vì `high`) | Giảm token/lượt ⇒ giảm 429 |
| Combos | `ag-safe`: high → medium → low | 429 tự nhảy model, không fail cả lượt |

> Lưu ý: `settings.data.providerThinking.antigravity.mode` hiện là `high` và được cache trong RAM — đổi bằng UI, không sửa DB trực tiếp.

---

## 6. Khi nào model `ag/*` trả 404 (không phải lỗi router)

Model có trong `/v1/models` (catalog toàn cục) **không đồng nghĩa account có quyền**. Kiểm tra thực tế ngày 2026-11-16:

| Model | Kết quả |
|---|---|
| `ag/gemini-3.8-flash-high` / `-medium` / `-low` / `gemini-3.8-flash` | ✅ 200 |
| `ag/gemini-3.7-flash-*` (upstream `gemini-3.7-flash-tiered`) | ❌ 404 `Requested entity was not found` |
| `ag/gemini-3.6-flash-*` (upstream `gemini-3.6-flash-tiered`) | ❌ 404 |
| `ag/gemini-4-argon-*` | ❌ 404 |
| `ag/gemini-3.1-pro-high`, `ag/gemini-pro-agent` | ❌ 404 |

⇒ Chỉ nhóm `gemini-3.8-flash-*` dùng được với account hiện tại. Mapping model nằm ở `open-sse/providers/registry/antigravity.js` (field `models[].upstreamModelId`).

---

## 7. Re-apply sau khi cập nhật image (QUAN TRỌNG)

`docker compose pull` sẽ **ghi đè** `registry/antigravity.js` ⇒ patch mất tác dụng. Quy trình:

```bash
cd ~/Repos/github.com/trongnghiango/llm-stack

# 1) Lấy file mới từ image vừa pull
docker cp llm-9router:/app/open-sse/providers/registry/antigravity.js patch/antigravity.js

# 2) Sửa attempts: 3 → 2 trong block "429" (kiểm tra bằng mắt trước khi sửa)
grep -n -A3 '"429": {' patch/antigravity.js

# 3) Recreate
docker compose up -d --no-deps 9router

# 4) Kiểm chứng lại (mục 4)
```

Nếu bản mới đã tự sửa logic strike/cache-block (ví dụ có config riêng cho retry), **bỏ patch này** và cập nhật lại tài liệu.

> Cũng cần kiểm tra `ANTIGRAVITY_IDE_VERSION` (hiện `2.24.0`, tại `open-sse/providers/shared.js`) mỗi khi Antigravity IDE ra bản mới — UA lệch version có thể bị upstream từ chối.

---

## 8. Rollback

```bash
cd ~/Repos/github.com/trongnghiango/llm-stack
ls -1 docker-compose.yml.bak-*        # chọn bản backup trước khi sửa
cp docker-compose.yml.bak-20261116_120745 docker-compose.yml
docker compose up -d --no-deps 9router
```

Rollback chỉ bỏ mount, **không** mất dữ liệu (`data/9router` là bind mount riêng).

---

## 9. Giới hạn & rủi ro

- Patch này **không vượt được quota thật**. Nếu Google chặn theo account/model thì vẫn 429; patch chỉ ngăn router tự biến một 429 tạm thời thành 15 phút chết.
- Một số 429 là "429 giả" do fingerprint nội dung — 9router đã tự xử lý sẵn (xem `ANTIGRAVITY_PROMPT_REWRITES`: bỏ `x-anthropic-billing-header`, đổi `opencode`→`antigravity`; và xoá `requestType: "agent"`).
- Dùng OAuth Antigravity qua router **không phải kênh chính thức** của Google. Mức rủi ro:

  | Cách dùng | Rủi ro ban |
  |---|---|
  | 1–2 account chính chủ + API path + giãn nhịp | Thấp |
  | 3–5 account luân phiên trên 1 IP | Trung bình |
  | MITM (rewrite traffic IDE, cài CA) | Cao nhất |

---

## 10. Nhật ký

| Ngày | Việc |
|---|---|
| 2026-11-16 | Chẩn đoán 429; phát hiện retry 3 lần tự kích hoạt `CACHE_BLOCK 15m`; tạo `patch/antigravity.js` (429: 3→2); thêm mount vào `docker-compose.yml`; recreate container; smoke test 200 OK |
| 2026-11-16 12:19 | Lấy quota thật từ Google (5h còn 97%, weekly 94.8%) ⇒ loại trừ nguyên nhân hết hạn mức; bật account `nghia.ewsn` (weekly 99.99%, có Argon) + gán `projectId`; restart; request chuyển sang account 2 và trả 200 |
| 2026-11-16 12:31 | 429 xuất hiện ở **cả 2 account cùng lúc** dù quota 94-100% ⇒ throttle theo project/hạ tầng, không theo account. Các request lỗi đều dừng ở ~16s (Google xếp hàng rồi shed). Giảm `retry["429"]` 3→2→**1** trong patch; đặt `retryPolicy.maxRetries=1, initialDelayMs=15000` cho provider `antigravity` trong `~/.dsh/profiles/web/cordis.patch.yml` |
