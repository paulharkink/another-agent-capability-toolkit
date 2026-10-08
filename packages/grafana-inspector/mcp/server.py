"""Read-only Grafana and Loki inspection MCP with in-memory session rotation."""

from __future__ import annotations

import os
import ssl
import threading
import time
from datetime import datetime, timezone
from typing import Any
import json
from pathlib import Path
from config import GrafanaTarget, load_target
from urllib.parse import urlparse

import requests
from fastmcp import FastMCP

TARGET_CONFIG = os.environ.get("GRAFANA_CONFIG", "/config/target.toml")
TARGET = os.environ.get("GRAFANA_TARGET", "target")
if Path(TARGET_CONFIG).is_file():
    PROFILE = load_target(Path(TARGET_CONFIG), os.environ.get("MCP_ENVIRONMENT", "default"), TARGET, Path("/state").parent.parent.parent.parent)
else:
    pod_url = os.environ.get("GRAFANA_URL", "")
    if urlparse(pod_url).scheme != "https" or not urlparse(pod_url).hostname:
        raise ValueError("Grafana Inspector requires a target TOML or HTTPS GRAFANA_URL")
    PROFILE = GrafanaTarget(pod_url.rstrip("/"), 8765, os.environ.get("GRAFANA_DATASOURCE_UID") or None, os.environ.get("GRAFANA_AUTH_MODE", "api_token"), Path("/state"), Path(TARGET_CONFIG))
GRAFANA_URL = PROFILE.url
LOKI_DATASOURCE_UID = PROFILE.datasource_uid
AUTH_PATH = Path(os.environ.get("GRAFANA_AUTH_FILE", "/state/auth.json"))
_AUTH = json.loads(AUTH_PATH.read_text()) if AUTH_PATH.exists() else {}
AUTH_MODE = os.environ.get("GRAFANA_AUTH_MODE") or _AUTH.get("auth_mode") or PROFILE.auth_mode
GRAFANA_SESSION = _AUTH.get("grafana_session", "")
GRAFANA_SESSION_EXPIRY = _AUTH.get("session_expiry", "")
OAUTH_REFRESH = _AUTH.get("oauth_refresh", "")
REFRESH_COOKIE_NAME = PROFILE.refresh_cookie_name
TOKEN_FILE = os.environ.get("GRAFANA_API_TOKEN_FILE", "")
API_TOKEN = (Path(TOKEN_FILE).read_text().strip() if TOKEN_FILE else os.environ.get("GRAFANA_API_TOKEN", "")) or _AUTH.get("api_token", "")
REQUEST_TIMEOUT_SECONDS = 15
MAX_RANGE_SECONDS = 3600
MAX_LOG_LIMIT = 1000
MAX_QUERY_LENGTH = 10_000
ROTATE_BEFORE_SECONDS = 10

mcp = FastMCP(
    "grafana-inspector",
    instructions=(
        "Read-only Grafana and Loki diagnostics for one configured target. Discover Loki "
        "datasources first, then run bounded LogQL range queries. Dashboard and datasource "
        "inspection is read-only; mutations are unavailable."
    ),
)


def _error(message: str) -> dict[str, str]:
    return {"error": message}


def _http_session() -> requests.Session:
    client = requests.Session()
    if PROFILE.ca_file is not None or PROFILE.verify_x509_strict is not None:
        ca_file = str(PROFILE.ca_file) if PROFILE.ca_file is not None else None
        client.verify = ca_file or True
        context = ssl.create_default_context(cafile=ca_file)
        strict_flag = getattr(ssl, "VERIFY_X509_STRICT", 0)
        if strict_flag:
            if PROFILE.verify_x509_strict is True:
                context.verify_flags |= strict_flag
            elif PROFILE.verify_x509_strict is False:
                context.verify_flags &= ~strict_flag

        from requests.adapters import HTTPAdapter

        class ProfileCAAdapter(HTTPAdapter):
            def __init__(self) -> None:
                self.ssl_context = context
                super().__init__()

            def init_poolmanager(self, connections: int, maxsize: int, block: bool = False, **pool_kwargs: Any) -> None:
                pool_kwargs["ssl_context"] = self.ssl_context
                super().init_poolmanager(connections, maxsize, block, **pool_kwargs)

            def proxy_manager_for(self, proxy: str, **proxy_kwargs: Any) -> Any:
                proxy_kwargs["ssl_context"] = self.ssl_context
                return super().proxy_manager_for(proxy, **proxy_kwargs)

        client.mount("https://", ProfileCAAdapter())
    return client


