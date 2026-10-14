import os
import sys
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from opereta_cc import logic  # noqa: E402


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


class ServerFailureTests(unittest.TestCase):
    def test_last_error_gets_a_hint(self):
        notes = logic.server_failure_notes({"last_error": {"code": "gpu_offload_failed", "message": "offloaded 0/33 layers\nmore"}})
        self.assertEqual(notes[0], "gpu_offload_failed: offloaded 0/33 layers more")
        self.assertIn("simultaneous requests", notes[1])

    def test_unknown_code_has_no_hint_and_long_messages_are_cut(self):
        notes = logic.server_failure_notes({"last_error": {"code": "weird", "message": "x" * 900}})
        self.assertEqual(len(notes), 1)
        self.assertTrue(notes[0].endswith("…"))
        self.assertLess(len(notes[0]), 420)

    def test_restart_progress_and_giving_up(self):
        self.assertIn("attempt 2 of 5", logic.server_failure_notes({"restart": {"attempt": 2, "max": 5}})[0])
        self.assertIn("5 automatic restarts failed", logic.server_failure_notes({"restart": {"attempt": 5, "max": 5, "gave_up": True}})[0])
        self.assertEqual(logic.server_failure_notes(None), [])


class RemoteHelperTests(unittest.TestCase):
    def test_fmt_rate(self):
        self.assertEqual(logic.fmt_rate(0), "0 B/s")
        self.assertEqual(logic.fmt_rate(2048), "2.0 KiB/s")
        self.assertEqual(logic.fmt_rate(5 * 1024 * 1024), "5.0 MiB/s")
        self.assertEqual(logic.fmt_rate(None), "-")

    def test_ports_and_sources(self):
        self.assertEqual(logic.port_text(80), "80")
        self.assertEqual(logic.port_text(6000, 6010), "6000-6010")
        self.assertEqual(logic.port_text(80, 80), "80")
        self.assertEqual(logic.source_label("lan"), "the local network")
        self.assertEqual(logic.source_label("any"), "any address")
        self.assertEqual(logic.source_label("10.0.0.0/8"), "10.0.0.0/8")
        self.assertEqual(logic.rule_subtitle({"protocol": "udp", "port": 5, "port_end": 9, "source": "lan"}), "udp 5-9 · the local network")

    def test_split_list_and_service_state(self):
        self.assertEqual(logic.split_list("1.1.1.1, 9.9.9.9  8.8.8.8"), ["1.1.1.1", "9.9.9.9", "8.8.8.8"])
        self.assertEqual(logic.split_list(""), [])
        self.assertEqual(logic.service_state_text({"active": "active", "sub": "running"}), "active (running)")
        self.assertEqual(logic.service_state_text({"active": "inactive", "sub": "inactive"}), "inactive")
        self.assertEqual(logic.service_state_text({"installed": False}), "not installed")
        self.assertEqual(logic.exposure_level_icon("warning"), "dialog-warning-symbolic")


