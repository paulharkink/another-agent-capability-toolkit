import json
from http.server import BaseHTTPRequestHandler, HTTPServer

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        with open('/fixture/profile.txt') as profile:
            name = profile.read().strip()
        body = json.dumps({'jsonrpc': '2.0', 'id': request['id'], 'result': {
            'protocolVersion': '2024-11-05', 'capabilities': {},
            'serverInfo': {'name': name, 'version': '1'}}}).encode()
        self.send_response(200)
        self.send_header('Content-Type', 'application/json')
        self.send_header('Content-Length', str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def do_GET(self):
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        self.wfile.write(b'event: endpoint\ndata: /messages\n\n')
        self.wfile.flush()

HTTPServer(('0.0.0.0', 8765), Handler).serve_forever()