def _session() -> requests.Session:
    if AUTH_MODE == "api_token":
        if not API_TOKEN: raise ValueError("Grafana API token is unavailable. Restart this MCP with a token.")
        client = _http_session(); client.headers.update({"Accept": "application/json", "Authorization": f"Bearer {API_TOKEN}"}); return client
    if not GRAFANA_URL or not GRAFANA_SESSION or (REFRESH_COOKIE_NAME and not OAUTH_REFRESH):
        raise ValueError("Grafana session authentication is unavailable. Restart this MCP with the configured browser cookies.")
    parsed_url = urlparse(GRAFANA_URL)
    if parsed_url.scheme != "https" or not parsed_url.hostname:
        raise ValueError("GRAFANA_URL must be an HTTPS URL.")
    client = _http_session()
    client.headers["Accept"] = "application/json"
    client.headers["User-Agent"] = "grafana-inspector-mcp/1.0"
    cookie_path = parsed_url.path or "/"
    cookies = {"grafana_session": GRAFANA_SESSION}
    if REFRESH_COOKIE_NAME:
        cookies[REFRESH_COOKIE_NAME] = OAUTH_REFRESH
    if GRAFANA_SESSION_EXPIRY:
        cookies["grafana_session_expiry"] = GRAFANA_SESSION_EXPIRY
    for name, value in cookies.items():
        client.cookies.set(name, value, domain=parsed_url.hostname, path=cookie_path, secure=True)
    return client


CLIENT = _session()
SESSION_LOCK = threading.Lock()


def _session_expiry() -> int:
    for cookie in CLIENT.cookies:
        if cookie.name == "grafana_session_expiry":
            try:
                return int(cookie.value)
            except ValueError as error:
                raise ValueError("Grafana returned an invalid session-expiry cookie.") from error
    raise ValueError("Grafana session-expiry cookie is unavailable. Restart this MCP with fresh browser cookies.")


def _rotate_session() -> None:
    with SESSION_LOCK:
        parsed_url = urlparse(GRAFANA_URL)
        response = CLIENT.post(
            f"{GRAFANA_URL}/api/user/auth-tokens/rotate",
            data=b"",
            headers={
                "Accept": "*/*",
                "Origin": f"{parsed_url.scheme}://{parsed_url.netloc}",
                "Referer": f"{GRAFANA_URL}/",
            },
            timeout=REQUEST_TIMEOUT_SECONDS,
            allow_redirects=False,
        )
        _response_json(response)
        _session_expiry()
        _persist_refreshed_cookies()


def _persist_refreshed_cookies() -> None:
    """Persist only refreshed session fields with private file permissions."""
    if not AUTH_PATH.parent.is_dir(): return
    values = {}
    for cookie in CLIENT.cookies:
        if cookie.name in {"grafana_session", "grafana_session_expiry", REFRESH_COOKIE_NAME}:
            values[cookie.name] = cookie.value
    try:
        current = json.loads(AUTH_PATH.read_text()) if AUTH_PATH.exists() else {}
    except (OSError, ValueError): current = {}
    current.update({"grafana_session": values.get("grafana_session", GRAFANA_SESSION),
                    "session_expiry": values.get("grafana_session_expiry", GRAFANA_SESSION_EXPIRY)})
    if REFRESH_COOKIE_NAME:
        current.update({"oauth_refresh": values.get(REFRESH_COOKIE_NAME, OAUTH_REFRESH),
                        "refresh_cookie_name": REFRESH_COOKIE_NAME})
    tmp = AUTH_PATH.with_suffix(".tmp")
    fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_TRUNC, 0o600)
    try:
        with os.fdopen(fd, "w") as stream: json.dump(current, stream)
        os.chmod(tmp, 0o600); os.replace(tmp, AUTH_PATH); os.chmod(AUTH_PATH, 0o600)
    finally:
        try: tmp.unlink()
        except FileNotFoundError: pass


def _rotate_before_expiry() -> None:
    if _session_expiry() - time.time() <= ROTATE_BEFORE_SECONDS:
        _rotate_session()


