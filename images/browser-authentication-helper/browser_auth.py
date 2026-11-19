#!/usr/bin/env python3
"""Generic, read-only Chromium profile credential extraction and adapter bridge.

This module deliberately knows nothing about any website's authentication
protocol. Site-specific adapter source is supplied by the trusted host and is
run from a Linux memfd; selected profile material reaches it only on stdin.
"""

from __future__ import annotations

import base64
import hashlib
import ipaddress
import json
import logging
import os
import re
import secrets
import selectors
import socket
import sqlite3
import subprocess
import sys
import time
import urllib.error
import urllib.request
from contextlib import contextmanager
from pathlib import Path
from urllib.parse import urlsplit

ACCESS_POLICY_DIR = Path("/opt/aact-browser-access")
if ACCESS_POLICY_DIR.is_dir() and str(ACCESS_POLICY_DIR) not in sys.path:
    sys.path.insert(0, str(ACCESS_POLICY_DIR))
from browser_access import url_pattern_matches


MAX_REQUEST = 256 * 1024
MAX_RELOAD_REQUEST = 4096
MAX_ACCESS_CONFIG = 64 * 1024
MAX_ADAPTER_SOURCE = 64 * 1024
MAX_ADAPTER_OUTPUT = 64 * 1024
MAX_ADAPTER_INPUT = 4 * 1024 * 1024
MAX_CDP_RESPONSE = 1024 * 1024
MAX_CDP_PAGES = 32
CDP_RELOAD_TIMEOUT = 30
CDP_ADDRESS = "127.0.0.1"
CDP_PORT = 9222
CDP_MAGIC = "258EAFA5-E914-47DA-95CA-C5AB0DC85B11"
MAX_MATCHES = 128
MAX_VALUE_SIZE = 1024 * 1024
MAX_PROFILE_READ_ATTEMPTS = 3
MAX_LEVELDB_FILES = 2048
MAX_LEVELDB_BYTES = 512 * 1024 * 1024
MAX_LEVELDB_RECORDS = 10000
DEFAULT_CHROMIUM_ROOT = Path("/home/kasm-user/.config/chromium")
DEFAULT_PROFILE = DEFAULT_CHROMIUM_ROOT / "Default"
ACCESS_CONFIG_PATH = Path("/opt/aact-browser-access/runtime/access-config.json")


class BrowserAuthError(RuntimeError):
    """Safe, value-free error suitable for reporting to the caller."""


def _normalized_origin(origin: str) -> tuple[str, str]:
    if not isinstance(origin, str) or len(origin) > 2048:
        raise BrowserAuthError("invalid origin")
    try:
        parsed = urlsplit(origin)
        hostname = parsed.hostname
        port = parsed.port
    except ValueError as error:
        raise BrowserAuthError("invalid origin") from error
    if parsed.scheme not in {"http", "https"} or not hostname:
        raise BrowserAuthError("invalid origin")
    if parsed.username or parsed.password or parsed.query or parsed.fragment:
        raise BrowserAuthError("invalid origin")
    if parsed.path not in {"", "/"}:
        raise BrowserAuthError("origin must not include a path")
    host = hostname
    try:
        host.encode("ascii")
    except UnicodeEncodeError as error:
        # Chromium's URL parser applies UTS #46 processing, while Python's
        # casefolding can map distinct Unicode names to the same ASCII host.
        # Accept explicit ASCII punycode, but fail closed on Unicode here.
        raise BrowserAuthError("invalid origin") from error
    host = host.lower()
    host_part = f"[{host}]" if ":" in host else host
    if port == (80 if parsed.scheme.casefold() == "http" else 443):
        port = None
    if port is not None:
        normalized = f"{parsed.scheme.casefold()}://{host_part}:{port}"
    else:
        normalized = f"{parsed.scheme.casefold()}://{host_part}"
    return normalized, host


def _validate_profile_path(profile_path: Path) -> Path:
    try:
        resolved = profile_path.resolve(strict=True)
        root = DEFAULT_CHROMIUM_ROOT.resolve(strict=True)
    except (OSError, RuntimeError) as error:
        raise BrowserAuthError("Chromium profile is unavailable") from error
    if not resolved.is_dir() or not resolved.is_relative_to(root):
        raise BrowserAuthError("profile path is outside the Chromium profile root")
    return resolved


def _validate_profile_child(profile_dir: Path, child: Path, label: str) -> Path:
    try:
        relative = child.relative_to(profile_dir)
        current = profile_dir
        for component in relative.parts:
            current = current / component
            if current.is_symlink():
                raise BrowserAuthError(f"{label} contains a symbolic link")
        resolved = child.resolve(strict=True)
        root = profile_dir.resolve(strict=True)
    except BrowserAuthError:
        raise
    except ValueError as error:
        raise BrowserAuthError(f"{label} is outside the Chromium profile") from error
    except (OSError, RuntimeError) as error:
        raise BrowserAuthError(f"{label} is unavailable") from error
    if not resolved.is_relative_to(root):
        raise BrowserAuthError(f"{label} is outside the Chromium profile")
    return resolved


