"""Exercise real MCP transport in a network-isolated container."""
import json
import os
from pathlib import Path
import subprocess
import sys
import time
import urllib.request


def test_loopback_mcp_exposes_read_tools_without_contacting_cluster(tmp_path):
    token=tmp_path/"projected-token";token.write_text("synthetic-token")
    ca=tmp_path/"ca.pem";ca.write_text("synthetic-ca")
    env=dict(os.environ,MCP_KUBERNETES_API_SERVER="https://kubernetes.default.svc",MCP_KUBERNETES_AUTH_MODE="service_account",MCP_KUBERNETES_TOKEN_FILE=str(token),MCP_KUBERNETES_CA_FILE=str(ca),MCP_KUBERNETES_GENERATED_KUBECONFIG=str(tmp_path/"kubeconfig"),MCP_HTTP_PORT="18765",CLUSTER_INSPECTOR_TARGET="fixture")
    process=subprocess.Popen([sys.executable,str(Path(__file__).resolve().parents[1]/"server.py")],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        deadline=time.monotonic()+10
        while time.monotonic()<deadline:
            query=urllib.request.Request("http://127.0.0.1:18765/mcp",data=json.dumps({"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}).encode(),headers={"Content-Type":"application/json","Accept":"application/json, text/event-stream"})
            try:
                with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(query,timeout=1) as response:
                    body=response.read().decode(); result=json.loads(next(line[6:] for line in body.splitlines() if line.startswith("data: "))) if body.startswith("event:") else json.loads(body)
                tools={tool["name"] for tool in result["result"]["tools"]}
                assert {"list_pods","get_resource","get_logs","list_connections","run_query"}.issubset(tools)
                assert "branch_pipeline_runs" not in tools
                return
            except OSError:
                if process.poll() is not None: break
                time.sleep(0.05)
        raise AssertionError("isolated Cluster MCP transport failed to start")
    finally:
        process.terminate()
        stdout,stderr=process.communicate(timeout=5)
        assert process.returncode in (0,-15),stderr
