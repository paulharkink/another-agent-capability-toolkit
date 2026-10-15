"""Deterministic tests for background container-owned authentication."""

from __future__ import annotations

import importlib.util
import io
import json
import os
from pathlib import Path
import subprocess
import sys
import time
from starlette.testclient import TestClient

import pytest


SERVER = Path(__file__).resolve().parents[1] / "server.py"
PROFILE_ENV = {
    "server": "AACT_ARGOCD_SERVER",
    "auth_mode": "AACT_ARGOCD_AUTH_MODE",
    "grpc_web": "AACT_ARGOCD_GRPC_WEB",
    "grpc_web_root_path": "AACT_ARGOCD_GRPC_WEB_ROOT_PATH",
    "plaintext": "AACT_ARGOCD_PLAINTEXT",
    "insecure": "AACT_ARGOCD_INSECURE",
    "skip_test_tls": "AACT_ARGOCD_SKIP_TEST_TLS",
}


def set_profile_env(monkeypatch, *, server="argocd.example.test", auth_mode="sso",
                    grpc_web=False, grpc_web_root_path="", plaintext=False, insecure=False,
                    skip_test_tls=False):
    values = {
        "server": server,
        "auth_mode": auth_mode,
        "grpc_web": grpc_web,
        "grpc_web_root_path": grpc_web_root_path,
        "plaintext": plaintext,
        "insecure": insecure,
        "skip_test_tls": skip_test_tls,
    }
    for key, env_name in PROFILE_ENV.items():
        value = values[key]
        monkeypatch.setenv(env_name, value if isinstance(value, str) else str(value).lower())


def load_server():
    spec = importlib.util.spec_from_file_location("argocd_server_auth_test", SERVER)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def request(tmp_path, mode="sso", **inputs):
    return {
        "protocol_version": 1,
        "action": "authenticate",
        "inputs": {"server": "argocd.example.test", "auth_mode": mode, **inputs},
        "state_dir": str(tmp_path / "state"),
    }


def seed_previous_auth(state: Path) -> tuple[bytes, bytes]:
    state.mkdir(parents=True)
    config = state / "config"
    config.write_bytes(b"previous session")
    config.chmod(0o600)
    credentials = state / "credentials.json"
    credentials.write_text('{"mode":"api_token","api_token":"old-token","server":"old.example"}')
    credentials.chmod(0o600)
    return config.read_bytes(), credentials.read_bytes()


def test_authenticate_returns_login_url_and_leaves_worker_running(tmp_path, monkeypatch):
    module = load_server()
    request_value = request(tmp_path, callback_port=8085)
    calls = []

    def launch(value):
        calls.append(value)
        return {"login_url": "https://login.example/device"}

    monkeypatch.setattr(module, "launch_auth_worker", launch)
    result = module.authenticate(request_value)
    assert result == {"login_url": "https://login.example/device"}
    assert calls == [request_value]
    assert "status" not in result


def test_worker_launcher_returns_event_without_waiting_for_or_killing_child(tmp_path, monkeypatch):
    module = load_server()
    read_fd, write_fd = os.pipe()

    class Child:
        def __init__(self, argv, **kwargs):
            self.argv = argv
            self.kwargs = kwargs
            self.stdin = io.BytesIO()
            self.stdout = os.fdopen(read_fd, "rb")
            self.waited = False
            self.terminated = False

        def wait(self, *args, **kwargs):
            self.waited = True

        def terminate(self):
            self.terminated = True

    children = []

    def launch(argv, **kwargs):
        process = Child(argv, **kwargs)
        children.append(process)
        return process

    monkeypatch.setattr(module.subprocess, "Popen", launch)
    os.write(write_fd, b'{"login_url":"https://login.example/device"}\n')
    try:
        req = request(tmp_path, callback_port=8085)
        assert module.launch_auth_worker(req) == {"login_url": "https://login.example/device"}
        child = children[0]
        assert child.kwargs["start_new_session"] is True
        assert child.waited is False and child.terminated is False
    finally:
        os.close(write_fd)


def test_background_worker_completes_sso_and_promotes_config_without_action_poll(tmp_path):
    module = load_server()
    req = request(tmp_path, callback_port=8085)
    state = Path(req["state_dir"])
    observed = {}

    def login_runner(mode, argv, *, env, password, on_login_url):
        assert mode == "sso"
        assert argv == ["/usr/local/bin/argocd", "login", "--server", "argocd.example.test", "--sso", "--sso-port", "8085"]
        assert password is None
        observed["candidate"] = Path(env["ARGOCD_CONFIG"])
        on_login_url("https://login.example/device")
        observed["candidate"].write_text("callback completed login")
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    def identity(_state, server, inputs, *, config_path, token=None):
        assert server == "argocd.example.test"
        assert token is None
        assert config_path == observed["candidate"]
        return True

    assert module.run_auth_worker(req, login_runner=login_runner, identity_checker=identity) == {
        "login_url": "https://login.example/device"
    }
    assert (state / "config").read_text() == "callback completed login"
    assert (state / "config").stat().st_mode & 0o777 == 0o600
    assert json.loads((state / "credentials.json").read_text()) == {
        "mode": "sso", "server": "argocd.example.test", "connection": {
            "grpc_web": False, "grpc_web_root_path": "", "plaintext": False, "insecure": False,
        }
    }
    assert module.is_authenticated(req, identity_checker=lambda *_a, **_k: True) is True
    assert module.is_authenticated(req, identity_checker=lambda *_a, **_k: False) is False
    assert not hasattr(module, "authentication_status")
    assert not hasattr(module, "cancel_authentication")


