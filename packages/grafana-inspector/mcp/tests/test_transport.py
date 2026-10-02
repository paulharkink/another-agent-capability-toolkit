"""Real HTTPS vendor fixture and MCP transport; no remote Grafana access."""
import datetime
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import sys
import threading
import time
import urllib.request
from http.server import BaseHTTPRequestHandler, HTTPServer
from cryptography import x509
from cryptography.hazmat.primitives import hashes, serialization
from cryptography.hazmat.primitives.asymmetric import rsa
from cryptography.x509.oid import NameOID


def test_tls_vendor_fixture_and_loopback_mcp_startup(tmp_path):
    key=rsa.generate_private_key(public_exponent=65537,key_size=2048)
    subject=x509.Name([x509.NameAttribute(NameOID.COMMON_NAME,"localhost")])
    now=datetime.datetime.now(datetime.timezone.utc)
    cert=(x509.CertificateBuilder().subject_name(subject).issuer_name(subject).public_key(key.public_key()).serial_number(x509.random_serial_number()).not_valid_before(now-datetime.timedelta(days=1)).not_valid_after(now+datetime.timedelta(days=1)).add_extension(x509.SubjectAlternativeName([x509.DNSName("localhost")]),critical=False).add_extension(x509.BasicConstraints(ca=True,path_length=None),critical=True).sign(key,hashes.SHA256()))
    certfile=tmp_path/"ca.pem";keyfile=tmp_path/"key.pem"
    certfile.write_bytes(cert.public_bytes(serialization.Encoding.PEM));keyfile.write_bytes(key.private_bytes(serialization.Encoding.PEM,serialization.PrivateFormat.PKCS8,serialization.NoEncryption()))
    calls=[]
    class Handler(BaseHTTPRequestHandler):
        def do_GET(self):
            calls.append(self.path)
            assert self.headers.get("Authorization")=="Bearer synthetic-token"
            if self.path!="/api/datasources":self.send_error(404);return
            body=b'[]';self.send_response(200);self.send_header("Content-Length",str(len(body)));self.end_headers();self.wfile.write(body)
        def log_message(self,*args):pass
    fixture=HTTPServer(("127.0.0.1",0),Handler)
    context=ssl.SSLContext(ssl.PROTOCOL_TLS_SERVER);context.load_cert_chain(certfile,keyfile)
    fixture.socket=context.wrap_socket(fixture.socket,server_side=True)
    thread=threading.Thread(target=fixture.serve_forever,daemon=True);thread.start()
    target=tmp_path/"target.toml"
    target.write_text(f'[mcp]\nlocal_port=18766\n[grafana]\nurl="https://localhost:{fixture.server_port}"\nauth_mode="api_token"\nca_file="ca.pem"\nverify_x509_strict=false\n')
    env=dict(os.environ,GRAFANA_CONFIG=str(target),GRAFANA_API_TOKEN="synthetic-token",GRAFANA_TARGET="fixture",GRAFANA_AUTH_FILE=str(tmp_path/"absent-auth.json"),MCP_HTTP_PORT="18766",GRAFANA_AUTH_MODE="api_token")
    process=subprocess.Popen([sys.executable,str(Path(__file__).resolve().parents[1]/"server.py")],env=env,stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True)
    try:
        deadline=time.monotonic()+10
        while time.monotonic()<deadline:
            query=urllib.request.Request("http://127.0.0.1:18766/mcp",data=json.dumps({"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}).encode(),headers={"Content-Type":"application/json","Accept":"application/json, text/event-stream"})
            try:
                with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(query,timeout=1) as response: body=response.read().decode(); result=json.loads(next(line[6:] for line in body.splitlines() if line.startswith("data: "))) if body.startswith("event:") else json.loads(body)
                tools={tool["name"] for tool in result["result"]["tools"]}
                assert {"grafana_health","list_datasources","get_dashboard","loki_query_range","datasource_query_range"}.issubset(tools)
                assert calls==["/api/datasources"]
                return
            except OSError:
                if process.poll() is not None:break
                time.sleep(0.05)
        raise AssertionError("isolated Grafana MCP transport failed to start")
    finally:
        process.terminate();stdout,stderr=process.communicate(timeout=5)
        fixture.shutdown();fixture.server_close();thread.join(2)
        assert process.returncode in (0,-15),stderr
