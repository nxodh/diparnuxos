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
    ("GET", "/v1/config"): (200, {"port": 8080, "lan_exposed": False, "autostart": True, "models_dir": "/m", "hf_token": "", "local_no_key": True,
        "overrides": {"parallel": 3, "profile": "agent"}}),
    ("PUT", "/v1/config"): (200, {"config": {}, "restart_required": True}),
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
        "mdns_url": "http://diparnux.local:8080/v1", "warnings": ["No API key exists."], "slot_ctx": 8192,
        "examples": {"vedecaps": "{\n  \"model\": \"diparnux/a.gguf\"\n}", "settings": "Base URL: http://192.168.0.23:8080/v1\nAPI key: <your API key>", "curl": "curl http://192.168.0.23:8080/v1/chat/completions"}}),
    ("GET", "/v1/gateway"): (200, {"enabled": True, "public_port": 8080, "lan_exposed": True, "state": {"listening": True, "addr": "0.0.0.0:8080", "backend_port": 18080},
        "stats": {"in_flight": 0, "requests": 12, "errors": 1, "unauthorized": 2, "since": "2026-10-13T00:00:00Z", "recent": [],
        "keys": [{"id": "k1", "label": "orca", "prefix": "dpx_ab12", "requests": 1500, "errors": 1, "prompt_tokens": 420, "completion_tokens": 900, "last_used": "2026-10-13T01:02:03Z"}]}}),
    ("POST", "/v1/server/toolcheck"): (200, {"ok": False, "code": "no_tool_call", "message": "the model answered in text", "finish_reason": "stop", "latency_ms": 5}),
    ("POST", "/v1/server/chat"): (200, {"choices": [{"message": {"content": "pong"}}]}),
    ("GET", "/v1/firewall"): (200, {"policy": "drop", "max_rule": 64,
        "builtin": [{"name": "SSH", "protocol": "tcp", "port": 22, "source": "any", "reason": "the SSH server is on"}],
        "rules": [{"id": "r1", "name": "WebUI", "protocol": "tcp", "port": 3000, "source": "lan", "enabled": True}]}),
    ("POST", "/v1/firewall/rules"): (201, {"id": "r2", "name": "Range", "protocol": "udp", "port": 6000, "port_end": 6010, "source": "any", "enabled": True}),
    ("PUT", "/v1/firewall/rules/r1"): (200, {"id": "r1", "name": "WebUI", "protocol": "tcp", "port": 3000, "source": "lan", "enabled": False}),
    ("DELETE", "/v1/firewall/rules/r1"): (200, {"id": "r1", "deleted": True}),
    ("GET", "/v1/services"): (200, {"services": [
        {"unit": "ssh.service", "title": "SSH server", "installed": True, "active": "active", "sub": "running", "enabled": "enabled", "memory_mib": 4, "actions": ["start", "stop", "restart", "enable", "disable"]},
        {"unit": "diparnuxd.service", "title": "Diparnux daemon", "installed": True, "active": "active", "sub": "running", "enabled": "enabled", "actions": []},
        {"unit": "xrdp.service", "title": "Remote desktop (RDP)", "installed": False, "active": "inactive", "sub": "dead", "enabled": "not-found", "actions": []}]}),
    ("POST", "/v1/services/ssh.service/restart"): (200, {"unit": "ssh.service", "action": "restart", "done": True}),
    ("GET", "/v1/resources"): (200, {"system": {"uptime_seconds": 3700,
        "cpu": {"cores": 8, "used_percent": 12.5, "load1": 0.5, "load5": 0.4, "load15": 0.3},
        "memory": {"total_mib": 16000, "available_mib": 8000, "used_mib": 8000, "swap_total_mib": 0, "swap_used_mib": 0},
        "disks": [{"mount": "/", "fs_type": "ext4", "total_mib": 100000, "used_mib": 40000, "used_percent": 40}],
        "network": [{"name": "enp3s0", "rx_bytes_per_second": 2048.0, "tx_bytes_per_second": 0.0}],
        "temperatures": [{"name": "x86_pkg_temp", "celsius": 52.5}],
        "top_processes": [{"pid": 4242, "name": "llama-server", "rss_mib": 3000}]},
        "gpus": [{"index": 0, "name": "RTX 3060", "memory_total_mib": 6144, "memory_used_mib": 3000, "util_gpu_percent": 5, "temperature_c": 50, "power_mw": 20000}],
        "gpu_available": True}),
    ("GET", "/v1/ssh/advanced"): (200, {"enabled": True, "port": 2222, "password_login_configured": False, "key_count": 1,
        "host_keys": [{"type": "ssh-ed25519", "fingerprint": "SHA256:abc", "comment": "ssh_host_ed25519_key.pub"}]}),
    ("GET", "/v1/ssh/keys"): (200, {"user": "alice", "keys": [{"type": "ssh-ed25519", "fingerprint": "SHA256:key1", "comment": "alice@laptop"}]}),
    ("POST", "/v1/ssh/keys"): (201, {"key": {"type": "ssh-ed25519", "fingerprint": "SHA256:key2"}, "added": True}),
    ("GET", "/v1/ssh/log"): (200, {"events": [{"time": "2026-10-13T12:00:00+0900", "result": "accepted", "user": "alice", "method": "publickey", "from": "192.168.0.5"}]}),
    ("GET", "/v1/remote-desktop"): (200, {"installed": True, "enabled": False, "active": False, "port": 3389, "source": "lan"}),
    ("PUT", "/v1/remote-desktop"): (200, {"installed": True, "enabled": True, "active": True, "port": 3389, "source": "lan"}),
    ("GET", "/v1/vpn"): (200, {"enabled": True, "interface": "wg0", "address": "10.8.0.1/24", "listen_port": 51820, "public_key": "serverpub",
        "peers": [{"name": "phone", "public_key": "peerpub", "allowed_ips": ["10.8.0.2/32"]}]}),
    ("GET", "/v1/tls"): (200, {"enabled": False, "active": False, "port": 8444, "lan_plain": False, "names": [], "plain_port": 8080, "plain_lan_open": False, "listening": False}),
    ("GET", "/v1/webadmin"): (200, {"enabled": False, "running": False, "port": 8443, "source": "lan", "password_set": False, "urls": []}),
    ("GET", "/v1/network/config"): (200, {"hostname": "diparnux", "wake_on_lan": False, "pending": None,
        "interfaces": [{"interface": "enp3s0", "mode": "dhcp", "addresses": [], "managed": False}]}),
    ("PUT", "/v1/network/interfaces/enp3s0"): (200, {"config": {"interface": "enp3s0", "mode": "static"}, "pending": {"interface": "enp3s0", "deadline": "x"}, "confirm_seconds": 60}),
    ("POST", "/v1/network/diagnose"): (200, {"checks": [{"name": "address", "ok": True, "detail": "enp3s0 192.168.0.23/24"}, {"name": "dns", "ok": False, "detail": "huggingface.co could not be resolved"}]}),
    ("GET", "/v1/exposure"): (200, {"level": "warning", "listening": [], "findings": [
        {"level": "warning", "title": "SSH accepts password logins", "detail": "Keys are safer."},
        {"level": "ok", "title": "The model server is reachable only from this computer", "detail": ""}]}),
    ("PUT", "/v1/models/import"): (201, {"model_id": "imported.gguf", "size_bytes": 8, "sha256": "ab" * 32, "default": False}),
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
        self.assertTrue(pump(5, until=lambda: server.parallel_row.get_value() == 3), "the saved slot count was not shown")
        server.parallel_row.set_value(4)
        self.assertTrue(pump(5, until=lambda: any(c[0] == "PUT" and c[1] == "/v1/config" for c in self.daemon.calls)))
        put = [c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/config"][-1]
        self.assertEqual(put[2], {"overrides": {"parallel": 4, "profile": "agent"}})
        # 저장된 용도와 키 면제가 보이고, 바꾸면 그대로 저장한다.
        self.assertEqual(server.profile_row.get_selected(), 1)
        self.assertTrue(server.local_row.get_active())
        server.profile_row.set_selected(0)
        self.assertTrue(pump(5, until=lambda: [c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/config"][-1][2]
                             == {"overrides": {"parallel": 4}}), "the chat profile was not saved")
        server.local_row.set_active(False)
        self.assertTrue(pump(5, until=lambda: [c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/config"][-1][2]
                             == {"local_no_key": False}), "local_no_key was not saved")
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

    def test_vedecaps_page_connects_checks_tools_and_offers_the_agent_profile(self):
        import diparnux_cc.vedecaps as vp

        calls = []
        orig_run, orig_find = vp.run_connect, vp.find_vedecaps
        vp.run_connect = lambda: (calls.append("connect"), (True, "config   /home/u/.config/vedecaps/vedecaps.json"))[1]
        vp.find_vedecaps = lambda: "/usr/local/bin/vedecaps"
        try:
            page = vp.VedecapsPage(self.ctx)
            self.assertTrue(pump(5, until=lambda: "a.gguf" in (page.server_row.get_subtitle() or "")), "the server was not shown")
            self.assertEqual(page.program_row.get_subtitle(), "/usr/local/bin/vedecaps")
            # 슬롯당 컨텍스트가 작으면 에이전트 용도를 권한다. 저장된 용도가 agent 이면 단추를 숨긴다.
            self.assertIn("8192", page.context_row.get_subtitle())
            self.assertIn("16384", page.context_row.get_subtitle())
            self.assertTrue(pump(5, until=lambda: len(page._example_rows) == 1), "the settings were not shown")
            self.assertTrue(pump(5, until=lambda: page.profile_row.get_subtitle() not in (None, "-")))
            self.assertFalse(page.profile_btn.get_visible(), "the stored profile is agent")

            page.check_tools()
            self.assertTrue(pump(5, until=lambda: "answered in text" in (page.tools_row.get_subtitle() or "")))

            page.connect()
            self.assertTrue(pump(5, until=lambda: calls == ["connect"]))
        finally:
            vp.run_connect, vp.find_vedecaps = orig_run, orig_find

    def test_models_page_imports_a_file(self):
        import tempfile

        from diparnux_cc.models import ModelsPage

        page = ModelsPage(self.ctx)
        self.assertTrue(pump(5, until=lambda: len(page._installed_rows) >= 1))
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "imported.gguf")
            with open(path, "wb") as fh:
                fh.write(b"GGUF1234")
            page.start_import(path)
            self.assertTrue(pump(5, until=lambda: any(c[0] == "PUT" and c[1].startswith("/v1/models/import") for c in self.daemon.calls)))
        self.assertTrue(pump(5, until=lambda: not page._job_rows), "the import row was not removed")

    def test_server_page_renders_live_stats(self):
        from diparnux_cc.server import ServerPage

        page = ServerPage(self.ctx)
        page._render_stats({
            "slots": {"available": True, "total": 4, "busy": 1}, "slot_ctx": 2048, "uptime_seconds": 3700,
            "metrics": {"available": True, "generated_tokens_per_second": 31.5, "prompt_tokens_per_second": 700.0},
        })
        self.assertIn("1 of 4 slots busy", page.slots_row.get_subtitle())
        self.assertIn("31.5 tokens/s", page.speed_row.get_subtitle())
        self.assertIn("1h 1m", page.speed_row.get_subtitle())
        page._render_stats({"slots": {"available": False}, "metrics": {"available": False}})
        self.assertEqual(page.slots_row.get_subtitle(), "Not available")

    def test_main_window_has_a_sidebar_with_every_page(self):
        from diparnux_cc.window import MainWindow

        win = MainWindow(self.app, self.ctx)
        win.present()
        names = [n for n, _t, _i in win._entries]
        self.assertEqual(
            names,
            ["dashboard", "models", "server", "connection", "vedecaps", "network", "firewall", "remote", "services", "resources", "system"],
        )
        for name in names:
            self.assertIsNotNone(win.stack.get_child_by_name(name), name)
        win.show_page("firewall")
        self.assertEqual(win.stack.get_visible_child_name(), "firewall")
        self.assertIs(win.sidebar_list.get_selected_row(), win._rows["firewall"])
        win.sidebar_list.select_row(win._rows["services"])
        self.assertTrue(pump(2, until=lambda: win.stack.get_visible_child_name() == "services"))
        win.destroy()

    def test_firewall_page_lists_features_and_rules_and_adds_one(self):
        from diparnux_cc.firewall import FirewallPage

        page = FirewallPage(self.ctx)
        self.assertTrue(pump(5, until=lambda: len(page._rule_rows) == 1 and len(page._builtin_rows) == 1))
        self.assertEqual(page._rule_rows[0].get_title(), "WebUI")
        self.assertIn("tcp 3000", page._rule_rows[0].get_subtitle())
        page.name_row.set_text("Range")
        page.proto_row.set_selected(1)
        page.port_row.set_text("6000-6010")
        page.source_row.set_selected(1)
        page.add_rule()
        self.assertTrue(pump(5, until=lambda: any(c[0] == "POST" and c[1] == "/v1/firewall/rules" for c in self.daemon.calls)))
        post = [c for c in self.daemon.calls if c[0] == "POST" and c[1] == "/v1/firewall/rules"][0]
        self.assertEqual(post[2], {"name": "Range", "protocol": "udp", "port": 6000, "source": "any", "port_end": 6010})

    def test_services_page_offers_only_the_allowed_actions(self):
        from diparnux_cc.services import ServicesPage

        page = ServicesPage(self.ctx)
        page.reload()
        self.assertTrue(pump(5, until=lambda: len(page._rows) == 3))
        titles = [r.get_title() for r in page._rows]
        self.assertEqual(titles, ["SSH server", "Diparnux daemon", "Remote desktop (RDP)"])
        page.act("ssh.service", "restart", "SSH server")
        self.assertTrue(pump(5, until=lambda: any(c[1] == "/v1/services/ssh.service/restart" for c in self.daemon.calls)))

    def test_resources_page_renders_a_measurement(self):
        from diparnux_cc.resources import ResourcesPage

        page = ResourcesPage(self.ctx)
        page.refresh()
        self.assertTrue(pump(5, until=lambda: "12.5%" in page.cpu_row.get_subtitle()))
        self.assertIn("7.8 GiB used of 15.6 GiB", page.mem_row.get_subtitle())
        self.assertTrue(pump(2, until=lambda: len(page._rows[page.gpu_group]) == 1))
        self.assertEqual(page._rows[page.net_group][0].get_title(), "enp3s0")
        self.assertIn("2.0 KiB/s", page._rows[page.net_group][0].get_subtitle())

    def test_remote_page_shows_ssh_vpn_and_asks_before_changing(self):
        from diparnux_cc.remote import RemotePage

        page = RemotePage(self.ctx)
        page.reload()
        self.assertTrue(pump(5, until=lambda: page.ssh_port_row.get_value() == 2222))
        self.assertFalse(page.ssh_pw_row.get_active())
        self.assertTrue(pump(5, until=lambda: len(page._host_rows) == 1 and len(page._peer_rows) == 1))
        self.assertTrue(pump(5, until=lambda: len(page._key_rows) == 1))
        self.assertTrue(page.vpn_row.get_active())
        page.key_entry.set_text("ssh-ed25519 AAAA test")
        page.add_key()
        self.assertTrue(pump(5, until=lambda: any(c[0] == "POST" and c[1] == "/v1/ssh/keys" for c in self.daemon.calls)))
        post = [c for c in self.daemon.calls if c[0] == "POST" and c[1] == "/v1/ssh/keys"][0]
        self.assertEqual(post[2]["key"], "ssh-ed25519 AAAA test")

    def test_network_page_wired_settings_and_diagnose(self):
        from diparnux_cc.network import NetworkPage

        page = NetworkPage(self.ctx)
        page.wired.reload()
        self.assertTrue(pump(5, until=lambda: len(page.wired._wired_rows) == 1))
        self.assertEqual(page.wired.host_row.get_text(), "diparnux")
        self.assertFalse(page.wired.pending_group.get_visible())
        page.wired.diagnose()
        self.assertTrue(pump(5, until=lambda: len(page.wired._diag_rows) == 2))
        self.assertIn("Problem", page.wired._diag_rows[1].get_subtitle())

    def test_connection_page_shows_the_safety_check(self):
        from diparnux_cc.connection import ConnectionPage

        page = ConnectionPage(self.ctx)
        self.assertTrue(pump(5, until=lambda: len(page._safety_rows) == 2))
        self.assertEqual(page._safety_rows[0].get_title(), "SSH accepts password logins")

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

    def test_wizard_model_step_lets_the_user_pick_the_use(self):
        from diparnux_cc import logic
        from diparnux_cc.window import MainWindow
        from diparnux_cc.wizard import WizardWindow

        win = MainWindow(self.app, self.ctx)
        wiz = WizardWindow(self.ctx, parent=win)
        wiz.present()
        self.assertTrue(pump(5, until=lambda: wiz._data.get("status") is not None))
        wiz._index = logic.STEPS.index("model")
        wiz._render()
        self.assertTrue(hasattr(wiz, "_use"), "the model step has no use selector")
        # 시험 설정의 저장된 용도는 agent 다. 선택기도 그것을 보여 준다.
        self.assertEqual(wiz._use.get_selected(), 1)
        before = len([c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/config"])
        wiz._use.set_selected(0)
        self.assertTrue(pump(5, until=lambda: len([c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/config"]) > before))
        put = [c for c in self.daemon.calls if c[0] == "PUT" and c[1] == "/v1/config"][-1]
        self.assertEqual(put[2], {"overrides": {"parallel": 3}})
        win.destroy()


if __name__ == "__main__":
    unittest.main()
