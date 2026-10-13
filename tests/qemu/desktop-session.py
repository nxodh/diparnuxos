#!/usr/bin/env python3
"""UEFI 게스트에서 데스크톱 모드 전환, lightdm 자동 로그인과 로그인 화면, Xfce 세션, Control Center 를 검사한다.

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
TEST_PASSWORD = "diparnuxtest1"

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
    # 같은 화면인지 비교하려고 화소 전체의 해시를 함께 남긴다(캡처가 이전 프레임 그대로인지 가린다).
    digest = zlib.crc32(bytes(pixels)) & 0xFFFFFFFF
    return {"width": width, "height": height, "colors": len(colors), "lit_fraction": round(lit / max(samples, 1), 4),
            "digest": f"{digest:08x}"}


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


def greeter_diagnostics(console, cmd, tag):
    """로그인 화면 문제를 가릴 정보를 남긴다. 실패해도 시험을 끝내지 않는다."""
    console.command(
        f"greeter-diagnostics-{tag}",
        "ps -eo pid,user,etime,args | grep -E 'Xorg|lightdm|greeter|xfce4-session' | grep -v grep; "
        "echo '== conf.d'; sudo -n sh -c 'for f in /etc/lightdm/lightdm.conf.d/* /usr/share/lightdm/lightdm.conf.d/*; do echo \"-- $f\"; cat \"$f\"; done'; "
        "echo '== lightdm.log'; sudo -n tail -n 70 /var/log/lightdm/lightdm.log; "
        "echo '== x-0.log (EE/WW)'; sudo -n grep -E '\\(EE\\)|\\(WW\\)' /var/log/lightdm/x-0.log | tail -n 25; "
        "echo '== greeter log'; sudo -n sh -c 'tail -n 40 /var/log/lightdm/seat0-greeter.log 2>&1'; "
        "echo '== journal'; sudo -n journalctl -b -u lightdm.service --no-pager | tail -n 25; true",
        cmd + 20,
    )


def scenario(console, monitor, budgets, logs, workdir):
    cmd = budgets["COMMAND_TIMEOUT"]
    console.login(budgets["BOOT_TIMEOUT"])
    monitor.connect()

    identity = console.command("identity", "id -un && id -Gn && uname -m", cmd).splitlines()
    require(identity[0] == "user" and "diparnux" in identity[1].split(), "Serial session must be the live user in group diparnux")

    # 이미지는 서버 모드로 부팅하고 데스크톱 구성 요소를 갖는다.
    mode = console.command("mode-initial", "diparnux --json mode", cmd)
    require(json_object(mode, "mode-initial").get("mode") == "server", "The image must boot in server mode")
    console.command("lightdm-installed", "systemctl cat lightdm.service >/dev/null", cmd)
    unit = console.command("lightdm-unit", "systemctl cat lightdm.service", cmd)
    require("lightdm" in unit, "lightdm unit was not found")
    console.command("lightdm-not-active", "! systemctl is-active --quiet lightdm.service", cmd)
    console.command("no-x-server-in-server-mode", "! pgrep -x Xorg && ! pgrep -x lightdm", cmd)
    console.command(
        "desktop-files",
        "test -x /usr/bin/xfce4-session && test -x /usr/bin/xfwm4 && test -x /usr/sbin/lightdm "
        "&& test -x /usr/bin/diparnux-control-center && test -s /etc/xdg/diparnux/xfce4/panel/default.xml",
        cmd,
    )
    console.command("fonts-korean", "fc-list :lang=ko family | head -n 3 | grep .", cmd)
    # 옛 이름의 흔적: 설치된 파일 이름, 유닛, 계정, 환경에 없어야 한다(패턴을 두 조각으로 나누어 이 파일이 걸리지 않게 한다).
    console.command(
        "legacy-name-absent",
        "p=light''lix; "
        "! find /etc /usr/bin /usr/sbin /usr/lib /usr/share /var/lib /opt -iname \"*$p*\" 2>/dev/null | grep -q . "
        "&& ! systemctl list-unit-files --no-pager | grep -qi \"$p\" "
        "&& ! getent group | grep -qi \"$p\" "
        "&& ! grep -qi \"$p\" /etc/os-release /etc/hostname /etc/issue",
        cmd,
    )

    # 사용자가 정한 이름과 비밀번호로 계정을 만든다. 비밀번호는 표준 입력으로만 넘긴다.
    created = console.command(
        "account-create",
        f"printf '%s\\n' '{TEST_PASSWORD}' | diparnux --json user add {TEST_USER} --admin --password-stdin",
        cmd + 30,
    )
    require(json_object(created, "account-create").get("name") == TEST_USER, "The account was not created")
    require(TEST_PASSWORD not in created, "The password must not appear in the command output")
    listed = console.command("account-list", "diparnux --json user list", cmd)
    require(TEST_USER in json.dumps(json_object(listed, "account-list")), "The account is missing from the list")
    console.command("account-passwd-entry", f"getent passwd {TEST_USER} | grep -q ':/home/{TEST_USER}:/bin/bash$'", cmd)
    console.command("account-shadow-hash", f"sudo -n getent shadow {TEST_USER} | cut -d: -f2 | grep -q '^[$]'", cmd)
    console.command("account-groups", f"id -nG {TEST_USER} | tr ' ' '\\n' | grep -cxE 'diparnux|video|render|sudo' | grep -qx 4", cmd)
    console.command("account-home", f"sudo -n test -d /home/{TEST_USER}", cmd)
    # 같은 이름은 다시 만들 수 없고, 약한 비밀번호는 거절된다.
    console.command(
        "account-duplicate",
        f"printf '%s\\n' '{TEST_PASSWORD}' | diparnux --json user add {TEST_USER} --password-stdin",
        cmd + 30, expected=1,
    )
    console.command(
        "account-weak-password",
        "printf '%s\\n' 'short' | diparnux --json user add weakone --password-stdin",
        cmd + 30, expected=1,
    )

    # SSH: 켜면 이 계정만 비밀번호로 접속할 수 있고 기본 live 계정은 거절된다.
    ssh_state = console.command("ssh-on", "diparnux --json ssh on", cmd + 60)
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

    # 데스크톱 모드로 전환한다. 라이브 세션은 로그인 화면 없이 라이브 사용자로 자동 로그인한다.
    switched = console.command("mode-desktop", "diparnux --json mode desktop", budgets["START_TIMEOUT"])
    require(json_object(switched, "mode-desktop").get("mode") == "desktop", "Mode switch did not report desktop")
    poll_command(console, "lightdm-active", "systemctl is-active --quiet lightdm.service", budgets["START_TIMEOUT"])
    console.command("default-target-desktop", "systemctl get-default | grep -x graphical.target", cmd)
    poll_command(console, "autologin-session", "pgrep -u user -x xfce4-session", budgets["START_TIMEOUT"])
    poll_command(console, "autologin-panel", "pgrep -u user -x xfce4-panel", budgets["START_TIMEOUT"])
    time.sleep(8)
    auto = capture(console, monitor, logs, "autologin", workdir)
    require(auto["width"] >= SCREEN_SIZE_MIN[0] and auto["height"] >= SCREEN_SIZE_MIN[1], "Screen is too small")
    require(auto["colors"] >= 3 and auto["lit_fraction"] > 0.0005, f"The autologin desktop looks blank: {auto}")
    # 라이브 자동 로그인은 live-config 가 부팅 때 /etc/lightdm/lightdm.conf 에 쓴다.
    console.command("autologin-config", "grep -x 'autologin-user=user' /etc/lightdm/lightdm.conf", cmd)
    back = console.command("mode-server-after-autologin", "diparnux --json mode server", budgets["START_TIMEOUT"])
    require(json_object(back, "mode-server-after-autologin").get("mode") == "server", "Mode switch did not report server")
    poll_command(console, "autologin-session-gone", "! pgrep -x xfce4-session", budgets["START_TIMEOUT"])

    # 로그인 화면: 자동 로그인을 끄고 직접 만든 계정으로 로그인한다. 사용자 이름을 직접 입력하는 방식으로 바꿔 둔다.
    console.command(
        "disable-autologin",
        "sudo -n sed -i -E 's/^(autologin-user(-timeout)?=.*)$/#\\1/' /etc/lightdm/lightdm.conf",
        cmd,
    )
    console.command(
        "greeter-manual-login",
        "sudo -n mkdir -p /etc/lightdm/lightdm.conf.d && printf '[Seat:*]\\ngreeter-hide-users=true\\ngreeter-show-manual-login=true\\n' "
        "| sudo -n tee /etc/lightdm/lightdm.conf.d/70-diparnux-test.conf >/dev/null",
        cmd,
    )
    switched = console.command("mode-desktop-greeter", "diparnux --json mode desktop", budgets["START_TIMEOUT"])
    require(json_object(switched, "mode-desktop-greeter").get("mode") == "desktop", "Mode switch did not report desktop")
    poll_command(console, "lightdm-active-greeter", "systemctl is-active --quiet lightdm.service", budgets["START_TIMEOUT"])
    poll_command(console, "greeter-running", "pgrep -f /usr/sbin/lightdm-gtk-greeter", budgets["START_TIMEOUT"])
    console.command("no-autologin-config", "! grep -E '^autologin-user=' /etc/lightdm/lightdm.conf", cmd)
    console.command("no-session-before-login", "! pgrep -x xfce4-session", cmd)
    # 이전 세션의 마지막 프레임이 화면에 남아 있을 수 있으므로, 프레임이 바뀔 때까지 기다린다.
    greeter = None
    deadline = time.monotonic() + budgets["START_TIMEOUT"] / 2
    while True:
        time.sleep(5)
        greeter = capture(console, monitor, logs, "greeter", workdir)
        if greeter["digest"] != auto["digest"] or time.monotonic() > deadline:
            break
    greeter_diagnostics(console, cmd, "before-login")
    require(greeter["digest"] != auto["digest"], f"The login screen never replaced the previous frame: {greeter}")
    require(greeter["width"] >= SCREEN_SIZE_MIN[0] and greeter["height"] >= SCREEN_SIZE_MIN[1], "Screen is too small")
    require(greeter["colors"] >= 3 and greeter["lit_fraction"] > 0.0005,
            f"The login screen looks blank: {greeter}")

    # 키 입력으로 로그인해 Xfce 세션과 Control Center 를 확인한다.
    # 방금 만든 계정으로 로그인한다(기본 live 계정이 아니다).
    monitor.type_text(TEST_USER + "\n")
    time.sleep(2)
    monitor.type_text(TEST_PASSWORD + "\n")
    try:
        poll_command(console, "xfce-session-running", f"pgrep -u {TEST_USER} -x xfce4-session", budgets["START_TIMEOUT"])
    except Failure:
        # 로그인에 실패하면 화면과 로그를 남기고 같은 오류로 끝낸다.
        capture(console, monitor, logs, "after-login-failure", workdir)
        greeter_diagnostics(console, cmd, "after-login-failure")
        raise
    for name in ("xfwm4", "xfce4-panel", "xfdesktop", "xfsettingsd"):
        poll_command(console, f"{name}-running", f"pgrep -u {TEST_USER} -x {name}", budgets["START_TIMEOUT"])
    console.command("xorg-running", "pgrep -x Xorg", cmd)
    console.command(
        "session-env",
        f"sudo -n sh -c 'pid=$(pgrep -u {TEST_USER} -x xfce4-session | head -n 1); tr \"\\0\" \"\\n\" < /proc/$pid/environ "
        "| grep -E \"^(XDG_SESSION_TYPE|XDG_CURRENT_DESKTOP|XDG_CONFIG_DIRS|DISPLAY)=\"'",
        cmd,
    )
    console.command(
        "session-env-check",
        f"sudo -n sh -c 'pid=$(pgrep -u {TEST_USER} -x xfce4-session | head -n 1); tr \"\\0\" \"\\n\" < /proc/$pid/environ "
        "| grep -x \"XDG_CURRENT_DESKTOP=XFCE\" && tr \"\\0\" \"\\n\" < /proc/$pid/environ | grep -q \"^XDG_CONFIG_DIRS=/etc/xdg/diparnux:\"'",
        cmd,
    )
    poll_command(console, "control-center-running", f"pgrep -u {TEST_USER} -f diparnux-control-center", budgets["START_TIMEOUT"])

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
        "ps -eo pid,etime,rss,stat,args | grep -E 'Xorg|xfce|xfwm|xfdesktop|control-center|lightdm' | grep -v grep; true",
        cmd,
    )
    console.command(
        "session-journal",
        "sudo -n journalctl -b --no-pager -n 400 | grep -iE 'lightdm|xfce|xfwm|control-center|python|gtk|gdk|adw|segfault|traceback' | tail -n 100; true",
        cmd,
    )
    console.command("lightdm-log", "sudo -n sh -c 'tail -n 40 /var/log/lightdm/lightdm.log /var/log/lightdm/x-0.log 2>&1'; true", cmd)
    console.command(
        "control-center-manual-run",
        f"sudo -n -u {TEST_USER} env DISPLAY=:0 XAUTHORITY=/home/{TEST_USER}/.Xauthority timeout 25 diparnux-control-center --wizard 2>&1 | head -n 60; true",
        cmd + 30,
    )
    console.command("memory-desktop-mode", "awk '/MemTotal|MemAvailable/' /proc/meminfo", cmd)
    console.command("ps-desktop", "ps -eo pid,rss,comm --sort=-rss | head -n 15", cmd)
    # 패널과 첫 로그인 설정: 진단을 남긴 뒤 판정한다.
    console.command(
        "first-login-setup",
        f"sudo -n sh -c 'ls -la /home/{TEST_USER}/.config/diparnux /home/{TEST_USER}/.config/xfce4 2>&1 | head -n 20'; "
        f"sudo -n test -e /home/{TEST_USER}/.config/diparnux/session-setup-done",
        cmd,
    )
    console.command("panel-network-text", "diparnux-panel-net | grep -E '^<txt>.+</txt>$'", cmd)
    require(session["colors"] >= 8, f"The desktop session looks blank after {attempt} captures: {session}")

    # Control Center 가 데몬과 통신하는지: 프로세스가 살아 있고 오류 없이 상태를 받는다.
    console.command("daemon-status", "diparnux --json status | grep -q '\"mode\": \"desktop\"'", cmd)

    # 그래픽 설치기(Calamares)를 같은 Xfce 세션에서 띄워 창이 그려지는지만 본다. 설치는 하지 않는다.
    console.command(
        "installer-files",
        "test -x /usr/bin/diparnux-installer && test -x /usr/bin/calamares && test -s /etc/calamares/settings.conf "
        "&& test -s /etc/calamares/branding/diparnux/branding.desc && test -x /usr/sbin/diparnux-install",
        cmd,
    )
    uid = console.command("tester-uid", f"id -u {TEST_USER}", cmd).strip().splitlines()[-1]
    console.command(
        "installer-start",
        f"sudo -n sh -c 'XDG_RUNTIME_DIR=/run/user/{uid} DISPLAY=:0 XAUTHORITY=/home/{TEST_USER}/.Xauthority "
        "nohup diparnux-installer -d > /tmp/installer.log 2>&1 &'; sleep 2",
        cmd,
    )
    poll_command(console, "installer-running", "pgrep -x calamares", budgets["START_TIMEOUT"])
    time.sleep(20)
    # 사라졌다면 원인을 가릴 수 있게 설치기 출력과 저널을 오류 메시지에 담는다.
    console.command(
        "installer-still-running",
        "pgrep -x calamares || { echo '-- installer.log'; sudo -n tail -n 80 /tmp/installer.log; "
        "echo '-- journal'; sudo -n journalctl -b --no-pager | tail -n 40; echo '-- dmesg'; sudo -n dmesg | tail -n 20; "
        "echo '-- calamares logs'; sudo -n sh -c 'ls -la /root/.cache/calamares /root/.cache/Calamares 2>&1 | head; "
        "tail -n 40 /root/.cache/calamares/session.log 2>&1'; exit 1; }",
        cmd,
    )
    # 창이 뜬 것만으로는 부족하다. 파티션 모듈이 보조 프로그램(KPMCore, polkit 로 권한 확인)을 띄워 장치를 읽어야
    # "Waiting for N modules" 상태를 벗어난다. 보조 프로그램이 뜨지 않으면 진단 정보를 남기고 실패한다.
    console.command("installer-polkit", "dpkg-query -W -f '${db:Status-Status}\\n' polkitd | grep -x installed", cmd)
    console.command(
        "installer-diagnostics",
        "ps -eo pid,etime,args | grep -E 'calamares|kpmcore|polkit' | grep -v grep; "
        "sudo -n sh -c 'ls -la /root/.cache 2>/dev/null; for f in /root/.cache/calamares/session.log /root/.cache/Calamares/Calamares/Calamares.log; do "
        "[ -r $f ] && tail -n 60 $f; done'; true",
        cmd,
    )
    poll_command(console, "installer-backend", "pgrep -f kpmcore_externalcommand", budgets["START_TIMEOUT"])
    console.command(
        "installer-log",
        "sudo -n tail -n 60 /tmp/installer.log; "
        "! sudo -n grep -qiE 'could not load the qt platform plugin|failed to create wl_display|cannot connect to' /tmp/installer.log",
        cmd,
    )
    installer = capture(console, monitor, logs, "installer", workdir)
    require(installer["colors"] >= 8, f"The installer window looks blank: {installer}")
    console.command("installer-stop", "sudo -n pkill -x calamares; sleep 2; ! pgrep -x calamares", cmd)

    # 램 증가 시험: 세션이 떠 있는 채로 8분 동안 1분마다 주요 프로세스의 RSS 와 MemAvailable 을 잰다.
    # Control Center 가 3초마다 상태를 읽는 동안 위젯이나 연결이 새면 여기서 드러난다.
    soak_script = (
        "rss() { pid=$(pgrep -f -o \"$1\" | head -n 1); "
        "[ -n \"$pid\" ] && awk '/VmRSS/ {print $2}' /proc/$pid/status || echo 0; }; "
        "for i in 0 1 2 3 4 5 6 7; do "
        "echo \"sample=$i diparnuxd=$(rss /usr/sbin/diparnuxd) cc=$(rss diparnux-control-center) xorg=$(rss Xorg) "
        "xfwm4=$(rss xfwm4) panel=$(rss xfce4-panel) desktop=$(rss xfdesktop) avail=$(awk '/MemAvailable/ {print $2}' /proc/meminfo)\"; "
        "[ $i -lt 7 ] && sleep 60; done; true"
    )
    soak = console.command("soak", soak_script, 8 * 60 + 90)
    samples = []
    for line in soak.splitlines():
        if line.startswith("sample="):
            samples.append({k: int(v) for k, v in (kv.split("=") for kv in line.split())})
    require(len(samples) == 8, f"Expected 8 memory samples, got {len(samples)}: {soak[-400:]}")
    # 첫 표본은 시작 직후의 값이라 워밍업으로 보고 두 번째를 기준으로 삼는다.
    base, last = samples[1], samples[-1]
    # Xfce 구성 요소의 상한은 첫 측정 전의 넉넉한 값이다. 실측을 보고 조인다(램 목표가 아니라 누수 검사다).
    limits_kb = {"diparnuxd": 6 * 1024, "cc": 24 * 1024, "xorg": 48 * 1024, "xfwm4": 16 * 1024, "panel": 32 * 1024, "desktop": 32 * 1024}
    for key, limit in limits_kb.items():
        growth = last[key] - base[key]
        print(f"desktop-session: soak {key}: {base[key]} kB -> {last[key]} kB (+{growth} kB, limit {limit} kB)", flush=True)
        require(growth <= limit, f"{key} grew by {growth} kB in 6 minutes (limit {limit} kB)")
    avail_drop = base["avail"] - last["avail"]
    print(f"desktop-session: soak MemAvailable dropped {avail_drop} kB", flush=True)
    require(avail_drop <= 80 * 1024, f"MemAvailable dropped by {avail_drop} kB in 6 minutes")

    # 브라우저 실험: 이미지에 들어 있는 firefox-esr 이 이 세션에서 창을 여는지 본다(0.4.0 에서 사용자가 브라우저가 안 뜬다고 보고했다).
    # 실패해도 시험 전체를 실패시키지 않고 결과를 기록한다.
    try:
        console.command("browser-installed", "command -v firefox-esr", cmd)
        console.command(
            "browser-window-start",
            f"sudo -n sh -c 'HOME=/home/{TEST_USER} XDG_RUNTIME_DIR=/run/user/{uid} DISPLAY=:0 XAUTHORITY=/home/{TEST_USER}/.Xauthority "
            f"nohup setpriv --reuid={TEST_USER} --regid={TEST_USER} --init-groups firefox-esr --no-remote about:blank "
            f"> /tmp/browser.log 2>&1 &'; sleep 40; pgrep -u {TEST_USER} -f firefox | head -n 3",
            cmd + 90,
        )
        browser = capture(console, monitor, logs, "browser", workdir)
        print(f"desktop-session: browser capture {browser}", flush=True)
        console.command("browser-log", "tail -n 30 /tmp/browser.log; true", cmd)
        console.command("browser-stop", "sudo -n pkill -f firefox; true", cmd)
    except Failure as error:
        print(f"desktop-session: browser experiment did not complete: {error.kind}: {str(error)[:300]}", flush=True)

    # 서버 모드로 돌아가면 데스크톱이 완전히 내려간다.
    back = console.command("mode-server", "diparnux --json mode server", budgets["START_TIMEOUT"])
    require(json_object(back, "mode-server").get("mode") == "server", "Mode switch did not report server")
    poll_command(console, "xfce-session-gone", "! pgrep -x xfce4-session", budgets["START_TIMEOUT"])
    console.command("lightdm-stopped", "! systemctl is-active --quiet lightdm.service", cmd)
    console.command("xorg-gone", "! pgrep -x Xorg", cmd)
    console.command("default-target-server", "systemctl get-default | grep -x multi-user.target", cmd)
    time.sleep(5)
    console.command("memory-server-after", "awk '/MemTotal|MemAvailable/' /proc/meminfo", cmd)

    # 계정을 지우면 목록과 passwd 에서 사라지고, SSH 정책에서도 빠진다. 홈 디렉터리는 남는다.
    poll_command(console, "account-processes-gone", f"! pgrep -u {TEST_USER}", budgets["START_TIMEOUT"])
    removed = console.command("account-remove", f"diparnux --json user rm {TEST_USER}", cmd + 60)
    require(json_object(removed, "account-remove").get("home_kept") is True, "Account removal must keep the home directory")
    console.command("account-gone", f"! getent passwd {TEST_USER}", cmd)
    console.command("account-home-kept", f"sudo -n test -d /home/{TEST_USER}", cmd)
    console.command("ssh-policy-removed", "! sudo -n test -e /etc/ssh/sshd_config.d/50-diparnux.conf", cmd)


COLLECT_SCRIPT = r"""#!/bin/sh
echo "## meminfo"
grep -E '^(MemTotal|MemFree|MemAvailable|Buffers|Cached|Shmem|AnonPages|Slab|SReclaimable|SUnreclaim|KernelStack|PageTables|Mapped):' /proc/meminfo
echo "## pss"
for d in /proc/[0-9]*; do
  [ -r "$d/smaps_rollup" ] || continue
  pss=$(awk '/^Pss:/{print $2}' "$d/smaps_rollup" 2>/dev/null)
  [ -n "$pss" ] || continue
  rss=$(awk '/^Rss:/{print $2}' "$d/smaps_rollup" 2>/dev/null)
  printf '%s %s %s %s\n' "$pss" "$rss" "${d#/proc/}" "$(cat "$d/comm" 2>/dev/null)"
