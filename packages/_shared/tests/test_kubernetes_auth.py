import base64
import json
import os
import ssl
import threading
import time
from pathlib import Path
from unittest.mock import patch

import pytest
import yaml

import sys
sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from kubernetes_auth import (
    _ssl_context,
    check_token,
    copy_kubeconfig,
    decode_service_account_token,
    load_cached_auth,
    prepare_kubeconfig_auth,
    prepare_token_auth,
    start_token_renewal,
    write_token_kubeconfig,
    resolve_runtime_kubeconfig,
)


def test_target_can_disable_strict_x509_checks_without_disabling_tls_verification():
    context = _ssl_context(None, verify_x509_strict=False)

    strict_flag = getattr(ssl, "VERIFY_X509_STRICT", 0)
    assert not context.verify_flags & strict_flag
    assert context.verify_mode == ssl.CERT_REQUIRED
    assert context.check_hostname


def test_token_check_uses_target_x509_strictness_setting():
    from urllib.error import HTTPError

    contexts = []

    def unauthorized(_request, **kwargs):
        contexts.append(kwargs["context"])
        raise HTTPError("https://cluster.invalid", 401, "Unauthorized", {}, None)

    with patch("urllib.request.urlopen", unauthorized):
        result = check_token("https://cluster.invalid", None, "token", verify_x509_strict=False)

    assert result.status == "invalid"
    assert contexts[0].verify_mode == ssl.CERT_REQUIRED
    assert contexts[0].check_hostname
    assert not contexts[0].verify_flags & getattr(ssl, "VERIFY_X509_STRICT", 0)


def test_projected_service_account_uses_rotating_token_file_and_ca(tmp_path):
    token_file = tmp_path / "projected/token"
    ca_file = tmp_path / "projected/ca.crt"
    token_file.parent.mkdir()
    token_file.write_text("rotating-secret")
    ca_file.write_text("ca pem")
    generated = tmp_path / "state/kubeconfig"
    env = {"MCP_KUBERNETES_AUTH_MODE": "service_account", "MCP_KUBERNETES_TOKEN_FILE": str(token_file), "MCP_KUBERNETES_CA_FILE": str(ca_file), "MCP_KUBERNETES_GENERATED_KUBECONFIG": str(generated)}
    with patch.dict(os.environ, env, clear=True):
        assert resolve_runtime_kubeconfig("https://cluster.invalid") == generated
    config = json.loads(generated.read_text())
    assert config["users"][0]["user"] == {"tokenFile": str(token_file)}
    assert config["clusters"][0]["cluster"] == {"server": "https://cluster.invalid", "certificate-authority": str(ca_file)}
    assert "rotating-secret" not in generated.read_text()


def test_projected_kubeconfig_does_not_change_existing_shared_directory_permissions(tmp_path):
    shared = tmp_path / "shared"
    shared.mkdir(mode=0o755)
    shared.chmod(0o755)
    token_file = tmp_path / "token"
    ca_file = tmp_path / "ca.crt"
    token_file.write_text("token")
    ca_file.write_text("ca")
    env = {"MCP_KUBERNETES_AUTH_MODE": "service_account", "MCP_KUBERNETES_TOKEN_FILE": str(token_file), "MCP_KUBERNETES_CA_FILE": str(ca_file), "MCP_KUBERNETES_GENERATED_KUBECONFIG": str(shared / "kubeconfig")}
    with patch.dict(os.environ, env, clear=True):
        resolve_runtime_kubeconfig("https://cluster.invalid")
    assert shared.stat().st_mode & 0o777 == 0o755


def test_projected_token_symlink_path_is_preserved_for_rotation(tmp_path):
    first = tmp_path / "token-v1"
    first.write_text("first")
    projected = tmp_path / "projected-token"
    projected.symlink_to(first)
    ca_file = tmp_path / "ca.crt"
    ca_file.write_text("ca")
    dest = tmp_path / "private/kubeconfig"
    env = {"MCP_KUBERNETES_AUTH_MODE": "service_account", "MCP_KUBERNETES_TOKEN_FILE": str(projected), "MCP_KUBERNETES_CA_FILE": str(ca_file), "MCP_KUBERNETES_GENERATED_KUBECONFIG": str(dest)}
    with patch.dict(os.environ, env, clear=True):
        resolve_runtime_kubeconfig("https://cluster.invalid")
    assert json.loads(dest.read_text())["users"][0]["user"]["tokenFile"] == str(projected)


