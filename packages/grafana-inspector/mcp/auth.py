"""Container action for target-scoped Grafana authentication.

Read one ActionRequest from stdin and write {"auth_required": bool} to stdout.
The native helper mounts selected configuration at /config and state at /state.
No interactive sign-in is performed. Secrets are never printed.
"""
from __future__ import annotations
import json
import os
from pathlib import Path
import sys
import tempfile
import urllib.request
from config import load_target


def _persist(path: Path, auth: dict) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=".auth-", suffix=".tmp", dir=path.parent)
    try:
        with os.fdopen(fd, "w") as stream:
            json.dump(auth, stream)
        os.replace(temporary, path)
        path.chmod(0o600)
    finally:
        Path(temporary).unlink(missing_ok=True)


def prepare_auth(request: dict) -> dict:
    if request.get("protocol_version") != 1:
        raise ValueError("Unsupported action protocol version.")
    target = request.get("target", {})
    if target.get("path"):
        load_target(Path(target["path"]), target.get("environment", "lab"), target.get("name", "target"), Path(request.get("state_dir", "/state")))
    cfg = request.get("target", {}).get("raw", {}).get("grafana", {})
    inputs = request.get("inputs", {})
    path = Path(request.get("state_dir", "/state")) / "auth.json"
    try:
        old = json.loads(path.read_text())
        if not isinstance(old, dict):
            old = {}
    except (OSError, ValueError):
        old = {}
    auth = dict(old)
    mode = inputs.get("auth_mode") or auth.get("auth_mode") or cfg.get("auth_mode")
    if mode not in {"api_token", "session_cookie"}:
        raise ValueError("Grafana auth_mode must be api_token or session_cookie.")
    auth["auth_mode"] = mode
    for name, key in (("token", "api_token"), ("api_token", "api_token"), ("grafana_session", "grafana_session"), ("oauth_refresh", "oauth_refresh"), ("session_expiry", "session_expiry"), ("refresh_cookie_name", "refresh_cookie_name")):
        value = inputs.get(name)
        if value:
            if not isinstance(value, str):
                raise ValueError("Grafana authentication values must be strings.")
            auth[key] = value
    refresh_name = inputs.get("refresh_cookie_name") or cfg.get("refresh_cookie_name", "")
    if not isinstance(refresh_name, str):
        raise ValueError("Grafana refresh_cookie_name must be a string.")
    if refresh_name:
        auth["refresh_cookie_name"] = refresh_name
    if mode == "api_token" and not auth.get("api_token"):
        address, vault_path = cfg.get("vault_addr", ""), cfg.get("vault_path", "")
        if address and vault_path:
            login = os.environ.get("VAULT_TOKEN", "")
            if not login:
                # A native helper can mount the explicitly selected Vault login
                # file; never consult the user's host home from this container.
                login_file = os.environ.get("VAULT_TOKEN_FILE", "")
                if login_file:
                    try:
                        login = Path(login_file).read_text().strip()
                    except OSError:
                        pass
            if login:
                query = urllib.request.Request(address.rstrip("/") + "/v1/" + vault_path.lstrip("/"), headers={"X-Vault-Token": login})
                try:
                    with urllib.request.urlopen(query, timeout=10) as response:
                        token = json.load(response)["data"]["data"][cfg.get("vault_key", "token")]
                except (OSError, KeyError, ValueError):
                    raise ValueError("Could not load Grafana service account token from Vault.") from None
                if not isinstance(token, str) or not token:
                    raise ValueError("Vault returned an empty Grafana service account token.")
                auth["api_token"] = token
    required = not auth.get("api_token") if mode == "api_token" else not auth.get("grafana_session") or bool(refresh_name and not auth.get("oauth_refresh"))
    # Preserve supplied partial cookies as the old launcher did, but a missing
    # credential alone creates no state file or noninteractive prompt.
    if auth != old and (not required or len(auth) > 1):
        _persist(path, auth)
    return {"auth_required": bool(required)}


if __name__ == "__main__":
    try:
        if len(sys.argv) != 2 or sys.argv[1] not in {"prepare", "authenticate"}:
            raise ValueError("Expected prepare or authenticate action.")
        print(json.dumps(prepare_auth(json.load(sys.stdin))))
    except (ValueError, TypeError, KeyError, OSError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
