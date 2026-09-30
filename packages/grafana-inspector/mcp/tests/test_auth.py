"""Preserve launcher authentication through the container action boundary."""
import json
import os
import stat
import subprocess
import sys
from pathlib import Path
from http.server import BaseHTTPRequestHandler, HTTPServer
from threading import Thread
import pytest

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

def action(tmp_path, inputs=None, cfg=None):
    return {"protocol_version": 1, "inputs": inputs or {}, "target": {"raw": {"grafana": cfg or {"url":"https://grafana.example.test", "auth_mode":"session_cookie"}}}, "state_dir": str(tmp_path), "interactive": False}

def invoke(request, operation="prepare"):
    result = subprocess.run([sys.executable, str(Path(__file__).resolve().parents[1]/"auth.py"), operation], input=json.dumps(request), text=True, capture_output=True)
    assert result.returncode == 0, result.stderr
    return json.loads(result.stdout)

def test_missing_credentials_return_auth_required_without_prompt(tmp_path):
    assert invoke(action(tmp_path)) == {"auth_required": True}
    assert not (tmp_path/"auth.json").exists()

def test_native_session_needs_no_proxy_refresh_cookie_and_private_atomic_state(tmp_path):
    assert invoke(action(tmp_path, {"grafana_session":"synthetic-session"}), "authenticate") == {"auth_required":False}
    auth = tmp_path/"auth.json"
    assert json.loads(auth.read_text()) == {"auth_mode":"session_cookie", "grafana_session":"synthetic-session"}
    assert stat.S_IMODE(auth.stat().st_mode) == 0o600
    assert not list(tmp_path.glob("*.tmp"))

def test_proxy_refresh_cookie_required_only_if_configured(tmp_path):
    request = action(tmp_path, {"grafana_session":"synthetic-session"}, {"url":"https://grafana.example.test", "auth_mode":"session_cookie", "refresh_cookie_name":"example_refresh"})
    assert invoke(request, "authenticate") == {"auth_required":True}
    request["inputs"]["oauth_refresh"] = "synthetic-refresh"
    assert invoke(request, "authenticate") == {"auth_required":False}
    assert json.loads((tmp_path/"auth.json").read_text())["refresh_cookie_name"] == "example_refresh"

def test_api_token_override_and_persisted_mode_survive_prepare(tmp_path):
    request = action(tmp_path, {"auth_mode":"api_token", "token":"synthetic-token"})
    assert invoke(request,"authenticate") == {"auth_required":False}
    assert invoke(action(tmp_path)) == {"auth_required":False}
    assert json.loads((tmp_path/"auth.json").read_text())["auth_mode"] == "api_token"

def test_token_update_preserves_other_auth_fields(tmp_path):
    (tmp_path/"auth.json").write_text(json.dumps({"auth_mode":"api_token","api_token":"old", "session_expiry":"1700000000"}))
    assert invoke(action(tmp_path,{"token":"new"}), "authenticate") == {"auth_required":False}
    assert json.loads((tmp_path/"auth.json").read_text())["session_expiry"] == "1700000000"

def test_vault_fallback_uses_local_http_fixture(tmp_path, monkeypatch):
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            if self.path != "/v1/secret/data/example" or self.headers.get("X-Vault-Token") != "synthetic-vault-login":
                self.send_error(403); return
            body=json.dumps({"data":{"data":{"token":"synthetic-grafana-token"}}}).encode()
            self.send_response(200); self.send_header("Content-Length",str(len(body))); self.end_headers(); self.wfile.write(body)
        def log_message(self,*args): pass
    server=HTTPServer(("127.0.0.1",0),Handler); thread=Thread(target=server.serve_forever,daemon=True); thread.start()
    monkeypatch.setenv("VAULT_TOKEN","synthetic-vault-login")
    try:
        cfg={"url":"https://grafana.example.test","auth_mode":"api_token","vault_addr":f"http://127.0.0.1:{server.server_port}","vault_path":"secret/data/example"}
        assert invoke(action(tmp_path,cfg=cfg)) == {"auth_required":False}
        assert json.loads((tmp_path/"auth.json").read_text())["api_token"] == "synthetic-grafana-token"
    finally:
        server.shutdown(); server.server_close(); thread.join(2)
