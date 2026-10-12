import os
import socket
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from diparnux_cc import api  # noqa: E402
from tests.fakedaemon import FakeDaemon  # noqa: E402


class ClientTests(unittest.TestCase):
    def test_status_roundtrip(self):
        routes = {("GET", "/v1/status"): (200, {"version": "0.2.0", "mode": "server"})}
        with FakeDaemon(routes) as d:
            st = api.Client(d.path).status()
        self.assertEqual(st["version"], "0.2.0")
        self.assertEqual(d.calls[0][:2], ("GET", "/v1/status"))

    def test_error_body_becomes_daemon_error(self):
        routes = {("POST", "/v1/mode"): (409, {"error": {"code": "busy", "message": "a server operation is in progress"}})}
        with FakeDaemon(routes) as d:
            with self.assertRaises(api.DaemonError) as cm:
                api.Client(d.path).set_mode("server")
        self.assertEqual(cm.exception.code, "busy")
        self.assertEqual(cm.exception.status, 409)
        self.assertIn("in progress", cm.exception.message)

    def test_non_json_error_keeps_status(self):
        with FakeDaemon({("GET", "/v1/status"): (502, "bad gateway")}) as d:
            with self.assertRaises(api.DaemonError) as cm:
                api.Client(d.path).status()
        self.assertEqual(cm.exception.code, "http_502")

    def test_missing_socket_is_unavailable(self):
        with self.assertRaises(api.DaemonUnavailable) as cm:
            api.Client("/nonexistent/diparnuxd.sock").status()
        self.assertEqual(cm.exception.code, "unavailable")

    def test_permission_denied_is_distinct(self):
        with mock.patch.object(socket.socket, "connect", side_effect=PermissionError()):
            with self.assertRaises(api.DaemonUnavailable) as cm:
                api.Client("/run/diparnux/diparnuxd.sock").status()
        self.assertEqual(cm.exception.code, "permission_denied")
        self.assertIn("diparnux", cm.exception.message)

    def test_query_parameters_skip_none_and_escape(self):
        routes = {("GET", "/v1/server/plan"): (200, {"model_id": "m"})}
        with FakeDaemon(routes) as d:
            api.Client(d.path).plan(model="a b.gguf", ctx=4096, batch=None)
        self.assertEqual(d.calls[0][1], "/v1/server/plan?model=a+b.gguf&ctx=4096")

    def test_model_id_is_path_escaped(self):
        routes = {("DELETE", "/v1/models/a%2Fb.gguf"): (200, {"id": "a/b.gguf", "deleted": True})}
        with FakeDaemon(routes) as d:
            res = api.Client(d.path).delete_model("a/b.gguf")
        self.assertTrue(res["deleted"])

    def test_request_bodies(self):
        routes = {
            ("PUT", "/v1/system/ssh"): (200, {"enabled": True}),
            ("PUT", "/v1/firstboot"): (200, {"done": True}),
            ("PUT", "/v1/config"): (200, {"config": {}, "restart_required": False}),
            ("POST", "/v1/server/chat"): (200, {"content": "hi", "tokens_predicted": 1, "tokens_per_second": 5.0}),
        }
        with FakeDaemon(routes) as d:
            c = api.Client(d.path)
            c.set_ssh(True)
            c.complete_firstboot(["gpu", "model"])
            c.set_config(lan_exposed=True)
            c.chat([{"role": "user", "content": "hello"}], max_tokens=32)
        bodies = {(m, p): b for m, p, b in d.calls}
        self.assertEqual(bodies[("PUT", "/v1/system/ssh")], {"enabled": True})
        self.assertEqual(bodies[("PUT", "/v1/firstboot")], {"done": True, "skipped": ["gpu", "model"]})
        self.assertEqual(bodies[("PUT", "/v1/config")], {"lan_exposed": True})
        self.assertEqual(bodies[("POST", "/v1/server/chat")]["max_tokens"], 32)

    def test_user_calls(self):
        routes = {
            ("GET", "/v1/users"): (200, {"users": []}),
            ("POST", "/v1/users"): (201, {"name": "alice", "admin": False, "created": "x"}),
            ("DELETE", "/v1/users/alice"): (200, {"name": "alice", "deleted": True, "home_kept": True}),
        }
        with FakeDaemon(routes) as d:
            c = api.Client(d.path)
            self.assertEqual(c.users(), {"users": []})
            c.create_user("alice", "correct-horse-9")
            c.delete_user("alice")
        bodies = {(m, p): b for m, p, b in d.calls}
        self.assertEqual(bodies[("POST", "/v1/users")], {"name": "alice", "password": "correct-horse-9", "admin": False})

    def test_report_is_returned_as_text(self):
        with FakeDaemon({("GET", "/v1/report"): (200, "Diparnux report\nline 2\n")}) as d:
            text = api.Client(d.path).report()
        self.assertTrue(text.startswith("Diparnux report"))


if __name__ == "__main__":
    unittest.main()
