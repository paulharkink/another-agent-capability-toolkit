"""Read-only Kubernetes inspection MCP for one configured cluster profile."""

from __future__ import annotations

import json
import os
import re
import socket
import subprocess
import sys
import time
from base64 import b64decode
from contextlib import contextmanager
from pathlib import Path
from typing import Any, Iterator

import psycopg
from fastmcp import FastMCP

from config import ClusterProfile, DatabaseProfile, TenantProfile, load_profile, materialize_kubeconfig, validate_selections

sys.path.insert(0, str(Path(__file__).resolve().parent))
from kubernetes_auth import resolve_runtime_kubeconfig, start_token_renewal
from tekton_tools import register_tekton_tools

TARGET = os.environ.get("CLUSTER_INSPECTOR_TARGET", "unknown")
NAMESPACE = re.compile(r"^[a-z0-9]([-a-z0-9]*[a-z0-9])?$")
MAX_ERROR_LENGTH = 500
REQUEST_TIMEOUT_SECONDS = 30
CONNECT_TIMEOUT_SECONDS = 10
STATEMENT_TIMEOUT_SECONDS = 60
credentials: dict[str, str] = {}
credential_errors: dict[str, str] = {}
def initialize_target(config_path: str | None, selections: str) -> tuple[ClusterProfile | None, set[str]]:
    profile = load_profile(Path(config_path)) if config_path else None
    chosen = validate_selections(profile, list(filter(None, selections.split(",")))) if profile else set()
    return profile, chosen


PROFILE, SELECTED_CONNECTIONS = initialize_target(os.environ.get("CLUSTER_INSPECTOR_CONFIG"), os.environ.get("CLUSTER_INSPECTOR_CONNECTIONS", ""))

mcp = FastMCP(
    "cluster-inspector",
    instructions=(
        "Read-only Kubernetes inspection and configured read-only database queries for one "
        "configured target. Kubernetes RBAC and database account permissions determine "
        "access. Use list_connections then run_query for databases. Mutations, port-forwarding, "
        "exec, attach, proxying, arbitrary commands, and database connection details are unavailable."
    ),
)


def _error(message: str) -> dict[str, str]:
    return {"error": message}


def _namespace(value: str) -> str:
    if not NAMESPACE.fullmatch(value) or len(value) > 63:
        raise ValueError("namespace must be a lowercase Kubernetes DNS label of at most 63 characters.")
    return value


def _value(value: str, name: str) -> str:
    if not value or "\0" in value or value.startswith("-"):
        raise ValueError(f"{name} must be non-empty, contain no NUL bytes, and not begin with '-'.")
    return value


def _resource_argument(value: str) -> str:
    resource = _value(value, "resource")
    if {"secret", "secrets"}.intersection(re.split(r"[,/.]", resource.lower())):
        raise ValueError("Kubernetes Secret resources cannot be read through Cluster Inspector.")
    if not re.fullmatch(r"[A-Za-z][A-Za-z0-9.-]*", resource):
        raise ValueError("resource must name one Kubernetes resource type.")
    return resource


def _resource_name(value: str) -> str:
    name = _value(value, "name")
    if not re.fullmatch(r"[a-z0-9]([-a-z0-9.]*[a-z0-9])?", name) or len(name) > 253:
        raise ValueError("name must be one Kubernetes resource name.")
    return name


def _optional_value(value: str | None, name: str) -> str | None:
    return _value(value, name) if value is not None else None


def _non_negative(value: int | None, name: str) -> int | None:
    if value is not None and value < 0:
        raise ValueError(f"{name} must be non-negative.")
    return value


def _failure(stderr: str) -> ValueError:
    detail = stderr.strip()[:MAX_ERROR_LENGTH]
    lower_detail = detail.lower()
    if "unauthorized" in lower_detail or "token" in lower_detail or "provide credentials" in lower_detail:
        return ValueError("Kubernetes authentication failed or the token expired. Restart this cluster-inspector profile with a new token.")
    if "forbidden" in lower_detail or "notfound" in lower_detail or "not found" in lower_detail:
        return ValueError(detail or "Kubernetes API request was denied or the resource was not found.")
    return ValueError(detail or "Kubernetes request failed.")


def _kubectl(arguments: list[str], timeout: int = REQUEST_TIMEOUT_SECONDS) -> str:
    try:
        result = subprocess.run(
            ["kubectl", *arguments],
            capture_output=True,
            check=False,
            text=True,
            timeout=timeout,
        )
    except subprocess.TimeoutExpired:
        raise ValueError("Kubernetes API request timed out.") from None
    except OSError:
        raise ValueError("kubectl is unavailable.") from None
    if result.returncode:
        raise _failure(result.stderr)
    return result.stdout


