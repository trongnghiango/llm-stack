#!/usr/bin/env python3
import os
import csv
import sqlite3
import json
from datetime import datetime

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
                if row.get("ID") and row.get("name") and row.get("token"):
                    accounts.append({
                        "id": row["ID"].strip(),
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
        
        # 3. Dọn dẹp providerNode và providerConnection custom cũ (nếu có) để tránh rác DB
        cursor.execute("DELETE FROM providerNodes WHERE id='openai-compatible-nvidia-nim';")
        cursor.execute("DELETE FROM providerConnections WHERE provider='openai-compatible-nvidia-nim';")

        # 4. Inject các Connections loại 'nvidia' chuẩn của 9router
        injected_count = 0
        for acc in accounts:
            # Dùng luôn ID trong CSV làm ID của Connection hoặc sinh UUID
            # Để đồng bộ, ta dùng luôn ID từ CSV (ví dụ: bb0b2348-fe3c-4db7-b872-87e90...)
            conn_id = acc["id"]
            conn_name = acc["name"]
            api_key = acc["token"]
            
            # Cấu hình data JSON cho Connection loại 'nvidia'
            conn_data = {
                "apiKey": api_key,
                "testStatus": "unknown",
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
                "nvidia",       # Tên Provider chuẩn của 9router
                "apikey",
                conn_name,
                1,              # Priority mặc định
                1,              # isActive = true
                json.dumps(conn_data),
                datetime.now().isoformat() + "Z",
                datetime.now().isoformat() + "Z"
            ))
            injected_count += 1
            print(f"  ⚡ Đã nạp Connection: {conn_name} (ID: {conn_id}) -> Loại: nvidia")

        conn.commit()
        conn.close()
        print(f"🎉 Đồng bộ thành công! Đã nạp {injected_count} connections NVIDIA NIM vào database của 9router.")

    except sqlite3.Error as e:
        print(f"❌ Lỗi SQLite: {e}")

if __name__ == "__main__":
    sync_nim()
