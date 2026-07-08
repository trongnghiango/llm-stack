# GUIDE.md

## Hướng dẫn cấu hình tự động chuyển đổi model cho Claude Code

Repository này dùng **Claude Code** để điều phối nhiều mô hình LLM (GPT‑OSS, GLM‑5.2, DeepSeek, MiniMax). Để cho Claude Code tự động chọn model phù hợp cho mỗi giai đoạn (planning, implementation, testing, review, doc) cần thực hiện các bước sau.

---
### 1. Bật khám phá mô hình tự động (gateway)
`config/model_mappings.json` đã có trường `"comment": "Mapper layer …"`. Đảm bảo trong **`.claude/settings.json`** có:
```json
"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "1"
```
Giá trị `1` bật việc Claude Code tự động đọc file mapping khi khởi chạy.

---
### 2. Kích hoạt chuyển model bằng flag
Mặc định `"switchModelsOnFlag": false`. Đổi thành `true` để cho phép người dùng truyền flag `--model <alias>` hoặc `--phase <phase>` để thay đổi model trong mỗi lần chạy.
```json
"switchModelsOnFlag": true
```
Lưu file, khởi động lại Claude Code.

---
### 3. Định nghĩa mapping mô hình → API model
`config/model_mappings.json` hiện có mapping:
```json
{
  "comment": "Mapper layer …",
  "mappings": {
    "GPT-OSS-120B": { "target_api_model": "claude-3-5-opus-latest", "alias": "OPUS", "tier": "Tier-1-Reasoning" },
    "GLM-5.2":     { "target_api_model": "claude-3-5-sonnet-latest", "alias": "SONNET", "tier": "Tier-2-Execution" },
    "DeepSeek":    { "target_api_model": "claude-3-5-haiku-latest", "alias": "HAIKU", "tier": "Tier-3-Boilerplate-Speed" },
    "MiniMax":     { "target_api_model": "custom-model-endpoint", "alias": "CUSTOM", "tier": "Tier-4-Knowledge-Processing" }
  }
}
```
Nếu muốn thêm mô hình mới, chỉ cần bổ sung một mục trong `"mappings"` với cùng cấu trúc.

---
### 4. Định nghĩa routing giai đoạn → mô hình khái niệm
Thêm một object `"routing"` vào cùng file **hoặc** tạo file `config/phase_routing.json`. Ví dụ:
```json
{
  "routing": {
    "planning":      "GPT-OSS-120B",
    "implementation":"GLM-5.2",
    "testing":       "DeepSeek",
    "review":        "GPT-OSS-120B",
    "doc":           "MiniMax"
  }
}
```
Mỗi khóa là tên giai đoạn (khớp với mô tả trong `docs/AI_WORKFLOW.md`). Giá trị là tên mô hình khái niệm, sẽ được tra‑lookup trong `mappings` để lấy `target_api_model` và `alias`.

---
### 5. Sử dụng flag / biến môi trường để chuyển model
Khi `switchModelsOnFlag` bật, Claude Code chấp nhận các flag:
- `--model <alias>` – chọn model ngay lập tức (alias từ `model_mappings.json`).
- `--phase <phase>` – Claude sẽ tra‑lookup `phase` trong `routing`, lấy mô hình khái niệm, rồi lấy `alias` tương ứng.

Ví dụ chạy bước lập kế hoạch:
```bash
claude --phase planning   # sẽ dùng OPUS (GPT‑OSS-120B)
```
Chạy bước thực thi:
```bash
claude --phase implementation   # sẽ dùng SONNET (GLM‑5.2)
```
Nếu muốn ghi tạm thời qua biến môi trường:
```bash
export CLAUDE_PHASE=testing
claude                # sẽ dùng HAIKU (DeepSeek)
```

---
### 6. Kiểm tra cấu hình
1. Kiểm tra JSON hợp lệ:
```bash
jq . .claude/settings.json    # không báo lỗi
jq . config/model_mappings.json
```
2. Kiểm tra rằng Claude Code đọc mapping:
```bash
claude --list-models   # hiển thị alias OPUS, SONNET, HAIK... và model tương ứng
```
3. Kiểm tra chuyển model theo phase:
```bash
claude --phase review   # phải báo "using model OPUS (claude-3-5-opus-latest)"
```

---
### 7. Lưu ý
- Giữ `target_api_model` trỏ tới endpoint Claude hợp lệ (`claude-3-5-…`).
- Khi thay đổi file mapping, khởi động lại Claude Code để reload.
- Không cần sửa `CLAUDE_CODE_SUBAGENT_MODEL` – chỉ dùng khi muốn thay đổi model cho sub‑agent.
- Nếu muốn tắt khám phá tự động, đưa `"CLAUDE_CODE_ENABLE_GATEWAY_MODEL_DISCOVERY": "0"`.

---
**Tóm tắt**: bật `switchModelsOnFlag`, định nghĩa `routing` (phase → khái niệm model) và duy trì `model_mappings.json`. Sau đó dùng flag `--phase <tên>` hoặc biến môi trường để Claude Code tự động chuyển sang model thích hợp cho mỗi công việc.
