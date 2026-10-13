"""Argo CD MCP entrypoint and explicit container-owned authentication."""

from __future__ import annotations

import fcntl
import json
import os
from pathlib import Path
import pty
import re
import select
import signal
import subprocess
import sys
import tempfile
import time
from typing import Any, Callable

from fastmcp import FastMCP
from starlette.requests import Request
from starlette.responses import JSONResponse

mcp = FastMCP("argocd")
CLI = "/usr/local/bin/argocd"
STATE_DIR = Path(os.environ.get("AACT_AUTH_STATE_DIR", "/state"))
URI_RE = re.compile(r"https?://[^\s\"'<>]+")
PASSWORD_PROMPT_RE = re.compile(rb"password\s*:", re.I)
MAX_OUTPUT = 16_384
WORKER_HANDSHAKE_SECONDS = 15
PROFILE_ENV = {
    "server": "AACT_ARGOCD_SERVER",
    "auth_mode": "AACT_ARGOCD_AUTH_MODE",
    "grpc_web": "AACT_ARGOCD_GRPC_WEB",
    "grpc_web_root_path": "AACT_ARGOCD_GRPC_WEB_ROOT_PATH",
    "plaintext": "AACT_ARGOCD_PLAINTEXT",
    "insecure": "AACT_ARGOCD_INSECURE",
    "skip_test_tls": "AACT_ARGOCD_SKIP_TEST_TLS",
}


def _profile_from_environment() -> dict[str, Any] | None:
    present = [name in os.environ for name in PROFILE_ENV.values()]
    if not any(present):
        return None
    if not all(present):
        raise ValueError("The selected container profile configuration is incomplete.")
    profile: dict[str, Any] = {}
    for key, name in PROFILE_ENV.items():
        value = os.environ[name]
        if key in {"grpc_web", "plaintext", "insecure", "skip_test_tls"}:
            if value not in {"true", "false"}:
                raise ValueError("The selected container profile configuration is invalid.")
            profile[key] = value == "true"
        else:
            profile[key] = value
    server, mode = profile["server"], profile["auth_mode"]
    if (not isinstance(server, str) or not server.strip() or any(c.isspace() for c in server)
            or mode not in {"sso", "api_token", "username_password"}):
        raise ValueError("The selected container profile configuration is invalid.")
    return profile


def _connection_scope(inputs: dict[str, Any]) -> dict[str, Any]:
    root_path = inputs.get("grpc_web_root_path", "")
    return {
        "grpc_web": inputs.get("grpc_web") is True,
        "grpc_web_root_path": root_path if isinstance(root_path, str) else "",
        "plaintext": inputs.get("plaintext") is True,
        "insecure": inputs.get("insecure") is True,
    }


def _state_dir(request: dict[str, Any], *, create: bool = True) -> Path:
    value = request.get("state_dir")
    if not isinstance(value, str) or not value:
        raise ValueError("Private authentication state directory is required.")
    state = Path(value)
    if create:
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


def _auth_inputs(request: dict[str, Any], *, require_credentials: bool = True,
                 create_state: bool = True) -> tuple[Path, str, dict[str, Any], str]:
    state = _state_dir(request, create=create_state)
    inputs = request.get("inputs")
    if not isinstance(inputs, dict):
        raise ValueError("Saved profile inputs are required.")
    server = inputs.get("server")
    if not isinstance(server, str) or not server.strip() or any(c.isspace() for c in server):
        raise ValueError("The saved Argo CD server is invalid.")
    mode = inputs.get("auth_mode")
    if mode not in {"sso", "api_token", "username_password"}:
        raise ValueError("The saved authentication mode is unsupported.")
    selected = _profile_from_environment()
    if selected is not None and (
        server != selected["server"] or mode != selected["auth_mode"]
        or _connection_scope(inputs) != _connection_scope(selected)
        or (inputs.get("skip_test_tls") is True) != selected["skip_test_tls"]
    ):
        raise ValueError("Authentication request does not match the selected container profile.")
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
    env = _cli_environment(config_path or state / "config")
    if token:
        env["ARGOCD_AUTH_TOKEN"] = token
    result = run_cli([CLI, "account", "get-user-info", "--server", server, *_connection_args(inputs)], env=env)
    return result.returncode == 0


