# Phân tích Kỹ thuật — 6 Lỗi & 10 Đề xuất Cải tiến (2026-07-06)

Tài liệu này ghi lại quá trình phân tích kỹ thuật hệ thống Proxy Go ở thời điểm nhận bàn giao (commit `662aa27`).

---

## 1. Phân tích 6 Vấn đề Correctness & Operability

### Lỗi 1 — Cache poisoning (routing_fsm.go)
- **Triệu chứng:** Khi bộ phân loại LLM gặp lỗi (timeout, lỗi 500, JSON sai cấu trúc), FSM của router sẽ chuyển hướng fallback về static route hoặc khớp từ khóa. Tuy nhiên, FSM vô tình ghi lại kết quả fallback/keyword này vào `decisionCache`. Các request tiếp theo với cùng prompt sẽ luôn lấy từ cache ra kết quả fallback/keyword này mà không bao giờ thử gọi lại LLM nữa.
- **Giải pháp:** Xóa lệnh `decisionCache.Store` ở các nhánh `stateKeywordMatch` và `stateFallbackRule`. Chỉ ghi nhận kết quả thành công được LLM xác thực.

### Lỗi 2 — Bất nhất trạng thái cấu hình (router.go / routing_fsm.go)
- **Triệu chứng:** Khi có tín hiệu SIGHUP reload cấu hình, config được cập nhật nguyên tử qua `globalState.Store()`. Tuy nhiên, các hàm con trong FSM (như `resolveDynamic` và `resolveRecursive`) lại truyền nhận một bản sao `cfg Config` riêng lẻ tại các thời điểm khác nhau. Điều này có thể dẫn đến việc cấu hình và các bản đồ lookup (maps) thuộc về hai thế hệ cấu hình khác nhau trong một khoảng thời gian cực kỳ hẹp.
- **Giải pháp:** Loại bỏ tham số `cfg` khỏi các hàm con. Thay vào đó, gọi `getState()` đúng một lần ở đầu hàm xử lý routing FSM (`resolveDynamic`), lấy cả `config` lẫn `maps` từ cùng một snapshot nguyên tử.

### Lỗi 3 — Rò rỉ bộ nhớ từ cache không giới hạn (utils.go)
- **Triệu chứng:** Bộ cache cũ sử dụng `sync.Map` toàn cục không có cơ chế hết hạn (TTL) hay dọn dẹp (eviction). Do prompt đầu vào của lập trình viên SWE thường là độc nhất (unique), bộ cache sẽ phình to vô hạn theo thời gian và gây cạn kiệt RAM.
- **Giải pháp:** Thiết kế `ttlCache` tùy chỉnh sử dụng `map[string]cacheEntry` được bảo vệ bằng `sync.RWMutex`. Thêm thời gian sống (TTL) mặc định 30 phút (có thể cấu hình qua env `ROUTER_CACHE_TTL_MINUTES`) và một goroutine dọn dẹp định kỳ mỗi 5 phút.

### Lỗi 4 — Thiếu khả năng truy vết Request-ID (middleware.go)
- **Triệu chứng:** Khi các request chạy song song, log của hệ thống proxy đan xen vào nhau khiến việc phân tích lỗi định tuyến của một request cụ thể trở nên cực kỳ khó khăn.
- **Giải pháp:** Tạo middleware tự động sinh `X-Request-ID` dưới dạng mã hash 16 ký tự ngẫu nhiên nếu client không gửi. Đảm bảo trả ID này trong response header của client và chuyển tiếp (forward) lên LLM upstream để tăng tính liên kết trace ID.

### Lỗi 5 & 6 — Thiếu kiểm thử đơn vị & Biên kiểm (config_loader_test.go / router_test.go)
- **Triệu chứng:** Không có test kiểm tra reload cấu hình qua SIGHUP khi cấu hình sai (có thể gây crash). Hàm `callLLMRouter` gọi HTTP client cũng chưa được unit test riêng cho các dạng payload phản hồi lỗi/thành công từ Anthropic/OpenAI.
- **Giải pháp:**
  - Bổ sung unit test cho việc reload cấu hình hợp lệ và không hợp lệ (đảm bảo không panic khi file config lỗi/mất).
  - Viết test bảng (table-driven test) kiểm thử 7 định dạng response và kịch bản timeout/mất kết nối của LLM Router.

