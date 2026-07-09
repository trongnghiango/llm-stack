#!/usr/bin/env python3
import os
import csv
import sqlite3
import json
import uuid
from datetime import datetime

def is_valid_uuid(val):
    try:
        uuid.UUID(str(val))
        return True
    except ValueError:
        return False

def sync_nim():
    project_dir = "/home/ka/Repos/github.com/trongnghiango/llm-stack"
    csv_path = os.path.join(project_dir, "NIM_accounts.csv")
    db_path = os.path.join(project_dir, "data/9router/db/data.sqlite")

    if not os.path.exists(csv_path):
        print(f"❌ Không tìm thấy file {csv_path}")
        return

    if not os.path.exists(db_path):
        print(f"❌ Không tìm thấy file database SQLite của 9router tại {db_path}")
        return

    # 1. Đọc danh sách NIM accounts từ CSV
    accounts = []
    try:
        with open(csv_path, mode="r", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            reader.fieldnames = [name.strip() for name in reader.fieldnames]
            
            for row in reader:
                if row.get("name") and row.get("token"):
                    csv_id = row.get("ID", "").strip()
                    conn_id = csv_id
                    if not csv_id or not is_valid_uuid(csv_id):
                        conn_id = str(uuid.uuid4())

                    accounts.append({
                        "id": conn_id,
                        "name": row["name"].strip(),
                        "token": row["token"].strip(),
                        "expiration": row.get("expiration", "").strip()
                    })
    except Exception as e:
        print(f"❌ Lỗi khi đọc file CSV: {e}")
        return

    if not accounts:
        print("⚠️ File NIM_accounts.csv không chứa tài khoản hợp lệ nào!")
        return

    print(f"🔍 Tìm thấy {len(accounts)} tài khoản NVIDIA NIM từ CSV.")

    # 2. Kết nối tới SQLite DB của 9router
    try:
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        cursor.execute("PRAGMA foreign_keys = ON;")
        
        # Dọn dẹp các connection loại 'nvidia' cũ để tránh rác DB (bao gồm cả TEST_NIM để sạch DB)
        cursor.execute("DELETE FROM providerConnections WHERE provider='nvidia';")

        # 3. Inject các Connections loại 'nvidia' chuẩn của 9router
        # LƯU Ý QUAN TRỌNG: Cấu trúc mapping DB của 9router bị ngược cột:
        # - Tên connection (NIM_GOON_003) được lưu vào cột authType.
        # - Phương thức xác thực (apikey) được lưu vào cột name.
        injected_count = 0
        for acc in accounts:
            conn_id = acc["id"]
            conn_name = acc["name"]
            api_key = acc["token"]
            
            conn_data = {
                "apiKey": api_key,
                "testStatus": "active", # Đặt active luôn để UI hiển thị xanh đẹp
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
                conn_name,      # authType (Gán tên connection vào đây do 9router bị ngược)
                "apikey",       # name     (Gán loại xác thực vào đây do 9router bị ngược)
                1,              # priority
                1,              # isActive = true
                json.dumps(conn_data),
                datetime.now().isoformat() + "Z",
                datetime.now().isoformat() + "Z"
            ))
            injected_count += 1
            print(f"  ⚡ Đã nạp Connection: {conn_name} (ID: {conn_id}) -> Khớp mapping ngược của 9router")

        conn.commit()
        conn.close()
        print(f"🎉 Đồng bộ thành công! Đã nạp {injected_count} connections NVIDIA NIM vào database của 9router.")

    except sqlite3.Error as e:
        print(f"❌ Lỗi SQLite: {e}")

if __name__ == "__main__":
    sync_nim()