def _cli_environment(config_path: Path, *, token: str | None = None) -> dict[str, str]:
    """Build a CLI environment that cannot inherit ambient Argo CD auth/config overrides."""
    env = {key: value for key, value in os.environ.items() if not key.startswith("ARGOCD_")}
    env["ARGOCD_CONFIG"] = str(config_path)
    if token:
        env["ARGOCD_AUTH_TOKEN"] = token
    return env


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


def _credential_record(mode: str, server: str, inputs: dict[str, Any]) -> dict[str, Any]:
    record: dict[str, Any] = {
        "mode": mode,
        "server": server,
        "connection": _connection_scope(inputs),
    }
    if mode == "api_token":
        record["api_token"] = inputs["api_token"]
    return record


def _stage_json(path: Path, value: dict[str, Any]) -> Path:
    fd, name = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    staged = Path(name)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "w", encoding="utf-8") as stream:
            json.dump(value, stream, separators=(",", ":"))
            stream.flush()
            os.fsync(stream.fileno())
        return staged
    except Exception:
        staged.unlink(missing_ok=True)
        raise


def _backup_link(path: Path) -> Path | None:
    if not path.exists():
        return None
    fd, name = tempfile.mkstemp(prefix=f".{path.name}.backup-", dir=path.parent)
    os.close(fd)
    backup = Path(name)
    backup.unlink()
    os.link(path, backup)
    return backup


def _promote_login_state(config: Path, state: Path, credentials: dict[str, Any]) -> None:
    """Promote config and matching metadata, rolling back handled failures."""
    config_path = state / "config"
    credentials_path = state / "credentials.json"
    staged_credentials = _stage_json(credentials_path, credentials)
    config_backup: Path | None = None
    credentials_backup: Path | None = None
    config_promoted = False
    credentials_promoted = False
    try:
        config_backup = _backup_link(config_path)
        credentials_backup = _backup_link(credentials_path)
        os.replace(config, config_path)
        config_promoted = True
        os.replace(staged_credentials, credentials_path)
        credentials_promoted = True
        os.chmod(config_path, 0o600)
    except OSError:
        if config_promoted:
            if config_backup is None:
                config_path.unlink(missing_ok=True)
            else:
                os.replace(config_backup, config_path)
                config_backup = None
        if credentials_promoted:
            if credentials_backup is None:
                credentials_path.unlink(missing_ok=True)
            else:
                os.replace(credentials_backup, credentials_path)
                credentials_backup = None
        raise
    finally:
        staged_credentials.unlink(missing_ok=True)
        if config_backup is not None:
            config_backup.unlink(missing_ok=True)
        if credentials_backup is not None:
            credentials_backup.unlink(missing_ok=True)


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
            _atomic_json(state / "credentials.json", _credential_record(mode, server, inputs))
        return event

    config = _candidate_config(state)
    env = _cli_environment(config)
    if mode == "sso":
        argv = [CLI, "login", "--server", server, "--sso", "--sso-port",
                str(inputs.get("callback_port", 8085)), *_connection_args(inputs)]
        password = None
    else:
        # Username is a CLI-supported selector; the password itself is delivered only via PTY.
        argv = [CLI, "login", "--server", server, "--username", inputs["username"], *_connection_args(inputs)]
        password = inputs["password"]
        publish()
    if inputs.get("skip_test_tls") is True:
        argv.append("--skip-test-tls")
    runner = login_runner or run_interactive_cli
    try:
        result = runner(mode, argv, env=env, password=password, on_login_url=publish)
        if not emitted:
            publish()
        if result.returncode != 0 or not check_identity(state, server, inputs, config_path=config):
            return event
        _promote_login_state(config, state, _credential_record(mode, server, inputs))
        return event
    except OSError:
        return event
    finally:
        config.unlink(missing_ok=True)


