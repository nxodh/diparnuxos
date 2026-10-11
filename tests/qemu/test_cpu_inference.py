#!/usr/bin/env python3
"""ISO 없이 제어된 프로세스와 직렬 fixture 로 오판 및 정리 경로를 확인한다."""

import json
import os
from pathlib import Path
import runpy
import signal
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parent
STATUS_POLL_COMMAND = runpy.run_path(str(ROOT / "cpu-inference.py"))["status_poll_command"]


class StatusPollTests(unittest.TestCase):
    def run_poll(self, script):
        with tempfile.TemporaryDirectory(prefix="lightlix-status-poll-") as work:
            work = Path(work)
            binary_dir = work / "bin"
            binary_dir.mkdir()
            fake = binary_dir / "lightlix"
            fake.write_text(script)
            fake.chmod(0o755)
            calls = work / "calls"
            environment = os.environ.copy()
            environment.update({"PATH": str(binary_dir) + os.pathsep + environment["PATH"],
                                "POLL_CALLS": str(calls)})
            result = subprocess.run(["sh", "-c", STATUS_POLL_COMMAND()], env=environment,
                                    capture_output=True, text=True)
            return result, int(calls.read_text())

    def test_retries_temporary_daemon_unavailability(self):
        result, calls = self.run_poll("""#!/bin/sh
count=0
if [ -f "$POLL_CALLS" ]; then count=$(cat "$POLL_CALLS"); fi
count=$((count + 1))
printf '%s' "$count" > "$POLL_CALLS"
if [ "$count" -eq 1 ]; then
    echo "lightlix: lightlixd is not running (systemctl status lightlixd)" >&2
    exit 3
fi
printf '%s\\n' '{"server":{"state":"running_cpu_fallback"}}'
""")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(result.stdout, '{"server":{"state":"running_cpu_fallback"}}\n')
        self.assertEqual(calls, 2)

    def test_propagates_non_transport_status_error_without_retry(self):
        result, calls = self.run_poll("""#!/bin/sh
printf '1' > "$POLL_CALLS"
echo "lightlix: permission denied: add your user to the 'lightlix' group" >&2
exit 3
""")
        self.assertEqual(result.returncode, 3)
        self.assertIn("permission denied", result.stderr)
        self.assertEqual(calls, 1)


