"""Grafana credentials written after startup become active without a restart."""

import asyncio
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import types


ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))


class _Cookies:
    def __init__(self):
        self.values = {}

    def set(self, name, value, **kwargs):
        self.values[name] = value

    def __iter__(self):
        return iter(types.SimpleNamespace(name=name, value=value) for name, value in self.values.items())


class _Response:
    def __init__(self, status, payload):
        self.status_code = status
        self.is_redirect = False
        self.headers = {"Content-Type": "application/json"}
        self._payload = payload

    def raise_for_status(self):
        if self.status_code >= 400:
            raise AssertionError(f"unexpected provider status: {self.status_code}")

    def json(self):
        return self._payload


def _write_auth_with_existing_helper(root, session):
    request = {
        "protocol_version": 1,
        "interactive": False,
        "state_dir": str(root),
        "target": {"raw": {"grafana": {"url": "https://grafana.example.test", "auth_mode": "session_cookie"}}},
        "inputs": {
            "auth_mode": "session_cookie",
            "grafana_session": session,
            "session_expiry": "4102444800",
        },
    }
    completed = subprocess.run(
        [sys.executable, str(ROOT / "auth.py"), "authenticate"],
        input=json.dumps(request),
        text=True,
        capture_output=True,
        check=False,
    )
    assert completed.returncode == 0, completed.stderr
    assert json.loads(completed.stdout) == {"auth_required": False}


def test_running_server_uses_auth_file_created_and_replaced_after_startup(tmp_path, monkeypatch):
    target = tmp_path / "target.toml"
    target.write_text(
        '[mcp]\nlocal_port=8765\n[grafana]\n'
        'url="https://grafana.example.test"\nauth_mode="session_cookie"\n'
    )
    auth_path = tmp_path / "auth.json"
    assert not auth_path.exists()

    provider_requests = []

    class Session:
        def __init__(self):
            self.headers = {}
            self.cookies = _Cookies()

        def request(self, method, url, **kwargs):
            session = self.cookies.values.get("grafana_session")
            path = url.removeprefix("https://grafana.example.test")
            provider_requests.append((method, path, session))
            if session in {"current-session", "replacement-session"}:
                if path == "/api/user":
                    return _Response(200, {"id": 42, "login": "operator", "isAnonymous": False})
                if path == "/api/health":
                    return _Response(200, {"database": "ok"})
            return _Response(401, {"message": "unauthorized"})

    requests = types.ModuleType("requests")
    requests.Session = Session
    requests.RequestException = Exception

    class FastMCP:
        def __init__(self, *args, **kwargs):
            pass

        def tool(self):
            return lambda function: function

        def custom_route(self, *args, **kwargs):
            return lambda function: function

    fastmcp = types.ModuleType("fastmcp")
    fastmcp.FastMCP = FastMCP

    monkeypatch.setitem(sys.modules, "requests", requests)
    monkeypatch.setitem(sys.modules, "fastmcp", fastmcp)
    monkeypatch.setenv("GRAFANA_CONFIG", str(target))
    monkeypatch.setenv("GRAFANA_AUTH_FILE", str(auth_path))
    monkeypatch.setenv("GRAFANA_TARGET", "sample")
    monkeypatch.setenv("MCP_ENVIRONMENT", "test")
    monkeypatch.setenv("GRAFANA_AUTH_MODE", "session_cookie")
    monkeypatch.delenv("GRAFANA_API_TOKEN", raising=False)
    monkeypatch.delenv("GRAFANA_API_TOKEN_FILE", raising=False)

    spec = importlib.util.spec_from_file_location(
        "grafana_auth_reload_regression",
        ROOT / "server.py",
    )
    server = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(server)
    assert server.CLIENT is None

    unavailable = asyncio.run(server.health(None))
    assert json.loads(unavailable.body) == {"authenticated": False}
    assert provider_requests == []

    _write_auth_with_existing_helper(tmp_path, "current-session")
    assert json.loads(auth_path.read_text())["grafana_session"] == "current-session"

    available = asyncio.run(server.health(None))
    assert json.loads(available.body) == {"authenticated": True}
    tool_result = server.grafana_health()
    assert tool_result["health"] == {"database": "ok"}
    assert provider_requests[-2:] == [
        ("GET", "/api/user", "current-session"),
        ("GET", "/api/health", "current-session"),
    ]

    _write_auth_with_existing_helper(tmp_path, "replacement-session")
    assert json.loads(auth_path.read_text())["grafana_session"] == "replacement-session"

    replaced = asyncio.run(server.health(None))
    assert json.loads(replaced.body) == {"authenticated": True}
    tool_result = server.grafana_health()
    assert tool_result["health"] == {"database": "ok"}
    assert provider_requests[-2:] == [
        ("GET", "/api/user", "replacement-session"),
        ("GET", "/api/health", "replacement-session"),
    ]