done
echo "## services"
systemctl list-units --type=service --state=running --no-legend --no-pager | awk '{print $1}'
echo "## df"
df -k / /run /tmp 2>/dev/null
echo "## zram"
cat /sys/block/zram0/mm_stat 2>/dev/null || true
echo "## end"
"""


def parse_profile(text):
    """collect 스크립트의 출력을 읽어 메모리 현황 사전으로 만든다(KiB)."""
    sections, current = {}, None
    for line in text.splitlines():
        if line.startswith("## "):
            current = line[3:].strip()
            sections[current] = []
        elif current:
            sections[current].append(line)
    meminfo = {}
    for line in sections.get("meminfo", []):
        m = re.match(r"(\w+):\s+(\d+) kB", line)
        if m:
            meminfo[m.group(1)] = int(m.group(2))
    by_comm = {}
    total_pss = 0
    for line in sections.get("pss", []):
        parts = line.split(None, 3)
        if len(parts) < 4 or not parts[0].isdigit():
            continue
        pss, rss, _pid, comm = int(parts[0]), int(parts[1]), parts[2], parts[3].strip()
        entry = by_comm.setdefault(comm, {"pss": 0, "rss": 0, "count": 0})
        entry["pss"] += pss
        entry["rss"] += rss
        entry["count"] += 1
        total_pss += pss
    used = meminfo.get("MemTotal", 0) - meminfo.get("MemAvailable", 0)
    top = sorted(by_comm.items(), key=lambda kv: -kv[1]["pss"])
    return {
        "meminfo_kib": meminfo,
        "used_kib": used,
        "userland_pss_kib": total_pss,
        "other_kib": used - total_pss,
        "services": [x.strip() for x in sections.get("services", []) if x.strip()],
        "processes": [{"comm": k, **v} for k, v in top],
    }


def profile_scenario(console, monitor, budgets, logs, workdir):
    """서버 모드와 데스크톱 모드(자동 로그인)의 메모리 사용을 프로세스별 PSS 로 기록한다. 램 목표의 판정이 아니라 측정이다."""
    import base64

    cmd = budgets["COMMAND_TIMEOUT"]
    console.login(budgets["BOOT_TIMEOUT"])
    monitor.connect()
    encoded = base64.b64encode(COLLECT_SCRIPT.encode()).decode()
    console.command("install-collector", f"printf '%s' '{encoded}' | base64 -d > /tmp/collect.sh && chmod +x /tmp/collect.sh", cmd)
    result = {}

    def collect(label):
        text = console.command(f"collect-{label}", "sudo -n sh /tmp/collect.sh", cmd + 60)
        info = parse_profile(text)
        result[label] = info
        top = ", ".join(f"{p['comm']}={p['pss'] // 1024}M" for p in info["processes"][:8])
        print(f"profile: {label}: used={info['used_kib'] // 1024} MiB userland_pss={info['userland_pss_kib'] // 1024} MiB "
              f"other={info['other_kib'] // 1024} MiB top: {top}", flush=True)
        return info

    # 서버 모드: 로그인 직후의 일시적 값을 피하려고 잠시 기다린다.
    time.sleep(90)
    console.command("boot-time", "systemd-analyze 2>&1 | head -n 3; systemd-analyze blame --no-pager 2>&1 | head -n 15; true", cmd)
    collect("server")

    # 데스크톱 모드: 자동 로그인 세션이 안정될 때까지 기다린 뒤 잰다. 첫 설정 마법사(Control Center)가 켜진 채로 한 번 잰다.
    console.command("mode-desktop", "diparnux --json mode desktop", budgets["START_TIMEOUT"])
    poll_command(console, "session", "pgrep -u user -x xfce4-session", budgets["START_TIMEOUT"])
    poll_command(console, "panel", "pgrep -u user -x xfce4-panel", budgets["START_TIMEOUT"])
    time.sleep(90)
    collect("desktop-with-control-center")
    capture(console, monitor, logs, "profile-desktop", workdir)

    # Control Center 를 끄면 Xfce 세션 자체의 비용이 드러난다.
    console.command("stop-control-center", "pkill -u user -f diparnux-control-center; sleep 5; true", cmd)
    time.sleep(30)
    collect("desktop-session-only")

    out = logs / "memory-profile.json"
    out.write_text(json.dumps(result, indent=1) + "\n", encoding="ascii")
    lines = ["# Memory profile (QEMU, live session, KiB values shown in MiB)", ""]
    for label, info in result.items():
        lines.append(f"## {label}: used {info['used_kib'] // 1024} MiB (userland PSS {info['userland_pss_kib'] // 1024} MiB, "
                     f"kernel/tmpfs/other {info['other_kib'] // 1024} MiB)")
        mi = info["meminfo_kib"]
        lines.append(f"MemTotal {mi.get('MemTotal', 0) // 1024}, MemAvailable {mi.get('MemAvailable', 0) // 1024}, "
                     f"Shmem {mi.get('Shmem', 0) // 1024}, Cached {mi.get('Cached', 0) // 1024}, "
                     f"Slab {mi.get('Slab', 0) // 1024}, AnonPages {mi.get('AnonPages', 0) // 1024}")
        lines.append("")
        lines.append("| process | PSS MiB | RSS MiB | count |")
        lines.append("|---|---|---|---|")
        for p in info["processes"][:25]:
            lines.append(f"| {p['comm']} | {p['pss'] / 1024:.1f} | {p['rss'] / 1024:.1f} | {p['count']} |")
        lines.append("")
        lines.append("running services: " + ", ".join(info["services"]))
        lines.append("")
    (logs / "memory-profile.md").write_text("\n".join(lines) + "\n", encoding="ascii")
    print(f"desktop-session: memory profile written to {out}", flush=True)


def main():
    sys.stdout.reconfigure(encoding="ascii", errors="backslashreplace")
    sys.stderr.reconfigure(encoding="ascii", errors="backslashreplace")
    parser = argparse.ArgumentParser(description="Diparnux UEFI desktop session E2E")
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
        with tempfile.TemporaryDirectory(prefix="diparnux-desktop-") as work:
            variables = Path(work) / "OVMF_VARS.fd"
            shutil.copyfile(template, variables)
            mon_path = str(Path(work) / "monitor.sock")
            argv = [qemu, "-name", "diparnux-desktop-e2e", "-machine", "q35", "-accel", chosen,
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
            run = profile_scenario if os.environ.get("E2E_SCENARIO") == "memory-profile" else scenario
            run(console, monitor, budgets, logs, work)
        result.update({"outcome": "succeeded", "kind": "pass", "message": "Desktop session started and stopped"})
        exit_code = 0
        print("PASS: server mode boot, autologin, lightdm greeter, Xfce session, Control Center, back to server", flush=True)
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