def test_one_shot_action_returns_url_then_synthetic_callback_completes_detached_worker(tmp_path):
    """Local fake CLI exercises the actual detached worker and a synthetic callback signal."""
    source = SERVER.read_text(encoding="utf-8")
    fake_cli = tmp_path / "fake-argocd"
    fake_cli.write_text(
        "#!/usr/bin/env python3\n"
        "import os, pathlib, sys, time\n"
        "args=sys.argv[1:]\n"
        "if args and args[0]=='login':\n"
        " print('Open https://login.synthetic.test/device', flush=True)\n"
        " deadline=time.monotonic()+5\n"
        " marker=pathlib.Path(os.environ['FAKE_CALLBACK_FILE'])\n"
        " while not marker.exists() and time.monotonic()<deadline: time.sleep(.02)\n"
        " if not marker.exists(): raise SystemExit(9)\n"
        " pathlib.Path(os.environ['ARGOCD_CONFIG']).write_text('synthetic callback session')\n"
        " pathlib.Path(os.environ['WORKER_PID_FILE']).write_text(str(os.getppid()))\n"
        " raise SystemExit(0)\n"
        "if args[:2]==['account','get-user-info']:\n"
        " config=pathlib.Path(os.environ['ARGOCD_CONFIG'])\n"
        " raise SystemExit(0 if config.exists() and 'synthetic callback session' in config.read_text() else 1)\n"
        "raise SystemExit(2)\n",
        encoding="utf-8",
    )
    fake_cli.chmod(0o700)
    test_server = tmp_path / "server.py"
    test_server.write_text(source.replace('CLI = "/usr/local/bin/argocd"', f'CLI = "{fake_cli}"'), encoding="utf-8")

    callback = tmp_path / "synthetic-callback"
    worker_pid_file = tmp_path / "worker-pid"
    req = request(tmp_path, callback_port=8085)
    env = os.environ.copy()
    env["FAKE_CALLBACK_FILE"] = str(callback)
    env["WORKER_PID_FILE"] = str(worker_pid_file)
    try:
        action = subprocess.run(
            [sys.executable, str(test_server), "action", "authenticate"],
            input=json.dumps(req).encode(), stdout=subprocess.PIPE, stderr=subprocess.PIPE,
            env=env, timeout=8, check=False,
        )
        assert action.returncode == 0
        assert json.loads(action.stdout) == {"login_url": "https://login.synthetic.test/device"}
        assert worker_pid_file.exists() is False

        # The one-shot action has exited; this marker stands in for the browser callback.
        callback.touch()
        deadline = time.monotonic() + 6
        saved = Path(req["state_dir"]) / "credentials.json"
        while time.monotonic() < deadline and (
            not saved.exists() or json.loads(saved.read_text()).get("replacement_pending") is True
        ):
            time.sleep(0.02)
        assert saved.exists()
        assert json.loads(saved.read_text()) == {
            "mode": "sso", "server": "argocd.example.test", "connection": {
                "grpc_web": False, "grpc_web_root_path": "", "plaintext": False, "insecure": False,
            }
        }
        assert (Path(req["state_dir"]) / "config").read_text() == "synthetic callback session"

        worker_pid = int(worker_pid_file.read_text())
        exit_deadline = time.monotonic() + 2
        while time.monotonic() < exit_deadline:
            try:
                os.kill(worker_pid, 0)
            except ProcessLookupError:
                break
            time.sleep(0.02)
    finally:
        # Always release only this test's synthetic CLI if an assertion fails.
        callback.touch()


def test_auth_token_and_password_modes_share_boolean_auth_check(tmp_path):
    module = load_server()
    token_req = request(tmp_path / "token", "api_token", api_token="synthetic-token")
    observed = {}

    def identity(_state, server, inputs, *, config_path=None, token=None):
        observed["identity"] = (server, token, config_path)
        return True

    assert module.run_auth_worker(token_req, identity_checker=identity) == {}
    assert observed["identity"][1] == "synthetic-token"
    assert Path(token_req["state_dir"]).stat().st_mode & 0o777 == 0o700
    assert (Path(token_req["state_dir"]) / "credentials.json").stat().st_mode & 0o777 == 0o600
    assert module.is_authenticated(token_req, identity_checker=identity) is True

    password_req = request(
        tmp_path / "password", "username_password", username="synthetic-user", password="synthetic-password"
    )

    def password_login(mode, argv, *, env, password, on_login_url):
        assert mode == "username_password"
        assert "synthetic-password" not in " ".join(argv)
        assert password == "synthetic-password"
        Path(env["ARGOCD_CONFIG"]).write_text("password login session")
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    assert module.run_auth_worker(password_req, login_runner=password_login, identity_checker=identity) == {}
    assert module.is_authenticated(password_req, identity_checker=identity) is True
    assert module.is_authenticated(password_req, identity_checker=lambda *_a, **_k: False) is False

    saved_only = {"state_dir": token_req["state_dir"], "inputs": {"server": "argocd.example.test", "auth_mode": "api_token"}}
    assert module.is_authenticated(saved_only, identity_checker=identity) is True


