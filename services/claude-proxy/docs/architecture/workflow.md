# Quy trình Phát triển phần mềm bằng AI (AI Software Engineering Workflow)

Tài liệu này định nghĩa quy trình làm việc phối hợp đa mô hình (multi-model) và các nguyên tắc thiết kế/mã nguồn cần tuân thủ trong kho lưu trữ này.

## Quy trình tổng thể (Workflow Overview)

```text
       Yêu cầu của người dùng (User Task)
                       │
                       ▼
         GPT-OSS / OPUS (Lên kế hoạch)
                       │
                       ▼
           Bản kế hoạch (Execution Plan)
                       │
           ┌───────────┴───────────┐
           ▼                       ▼
     GLM-5.2 / SONNET       DeepSeek / HAIKU
  (Chỉnh sửa mã nguồn chính)   (Viết Unit Test/Thuật toán)
           │                       │
           └───────────┬───────────┘
                       ▼
             Bản vá (Repository Patch)
                       │
                       ▼
         GPT-OSS / OPUS (Đánh giá mã nguồn)
                       │
                       ▼
       Tài liệu hóa (MiniMax / CUSTOM)
```

---

## Chi tiết các bước thực hiện

### Bước 1: Lên kế hoạch (Planning - OPUS)
Mô hình đảm nhận vai trò kiến trúc sư (`swe.architect`) sẽ phân tích yêu cầu và xuất ra một bản kế hoạch thực thi rõ ràng bao gồm:
- Mục tiêu và các ràng buộc kỹ thuật.
- Tiêu chí chấp nhận (Acceptance criteria).
- Các module và tệp tin đích cần chỉnh sửa.
- Phân tích rủi ro và các kịch bản lỗi có thể xảy ra.

### Bước 2: Triển khai (Implementation - SONNET & HAIKU)
- **SONNET (`swe.engineer`)**: Thực hiện các thay đổi cốt lõi trên mã nguồn, cấu trúc thư mục, và các thay đổi phức tạp liên quan đến logic nghiệp vụ chính.
- **HAIKU (`swe.subagent` / `swe.utility`)**: Viết các unit test đi kèm, triển khai các thuật toán hoặc hàm phụ trợ (helper), và viết các đoạn code boilerplate lặp đi lặp lại.

### Bước 3: Đánh giá mã nguồn (Review - OPUS)
Sau khi có bản vá (patch), mô hình lập kế hoạch sẽ đối chiếu lại với bản kế hoạch ban đầu để kiểm tra:
- Đảm bảo tính toàn vẹn của kiến trúc hệ thống hiện tại.
- Đảm bảo độ bao phủ của kiểm thử (test coverage) đạt yêu cầu.
- Phát hiện và cảnh báo các rủi ro phát sinh hoặc lỗi hồi quy (regression).

### Bước 4: Viết tài liệu (Documentation - CUSTOM)
Mô hình tri thức/tài liệu (`swe.knowledge`) sẽ viết hướng dẫn sử dụng, cập nhật tệp tin `README.md`, ADR hoặc các mục lưu trữ tài liệu kỹ thuật khác.

---

## Nguyên tắc Lập trình của Kiến trúc sư (Architect Guidelines)

Khi chỉnh sửa bất kỳ phần nào của kho lưu trữ, các tác nhân AI phải tuân thủ nghiêm ngặt các nguyên tắc sau:
1. **Tôn trọng kiến trúc hiện tại**: Không tự ý giới thiệu các design pattern mới nếu đã có một pattern được thiết lập sẵn trong codebase.
2. **Tái sử dụng các hàm trừu tượng có sẵn**: Luôn tìm kiếm các hàm helper, tiện ích (utility) hiện có trong dự án trước khi viết một hàm mới.
3. **Ưu tiên thư viện chuẩn (Standard Library)**: Hạn chế tối đa việc thêm các thư viện bên thứ ba không thực sự cần thiết.
4. **Xóa mã nguồn chết**: Chủ động dọn dẹp các nhánh code không còn được sử dụng khi thực hiện tái cấu trúc (refactoring).
5. **Giới hạn phạm vi thay đổi**: Thực hiện các commit nhỏ, dễ kiểm duyệt thay vì các thay đổi quy mô lớn, ảnh hưởng lan rộng.
