#!/bin/sh
# =============================================================================
# 9router entrypoint: khởi tạo SQLite DB từ init.sql nếu chưa tồn tại
# =============================================================================
set -e

DB_PATH="/app/data/db/data.sqlite"
INIT_SQL="/app/data/db/init.sql"

# Đảm bảo thư mục tồn tại
mkdir -p "$(dirname "$DB_PATH")"

if [ ! -f "$DB_PATH" ]; then
  echo "[Init] data.sqlite chưa tồn tại – khởi tạo từ init.sql..."
  if [ -f "$INIT_SQL" ]; then
    sqlite3 "$DB_PATH" < "$INIT_SQL"
    echo "[Init] SQLite DB khởi tạo thành công."
  else
    echo "[Init] Không tìm thấy init.sql tại $INIT_SQL – 9router sẽ tự tạo DB trống."
  fi
else
  echo "[Init] data.sqlite đã tồn tại – bỏ qua seed."
fi

# Chuyển quyền sang entrypoint gốc của 9router
# Gọi /entrypoint.sh gốc để nó setup môi trường, sau đó node custom-server.js start
exec /entrypoint.sh
