# Walkthrough: Unified `llm-stack` Project Setup & Tool Call Restructuring

Chúng ta đã hoàn thành việc tái cấu trúc lớn và hợp nhất 3 service chính vào một project duy nhất tên là `llm-stack` tại địa chỉ [llm-stack](file:///home/ka/Repos/github.com/trongnghiango/llm-stack).

---

## 🚀 Các thay đổi đã thực hiện

### 1. Hợp nhất dự án và Docker hóa
- **Cấu trúc thư mục mới**: Gom `claude-proxy` và `cf-ai-proxy` vào thư mục `services/`.
- **Docker Compose**: Tạo `docker-compose.yml` định nghĩa mạng nội bộ `llm-net`, liên kết 4 dịch vụ:
  1. `claude-proxy` (port 20129)
  2. `9router` (port 20128)
  3. `cf-ai-proxy` (port 20127, nội bộ)
  4. `redis` (port 6379, nội bộ)
- **Tạo Dockerfiles**: Sử dụng multi-stage build với Go 1.24 và Alpine Linux giúp dung lượng image siêu nhẹ và an toàn.

### 2. Tự động hóa Seed Cơ sở dữ liệu 9router
- Tạo SQL seed `data/9router/db/init.sql` đăng ký sẵn:
  - API Key: `sk-e03c947dc728e9f5-1lt0v4-32e6f2f4` cho claude-proxy.
  - Custom Provider: trỏ về `http://cf-ai-proxy:20127/v1` (Docker network internal URL).
  - 5 Combos mapping: `ka.zzz`, `ka.xxx`, `ka.ddd`, `ka.ccc`, `ka.mmm`.
  - 9 model registry trong KV cache của 9router.
- Tạo script wrapper khởi động `start.sh` trên Host để:
  - Tự động dùng `sqlite3` của Host tạo và seed file `data.sqlite` trước khi chạy docker (tránh lỗi thiếu công cụ trong container).
  - Set permissions `chmod 777` cho data volume để tránh lỗi Permission Denied.
  - Chạy `docker compose up -d --build`.

### 3. Tái cấu trúc logic Tool Call của `cf-ai-proxy`
Để đảm bảo Claude Code giữ nguyên các tính năng chuyên nghiệp (giao diện, xin ý kiến người dùng, auto-approve...):
- **Bỏ `runLocalTool` khi sinh response**: Khi Cloudflare trả về tool calls, proxy chỉ đóng gói block `tool_use` (chứa `id`, `name`, `input`) gửi thẳng về cho Claude Code. Không tự động thực thi bash/write nữa.
- **Giữ nguyên logic convert request**: Khi Claude Code chạy tool xong và gửi request kế tiếp, proxy vẫn chuyển đổi chuẩn xác `tool_result` thành format OpenAI `tool` message để Cloudflare LLM hiểu được lịch sử chat.
- Đã sửa đổi cho cả luồng **Non-Streaming** và **Streaming**.
- Chạy test suite `go test -v ./...` trong `cf-ai-proxy` vượt qua 100% thành công.

---

## 📊 Trạng thái các Tài khoản Cloudflare hiện tại (Kiểm tra E2E)
Khi chạy thử file test E2E `test_upstream.py`, Cloudflare trả về lỗi 401/410. Sau khi debug trực tiếp, đây là trạng thái thực tế:
- `f694fb73`: Hoạt động bình thường (dùng token cfut_).
- `a3036b15`: Đã dùng hết định mức miễn phí hôm nay (`used up daily free allocation of 10,000 neurons`).
- `fe82cd11`: Đã dùng hết định mức miễn phí hôm nay (`used up daily free allocation of 10,000 neurons`).
- `5f8d3bed`: Đã dùng hết định mức miễn phí hôm nay (`used up daily free allocation of 10,000 neurons`).

> [!TIP]
> Bạn hãy cập nhật API Token mới còn hạn vào file `.env` hoặc vào thẳng UI quản lý 9router tại http://localhost:20128 (mật khẩu mặc định: `llmstack2026`) để test E2E ngay lập tức. Mức Neurons sẽ tự động reset sau 24h.

---

## 🛠️ Hướng dẫn vận hành nhanh
```bash
cd /home/ka/Repos/github.com/trongnghiango/llm-stack

# Khởi động lại toàn bộ stack (tự động seed DB nếu chưa có)
./start.sh

# Xem log các dịch vụ
docker compose logs -f

# Xem trạng thái hoạt động của các container
docker compose ps
```

### 🔄 Cách đồng bộ toàn bộ Available Models từ cf-ai-proxy lên UI 9router:
1. Đăng nhập vào UI 9router (http://localhost:20128) bằng mật khẩu `llmstack2026`.
2. Vào mục **Providers**, kéo xuống phần Custom Provider **`CF-AI-PROXY-MAIN`**.
3. Tại phần **Available Models**, click vào nút **`Import from /models`** ở góc phải.
4. Hệ thống sẽ tự động fetch danh sách từ `http://cf-ai-proxy:20127/v1/models` và nạp đầy đủ 9 model vào giao diện của bạn.

### 🔥 Tính năng bổ sung: Tự động Hot Reload cấu hình CSV
- Đã cài đặt một background file watcher chạy định kỳ **mỗi 5 giây** để giám sát sự thay đổi thời gian chỉnh sửa (`ModTime`) của `accounts.csv` và `models.csv`.
- Khi người dùng chỉnh sửa trực tiếp các file CSV này trên máy Host, proxy sẽ tự động nạp lại cấu hình tức thì trong nền (Zero-Downtime), không làm gián đoạn hay đứt kết nối HTTP đang chạy, đồng thời tự động kích hoạt đồng bộ Neurons cho các tài khoản mới nạp.

### ⚡ Tính năng bổ sung: Cấu hình tự động NVIDIA NIM
- Đã tạo script tự động hóa: [sync_nim_accounts.py](file:///home/ka/Repos/github.com/trongnghiango/llm-stack/scripts/sync_nim_accounts.py) đọc file `NIM_accounts.csv` ở root dự án và chèn cấu hình trực tiếp vào SQLite database của 9router.
- Đăng ký các connections dưới dạng Native Provider loại `nvidia` tích hợp sẵn trong 9router.
- Tối ưu hóa cấu trúc dữ liệu nạp để tương thích tốt nhất với giao diện quản lý và định tuyến của 9router.