def _fingerprint_tree(path: Path) -> tuple[tuple[str, int, int, str], ...]:
    """Hash LevelDB inputs so a concurrent browser write fails closed."""
    entries = []
    try:
        for item in sorted(path.iterdir()):
            if item.is_symlink():
                raise BrowserAuthError("storage contains a symbolic link")
            if not item.is_file():
                continue
            if len(entries) >= MAX_LEVELDB_FILES:
                raise BrowserAuthError("sessionStorage has too many files")
            before = item.stat()
            if before.st_size > MAX_LEVELDB_BYTES:
                raise BrowserAuthError("sessionStorage file is too large")
            digest = hashlib.sha256()
            with item.open("rb") as stream:
                for chunk in iter(lambda: stream.read(128 * 1024), b""):
                    digest.update(chunk)
            after = item.stat()
            if (before.st_ino, before.st_size, before.st_mtime_ns) != (after.st_ino, after.st_size, after.st_mtime_ns):
                raise _ChangedDuringRead
            entries.append((item.name, after.st_size, after.st_mtime_ns, digest.hexdigest()))
            if sum(entry[1] for entry in entries) > MAX_LEVELDB_BYTES:
                raise BrowserAuthError("sessionStorage files are too large")
    except OSError as error:
        raise BrowserAuthError("sessionStorage is unavailable") from error
    return tuple(entries)


def open_session_store(path: Path):
    # Imported lazily so protocol validation tests do not require Chromium data.
    from ccl_chromium_reader.ccl_chromium_sessionstorage import SessionStoreDb

    return SessionStoreDb(path)


def open_local_store(path: Path):
    # Imported lazily so protocol validation tests do not require Chromium data.
    from ccl_chromium_reader.ccl_chromium_localstorage import LocalStoreDb

    return LocalStoreDb(path)


@contextmanager
def _suppress_localstorage_library_logs():
    """Hide dependency diagnostics that may include raw browser storage values."""
    logger = logging.getLogger("ccl_chromium_reader.ccl_chromium_localstorage")
    private_filter = logging.Filter()
    private_filter.filter = lambda _record: False
    logger.addFilter(private_filter)
    try:
        yield
    finally:
        logger.removeFilter(private_filter)


def _latest_records(records) -> dict[str, object]:
    latest = {}
    for record in records:
        key = getattr(record, "key", None)
        if not isinstance(key, str):
            continue
        sequence = int(getattr(record, "leveldb_sequence_number", -1))
        prior = latest.get(key)
        prior_sequence = int(getattr(prior, "leveldb_sequence_number", -1)) if prior else -1
        deleted = bool(getattr(record, "is_deleted", False))
        prior_deleted = bool(getattr(prior, "is_deleted", False)) if prior else False
        if prior is None or sequence > prior_sequence or (sequence == prior_sequence and deleted and not prior_deleted):
            latest[key] = record
    return latest


class _NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


