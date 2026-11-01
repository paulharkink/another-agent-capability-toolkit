"""Session rotation recovery using a controlled Grafana HTTP fixture."""

import importlib.util
import json
from pathlib import Path
import sys
import threading
import types
from email.message import Message
from io import BytesIO
from urllib.request import Request
from urllib.response import addinfourl

_mock_requests_module = sys.modules.get("requests")
if _mock_requests_module is not None and not hasattr(_mock_requests_module, "__path__"):
    del sys.modules["requests"]
try:
    from requests.cookies import RequestsCookieJar
finally:
    if _mock_requests_module is not None and not hasattr(_mock_requests_module, "__path__"):
        for _module_name in tuple(sys.modules):
            if _module_name.startswith("requests."):
                del sys.modules[_module_name]
        sys.modules["requests"] = _mock_requests_module

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))


class TransportError(Exception):
    pass


class StopWorker(BaseException):
    """Stop the synchronous worker after observable recovery assertions."""


class Cookies(RequestsCookieJar):
    def set(self, name, value, **kwargs):
        if name in {"grafana_session", "grafana_session_expiry"}:
            kwargs.setdefault("domain", "grafana.example.test")
            kwargs.setdefault("path", "/")
            kwargs.setdefault("secure", True)
        return super().set(name, value, **kwargs)

    @property
    def values(self):
        return {cookie.name: cookie.value for cookie in self}

    def metadata_for(self, name):
        cookie = next(cookie for cookie in self if cookie.name == name)
        return {
            "domain": cookie.domain,
            "path": cookie.path,
            "secure": cookie.secure,
            "expires": cookie.expires,
            "discard": cookie.discard,
            "domain_specified": cookie.domain_specified,
            "http_only": cookie.has_nonstandard_attr("HttpOnly"),
        }


class FakeSession:
    def __init__(self):
        self.cookies = Cookies()
        self.headers = {}


def load_server(monkeypatch, tmp_path, with_auth=True):
    target = tmp_path / "target.toml"
    target.write_text(
        '[mcp]\nlocal_port=8765\n[grafana]\n'
        'url="https://grafana.example.test"\nauth_mode="session_cookie"\n'
    )
    auth = tmp_path / "auth.json"
    if with_auth:
        auth.write_text(json.dumps({
            "auth_mode": "session_cookie",
            "grafana_session": "synthetic-session",
            "session_expiry": "0",
        }))
    requests = types.ModuleType("requests")
    requests.Session = FakeSession
    requests.RequestException = TransportError

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
    monkeypatch.setenv("GRAFANA_AUTH_FILE", str(auth))
    monkeypatch.setenv("GRAFANA_TARGET", "fixture")
    monkeypatch.setenv("MCP_ENVIRONMENT", "test")
    monkeypatch.setenv("GRAFANA_AUTH_MODE", "session_cookie")
    monkeypatch.delenv("GRAFANA_API_TOKEN", raising=False)
    spec = importlib.util.spec_from_file_location(
        f"grafana_rotation_test_{id(tmp_path)}", ROOT / "server.py"
    )
    server = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(server)
    return server, auth


def test_worker_recovers_when_auth_file_arrives_after_startup(monkeypatch, tmp_path):
    server, auth_path = load_server(monkeypatch, tmp_path, with_auth=False)
    assert server.CLIENT is None
    rotations = []
    sleeps = []

    def sleep(delay):
        sleeps.append(delay)
        if len(sleeps) == 1:
            auth_path.write_text(json.dumps({
                "auth_mode": "session_cookie",
                "grafana_session": "later-session",
                "session_expiry": "0",
            }))
            return
        if rotations:
            raise StopWorker()

    def rotate():
        rotations.append(server.CLIENT.cookies.values["grafana_session"])
        server.CLIENT.cookies.set("grafana_session_expiry", "4102444800")

    monkeypatch.setattr(server.time, "sleep", sleep)
    monkeypatch.setattr(server.time, "time", lambda: 100)
    monkeypatch.setattr(server, "_rotate_session", rotate)

    with pytest.raises(StopWorker):
        server._rotation_worker()

    assert rotations == ["later-session"]
    assert sleeps == [1, 0, 30]


