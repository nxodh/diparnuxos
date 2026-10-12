"""GTK 화면이 가짜 데몬과 함께 예외 없이 만들어지고 값을 채우는지 본다.

디스플레이가 없거나 PyGObject 가 없으면 건너뛴다(Docker 의 xvfb 에서 실행한다).
"""

import os
import sys
import time
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
os.environ.setdefault("GSK_RENDERER", "cairo")

try:
    import gi

    gi.require_version("Gtk", "4.0")
    gi.require_version("Adw", "1")
    from gi.repository import Adw, GLib, Gtk

    HAVE_GTK = Gtk.init_check()
except (ImportError, ValueError):
    HAVE_GTK = False

from tests.fakedaemon import FakeDaemon  # noqa: E402

STATUS = {
    "version": "0.2.0",
    "mode": "server",
    "gpu": {
        "status": {
            "state": "ready", "message": "ok", "present": True, "pci_count": 1, "count": 1, "usable_count": 1,
            "usable_indexes": [0], "secure_boot": "disabled", "driver_version": "595.91.07",
            "gpus": [{"index": 0, "name": "RTX 3060 Laptop", "compute_cap": "8.6", "supported": True, "reason": "ok"}],
        },
        "open_module": "yes", "secure_boot": "disabled", "driver_version": "595.91.07",
        "nvml": {"available": True, "cuda_driver_version": "13.2", "devices": [
            {"index": 0, "name": "RTX 3060 Laptop", "arch": "ampere", "compute_cap": "8.6", "memory_total_mib": 6144,
             "memory_free_mib": 5632, "memory_used_mib": 512, "temperature_c": 50, "power_mw": 15000,
             "util_gpu_percent": 2, "util_mem_percent": 1}]},
    },
    "memory": {"total_mib": 16000, "available_mib": 12000},
    "server": {"state": "stopped", "model_id": "", "warnings": [], "offload": {"requested": "", "offloaded": 0, "total": 0, "verified": False}, "plan": None},
}

ROUTES = {
    ("GET", "/v1/status"): (200, STATUS),
    ("GET", "/v1/models"): (200, {"default": "a.gguf", "models": [
        {"id": "a.gguf", "size_bytes": 1185376, "architecture": "llama", "license": "mit", "default": True, "incomplete": False}]}),
    ("GET", "/v1/recommendations"): (200, {"basis": "gpu", "hardware": {"vram_mib": 6144, "ram_mib": 16000, "gpu_state": "ready"},
        "recommended": 0, "models": [{"name": "Gemma 3 4B IT", "repo": "r/g", "file": "g.gguf", "params": "4B", "size_mib": 2374,
        "license": "gemma", "fits": True, "placement": "gpu", "need_mib": 3400}]}),
    ("GET", "/v1/config"): (200, {"port": 8080, "lan_exposed": False, "autostart": True, "models_dir": "/m", "hf_token": "", "overrides": {}}),
    ("GET", "/v1/apikeys"): (200, {"keys": []}),
    ("GET", "/v1/server/logs"): (200, {"lines": 1, "log": "server log line\n"}),
    ("GET", "/v1/system/ssh"): (200, {"enabled": False, "installed": True, "active": False, "password_auth": "no", "port": 22}),
    ("GET", "/v1/users"): (200, {"users": [{"name": "alice", "admin": True, "created": "2026-10-12T00:00:00Z"}]}),
    ("POST", "/v1/users"): (201, {"name": "bob", "admin": False, "created": "2026-10-12T01:00:00Z"}),
    ("GET", "/v1/firstboot"): (200, {"done": False, "completed_at": "", "skipped": []}),
    ("PUT", "/v1/firstboot"): (200, {"done": True, "completed_at": "2026-10-12T00:00:00Z", "skipped": []}),
}


def pump(seconds, until=None):
    ctx = GLib.MainContext.default()
    end = time.time() + seconds
    while time.time() < end:
        while ctx.pending():
            ctx.iteration(False)
        if until is not None and until():
            return True
        time.sleep(0.02)
    return until() if until else True


