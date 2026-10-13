import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from diparnux_cc import logic  # noqa: E402


def status(state="ready", **gpu_extra):
    fs = {
        "state": state,
        "present": state != "no_gpu",
        "usable_count": 1 if state == "ready" else 0,
        "gpus": [{"index": 0, "name": "RTX 3060 Laptop", "compute_cap": "8.6", "supported": True, "reason": "ok"}],
        "secure_boot": "disabled",
        "driver_version": "595.91.07",
    }
    gpu = {
        "status": fs,
        "open_module": "yes",
        "secure_boot": "disabled",
        "driver_version": "595.91.07",
        "nvml": {
            "available": True,
            "devices": [
                {
                    "index": 0,
                    "name": "RTX 3060 Laptop",
                    "arch": "ampere",
                    "compute_cap": "8.6",
                    "memory_total_mib": 6144,
                    "memory_used_mib": 512,
                    "temperature_c": 51,
                    "power_mw": 18000,
                    "util_gpu_percent": 3,
                }
            ],
        },
    }
    gpu.update(gpu_extra)
    return {"mode": "server", "gpu": gpu, "memory": {"total_mib": 16000, "available_mib": 12000}, "server": {}}


class FormatTests(unittest.TestCase):
    def test_fmt_bytes(self):
        self.assertEqual(logic.fmt_bytes(0), "0 B")
        self.assertEqual(logic.fmt_bytes(1536), "1.5 KiB")
        self.assertEqual(logic.fmt_bytes(1185376), "1.1 MiB")
        self.assertEqual(logic.fmt_bytes(None), "-")
        self.assertEqual(logic.fmt_bytes(-1), "-")

    def test_fmt_mib(self):
        self.assertEqual(logic.fmt_mib(512), "512 MiB")
        self.assertEqual(logic.fmt_mib(6144), "6.0 GiB")
        self.assertEqual(logic.fmt_mib(-1), "-")


class DashboardTests(unittest.TestCase):
    def test_ready_has_no_banner(self):
        self.assertIsNone(logic.gpu_banner(status("ready")))

    def test_states_have_banners(self):
        for state in ("no_gpu", "module_unloaded", "secure_boot", "proprietary_module", "version_mismatch",
                      "driver_missing", "query_failed", "unsupported_gpu", "config_missing", "internal_error"):
            banner = logic.gpu_banner(status(state))
            self.assertIsNotNone(banner, state)
            level, title, text = banner
            self.assertIn(level, (logic.INFO, logic.WARN, logic.ERROR))
            self.assertTrue(title and text, state)

    def test_missing_status_file_banner_mentions_reason(self):
        st = {"gpu": {"status": None, "status_error": "GPU check has not run"}}
        level, _title, text = logic.gpu_banner(st)
        self.assertEqual(level, logic.INFO)
        self.assertIn("has not run", text)

    def test_device_rows_prefer_nvml(self):
        rows = logic.device_rows(status())
        self.assertEqual(len(rows), 1)
        self.assertEqual(rows[0]["arch"], "Ampere")
        self.assertEqual(rows[0]["power_w"], 18.0)
        sub = logic.device_subtitle(rows[0])
        self.assertIn("CC 8.6", sub)
        self.assertIn("512 MiB / 6.0 GiB", sub)
        self.assertIn("51", sub)

    def test_device_rows_fall_back_to_status_file(self):
        st = status("module_unloaded")
        st["gpu"]["nvml"] = {"available": False, "devices": []}
        rows = logic.device_rows(st)
        self.assertEqual(rows[0]["name"], "RTX 3060 Laptop")
        self.assertEqual(rows[0]["vram_total"], -1)
        self.assertNotIn("MiB", logic.device_subtitle(rows[0]))

    def test_memory_summary(self):
        self.assertEqual(logic.memory_summary(status()), {"total": 16000, "used": 4000, "available": 12000})
        self.assertIsNone(logic.memory_summary({"memory": None}))

    def test_server_advice_only_in_desktop_mode(self):
        st = status()
        st["server"] = {"plan": {"advice": ["switch to server mode"]}}
        self.assertEqual(logic.server_advice(st), [])
        st["mode"] = "desktop"
        self.assertEqual(logic.server_advice(st), ["switch to server mode"])


class JobTests(unittest.TestCase):
    def test_progress(self):
        self.assertEqual(logic.progress_fraction({"bytes_done": 50, "bytes_total": 200}), 0.25)
        self.assertEqual(logic.progress_fraction({"bytes_done": 5, "bytes_total": 0}), 0.0)
        self.assertEqual(logic.progress_fraction({"bytes_done": 300, "bytes_total": 200}), 1.0)

    def test_final_states(self):
        for state in ("done", "failed", "canceled"):
            self.assertTrue(logic.job_is_final({"state": state}))
        for state in ("queued", "running"):
            self.assertFalse(logic.job_is_final({"state": state}))