def test_worker_retries_transient_rotation_failure_and_persists_recovery(monkeypatch, tmp_path):
    server, auth_path = load_server(monkeypatch, tmp_path)
    attempts = []
    sleeps = []

    def sleep(delay):
        sleeps.append(delay)
        if len(attempts) >= 2 and delay > 1:
            raise StopWorker()

    def rotate():
        attempts.append("rotate")
        if len(attempts) == 1:
            raise TransportError("private-cookie-value")
        server.CLIENT.cookies.set("grafana_session", "rotated-session")
        server.CLIENT.cookies.set("grafana_session_expiry", "4102444800")
        server._persist_refreshed_cookies()

    monkeypatch.setattr(server.time, "sleep", sleep)
    monkeypatch.setattr(server.time, "time", lambda: 100)
    monkeypatch.setattr(server, "_rotate_session", rotate)

    with pytest.raises(StopWorker):
        server._rotation_worker()

    persisted = json.loads(auth_path.read_text())
    assert attempts == ["rotate", "rotate"]
    assert persisted["grafana_session"] == "rotated-session"
    assert persisted["session_expiry"] == "4102444800"
    assert persisted["auth_mode"] == "session_cookie"
    assert sleeps == [0, 1, 0, 30]


def test_route_affinity_cookie_survives_auth_reload_and_process_restart(monkeypatch, tmp_path):
    server, auth_path = load_server(monkeypatch, tmp_path)
    route_cookie_name = "9b1c88d0b504a1c37ef94c2da7d6be43"
    route_cookie_value = "synthetic-route-cookie"
    route_cookie_metadata = {
        "domain": ".grafana.example.test",
        "path": "/",
        "secure": True,
        "expires": None,
        "discard": True,
        "domain_specified": True,
        "http_only": True,
    }
    cookie_headers = Message()
    cookie_headers.add_header(
        "Set-Cookie",
        f"{route_cookie_name}={route_cookie_value}; Domain=grafana.example.test; Path=/; Secure; HttpOnly",
    )
    cookie_headers.add_header(
        "Set-Cookie",
        "0123456789abcdef0123456789abcdef=not-session; Domain=grafana.example.test; "
        "Path=/; Secure; HttpOnly; Max-Age=3600",
    )
    cookie_response = addinfourl(BytesIO(), cookie_headers, "https://grafana.example.test/")
    cookie_response.code = 200
    cookie_response.msg = "OK"
    cookie_request = Request("https://grafana.example.test/")

    # A successful upstream request teaches Requests the router's opaque
    # affinity cookie, which must be persisted without waiting for rotation.
    class Response:
        status_code = 200
        is_redirect = False
        headers = {"Content-Type": "application/json"}

        def raise_for_status(self):
            pass

        def json(self):
            return {"id": 1}

    def request(*args, **kwargs):
        server.CLIENT.cookies.extract_cookies(cookie_response, cookie_request)
        return Response()

    server.CLIENT.request = request
    monkeypatch.setattr(server, "_rotate_before_expiry", lambda: None)
    assert server._request("GET", "/api/user") == {"id": 1}

    persisted = json.loads(auth_path.read_text())
    assert persisted["route_cookies"] == [{
        "name": route_cookie_name,
        "value": route_cookie_value,
        "domain": ".grafana.example.test",
        "path": "/",
        "secure": True,
        "expires": None,
        "domain_specified": True,
        "http_only": True,
    }]
    assert auth_path.stat().st_mode & 0o777 == 0o600

    # A credential edit rebuilds the in-process Requests session, and a
    # container restart reconstructs it from the private auth file.
    auth_path.write_text(json.dumps({
        **persisted,
        "grafana_session": "replacement-session",
    }))
    server._reload_auth_if_changed()
    assert server.CLIENT.cookies.values[route_cookie_name] == route_cookie_value
    assert server.CLIENT.cookies.metadata_for(route_cookie_name) == route_cookie_metadata

    restarted, _ = load_server(monkeypatch, tmp_path, with_auth=False)
    assert restarted.CLIENT.cookies.values[route_cookie_name] == route_cookie_value
    assert restarted.CLIENT.cookies.metadata_for(route_cookie_name) == route_cookie_metadata
    for host in ("grafana.example.test", "sub.grafana.example.test"):
        request = Request(f"https://{host}/api/user")
        restarted.CLIENT.cookies.add_cookie_header(request)
        assert route_cookie_name in (request.get_header("Cookie") or "")