def _cdp_targets():
    """Read the fixed loopback target list; never consult proxy variables."""
    request = urllib.request.Request(f"http://{CDP_ADDRESS}:{CDP_PORT}/json/list", method="GET")
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), _NoRedirect())
    try:
        with opener.open(request, timeout=2) as response:
            raw = response.read(MAX_CDP_RESPONSE + 1)
    except (OSError, urllib.error.URLError, urllib.error.HTTPError):
        return None
    if len(raw) > MAX_CDP_RESPONSE:
        raise BrowserAuthError("browser target list is too large")
    try:
        targets = json.loads(raw.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise BrowserAuthError("browser target list is invalid") from error
    if not isinstance(targets, list) or len(targets) > 1024:
        raise BrowserAuthError("browser target list is invalid")
    return targets


def _recv_exact(sock: socket.socket, size: int) -> bytes:
    parts = bytearray()
    while len(parts) < size:
        chunk = sock.recv(size - len(parts))
        if not chunk:
            raise BrowserAuthError("browser connection closed")
        parts.extend(chunk)
    return bytes(parts)


def _send_ws_frame(sock: socket.socket, payload: bytes, opcode: int = 1):
    if len(payload) > MAX_CDP_RESPONSE:
        raise BrowserAuthError("browser command is too large")
    mask = secrets.token_bytes(4)
    length = len(payload)
    header = bytearray([0x80 | opcode])
    if length < 126:
        header.append(0x80 | length)
    elif length <= 0xFFFF:
        header.append(0x80 | 126)
        header.extend(length.to_bytes(2, "big"))
    else:
        header.append(0x80 | 127)
        header.extend(length.to_bytes(8, "big"))
    header.extend(mask)
    masked = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
    sock.sendall(header + masked)


def _recv_ws_message(sock: socket.socket) -> bytes:
    message = bytearray()
    while True:
        first, second = _recv_exact(sock, 2)
        fin = bool(first & 0x80)
        opcode = first & 0x0F
        masked = bool(second & 0x80)
        length = second & 0x7F
        if length == 126:
            length = int.from_bytes(_recv_exact(sock, 2), "big")
        elif length == 127:
            length = int.from_bytes(_recv_exact(sock, 8), "big")
        if length > MAX_CDP_RESPONSE or len(message) + length > MAX_CDP_RESPONSE:
            raise BrowserAuthError("browser response is too large")
        mask = _recv_exact(sock, 4) if masked else b""
        payload = _recv_exact(sock, length)
        if masked:
            payload = bytes(value ^ mask[index % 4] for index, value in enumerate(payload))
        if opcode == 8:
            raise BrowserAuthError("browser connection closed")
        if opcode == 9:
            _send_ws_frame(sock, payload, opcode=10)
            continue
        if opcode in {1, 0}:
            message.extend(payload)
            if fin:
                return bytes(message)
        elif opcode not in {10}:
            raise BrowserAuthError("unsupported browser response frame")


def _websocket_cdp(
    ws_url: str,
    command: str,
    params: dict,
    *,
    wait_for_event: str | None = None,
    timeout: float = 2,
) -> dict:
    try:
        parsed = urlsplit(ws_url)
        port = parsed.port
    except ValueError as error:
        raise BrowserAuthError("invalid browser target") from error
    if (
        parsed.scheme != "ws"
        or parsed.hostname not in {CDP_ADDRESS, "localhost"}
        or port != CDP_PORT
        or parsed.username
        or parsed.password
        or parsed.query
        or parsed.fragment
        or not re.fullmatch(r"/devtools/page/[A-Za-z0-9_-]+", parsed.path)
    ):
        raise BrowserAuthError("invalid browser target")
    key = base64.b64encode(secrets.token_bytes(16)).decode("ascii")
    try:
        with socket.create_connection((CDP_ADDRESS, CDP_PORT), timeout=2) as sock:
            sock.settimeout(2)
            handshake = (
                f"GET {parsed.path} HTTP/1.1\r\n"
                f"Host: {CDP_ADDRESS}:{CDP_PORT}\r\n"
                "Upgrade: websocket\r\nConnection: Upgrade\r\n"
                f"Sec-WebSocket-Key: {key}\r\nSec-WebSocket-Version: 13\r\n\r\n"
            ).encode("ascii")
            sock.sendall(handshake)
            response = bytearray()
            while not response.endswith(b"\r\n\r\n"):
                if len(response) >= 8192:
                    raise BrowserAuthError("invalid browser handshake")
                byte = sock.recv(1)
                if not byte:
                    raise BrowserAuthError("browser handshake failed")
                response.extend(byte)
            lines = bytes(response).decode("latin1").split("\r\n")
            headers = {}
            for line in lines[1:]:
                if ":" in line:
                    name, value = line.split(":", 1)
                    headers[name.casefold()] = value.strip()
            expected = base64.b64encode(hashlib.sha1((key + CDP_MAGIC).encode("ascii")).digest()).decode("ascii")
            if not lines[0].startswith("HTTP/1.1 101 ") or headers.get("sec-websocket-accept") != expected:
                raise BrowserAuthError("browser handshake failed")
            request_id = 1
            if wait_for_event is not None:
                _send_ws_frame(sock, json.dumps({"id": request_id, "method": "Page.enable", "params": {}}, separators=(",", ":")).encode("utf-8"))
                while True:
                    try:
                        message = json.loads(_recv_ws_message(sock).decode("utf-8"))
                    except (UnicodeError, json.JSONDecodeError) as error:
                        raise BrowserAuthError("invalid browser response") from error
                    if not isinstance(message, dict) or message.get("id") != request_id:
                        continue
                    if "error" in message or not isinstance(message.get("result"), dict):
                        raise BrowserAuthError("browser command failed")
                    break
                request_id += 1
            deadline = time.monotonic() + timeout if wait_for_event is not None else None
            _send_ws_frame(sock, json.dumps({"id": request_id, "method": command, "params": params}, separators=(",", ":")).encode("utf-8"))
            result = None
            event_seen = wait_for_event is None
            while result is None or not event_seen:
                if deadline is not None:
                    remaining = deadline - time.monotonic()
                    if remaining <= 0:
                        raise BrowserAuthError("browser reload timed out")
                    sock.settimeout(remaining)
                try:
                    message = json.loads(_recv_ws_message(sock).decode("utf-8"))
                except (UnicodeError, json.JSONDecodeError) as error:
                    raise BrowserAuthError("invalid browser response") from error
                except TimeoutError as error:
                    if wait_for_event is not None:
                        raise BrowserAuthError("browser reload timed out") from error
                    raise BrowserAuthError("browser command timed out") from error
                if not isinstance(message, dict):
                    continue
                if wait_for_event is not None and message.get("method") == wait_for_event:
                    event_seen = True
                if message.get("id") == request_id:
                    if "error" in message or not isinstance(message.get("result"), dict):
                        raise BrowserAuthError("browser command failed")
                    result = message["result"]
            return result
    except BrowserAuthError:
        raise
    except (OSError, TimeoutError) as error:
        raise BrowserAuthError("browser storage query failed") from error


def _target_origin(target: dict):
    page_url = target.get("url")
    if not isinstance(page_url, str):
        return None
    parsed = urlsplit(page_url)
    if parsed.scheme not in {"http", "https"} or not parsed.netloc:
        return None
    try:
        return _normalized_origin(f"{parsed.scheme}://{parsed.netloc}")[0]
    except BrowserAuthError:
        return None


def _validate_cdp_records(raw) -> list[dict]:
    if not isinstance(raw, list) or len(raw) > MAX_MATCHES:
        raise BrowserAuthError("browser storage result is invalid")
    records = []
    total = 0
    for entry in raw:
        if not isinstance(entry, dict) or not isinstance(entry.get("key"), str) or not isinstance(entry.get("value"), str):
            raise BrowserAuthError("browser storage result is invalid")
        size = len(entry["key"].encode("utf-8")) + len(entry["value"].encode("utf-8"))
        total += size
        if size > MAX_VALUE_SIZE or total > MAX_ADAPTER_INPUT:
            raise BrowserAuthError("browser storage result is too large")
        records.append({"key": entry["key"], "value": entry["value"]})
    return records


def _cdp_records_for_target(target: dict, origin: str, name: str | list[str], match: str, kind: str):
    ws_url = target.get("webSocketDebuggerUrl")
    if not isinstance(ws_url, str):
        raise BrowserAuthError("browser target is invalid")
    if kind in {"sessionStorage", "localStorage"}:
        storage = "localStorage" if kind == "localStorage" else "sessionStorage"
        expression = (
            "(()=>{const mode=" + json.dumps(match) + ";const selected=" + json.dumps(name) + ";"
            f"const storage={storage};const out=[];for(let i=0;i<storage.length;i++){{const key=storage.key(i);"
            "if(mode==='all'||(mode==='exact'&&key===selected)||(mode==='prefix'&&key.startsWith(selected))){"
            "const value=storage.getItem(key);if(value===null||value.length>1048576||out.length>=128)"
            "return {__aact_result:'too_large'};out.push({key:key,value:value});}}return out;})()"
        )
        result = _websocket_cdp(ws_url, "Runtime.evaluate", {
            "expression": expression,
            "returnByValue": True,
            "awaitPromise": False,
            "silent": True,
        })
        remote = result.get("result")
        if result.get("exceptionDetails") or not isinstance(remote, dict) or not isinstance(remote.get("value"), list):
            raise BrowserAuthError("browser storage query failed")
        return _validate_cdp_records(remote["value"])
    names = name if isinstance(name, list) else [name]
    result = _websocket_cdp(ws_url, "Network.getCookies", {"urls": [origin]})
    cookies = result.get("cookies")
    if not isinstance(cookies, list):
        raise BrowserAuthError("browser cookie query failed")
    matches_by_name = {item: [] for item in names}
    for cookie in cookies:
        if isinstance(cookie, dict) and cookie.get("name") in matches_by_name and isinstance(cookie.get("value"), str):
            matches_by_name[cookie["name"]].append({"key": cookie["name"], "value": cookie["value"]})
    matches = []
    for cookie_name in names:
        if len(matches_by_name[cookie_name]) != 1:
            raise BrowserAuthError("cookie selector did not match exactly one cookie")
        matches.extend(matches_by_name[cookie_name])
    return _validate_cdp_records(matches)


def read_from_live_browser(*, origin: str, name: str | list[str], match: str, kind: str):
    targets = _cdp_targets()
    if targets is None:
        return None
    normalized, _ = _normalized_origin(origin)
    pages = [target for target in targets if isinstance(target, dict) and target.get("type") == "page" and _target_origin(target) == normalized]
    if not pages:
        return None
    if len(pages) > MAX_CDP_PAGES:
        raise BrowserAuthError("too many matching tabs")
    selected = [_cdp_records_for_target(page, normalized, name, match, kind) for page in pages]
    canonical = {json.dumps(records, sort_keys=True, separators=(",", ":")) for records in selected}
    if len(canonical) > 1:
        raise BrowserAuthError("multiple matching tabs have different storage values")
    return selected[0]


def _configured_reload_origin(origin: str) -> tuple[str, list[str] | None]:
    normalized, _ = _normalized_origin(origin)
    try:
        if ACCESS_CONFIG_PATH.is_symlink() or not ACCESS_CONFIG_PATH.is_file():
            raise BrowserAuthError("browser access configuration is unavailable")
        raw = ACCESS_CONFIG_PATH.read_bytes()
    except OSError as error:
        raise BrowserAuthError("browser access configuration is unavailable") from error
    if len(raw) > MAX_ACCESS_CONFIG:
        raise BrowserAuthError("browser access configuration is invalid")
    try:
        config = json.loads(raw.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise BrowserAuthError("browser access configuration is invalid") from error
    allowed_urls = config.get("allowed_urls", []) if isinstance(config, dict) else None
    if not isinstance(allowed_urls, list) or len(allowed_urls) > 100:
        raise BrowserAuthError("browser access configuration is invalid")
    return normalized, allowed_urls or None


def reload_configured_origin(origin: str) -> dict:
    """Reload one already-open page at a configured exact origin, without navigation."""
    normalized, allowed_urls = _configured_reload_origin(origin)
    targets = _cdp_targets()
    if targets is None:
        raise BrowserAuthError("browser is unavailable")
    pages = [
        target for target in targets
        if isinstance(target, dict)
        and target.get("type") == "page"
        and _target_origin(target) == normalized
    ]
    if not pages:
        return {"status": "not_found", "tabs": 0}
    if len(pages) != 1:
        return {"status": "ambiguous", "tabs": len(pages)}
    target = pages[0]
    if allowed_urls and not any(url_pattern_matches(target.get("url"), pattern) for pattern in allowed_urls):
        return {"status": "not_configured", "tabs": 0}
    ws_url = target.get("webSocketDebuggerUrl")
    if not isinstance(ws_url, str):
        raise BrowserAuthError("browser target is invalid")
    _websocket_cdp(
        ws_url,
        "Page.reload",
        {},
        wait_for_event="Page.loadEventFired",
        timeout=CDP_RELOAD_TIMEOUT,
    )
    return {"status": "reloaded", "tabs": 1}


def _read_session_storage_once(profile_dir: Path, origin: str, name: str, match: str, allow_empty: bool):
    session_dir = profile_dir / "Session Storage"
    if session_dir.is_symlink():
        raise BrowserAuthError("sessionStorage contains a symbolic link")
    if not session_dir.is_dir():
        if allow_empty:
            return []
        raise BrowserAuthError("sessionStorage is unavailable")
    _validate_profile_child(profile_dir, session_dir, "sessionStorage")
    before = _fingerprint_tree(session_dir)
    try:
        with open_session_store(session_dir) as store:
            hosts = []
            for host in store.iter_hosts():
                try:
                    normalized_host, _ = _normalized_origin(host)
                except BrowserAuthError:
                    # Chromium session storage can include extension or other
                    # non-web origins. They cannot match the requested HTTP(S)
                    # origin, so ignore them instead of aborting the read.
                    continue
                if normalized_host == origin:
                    hosts.append(host)
            if not hosts and not allow_empty:
                raise BrowserAuthError("origin is missing from sessionStorage")
            records = []
            for host in hosts:
                for record in store.iter_records_for_host(host, include_deletions=True, raise_on_no_result=False):
                    records.append(record)
                    if len(records) > MAX_LEVELDB_RECORDS:
                        raise BrowserAuthError("sessionStorage has too many records")
    except BrowserAuthError:
        raise
    except Exception as error:
        if before != _fingerprint_tree(session_dir):
            raise _ChangedDuringRead from error
        raise BrowserAuthError("sessionStorage could not be parsed safely") from error
    after = _fingerprint_tree(session_dir)
    if before != after:
        raise _ChangedDuringRead
    latest = _latest_records(records)
    result = []
    for key, record in sorted(latest.items()):
        if bool(getattr(record, "is_deleted", False)):
            continue
        if (match == "exact" and key != name) or (match == "prefix" and not key.startswith(name)):
            continue
        value = getattr(record, "value", None)
        if not isinstance(value, str) or len(value.encode("utf-8")) > MAX_VALUE_SIZE:
            continue
        result.append({"key": key, "value": value})
        if len(result) > MAX_MATCHES:
            raise BrowserAuthError("too many sessionStorage matches")
    return result


def _read_local_storage_once(profile_dir: Path, origin: str, name: str, match: str, allow_empty: bool):
    local_storage_dir = profile_dir / "Local Storage"
    storage_dir = local_storage_dir / "leveldb"
    if local_storage_dir.is_symlink() or storage_dir.is_symlink():
        raise BrowserAuthError("localStorage contains a symbolic link")
    if not storage_dir.is_dir():
        if allow_empty:
            return []
        raise BrowserAuthError("localStorage is unavailable")
    _validate_profile_child(profile_dir, local_storage_dir, "localStorage")
    _validate_profile_child(profile_dir, storage_dir, "localStorage")
    before = _fingerprint_tree(storage_dir)
    try:
        with _suppress_localstorage_library_logs():
            with open_local_store(storage_dir) as store:
                keys = []
                for storage_key in store.iter_storage_keys():
                    try:
                        normalized_key, _ = _normalized_origin(storage_key)
                    except BrowserAuthError:
                        continue
                    if normalized_key == origin:
                        keys.append(storage_key)
                if not keys and not allow_empty:
                    raise BrowserAuthError("origin is missing from localStorage")
                records = []
                for storage_key in keys:
                    for record in store.iter_records_for_storage_key(
                        storage_key, include_deletions=True, raise_on_no_result=False
                    ):
                        records.append(record)
                        if len(records) > MAX_LEVELDB_RECORDS:
                            raise BrowserAuthError("localStorage has too many records")
    except BrowserAuthError:
        raise
    except Exception as error:
        if before != _fingerprint_tree(storage_dir):
            raise _ChangedDuringRead from error
        raise BrowserAuthError("localStorage could not be parsed safely") from error
    after = _fingerprint_tree(storage_dir)
    if before != after:
        raise _ChangedDuringRead
    latest = {}
    for record in records:
        key = getattr(record, "script_key", None)
        if not isinstance(key, str):
            continue
        sequence = int(getattr(record, "leveldb_seq_number", -1))
        prior = latest.get(key)
        prior_sequence = int(getattr(prior, "leveldb_seq_number", -1)) if prior else -1
        if prior is None or sequence >= prior_sequence:
            latest[key] = record
    result = []
    for key, record in sorted(latest.items()):
        if not bool(getattr(record, "is_live", True)):
            continue
        if (match == "exact" and key != name) or (match == "prefix" and not key.startswith(name)):
            continue
        value = getattr(record, "value", None)
        if not isinstance(value, str) or len(value.encode("utf-8")) > MAX_VALUE_SIZE:
            continue
        result.append({"key": key, "value": value})
        if len(result) > MAX_MATCHES:
            raise BrowserAuthError("too many localStorage matches")
    return result


def read_local_storage(
    profile_path: Path, *, origin: str, name: str, match: str = "exact", allow_empty: bool = False, offline: bool = False
):
    normalized_origin, _ = _normalized_origin(origin)
    if not isinstance(match, str) or not isinstance(name, str) or (not name and match != "all") or len(name) > 512:
        raise BrowserAuthError("invalid selector name")
    if match not in {"exact", "prefix", "all"}:
        raise BrowserAuthError("invalid selector match mode")
    profile_dir = _validate_profile_path(profile_path)
    if not offline:
        live = read_from_live_browser(origin=normalized_origin, name=name, match=match, kind="localStorage")
        if live is not None:
            if not live and not allow_empty:
                raise BrowserAuthError("selector did not match localStorage")
            return live
    for attempt in range(MAX_PROFILE_READ_ATTEMPTS):
        try:
            result = _read_local_storage_once(profile_dir, normalized_origin, name, match, allow_empty)
        except _ChangedDuringRead:
            if attempt + 1 == MAX_PROFILE_READ_ATTEMPTS:
                raise BrowserAuthError("localStorage changed during read")
            continue
        if not result and not allow_empty:
            raise BrowserAuthError("selector did not match localStorage")
        return result
    raise BrowserAuthError("localStorage changed during read")


class _ChangedDuringRead(Exception):
    pass


def read_session_storage(profile_path: Path, *, origin: str, name: str, match: str = "exact", allow_empty: bool = False, offline: bool = False):
    normalized_origin, _ = _normalized_origin(origin)
    if not isinstance(match, str) or not isinstance(name, str) or (not name and match != "all") or len(name) > 512:
        raise BrowserAuthError("invalid selector name")
    if match not in {"exact", "prefix", "all"}:
        raise BrowserAuthError("invalid selector match mode")
    profile_dir = _validate_profile_path(profile_path)
    if not offline:
        live = read_from_live_browser(origin=normalized_origin, name=name, match=match, kind="sessionStorage")
        if live is not None:
            if not live and not allow_empty:
                raise BrowserAuthError("selector did not match sessionStorage")
            return live
    for attempt in range(MAX_PROFILE_READ_ATTEMPTS):
        try:
            result = _read_session_storage_once(profile_dir, normalized_origin, name, match, allow_empty)
        except _ChangedDuringRead:
            if attempt + 1 == MAX_PROFILE_READ_ATTEMPTS:
                raise BrowserAuthError("sessionStorage changed during read")
            continue
        if not result and not allow_empty:
            raise BrowserAuthError("selector did not match sessionStorage")
        return result
    raise BrowserAuthError("sessionStorage changed during read")


def _cookie_db(profile_dir: Path) -> Path:
    candidates = (profile_dir / "Network" / "Cookies", profile_dir / "Cookies")
    for candidate in candidates:
        if candidate.is_symlink():
            raise BrowserAuthError("cookie database contains a symbolic link")
        if candidate.is_file():
            return _validate_profile_child(profile_dir, candidate, "cookie database")
    raise BrowserAuthError("cookie database is unavailable")


def read_cookie_rows(profile_dir: Path, *, host: str, name: str | None = None, names: list[str] | None = None):
    path = _cookie_db(profile_dir)
    names = names if names is not None else [name]
    if not names or any(not isinstance(item, str) or not item for item in names):
        raise BrowserAuthError("invalid selector name")
    try:
        try:
            ipaddress.ip_address(host)
            domains = {host}
        except ValueError:
            labels = host.split(".")
            domains = set()
            for index in range(len(labels)):
                domain = ".".join(labels[index:])
                domains.add(domain)
                domains.add("." + domain)
        placeholders = ",".join("?" for _ in domains)
        wal_path = path.with_name(path.name + "-wal")
        if wal_path.is_symlink():
            raise BrowserAuthError("cookie database contains a symbolic link")
        db_before = path.stat()
        wal_before = wal_path.stat() if wal_path.exists() else None
        uri = path.as_uri() + "?mode=ro"
        connection = sqlite3.connect(uri, uri=True, timeout=1, isolation_level=None)
        try:
            connection.execute("PRAGMA query_only=ON")
            connection.execute("BEGIN")
            rows = connection.execute(
                f"SELECT host_key, name, value, encrypted_value, expires_utc, is_secure FROM cookies "
                f"WHERE name IN ({','.join('?' for _ in names)}) AND host_key IN ({placeholders})",
                [*names, *sorted(domains)],
            ).fetchmany(MAX_MATCHES + 1)
            if len(rows) > MAX_MATCHES:
                raise BrowserAuthError("cookie selector matched too many entries")
            connection.execute("ROLLBACK")
            db_after = path.stat()
            wal_after = wal_path.stat() if wal_path.exists() else None
            if (db_before.st_ino, db_before.st_size, db_before.st_mtime_ns) != (db_after.st_ino, db_after.st_size, db_after.st_mtime_ns):
                raise BrowserAuthError("cookie database changed during read")
            if (wal_before is None) != (wal_after is None) or (wal_before and wal_after and
                    (wal_before.st_ino, wal_before.st_size, wal_before.st_mtime_ns) != (wal_after.st_ino, wal_after.st_size, wal_after.st_mtime_ns)):
                raise BrowserAuthError("cookie database changed during read")
            return [
                {
                    "host_key": row[0],
                    "name": row[1],
                    "value": row[2],
                    "encrypted_value": row[3],
                    "expires_utc": row[4],
                    "is_secure": bool(row[5]),
                }
                for row in rows
            ]
        finally:
            connection.close()
    except BrowserAuthError:
        raise
    except (sqlite3.Error, OSError) as error:
        raise BrowserAuthError("cookie database could not be read safely") from error


def _decrypt_linux_v10(encrypted: bytes, host_key: str) -> str:
    if not encrypted.startswith(b"v10"):
        raise BrowserAuthError("unsupported encrypted cookie format")
    try:
        from cryptography.hazmat.primitives import hashes, padding
        from cryptography.hazmat.primitives.ciphers import Cipher, algorithms, modes
        from cryptography.hazmat.primitives.kdf.pbkdf2 import PBKDF2HMAC

        key = PBKDF2HMAC(algorithm=hashes.SHA1(), length=16, salt=b"saltysalt", iterations=1).derive(b"peanuts")
        payload = encrypted[3:]
        if not payload or len(payload) % 16:
            raise BrowserAuthError("encrypted cookie is malformed")
        decryptor = Cipher(algorithms.AES(key), modes.CBC(b" " * 16)).decryptor()
        unpadder = padding.PKCS7(128).unpadder()
        clear = unpadder.update(decryptor.update(payload) + decryptor.finalize()) + unpadder.finalize()
        domain_hash = hashlib.sha256(host_key.encode("utf-8")).digest()
        if clear.startswith(domain_hash):
            clear = clear[len(domain_hash):]
        return clear.decode("utf-8")
    except BrowserAuthError:
        raise
    except Exception as error:
        raise BrowserAuthError("encrypted cookie could not be decrypted") from error


def read_cookies(profile_path: Path, *, origin: str, names: list[str], offline: bool = False):
    _, host = _normalized_origin(origin)
    if not isinstance(names, list) or not 1 <= len(names) <= MAX_MATCHES or any(
        not isinstance(name, str) or not name or len(name) > 512 for name in names
    ) or len(set(names)) != len(names):
        raise BrowserAuthError("invalid selector name")
    profile_dir = _validate_profile_path(profile_path)
    if not offline:
        live = read_from_live_browser(origin=origin, name=names, match="exact", kind="cookie")
        if live is not None:
            if [item.get("key") for item in live] != names:
                raise BrowserAuthError("cookie selector did not match exactly one cookie")
            return live
    rows = None
    for attempt in range(MAX_PROFILE_READ_ATTEMPTS):
        try:
            rows = read_cookie_rows(profile_dir, host=host, names=names)
            break
        except BrowserAuthError as error:
            if "changed during read" not in str(error) or attempt + 1 == MAX_PROFILE_READ_ATTEMPTS:
                raise
    if rows is None:
        raise BrowserAuthError("cookie database changed during read")
    matches_by_name = {name: [] for name in names}
    for row in rows:
        host_key = str(row.get("host_key", ""))
        cookie_host = host_key.lstrip(".").casefold().rstrip(".")
        if host != cookie_host and not (host_key.startswith(".") and host.endswith("." + cookie_host)):
            continue
        if row.get("name") not in matches_by_name:
            continue
        expires_utc = int(row.get("expires_utc", 0) or 0)
        if expires_utc and expires_utc / 1_000_000 - 11_644_473_600 <= time.time():
            continue
        if row.get("is_secure") and not origin.casefold().startswith("https://"):
            continue
        encrypted = row.get("encrypted_value") or b""
        value = row.get("value") or ""
        if encrypted:
            value = _decrypt_linux_v10(bytes(encrypted), str(row.get("host_key", "")))
        if not isinstance(value, str) or not value:
            continue
        matches_by_name[row["name"]].append({"key": row["name"], "value": value})
    if any(len(matches_by_name[name]) != 1 for name in names):
        raise BrowserAuthError("cookie selector did not match exactly one cookie")
    return [matches_by_name[name][0] for name in names]


def read_cookie(profile_path: Path, *, origin: str, name: str):
    return read_cookies(profile_path, origin=origin, names=[name])


def parse_adapter_output(output: bytes) -> dict:
    if len(output) > MAX_ADAPTER_OUTPUT:
        raise BrowserAuthError("adapter output is too large")
    try:
        result = json.loads(output.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise BrowserAuthError("adapter did not return valid JSON") from error
    if not isinstance(result, dict):
        raise BrowserAuthError("adapter output must be a JSON object")
    return result


def execute_adapter_for_test(*, source: str, config: dict, credential: list[dict]):
    """Run a trusted `def run(credentials, config)` adapter from a memfd.

    The only credential-bearing channel is the child's stdin. Adapter source is
    non-secret and inherited from a memfd so it never needs a disk file.
    """
    if not isinstance(source, str) or not source or len(source.encode("utf-8")) > MAX_ADAPTER_SOURCE:
        raise BrowserAuthError("adapter source is invalid")
    if sys.platform != "linux" or not hasattr(os, "memfd_create"):
        raise BrowserAuthError("in-memory adapter execution is unavailable")
    fd = os.memfd_create("aact-site-adapter", flags=0)
    try:
        os.write(fd, source.encode("utf-8"))
        child_input = json.dumps({"config": config, "credential": credential}, separators=(",", ":")).encode("utf-8")
        if len(child_input) > MAX_ADAPTER_INPUT:
            raise BrowserAuthError("adapter input is too large")
        bootstrap = (
            "import json,runpy,sys\n"
            "request=json.loads(sys.stdin.buffer.read())\n"
            "module=runpy.run_path(sys.argv[1])\n"
            "result=module['run'](request['credential'],request['config'])\n"
            "sys.stdout.write(json.dumps(result,separators=(',',':')))\n"
        )
        process = subprocess.Popen(
            [sys.executable, "-c", bootstrap, f"/proc/self/fd/{fd}"],
            stdin=subprocess.PIPE,
            stdout=subprocess.PIPE,
            stderr=subprocess.DEVNULL,
            pass_fds=(fd,),
        )
        return _communicate_bounded(process, child_input)
    except OSError as error:
        raise BrowserAuthError("adapter could not be started") from error
    finally:
        os.close(fd)


class SimpleProcessResult:
    def __init__(self, returncode, stdout, stderr):
        self.returncode = returncode
        self.stdout = stdout
        self.stderr = stderr


def _communicate_bounded(process, child_input: bytes):
    selector = selectors.DefaultSelector()
    output = bytearray()
    offset = 0
    deadline = time.monotonic() + 30
    try:
        os.set_blocking(process.stdin.fileno(), False)
        os.set_blocking(process.stdout.fileno(), False)
        selector.register(process.stdout, selectors.EVENT_READ, "stdout")
        selector.register(process.stdin, selectors.EVENT_WRITE, "stdin")
        while selector.get_map():
            remaining = deadline - time.monotonic()
            if remaining <= 0:
                raise BrowserAuthError("adapter timed out")
            for key, _ in selector.select(min(remaining, 0.5)):
                if key.data == "stdin":
                    try:
                        written = os.write(key.fd, child_input[offset:offset + 16384])
                    except BrokenPipeError:
                        written = 0
                    offset += written
                    if offset >= len(child_input) or written == 0:
                        selector.unregister(key.fileobj)
                        key.fileobj.close()
                else:
                    try:
                        chunk = os.read(key.fd, 16384)
                    except BlockingIOError:
                        continue
                    if not chunk:
                        selector.unregister(key.fileobj)
                        key.fileobj.close()
                        continue
                    output.extend(chunk)
                    if len(output) > MAX_ADAPTER_OUTPUT:
                        raise BrowserAuthError("adapter output is too large")
        return SimpleProcessResult(process.wait(timeout=max(0, deadline - time.monotonic())), bytes(output), b"")
    except Exception:
        process.kill()
        process.wait()
        raise
    finally:
        selector.close()


def run_adapter(source: str, config: dict, credential: list[dict]) -> dict:
    process = execute_adapter_for_test(source=source, config=config, credential=credential)
    if process.returncode != 0:
        raise BrowserAuthError("adapter failed")
    return parse_adapter_output(process.stdout)


def parse_request(raw: bytes) -> dict:
    if len(raw) > MAX_REQUEST:
        raise BrowserAuthError("request is too large")
    try:
        request = json.loads(raw.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise BrowserAuthError("request must be JSON") from error
    if not isinstance(request, dict):
        raise BrowserAuthError("request must be a JSON object")
    allowed = {"profile_path", "origin", "selector", "adapter"}
    if set(request) != allowed:
        raise BrowserAuthError("request fields are invalid")
    selector = request["selector"]
    adapter = request["adapter"]
    if not isinstance(selector, dict) or set(selector) - {"kind", "name", "names", "match", "allow_empty"} or "kind" not in selector:
        raise BrowserAuthError("selector is invalid")
    if "allow_empty" in selector and not isinstance(selector["allow_empty"], bool):
        raise BrowserAuthError("selector is invalid")
    if "match" in selector and not isinstance(selector["match"], str):
        raise BrowserAuthError("selector is invalid")
    if selector["kind"] == "none":
        if set(selector) != {"kind"}:
            raise BrowserAuthError("selector is invalid")
    elif selector["kind"] == "cookie":
        names = selector.get("names", [selector.get("name")])
        if ("name" in selector and "names" in selector) or not isinstance(names, list) or not 1 <= len(names) <= MAX_MATCHES or any(
            not isinstance(name, str) or not name or len(name) > 512 for name in names
        ) or len(set(names)) != len(names) or selector.get("match", "exact") != "exact" or selector.get("allow_empty", False):
            raise BrowserAuthError("selector is invalid")
    elif selector["kind"] == "sessionStorage":
        name = selector.get("name", "")
        match = selector.get("match", "exact")
        if (
            "names" in selector
            or not isinstance(name, str)
            or len(name) > 512
            or match not in {"exact", "prefix", "all"}
            or (not name and match != "all")
        ):
            raise BrowserAuthError("selector is invalid")
    elif selector["kind"] == "localStorage":
        name = selector.get("name", "")
        match = selector.get("match", "exact")
        if (
            "names" in selector
            or not isinstance(name, str)
            or len(name) > 512
            or match not in {"exact", "prefix", "all"}
            or (not name and match != "all")
        ):
            raise BrowserAuthError("selector is invalid")
    else:
        raise BrowserAuthError("selector is invalid")
    if not isinstance(adapter, dict) or set(adapter) != {"source", "config"} or not isinstance(adapter["config"], dict):
        raise BrowserAuthError("adapter request is invalid")
    return request


def parse_reload_request(raw: bytes) -> dict:
    if len(raw) > MAX_RELOAD_REQUEST:
        raise BrowserAuthError("reload request is too large")
    try:
        request = json.loads(raw.decode("utf-8"))
    except (UnicodeError, json.JSONDecodeError) as error:
        raise BrowserAuthError("reload request must be JSON") from error
    if not isinstance(request, dict) or set(request) != {"origin"} or not isinstance(request.get("origin"), str):
        raise BrowserAuthError("reload request fields are invalid")
    return request


def require_read_only_profile(profile_path: Path):
    """Refuse the standalone extraction mode unless the profile mount is read-only."""
    try:
        flags = os.statvfs(profile_path).f_flag
    except OSError as error:
        raise BrowserAuthError("read-only profile mount is unavailable") from error
    if not flags & getattr(os, "ST_RDONLY", 1):
        raise BrowserAuthError("offline extraction requires a read-only profile mount")


def main() -> int:
    try:
        command = sys.argv[1:]
        if command == ["reload"]:
            request = parse_reload_request(sys.stdin.buffer.read(MAX_RELOAD_REQUEST + 1))
            result = reload_configured_origin(request["origin"])
            sys.stdout.write(json.dumps(result, separators=(",", ":")) + "\n")
            return 0
        if command not in (["run-adapter"], ["run-adapter-offline"]):
            raise BrowserAuthError("expected run-adapter, run-adapter-offline or reload command")
        raw = sys.stdin.buffer.read(MAX_REQUEST + 1)
        request = parse_request(raw)
        profile_path = Path(request.get("profile_path") or DEFAULT_PROFILE)
        offline = command == ["run-adapter-offline"]
        if offline:
            require_read_only_profile(profile_path)
        selector = request["selector"]
        kind = selector.get("kind")
        if kind == "none":
            credential = []
        elif kind == "sessionStorage":
            credential = read_session_storage(
                profile_path,
                origin=request["origin"],
                name=selector.get("name", ""),
                match=selector.get("match", "exact"),
                allow_empty=selector.get("allow_empty", False),
                offline=offline,
            )
        elif kind == "localStorage":
            credential = read_local_storage(
                profile_path,
                origin=request["origin"],
                name=selector.get("name", ""),
                match=selector.get("match", "exact"),
                allow_empty=selector.get("allow_empty", False),
                offline=offline,
            )
        elif kind == "cookie":
            credential = read_cookies(
                profile_path,
                origin=request["origin"],
                names=selector.get("names", [selector.get("name")]),
                offline=offline,
            )
        else:
            raise BrowserAuthError("unsupported selector kind")
        result = run_adapter(request["adapter"]["source"], request["adapter"]["config"], credential)
        sys.stdout.write(json.dumps(result, separators=(",", ":")))
        sys.stdout.write("\n")
        return 0
    except BrowserAuthError as error:
        sys.stderr.write(f"browser_auth_error={error}\n")
        return 1
    except Exception as error:
        # Never include exception text: libraries may put values in messages.
        sys.stderr.write(f"browser_auth_error={type(error).__name__}\n")
        return 1


if __name__ == "__main__":
    raise SystemExit(main())
