#!/usr/bin/env python3
"""드라이버 회귀 검사 전용 가짜 직렬 장치다. ISO 나 실제 추론을 실행하지 않는다."""

import json
import os
from pathlib import Path
import re
import shlex
import sys
import time


mode = os.environ.get("FAKE_MODE", "pass")
sha = "047bf46455a544931cff6fef14d7910154c56afbc23ab1c5e56a72e69912c04b"
model = "stories260K.gguf"
repo = "ggml-org/tiny-llamas"
pidfile = Path(os.environ["FAKE_PIDFILE"])
Path(os.environ["FAKE_ARGV"]).write_text(json.dumps(sys.argv[1:]))
# 신호 테스트의 준비 표시는 인자 파일 기록이 끝난 뒤에만 만든다.
pidfile.write_text(str(os.getpid()))
stopped = True
default = ""


def emit(text):
    # 실제 tty 처럼 CRLF 를 보내되 일부를 여러 read 로 쪼갠다.
    data = text.replace("\n", "\r\n")
    midpoint = len(data) // 2
    sys.stdout.write(data[:midpoint])
    sys.stdout.flush()
    sys.stdout.write(data[midpoint:])
    sys.stdout.flush()


def status():
    return {"gpu": {"status": {"state": "no_gpu"}}, "server": {
        "state": "stopped" if stopped else "running_cpu_fallback", "pid": 0 if stopped else 42,
        "model_id": model, "host": "127.0.0.1", "lan_exposed": False,
        "plan": {"cpu_only": True, "ngl": "0"}, "offload": {"offloaded": 0},
        "warnings": ["No GPU; using CPU"],
    }}


def result(command):
    global stopped, default
    if command.startswith("stty "):
        return "", 0
    if command.startswith("id -u"):
        return "1000\nuser\nuser lightlix\nx86_64", 0
    if command.startswith("test -S"):
        return ("666 root root" if mode == "bad-permissions" else "660 root lightlix"), 0
    if command.startswith("pid=$(systemctl show lightlixd.service"):
        return "Name:\tlightlixd\nVmSize:\t1200000 kB\nVmRSS:\t14000 kB", 0
    if command.startswith("systemctl show"):
        unit = command.split()[2]
        active = "inactive" if unit == "lightlix-firewall.service" or (unit == "lightlix-llama.service" and stopped) else "active"
        sub = "running" if unit == "lightlix-llama.service" and not stopped else "dead"
        pid = "42" if unit == "lightlix-llama.service" and not stopped else "0"
        if mode == "failed-unit" and unit == "lightlixd.service":
            active = "failed"
        return f"LoadState=loaded\nActiveState={active}\nSubState={sub}\nResult=success\nMainPID={pid}", 0
    if command.startswith("sudo -n -u nobody"):
        return "lightlix: permission denied: add your user to the 'lightlix' group or run with sudo", 3
    if command.startswith("LIGHTLIX_SOCKET="):
        return "lightlix: lightlixd is not running (systemctl status lightlixd)", 3
    if command.startswith("sudo -n sha256sum"):
        return ("0" * 64 if mode == "bad-sha" else sha) + "  /var/lib/lightlix/models/" + model, 0
    if command.startswith("sudo -n journalctl"):
        return "Lightlix test journal", 0
    args = command.removeprefix("lightlix --json ")
    if args == "status":
        return json.dumps(status()), 0
    if args == "serve start e2e-missing.gguf":
        return "lightlix: model was not found (no_model)", 1
    if args.startswith("model pull "):
        if mode == "guest-timeout":
            return "", 124
        if mode == "command-exit":
            return "Download failed", 9
        if mode == "guest-killed":
            return "Killed", 137
        return json.dumps({"state": "done", "model_id": model, "bytes_done": 1185376, "bytes_total": 1185376}), 0
    if args == "model list":
        return json.dumps({"default": default, "models": [{"id": model, "sha256": sha,
                          "repo": repo, "size_bytes": 1185376, "incomplete": False}]}), 0
    if args.startswith("model default "):
        default = model
        return json.dumps({"default": default}), 0
    if args.startswith("serve plan "):
        return json.dumps({"model_id": model, "plan": {"ngl": "0", "cpu_only": True, "warnings": ["CPU fallback"]}}), 0
    if args.startswith("serve start "):
        stopped = False
        return json.dumps(status()), 0
    if args.startswith("chat "):
        if stopped:
            return "lightlix: the server is not running (server_not_running)", 1
        if mode == "bad-json":
            return "not JSON", 0
        return json.dumps({"content": " " if mode == "empty-chat" else "Once there was a little dog.",
                           "tokens_predicted": 16, "tokens_per_second": 12.5}), 0
    if args.startswith("serve logs "):
        return json.dumps({"lines": 1, "log": "CPU backend ready"}), 0
    if args == "serve stop":
        stopped = True
        return json.dumps({"state": "stopped"}), 0
    return "Unexpected fixture command: " + command, 97


if mode == "qemu-exit":
    print("Fixture QEMU startup failure", file=sys.stderr)
    sys.exit(7)
if mode == "kvm-fallback" and sys.argv[sys.argv.index("-accel") + 1] == "kvm":
    print("failed to initialize kvm: Permission denied", file=sys.stderr)
    sys.exit(7)
if mode in ("boot-timeout", "total-timeout"):
    time.sleep(60)
if mode == "signal":
    emit("lightlix login: ")
    time.sleep(60)
emit("Lightlix GPU [no_gpu]: No GPU found\nlightlix login: ")
if sys.stdin.readline().strip() != "user":
    sys.exit(98)
emit("Password: ")
if sys.stdin.readline().strip() != "live":
    sys.exit(99)
emit("\nuser@lightlix:~$ ")
for line in sys.stdin:
    # 입력 에코도 일부러 보내 마커 파서가 명령 텍스트를 응답으로 보지 않는지 확인한다.
    emit(line)
    token = re.search(r"'([0-9a-f]+)_BEGIN__'", line).group(1)
    parts = shlex.split(line)
    command = parts[parts.index("-c") + 1].removesuffix(";")
    if mode == "serial-timeout" and command.startswith("lightlix --json model pull"):
        time.sleep(60)
    output, rc = result(command)
    emit(f"\n__LLX_{token}_BEGIN__\n{output}\n__LLX_{token}_END__:{rc}\n")
