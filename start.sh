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

# 1. Đảm bảo thư mục tồn tại
mkdir -p "$DB_DIR"
mkdir -p "$PROJECT_ROOT/data/redis"

# Set quyền ghi ban đầu cho data volume
chmod -R 777 "$PROJECT_ROOT/data" || true

# Ghi nhận trạng thái DB trước khi khởi động
DB_EXISTS=true
if [ ! -f "$DB_PATH" ]; then
  DB_EXISTS=false
fi

# 2. Khởi động Docker Compose
echo "🚀 Đang khởi động llm-stack bằng Docker Compose..."
docker compose down 2>/dev/null || true
docker compose up -d --build

# 3. Thực hiện Seeding nếu DB được tạo mới
if [ "$DB_EXISTS" = "false" ]; then
  echo "⏳ Chờ 9router khởi chạy và tạo cấu trúc DB (tối đa 15 giây)..."
  for i in {1..15}; do
    if [ -f "$DB_PATH" ]; then
      break
    fi
    sleep 1
  done
  if [ -f "$DB_PATH" ]; then
    echo "🤖 [Init] Đang nạp cấu hình Custom Providers và Combos từ init.sql..."
    if command -v sqlite3 >/dev/null 2>&1; then
      sqlite3 "$DB_PATH" < "$INIT_SQL"
      echo "✅ [Init] Nạp dữ liệu seed thành công!"
      
      # Tự động đồng bộ các NIM accounts
      if [ -f "$PROJECT_ROOT/scripts/sync_nim_accounts.py" ]; then
        echo "🤖 [Init] Đang đồng bộ các tài khoản NVIDIA NIM từ file CSV..."
        python3 "$PROJECT_ROOT/scripts/sync_nim_accounts.py" || true
      fi
      
      echo "🔄 Restarting 9router để cập nhật cấu hình..."
      docker compose restart 9router
    else
      echo "❌ [Init] Lỗi: Máy Host thiếu 'sqlite3' để nạp seed. Vui lòng tự import init.sql."
    fi
  else
    echo "⚠️ [Init] Không tìm thấy data.sqlite được tạo ra bởi 9router. Bỏ qua bước seed."
  fi
else
  echo "ℹ️ [Init] SQLite DB đã tồn tại. Bỏ qua bước seed dữ liệu."
fi

# Set lại quyền lần cuối cho an toàn
chmod -R 777 "$PROJECT_ROOT/data" || true

echo "🎉 llm-stack đã được khởi động thành công!"
docker compose ps
