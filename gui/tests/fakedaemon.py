"""유닉스 소켓 위에서 lightlixd API 를 흉내 내는 테스트용 서버."""

import json
import os
import socketserver
import tempfile
import threading
from http.server import BaseHTTPRequestHandler


class _Handler(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.0"

    def log_message(self, *args):  # noqa: D401 - 테스트 출력을 조용히 한다
        pass

    def address_string(self):
        return "unix"

    def _serve(self):
        length = int(self.headers.get("Content-Length") or 0)
        body = self.rfile.read(length) if length else b""
        parsed = json.loads(body) if body else None
        self.server.calls.append((self.command, self.path, parsed))
        route = self.server.routes.get((self.command, self.path.split("?")[0]))
        if route is None:
            status, payload = 404, {"error": {"code": "not_found", "message": "no route"}}
        else:
            status, payload = route(self.path, parsed) if callable(route) else route
        if isinstance(payload, str):
            data, ctype = payload.encode(), "text/plain; charset=utf-8"
        else:
            data, ctype = json.dumps(payload).encode(), "application/json"
        self.send_response(status)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(data)))
        self.end_headers()
        self.wfile.write(data)

    do_GET = do_POST = do_PUT = do_DELETE = _serve


class _Server(socketserver.ThreadingMixIn, socketserver.UnixStreamServer):
    daemon_threads = True


class FakeDaemon:
    """with FakeDaemon({("GET", "/v1/status"): (200, {...})}) as d: Client(d.path)"""

    def __init__(self, routes=None):
        self.routes = dict(routes or {})
        self.calls = []
        self._dir = tempfile.TemporaryDirectory()
        self.path = os.path.join(self._dir.name, "lightlixd.sock")
        self._server = None

    def __enter__(self):
        self._server = _Server(self.path, _Handler)
        self._server.routes = self.routes
        self._server.calls = self.calls
        threading.Thread(target=self._server.serve_forever, daemon=True).start()
        return self

    def __exit__(self, *exc):
        self._server.shutdown()
        self._server.server_close()
        self._dir.cleanup()
