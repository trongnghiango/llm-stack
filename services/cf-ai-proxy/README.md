# Cloudflare Workers AI - Multi-Account Rotating Proxy (cf-ai-proxy)

Hệ thống Proxy hiệu năng cao viết bằng **Golang (Gin Framework)**, đóng vai trò làm cổng trung gian (Adapter) chuyển đổi các yêu cầu từ AI Agents hoặc API Gateway (như OmniRoute) thành cấu trúc tương thích với **Cloudflare Workers AI**.

Hệ thống tích hợp giải pháp quản lý bộ nhớ thông minh giúp **xoay vòng nhiều tài khoản Cloudflare (Round-Robin)**, cố định tài khoản theo phiên làm việc **(Session Affinity)** để tối ưu Prompt Caching, tự động **chuyển vùng chủ động (Handoff)** ở mức 95% hạn mức để tránh đứt gãy luồng streaming, và tự động **tối ưu hóa Context Window (Dynamic Token Capping)**.

---

## 🏗️ Cấu trúc dự án (Architecture)

Mã nguồn được tổ chức sạch sẽ, phân tách rõ ràng trách nhiệm:

* **[main.go](file:///home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/main.go)**: Khởi tạo HTTP Server, đăng ký định tuyến các API OpenAI và Anthropic.
* **[models.go](file:///home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/models.go)**: Định nghĩa cấu trúc dữ liệu cho Request/Response của OpenAI và Anthropic.
* **[session.go](file:///home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/session.go)**: Quản lý danh sách tài khoản, trạng thái bám dính session, nạp cấu hình tài khoản (`accounts.csv`) và danh sách mô hình động (`models.csv`).
* **[handler.go](file:///home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/handler.go)**: Xử lý logic nghiệp vụ cho từng Endpoint, biên dịch dữ liệu Stream/Non-stream và tích hợp tính năng tự động tối ưu hóa Context Window.
* **[accounts.csv](file:///home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/accounts.csv)**: Chứa danh sách `account_id` và `api_token` của Cloudflare xoay vòng.
* **[models.csv](file:///home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/models.csv)**: Chứa danh sách ánh xạ tên mô hình alias và ID thực tế chạy trên GPU Cloudflare.

---

## ⚙️ Quy Ước Đặt Tên & Tránh Xung Đột Trên OmniRoute

Để tránh xung đột với Cloudflare provider chính thức trực tiếp trên OmniRoute, hệ thống sử dụng quy ước đặt tên riêng biệt (Isolated Namespace Prefix):

### 1. Trên OmniRoute (Cấu hình Provider)
* Khai báo một Custom Provider (OpenAI hoặc Anthropic Compatible) trỏ về proxy của chúng ta:
  * **Base URL**: `http://host.docker.internal:3000/v1` (Nếu OmniRoute chạy Docker) hoặc `http://127.0.0.1:3000/v1` (Chạy trực tiếp trên Host).
  * **Provider Prefix**: Thiết lập là **`cf-ai-proxy`**.

### 2. File cấu hình [models.csv](file:///home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/models.csv)
Các mô hình được khai báo **tuyệt đối không chứa ký tự gạch chéo `/`** trong tên alias:
```csv
alias,target
qwen2.5-coder-7b-instruct,@cf/qwen/qwen2.5-coder-32b-instruct
llama-3.1-8b-instruct,@cf/meta/llama-3.1-8b-instruct
qwen-2.5-coder,@cf/qwen/qwen2.5-coder-32b-instruct
```
Khi client gọi combo thông qua OmniRoute:
1. OmniRoute sẽ định tuyến theo tiền tố `cf-ai-proxy/` và chuyển tiếp yêu cầu về proxy.
2. Proxy nhận được alias sạch (ví dụ: `qwen2.5-coder-7b-instruct`) và ánh xạ sang đúng model đích của Cloudflare (ví dụ: `@cf/qwen/qwen2.5-coder-32b-instruct`).
3. Cách làm này cô lập hoàn toàn luồng xử lý và không gây xung đột định tuyến với Cloudflare provider trực tiếp của OmniRoute.

---

## 🛠️ Cơ chế tối ưu hóa Context Window (Dynamic Token Capping)

Để khắc phục triệt để lỗi vượt quá dung lượng context length của mô hình Cloudflare (lỗi `3030` khi tổng số token prompt + completion vượt quá `32768`), proxy tự động:
1. Tính toán số lượng token của prompt đầu vào.
2. Kiểm tra giới hạn tối đa của mô hình đích (ví dụ: 32768 tokens đối với Qwen Coder).
3. Nếu tổng số vượt ngưỡng, proxy sẽ tự động hạ giảm `max_tokens` của request xuống mức tối đa cho phép để cuộc gọi luôn thành công.

---

## 🚀 Cách Vận Hành

1. **Biên dịch**:
   ```bash
   go build -ldflags "-s -w" -o cf-ai-proxy
   ```
2. **Khởi chạy**:
   ```bash
   ./cf-ai-proxy
   ```
3. **Kiểm tra danh sách mô hình khả dụng**:
   ```bash
   curl http://127.0.0.1:3000/v1/models
   ```