def test_only_host_bound_secure_session_route_cookie_is_restored(monkeypatch, tmp_path):
    server, _ = load_server(monkeypatch, tmp_path)
    base = {
        "name": "9b1c88d0b504a1c37ef94c2da7d6be43",
        "value": "synthetic-route-cookie",
        "domain": "grafana.example.test",
        "path": "/",
        "secure": True,
        "expires": None,
        "domain_specified": True,
        "http_only": True,
    }
    invalid_records = [
        {key: value for key, value in base.items() if key != "domain"},
        {key: value for key, value in base.items() if key != "domain_specified"},
        {key: value for key, value in base.items() if key != "expires"},
        {**base, "domain": ".example.test"},
        {**base, "domain": ".grafana.example.test", "domain_specified": False},
        {**base, "domain": ".grafana.example.test.evil.test"},
        {**base, "path": "/other"},
        {**base, "secure": False},
        {**base, "http_only": False},
        {**base, "expires": 4102444800},
        {**base, "name": "grafana_session"},
        {**base, "name": "not-a-route-cookie"},
    ]

    for record in invalid_records:
        assert server._configured_route_cookies({"route_cookies": [record]}) == []

    assert server._configured_route_cookies({"route_cookies": [base]}) == [base]
    monkeypatch.setattr(server, "GRAFANA_URL", "https://other.example.test")
    assert server._configured_route_cookies({"route_cookies": [base]}) == []


def test_host_only_affinity_cookie_remains_host_only_after_reload_and_restart(monkeypatch, tmp_path):
    server, auth_path = load_server(monkeypatch, tmp_path)
    route_cookie_name = "91b1a8d0b504a1c37ef94c2da7d6be43"
    route_cookie_value = "synthetic-host-only-cookie"
    headers = Message()
    headers.add_header(
        "Set-Cookie",
        f"{route_cookie_name}={route_cookie_value}; Path=/; Secure; HttpOnly",
    )
    response = addinfourl(BytesIO(), headers, "https://grafana.example.test/")
    response.code = 200
    response.msg = "OK"
    request = Request("https://grafana.example.test/")

    class Response:
        status_code = 200
        is_redirect = False
        headers = {"Content-Type": "application/json"}

        def raise_for_status(self):
            pass

        def json(self):
            return {"id": 1}

    def upstream_request(*args, **kwargs):
        server.CLIENT.cookies.extract_cookies(response, request)
        return Response()

    server.CLIENT.request = upstream_request
    monkeypatch.setattr(server, "_rotate_before_expiry", lambda: None)
    assert server._request("GET", "/api/user") == {"id": 1}
    persisted = json.loads(auth_path.read_text())["route_cookies"][0]
    assert persisted["domain"] == "grafana.example.test"
    assert persisted["domain_specified"] is False

    auth_path.write_text(json.dumps({
        "auth_mode": "session_cookie",
        "grafana_session": "replacement-session",
        "session_expiry": "4102444800",
        "route_cookies": [persisted],
    }))
    server._reload_auth_if_changed()
    restarted, _ = load_server(monkeypatch, tmp_path, with_auth=False)

    cookie = next(item for item in restarted.CLIENT.cookies if item.name == route_cookie_name)
    assert cookie.domain_specified is False
    for client in (server.CLIENT.cookies, restarted.CLIENT.cookies):
        host_request = Request("https://grafana.example.test/api/user")
        client.add_cookie_header(host_request)
        assert route_cookie_name in (host_request.get_header("Cookie") or "")
        subdomain_request = Request("https://sub.grafana.example.test/api/user")
        client.add_cookie_header(subdomain_request)
        assert route_cookie_name not in (subdomain_request.get_header("Cookie") or "")


def test_worker_failure_log_omits_exception_values(monkeypatch, tmp_path, capsys):
    server, _ = load_server(monkeypatch, tmp_path)
    calls = []

    def sleep(delay):
        calls.append(delay)
        if len(calls) == 2:
            raise StopWorker()

    monkeypatch.setattr(server.time, "sleep", sleep)
    monkeypatch.setattr(server.time, "time", lambda: 100)
    monkeypatch.setattr(
        server,
        "_rotate_session",
        lambda: (_ for _ in ()).throw(TransportError("private-cookie-value")),
    )

    with pytest.raises(StopWorker):
        server._rotation_worker()

    output = capsys.readouterr().out
    assert "TransportError" in output
    assert "private-cookie-value" not in output
    assert "retrying in 1s" in output