---

## 2. Phân tích 10 Đề xuất Cải tiến

### Đề xuất 1: Xác thực cấu hình chặt chẽ (Startup & SIGHUP)
- **Phân tích:** Proxy cần fail-fast nếu config lỗi thay vì chạy ngầm và trả lỗi 502/503 cho toàn bộ request.
- **Giải pháp:** Viết hàm `validateConfig` bằng Go struct thuần (không import thư viện JSON schema ngoài để tránh tăng dung lượng binary và tech debt).

### Đề xuất 2: Nâng cấp chiến lược Cache (LRU-có-TTL)
- **Phân tích:** Thay thế cache bằng thư viện ngoài `hashicorp/golang-lru`. Tuy nhiên, vì `ttlCache` tùy chỉnh vừa viết ở session trước hoạt động ổn định và an toàn, việc import thêm thư viện ngoài chỉ để thay thế 100 dòng code hiện có là không cần thiết. Thay vào đó, bổ sung thêm metric `cache_evictions_total` vào cơ chế dọn dẹp hiện tại.

### Đề xuất 3: Circuit-breaker bảo vệ LLM calls
- **Phân tích:** Nếu upstream LLM classifier bị quá tải hoặc sập, proxy không nên tiếp tục gửi request và chịu timeout 8 giây cho mỗi request.
- **Giải pháp:** Tự triển khai mẫu Circuit Breaker 3 trạng thái (Closed, Open, Half-Open) gọn nhẹ trong 70 dòng code Go. Khi lỗi consecutive đạt ngưỡng, CB sẽ mở ra và tự động ngắt kết nối LLM, trả ngay `"FALLBACK"` để FSM định tuyến qua từ khóa tức thì.

### Đề xuất 4: Log có cấu trúc dạng JSON (Structured Logging)
- **Phân tích:** Log dạng text tốt cho lập trình viên đọc lúc dev, log JSON cần thiết cho việc phân tích tự động bằng ELK/Datadog.
- **Giải pháp:** Sử dụng dual-mode logging. Ghi đồng thời text log và JSON log (dạng `.jsonl`) một cách không đồng bộ.

### Đề xuất 5: Bổ sung Prometheus Metrics & Debug Health
- **Phân tích:** Cần đo lường chính xác thời gian xử lý của từng công đoạn định tuyến và trạng thái sống của upstream/CB.
- **Giải pháp:** Đăng ký thêm 5 metrics mới bao gồm histogram đo latency chi tiết theo stage và endpoint `/debug/health`.

### Đề xuất 6: OpenAPI/Swagger
- **Phân tích:** Proxy chỉ chuyển tiếp nguyên bản Anthropic API, không thay đổi API hay thêm route nghiệp vụ mới. Việc sinh tài liệu OpenAPI cho các route hệ thống là không mang lại giá trị thực tế. Đề xuất hoãn lại.

### Đề xuất 7: Graceful Shutdown & Signal Isolation
- **Phân tích:** Tránh tình trạng reload và shutdown xảy ra đồng thời gây race condition. Đảm bảo in-flight requests được xử lý xong trước khi thoát.
- **Giải pháp:** Tách biệt goroutine xử lý tín hiệu, sử dụng cờ hiệu atomic bảo vệ, và dùng `server.Shutdown` chặn main goroutine.

### Đề xuất 8: Security Hardening (Whitelists & Payload limits)
- **Phân tích:** Phòng chống tấn công từ chối dịch vụ bằng payload khổng lồ và cảnh báo việc định tuyến sai model.
- **Giải pháp:** Sử dụng `http.MaxBytesReader` giới hạn dung lượng request body (mặc định 5 MiB) và kiểm tra whitelist model.

### Đề xuất 9 & 10: Test coverage & README Docs
- **Phân tích:** Tăng độ tin cậy của code CB mới và hướng dẫn quản trị viên vận hành hệ thống.
- **Giải pháp:** Bổ sung unit test cho circuit breaker, viết fuzzer cho config loader, và cập nhật tài liệu hướng dẫn PromQL.
