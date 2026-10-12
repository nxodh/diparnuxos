#!/usr/bin/env python3
"""UEFI 게스트에서 데스크톱 모드 전환, greetd 로그인 화면, labwc 세션, Control Center 를 검사한다.

cpu-inference.py 의 Console 과 보조 함수를 그대로 쓴다. 이 드라이버는 QEMU 모니터 소켓으로
화면을 캡처하고 키 입력을 보낸다. 화면 캡처는 "무엇인가 그려졌다"만 보며 실제 모니터 표시를 대신하지 않는다.
"""

import argparse
import importlib.util
import json
import os
from pathlib import Path
import re
import secrets
import shutil
import signal
import socket
import struct
import sys
import tempfile
import time
import zlib

HERE = Path(__file__).resolve().parent


def load_base():
    spec = importlib.util.spec_from_file_location("cpu_inference_base", HERE / "cpu-inference.py")
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


base = load_base()
Failure, require, seconds, qemu_value, Console = base.Failure, base.require, base.seconds, base.qemu_value, base.Console

# 이 테스트가 만드는 계정이다. 키 입력(sendkey)으로 칠 수 있게 소문자와 숫자만 쓴다.
TEST_USER = "tester"
TEST_PASSWORD = "lightlixtest1"

# 키 이름은 QEMU 모니터 sendkey 의 이름이다. 로그인 이름과 비밀번호의 문자만 쓴다.
SPECIAL_KEYS = {"\n": "ret", " ": "spc"}
SCREEN_SIZE_MIN = (320, 200)


class Monitor:
    """QEMU 모니터(HMP) 유닉스 소켓. 명령 하나를 보내고 프롬프트까지 읽는다."""

    def __init__(self, path, timeout=10):
        self.path = path
        self.timeout = timeout
        self.sock = None

    def connect(self, wait=30):
        end = time.monotonic() + wait
        while True:
            try:
                s = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
                s.settimeout(self.timeout)
                s.connect(self.path)
                self.sock = s
                self._read_prompt()
                return
            except (FileNotFoundError, ConnectionRefusedError):
                s.close()
                if time.monotonic() > end:
                    raise Failure("monitor", f"QEMU monitor socket did not appear: {self.path}") from None
                time.sleep(0.2)

    def _read_prompt(self):
        data = b""
        while not data.endswith(b"(qemu) "):
            chunk = self.sock.recv(4096)
            if not chunk:
                raise Failure("monitor", "QEMU monitor closed the connection")
            data += chunk
        return data.decode("utf-8", errors="replace")

    def command(self, line):
        self.sock.sendall((line + "\n").encode("ascii"))
        return self._read_prompt()

    def close(self):
        if self.sock:
            self.sock.close()
            self.sock = None

    def type_text(self, text, delay=0.12):
        for ch in text:
            if ch in SPECIAL_KEYS:
                key = SPECIAL_KEYS[ch]
            elif re.fullmatch(r"[a-z0-9]", ch):
                key = ch
            else:
                raise Failure("usage", f"Unsupported character for sendkey: {ch!r}")
            self.command(f"sendkey {key}")
            time.sleep(delay)

    def screendump(self, path):
        self.command(f"screendump {path}")
        end = time.monotonic() + 10
        while time.monotonic() < end:
            if os.path.exists(path) and os.path.getsize(path) > 0:
                time.sleep(0.2)
                return
            time.sleep(0.1)
        raise Failure("screenshot", f"screendump did not write {path}")


def json_object(text, label):
    """명령 출력에서 JSON 객체를 꺼낸다. 직렬 콘솔에는 커널과 systemd 의 로그 줄이 섞여 들어올 수 있다."""
    lines = text.splitlines()
    for start, line in enumerate(lines):
        if line.strip() == "{":
            try:
                value = json.loads("\n".join(lines[start:]))
            except ValueError:
                continue
            if isinstance(value, dict):
                return value
    raise Failure("invalid_json", f"No JSON object in the output of {label}: {text[:200]!r}")


def read_ppm(path):
    data = Path(path).read_bytes()
    match = re.match(rb"P6\s+(\d+)\s+(\d+)\s+(\d+)\s", data)
    if not match:
        raise Failure("screenshot", "Screen dump is not a binary PPM")
    width, height, maxval = (int(g) for g in match.groups())
    if maxval != 255:
        raise Failure("screenshot", f"Unsupported PPM depth {maxval}")
    pixels = data[match.end():]
    if len(pixels) < width * height * 3:
        raise Failure("screenshot", "Screen dump is truncated")
    return width, height, pixels[: width * height * 3]


