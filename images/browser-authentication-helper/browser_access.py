#!/usr/bin/env python3
"""Strict validation and managed-policy generation for optional browser access."""

from __future__ import annotations

import json
import ipaddress
import re
from urllib.parse import urlsplit


MAX_CONFIG_BYTES = 64 * 1024
MAX_PATTERNS = 100
MAX_PATTERN_LENGTH = 2048
# Stay well below Chrome's compiled 2 KiB regex limit. The supported patterns
# compile to simple literals plus fixed host/port groups; 1 KiB leaves room for
# engine overhead and avoids rules being silently skipped by Chromium.
MAX_DNR_REGEX_LENGTH = 1024
MAX_TABS = 100
MAX_GRACE_SECONDS = 30


class AccessConfigError(ValueError):
    """Safe configuration error; contains no supplied credential material."""


def _pattern(value: object) -> str:
    if not isinstance(value, str) or not value or len(value) > MAX_PATTERN_LENGTH:
        raise AccessConfigError("URL pattern is invalid")
    # A deliberately small subset of Chrome URLBlocklist filters, which can be
    # mirrored by the extension matcher without broadening configured scope.
    match = re.fullmatch(r"(https?)://([^/?#@]+)(/[^?#@]*)?", value)
    if not match:
        raise AccessConfigError("URL pattern is invalid")
    _scheme, authority, path = match.groups()
    path = path or ""
    port = None
    if authority.startswith("["):
        closing = authority.find("]")
        if closing < 0:
            raise AccessConfigError("URL pattern is invalid")
        host = authority[:closing + 1]
        suffix = authority[closing + 1:]
        if suffix:
            if not suffix.startswith(":"):
                raise AccessConfigError("URL pattern is invalid")
            port = suffix[1:]
        try:
            import ipaddress
            ipaddress.IPv6Address(host[1:-1])
        except ValueError as error:
            raise AccessConfigError("URL pattern is invalid") from error
    else:
        host, separator, candidate_port = authority.rpartition(":")
        if separator:
            port = candidate_port
        else:
            host = authority
    exact = host.startswith(".")
    hostname = host[1:] if exact else host
    if not hostname:
        raise AccessConfigError("URL pattern is invalid")
    if hostname == "*":
        if exact:
            raise AccessConfigError("URL pattern is invalid")
    elif hostname.startswith("["):
        if exact:
            raise AccessConfigError("URL pattern is invalid")
    elif not re.fullmatch(r"[A-Za-z0-9](?:[A-Za-z0-9.-]*[A-Za-z0-9])?", hostname):
        raise AccessConfigError("URL pattern is invalid")
    if ".." in hostname or hostname.endswith("."):
        raise AccessConfigError("URL pattern is invalid")
    if port is not None and port != "*":
        if not port.isdecimal() or not 1 <= int(port) <= 65535:
            raise AccessConfigError("URL pattern is invalid")
    if "*" in path or any(ord(character) < 0x20 or character == " " for character in path):
        raise AccessConfigError("URL pattern is invalid")
    try:
        path.encode("ascii")
    except UnicodeEncodeError as error:
        raise AccessConfigError("URL path must use ASCII percent-encoding") from error
    return value


def url_pattern_matches(url: object, pattern: object) -> bool:
    """Match one URL using the supported Chrome URLBlocklist filter subset."""
    if not isinstance(url, str):
        return False
    try:
        _pattern(pattern)
        match = re.fullmatch(r"(https?)://([^/?#@]+)(/[^?#@]*)?", pattern)
        if not match:
            return False
        scheme, authority, path = match.groups()
        path = path or ""
        if authority.startswith("["):
            closing = authority.find("]")
            if closing < 0:
                return False
            host = authority[:closing + 1]
            suffix = authority[closing + 1:]
            port = suffix[1:] if suffix.startswith(":") else None
        else:
            host, separator, candidate_port = authority.rpartition(":")
            if separator:
                port = candidate_port
            else:
                host, port = authority, None

        parsed_url = urlsplit(url)
        actual_host = parsed_url.hostname
        if parsed_url.scheme != scheme or not actual_host or parsed_url.username or parsed_url.password:
            return False
        actual_host.encode("ascii")
        actual_host = actual_host.lower()
        host_is_exact = host.startswith(".")
        if host_is_exact:
            host = host[1:]
        if host.startswith("["):
            expected_host = host[1:-1].lower()
            if actual_host != expected_host:
                return False
        elif host == "*":
            pass
        else:
            host_is_ip = bool(re.fullmatch(r"\d+\.\d+\.\d+\.\d+", host))
            try:
                ipaddress.ip_address(host)
                host_is_ip = True
            except ValueError:
                pass
            expected_host = host.lower()
            if host_is_exact or host_is_ip:
                if actual_host != expected_host:
                    return False
            elif actual_host != expected_host and not actual_host.endswith(f".{expected_host}"):
                return False

        if port not in {None, "*"}:
            actual_port = parsed_url.port or (443 if scheme == "https" else 80)
            if actual_port != int(port):
                return False
        actual_path = parsed_url.path or "/"
        return actual_path.startswith(path or "/")
    except (AccessConfigError, UnicodeError, ValueError):
        return False


