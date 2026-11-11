import asyncio
import json
import time
from urllib.parse import parse_qs, urlsplit

import httpx
import pytest
from mcp.client.auth import OAuthClientProvider
from mcp.shared.auth import OAuthClientInformationFull, OAuthClientMetadata, OAuthMetadata, OAuthToken

from auth import (
    OAUTH_SCOPES,
    ExplicitOAuthLogin,
    LoginRequiredError,
    OAuthTokenStore,
    constrain_authorization_url,
    create_oauth_provider,
    upstream_auth,
)


CLIENT_METADATA_URL = "https://paulharkink.github.io/another-agent-capability-toolkit/oauth/client.json"


def test_oauth_store_round_trips_private_token_and_client_info(tmp_path):
    path = tmp_path / "private" / "oauth-state.json"
    store = OAuthTokenStore(path)

    async def exercise():
        token = OAuthToken(
            access_token="access-fixture",
            token_type="Bearer",
            expires_in=3600,
            refresh_token="refresh-fixture",
            scope=OAUTH_SCOPES,
        )
        client = OAuthClientInformationFull(
            client_id=CLIENT_METADATA_URL,
            redirect_uris=["http://127.0.0.1:8765/callback"],
        )
        await store.set_tokens(token)
        await store.set_client_info(client)
        return await store.get_tokens(), await store.get_client_info()

    token, client = asyncio.run(exercise())

    assert token.access_token == "access-fixture"
    assert token.refresh_token == "refresh-fixture"
    assert client.client_id == CLIENT_METADATA_URL
    assert path.stat().st_mode & 0o777 == 0o600
    assert path.parent.stat().st_mode & 0o777 == 0o700
    assert not list(path.parent.glob("*.tmp"))
    assert json.loads(path.read_text())["tokens"]["access_token"] == "access-fixture"


def test_oauth_store_preserves_absolute_expiry_for_process_restart(tmp_path):
    store = OAuthTokenStore(tmp_path / "oauth-state.json")
    before = time.time()

    asyncio.run(
        store.set_tokens(
            OAuthToken(
                access_token="access-fixture",
                token_type="Bearer",
                expires_in=3600,
                refresh_token="refresh-fixture",
                scope=OAUTH_SCOPES,
            )
        )
    )

    assert before + 3590 <= store.get_expiry_time() <= time.time() + 3600


def test_parallel_expired_requests_refresh_once_and_persist_rotated_token(tmp_path):
    async def exercise():
        store = OAuthTokenStore(tmp_path / "oauth-state.json")
        await store.set_tokens(
            OAuthToken(
                access_token="expired-access",
                token_type="Bearer",
                expires_in=-1,
                refresh_token="old-refresh",
                scope=OAUTH_SCOPES,
            )
        )
        await store.set_client_info(
            OAuthClientInformationFull(
                client_id=CLIENT_METADATA_URL,
                redirect_uris=["http://127.0.0.1:8765/callback"],
                token_endpoint_auth_method="none",
            )
        )
        provider = create_oauth_provider(
            store=store,
            client_metadata_url=CLIENT_METADATA_URL,
            redirect_uri="http://127.0.0.1:8765/callback",
            redirect_handler=None,
            callback_handler=None,
        )
        calls = {"refresh": 0, "resource": 0}

        async def respond(request):
            if request.url.path == "/token":
                calls["refresh"] += 1
                await asyncio.sleep(0.02)
                return httpx.Response(
                    200,
                    json={
                        "access_token": "rotated-access",
                        "token_type": "Bearer",
                        "expires_in": 3600,
                        "refresh_token": "rotated-refresh",
                        "scope": OAUTH_SCOPES,
                    },
                    request=request,
                )
            calls["resource"] += 1
            assert request.headers.get("authorization") == "Bearer rotated-access"
            return httpx.Response(200, request=request)

        async with httpx.AsyncClient(auth=provider, transport=httpx.MockTransport(respond)) as client:
            await asyncio.gather(
                client.get("https://mcp.atlassian.com/v2/mcp"),
                client.get("https://mcp.atlassian.com/v2/mcp"),
            )
        token = await store.get_tokens()
        return calls, token

    calls, token = asyncio.run(exercise())
    assert calls == {"refresh": 1, "resource": 2}
    assert token.access_token == "rotated-access"
    assert token.refresh_token == "rotated-refresh"


