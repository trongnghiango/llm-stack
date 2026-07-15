# llm-stack

Unified Docker Compose stack cho hệ thống LLM proxy: **Claude Code → cf-ai-proxy → Cloudflare Workers AI**.

## Kiến trúc

```
Claude Code  (ANTHROPIC_BASE_URL=http://127.0.0.1:20129)
     │
     ▼  POST /v1/messages  model: "swe.engineer"
claude-proxy  :20129
     │  Rewrite model name: swe.* → ka.*
     ▼  model: "ka.base"
omniroute  :20128  [UI: http://localhost:20128]
     │  Route ka.base → cf-ai-proxy/qwen-2.5-coder
     ▼  POST /v1/messages  (Anthropic format)
cf-ai-proxy  :20127  [internal only]
     │  Convert Anthropic ↔ Cloudflare format
     │  Load-balance qua nhiều CF accounts
     ▼
Cloudflare Workers AI
(Qwen 2.5 Coder, Qwen3 30B, DeepSeek R1, LLaMA...)
     +
Redis  :6379  [session & quota tracking]
```

## Khởi động nhanh

```bash
# 1. Clone và setup
git clone <repo> llm-stack
cd llm-stack

# 2. Copy và điền secrets
cp .env.example .env
# Mở .env và điền CF_ACCOUNT_*_TOKEN

# 3. Quản lý hệ thống bằng CLI `./stack`
./stack start

# 4. Kiểm tra trạng thái
./stack status
```

## Model Mapping

| Claude Code env var           | claude-proxy alias | ka.* alias | Model (qua cf-ai-proxy)          |
|-------------------------------|--------------------|------------|----------------------------------|
| `ANTHROPIC_DEFAULT_OPUS_MODEL`   | `swe.architect`    | `ka.reason`   | `qwen3-30b-a3b-fp8`              |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | `swe.engineer`     | `ka.base`     | `qwen-2.5-coder`                 |
| `CLAUDE_CODE_SUBAGENT_MODEL`     | `swe.subagent`     | `ka.base`     | `qwen-2.5-coder`                 |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL`  | `swe.utility`      | `ka.simple`/`ka.docs` | `deepseek-r1` / `llama-3.1-8b-fp8` |
| `ANTHROPIC_CUSTOM_MODEL_OPTION`  | `swe.knowledge`    | `ka.docs`     | `llama-3.1-8b-instruct-fp8-fast` |

## Cấu hình Claude Code

Trong `~/.claude/settings.json`:
```json
{
  "env": {
    "ANTHROPIC_BASE_URL": "http://127.0.0.1:20129",
    "ANTHROPIC_AUTH_TOKEN": "sk-local-dummy-token",
    "ANTHROPIC_DEFAULT_SONNET_MODEL": "swe.engineer",
    "ANTHROPIC_DEFAULT_OPUS_MODEL":   "swe.architect",
    "CLAUDE_CODE_SUBAGENT_MODEL":     "swe.subagent",
    "ANTHROPIC_DEFAULT_HAIKU_MODEL":  "swe.utility",
    "ANTHROPIC_CUSTOM_MODEL_OPTION":  "swe.knowledge"
  }
}
```

## Services

| Service        | Port  | Mô tả                                      |
|---------------|-------|--------------------------------------------|
| `claude-proxy` | 20129 | Model rewriter, chỉ bind localhost         |
| `omniroute`    | 20128 | LLM router + UI admin                      |
| `cf-ai-proxy`  | 20127 | Cloudflare proxy (internal only)           |
| `redis`        | —     | Session/quota storage (internal only)      |

## OmniRoute UI
 
Truy cập http://localhost:20128 để quản lý providers, models, và combos.
 
**Mật khẩu mặc định**: `llmstack2026` (hoặc cấu hình qua `INITIAL_PASSWORD` trong `.env`)
 
### 🔄 Cách đồng bộ toàn bộ Available Models từ cf-ai-proxy:
1. Đăng nhập vào UI OmniRoute (http://localhost:20128).
2. Vào mục **Providers**, kéo xuống phần Custom Provider **`CF-AI-PROXY-MAIN`**.
3. Tại phần **Available Models**, click vào nút **`Import from /models`** ở góc phải.
4. Hệ thống sẽ tự động fetch danh sách từ `http://cf-ai-proxy:20127/v1/models` và nạp đầy đủ 9 model được khai báo trong `models.csv` vào giao diện của bạn.