def test_auth_failure_expiry_and_invalid_credentials_preserve_previous_state(tmp_path):
    module = load_server()
    req = request(tmp_path, "username_password", username="synthetic-user", password="new-secret")
    state = Path(req["state_dir"])
    old_config, old_credentials = seed_previous_auth(state)

    def rejected_login(_mode, argv, *, env, password, on_login_url):
        Path(env["ARGOCD_CONFIG"]).write_text("incomplete login state")
        return subprocess.CompletedProcess(argv, 1, stdout=b"", stderr=b"expired password")

    assert module.run_auth_worker(req, login_runner=rejected_login, identity_checker=lambda *_a, **_k: False) == {}
    assert (state / "config").read_bytes() == old_config
    credentials_after_failure = json.loads((state / "credentials.json").read_text())
    assert credentials_after_failure["api_token"] == "old-token"
    assert credentials_after_failure["replacement_pending"] is True
    assert (state / "credentials.json").stat().st_mode & 0o777 == 0o600
    assert module.is_authenticated(req, identity_checker=lambda *_a, **_k: False) is False
    assert not list(state.glob(".argocd-config-*"))

    token_req = request(tmp_path / "token", "api_token", api_token="rejected-token")
    token_state = Path(token_req["state_dir"])
    _, token_old = seed_previous_auth(token_state)
    assert module.run_auth_worker(token_req, identity_checker=lambda *_a, **_k: False) == {}
    credentials_after_failure = json.loads((token_state / "credentials.json").read_text())
    assert credentials_after_failure["api_token"] == "old-token"
    assert credentials_after_failure["replacement_pending"] is True
    assert module.is_authenticated(token_req, identity_checker=lambda *_a, **_k: False) is False


@pytest.mark.parametrize(
    ("mode", "replacement_inputs"),
    [
        ("api_token", {}),
        ("api_token", {"api_token": ""}),
        ("username_password", {"username": "new-user"}),
        ("username_password", {"username": "new-user", "password": ""}),
    ],
)
def test_invalid_replacement_credentials_make_health_false_and_preserve_config(
    tmp_path, monkeypatch, mode, replacement_inputs
):
    state = tmp_path / "state"
    original_config = b"existing authenticated config"
    state.mkdir(mode=0o700)
    (state / "config").write_bytes(original_config)
    (state / "config").chmod(0o600)
    previous_credentials = {
        "mode": mode,
        "server": "argocd.example.test",
        "connection": {
            "grpc_web": False, "grpc_web_root_path": "", "plaintext": False, "insecure": False,
        },
    }
    if mode == "api_token":
        previous_credentials["api_token"] = "valid-old-token"
    credentials_path = state / "credentials.json"
    credentials_path.write_text(json.dumps(previous_credentials))
    credentials_path.chmod(0o600)

    monkeypatch.setenv("AACT_AUTH_STATE_DIR", str(state))
    set_profile_env(monkeypatch, auth_mode=mode)
    module = load_server()
    monkeypatch.setattr(module, "_identity", lambda *_a, **_k: True)
    health = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http"))
    assert health.get("/health").json() == {"authenticated": True}

    req = request(tmp_path, mode, **replacement_inputs)
    result = module.authenticate(req)
    assert "diagnostic" in result
    assert health.get("/health").json() == {"authenticated": False}
    assert (state / "config").read_bytes() == original_config
    assert (state / "config").stat().st_mode & 0o777 == 0o600
    saved_credentials = json.loads(credentials_path.read_text())
    if mode == "api_token":
        assert saved_credentials["api_token"] == "valid-old-token"
    assert saved_credentials["replacement_pending"] is True
    assert credentials_path.stat().st_mode & 0o777 == 0o600