def test_explicit_mounted_kubeconfig_rejects_wrong_api_and_never_uses_host_default(tmp_path):
    source = tmp_path / "mounted.json"
    source.write_text(json.dumps({"clusters": [{"name": "c", "cluster": {"server": "https://wrong.invalid"}}], "contexts": [{"name": "x", "context": {"cluster": "c", "user": "u"}}], "current-context": "x", "users": [{"name": "u", "user": {"token": "secret"}}]}))
    with patch.dict(os.environ, {"MCP_KUBERNETES_AUTH_MODE": "kubeconfig", "MCP_KUBERNETES_KUBECONFIG": str(source)}, clear=True):
        with pytest.raises(ValueError, match="API server"):
            resolve_runtime_kubeconfig("https://cluster.invalid")
    with patch.dict(os.environ, {"MCP_KUBERNETES_AUTH_MODE": "kubeconfig", "HOME": str(tmp_path)}, clear=True):
        with pytest.raises(ValueError, match="MCP_KUBERNETES_KUBECONFIG"):
            resolve_runtime_kubeconfig("https://cluster.invalid")


def token(claims):
    payload = base64.urlsafe_b64encode(json.dumps(claims).encode()).decode().rstrip("=")
    return f"header.{payload}.signature"


def claims(expiry=None):
    result = {"sub": "system:serviceaccount:build:robot"}
    if expiry is not None:
        result["exp"] = expiry
    return result


def test_decode_service_account_token_returns_username_namespace_name_and_expiry():
    exp = time.time() + 3600
    identity = decode_service_account_token(token(claims(exp)), now=time.time())
    assert (identity.username, identity.namespace, identity.name, identity.expires_at) == (
        "system:serviceaccount:build:robot", "build", "robot", exp
    )


def test_decode_service_account_token_rejects_expired_token():
    with pytest.raises(ValueError, match="expired"):
        decode_service_account_token(token(claims(100)), now=101)


def test_decode_service_account_token_rejects_non_expiring_token():
    with pytest.raises(ValueError, match="exp"):
        decode_service_account_token(token(claims()), now=1)


@pytest.mark.parametrize("value", ["not-a-jwt", token({"sub": "user", "exp": 9999999999}), token({"sub": "system:serviceaccount:bad", "exp": 9999999999}), token({"sub": "system:serviceaccount:n:s", "exp": "9999999999"})])
def test_decode_service_account_token_rejects_malformed_or_non_service_account_claims(value):
    with pytest.raises(ValueError):
        decode_service_account_token(value, now=1)


def test_check_token_classifies_401_403_and_transport_failure():
    # Response/error fixtures are isolated from any live cluster.
    from urllib.error import HTTPError, URLError
    def request(code):
        def fake(*args, **kwargs):
            if isinstance(code, Exception): raise code
            raise HTTPError("https://cluster.invalid", code, "", {}, None)
        return fake
    with patch("urllib.request.urlopen", request(401)):
        assert check_token("https://cluster.invalid", None, "secret").status == "invalid"
    with patch("urllib.request.urlopen", request(403)):
        assert check_token("https://cluster.invalid", None, "secret").status == "valid"
    with patch("urllib.request.urlopen", request(403)):
        assert check_token("https://cluster.invalid", None, "secret").username is None
    with patch("urllib.request.urlopen", request(URLError("offline"))):
        assert check_token("https://cluster.invalid", None, "secret").status == "unknown"


def test_write_token_kubeconfig_uses_0600_and_never_puts_token_in_argv(tmp_path):
    path = write_token_kubeconfig("https://cluster.invalid", None, tmp_path / "kubeconfig", "never-log-me")
    assert path.stat().st_mode & 0o777 == 0o600
    assert "never-log-me" in path.read_text()