def _patterns(value: object, field: str) -> list[str]:
    if not isinstance(value, list) or len(value) > MAX_PATTERNS:
        raise AccessConfigError(f"{field} is invalid")
    return [_pattern(item) for item in value]


def _fields(value: object, allowed: set[str], label: str) -> dict:
    if not isinstance(value, dict) or set(value) - allowed:
        raise AccessConfigError(f"{label} is invalid")
    return value


def _integer(value: object, low: int, high: int, label: str) -> int:
    if isinstance(value, bool) or not isinstance(value, int) or not low <= value <= high:
        raise AccessConfigError(f"{label} is invalid")
    return value


def _dnr_pattern_filter(pattern: str) -> str:
    match = re.fullmatch(r"(https?)://([^/?#@]+)(/[^?#@]*)?", pattern)
    if not match:
        raise AccessConfigError("browser URL pattern is invalid")
    scheme, authority, path = match.groups()
    path = path or ""
    port = None
    if authority.startswith("["):
        closing = authority.find("]")
        host = authority[:closing + 1]
        suffix = authority[closing + 1:]
        port = suffix[1:] if suffix.startswith(":") else None
    else:
        host, separator, candidate = authority.rpartition(":")
        if separator:
            port = candidate
        else:
            host = authority

    exact_host = host.startswith(".")
    if exact_host:
        host = host[1:]
    if host == "*":
        host_filter = r"(?:\[[0-9A-Fa-f:.]+\]|[A-Za-z0-9.-]+)"
    elif host.startswith("["):
        host_filter = re.escape(host)
    else:
        try:
            is_ip = bool(ipaddress.ip_address(host))
        except ValueError:
            is_ip = False
        escaped = re.escape(host.lower())
        if exact_host or is_ip:
            host_filter = escaped
        else:
            host_filter = rf"(?:[A-Za-z0-9-]+\.)*{escaped}"

    if port in {None, "*"}:
        port_filter = r"(?::[0-9]+)?"
    else:
        default_port = "443" if scheme == "https" else "80"
        port_filter = rf"(?::{port})?" if port == default_port else rf":{port}"
    path_filter = re.escape(path) if path else r"(?:/|[?#]|$)"
    regex_filter = rf"^{scheme}://{host_filter}{port_filter}{path_filter}"
    if len(regex_filter.encode("utf-8")) > MAX_DNR_REGEX_LENGTH:
        raise AccessConfigError("blocked or allowed URL pattern is too large")
    return regex_filter


def dnr_block_all_rules(config: dict) -> list[dict]:
    """Static safety barrier enabled whenever the deny list is configured."""
    if not config.get("blocked_urls"):
        return []
    return [{
        "id": 1,
        "priority": 1,
        "action": {"type": "block"},
        "condition": {"regexFilter": r"^https?://", "resourceTypes": ["main_frame", "sub_frame"]},
    }]


def dnr_navigation_rules(config: dict) -> list[dict]:
    """Compile allow/deny exceptions for installation only after DNR validation."""
    if not config.get("blocked_urls"):
        return []
    rules = []
    if config.get("allowed_urls"):
        allowed_patterns = config["allowed_urls"]
        for index, pattern in enumerate(allowed_patterns, start=1001):
            rules.append({
                "id": index,
                "priority": 2,
                "action": {"type": "allow"},
                "condition": {"regexFilter": _dnr_pattern_filter(pattern), "resourceTypes": ["main_frame", "sub_frame"]},
            })
    else:
        rules.append({
            "id": 1000,
            "priority": 2,
            "action": {"type": "allow"},
            "condition": {"regexFilter": r"^https?://", "resourceTypes": ["main_frame", "sub_frame"]},
        })
    for index, pattern in enumerate(config["blocked_urls"], start=2001):
        rules.append({
            "id": index,
            "priority": 3,
            "action": {"type": "block"},
            "condition": {"regexFilter": _dnr_pattern_filter(pattern), "resourceTypes": ["main_frame", "sub_frame"]},
        })
    return rules


def dnr_rule_matches_url(rule: dict, url: str) -> bool:
    """Expose the compiled regex semantics for the synthetic contract tests."""
    return re.search(rule["condition"]["regexFilter"], url) is not None


