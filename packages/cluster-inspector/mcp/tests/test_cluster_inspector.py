import base64
import os
import subprocess
import sys
import unittest
from pathlib import Path
from unittest.mock import MagicMock, patch

ROOT = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(ROOT))

import server
from server import _kubectl, _namespace, _pod_summary, describe_resource, get_logs, get_resource, list_connections, list_resources, run_query
import config


class ProfileTests(unittest.TestCase):
    def test_profile_without_tekton_has_no_tekton_capability(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            path = Path(directory) / "target.toml"
            path.write_text('[mcp]\nlocal_port=18800\n[cluster]\napi_server="https://cluster.test"\n')
            profile = config.load_profile(path)
            self.assertIsNone(profile.tekton)

    def test_x509_strictness_setting_must_be_boolean(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            path = Path(directory) / "target.toml"
            path.write_text('[mcp]\nlocal_port=18800\n[cluster]\napi_server="https://cluster.test"\nverify_x509_strict="false"\n')
            with self.assertRaisesRegex(ValueError, "verify_x509_strict"):
                config.load_profile(path)

    def test_profile_can_opt_out_of_strict_x509_profile_validation(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            path = Path(directory) / "target.toml"
            path.write_text('[mcp]\nlocal_port=18800\n[cluster]\napi_server="https://cluster.test"\nverify_x509_strict=false\n')
            profile = config.load_profile(path)
            self.assertFalse(profile.cluster["verify_x509_strict"])

    def test_tekton_profile_loads_selectors_and_resolves_hook(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            root = Path(directory)
            hook = root / "context.sh"
            hook.write_text("#!/bin/sh\n")
            hook.chmod(0o755)
            path = root / "target.toml"
            path.write_text('[mcp]\nlocal_port=18800\n[cluster]\napi_server="https://cluster.test"\n[tekton]\ndefault_namespace="ci"\nbranch_selector_key="ci.example/branch"\nrepository_selector_key="ci.example/repository"\nrepository_context_hook="context.sh"\n')
            profile = config.load_profile(path)
            self.assertEqual(profile.tekton.default_namespace, "ci")
            self.assertEqual(profile.tekton.branch_selector_key, "ci.example/branch")
            self.assertEqual(profile.tekton.repository_selector_key, "ci.example/repository")
            self.assertEqual(profile.tekton.repository_context_hook, str(hook.resolve()))

    def test_postgres_tls_options_are_validated_and_loaded(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            root=Path(directory); (root/"ca.pem").write_text("CA")
            path=root/"target.toml"
            path.write_text('[mcp]\nlocal_port=18800\n[cluster]\napi_server="https://cluster.test"\n[dbms.pg]\ntransport="direct"\nhost="db.test"\nport=5432\n[dbms.pg.tls]\nsslmode="verify-full"\nroot_cert="ca.pem"\n')
            profile=config.load_profile(path)
            self.assertEqual(profile.dbms["pg"].tls["sslmode"],"verify-full")
            self.assertEqual(server._tls_options(profile.dbms["pg"]),{"sslmode":"verify-full","sslrootcert":str((root/"ca.pem").resolve())})
            path.write_text(path.read_text().replace("verify-full","bogus"))
            with self.assertRaisesRegex(ValueError,"sslmode"):
                config.load_profile(path)

class ServerTests(unittest.TestCase):
    def test_config_free_pod_auth_uses_explicit_api_and_skips_local_renewal(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            runtime = Path(directory) / "pod-kubeconfig"
            with patch.dict(os.environ, {"MCP_KUBERNETES_API_SERVER": "https://kubernetes.default.svc", "MCP_KUBERNETES_AUTH_MODE": "service_account"}), patch.object(server, "resolve_runtime_kubeconfig", return_value=runtime) as resolve, patch.object(server, "start_token_renewal") as renewal:
                server.prepare_server_auth(None, Path(directory))
                self.assertEqual(os.environ["KUBECONFIG"], str(runtime))
                with patch.object(server, "TARGET", "sample"):
                    self.assertEqual(server._database_connections(), {})
            resolve.assert_called_once_with("https://kubernetes.default.svc")
            renewal.assert_not_called()

    def setUp(self):
        self.target = server.TARGET
        server.TARGET = "sample"
        server.credentials = {}
        server.credential_errors = {}

    def tearDown(self):
        server.TARGET = self.target
        server.credentials = {}
        server.credential_errors = {}

    def test_namespace_validation(self):
        self.assertEqual(_namespace("app-ns"), "app-ns")
        with self.assertRaises(ValueError):
            _namespace("App")

    def test_pod_summary(self):
        pod = {
            "metadata": {"name": "pod", "creationTimestamp": "2026-01-01T00:00:00Z"},
            "spec": {"containers": [{}, {}], "nodeName": "node"},
            "status": {
                "phase": "Running",
                "containerStatuses": [{"ready": True, "restartCount": 2}, {"ready": False, "restartCount": 3}],
                "initContainerStatuses": [{"restartCount": 1}],
            },
        }
        self.assertEqual(_pod_summary(pod)["ready"], "1/2")
        self.assertEqual(_pod_summary(pod)["restarts"], 6)

    @patch("server.subprocess.run")
    def test_get_resource_uses_fixed_json_command(self, run):
        run.return_value.returncode = 0
        run.return_value.stdout = '{"kind":"Deployment"}'
        result = get_resource("deployments.apps", "manager", "app-ns")
        self.assertEqual(result["resource"]["kind"], "Deployment")
        self.assertEqual(
            run.call_args.args[0],
            ["kubectl", "get", "deployments.apps", "manager", "--output", "json", "--namespace", "app-ns"],
        )

    @patch("server.subprocess.run")
    def test_raw_resource_tools_reject_secret_resources(self, run):
        run.return_value.returncode = 0
        run.return_value.stdout = '{"kind":"Secret","data":{"password":"c2VjcmV0"}}'
        for resource in ("secret", "secrets", "secrets.v1", "pods,secrets", "secret/example", "pods,secret/example"):
            with self.subTest(resource=resource):
                self.assertEqual(
                    get_resource(resource, "db-auth", "database"),
                    {"error": "Kubernetes Secret resources cannot be read through Cluster Inspector."},
                )
                self.assertEqual(
                    list_resources(resource, "database"),
                    {"error": "Kubernetes Secret resources cannot be read through Cluster Inspector."},
                )
                self.assertEqual(
                    describe_resource(resource, "db-auth", "database"),
                    {"error": "Kubernetes Secret resources cannot be read through Cluster Inspector."},
                )
        run.assert_not_called()

    @patch("server.subprocess.run")
    def test_resource_name_cannot_supply_another_kubectl_resource(self, run):
        result = get_resource("pods", "example,secret/password", "example")
        self.assertIn("name", result["error"])
        run.assert_not_called()

    @patch("server.subprocess.run")
    def test_raw_resource_tools_accept_only_one_resource_type(self, run):
        for resource in ("pods,services", "pod/example", "pods services"):
            with self.subTest(resource=resource):
                self.assertIn("one Kubernetes resource type", list_resources(resource)["error"])
        run.assert_not_called()

    @patch("server._kubectl_json")
    def test_config_free_target_has_no_baked_in_database_connections(self, kubectl):
        with patch.object(server, "PROFILE", None), patch.dict(os.environ, {}, clear=True):
            self.assertEqual(server._database_connections(), {})
            self.assertEqual(list_connections()["connections"], [])
            server.load_database_credentials()
            self.assertEqual(server.credentials, {})
        kubectl.assert_not_called()

    @patch("server.subprocess.run")
    def test_list_resources_maps_selectors(self, run):
        run.return_value.returncode = 0
        run.return_value.stdout = '{"items":[]}'
        list_resources("pods", "app-ns", label_selector="app=manager", field_selector="status.phase=Running", limit=100)
        self.assertEqual(
            run.call_args.args[0],
            ["kubectl", "get", "pods", "--output", "json", "--namespace", "app-ns", "--selector", "app=manager", "--field-selector", "status.phase=Running", "--chunk-size", "100"],
        )

    @patch("server.subprocess.run")
    def test_logs_does_not_follow(self, run):
        run.return_value.returncode = 0
        run.return_value.stdout = "line\n"
        result = get_logs("app-ns", "manager", container="app", previous=True, tail_lines=10)
        self.assertEqual(result["text"], "line\n")
        self.assertEqual(
            run.call_args.args[0],
            ["kubectl", "logs", "manager", "--namespace", "app-ns", "--container", "app", "--previous", "--tail", "10", "--timestamps"],
        )
        self.assertNotIn("--follow", run.call_args.args[0])

    def test_rejects_flag_as_resource(self):
        self.assertIn("not begin", get_resource("--port-forward", "anything")["error"])

    def test_rejects_namespace_with_all_namespaces(self):
        self.assertEqual(
            list_resources("pods", namespace="app-ns", all_namespaces=True)["error"],
            "namespace and all_namespaces cannot both be set.",
        )

    def test_list_connections_uses_selected_configured_tenants(self):
        profile = config.ClusterProfile("lab", "sample", 18800, "https://cluster.test", {}, {"pg": config.DatabaseProfile("pg", "direct", "db.test", 5432, tenants={"one": config.TenantProfile("one", "one", "reader", {"type": "literal", "password": "p"})})})
        with patch.object(server, "PROFILE", profile), patch.object(server, "SELECTED_CONNECTIONS", {"pg/one"}):
            self.assertEqual(list_connections()["connections"], [{"name": "pg/one", "purpose": "configured read-only database access"}])

    def test_unavailable_database_credentials_do_not_stop_other_connections(self):
        tenants = {"one": config.TenantProfile("one", "one", "reader", {"type": "literal", "password": "p"}), "two": config.TenantProfile("two", "two", "reader", {"type": "executable", "command": "/missing"})}
        profile = config.ClusterProfile("lab", "sample", 18800, "https://cluster.test", {}, {"pg": config.DatabaseProfile("pg", "direct", "db.test", 5432, tenants=tenants)})
        with patch.object(server, "PROFILE", profile), patch.object(server, "SELECTED_CONNECTIONS", {"pg/one", "pg/two"}), patch("server.subprocess.run", side_effect=OSError("missing")):
            server.load_database_credentials()
            self.assertEqual(server.credentials, {"pg/one": "p"})
            self.assertEqual(server.credential_errors, {"pg/two": "Database connection is unavailable or unauthorized."})
            self.assertEqual(run_query("pg/two", "SELECT 1"), {"error": "Database connection is unavailable or unauthorized."})

    def test_unknown_database_connection_is_rejected(self):
        self.assertEqual(run_query("unknown", "SELECT 1"), {"error": "Configured connection not found: unknown"})

    @patch("server.psycopg.connect")
    def test_run_query_uses_read_only_transaction_and_parameters(self, connection):
        profile = config.ClusterProfile("lab", "sample", 18800, "https://cluster.test", {}, {"pg": config.DatabaseProfile("pg", "direct", "db.test", 5432, tenants={"one": config.TenantProfile("one", "one", "reader", {"type": "literal", "password": "password"})})})
        database = MagicMock()
        cursor = MagicMock()
        cursor.description = [MagicMock(name="value", type_code=23)]
        cursor.description[0].name = "value"
        cursor.fetchall.return_value = [(1,)]
        database.cursor.return_value.__enter__.return_value = cursor
        connection.return_value.__enter__.return_value = database

        with patch.object(server, "PROFILE", profile), patch.object(server, "SELECTED_CONNECTIONS", {"pg/one"}):
            server.credentials = {"pg/one": "password"}
            result = run_query("pg/one", "SELECT %s", [1], statement_timeout_seconds=90)

        self.assertEqual(result["rows"], [[1]])
        cursor.execute.assert_any_call("BEGIN READ ONLY")
        cursor.execute.assert_any_call("SELECT set_config('statement_timeout', %s, true)", ("90s",))
        cursor.execute.assert_any_call("SELECT %s", [1])
        database.rollback.assert_called_once()

    @patch("server.subprocess.run")
    def test_timeout_is_classified(self, run):
        run.side_effect = __import__("subprocess").TimeoutExpired("kubectl", 30)
        with self.assertRaisesRegex(ValueError, "timed out"):
            _kubectl(["get", "pods"])


class KubeconfigTests(unittest.TestCase):
    def test_profile_rejects_local_kubeconfig_or_context(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            profile_path = Path(directory) / "profile.toml"
            profile_path.write_text('[mcp]\nlocal_port=18800\n[cluster]\napi_server="https://cluster.test"\nkubeconfig="/Users/example/.kube/config"\ncontext="chosen"\n')
            with self.assertRaisesRegex(ValueError, "token.*api_server"):
                config.load_profile(profile_path)

    def test_direct_connection_does_not_start_port_forward(self):
        with patch("server.subprocess.Popen") as popen:
            with server._database_transport(config.DatabaseProfile("db", "direct", "db.test", 5432)) as endpoint:
                self.assertEqual(endpoint, ("db.test", 5432))
            popen.assert_not_called()

    def test_port_forward_uses_configured_service(self):
        profile = config.DatabaseProfile("db", "port_forward", "service", 5432, namespace="ns", service="pg")
        with patch("server._port_forward") as forward:
            forward.return_value.__enter__.return_value = ("127.0.0.1", 45555)
            with server._database_transport(profile) as endpoint:
                self.assertEqual(endpoint, ("127.0.0.1", 45555))
            forward.assert_called_once_with("ns", "pg", 5432)

    def test_reads_each_tenant_secret_independently(self):
        db = config.DatabaseProfile("pg", "direct", "db", 5432, tenants={
            "one": config.TenantProfile("one", "one", "u1", {"type": "kubernetes_secret", "namespace": "a", "name": "s1", "key": "password"}),
            "two": config.TenantProfile("two", "two", "u2", {"type": "kubernetes_secret", "namespace": "b", "name": "s2", "key": "password"}),
        })
        original_profile, original_selected = server.PROFILE, server.SELECTED_CONNECTIONS
        server.PROFILE = config.ClusterProfile("e", "t", 18800, "https://cluster.test", {}, {"pg": db})
        server.SELECTED_CONNECTIONS = {"pg/one", "pg/two"}
        try:
            def secret_response(args):
                value=b"p1" if "s1" in args else b"p2"
                return {"data":{"password":base64.b64encode(value).decode()}}
            with patch("server._kubectl_json", side_effect=secret_response) as get_secret:
                server.load_database_credentials(); loaded = dict(server.credentials)
            self.assertEqual(loaded, {"pg/one": "p1", "pg/two": "p2"})
            secret_calls=[call.args[0] for call in get_secret.call_args_list]
            self.assertEqual({call[2] for call in secret_calls},{"s1","s2"})
            self.assertEqual({call[4] for call in secret_calls},{"a","b"})
        finally:
            server.PROFILE, server.SELECTED_CONNECTIONS = original_profile, original_selected

    def test_run_query_rejects_unselected_tenant(self):
        original = server.PROFILE
        original_selected = server.SELECTED_CONNECTIONS
        profile = config.ClusterProfile("e", "t", 18800, "https://cluster.test", {}, {"pg": config.DatabaseProfile("pg", "direct", "db", 5432, tenants={"one": config.TenantProfile("one", "one", "u", {"type": "literal", "password": "p"}), "two": config.TenantProfile("two", "two", "u", {"type": "literal", "password": "p"})})})
        server.PROFILE = profile
        server.SELECTED_CONNECTIONS = config.validate_selections(profile, ["pg/one"])
        try:
            self.assertIn("not selected", run_query("pg/two", "SELECT 1")["error"])
        finally:
            server.PROFILE = original
            server.SELECTED_CONNECTIONS = original_selected

    def test_target_initialization_selects_only_requested_tenant(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            path=Path(directory)/"target.toml"
            path.write_text('''[mcp]\nlocal_port=18800\n[cluster]\napi_server="https://cluster.test"\n[dbms.pg]\ntransport="direct"\nhost="db.test"\nport=5432\n[dbms.pg.tenants.one]\ndatabase="one"\nusername="one"\ncredential_source="literal"\npassword="secret"\n[dbms.pg.tenants.two]\ndatabase="two"\nusername="two"\ncredential_source="literal"\npassword="secret"\n''')
            old_profile, old_selected = server.PROFILE, server.SELECTED_CONNECTIONS
            try:
                server.PROFILE, server.SELECTED_CONNECTIONS = server.initialize_target(str(path), "pg/one")
                self.assertEqual(server.SELECTED_CONNECTIONS,{"pg/one"})
                self.assertEqual(server._profile_dbms("pg/one")[1].name,"one")
                self.assertIn("not selected",run_query("pg/two","SELECT 1")["error"])
            finally:
                server.PROFILE, server.SELECTED_CONNECTIONS = old_profile, old_selected

    def test_state_path_isolated_by_environment_and_target(self):
        first = config.state_path(Path("/state"), "home", "sample")
        second = config.state_path(Path("/state"), "work", "sample")
        self.assertNotEqual(first, second)

    def test_state_path_rejects_traversal_components(self):
        with self.assertRaisesRegex(ValueError, "safe identifier"):
            config.state_path(Path("/state"), "../../outside", "target")

    def test_materialize_requires_target_auth_without_reading_home_kubeconfig(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            root = Path(directory); kube = root / ".kube"; kube.mkdir()
            (kube / "config").write_text('apiVersion: v1\nkind: Config\ncurrent-context: mac-context\nclusters: [{name: c, cluster: {server: https://mac-only.invalid}}]\nusers: [{name: u, user: {token: mac-secret}}]\ncontexts: [{name: mac-context, context: {cluster: c, user: u}}]\n')
            profile = config.ClusterProfile("env", "target", 18800, "https://cluster.test", {"api_server": "https://cluster.test"}, {}, set())
            with patch.dict(os.environ, {"HOME": str(root)}):
                with self.assertRaisesRegex(ValueError, "target-scoped.*credentials"):
                    config.materialize_kubeconfig(profile, root / "state")

    def test_materialize_reuses_explicit_target_state_without_host_kubeconfig(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            root = Path(directory); kube = root / ".kube"; kube.mkdir()
            (kube / "config").write_text("mac secret must not be loaded")
            profile = config.ClusterProfile("env", "target", 18800, "https://cluster.test", {"api_server": "https://cluster.test"}, {}, set())
            state = root / "state"; state.mkdir()
            target_auth = state / "kubeconfig"; target_auth.write_text("explicit target auth")
            with patch.dict(os.environ, {"HOME": str(root)}):
                result = config.materialize_kubeconfig(profile, state)
            self.assertEqual(result, target_auth)
            self.assertEqual(result.read_text(), "explicit target auth")

    def test_materialize_uses_only_environment_uri_and_tui_token(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            root = Path(directory); kube = root / ".kube"; kube.mkdir()
            (kube / "config").write_text('apiVersion: v1\nkind: Config\ncurrent-context: mac-context\nclusters: [{name: c, cluster: {server: https://mac-only.invalid}}]\nusers: [{name: u, user: {token: mac-secret}}]\ncontexts: [{name: mac-context, context: {cluster: c, user: u}}]\n')
            profile = config.ClusterProfile("env", "target", 18800, "https://environment.test:6443", {"api_server": "https://environment.test:6443"}, {}, set())
            with patch.dict(os.environ, {"HOME": str(root)}):
                output = config.materialize_kubeconfig(profile, root / "state", "tui-bearer-token")
            document = __import__("yaml").safe_load(output.read_text())
            self.assertEqual(document["clusters"][0]["cluster"]["server"], "https://environment.test:6443")
            self.assertEqual(document["users"][0]["user"]["token"], "tui-bearer-token")
            self.assertNotIn("mac-only.invalid", output.read_text())
            self.assertNotIn("mac-secret", output.read_text())


    def test_auth_files_use_mode_0600(self):
        with __import__("tempfile").TemporaryDirectory() as directory:
            path = config.write_auth_file(Path(directory) / "auth", "secret")
            self.assertEqual(path.stat().st_mode & 0o777, 0o600)


if __name__ == "__main__":
    unittest.main()
