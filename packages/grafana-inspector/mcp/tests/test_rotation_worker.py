"""Session rotation recovery using a controlled Grafana HTTP fixture."""

import importlib.util
import json
from pathlib import Path
import sys
import threading
import types

import pytest

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))


class TransportError(Exception):
    pass


class StopWorker(BaseException):
    """Stop the synchronous worker after observable recovery assertions."""


class Cookies:
    def __init__(self):
        self.values = {}

    def set(self, name, value, **kwargs):
        self.values[name] = value

    def __iter__(self):
        return iter(types.SimpleNamespace(name=n, value=v) for n, v in self.values.items())


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
