import importlib.util
import json
import os
from pathlib import Path
from unittest.mock import patch
import pytest
import kubernetes_auth

ROOT=Path(__file__).resolve().parents[1]

def load_action():
    assert (ROOT/"auth.py").is_file(), "cluster container auth action missing"
    spec=importlib.util.spec_from_file_location("cluster_action",ROOT/"auth.py")
    module=importlib.util.module_from_spec(spec); spec.loader.exec_module(module)
    return module

def request(tmp_path, inputs=None):
    cfg=tmp_path/"config"; cfg.mkdir(exist_ok=True)
    target=cfg/"target.toml"
    target.write_text('[mcp]\nlocal_port=8765\n[cluster]\napi_server="https://cluster.example.test"\n')
    return {"protocol_version":1, "action":"prepare", "target":{"path":str(target),"environment":"lab","name":"sample"}, "inputs":inputs or {}, "state_dir":str(tmp_path/"state"), "interactive":False}

class Response:
    def __init__(self,username="synthetic-user"): self.username=username
    def __enter__(self): return self
    def __exit__(self,*args): pass
    def read(self): return json.dumps({"status":{"userInfo":{"username":self.username}}}).encode()

def test_prepare_without_cache_requires_auth_and_ignores_host_default(tmp_path,monkeypatch):
    host=tmp_path/"home/.kube"; host.mkdir(parents=True); (host/"config").write_text("must never read")
    monkeypatch.setenv("HOME",str(host.parent))
    module=load_action()
    with patch.object(kubernetes_auth.urllib.request,"urlopen") as network:
        assert module.prepare_auth(request(tmp_path)) == {"auth_required":True}
        network.assert_not_called()

def test_authenticate_without_explicit_credential_returns_actionable_error(tmp_path):
    req=request(tmp_path);req["action"]="authenticate"
    with pytest.raises(ValueError,match="Enter a Token or select a source kubeconfig"):
        load_action().prepare_auth(req)

def test_explicit_token_auth_writes_private_target_cache_and_prepares_again(tmp_path):
    module=load_action(); req=request(tmp_path,{"token":"synthetic-token"}); req["action"]="authenticate"
    with patch.object(kubernetes_auth.urllib.request,"urlopen",return_value=Response()):
        assert module.prepare_auth(req) == {"auth_required":False}
        cache=Path(req["state_dir"])/"kubeconfig"
        assert cache.stat().st_mode & 0o777 == 0o600
        assert json.loads(cache.read_text())["clusters"][0]["cluster"]["server"] == "https://cluster.example.test"
        req["inputs"]={};req["action"]="prepare"
        assert module.prepare_auth(req) == {"auth_required":False}

def test_invalid_replacement_token_preserves_previous_credential(tmp_path):
    from urllib.error import HTTPError
    module=load_action(); req=request(tmp_path,{"token":"good"}); req["action"]="authenticate"
    with patch.object(kubernetes_auth.urllib.request,"urlopen",return_value=Response()): module.prepare_auth(req)
    before=(Path(req["state_dir"])/"kubeconfig").read_bytes()
    req["inputs"]["token"]="bad"
    with patch.object(kubernetes_auth.urllib.request,"urlopen",side_effect=HTTPError("https://cluster.example.test",401,"invalid",{},None)):
        assert module.prepare_auth(req) == {"auth_required":True, "diagnostic":"Kubernetes API rejected credentials (HTTP 401)."}
    assert (Path(req["state_dir"])/"kubeconfig").read_bytes() == before

def test_explicit_kubeconfig_requires_selected_api_match(tmp_path):
    module=load_action(); req=request(tmp_path)
    source=tmp_path/"source-kubeconfig"
    kubernetes_auth.write_token_kubeconfig("https://another.example.test",None,source,"synthetic-token")
    req["inputs"]={"kubeconfig":str(source)};req["action"]="authenticate"
    with pytest.raises(ValueError,match="configured API"):
        module.prepare_auth(req)
    assert not (Path(req["state_dir"])/"kubeconfig").exists()

