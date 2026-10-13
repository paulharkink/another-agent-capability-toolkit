"""Argo CD MCP entrypoint and explicit container-owned authentication."""

from __future__ import annotations

import json
import os
from pathlib import Path
import pty
import re
import select
import subprocess
import sys
import tempfile
import time
from typing import Any, Callable

from fastmcp import FastMCP

mcp = FastMCP("argocd")
CLI = "/usr/local/bin/argocd"
URI_RE = re.compile(r"https?://[^\s\"'<>]+")
PASSWORD_PROMPT_RE = re.compile(rb"password\s*:", re.I)
MAX_OUTPUT = 16_384
WORKER_HANDSHAKE_SECONDS = 15


def _state_dir(request: dict[str, Any]) -> Path:
    value = request.get("state_dir")
    if not isinstance(value, str) or not value:
        raise ValueError("Private authentication state directory is required.")
    state = Path(value)
    state.mkdir(mode=0o700, parents=True, exist_ok=True)
    os.chmod(state, 0o700)
    return state


def _atomic_json(path: Path, value: dict[str, Any]) -> None:
    fd, name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    temporary = Path(name)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, separators=(",", ":"))
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temporary, path)
        os.chmod(path, 0o600)
    finally:
        temporary.unlink(missing_ok=True)


def _read_json(path: Path) -> dict[str, Any] | None:
    try:
        value = json.loads(path.read_text(encoding="utf-8"))
    except (OSError, ValueError):
        return None
    return value if isinstance(value, dict) else None


def _auth_inputs(request: dict[str, Any], *, require_credentials: bool = True) -> tuple[Path, str, dict[str, Any], str]:
    state = _state_dir(request)
    inputs = request.get("inputs")
    if not isinstance(inputs, dict):
        raise ValueError("Saved profile inputs are required.")
    server = inputs.get("server")
    if not isinstance(server, str) or not server.strip() or any(c.isspace() for c in server):
        raise ValueError("The saved Argo CD server is invalid.")
    mode = inputs.get("auth_mode")
    if mode not in {"sso", "api_token", "username_password"}:
        raise ValueError("The saved authentication mode is unsupported.")
    if mode == "sso" and require_credentials:
        port = inputs.get("callback_port", 8085)
        if not isinstance(port, int) or isinstance(port, bool) or not 1 <= port <= 65535:
            raise ValueError("The saved SSO callback port is invalid.")
    elif mode == "api_token" and require_credentials:
        if not isinstance(inputs.get("api_token"), str) or not inputs["api_token"]:
            raise ValueError("Enter the API token and try again.")
    elif mode == "username_password" and require_credentials and (not isinstance(inputs.get("username"), str) or not inputs["username"]
          or not isinstance(inputs.get("password"), str) or not inputs["password"]):
        raise ValueError("Enter the username and password and try again.")
    return state, server, inputs, mode


def _connection_args(inputs: dict[str, Any]) -> list[str]:
    args: list[str] = []
    if inputs.get("grpc_web") is True:
        args.append("--grpc-web")
    root_path = inputs.get("grpc_web_root_path")
    if isinstance(root_path, str) and root_path:
        args.extend(("--grpc-web-root-path", root_path))
    if inputs.get("plaintext") is True:
        args.append("--plaintext")
    if inputs.get("insecure") is True:
        args.append("--insecure")
    return args


def run_cli(argv: list[str], *, env: dict[str, str]) -> subprocess.CompletedProcess:
    try:
        return subprocess.run(argv, env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                              timeout=30, check=False)
    except (OSError, subprocess.TimeoutExpired):
        return subprocess.CompletedProcess(argv, 127, stdout=b"", stderr=b"")


def _identity(state: Path, server: str, inputs: dict[str, Any], *,
              config_path: Path | None = None, token: str | None = None) -> bool:
    env = os.environ.copy()
    env["ARGOCD_CONFIG"] = str(config_path or state / "config")
    if token:
        env["ARGOCD_AUTH_TOKEN"] = token
    result = run_cli([CLI, "account", "get-user-info", "--server", server, *_connection_args(inputs)], env=env)
    return result.returncode == 0


def run_interactive_cli(mode: str, argv: list[str], *, env: dict[str, str], password: str | None,
                        on_login_url: Callable[[str], None]) -> subprocess.CompletedProcess:
    """Run CLI login in a private PTY so password prompts never require argv/stdin secrets."""
    master, slave = pty.openpty()
    try:
        process = subprocess.Popen(argv, env=env, stdin=slave, stdout=slave, stderr=slave,
                                   start_new_session=False, close_fds=True)
    except OSError:
        os.close(master)
        os.close(slave)
        on_login_url("")
        return subprocess.CompletedProcess(argv, 127, stdout=b"", stderr=b"")
    os.close(slave)
    output = bytearray()
    password_sent = password is None
    url_sent = False
    deadline = time.monotonic() + 300
    try:
        while time.monotonic() < deadline and process.poll() is None:
            readable, _, _ = select.select([master], [], [], 0.2)
            if readable:
                try:
                    chunk = os.read(master, 4096)
                except OSError:
                    break
                output.extend(chunk)
                if len(output) > MAX_OUTPUT:
                    output[:] = output[-MAX_OUTPUT:]
                if not password_sent and PASSWORD_PROMPT_RE.search(output):
                    os.write(master, password.encode() + b"\n")
                    password_sent = True
                if mode == "sso" and not url_sent:
                    match = URI_RE.search(output.decode("utf-8", "replace"))
                    if match:
                        url_sent = True
                        on_login_url(match.group(0).rstrip(".,)"))
        if process.poll() is None:
            process.terminate()
            process.wait(timeout=2)
        if not url_sent:
            on_login_url("")
        return subprocess.CompletedProcess(argv, process.returncode if process.returncode is not None else 124,
                                           stdout=bytes(output), stderr=b"")
    finally:
        os.close(master)


