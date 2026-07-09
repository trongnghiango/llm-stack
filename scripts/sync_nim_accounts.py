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
        print("Vui lòng đảm bảo stack docker compose đang chạy và 9router đã được khởi chạy ít nhất một lần để tạo file DB.")
        return

    # 1. Đọc danh sách NIM accounts từ CSV
    accounts = []
    try:
        with open(csv_path, mode="r", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            # Chuẩn hóa tên cột để tránh khoảng trắng hoặc ký tự đặc biệt
            reader.fieldnames = [name.strip() for name in reader.fieldnames]
            
            for row in reader:
                # Đảm bảo các trường bắt buộc tồn tại
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

        # Bật foreign keys và WAL mode
        cursor.execute("PRAGMA foreign_keys = ON;")
        
        # 3. Tạo/Cập nhật Provider Node cho NVIDIA NIM
        node_id = "openai-compatible-nvidia-nim"
        node_type = "openai-compatible"
        node_name = "NVIDIA-NIM"
        node_data = {
            "prefix": "nvidia-nim",
            "baseUrl": "https://integrate.api.nvidia.com/v1"
        }
        
        cursor.execute("""
            INSERT OR REPLACE INTO providerNodes (id, type, name, data, createdAt, updatedAt)
            VALUES (?, ?, ?, ?, ?, ?);
        """, (
            node_id,
            node_type,
            node_name,
            json.dumps(node_data),
            datetime.now().isoformat() + "Z",
            datetime.now().isoformat() + "Z"
        ))
        print(f"✅ Đã cấu hình Provider Node '{node_name}' (ID: {node_id})")

        # 4. Inject các Connections từ CSV
        injected_count = 0
        for acc in accounts:
            conn_id = f"pconn-nvidia-nim-{acc['name']}"
            conn_name = acc["name"]
            api_key = acc["token"]
            
            # Cấu hình data JSON cho Connection
            # Sử dụng model mặc định phổ biến của NVIDIA NIM là meta/llama-3.3-70b-instruct
            conn_data = {
                "defaultModel": "meta/llama-3.3-70b-instruct",
                "apiKey": api_key,
                "testStatus": "unknown",
                "providerSpecificData": {
                    "prefix": "nvidia-nim",
                    "baseUrl": "https://integrate.api.nvidia.com/v1",
                    "nodeName": conn_name,
                    "connectionProxyEnabled": 0,
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
                node_id,
                "apikey",
                conn_name,
                1, # Priority mặc định
                1, # isActive = true
                json.dumps(conn_data),
                datetime.now().isoformat() + "Z",
                datetime.now().isoformat() + "Z"
            ))
            injected_count += 1
            print(f"  ⚡ Đã nạp Connection: {conn_name} (ID: {conn_id})")

        conn.commit()
        conn.close()
        print(f"🎉 Đồng bộ thành công! Đã nạp {injected_count} connections NVIDIA NIM vào database của 9router.")

    except sqlite3.Error as e:
        print(f"❌ Lỗi SQLite: {e}")

if __name__ == "__main__":
    sync_nim()
