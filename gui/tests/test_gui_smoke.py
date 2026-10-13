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
    ("GET", "/v1/network"): (200, {
        "interfaces": [{"name": "wlan0", "kind": "wifi", "state": "up", "addresses": ["192.168.0.23/24"], "gateway": "192.168.0.1"}],
        "wifi": {"available": True, "device": "wlan0", "powered": True, "state": "connected", "scanning": False, "connected_ssid": "HomeWiFi"},
        "online": True}),
    ("GET", "/v1/network/wifi/networks"): (200, {"networks": [
        {"ssid": "HomeWiFi", "security": "psk", "signal": 80, "signal_dbm": -42, "connected": True, "known": True},
        {"ssid": "CafeFree", "security": "open", "signal": 50, "signal_dbm": -61, "connected": False, "known": False},
        {"ssid": "Office 5G", "security": "psk", "signal": 30, "signal_dbm": -73, "connected": False, "known": False}]}),
    ("POST", "/v1/network/wifi/scan"): (202, {"scanning": True}),
    ("GET", "/v1/server/logs"): (200, {"lines": 1, "log": "server log line\n"}),
    ("GET", "/v1/system/power"): (200, {"always_on": True, "enabled": True, "checks": [], "lid": "closed", "has_battery": True,
        "on_battery": False, "battery_percent": 88, "uptime_seconds": 90061, "notes": []}),
    ("PUT", "/v1/system/power"): (200, {"always_on": False, "enabled": False, "checks": [], "lid": "open", "has_battery": False,
        "on_battery": False, "battery_percent": -1, "uptime_seconds": 5, "notes": ["Always-on is turned off."]}),
    ("GET", "/v1/connection"): (200, {
        "server_state": "running", "model_id": "a.gguf", "models": ["a.gguf"], "public_port": 8080, "lan_exposed": True,
        "gateway": True, "key_required": True, "key_count": 1, "hostname": "diparnux", "local_url": "http://127.0.0.1:8080/v1",
        "addresses": [{"interface": "enp3s0", "address": "192.168.0.23", "family": "ipv4", "url": "http://192.168.0.23:8080/v1"}],
        "mdns_url": "http://diparnux.local:8080/v1", "warnings": ["No API key exists."],
        "examples": {"settings": "Base URL: http://192.168.0.23:8080/v1\nAPI key: <your API key>", "curl": "curl http://192.168.0.23:8080/v1/chat/completions"}}),
    ("GET", "/v1/gateway"): (200, {"enabled": True, "public_port": 8080, "lan_exposed": True, "state": {"listening": True, "addr": "0.0.0.0:8080", "backend_port": 18080},
        "stats": {"in_flight": 0, "requests": 12, "errors": 1, "unauthorized": 2, "since": "2026-10-13T00:00:00Z", "recent": [],
        "keys": [{"id": "k1", "label": "orca", "prefix": "dpx_ab12", "requests": 1500, "errors": 1, "prompt_tokens": 420, "completion_tokens": 900, "last_used": "2026-10-13T01:02:03Z"}]}}),
    ("POST", "/v1/server/chat"): (200, {"choices": [{"message": {"content": "pong"}}]}),
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
        from diparnux_cc import api, i18n
        from diparnux_cc.context import Context

        i18n.setup(localedir="/nonexistent")
        self.daemon = FakeDaemon(ROUTES).__enter__()
        self.addCleanup(self.daemon.__exit__, None, None, None)
        self.ctx = Context(api.Client(self.daemon.path))
        self.app = Adw.Application(application_id="io.github.nxodh.diparnuxos.Test")
        self.app.register(None)

    def test_main_window_fills_pages(self):
        from diparnux_cc.window import MainWindow

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

    def test_connection_page_shows_addresses_examples_and_usage(self):
        from diparnux_cc.connection import ConnectionPage

        page = ConnectionPage(self.ctx)
        self.assertTrue(pump(5, until=lambda: len(page._addr_rows) == 3), "the addresses were not shown")
        self.assertEqual(page._addr_rows[0].get_title(), "http://192.168.0.23:8080/v1")
        self.assertEqual(page._addr_rows[1].get_title(), "http://diparnux.local:8080/v1")
        self.assertEqual(len(page._example_rows), 2)
        self.assertTrue(page.notes_row.get_visible())
        self.assertIn("No API key exists.", page.notes_row.get_subtitle())
        self.assertTrue(pump(5, until=lambda: len(page._usage_rows) == 2), "the usage was not shown")
        sub = page._usage_rows[0].get_subtitle()
        self.assertIn("1.5K", sub)
        self.assertIn("dpx_ab12", sub)
        page.check()
        self.assertTrue(pump(5, until=lambda: any(c[1] == "/v1/server/chat" for c in self.daemon.calls)))

    def test_system_page_lists_accounts(self):
        from diparnux_cc.window import MainWindow

        win = MainWindow(self.app, self.ctx)
        win.present()
        system = win.stack.get_child_by_name("system")
        self.assertTrue(pump(5, until=lambda: any("alice" in r.get_title() for r in system._user_rows)))
        win.destroy()

    def test_system_page_shows_always_on_state_and_toggles(self):
        from diparnux_cc.window import MainWindow

        win = MainWindow(self.app, self.ctx)
        win.present()
        system = win.stack.get_child_by_name("system")
        self.assertTrue(pump(5, until=lambda: system.power_row.get_active()), "always-on state was not shown")
        sub = system.power_info.get_subtitle()
        self.assertIn("1d 1h 1m", sub)
        self.assertIn("88%", sub)
        system.power_row.set_active(False)
        self.assertTrue(pump(5, until=lambda: any(c[0] == "PUT" and c[1] == "/v1/system/power" for c in self.daemon.calls)))
        puts = [c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/system/power"]
        self.assertEqual(puts[0][2], {"always_on": False})
        win.destroy()

    def test_create_account_dialog_validates_and_posts(self):
        from diparnux_cc.accounts import CreateAccountDialog
        from diparnux_cc.window import MainWindow

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

    def test_network_page_lists_wifi_and_asks_for_the_password(self):
        from diparnux_cc.network import NetworkPage, WifiPasswordDialog

        page = NetworkPage(self.ctx)
        page.refresh()
        self.assertTrue(pump(5, until=lambda: len(page._wifi_rows) == 3), "the Wi-Fi list was not shown")
        titles = [r.get_title() for r in page._wifi_rows]
        self.assertEqual(titles, ["HomeWiFi", "CafeFree", "Office 5G"])
        self.assertIn("HomeWiFi", page.summary_row.get_subtitle())
        self.assertTrue(page.power_row.get_active())
        self.assertEqual(len(page._iface_rows), 1)

        # 새 보안 네트워크는 비밀번호를 받는다. 규칙에 맞지 않으면 연결 버튼이 꺼져 있다.
        dialog = WifiPasswordDialog(self.ctx, "Office 5G")
        self.assertFalse(dialog.connect_btn.get_sensitive())
        dialog.pw_row.set_text("short")
        self.assertFalse(dialog.connect_btn.get_sensitive())
        self.assertIn("8 characters", dialog.message.get_text())
        dialog.pw_row.set_text("correct horse battery")
        self.assertTrue(dialog.connect_btn.get_sensitive())
        self.daemon.routes[("POST", "/v1/network/wifi/connect")] = (200, {"connected": True, "ssid": "Office 5G"})
        done = []
        dialog._on_connected = lambda: done.append(True)
        dialog._submit()
        self.assertTrue(pump(5, until=lambda: bool(done)))
        posts = [c for c in self.daemon.calls if c[0] == "POST" and c[1] == "/v1/network/wifi/connect"]
        self.assertEqual(posts[0][2], {"ssid": "Office 5G", "passphrase": "correct horse battery"})

    def test_daemon_down_shows_banner(self):
        from diparnux_cc import api
        from diparnux_cc.context import Context
        from diparnux_cc.window import MainWindow

        ctx = Context(api.Client("/nonexistent/diparnuxd.sock"))
        win = MainWindow(self.app, ctx)
        win.present()
        ctx.refresh()
        self.assertTrue(pump(5, until=lambda: win.banner.get_revealed()))
        self.assertIn("daemon", win.banner.get_title().lower())
        win.destroy()

    def test_wizard_walks_all_steps_and_completes(self):
        from diparnux_cc import logic
        from diparnux_cc.window import MainWindow
        from diparnux_cc.wizard import WizardWindow

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
