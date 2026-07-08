# Chính sách Định tuyến & Ánh xạ Mô hình (AI Model Routing & Mapping Policy)

Tài liệu này quy định chi tiết cách phân bổ nhiệm vụ cho các mô hình AI khác nhau và cách ánh xạ các vai trò trừu tượng sang các mô hình vật lý cụ thể thông qua Proxy và Claude Code.

---

## Kiến trúc Ánh xạ hai lớp (Two-Layer Mapping Architecture)

Để đảm bảo tính linh hoạt, hệ thống chia việc ánh xạ mô hình thành 2 lớp độc lập. Điều này giúp chúng ta có thể thay đổi hoặc nâng cấp các mô hình bên dưới mà không cần sửa đổi mã nguồn ứng dụng Go.

```
[Các vai trò trừu tượng] (swe.architect, swe.engineer, swe.utility, ...)
            │
            ▼ (Lớp 1: Cấu hình phía Server Proxy qua config.json)
[Proxy Router nội bộ] (Định tuyến tĩnh, Khớp từ khóa, hoặc LLM Classifier)
            │
            ▼ (Lớp 2: Cấu hình phía Client / 9router Endpoint)
[Mô hình vật lý / Claude Code Alias] (Gemini, GPT-OSS, DeepSeek, MiniMax, StepFun)
```

### Lớp 1: Ánh xạ Vai trò sang Mô hình Vật lý (Proxy Layer)
Proxy trung gian chặn các yêu cầu và ánh xạ các mô hình trừu tượng (`swe.*`) sang các endpoint mục tiêu được định nghĩa trong `config.json`.
- **Định tuyến tĩnh (Static)**: Ánh xạ trực tiếp một model gốc sang model vật lý tương ứng.
- **Phân loại động (Dynamic)**: Sử dụng LLM Router để tự động phân loại yêu cầu của `swe.utility` thành các quyết định cụ thể như `MINIMAX` hay `DEEPSEEK`.

### Lớp 2: Ánh xạ Lý luận sang API Gateway (Client Layer)
Đối với các thành phần client sử dụng framework trực tiếp (như Claude Code trong `config/model_mappings.json`), các mô hình được phân chia theo phân tầng năng lực lý luận (Reasoning Tiers):

| Vai trò Trừu tượng | Mô hình Vật lý tương ứng (9router) | Mô hình API tương thích | Claude Code Alias | Phân tầng (Tier) |
| :--- | :--- | :--- | :--- | :--- |
| **swe.architect** | `nvidia/openai/gpt-oss-120b` | `claude-3-5-opus-latest` | **OPUS** | Tier-1-Reasoning (Lập kế hoạch & Đánh giá) |
| **swe.engineer** & **swe.subagent** | `ag/gemini-3-flash-agent` | `claude-3-5-sonnet-latest` | **SONNET** | Tier-2-Execution (Lập trình & Chỉnh sửa file) |
| **swe.utility** | Phân loại động (DeepSeek / MiniMax) | `claude-3-5-haiku-latest` | **HAIKU** | Tier-3-Boilerplate-Speed (Thuật toán & Unit Test) |
| **swe.knowledge** | `nvidia/minimaxai/minimax-m3` | Cấu hình Custom | **CUSTOM** | Tier-4-Knowledge-Processing (Tài liệu & Tri thức) |

---

## Chi tiết các vai trò Mô hình (Conceptual Model Profiles)

### 1. Kiến trúc sư (`swe.architect`)
- **Năng lực cốt lõi**: Khả năng suy luận logic sâu sắc, thiết kế hệ thống, phân rã tác vụ, soạn thảo RFC/ADR.
- **Tác vụ khuyến nghị**: Phân tích yêu cầu, lập kế hoạch thực hiện, đánh giá mã nguồn (code review), phát hiện rủi ro hồi quy.
- **Mô hình mặc định**: Mapped tới `nvidia/openai/gpt-oss-120b`.

### 2. Kỹ sư lập trình (`swe.engineer` & `swe.subagent`)
- **Năng lực cốt lõi**: Hiểu ngữ cảnh mã nguồn lớn, chỉnh sửa nhiều tệp tin đồng thời, sửa lỗi, tái cấu trúc mã nguồn.
- **Tác vụ khuyến nghị**: Triển khai tính năng mới, bảo trì hệ thống, di chuyển mã nguồn.
- **Mô hình mặc định**: Mapped tới `ag/gemini-3-flash-agent` (GLM 5.2).

### 3. Trợ lý tiện ích (`swe.utility`)
- **Năng lực cốt lõi**: Viết code nhanh, giải thuật, viết unit test, sinh mã nguồn lặp lại.
- **Định tuyến động**: Khi proxy nhận yêu cầu từ `swe.utility`, nó sẽ phân tích prompt:
  - Nếu liên quan đến thuật toán/unit test (phân loại là `DEEPSEEK`) ➔ chuyển hướng sang `ds/deepseek-v4-flash`.
  - Nếu liên quan đến tài liệu/giải thích mã nguồn (phân loại là `MINIMAX`) ➔ chuyển hướng sang `nvidia/minimaxai/minimax-m3`.

### 4. Quản lý Tri thức (`swe.knowledge`)
- **Năng lực cốt lõi**: Đọc ngữ cảnh dài, tóm tắt thông tin phức tạp, tổng hợp tài liệu kỹ thuật.
- **Tác vụ khuyến nghị**: Viết tài liệu, tổng hợp ghi chú cuộc họp, cập nhật kiến thức hệ thống.
- **Mô hình mặc định**: Mapped tới `nvidia/minimaxai/minimax-m3`.

---

## Hướng dẫn Thay đổi / Nâng cấp Mô hình

Khi cần nâng cấp hoặc hoán đổi mô hình vật lý (ví dụ: đổi mô hình tài liệu từ `MiniMax` sang một mô hình khác, hoặc chuyển mô hình kỹ sư chính từ `Gemini` sang `Claude Sonnet`):

1. **Tuyệt đối không sửa mã nguồn Go**: Trình định tuyến của Proxy được thiết kế hoàn toàn độc lập với mô hình. Không hardcode tên mô hình trong code.
2. **Cập nhật ánh xạ phía Server**: Sửa đổi thuộc tính `target_model` tương ứng trong phần `semantic_rules` hoặc `model_settings` của file `config.json`. Proxy sẽ tự động nạp lại cấu hình mới khi restart dịch vụ.
3. **Cập nhật ánh xạ phía Client (nếu cần)**: Điều chỉnh cấu hình trong file `config/model_mappings.json` của Claude Code để trỏ tới API endpoint mới.
