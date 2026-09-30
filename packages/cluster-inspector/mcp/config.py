"""User supplied Cluster Inspector target configuration."""
from __future__ import annotations

import os
import base64
import json
import re
import tomllib
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any

@dataclass(frozen=True)
class TenantProfile:
    name: str
    database: str
    username: str
    credential: dict[str, Any]

@dataclass(frozen=True)
class DatabaseProfile:
    name: str
    transport: str
    host: str
    port: int
    namespace: str = ""
    service: str = ""
    tenants: dict[str, TenantProfile] = field(default_factory=dict)
    tls: dict[str, str] = field(default_factory=dict)

@dataclass(frozen=True)
class TektonProfile:
    default_namespace: str
    branch_selector_key: str | None = None
    repository_selector_key: str | None = None
    repository_context_hook: str | None = None

@dataclass(frozen=True)
class ClusterProfile:
    environment: str
    target: str
    local_port: int
    api_server: str
    cluster: dict[str, Any]
    dbms: dict[str, DatabaseProfile]
    selected: set[str] = field(default_factory=set)
    tekton: TektonProfile | None = None

def _required(mapping: dict, key: str, section: str) -> str:
    value = mapping.get(key)
    if not isinstance(value, str) or not value:
        raise ValueError(f"{section}.{key} must be a non-empty string")
    return value

_DNS_LABEL = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
_LABEL_KEY = re.compile(r"^(?:[A-Za-z0-9](?:[-A-Za-z0-9.]*[A-Za-z0-9])?/)?[A-Za-z0-9]([-A-Za-z0-9_.]*[A-Za-z0-9])?$")

def _tekton_profile(data: dict, config_path: Path) -> TektonProfile | None:
    if "tekton" not in data:
        return None
    values = data["tekton"]
    if not isinstance(values, dict):
        raise ValueError("[tekton] must be a table")
    namespace = _required(values, "default_namespace", "tekton")
    if len(namespace) > 63 or not _DNS_LABEL.fullmatch(namespace):
        raise ValueError("tekton.default_namespace must be a Kubernetes DNS label")
    branch_key = values.get("branch_selector_key")
    repository_key = values.get("repository_selector_key")
    for key_name, key in (("branch_selector_key", branch_key), ("repository_selector_key", repository_key)):
        if key is not None and (not isinstance(key, str) or not _LABEL_KEY.fullmatch(key)):
            raise ValueError(f"tekton.{key_name} must be a valid Kubernetes label key")
    hook_value = values.get("repository_context_hook")
    hook = None
    if hook_value is not None:
        if not isinstance(hook_value, str) or not hook_value.strip():
            raise ValueError("tekton.repository_context_hook must be a non-empty path")
        base = config_path.resolve().parent
        candidate = Path(hook_value).expanduser()
        if not candidate.is_absolute():
            candidate = base / candidate
        hook = candidate.resolve()
        try:
            hook.relative_to(base)
        except ValueError:
            raise ValueError("tekton.repository_context_hook must be inside the target directory") from None
        if not hook.is_file() or not os.access(hook, os.X_OK):
            raise ValueError("tekton.repository_context_hook must be an executable file")
        hook = str(hook)
    return TektonProfile(namespace, branch_key, repository_key, hook)