def test_copy_kubeconfig_embeds_referenced_files_and_returns_certificate_username(tmp_path):
    source = tmp_path / "source.yaml"
    source.write_text("placeholder")
    dest = tmp_path / "state" / "kubeconfig"
    views = [{"clusters": [{"name": "c", "cluster": {"server": "https://cluster.invalid", "certificate-authority-data": "Y2E="}}], "users": [{"name": "u", "user": {"client-certificate-data": "Y2VydA==", "client-key-data": "a2V5"}}], "contexts": [{"name": "x", "context": {"cluster": "c", "user": "u"}}], "current-context": "x"}]
    def run(args, **kwargs):
        assert "--kubeconfig" in args
        if "view" in args:
            assert all(flag in args for flag in ("--raw", "--flatten", "--minify"))
            return type("R", (), {"stdout": json.dumps(views[0])})()
        candidate = Path(args[args.index("--kubeconfig") + 1])
        assert candidate.parent == dest.parent
        assert json.loads(candidate.read_text()) == views[0]
        assert candidate.stat().st_mode & 0o777 == 0o600
        assert not dest.exists()
        return type("R", (), {"stdout": json.dumps({"status": {"userInfo": {"username": "certificate-user"}}})})()
    with patch("subprocess.run", run):
        assert copy_kubeconfig(source, dest, "https://cluster.invalid") == "certificate-user"
    assert dest.stat().st_mode & 0o777 == 0o600
    assert "Y2VydA==" in dest.read_text()
    assert "a2V5" in dest.read_text()


@pytest.mark.parametrize("user", [{"exec": {"command": "evil"}}, {"auth-provider": {"name": "gcp"}}])
def test_copy_kubeconfig_rejects_missing_or_external_exec_credentials(tmp_path, user):
    source, dest = tmp_path / "source", tmp_path / "state" / "config"
    source.write_text("source")
    data = {"clusters": [{"name": "c", "cluster": {"server": "https://cluster.invalid"}}], "users": [{"name": "u", "user": user}], "contexts": [{"name": "x", "context": {"cluster": "c", "user": "u"}}], "current-context": "x"}
    with patch("subprocess.run", return_value=type("R", (), {"stdout": json.dumps(data)})()):
        with pytest.raises(ValueError): copy_kubeconfig(source, dest, "https://cluster.invalid")


def test_load_cached_auth_migrates_valid_legacy_cluster_and_tekton_files(tmp_path):
    for filename in ("kubeconfig", "config"):
        state = tmp_path / filename
        state.mkdir()
        credential = state / filename
        credential.write_text(yaml.safe_dump({"apiVersion":"v1", "kind":"Config", "clusters":[{"name":"c","cluster":{"server":"https://cluster.invalid"}}], "users":[{"name":"u","user":{"client-certificate-data":"Y2VydA==","client-key-data":"a2V5"}}], "contexts":[{"name":"x","context":{"cluster":"c","user":"u"}}], "current-context":"x"}))
        def kubectl(command, **kwargs):
            if "view" in command:
                data = {"apiVersion":"v1", "kind":"Config", "clusters":[{"name":"c","cluster":{"server":"https://cluster.invalid"}}], "users":[{"name":"u","user":{"client-certificate-data":"Y2VydA==","client-key-data":"a2V5"}}], "contexts":[{"name":"x","context":{"cluster":"c","user":"u"}}], "current-context":"x"}
                return type("R", (), {"stdout": json.dumps(data)})()
            return type("R", (), {"stdout": json.dumps({"status":{"userInfo":{"username":"certificate-user"}}})})()
        with patch("subprocess.run", side_effect=kubectl) as cli:
            result = load_cached_auth(state, credential, "https://cluster.invalid", None)
        assert "--kubeconfig" in cli.call_args.args[0]
        assert result.status == "valid"
        assert json.loads((state / "auth.json").read_text())["username"] == "certificate-user"
        assert json.loads((state / "auth.json").read_text())["mode"] == "kubeconfig"
        assert (state / "auth.json").stat().st_mode & 0o777 == 0o600