def _stop_owned_worker(process: subprocess.Popen) -> None:
    """Stop a timed-out worker and its CLI child in the process group it created."""
    try:
        os.killpg(process.pid, signal.SIGTERM)
    except ProcessLookupError:
        process.wait()
        return
    try:
        process.wait(timeout=2)
    except subprocess.TimeoutExpired:
        pass
    # The worker may exit on SIGTERM while an owned CLI descendant ignores it.
    # Escalate against the original group even after reaping the group leader.
    try:
        os.killpg(process.pid, signal.SIGKILL)
    except ProcessLookupError:
        pass
    process.wait()


def launch_auth_worker(request: dict[str, Any]) -> dict[str, str]:
    """Start an independent worker; return its one-time URL event without waiting for login."""
    state = _state_dir(request)
    lock_path = state / ".auth-worker.lock"
    lock_fd = os.open(lock_path, os.O_CREAT | os.O_RDWR, 0o600)
    os.chmod(lock_path, 0o600)
    process = None
    worker_accepted = False
    try:
        try:
            fcntl.flock(lock_fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
        except BlockingIOError:
            return {"diagnostic": "Authentication is already running for this profile."}
        process = subprocess.Popen(
            [sys.executable, str(Path(__file__).resolve()), "auth-worker", str(lock_fd)],
            stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.DEVNULL,
            start_new_session=True, close_fds=True, pass_fds=(lock_fd,),
        )
        assert process.stdin is not None and process.stdout is not None
        process.stdin.write(json.dumps(request, separators=(",", ":")).encode() + b"\n")
        process.stdin.close()
        readable, _, _ = select.select([process.stdout], [], [], WORKER_HANDSHAKE_SECONDS)
        if not readable:
            process.stdout.close()
            return {}
        worker_accepted = True
        line = process.stdout.readline(4096)
        process.stdout.close()
        try:
            event = json.loads(line) if line else {}
        except ValueError:
            event = {}
        return {"login_url": event["login_url"]} if isinstance(event, dict) and isinstance(event.get("login_url"), str) else {}
    finally:
        if process is not None and not worker_accepted:
            _stop_owned_worker(process)
        os.close(lock_fd)


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
        state, server, inputs, mode = _auth_inputs(request, require_credentials=False, create_state=False)
    except (ValueError, OSError):
        return False
    credentials = _read_json(state / "credentials.json")
    if not credentials or credentials.get("mode") != mode or credentials.get("server") != server:
        return False
    saved_connection = credentials.get("connection", {})
    if not isinstance(saved_connection, dict) or _connection_scope(saved_connection) != _connection_scope(inputs):
        return False
    token = credentials.get("api_token") if mode == "api_token" else None
    check_identity = identity_checker or _identity
    return bool(check_identity(state, server, inputs, token=token))


def _current_profile_request() -> dict[str, Any] | None:
    try:
        profile = _profile_from_environment()
    except ValueError:
        return None
    if profile is None:
        return None
    credentials = _read_json(STATE_DIR / "credentials.json")
    if not credentials:
        return None
    inputs: dict[str, Any] = profile
    return {"state_dir": str(STATE_DIR), "inputs": inputs}


@mcp.custom_route("/health", methods=["GET"], include_in_schema=False)
async def health(_: Request) -> JSONResponse:
    """Binary auth signal; MCP initialization/readiness remains a separate check."""
    authenticated = False
    try:
        request = _current_profile_request()
        authenticated = is_authenticated(request) if request is not None else False
    except Exception:
        authenticated = False
    return JSONResponse({"authenticated": authenticated}, status_code=200)


def _read_worker_request() -> dict[str, Any]:
    value = json.load(sys.stdin)
    if not isinstance(value, dict) or value.get("protocol_version") != 1:
        raise ValueError("Invalid authentication worker request.")
    return value


def _worker_entry(lock_fd: int | None = None) -> None:
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
    finally:
        if lock_fd is not None:
            os.close(lock_fd)


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
        lock_fd = int(sys.argv[2]) if len(sys.argv) > 2 else None
        _worker_entry(lock_fd)
        return
    if len(sys.argv) > 1 and sys.argv[1] == "action":
        action = sys.argv[2] if len(sys.argv) > 2 else "unknown"
        _run_action(action)
        return
    mcp.run(transport="http", host="0.0.0.0", port=8765, path="/mcp")


if __name__ == "__main__":
    main()