def parse_access_config(raw: str | bytes | None) -> dict:
    if raw is None or raw == "":
        return {}
    if isinstance(raw, bytes):
        if len(raw) > MAX_CONFIG_BYTES:
            raise AccessConfigError("browser access config is too large")
        try:
            raw = raw.decode("utf-8")
        except UnicodeError as error:
            raise AccessConfigError("browser access config is invalid") from error
    if not isinstance(raw, str) or len(raw.encode("utf-8")) > MAX_CONFIG_BYTES:
        raise AccessConfigError("browser access config is too large")
    try:
        value = json.loads(raw)
    except (UnicodeError, json.JSONDecodeError) as error:
        raise AccessConfigError("browser access config must be JSON") from error
    value = _fields(value, {"allowed_urls", "blocked_urls", "popup_allowed_urls", "auto_allow_popups", "tab_limits"}, "browser access config")
    result: dict = {}
    for key in ("allowed_urls", "blocked_urls", "popup_allowed_urls"):
        if key in value:
            patterns = _patterns(value[key], key)
            if patterns:
                result[key] = patterns
    if "auto_allow_popups" in value:
        auto_allow_popups = value["auto_allow_popups"]
        if not isinstance(auto_allow_popups, bool) or (auto_allow_popups and not result.get("allowed_urls")):
            raise AccessConfigError("auto_allow_popups requires a nonempty allowed_urls list")
        result["auto_allow_popups"] = auto_allow_popups
    dnr_navigation_rules(result)
    dnr_block_all_rules(result)
    if "tab_limits" in value:
        limits = _fields(value["tab_limits"], {"max_tabs", "hard_max_tabs", "per_pattern", "popup_exemptions"}, "tab_limits")
        if "max_tabs" not in limits or "hard_max_tabs" not in limits:
            raise AccessConfigError("tab_limits is invalid")
        maximum = _integer(limits["max_tabs"], 1, MAX_TABS, "max_tabs")
        hard_maximum = _integer(limits["hard_max_tabs"], maximum, MAX_TABS, "hard_max_tabs")
        parsed_limits = {"max_tabs": maximum, "hard_max_tabs": hard_maximum}
        per_pattern = limits.get("per_pattern", [])
        if not isinstance(per_pattern, list) or len(per_pattern) > MAX_PATTERNS:
            raise AccessConfigError("per_pattern is invalid")
        parsed_per_pattern = []
        for item in per_pattern:
            item = _fields(item, {"pattern", "max_tabs"}, "per_pattern entry")
            if set(item) != {"pattern", "max_tabs"}:
                raise AccessConfigError("per_pattern entry is invalid")
            parsed_per_pattern.append({
                "pattern": _pattern(item["pattern"]),
                "max_tabs": _integer(item["max_tabs"], 1, hard_maximum, "per_pattern max_tabs"),
            })
        parsed_limits["per_pattern"] = parsed_per_pattern
        exemptions = limits.get("popup_exemptions", [])
        if not isinstance(exemptions, list) or len(exemptions) > MAX_PATTERNS:
            raise AccessConfigError("popup_exemptions is invalid")
        parsed_exemptions = []
        for item in exemptions:
            item = _fields(item, {"pattern", "max_tabs", "grace_seconds"}, "popup exemption")
            if set(item) != {"pattern", "max_tabs", "grace_seconds"}:
                raise AccessConfigError("popup exemption is invalid")
            parsed_exemptions.append({
                "pattern": _pattern(item["pattern"]),
                "max_tabs": _integer(item["max_tabs"], 1, hard_maximum, "popup max_tabs"),
                "grace_seconds": _integer(item["grace_seconds"], 0, MAX_GRACE_SECONDS, "grace_seconds"),
            })
        parsed_limits["popup_exemptions"] = parsed_exemptions
        result["tab_limits"] = parsed_limits
    return result


def managed_policy(config: dict, existing: dict | None = None) -> dict:
    """Merge configured navigation policy while preserving the base file block."""
    existing = existing or {}
    blocked = existing.get("URLBlocklist", [])
    if not isinstance(blocked, list) or not all(isinstance(item, str) for item in blocked):
        raise AccessConfigError("existing managed URL policy is invalid")
    blocked = list(dict.fromkeys([*blocked, "file://*"]))
    policy = dict(existing)
    if config.get("allowed_urls"):
        blocked.append("*")
        policy["URLAllowlist"] = config["allowed_urls"]
    policy["URLBlocklist"] = list(dict.fromkeys(blocked))
    popup_sources = list(config.get("popup_allowed_urls", []))
    if config.get("auto_allow_popups"):
        popup_sources = list(dict.fromkeys([*config.get("allowed_urls", []), *popup_sources]))
    if popup_sources:
        policy["PopupsAllowedForUrls"] = popup_sources
    return policy
