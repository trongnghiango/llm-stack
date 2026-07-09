# PROMPT GUIDELINES

> **🤖 AGENT INSTRUCTION (HƯỚNG DẪN CHO AGENT)**:
> 1. Khi nhận được câu lệnh dạng `[chức_năng] theo PROMPT L1-L16`, bạn **chỉ được phép đọc L1-L16** của file `PROMPT.md` để lấy Mục lục (TOC).
> 2. Phân tích TOC bên dưới để tìm dải dòng (Line Range) của chức năng tương ứng (ví dụ: `handoff` là `L19-L27`).
> 3. Sử dụng công cụ đọc file (hoặc lệnh `sed -n 'start,endp'`) để chỉ đọc đúng dải dòng đó, không đọc toàn bộ file.
> 4. Tiến hành thực thi chức năng theo đúng chỉ dẫn của dải dòng vừa đọc.

## 📇 MỤC LỤC TRA CỨU NHANH (TOC)
* [1. handoff (Bàn giao ca) (L19-L27)](#-1-handoff-ban-giao-ca)
* [2. takeover (Tiếp quản ca) (L30-L38)](#-2-takeover-tiep-quan-ca)
* [3. diagnose (Chẩn đoán lỗi hệ thống) (L41-L51)](#-3-diagnose-chan-doan-loi-he-thong)
* [4. verify (Xác minh sau chỉnh sửa) (L54-L61)](#-4-verify-xac-minh-sau-chinh-sua)
* [5. refactor (Tái cấu trúc & Dọn dẹp code) (L64-L71)](#-5-refactor-tai-cau-truc--don-dep-code)
* [6. Các câu lệnh mẫu (Sample Prompts) (L74-L80)](#-6-cac-cau-lenh-mau-sample-prompts)

---

### 📋 1. handoff (Bàn giao ca)
* **Mục đích**: Ghi lại trạng thái công việc khi kết thúc phiên làm việc để Agent ca sau tiếp quản dễ dàng.
* **Hành động**:
  * Tóm tắt toàn bộ nội dung của hội thoại hiện tại.
  * Nêu rõ các hạng mục **đã làm được** và **chưa làm được** (tồn đọng).
  * Đặt ra các câu hỏi mở hoặc gợi ý hướng phát triển tiếp theo dựa trên cuộc thảo luận hiện tại.
  * Lưu vào thư mục: `docs/handoff/`
  * Định dạng tên file: `YYYY-MM-DD-HHMM_handoff<-title-neu-muon-option>.md`

---

### 📋 2. takeover (Tiếp quản ca)
* **Mục đích**: Nhận diện và tiếp tục công việc từ Agent ca trước mà không bị mất dấu ngữ cảnh.
* **Hành động**:
  * Quét thư mục `docs/handoff/` và tự động đọc file `YYYY-MM-DD-HHMM_handoff*.md` mới nhất.
  * Phân tích kỹ nội dung file handoff đó.
  * Tóm tắt ngắn gọn cho người dùng: Tình trạng dự án hiện tại, việc đã làm, việc chưa làm.
  * Trả lời các câu hỏi mở từ session trước (nếu có).
  * Đóng vai trò là trợ lý tiếp quản dự án, sẵn sàng nhận các yêu cầu tiếp theo.

---

### 📋 3. diagnose (Chẩn đoán lỗi hệ thống)
* **Mục đích**: Cô lập và tìm nguyên nhân gốc của lỗi (API 400/500, crash container, treo cổng...) một cách bài bản.
* **Hành động**:
  * **Bước 1 (Log Sweep)**: Chạy lệnh lấy log của container/dịch vụ bị nghi ngờ (ví dụ: `docker compose logs --tail=100 <service>`).
  * **Bước 2 (Config Check)**: Rà soát các file cấu hình liên quan (`.env`, `config.json`, `accounts.csv`) để tìm lỗi cú pháp hoặc thiếu biến.
  * **Bước 3 (Isolated Testing)**: Viết script test nhanh bằng Python hoặc Curl trong thư mục `scratch/` để giả lập gọi API trực tiếp.
  * **Bước 4 (Report)**: Tạo báo cáo chẩn đoán tại `docs/diagnose/YYYY-MM-DD-HHMM_diagnose.md` nêu rõ:
    1. Hiện tượng lỗi xảy ra.
    2. Nguyên nhân gốc rễ (Root Cause).
    3. Phương án xử lý đề xuất.

---

### 📋 4. verify (Xác minh sau chỉnh sửa)
* **Mục đích**: Đảm bảo mọi thay đổi về mã nguồn không phá vỡ hệ thống hiện tại trước khi bàn giao.
* **Hành động**:
  * **Bước 1 (Lint & Format)**: Định dạng lại code để đảm bảo chuẩn phong cách lập trình (ví dụ: `go fmt ./...`).
  * **Bước 2 (Compilation Check)**: Biên dịch thử dự án để xác nhận không có lỗi cú pháp hay import (ví dụ: `go build` hoặc `npm run build`).
  * **Bước 3 (Unit Tests)**: Tìm và chạy toàn bộ unit test liên quan đến khu vực vừa sửa đổi (ví dụ: `go test -v ./...` hoặc `npm test`).
  * **Bước 4 (Walkthrough)**: Ghi nhận kết quả xác minh vào tệp tin walkthrough hoặc tạo tài liệu kiểm thử nhanh trong `docs/verify/`.

---

### 📋 5. refactor (Tái cấu trúc & Dọn dẹp code)
* **Mục đích**: Nâng cao chất lượng mã nguồn, dọn dẹp các đoạn code nháp hoặc dư thừa.
* **Hành động**:
  * Xóa bỏ hoàn toàn các biến không sử dụng, thư viện import dư thừa, hoặc các dòng log debug tạm bợ.
  * Bổ sung tài liệu (docstring/comments) giải thích rõ logic cho các khối code phức tạp.
  * Đảm bảo giữ nguyên các comment cũ không liên quan.
  * Thực hiện commit thay đổi theo chuẩn **Conventional Commits** (ví dụ: `refactor: clean up unused variables inside handler.go`).

---

### 📋 6. Các câu lệnh mẫu (Sample Prompts)
Để kích hoạt nhanh các quy trình trên bằng cách hướng dẫn Agent sử dụng mẹo đọc đúng dải dòng:
* **handoff**: `"handoff theo PROMPT L1-L16"`
* **takeover**: `"takeover theo PROMPT L1-L16"`
* **diagnose**: `"diagnose theo PROMPT L1-L16 để chẩn đoán lỗi container cf-ai-proxy."`
* **verify**: `"verify theo PROMPT L1-L16 để xác minh code Go trong cf-ai-proxy."`
* **refactor**: `"refactor theo PROMPT L1-L16 để dọn dẹp biến thừa trong handler.go."`