## Available Models (qua cf-ai-proxy)

| Alias                          | Cloudflare Model                              |
|-------------------------------|-----------------------------------------------|
| `qwen-2.5-coder`               | `@cf/qwen/qwen2.5-coder-32b-instruct`        |
| `qwen3-30b-a3b-fp8`            | `@cf/qwen/qwen3-30b-a3b-fp8`                 |
| `deepseek-r1-distill-qwen-32b` | `@cf/deepseek-ai/deepseek-r1-distill-qwen-32b` |
| `qwen2.5-coder-7b-instruct`    | `@cf/qwen/qwen2.5-coder-32b-instruct`        |
| `llama-3.1-8b`                 | `@cf/meta/llama-3.1-8b-instruct`             |
| `llama-3.1-8b-instruct-fp8-fast` | `@cf/meta/llama-3.1-8b-instruct-fp8-fast`  |
| `llama-3.1-70b`                | `@cf/meta/llama-3.1-70b-instruct`            |
| `mistral-7b`                   | `@cf/mistralai/mistral-7b-instruct-v0.2`     |
| `gemma-2-9b`                   | `@cf/google/gemma-2-9b-it`                   |

## Thêm model mới
 
1. Thêm vào `services/cf-ai-proxy/models.csv`
2. Thêm `INSERT INTO kv` vào `data/omniroute/db/init.sql`
3. Xóa `data/omniroute/storage.sqlite` để seed lại
4. `./stack restart omniroute`

## Cấu trúc thư mục

```
llm-stack/
├── docker-compose.yml
├── .env.example          ← Template (commit)
├── .env                  ← Secrets (KHÔNG commit)
├── .gitignore
├── README.md
├── services/
│   ├── claude-proxy/     ← Source + Dockerfile
│   └── cf-ai-proxy/      ← Source + Dockerfile
├── data/
│   ├── omniroute/
│   │   └── db/
│   │       └── init.sql  ← Seed schema + data
│   └── redis/            ← Redis persistence
└── scripts/
    └── sync_nim_accounts.py ← Sync NIM connections script
```

## Lệnh quản lý hệ thống (`./stack`)

Dự án cung cấp CLI `./stack` thống nhất để quản trị stack.

```bash
# Xem hướng dẫn đầy đủ
./stack help

# Khởi động toàn bộ stack
./stack start

# Xem trạng thái các container
./stack status

# Xem logs thời gian thực (toàn bộ hoặc từng service)
./stack logs
./stack logs cf-ai-proxy

# Khởi động lại service
./stack restart omniroute

# Xóa cache Redis
./stack flush

# Đồng bộ NVIDIA NIM
./stack sync-nim

# Dừng hệ thống
./stack stop
```

## Cập nhật và Bảo đảm Dữ liệu (Update & Backup)

Để nâng cấp dịch vụ lên phiên bản mới nhất (như `omniroute` hay `cf-ai-proxy`) mà không bị mất cấu hình và cơ sở dữ liệu SQLite:

```bash
# Cập nhật omniroute (mặc định sẽ tự động sao lưu dữ liệu SQLite/Auth sang file nén .tar.gz trước khi pull bản mới)
./stack update
 
# Cập nhật một dịch vụ cụ thể
./stack update cf-ai-proxy
```
 
Quá trình `update` sẽ tự động:
1. Tạo bản sao lưu dự phòng: `omniroute-backup-YYYYMMDD_HHMMSS.tar.gz` (nếu cập nhật `omniroute`).
2. Kéo (pull) image docker mới nhất từ hub.
3. Restart lại duy nhất container được chỉ định mà không tắt các thành phần khác.

---

## Cấu trúc thư mục
