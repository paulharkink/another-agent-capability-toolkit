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
        assert module.prepare_auth(req) == {"auth_required":True}
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
