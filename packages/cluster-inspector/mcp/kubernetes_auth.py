"""Target-scoped Kubernetes authentication helpers.

JWT claim decoding supplies renewal metadata only; Kubernetes remains the authority
for whether a credential is currently authenticated.
"""
from __future__ import annotations

import base64
import json
import os
import re
import ssl
import subprocess
import tempfile
import threading
import time
import urllib.error
import urllib.request
from dataclasses import dataclass
from pathlib import Path
from typing import Literal
from urllib.parse import urlparse



@dataclass(frozen=True)
class ServiceAccountIdentity:
    username: str
    namespace: str
    name: str
    expires_at: float


@dataclass(frozen=True)
class AuthCheck:
    status: Literal["valid", "invalid", "unknown"]
    username: str | None


def decode_service_account_token(token: str, now: float | None = None) -> ServiceAccountIdentity:
    """Decode identity metadata from a JWT without claiming signature verification."""
    try:
        parts = token.split(".")
        if len(parts) != 3:
            raise ValueError
        payload = parts[1] + "=" * (-len(parts[1]) % 4)
        claims = json.loads(base64.urlsafe_b64decode(payload.encode("ascii")))
        subject = claims.get("sub")
        prefix = "system:serviceaccount:"
        if not isinstance(subject, str) or not subject.startswith(prefix):
            raise ValueError
        fields = subject[len(prefix):].split(":")
        if len(fields) != 2 or not all(fields):
            raise ValueError
        expiry = claims.get("exp")
        if isinstance(expiry, bool) or not isinstance(expiry, (int, float)):
            raise ValueError("exp must be a numeric timestamp")
        if expiry <= (time.time() if now is None else now):
            raise ValueError("service account token is expired")
        return ServiceAccountIdentity(subject, fields[0], fields[1], float(expiry))
    except (ValueError, TypeError, KeyError, UnicodeError, json.JSONDecodeError) as error:
        if isinstance(error, ValueError) and str(error) in {"service account token is expired", "exp must be a numeric timestamp"}:
            raise
        raise ValueError("malformed or non-service-account token") from None


def _validate_api(api_server: str) -> str:
    parsed = urlparse(api_server)
    if parsed.scheme != "https" or not parsed.hostname or parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise ValueError("Kubernetes API server must be an HTTPS URL without credentials, query, or fragment")
    return api_server.rstrip("/")


def _ssl_context(ca_data: str | None, verify_x509_strict: bool = True) -> ssl.SSLContext:
    if not ca_data:
        context = ssl.create_default_context()
    else:
        try:
            raw = base64.b64decode(ca_data, validate=True)
            pem = raw.decode("ascii")
            context = ssl.create_default_context(cadata=pem)
        except (ValueError, UnicodeError) as error:
            raise ValueError("Invalid certificate authority data") from error
    if not verify_x509_strict:
        context.verify_flags &= ~getattr(ssl, "VERIFY_X509_STRICT", 0)
    return context


def check_token(api_server: str, ca_data: str | None, token: str, timeout: float = 5.0, verify_x509_strict: bool = True) -> AuthCheck:
    api = _validate_api(api_server)
    body = json.dumps({"apiVersion": "authentication.k8s.io/v1", "kind": "SelfSubjectReview"}).encode()
    request = urllib.request.Request(api + "/apis/authentication.k8s.io/v1/selfsubjectreviews", data=body, headers={"Authorization": "Bearer " + token, "Content-Type": "application/json"}, method="POST")
    try:
        with urllib.request.urlopen(request, timeout=timeout, context=_ssl_context(ca_data, verify_x509_strict)) as response:
            data = json.loads(response.read())
        username = data.get("status", {}).get("userInfo", {}).get("username")
        return AuthCheck("valid", username if isinstance(username, str) else None)
    except urllib.error.HTTPError as error:
        if error.code == 401:
            return AuthCheck("invalid", None)
        if error.code == 403:
            # Kubernetes has authenticated the identity but denied SelfSubjectReview.
            return AuthCheck("valid", None)
        return AuthCheck("unknown", None)
    except (OSError, TimeoutError, ssl.SSLError, ValueError, json.JSONDecodeError):
        return AuthCheck("unknown", None)


def _private_write(destination: Path, content: bytes, *, private_parent: bool = True) -> Path:
    destination = Path(destination)
    destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    if private_parent:
        os.chmod(destination.parent, 0o700)
    fd, temp_name = tempfile.mkstemp(prefix="." + destination.name + ".", dir=destination.parent)
    try:
        os.fchmod(fd, 0o600)
        with os.fdopen(fd, "wb") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.replace(temp_name, destination)
        os.chmod(destination, 0o600)
    except BaseException:
        try: os.unlink(temp_name)
        except OSError: pass
        raise
    return destination


