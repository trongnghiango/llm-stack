Viewed handler.go:300-360
Searched web: "handleAnthropicStream tool_calls"

## 📋 Kế hoạch thực hiện “Tool Write” trực tiếp trên proxy

### Mục tiêu
Khi mô hình Claude‑code trả về một `tool_use` (ví dụ: **Write**), proxy sẽ:
1. **Phân tích** `tool_calls` nhận từ Cloudflare.  
2. **Thực thi** công cụ trên máy chủ (ghi file, cập nhật file, …).  
3. **Thay thế** block `tool_use` bằng một block `tool_result` chứa kết quả (hoặc thông báo lỗi).  
4. Trả về phản hồi cho client theo định dạng Anthropic (đúng `role`, `content`, `stop_reason`).

### Các bước thay đổi mã

| File | Thay đổi | Mô tả |
|------|----------|------|
| `handler.go` | 1️⃣ Thêm hàm **runLocalTool** (ở cuối file) | Nhận `name` và `argsMap`, thực hiện hành động. Hiện tại chỉ hỗ trợ `Write` (viết nội dung vào file). Trả về chuỗi kết quả hoặc lỗi. |
| `handler.go` | 2️⃣ Cập nhật **handleAnthropicStandard** (ở phần xử lý `tool_calls`) | - Sau khi tạo `toolCalls` → gọi `runLocalTool(name, argsMap)`.<br>- Nếu thành công, thay `type: "tool_use"` bằng `type: "tool_result"` và gắn `content` là kết quả.<br>- Nếu có lỗi, trả về `tool_result` với thông báo lỗi. |
| `handler.go` | 3️⃣ Cập nhật **handleAnthropicStream** (vòng xử lý `xmlToolCalls`) | - Khi phát hiện `tool_use` trong luồng, lưu `id`, `name`, `args` vào một map tạm.<br>- Khi gặp `tool_result` (hoặc khi luồng kết thúc) – ở đây chúng ta sẽ **thực thi** ngay khi toàn bộ `tool_use` được nhận (sau `content_block_stop`).<br>- Gọi `runLocalTool` và viết lại các sự kiện `content_block_start/delta/stop` để gửi một `tool_result` block thay cho `tool_use`. |
| `handler.go` | 4️⃣ Thêm **logging debug** để dễ quan sát việc gọi tool | Log tên tool, đường dẫn file, kết quả/ lỗi. |
| `go.mod` (nếu cần) | Không thay đổi – các thư viện chuẩn (`os`, `io/ioutil`, `log`) đã đủ. |
| **Tests** (tùy chọn) | Thêm một test đơn giản trong `main_test.go` để gửi request có `tools: [{name:"Write", ...}]` và kiểm tra file được tạo. | Không bắt buộc để triển khai, nhưng nên thêm để bảo chứng. |

### Chi tiết hàm `runLocalTool`

```go
// runLocalTool thực thi tool nội bộ.
// Hiện tại chỉ hỗ trợ tên "Write" với đối số:
//   - file_path (string): đường dẫn file (relative/absolute)
//   - content   (string): nội dung cần ghi
// Trả về nội dung kết quả (thành công) hoặc thông báo lỗi.
func (h *ProxyHandler) runLocalTool(name string, args map[string]interface{}) (string, error) {
    switch name {
    case "Write":
        // Lấy tham số
        fpRaw, ok1 := args["file_path"]
        contentRaw, ok2 := args["content"]
        if !ok1 || !ok2 {
            return "", fmt.Errorf("missing required arguments for Write")
        }
        filePath, ok1 := fpRaw.(string)
        content, ok2 := contentRaw.(string)
        if !ok1 || !ok2 {
            return "", fmt.Errorf("invalid argument types for Write")
        }

        // Đảm bảo thư mục tồn tại
        dir := filepath.Dir(filePath)
        if err := os.MkdirAll(dir, 0o755); err != nil {
            return "", fmt.Errorf("failed to create directories: %w", err)
        }

        // Ghi file (overwrite)
        if err := os.WriteFile(filePath, []byte(content), 0o644); err != nil {
            return "", fmt.Errorf("failed to write file: %w", err)
        }

        log.Printf("[Tool-Write] Đã ghi %d byte vào %s", len(content), filePath)
        return fmt.Sprintf("File written successfully: %s", filePath), nil

    default:
        return "", fmt.Errorf("tool %s chưa được hỗ trợ", name)
    }
}
```

