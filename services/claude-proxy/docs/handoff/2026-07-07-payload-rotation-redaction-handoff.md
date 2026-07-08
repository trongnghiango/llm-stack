# Handoff: LLM Request Payload Log Rotation & Redaction
**Thời gian:** 2026-07-07

## 1. Nội dung cuộc hội thoại hiện tại (Conversation Content)
Cuộc hội thoại tập trung vào việc hoàn thiện các tính năng còn thiếu cho cơ chế ghi log payload yêu cầu gửi tới LLM. Dựa trên phản hồi từ người dùng, chúng ta giữ nguyên thiết kế lưu log thành từng file riêng lẻ (`req-<trace_id>.json`), bổ sung cơ chế tự động dọn dẹp log theo số ngày tuổi (Clean by Age), và thực hiện ẩn/mã hóa các trường thông tin nhạy cảm trước khi ghi log xuống đĩa cứng.

## 2. Những việc đã làm được (Done)
- **Cấu hình tham số mới**:
  - Bổ sung `payload_log_retention_days` (mặc định: `7` ngày) và `redact_sensitive_payloads` (mặc định: `true`) vào struct `Config`.
  - Hỗ trợ ghi đè qua các biến môi trường tương ứng: `PAYLOAD_LOG_RETENTION_DAYS` và `REDACT_SENSITIVE_PAYLOADS`.
  - Thực hiện cập nhật tự động các tham số này cho logger khi config được khởi tạo hoặc reload bằng tín hiệu SIGHUP.
- **Ẩn thông tin nhạy cảm (Payload Redaction)**:
  - Triển khai hàm `RedactPayload(payload []byte) []byte` sử dụng đệ quy để duyệt và thay thế các trường nhạy cảm trong JSON body.
  - Lọc các key không phân biệt chữ hoa/thường: `api_key`, `apikey`, `api-key`, `token`, `access_token`, `secret`, `password`, `authorization`, `auth`.
  - Lọc các giá trị chuỗi khớp với định dạng API key chung (bắt đầu bằng `sk-` hoặc `Bearer `).
  - Tích hợp gọi ẩn danh trong luồng ghi log bất đồng bộ của `LogPayload`.
- **Dọn dẹp log tự động (Log Rotation/Cleanup)**:
  - Triển khai hàm `CleanOldPayloadLogs()` quét thư mục log và xóa các file khớp mẫu `req-*.json` có thời gian sửa đổi (Modification Time) cũ hơn thời gian cấu hình.
  - Khởi chạy một goroutine worker định kỳ mỗi giờ (`StartPayloadLogCleanupWorker`) trong hàm `main()` của proxy để quét dọn tự động.
- **Kiểm thử & Xác minh**:
  - Viết unit test cho các tham số cấu hình và override qua biến môi trường.
  - Viết unit test kiểm chứng độ chính xác của cơ chế lọc thông tin nhạy cảm đối với JSON hợp lệ, JSON lồng nhau, mảng và các payload lỗi cấu trúc.
  - Viết unit test giả lập file log cũ bằng cách thay đổi mod time (`os.Chtimes`) để kiểm tra hàm dọn dẹp log.
  - Chạy `go test ./...` đạt 100% pass và `go vet ./...` không có lỗi phân tích tĩnh.
  - Biên dịch thành công file nhị phân `./proxy`.

## 3. Những việc chưa làm được (Undone)
- Không có. Tất cả các yêu cầu về dọn dẹp log theo độ tuổi và ẩn thông tin nhạy cảm đã được hoàn thành đầy đủ.

## 4. Các câu hỏi mở (Open Questions)
- Chúng ta có cần cung cấp một cờ CLI hoặc endpoint admin (ví dụ: `/admin/clean`) để quản trị viên có thể kích hoạt dọn dẹp log thủ công ngay lập tức mà không phải chờ worker chạy nền không?
- Bạn có nhu cầu cho phép người dùng tùy biến danh sách đen các trường cần ẩn (custom blacklists) thông qua file cấu hình `config.json` hay không?