class WizardTests(unittest.TestCase):
    def test_gpu_step(self):
        self.assertEqual(logic.evaluate_gpu(status()).level, logic.OK)
        self.assertEqual(logic.evaluate_gpu(status("no_gpu")).level, logic.INFO)
        self.assertEqual(logic.evaluate_gpu(status("module_unloaded")).level, logic.WARN)
        self.assertEqual(logic.evaluate_gpu({"gpu": {"status": None}}).level, logic.INFO)

    def test_driver_step(self):
        ok = logic.evaluate_driver(status())
        self.assertEqual(ok.level, logic.OK)
        self.assertIn("595.91.07", ok.detail)
        self.assertEqual(logic.evaluate_driver(status("no_gpu")).level, logic.INFO)
        self.assertEqual(logic.evaluate_driver(status("version_mismatch")).level, logic.ERROR)

    def test_secure_boot_step(self):
        self.assertEqual(logic.evaluate_secure_boot(status()).level, logic.OK)
        blocked = status("secure_boot", secure_boot="enabled")
        self.assertEqual(logic.evaluate_secure_boot(blocked).level, logic.WARN)
        loaded = status("ready", secure_boot="enabled")
        self.assertEqual(logic.evaluate_secure_boot(loaded).level, logic.INFO)
        unknown = status("ready", secure_boot="unknown")
        unknown["gpu"]["status"]["secure_boot"] = "unknown"
        self.assertEqual(logic.evaluate_secure_boot(unknown).level, logic.INFO)

    def test_model_step(self):
        rec = {"recommended": 1, "models": [{"name": "A"}, {"name": "B", "params": "1.7B", "size_mib": 1223}]}
        r = logic.evaluate_model(rec, [])
        self.assertEqual(r.level, logic.INFO)
        self.assertIn("B", r.title)
        self.assertEqual(logic.evaluate_model(rec, [{"id": "x.gguf"}]).level, logic.OK)
        self.assertEqual(logic.evaluate_model({"recommended": -1, "models": []}, []).level, logic.WARN)

    def test_api_step(self):
        self.assertEqual(logic.evaluate_api({"lan_exposed": False}, 0).level, logic.OK)
        self.assertEqual(logic.evaluate_api({"lan_exposed": True}, 0).level, logic.ERROR)
        self.assertEqual(logic.evaluate_api({"lan_exposed": True}, 1).level, logic.INFO)

    def test_account_step(self):
        self.assertEqual(logic.evaluate_account([]).level, logic.INFO)
        r = logic.evaluate_account([{"name": "alice"}])
        self.assertEqual(r.level, logic.OK)
        self.assertIn("alice", r.detail)
        self.assertIn("account", logic.STEPS)

    def test_username_rules(self):
        self.assertIsNone(logic.check_username("alice"))
        self.assertIsNone(logic.check_username("a_b-9"))
        for bad in ("", "Alice", "1abc", "a b", "root", "user", "diparnuxd", "x" * 33, "a.b"):
            self.assertIsNotNone(logic.check_username(bad), bad)

    def test_password_rules(self):
        self.assertIsNone(logic.check_password("alice", "correct-horse-9", "correct-horse-9"))
        self.assertIsNotNone(logic.check_password("alice", "short", "short"))
        self.assertIsNotNone(logic.check_password("alice", "correct-horse-9", "other-horse-9"))
        self.assertIsNotNone(logic.check_password("alicealice", "alicealice", "alicealice"))
        self.assertIsNotNone(logic.check_password("alice", "han\uae00password", "han\uae00password"))
        self.assertIsNotNone(logic.check_password("alice", "x" * 129, "x" * 129))

    def test_skipped_steps_collects_problems_only(self):
        results = [
            logic.StepResult("gpu", logic.OK, "a"),
            logic.StepResult("driver", logic.ERROR, "b"),
            logic.StepResult("secure_boot", logic.WARN, "c"),
            logic.StepResult("model", logic.INFO, "d"),
        ]
        self.assertEqual(logic.skipped_steps(results), ["driver", "secure_boot"])
        for step in logic.skipped_steps(results):
            self.assertIn(step, logic.STEPS)




