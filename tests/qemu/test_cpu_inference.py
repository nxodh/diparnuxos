#!/usr/bin/env python3
"""ISO 없이 제어된 프로세스와 직렬 fixture 로 오판 및 정리 경로를 확인한다."""

import json
import os
from pathlib import Path
import signal
import subprocess
import sys
import tempfile
import time
import unittest


ROOT = Path(__file__).resolve().parent


class DriverTests(unittest.TestCase):
    def run_fixture(self, mode, expected_exit=1, interrupt=False):
        with tempfile.TemporaryDirectory(prefix="lightlix-e2e-fixture-") as work:
            work = Path(work)
            iso, firmware = work / "fixture.iso", work / "firmware.fd"
            iso.touch()
            firmware.write_bytes(b"fixture")
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
            proc = subprocess.Popen(driver_args,
                                    env=environment, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
            try:
                if interrupt:
                    deadline = time.monotonic() + 5
                    while not (work / "pid").exists() and time.monotonic() < deadline:
                        time.sleep(0.02)
                    self.assertTrue((work / "pid").exists(), "fixture did not start")
                    proc.send_signal(signal.SIGTERM)
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
            commands = [json.loads(line) for line in (logs[0] / "commands.jsonl").read_text().splitlines()]
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
        for event in starts:
            if event["label"] in ("status-initial", "model-pull", "model-default", "serve-plan", "serve-start", "chat", "serve-stop"):
                self.assertTrue(event["command"].startswith("lightlix --json "))
                self.assertNotIn("sudo", event["command"])

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
        result, _ = self.run_fixture("signal", expected_exit=143, interrupt=True)
        self.assertEqual(result["kind"], "signal")


class DockerWrapperTests(unittest.TestCase):
    def wrapper_fixture(self, interrupt=False):
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
                    proc.send_signal(signal.SIGTERM)
                stdout, stderr = proc.communicate(timeout=10)
            finally:
                if proc.poll() is None:
                    proc.kill()
                    proc.communicate(timeout=5)
            self.assertEqual(proc.returncode, 143 if interrupt else 17, (stdout + stderr).decode())
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
        self.wrapper_fixture(interrupt=True)


if __name__ == "__main__":
    unittest.main(verbosity=2)