class DriverTests(unittest.TestCase):
    def assert_unprivileged_reader_can_read(self, files):
        for path in files:
            self.assertTrue(path.stat().st_mode & 0o004, f"other-read bit is missing: {path}")
        if os.geteuid() != 0:
            for path in files:
                path.read_bytes()
            return

        def drop_privileges():
            os.setgroups([])
            os.setgid(65534)
            os.setuid(65534)

        reader = subprocess.run(
            [sys.executable, "-c", "from pathlib import Path; import sys; [Path(p).read_bytes() for p in sys.argv[1:]]",
             *(str(path) for path in files)],
            preexec_fn=drop_privileges, capture_output=True, text=True,
        )
        self.assertEqual(reader.returncode, 0, reader.stderr)

    def run_fixture(self, mode, expected_exit=1, interrupt=False, interrupt_signal=signal.SIGTERM):
        with tempfile.TemporaryDirectory(prefix="lightlix-e2e-fixture-") as work:
            work = Path(work)
            work.chmod(0o755)
            iso, firmware = work / "fixture.iso", work / "firmware.fd"
            iso.touch()
            firmware.write_bytes(b"fixture")
            log_root = work / "logs"
            log_root.mkdir(mode=0o755)
            log_root.chmod(0o755)
            unrelated = log_root / "existing-run"
            unrelated.mkdir(mode=0o700)
            private_log = unrelated / "private.log"
            private_log.touch(mode=0o600)
            environment = os.environ.copy()
            environment.update({
                "QEMU_BIN": str(ROOT / "fixtures/fake-qemu.py"), "QEMU_ACCEL": "tcg",
                "OVMF_CODE": str(firmware), "OVMF_VARS_TEMPLATE": str(firmware),
                "E2E_LOG_DIR": str(work / "logs"), "TMPDIR": str(work),
                "BOOT_TIMEOUT": "5", "COMMAND_TIMEOUT": "1", "PULL_TIMEOUT": "1",
                "START_TIMEOUT": "1", "CHAT_TIMEOUT": "1", "E2E_TIMEOUT": "30",
                "FAKE_MODE": mode, "FAKE_PIDFILE": str(work / "pid"), "FAKE_ARGV": str(work / "argv"),
            })
            driver_args = [sys.executable, str(ROOT / "cpu-inference.py"), str(iso)]
            if mode == "total-timeout":
                environment["E2E_TIMEOUT"] = "3"
            if mode == "kvm-fallback":
                environment["QEMU_ACCEL"] = "auto"
                # fixture 안에서만 KVM 장치가 있는 것으로 보이게 한다. 실제 장치는 열지 않는다.
                launcher = (
                    "import os,runpy,sys; original=os.access; "
                    "os.access=lambda path,mode: True if path=='/dev/kvm' else original(path,mode); "
                    "sys.argv=sys.argv[1:]; runpy.run_path(sys.argv[0],run_name='__main__')"
                )
                driver_args = [sys.executable, "-c", launcher, str(ROOT / "cpu-inference.py"), str(iso)]
            proc = subprocess.Popen(
                driver_args, env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE,
                preexec_fn=lambda: os.umask(0o077),
            )
            try:
                if interrupt:
                    deadline = time.monotonic() + 5
                    while not (work / "pid").exists() and time.monotonic() < deadline:
                        time.sleep(0.02)
                    self.assertTrue((work / "pid").exists(), "fixture did not start")
                    proc.send_signal(interrupt_signal)
                stdout, stderr = proc.communicate(timeout=35)
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.communicate(timeout=5)
            self.assertEqual(proc.returncode, expected_exit, (stdout + stderr).decode())
            logs = list((work / "logs").glob("run-*"))
            self.assertEqual(len(logs), 1)
            result = json.loads((logs[0] / "result.json").read_text())
            self.assertTrue((logs[0] / "serial.log").exists())
            self.assertTrue((logs[0] / "commands.jsonl").exists())
            self.assertEqual(logs[0].stat().st_mode & 0o777, 0o755)
            log_files = [path for path in logs[0].iterdir() if path.is_file()]
            self.assertTrue(log_files)
            for path in log_files:
                self.assertEqual(path.stat().st_mode & 0o777, 0o644, path)
            self.assert_unprivileged_reader_can_read(log_files)
            self.assertEqual(unrelated.stat().st_mode & 0o777, 0o700)
            self.assertEqual(private_log.stat().st_mode & 0o777, 0o600)
            qemu_pid = int((work / "pid").read_text())
            with self.assertRaises(ProcessLookupError):
                os.kill(qemu_pid, 0)
            # 임시 펌웨어는 삭제되고, 원본과 로그는 남아야 한다.
            argv = json.loads((work / "argv").read_text())
            vars_option = next(arg for arg in argv if arg.startswith("if=pflash,format=raw,unit=1,"))
            self.assertFalse(Path(vars_option.split("file=", 1)[1]).exists())
            self.assertEqual(firmware.read_bytes(), b"fixture")
            self.assertEqual(argv[argv.index("-nic") + 1], "user,model=virtio-net-pci,ipv6=off")
            self.assertIn("-no-reboot", argv)
            self.assertEqual(argv[argv.index("-m") + 1], "4096M")
            commands = [json.loads(line) for line in (logs[0] / "commands.jsonl").read_text().splitlines()]
            result["_fixture_outputs"] = {
                path.name: path.read_text(encoding="utf-8")
                for path in logs[0].glob("*-*.txt")
            }
            if mode == "pass":
                labels = [event["label"] for event in commands if event["event"] == "start"]
                self.assertLess(labels.index("disk-memory-before-pull"), labels.index("model-pull"))
                diagnostics = next(logs[0].glob("*-disk-memory-before-pull.txt")).read_text()
                for evidence in ("Available", "MemTotal", "overlay"):
                    self.assertIn(evidence, diagnostics)
            if mode == "kvm-fallback":
                attempts = [json.loads(line) for line in (logs[0] / "qemu-command.jsonl").read_text().splitlines()]
                self.assertEqual([attempt[attempt.index("-accel") + 1] for attempt in attempts], ["kvm", "tcg"])
            return result, commands

    def test_complete_scenario(self):
        result, commands = self.run_fixture("pass", expected_exit=0)
        self.assertEqual(result["outcome"], "succeeded")
        starts = [event for event in commands if event["event"] == "start"]
        self.assertTrue(any(event["label"] == "model-sha256" for event in starts))
        self.assertTrue(any(event["label"] == "chat-after-stop" for event in starts))
        labels = [event["label"] for event in starts]
        for label in ("autostart-off", "daemon-restart-autostart-off", "status-after-daemon-restart-off",
                      "chat-after-daemon-restart-off", "autostart-on", "daemon-restart-autostart-on",
                      "status-after-daemon-restart-on", "chat-after-daemon-restart-on"):
            self.assertIn(label, labels)
        self.assertLess(labels.index("autostart-off"), labels.index("daemon-restart-autostart-off"))
        self.assertLess(labels.index("daemon-restart-autostart-off"), labels.index("autostart-on"))
        self.assertLess(labels.index("autostart-on"), labels.index("daemon-restart-autostart-on"))
        for event in starts:
            if event["label"] in ("status-initial", "model-pull", "model-default", "serve-plan", "serve-start", "chat", "serve-stop"):
                self.assertTrue(event["command"].startswith("lightlix --json "))
                self.assertNotIn("sudo", event["command"])

    def test_trained_context_is_taken_from_the_actual_plan(self):
        result, commands = self.run_fixture("trained-context-128", expected_exit=0)
        self.assertEqual(result["outcome"], "succeeded")
        outputs = result["_fixture_outputs"]

        def raw_output(label):
            matches = [content for name, content in outputs.items() if name.endswith("-" + label + ".txt")]
            self.assertEqual(len(matches), 1, f"missing or duplicated {label} fixture output")
            return matches[0]

        def output(label):
            return json.loads(raw_output(label))

        plan = output("serve-plan")
        self.assertEqual(plan["input"]["ctx_train"], 128)
        self.assertEqual(plan["plan"]["ctx"], 128)
        self.assertEqual(plan["plan"]["batch"], 32)
        self.assertIn("ctx override 256 ignored", " ".join(plan["plan"]["warnings"]))
        commands_by_label = {event["label"]: event["command"] for event in commands if event["event"] == "start"}
        self.assertEqual(commands_by_label["serve-plan"],
                         "lightlix --json serve plan --ctx 256 --batch 32")
        self.assertEqual(commands_by_label["serve-start"],
                         "lightlix --json serve start --ctx 256 --batch 32")

        expected_pid = 42
        for label in ("serve-start", "status-running", "status-after-daemon-restart-off",
                      "status-after-daemon-restart-on"):
            server = output(label)["server"]
            self.assertEqual(server["model_id"], "stories260K.gguf")
            self.assertEqual(server["pid"], expected_pid)
            self.assertEqual(server["plan"]["ctx"], plan["plan"]["ctx"])
            self.assertEqual(server["plan"]["batch"], 32)

        starts = [event["label"] for event in commands if event["event"] == "start"]
        expected_order = (
            "serve-plan", "serve-start", "status-running", "chat", "autostart-off",
            "daemon-restart-autostart-off", "status-after-daemon-restart-off", "chat-after-daemon-restart-off",
            "autostart-on", "daemon-restart-autostart-on", "status-after-daemon-restart-on",
            "chat-after-daemon-restart-on", "serve-stop", "status-stopped", "chat-after-stop",
        )
        positions = [starts.index(label) for label in expected_order]
        self.assertEqual(positions, sorted(positions))
        for label in ("model-sha256", "chat", "chat-after-daemon-restart-off",
                      "chat-after-daemon-restart-on", "serve-stop", "status-stopped", "chat-after-stop"):
            self.assertIn(label, starts)
        self.assertEqual(raw_output("model-sha256").split()[0],
                         "047bf46455a544931cff6fef14d7910154c56afbc23ab1c5e56a72e69912c04b")
        for label in ("chat", "chat-after-daemon-restart-off", "chat-after-daemon-restart-on"):
            chat = output(label)
            self.assertTrue(chat["content"].strip())
            self.assertGreater(chat["tokens_predicted"], 0)
            self.assertGreater(chat["tokens_per_second"], 0)
        self.assertEqual(output("serve-stop")["state"], "stopped")
        self.assertEqual(output("status-stopped")["server"]["state"], "stopped")
        self.assertEqual(output("status-stopped")["server"]["pid"], 0)

    def test_serve_start_context_must_match_the_validated_plan(self):
        result, commands = self.run_fixture("wrong-start-context")
        self.assertEqual(result["kind"], "assertion")
        self.assertEqual(result["outcome"], "failed")
        self.assertEqual(result["message"], "Serve start changed the active context size")
        labels = [event["label"] for event in commands if event["event"] == "start"]
        self.assertIn("serve-plan", labels)
        self.assertIn("serve-start", labels)
        self.assertNotIn("status-running", labels)

    def test_rejects_bad_data_and_service_states(self):
        for mode, kind in (("bad-permissions", "assertion"), ("failed-unit", "assertion"),
                           ("bad-sha", "assertion"), ("empty-chat", "assertion"), ("bad-json", "invalid_json")):
            with self.subTest(mode=mode):
                result, _ = self.run_fixture(mode)
                self.assertEqual(result["kind"], kind)
                self.assertEqual(result["outcome"], "failed")

    def test_process_exit_is_not_timeout(self):
        result, _ = self.run_fixture("qemu-exit")
        self.assertEqual(result["kind"], "qemu_exit")
        self.assertIn("7", result["message"])

    def test_boot_timeout(self):
        result, _ = self.run_fixture("boot-timeout")
        self.assertEqual(result["kind"], "serial_timeout")

    def test_efi_check_failure_propagates(self):
        result, _ = self.run_fixture("efi-check-failure")
        self.assertEqual(result["kind"], "command_exit")
        self.assertIn("identity exited 1", result["message"])

    def test_daemon_restart_rejects_missing_adoption_pid_or_plan(self):
        for mode in ("no-adoption", "replaced-pid"):
            with self.subTest(mode=mode):
                result, _ = self.run_fixture(mode)
                self.assertEqual(result["kind"], "assertion")
                self.assertEqual(result["outcome"], "failed")

    def test_daemon_restart_context_change_is_rejected_after_each_setting(self):
        for mode, setting, expected_labels in (
            ("lost-runtime-plan-off", "off", ("status-after-daemon-restart-off",)),
            ("lost-runtime-plan-on", "on", ("status-after-daemon-restart-off",
                                               "status-after-daemon-restart-on")),
        ):
            with self.subTest(mode=mode):
                result, commands = self.run_fixture(mode)
                self.assertEqual(result["kind"], "assertion")
                self.assertEqual(result["outcome"], "failed")
                self.assertIn(f"Daemon restart with autostart {setting} changed the active context size",
                              result["message"])
                labels = [event["label"] for event in commands if event["event"] == "start"]
                for label in expected_labels:
                    self.assertIn(label, labels)
                if setting == "off":
                    self.assertNotIn("status-after-daemon-restart-on", labels)

    def test_shell_efi_check_failure_is_not_masked_by_uname(self):
        with tempfile.TemporaryDirectory(prefix="lightlix-missing-efi-") as work:
            missing_efi = Path(work) / "sys/firmware/efi"
            command = "id -u && id -un && id -Gn && test -d \"$1\" && uname -m"
            result = subprocess.run(
                ["sh", "-c", command, "identity-check", str(missing_efi)],
                capture_output=True, text=True,
            )
        self.assertNotEqual(result.returncode, 0)
        self.assertNotIn("x86_64", result.stdout)

    def test_guest_timeout_and_exit_are_distinct(self):
        for mode, kind in (("guest-timeout", "command_timeout"), ("command-exit", "command_exit"),
                           ("guest-killed", "command_exit"), ("serial-timeout", "serial_timeout")):
            with self.subTest(mode=mode):
                result, _ = self.run_fixture(mode)
                self.assertEqual(result["kind"], kind)

    def test_total_timeout(self):
        result, _ = self.run_fixture("total-timeout")
        self.assertEqual(result["kind"], "total_timeout")

    def test_kvm_initialization_falls_back_to_tcg(self):
        result, _ = self.run_fixture("kvm-fallback", expected_exit=0)
        self.assertEqual(result["accel"], "tcg")

    def test_signal_cleans_process_and_firmware(self):
        for interrupt_signal in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signum=interrupt_signal):
                result, _ = self.run_fixture("signal", expected_exit=128 + interrupt_signal,
                                             interrupt=True, interrupt_signal=interrupt_signal)
                self.assertEqual(result["kind"], "signal")