@unittest.skipUnless(HAVE_GTK, "GTK4 and libadwaita with a display are required")
class SmokeTests(unittest.TestCase):
    def setUp(self):
        from lightlix_cc import api, i18n
        from lightlix_cc.context import Context

        i18n.setup(localedir="/nonexistent")
        self.daemon = FakeDaemon(ROUTES).__enter__()
        self.addCleanup(self.daemon.__exit__, None, None, None)
        self.ctx = Context(api.Client(self.daemon.path))
        self.app = Adw.Application(application_id="io.github.nxodh.lightlixos.Test")
        self.app.register(None)

    def test_main_window_fills_pages(self):
        from lightlix_cc.window import MainWindow

        win = MainWindow(self.app, self.ctx)
        win.present()
        self.ctx.refresh()
        dash = win.stack.get_child_by_name("dashboard")
        ok = pump(5, until=lambda: bool(dash._gpu_rows) and "RTX 3060" in dash._gpu_rows[0].get_title())
        self.assertTrue(ok, "dashboard did not show the GPU")
        self.assertEqual(dash.driver_row.get_subtitle(), "595.91.07")
        self.assertFalse(win.banner.get_revealed())

        models = win.stack.get_child_by_name("models")
        self.assertTrue(pump(5, until=lambda: any("a.gguf" in r.get_title() for r in models._installed_rows)))
        self.assertTrue(pump(5, until=lambda: len(models._rec_rows) >= 2))

        server = win.stack.get_child_by_name("server")
        self.assertTrue(pump(5, until=lambda: server._models == ["a.gguf"]))
        self.assertIn("server log line", server.log_view.get_buffer().get_text(
            server.log_view.get_buffer().get_start_iter(), server.log_view.get_buffer().get_end_iter(), False))
        win.destroy()

    def test_system_page_lists_accounts(self):
        from lightlix_cc.window import MainWindow

        win = MainWindow(self.app, self.ctx)
        win.present()
        system = win.stack.get_child_by_name("system")
        self.assertTrue(pump(5, until=lambda: any("alice" in r.get_title() for r in system._user_rows)))
        win.destroy()

    def test_create_account_dialog_validates_and_posts(self):
        from lightlix_cc.accounts import CreateAccountDialog
        from lightlix_cc.window import MainWindow

        win = MainWindow(self.app, self.ctx)
        created = []
        dialog = CreateAccountDialog(self.ctx, on_created=created.append)
        self.assertFalse(dialog.create_btn.get_sensitive(), "an empty form must not be submittable")
        dialog.name_row.set_text("bob")
        dialog.pw_row.set_text("short")
        dialog.pw2_row.set_text("short")
        self.assertFalse(dialog.create_btn.get_sensitive())
        self.assertIn("8 characters", dialog.message.get_text())
        dialog.pw_row.set_text("correct-horse-9")
        dialog.pw2_row.set_text("different-one-1")
        self.assertIn("do not match", dialog.message.get_text())
        dialog.pw2_row.set_text("correct-horse-9")
        self.assertTrue(dialog.create_btn.get_sensitive())
        self.assertEqual(dialog.message.get_text(), "")
        dialog._submit()
        self.assertTrue(pump(5, until=lambda: bool(created)))
        posts = [c for c in self.daemon.calls if c[0] == "POST" and c[1] == "/v1/users"]
        self.assertEqual(posts[0][2], {"name": "bob", "password": "correct-horse-9", "admin": True})
        win.destroy()

    def test_daemon_down_shows_banner(self):
        from lightlix_cc import api
        from lightlix_cc.context import Context
        from lightlix_cc.window import MainWindow

        ctx = Context(api.Client("/nonexistent/lightlixd.sock"))
        win = MainWindow(self.app, ctx)
        win.present()
        ctx.refresh()
        self.assertTrue(pump(5, until=lambda: win.banner.get_revealed()))
        self.assertIn("daemon", win.banner.get_title().lower())
        win.destroy()

    def test_wizard_walks_all_steps_and_completes(self):
        from lightlix_cc import logic
        from lightlix_cc.window import MainWindow
        from lightlix_cc.wizard import WizardWindow

        win = MainWindow(self.app, self.ctx)
        done = []
        wiz = WizardWindow(self.ctx, parent=win, on_finished=lambda: done.append(True))
        wiz.present()
        self.assertTrue(pump(5, until=lambda: wiz._data.get("status") is not None))
        for _i in range(len(logic.STEPS) - 1):
            wiz.next()
            pump(0.1)
        self.assertEqual(wiz._index, len(logic.STEPS) - 1)
        wiz.next()
        self.assertTrue(pump(5, until=lambda: bool(done)))
        puts = [c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/firstboot"]
        self.assertEqual(len(puts), 1)
        self.assertEqual(puts[0][2]["done"], True)
        win.destroy()


if __name__ == "__main__":
    unittest.main()
