"""Policy tests use a fixture captured independently from the policy registry."""

import asyncio
import importlib.util
import json
from pathlib import Path

import pytest

ROOT = Path(__file__).resolve().parents[1]
SERVER = ROOT / "server.py"
INVENTORY = ROOT / "operation_inventory.json"


def load_server():
    spec = importlib.util.spec_from_file_location("argocd_server_policy_test", SERVER)
    module = importlib.util.module_from_spec(spec)
    assert spec.loader is not None
    spec.loader.exec_module(module)
    return module


def inventory():
    return json.loads(INVENTORY.read_text(encoding="utf-8"))


def test_inventory_fixture_is_complete_and_every_operation_modifier_classified():
    server = load_server()
    captured = inventory()
    assert captured["schema_version"] == 1
    assert set(captured["versions"]) == {"3.2.0"}
    v32 = captured["versions"]["3.2.0"]["commands"]
    assert len(v32) == 160
    assert sum(bool(entry["children"]) for entry in v32) == 24
    serialized = INVENTORY.read_text(encoding="utf-8")
    assert "/Users/" not in serialized
    assert "/home/username" not in serialized and "yourusername" not in serialized
    all_leaves = set()
    for version, data in captured["versions"].items():
        assert data["evidence"]["source"] == "official-release-binary"
        assert data["evidence"]["checksum_verified"] is True
        assert data["commands"]
        assert "Available Commands:" in data["root_help_text"]
        assert data["global_flags"]
        assert data["modifiers"]
        for entry in data["commands"]:
            assert entry["path"]
            assert entry["flags"]
            if entry["children"]:
                with pytest.raises(ValueError, match="[Cc]ommand group"):
                    server.classify_operation(entry["path"], version=version)
                continue
            all_leaves.add(entry["path"])
            classification = server.classify_operation(entry["path"], entry.get("modifiers", ()), version=version)
            assert classification == server.OPERATION_POLICY[entry["path"]]["class"]
        for modifier in data["modifiers"]:
            server.classify_operation(modifier["operation"], [modifier["name"]], version=version)
    assert all_leaves == set(server.OPERATION_POLICY)
    assert len(all_leaves) == 136
    classes = {name: sum(policy["class"] == name for policy in server.OPERATION_POLICY.values())
               for name in ("read", "mutation", "client", "unsupported")}
    assert classes == {"read": 31, "mutation": 66, "client": 15, "unsupported": 24}


def test_discovery_matches_dispatch_in_both_modes():
    server = load_server()
    for read_only in (True, False):
        discovered = set(server.discover_operations(read_only=read_only, version="3.2.0"))
        for operation, policy in server.OPERATION_POLICY.items():
            dispatchable = server.operation_is_allowed(
                operation, read_only=read_only, version="3.2.0"
            )
            assert (operation in discovered) is dispatchable


def test_read_only_refresh_and_hard_refresh():
    server = load_server()
    assert server.classify_operation("app get", ["--refresh"], version="3.2.0") == "read"
    assert server.operation_is_allowed("app get", modifiers=["--refresh"], read_only=True, version="3.2.0")
    assert server.classify_operation("app get", ["--hard-refresh"], version="3.2.0") == "mutation"
    assert not server.operation_is_allowed("app get", modifiers=["--hard-refresh"], read_only=True, version="3.2.0")
    assert server.operation_is_allowed("app get", modifiers=["--hard-refresh"], read_only=False, version="3.2.0")
    assert server.classify_operation("app diff", ["--refresh"], version="3.2.0") == "read"
    assert server.classify_operation("app diff", ["--hard-refresh"], version="3.2.0") == "mutation"
    assert not server.operation_is_allowed("repo list", modifiers=["--refresh"], read_only=True, version="3.2.0")
    assert server.operation_is_allowed("repo list", modifiers=["--refresh"], read_only=False, version="3.2.0")
    with pytest.raises(ValueError, match="Unknown or incompatible modifier"):
        server.classify_operation("app list", ["--hard-refresh"], version="3.2.0")
    with pytest.raises(ValueError, match="read-only policy"):
        server.dispatch_operation("repo list", {"--refresh": "hard"}, read_only=True, version="3.2.0")
    with pytest.raises(ValueError, match="do not contain CLI flags"):
        server.dispatch_operation("app get", {"positionals": ["--server", "evil"]}, read_only=True, version="3.2.0")


