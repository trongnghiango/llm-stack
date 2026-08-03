#!/bin/bash
# =============================================================================
# llm-stack Start Wrapper Script
# Tự động khởi tạo DB trên Host trước khi boot Docker Compose.
# =============================================================================
set -e

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
ENV_FILE="$PROJECT_ROOT/.env"
ENV_EXAMPLE="$PROJECT_ROOT/.env.example"
CONFIG_DIR="$PROJECT_ROOT/config"
PROXY_CONF="$CONFIG_DIR/proxy_routes.json"
PROXY_CONF_EXAMPLE="$CONFIG_DIR/proxy_routes.json.example"
CF_ACCOUNTS="$CONFIG_DIR/cf_accounts.csv"
CF_ACCOUNTS_EXAMPLE="$CONFIG_DIR/cf_accounts.csv.example"
NIM_ACCOUNTS="$CONFIG_DIR/nim_accounts.csv"
NIM_ACCOUNTS_EXAMPLE="$CONFIG_DIR/nim_accounts.csv.example"

# Centralized log directories
LOGS_DIR="$PROJECT_ROOT/data/logs"
DB_DIR="$PROJECT_ROOT/data/omniroute"
DB_PATH="$DB_DIR/storage.sqlite"
INIT_SQL="$PROJECT_ROOT/data/init.sql"

# Đảm bảo thư mục config tồn tại
mkdir -p "$CONFIG_DIR"

# 1. Tự động khởi tạo .env nếu chưa có và sinh key ngẫu nhiên bằng Python / OpenSSL
if [ ! -f "$ENV_FILE" ] && [ -f "$ENV_EXAMPLE" ]; then
  echo "ℹ️ Không tìm thấy .env. Tự động tạo từ .env.example..."
  cp "$ENV_EXAMPLE" "$ENV_FILE"

  if hash python3 2>/dev/null; then
    echo "🔑 Đang sinh khóa bảo mật ngẫu nhiên cho JWT_SECRET và API_KEY_SECRET..."
    JWT_SEC=$(python3 -c "import secrets; print(secrets.token_urlsafe(48))")
    API_SEC=$(python3 -c "import secrets; print(secrets.token_hex(32))")

    python3 -c "
with open('$ENV_FILE', 'r') as f:
    lines = f.readlines()

new_lines = []
for line in lines:
    if line.startswith('JWT_SECRET='):
        new_lines.append(f'JWT_SECRET={JWT_SEC}\n')
    elif line.startswith('API_KEY_SECRET='):
        new_lines.append(f'API_KEY_SECRET={API_SEC}\n')
    else:
        new_lines.append(line)

with open('$ENV_FILE', 'w') as f:
    f.writelines(new_lines)
"
  elif hash openssl 2>/dev/null; then
    echo "🔑 Đang sinh khóa bảo mật ngẫu nhiên qua openssl..."
    JWT_SEC=$(openssl rand -base64 48 | tr -d '\n')
    API_SEC=$(openssl rand -hex 32 | tr -d '\n')
    sed -i.bak "s|^JWT_SECRET=.*|JWT_SECRET=${JWT_SEC}|" "$ENV_FILE" && rm -f "$ENV_FILE.bak"
    sed -i.bak "s|^API_KEY_SECRET=.*|API_KEY_SECRET=${API_SEC}|" "$ENV_FILE" && rm -f "$ENV_FILE.bak"
  else
    echo "⚠️ Cảnh báo: Vui lòng tự điền JWT_SECRET và API_KEY_SECRET trong file .env."
  fi
fi

# 2. Tự động khởi tạo proxy_routes.json cho claude-proxy nếu chưa có
if [ ! -f "$PROXY_CONF" ] && [ -f "$PROXY_CONF_EXAMPLE" ]; then
  echo "ℹ️ Tự động tạo config/proxy_routes.json từ bản ví dụ..."
  cp "$PROXY_CONF_EXAMPLE" "$PROXY_CONF"
fi

# 3. Tự động khởi tạo cf_accounts.csv cho cf-ai-proxy nếu chưa có
if [ ! -f "$CF_ACCOUNTS" ] && [ -f "$CF_ACCOUNTS_EXAMPLE" ]; then
  echo "ℹ️ Tự động tạo config/cf_accounts.csv từ bản ví dụ..."
  cp "$CF_ACCOUNTS_EXAMPLE" "$CF_ACCOUNTS"
