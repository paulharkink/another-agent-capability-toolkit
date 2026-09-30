"""Synthetic Forgejo API for isolated container transport validation."""
from http.server import BaseHTTPRequestHandler, HTTPServer
import json

class Handler(BaseHTTPRequestHandler):
    def do_GET(self):
        if self.path == "/api/v1/version":
            body = {"version":"11.0.0"}
        elif self.path == "/api/v1/user" and self.headers.get("Authorization") == "token synthetic-token":
            body = {"id":1,"login":"fixture","username":"fixture","full_name":"Synthetic fixture","email":"fixture@example.test"}
        else:
            self.send_error(404); return
        encoded = json.dumps(body).encode()
        self.send_response(200); self.send_header("Content-Type","application/json"); self.send_header("Content-Length",str(len(encoded))); self.end_headers(); self.wfile.write(encoded)

HTTPServer(("127.0.0.1",18080),Handler).serve_forever()
