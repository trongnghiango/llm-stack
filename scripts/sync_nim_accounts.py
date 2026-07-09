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
        
        # Dọn dẹp các connection loại 'nvidia' cũ
        cursor.execute("DELETE FROM providerConnections WHERE provider='nvidia';")

        # 3. Inject cả 2 dạng connection (Normal và Invert) để thực nghiệm trên UI
        injected_count = 0
        for acc in accounts:
            api_key = acc["token"]
            
            # --- DẠNG 1: XUÔI CỘT (NORMAL) ---
            id_normal = str(uuid.uuid4())
            name_normal = acc["name"]
            authtype_normal = "apikey"
            
            conn_data_normal = {
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
                id_normal,
                "nvidia",
                authtype_normal, # authType = apikey
                name_normal,      # name = NIM_GOON_003
                1,
                1,
                json.dumps(conn_data_normal),
                datetime.now().isoformat() + "Z",
                datetime.now().isoformat() + "Z"
            ))
            print(f"  ⚡ Đã nạp Normal: {name_normal} (authType=apikey, name={name_normal})")

            # --- DẠNG 2: NGƯỢC CỘT (INVERT) ---
            id_invert = str(uuid.uuid4())
            name_invert = "apikey"
            authtype_invert = f"{acc['name']}_INV"
            
            conn_data_invert = {
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
                id_invert,
                "nvidia",
                authtype_invert, # authType = NIM_GOON_003_INV
                name_invert,      # name = apikey
                1,
                1,
                json.dumps(conn_data_invert),
                datetime.now().isoformat() + "Z",
                datetime.now().isoformat() + "Z"
            ))
            print(f"  ⚡ Đã nạp Invert: {authtype_invert} (authType={authtype_invert}, name=apikey)")
            injected_count += 2

        conn.commit()
        conn.close()
        print(f"🎉 Đồng bộ hoàn tất! Đã nạp song song {injected_count} connections thử nghiệm vào SQLite.")

    except sqlite3.Error as e:
        print(f"❌ Lỗi SQLite: {e}")

if __name__ == "__main__":
    sync_nim()
