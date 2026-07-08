"""Minimal upstream server for real-world validation."""
import json
from http.server import HTTPServer, BaseHTTPRequestHandler

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        body = self.rfile.read(int(self.headers["Content-Length"]))
        req = json.loads(body)
        target = req.get("model", "unknown")
        print(f"  → Upstream received: model={target}")
        resp = json.dumps({"id": "msg_test", "model": target, "choices": [{"message": {"role": "assistant", "content": f"Routed to {target}"}}]}).encode()
        self.send_response(200)
        self.send_header("Content-Type", "application/json")
        self.end_headers()
        self.wfile.write(resp)

    def log_message(self, fmt, *args):
        pass  # quiet

HTTPServer(("127.0.0.1", 20128), Handler).serve_forever()
