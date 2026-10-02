"""Validate selected configuration and target-scoped credentials in Docker.

Invocation: python /app/auth.py prepare|authenticate < action-request.json.
The helper maps target.path to /config/<name>.toml and state_dir to /state.
An explicitly selected kubeconfig input must be mounted read-only and remapped.
Only explicit authenticate actions prepare new credentials; prepare checks cache.
"""
from __future__ import annotations
import json
from pathlib import Path
import sys
from config import load_profile, validate_selections
from kubernetes_auth import load_cached_auth, prepare_token_auth, prepare_kubeconfig_auth


def prepare_auth(request: dict) -> dict:
    if request.get("protocol_version") != 1:
        raise ValueError("Unsupported action protocol version.")
    target = request.get("target", {})
    path = target.get("path")
    if not isinstance(path, str) or not path:
        raise ValueError("Selected target.path is required.")
    profile = load_profile(Path(path))
    inputs = request.get("inputs", {})
    selections = inputs.get("connections", [])
    if isinstance(selections, str):
        selections = list(filter(None, selections.split(",")))
    if not isinstance(selections, list) or any(not isinstance(item, str) for item in selections):
        raise ValueError("connections must be an array of DBMS/tenant identifiers.")
    validate_selections(profile, selections)
    state = Path(request.get("state_dir", "/state"))
    credential = state / "kubeconfig"
    ca = profile.cluster.get("ca_data")
    strict = profile.cluster.get("verify_x509_strict", True)
    if request.get("action") == "authenticate" and inputs.get("token"):
        token = inputs["token"]
        if not isinstance(token, str):
            raise ValueError("token must be a string.")
        check = prepare_token_auth(state, credential, profile.api_server, ca, token, strict)
    elif request.get("action") == "authenticate" and inputs.get("kubeconfig"):
        check = prepare_kubeconfig_auth(state, credential, Path(inputs["kubeconfig"]), profile.api_server)
    else:
        check = load_cached_auth(state, credential, profile.api_server, ca, strict)
    result = {"auth_required": check.status != "valid"}
    known = sorted(f"{db}/{tenant}" for db, item in profile.dbms.items() for tenant in item.tenants)
    if known:
        result["choices"] = {"connections": [{"value": value, "label": value} for value in known]}
    return result


if __name__ == "__main__":
    try:
        if len(sys.argv) != 2 or sys.argv[1] not in {"prepare", "authenticate"}:
            raise ValueError("Expected prepare or authenticate action.")
        request = json.load(sys.stdin)
        request["action"] = sys.argv[1]
        print(json.dumps(prepare_auth(request)))
    except (ValueError, TypeError, KeyError) as error:
        print(str(error), file=sys.stderr)
        sys.exit(1)