def _load_kubeconfig(path: Path) -> dict:
    """Load JSON directly or normalize an explicit YAML kubeconfig with kubectl."""
    try:
        value = json.loads(Path(path).read_text())
        if isinstance(value, dict):
            return value
    except (OSError, json.JSONDecodeError):
        pass
    try:
        result = subprocess.run(
            ["kubectl", "config", "view", "--raw", "--flatten", "--minify", "--kubeconfig", str(path), "-o", "json"],
            check=True, capture_output=True, text=True, timeout=15,
        )
        value = json.loads(result.stdout)
        if not isinstance(value, dict):
            raise ValueError
        return value
    except (OSError, subprocess.SubprocessError, json.JSONDecodeError, ValueError) as error:
        raise ValueError("Could not parse explicit kubeconfig") from error


def write_token_kubeconfig(api_server: str, ca_data: str | None, destination: Path, token: str) -> Path:
    api = _validate_api(api_server)
    cluster = {"server": api}
    if ca_data:
        cluster["certificate-authority-data"] = ca_data
    document = {"apiVersion": "v1", "kind": "Config", "clusters": [{"name": "selected", "cluster": cluster}], "users": [{"name": "selected", "user": {"token": token}}], "contexts": [{"name": "selected", "context": {"cluster": "selected", "user": "selected"}}], "current-context": "selected"}
    return _private_write(Path(destination), (json.dumps(document, sort_keys=True) + "\n").encode())


def resolve_runtime_kubeconfig(api_server: str) -> Path | None:
    """Resolve explicitly selected pod credentials without consulting host kubeconfig state."""
    mode = os.environ.get("MCP_KUBERNETES_AUTH_MODE", "").strip()
    if not mode:
        return None
    api = _validate_api(api_server)
    if mode == "service_account":
        token_value = os.environ.get("MCP_KUBERNETES_TOKEN_FILE", "").strip()
        ca_value = os.environ.get("MCP_KUBERNETES_CA_FILE", "").strip()
        if not token_value or not ca_value:
            raise ValueError("service_account mode requires MCP_KUBERNETES_TOKEN_FILE and MCP_KUBERNETES_CA_FILE")
        token_file, ca_file = Path(token_value), Path(ca_value)
        if not token_file.is_absolute() or not ca_file.is_absolute():
            raise ValueError("Projected service-account token and CA paths must be absolute")
        if not token_file.is_file() or not ca_file.is_file():
            raise ValueError("Projected service-account token and CA files must be readable")
        destination = Path(os.environ.get("MCP_KUBERNETES_GENERATED_KUBECONFIG", "/tmp/mcp-kubernetes-auth/kubeconfig"))
        document = {
            "apiVersion": "v1", "kind": "Config",
            "clusters": [{"name": "selected", "cluster": {"server": api, "certificate-authority": str(ca_file)}}],
            "users": [{"name": "selected", "user": {"tokenFile": str(token_file)}}],
            "contexts": [{"name": "selected", "context": {"cluster": "selected", "user": "selected"}}],
            "current-context": "selected",
        }
        return _private_write(destination, (json.dumps(document, sort_keys=True) + "\n").encode(), private_parent=False)
    if mode == "kubeconfig":
        source_value = os.environ.get("MCP_KUBERNETES_KUBECONFIG", "").strip()
        if not source_value:
            raise ValueError("kubeconfig mode requires MCP_KUBERNETES_KUBECONFIG")
        source = Path(source_value).resolve()
        if not source.is_file():
            raise ValueError("Explicit mounted kubeconfig does not exist")
        config = _load_kubeconfig(source)
        current = config.get("current-context")
        try:
            context = next(item["context"] for item in config["contexts"] if item["name"] == current)
            cluster = next(item["cluster"] for item in config["clusters"] if item["name"] == context["cluster"])
            next(item["user"] for item in config["users"] if item["name"] == context["user"])
        except (KeyError, StopIteration, TypeError) as error:
            raise ValueError("Explicit mounted kubeconfig has no complete current context") from error
        if cluster.get("server", "").rstrip("/") != api:
            raise ValueError("Explicit mounted kubeconfig targets a different API server")
        return source
    raise ValueError("MCP_KUBERNETES_AUTH_MODE must be service_account or kubeconfig")


def _ensure_target_path(state_directory: Path, credential_path: Path) -> tuple[Path, Path]:
    state = Path(state_directory).resolve()
    credential = Path(credential_path).resolve()
    if credential.parent != state:
        raise ValueError("Credential file must be directly inside its target state directory")
    if credential.name in {"auth.json", ".", ".."}:
        raise ValueError("Invalid credential filename")
    return state, credential


