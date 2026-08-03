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
    # Tự động nhận diện thư mục gốc của dự án (hỗ trợ Windows, macOS, Linux)
    project_dir = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
    
    # Ưu tiên đọc từ config/nim_accounts.csv, fallback về thư mục gốc nếu có
    csv_path = os.path.join(project_dir, "config", "nim_accounts.csv")
    if not os.path.exists(csv_path):
        legacy_csv = os.path.join(project_dir, "NIM_accounts.csv")
        if os.path.exists(legacy_csv):
            csv_path = legacy_csv

    db_path = os.path.join(project_dir, "data", "omniroute", "storage.sqlite")

    if not os.path.exists(csv_path):
        print(f"❌ Không tìm thấy file {csv_path} (hoặc config/nim_accounts.csv)")
        return

    if not os.path.exists(db_path):
        print(f"❌ Không tìm thấy file database SQLite của OmniRoute tại {db_path}")
        return

    # 1. Đọc danh sách NIM accounts từ CSV
    accounts = []
    try:
        with open(csv_path, mode="r", encoding="utf-8-sig") as f:
            reader = csv.DictReader(f)
            reader.fieldnames = [name.strip() for name in reader.fieldnames]
            
            for row in reader:
                if row.get("name") and row.get("token"):
                    acc_name = row["name"].strip()
                    csv_id = row.get("ID", "").strip()
                    # Sử dụng UUID từ CSV nếu chuẩn, nếu không sinh deterministic UUID theo tên tài khoản để ID luôn cố định
                    if csv_id and is_valid_uuid(csv_id):
                        conn_id = csv_id
                    else:
                        conn_id = str(uuid.uuid5(uuid.NAMESPACE_DNS, f"omniroute-nim-{acc_name}"))

                    accounts.append({
                        "id": conn_id,
                        "name": acc_name,
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

    # 2. Kết nối tới SQLite DB của OmniRoute
    try:
        conn = sqlite3.connect(db_path)
        cursor = conn.cursor()
        cursor.execute("PRAGMA foreign_keys = ON;")
        
        # Dọn dẹp các connection loại 'nvidia' cũ
        cursor.execute("DELETE FROM provider_connections WHERE provider='nvidia';")

        # 3. Inject các Connections loại 'nvidia' chuẩn của OmniRoute
        injected_count = 0
        nim_conns = []
        for acc in accounts:
            conn_id = acc["id"]
            conn_name = acc["name"]
            api_key = acc["token"]
            
            conn_data = {
                "connectionProxyEnabled": False,
                "connectionProxyUrl": "",
                "connectionNoProxy": "",
                "apiKeyHealth": {}
            }

            cursor.execute("""
                INSERT OR REPLACE INTO provider_connections (
                    id, provider, auth_type, name, priority, is_active, api_key, provider_specific_data, created_at, updated_at
                ) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
            """, (
                conn_id,
                "nvidia",       # provider
                "apikey",       # auth_type
                conn_name,      # name
                1,              # priority
                1,              # is_active = true
                api_key,
                json.dumps(conn_data),
                datetime.now().isoformat() + "Z",
                datetime.now().isoformat() + "Z"
            ))
            injected_count += 1
            nim_conns.append((conn_id, conn_name))
            print(f"  ⚡ Đã nạp Connection: {conn_name} (ID: {conn_id})")

        # 4. Tự động cập nhật combo ka.reason để đảm bảo định tuyến luôn hoạt động mượt mà
        ag_conns = cursor.execute('SELECT id, name FROM provider_connections WHERE provider="antigravity"').fetchall()
        reason_models = []
        for cid, cname in ag_conns:
            reason_models.append({
                'id': f'ka-reason-ag-oss-{cid}',
                'kind': 'model',
                'model': 'antigravity/gpt-oss-120b-medium',
                'providerId': 'antigravity',
                'connectionId': cid,
                'weight': 100,
                'label': f'AG-120B ({cname})'
            })
        for cid, cname in ag_conns:
            reason_models.append({
                'id': f'ka-reason-ag-opus-{cid}',
                'kind': 'model',
                'model': 'antigravity/claude-opus-4-6-thinking',
                'providerId': 'antigravity',
                'connectionId': cid,
                'weight': 80,
                'label': f'AG-OpusThinking ({cname})'
            })
        for cid, cname in nim_conns:
            reason_models.append({
                'id': f'ka-reason-nim-{cid}',
                'kind': 'model',
                'model': 'openai/gpt-oss-120b',
                'providerId': 'nvidia',
                'connectionId': cid,
                'weight': 50,
                'label': f'NIM ({cname})'
            })

        reason_data = {
            'id': 'combo-ka-reason',
            'name': 'ka.reason',
            'models': reason_models,
            'strategy': 'quota-share',
            'config': {
                'maxRetries': 3,
                'retryDelayMs': 1000,
                'handoffThreshold': 0.85,
                'trackMetrics': True,
                'reasoningTokenBufferEnabled': True
            },
            'isHidden': False,
            'sortOrder': 1,
            'version': 2
        }
        cursor.execute('UPDATE combos SET data = ? WHERE name = "ka.reason"', (json.dumps(reason_data),))

        conn.commit()
        conn.close()
        print(f"🎉 Đồng bộ thành công! Đã nạp {injected_count} connections NVIDIA NIM và tối ưu combo ka.reason.")

    except sqlite3.Error as e:
        print(f"❌ Lỗi SQLite: {e}")

if __name__ == "__main__":
    sync_nim()