def test_insufficient_scope_does_not_start_a_second_consent_or_clear_working_token(tmp_path):
    async def exercise():
        store = OAuthTokenStore(tmp_path / "oauth-state.json")
        working_token = OAuthToken(
            access_token="working-confluence-token",
            token_type="Bearer",
            expires_in=3600,
            refresh_token="working-refresh-token",
            scope=OAUTH_SCOPES,
        )
        await store.set_tokens(working_token)
        await store.set_client_info(
            OAuthClientInformationFull(
                client_id=CLIENT_METADATA_URL,
                redirect_uris=["http://127.0.0.1:8765/callback"],
                token_endpoint_auth_method="none",
            )
        )
        login = ExplicitOAuthLogin(
            client_metadata_url=CLIENT_METADATA_URL,
            redirect_uri="http://127.0.0.1:8765/callback",
        )

        async def callback_handler():
            raise AssertionError("ordinary scope failures must not wait for a callback")

        provider = create_oauth_provider(
            store=store,
            client_metadata_url=CLIENT_METADATA_URL,
            redirect_uri="http://127.0.0.1:8765/callback",
            redirect_handler=login.redirect_handler,
            callback_handler=callback_handler,
        )
        provider.context.oauth_metadata = OAuthMetadata(
            issuer="https://auth.example",
            authorization_endpoint="https://auth.example/authorize",
            token_endpoint="https://auth.example/token",
        )
        calls = []

        async def respond(request):
            calls.append(request)
            return httpx.Response(
                403,
                headers={"WWW-Authenticate": 'Bearer error="insufficient_scope"'},
                request=request,
            )

        async with httpx.AsyncClient(auth=provider, transport=httpx.MockTransport(respond)) as client:
            with pytest.raises(LoginRequiredError, match="AACT authenticate"):
                await client.get("https://mcp.atlassian.com/v2/mcp")
        stored = await store.get_tokens()
        return calls, stored, login.authorization_url

    calls, token, authorization_url = asyncio.run(exercise())
    assert len(calls) == 1
    assert token.access_token == "working-confluence-token"
    assert token.refresh_token == "working-refresh-token"
    assert authorization_url is None


def test_authorization_url_keeps_sdk_state_but_requests_only_approved_scopes():
    source = (
        "https://auth.atlassian.com/authorize?response_type=code"
        "&client_id=https%3A%2F%2Fexample.test%2Fclient.json"
        "&redirect_uri=http%3A%2F%2F127.0.0.1%3A8765%2Fcallback"
        "&state=opaque-state&code_challenge=opaque-challenge"
        "&code_challenge_method=S256&scope=read%3Ame+write%3Ajira"
    )

    result = constrain_authorization_url(
        source,
        client_metadata_url="https://example.test/client.json",
        redirect_uri="http://127.0.0.1:8765/callback",
        scopes=OAUTH_SCOPES,
    )
    query = parse_qs(urlsplit(result).query)

    assert set(query["scope"][0].split()) == set(OAUTH_SCOPES.split())
    assert query["client_id"] == ["https://example.test/client.json"]
    assert query["redirect_uri"] == ["http://127.0.0.1:8765/callback"]
    assert query["state"] == ["opaque-state"]
    assert query["code_challenge"] == ["opaque-challenge"]
    assert "write:jira" not in query["scope"][0]


def test_oauth_consent_is_available_only_inside_explicit_login_task():
    async def exercise():
        login = ExplicitOAuthLogin(
            client_metadata_url="https://example.test/client.json",
            redirect_uri="http://127.0.0.1:8765/callback",
        )
        broad_url = (
            "https://auth.example/authorize?client_id=https%3A%2F%2Fexample.test%2Fclient.json"
            "&redirect_uri=http%3A%2F%2F127.0.0.1%3A8765%2Fcallback"
            "&state=s&code_challenge=c&scope=read%3Ame+write%3Ajira"
        )

        async def ordinary_request():
            await login.redirect_handler(broad_url)

        with pytest.raises(LoginRequiredError, match="AACT authenticate"):
            await ordinary_request()

        async def explicit_request():
            await asyncio.create_task(login.redirect_handler(broad_url))
            with pytest.raises(LoginRequiredError, match="AACT authenticate"):
                await asyncio.create_task(login.redirect_handler(broad_url))

        await login.run(explicit_request)
        return login.authorization_url

    url = asyncio.run(exercise())
    assert url is not None
    assert set(parse_qs(urlsplit(url).query)["scope"][0].split()) == set(OAUTH_SCOPES.split())


@pytest.mark.parametrize(
    ("mode", "email", "expected"),
    [
        ("service_account", "", ("bearer", "fixture-token")),
        ("personal", "person@example.invalid", ("basic", "person@example.invalid", "fixture-token")),
    ],
)
def test_upstream_token_modes_preserve_bearer_and_basic_auth(mode, email, expected):
    auth = upstream_auth(mode=mode, token="fixture-token", email=email)

    async def call_synthetic_backend():
        received = {}

        async def backend(request):
            received["authorization"] = request.headers["authorization"]
            return httpx.Response(200, request=request)

        async with httpx.AsyncClient(auth=auth, transport=httpx.MockTransport(backend)) as client:
            await client.post("https://mcp.atlassian.com/v2/mcp")
        return received["authorization"]

    authorization = asyncio.run(call_synthetic_backend())

    if expected[0] == "bearer":
        assert authorization == "Bearer fixture-token"
    else:
        assert authorization == "Basic cGVyc29uQGV4YW1wbGUuaW52YWxpZDpmaXh0dXJlLXRva2Vu"
