import asyncio
import json
import logging

import httpx
import pytest

import server
from server import observe_login_task, protected_health


def test_health_runs_a_protected_identity_operation():
    calls = []

    async def identity_check():
        calls.append("atlassianUserInfo")

    result = asyncio.run(protected_health(identity_check))

    assert calls == ["atlassianUserInfo"]
    assert result == {"authenticated": True}


def test_health_reports_false_when_protected_identity_operation_fails():
    calls = []

    async def identity_check():
        calls.append("atlassianUserInfo")
        raise PermissionError("unauthenticated")

    result = asyncio.run(protected_health(identity_check))

    assert calls == ["atlassianUserInfo"]
    assert result == {"authenticated": False}


def test_health_poll_does_not_wait_on_an_in_progress_explicit_login():
    calls = []

    async def identity_check():
        calls.append("atlassianUserInfo")

    result = asyncio.run(
        protected_health(identity_check, login_pending=True, cached_authenticated=False)
    )

    assert calls == []
    assert result == {"authenticated": False}


def test_health_route_returns_false_promptly_while_login_waits_for_callback():
    async def exercise():
        original = server.upstream._login_task
        never = asyncio.Event()
        pending = asyncio.create_task(never.wait())
        server.upstream._login_task = pending
        try:
            async with httpx.AsyncClient(
                transport=httpx.ASGITransport(app=server.app), base_url="http://test"
            ) as client:
                response = await asyncio.wait_for(client.get("/health"), timeout=1)
        finally:
            pending.cancel()
            server.upstream._login_task = original
        return response

    response = asyncio.run(exercise())
    assert response.status_code == 200
    assert response.json() == {"authenticated": False}


def test_health_identity_probe_has_an_overall_timeout():
    async def exercise():
        async def slow_identity_check():
            await asyncio.sleep(1)

        return await protected_health(slow_identity_check, timeout=0.01)

    assert asyncio.run(exercise()) == {"authenticated": False}


def test_explicit_login_when_already_authenticated_succeeds_without_new_url(monkeypatch):
    async def already_authenticated():
        return None

    async def exercise():
        monkeypatch.setattr(server.upstream, "start_login", already_authenticated)
        async with httpx.AsyncClient(
            transport=httpx.ASGITransport(app=server.app), base_url="http://test"
        ) as client:
            return await client.post("/auth/login")

    response = asyncio.run(exercise())
    assert response.status_code == 200
    assert response.json() == {"authenticated": True}


def test_container_authenticate_action_accepts_authenticated_status_without_url(monkeypatch, capsys):
    class Response:
        def __enter__(self):
            return self

        def __exit__(self, *_):
            return None

        def read(self):
            return b'{"authenticated":true}'

    monkeypatch.setattr(server.sys, "argv", ["server.py", "action", "authenticate"])
    monkeypatch.setattr(server.urllib.request, "urlopen", lambda *_args, **_kwargs: Response())

    assert server.authenticate_action() == 0
    assert json.loads(capsys.readouterr().out) == {"auth_required": False}


def test_container_authenticate_action_returns_oauth_url_as_action_json(monkeypatch, capsys):
    class Response:
        def __enter__(self):
            return self

        def __exit__(self, *_):
            return None

        def read(self):
            return b'{"authorization_url":"https://auth.example/authorize?state=opaque"}'

    monkeypatch.setattr(server.sys, "argv", ["server.py", "action", "authenticate"])
    monkeypatch.setattr(server, "AUTH_MODE", "oauth")
    monkeypatch.setattr(server.urllib.request, "urlopen", lambda *_args, **_kwargs: Response())

    assert server.authenticate_action() == 0
    assert json.loads(capsys.readouterr().out) == {
        "auth_required": True,
        "login_url": "https://auth.example/authorize?state=opaque",
        "diagnostic": "Atlassian OAuth authorization is pending; complete it in the browser, then retry.",
    }


