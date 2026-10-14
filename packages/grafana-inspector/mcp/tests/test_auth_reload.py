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
            if self.headers.get("Authorization") == "Bearer authoritative-env-token":
                if path == "/api/user/preferences":
                    return _Response(200, {"theme": "dark"})
                if path == "/api/health":
                    return _Response(200, {"database": "ok"})
            if session in {"current-session", "replacement-session"}:
                if path == "/api/user":
                    return _Response(200, {"id": 42, "login": "operator", "isAnonymous": False})
                if path == "/api/health":
                    return _Response(200, {"database": "ok"})
            return _Response(401, {"message": "unauthorized"})

        def post(self, url, **kwargs):
            return self.request("POST", url, **kwargs)

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
    monkeypatch.setenv("GRAFANA_API_TOKEN_FILE", str(tmp_path / "irrelevant-missing-token"))

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

    def assert_fail_closed():
        before = len(provider_requests)
        unavailable = asyncio.run(server.health(None))
        assert json.loads(unavailable.body) == {"authenticated": False}
        tool_result = server.grafana_health()
        assert tool_result == {"error": "Grafana authentication is unavailable."}
        assert len(provider_requests) == before

    # A failed read cannot leave the previously valid session client in use.
    original_read_bytes = Path.read_bytes

    def unreadable_auth(path):
        if path == auth_path:
            raise PermissionError("private auth details")
        return original_read_bytes(path)

    monkeypatch.setattr(Path, "read_bytes", unreadable_auth)
    assert_fail_closed()
    monkeypatch.setattr(Path, "read_bytes", original_read_bytes)
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}

    for invalid_auth in ("{broken", "[]"):
        auth_path.write_text(invalid_auth)
        assert_fail_closed()
        _write_auth_with_existing_helper(tmp_path, "current-session")
        assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}

    auth_path.unlink()
    assert_fail_closed()
    _write_auth_with_existing_helper(tmp_path, "current-session")
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}

    # The existing helper persists a replacement session atomically. A rejected
    # replacement must never fall back to the previously valid principal.
    _write_auth_with_existing_helper(tmp_path, "invalid-replacement-session")
    rejected = asyncio.run(server.health(None))
    assert json.loads(rejected.body) == {"authenticated": False}
    tool_result = server.grafana_health()
    assert tool_result == {
        "error": "Grafana rejected credentials (HTTP 401). Check the configured authentication method and credentials."
    }
    assert provider_requests[-4:] == [
        ("GET", "/api/user", "invalid-replacement-session"),
        ("POST", "/api/user/auth-tokens/rotate", "invalid-replacement-session"),
        ("GET", "/api/health", "invalid-replacement-session"),
        ("POST", "/api/user/auth-tokens/rotate", "invalid-replacement-session"),
    ]

    # An explicit environment token remains authoritative over malformed file data.
    monkeypatch.setenv("GRAFANA_AUTH_MODE", "api_token")
    monkeypatch.setenv("GRAFANA_API_TOKEN", "authoritative-env-token")
    auth_path.write_text(json.dumps({"api_token": "file-token"}))
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}
    auth_path.write_text("{broken")
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}
    assert server.grafana_health()["health"] == {"database": "ok"}


def test_malformed_auth_file_at_startup_keeps_health_route_available(tmp_path, monkeypatch):
    target = tmp_path / "target.toml"
    target.write_text(
        '[mcp]\nlocal_port=8765\n[grafana]\n'
        'url="https://grafana.example.test"\nauth_mode="session_cookie"\n'
    )
    auth_path = tmp_path / "auth.json"
    auth_path.write_text("{broken")

    class Session:
        def __init__(self):
            self.headers = {}
            self.cookies = _Cookies()

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
        "grafana_malformed_startup_regression",
        ROOT / "server.py",
    )
    server = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(server)

    assert server.CLIENT is None
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": False}


def test_health_rejects_malformed_is_anonymous_identity_flag(tmp_path, monkeypatch):
    # Reuse the full server setup above while varying only Grafana's identity response.
    target = tmp_path / "target.toml"
    target.write_text(
        '[mcp]\nlocal_port=8765\n[grafana]\n'
        'url="https://grafana.example.test"\nauth_mode="session_cookie"\n'
    )
    auth_path = tmp_path / "auth.json"
    auth_path.write_text(json.dumps({"grafana_session": "current-session", "session_expiry": "4102444800"}))

    identities = [{"id": 42, "login": "operator", "isAnonymous": "true"}]

    class Session:
        def __init__(self):
            self.headers = {}
            self.cookies = _Cookies()
            self.cookies.set("grafana_session", "current-session")

        def request(self, method, url, **kwargs):
            if url.endswith("/api/user"):
                return _Response(200, identities[0])
            return _Response(200, {"database": "ok"})

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
        "grafana_malformed_identity_regression",
        ROOT / "server.py",
    )
    server = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(server)

    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": False}
    identities[0] = {"id": 42, "login": "operator", "isAnonymous": False}
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}
    identities[0] = {"id": 42, "login": "operator"}
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}


