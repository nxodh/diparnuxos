#!/usr/bin/env python3
"""UEFI 게스트에서 헤드리스 설치(lightlix-install)와 설치된 시스템의 부팅을 검사한다.

흐름은 세 번의 부팅이다.
1. ISO 로 부팅해 빈 가상 디스크에 lightlix-install 로 설치한다(거절 경로도 확인한다).
2. 디스크만으로 부팅해 설치된 시스템을 검사한다: 루트, 계정, 서비스, 부트로더, 업데이트 상태, 다시 부팅 뒤 영속성.
3. Microsoft 키가 들어 있는 OVMF 변수로 Secure Boot 를 켜고 shim 경로(EFI/BOOT/BOOTX64.EFI)로 부팅한다.

cpu-inference.py 의 Console 을 그대로 쓴다. QEMU 가상 디스크 설치이며 실기 NVMe 설치나 펌웨어 부팅 항목은 확인하지 않는다.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time

HERE = Path(__file__).resolve().parent


def load_base():
    spec = importlib.util.spec_from_file_location("cpu_inference_base", HERE / "cpu-inference.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


base = load_base()
Failure, require, seconds, qemu_value, Console = base.Failure, base.require, base.seconds, base.qemu_value, base.Console

HOSTNAME = "lxtest"
USER = "tester"
PASSWORD = "lightlixtest1"


def json_object(text, label):
    """명령 출력에서 마지막 JSON 객체 줄을 꺼낸다. 직렬 콘솔에는 로그 줄이 섞일 수 있다."""
    for line in reversed(text.splitlines()):
        line = line.strip()
        if line.startswith("{") and line.endswith("}"):
            try:
                value = json.loads(line)
            except ValueError:
                continue
            if isinstance(value, dict):
                return value
    raise Failure("invalid_json", f"No JSON object in the output of {label}: {text[-300:]!r}")


def multiline_json(text, label):
    lines = text.splitlines()
    for start, line in enumerate(lines):
        if line.strip() == "{":
            try:
                value = json.loads("\n".join(lines[start:]))
            except ValueError:
                continue
            if isinstance(value, dict):
                return value
    return json_object(text, label)


def login_installed(console, timeout):
    console.wait(rf"{HOSTNAME} login: *", timeout, "installed boot/login prompt")
    console.send(USER)
    console.wait(r"Password: *", timeout, "installed login/password prompt")
    console.send(PASSWORD)
    console.wait(rf"(?:{USER}@[^\n]*[$] |\n[$] )", timeout, "installed login/shell prompt")
    console.command("session", "stty -echo; export LC_ALL=C TERM=dumb; unset PROMPT_COMMAND; PS1=''", min(timeout, 60))


def firmware(name, default):
    return base.firmware(name, default)


def argv_for(qemu, chosen, code, variables, disk, iso=None, name="lightlix-install-e2e"):
    argv = [qemu, "-name", name, "-machine", "q35", "-accel", chosen,
            "-cpu", "host" if chosen == "kvm" else "max", "-smp", "2", "-m", "3072M",
            "-drive", f"if=pflash,format=raw,unit=0,readonly=on,file={qemu_value(code)}",
            "-drive", f"if=pflash,format=raw,unit=1,file={qemu_value(variables)}",
            "-drive", f"if=virtio,format=qcow2,file={qemu_value(disk)}",
            "-nic", "user,model=virtio-net-pci,ipv6=off",
            "-display", "none", "-serial", "stdio", "-no-reboot"]
    if iso is not None:
        argv += ["-cdrom", str(iso), "-boot", "order=d"]
    return argv


def wait_exit(console, timeout, phase):
    try:
        console.proc.wait(timeout=timeout)
    except subprocess.TimeoutExpired:
        raise Failure("poweroff_timeout", f"QEMU did not exit within {timeout}s during {phase}") from None


def scenario_live(console, budgets):
    cmd = budgets["COMMAND_TIMEOUT"]
    console.login(budgets["BOOT_TIMEOUT"])
    console.command("live-check", "test -d /run/live && test -d /sys/firmware/efi && lsblk -dno NAME,SIZE,TYPE", cmd)

    # 거절 경로: 디스크가 아닌 장치, 없는 디스크, 확인 없음, 약한 비밀번호. 어느 것도 디스크를 바꾸지 않는다.
    base_args = f"--hostname {HOSTNAME} --user {USER} --password-stdin --json"
    refused = {
        "refuse-partition": (f"--disk /dev/vda1 {base_args} --yes", "invalid_option"),
        "refuse-missing-disk": (f"--disk /dev/vdz {base_args} --yes", "disk_not_found"),
        "refuse-no-confirmation": (f"--disk /dev/vda {base_args}", "not_confirmed"),
    }
    for label, (args, code) in refused.items():
        out = console.command(label, f"printf '%s\\n' {shlex.quote(PASSWORD)} | sudo -n lightlix-install run {args}", cmd, expected=1)
        require(f'"code":"{code}"' in out, f"{label}: expected code {code}: {out[-300:]}")
    weak = console.command("refuse-weak-password",
                           f"printf 'short\\n' | sudo -n lightlix-install run --disk /dev/vda {base_args} --yes", cmd, expected=1)
    require('"code":"invalid_option"' in weak, "A weak password must be refused")
    console.command("disk-untouched", "! sudo -n blkid /dev/vda", cmd)

    output = console.command(
        "install",
        f"printf '%s\\n' {shlex.quote(PASSWORD)} | sudo -n lightlix-install run --disk /dev/vda "
        f"--hostname {HOSTNAME} --user {USER} --admin --password-stdin --mode server --console serial "
        f"--locale en_US.UTF-8 --timezone UTC --keyboard us --yes --json",
        budgets["INSTALL_TIMEOUT"],
    )
    result = json_object(output, "install")
    require(result.get("ok") is True and result.get("disk") == "/dev/vda", f"Install did not report success: {result}")
    require(PASSWORD not in output, "The password appeared in the installer output")
    steps = [json.loads(l)["step"] for l in output.splitlines() if l.startswith('{"time"')]
    for want in ("disk", "format", "mount", "copy", "configure", "account", "cleanup", "initramfs", "bootloader", "done"):
        require(want in steps, f"Installer step {want!r} did not run: {steps}")
    console.command("install-log", "sudo -n test -s /var/log/lightlix-install.log && ! sudo -n grep -q " + shlex.quote(PASSWORD) + " /var/log/lightlix-install.log", cmd)
    console.command("target-unmounted", "! findmnt /mnt/lightlix-target", cmd)
    console.command("layout", "sudo -n sfdisk -d /dev/vda", cmd)
    console.send("sudo -n systemctl poweroff")
    wait_exit(console, 180, "poweroff after install")


def scenario_installed(console, budgets, secure_boot):
    cmd = budgets["COMMAND_TIMEOUT"]
    login_installed(console, budgets["BOOT_TIMEOUT"])
    label = "sb-" if secure_boot else ""

    console.command(label + "os-release", ". /etc/os-release && test \"$ID\" = lightlix && echo \"$PRETTY_NAME\"", cmd)
    console.command(label + "not-live", "! test -e /run/live && ! grep -q boot=live /proc/cmdline", cmd)
    root = console.command(label + "root-device", "findmnt -no SOURCE,FSTYPE /", cmd)
    require(root.split()[0] == "/dev/vda2" and root.split()[1] == "ext4", f"Unexpected root file system: {root}")
    console.command(label + "efi-mounted", "findmnt -no SOURCE,FSTYPE /boot/efi | grep -x '/dev/vda1 vfat'", cmd)
    console.command(label + "hostname", f"test \"$(hostname)\" = {HOSTNAME}", cmd)
    # 부트로더 파일. 목록을 먼저 남기고 하나씩 판정한다(어느 파일이 없는지 로그에서 바로 보이게).
    console.command(label + "esp-listing", "find /boot/efi -type f | sort", cmd)
    for name, path in (("shim", "/boot/efi/EFI/lightlix/shimx64.efi"), ("grub", "/boot/efi/EFI/lightlix/grubx64.efi"),
                       ("fallback", "/boot/efi/EFI/BOOT/BOOTX64.EFI")):
        console.command(label + "esp-" + name, f"test -s {path}", cmd)
    ids = console.command(label + "identity", "id -nG && id -u", cmd).splitlines()
    groups = ids[0].split()
    require(all(g in groups for g in ("lightlix", "sudo", "video", "render")), f"Missing groups: {groups}")
    sb = console.command(label + "secure-boot-state", "mokutil --sb-state 2>&1 || true", cmd)
    if secure_boot:
        require("SecureBoot enabled" in sb, f"Secure Boot should be enabled: {sb}")
    else:
        require("enabled" not in sb.replace("disabled", ""), f"Secure Boot unexpectedly enabled: {sb}")

    if secure_boot:
        # shim 경로로 부팅했고 커널은 잠금 상태다. 서버 상태만 확인하고 끝낸다.
        console.command("sb-lockdown", "cat /sys/kernel/security/lockdown 2>/dev/null || true", cmd)
        poll = f"end=$(( $(date +%s) + {budgets['START_TIMEOUT']} )); until lightlix --json status >/dev/null 2>&1; do [ \"$(date +%s)\" -ge \"$end\" ] && exit 1; sleep 2; done"
        console.command("sb-daemon-ready", poll, budgets["START_TIMEOUT"] + 20)
        return

    # 서비스
    poll = f"end=$(( $(date +%s) + {budgets['START_TIMEOUT']} )); until lightlix --json status >/dev/null 2>&1; do [ \"$(date +%s)\" -ge \"$end\" ] && exit 1; sleep 2; done"
    console.command("daemon-ready", poll, budgets["START_TIMEOUT"] + 20)
    status = json_object(console.command("status", "lightlix --json status | tr -d '\\n'", cmd), "status")
    require(status.get("mode") == "server", "Installed system must start in server mode")
    console.command("default-target", "systemctl get-default | grep -x multi-user.target", cmd)
    users = json_object(console.command("user-list", "lightlix --json user list | tr -d '\\n'", cmd), "user-list")
    require([u["name"] for u in users.get("users", [])] == [USER], f"Account list: {users}")
    console.command("live-packages-gone", "! dpkg-query -W -f '${db:Status-Status}\\n' live-boot live-config 2>/dev/null | grep -qx installed", cmd)
    console.command("grub-default", "grep -x 'saved_entry=gnulinux-.*' /boot/grub/grubenv", cmd)
    console.command("grub-dropin", "grep -q '^GRUB_DEFAULT=saved' /etc/default/grub.d/20-lightlix.cfg", cmd)
    console.command("nvidia-modules", "modinfo -k \"$(uname -r)\" -F license nvidia | grep -x 'Dual MIT/GPL'", cmd)
    console.command("root-locked", "sudo -n passwd -S root | awk '{exit $2 == \"L\" || $2 == \"LK\" ? 0 : 1}'", cmd)
    console.command("memory-installed-server", "awk '/MemTotal|MemAvailable/' /proc/meminfo", cmd)

    # 업데이트 상태. 설치된 시스템이므로 사용할 수 있어야 한다.
    info = multiline_json(console.command("update-status", "lightlix --json update --status", cmd), "update-status")
    require(info.get("available") is True and info.get("live") is False, f"Updates should be available: {info}")
    require(info.get("state", {}).get("phase") == "idle", f"Fresh install must be idle: {info.get('state')}")
    check = console.command("update-check", "lightlix update --check; echo rc=$?", budgets["UPDATE_TIMEOUT"])
    require("rc=0" in check or "network" in check, f"update --check neither succeeded nor reported a network problem: {check[-300:]}")

    # 재부팅 뒤에도 같은 시스템이다.
    console.command("reboot-marker", "sudo -n sh -c 'echo persisted > /var/lib/lightlix/e2e-marker'", cmd)
    console.send("sudo -n systemctl reboot")


def main():
    sys.stdout.reconfigure(encoding="ascii", errors="backslashreplace")
    sys.stderr.reconfigure(encoding="ascii", errors="backslashreplace")
    parser = argparse.ArgumentParser(description="Lightlix headless installation E2E")
    parser.add_argument("iso", type=Path)
    args = parser.parse_args()
    log_root = Path(os.environ.get("E2E_LOG_DIR", "out/qemu-install"))
    os.umask(0o022)
    log_root.mkdir(parents=True, exist_ok=True)
    logs = log_root / f"run-{secrets.token_hex(4)}"
    logs.mkdir(mode=0o755)
    print(f"install: logs={logs}", flush=True)

    result = {"outcome": "failed", "kind": "internal", "message": "Driver did not finish"}
    console = None
    exit_code = 1
    start = time.monotonic()

    def interrupted(signum, _frame):
        raise Failure("signal", f"Interrupted by signal {signum}")

    signal.signal(signal.SIGTERM, interrupted)
    signal.signal(signal.SIGINT, interrupted)
    try:
        if not args.iso.is_file():
            raise Failure("usage", f"ISO not found: {args.iso}")
        qemu = os.environ.get("QEMU_BIN", "qemu-system-x86_64")
        if not shutil.which(qemu) or not shutil.which("qemu-img"):
            raise Failure("usage", "qemu-system-x86_64 and qemu-img are required")
        code = firmware("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.fd")
        template = firmware("OVMF_VARS_TEMPLATE", "/usr/share/OVMF/OVMF_VARS_4M.fd")
        sb_template = os.environ.get("OVMF_VARS_SB_TEMPLATE", "/usr/share/OVMF/OVMF_VARS_4M.ms.fd")
        sb_code = os.environ.get("OVMF_CODE_SB", "/usr/share/OVMF/OVMF_CODE_4M.secboot.fd")
        accel = os.environ.get("QEMU_ACCEL", "auto")
        kvm = os.access("/dev/kvm", os.R_OK | os.W_OK)
        if accel == "kvm" and not kvm:
            raise Failure("usage", "QEMU_ACCEL=kvm requires read/write access to /dev/kvm")
        chosen = "kvm" if accel != "tcg" and kvm else "tcg"
        slow = chosen == "tcg"
        budgets = {name: seconds(name, value) for name, value in {
            "BOOT_TIMEOUT": 1800 if slow else 300,
            "COMMAND_TIMEOUT": 180 if slow else 60,
            "START_TIMEOUT": 900 if slow else 240,
            "INSTALL_TIMEOUT": 5400 if slow else 1200,
            "UPDATE_TIMEOUT": 900 if slow else 300,
            "E2E_TIMEOUT": 14400 if slow else 3600,
        }.items()}
        result.update({"accel": chosen, "budgets": budgets})
        print(f"install: accel={chosen} total_timeout={budgets['E2E_TIMEOUT']}s", flush=True)
        deadline = start + budgets["E2E_TIMEOUT"]

        with tempfile.TemporaryDirectory(prefix="lightlix-install-") as work:
            work = Path(work)
            disk = work / "disk.qcow2"
            subprocess.run(["qemu-img", "create", "-q", "-f", "qcow2", str(disk), "20G"], check=True)
            variables = work / "OVMF_VARS.fd"
            shutil.copyfile(template, variables)

            # 1. ISO 로 부팅해 설치한다.
            sub = logs / "1-install"
            sub.mkdir()
            console = Console(argv_for(qemu, chosen, code, variables, disk, iso=args.iso), sub, deadline)
            console.start()
            scenario_live(console, budgets)
            console.close()
            console = None

            # 2. 디스크만으로 부팅한다.
            sub = logs / "2-installed"
            sub.mkdir()
            console = Console(argv_for(qemu, chosen, code, variables, disk, name="lightlix-installed-e2e"), sub, deadline)
            console.start()
            scenario_installed(console, budgets, secure_boot=False)
            wait_exit(console, 180, "reboot of the installed system")
            console.close()
            console = None

            # 다시 부팅한 뒤에도 표식과 계정이 남아 있어야 한다.
            sub = logs / "3-installed-again"
            sub.mkdir()
            console = Console(argv_for(qemu, chosen, code, variables, disk, name="lightlix-installed-e2e"), sub, deadline)
            console.start()
            login_installed(console, budgets["BOOT_TIMEOUT"])
            console.command("marker-persisted", "test \"$(cat /var/lib/lightlix/e2e-marker)\" = persisted", budgets["COMMAND_TIMEOUT"])
            console.send("sudo -n systemctl poweroff")
            wait_exit(console, 180, "poweroff of the installed system")
            console.close()
            console = None

            # 4. Secure Boot 를 켜고 shim 경로로 부팅한다.
            if Path(sb_template).is_file() and Path(sb_code).is_file():
                sb_vars = work / "OVMF_VARS_SB.fd"
                shutil.copyfile(sb_template, sb_vars)
                sub = logs / "4-secure-boot"
                sub.mkdir()
                argv = argv_for(qemu, chosen, sb_code, sb_vars, disk, name="lightlix-secureboot-e2e")
                # secboot 펌웨어는 SMM 이 필요하다.
                argv[argv.index("-machine") + 1] = "q35,smm=on"
                argv += ["-global", "driver=cfi.pflash01,property=secure,value=on"]
                console = Console(argv, sub, deadline)
                console.start()
                scenario_installed(console, budgets, secure_boot=True)
                console.send("sudo -n systemctl poweroff")
                wait_exit(console, 180, "poweroff under Secure Boot")
                console.close()
                console = None
                result["secure_boot"] = "booted"
            else:
                result["secure_boot"] = "skipped: no OVMF Secure Boot firmware"
                print("install: Secure Boot firmware not found; skipping the Secure Boot boot", flush=True)

        result.update({"outcome": "succeeded", "kind": "pass", "message": "Installed, booted from disk and verified"})
        exit_code = 0
        print("PASS: headless install, boot from disk, reboot persistence, Secure Boot shim boot", flush=True)
    except Failure as error:
        result.update({"kind": error.kind, "message": str(error)})
        exit_code = 2 if error.kind == "usage" else 1
        if error.kind == "signal":
            exit_code = 128 + int(str(error).split()[-1])
        print(f"FAIL: {error.kind}: {error}", file=sys.stderr)
    except (OSError, ValueError, KeyError, TypeError, subprocess.CalledProcessError) as error:
        result.update({"kind": "driver_error", "message": str(error)})
        print(f"FAIL: driver_error: {error}", file=sys.stderr)
    finally:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        if console:
            console.close()
        result["elapsed_seconds"] = round(time.monotonic() - start, 3)
        (logs / "result.json").write_text(json.dumps(result, indent=2) + "\n", encoding="ascii")
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