@pytest.mark.parametrize(
    ("auth_mode", "authenticated"),
    [(mode, result) for mode in ("personal", "service_account") for result in (True, False)],
)
def test_container_authenticate_action_validates_static_credentials(monkeypatch, capsys, auth_mode, authenticated):
    class Response:
        def __enter__(self):
            return self

        def __exit__(self, *_):
            return None

        def read(self):
            return json.dumps({"authenticated": authenticated}).encode()

    calls = []

    def urlopen(request, **kwargs):
        calls.append((request.full_url, request.method, kwargs.get("timeout")))
        return Response()

    monkeypatch.setattr(server.sys, "argv", ["server.py", "action", "authenticate"])
    monkeypatch.setattr(server, "AUTH_MODE", auth_mode)
    monkeypatch.setattr(server.urllib.request, "urlopen", urlopen)

    assert server.authenticate_action() == 0
    result = json.loads(capsys.readouterr().out)
    assert calls == [("http://127.0.0.1:8765/health", "GET", 35)]
    if authenticated:
        assert result == {"auth_required": False}
    else:
        assert result["auth_required"] is True
        assert "credentials" in result["diagnostic"].lower()


def test_background_login_failure_is_consumed_without_logging_exception_text(caplog):
    async def exercise():
        async def fail():
            raise RuntimeError("oauth response included private token material")

        task = asyncio.create_task(fail())
        await asyncio.sleep(0)
        observe_login_task(task)
        await asyncio.sleep(0)

    with caplog.at_level(logging.WARNING):
        asyncio.run(exercise())

    assert "RuntimeError" in caplog.text
    assert "private token material" not in caplog.text


def test_tools_list_fails_promptly_when_oauth_login_is_pending(monkeypatch):
    async def exercise():
        gate = asyncio.Event()
        pending = asyncio.create_task(gate.wait())
        monkeypatch.setattr(server.upstream, "_login_task", pending)

        def unexpected_proxy_client(*_args, **_kwargs):
            raise AssertionError("tools/list waited instead of rejecting pending OAuth")

        monkeypatch.setattr(server, "ProxyClient", unexpected_proxy_client)
        try:
            with pytest.raises(RuntimeError, match="Atlassian OAuth login is pending"):
                await asyncio.wait_for(server.mcp.list_tools(), timeout=0.2)
        finally:
            pending.cancel()
            try:
                await pending
            except asyncio.CancelledError:
                pass

    asyncio.run(exercise())


def test_login_url_discovery_timeout_cancels_task_and_releases_oauth_lock(monkeypatch):
    async def exercise():
        upstream = server.AtlassianUpstream()
        monkeypatch.setattr(server, "AUTH_MODE", "oauth")
        monkeypatch.setattr(server, "LOGIN_URL_DISCOVERY_TIMEOUT_SECONDS", 0.01)
        monkeypatch.setattr(upstream, "authenticated", lambda: asyncio.sleep(0, result=False))

        async def block_inside_oauth_lock(operation):
            async with upstream._client.context.lock:
                await asyncio.Event().wait()

        monkeypatch.setattr(upstream._login, "run", block_inside_oauth_lock)
        with pytest.raises(TimeoutError, match="did not produce an authorization URL"):
            await upstream.start_login()
        assert upstream._login_task.done()
        assert upstream._login_task.cancelled()
        assert not upstream._client.context.lock.locked()

    asyncio.run(exercise())


def test_cancelled_login_request_cancels_task_and_releases_oauth_lock(monkeypatch):
    async def exercise():
        upstream = server.AtlassianUpstream()
        monkeypatch.setattr(server, "AUTH_MODE", "oauth")
        monkeypatch.setattr(upstream, "authenticated", lambda: asyncio.sleep(0, result=False))
        entered = asyncio.Event()

        async def block_inside_oauth_lock(operation):
            async with upstream._client.context.lock:
                entered.set()
                await asyncio.Event().wait()

        monkeypatch.setattr(upstream._login, "run", block_inside_oauth_lock)
        request = asyncio.create_task(upstream.start_login())
        await entered.wait()
        request.cancel()
        with pytest.raises(asyncio.CancelledError):
            await request
        assert upstream._login_task.cancelled()
        assert not upstream._client.context.lock.locked()

    asyncio.run(exercise())


def test_auth_status_reports_failure_without_exception_details(monkeypatch):
    async def exercise():
        task = asyncio.create_task(fail_login())
        await asyncio.sleep(0)
        monkeypatch.setattr(server, "upstream", server.AtlassianUpstream())
        server.upstream._login_task = task
        async with httpx.AsyncClient(transport=httpx.ASGITransport(app=server.app), base_url="http://test") as client:
            response = await client.get("/auth/status")
        return response

    async def fail_login():
        raise RuntimeError("secret token and callback state")

    response = asyncio.run(exercise())
    assert response.json() == {"login_status": "failed", "authenticated": False}
    assert "secret" not in response.text
