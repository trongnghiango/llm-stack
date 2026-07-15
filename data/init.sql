-- =============================================================================
-- OmniRoute SQLite Seed – llm-stack
-- =============================================================================
-- Chạy một lần khi database của OmniRoute được tạo mới và migrations đã hoàn tất.
-- Provider URLs dùng Docker service names (internal network).
-- =============================================================================

PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

-- ---------------------------------------------------------------------------
-- 1. API Key cho claude-proxy / Claude Code xác thực với OmniRoute
-- Key này phải trùng với upstream_api_key trong claude-proxy/config.json
-- ---------------------------------------------------------------------------
INSERT OR IGNORE INTO api_keys (
  id, name, key, machine_id, created_at
) VALUES (
  '5e89927d-0482-4232-9eb0-57457a4d1b84',
  'KAKA',
  'sk-e03c947dc728e9f5-1lt0v4-32e6f2f4',
  NULL,
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- 2. Provider Node: anthropic-compatible → cf-ai-proxy
-- Dùng Docker service name "cf-ai-proxy" thay vì host.docker.internal
-- ---------------------------------------------------------------------------
INSERT OR IGNORE INTO provider_nodes (
  id, type, name, prefix, base_url, created_at, updated_at
) VALUES (
  'anthropic-compatible-cf-ai-proxy',
  'anthropic-compatible',
  'CF-AI-PROXY',
  'cf-ai-proxy',
  'http://cf-ai-proxy:20127/v1',
  datetime('now'),
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- 3. Provider Connection: Các tài khoản CF được inject qua cf-ai-proxy
-- cf-ai-proxy tự load balancing qua accounts.csv → chỉ cần 1 connection ở đây
-- API key là dummy vì cf-ai-proxy không kiểm tra key từ OmniRoute
-- ---------------------------------------------------------------------------
INSERT OR IGNORE INTO provider_connections (
  id, provider, auth_type, name, priority, is_active, api_key, default_model, provider_specific_data, created_at, updated_at
) VALUES (
  'pconn-cf-ai-proxy-001',
  'anthropic-compatible-cf-ai-proxy',
  'apikey',
  'CF-AI-PROXY-MAIN',
  1,
  1,
  'dummy-key-cf-ai-proxy-handles-auth',
  'qwen-2.5-coder',
  '{"prefix":"cf-ai-proxy","baseUrl":"http://cf-ai-proxy:20127/v1","nodeName":"CF-AI-PROXY-MAIN","connectionProxyEnabled":0,"connectionProxyUrl":"","connectionNoProxy":""}',
  datetime('now'),
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- 4. Combos – map tên alias (ka.xxx) sang list models
-- Format: Định dạng JSON chuẩn tương thích hoàn toàn với schema của OmniRoute
--
-- Mapping với claude-proxy/config.json:
--   swe.architect → ka.reason  → qwen3-30b (model mạnh nhất)
--   swe.engineer  → ka.base    → qwen-2.5-coder (default coding model)
--   swe.subagent  → ka.base    → qwen-2.5-coder (default coding model)
--   swe.utility   → ka.simple  → deepseek-r1 (fast utility)
--               or → ka.docs    → llama-3.1-8b (doc/explain tasks)
--   swe.knowledge → ka.docs    → llama-3.1-8b (doc/explain tasks)
-- ---------------------------------------------------------------------------

-- ka.zzz = Test combo
INSERT OR IGNORE INTO combos (
  id, name, data, sort_order, created_at, updated_at
) VALUES (
  'combo-ka-zzz',
  'ka.zzz',
  '{"id":"combo-ka-zzz","name":"ka.zzz","models":[{"id":"model-ka-zzz-1","kind":"model","model":"cf-ai-proxy/qwen3-30b-a3b-fp8","providerId":"anthropic-compatible-cf-ai-proxy","connectionId":"pconn-cf-ai-proxy-001","weight":0}],"strategy":"priority","config":{"maxRetries":1,"retryDelayMs":2000,"handoffThreshold":0.85,"maxMessagesForSummary":30,"trackMetrics":true,"reasoningTokenBufferEnabled":true,"zeroLatencyOptimizationsEnabled":false},"isHidden":false,"sortOrder":0,"createdAt":"2026-07-15T07:20:00.000Z","updatedAt":"2026-07-15T07:20:00.000Z"}',
  0,
  datetime('now'),
  datetime('now')
);

-- ka.reason = Architect (Opus slot) → model mạnh: qwen3-30b-a3b-fp8
INSERT OR IGNORE INTO combos (
  id, name, data, sort_order, created_at, updated_at
) VALUES (
  'combo-ka-reason',
  'ka.reason',
  '{"id":"combo-ka-reason","name":"ka.reason","models":[{"id":"model-ka-reason-1","kind":"model","model":"cf-ai-proxy/qwen3-30b-a3b-fp8","providerId":"anthropic-compatible-cf-ai-proxy","connectionId":"pconn-cf-ai-proxy-001","weight":0}],"strategy":"priority","config":{"maxRetries":1,"retryDelayMs":2000,"handoffThreshold":0.85,"maxMessagesForSummary":30,"trackMetrics":true,"reasoningTokenBufferEnabled":true,"zeroLatencyOptimizationsEnabled":false},"isHidden":false,"sortOrder":1,"createdAt":"2026-07-15T07:20:00.000Z","updatedAt":"2026-07-15T07:20:00.000Z"}',
  1,
  datetime('now'),
  datetime('now')
);

-- ka.base = Engineer / Subagent (Sonnet slot) → coding model tốt nhất
INSERT OR IGNORE INTO combos (
  id, name, data, sort_order, created_at, updated_at
) VALUES (
  'combo-ka-base',
  'ka.base',
  '{"id":"combo-ka-base","name":"ka.base","models":[{"id":"model-ka-base-1","kind":"model","model":"cf-ai-proxy/qwen-2.5-coder","providerId":"anthropic-compatible-cf-ai-proxy","connectionId":"pconn-cf-ai-proxy-001","weight":0}],"strategy":"priority","config":{"maxRetries":1,"retryDelayMs":2000,"handoffThreshold":0.85,"maxMessagesForSummary":30,"trackMetrics":true,"reasoningTokenBufferEnabled":true,"zeroLatencyOptimizationsEnabled":false},"isHidden":false,"sortOrder":2,"createdAt":"2026-07-15T07:20:00.000Z","updatedAt":"2026-07-15T07:20:00.000Z"}',
  2,
  datetime('now'),
  datetime('now')
);

-- ka.simple = Utility fallback (Haiku slot) → DeepSeek R1 (code / algorithm)
INSERT OR IGNORE INTO combos (
  id, name, data, sort_order, created_at, updated_at
) VALUES (
  'combo-ka-simple',
  'ka.simple',
  '{"id":"combo-ka-simple","name":"ka.simple","models":[{"id":"model-ka-simple-1","kind":"model","model":"cf-ai-proxy/deepseek-r1-distill-qwen-32b","providerId":"anthropic-compatible-cf-ai-proxy","connectionId":"pconn-cf-ai-proxy-001","weight":0}],"strategy":"priority","config":{"maxRetries":1,"retryDelayMs":2000,"handoffThreshold":0.85,"maxMessagesForSummary":30,"trackMetrics":true,"reasoningTokenBufferEnabled":true,"zeroLatencyOptimizationsEnabled":false},"isHidden":false,"sortOrder":3,"createdAt":"2026-07-15T07:20:00.000Z","updatedAt":"2026-07-15T07:20:00.000Z"}',
  3,
  datetime('now'),
  datetime('now')
);

-- ka.docs = Utility doc / Knowledge (Haiku / Custom slot) → llama-3.1-8b-instruct-fp8-fast (doc/explain nhanh)
INSERT OR IGNORE INTO combos (
  id, name, data, sort_order, created_at, updated_at
) VALUES (
  'combo-ka-docs',
  'ka.docs',
  '{"id":"combo-ka-docs","name":"ka.docs","models":[{"id":"model-ka-docs-1","kind":"model","model":"cf-ai-proxy/llama-3.1-8b-instruct-fp8-fast","providerId":"anthropic-compatible-cf-ai-proxy","connectionId":"pconn-cf-ai-proxy-001","weight":0}],"strategy":"priority","config":{"maxRetries":1,"retryDelayMs":2000,"handoffThreshold":0.85,"maxMessagesForSummary":30,"trackMetrics":true,"reasoningTokenBufferEnabled":true,"zeroLatencyOptimizationsEnabled":false},"isHidden":false,"sortOrder":4,"createdAt":"2026-07-15T07:20:00.000Z","updatedAt":"2026-07-15T07:20:00.000Z"}',
  4,
  datetime('now'),
  datetime('now')
);

-- ---------------------------------------------------------------------------
-- 5. Model registry: Đăng ký tất cả models của cf-ai-proxy vào model registry
-- Format: namespace="{providerNodeId}|{modelAlias}|llm" key="model" value="{json}"
-- Đây giúp OmniRoute nhận diện được các model của provider
-- ---------------------------------------------------------------------------

-- Qwen 2.5 Coder 32B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|qwen-2.5-coder|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"qwen-2.5-coder","type":"llm","name":"qwen-2.5-coder"}'
);

-- Qwen3 30B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|qwen3-30b-a3b-fp8|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"qwen3-30b-a3b-fp8","type":"llm","name":"qwen3-30b-a3b-fp8"}'
);

