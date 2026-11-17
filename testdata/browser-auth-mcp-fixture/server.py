import json
import os
import ssl
import urllib.error
import urllib.request
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

with open('/run/aact/vault.json', 'r', encoding='utf-8') as f:
    handoff = json.load(f)


def credential():
    context = ssl.create_default_context(cadata=handoff['certificate'])
    payload = json.dumps({'selector': {'kind': 'cookie', 'match': 'exact', 'names': ['session']}}).encode()
    request = urllib.request.Request(
        handoff['api_address'] + '/v1/browser/credentials',
        data=payload,
        headers={'Authorization': 'Bearer ' + handoff['token'], 'Content-Type': 'application/json'},
    )
    opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=context))
    try:
        with opener.open(request, timeout=40) as response:
            return response.status, json.loads(response.read())
    except urllib.error.HTTPError as error:
        return error.code, json.loads(error.read())


class Handler(BaseHTTPRequestHandler):
    def respond(self, value, status=200):
        body = json.dumps(value).encode()
        self.send_response(status)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers.get('Content-Length', '0'))))
        method = request.get('method')
        request_id = request.get('id')
        if method == 'initialize':
            return self.respond({'jsonrpc': '2.0', 'id': request_id, 'result': {
                'protocolVersion': '2024-11-05', 'capabilities': {'tools': {}},
                'serverInfo': {'name': 'synthetic-auth-capability', 'version': '1'},
            }})
        if method == 'notifications/initialized':
            self.send_response(202)
            self.end_headers()
            return
        if method == 'tools/list':
            return self.respond({'jsonrpc': '2.0', 'id': request_id, 'result': {'tools': [
                {'name': 'list_projects', 'description': 'List projects from the authenticated synthetic API',
                 'inputSchema': {'type': 'object', 'properties': {}, 'additionalProperties': False}},
            ]}})
        if method != 'tools/call' or request.get('params', {}).get('name') != 'list_projects':
            return self.respond({'jsonrpc': '2.0', 'id': request_id, 'error': {'code': -32601, 'message': 'unknown MCP method or tool'}})
        status, auth = credential()
        if status != 200:
            viewer = auth.get('viewer_url', '')
            return self.respond({'jsonrpc': '2.0', 'id': request_id, 'result': {
                'isError': True, 'content': [{'type': 'text', 'text': 'authentication required ' + viewer}],
            }})
        access_token = auth.get('result', {}).get('access_token', '')
        upstream = urllib.request.Request(os.environ['AACT_SYNTHETIC_UPSTREAM'] + '/projects', headers={'Authorization': 'Bearer ' + access_token})
        try:
            with urllib.request.urlopen(upstream, timeout=10) as response:
                result = json.loads(response.read())
        except urllib.error.HTTPError as error:
            return self.respond({'jsonrpc': '2.0', 'id': request_id, 'result': {
                'isError': True, 'content': [{'type': 'text', 'text': 'upstream rejected credentials: ' + str(error.code)}],
            }})
        return self.respond({'jsonrpc': '2.0', 'id': request_id, 'result': {
            'content': [{'type': 'text', 'text': json.dumps(result)}],
        }})

    def log_message(self, *_args):
        pass


ThreadingHTTPServer(('0.0.0.0', 8765), Handler).serve_forever()