def _marker(state: Path, mode: str, username: str, identity: ServiceAccountIdentity | None = None) -> None:
    data = {"mode": mode, "username": username}
    if identity:
        data.update({"namespace": identity.namespace, "service_account": identity.name, "expires_at": identity.expires_at})
    _private_write(state / "auth.json", (json.dumps(data, sort_keys=True, indent=2) + "\n").encode())


def _reject_default_kubeconfig(source: Path) -> None:
    forbidden = Path.home() / ".kube" / "config"
    try:
        if Path(source).resolve() == forbidden.resolve():
            raise ValueError("The Mac default kubeconfig cannot be selected")
    except OSError:
        pass


def copy_kubeconfig(source: Path, destination: Path, expected_api_server: str) -> str:
    source, destination = Path(source), Path(destination)
    _reject_default_kubeconfig(source)
    expected_api_server = _validate_api(expected_api_server)
    if not source.is_file():
        raise ValueError("Selected kubeconfig does not exist")
    try:
        result = subprocess.run(["kubectl", "config", "view", "--raw", "--flatten", "--minify", "--kubeconfig", str(source), "-o", "json"], check=True, capture_output=True, text=True, timeout=15)
        config = json.loads(result.stdout)
        users = config.get("users") or []
        if not users or not isinstance(users[0].get("user"), dict):
            raise ValueError("Selected kubeconfig has no supported user credentials")
        user = users[0]["user"]
        if "exec" in user or "auth-provider" in user:
            raise ValueError("Exec and auth-provider kubeconfig credentials are unsupported")
        clusters = config.get("clusters") or []
        cluster = clusters[0].get("cluster", {}) if clusters else {}
        if not clusters or cluster.get("server", "").rstrip("/") != expected_api_server:
            raise ValueError("Selected kubeconfig does not target the configured API server")
        if any(key in cluster for key in ("certificate-authority",)) or any(key in user for key in ("client-certificate", "client-key", "tokenFile")):
            raise ValueError("Selected kubeconfig contains an unflattened external credential file")
        if not any(key in user for key in ("token", "tokenFile", "client-certificate-data")):
            raise ValueError("Selected kubeconfig has no supported credentials")
        # Validate a private candidate before replacing an existing target cache.
        destination.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        fd, name = tempfile.mkstemp(prefix=".kubeconfig-validation-", dir=destination.parent)
        os.close(fd)
        candidate = Path(name)
        try:
            _private_write(candidate, json.dumps(config, sort_keys=True).encode())
            who = subprocess.run(["kubectl", "auth", "whoami", "--kubeconfig", str(candidate), "-o", "json"], check=True, capture_output=True, text=True, timeout=15)
            data = json.loads(who.stdout)
            username = data.get("status", {}).get("userInfo", {}).get("username")
            if not isinstance(username, str) or not username:
                # Older kubectl versions may emit a direct username/table.
                username = who.stdout.strip()
            if not username:
                raise ValueError("Could not determine authenticated username")
            _private_write(destination, json.dumps(config, sort_keys=True).encode())
            return username
        finally:
            candidate.unlink(missing_ok=True)
    except (OSError, subprocess.SubprocessError, json.JSONDecodeError) as error:
        raise ValueError("Could not validate selected kubeconfig") from error


def prepare_token_auth(state_directory: Path, credential_path: Path, api_server: str, ca_data: str | None, token: str, verify_x509_strict: bool = True) -> AuthCheck:
    state, credential = _ensure_target_path(state_directory, credential_path)
    check = check_token(api_server, ca_data, token, verify_x509_strict=verify_x509_strict)
    if check.status != "valid":
        return check
    try:
        identity = decode_service_account_token(token)
    except ValueError:
        identity = None
    write_token_kubeconfig(api_server, ca_data, credential, token)
    _marker(state, "token", check.username or (identity.username if identity else "unknown"), identity)
    return check


def prepare_kubeconfig_auth(state_directory: Path, credential_path: Path, source: Path, api_server: str) -> AuthCheck:
    state, credential = _ensure_target_path(state_directory, credential_path)
    username = copy_kubeconfig(source, credential, api_server)
    _marker(state, "kubeconfig", username)
    return AuthCheck("valid", username)