class DescribeUpdateTests(unittest.TestCase):
    def info(self, **kw):
        base = {"available": True, "live": False, "running": False, "state": {"phase": "idle"}, "rollback_available": False}
        base.update(kw)
        return base

    def test_live_session_blocks_everything(self):
        d = logic.describe_update(self.info(live=True, available=False))
        self.assertFalse(d["can_check"] or d["can_install"] or d["can_rollback"])

    def test_missing_helper(self):
        d = logic.describe_update(self.info(available=False))
        self.assertEqual(d["level"], logic.WARN)
        self.assertFalse(d["can_check"])

    def test_running_is_busy_and_names_the_step(self):
        d = logic.describe_update(self.info(running=True, state={"phase": "running", "step": "kernel"}))
        self.assertTrue(d["busy"])
        self.assertFalse(d["can_check"] or d["can_install"])
        self.assertEqual(d["detail"], "Installing and building the new kernel")

    def test_pending_boot_asks_for_restart_and_blocks_install(self):
        d = logic.describe_update(self.info(state={"phase": "pending_boot", "new_kernel": "6.12.112+deb13-amd64"}, rollback_available=True))
        self.assertTrue(d["reboot"])
        self.assertTrue(d["can_check"])
        self.assertFalse(d["can_install"] or d["can_rollback"])
        self.assertIn("6.12.112+deb13-amd64", d["detail"])

    def test_failed_shows_the_message(self):
        d = logic.describe_update(self.info(state={"phase": "failed", "code": "dkms_failed", "message": "module not found"}))
        self.assertEqual(d["level"], logic.ERROR)
        self.assertEqual(d["detail"], "module not found")

    def test_up_to_date_and_available(self):
        d = logic.describe_update(self.info(last_check={"time": "t", "plan": {"up_to_date": True, "changes": []}}))
        self.assertEqual(d["level"], logic.OK)
        d = logic.describe_update(
            self.info(
                last_check={
                    "time": "t",
                    "plan": {
                        "up_to_date": False,
                        "changes": [{"name": "a"}, {"name": "b"}],
                        "kernels": ["6.12.112+deb13-amd64"],
                        "driver_from": "595.91.07-1",
                        "driver_to": "595.100.01-1",
                    },
                },
                rollback_available=True,
            )
        )
        self.assertIn("2", d["title"])
        self.assertIn("6.12.112+deb13-amd64", d["detail"])
        self.assertIn("595.100.01-1", d["detail"])
        self.assertTrue(d["can_install"] and d["can_rollback"])

    def test_check_error_is_shown(self):
        d = logic.describe_update(self.info(last_check={"time": "t", "error": {"code": "network", "message": "no route"}}))
        self.assertEqual(d["level"], logic.WARN)
        self.assertEqual(d["detail"], "no route")

    def test_garbage_input(self):
        self.assertFalse(logic.describe_update(None)["can_check"])


class NetworkLogicTests(unittest.TestCase):
    def test_online_summary_names_the_connection(self):
        d = logic.describe_network({
            "online": True,
            "interfaces": [{"name": "wlan0", "kind": "wifi", "state": "up", "addresses": ["192.168.0.23/24"], "gateway": "192.168.0.1"}],
            "wifi": {"available": True, "powered": True, "connected_ssid": "HomeWiFi"},
        })
        self.assertTrue(d["online"])
        self.assertIn("HomeWiFi", d["summary"])
        self.assertIn("192.168.0.23/24", d["summary"])
        self.assertEqual(d["wifi_note"], "")

    def test_offline_and_wifi_notes(self):
        d = logic.describe_network({"online": False, "interfaces": [], "wifi": {"available": False, "reason": "no_wifi_device"}})
        self.assertFalse(d["online"])
        self.assertFalse(d["wifi_available"])
        self.assertIn("adapter", d["wifi_note"])
        d = logic.describe_network({"online": False, "interfaces": [], "wifi": {"available": True, "powered": False}})
        self.assertIn("off", d["wifi_note"])
        self.assertFalse(logic.describe_network(None)["online"])

    def test_wifi_password_rules(self):
        self.assertIsNotNone(logic.check_wifi_password(""))
        self.assertIsNotNone(logic.check_wifi_password("short"))
        self.assertIsNotNone(logic.check_wifi_password("a" * 64 + "g"))
        self.assertIsNotNone(logic.check_wifi_password("한글비밀번호입니다"))
        self.assertIsNone(logic.check_wifi_password("correct horse"))
        self.assertIsNone(logic.check_wifi_password("f" * 64))

    def test_signal_labels(self):
        self.assertEqual(logic.signal_label(90), "Strong")
        self.assertEqual(logic.signal_label(50), "Good")
        self.assertEqual(logic.signal_label(10), "Weak")


if __name__ == "__main__":
    unittest.main()


class PowerLogicTests(unittest.TestCase):
    def test_fmt_uptime(self):
        self.assertEqual(logic.fmt_uptime(90061), "1d 1h 1m")
        self.assertEqual(logic.fmt_uptime(3700), "1h 1m")
        self.assertEqual(logic.fmt_uptime(120), "2m")
        self.assertEqual(logic.fmt_uptime(0), "-")
        self.assertEqual(logic.fmt_uptime(None), "-")

    def test_power_failed_checks(self):
        state = {"checks": [{"name": "HandleLidSwitch", "got": "suspend", "ok": False},
                            {"name": "CanSuspend", "got": "yes", "ok": False},
                            {"name": "IdleAction", "got": "ignore", "ok": True}]}
        self.assertEqual(logic.power_failed_checks(state), ["HandleLidSwitch=suspend", "CanSuspend=yes"])
        self.assertEqual(logic.power_failed_checks({}), [])