def _rotation_worker() -> None:
    """Rotate once per Grafana-issued expiry; each rotation schedules the next."""
    while True:
        delay = max(0, _session_expiry() - time.time() - ROTATE_BEFORE_SECONDS)
        time.sleep(delay)
        try:
            _rotate_session()
        except (requests.RequestException, ValueError):
            # Requests return the rotation failure to callers; never leak credentials in logs.
            return


def _bootstrap_session() -> None:
    """Require Grafana to replace the copied browser session before serving MCP calls."""
    _rotate_session()
    expiry = datetime.fromtimestamp(_session_expiry(), timezone.utc).isoformat().replace("+00:00", "Z")
    print(f"Grafana {TARGET} session rotation succeeded; next rotation before {expiry}.", flush=True)


def _request(method: str, path: str, parameters: dict[str, Any] | None = None, payload: dict[str, Any] | None = None) -> Any:
    if AUTH_MODE == "session_cookie":
        _rotate_before_expiry()
    response = CLIENT.request(
        method,
        f"{GRAFANA_URL}{path}",
        params=parameters,
        json=payload,
        timeout=REQUEST_TIMEOUT_SECONDS,
        allow_redirects=False,
    )
    if AUTH_MODE == "session_cookie" and response.status_code in {401, 403}:
        _rotate_session()
        response = CLIENT.request(
            method,
            f"{GRAFANA_URL}{path}",
            params=parameters,
            json=payload,
            timeout=REQUEST_TIMEOUT_SECONDS,
            allow_redirects=False,
        )
    return _response_json(response)


def _get(path: str, parameters: dict[str, Any] | None = None) -> Any:
    return _request("GET", path, parameters)


def _validate_api_token() -> None:
    _get("/api/datasources")


def _post(path: str, parameters: dict[str, Any], payload: dict[str, Any]) -> Any:
    return _request("POST", path, parameters, payload)


def _datasource(uid: str) -> dict[str, Any]:
    for datasource in _get("/api/datasources"):
        if datasource.get("uid") == uid:
            return datasource
    raise ValueError(f"Datasource {uid} is not available.")


def _query_range(datasource_uid: str, query: dict[str, Any], start: str, end: str) -> dict[str, Any]:
    if not datasource_uid:
        return _error("No default datasource is configured for this target.")
    start_seconds = _unix_seconds(start)
    end_seconds = _unix_seconds(end)
    if end_seconds <= start_seconds:
        return _error("end must be after start.")
    if end_seconds - start_seconds > MAX_RANGE_SECONDS:
        return _error(f"Query range must not exceed {MAX_RANGE_SECONDS} seconds.")
    datasource = _datasource(datasource_uid)
    request_query = {**query, "refId": "A", "datasource": {"type": datasource["type"], "uid": datasource_uid}}
    started = time.monotonic()
    result = _post(
        "/api/ds/query",
        {"ds_type": datasource["type"], "requestId": "mcp_datasource_query"},
        {
            "from": str(start_seconds * 1000),
            "to": str(end_seconds * 1000),
            "queries": [request_query],
        },
    )
    return {
        "target": TARGET,
        "datasource": {"uid": datasource_uid, "name": datasource.get("name"), "type": datasource["type"]},
        "result": result,
        "executionMs": round((time.monotonic() - started) * 1000),
    }


def _response_json(response: requests.Response) -> Any:
    if response.status_code == 401:
        raise ValueError("Grafana rejected credentials (HTTP 401). Check the configured authentication method and credentials.")
    if response.status_code == 403:
        raise ValueError("Grafana denied access (HTTP 403); the configured identity may lack permission.")
    if response.is_redirect:
        raise ValueError("Grafana redirected to interactive login. Restart this MCP with fresh browser cookies from a successful Grafana API request.")
    if response.status_code == 404:
        raise ValueError("Grafana resource was not found.")
    response.raise_for_status()
    try:
        return response.json()
    except requests.JSONDecodeError as error:
        content_type = response.headers.get("Content-Type", "unknown")
        raise ValueError(
            f"Grafana returned non-JSON content (HTTP {response.status_code}, {content_type}). "
            "The supplied browser cookies may be expired or incomplete."
        ) from error


def _unix_seconds(value: str) -> int:
    try:
        parsed = datetime.fromisoformat(value.replace("Z", "+00:00"))
    except ValueError as error:
        raise ValueError("Time must use RFC3339 format, for example 2026-08-20T12:00:00Z.") from error
    if parsed.tzinfo is None:
        raise ValueError("Time must include a timezone.")
    return int(parsed.astimezone(timezone.utc).timestamp())


