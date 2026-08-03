# 📘 Hướng Dẫn Triển Khai và Vận Hành Toàn Diện llm-stack
### Hệ Thống Proxy Đa Tầng, Tự Động Định Tuyến & Cân Bằng Tải Multi-LLM

Tài liệu này cung cấp hướng dẫn từng bước (Step-by-step) để cài đặt, cấu hình và vận hành **llm-stack** trên **Windows**, **macOS** và **Linux**, đảm bảo tính bảo mật tuyệt đối cho thông tin tài khoản và API Keys.

---

## 📑 Mục Lục
1. [Kiến Trúc Tổng Quan](#1-kiến-trúc-tổng-quan)
2. [Quản Lý Cấu Hình Tập Trung (`config/`)](#2-quản-lý-cấu-hình-tập-trung-config)
3. [Hướng Dẫn Cài Đặt Theo Từng Hệ Điều Hành](#3-hướng-dẫn-cài-đặt-theo-từng-hệ-điều-hành)
   - [Windows (PowerShell / WSL2)](#31-triển-khai-trên-windows)
   - [macOS (Apple Silicon / Intel)](#32-triển-khai-trên-macos)
   - [Linux (Ubuntu / Debian / Arch)](#33-triển-khai-trên-linux)
4. [Tích Hợp Với Claude Code CLI](#4-tích-hợp-với-claude-code-cli)
5. [Thêm & Quản Lý Tài Khoản AI](#5-thêm--quản-lý-tài-khoản-ai)
6. [Bộ Lệnh Quản Trị Nhanh (`stack` CLI)](#6-bộ-lệnh-quản-trị-nhanh-stack-cli)
7. [Bảo Mật & Quy Chuẩn Git](#7-bảo-mật--quy-chuẩn-git)
8. [Xử Lý Sự Cố Thường Gặp (Troubleshooting)](#8-xử-lý-sự-cố-thường-gặp-troubleshooting)

---

## 1. Kiến Trúc Tổng Quan

```
[Claude Code CLI / App Client] (Host)
              │
              ▼ (Port 20129)
┌────────────────────────────────────────────────────────┐
│  claude-proxy                                          │
│  - Phân tích cú pháp Anthropic Messages API            │
│  - Rewrite model tier: swe.* → ka.*                    │
│  - Spoofing tool-use format                            │
└────────────────────────────────────────────────────────┘
              │
              ▼ (Port 20128)
┌────────────────────────────────────────────────────────┐
│  omniroute (Load Balancer & LLM Gateway)               │
│  - Web Admin Dashboard: http://localhost:20128         │
│  - Quản lý Combos, Quota, Priority, Session            │
│  - Tích hợp Google Antigravity, NVIDIA NIM, OpenAI...  │
└────────────────────────────────────────────────────────┘
              │
              ▼ (Port 20127)
┌────────────────────────────────────────────────────────┐
│  cf-ai-proxy (Cloudflare Workers AI Gateway)           │
│  - Load balancing qua dàn tài khoản Cloudflare         │
│  - Đồng bộ quota Neurons tự động                       │
│  - Redis lưu trữ session state                         │
└────────────────────────────────────────────────────────┘
```

---

## 2. Quản Lý Cấu Hình Tập Trung (`config/`)

Toàn bộ cấu hình và tài khoản của hệ thống được quản lý **tập trung tại một nơi duy nhất**:

```
llm-stack/
├── .env.example                     # Mẫu biến môi trường hệ thống
├── .env                             # Biến môi trường thực tế (Tự động sinh khi chạy lần đầu)
│
├── config/                          # 📂 TRUNG TÂM CẤU HÌNH DUY NHẤT
│   ├── cf_accounts.csv              # 1. Danh sách tài khoản Cloudflare Workers AI
│   ├── cf_accounts.csv.example      #    (Mẫu cấu hình Cloudflare)
│   ├── cf_models.csv                # 2. Bảng Catalog model Cloudflare
│   ├── nim_accounts.csv             # 3. Danh sách token NVIDIA NIM
│   ├── nim_accounts.csv.example     #    (Mẫu cấu hình NVIDIA NIM)
│   ├── proxy_routes.json            # 4. Quy tắc định tuyến & rewrite model của claude-proxy
│   ├── proxy_routes.json.example    #    (Mẫu cấu hình định tuyến)
│   └── claude_settings.json.example # 5. Mẫu cấu hình settings.json cho Claude Code CLI
```

> 🔒 **Lưu ý bảo mật:** Hệ thống `.gitignore` đã được thiết lập để **tự động loại trừ tất cả các file chứa secret** (`.env`, `config/*.csv`, `config/proxy_routes.json`, SQLite DB). Bạn có thể yên tâm commit hoặc push mã nguồn mà không sợ lộ token.

---

## 3. Hướng Dẫn Cài Đặt Theo Từng Hệ Điều Hành

### Yêu Cầu Chung
* Đã cài đặt **Docker** và **Docker Compose v2+**.
* Đã cài đặt **Git**.

---

### 3.1. Triển Khai Trên Windows

Bạn có thể chạy trực tiếp bằng **PowerShell** hoặc thông qua **WSL2**.

#### Cách 1: Sử dụng PowerShell (Khuyên dùng)
1. Mở **PowerShell** (hoặc Windows Terminal).
2. Clone repository và di chuyển vào thư mục:
   ```powershell
   git clone https://github.com/trongnghiango/llm-stack.git
   cd llm-stack
   ```
3. Khởi chạy toàn bộ hệ thống (Script sẽ tự động tạo file `.env`, sinh ngẫu nhiên JWT secret và nạp dữ liệu):
   ```powershell
   .\stack.ps1 start
   ```
4. Kiểm tra trạng thái các container:
   ```powershell
   .\stack.ps1 status
   ```

#### Cách 2: Sử dụng WSL2 (Ubuntu)
Mở terminal Ubuntu trong WSL2 và thực hiện tương tự như Linux (xem mục 3.3).

---

### 3.2. Triển Khai Trên macOS

1. Mở ứng dụng **Terminal**.
2. Clone repository:
   ```bash
   git clone https://github.com/trongnghiango/llm-stack.git
   cd llm-stack
   ```
3. Cấp quyền thực thi và khởi chạy hệ thống:
   ```bash
   chmod +x stack start.sh
   ./stack start
   ```
4. Kiểm tra các dịch vụ đang chạy:
   ```bash
   ./stack status
   ```

---

### 3.3. Triển Khai Trên Linux (Ubuntu / Debian / Server)

1. Cài đặt Docker và Docker Compose (nếu chưa có):
   ```bash
   sudo apt update && sudo apt install -y docker.io docker-compose-v2 python3
   sudo usermod -aG docker $USER
   # Đăng xuất và đăng nhập lại để nhận quyền docker
   ```
2. Clone repository:
   ```bash
   git clone https://github.com/trongnghiango/llm-stack.git
   cd llm-stack
   ```
3. Khởi chạy hệ thống:
   ```bash
   chmod +x stack start.sh
   ./stack start
   ```
4. Đồng bộ danh sách tài khoản NVIDIA NIM (nếu có):
   ```bash
   ./stack sync-nim
   ```

---

## 4. Tích Hợp Với Claude Code CLI

Sau khi `llm-stack` khởi chạy thành công, `claude-proxy` sẽ lắng nghe tại `http://localhost:20129`.

### Cấu hình biến môi trường cho Claude Code
Thêm vào file cấu hình shell của bạn (`~/.bashrc`, `~/.zshrc` trên Linux/Mac) hoặc PowerShell Profile trên Windows:

#### Trên Linux / macOS:
```bash
export ANTHROPIC_BASE_URL="http://localhost:20129"
export ANTHROPIC_API_KEY="sk-omniroute"
```

#### Trên Windows PowerShell:
```powershell
[System.Environment]::SetEnvironmentVariable('ANTHROPIC_BASE_URL', 'http://localhost:20129', 'User')
[System.Environment]::SetEnvironmentVariable('ANTHROPIC_API_KEY', 'sk-omniroute', 'User')
```

### Sử dụng file cấu hình mẫu `config/claude_settings.json.example`
Bạn có thể copy file mẫu `config/claude_settings.json.example` vào thư mục cấu hình của Claude Code (`~/.claude/settings.json` hoặc `%USERPROFILE%\.claude\settings.json`):

```json
{
  "model": "swe.engineer",
  "architect_model": "swe.architect",
  "subagent_model": "swe.subagent",
  "utility_model": "swe.utility"
}
```

* **`swe.architect`**: Tự động chuyển tiếp sang model suy luận logic đỉnh cao (`ka.reason` - GPT-OSS 120B).
* **`swe.engineer`**: Model lập trình chính (`ka.base` - Gemini 2.5 Flash).
* **`swe.subagent`**: Model chạy các subagent song song (`ka.base`).
* **`swe.utility`**: Model xử lý tài liệu, giải thích văn bản (`ka.docs`).

---

## 5. Thêm & Quản Lý Tài Khoản AI

### 5.1. Thêm Tài Khoản Cloudflare Workers AI
1. Mở file **`config/cf_accounts.csv`**.
2. Thêm dòng mới theo định dạng:
   ```csv
   ID,Name,AccountID,APIToken,DailyLimit
   cf-01,MyAccount1,your_cloudflare_account_id,your_cloudflare_api_token,10000
   ```
3. Lưu file. Container `cf-ai-proxy` sẽ **tự động nạp tài khoản mới sau 5 giây (Hot Reload)** mà không cần restart!

---

### 5.2. Thêm Token NVIDIA NIM
1. Mở file **`config/nim_accounts.csv`**.
2. Thêm dòng token theo định dạng:
   ```csv
   ID,name,token,expiration,models
   uuid-001,NIM_ACC_01,nvapi-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx,07/09/2027,openai/gpt-oss-120b
   ```
3. Chạy lệnh đồng bộ:
   * **Linux/macOS:** `./stack sync-nim`
   * **Windows:** `.\stack.ps1 sync-nim`

---

### 5.3. Tùy Chỉnh Định Tuyến (Routing & Rewriting)
Để chỉnh sửa luật chuyển hướng model hoặc từ khóa kích hoạt, hãy chỉnh sửa trực tiếp file **`config/proxy_routes.json`** và chạy:
* **Linux/macOS:** `./stack restart claude-proxy`
* **Windows:** `.\stack.ps1 restart claude-proxy`

---

## 6. Bộ Lệnh Quản Trị Nhanh (`stack` CLI)

Hệ thống cung cấp CLI đồng nhất trên cả 3 hệ điều hành:

| Tác Vụ | Linux / macOS | Windows PowerShell |
| :--- | :--- | :--- |
| **Khởi động hệ thống** | `./stack start` | `.\stack.ps1 start` |
| **Dừng hệ thống** | `./stack stop` | `.\stack.ps1 stop` |
| **Xem trạng thái** | `./stack status` | `.\stack.ps1 status` |
| **Xem logs toàn bộ** | `./stack logs` | `.\stack.ps1 logs` |
| **Xem logs 1 dịch vụ** | `./stack logs claude-proxy` | `.\stack.ps1 logs claude-proxy` |
| **Khởi động lại 1 dịch vụ** | `./stack restart omniroute` | `.\stack.ps1 restart omniroute` |
| **Đồng bộ NVIDIA NIM** | `./stack sync-nim` | `.\stack.ps1 sync-nim` |
| **Xóa sạch Cache Redis** | `./stack flush` | `.\stack.ps1 flush` |
| **Cập nhật OmniRoute mới** | `./stack update` | `.\stack.ps1 update` |

---

## 7. Bảo Mật & Quy Chuẩn Git

Để bảo vệ tuyệt đối thông tin nhạy cảm:
1. **Tuyệt đối không xoá file `.gitignore`**.
2. Các file cấu hình mẫu (`.example`) chỉ chứa thông tin giả định. Khi cấu hình thực tế, hãy điền vào các file không có đuôi `.example`.
3. Khóa bí mật `JWT_SECRET` và `API_KEY_SECRET` được sinh ngẫu nhiên trên máy của bạn và không bao giờ được chia sẻ ra ngoài.

---

## 8. Xử Lý Sự Cố Thường Gặp (Troubleshooting)

#### 1. Lỗi Port Đã Bị Chiếm Dụng (Port Conflict)
Nếu cổng 20127, 20128 hoặc 20129 bị ứng dụng khác chiếm:
* Mở file `.env` và đổi cổng tương ứng (ví dụ: `OMNIROUTE_PORT=20228`).
* Chạy `./stack restart` (hoặc `.\stack.ps1 restart`).

#### 2. Lỗi Timeout Khi Model Suy Luận Quá Lâu
Hệ thống đã được cấu hình mặc định timeout **600 giây (10 phút)** cho các model reasoning nặng như `gpt-oss-120b`. Nếu gặp lỗi kết nối, kiểm tra logs bằng:
```bash
./stack logs claude-proxy
```

#### 3. Cần Đăng Nhập Dashboard Admin
Truy cập trình duyệt: **`http://localhost:20128`**
* Mật khẩu mặc định: `llmstack2026` (hoặc giá trị bạn đặt trong biến `INITIAL_PASSWORD` tại file `.env`).
