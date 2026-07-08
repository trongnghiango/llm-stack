# Chính sách Tối ưu hóa Token & Hướng dẫn Hành vi (Token Optimization & Behavior Guidelines)

Tài liệu này quy định các phương pháp tối ưu hóa tài nguyên ngữ cảnh (context length) và hành vi phản hồi của các tác nhân AI khi tương tác với kho lưu trữ.

---

## Ma trận cấu hình tối ưu hóa (Optimization Configuration Matrix)

Để đảm bảo hiệu năng và độ ổn định khi chạy các tác vụ lập trình tự động kéo dài, các tính năng quản lý ngữ cảnh được phân bổ như sau:

| Tính năng | Trạng thái | Trường hợp sử dụng tối ưu | Tránh sử dụng cho |
| :--- | :--- | :--- | :--- |
| **RTK (Lossless Compression)** | **BẬT (ON)** | Nén không mất dữ liệu các đầu ra lệnh git, grep, log, directory tree. | Không có |
| **Headroom** | **TẮT (OFF) / Tùy chọn** | Các tệp tài liệu dài, bài viết nghiên cứu, tệp Markdown lớn, hệ thống RAG. | Lỗi biên dịch, stack trace, các tệp bản vá `.patch`. |
| **Caveman (Lite)** | **BẬT (ON)** | Mọi hoạt động sinh mã nguồn (code generation) và thực thi lệnh terminal. | Giải thích kiến trúc hệ thống cấp cao cần độ chi tiết lớn. |

---

## Mức độ ưu tiên của Ngữ cảnh (Context Priorities)

Trong trường hợp ngữ cảnh của phiên làm việc bị quá tải, thông tin sẽ được giữ lại theo thứ tự ưu tiên giảm dần dưới đây:
1. **Memory Graph**: Bản đồ quan hệ phụ thuộc giữa các tệp trong kho lưu trữ (luôn giữ lại để tránh mất kiểm soát kiến trúc).
2. **Claude Code Context**: Cơ chế cấp phát tài nguyên gốc của Claude Code để quản lý các active buffers.
3. **Lossless Compression (Nén không mất thông tin)**: Không bao giờ áp dụng tóm tắt mất dữ liệu (lossy summarization) đối với mã nguồn gốc (source code) hoặc log lỗi của trình biên dịch.

---

## Hướng dẫn Hành vi Caveman (Lite)

Để giảm thiểu chi phí token và tăng tốc độ phản hồi, hệ thống áp dụng chế độ **Caveman (Lite)** đối với tất cả các mô hình.

### Các yêu cầu bắt buộc đối với mô hình:
- **Giảm thiểu từ ngữ thừa**: Bỏ qua các câu chào hỏi xã giao, câu kết, hay các giải thích rườm rà (ví dụ: "Chắc chắn rồi", "Tôi rất vui được giúp đỡ...", "Như yêu cầu của bạn...").
- **Tập trung vào kỹ thuật**: Trình bày thẳng vào vấn đề kỹ thuật, nguyên nhân lỗi và giải pháp thực hiện.
- **Trực tiếp xuất mã nguồn**: Cung cấp các khối mã lệnh (code blocks) và lệnh terminal trực tiếp với văn bản giải thích tối giản nhất có thể.
- **Mẫu hội thoại chuẩn**:
  - *Không khuyến nghị*: "Chào bạn, tôi đã đọc tệp tin. Có vẻ như hàm này bị lỗi chia cho 0. Tôi sẽ giúp bạn sửa nó bằng cách thêm điều kiện if..."
  - *Khuyến nghị*: "Lỗi chia cho 0 tại `main.go:42`. Thêm kiểm tra điều kiện. Bản vá:" (sau đó in trực tiếp code block).