class _Response:
    def __init__(self, body): self.body = body
    def __enter__(self): return self
    def __exit__(self, *args): return False
    def read(self): return self.body


def test_renewal_requests_same_service_account_and_bounded_ttl(tmp_path):
    path = write_token_kubeconfig("https://cluster.invalid", None, tmp_path / "kubeconfig", token(claims(time.time()+120)))
    stop = threading.Event()
    renewed = token(claims(time.time()+3600))
    def respond(request, **kwargs):
        assert request.full_url.endswith("/namespaces/build/serviceaccounts/robot/token")
        assert json.loads(request.data)["spec"]["expirationSeconds"] == 3600
        assert request.headers["Authorization"].startswith("Bearer header.")
        stop.set()
        return _Response(json.dumps({"status":{"token":renewed}}).encode())
    with patch("urllib.request.urlopen", respond):
        worker = start_token_renewal(path, "https://cluster.invalid", None, stop)
        worker.join(2)
    assert not worker.is_alive()
    assert yaml.safe_load(path.read_text())["users"][0]["user"]["token"] == renewed


def test_renewal_uses_returned_expiry_after_server_caps_ttl(tmp_path):
    path = write_token_kubeconfig("https://cluster.invalid", None, tmp_path / "kubeconfig", token(claims(time.time()+120)))
    renewed_exp = time.time() + 900
    renewed = token(claims(renewed_exp))
    class Stop:
        def __init__(self): self.waits=[]
        def is_set(self): return False
        def wait(self, seconds):
            self.waits.append(seconds)
            return len(self.waits) == 2
    stop = Stop()
    def respond(*args, **kwargs): return _Response(json.dumps({"status":{"token":renewed}}).encode())
    with patch("urllib.request.urlopen", respond):
        worker = start_token_renewal(path, "https://cluster.invalid", None, stop)
        worker.join(2)
    assert len(stop.waits) == 2
    assert stop.waits[1] == pytest.approx(max(0, renewed_exp-time.time()-300), abs=2)


def test_forbidden_renewal_stops_quietly_and_keeps_current_auth(tmp_path):
    from urllib.error import HTTPError
    path = write_token_kubeconfig("https://cluster.invalid", None, tmp_path / "kubeconfig", token(claims(time.time()+120)))
    before = path.read_bytes()
    with patch("urllib.request.urlopen", side_effect=HTTPError("https://cluster.invalid",403,"forbidden",{},None)):
        worker = start_token_renewal(path, "https://cluster.invalid", None)
        worker.join(2)
    assert not worker.is_alive()
    assert path.read_bytes() == before


def test_renewal_atomically_replaces_only_its_kubeconfig(tmp_path):
    path = write_token_kubeconfig("https://cluster.invalid", None, tmp_path / "kubeconfig", token(claims(time.time()+120)))
    other = tmp_path / "other-config"
    other.write_text("unchanged")
    before_inode = path.stat().st_ino
    renewed = token(claims(time.time()+3600))
    stop = threading.Event()
    def respond(*args, **kwargs):
        stop.set()
        return _Response(json.dumps({"status":{"token":renewed}}).encode())
    with patch("urllib.request.urlopen", respond):
        worker = start_token_renewal(path, "https://cluster.invalid", None, stop)
        worker.join(2)
    assert path.stat().st_ino != before_inode
    assert yaml.safe_load(path.read_text())["users"][0]["user"]["token"] == renewed
    assert other.read_text() == "unchanged"


def test_prepare_token_auth_writes_only_to_target_and_marker_has_no_token(tmp_path):
    state = tmp_path / "target"
    credential = state / "kubeconfig"
    secret = token(claims(time.time()+3600))
    with patch("kubernetes_auth.check_token", return_value=type("C", (), {"status":"valid", "username":"system:serviceaccount:build:robot"})()):
        result = prepare_token_auth(state, credential, "https://cluster.invalid", None, secret)
    marker = json.loads((state / "auth.json").read_text())
    assert result.status == "valid"
    assert marker == {"mode":"token", "username":"system:serviceaccount:build:robot", "namespace":"build", "service_account":"robot", "expires_at":pytest.approx(decode_service_account_token(secret).expires_at)}
    assert secret not in (state / "auth.json").read_text()
    assert credential.stat().st_mode & 0o777 == 0o600
    assert state.stat().st_mode & 0o777 == 0o700