class UsageTests(unittest.TestCase):
    def test_fmt_count(self):
        self.assertEqual(logic.fmt_count(0), "0")
        self.assertEqual(logic.fmt_count(None), "0")
        self.assertEqual(logic.fmt_count(999), "999")
        self.assertEqual(logic.fmt_count(1500), "1.5K")
        self.assertEqual(logic.fmt_count(2400000), "2.4M")

    def test_key_usage_text(self):
        text = logic.key_usage_text({"prefix": "opr_ab12", "requests": 3, "errors": 0, "prompt_tokens": 10, "completion_tokens": 5,
                                     "last_used": "2026-10-13T01:02:03Z"})
        self.assertEqual(text, "opr_ab12… · 3 requests · 0 errors · 15 tokens · last used 2026-10-13 01:02:03")
        unused = logic.key_usage_text({"requests": 0, "last_used": "0001-01-01T00:00:00Z"})
        self.assertIn("never used", unused)


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
        for bad in ("", "Alice", "1abc", "a b", "root", "user", "operetad", "x" * 33, "a.b"):
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

    def test_new_opereta_version_is_the_headline(self):
        d = logic.describe_update(
            self.info(
                os_version="0.6.0",
                channel={"enabled": True, "url": "https://example.invalid/latest/download"},
                last_check={"time": "t", "plan": {"up_to_date": False, "changes": [{"name": "opereta-base"}], "os_from": "0.6.0", "os_to": "0.6.1"}},
            )
        )
        self.assertIn("0.6.1", d["title"])
        self.assertIn("0.6.0", d["detail"])
        self.assertTrue(d["can_install"])

    def test_up_to_date_with_channel_off_is_a_warning(self):
        d = logic.describe_update(
            self.info(
                os_version="0.6.0",
                channel={"enabled": False, "reason": "disabled"},
                last_check={"time": "t", "plan": {"up_to_date": True, "changes": []}},
            )
        )
        self.assertEqual(d["level"], logic.WARN)
        self.assertIn("channel", d["detail"])

    def test_channel_unreachable_is_a_warning(self):
        d = logic.describe_update(
            self.info(
                channel={"enabled": True},
                last_check={"time": "t", "plan": {"up_to_date": True, "changes": []}, "channel_error": "404"},
            )
        )
        self.assertEqual(d["level"], logic.WARN)
        self.assertIn("could not be reached", d["detail"])

    def test_up_to_date_shows_the_installed_version(self):
        d = logic.describe_update(
            self.info(
                os_version="0.6.0",
                channel={"enabled": True},
                last_check={"time": "t", "plan": {"up_to_date": True, "changes": []}},
            )
        )
        self.assertEqual(d["level"], logic.OK)
        self.assertIn("0.6.0", d["detail"])

    def test_check_error_is_shown(self):
        d = logic.describe_update(self.info(last_check={"time": "t", "error": {"code": "network", "message": "no route"}}))
        self.assertEqual(d["level"], logic.WARN)
        self.assertEqual(d["detail"], "no route")

    def test_garbage_input(self):
        self.assertFalse(logic.describe_update(None)["can_check"])


class CudaRuntimeLogicTests(unittest.TestCase):
    def test_missing_runtime_can_be_installed_even_in_a_live_session(self):
        d = logic.describe_cuda_runtime({"live": True, "available": False, "cuda_runtime": {"installed": False, "reason": "not_installed"}})
        self.assertFalse(d["installed"])
        self.assertTrue(d["can_install"])

    def test_installed_runtime_hides_the_install_action(self):
        d = logic.describe_cuda_runtime({"cuda_runtime": {"installed": True}})
        self.assertTrue(d["installed"])
        self.assertFalse(d["can_install"])

    def test_install_is_blocked_while_an_update_runs(self):
        d = logic.describe_cuda_runtime({"running": True, "cuda_runtime": {"installed": False, "reason": "not_installed"}})
        self.assertFalse(d["can_install"])

    def test_image_without_settings_cannot_install(self):
        d = logic.describe_cuda_runtime({"cuda_runtime": {"installed": False, "reason": "no_settings"}})
        self.assertFalse(d["can_install"])
        self.assertFalse(logic.describe_cuda_runtime(None)["can_install"])


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


class TlsLogicTests(unittest.TestCase):
    def test_ca_subtitle(self):
        self.assertEqual(logic.tls_ca_subtitle({}), "")
        self.assertEqual(logic.tls_ca_subtitle(None), "")
        self.assertEqual(logic.tls_ca_subtitle({"ca_fingerprint": "AA:BB"}), "AA:BB")
        self.assertEqual(
            logic.tls_ca_subtitle({"ca_fingerprint": "AA:BB", "ca_not_after": "2036-10-14T00:00:00Z"}),
            "AA:BB · 2036-10-14",
        )

    def test_listening_text(self):
        self.assertEqual(logic.tls_listening_text({"enabled": False}), "off")
        self.assertEqual(logic.tls_listening_text({"enabled": True, "listening": True, "port": 8444}), "listening 8444")
        self.assertEqual(logic.tls_listening_text({"enabled": True}), "waiting")
        self.assertEqual(logic.tls_listening_text({"enabled": True, "listener_error": "cannot listen"}), "error")


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
