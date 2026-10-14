import json
import asyncio
import os
import sys
import tempfile
import types
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))
_directory = tempfile.TemporaryDirectory()
_root = Path(_directory.name)
_config = _root / "target.toml"
_config.write_text('[mcp]\nlocal_port=18800\n[grafana]\nurl="https://grafana.example.test"\nauth_mode="api_token"\n')
_auth = _root / "auth.json"
_auth.write_text(json.dumps({"api_token": "placeholder-token"}))
os.environ.update({"GRAFANA_CONFIG":str(_config),"GRAFANA_AUTH_FILE":str(_auth),"GRAFANA_TARGET":"unit","MCP_ENVIRONMENT":"test"})
class _Cookies:
    def set(self,*args,**kwargs): pass
    def __iter__(self): return iter(())
class _Session:
    def __init__(self): self.headers={}; self.cookies=_Cookies()
    def request(self,*args,**kwargs): raise AssertionError("request should be mocked")
_requests=types.ModuleType("requests")
_requests.Session=_Session
_requests.RequestException=Exception
sys.modules.setdefault("requests",_requests)
class _FastMCP:
    def __init__(self,*args,**kwargs): pass
    def tool(self): return lambda function:function
    def custom_route(self,*args,**kwargs): return lambda function:function
    def run(self,*args,**kwargs): pass
_fastmcp=types.ModuleType("fastmcp")
_fastmcp.FastMCP=_FastMCP
sys.modules.setdefault("fastmcp",_fastmcp)
import server


class ApiTokenModeTests(unittest.TestCase):
    def test_session_health_rejects_expired_cookie_even_if_datasources_are_anonymous(self):
        outcomes = {
            "expired": ValueError("Grafana rejected credentials (HTTP 401)."),
            "valid": {"id": 17, "login": "operator", "isAnonymous": False},
            "anonymous": {"id": 1, "login": "Anonymous", "isAnonymous": True},
            "malformed": {"login": "operator"},
        }
        with patch.object(server, "AUTH_MODE", "session_cookie"):
            for mode, expected in (("expired", False), ("valid", True), ("anonymous", False), ("malformed", False)):
                calls = []
                def fake_get(path, parameters=None):
                    calls.append(path)
                    if path == "/api/datasources":
                        return [{"uid": "public-datasource"}]
                    if path == "/api/user":
                        outcome = outcomes[mode]
                        if isinstance(outcome, Exception):
                            raise outcome
                        return outcome
                    raise AssertionError(f"unexpected health endpoint: {path}")

                with self.subTest(mode=mode), patch.object(server, "_get", side_effect=fake_get):
                    response = asyncio.run(server.health(None))
                    self.assertEqual(response.status_code, 200)
                    self.assertEqual(json.loads(response.body), {"authenticated": expected})
                    self.assertEqual(calls, ["/api/user"])

    def test_api_token_health_rejects_anonymously_listable_datasources_when_preferences_reject_token(self):
        calls = []

        def fake_get(path, parameters=None):
            calls.append(path)
            if path == "/api/datasources":
                return [{"uid": "public-datasource"}]
            if path == "/api/user/preferences":
                raise ValueError("Grafana rejected credentials (HTTP 401).")
            raise AssertionError(f"unexpected health endpoint: {path}")

        with patch.object(server, "AUTH_MODE", "api_token"), patch.object(server, "_get", side_effect=fake_get):
            response = asyncio.run(server.health(None))

        self.assertEqual(response.status_code, 200)
        self.assertEqual(json.loads(response.body), {"authenticated": False})
        self.assertEqual(calls, ["/api/user/preferences"])

    def test_api_token_health_accepts_service_account_and_legacy_token_preferences(self):
        preferences_by_token = {
            "service-account": {"theme": "dark", "timezone": "browser", "weekStart": ""},
            "legacy-api-key": {"theme": "", "timezone": ""},
        }
        with patch.object(server, "AUTH_MODE", "api_token"):
            for token_kind, expected_preferences in preferences_by_token.items():
                calls = []

                def fake_get(path, parameters=None):
                    calls.append(path)
                    if path == "/api/user/preferences":
                        return expected_preferences
                    raise AssertionError(f"unexpected health endpoint: {path}")

                with self.subTest(token_kind=token_kind), patch.object(server, "_get", side_effect=fake_get):
                    response = asyncio.run(server.health(None))
                    self.assertEqual(response.status_code, 200)
                    self.assertEqual(json.loads(response.body), {"authenticated": True})
                    self.assertEqual(calls, ["/api/user/preferences"])

    def test_api_token_health_rejects_unexpected_preferences_response_shape(self):
        for preferences in (None, [], "ok", {}, {"message": "unexpected response"}, {"theme": [], "timezone": 7}):
            with self.subTest(preferences=preferences), patch.object(server, "AUTH_MODE", "api_token"), patch.object(server, "_get", return_value=preferences):
                response = asyncio.run(server.health(None))
                self.assertEqual(response.status_code, 200)
                self.assertEqual(json.loads(response.body), {"authenticated": False})

    def test_startup_validates_token_on_protected_datasource_api(self):
        with patch.object(server, "_get", return_value=[]) as get:
            server._validate_api_token()
        get.assert_called_once_with("/api/datasources")

    def test_authentication_and_permission_failures_have_distinct_diagnostics(self):
        cases = {
            401: "Grafana rejected credentials (HTTP 401).",
            403: "Grafana denied access (HTTP 403); the configured identity may lack permission.",
        }
        for status, expected in cases.items():
            response = MagicMock(status_code=status, is_redirect=False, headers={})
            with self.subTest(status=status), self.assertRaisesRegex(ValueError, expected.replace("(", r"\(").replace(")", r"\)")) as raised:
                server._response_json(response)
            self.assertNotIn("expired", str(raised.exception).lower())

    def test_api_token_request_never_runs_cookie_rotation(self):
        response=MagicMock(status_code=200,is_redirect=False,headers={})
        response.json.return_value={"database":"ok"}
        with patch.object(server,"_rotate_before_expiry") as rotate, patch.object(server.CLIENT,"request",return_value=response) as request:
            self.assertEqual(server._get("/api/health"),{"database":"ok"})
        rotate.assert_not_called()
        self.assertEqual(server.CLIENT.headers["Authorization"],"Bearer placeholder-token")
        self.assertTrue(request.called)
