# Handoff: LLM Request Payload Logging
**Thời gian:** 2026-07-07

## 1. Nội dung cuộc hội thoại hiện tại (Conversation Content)
Cuộc hội thoại tập trung vào yêu cầu của người dùng muốn ghi lại toàn bộ nội dung yêu cầu (payload) gửi tới LLM upstream để kiểm tra cấu trúc system prompt, rules, messages, và tools cấu hình đi kèm. Sau khi triển khai, người dùng ban đầu chưa thấy log do proxy cũ chưa cập nhật và chatbot gặp lỗi hệ thống về quyền chạy lệnh shell. Chatbot đã hỗ trợ sửa lỗi thiếu import, biên dịch file nhị phân proxy mới thành công và commit toàn bộ thay đổi.

## 2. Những việc đã làm được (Done)
- **Thiết lập log payload**:
  - Triển khai hàm `LogPayload(reqID string, payload []byte)` trong `internal/logger/logger.go`.
  - Tự động chuyển đổi JSON dạng nén sang định dạng đẹp (pretty-print) với thụt dòng dễ đọc.
  - Lưu vào file cục bộ tại `logs/payloads/req-<trace_id>.json`.
  - Chạy bất đồng bộ thông qua goroutine giúp tránh gây ảnh hưởng đến hiệu năng hoạt động của proxy.
  - Bật/tắt linh hoạt thông qua biến môi trường `LOG_PAYLOADS=1` hoặc `LOG_DEBUG=1`.
- **Tích hợp vào proxy handler**:
  - Trích xuất mã trace ID `X-Request-ID` trong `handleProxy` (`cmd/proxy/main.go`).
  - Ghi log payload ngay trước khi forward request đã ghi đè target model sang upstream server.
- **Kiểm thử & Biên dịch**:
  - Viết unit test `TestLogPayload` và E2E test `TestProxyPayloadLogging` để kiểm tra độ ổn định và cách xử lý khi JSON lỗi/không hợp lệ.
  - Sửa lỗi thiếu import `"path/filepath"` giúp toàn bộ bộ test chạy qua 100%.
  - Biên dịch thành công file nhị phân `./proxy` để người dùng chạy trực tiếp.
- **Commit mã nguồn**:
  - Commit toàn bộ thay đổi thành công lên nhánh `main` (commit hash `6a3110b`).

## 3. Những việc chưa làm được (Undone)
- Chưa có tính năng tự động dọn dẹp (cleanup/rotator) các file log trong thư mục `logs/payloads/` khi chạy trong thời gian dài (tránh đầy ổ đĩa khi số lượng request lớn).

## 4. Các câu hỏi mở (Open Questions)
- Chúng ta có cần tích hợp một cơ chế tự động xoá hoặc giới hạn số lượng/dung lượng file log cũ trong `logs/payloads/` (log rotation) không?
- Bạn có muốn lọc bỏ bớt các trường thông tin nhạy cảm (như Authorization header hoặc API Key nếu vô tình có trong body) trong log payload trước khi ghi xuống đĩa cứng không?
- Bạn có muốn lưu trữ log payload dưới dạng một file JSONL duy nhất thay vì chia nhỏ thành từng file theo `reqID` hay không?
