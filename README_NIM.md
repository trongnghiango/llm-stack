# Hướng Dẫn Cấu Hình Tự Động NVIDIA NIM trong llm-stack

Tài liệu này hướng dẫn cách tự động nạp (sync) danh sách các tài khoản NVIDIA NIM từ tệp cấu hình CSV vào hệ thống 9router trong dự án hợp nhất `llm-stack`.

---

## 📋 Bước 1: Chuẩn bị file CSV cấu hình
Đảm bảo tệp tin **`NIM_accounts.csv`** nằm ở thư mục gốc của dự án `llm-stack` (`/home/ka/Repos/github.com/trongnghiango/llm-stack/NIM_accounts.csv`) có định dạng chuẩn như sau:

```csv
ID,name,token,expiration
bb0b2348-fe3c-4db7-b872-87e90,NIM_GOON_003,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,07/09/2027
```

*Lưu ý:*
- Cột `ID` có thể để trống hoặc điền mã bất kỳ. Nếu ID không chuẩn UUIDv4 (36 ký tự), script sẽ tự động sinh UUIDv4 chuẩn mới để vượt qua các bộ kiểm tra (zod validators) của 9router.
- Cột `name` là tên hiển thị của kết nối (Ví dụ: `NIM_GOON_003`).
- Cột `token` là API Key NVIDIA NIM bắt đầu bằng `nvapi-`.

---

## ⚡ Bước 2: Chạy script đồng bộ hóa
Chạy script Python nằm trong thư mục `scripts/` của `llm-stack`:

```bash
python3 scripts/sync_nim_accounts.py
```

Script sẽ tự động:
1. Đọc và phân tích file `NIM_accounts.csv`.
2. Tạo/Cập nhật các kết nối tương ứng loại provider **`nvidia`** trong SQLite database của 9router.
3. Đồng bộ hóa trực tiếp vào cơ sở dữ liệu thực tế đang chạy.

---

## 🔄 Bước 3: Làm mới bộ nhớ đệm (Flush Cache)
Vì 9router sử dụng Redis để lưu bộ nhớ đệm (cache), sau khi chạy script đồng bộ hóa, hãy chạy 2 lệnh sau để làm sạch cache và khởi động lại 9router nhận diện ngay lập tức:

```bash
# 1. Xóa sạch mọi cache cũ trong Redis
docker exec llm-redis redis-cli flushall

# 2. Khởi động lại container 9router
docker compose restart 9router
```

Sau khi hoàn thành, bạn chỉ cần F5/Refresh trình duyệt và truy cập quản trị 9router tại: [http://localhost:20128](http://localhost:20128) để kiểm tra các kết nối NVIDIA NIM đã hiển thị đầy đủ và xanh tươi (`active`).