def test_all_server_mutations_and_client_operations():
    server = load_server()
    for operation, policy in server.OPERATION_POLICY.items():
        if policy["class"] == "mutation":
            assert not server.operation_is_allowed(operation, read_only=True, version="3.2.0")
            assert server.operation_is_allowed(operation, read_only=False, version="3.2.0")
        elif policy["class"] == "client":
            assert not server.operation_is_allowed(operation, read_only=True, version="3.2.0")
            assert not server.operation_is_allowed(operation, read_only=False, version="3.2.0")
    assert server.operation_is_allowed("account get-user-info", read_only=True, version="3.2.0")


def test_connection_overrides_rejected():
    server = load_server()
    for key, value in {
        "server": "other.example", "context": "other", "auth_token": "secret",
        "config": "/tmp/other", "grpc_web": True, "grpc_web_root_path": "/",
        "plaintext": True, "insecure": True, "skip_test_tls": True,
        "port_forward": True, "loglevel": "debug",
    }.items():
        with pytest.raises(ValueError, match="override|unsupported|not allowed"):
            server.validate_operation_arguments("app list", {key: value}, version="3.2.0")


def test_no_raw_command_or_rpc_escape():
    server = load_server()
    for operation in ("raw", "command", "exec", "rpc", "grpc", "app sync --server evil"):
        with pytest.raises(ValueError, match="unknown|unsupported|not allowed|unclassified"):
            server.classify_operation(operation, version="3.2.0")
    with pytest.raises(ValueError, match="unsupported|not allowed"):
        server.validate_operation_arguments("app list", {"command": ["version"]}, version="3.2.0")
    tool_names = {tool.name for tool in asyncio.run(server.mcp.list_tools())}
    assert "argocd_raw_command" not in tool_names
    assert "argocd_rpc" not in tool_names
    assert "argocd_operation" not in tool_names


def test_read_only_profile_setting_is_passed_to_policy():
    import tomllib

    package = tomllib.loads((ROOT.parent / "package.toml").read_text(encoding="utf-8"))
    environment = package["mcp"]["env"]
    assert environment["AACT_ARGOCD_READ_ONLY"] == "${ tostring(inputs.read_only) }"


def test_help_inventory_parser_preserves_one_and_two_space_command_rows():
    captured = inventory()["versions"]
    for version, data in captured.items():
        paths = {entry["path"] for entry in data["commands"]}
        assert "app get" in paths
        assert "admin repo generate-spec" in paths
        admin_repo = next(entry for entry in data["commands"] if entry["path"] == "admin repo")
        assert "generate-spec" in admin_repo["children"]


def test_public_mcp_tool_rejects_hard_refresh_in_read_only_profile(monkeypatch):
    server = load_server()
    monkeypatch.setattr(server, "selected_cli_version", lambda: "3.2.0")
    monkeypatch.setenv("AACT_ARGOCD_READ_ONLY", "true")
    server._register_operation_tools()

    async def call_public_tool():
        tools = {tool.name for tool in await server.mcp.list_tools()}
        assert "argocd_app_get" in tools
        return await server.mcp.call_tool(
            "argocd_app_get", {"arguments": {"hard_refresh": True}}
        )

    result = asyncio.run(call_public_tool())
    error = json.loads(result.content[0].text)["error"]
    assert "denied by read-only policy" in error


def test_versioned_exclusions_and_unsupported_admin_have_distinct_errors():
    server = load_server()
    with pytest.raises(ValueError, match="no complete operation policy inventory"):
        server.dispatch_operation("app get-resource", read_only=True, version="2.14.11")
    with pytest.raises(ValueError, match="requires direct Kubernetes access"):
        server.dispatch_operation("admin import", read_only=False, version="3.2.0")
    with pytest.raises(ValueError, match="no complete operation policy inventory"):
        server.discover_operations(read_only=True, version="9.9.9")
