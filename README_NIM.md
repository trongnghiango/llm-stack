# Hướng Dẫn Cấu Hình Tự Động NVIDIA NIM trong llm-stack

Tài liệu này hướng dẫn cách tự động nạp (sync) danh sách các tài khoản NVIDIA NIM từ tệp cấu hình CSV vào hệ thống 9router trong dự án hợp nhất `llm-stack`.

---

## 📋 Bước 1: Chuẩn bị file CSV cấu hình
Đảm bảo tệp tin **`NIM_accounts.csv`** nằm ở thư mục gốc của dự án `llm-stack` (`/home/ka/Repos/github.com/trongnghiango/llm-stack/NIM_accounts.csv`) có định dạng chuẩn như sau:

```csv
ID,name,token,expiration
bb0b2348-fe3c-4db7-b872-87e90,NIM_GOON_003,nvapi-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx,07/09/2027
```

*Lưu ý:*
- Cột `ID` có thể để trống hoặc điền mã bất kỳ. Nếu ID không chuẩn UUIDv4 (36 ký tự), script sẽ tự động sinh UUIDv4 chuẩn mới để vượt qua các bộ kiểm tra (zod validators) của 9router.
- Cột `name` là tên hiển thị của kết nối (Ví dụ: `NIM_GOON_003`).
- Cột `token` là API Key NVIDIA NIM bắt đầu bằng `nvapi-`.

---

## ⚡ Bước 2: Chạy đồng bộ hóa bằng CLI tool
Thay vì gõ nhiều lệnh đơn lẻ, bạn chỉ cần sử dụng CLI tích hợp sẵn ngay tại thư mục gốc của dự án:

```bash
./stack sync-nim
```

Lệnh trên sẽ tự động thực hiện:
1. Đọc file `NIM_accounts.csv` và import connections loại `nvidia` vào database SQLite của 9router.
2. Xóa sạch bộ nhớ đệm (cache) trong Redis (`redis-cli flushall`).
3. Khởi động lại container `9router` để nạp cấu hình mới.

Sau khi hoàn thành, bạn chỉ cần F5/Refresh trình duyệt và truy cập quản trị 9router tại: [http://localhost:20128](http://localhost:20128) để kiểm tra các kết nối NVIDIA NIM đã hiển thị đầy đủ và xanh tươi (`active`).
