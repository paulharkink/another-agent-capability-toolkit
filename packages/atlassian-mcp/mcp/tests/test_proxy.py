import asyncio
import json
from urllib.parse import parse_qs, urlsplit

import httpx
import pytest
from fastmcp.server.providers.proxy import ProxyProvider
from mcp import ClientSession
from mcp.client.streamable_http import streamable_http_client

import server
from auth import OAUTH_SCOPES


UPSTREAM_URL = "https://mcp.atlassian.com/v2/mcp"


def test_streamable_http_tools_list_does_not_wait_for_pending_oauth_and_recovers_after_callback(
    monkeypatch, tmp_path
):
    async def exercise():
        monkeypatch.setattr(server, "STATE_DIR", tmp_path)
        upstream = server.AtlassianUpstream()
        monkeypatch.setattr(server, "upstream", upstream)
        provider = next(
            provider
            for provider in server.mcp.providers
            if isinstance(provider, ProxyProvider)
        )
        monkeypatch.setattr(provider, "client_factory", upstream.proxy_client)

        requests = []

        async def respond(request):
            requests.append((request.method, str(request.url), request.headers.get("authorization")))
            if request.url == httpx.URL(UPSTREAM_URL):
                if request.headers.get("authorization") != "Bearer fixture-access":
                    return httpx.Response(
                        401,
                        headers={
                            "WWW-Authenticate": (
                                'Bearer resource_metadata="https://mcp.atlassian.com/'
                                '.well-known/oauth-protected-resource/v2/mcp"'
                            )
                        },
                        request=request,
                    )
                body = json.loads(request.content)
                method = body.get("method")
                if method == "initialize":
                    result = {
                        "protocolVersion": "2024-11-05",
                        "capabilities": {"tools": {}},
                        "serverInfo": {"name": "Atlassian fixture", "version": "1"},
                    }
                elif method == "tools/list":
                    result = {
                        "tools": [
                            {
                                "name": "fixture_search",
                                "description": "Synthetic local Atlassian search",
                                "inputSchema": {"type": "object", "properties": {}},
                            }
                        ]
                    }
                elif method == "tools/call":
                    result = {
                        "content": [{"type": "text", "text": "fixture identity"}],
                        "isError": False,
                    }
                else:
                    return httpx.Response(202, request=request)
                return httpx.Response(
                    200,
                    json={"jsonrpc": "2.0", "id": body["id"], "result": result},
                    request=request,
                )
            if request.url.host == "mcp.atlassian.com" and "oauth-protected-resource" in request.url.path:
                return httpx.Response(
                    200,
                    json={
                        "resource": UPSTREAM_URL,
                        "authorization_servers": ["https://auth.example"],
                        "scopes_supported": OAUTH_SCOPES.split(),
                    },
                    request=request,
                )
            if request.url.host == "auth.example" and "oauth-authorization-server" in request.url.path:
                return httpx.Response(
                    200,
                    json={
                        "issuer": "https://auth.example",
                        "authorization_endpoint": "https://auth.example/authorize",
                        "token_endpoint": "https://auth.example/oauth/token",
                        "client_id_metadata_document_supported": True,
                    },
                    request=request,
                )
            if str(request.url) == "https://auth.example/oauth/token":
                form = dict(parse_qs(request.content.decode()))
                assert form["grant_type"] == ["authorization_code"]
                assert form["code"] == ["fixture-code"]
                assert form["code_verifier"][0]
                return httpx.Response(
                    200,
                    json={
                        "access_token": "fixture-access",
                        "token_type": "Bearer",
                        "expires_in": 3600,
                        "refresh_token": "fixture-refresh",
                        "scope": OAUTH_SCOPES,
                    },
                    request=request,
                )
            return httpx.Response(404, request=request)

        from fastmcp.client.transports import http as fastmcp_http

        def create_mock_upstream_client(*, headers=None, timeout=None, auth=None, **_kwargs):
            return httpx.AsyncClient(
                transport=httpx.MockTransport(respond),
                headers=headers,
                timeout=timeout,
                auth=auth,
            )

        monkeypatch.setattr(fastmcp_http, "create_mcp_http_client", create_mock_upstream_client)

        async with server.app.router.lifespan_context(server.app):
            async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=server.app), base_url="http://test"
            ) as local_http:
                async def list_local_tools(timeout=1):
                    async with streamable_http_client(
                        "http://test/mcp", http_client=local_http
                    ) as (read_stream, write_stream, _):
                        async with ClientSession(read_stream, write_stream) as session:
                            await session.initialize()
                            return await asyncio.wait_for(session.list_tools(), timeout=timeout)

                authorization_url = await upstream.start_login()
                assert authorization_url
                assert upstream._client.context.lock.locked()
                pending_status = await local_http.get("/auth/status")
                assert pending_status.json() == {"login_status": "pending", "authenticated": False}

                try:
                    result = await list_local_tools(timeout=0.3)
                except asyncio.TimeoutError:
                    pytest.fail("tools/list waited on the pending Atlassian OAuth flow")
                except Exception as error:
                    message = getattr(getattr(error, "error", None), "message", "")
                    if isinstance(error, BaseExceptionGroup):
                        messages = []
                        pending = list(error.exceptions)
                        while pending:
                            child = pending.pop()
                            if isinstance(child, BaseExceptionGroup):
                                pending.extend(child.exceptions)
                            else:
                                messages.append(str(child))
                        message = " ".join(messages)
                    message = message or str(error)
                    assert "Atlassian OAuth login is pending" in message
                else:
                    pytest.fail(f"tools/list returned success while login was pending: {result!r}")

                state = parse_qs(urlsplit(authorization_url).query)["state"][0]
                callback = await local_http.get(
                    "/callback", params={"code": "fixture-code", "state": state}
                )
                assert callback.status_code == 200
                await asyncio.wait_for(upstream._login_task, timeout=2)
                assert upstream._authenticated is True
                assert not upstream._client.context.lock.locked()
                saved_state = json.loads((tmp_path / "oauth-state.json").read_text())
                assert saved_state["oauth_metadata"]["token_endpoint"] == "https://auth.example/oauth/token"

                tools = await list_local_tools(timeout=2)
                assert [tool.name for tool in tools.tools] == ["fixture_search"]
                status = await local_http.get("/auth/status")
                assert status.status_code == 200
                assert status.json()["login_status"] == "complete"

                return requests

    requests = asyncio.run(exercise())
    assert any(url == UPSTREAM_URL and auth == "Bearer fixture-access" for _, url, auth in requests)