@pytest.mark.parametrize("mode", ["api_token", "username_password"])
def test_failed_replacement_login_makes_health_false_until_requested_login_succeeds(
    tmp_path, monkeypatch, mode
):
    module = load_server()
    state = tmp_path / "state"
    state.mkdir(mode=0o700)
    old_credentials = {
        "mode": mode,
        "server": "argocd.example.test",
        "connection": {
            "grpc_web": False, "grpc_web_root_path": "", "plaintext": False, "insecure": False,
        },
    }
    if mode == "api_token":
        old_credentials["api_token"] = "valid-old-token"
        req = request(tmp_path, mode, api_token="invalid-new-token")
    else:
        req = request(tmp_path, mode, username="new-user", password="invalid-new-password")
    (state / "credentials.json").write_text(json.dumps(old_credentials))
    (state / "config").write_text("valid old session")
    monkeypatch.setattr(module, "STATE_DIR", state)
    set_profile_env(monkeypatch, auth_mode=mode)
    monkeypatch.setattr(module, "_identity", lambda *_args, **_kwargs: True)

    if mode == "api_token":
        assert module.run_auth_worker(req, identity_checker=lambda *_a, **_k: False) == {}
    else:
        def rejected_login(_mode, argv, *, env, password, on_login_url):
            return subprocess.CompletedProcess(argv, 1, stdout=b"", stderr=b"rejected")

        assert module.run_auth_worker(
            req, login_runner=rejected_login, identity_checker=lambda *_a, **_k: False
        ) == {}

    response = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.status_code == 200
    assert response.json() == {"authenticated": False}
    credentials_after_failure = json.loads((state / "credentials.json").read_text())
    if mode == "api_token":
        assert credentials_after_failure["api_token"] == "valid-old-token"
    assert credentials_after_failure["replacement_pending"] is True
    assert (state / "config").read_text() == "valid old session"
    assert (state / "credentials.json").stat().st_mode & 0o777 == 0o600

    # A fresh server process must keep reporting false from the protected marker.
    restarted = load_server()
    monkeypatch.setattr(restarted, "STATE_DIR", state)
    monkeypatch.setattr(restarted, "_identity", lambda *_args, **_kwargs: True)
    response = TestClient(restarted.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.json() == {"authenticated": False}

    if mode == "api_token":
        assert module.run_auth_worker(req, identity_checker=lambda *_a, **_k: True) == {}
    else:
        def successful_login(_mode, argv, *, env, password, on_login_url):
            Path(env["ARGOCD_CONFIG"]).write_text("new valid session")
            return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

        assert module.run_auth_worker(
            req, login_runner=successful_login, identity_checker=lambda *_a, **_k: True
        ) == {}

    response = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.status_code == 200
    assert response.json() == {"authenticated": True}
    assert "replacement_pending" not in json.loads((state / "credentials.json").read_text())


def test_credential_promotion_failure_preserves_both_previous_files(tmp_path, monkeypatch):
    module = load_server()
    req = request(tmp_path, "username_password", username="synthetic-user", password="new-secret")
    state = Path(req["state_dir"])
    old_config, old_credentials = seed_previous_auth(state)

    def successful_login(_mode, argv, *, env, password, on_login_url):
        Path(env["ARGOCD_CONFIG"]).write_text("new session")
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    monkeypatch.setattr(module, "_stage_json", lambda *_args: (_ for _ in ()).throw(OSError("synthetic disk failure")))
    assert module.run_auth_worker(req, login_runner=successful_login, identity_checker=lambda *_a, **_k: True) == {}
    assert (state / "config").read_bytes() == old_config
    credentials_after_failure = json.loads((state / "credentials.json").read_text())
    assert credentials_after_failure["api_token"] == "old-token"
    assert credentials_after_failure["replacement_pending"] is True
    assert (state / "credentials.json").stat().st_mode & 0o777 == 0o600


def test_replacement_marker_write_failure_is_hard_error_before_login_and_preserves_old_files(
    tmp_path, monkeypatch
):
    module = load_server()
    req = request(tmp_path, "username_password", username="new-user", password="new-secret")
    state = Path(req["state_dir"])
    old_config, old_credentials = seed_previous_auth(state)
    old_modes = {
        "config": (state / "config").stat().st_mode & 0o777,
        "credentials.json": (state / "credentials.json").stat().st_mode & 0o777,
    }
    login_started = []

    def fail_marker_write(*_args):
        raise OSError("synthetic marker write failure")

    def login(*_args, **_kwargs):
        login_started.append(True)
        raise AssertionError("replacement login must not begin before its pending marker is durable")

    class WorkerInput(io.BytesIO):
        def __init__(self, write_fd):
            super().__init__()
            self.write_fd = write_fd

        def close(self):
            request_json = self.getvalue().decode()
            original_stdin, original_stdout = module.sys.stdin, module.sys.stdout
            worker_output = os.fdopen(self.write_fd, "w", encoding="utf-8")
            try:
                module.sys.stdin = io.StringIO(request_json)
                module.sys.stdout = worker_output
                module._worker_entry()
            finally:
                module.sys.stdin, module.sys.stdout = original_stdin, original_stdout
                worker_output.close()
                super().close()

    class Worker:
        def __init__(self, *_args, **_kwargs):
            read_fd, write_fd = os.pipe()
            self.stdin = WorkerInput(write_fd)
            self.stdout = os.fdopen(read_fd, "rb")

    monkeypatch.setattr(module, "_atomic_json", fail_marker_write)
    monkeypatch.setattr(module.subprocess, "Popen", Worker)
    monkeypatch.setattr(module, "run_interactive_cli", login)
    with pytest.raises(RuntimeError, match="Could not persist pending credential replacement"):
        module.authenticate(req)

    assert login_started == []
    assert (state / "config").read_bytes() == old_config
    assert (state / "credentials.json").read_bytes() == old_credentials
    assert {name: (state / name).stat().st_mode & 0o777 for name in old_modes} == old_modes


@pytest.mark.parametrize(
    ("mode", "inputs"),
    [
        ("api_token", {}),
        ("api_token", {"api_token": ""}),
        ("username_password", {"username": "new-user"}),
        ("username_password", {"username": "new-user", "password": ""}),
    ],
    ids=["missing-token", "empty-token", "missing-password", "empty-password"],
)
def test_missing_or_empty_credentials_marker_write_failure_is_hard_error_and_preserves_old_files(
    tmp_path, monkeypatch, mode, inputs
):
    module = load_server()
    req = request(tmp_path, mode, **inputs)
    state = Path(req["state_dir"])
    old_config, old_credentials = seed_previous_auth(state)
    old_modes = {
        "config": (state / "config").stat().st_mode & 0o777,
        "credentials.json": (state / "credentials.json").stat().st_mode & 0o777,
    }
    worker_started = []

    def fail_marker_write(*_args):
        raise OSError("synthetic marker write failure")

    def launch(_request):
        worker_started.append(True)
        return {"login_url": "https://login.example/device"}

    monkeypatch.setattr(module, "_atomic_json", fail_marker_write)
    monkeypatch.setattr(module, "launch_auth_worker", launch)
    with pytest.raises(RuntimeError, match="Could not persist pending credential replacement"):
        module.authenticate(req)

    assert worker_started == []
    assert (state / "config").read_bytes() == old_config
    assert (state / "credentials.json").read_bytes() == old_credentials
    assert {name: (state / name).stat().st_mode & 0o777 for name in old_modes} == old_modes


def test_credential_replace_failure_rolls_back_promoted_config(tmp_path, monkeypatch):
    module = load_server()
    req = request(tmp_path, "username_password", username="synthetic-user", password="new-secret")
    state = Path(req["state_dir"])
    old_config, old_credentials = seed_previous_auth(state)

    def successful_login(_mode, argv, *, env, password, on_login_url):
        Path(env["ARGOCD_CONFIG"]).write_text("new session")
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    real_replace = module.os.replace
    credential_replacements = 0

    def fail_credentials_replace(source, destination):
        nonlocal credential_replacements
        if Path(destination) == state / "credentials.json":
            credential_replacements += 1
            if credential_replacements == 2:
                raise OSError("synthetic replacement failure")
        return real_replace(source, destination)

    monkeypatch.setattr(module.os, "replace", fail_credentials_replace)
    assert module.run_auth_worker(req, login_runner=successful_login, identity_checker=lambda *_a, **_k: True) == {}
    assert credential_replacements == 2
    assert (state / "config").read_bytes() == old_config
    credentials_after_failure = json.loads((state / "credentials.json").read_text())
    assert credentials_after_failure["api_token"] == "old-token"
    assert credentials_after_failure["replacement_pending"] is True
    assert not list(state.glob(".*backup-*"))


@pytest.mark.parametrize("mode", ["sso", "api_token", "username_password"])
@pytest.mark.parametrize("authenticated", [True, False])
def test_health_route_returns_only_current_binary_identity_without_mutating_state(
    tmp_path, monkeypatch, mode, authenticated
):
    module = load_server()
    state = tmp_path / "state"
    state.mkdir(mode=0o700)
    credentials = {"mode": mode, "server": "argocd.example.test", "connection": {"grpc_web": True}}
    if mode == "api_token":
        credentials["api_token"] = "synthetic-health-secret"
    (state / "credentials.json").write_text(json.dumps(credentials))
    (state / "config").write_text("private synthetic config")
    before = {p.name: (p.read_bytes(), p.stat().st_mode & 0o777, p.stat().st_mtime_ns) for p in state.iterdir()}
    monkeypatch.setattr(module, "STATE_DIR", state)
    set_profile_env(monkeypatch, auth_mode=mode, grpc_web=True)
    monkeypatch.setattr(module, "_identity", lambda *_args, **_kwargs: authenticated)

    response = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.status_code == 200
    assert response.json() == {"authenticated": authenticated}
    assert "synthetic-health-secret" not in response.text
    assert {p.name: (p.read_bytes(), p.stat().st_mode & 0o777, p.stat().st_mtime_ns) for p in state.iterdir()} == before


def test_health_route_is_false_for_missing_or_expired_private_state_without_creating_files(tmp_path, monkeypatch):
    module = load_server()
    state = tmp_path / "missing-state"
    monkeypatch.setattr(module, "STATE_DIR", state)
    set_profile_env(monkeypatch)
    response = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.status_code == 200
    assert response.json() == {"authenticated": False}
    assert not state.exists()

    state.mkdir()
    (state / "credentials.json").write_text('{"mode":"sso","server":"argocd.example.test"}')
    monkeypatch.setattr(module, "_identity", lambda *_args, **_kwargs: False)
    response = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.status_code == 200
    assert response.json() == {"authenticated": False}
    assert list(state.iterdir()) == [state / "credentials.json"]


def test_main_uses_streamable_http_transport_for_health_route(monkeypatch):
    module = load_server()
    observed = {}
    monkeypatch.setattr(module.mcp, "run", lambda **kwargs: observed.update(kwargs))

    module.main()

    assert observed == {
        "transport": "streamable-http",
        "host": "0.0.0.0",
        "port": 8765,
        "path": "/mcp",
    }


@pytest.mark.parametrize(
    ("field", "selected"),
    [
        ("server", "new.argocd.example.test"),
        ("auth_mode", "api_token"),
        ("plaintext", True),
        ("insecure", True),
        ("grpc_web", True),
        ("grpc_web_root_path", "/new-root"),
    ],
)
def test_health_rejects_saved_session_from_changed_profile_scope(tmp_path, monkeypatch, field, selected):
    module = load_server()
    state = tmp_path / "state"
    state.mkdir(mode=0o700)
    old_scope = {
        "server": "argocd.example.test", "auth_mode": "sso", "grpc_web": False,
        "grpc_web_root_path": "", "plaintext": False, "insecure": False,
    }
    (state / "credentials.json").write_text(json.dumps({
        "mode": "sso", "server": old_scope["server"], "connection": {
            "grpc_web": old_scope["grpc_web"], "grpc_web_root_path": old_scope["grpc_web_root_path"],
            "plaintext": old_scope["plaintext"], "insecure": old_scope["insecure"],
        },
    }))
    (state / "config").write_text("old synthetic session")
    monkeypatch.setattr(module, "STATE_DIR", state)
    old_scope[field] = selected
    set_profile_env(monkeypatch, **old_scope)
    identity_calls = []
    monkeypatch.setattr(module, "_identity", lambda *args, **kwargs: identity_calls.append((args, kwargs)) or True)

    response = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.status_code == 200
    assert response.json() == {"authenticated": False}
    assert identity_calls == []


@pytest.mark.parametrize("mode", ["sso", "api_token", "username_password"])
def test_health_uses_matching_current_profile_settings_for_identity_probe(tmp_path, monkeypatch, mode):
    module = load_server()
    state = tmp_path / "state"
    state.mkdir(mode=0o700)
    connection = {"grpc_web": True, "grpc_web_root_path": "/current", "plaintext": False, "insecure": True}
    credentials = {"mode": mode, "server": "current.argocd.example.test", "connection": connection}
    if mode == "api_token":
        credentials["api_token"] = "private-synthetic-token"
    (state / "credentials.json").write_text(json.dumps(credentials))
    (state / "config").write_text("current synthetic session")
    monkeypatch.setattr(module, "STATE_DIR", state)
    set_profile_env(
        monkeypatch, server="current.argocd.example.test", auth_mode=mode, grpc_web=True,
        grpc_web_root_path="/current", insecure=True, skip_test_tls=True,
    )
    observed = {}

    def run_cli(argv, *, env):
        observed["argv"] = argv
        observed["env"] = env
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    monkeypatch.setattr(module, "run_cli", run_cli)
    response = TestClient(module.mcp.http_app(path="/mcp", transport="streamable-http")).get("/health")
    assert response.status_code == 200
    assert response.json() == {"authenticated": True}
    assert observed["argv"] == [
        "/usr/local/bin/argocd", "account", "get-user-info", "--server", "current.argocd.example.test",
        "--grpc-web", "--grpc-web-root-path", "/current", "--insecure",
    ]
    assert "--skip-test-tls" not in observed["argv"]
    assert observed["env"]["ARGOCD_CONFIG"] == str(state / "config")
    if mode == "api_token":
        assert observed["env"]["ARGOCD_AUTH_TOKEN"] == "private-synthetic-token"
    else:
        assert "ARGOCD_AUTH_TOKEN" not in observed["env"]


@pytest.mark.parametrize(
    ("field", "selected", "request_value"),
    [
        ("server", "selected.argocd.example.test", "old.argocd.example.test"),
        ("auth_mode", "api_token", "sso"),
        ("plaintext", True, False),
        ("insecure", True, False),
        ("skip_test_tls", True, False),
        ("grpc_web", True, False),
        ("grpc_web_root_path", "/current", "/old"),
    ],
)
def test_authenticate_rejects_inputs_that_differ_from_current_container_profile(
    tmp_path, monkeypatch, field, selected, request_value
):
    module = load_server()
    selected_profile = {
        "server": "selected.argocd.example.test", "auth_mode": "sso", "grpc_web": False,
        "grpc_web_root_path": "", "plaintext": False, "insecure": False, "skip_test_tls": False,
    }
    selected_profile[field] = selected
    set_profile_env(monkeypatch, **selected_profile)
    monkeypatch.setattr(module, "launch_auth_worker", lambda *_args: pytest.fail("mismatched profile started login"))
    request_profile = dict(selected_profile)
    request_profile[field] = request_value
    request_value = {
        "protocol_version": 1, "action": "authenticate", "state_dir": str(tmp_path / "state"),
        "inputs": {**request_profile, "callback_port": 8085},
    }
    assert module.authenticate(request_value) == {
        "diagnostic": "Authentication request does not match the selected container profile."
    }


@pytest.mark.parametrize(
    ("skip_test_tls", "insecure"),
    [(False, False), (True, False), (False, True), (True, True)],
)
def test_login_applies_skip_test_tls_independently_from_insecure(tmp_path, monkeypatch, skip_test_tls, insecure):
    module = load_server()
    req = request(
        tmp_path, "sso", callback_port=8085, skip_test_tls=skip_test_tls, insecure=insecure
    )
    set_profile_env(monkeypatch, skip_test_tls=skip_test_tls, insecure=insecure)
    observed = {}

    def login(mode, argv, *, env, password, on_login_url):
        observed["argv"] = argv
        Path(env["ARGOCD_CONFIG"]).write_text("synthetic login config")
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    assert module.run_auth_worker(req, login_runner=login, identity_checker=lambda *_a, **_k: True) == {}
    assert ("--skip-test-tls" in observed["argv"]) is skip_test_tls
    assert ("--insecure" in observed["argv"]) is insecure


def test_identity_probe_never_passes_login_only_skip_test_tls_flag(tmp_path, monkeypatch):
    module = load_server()
    set_profile_env(monkeypatch, skip_test_tls=True)
    observed = {}

    def run_cli(argv, *, env):
        observed["argv"] = argv
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    monkeypatch.setattr(module, "run_cli", run_cli)
    assert module._identity(
        tmp_path, "argocd.example.test", {"server": "argocd.example.test", "auth_mode": "sso", "skip_test_tls": True}
    ) is True
    assert "--skip-test-tls" not in observed["argv"]


def test_auth_worker_ignores_inherited_argocd_overrides_for_login_and_identity(tmp_path, monkeypatch):
    module = load_server()
    req = request(tmp_path, "username_password", username="synthetic-user", password="synthetic-password")
    monkeypatch.setenv("ARGOCD_AUTH_TOKEN", "ambient-token-must-not-authenticate")
    monkeypatch.setenv("ARGOCD_OPTS", "--server ambient.example.test")
    monkeypatch.setenv("ARGOCD_SERVER", "ambient.example.test")
    observed = {}

    def login(_mode, argv, *, env, password, on_login_url):
        observed["login_env"] = env
        Path(env["ARGOCD_CONFIG"]).write_text("new login session")
        return subprocess.CompletedProcess(argv, 0, stdout=b"", stderr=b"")

    def identity(_state, _server, _inputs, *, config_path=None, token=None):
        observed["identity"] = (config_path, token)
        return False

    assert module.run_auth_worker(req, login_runner=login, identity_checker=identity) == {}
    assert "ARGOCD_AUTH_TOKEN" not in observed["login_env"]
    assert "ARGOCD_OPTS" not in observed["login_env"]
    assert "ARGOCD_SERVER" not in observed["login_env"]
    assert observed["identity"][1] is None
    assert not (Path(req["state_dir"]) / "credentials.json").exists()


def test_authenticate_reuses_active_worker_without_spawning_another(tmp_path, monkeypatch):
    module = load_server()
    req = request(tmp_path, callback_port=8085)
    lock_path = Path(req["state_dir"]) / ".auth-worker.lock"
    lock_path.parent.mkdir(mode=0o700, parents=True)
    lock_path.touch(mode=0o600)
    held = open(lock_path, "r+")
    module.fcntl.flock(held.fileno(), module.fcntl.LOCK_EX | module.fcntl.LOCK_NB)
    monkeypatch.setattr(module.subprocess, "Popen", lambda *_a, **_k: pytest.fail("duplicate auth worker spawned"))
    try:
        assert module.authenticate(req) == {"diagnostic": "Authentication is already running for this profile."}
    finally:
        held.close()


def test_worker_handshake_timeout_terminates_only_its_owned_process_group(tmp_path, monkeypatch):
    module = load_server()
    req = request(tmp_path, callback_port=8085)
    state = Path(req["state_dir"])
    state.mkdir(mode=0o700)
    terminated = []

    class SilentWorker:
        pid = 424242
        stdin = io.BytesIO()
        stdout = io.BytesIO()

        def poll(self):
            return None

        def terminate(self):
            terminated.append(self.pid)

        def wait(self, timeout=None):
            return 0

    worker = SilentWorker()
    killed_groups = []
    monkeypatch.setattr(module.subprocess, "Popen", lambda *_a, **_k: worker)
    monkeypatch.setattr(module.select, "select", lambda *_a, **_k: ([], [], []))
    monkeypatch.setattr(module.os, "killpg", lambda pid, sig: killed_groups.append((pid, sig)))
    monkeypatch.setattr(module, "WORKER_HANDSHAKE_SECONDS", 0)

    assert module.launch_auth_worker(req) == {}
    assert killed_groups == [
        (worker.pid, module.signal.SIGTERM),
        (worker.pid, module.signal.SIGKILL),
    ]
    assert terminated == []
    assert not list(state.glob("*.json"))


def test_worker_timeout_kills_term_ignoring_descendants_before_releasing_lock(tmp_path, monkeypatch):
    module = load_server()
    req = request(tmp_path, callback_port=8085)
    state = Path(req["state_dir"])
    state.mkdir(mode=0o700)
    marker = state / "config"
    child_pid_file = tmp_path / "child-pid"
    child_ready_file = tmp_path / "child-ready"
    child_script = (
        "import signal, time; signal.signal(signal.SIGTERM, signal.SIG_IGN); "
        f"open({str(child_ready_file)!r}, 'w').write('ready'); "
        f"time.sleep(0.5); open({str(marker)!r}, 'w').write('stale')"
    )
    parent_script = (
        "import signal, subprocess, sys, time\n"
        f"child = subprocess.Popen([sys.executable, '-c', {child_script!r}])\n"
        f"deadline = time.monotonic() + 2\nwhile time.monotonic() < deadline and not __import__('os').path.exists({str(child_ready_file)!r}): time.sleep(0.01)\n"
        f"open({str(child_pid_file)!r}, 'w').write(str(child.pid))\n"
        "time.sleep(30)\n"
    )
    real_popen = subprocess.Popen

    def launch_owned_process(_argv, **kwargs):
        return real_popen([sys.executable, "-c", parent_script], **kwargs)

    owned_processes = []

    def launch_and_remember(argv, **kwargs):
        process = launch_owned_process(argv, **kwargs)
        owned_processes.append(process)
        return process

    def wait_for_child_then_timeout(*_a, **_k):
        deadline = time.monotonic() + 2
        while time.monotonic() < deadline and not child_pid_file.exists():
            time.sleep(0.01)
        assert child_pid_file.exists(), "worker failed to start its CLI descendant"
        return ([], [], [])

    monkeypatch.setattr(module.subprocess, "Popen", launch_and_remember)
    monkeypatch.setattr(module.select, "select", wait_for_child_then_timeout)
    monkeypatch.setattr(module, "WORKER_HANDSHAKE_SECONDS", 0)

    try:
        assert module.launch_auth_worker(req) == {}

        lock_path = state / ".auth-worker.lock"
        lock_fd = os.open(lock_path, os.O_CREAT | os.O_RDWR, 0o600)
        try:
            module.fcntl.flock(lock_fd, module.fcntl.LOCK_EX | module.fcntl.LOCK_NB)
        finally:
            os.close(lock_fd)

        assert child_pid_file.exists()
        child_pid = int(child_pid_file.read_text())
        time.sleep(0.6)
        try:
            os.kill(child_pid, 0)
        except ProcessLookupError:
            child_alive = False
        else:
            child_alive = True
        assert not child_alive, f"timed-out worker descendant {child_pid} is still alive"
        assert not marker.exists(), "a stale worker descendant promoted state after timeout"
    finally:
        if owned_processes:
            try:
                os.killpg(owned_processes[0].pid, module.signal.SIGKILL)
            except ProcessLookupError:
                pass
            owned_processes[0].wait()


def test_missing_and_invalid_auth_inputs_fail_without_mode_fallback(tmp_path, monkeypatch):
    module = load_server()
    monkeypatch.setattr(module, "launch_auth_worker", lambda *_a: pytest.fail("invalid inputs must not start a worker"))
    missing_token = request(tmp_path / "missing", "api_token")
    assert module.authenticate(missing_token) == {"diagnostic": "Enter the API token and try again."}
    unknown_mode = request(tmp_path / "invalid", "unsupported")
    assert module.authenticate(unknown_mode) == {"diagnostic": "The saved authentication mode is unsupported."}
    assert not (Path(unknown_mode["state_dir"]) / "credentials.json").exists()


def test_auth_output_redacts_secrets_and_never_puts_password_in_argv(tmp_path):
    module = load_server()
    password = "synthetic-password-secret"
    req = request(tmp_path, "username_password", username="synthetic-user", password=password)
    observed = {}

    def rejected(mode, argv, *, env, password, on_login_url):
        observed["argv"] = argv
        observed["password"] = password
        observed["raw_output"] = f"CLI debug echoed {password}"
        return subprocess.CompletedProcess(argv, 1, stdout=observed["raw_output"].encode(), stderr=b"")

    result = module.run_auth_worker(req, login_runner=rejected, identity_checker=lambda *_a, **_k: False)
    assert result == {}
    assert password not in " ".join(observed["argv"])
    assert password not in json.dumps(result)
    assert not (Path(req["state_dir"]) / "credentials.json").exists()
    assert not list(Path(req["state_dir"]).glob("*.log"))


def test_password_is_sent_only_after_prompt_through_tty(tmp_path, monkeypatch):
    module = load_server()
    real_popen = subprocess.Popen
    password = "synthetic-pty-password"
    launched = {}

    def fake_cli_process(_argv, **kwargs):
        script = "import getpass; assert getpass.getpass('Password: ') == '" + password + "'; print('authenticated')"
        launched["kwargs"] = kwargs
        return real_popen([sys.executable, "-c", script], **kwargs)

    monkeypatch.setattr(module.subprocess, "Popen", fake_cli_process)
    urls = []
    result = module.run_interactive_cli(
        "username_password", ["/usr/local/bin/argocd", "login", "--username", "synthetic-user"],
        env=os.environ.copy(), password=password, on_login_url=urls.append,
    )
    assert result.returncode == 0
    assert b"authenticated" in result.stdout
    assert password.encode() not in result.stdout
    assert launched["kwargs"]["start_new_session"] is False
    assert urls == [""]


@pytest.mark.parametrize("action", ["authentication_status", "cancel_authentication"])
def test_authentication_status_and_cancel_are_not_aact_actions(action, monkeypatch, capsys):
    module = load_server()
    monkeypatch.setattr(module.sys, "stdin", io.StringIO('{"protocol_version":1}'))
    module._run_action(action)
    assert json.loads(capsys.readouterr().out) == {"diagnostic": "Unsupported action."}