def load_profile(config_path: Path) -> ClusterProfile:
    with Path(config_path).expanduser().open("rb") as stream:
        data = tomllib.load(stream)
    mcp = data.get("mcp", {}); cluster = dict(data.get("cluster", {}))
    if "kubeconfig" in cluster or "context" in cluster:
        raise ValueError("cluster.kubeconfig/context are unsupported; enter a bearer token in the TUI and configure cluster.api_server")
    if cluster.get("ca_file"):
        ca_path = Path(cluster["ca_file"]).expanduser()
        if not ca_path.is_absolute(): ca_path = Path(config_path).parent / ca_path
        cluster["ca_data"] = base64.b64encode(ca_path.read_bytes()).decode("ascii")
    if not isinstance(cluster.get("verify_x509_strict", True), bool):
        raise ValueError("cluster.verify_x509_strict must be a boolean")
    if not isinstance(mcp.get("local_port"), int) or not 1024 <= mcp["local_port"] <= 65535:
        raise ValueError("mcp.local_port must be between 1024 and 65535")
    api = cluster.get("api_server", "")
    if not api:
        raise ValueError("cluster.api_server is required")
    cluster.setdefault("api_server", api)
    if not api.startswith("https://"):
        raise ValueError("cluster.api_server must use HTTPS")
    dbms = {}
    for name, item in data.get("dbms", {}).items():
        transport = _required(item, "transport", f"dbms.{name}")
        if transport not in {"direct", "port_forward"}:
            raise ValueError(f"dbms.{name}.transport must be direct or port_forward")
        port = item.get("port")
        if not isinstance(port, int) or not 1 <= port <= 65535:
            raise ValueError(f"dbms.{name}.port must be between 1 and 65535")
        host = item.get("host", "")
        namespace, service = item.get("namespace", ""), item.get("service", "")
        if transport == "direct" and not host: raise ValueError(f"dbms.{name}.host is required for direct transport")
        if transport == "port_forward" and (not namespace or not service): raise ValueError(f"dbms.{name} requires namespace and service")
        tenants = {}
        tls = {}
        tls_values = item.get("tls", {})
        if not isinstance(tls_values, dict): raise ValueError(f"dbms.{name}.tls must be a table")
        sslmode = tls_values.get("sslmode", "prefer")
        if sslmode not in {"disable", "allow", "prefer", "require", "verify-ca", "verify-full"}:
            raise ValueError(f"dbms.{name}.tls.sslmode is invalid")
        tls["sslmode"] = sslmode
        for key in ("root_cert", "client_cert", "client_key"):
            value = tls_values.get(key)
            if value:
                if not isinstance(value, str): raise ValueError(f"dbms.{name}.tls.{key} must be a file path")
                file_path = Path(value).expanduser()
                if not file_path.is_absolute(): file_path = Path(config_path).resolve().parent / file_path
                if not file_path.is_file(): raise ValueError(f"dbms.{name}.tls.{key} file does not exist")
                tls[key] = str(file_path.resolve())
        for tenant_name, t in item.get("tenants", {}).items():
            kind = _required(t, "credential_source", f"dbms.{name}.tenants.{tenant_name}")
            credential = {"type": kind}
            if kind == "literal": credential["password"] = _required(t, "password", tenant_name)
            elif kind == "kubernetes_secret":
                for source, dest in (("secret_namespace", "namespace"), ("secret_name", "name"), ("secret_key", "key")):
                    credential[dest] = _required(t, source, tenant_name)
            elif kind == "executable":
                command = Path(_required(t, "command", tenant_name))
                resolved = (Path(config_path).parent / command).resolve() if not command.is_absolute() else command.resolve()
                try: resolved.relative_to(Path(config_path).parent.resolve())
                except ValueError: raise ValueError(f"dbms.{name}.tenants.{tenant_name}.command must be inside the target directory") from None
                if not os.access(resolved, os.X_OK): raise ValueError(f"dbms.{name}.tenants.{tenant_name}.command must be executable")
                credential["command"] = str(resolved)
            else: raise ValueError(f"Unsupported credential_source: {kind}")
            tenants[tenant_name] = TenantProfile(tenant_name, _required(t,"database",tenant_name), _required(t,"username",tenant_name), credential)
        dbms[name] = DatabaseProfile(name, transport, host, port, namespace, service, tenants, tls)
    environment = str(mcp.get("environment", ""))
    target = str(mcp.get("target", Path(config_path).stem))
    if environment: validate_identifier(environment, "environment")
    validate_identifier(target, "target")
    tekton = _tekton_profile(data, Path(config_path))
    return ClusterProfile(environment, target, mcp["local_port"], api, cluster, dbms, tekton=tekton)

def state_path(root: Path, environment: str, target: str) -> Path:
    validate_identifier(environment, "environment")
    validate_identifier(target, "target")
    return Path(root) / "cluster-inspector" / environment / target

def validate_identifier(value: str, label: str = "identifier") -> str:
    if not isinstance(value, str) or not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", value):
        raise ValueError(f"{label} must be a safe identifier containing lowercase letters, digits, and single hyphens")
    return value

def validate_selections(profile: ClusterProfile, selections: list[str]) -> set[str]:
    known = {f"{db}/{tenant}" for db, item in profile.dbms.items() for tenant in item.tenants}
    unknown = set(selections) - known
    if unknown: raise ValueError("Unknown database tenant selection: " + ", ".join(sorted(unknown)))
    return set(selections)

def write_auth_file(path: Path, content: str) -> Path:
    path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(path.parent, 0o700)
    fd = os.open(path, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    with os.fdopen(fd, "w") as stream: stream.write(content)
    os.chmod(path, 0o600)
    return path

def materialize_kubeconfig(profile: ClusterProfile, state_directory: Path, token: str | None = None) -> Path:
    """Write a target-scoped kubeconfig using only the configured API URI and TUI token."""
    if not token:
        existing = Path(state_directory) / "kubeconfig"
        if existing.is_file():
            return existing
        raise ValueError("No target-scoped Kubernetes credentials are available")
    cluster = {"server": profile.api_server}
    if profile.cluster.get("ca_data"):
        cluster["certificate-authority-data"] = profile.cluster["ca_data"]
    result = {
        "apiVersion": "v1",
        "kind": "Config",
        "clusters": [{"name": "selected", "cluster": cluster}],
        "users": [{"name": "selected", "user": {"token": token}}],
        "contexts": [{"name": "selected", "context": {"cluster": "selected", "user": "selected"}}],
        "current-context": "selected",
    }
    path = Path(state_directory) / "kubeconfig"
    return write_auth_file(path, json.dumps(result, sort_keys=True) + "\n")
