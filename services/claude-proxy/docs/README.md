# Bản đồ Tài liệu (Documentation Hub)

Chào mừng bạn đến với hệ thống tài liệu kỹ thuật của kho lưu trữ **multi-llm-swe-protocol**. Thư mục `docs/` được cấu trúc thành các nhóm chức năng rõ ràng để hỗ trợ cả nhà phát triển (con người) và các tác nhân AI (agents) dễ dàng tra cứu, hiểu cấu trúc hệ thống và vận hành chính xác.

---

## 1. Cấu trúc Tài liệu (Documentation Structure)

### 🚀 Kiến trúc & Quy trình (Core Architecture & Flow)
Các tài liệu mô tả luồng hoạt động tổng thể của hệ thống và thiết kế kỹ thuật của bộ định tuyến:
- [Quy trình Phát triển phần mềm bằng AI (AI Workflow)](architecture/workflow.md) — Quy trình phối hợp đa mô hình (OPUS ➔ SONNET ➔ HAIKU) và nguyên tắc lập trình sạch.
- [Thiết kế Routing Proxy (Proxy Architecture)](architecture/proxy.md) — Chi tiết thiết kế kỹ thuật của Proxy định tuyến, luồng xử lý FSM, cấu trúc file và cách kiểm thử.

### 📋 Chính sách & Cấu hình (Policy & Guidelines)
Các tài liệu mô tả chính sách phân bổ mô hình và các cấu hình tối ưu hóa tài nguyên:
- [Chính sách Định tuyến & Ánh xạ Mô hình (Routing Policy)](policy/routing.md) — Cách cấu hình Layer 1 (Server Proxy qua `config.json`) và Layer 2 (Client qua `config/model_mappings.json`).
- [Tối ưu hóa Token & Hướng dẫn Hành vi (Token Optimization)](policy/optimization.md) — Quy định về chế độ Caveman (Lite), nén RTK và thứ tự ưu tiên của ngữ cảnh.

### 📝 Nhật ký Quyết định Thiết kế (ADR - Architectural Decision Records)
Hồ sơ ghi lại lý do đưa ra các quyết định thiết kế quan trọng trong quá khứ:
- [ADR-001: Lựa chọn mô hình cho các vai trò tác nhân](adr/001-model-selection.md)
- [ADR-002: Quản lý ngữ cảnh và chiến lược truy xuất](adr/002-context-management.md)
- [ADR-003: Tối ưu hóa Token và độ dài phản hồi](adr/003-token-optimization.md)

### 🤝 Lịch sử Bàn giao (Handoffs)
Nhật ký chuyển tiếp công việc giữa các phiên làm việc của tác nhân AI nằm trong thư mục [docs/handoff/](handoff/).

---

## 2. Hướng dẫn Luồng đọc (Suggested Reading Path)

### Dành cho nhà phát triển mới / AI Agent mới nhận việc:
1. Đọc [Quy trình AI Workflow](architecture/workflow.md) để hiểu vai trò của mình trong guồng máy phát triển chung.
2. Đọc [Chính sách Định tuyến](policy/routing.md) để biết các mô hình logic đang được ánh xạ sang mô hình vật lý nào.
3. Đọc [Chính sách Tối ưu hóa Token](policy/optimization.md) để biết cách giao tiếp chuẩn (ngắn gọn, trực diện, không filler words).
4. Đọc [Thiết kế Routing Proxy](architecture/proxy.md) nếu nhiệm vụ trực tiếp liên quan đến mã nguồn của bộ định tuyến Proxy.
