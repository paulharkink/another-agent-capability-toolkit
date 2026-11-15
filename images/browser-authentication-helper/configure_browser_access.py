#!/usr/bin/env python3
"""Apply optional browser access config before Kasm starts Chromium."""

import json
import ipaddress
import os
from pathlib import Path
import shutil
import sys
import tempfile

from browser_access import AccessConfigError, dnr_block_all_rules, dnr_navigation_rules, managed_policy, parse_access_config


BASE_POLICY = Path("/opt/aact-browser-access/base_url_policy.json")
POLICY = Path("/etc/chromium/policies/managed/urlblocklist.json")
EXTENSION_SOURCE = Path("/opt/aact-browser-access/browser_access_extension.js")
EXTENSION_DIR = Path("/opt/aact-browser-access/runtime")


def host_permission(pattern: str) -> list[str]:
    scheme, remainder = pattern.split("://", 1)
    authority = remainder.split("/", 1)[0]
    port = ""
    if authority.startswith("["):
        closing = authority.find("]")
        host = authority[:closing + 1]
        suffix = authority[closing + 1:]
        if suffix.startswith(":") and suffix[1:] != "*":
            port = suffix
    else:
        host, separator, candidate = authority.rpartition(":")
        if not separator:
            host = authority
        elif candidate != "*":
            port = f":{candidate}"
    try:
        ipaddress.ip_address(host.strip("[]"))
        is_ip_literal = True
    except ValueError:
        is_ip_literal = host.startswith("[")
    if host.startswith("."):
        hosts = [host[1:]]
    elif host == "*" or is_ip_literal:
        hosts = [host]
    else:
        hosts = [host, f"*.{host}"]
    return [f"{scheme}://{item}{port}/*" for item in hosts]


def _atomic_write(path: Path, content: bytes, mode: int = 0o644):
    path.parent.mkdir(parents=True, exist_ok=True)
    fd, temporary = tempfile.mkstemp(prefix=f".{path.name}.", dir=path.parent)
    try:
        with os.fdopen(fd, "wb") as stream:
            stream.write(content)
            stream.flush()
            os.fsync(stream.fileno())
        os.chmod(temporary, mode)
        os.replace(temporary, path)
    finally:
        try:
            os.unlink(temporary)
        except FileNotFoundError:
            pass


def _rewrite_managed_policy(content: bytes):
    # Kasm makes this file writable by uid 1000, while its parent directory is
    # root-owned. Rewrite the existing file without replacing its inode.
    if POLICY.is_symlink() or not POLICY.is_file():
        raise AccessConfigError("managed URL policy is unavailable")
    try:
        with POLICY.open("r+b") as stream:
            stream.seek(0)
            stream.write(content)
            stream.truncate()
            stream.flush()
            os.fsync(stream.fileno())
    except OSError as error:
        raise AccessConfigError("managed URL policy cannot be updated") from error


def configure(raw: str | None):
    config = parse_access_config(raw)
    try:
        base_policy = json.loads(BASE_POLICY.read_text(encoding="utf-8"))
    except (OSError, UnicodeError, json.JSONDecodeError) as error:
        raise AccessConfigError("base browser URL policy is unavailable") from error
    policy = managed_policy(config, base_policy)
    _rewrite_managed_policy((json.dumps(policy, separators=(",", ":")) + "\n").encode("utf-8"))

    EXTENSION_DIR.mkdir(parents=True, exist_ok=True)
    host_patterns = []
    limits = config.get("tab_limits") or {}
    host_patterns.extend(item["pattern"] for item in limits.get("per_pattern", []))
    host_patterns.extend(item["pattern"] for item in limits.get("popup_exemptions", []))
    host_permissions = sorted({permission for item in host_patterns for permission in host_permission(item)})
    manifest = {
        "manifest_version": 3,
        "name": "AACT Browser Access Limits",
        "version": "1.0.0",
        "permissions": ["alarms", "storage", "declarativeNetRequest"],
        "host_permissions": host_permissions,
        "background": {"service_worker": "browser_access_extension.js"},
        "action": {"default_title": "AACT browser access limits"},
        "declarative_net_request": {
            "rule_resources": [{"id": "blocked-urls", "enabled": True, "path": "blocked_urls.json"}],
        },
    }
    barrier_rules = dnr_block_all_rules(config)
    navigation_rules = dnr_navigation_rules(config)
    _atomic_write(EXTENSION_DIR / "manifest.json", (json.dumps(manifest, separators=(",", ":")) + "\n").encode())
    _atomic_write(EXTENSION_DIR / "access-config.json", (json.dumps(config, separators=(",", ":")) + "\n").encode())
    _atomic_write(EXTENSION_DIR / "blocked_urls.json", (json.dumps(barrier_rules, separators=(",", ":")) + "\n").encode())
    _atomic_write(EXTENSION_DIR / "navigation_rules.json", (json.dumps(navigation_rules, separators=(",", ":")) + "\n").encode())
    _atomic_write(EXTENSION_DIR / "browser_access_extension.js", EXTENSION_SOURCE.read_bytes())


def main() -> int:
    try:
        configure(os.environ.get("AACT_BROWSER_ACCESS_CONFIG_JSON"))
        return 0
    except AccessConfigError as error:
        print(f"browser_access_config_error={error}", file=sys.stderr)
        return 1
    except Exception as error:
        print(f"browser_access_config_error={type(error).__name__}", file=sys.stderr)
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