def test_explicit_default_kubeconfig_is_imported_into_target_state(tmp_path, monkeypatch):
    import kubernetes_auth
    from types import SimpleNamespace
    home=tmp_path/"home"; default=home/".kube"/"config"
    default.parent.mkdir(parents=True); default.write_text("synthetic fixture")
    alias=tmp_path/"selected"; alias.symlink_to(default)
    monkeypatch.setattr(Path,"home",classmethod(lambda cls: home))
    config={"clusters":[{"name":"c","cluster":{"server":"https://cluster.example.test"}}],"users":[{"name":"u","user":{"token":"synthetic"}}],"contexts":[{"name":"x","context":{"cluster":"c","user":"u"}}],"current-context":"x"}
    def kubectl(args,**kwargs):
        if "view" in args:
            assert args[args.index("--kubeconfig")+1] == str(alias)
            return SimpleNamespace(stdout=json.dumps(config))
        return SimpleNamespace(stdout=json.dumps({"status":{"userInfo":{"username":"explicit-user"}}}))
    monkeypatch.setattr(kubernetes_auth.subprocess,"run",kubectl)
    req=request(tmp_path,{"kubeconfig":str(alias)});req["action"]="authenticate"
    assert load_action().prepare_auth(req) == {"auth_required":False}
    managed=Path(req["state_dir"])/"kubeconfig"
    assert managed.stat().st_mode & 0o777 == 0o600
    assert json.loads(managed.read_text()) == config
    assert default.read_text() == "synthetic fixture"

def test_unknown_database_selection_rejected_without_auth_lookup(tmp_path):
    module=load_action();req=request(tmp_path,{"connections":["unknown/tenant"]})
    with pytest.raises(ValueError,match="Unknown database tenant"):
        module.prepare_auth(req)


def test_rejected_token_reports_http_cause_without_echoing_token():
    from urllib.error import HTTPError

    token = "synthetic-secret-token"
    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=HTTPError(
        "https://cluster.example.test", 401, "Unauthorized", {}, None
    )):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, token)

    assert result.status == "invalid"
    assert result.diagnostic == "Kubernetes API rejected credentials (HTTP 401)."
    assert token not in result.diagnostic


def test_unreachable_kubernetes_api_reports_network_cause():
    from urllib.error import URLError

    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=URLError("connection refused")):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, "synthetic-token")

    assert result.status == "unknown"
    assert "Kubernetes API is unreachable" in result.diagnostic
    assert "connection refused" in result.diagnostic


def test_malformed_kubernetes_auth_response_reports_parse_cause():
    class MalformedResponse(Response):
        def read(self):
            return b"not-json"

    with patch.object(kubernetes_auth.urllib.request, "urlopen", return_value=MalformedResponse()):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, "synthetic-token")

    assert result.status == "unknown"
    assert result.diagnostic == "Kubernetes API returned a malformed authentication response."


def test_auth_action_includes_primary_auth_diagnostic(tmp_path):
    from urllib.error import HTTPError
    import io

    req = request(tmp_path, {"token": "synthetic-secret-token"})
    req["action"] = "authenticate"
    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=HTTPError(
        "https://cluster.example.test", 401, "Unauthorized", {}, io.BytesIO(b"synthetic-secret-token\ninjected control text")
    )):
        result = load_action().prepare_auth(req)

    assert result["auth_required"] is True
    assert result["diagnostic"] == "Kubernetes API rejected credentials (HTTP 401)."
    assert "synthetic-secret-token" not in json.dumps(result)


def test_forbidden_optional_identity_probe_is_labeled_secondary():
    from urllib.error import HTTPError

    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=HTTPError(
        "https://cluster.example.test", 403, "Forbidden", {}, None
    )):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, "synthetic-token")

    assert result.status == "valid"
    assert result.diagnostic.startswith("Secondary:")
    assert "HTTP 403" in result.diagnostic


def test_invalid_kubernetes_response_shape_keeps_malformed_response_cause():
    class WrongShapeResponse(Response):
        def read(self):
            return b'{"status": null}'

    with patch.object(kubernetes_auth.urllib.request, "urlopen", return_value=WrongShapeResponse()):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, "synthetic-token")

    assert result.status == "unknown"
    assert "malformed authentication response" in result.diagnostic


def test_kubeconfig_validation_error_retains_kubectl_exit_status_without_secret(tmp_path, monkeypatch):
    import subprocess

    source = tmp_path / "kubeconfig"
    source.write_text("synthetic")
    secret = "synthetic-secret-token"

    def fail_whoami(args, **kwargs):
        if "view" in args:
            from types import SimpleNamespace
            return SimpleNamespace(stdout=json.dumps({
                "clusters": [{"name": "c", "cluster": {"server": "https://cluster.example.test"}}],
                "users": [{"name": "u", "user": {"token": secret}}],
                "contexts": [{"name": "x", "context": {"cluster": "c", "user": "u"}}],
                "current-context": "x",
            }))
        raise subprocess.CalledProcessError(7, args, stderr=f"unauthorized {secret}")

    monkeypatch.setattr(kubernetes_auth.subprocess, "run", fail_whoami)
    with pytest.raises(ValueError) as exc:
        kubernetes_auth.copy_kubeconfig(source, tmp_path / "managed", "https://cluster.example.test")

    assert "kubectl auth whoami" in str(exc.value)
    assert "exit 7" in str(exc.value)
    assert secret not in str(exc.value)


