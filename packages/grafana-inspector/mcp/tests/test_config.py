import tempfile
import json
import os
import subprocess
import importlib.util
import threading
import unittest
import sys
import types
from pathlib import Path
from unittest.mock import patch
from http.server import BaseHTTPRequestHandler, HTTPServer
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from config import load_target, state_directory


class GrafanaTargetTests(unittest.TestCase):
    def test_pod_mode_loads_mounted_eso_token_without_target_toml(self):
        with tempfile.TemporaryDirectory() as directory:
            token_file = Path(directory) / "grafana-token"
            token_file.write_text("eso-token\n")
            class Session:
                def __init__(self): self.headers = {}; self.cookies = []
            requests = types.ModuleType("requests")
            requests.Session = Session
            requests.RequestException = Exception
            fastmcp = types.ModuleType("fastmcp")
            class FastMCP:
                def __init__(self, *args, **kwargs): pass
                def tool(self): return lambda function: function
            fastmcp.FastMCP = FastMCP
            spec = importlib.util.spec_from_file_location("grafana_pod_mode_test", Path(__file__).resolve().parents[1] / "server.py")
            module = importlib.util.module_from_spec(spec)
            env = {"GRAFANA_CONFIG": str(Path(directory) / "missing.toml"), "GRAFANA_URL": "https://grafana.example.test", "GRAFANA_API_TOKEN_FILE": str(token_file), "GRAFANA_TARGET": "sample"}
            with patch.dict(sys.modules, {"requests": requests, "fastmcp": fastmcp}), patch.dict(os.environ, env):
                spec.loader.exec_module(module)
            self.assertEqual(module.CLIENT.headers["Authorization"], "Bearer eso-token")



    def test_native_session_client_uses_session_without_proxy_cookie(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            target = root / "target.toml"
            target.write_text('[mcp]\nlocal_port=8123\n[grafana]\nurl="https://grafana.example.test"\nauth_mode="api_token"\n')
            auth = root / "auth.json"
            auth.write_text(json.dumps({"auth_mode": "session_cookie", "grafana_session": "synthetic-session", "session_expiry": "1234567890"}))
            class Cookies:
                def __init__(self): self.values = {}
                def set(self, name, value, **kwargs): self.values[name] = value
            class Session:
                def __init__(self): self.headers = {}; self.cookies = Cookies()
            requests = types.ModuleType("requests")
            requests.Session = Session
            requests.RequestException = Exception
            fastmcp = types.ModuleType("fastmcp")
            class FastMCP:
                def __init__(self, *args, **kwargs): pass
                def tool(self): return lambda function: function
            fastmcp.FastMCP = FastMCP
            spec = importlib.util.spec_from_file_location("grafana_native_session_server_test", Path(__file__).resolve().parents[1] / "server.py")
            module = importlib.util.module_from_spec(spec)
            with patch.dict(sys.modules, {"requests": requests, "fastmcp": fastmcp}), patch.dict(os.environ, {"GRAFANA_CONFIG": str(target), "GRAFANA_AUTH_FILE": str(auth), "GRAFANA_TARGET": "sample", "MCP_ENVIRONMENT": "home"}):
                spec.loader.exec_module(module)
            self.assertEqual(module.AUTH_MODE, "session_cookie")
            self.assertEqual(module.CLIENT.cookies.values, {"grafana_session": "synthetic-session", "grafana_session_expiry": "1234567890"})
            module.REFRESH_COOKIE_NAME = "work_oauth_refresh"
            module.OAUTH_REFRESH = "synthetic-refresh"
            self.assertEqual(module._session().cookies.values["work_oauth_refresh"], "synthetic-refresh")



    def test_datasource_uid_is_optional(self):
        with tempfile.TemporaryDirectory() as directory:
            path=Path(directory)/"target.toml"
            path.write_text('[mcp]\nlocal_port=8123\n[grafana]\nurl="https://grafana.example.test"\nauth_mode="api_token"\n')
            target=load_target(path,"lab","no-default",Path(directory)/"state")
            self.assertIsNone(target.datasource_uid)

    def test_two_targets_resolve_distinct_endpoint_and_auth_state(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            configs = []
            for name, port, url in (("east", 8123, "https://east.example.test/grafana"), ("west", 8124, "https://west.example.test/grafana")):
                path = root / f"{name}.toml"
                path.write_text(f'[mcp]\nlocal_port={port}\n[grafana]\nurl="{url}"\ndatasource_uid="logs-{name}"\nauth_mode="api_token"\n')
                configs.append(load_target(path, "lab", name, root / "state"))
            self.assertEqual([item.url for item in configs], ["https://east.example.test/grafana", "https://west.example.test/grafana"])
            self.assertEqual([item.local_port for item in configs], [8123, 8124])
            self.assertNotEqual(configs[0].state_directory, configs[1].state_directory)
            self.assertEqual(configs[0].state_directory, state_directory(root / "state", "lab", "east"))

    def test_session_auth_file_is_target_scoped(self):
        with tempfile.TemporaryDirectory() as temporary:
            root = Path(temporary)
            a = root / "a.toml"; b = root / "b.toml"
            for path in (a, b):
                path.write_text('[mcp]\nlocal_port=8123\n[grafana]\nurl="https://grafana.example.test"\ndatasource_uid="loki"\nauth_mode="session_cookie"\n')
            target_a = load_target(a, "lab", "a", root / "state")
            target_b = load_target(b, "lab", "b", root / "state")
            target_a.auth_file.parent.mkdir(parents=True, exist_ok=True)
            target_a.auth_file.write_text(json.dumps({"grafana_session": "secret-a"}))
            self.assertEqual(target_a.auth_file.name, "auth.json")
            self.assertTrue(target_a.auth_file.exists())
            self.assertFalse(target_b.auth_file.exists())

    def test_profile_ca_file_is_relative_to_target_and_strict_setting_is_optional(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ca_file = root / "corporate-ca.pem"
            ca_file.write_text("synthetic certificate")
            target_path = root / "target.toml"
            target_path.write_text(
                '[mcp]\nlocal_port=8123\n[grafana]\n'
                'url="https://grafana.example.test"\nauth_mode="api_token"\n'
                'ca_file="corporate-ca.pem"\n'
            )
            target = load_target(target_path, "work", "grafana", root / "state")
            self.assertEqual(target.ca_file, ca_file.resolve())
            self.assertIsNone(target.verify_x509_strict)

            target_path.write_text(
                '[mcp]\nlocal_port=8123\n[grafana]\n'
                'url="https://grafana.example.test"\nauth_mode="api_token"\n'
                'ca_file="corporate-ca.pem"\nverify_x509_strict=false\n'
            )
            target = load_target(target_path, "work", "grafana", root / "state")
            self.assertFalse(target.verify_x509_strict)

            target_path.write_text(
                f'[mcp]\nlocal_port=8123\n[grafana]\n'
                f'url="https://grafana.example.test"\nauth_mode="api_token"\n'
                f'ca_file="{ca_file.resolve()}"\n'
            )
            with self.assertRaisesRegex(ValueError, "relative to the target TOML"):
                load_target(target_path, "work", "grafana", root / "state")

    def test_custom_ca_obeys_profile_x509_strict_setting(self):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            ca_file = root / "corporate-ca.pem"
            ca_file.write_text("synthetic certificate")
            import ssl

            requests = types.ModuleType("requests")
            adapters = types.ModuleType("requests.adapters")

            class HTTPAdapter:
                def __init__(self):
                    self.init_poolmanager(10, 10)

                def init_poolmanager(self, connections, maxsize, block=False, **pool_kwargs):
                    self.poolmanager = types.SimpleNamespace(connection_pool_kw=pool_kwargs)

                def proxy_manager_for(self, proxy, **proxy_kwargs):
                    self.proxy_kwargs = proxy_kwargs
                    return self

            adapters.HTTPAdapter = HTTPAdapter

            class Session:
                def __init__(self):
                    self.headers = {}
                    self.verify = True
                    self.adapters = {}

                def mount(self, prefix, adapter):
                    self.adapters[prefix] = adapter

                def get_adapter(self, url):
                    return self.adapters["https://"]

            requests.Session = Session
            requests.RequestException = Exception
            requests.adapters = adapters
            fastmcp = types.ModuleType("fastmcp")

            class FastMCP:
                def __init__(self, *args, **kwargs):
                    pass

                def tool(self):
                    return lambda function: function

            fastmcp.FastMCP = FastMCP
            cases = (
                ("ca_file=\"corporate-ca.pem\"\n", "", True, str(ca_file.resolve())),
                ("ca_file=\"corporate-ca.pem\"\n", "verify_x509_strict=false\n", False, str(ca_file.resolve())),
                ("", "verify_x509_strict=false\n", False, True),
            )
            for index, (ca_setting, strict_setting, expected_strict, expected_verify) in enumerate(cases):
                target = root / f"target-{index}.toml"
                target.write_text(
                    '[mcp]\nlocal_port=8123\n[grafana]\n'
                    'url="https://grafana.example.test"\nauth_mode="api_token"\n'
                    + ca_setting + strict_setting
                )
                context = ssl.create_default_context()
                context.verify_flags |= ssl.VERIFY_X509_STRICT
                spec = importlib.util.spec_from_file_location(
                    f"grafana_tls_profile_test_{index}",
                    Path(__file__).resolve().parents[1] / "server.py",
                )
                module = importlib.util.module_from_spec(spec)
                env = {
                    "GRAFANA_CONFIG": str(target),
                    "GRAFANA_TARGET": "sample",
                    "MCP_ENVIRONMENT": "work",
                    "GRAFANA_API_TOKEN": "synthetic-token",
                }
                with (
                    patch.dict(sys.modules, {"requests": requests, "requests.adapters": adapters, "fastmcp": fastmcp}),
                    patch.dict(os.environ, env),
                    patch("ssl.create_default_context", return_value=context),
                ):
                    spec.loader.exec_module(module)
                self.assertEqual(bool(context.verify_flags & ssl.VERIFY_X509_STRICT), expected_strict)
                self.assertEqual(module.CLIENT.verify, expected_verify)
                adapter = module.CLIENT.get_adapter("https://grafana.example.test")
                adapter.proxy_manager_for("http://proxy.example.test")
                self.assertIs(adapter.proxy_kwargs["ssl_context"], context)