class DockerWrapperTests(unittest.TestCase):
    def wrapper_fixture(self, interrupt=False, interrupt_signal=signal.SIGTERM):
        with tempfile.TemporaryDirectory(prefix="lightlix-docker-fixture-") as work:
            work = Path(work)
            (work / "fixture.iso").touch()
            fake = work / "docker"
            fake.write_text("""#!/usr/bin/env python3
import json,os,sys,time
from pathlib import Path
with open(os.environ['FAKE_DOCKER_LOG'],'a') as log:
    log.write(json.dumps(sys.argv[1:])+'\\n')
if sys.argv[1]=='run':
    Path(sys.argv[sys.argv.index('--cidfile')+1]).write_text('fixture-container\\n')
    if os.environ.get('FAKE_DOCKER_MODE')=='signal':
        Path(os.environ['FAKE_DOCKER_STATE']+'/pid').write_text(str(os.getpid()))
        while not Path(os.environ['FAKE_DOCKER_STATE']+'/stopped').exists():
            time.sleep(0.02)
    sys.exit(17)
if sys.argv[1]=='stop':
    Path(os.environ['FAKE_DOCKER_STATE']+'/stopped').touch()
""")
            fake.chmod(0o755)
            environment = os.environ.copy()
            environment.update({"PATH": str(work) + os.pathsep + environment["PATH"],
                                "TMPDIR": str(work), "E2E_LOG_DIR": str(work / "logs"),
                                "E2E_TIMEOUT": "27", "QEMU_ACCEL": "tcg",
                                "FAKE_DOCKER_LOG": str(work / "docker.jsonl"),
                                "FAKE_DOCKER_MODE": "signal" if interrupt else "exit",
                                "FAKE_DOCKER_STATE": str(work)})
            proc = subprocess.Popen(["bash", str(ROOT.parent.parent / "scripts/test-qemu-e2e-in-docker.sh"),
                                     str(work / "fixture.iso")], env=environment,
                                    stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                if interrupt:
                    deadline = time.monotonic() + 5
                    while not (work / "pid").exists() and time.monotonic() < deadline:
                        time.sleep(0.02)
                    self.assertTrue((work / "pid").exists())
                    proc.send_signal(interrupt_signal)
                stdout, stderr = proc.communicate(timeout=10)
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.communicate(timeout=5)
            expected_exit = 128 + interrupt_signal if interrupt else 17
            self.assertEqual(proc.returncode, expected_exit, (stdout + stderr).decode())
            calls = [json.loads(line) for line in (work / "docker.jsonl").read_text().splitlines()]
            run = next(call for call in calls if call[0] == "run")
            self.assertEqual(run[run.index("--platform") + 1], "linux/amd64")
            self.assertIn("--rm", run)
            self.assertIn("--init", run)
            self.assertNotIn("--privileged", run)
            self.assertIn("E2E_TIMEOUT=27", run)
            self.assertIn("QEMU_ACCEL=tcg", run)
            self.assertIn("E2E_LOG_DIR=/logs", run)
            name = run[run.index("--name") + 1]
            self.assertIn(["stop", "--time", "10", name], calls)
            self.assertIn(["rm", "-f", name], calls)
            self.assertFalse(Path(run[run.index("--cidfile") + 1]).exists())
            if interrupt:
                with self.assertRaises(ProcessLookupError):
                    os.kill(int((work / "pid").read_text()), 0)

    def test_flags_exit_code_and_cleanup(self):
        self.wrapper_fixture()

    def test_signal_stops_container_and_client(self):
        for interrupt_signal in (signal.SIGINT, signal.SIGTERM):
            with self.subTest(signum=interrupt_signal):
                self.wrapper_fixture(interrupt=True, interrupt_signal=interrupt_signal)


if __name__ == "__main__":
    unittest.main(verbosity=2)
