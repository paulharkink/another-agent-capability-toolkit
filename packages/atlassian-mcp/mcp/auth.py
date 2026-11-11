"""Private OAuth state and upstream authentication helpers for the wrapper."""

from __future__ import annotations

import asyncio
import contextvars
import json
import os
import tempfile
import time
from pathlib import Path
from urllib.parse import parse_qsl, urlencode, urlsplit, urlunsplit

import httpx
from mcp.client.auth import OAuthClientProvider
from mcp.shared.auth import OAuthClientInformationFull, OAuthClientMetadata, OAuthToken

OAUTH_SCOPES = (
    "read:me read:account offline_access email "
    "read:confluence:agent-interface search:confluence:agent-interface "
    "read:jira:agent-interface search:jira:agent-interface"
)


class LoginRequiredError(RuntimeError):
    """The upstream requested a new grant outside an explicit AACT login."""


class ExplicitOAuthLogin:
    """Restrict SDK consent redirects to the task started by AACT authenticate."""

    def __init__(self, *, client_metadata_url: str, redirect_uri: str):
        self.client_metadata_url = client_metadata_url
        self.redirect_uri = redirect_uri
        self.authorization_url: str | None = None
        self._explicit_login = contextvars.ContextVar("aact_explicit_atlassian_login", default=False)
        self._redirect_issued = False
        self._url_ready = asyncio.Event()

    async def run(self, operation):
        if self._explicit_login.get():
            raise RuntimeError("An Atlassian OAuth login is already in progress")
        self._redirect_issued = False
        token = self._explicit_login.set(True)
        try:
            return await operation()
        finally:
            self._explicit_login.reset(token)

    async def redirect_handler(self, url: str) -> None:
        if not self._explicit_login.get() or self._redirect_issued:
            raise LoginRequiredError(
                "Atlassian needs a new grant. Run the AACT authenticate action to continue."
            )
        self.authorization_url = constrain_authorization_url(
            url,
            client_metadata_url=self.client_metadata_url,
            redirect_uri=self.redirect_uri,
        )
        self._redirect_issued = True
        self._url_ready.set()

    async def wait_for_url(self, timeout: float = 10) -> str:
        await asyncio.wait_for(self._url_ready.wait(), timeout=timeout)
        assert self.authorization_url is not None
        return self.authorization_url

    def reset(self) -> None:
        if self._explicit_login.get():
            raise RuntimeError("Cannot reset an Atlassian OAuth login while it is running")
        self.authorization_url = None
        self._redirect_issued = False
        self._url_ready = asyncio.Event()


class OAuthTokenStore:
    """Persist SDK token/client data in a private, atomically replaced file."""

    def __init__(self, path: Path):
        self.path = Path(path)

    def _read(self) -> dict:
        try:
            return json.loads(self.path.read_text())
        except FileNotFoundError:
            return {}

    def _write(self, value: dict) -> None:
        self.path.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
        os.chmod(self.path.parent, 0o700)
        fd, temp_path = tempfile.mkstemp(prefix=f".{self.path.name}.", suffix=".tmp", dir=self.path.parent)
        try:
            os.fchmod(fd, 0o600)
            with os.fdopen(fd, "w") as stream:
                json.dump(value, stream, separators=(",", ":"))
                stream.flush()
                os.fsync(stream.fileno())
            os.replace(temp_path, self.path)
            os.chmod(self.path, 0o600)
        finally:
            try:
                os.unlink(temp_path)
            except FileNotFoundError:
                pass

    async def get_tokens(self) -> OAuthToken | None:
        item = self._read().get("tokens")
        return OAuthToken.model_validate(item) if item else None

    async def set_tokens(self, tokens: OAuthToken) -> None:
        value = self._read()
        value["tokens"] = tokens.model_dump(mode="json", exclude_none=True)
        value["expires_at"] = time.time() + tokens.expires_in if tokens.expires_in is not None else None
        self._write(value)

    def get_expiry_time(self) -> float | None:
        return self._read().get("expires_at")

    async def get_client_info(self) -> OAuthClientInformationFull | None:
        item = self._read().get("client_info")
        return OAuthClientInformationFull.model_validate(item) if item else None

    async def set_client_info(self, client_info: OAuthClientInformationFull) -> None:
        value = self._read()
        value["client_info"] = client_info.model_dump(mode="json", exclude_none=True)
        self._write(value)


def create_oauth_provider(
    *,
    store: OAuthTokenStore,
    client_metadata_url: str,
    redirect_uri: str,
    redirect_handler,
    callback_handler,
    timeout: float = 3600,
) -> OAuthClientProvider:
    """Create the shared official SDK provider and restore expiry after restart."""
    metadata = OAuthClientMetadata(
        redirect_uris=[redirect_uri],
        token_endpoint_auth_method="none",
        grant_types=["authorization_code", "refresh_token"],
        response_types=["code"],
        scope=OAUTH_SCOPES,
        client_name="AACT Atlassian MCP",
    )
    provider = OAuthClientProvider(
        server_url="https://mcp.atlassian.com/v2/mcp",
        client_metadata=metadata,
        storage=store,
        redirect_handler=redirect_handler,
        callback_handler=callback_handler,
        timeout=timeout,
        client_metadata_url=client_metadata_url,
    )
    # mcp 1.30 restores tokens but not the expiry timestamp needed to refresh them.
    provider.context.token_expiry_time = store.get_expiry_time()
    return provider


def constrain_authorization_url(
    authorization_url: str,
    *,
    client_metadata_url: str,
    redirect_uri: str,
    scopes: str = OAUTH_SCOPES,
) -> str:
    """Retain SDK PKCE/state while pinning identity, callback and approved scopes."""
    parsed = urlsplit(authorization_url)
    params = parse_qsl(parsed.query, keep_blank_values=True)
    values: dict[str, list[str]] = {}
    for key, value in params:
        values.setdefault(key, []).append(value)
    if values.get("client_id") != [client_metadata_url]:
        raise ValueError("OAuth SDK generated an unexpected client_id")
    if values.get("redirect_uri") != [redirect_uri]:
        raise ValueError("OAuth SDK generated an unexpected redirect_uri")
    if len(values.get("state", [])) != 1 or len(values.get("code_challenge", [])) != 1:
        raise ValueError("OAuth SDK authorization URL is missing unique state or PKCE challenge")
    params = [(key, value) for key, value in params if key != "scope"]
    params.append(("scope", scopes))
    return urlunsplit((parsed.scheme, parsed.netloc, parsed.path, urlencode(params), parsed.fragment))


def upstream_auth(*, mode: str, token: str, email: str = "") -> httpx.Auth:
    """Create the existing Atlassian service-account or personal auth scheme."""
    if mode == "service_account":
        class BearerAuth(httpx.Auth):
            def auth_flow(self, request: httpx.Request):
                request.headers["Authorization"] = f"Bearer {token}"
                yield request

        return BearerAuth()
    if mode == "personal":
        if not email:
            raise ValueError("email is required for personal authentication")
        return httpx.BasicAuth(email, token)
    raise ValueError(f"unsupported static authentication mode: {mode}")
