-- =============================================================================
-- 9router SQLite Seed – llm-stack
-- =============================================================================
-- Chạy một lần khi /app/data/db/data.sqlite chưa tồn tại.
-- Provider URLs dùng Docker service names (internal network).
-- =============================================================================

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- ---------------------------------------------------------------------------
-- Schema
-- ---------------------------------------------------------------------------
CREATE TABLE IF NOT EXISTS _meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
  id   INTEGER PRIMARY KEY CHECK (id = 1),
  data TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS providerNodes (
  id        TEXT PRIMARY KEY,
  type      TEXT,
  name      TEXT,
  data      TEXT NOT NULL,
  createdAt TEXT NOT NULL,
  updatedAt TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pn_type ON providerNodes(type);

CREATE TABLE IF NOT EXISTS providerConnections (
  id        TEXT PRIMARY KEY,
  provider  TEXT NOT NULL,
  authType  TEXT NOT NULL,
  name      TEXT,
  email     TEXT,
  priority  INTEGER,
  isActive  INTEGER DEFAULT 1,
  data      TEXT NOT NULL,
  createdAt TEXT NOT NULL,
  updatedAt TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pc_provider        ON providerConnections(provider);
CREATE INDEX IF NOT EXISTS idx_pc_provider_active ON providerConnections(provider, isActive);
CREATE INDEX IF NOT EXISTS idx_pc_priority        ON providerConnections(provider, priority);

CREATE TABLE IF NOT EXISTS proxyPools (
  id         TEXT PRIMARY KEY,
  isActive   INTEGER DEFAULT 1,
  testStatus TEXT,
  data       TEXT NOT NULL,
  createdAt  TEXT NOT NULL,
  updatedAt  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_pp_active ON proxyPools(isActive);
CREATE INDEX IF NOT EXISTS idx_pp_status ON proxyPools(testStatus);

CREATE TABLE IF NOT EXISTS apiKeys (
  id        TEXT PRIMARY KEY,
  key       TEXT UNIQUE NOT NULL,
  name      TEXT,
  machineId TEXT,
  isActive  INTEGER DEFAULT 1,
  createdAt TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_ak_key ON apiKeys(key);

CREATE TABLE IF NOT EXISTS combos (
  id        TEXT PRIMARY KEY,
  name      TEXT UNIQUE NOT NULL,
  kind      TEXT,
  models    TEXT NOT NULL,
  createdAt TEXT NOT NULL,
  updatedAt TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_combo_name ON combos(name);

CREATE TABLE IF NOT EXISTS kv (
  scope TEXT NOT NULL,
  key   TEXT NOT NULL,
  value TEXT NOT NULL,
  PRIMARY KEY (scope, key)
);
CREATE INDEX IF NOT EXISTS idx_kv_scope ON kv(scope);

CREATE TABLE IF NOT EXISTS usageHistory (
  id               INTEGER PRIMARY KEY AUTOINCREMENT,
  timestamp        TEXT NOT NULL,
  provider         TEXT,
  model            TEXT,
  connectionId     TEXT,
  apiKey           TEXT,
  endpoint         TEXT,
  promptTokens     INTEGER DEFAULT 0,
  completionTokens INTEGER DEFAULT 0,
  cost             REAL DEFAULT 0,
  status           TEXT,
  tokens           TEXT,
  meta             TEXT
);
CREATE INDEX IF NOT EXISTS idx_uh_ts       ON usageHistory(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_uh_provider ON usageHistory(provider);
CREATE INDEX IF NOT EXISTS idx_uh_model    ON usageHistory(model);
CREATE INDEX IF NOT EXISTS idx_uh_conn     ON usageHistory(connectionId);

CREATE TABLE IF NOT EXISTS usageDaily (
  dateKey TEXT PRIMARY KEY,
  data    TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS requestDetails (
  id           TEXT PRIMARY KEY,
  timestamp    TEXT NOT NULL,
  provider     TEXT,
  model        TEXT,
  connectionId TEXT,
  status       TEXT,
  data         TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rd_ts       ON requestDetails(timestamp DESC);
CREATE INDEX IF NOT EXISTS idx_rd_provider ON requestDetails(provider);
CREATE INDEX IF NOT EXISTS idx_rd_model    ON requestDetails(model);
CREATE INDEX IF NOT EXISTS idx_rd_conn     ON requestDetails(connectionId);

-- ---------------------------------------------------------------------------
-- Metadata
-- ---------------------------------------------------------------------------
INSERT OR IGNORE INTO _meta (key, value) VALUES
  ('schemaVersion', '1'),
  ('appVersion', '0.5.20'),
  ('totalRequestsLifetime', '0');

-- ---------------------------------------------------------------------------
-- Settings
-- Password mặc định: "llmstack2026"
-- Hash bcrypt (cost=10): $2b$10$9K3BPKP1BVZZ6AjPvRn8XOF9Q1pLmNeTDxwEr2QrBmLQ7AXqZOhK
-- Đổi bằng lệnh: docker exec -it llm-9router sh -c "npx bcrypt-cli hash 'newpassword'"
-- ---------------------------------------------------------------------------
INSERT OR REPLACE INTO settings (id, data) VALUES (
  1,
  json_object(
    'password', '$2b$10$ArWCvvtCKvTiHySPtwbGeuc42mDPQ1ru7cVy/BAcC3kfWzaxTVbum',
    'providerStrategies', json_object(
      'anthropic-compatible-cf-ai-proxy', json_object(
        'fallbackStrategy', 'round-robin',
        'stickyRoundRobinLimit', 1
      ),
      'cloudflare-ai', json_object(
        'fallbackStrategy', 'round-robin',
        'stickyRoundRobinLimit', 1
      )
    )
  )
);

-- ---------------------------------------------------------------------------
-- API Key cho claude-proxy / Claude Code xác thực với 9router
-- Key này phải trùng với upstream_api_key trong claude-proxy/config.json
-- ---------------------------------------------------------------------------
INSERT OR IGNORE INTO apiKeys (id, key, name, isActive, createdAt) VALUES (
  '5e89927d-0482-4232-9eb0-57457a4d1b84',
  'sk-e03c947dc728e9f5-1lt0v4-32e6f2f4',
  'KAKA',
  1,
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- Provider Node: anthropic-compatible → cf-ai-proxy
-- Dùng Docker service name "cf-ai-proxy" thay vì host.docker.internal
-- ---------------------------------------------------------------------------
INSERT OR IGNORE INTO providerNodes (id, type, name, data, createdAt, updatedAt) VALUES (
  'anthropic-compatible-cf-ai-proxy',
  'anthropic-compatible',
  'CF-AI-PROXY',
  json_object(
    'prefix',   'cf-ai-proxy',
    'baseUrl',  'http://cf-ai-proxy:20127/v1'
  ),
  datetime('now'),
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- Provider Connections: các tài khoản CF được inject qua cf-ai-proxy
-- cf-ai-proxy tự load balancing qua accounts.csv → chỉ cần 1 connection ở đây
-- API key là dummy vì cf-ai-proxy không kiểm tra key từ 9router
-- ---------------------------------------------------------------------------
INSERT OR IGNORE INTO providerConnections (
  id, provider, authType, name, priority, isActive, data, createdAt, updatedAt
) VALUES (
  'pconn-cf-ai-proxy-001',
  'anthropic-compatible-cf-ai-proxy',
  'apikey',
  'CF-AI-PROXY-MAIN',
  1,
  1,
  json_object(
    'defaultModel',          'qwen-2.5-coder',
    'apiKey',                'dummy-key-cf-ai-proxy-handles-auth',
    'testStatus',            'unknown',
    'providerSpecificData',  json_object(
      'prefix',    'cf-ai-proxy',
      'baseUrl',   'http://cf-ai-proxy:20127/v1',
      'nodeName',  'CF-AI-PROXY-MAIN',
      'connectionProxyEnabled', 0,
      'connectionProxyUrl', '',
      'connectionNoProxy',  ''
    )
  ),
  datetime('now'),
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- Combos – map tên alias (ka.xxx) sang list models
-- Format: ["cf-ai-proxy/{alias}"] → 9router route tới cf-ai-proxy provider
--
-- Mapping với claude-proxy/config.json:
--   swe.architect → ka.reason  → qwen3-30b (model mạnh nhất)
--   swe.engineer  → ka.base    → qwen-2.5-coder (default coding model)
--   swe.subagent  → ka.base    → qwen-2.5-coder (default coding model)
--   swe.utility   → ka.simple  → deepseek-r1 (fast utility)
--               or → ka.docs    → llama-3.1-8b (doc/explain tasks)
--   swe.knowledge → ka.docs    → llama-3.1-8b (doc/explain tasks)
--   ka.zzz        → (giữ lại test combo)
-- ---------------------------------------------------------------------------

-- ka.zzz = Test combo
INSERT OR IGNORE INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (
  'combo-ka-zzz',
  'ka.zzz',
  NULL,
  '["cf-ai-proxy/qwen3-30b-a3b-fp8"]',
  datetime('now'),
  datetime('now')
);

-- ka.reason = Architect (Opus slot) → model mạnh: qwen3-30b-a3b-fp8
INSERT OR IGNORE INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (
  'combo-ka-reason',
  'ka.reason',
  NULL,
  '["cf-ai-proxy/qwen3-30b-a3b-fp8"]',
  datetime('now'),
  datetime('now')
);

-- ka.base = Engineer / Subagent (Sonnet slot) → coding model tốt nhất
INSERT OR IGNORE INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (
  'combo-ka-base',
  'ka.base',
  NULL,
  '["cf-ai-proxy/qwen-2.5-coder"]',
  datetime('now'),
  datetime('now')
);

-- ka.simple = Utility fallback (Haiku slot) → DeepSeek R1 (code / algorithm)
INSERT OR IGNORE INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (
  'combo-ka-simple',
  'ka.simple',
  NULL,
  '["cf-ai-proxy/deepseek-r1-distill-qwen-32b"]',
  datetime('now'),
  datetime('now')
);

-- ka.docs = Utility doc / Knowledge (Haiku / Custom slot) → llama-3.1-8b-instruct-fp8-fast (doc/explain nhanh)
INSERT OR IGNORE INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES (
  'combo-ka-docs',
  'ka.docs',
  NULL,
  '["cf-ai-proxy/llama-3.1-8b-instruct-fp8-fast"]',
  datetime('now'),
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- KV cache: đăng ký tất cả models của cf-ai-proxy vào 9router model registry
-- Format: scope="{providerNodeId}|{modelAlias}|llm" value="{json}"
-- Đây giúp 9router nhận ra các model name hợp lệ
-- ---------------------------------------------------------------------------

-- Qwen 2.5 Coder 32B (alias chính cho coding tasks)
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|qwen-2.5-coder|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"qwen-2.5-coder","type":"llm","name":"qwen-2.5-coder"}'
);

-- Qwen3 30B (mạnh nhất, cho architect tasks)
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|qwen3-30b-a3b-fp8|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"qwen3-30b-a3b-fp8","type":"llm","name":"qwen3-30b-a3b-fp8"}'
);

-- DeepSeek R1 Distill Qwen 32B
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|deepseek-r1-distill-qwen-32b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"deepseek-r1-distill-qwen-32b","type":"llm","name":"deepseek-r1-distill-qwen-32b"}'
);

-- Qwen 2.5 Coder 7B
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|qwen2.5-coder-7b-instruct|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"qwen2.5-coder-7b-instruct","type":"llm","name":"qwen2.5-coder-7b-instruct"}'
);

-- LLaMA 3.1 8B
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|llama-3.1-8b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"llama-3.1-8b","type":"llm","name":"llama-3.1-8b"}'
);

-- LLaMA 3.1 8B FP8 Fast
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|llama-3.1-8b-instruct-fp8-fast|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"llama-3.1-8b-instruct-fp8-fast","type":"llm","name":"llama-3.1-8b-instruct-fp8-fast"}'
);

-- LLaMA 3.1 70B
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|llama-3.1-70b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"llama-3.1-70b","type":"llm","name":"llama-3.1-70b"}'
);

-- Mistral 7B
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|mistral-7b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"mistral-7b","type":"llm","name":"mistral-7b"}'
);

-- Gemma 2 9B
INSERT OR IGNORE INTO kv (scope, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|gemma-2-9b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"gemma-2-9b","type":"llm","name":"gemma-2-9b"}'
);
