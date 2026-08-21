#!/usr/bin/env python3
import urllib.request
import json
import sys

PROXY_URL = "http://127.0.0.1:20129/v1/messages"
API_KEY = "sk-xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx"

# Payload mô phỏng chính xác chuẩn Anthropic
# Sử dụng pseudo-model "swe.engineer" để proxy tự định tuyến
payload = {
    "model": "swe.engineer",
    "max_tokens": 1024,
    "stream": True,
    "messages": [
        {
            "role": "user",
            "content": "Bạn hãy tính phép tính 15 * 8 và trả về dưới định dạng JSON theo cấu trúc: {\"result\": <number>}."
        }
    ]
}

data = json.dumps(payload).encode('utf-8')
req = urllib.request.Request(PROXY_URL, data=data)
req.add_header('Content-Type', 'application/json')
req.add_header('x-api-key', API_KEY)
req.add_header('anthropic-version', '2023-06-01')

print("Đang gửi request trực tiếp đến claude-proxy (Cổng 20129)...")
try:
    with urllib.request.urlopen(req) as response:
        print(f"\nMã phản hồi (Status Code): {response.status}\n")

        # In trực tiếp kết quả Streaming từng chunk (để kiểm tra xem stream.go có ngắt đúng không)
        for line in response:
            decoded_line = line.decode('utf-8').strip()
            if decoded_line:
                print(decoded_line)
                # Nếu client nhận được [DONE], mọi thứ diễn ra trơn tru

except urllib.error.URLError as e:
    print(f"\n❌ Lỗi kết nối (Proxy sập hoặc không phản hồi): {e}")
    sys.exit(1)
except Exception as e:
    print(f"\n❌ Lỗi không xác định: {e}")
    sys.exit(1)

print("\n\n✅ Test hoàn tất. Nếu bạn thấy các dòng 'data: {...}' hiện ra liên tục tức là Stream và Routing của claude-proxy đang hoạt động hoàn hảo.")