def _kubectl_json(arguments: list[str]) -> dict[str, Any]:
    try:
        value = json.loads(_kubectl(arguments))
    except json.JSONDecodeError:
        raise ValueError("Kubernetes returned invalid JSON.") from None
    if not isinstance(value, dict):
        raise ValueError("Kubernetes returned an unexpected JSON result.")
    return value


def _require_session() -> dict[str, str] | None:
    try:
        _kubectl(["auth", "whoami"], timeout=10)
    except ValueError:
        return _error("No valid Kubernetes session is available. Start and authenticate this cluster-inspector profile, then retry.")
    return None


def _database_connections() -> dict[str, TenantProfile]:
    if PROFILE is None:
        return {}
    return {f"{db}/{tenant}": item for db, source in PROFILE.dbms.items() for tenant, item in source.tenants.items()}


register_tekton_tools(mcp, PROFILE.tekton if PROFILE else None, _kubectl, _require_session)

def _profile_dbms(connection: str) -> tuple[DatabaseProfile, Any] | None:
    if PROFILE is None: return None
    db, tenant = connection.split("/", 1)
    source = PROFILE.dbms[db]
    return source, source.tenants[tenant]

def load_tenant_credentials(db: DatabaseProfile) -> dict[str, str]:
    loaded = {}
    for name, tenant in db.tenants.items():
        source = tenant.credential
        if source["type"] == "literal": loaded[name] = source["password"]
        elif source["type"] == "kubernetes_secret":
            doc = _kubectl_json(["get", "secret", source["name"], "--namespace", source["namespace"], "--output", "json"])
            loaded[name] = b64decode(doc["data"][source["key"]]).decode()
        elif source["type"] == "executable":
            result = subprocess.run([source["command"]], capture_output=True, check=True, text=True, timeout=REQUEST_TIMEOUT_SECONDS)
            loaded[name] = result.stdout.rstrip("\n")
    return loaded

def _load_one_tenant_credential(tenant: Any) -> str:
    source = tenant.credential
    if source["type"] == "literal": return source["password"]
    if source["type"] == "kubernetes_secret":
        doc = _kubectl_json(["get", "secret", source["name"], "--namespace", source["namespace"], "--output", "json"])
        return b64decode(doc["data"][source["key"]]).decode()
    result = subprocess.run([source["command"]], capture_output=True, check=True, text=True, timeout=REQUEST_TIMEOUT_SECONDS)
    return result.stdout.rstrip("\n")


def load_database_credentials() -> None:
    global credentials, credential_errors
    credentials = {}
    credential_errors = {}
    if PROFILE is None:
        return
    for db_name, db in PROFILE.dbms.items():
        selected = {pair.split("/", 1)[1] for pair in SELECTED_CONNECTIONS if pair.startswith(db_name + "/")}
        for name in selected:
            try:
                credentials[f"{db_name}/{name}"] = _load_one_tenant_credential(db.tenants[name])
            except Exception:
                credential_errors[f"{db_name}/{name}"] = "Database connection is unavailable or unauthorized."


def _free_port() -> int:
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as listener:
        listener.bind(("127.0.0.1", 0))
        return listener.getsockname()[1]


def _tls_options(profile: DatabaseProfile | None) -> dict[str, str]:
    if profile is None: return {}
    tls = profile.tls
    result = {"sslmode": tls.get("sslmode", "prefer")}
    for source, destination in (("root_cert", "sslrootcert"), ("client_cert", "sslcert"), ("client_key", "sslkey")):
        if source in tls: result[destination] = tls[source]
    return result


def _api_resources() -> list[dict[str, Any]]:
    lines = _kubectl(["api-resources", "--output", "wide"]).splitlines()
    if not lines:
        return []
    resources = []
    for line in lines[1:]:
        columns = line.split()
        if len(columns) < 5:
            continue
        namespaced_index = next((index for index, value in enumerate(columns) if value in {"true", "false"}), None)
        if namespaced_index is None or namespaced_index < 2 or namespaced_index + 1 >= len(columns):
            continue
        leading = columns[:namespaced_index]
        resources.append(
            {
                "name": leading[0],
                "shortNames": leading[1] if len(leading) == 3 else "",
                "apiVersion": leading[-1],
                "namespaced": columns[namespaced_index] == "true",
                "kind": columns[namespaced_index + 1],
            }
        )
    return resources