### Cập nhật `handleAnthropicStandard`

Sau đoạn `for _, tc := range toolCalls { … }` sẽ thay đổi thành:

```go
for _, tc := range toolCalls {
    if tcMap, ok := tc.(map[string]interface{}); ok {
        id, _ := tcMap["id"].(string)
        name, _ := tcMap["name"].(string)
        args := tcMap["arguments"]

        // Chuyển args sang map
        var argsMap map[string]interface{}
        if argsStr, ok := args.(string); ok {
            json.Unmarshal([]byte(argsStr), &argsMap)
        } else if am, ok := args.(map[string]interface{}); ok {
            argsMap = am
        }

        // **Thực thi tool**
        resultStr, err := h.runLocalTool(name, argsMap)
        if err != nil {
            resultStr = fmt.Sprintf("Tool execution error: %v", err)
        }

        // Thêm block tool_result thay cho tool_use
        content = append(content, map[string]interface{}{
            "type":        "tool_result",
            "tool_use_id": id,
            "content":     resultStr,
        })
    }
}
```

- `stopReason` sẽ được đặt thành `"tool_result"` nếu ít nhất một tool đã được thực thi.

### Cập nhật `handleAnthropicStream`

Trong vòng lặp `for _, tc := range xmlToolCalls` (đoạn 497‑530) sẽ:

1. Thu thập `id`, `name`, `argsMap` như trên.  
2. Khi nhận `content_block_stop` cho `index == 1` (đây là `tool_use`), **ngừng buffer** và gọi `runLocalTool`.  
3. Gửi lại các sự kiện SSE:
   - `content_block_start` (type `tool_result`)  
   - `content_block_delta` (type `text_delta`, `text` = kết quả)  
   - `content_block_stop`  

Điều này tái tạo luồng tương tự như Cloudflare nhưng với kết quả đã được tính toán nội bộ.

### Kiểm tra & Xác nhận

1. **Khởi động proxy** (`./cf-ai-proxy`).  
2. Gửi yêu cầu Anthropic tới endpoint `/v1/anthropic/completions` (hoặc tương tự) với payload:
   ```json
   {
     "model": "@cf/qwen/qwen2.5-coder-32b-instruct",
     "messages": [{ "role":"user", "content":"Hãy ghi file /tmp/demo.txt với nội dung Hello"}],
     "tools": [{
       "name":"Write",
       "description":"Ghi nội dung vào file",
       "input_schema": {
         "type":"object",
         "properties": {
           "file_path":{"type":"string"},
           "content":{"type":"string"}
         },
         "required":["file_path","content"]
       }
     }],
     "tool_choice":"auto"
   }
   ```
3. Kiểm tra:
   - File `/tmp/demo.txt` tồn tại với nội dung `Hello`.  
   - Phản hồi JSON chứa `content` block loại `tool_result` và `stop_reason":"tool_result"`.

### Rủi ro & Biện pháp

| Rủi ro | Giải pháp |
|--------|-----------|
| Việc ghi file có thể bị tấn công nếu người dùng gửi đường dẫn trái phép. | Kiểm tra đường dẫn: chỉ cho phép ghi trong thư mục dự án (`/home/ka/Repos/github.com/trongnghiango/cf-ai-proxy/docs/` hoặc subfolder an toàn). Có thể thêm `filepath.Clean` và so sánh prefix. |
| Các tool khác chưa được hỗ trợ. | Mở rộng `runLocalTool` khi cần. |
| Xung đột giữa `tool_use` và `tool_result` trong luồng SSE. | Đảm bảo `content_block_stop` được gửi đúng thứ tự và không trùng lặp. |

---

## 🎯 Hành động tiếp theo

1. **Tạo artifact `implementation_plan.md`** với nội dung trên (đã thực hiện).  
2. **Yêu cầu người dùng phê duyệt**: nếu bạn đồng ý, tôi sẽ thực hiện các thay đổi code thực tế (sử dụng `replace_file_content` / `multi_replace_file_content`).  

Bạn chỉ cần phản hồi **“phê duyệt”** hoặc cung cấp bất kỳ yêu cầu bổ sung nào (ví dụ: giới hạn đường dẫn ghi, thêm tool `Update`, v.v.).  

---  

**Bạn muốn tôi tiếp tục thực hiện các thay đổi trên không?**