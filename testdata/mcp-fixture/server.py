import json
import threading
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

initialized = threading.Event()

class Handler(BaseHTTPRequestHandler):
    def do_POST(self):
        request = json.loads(self.rfile.read(int(self.headers['Content-Length'])))
        if self.path.startswith('/messages'):
            if request.get('method') != 'initialize':
                self.send_response(400)
                self.end_headers()
                return
            initialized.set()
            self.send_response(202)
            self.end_headers()
            return
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
        if self.path == '/sse':
            initialized.clear()
            self.send_response(200)
            self.send_header('Content-Type', 'text/event-stream')
            self.end_headers()
            self.wfile.write(b'event: endpoint\ndata: /messages?sessionId=fixture\n\n')
            self.wfile.flush()
            if not initialized.wait(5):
                return
            with open('/fixture/profile.txt') as profile:
                name = profile.read().strip()
            body = json.dumps({'jsonrpc': '2.0', 'id': 1, 'result': {
                'protocolVersion': '2024-11-05', 'capabilities': {},
                'serverInfo': {'name': name, 'version': '1'}}})
            self.wfile.write(('event: message\ndata: ' + body + '\n\n').encode())
            self.wfile.flush()
            return
        self.send_response(200)
        self.send_header('Content-Type', 'text/event-stream')
        self.end_headers()
        self.wfile.write(b'event: endpoint\ndata: /messages?sessionId=fixture\n\n')
        self.wfile.flush()

ThreadingHTTPServer(('0.0.0.0', 8765), Handler).serve_forever()