def _namespace_arguments(namespace: str | None, all_namespaces: bool) -> list[str]:
    if namespace is not None and all_namespaces:
        raise ValueError("namespace and all_namespaces cannot both be set.")
    if namespace is not None:
        return ["--namespace", _namespace(namespace)]
    return ["--all-namespaces"] if all_namespaces else []


def _pod_summary(pod: dict[str, Any]) -> dict[str, Any]:
    regular = pod.get("spec", {}).get("containers", [])
    statuses = pod.get("status", {}).get("containerStatuses", [])
    init_statuses = pod.get("status", {}).get("initContainerStatuses", [])
    ready = sum(status.get("ready") is True for status in statuses)
    restarts = sum(status.get("restartCount", 0) for status in [*statuses, *init_statuses])
    return {
        "name": pod.get("metadata", {}).get("name"),
        "phase": pod.get("status", {}).get("phase"),
        "ready": f"{ready}/{len(regular)}",
        "restarts": restarts,
        "creationTimestamp": pod.get("metadata", {}).get("creationTimestamp"),
        "nodeName": pod.get("spec", {}).get("nodeName"),
    }


@mcp.tool()
def list_connections() -> dict[str, Any]:
    """List configured read-only database connections for this target."""
    return {"target": TARGET, "connections": [{"name": name, "purpose": "configured read-only database access"} for name in _database_connections() if name in SELECTED_CONNECTIONS]}


@mcp.tool()
def run_query(
    connection: str,
    query: str,
    parameters: dict[str, Any] | list[Any] | None = None,
    connect_timeout_seconds: int = CONNECT_TIMEOUT_SECONDS,
    statement_timeout_seconds: int = STATEMENT_TIMEOUT_SECONDS,
) -> dict[str, Any]:
    """Run raw SQL in an explicit read-only transaction on a configured database connection."""
    connections = _database_connections()
    if connection not in connections:
        return _error(f"Configured connection not found: {connection}")
    if PROFILE is not None and connection not in SELECTED_CONNECTIONS:
        return _error(f"Configured connection was not selected at startup: {connection}")
    if not query:
        return _error("query is required.")
    if connect_timeout_seconds < 0 or statement_timeout_seconds < 0:
        return _error("timeout values must be non-negative.")
    if connection in credential_errors:
        return _error(credential_errors[connection])
    if connection not in credentials:
        return _error(f"Credentials for {connection} were not loaded at startup.")
    started = time.monotonic()
    try:
        selected = _profile_dbms(connection)
        assert selected is not None
        transport = _database_transport(selected[0])
        with transport as endpoint:
            host, local_port = endpoint
            tenant = selected[1]
            database_context = psycopg.connect(host=host, port=local_port, dbname=tenant.database, user=tenant.username, password=credentials[connection], connect_timeout=connect_timeout_seconds, **_tls_options(selected[0]))
            with database_context as database:
                with database.cursor() as cursor:
                    cursor.execute("BEGIN READ ONLY")
                    cursor.execute("SELECT set_config('statement_timeout', %s, true)", (f"{statement_timeout_seconds}s",))
                    cursor.execute(query, parameters)
                    columns = []
                    rows = []
                    if cursor.description:
                        columns = [{"name": column.name, "type": str(column.type_code)} for column in cursor.description]
                        rows = [[value for value in row] for row in cursor.fetchall()]
                    database.rollback()
        return {"connection": connection, "columns": columns, "rows": rows, "rowCount": len(rows), "executionMs": round((time.monotonic() - started) * 1000)}
    except Exception as error:
        return _error(str(error))

@contextmanager
def _database_transport(profile: DatabaseProfile) -> Iterator[tuple[str, int]]:
    if profile.transport == "direct":
        yield profile.host, profile.port
    else:
        with _port_forward(profile.namespace, profile.service, profile.port) as endpoint:
            yield endpoint

@contextmanager
def _port_forward(namespace: str, service: str, port: int) -> Iterator[tuple[str, int]]:
    local_port = _free_port()
    process = subprocess.Popen(["kubectl", "port-forward", "--address", "127.0.0.1", "--namespace", namespace, f"service/{service}", f"{local_port}:{port}"], stdout=subprocess.DEVNULL, stderr=subprocess.PIPE, text=True)
    try:
        deadline = time.monotonic() + CONNECT_TIMEOUT_SECONDS
        while time.monotonic() < deadline:
            if process.poll() is not None: raise ValueError("Database transport could not be established.")
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as probe:
                probe.settimeout(.2)
                if probe.connect_ex(("127.0.0.1", local_port)) == 0:
                    yield "127.0.0.1", local_port; return
            time.sleep(.05)
        raise ValueError("Database transport timed out.")
    finally:
        process.terminate()
        try: process.wait(timeout=5)
        except subprocess.TimeoutExpired: process.kill(); process.wait()


