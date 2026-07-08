# Kiến trúc và Tài liệu Kỹ thuật Routing Proxy

Tài liệu này mô tả chi tiết thiết kế kỹ thuật, luồng xử lý và cấu hình của dịch vụ Routing Proxy - bộ định tuyến trung gian giữa Claude Code và hệ thống mô hình ngôn ngữ lớn (upstream APIs).

## Tổng quan (Overview)

Proxy đóng vai trò như một Gateway trung gian. Nó nhận yêu cầu từ client (ví dụ: Claude Code), phân tích prompt đầu vào, và quyết định mô hình vật lý nào sẽ xử lý tác vụ thông qua một máy trạng thái (FSM):
1. **LLM Router**: Gọi mô hình phân loại để đưa ra quyết định dựa trên mô hình cấu hình (`router_model` trong `config.json`).
2. **Keyword Router**: Định tuyến dựa trên từ khóa khớp trong prompt nếu LLM Router bị tắt hoặc gặp lỗi.

---

## Cấu trúc thư mục mã nguồn (Codebase Structure)

Dự án được phân rã thành các file Go độc lập để quản lý dễ dàng:

| File | Chức năng chính |
| :--- | :--- |
| `config.go` | Định nghĩa các cấu trúc dữ liệu cấu hình (`Config`, `SemanticRule`, `ModelSetting`) và các biến toàn cục. |
| `config_loader.go` | Đọc và giải tuần tự hóa cấu hình từ `config.json`, nạp biến môi trường để ghi đè (override) và khởi tạo map tìm kiếm nhanh. |
| `router.go` | Định nghĩa interface `ModelRouter` và các hàm phân loại (LLM client và parsing logic hỗ trợ cả Anthropic & OpenAI format). |
| `routing_fsm.go` | Máy trạng thái (FSM) hợp nhất để định tuyến động (`resolveDynamic`), quản lý bộ nhớ đệm cache biệt lập (`decisionCache`). |
| `anthropic.go` | Cấu trúc dữ liệu tối giản để phân tích phản hồi từ Anthropic API. |
| `utils.go` | Khởi tạo HTTP Client dùng chung (tái sử dụng kết nối) và bộ nhớ đệm `sync.Map`. |
| `main.go` | Điểm khởi chạy ứng dụng; lắng nghe HTTP, trích xuất prompt từ yêu cầu, đổi trường `model` và proxy yêu cầu tới upstream. |

---

## Luồng Định tuyến Động (FSM & Short-circuiting)

Để tối ưu hóa độ trễ và tránh lãng phí token, luồng xử lý định tuyến đi qua các bước sau:

```text
Yêu cầu tới proxy (Incoming Request)
         │
         ▼
[Kiểm tra Mô hình Tĩnh?] ──(Đúng)──► Trả về đích tĩnh (Short-circuit)
         │
       (Sai)
         ▼
[Kiểm tra Cache?] ───────(Đúng)──► Trả về kết quả từ Cache (Cache Hit)
         │
       (Sai)
         ▼
[LLM Router Bật?] ───────(Đúng)──► Gọi LLM Classifier ──► Tìm thấy? ──(Đúng)──► Lưu Cache & Trả về
         │                                                   │
       (Sai)                                               (Sai)
         │                                                   │
         ▼◄──────────────────────────────────────────────────┘
[Khớp Từ khóa?] ─────────(Đúng)──► Lưu Cache & Trả về
         │
       (Sai)
         ▼
[Luật Fallback] ─────────────────► Trả về Model mặc định & Lưu Cache
```

1. **Short-circuit (Tối ưu hóa tĩnh)**: Sử dụng hàm `isDynamicModel` để kiểm tra xem model yêu cầu có cần định tuyến động hay không. Nếu là model tĩnh, nó sẽ trả về kết quả ngay lập tức qua `getStaticTarget` mà không đi qua FSM, giúp tránh phình to cache và giảm độ trễ về 0ms.
2. **Kiểm tra bộ nhớ đệm (Cache Lookup)**: Dùng khóa cô lập `<originalModel>:<prompt>` để tránh xung đột kết quả giữa các tác vụ khác nhau.
3. **Phân loại bằng LLM (LLM Classification)**: Gửi prompt đã được gói (prompt-wrapping) kèm system prompt phân loại đến upstream model. Có cơ chế tự động bóc tách và phân tích các trường suy nghĩ (`reasoning_content` hoặc `reasoning`) từ phản hồi nếu mô hình router là mô hình lý luận (Reasoning Model).
4. **Khớp từ khóa (Keyword Matching)**: Duyệt danh sách từ khóa trong cấu hình nếu LLM router bị tắt hoặc phân loại không thành công.
5. **Fallback**: Sử dụng cấu hình fallback mặc định được định nghĩa cho model đó.

---

## Hướng dẫn cấu hình (`config.json`)

Ví dụ một file cấu hình chuẩn:

```json
{
  "upstream_url": "http://127.0.0.1:20128",
  "port": 20129,
  "use_llm_router": true,
  "router_model": "nvidia/stepfun-ai/step-3.7-flash",
  "upstream_api_key": "optional-key-here",
  "semantic_rules": [
    {
      "trigger_model": "swe.architect",
      "keywords": [],
      "target_model": "nvidia/openai/gpt-oss-120b",
      "description": "Architect -> GPT-OSS 120B (Static mapping)"
    },
    {
      "trigger_model": "swe.utility",
      "keywords": ["doc", "document", "readme", "explain", "markdown", "summary"],
      "target_model": "nvidia/minimaxai/minimax-m3",
      "description": "Routing for documentation-type tasks"
    }
  ],
  "decision_map": [
    {"decision": "MINIMAX", "target_model": "nvidia/minimaxai/minimax-m3"},
    {"decision": "DEEPSEEK", "target_model": "ds/deepseek-v4-flash"},
    {"decision": "FALLBACK", "target_model": "nvidia/stepfun-ai/step-3.7-flash"}
  ],
  "model_settings": {
    "swe.utility": {
      "system_prompt": "Classifier prompt here...",
      "decision_map": [
        {"decision": "MINIMAX", "target_model": "nvidia/minimaxai/minimax-m3"}
      ]
    }
  }
}
```

### Các trường cấu hình chính:
- **`use_llm_router`**: Bật/tắt tính năng phân loại thông minh bằng LLM. Có thể ghi đè biến này bằng biến môi trường `USE_LLM_ROUTER=true/1` hoặc `USE_LLM_ROUTER=false/0`.
- **`router_model`**: Định danh mô hình sẽ thực hiện nhiệm vụ phân loại.
- **`semantic_rules`**: Các luật ánh xạ tĩnh (không có từ khóa) hoặc ánh xạ dựa trên từ khóa.
- **`model_settings`**: Cấu hình chi tiết system prompt phân loại và ánh xạ quyết định (decision) riêng cho từng model gốc.

---

## Cách chạy và Kiểm thử (Deployment & Verification)

### Biên dịch & Chạy dịch vụ:
```bash
# Biên dịch
go build -o proxy .

# Chạy với ghi đè môi trường bật LLM Router
USE_LLM_ROUTER=1 ./proxy
```

### Chạy Unit Tests:
```bash
# Chạy toàn bộ test
go test -v ./...

# Chạy một test cụ thể
go test -v -run TestRouterOpenAIReasoningFormat ./...
```
