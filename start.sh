#!/bin/bash
# =============================================================================
# llm-stack Start Wrapper Script
# Tự động khởi tạo DB trên Host trước khi boot Docker Compose.
# =============================================================================
set -e

PROJECT_ROOT="$(cd "$(dirname "$0")" && pwd)"
DB_DIR="$PROJECT_ROOT/data/9router/db"
DB_PATH="$DB_DIR/data.sqlite"
INIT_SQL="$DB_DIR/init.sql"

# 1. Đảm bảo thư mục db tồn tại
mkdir -p "$DB_DIR"
mkdir -p "$PROJECT_ROOT/data/redis"

# 2. Khởi tạo DB SQLite trên Host nếu chưa tồn tại
if [ ! -f "$DB_PATH" ]; then
  echo "🤖 [Init] SQLite DB chưa tồn tại. Đang tạo và seed trên Host..."
  if command -v sqlite3 >/dev/null 2>&1; then
    sqlite3 "$DB_PATH" < "$INIT_SQL"
    echo "✅ [Init] Đã tạo SQLite DB thành công tại: $DB_PATH"
  else
    echo "❌ [Init] Không tìm thấy lệnh 'sqlite3' trên máy Host! Vui lòng cài đặt sqlite3 (sudo apt install sqlite3) hoặc tạo file DB thủ công."
    exit 1
  fi
else
  echo "ℹ️ [Init] SQLite DB đã tồn tại. Bỏ qua bước seed."
fi

# 3. Đảm bảo quyền ghi (write permissions) cho container
# 9router chạy với node user (thường uid 1000 hoặc root tùy image)
# Set chmod cho chắc chắn không dính lỗi Permission Denied trong volume mount
chmod -R 777 "$PROJECT_ROOT/data"

# 4. Khởi động Docker Compose
echo "🚀 Đang khởi động llm-stack bằng Docker Compose..."
docker compose down 2>/dev/null || true
docker compose up -d --build

echo "🎉 llm-stack đã được khởi động thành công!"
docker compose ps