def test_running_server_reloads_mounted_api_token_file_on_every_request(tmp_path, monkeypatch):
    target = tmp_path / "target.toml"
    target.write_text(
        '[mcp]\nlocal_port=8765\n[grafana]\n'
        'url="https://grafana.example.test"\nauth_mode="api_token"\n'
    )
    auth_path = tmp_path / "auth.json"
    auth_path.write_text('{"auth_mode":"api_token","api_token":"stale-auth-file-token"}')
    token_path = tmp_path / "mounted-token"
    token_path.write_text("first-valid-token\n")
    auth_before = auth_path.read_bytes()
    provider_requests = []

    class Session:
        def __init__(self):
            self.headers = {}
            self.cookies = _Cookies()

        def request(self, method, url, **kwargs):
            path = url.removeprefix("https://grafana.example.test")
            authorization = self.headers.get("Authorization")
            provider_requests.append((method, path, authorization))
            if authorization in {
                "Bearer first-valid-token",
                "Bearer replacement-valid-token",
                "Bearer malformed-auth-rotation-token",
                "Bearer unreadable-auth-rotation-token",
            }:
                payload = {"theme": "dark"} if path == "/api/user/preferences" else {"database": "ok"}
                return _Response(200, payload)
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
    monkeypatch.setenv("GRAFANA_API_TOKEN_FILE", str(token_path))
    monkeypatch.delenv("GRAFANA_API_TOKEN", raising=False)
    monkeypatch.delenv("GRAFANA_AUTH_MODE", raising=False)
    monkeypatch.setenv("GRAFANA_TARGET", "sample")
    monkeypatch.setenv("MCP_ENVIRONMENT", "test")

    spec = importlib.util.spec_from_file_location(
        "grafana_mounted_token_reload_regression", ROOT / "server.py"
    )
    server = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(server)
    assert server.CLIENT is not None
    assert auth_path.read_bytes() == auth_before

    def assert_health(authenticated):
        response = asyncio.run(server.health(None))
        assert json.loads(response.body) == {"authenticated": authenticated}

    assert_health(True)
    assert server.grafana_health()["health"] == {"database": "ok"}
    assert provider_requests[-2:] == [
        ("GET", "/api/user/preferences", "Bearer first-valid-token"),
        ("GET", "/api/health", "Bearer first-valid-token"),
    ]

    token_path.write_text("invalid-token\n")
    assert_health(False)
    assert server.grafana_health() == {
        "error": "Grafana rejected credentials (HTTP 401). Check the configured authentication method and credentials."
    }
    assert provider_requests[-2:] == [
        ("GET", "/api/user/preferences", "Bearer invalid-token"),
        ("GET", "/api/health", "Bearer invalid-token"),
    ]

    token_path.write_text("replacement-valid-token\n")
    assert_health(True)
    assert server.grafana_health()["health"] == {"database": "ok"}
    assert provider_requests[-2:] == [
        ("GET", "/api/user/preferences", "Bearer replacement-valid-token"),
        ("GET", "/api/health", "Bearer replacement-valid-token"),
    ]

    token_path.unlink()
    before_missing = len(provider_requests)
    assert_health(False)
    assert server.grafana_health() == {"error": "Grafana authentication is unavailable."}
    assert len(provider_requests) == before_missing

    token_path.write_text("replacement-valid-token\n")
    original_read_text = Path.read_text

    def unreadable_token(path, *args, **kwargs):
        if path == token_path:
            raise PermissionError("private token details")
        return original_read_text(path, *args, **kwargs)

    monkeypatch.setattr(Path, "read_text", unreadable_token)
    before_unreadable = len(provider_requests)
    assert_health(False)
    assert server.grafana_health() == {"error": "Grafana authentication is unavailable."}
    assert len(provider_requests) == before_unreadable
    monkeypatch.setattr(Path, "read_text", original_read_text)
    assert_health(True)
    assert server.grafana_health()["health"] == {"database": "ok"}
    assert provider_requests[-2:] == [
        ("GET", "/api/user/preferences", "Bearer replacement-valid-token"),
        ("GET", "/api/health", "Bearer replacement-valid-token"),
    ]

    # A broken inactive auth.json must not pin the previous mounted token.
    auth_path.write_text("{broken")
    token_path.write_text("malformed-auth-rotation-token\n")
    assert_health(True)
    assert server.grafana_health()["health"] == {"database": "ok"}
    assert provider_requests[-2:] == [
        ("GET", "/api/user/preferences", "Bearer malformed-auth-rotation-token"),
        ("GET", "/api/health", "Bearer malformed-auth-rotation-token"),
    ]

    # The same precedence applies when auth.json cannot be read at all.
    token_path.write_text("unreadable-auth-rotation-token\n")
    original_read_bytes = Path.read_bytes

    def unreadable_inactive_auth(path):
        if path == auth_path:
            raise PermissionError("private auth details")
        return original_read_bytes(path)

    monkeypatch.setattr(Path, "read_bytes", unreadable_inactive_auth)
    assert_health(True)
    assert server.grafana_health()["health"] == {"database": "ok"}
    assert provider_requests[-2:] == [
        ("GET", "/api/user/preferences", "Bearer unreadable-auth-rotation-token"),
        ("GET", "/api/health", "Bearer unreadable-auth-rotation-token"),
    ]
    monkeypatch.setattr(Path, "read_bytes", original_read_bytes)

    assert auth_path.read_bytes() == b"{broken"


