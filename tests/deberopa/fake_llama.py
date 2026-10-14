import json
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer

LOG = open("/tmp/fake-llama.log", "a", buffering=1)


class H(BaseHTTPRequestHandler):
    protocol_version = "HTTP/1.1"

    def log_message(self, *a):
        pass

    def send_json(self, obj, status=200):
        b = json.dumps(obj).encode()
        self.send_response(status)
        self.send_header("Content-Type", "application/json")
        self.send_header("Content-Length", str(len(b)))
        self.end_headers()
        self.wfile.write(b)

    def do_GET(self):
        LOG.write("GET %s auth=%r\n" % (self.path, self.headers.get("Authorization")))
        if self.path == "/props":
            return self.send_json({"default_generation_settings": {"n_ctx": 32768}, "total_slots": 2})
        if self.path in ("/v1/models", "/models"):
            return self.send_json({"object": "list", "data": [{"id": "fake.gguf", "object": "model"}]})
        if self.path == "/health":
            return self.send_json({"status": "ok"})
        self.send_json({"error": {"message": "not found"}}, 404)

    def do_POST(self):
        n = int(self.headers.get("Content-Length", "0"))
        body = json.loads(self.rfile.read(n) or b"{}")
        LOG.write("POST %s stream=%s tools=%d auth=%r\n" % (self.path, body.get("stream"), len(body.get("tools") or []), self.headers.get("Authorization")))
        if self.path != "/v1/chat/completions":
            return self.send_json({"error": {"message": "not found"}}, 404)
        if not body.get("stream"):
            return self.send_json({"choices": [{"message": {"role": "assistant", "content": "Hello from the fake model."}, "finish_reason": "stop"}],
                                   "usage": {"prompt_tokens": 12, "completion_tokens": 6, "total_tokens": 18}})
        self.send_response(200)
        self.send_header("Content-Type", "text/event-stream")
        self.send_header("Transfer-Encoding", "chunked")
        self.end_headers()

        def chunk(s):
            data = s.encode()
            self.wfile.write(b"%x\r\n%s\r\n" % (len(data), data))
            self.wfile.flush()

        for word in ("Hello ", "from ", "the ", "fake ", "model."):
            chunk("data: " + json.dumps({"choices": [{"index": 0, "delta": {"content": word}}]}) + "\n\n")
        chunk("data: " + json.dumps({"choices": [{"index": 0, "delta": {}, "finish_reason": "stop"}]}) + "\n\n")
        chunk("data: " + json.dumps({"choices": [], "usage": {"prompt_tokens": 12, "completion_tokens": 5, "total_tokens": 17}}) + "\n\n")
        chunk("data: [DONE]\n\n")
        self.wfile.write(b"0\r\n\r\n")
        self.wfile.flush()


ThreadingHTTPServer(("127.0.0.1", 18080), H).serve_forever()
