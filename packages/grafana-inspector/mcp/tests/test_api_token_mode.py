import json
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
    def run(self,*args,**kwargs): pass
_fastmcp=types.ModuleType("fastmcp")
_fastmcp.FastMCP=_FastMCP
sys.modules.setdefault("fastmcp",_fastmcp)
import server


class ApiTokenModeTests(unittest.TestCase):
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