fi

# 4. Tự động khởi tạo nim_accounts.csv cho omniroute nếu chưa có
if [ ! -f "$NIM_ACCOUNTS" ] && [ -f "$NIM_ACCOUNTS_EXAMPLE" ]; then
  echo "ℹ️ Tự động tạo config/nim_accounts.csv từ bản ví dụ..."
  cp "$NIM_ACCOUNTS_EXAMPLE" "$NIM_ACCOUNTS"
fi

# Load variables từ .env để Docker compose có thể đọc nếu không dùng shell environment
if [ -f "$ENV_FILE" ]; then
  export $(grep -v '^#' "$ENV_FILE" | xargs)
fi

# 4. Đảm bảo các thư mục cần thiết tồn tại và phân quyền ghi
mkdir -p "$DB_DIR"
mkdir -p "$PROJECT_ROOT/data/redis"
mkdir -p "$LOGS_DIR"
mkdir -p "$LOGS_DIR/claude-proxy"
mkdir -p "$LOGS_DIR/omniroute"
mkdir -p "$LOGS_DIR/omniroute-calls"

# Sét quyền 777 cho thư mục data chứa db và logs để tránh lỗi phân quyền docker container
chmod -R 777 "$PROJECT_ROOT/data" || true

# Ghi nhận trạng thái DB trước khi khởi động
DB_EXISTS=true
if [ ! -f "$DB_PATH" ]; then
  DB_EXISTS=false
fi

# 5. Khởi động Docker Compose
echo "🚀 Đang khởi động llm-stack bằng Docker Compose..."
docker compose down 2>/dev/null || true
docker compose up -d --build

# 6. Thực hiện Seeding nếu DB được tạo mới
if [ "$DB_EXISTS" = "false" ]; then
  echo "⏳ Chờ omniroute khởi chạy và tạo cấu trúc DB (5 giây)..."
  sleep 5

  if [ -f "$DB_PATH" ]; then
    echo "🤖 [Init] Đang nạp cấu hình Custom Providers và Combos từ init.sql..."

    # Ưu tiên sử dụng Python sqlite3 để chạy seed (độc lập với máy Host)
    if hash python3 2>/dev/null; then
      python3 -c "
import sqlite3
import sys

try:
    conn = sqlite3.connect('$DB_PATH')
    cursor = conn.cursor()
    with open('$INIT_SQL', 'r', encoding='utf-8') as f:
        sql = f.read()
    cursor.executescript(sql)
    conn.commit()
    conn.close()
    print('✅ [Init] Nạp dữ liệu seed thành công bằng Python!')
except Exception as e:
    print(f'❌ [Init] Lỗi thực thi seed bằng Python: {e}', file=sys.stderr)
    sys.exit(1)
"
      if [ $? -eq 0 ]; then
        echo "🔄 Khởi động lại omniroute để cập nhật cấu hình..."
        docker compose restart omniroute
      fi
    elif command -v sqlite3 >/dev/null 2>&1; then
      sqlite3 "$DB_PATH" < "$INIT_SQL"
      echo "✅ [Init] Nạp dữ liệu seed thành công bằng CLI sqlite3!"

      echo "🔄 Restarting omniroute để cập nhật cấu hình..."
      docker compose restart omniroute
    else
      echo "❌ [Init] Lỗi: Cần 'python3' hoặc 'sqlite3' trên máy Host để tự động nạp seed. Vui lòng tự seed bằng: sqlite3 $DB_PATH < $INIT_SQL"
    fi
  else
    echo "⚠️ [Init] Không tìm thấy storage.sqlite được tạo ra bởi omniroute. Bỏ qua bước seed."
  fi
else
  echo "ℹ [Init] SQLite DB đã tồn tại. Bỏ qua bước seed dữ liệu."
fi

# Set lại quyền lần cuối cho an toàn
chmod -R 777 "$PROJECT_ROOT/data" || true

echo "🎉 llm-stack đã được khởi động thành công!"
docker compose ps
