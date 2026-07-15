#!/usr/bin/env python3
import os
import csv
import sqlite3
import json
import uuid
import glob
from datetime import datetime

# Default built-in NVIDIA LLM model IDs from 9router registry
DEFAULT_NVIDIA_LLMS = [
    "minimaxai/minimax-m2.7",
    "minimaxai/minimax-m3",
    "z-ai/glm-5.2",
    "deepseek-ai/deepseek-v4-pro",
    "deepseek-ai/deepseek-v4-flash",
    "moonshotai/kimi-k2.6",
    "nvidia/nemotron-3-ultra-550b-a55b"
]

def is_valid_uuid(val):
    try:
        uuid.UUID(str(val))
        return True
    except ValueError:
        return False

def sync_nim():
    # Sử dụng duy nhất đường dẫn trong dự án hợp nhất llm-stack
    project_dir = "/home/ka/Repos/github.com/trongnghiango/llm-stack"
    csv_pattern = os.path.join(project_dir, "NIM_*.csv")
    db_path = os.path.join(project_dir, "data/9router/db/data.sqlite")

    if not os.path.exists(db_path):
        print(f"❌ Không tìm thấy file database SQLite của 9router tại {db_path}")
        return

    csv_files = glob.glob(csv_pattern)
    if not csv_files:
        print(f"❌ Không tìm thấy bất kỳ file NIM_*.csv nào tại {project_dir}")
        return

    # 1. Đọc danh sách NIM accounts từ tất cả các file CSV khớp mẫu
    accounts = []
    all_csv_models = set()
    
    for csv_path in csv_files:
        print(f"📖 Đang đọc file: {os.path.basename(csv_path)}")
        try:
            with open(csv_path, mode="r", encoding="utf-8-sig") as f:
                reader = csv.DictReader(f)
                reader.fieldnames = [name.strip() for name in reader.fieldnames]
                
                for row in reader:
                    if row.get("name") and row.get("token"):
                        csv_id = row.get("ID", "").strip()
                        conn_id = csv_id
                        # Tự sinh UUID nếu ID rỗng hoặc không đúng định dạng
                        if not csv_id or not is_valid_uuid(csv_id):
                            conn_id = str(uuid.uuid4())

                        # Đọc danh sách models của account này (nếu có)
                        models_str = row.get("models", "").strip()
                        if models_str:
                            row_models = [m.strip() for m in models_str.split(",") if m.strip()]
                            for m in row_models:
                                all_csv_models.add(m)

                        accounts.append({
                            "id": conn_id,
                            "name": row["name"].strip(),
                            "token": row["token"].strip(),
                            "expiration": row.get("expiration", "").strip()
                        })
        except Exception as e:
            print(f"❌ Lỗi khi đọc file CSV {csv_path}: {e}")
            continue

    if not accounts:
        print("⚠️ Không tìm thấy tài khoản NVIDIA NIM hợp lệ nào trong các file CSV!")
        return

    print(f"🔍 Tìm thấy {len(accounts)} tài khoản NVIDIA NIM từ CSV.")
    print(f"🎯 Các mô hình NVIDIA hoạt động từ CSV: {list(all_csv_models)}")

    # 2. Kết nối tới SQLite DB của 9router
    try:
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        cursor.execute("PRAGMA foreign_keys = ON;")
        
        # 2a. Dọn dẹp các connection loại 'nvidia' cũ
        cursor.execute("DELETE FROM providerConnections WHERE provider='nvidia';")

        # 2b. Inject các Connections loại 'nvidia' chuẩn của 9router
        injected_count = 0
        for acc in accounts:
            conn_id = acc["id"]
            conn_name = acc["name"]
            api_key = acc["token"]
            
            conn_data = {
                "apiKey": api_key,
                "testStatus": "active",
                "providerSpecificData": {
                    "connectionProxyEnabled": False,
                    "connectionProxyUrl": "",
                    "connectionNoProxy": ""
                }
            }

            cursor.execute("""
                INSERT OR REPLACE INTO providerConnections (
                    id, provider, authType, name, priority, isActive, data, createdAt, updatedAt
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?);
            """, (
                conn_id,
                "nvidia",       # provider
                "apikey",       # authType
                conn_name,      # name
                1,              # priority
                1,              # isActive = true
                json.dumps(conn_data),
                datetime.now().isoformat() + "Z",
                datetime.now().isoformat() + "Z"
            ))
            injected_count += 1
            print(f"  ⚡ Đã nạp Connection: {conn_name} (ID: {conn_id})")

        # 2c. Dọn dẹp và cập nhật danh sách Custom Models cho nvidia
        # Chỉ giữ lại các custom model không có sẵn trong mặc định và có khai báo trong CSV
        cursor.execute("DELETE FROM kv WHERE scope = 'customModels' AND key LIKE 'nvidia|%';")
        
        for model in all_csv_models:
            if model not in DEFAULT_NVIDIA_LLMS:
                model_key = f"nvidia|{model}|llm"
                model_value = {
                    "providerAlias": "nvidia",
                    "id": model,
                    "type": "llm",
                    "name": model
                }
                cursor.execute(
                    "INSERT INTO kv (scope, key, value) VALUES ('customModels', ?, ?);",
                    (model_key, json.dumps(model_value))
                )
                print(f"  ✨ Đã đăng ký Custom Model: {model}")

        # 2d. Cập nhật danh sách Disabled Models (mô hình mặc định nhưng không khai báo trong CSV)
        disabled_models = [m for m in DEFAULT_NVIDIA_LLMS if m not in all_csv_models]
        if disabled_models:
            cursor.execute(
                "INSERT OR REPLACE INTO kv (scope, key, value) VALUES ('disabledModels', 'nvidia', ?);",
                (json.dumps(disabled_models),)
            )
            print(f"  🚫 Đã vô hiệu hóa các mô hình mặc định: {disabled_models}")
        else:
            cursor.execute(
                "DELETE FROM kv WHERE scope = 'disabledModels' AND key = 'nvidia';"
            )
            print("  ✅ Tất cả mô hình mặc định đều được kích hoạt.")

        conn.commit()
        conn.close()
        print(f"🎉 Đồng bộ thành công! Đã cấu hình và nạp connections vào database của 9router.")

    except sqlite3.Error as e:
        print(f"❌ Lỗi SQLite: {e}")

if __name__ == "__main__":
    sync_nim()