-- DeepSeek R1 Distill Qwen 32B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|deepseek-r1-distill-qwen-32b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"deepseek-r1-distill-qwen-32b","type":"llm","name":"deepseek-r1-distill-qwen-32b"}'
);

-- Qwen 2.5 Coder 7B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|qwen2.5-coder-7b-instruct|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"qwen2.5-coder-7b-instruct","type":"llm","name":"qwen2.5-coder-7b-instruct"}'
);

-- LLaMA 3.1 8B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|llama-3.1-8b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"llama-3.1-8b","type":"llm","name":"llama-3.1-8b"}'
);

-- LLaMA 3.1 8B FP8 Fast
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|llama-3.1-8b-instruct-fp8-fast|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"llama-3.1-8b-instruct-fp8-fast","type":"llm","name":"llama-3.1-8b-instruct-fp8-fast"}'
);

-- LLaMA 3.1 70B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|llama-3.1-70b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"llama-3.1-70b","type":"llm","name":"llama-3.1-70b"}'
);

-- Mistral 7B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|mistral-7b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"mistral-7b","type":"llm","name":"mistral-7b"}'
);

-- Gemma 2 9B
INSERT OR IGNORE INTO key_value (namespace, key, value) VALUES (
  'anthropic-compatible-cf-ai-proxy|gemma-2-9b|llm',
  'model',
  '{"providerAlias":"anthropic-compatible-cf-ai-proxy","id":"gemma-2-9b","type":"llm","name":"gemma-2-9b"}'
);