def test_missing_mounted_api_token_at_startup_keeps_server_available_and_recovers(tmp_path, monkeypatch):
    target = tmp_path / "target.toml"
    target.write_text(
        '[mcp]\nlocal_port=8765\n[grafana]\n'
        'url="https://grafana.example.test"\nauth_mode="api_token"\n'
    )
    auth_path = tmp_path / "auth.json"
    auth_path.write_text('{"auth_mode":"api_token","api_token":"stale-auth-file-token"}')
    token_path = tmp_path / "mounted-token"
    provider_requests = []

    class Session:
        def __init__(self):
            self.headers = {}
            self.cookies = _Cookies()

        def request(self, method, url, **kwargs):
            path = url.removeprefix("https://grafana.example.test")
            authorization = self.headers.get("Authorization")
            provider_requests.append((method, path, authorization))
            if authorization == "Bearer appeared-token":
                payload = {"theme": "dark"} if path == "/api/user/preferences" else {"database": "ok"}
                return _Response(200, payload)
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
    monkeypatch.setenv("GRAFANA_API_TOKEN_FILE", str(token_path))
    monkeypatch.delenv("GRAFANA_API_TOKEN", raising=False)
    monkeypatch.delenv("GRAFANA_AUTH_MODE", raising=False)
    monkeypatch.setenv("GRAFANA_TARGET", "sample")
    monkeypatch.setenv("MCP_ENVIRONMENT", "test")

    spec = importlib.util.spec_from_file_location(
        "grafana_missing_mounted_token_startup_regression", ROOT / "server.py"
    )
    server = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(server)

    assert server.CLIENT is None
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": False}
    assert server.grafana_health() == {"error": "Grafana authentication is unavailable."}
    assert provider_requests == []

    token_path.write_text("appeared-token\n")
    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}
    assert server.grafana_health()["health"] == {"database": "ok"}
    assert provider_requests == [
        ("GET", "/api/user/preferences", "Bearer appeared-token"),
        ("GET", "/api/health", "Bearer appeared-token"),
    ]


def test_environment_api_token_precedes_token_file_and_ignores_malformed_auth_json(tmp_path, monkeypatch):
    target = tmp_path / "target.toml"
    target.write_text(
        '[mcp]\nlocal_port=8765\n[grafana]\n'
        'url="https://grafana.example.test"\nauth_mode="api_token"\n'
    )
    auth_path = tmp_path / "auth.json"
    auth_path.write_text("{malformed")
    token_path = tmp_path / "mounted-token"
    token_path.write_text("lower-priority-file-token")
    provider_requests = []

    class Session:
        def __init__(self):
            self.headers = {}
            self.cookies = _Cookies()

        def request(self, method, url, **kwargs):
            authorization = self.headers.get("Authorization")
            provider_requests.append(authorization)
            payload = {"theme": "dark"} if authorization == "Bearer explicit-env-token" else {"message": "unauthorized"}
            return _Response(200 if authorization == "Bearer explicit-env-token" else 401, payload)

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
    monkeypatch.setenv("GRAFANA_API_TOKEN_FILE", str(token_path))
    monkeypatch.setenv("GRAFANA_API_TOKEN", "explicit-env-token")
    monkeypatch.delenv("GRAFANA_AUTH_MODE", raising=False)
    monkeypatch.setenv("GRAFANA_TARGET", "sample")
    monkeypatch.setenv("MCP_ENVIRONMENT", "test")

    spec = importlib.util.spec_from_file_location(
        "grafana_env_token_precedence_regression", ROOT / "server.py"
    )
    server = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(server)

    assert json.loads(asyncio.run(server.health(None)).body) == {"authenticated": True}
    assert server.grafana_health()["health"] == {"theme": "dark"}
    assert provider_requests == [
        "Bearer explicit-env-token",
        "Bearer explicit-env-token",
    ]