def test_prepare_token_auth_accepts_api_valid_opaque_bearer_token(tmp_path):
    state = tmp_path / "target"
    credential = state / "kubeconfig"
    opaque = "openshift-user-token"
    check = type("C", (), {"status": "valid", "username": "user:example"})()
    with patch("kubernetes_auth.decode_service_account_token", side_effect=ValueError("not a service-account JWT")), \
            patch("kubernetes_auth.check_token", return_value=check):
        result = prepare_token_auth(state, credential, "https://cluster.invalid", None, opaque)

    marker = json.loads((state / "auth.json").read_text())
    config = yaml.safe_load(credential.read_text())
    assert result.status == "valid"
    assert marker == {"mode": "token", "username": "user:example"}
    assert config["users"][0]["user"]["token"] == opaque


def test_opaque_bearer_token_does_not_enter_service_account_renewal(tmp_path):
    path = tmp_path / "kubeconfig"
    write_token_kubeconfig("https://cluster.invalid", None, path, "opaque-token")
    with patch("kubernetes_auth.urllib.request.urlopen") as urlopen:
        worker = start_token_renewal(path, "https://cluster.invalid", None)
        worker.join(2)
    assert not worker.is_alive()
    urlopen.assert_not_called()


def test_auth_file_path_must_stay_inside_target_directory(tmp_path):
    secret = token(claims(time.time()+3600))
    with patch("kubernetes_auth.check_token", return_value=type("C", (), {"status":"valid", "username":"sa"})()):
        with pytest.raises(ValueError, match="target state"):
            prepare_token_auth(tmp_path / "target", tmp_path / "other" / "kubeconfig", "https://cluster.invalid", None, secret)


def test_copy_kubeconfig_rejects_symlink_to_default_kubeconfig_without_using_it(tmp_path, monkeypatch):
    fake_home = tmp_path / "home"
    default = fake_home / ".kube" / "config"
    default.parent.mkdir(parents=True)
    default.write_text("do not read")
    alias = tmp_path / "chosen-config"
    alias.symlink_to(default)
    monkeypatch.setattr(Path, "home", classmethod(lambda cls: fake_home))
    with pytest.raises(ValueError, match="default kubeconfig"):
        copy_kubeconfig(alias, tmp_path / "state" / "kubeconfig", "https://cluster.invalid")


def test_copy_kubeconfig_rejects_unflattened_external_file_references(tmp_path):
    source, dest = tmp_path / "source", tmp_path / "state" / "config"
    source.write_text("source")
    data = {"clusters":[{"name":"c","cluster":{"server":"https://cluster.invalid","certificate-authority":"/outside/ca.crt"}}], "users":[{"name":"u","user":{"client-key":"/outside/key.pem"}}], "contexts":[{"name":"x","context":{"cluster":"c","user":"u"}}], "current-context":"x"}
    with patch("subprocess.run", return_value=type("R", (), {"stdout":json.dumps(data)})()):
        with pytest.raises(ValueError): copy_kubeconfig(source, dest, "https://cluster.invalid")

def test_rejected_explicit_kubeconfig_preserves_previous_target_credential(tmp_path):
    import subprocess
    from types import SimpleNamespace
    source=tmp_path/"selected";source.write_text("explicit fixture")
    destination=tmp_path/"state/kubeconfig";destination.parent.mkdir();destination.write_text("previous private credential")
    document={"clusters":[{"cluster":{"server":"https://cluster.invalid"}}],"users":[{"user":{"token":"rejected-fixture-token"}}]}
    def run(args,**kwargs):
        if args[1:3]==["config","view"]:return SimpleNamespace(stdout=json.dumps(document))
        raise subprocess.CalledProcessError(1,args)
    with patch("kubernetes_auth.subprocess.run",side_effect=run):
        with pytest.raises(ValueError):copy_kubeconfig(source,destination,"https://cluster.invalid")
    assert destination.read_text()=="previous private credential"
