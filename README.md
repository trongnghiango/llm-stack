# 🚀 llm-stack

**Unified Multi-LLM Routing & Load-Balancing Gateway** dành cho Claude Code CLI và các ứng dụng AI.

Hệ thống kết hợp **claude-proxy** (Anthropic rewriter), **OmniRoute** (load balancer/gateway) và **cf-ai-proxy** (Cloudflare Workers AI & NVIDIA NIM/Antigravity integration) nhằm mang lại trải nghiệm lập trình AI tốc độ cao, khả năng xử lý context lớn và hoàn toàn miễn phí.

---

## 🎯 Điểm Nổi Bật

* **Trung tâm cấu hình tập trung (`config/`):** Quản lý toàn bộ tài khoản Cloudflare, NVIDIA NIM và routing rules tại một thư mục duy nhất.
* **Bảo mật tuyệt đối:** Tự động loại trừ 100% secret, token, database khỏi Git tracking.
* **Đa nền tảng (Cross-Platform):** Chạy 1-click mượt mà trên cả **Windows** (PowerShell / WSL2), **macOS** (Apple Silicon / Intel) và **Linux**.
* **Định tuyến thông minh theo nhu cầu (Multi-Tier):**
  * `claude-opus-5` (Architect) $\rightarrow$ **`ka.reason`** (GPT-OSS 120B / Reasoning Model).
  * `claude-sonnet-5` (Engineer/Subagent) $\rightarrow$ **`ka.base`** (Gemini 2.5 Flash / Fast Coding Agent).
  * `claude-haiku-4-5` / `claude-fable-5` (Utility/Knowledge) $\rightarrow$ **`ka.docs`** (Xử lý tài liệu context 1 Triệu tokens).

---

## ⚡ Khởi Động Nhanh

### Trên Linux / macOS:
```bash
git clone https://github.com/trongnghiango/llm-stack.git
cd llm-stack

# Khởi động (Tự động sinh .env và nạp cấu hình ban đầu)
chmod +x stack start.sh
./stack start

# Kiểm tra trạng thái
./stack status
```

### Trên Windows (PowerShell):
```powershell
git clone https://github.com/trongnghiango/llm-stack.git
cd llm-stack

# Khởi động bằng PowerShell
.\stack.ps1 start

# Kiểm tra trạng thái
.\stack.ps1 status
```

---

## 📂 Quản Lý Cấu Hình Tập Trung (`config/`)

Toàn bộ thông tin tài khoản và quy tắc định tuyến nằm gọn trong thư mục `config/`:

| File Cấu Hình | Mục Đích |
| :--- | :--- |
| **`config/cf_accounts.csv`** | Danh sách tài khoản Cloudflare Workers AI (tự động hot-reload sau 5s). |
| **`config/nim_accounts.csv`** | Danh sách API token NVIDIA NIM (chạy `./stack sync-nim` để nạp vào DB). |
| **`config/cf_models.csv`** | Danh mục model hỗ trợ trên Cloudflare Workers AI. |
| **`config/proxy_routes.json`** | Quy tắc rewrite tên model và routing giữa Claude Code và OmniRoute. |
| **`config/claude_settings.json.example`** | File mẫu cấu hình cho Claude Code CLI. |

---

## 💻 Kết Nối Claude Code CLI

Thêm vào cấu hình shell (`~/.bashrc`, `~/.zshrc` hoặc PowerShell):
```bash
export ANTHROPIC_BASE_URL="http://localhost:20129"
export ANTHROPIC_API_KEY="sk-omniroute"
```

Cấu hình các slot model trong `~/.claude/settings.json`:
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:20129",
    "ANTHROPIC_API_KEY": "sk-omniroute",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "swe.engineer",
    "ANTHROPIC_DEFAULT_OPUS_MODEL":   "swe.architect",
    "CLAUDE_CODE_SUBAGENT_MODEL":     "swe.subagent",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL":  "swe.utility",
    "ANTHROPIC_CUSTOM_MODEL_OPTION":  "swe.knowledge"
  }
}
```

---

## 🛠️ Bộ Lệnh Quản Trị (`./stack` hoặc `.\stack.ps1`)

* `start` — Khởi động toàn bộ dịch vụ.
* `stop` — Dừng toàn bộ dịch vụ.
* `restart [service]` — Khởi động lại toàn bộ hoặc 1 container cụ thể.
* `status` — Xem trạng thái hoạt động của các containers.
* `logs [service]` — Xem logs thời gian thực.
* `sync-nim` — Đồng bộ tài khoản NVIDIA NIM từ `config/nim_accounts.csv` vào database.
* `flush` — Xóa sạch bộ nhớ đệm cache Redis.
* `update` — Cập nhật bản dựng mới nhất của OmniRoute.

---

## 📖 Tài Liệu Chi Tiết

Xem hướng dẫn chi tiết từng bước cho từng hệ điều hành tại:
👉 **[Tài Liệu Hướng Dẫn Triển Khai Toàn Diện (DEPLOYMENT_GUIDE.md)](docs/DEPLOYMENT_GUIDE.md)**
