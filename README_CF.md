# Hướng Dẫn Sử Dụng cf-ai-proxy trong llm-stack

`cf-ai-proxy` là dịch vụ Reverse Proxy viết bằng Go, đóng vai trò quan trọng trong việc chuyển dịch giao thức, quản lý và tối ưu hóa tài nguyên Cloudflare Workers AI.

---

## 🛠️ 1. Các Tính Năng Chính
1. **Dịch giao thức (Protocol Translation)**: Chuyển đổi định dạng request của Anthropic Claude `/v1/messages` sang định dạng OpenAI `/v1/chat/completions` tương thích với Cloudflare Workers AI.
2. **Hỗ trợ Tool Calling cho Claude Code**: Bóc tách và dịch các cấu trúc gọi tool dạng raw JSON từ Qwen/DeepSeek thành block `tool_use`/`tool_result` tương thích chuẩn Anthropic để Claude Code có thể gọi các tool cục bộ (`Read`, `Write`...) mà không bị văng lỗi.
3. **Cơ chế Hot Reload (Zero-Downtime)**: Tự động phát hiện thay đổi của các tệp tin CSV cấu hình và cập nhật trực tiếp vào RAM mỗi 5 giây mà không làm gián đoạn các kết nối chat đang chạy.
4. **Cân bằng tải & Tối ưu Quota**: Tự động luân chuyển request qua danh sách nhiều tài khoản Cloudflare để tránh vượt ngưỡng giới hạn Neurons (10,000 Neurons/ngày/account đối với tài khoản free).

---

## 📂 2. Cấu hình Accounts và Models

Cấu hình của proxy nằm hoàn toàn trong thư mục: `/home/ka/Repos/github.com/trongnghiango/llm-stack/services/cf-ai-proxy/`

### a) Danh sách tài khoản (`accounts.csv`)
Tệp tin này lưu danh sách các tài khoản Cloudflare dùng để gọi AI.

```csv
account_id,api_token
f694fb73_xxxxxxxx,cfut_xxxxxxxxxxxxxxxx
a3036b15_xxxxxxxx,cfat_xxxxxxxxxxxxxxxx
```

> [!IMPORTANT]
> **Quy định về Token:**
> - Bạn nên sử dụng **User Token** (bắt đầu bằng tiền tố **`cfut_`**) thay vì API Token thông thường (`cfat_`).
> - User Token (`cfut_`) có đặc quyền đọc dữ liệu thống kê Neurons qua GraphQL API của Cloudflare giúp Dashboard tính toán Neurons chính xác.

### b) Danh sách model (`models.csv`)
Tệp tin này ánh xạ tên model rút gọn sang tên model gốc của Cloudflare.

```csv
# alias,cf_model_name
deepseek-r1-distill-qwen-32b,@cf/deepseek-ai/deepseek-r1-distill-qwen-32b
qwen-2.5-coder,@cf/qwen/qwen2.5-coder-32b-instruct
```

> [!WARNING]
> **Quy định về Alias:**
> - Các model `alias` tuyệt đối **KHÔNG chứa ký tự gạch chéo `/`** (Ví dụ: Dùng `qwen-2.5-coder` thay vì `qwen/2.5-coder`). Ký tự `/` sẽ làm hỏng logic định tuyến (routing path) của 9router.

---

## 📈 3. Trang Giám Sát Neurons (Web Dashboard)
Hệ thống tích hợp sẵn trang Web dashboard giúp giám sát lượng Neurons đã tiêu thụ theo thời gian thực và tình trạng sống/chết (quota) của từng token.

- **Đường dẫn truy cập:** [http://localhost:20127/admin/dashboard](http://localhost:20127/admin/dashboard)
- Dữ liệu hiển thị bao gồm:
  - Tổng số Neurons đã tiêu thụ của từng tài khoản.
  - Trạng thái hoạt động (Active / Rate Limited / Lock).
  - Lịch sử Neuron usage đồng bộ từ GraphQL API của Cloudflare.

---

## 🔄 4. Quy trình Cập Nhật Cấu Hình
Khi bạn thêm/sửa tài khoản trong `accounts.csv` hoặc thêm model mới trong `models.csv`:
1. Bạn chỉ cần mở tệp tin tương ứng bằng trình soạn thảo và sửa trực tiếp.
2. Proxy chạy nền sẽ **tự động nạp lại cấu hình sau tối đa 5 giây**. Bạn không cần khởi động lại container docker.
3. Nếu muốn xóa cache phạt/lock của các token ngay lập tức sau khi nạp tiền/reset quota Cloudflare:
   ```bash
   docker exec llm-redis redis-cli flushall
   ```