def test_retry_backoff_is_bounded(monkeypatch, tmp_path):
    server, _ = load_server(monkeypatch, tmp_path)
    sleeps = []

    def sleep(delay):
        sleeps.append(delay)
        if len(sleeps) == 6:
            raise StopWorker()

    monkeypatch.setattr(server.time, "sleep", sleep)
    monkeypatch.setattr(server.time, "time", lambda: 100)
    monkeypatch.setattr(server, "ROTATION_RETRY_INITIAL_SECONDS", 2)
    monkeypatch.setattr(server, "ROTATION_RETRY_MAX_SECONDS", 4)
    monkeypatch.setattr(
        server,
        "_rotate_session",
        lambda: (_ for _ in ()).throw(TransportError("synthetic outage")),
    )

    with pytest.raises(StopWorker):
        server._rotation_worker()

    assert sleeps == [0, 2, 0, 4, 0, 4]


def test_late_auth_is_detected_within_retry_poll_bound(monkeypatch, tmp_path):
    server, auth_path = load_server(monkeypatch, tmp_path, with_auth=False)
    sleeps = []
    rotations = []

    def sleep(delay):
        sleeps.append(delay)
        if len(sleeps) == 10:
            auth_path.write_text(json.dumps({
                "auth_mode": "session_cookie",
                "grafana_session": "late-session",
                "session_expiry": "0",
            }))
        elif rotations and delay == server.AUTH_RELOAD_INTERVAL_SECONDS:
            raise StopWorker()

    def rotate():
        rotations.append(server.CLIENT.cookies.values["grafana_session"])
        server.CLIENT.cookies.set("grafana_session_expiry", "4102444800")

    monkeypatch.setattr(server.time, "sleep", sleep)
    monkeypatch.setattr(server.time, "time", lambda: 100)
    monkeypatch.setattr(server, "_rotate_session", rotate)

    with pytest.raises(StopWorker):
        server._rotation_worker()

    assert rotations == ["late-session"]
    assert max(sleeps) <= server.AUTH_RELOAD_INTERVAL_SECONDS


def test_initial_bootstrap_failure_starts_only_one_background_worker(monkeypatch, tmp_path):
    server, _ = load_server(monkeypatch, tmp_path)
    starts = []

    class Thread:
        def __init__(self, **kwargs):
            self.kwargs = kwargs

        def start(self):
            starts.append(self.kwargs)

    monkeypatch.setattr(server.threading, "Thread", Thread)
    monkeypatch.setattr(
        server,
        "_bootstrap_session",
        lambda: (_ for _ in ()).throw(TransportError("secret must not be logged")),
    )

    server._start_session_rotation()
    server._start_session_rotation()

    assert len(starts) == 1
    assert starts[0]["target"] is server._rotation_worker
    assert starts[0]["daemon"] is True


def test_auth_reload_during_rotation_keeps_each_response_with_its_client(monkeypatch, tmp_path):
    server, auth_path = load_server(monkeypatch, tmp_path)
    old_client = server.CLIENT
    rotation_started = threading.Event()
    release_rotation = threading.Event()
    rotation_errors = []

    class Response:
        status_code = 200
        is_redirect = False

        def raise_for_status(self):
            return None

        def json(self):
            return {}

    def post(*args, **kwargs):
        rotation_started.set()
        assert release_rotation.wait(2)
        old_client.cookies.set("grafana_session", "rotated-old-session")
        old_client.cookies.set("grafana_session_expiry", "4102444800")
        return Response()

    old_client.post = post

    def rotate():
        try:
            server._rotate_session()
        except Exception as error:
            rotation_errors.append(f"{type(error).__name__}: {error}")

    rotation_thread = threading.Thread(target=rotate)
    rotation_thread.start()
    assert rotation_started.wait(2)

    # Simulate a later auth write appearing while Grafana is handling rotation.
    new_auth = {
        "auth_mode": "session_cookie",
        "grafana_session": "replacement-session",
    }
    expected_auth = json.dumps(new_auth).encode()
    auth_path.write_bytes(expected_auth)
    reload_thread = threading.Thread(target=server._reload_auth_if_changed)
    reload_thread.start()

    release_rotation.set()
    rotation_thread.join(2)
    reload_thread.join(2)

    assert not rotation_thread.is_alive()
    assert not reload_thread.is_alive()
    assert rotation_errors == []
    assert server.CLIENT.cookies.values["grafana_session"] == "replacement-session"
    assert auth_path.read_bytes() == expected_auth
