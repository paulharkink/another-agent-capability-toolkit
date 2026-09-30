from dataclasses import dataclass
from pathlib import Path
import re
import tomllib

@dataclass(frozen=True)
class GrafanaTarget:
    url: str
    local_port: int
    datasource_uid: str | None
    auth_mode: str
    state_directory: Path
    config_path: Path
    refresh_cookie_name: str = ""
    ca_file: Path | None = None
    verify_x509_strict: bool | None = None
    @property
    def auth_file(self) -> Path:
        """Credential JSON file used by the Grafana launcher and server."""
        return self.state_directory / "auth.json"

def state_directory(root: Path, environment: str, target: str) -> Path:
    for value in (environment, target):
        if not re.fullmatch(r"[a-z0-9]+(?:-[a-z0-9]+)*", value): raise ValueError("Invalid target identifier.")
    return Path(root) / "agent-skills" / "grafana-inspector" / environment / target

def load_target(path: Path, environment: str, target: str, state_home: Path | None = None) -> GrafanaTarget:
    data = tomllib.loads(Path(path).read_text())
    port = data.get("mcp", {}).get("local_port")
    cfg = data.get("grafana", {})
    url, uid, mode = cfg.get("url"), cfg.get("datasource_uid", cfg.get("loki_datasource_uid")), cfg.get("auth_mode")
    if not isinstance(port, int) or not 1024 <= port <= 65535: raise ValueError("[mcp].local_port must be between 1024 and 65535.")
    if not isinstance(url, str) or not re.fullmatch(r"https://[^\s]+", url): raise ValueError("[grafana].url must be an HTTPS URL.")
    if uid is not None and (not isinstance(uid, str) or not uid.strip()): raise ValueError("[grafana].datasource_uid must be a non-empty string when provided.")
    if mode not in {"api_token", "session_cookie"}: raise ValueError("[grafana].auth_mode must be api_token or session_cookie.")
    base = Path(state_home or Path.home() / ".local" / "state")
    refresh_name = cfg.get("refresh_cookie_name", "")
    if not isinstance(refresh_name, str): raise ValueError("[grafana].refresh_cookie_name must be a string.")
    verify_x509_strict = cfg.get("verify_x509_strict")
    if verify_x509_strict is not None and not isinstance(verify_x509_strict, bool): raise ValueError("[grafana].verify_x509_strict must be a boolean when provided.")
    ca_value = cfg.get("ca_file")
    ca_file = None
    if ca_value is not None:
        if not isinstance(ca_value, str) or not ca_value.strip(): raise ValueError("[grafana].ca_file must be a non-empty path.")
        configured_ca = Path(ca_value)
        if configured_ca.is_absolute(): raise ValueError("[grafana].ca_file must be relative to the target TOML.")
        target_directory = Path(path).resolve().parent
        ca_file = (target_directory / configured_ca).resolve()
        if not ca_file.is_relative_to(target_directory): raise ValueError("[grafana].ca_file must stay within the target TOML directory.")
        if not ca_file.is_file(): raise ValueError("[grafana].ca_file must point to a readable certificate file.")
    return GrafanaTarget(url.rstrip("/"), port, uid, mode, state_directory(base, environment, target), Path(path).resolve(), refresh_name, ca_file, verify_x509_strict)