def test_tls_failure_reports_validation_cause_without_token():
    import ssl

    secret = "synthetic-secret-token"
    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=ssl.SSLCertVerificationError("certificate verify failed")):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, secret)

    assert result.status == "unknown"
    assert "TLS validation failed" in result.diagnostic
    assert secret not in result.diagnostic


def test_cancelled_kubernetes_probe_keeps_interruption_cause():
    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=InterruptedError()):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, "synthetic-token")

    assert result.status == "unknown"
    assert result.diagnostic == "Kubernetes API authentication probe was interrupted."


def test_expired_local_claim_does_not_turn_server_401_into_expiry_claim():
    import base64
    from urllib.error import HTTPError

    claims = {"sub": "system:serviceaccount:sample:agent", "exp": 1}
    payload = base64.urlsafe_b64encode(json.dumps(claims).encode()).decode().rstrip("=")
    token = f"header.{payload}.signature"
    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=HTTPError(
        "https://cluster.example.test", 401, "Unauthorized", {}, None
    )):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, token)

    assert result.status == "invalid"
    assert result.diagnostic == "Kubernetes API rejected credentials (HTTP 401)."
    assert "expired" not in result.diagnostic.lower()


def test_malformed_explicit_kubeconfig_reports_kubectl_failure_without_secret(tmp_path, monkeypatch):
    import subprocess

    source = tmp_path / "malformed-kubeconfig"
    source.write_text("not a kubeconfig")
    secret = "synthetic-secret-token"

    def fail_view(args, **kwargs):
        raise subprocess.CalledProcessError(9, args, stderr=f"parse error {secret}")

    monkeypatch.setattr(kubernetes_auth.subprocess, "run", fail_view)
    with pytest.raises(ValueError) as exc:
        kubernetes_auth._load_kubeconfig(source)

    assert "kubectl config view failed (exit 9)" in str(exc.value)
    assert secret not in str(exc.value)


@pytest.mark.parametrize("ca_data", ["%%%", "bm90IGEgcGVt"])
def test_invalid_local_ca_is_not_reported_as_server_response_error(ca_data):
    with patch.object(kubernetes_auth.urllib.request, "urlopen") as network:
        result = kubernetes_auth.check_token("https://cluster.example.test", ca_data, "synthetic-token")

    assert result.status == "unknown"
    assert result.diagnostic.startswith("Kubernetes CA certificate configuration is invalid:")
    assert "certificate authority data" in result.diagnostic.lower() or "certificate" in result.diagnostic.lower()
    network.assert_not_called()


def test_forbidden_self_subject_review_does_not_claim_authentication():
    from urllib.error import HTTPError
    import io

    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=HTTPError(
        "https://cluster.example.test", 403, "Forbidden", {}, io.BytesIO(b"untrusted body with synthetic-token")
    )):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, "synthetic-token")

    assert result.status == "valid"
    assert result.diagnostic == "Secondary: SelfSubjectReview was denied (HTTP 403); authentication could not be confirmed by this probe."
    assert "authenticated" not in result.diagnostic.lower()
    assert "synthetic-token" not in result.diagnostic


def test_http_error_body_is_not_trusted_as_authentication_diagnostic():
    from urllib.error import HTTPError
    import io

    body = b"synthetic-token, malformed server detail, injected control text"
    with patch.object(kubernetes_auth.urllib.request, "urlopen", side_effect=HTTPError(
        "https://cluster.example.test", 401, "Unauthorized", {}, io.BytesIO(body)
    )):
        result = kubernetes_auth.check_token("https://cluster.example.test", None, "synthetic-token")

    assert result.diagnostic == "Kubernetes API rejected credentials (HTTP 401)."
    assert "synthetic-token" not in result.diagnostic
    assert "injected control text" not in result.diagnostic


def test_auth_action_preserves_local_ca_configuration_diagnostic(tmp_path):
    req = request(tmp_path, {"token": "synthetic-token"})
    target = Path(req["target"]["path"])
    target.write_text('[mcp]\nlocal_port=8765\n[cluster]\napi_server="https://cluster.example.test"\nca_data="%%%"\n')
    req["action"] = "authenticate"

    with patch.object(kubernetes_auth.urllib.request, "urlopen") as network:
        result = load_action().prepare_auth(req)

    assert result == {
        "auth_required": True,
        "diagnostic": "Kubernetes CA certificate configuration is invalid: Invalid certificate authority data",
    }
    network.assert_not_called()
