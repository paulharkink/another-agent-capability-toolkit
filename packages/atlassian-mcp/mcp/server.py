"""AACT-owned Atlassian OAuth wrapper and transparent MCP proxy."""

from __future__ import annotations

import asyncio
import json
import logging
import os
import sys
import urllib.request
from pathlib import Path
from typing import Awaitable, Callable

import httpx
import uvicorn
from fastmcp import Client, FastMCP
from fastmcp.server.providers.proxy import ProxyClient, ProxyProvider
from mcp.client.auth import OAuthClientProvider
from starlette.requests import Request
from starlette.responses import HTMLResponse, JSONResponse

from auth import ExplicitOAuthLogin, OAuthTokenStore, create_oauth_provider, upstream_auth

UPSTREAM_URL = "https://mcp.atlassian.com/v2/mcp"
STATE_DIR = Path(os.environ.get("AACT_AUTH_STATE_DIR", "/state"))
AUTH_MODE = os.environ.get("AACT_ATLASSIAN_AUTH_MODE", "oauth")
API_TOKEN = os.environ.get("AACT_ATLASSIAN_API_TOKEN", "")
EMAIL = os.environ.get("AACT_ATLASSIAN_EMAIL", "")
CLIENT_METADATA_URL = os.environ.get(
    "AACT_ATLASSIAN_CLIENT_METADATA_URL",
    "https://paulharkink.github.io/another-agent-capability-toolkit/oauth/client.json",
)
PORT = int(os.environ.get("AACT_ATLASSIAN_LOCAL_PORT", "8765"))
CALLBACK_PORT = int(os.environ.get("AACT_ATLASSIAN_CALLBACK_PORT", str(PORT)))
REDIRECT_URI = f"http://127.0.0.1:{CALLBACK_PORT}/callback"
HEALTH_CACHE_SECONDS = 15


async def protected_health(
    identity_check: Callable[[], Awaitable[object]],
    *,
    login_pending: bool = False,
    cached_authenticated: bool = False,
    timeout: float = 30,
) -> dict[str, bool]:
    """Health is true only after an actual protected Atlassian identity operation."""
    if login_pending:
        return {"authenticated": cached_authenticated}
    try:
        result = await asyncio.wait_for(identity_check(), timeout=timeout)
        if getattr(result, "isError", False):
            return {"authenticated": False}
        return {"authenticated": True}
    except Exception:
        return {"authenticated": False}


def observe_login_task(task: asyncio.Task) -> None:
    """Consume background task failures without logging provider response details."""
    if task.cancelled():
        return
    error = task.exception()
    if error is not None:
        logging.getLogger(__name__).warning("Atlassian OAuth login failed: %s", type(error).__name__)


class AtlassianUpstream:
    def __init__(self):
        self.callback_codes: asyncio.Queue[tuple[str | None, str | None, str | None]] = asyncio.Queue()
        self._health_until = 0.0
        self._authenticated = False
        self._login: ExplicitOAuthLogin | None = None
        self._login_task: asyncio.Task | None = None
        self._callback_used = False
        self._client: OAuthClientProvider | httpx.Auth

        if AUTH_MODE == "oauth":
            self._login = ExplicitOAuthLogin(
                client_metadata_url=CLIENT_METADATA_URL,
                redirect_uri=REDIRECT_URI,
            )
            token_store = OAuthTokenStore(STATE_DIR / "oauth-state.json")
            self._client = create_oauth_provider(
                store=token_store,
                client_metadata_url=CLIENT_METADATA_URL,
                redirect_uri=REDIRECT_URI,
                redirect_handler=self._login.redirect_handler,
                callback_handler=self._receive_callback,
                timeout=3600,
            )
        elif AUTH_MODE in {"service_account", "personal"}:
            if not API_TOKEN:
                raise ValueError("api_token is required for static Atlassian authentication")
            self._client = upstream_auth(mode=AUTH_MODE, token=API_TOKEN, email=EMAIL)
        else:
            raise ValueError(f"unsupported Atlassian authentication mode: {AUTH_MODE}")

    async def _receive_callback(self) -> tuple[str, str | None]:
        code, state, error = await self.callback_codes.get()
        if error:
            raise PermissionError("Atlassian authorization was declined or failed")
        assert code is not None
        return code, state

    def proxy_client(self) -> ProxyClient:
        return ProxyClient(UPSTREAM_URL, auth=self._client)

    async def identity(self) -> object:
        async with Client(UPSTREAM_URL, auth=self._client, timeout=30) as client:
            result = await client.call_tool("atlassianUserInfo", {})
        if getattr(result, "isError", False):
            raise PermissionError("Atlassian identity check returned an MCP error")
        return result

    async def authenticated(self) -> bool:
        login_pending = self._login_task is not None and not self._login_task.done()
        if login_pending:
            return self._authenticated
        now = asyncio.get_running_loop().time()
        if now < self._health_until:
            return self._authenticated
        result = await protected_health(self.identity)
        self._authenticated = result["authenticated"]
        self._health_until = now + HEALTH_CACHE_SECONDS
        return self._authenticated

    async def start_login(self) -> str | None:
        if AUTH_MODE != "oauth" or self._login is None:
            raise ValueError("Interactive OAuth login is available only in oauth mode")
        if await self.authenticated():
            return None
        if self._login_task and not self._login_task.done():
            return await self._login.wait_for_url(timeout=3600)
        self._login.reset()
        self._callback_used = False

        async def begin() -> None:
            async with Client(UPSTREAM_URL, auth=self._client, timeout=3600) as client:
                await client.initialize()
                result = await client.call_tool("atlassianUserInfo", {})
                if getattr(result, "isError", False):
                    raise PermissionError("Atlassian identity check returned an MCP error")
                self._authenticated = True
                self._health_until = asyncio.get_running_loop().time() + HEALTH_CACHE_SECONDS

        self._login_task = asyncio.create_task(self._login.run(begin))
        self._login_task.add_done_callback(observe_login_task)
        url_waiter = asyncio.create_task(self._login.wait_for_url(timeout=3600))
        done, _ = await asyncio.wait(
            {url_waiter, self._login_task}, timeout=60, return_when=asyncio.FIRST_COMPLETED
        )
        if url_waiter in done:
            return url_waiter.result()
        if self._login_task in done:
            url_waiter.cancel()
            await self._login_task
            raise RuntimeError("Atlassian OAuth login ended before creating an authorization URL")
        url_waiter.cancel()
        raise TimeoutError("Atlassian OAuth discovery did not produce an authorization URL in time")

    async def finish_callback(self, code: str | None, state: str | None, error: str | None = None) -> bool:
        if self._login_task is None or self._login_task.done() or self._callback_used:
            return False
        self._callback_used = True
        await self.callback_codes.put((code, state, error))
        return True