def describe_image(width, height, pixels):
    """색 종류와 어두운 화소가 아닌 비율을 센다. 균일한 화면(검은 화면)을 거른다."""
    step = max(1, (width * height) // 20000)
    colors = set()
    lit = 0
    samples = 0
    for i in range(0, width * height, step):
        r, g, b = pixels[3 * i], pixels[3 * i + 1], pixels[3 * i + 2]
        colors.add((r >> 3, g >> 3, b >> 3))
        if r + g + b > 96:
            lit += 1
        samples += 1
    return {"width": width, "height": height, "colors": len(colors), "lit_fraction": round(lit / max(samples, 1), 4)}


def write_png(path, width, height, pixels):
    """작은 PNG 로 저장한다(표준 라이브러리만 사용). 아티팩트 크기를 줄이려고 반으로 줄인다."""
    sw, sh = max(width // 2, 1), max(height // 2, 1)
    rows = []
    for y in range(sh):
        row = bytearray(b"\x00")
        base_y = (y * 2) * width
        for x in range(sw):
            i = 3 * (base_y + x * 2)
            row += pixels[i:i + 3]
        rows.append(bytes(row))
    raw = b"".join(rows)

    def chunk(tag, body):
        c = struct.pack(">I", len(body)) + tag + body
        return c + struct.pack(">I", zlib.crc32(tag + body) & 0xFFFFFFFF)

    png = b"\x89PNG\r\n\x1a\n" + chunk(b"IHDR", struct.pack(">IIBBBBB", sw, sh, 8, 2, 0, 0, 0))
    png += chunk(b"IDAT", zlib.compress(raw, 6)) + chunk(b"IEND", b"")
    Path(path).write_bytes(png)


def poll_command(console, label, command, timeout, attempts_sleep=2):
    """command 가 종료 코드 0 이 될 때까지 timeout 안에서 다시 시도한다. 마지막 출력은 로그에 남는다."""
    script = (
        f"end=$(( $(date +%s) + {timeout} )); "
        f"while :; do if out=$({command} 2>&1); then printf '%s\\n' \"$out\"; exit 0; fi; "
        f"[ \"$(date +%s)\" -ge \"$end\" ] && {{ printf '%s\\n' \"$out\" >&2; exit 1; }}; sleep {attempts_sleep}; done"
    )
    return console.command(label, script, timeout + 20)


def capture(console, monitor, logs, name, workdir):
    shot = os.path.join(workdir, name + ".ppm")
    monitor.screendump(shot)
    width, height, pixels = read_ppm(shot)
    info = describe_image(width, height, pixels)
    write_png(logs / f"{name}.png", width, height, pixels)
    (logs / f"{name}.json").write_text(json.dumps(info) + "\n", encoding="ascii")
    print(f"desktop-session: screenshot {name}: {info}", flush=True)
    return info


def scenario(console, monitor, budgets, logs, workdir):
    cmd = budgets["COMMAND_TIMEOUT"]
    console.login(budgets["BOOT_TIMEOUT"])
    monitor.connect()

    identity = console.command("identity", "id -un && id -Gn && uname -m", cmd).splitlines()
    require(identity[0] == "user" and "lightlix" in identity[1].split(), "Serial session must be the live user in group lightlix")

    # 이미지는 서버 모드로 부팅하고 데스크톱 구성 요소를 갖는다.
    mode = console.command("mode-initial", "lightlix --json mode", cmd)
    require(json_object(mode, "mode-initial").get("mode") == "server", "The image must boot in server mode")
    console.command("greetd-installed", "systemctl cat greetd.service >/dev/null", cmd)
    unit = console.command("greetd-unit", "systemctl cat greetd.service", cmd)
    require("--config /etc/greetd/lightlix.toml" in unit, "greetd does not use the Lightlix configuration")
    console.command("greetd-not-active", "! systemctl is-active --quiet greetd.service", cmd)
    console.command("desktop-files", "test -x /usr/bin/labwc && test -x /usr/bin/tuigreet && test -x /usr/bin/lightlix-control-center", cmd)
    console.command("fonts-korean", "fc-list :lang=ko family | head -n 3 | grep .", cmd)

    # 사용자가 정한 이름과 비밀번호로 계정을 만든다. 비밀번호는 표준 입력으로만 넘긴다.
    created = console.command(
        "account-create",
        f"printf '%s\\n' '{TEST_PASSWORD}' | lightlix --json user add {TEST_USER} --admin --password-stdin",
        cmd + 30,
    )
    require(json_object(created, "account-create").get("name") == TEST_USER, "The account was not created")
    require(TEST_PASSWORD not in created, "The password must not appear in the command output")
    listed = console.command("account-list", "lightlix --json user list", cmd)
    require(TEST_USER in json.dumps(json_object(listed, "account-list")), "The account is missing from the list")
    console.command("account-passwd-entry", f"getent passwd {TEST_USER} | grep -q ':/home/{TEST_USER}:/bin/bash$'", cmd)
    console.command("account-shadow-hash", f"sudo -n getent shadow {TEST_USER} | cut -d: -f2 | grep -q '^[$]'", cmd)
    console.command("account-groups", f"id -nG {TEST_USER} | tr ' ' '\\n' | grep -cxE 'lightlix|video|render|sudo' | grep -qx 4", cmd)
    console.command("account-home", f"sudo -n test -d /home/{TEST_USER}", cmd)
    # 같은 이름은 다시 만들 수 없고, 약한 비밀번호는 거절된다.
    console.command(
        "account-duplicate",
        f"printf '%s\\n' '{TEST_PASSWORD}' | lightlix --json user add {TEST_USER} --password-stdin",
        cmd + 30, expected=1,
    )
    console.command(
        "account-weak-password",
        "printf '%s\\n' 'short' | lightlix --json user add weakone --password-stdin",
        cmd + 30, expected=1,
    )

    # SSH: 켜면 이 계정만 비밀번호로 접속할 수 있고 기본 live 계정은 거절된다.
    ssh_state = console.command("ssh-on", "lightlix --json ssh on", cmd + 60)
    state = json_object(ssh_state, "ssh-on")
    require(state.get("enabled") is True and TEST_USER in (state.get("login_users") or []), "SSH did not list the account")
    poll_command(console, "ssh-active", "systemctl is-active --quiet ssh.service", cmd)
    console.command(
        "ssh-effective-config",
        f"sudo -n sshd -T -C user={TEST_USER},host=localhost,addr=127.0.0.1 | grep -E '^(passwordauthentication yes|permitrootlogin no|allowusers {TEST_USER})$' | wc -l | grep -qx 3",
        cmd,
    )
    askpass = (
        'd=$(mktemp -d); printf \'#!/bin/sh\\necho "$LLX_PW"\\n\' > "$d/ask"; chmod 700 "$d/ask"; '
        'ssh_try() { LLX_PW="$2" SSH_ASKPASS="$d/ask" SSH_ASKPASS_REQUIRE=force '
        'ssh -o BatchMode=no -o PreferredAuthentications=password -o PubkeyAuthentication=no '
        '-o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o ConnectTimeout=15 '
        '"$1@127.0.0.1" id -un; }; '
    )
    out = console.command("ssh-login-created-account", askpass + f"ssh_try {TEST_USER} {TEST_PASSWORD}", cmd + 40)
    require(out.strip().splitlines()[-1] == TEST_USER, "SSH password login with the created account failed")
    console.command("ssh-login-default-account-refused", askpass + "ssh_try user live", cmd + 40, expected=255)

    # 서버 모드의 유휴 메모리(참고용 측정). 판정에는 쓰지 않는다.
    console.command("memory-server-mode", "sleep 5; awk '/MemTotal|MemAvailable/' /proc/meminfo", cmd)

    # 데스크톱 모드로 전환한다.
    switched = console.command("mode-desktop", "lightlix --json mode desktop", budgets["START_TIMEOUT"])
    require(json_object(switched, "mode-desktop").get("mode") == "desktop", "Mode switch did not report desktop")
    poll_command(console, "greetd-active", "systemctl is-active --quiet greetd.service", budgets["START_TIMEOUT"])
    poll_command(console, "tuigreet-running", "pgrep -x tuigreet", budgets["START_TIMEOUT"])
    console.command("default-target-desktop", "systemctl get-default | grep -x graphical.target", cmd)
    time.sleep(3)
    greeter = capture(console, monitor, logs, "greeter", workdir)
    require(greeter["width"] >= SCREEN_SIZE_MIN[0] and greeter["height"] >= SCREEN_SIZE_MIN[1], "Screen is too small")
    require(greeter["colors"] >= 3 and greeter["lit_fraction"] > 0.0005,
            f"The login screen looks blank: {greeter}")

    # 키 입력으로 로그인해 labwc 세션과 Control Center 를 확인한다.
    # 방금 만든 계정으로 로그인한다(기본 live 계정이 아니다).
    monitor.type_text(TEST_USER + "\n")
    time.sleep(1.5)
    monitor.type_text(TEST_PASSWORD + "\n")
    poll_command(console, "labwc-running", f"pgrep -u {TEST_USER} -x labwc", budgets["START_TIMEOUT"])
    poll_command(console, "control-center-running", f"pgrep -u {TEST_USER} -f lightlix-control-center", budgets["START_TIMEOUT"])
    poll_command(console, "panel-running", f"pgrep -u {TEST_USER} -x yambar", budgets["START_TIMEOUT"])

    # 창이 뜰 때까지 기다린다. 소프트웨어 렌더링과 Python 시작은 느릴 수 있다.
    session = None
    deadline = time.monotonic() + budgets["START_TIMEOUT"] / 2
    attempt = 0
    while True:
        attempt += 1
        time.sleep(5)
        session = capture(console, monitor, logs, "session", workdir)
        if session["colors"] >= 8 or time.monotonic() > deadline:
            break

    # 진단: 세션 프로세스와 로그, 그리고 같은 세션에서 앱을 직접 실행했을 때의 stderr 를 남긴다.
    console.command(
        "session-processes",
        "ps -eo pid,etime,rss,stat,args | grep -E 'labwc|control-center|greetd|tuigreet' | grep -v grep; true",
        cmd,
    )
    console.command(
        "session-journal",
        "sudo -n journalctl -b --no-pager -n 400 | grep -iE 'labwc|control-center|python|gtk|gdk|adw|egl|wlr|greetd|segfault|traceback' | tail -n 100; true",
        cmd,
    )
    console.command(
        "control-center-manual-run",
        "XDG_RUNTIME_DIR=/run/user/$(id -u) WAYLAND_DISPLAY=wayland-0 timeout 25 lightlix-control-center --wizard 2>&1 | head -n 60; true",
        cmd + 30,
    )
    console.command("memory-desktop-mode", "awk '/MemTotal|MemAvailable/' /proc/meminfo", cmd)
    console.command("ps-desktop", "ps -eo pid,rss,comm --sort=-rss | head -n 15", cmd)
    require(session["colors"] >= 8, f"The desktop session looks blank after {attempt} captures: {session}")

    # Control Center 가 데몬과 통신하는지: 프로세스가 살아 있고 오류 없이 상태를 받는다.
    console.command("daemon-status", "lightlix --json status | grep -q '\"mode\": \"desktop\"'", cmd)

    # 서버 모드로 돌아가면 데스크톱이 완전히 내려간다.
    back = console.command("mode-server", "lightlix --json mode server", budgets["START_TIMEOUT"])
    require(json_object(back, "mode-server").get("mode") == "server", "Mode switch did not report server")
    poll_command(console, "labwc-gone", "! pgrep -x labwc", budgets["START_TIMEOUT"])
    console.command("greetd-stopped", "! systemctl is-active --quiet greetd.service", cmd)
    console.command("default-target-server", "systemctl get-default | grep -x multi-user.target", cmd)
    time.sleep(5)
    console.command("memory-server-after", "awk '/MemTotal|MemAvailable/' /proc/meminfo", cmd)

    # 계정을 지우면 목록과 passwd 에서 사라지고, SSH 정책에서도 빠진다. 홈 디렉터리는 남는다.
    poll_command(console, "account-processes-gone", f"! pgrep -u {TEST_USER}", budgets["START_TIMEOUT"])
    removed = console.command("account-remove", f"lightlix --json user rm {TEST_USER}", cmd + 60)
    require(json_object(removed, "account-remove").get("home_kept") is True, "Account removal must keep the home directory")
    console.command("account-gone", f"! getent passwd {TEST_USER}", cmd)
    console.command("account-home-kept", f"sudo -n test -d /home/{TEST_USER}", cmd)
    console.command("ssh-policy-removed", "! sudo -n test -e /etc/ssh/sshd_config.d/50-lightlix.conf", cmd)


def main():
    sys.stdout.reconfigure(encoding="ascii", errors="backslashreplace")
    sys.stderr.reconfigure(encoding="ascii", errors="backslashreplace")
    parser = argparse.ArgumentParser(description="Lightlix UEFI desktop session E2E")
    parser.add_argument("iso", type=Path)
    args = parser.parse_args()
    log_root = Path(os.environ.get("E2E_LOG_DIR", "out/qemu-desktop"))
    os.umask(0o022)
    log_root.mkdir(parents=True, exist_ok=True)
    logs = log_root / f"run-{secrets.token_hex(4)}"
    logs.mkdir(mode=0o755)
    print(f"desktop-session: logs={logs}", flush=True)

    result = {"outcome": "failed", "kind": "internal", "message": "Driver did not finish"}
    console = None
    monitor = None
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
        if not shutil.which(qemu):
            raise Failure("usage", f"QEMU not found: {qemu}")
        code = base.firmware("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.fd")
        template = base.firmware("OVMF_VARS_TEMPLATE", "/usr/share/OVMF/OVMF_VARS_4M.fd")
        accel = os.environ.get("QEMU_ACCEL", "auto")
        kvm = os.access("/dev/kvm", os.R_OK | os.W_OK)
        if accel == "kvm" and not kvm:
            raise Failure("usage", "QEMU_ACCEL=kvm requires read/write access to /dev/kvm")
        chosen = "kvm" if accel != "tcg" and kvm else "tcg"
        slow = chosen == "tcg"
        budgets = {name: seconds(name, value) for name, value in {
            "BOOT_TIMEOUT": 1800 if slow else 240,
            "COMMAND_TIMEOUT": 180 if slow else 60,
            "START_TIMEOUT": 900 if slow else 240,
            "E2E_TIMEOUT": 5400 if slow else 1200,
        }.items()}
        with tempfile.TemporaryDirectory(prefix="lightlix-desktop-") as work:
            variables = Path(work) / "OVMF_VARS.fd"
            shutil.copyfile(template, variables)
            mon_path = str(Path(work) / "monitor.sock")
            argv = [qemu, "-name", "lightlix-desktop-e2e", "-machine", "q35", "-accel", chosen,
                    "-cpu", "host" if chosen == "kvm" else "max", "-smp", "2", "-m", "4096M",
                    "-drive", f"if=pflash,format=raw,unit=0,readonly=on,file={qemu_value(code)}",
                    "-drive", f"if=pflash,format=raw,unit=1,file={qemu_value(variables)}",
                    "-cdrom", str(args.iso), "-nic", "user,model=virtio-net-pci,ipv6=off",
                    "-vga", "virtio", "-display", "none",
                    "-monitor", f"unix:{qemu_value(mon_path)},server=on,wait=off",
                    "-serial", "stdio", "-no-reboot"]
            result.update({"accel": chosen, "budgets": budgets})
            print(f"desktop-session: accel={chosen} total_timeout={budgets['E2E_TIMEOUT']}s", flush=True)
            console = Console(argv, logs, start + budgets["E2E_TIMEOUT"])
            console.start()
            monitor = Monitor(mon_path)
            scenario(console, monitor, budgets, logs, work)
        result.update({"outcome": "succeeded", "kind": "pass", "message": "Desktop session started and stopped"})
        exit_code = 0
        print("PASS: server mode boot, desktop switch, greetd screen, labwc session, Control Center, back to server", flush=True)
    except Failure as error:
        result.update({"kind": error.kind, "message": str(error)})
        exit_code = 2 if error.kind == "usage" else 1
        if error.kind == "signal":
            exit_code = 128 + int(str(error).split()[-1])
        print(f"FAIL: {error.kind}: {error}", file=sys.stderr)
    except (OSError, ValueError, KeyError, TypeError) as error:
        result.update({"kind": "driver_error", "message": str(error)})
        print(f"FAIL: driver_error: {error}", file=sys.stderr)
    finally:
        signal.signal(signal.SIGTERM, signal.SIG_IGN)
        signal.signal(signal.SIGINT, signal.SIG_IGN)
        if monitor:
            monitor.close()
        if console:
            console.close()
        result["elapsed_seconds"] = round(time.monotonic() - start, 3)
        (logs / "result.json").write_text(json.dumps(result, indent=2) + "\n", encoding="ascii")
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
