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
        "login_url": "https://auth.example/authorize?state=opaque"
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
