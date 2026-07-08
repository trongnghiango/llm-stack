# Handoff: Tái cấu trúc cấu trúc thư mục dự án (Go Standard Layout)
**Thời gian:** 2026-07-07

## 1. Tóm tắt công việc đã thực hiện
Trong session này, dự án đã được chuyển đổi từ cấu trúc phẳng (flat structure) ở thư mục gốc sang cấu trúc chuẩn dự án Go chuyên nghiệp (**Standard Go Layout**) sử dụng `cmd/` và `internal/`.

### Các package được định nghĩa:
- **`cmd/proxy/`**: Chứa entrypoint thực thi chính của reverse proxy (`main.go`, `flags.go`, `health.go` và tests).
- **`internal/config/`**: Đọc, phân tích cấu hình JSON và xác thực tham số đầu vào (`config.go`, `config_loader.go` và tests).
- **`internal/router/`**: Bộ máy FSM định tuyến mô hình, TTL Cache, LLM Circuit Breaker, State quản lý và tests liên quan.
- **`internal/logger/`**: Triển khai Logger ghi log bất đồng bộ mức độ cao, ghi log định dạng tiếng Việt "Vì...nên...".
- **`internal/metrics/`**: Khai báo các metric Prometheus giám sát gateway proxy.
- **`internal/utils/`**: Chứa HttpClient toàn cục và các middleware bảo mật & trace ID.

## 2. Giải quyết Circular Dependency (Vòng lặp Import)
Trong quá trình tổ chức package:
- `internal/router` cần import `internal/utils` để sử dụng `HTTPClient`.
- Lúc đầu, `internal/utils` (chứa `health.go`) lại cần import `internal/router` để kiểm tra trạng thái của Circuit Breaker và cấu hình router.
- **Giải pháp:** Di chuyển file kiểm tra sức khỏe `health.go` từ `internal/utils/` lên package `main` ở `cmd/proxy/`. Nhờ vậy, `internal/utils` hoàn toàn độc lập với `router`, tháo gỡ hoàn toàn vòng lặp import một cách tự nhiên và sạch sẽ.

## 3. Các điểm cải tiến về Code (APIs & Helper)
- **`logger.SetupTestLogger(t testing.TB) string`**: Hàm helper giúp thiết lập logger tạm thời cô lập trong thư mục test `t.TempDir()`. Rất hữu dụng để tránh log rác làm ô nhiễm terminal khi test các package.
- **`router.SetRouterState(st *RouterState)`**: Hàm ghi đè/thiết lập nhanh trạng thái định tuyến của router phục vụ cho mock/E2E test hoặc CLI flags override.
- **`router.ClearCache()`**: Giải phóng/xóa toàn bộ quyết định định tuyến đã lưu trong cache.

## 4. Trạng thái hiện tại của dự án
- Nhánh hiện tại: `main`
- Toàn bộ thay đổi đã được commit thành công với mã commit `8340e68`.
- Cây làm việc hoàn toàn sạch sẽ (`working tree clean`).
- Bỏ qua các tệp tạm của Claude (`.claude/`, `.agents/`) trong `.gitignore`.
- Thử nghiệm build thành công: `go build -o proxy ./cmd/proxy`
- Toàn bộ test của dự án chạy qua thành công 100% (cả package test độc lập lẫn E2E proxy test): `go test -v ./...`

## 5. Hướng dẫn Session tiếp theo
- Dự án đã hoàn toàn sẵn sàng cho bất kỳ tính năng mới nào.
- Khi viết mã nguồn mới:
  - Entry point và HTTP handler chính nằm ở `cmd/proxy/`.
  - Core logic định tuyến nằm ở `internal/router/`.
  - Helper hoặc middleware nằm ở `internal/utils/`.
  - Cấu hình tải hoặc xác thực nằm ở `internal/config/`.
- Luôn kiểm tra tính đúng đắn bằng lệnh:
  ```bash
  go test -v ./...
  go vet ./...
  go build -o proxy ./cmd/proxy
  ```
