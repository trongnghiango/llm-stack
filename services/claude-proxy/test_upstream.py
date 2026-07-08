#!/usr/bin/env python3
import urllib.request
import json
import sys

url = "http://127.0.0.1:20129/v1/messages"
headers = {
    "content-type": "application/json",
    "x-api-key": "sk-local-dummy-token",
    "anthropic-version": "2023-06-01"
}

payload = {
    "model": "swe.engineer",
    "max_tokens": 1024,
    "system": "You are a helpful assistant. You must use the 'get_weather' tool to answer.",
    "tools": [
        {
            "name": "get_weather",
            "description": "Get the current weather in a given location",
            "input_schema": {
                "type": "object",
                "properties": {
                    "location": {
                        "type": "string",
                        "description": "The city and state, e.g. San Francisco, CA"
                    }
                },
                "required": ["location"]
            }
        }
    ],
    "messages": [
        {
            "role": "user",
            "content": "What is the weather like in Hanoi right now?"
        }
    ]
}

req = urllib.request.Request(url, data=json.dumps(payload).encode(), headers=headers, method="POST")

try:
    with urllib.request.urlopen(req, timeout=30) as response:
        status = response.status
        body = response.read().decode()
        print(f"Status: {status}")
        try:
            parsed = json.loads(body)
            print(json.dumps(parsed, indent=2))
        except:
            print(body)
except urllib.error.HTTPError as e:
    print(f"HTTP Error {e.code}: {e.read().decode()}")
except Exception as e:
    print(f"Error: {e}")
