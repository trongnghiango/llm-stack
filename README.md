# llm-stack

Unified Docker Compose stack cho hệ thống LLM proxy: **Claude Code → cf-ai-proxy → Cloudflare Workers AI**.

## Kiến trúc

```
Claude Code  (ANTHROPIC_BASE_URL=http://127.0.0.1:20129)
     │
     ▼  POST /v1/messages  model: "swe.engineer"
claude-proxy  :20129
     │  Rewrite model name: swe.* → ka.*
     ▼  model: "ka.xxx"
9router  :20128  [UI: http://localhost:20128]
     │  Route ka.xxx → cf-ai-proxy/qwen-2.5-coder
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

# 3. Build và chạy
docker compose up -d --build

# 4. Kiểm tra trạng thái
docker compose ps
docker compose logs -f
```

## Model Mapping

| Claude Code env var           | claude-proxy alias | ka.* alias | Model (qua cf-ai-proxy)          |
|-------------------------------|--------------------|------------|----------------------------------|
| `ANTHROPIC_DEFAULT_OPUS_MODEL`   | `swe.architect`    | `ka.zzz`   | `qwen3-30b-a3b-fp8`              |
| `ANTHROPIC_DEFAULT_SONNET_MODEL` | `swe.engineer`     | `ka.xxx`   | `qwen-2.5-coder`                 |
| `CLAUDE_CODE_SUBAGENT_MODEL`     | `swe.subagent`     | `swe.subagent` | `qwen-2.5-coder`             |
| `ANTHROPIC_DEFAULT_HAIKU_MODEL`  | `swe.utility`      | `ka.ddd`/`ka.ccc` | `deepseek-r1` / `llama-3.1-8b-fp8` |
| `ANTHROPIC_CUSTOM_MODEL_OPTION`  | `swe.knowledge`    | `ka.mmm`   | `qwen2.5-coder-7b-instruct`      |

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
| `9router`      | 20128 | LLM router + UI admin                      |
| `cf-ai-proxy`  | 20127 | Cloudflare proxy (internal only)           |
| `redis`        | —     | Session/quota storage (internal only)      |

## 9router UI

Truy cập http://localhost:20128 để quản lý providers, models, và combos.

**Password mặc định**: `llmstack2026`  
*(Hash được lưu trong `data/9router/db/init.sql` – đổi trước khi production)*

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
2. Thêm `INSERT INTO kv` vào `data/9router/db/init.sql`
3. Xóa `data/9router/db/data.sqlite` để seed lại
4. `docker compose restart 9router`

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
│   ├── 9router/
│   │   └── db/
│   │       └── init.sql  ← Seed schema + data
│   └── redis/            ← Redis persistence
└── scripts/
    └── 9router-init.sh   ← Auto-init entrypoint
```

## Lệnh hữu ích

```bash
# Xem logs realtime
docker compose logs -f

# Xem log của một service
docker compose logs -f cf-ai-proxy

# Restart một service
docker compose restart 9router

# Rebuild sau khi thay đổi code
docker compose up -d --build cf-ai-proxy

# Reset 9router DB (re-seed)
rm data/9router/db/data.sqlite
docker compose restart 9router

# Dừng tất cả
docker compose down

# Dừng và xóa volumes
docker compose down -v
```