def _validate_existing(credential: Path, api_server: str, ca_data: str | None, verify_x509_strict: bool = True) -> AuthCheck:
    try:
        config = _load_kubeconfig(credential)
        current = config.get("current-context")
        context = next(item["context"] for item in config.get("contexts", []) if item["name"] == current)
        cluster = next(item["cluster"] for item in config.get("clusters", []) if item["name"] == context["cluster"])
        if cluster.get("server", "").rstrip("/") != _validate_api(api_server):
            return AuthCheck("invalid", None)
        user = next(item["user"] for item in config.get("users", []) if item["name"] == context["user"])
        if "token" in user:
            check = check_token(api_server, ca_data, user["token"], verify_x509_strict=verify_x509_strict)
            if check.status == "valid":
                return check
            return check
        if "exec" in user or "auth-provider" in user:
            return AuthCheck("invalid", None)
        result = subprocess.run(["kubectl", "auth", "whoami", "--kubeconfig", str(credential), "-o", "json"], check=True, capture_output=True, text=True, timeout=15)
        info = json.loads(result.stdout).get("status", {}).get("userInfo", {})
        username = info.get("username")
        if username:
            return AuthCheck("valid", username)
    except (OSError, ValueError, KeyError, StopIteration, TypeError, subprocess.SubprocessError, json.JSONDecodeError):
        pass
    return AuthCheck("unknown", None)


def load_cached_auth(state_directory: Path, credential_path: Path, api_server: str, ca_data: str | None, verify_x509_strict: bool = True) -> AuthCheck:
    state, credential = _ensure_target_path(state_directory, credential_path)
    if not credential.is_file():
        return AuthCheck("invalid", None)
    result = _validate_existing(credential, api_server, ca_data, verify_x509_strict)
    if result.status == "valid" and result.username:
        try:
            marker = json.loads((state / "auth.json").read_text())
            if marker.get("username") == result.username:
                return result
        except (OSError, ValueError, AttributeError):
            pass
        mode = "token"
        identity = None
        try:
            config = _load_kubeconfig(credential)
            c = next(item["context"] for item in config.get("contexts", []) if item["name"] == config.get("current-context"))
            u = next(item["user"] for item in config.get("users", []) if item["name"] == c["user"])
            if "token" in u:
                identity = decode_service_account_token(u["token"])
            else: mode = "kubeconfig"
        except (ValueError, KeyError, StopIteration, TypeError):
            mode = "kubeconfig"
        _marker(state, mode, result.username, identity)
    return result


def start_token_renewal(kubeconfig_path: Path, api_server: str, ca_data: str | None, stop_event: threading.Event | None = None, verify_x509_strict: bool = True) -> threading.Thread:
    """Renew only the token in the supplied target-local kubeconfig."""
    path = Path(kubeconfig_path)
    stop = stop_event or threading.Event()
    api = _validate_api(api_server)

    def worker() -> None:
        while not stop.is_set():
            try:
                config = _load_kubeconfig(path)
                current = config.get("current-context")
                context = next(item["context"] for item in config["contexts"] if item["name"] == current)
                username = context["user"]
                user_entry = next(item for item in config["users"] if item["name"] == username)
                user = user_entry["user"]
                current_token = user["token"]
                identity = decode_service_account_token(current_token)
            except (OSError, ValueError, KeyError, StopIteration, TypeError):
                return

            # Re-read the latest file on every cycle, and sleep until 5 minutes
            # before this token's own exp claim.
            delay = max(0.0, identity.expires_at - time.time() - 300.0)
            if stop.wait(delay):
                return
            try:
                payload = {"apiVersion": "authentication.k8s.io/v1", "kind": "TokenRequest", "spec": {"expirationSeconds": 3600}}
                url = f"{api}/api/v1/namespaces/{identity.namespace}/serviceaccounts/{identity.name}/token"
                request = urllib.request.Request(url, data=json.dumps(payload).encode(), headers={"Authorization": "Bearer " + current_token, "Content-Type": "application/json"}, method="POST")
                with urllib.request.urlopen(request, timeout=5.0, context=_ssl_context(ca_data, verify_x509_strict)) as response:
                    response_data = json.loads(response.read())
                replacement = response_data["status"]["token"]
                replacement_identity = decode_service_account_token(replacement)
                if (replacement_identity.namespace, replacement_identity.name) != (identity.namespace, identity.name):
                    return
                # Refuse to overwrite a newer credential installed by another action.
                latest = _load_kubeconfig(path)
                latest_context = latest.get("current-context")
                latest_ctx = next(item["context"] for item in latest["contexts"] if item["name"] == latest_context)
                latest_user = next(item for item in latest["users"] if item["name"] == latest_ctx["user"])
                if latest_user["user"].get("token") != current_token:
                    continue
                latest_user["user"]["token"] = replacement
                _private_write(path, (json.dumps(latest, sort_keys=True) + "\n").encode())
            except (OSError, urllib.error.URLError, ssl.SSLError, ValueError, KeyError, StopIteration, TypeError, json.JSONDecodeError):
                return

    thread = threading.Thread(target=worker, name=f"kube-token-renewal-{path.name}", daemon=True)
    thread.start()
    return thread