def _candidate_config(state: Path) -> Path:
    fd, name = tempfile.mkstemp(prefix=".argocd-config-", dir=state)
    os.close(fd)
    config = Path(name)
    os.chmod(config, 0o600)
    return config


def run_auth_worker(request: dict[str, Any], *,
                    login_runner: Callable[..., subprocess.CompletedProcess] | None = None,
                    identity_checker: Callable[..., bool] | None = None,
                    emit: Callable[[dict[str, str]], None] | None = None) -> dict[str, str]:
    """Complete one login in the detached container worker and atomically save success."""
    state, server, inputs, mode = _auth_inputs(request)
    event: dict[str, str] = {}
    emitted = False

    def publish(url: str = "") -> None:
        nonlocal emitted
        if emitted:
            return
        if url:
            event["login_url"] = url
        if emit is not None:
            emit(event.copy())
        emitted = True

    check_identity = identity_checker or _identity
    if mode == "api_token":
        publish()
        token = inputs["api_token"]
        if check_identity(state, server, inputs, token=token):
            _atomic_json(state / "credentials.json", {"mode": mode, "api_token": token, "server": server})
        return event

    config = _candidate_config(state)
    env = os.environ.copy()
    env["ARGOCD_CONFIG"] = str(config)
    if mode == "sso":
        argv = [CLI, "login", "--server", server, "--sso", "--sso-port",
                str(inputs.get("callback_port", 8085)), *_connection_args(inputs)]
        password = None
    else:
        # Username is a CLI-supported selector; the password itself is delivered only via PTY.
        argv = [CLI, "login", "--server", server, "--username", inputs["username"], *_connection_args(inputs)]
        password = inputs["password"]
        publish()
    runner = login_runner or run_interactive_cli
    try:
        result = runner(mode, argv, env=env, password=password, on_login_url=publish)
        if not emitted:
            publish()
        if result.returncode != 0 or not check_identity(state, server, inputs, config_path=config):
            return event
        os.replace(config, state / "config")
        os.chmod(state / "config", 0o600)
        _atomic_json(state / "credentials.json", {"mode": mode, "server": server})
        return event
    finally:
        config.unlink(missing_ok=True)


def launch_auth_worker(request: dict[str, Any]) -> dict[str, str]:
    """Start an independent worker; return its one-time URL event without waiting for login."""
    process = subprocess.Popen([sys.executable, str(Path(__file__).resolve()), "auth-worker"],
                              stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
                              start_new_session=True, close_fds=True)
    assert process.stdin is not None and process.stdout is not None
    process.stdin.write(json.dumps(request, separators=(",", ":")).encode() + b"\n")
    process.stdin.close()
    readable, _, _ = select.select([process.stdout], [], [], WORKER_HANDSHAKE_SECONDS)
    if not readable:
        process.stdout.close()
        return {}
    line = process.stdout.readline(4096)
    process.stdout.close()
    try:
        event = json.loads(line) if line else {}
    except ValueError:
        event = {}
    return {"login_url": event["login_url"]} if isinstance(event, dict) and isinstance(event.get("login_url"), str) else {}


def authenticate(request: dict[str, Any]) -> dict[str, Any]:
    try:
        _auth_inputs(request)
        return launch_auth_worker(request)
    except (ValueError, OSError) as error:
        return {"diagnostic": str(error)}


def is_authenticated(request: dict[str, Any], *,
                     identity_checker: Callable[..., bool] | None = None) -> bool:
    """Return a binary read-only login check for internal MCP/server use."""
    try:
        state, server, inputs, mode = _auth_inputs(request, require_credentials=False)
    except (ValueError, OSError):
        return False
    credentials = _read_json(state / "credentials.json")
    if not credentials or credentials.get("mode") != mode or credentials.get("server") != server:
        return False
    token = credentials.get("api_token") if mode == "api_token" else None
    check_identity = identity_checker or _identity
    return bool(check_identity(state, server, inputs, token=token))


def _read_worker_request() -> dict[str, Any]:
    value = json.load(sys.stdin)
    if not isinstance(value, dict) or value.get("protocol_version") != 1:
        raise ValueError("Invalid authentication worker request.")
    return value


def _worker_entry() -> None:
    try:
        request = _read_worker_request()
        emitted = False

        def send(event: dict[str, str]) -> None:
            nonlocal emitted
            if emitted:
                return
            try:
                sys.stdout.write(json.dumps(event, separators=(",", ":")) + "\n")
                sys.stdout.flush()
            except BrokenPipeError:
                pass
            emitted = True

        run_auth_worker(request, emit=send)
        send({})
    except Exception:
        try:
            sys.stdout.write("{}\n")
            sys.stdout.flush()
        except BrokenPipeError:
            pass


def _run_action(action: str) -> None:
    try:
        request = json.load(sys.stdin)
        if not isinstance(request, dict) or request.get("protocol_version") != 1:
            raise ValueError("Unsupported action request.")
        result = authenticate(request) if action == "authenticate" else {"diagnostic": "Unsupported action."}
    except (TypeError, ValueError, json.JSONDecodeError):
        result = {"diagnostic": "The authentication action request is invalid."}
    print(json.dumps(result, separators=(",", ":")))


def main() -> None:
    if len(sys.argv) > 1 and sys.argv[1] == "auth-worker":
        _worker_entry()
        return
    if len(sys.argv) > 1 and sys.argv[1] == "action":
        action = sys.argv[2] if len(sys.argv) > 2 else "unknown"
        _run_action(action)
        return
    mcp.run(transport="http", host="0.0.0.0", port=8765, path="/mcp")


if __name__ == "__main__":
    main()