upstream = AtlassianUpstream()
mcp = FastMCP(
    "atlassian",
    instructions="Transparent, authenticated proxy to Atlassian's official MCP service.",
    providers=[ProxyProvider(upstream.proxy_client, cache_ttl=60)],
)


@mcp.custom_route("/health", methods=["GET"])
async def health(_: Request) -> JSONResponse:
    return JSONResponse({"authenticated": await upstream.authenticated()})


@mcp.custom_route("/auth/login", methods=["POST"])
async def login(_: Request) -> JSONResponse:
    try:
        authorization_url = await upstream.start_login()
        if authorization_url is None:
            return JSONResponse({"authenticated": True})
        return JSONResponse({"authorization_url": authorization_url})
    except Exception as error:
        logging.getLogger(__name__).warning("Atlassian OAuth login could not start: %s", type(error).__name__)
        return JSONResponse({"error": "Atlassian OAuth login could not start"}, status_code=502)


@mcp.custom_route("/callback", methods=["GET"])
async def callback(request: Request) -> HTMLResponse:
    codes = request.query_params.getlist("code")
    states = request.query_params.getlist("state")
    errors = request.query_params.getlist("error")
    if errors:
        accepted = await upstream.finish_callback(None, None, "authorization_error")
        if not accepted:
            return HTMLResponse("No Atlassian authorization is waiting.", status_code=400)
        return HTMLResponse("Atlassian authorization was not completed. Return to AACT.", status_code=400)
    if len(codes) != 1 or len(states) != 1:
        return HTMLResponse("Atlassian authorization was not completed. Return to AACT.", status_code=400)
    if not await upstream.finish_callback(codes[0], states[0]):
        return HTMLResponse("No Atlassian authorization is waiting.", status_code=400)
    return HTMLResponse("Atlassian authorization received. Return to AACT to confirm the connection.")


app = mcp.http_app(path="/mcp", transport="streamable-http")


def authenticate_action() -> int:
    if AUTH_MODE in {"personal", "service_account"}:
        request = urllib.request.Request(f"http://127.0.0.1:{PORT}/health", method="GET")
        try:
            with urllib.request.urlopen(request, timeout=35) as response:
                result = json.load(response)
        except Exception:
            result = {"authenticated": False}
        if result.get("authenticated") is True:
            print(json.dumps({"auth_required": False}, separators=(",", ":")))
        else:
            print(json.dumps({
                "auth_required": True,
                "diagnostic": "Atlassian user identity check failed; verify the configured authentication mode and credentials.",
            }, separators=(",", ":")))
        return 0

    request = urllib.request.Request(f"http://127.0.0.1:{PORT}/auth/login", method="POST")
    try:
        with urllib.request.urlopen(request, timeout=65) as response:
            result = json.load(response)
    except Exception:
        print(json.dumps({
            "auth_required": True,
            "diagnostic": "Atlassian OAuth login could not start; check the client metadata URL and wrapper health.",
        }, separators=(",", ":")))
        return 0
    if result.get("authenticated") is True:
        print(json.dumps({"auth_required": False}, separators=(",", ":")))
        return 0
    authorization_url = result.get("authorization_url")
    if not isinstance(authorization_url, str) or not authorization_url:
        print(json.dumps({
            "auth_required": True,
            "diagnostic": "Atlassian OAuth login did not return an authorization URL.",
        }, separators=(",", ":")))
        return 0
    print(json.dumps({"login_url": authorization_url}, separators=(",", ":")))
    return 0


def main() -> int:
    if len(sys.argv) == 3 and sys.argv[1:] == ["action", "authenticate"]:
        return authenticate_action()
    uvicorn.run(app, host="0.0.0.0", port=PORT, access_log=False)
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
