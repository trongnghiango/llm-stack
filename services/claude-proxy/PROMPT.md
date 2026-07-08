# PROMPT

### handoff
- Ghi lại nội dung của conversation hiện tại.
    - Nêu rõ những việc đã làm được và chưa được.
    - Đưa ra các câu hỏi mở dựa trên cuộc thảo luận hiện tại.
- Lưu lại tại thư mục dự án -> `docs/handoff/`
- Định dạng tên file: `YYYY-MM-DD-HHMM_handoff<-title-neu-muon-option>.md`

### takeover
- Quét thư mục `docs/handoff/` và tự động chọn file có định dạng `YYYY-MM-DD-HHMM_handoff*.md` mới nhất (dựa trên mốc thời gian ở tên file).
- Phân tích kỹ nội dung file handoff đó.
- Tóm tắt lại ngắn gọn: Tình trạng dự án hiện tại, việc đã làm, việc chưa làm.
- Trả lời các câu hỏi mở từ session trước.
- Đóng vai trò là trợ lý tiếp quản dự án, sẵn sàng cho các bước tiếp theo.