@mcp.tool()
def grafana_health() -> dict[str, Any]:
    """Verify the configured Grafana target and browser session."""
    try:
        return {"target": TARGET, "grafanaUrl": GRAFANA_URL, "health": _get("/api/health")}
    except (requests.RequestException, ValueError) as error:
        return _error(str(error))


@mcp.tool()
def list_datasources() -> dict[str, Any]:
    """List Grafana datasources visible to the authenticated browser session."""
    try:
        datasources = _get("/api/datasources")
        return {
            "target": TARGET,
            "datasources": [
                {"id": item.get("id"), "uid": item.get("uid"), "name": item.get("name"), "type": item.get("type")}
                for item in datasources
            ],
        }
    except (requests.RequestException, ValueError) as error:
        return _error(str(error))


@mcp.tool()
def search_dashboards(query: str = "") -> dict[str, Any]:
    """Search Grafana dashboards visible to the authenticated browser session."""
    try:
        results = _get("/api/search", {"query": query})
        return {
            "target": TARGET,
            "dashboards": [
                {"uid": item.get("uid"), "title": item.get("title"), "uri": item.get("uri"), "url": item.get("url")}
                for item in results
                if item.get("type") == "dash-db"
            ],
        }
    except (requests.RequestException, ValueError) as error:
        return _error(str(error))


@mcp.tool()
def get_dashboard(uid: str) -> dict[str, Any]:
    """Get one Grafana dashboard by UID."""
    try:
        if not uid:
            return _error("uid is required.")
        return {"target": TARGET, "dashboard": _get(f"/api/dashboards/uid/{uid}")}
    except (requests.RequestException, ValueError) as error:
        return _error(str(error))


@mcp.tool()
def loki_query_range(query: str, start: str, end: str, limit: int = 100, datasource_uid: str = "") -> dict[str, Any]:
    """Run a bounded read-only LogQL range query through the default or selected Loki datasource."""
    try:
        if not query:
            return _error("query is required.")
        if len(query) > MAX_QUERY_LENGTH:
            return _error(f"query must not exceed {MAX_QUERY_LENGTH} characters.")
        if not 1 <= limit <= MAX_LOG_LIMIT:
            return _error(f"limit must be between 1 and {MAX_LOG_LIMIT}.")
        selected_uid = datasource_uid or LOKI_DATASOURCE_UID
        datasource = _datasource(selected_uid)
        if datasource.get("type") != "loki":
            return _error(f"Datasource {selected_uid} is {datasource.get('type')}, not loki.")
        return _query_range(selected_uid, {"expr": query, "queryType": "range", "maxLines": limit, "intervalMs": 1_000, "maxDataPoints": 1_000}, start, end)
    except (requests.RequestException, ValueError) as error:
        return _error(str(error))


@mcp.tool()
def datasource_query_range(datasource_uid: str, query: dict[str, Any], start: str, end: str) -> dict[str, Any]:
    """Run a bounded read-only Grafana range query using datasource-native query fields."""
    try:
        if not isinstance(query, dict) or not query:
            return _error("query must be a non-empty datasource-native query object.")
        return _query_range(datasource_uid, query, start, end)
    except (requests.RequestException, ValueError) as error:
        return _error(str(error))


if __name__ == "__main__":
    if AUTH_MODE == "session_cookie":
        try:
            _bootstrap_session()
        except (requests.RequestException, ValueError) as error:
            raise SystemExit(f"Grafana target session rotation failed during startup: {error}") from error
        threading.Thread(target=_rotation_worker, name="grafana-session-rotation", daemon=True).start()
    else:
        try:
            _validate_api_token()
        except (requests.RequestException, ValueError) as error:
            raise SystemExit(f"Grafana API token validation failed: {error}") from error
    hosts = ["localhost", "127.0.0.1", *[item.strip() for item in os.environ.get("MCP_ALLOWED_HOSTS", "").split(",") if item.strip()]]
    mcp.run(transport="http", host=os.environ.get("MCP_HTTP_BIND_HOST", "0.0.0.0"), port=int(os.environ.get("MCP_HTTP_PORT", "8765")), path="/mcp", stateless_http=True, host_origin_protection=True, allowed_hosts=hosts)