@mcp.tool()
def api_resources(api_group: str | None = None, namespaced: bool | None = None) -> dict[str, Any]:
    """Discover Kubernetes API resources and their supported verbs."""
    try:
        resources = _api_resources()
        selected = []
        for resource in resources:
            group = resource["apiVersion"].split("/", 1)[0] if "/" in resource["apiVersion"] else ""
            if api_group is not None and group != api_group:
                continue
            if namespaced is not None and resource["namespaced"] is not namespaced:
                continue
            selected.append(resource)
        return {"target": TARGET, "resources": selected}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def get_resource(resource: str, name: str, namespace: str | None = None) -> dict[str, Any]:
    """Get one complete Kubernetes resource as raw JSON."""
    try:
        arguments = ["get", _resource_argument(resource), _resource_name(name), "--output", "json"]
        if namespace is not None:
            arguments.extend(["--namespace", _namespace(namespace)])
        return {"target": TARGET, "resource": _kubectl_json(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def list_resources(
    resource: str,
    namespace: str | None = None,
    all_namespaces: bool = False,
    label_selector: str | None = None,
    field_selector: str | None = None,
    limit: int | None = None,
) -> dict[str, Any]:
    """List complete Kubernetes resources as raw JSON, subject to Kubernetes RBAC."""
    try:
        arguments = ["get", _resource_argument(resource), "--output", "json"]
        arguments.extend(_namespace_arguments(namespace, all_namespaces))
        if label_selector is not None:
            arguments.extend(["--selector", _value(label_selector, "label_selector")])
        if field_selector is not None:
            arguments.extend(["--field-selector", _value(field_selector, "field_selector")])
        if limit is not None:
            arguments.extend(["--chunk-size", str(_non_negative(limit, "limit"))])
        return {"target": TARGET, "resources": _kubectl_json(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def describe_resource(resource: str, name: str, namespace: str | None = None) -> dict[str, Any]:
    """Describe one Kubernetes resource with related operational details."""
    try:
        arguments = ["describe", _resource_argument(resource), _resource_name(name)]
        if namespace is not None:
            arguments.extend(["--namespace", _namespace(namespace)])
        return {"target": TARGET, "text": _kubectl(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def get_logs(
    namespace: str,
    pod: str,
    container: str | None = None,
    previous: bool = False,
    since: str | None = None,
    since_time: str | None = None,
    tail_lines: int | None = 500,
    timestamps: bool = True,
) -> dict[str, Any]:
    """Get bounded current or previous logs for one Kubernetes pod container."""
    try:
        if since is not None and since_time is not None:
            raise ValueError("since and since_time cannot both be set.")
        arguments = ["logs", _value(pod, "pod"), "--namespace", _namespace(namespace)]
        if container is not None:
            arguments.extend(["--container", _value(container, "container")])
        if previous:
            arguments.append("--previous")
        if since is not None:
            arguments.extend(["--since", _value(since, "since")])
        if since_time is not None:
            arguments.extend(["--since-time", _value(since_time, "since_time")])
        if tail_lines is not None:
            arguments.extend(["--tail", str(_non_negative(tail_lines, "tail_lines"))])
        if timestamps:
            arguments.append("--timestamps")
        return {"target": TARGET, "text": _kubectl(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def get_events(
    namespace: str | None = None,
    all_namespaces: bool = False,
    field_selector: str | None = None,
    label_selector: str | None = None,
) -> dict[str, Any]:
    """List raw Kubernetes Event objects."""
    try:
        arguments = ["get", "events", "--output", "json"]
        arguments.extend(_namespace_arguments(namespace, all_namespaces))
        if field_selector is not None:
            arguments.extend(["--field-selector", _value(field_selector, "field_selector")])
        if label_selector is not None:
            arguments.extend(["--selector", _value(label_selector, "label_selector")])
        return {"target": TARGET, "events": _kubectl_json(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def top_pods(
    namespace: str | None = None,
    all_namespaces: bool = False,
    pod: str | None = None,
    label_selector: str | None = None,
    containers: bool = False,
) -> dict[str, Any]:
    """Get raw Metrics API CPU and memory usage for Kubernetes pods."""
    try:
        arguments = ["top", "pods"]
        arguments.extend(_namespace_arguments(namespace, all_namespaces))
        if pod is not None:
            arguments.append(_value(pod, "pod"))
        if label_selector is not None:
            arguments.extend(["--selector", _value(label_selector, "label_selector")])
        if containers:
            arguments.append("--containers")
        return {"target": TARGET, "text": _kubectl(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def top_nodes(node: str | None = None, label_selector: str | None = None) -> dict[str, Any]:
    """Get raw Metrics API CPU and memory usage for Kubernetes nodes."""
    try:
        arguments = ["top", "nodes"]
        if node is not None:
            arguments.append(_value(node, "node"))
        if label_selector is not None:
            arguments.extend(["--selector", _value(label_selector, "label_selector")])
        return {"target": TARGET, "text": _kubectl(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def explain_resource(resource: str, field: str | None = None, api_version: str | None = None, recursive: bool = False) -> dict[str, Any]:
    """Explain Kubernetes resource fields using the cluster OpenAPI schema."""
    try:
        selected_resource = _resource_argument(resource)
        if field is not None:
            selected_resource = f"{selected_resource}.{_value(field, 'field')}"
        arguments = ["explain", selected_resource]
        if api_version is not None:
            arguments.extend(["--api-version", _value(api_version, "api_version")])
        if recursive:
            arguments.append("--recursive")
        return {"target": TARGET, "text": _kubectl(arguments)}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def version() -> dict[str, Any]:
    """Get Kubernetes client and configured server version information."""
    try:
        return {"target": TARGET, "version": _kubectl_json(["version", "--output", "json"])}
    except ValueError as error:
        return _error(str(error))


@mcp.tool()
def list_pods(namespace: str) -> dict[str, Any]:
    """List compact pod status in one exact Kubernetes namespace."""
    try:
        selected_namespace = _namespace(namespace)
        pods = _kubectl_json(["get", "pods", "--namespace", selected_namespace, "--output", "json"]).get("items", [])
        pods = sorted(pods, key=lambda pod: ((pod.get("metadata", {}).get("creationTimestamp") or ""), pod.get("metadata", {}).get("name") or ""), reverse=True)
        total = len(pods)
        return {"target": TARGET, "namespace": selected_namespace, "total": total, "truncated": total > 500, "pods": [_pod_summary(pod) for pod in pods[:500]]}
    except ValueError as error:
        return _error(str(error))


def prepare_server_auth(profile: ClusterProfile | None, state_directory: Path) -> Path:
    api_server = profile.api_server if profile else os.environ.get("MCP_KUBERNETES_API_SERVER", "")
    if not api_server:
        raise ValueError("Cluster Inspector requires a target config or MCP_KUBERNETES_API_SERVER")
    runtime = resolve_runtime_kubeconfig(api_server)
    if runtime is not None:
        os.environ["KUBECONFIG"] = str(runtime)
        return runtime
    if profile is None:
        raise ValueError("Config-free Cluster Inspector requires explicit MCP_KUBERNETES_AUTH_MODE")
    state_directory.mkdir(parents=True, exist_ok=True, mode=0o700)
    os.chmod(state_directory, 0o700)
    kubeconfig = materialize_kubeconfig(profile, state_directory, os.environ.get("CLUSTER_INSPECTOR_TOKEN"))
    os.environ["KUBECONFIG"] = str(kubeconfig)
    try:
        auth = json.loads((state_directory / "auth.json").read_text())
        if auth.get("mode") == "token":
            start_token_renewal(kubeconfig, profile.api_server, profile.cluster.get("ca_data"), verify_x509_strict=profile.cluster.get("verify_x509_strict", True))
    except (OSError, ValueError):
        pass
    return kubeconfig


if __name__ == "__main__":
    prepare_server_auth(PROFILE, Path(os.environ.get("CLUSTER_INSPECTOR_STATE", "/state")))
    load_database_credentials()
    hosts = ["localhost", "127.0.0.1", *[item.strip() for item in os.environ.get("MCP_ALLOWED_HOSTS", "").split(",") if item.strip()]]
    mcp.run(transport="http", host=os.environ.get("MCP_HTTP_BIND_HOST", "0.0.0.0"), port=int(os.environ.get("MCP_HTTP_PORT", "8765")), path="/mcp", stateless_http=True, host_origin_protection=True, allowed_hosts=hosts)
