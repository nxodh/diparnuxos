#!/usr/bin/env python3
"""UEFI 게스트의 직렬 콘솔을 제어하고 CLI JSON 으로 CPU 추론을 판정한다."""

import argparse
import codecs
from contextlib import ExitStack
import json
import os
from pathlib import Path
import re
import select
import secrets
import shlex
import shutil
import signal
import subprocess
import sys
import tempfile
import time


MODEL_REPO = "ggml-org/tiny-llamas"
MODEL_FILE = "stories260K.gguf"
# Hugging Face 파일 페이지와 기존 tests/llama/package.sh 의 고정 해시다.
MODEL_SHA256 = "047bf46455a544931cff6fef14d7910154c56afbc23ab1c5e56a72e69912c04b"
SOCKET = "/run/diparnux/diparnuxd.sock"
UNITS = (
    "diparnuxd.socket", "diparnuxd.service", "diparnux-llama.service",
    "diparnux-firewall-default.service", "diparnux-firewall.service",
    "diparnux-gpu-check.service",
)


class Failure(Exception):
    def __init__(self, kind, message):
        super().__init__(message)
        self.kind = kind


def require(condition, message):
    if not condition:
        raise Failure("assertion", message)


def seconds(name, default):
    value = os.environ.get(name, str(default))
    if not re.fullmatch(r"[1-9][0-9]*", value):
        raise Failure("usage", f"{name} must be a positive integer in seconds")
    return int(value)


def qemu_value(path):
    # QEMU 의 쉼표 구분 옵션은 파일명 안의 쉼표를 두 번 써야 한다.
    return str(path).replace(",", ",,")


class Console:
    def __init__(self, argv, logs, deadline):
        self.logs = logs
        self.deadline = deadline
        self.buffer = ""
        self.counter = 0
        self.serial = (logs / "serial.log").open("ab", buffering=0)
        self.stderr = (logs / "qemu.log").open("ab", buffering=0)
        self.commands = (logs / "commands.jsonl").open("a", encoding="ascii", buffering=1)
        self.proc = None
        self.argv = argv
        self.closed = False
        self.decoder = codecs.getincrementaldecoder("utf-8")(errors="replace")

    def start(self):
        with (self.logs / "qemu-command.jsonl").open("a", encoding="ascii") as output:
            output.write(json.dumps(self.argv) + "\n")
        self.proc = subprocess.Popen(
            self.argv, stdin=subprocess.PIPE, stdout=subprocess.PIPE,
            stderr=self.stderr, start_new_session=True,
        )

    def close(self):
        # 신호나 예외로 끝나도 QEMU 와 그 자식을 끝내고 회수한다.
        if self.closed:
            return
        self.closed = True
        if self.proc is not None:
            if self.proc.poll() is None:
                try:
                    os.killpg(self.proc.pid, signal.SIGTERM)
                except ProcessLookupError:
                    pass
                try:
                    self.proc.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    try:
                        os.killpg(self.proc.pid, signal.SIGKILL)
                    except ProcessLookupError:
                        pass
                    self.proc.wait(timeout=5)
            self.proc.stdin.close()
            self.proc.stdout.close()
        self.serial.close()
        self.stderr.close()
        self.commands.close()

    def wait(self, pattern, timeout, phase):
        end = min(time.monotonic() + timeout, self.deadline)
        while True:
            # 종료 직전 출력에 PASS 표시가 있어도 프로세스 종료를 성공으로 보지 않는다.
            rc = self.proc.poll()
            if rc is not None:
                raise Failure("qemu_exit", f"QEMU exited with code {rc} during {phase}")
            match = re.search(pattern, self.buffer, re.MULTILINE | re.DOTALL)
            if match:
                self.buffer = self.buffer[match.end():]
                return match
            remaining = end - time.monotonic()
            if remaining <= 0:
                kind = "total_timeout" if time.monotonic() >= self.deadline else "serial_timeout"
                raise Failure(kind, f"Timed out during {phase} after at most {timeout}s")
            ready, _, _ = select.select([self.proc.stdout], [], [], min(remaining, 0.25))
            if ready:
                data = os.read(self.proc.stdout.fileno(), 65536)
                if not data:
                    try:
                        rc = self.proc.wait(timeout=1)
                    except subprocess.TimeoutExpired:
                        raise Failure("serial_closed", f"Serial output closed during {phase}") from None
                    raise Failure("qemu_exit", f"QEMU exited with code {rc} during {phase}")
                self.serial.write(data)
                # 일반적인 직렬 tty 의 CRLF 만 정규화한다. 잘못된 JSON 은 숨기지 않는다.
                self.buffer += self.decoder.decode(data).replace("\r", "")

    def send(self, line):
        try:
            self.proc.stdin.write((line + "\n").encode("ascii"))
            self.proc.stdin.flush()
        except BrokenPipeError:
            raise Failure("qemu_exit", "QEMU closed serial input") from None

    def login(self, timeout):
        self.wait(r"diparnux login: *", timeout, "boot/login prompt")
        self.send("user")
        self.wait(r"Password: *", timeout, "login/password prompt")
        self.send("live")
        self.wait(r"(?:user@[^\n]*[$] |\n[$] )", timeout, "login/shell prompt")
        self.command(
            "session", "stty -echo; export LC_ALL=C TERM=dumb; unset PROMPT_COMMAND; PS1=''",
            min(timeout, 60),
        )

    def command(self, label, command, timeout, expected=0):
        self.counter += 1
        token = secrets.token_hex(8)
        stem = f"__DPX_{token}"
        begin, end = stem + "_BEGIN__", stem + "_END__"
        # 마커를 두 문자열로 만들어 tty 입력 에코가 완료 마커로 오인되지 않게 한다.
        line = (
            f"printf '\\n%s%s\\n' '__DPX_' '{token}_BEGIN__'; "
            f"LC_ALL=C TERM=dumb timeout --foreground --verbose --kill-after=5s {timeout}s sh -c {shlex.quote(command)}; "
            f"dpx_rc=$?; printf '\\n%s%s:%s\\n' '__DPX_' '{token}_END__' \"$dpx_rc\""
        )
        self.commands.write(json.dumps({"event": "start", "label": label, "command": command,
                                        "timeout_seconds": timeout, "expected_exit": expected}) + "\n")
        print(f"cpu-inference: step={label} timeout={timeout}s", flush=True)
        self.send(line)
        match = self.wait(
            rf"^{re.escape(begin)}\n(.*?)\n{re.escape(end)}:([0-9]+)\n",
            timeout + 15, label,
        )
        output, rc = match.group(1).strip(), int(match.group(2))
        (self.logs / f"{self.counter:02d}-{label}.txt").write_text(output + "\n", encoding="utf-8")
        self.commands.write(json.dumps({"event": "finish", "label": label, "exit_code": rc}) + "\n")
        if rc == 124 or (rc == 137 and "timeout: sending signal" in output):
            raise Failure("command_timeout", f"Guest command {label} timed out (exit {rc})")
        if rc != expected:
            raise Failure("command_exit", f"Guest command {label} exited {rc}; expected {expected}: {output[:800]}")
        return output

    def cli(self, label, args, timeout):
        output = self.command(label, "diparnux --json " + args, timeout)
        try:
            result = json.loads(output)
        except ValueError:
            raise Failure("invalid_json", f"CLI output for {label} is not a single JSON value") from None
        require(isinstance(result, dict), f"CLI output for {label} must be an object")
        return result


def unit_properties(console, label, unit, timeout):
    text = console.command(
        label, "systemctl show " + unit + " -p LoadState -p ActiveState -p SubState -p Result -p MainPID",
        timeout,
    )
    return dict(line.split("=", 1) for line in text.splitlines() if "=" in line)


def running_status(status, model):
    server = status.get("server", {})
    require(server.get("state") == "running_cpu_fallback", "Expected running_cpu_fallback")
    require(server.get("model_id") == model, "Running model differs from the default model")
    require(isinstance(server.get("pid"), int) and server["pid"] > 0, "Server PID is missing")
    require(server.get("host") == "127.0.0.1" and server.get("lan_exposed") is False,
            "Server must listen on loopback with LAN exposure disabled")
    require(not server.get("last_error"), "Running server has a last_error")
    require(server.get("plan", {}).get("cpu_only") is True, "Running plan is not CPU-only")
    require(server.get("plan", {}).get("ngl") == "0", "CPU plan must request NGL=0")
    require(server.get("offload", {}).get("offloaded") == 0, "Unexpected GPU offload in QEMU")
    require(bool(server.get("warnings")), "CPU fallback warning is missing")


def require_runtime(status, model, pid, expected_context, phase):
    running_status(status, model)
    server = status["server"]
    require(server.get("pid") == pid, f"{phase} changed the active llama-server PID")
    plan = server.get("plan", {})
    require(plan.get("ctx") == expected_context, f"{phase} changed the active context size")
    require(plan.get("batch") == 32, f"{phase} changed the active batch size")


def set_autostart(console, enabled, label, timeout):
    value = "on" if enabled else "off"
    result = console.cli(label, f"config set autostart {value}", timeout)
    require(result.get("config", {}).get("autostart") is enabled,
            f"Autostart setting did not change to {value}")
    require(result.get("restart_required") is False, "Autostart change unexpectedly requires a server restart")


def status_poll_command():
    return (
        "attempt=0; while [ \"$attempt\" -lt 10 ]; do "
        "if status=$(diparnux --json status 2>&1); then printf '%s\\n' \"$status\"; exit 0; "
        "else rc=$?; case \"$status\" in "
        "*\"diparnuxd is not running (systemctl status diparnuxd)\"*) ;; "
        "*) printf '%s\\n' \"$status\" >&2; exit \"$rc\";; esac; fi; "
        "attempt=$((attempt + 1)); sleep 1; done; "
        "printf '%s\\n' 'diparnux status remained unavailable after daemon restart' >&2; exit 1"
    )


def wait_for_status(console, label, timeout):
    # 데몬 재시작 직후 소켓 연결 오류만 다시 시도하고 나머지 오류는 바로 돌려준다.
    output = console.command(label, status_poll_command(), timeout)
    try:
        status = json.loads(output)
    except ValueError:
        raise Failure("invalid_json", f"CLI output for {label} is not a single JSON value") from None
    require(isinstance(status, dict), f"CLI output for {label} must be an object")
    return status


def scenario(console, budgets):
    cmd = budgets["COMMAND_TIMEOUT"]
    console.login(budgets["BOOT_TIMEOUT"])
    identity = console.command(
        "identity", "id -u && id -un && id -Gn && test -d /sys/firmware/efi && uname -m", cmd
    )
    lines = identity.splitlines()
    require(len(lines) == 4 and lines[0].isdigit() and int(lines[0]) != 0 and lines[1] == "user",
            "Serial session must run as the unprivileged live user")
    require("diparnux" in lines[2].split() and lines[3] == "x86_64", "Live user group or amd64 UEFI check failed")
    permission = console.command("socket-permissions", f"test -S {SOCKET} && stat -c '%a %U %G' {SOCKET}", cmd)
    require(permission == "660 root diparnux", "Daemon socket must be root:diparnux mode 0660")

    initial = console.cli("status-initial", "status", cmd)
    require(initial.get("gpu", {}).get("status", {}).get("state") == "no_gpu", "Expected no_gpu diagnostic in QEMU")
    require(initial.get("server", {}).get("state") == "stopped", "Fresh ISO server must start stopped")
    require(not initial["server"].get("last_error"), "Fresh ISO contains a server error")
    # 유휴 RSS 목표 판정에 쓸 실제 데몬 값을 로그에 남긴다. GPU 실기 값과 구분한다.
    console.command("daemon-memory", "pid=$(systemctl show diparnuxd.service -p MainPID --value); "
                    "test \"$pid\" -gt 0 && grep -E '^(Name|VmRSS|VmSize):' /proc/\"$pid\"/status", cmd)
    for unit in UNITS:
        props = unit_properties(console, "unit-initial-" + unit.split(".")[0], unit, cmd)
        require(props.get("LoadState") == "loaded" and props.get("ActiveState") != "failed",
                f"Unit {unit} is missing or failed")
        if unit in ("diparnuxd.socket", "diparnuxd.service", "diparnux-firewall-default.service", "diparnux-gpu-check.service"):
            require(props.get("ActiveState") == "active", f"Unit {unit} is not active")
        if unit == "diparnux-llama.service":
            require(props.get("ActiveState") == "inactive", "llama unit unexpectedly started on a fresh ISO")
        # 저장된 규칙 파일이 없는 첫 부팅에서는 saved firewall 유닛이 조건 때문에 inactive 이다.
        require(props.get("Result", "success") == "success", f"Unit {unit} has an error result")

    # root 사용은 게스트 안의 읽기 전용 진단과 nobody 전환뿐이다. 정상 CLI 흐름은 user 가 실행한다.
    denied = console.command("socket-denied", "sudo -n -u nobody env DIPARNUX_SOCKET=" + SOCKET + " diparnux --json status", cmd, expected=3)
    require("permission denied" in denied, "A user outside diparnux must be denied socket access")
    unavailable = console.command("socket-unavailable", "DIPARNUX_SOCKET=/run/diparnux/e2e-missing.sock diparnux --json status", cmd, expected=3)
    require("not running" in unavailable, "Missing socket must report daemon unavailability")
    missing = console.command("missing-model", "diparnux --json serve start e2e-missing.gguf", cmd, expected=1)
    require("(no_model)" in missing, "Missing model error code was not reported")
    stopped_chat = console.command("chat-before-start", "diparnux --json chat --max-tokens 1 hello", cmd, expected=1)
    require("(server_not_running)" in stopped_chat, "Stopped server must reject chat")

    console.command(
        "disk-memory-before-pull",
        "df -B1 -P /; grep -E '^(MemTotal|MemFree|MemAvailable|SwapTotal|SwapFree):' /proc/meminfo; "
        "findmnt -no SOURCE,FSTYPE,OPTIONS /",
        cmd,
    )
    pull = console.cli("model-pull", f"model pull {MODEL_REPO} --file {MODEL_FILE}", budgets["PULL_TIMEOUT"])
    require(pull.get("state") == "done" and pull.get("model_id") == MODEL_FILE, "HF download job did not finish")
    require(pull.get("bytes_done", 0) > 0 and pull.get("bytes_done") == pull.get("bytes_total"), "HF download size is incomplete")
    models = console.cli("model-list", "model list", cmd)
    matches = [model for model in models.get("models", []) if model.get("id") == MODEL_FILE]
    require(len(matches) == 1, "Downloaded model is absent or duplicated")
    model = matches[0]
    require(model.get("sha256") == MODEL_SHA256 and model.get("repo") == MODEL_REPO,
            "Downloaded model metadata SHA-256 or repository mismatch")
    require(model.get("incomplete") is False and model.get("size_bytes", 0) > 0, "Downloaded GGUF is incomplete")
    digest = console.command("model-sha256", f"sudo -n sha256sum /var/lib/diparnux/models/{MODEL_FILE}", cmd)
    require(bool(digest.split()) and digest.split()[0] == MODEL_SHA256,
            "Actual GGUF bytes do not match the pinned SHA-256")
    default = console.cli("model-default", "model default " + MODEL_FILE, cmd)
    require(default.get("default") == MODEL_FILE, "Default model was not saved")
    default_list = console.cli("model-default-list", "model list", cmd)
    require(default_list.get("default") == MODEL_FILE, "Default model was not persisted")

    plan = console.cli("serve-plan", "serve plan --ctx 256 --batch 32", cmd)
    require(plan.get("model_id") == MODEL_FILE, "Plan did not use the default model")
    require(plan.get("plan", {}).get("cpu_only") is True and plan["plan"].get("ngl") == "0",
            "Auto plan must select CPU fallback without a manual NGL override")
    require(bool(plan["plan"].get("warnings")), "CPU plan must explain fallback")
    planned_context = plan["plan"].get("ctx")
    require(isinstance(planned_context, int) and not isinstance(planned_context, bool) and planned_context > 0,
            "Serve plan did not report a valid positive context size")
    require(plan["plan"].get("batch") == 32, "Serve plan did not preserve requested batch size 32")
    started = console.cli("serve-start", "serve start --ctx 256 --batch 32", budgets["START_TIMEOUT"])
    require_runtime(started, MODEL_FILE, started.get("server", {}).get("pid"), planned_context, "Serve start")
    initial_context = started["server"]["plan"]["ctx"]
    status = console.cli("status-running", "status", cmd)
    require_runtime(status, MODEL_FILE, started["server"]["pid"], initial_context, "Initial status")
    props = unit_properties(console, "unit-running-llama", "diparnux-llama.service", cmd)
    require(props.get("ActiveState") == "active" and props.get("SubState") == "running" and props.get("Result") == "success",
            "llama systemd service did not start cleanly")
    require(props.get("MainPID") == str(status["server"]["pid"]), "Daemon PID differs from systemd MainPID")
    chat = console.cli("chat", "chat --max-tokens 16 'Once upon a time'", budgets["CHAT_TIMEOUT"])
    require(isinstance(chat.get("content"), str) and bool(chat["content"].strip()), "Chat generated no text")
    require(isinstance(chat.get("tokens_predicted"), int) and chat["tokens_predicted"] > 0, "Chat token count must be positive")
    require(isinstance(chat.get("tokens_per_second"), (int, float)) and chat["tokens_per_second"] > 0,
            "Chat generation speed must be positive")

    for enabled, setting in ((False, "off"), (True, "on")):
        set_autostart(console, enabled, "autostart-" + setting, cmd)
        console.command("daemon-restart-autostart-" + setting,
                        "sudo -n systemctl restart diparnuxd.service", budgets["START_TIMEOUT"])
        recovered = wait_for_status(console, "status-after-daemon-restart-" + setting,
                                    budgets["START_TIMEOUT"])
        require_runtime(recovered, MODEL_FILE, started["server"]["pid"], initial_context,
                        "Daemon restart with autostart " + setting)
        props = unit_properties(console, "unit-after-daemon-restart-" + setting,
                                "diparnux-llama.service", cmd)
        require(props.get("ActiveState") == "active" and props.get("SubState") == "running" and
                props.get("MainPID") == str(started["server"]["pid"]),
                f"llama-server was not preserved after autostart {setting} daemon restart")
        restart_chat = console.cli("chat-after-daemon-restart-" + setting,
                                   "chat --max-tokens 4 'daemon restart check'", budgets["CHAT_TIMEOUT"])
        require(isinstance(restart_chat.get("content"), str) and bool(restart_chat["content"].strip()),
                f"Chat after autostart {setting} daemon restart generated no text")
        require(isinstance(restart_chat.get("tokens_predicted"), int) and restart_chat["tokens_predicted"] > 0,
                f"Chat after autostart {setting} daemon restart returned no token count")
        require(isinstance(restart_chat.get("tokens_per_second"), (int, float)) and
                restart_chat["tokens_per_second"] > 0,
                f"Chat after autostart {setting} daemon restart returned no generation speed")

    check_gateway(console, budgets)

    console.cli("serve-logs", "serve logs -n 80", cmd)
    stop = console.cli("serve-stop", "serve stop", cmd)
    require(stop.get("state") == "stopped", "Stop did not return stopped")
    final = console.cli("status-stopped", "status", cmd)
    require(final.get("server", {}).get("state") == "stopped" and final["server"].get("pid") == 0,
            "Daemon did not clear the stopped server PID")
    require(not final["server"].get("last_error"), "Clean stop left a server error")
    props = unit_properties(console, "unit-stopped-llama", "diparnux-llama.service", cmd)
    require(props.get("ActiveState") == "inactive" and props.get("MainPID") == "0" and props.get("Result") == "success",
            "llama systemd service did not stop cleanly")
    after = console.command("chat-after-stop", "diparnux --json chat --max-tokens 1 hello", cmd, expected=1)
    require("(server_not_running)" in after, "Stopped server accepted chat")
    console.command("journal", "sudo -n journalctl --no-pager -b -n 160 " + " ".join("-u " + unit for unit in UNITS), cmd)


# 라이브 이미지에는 curl 이 없으므로 python3(Control Center 가 들여온다)로 HTTP 상태 코드만 읽는다. 직렬 콘솔로 보내므로 한 줄이어야 한다.
HTTP_STATUS = (
    "python3 -I -c 'import sys,http.client as h,urllib.parse as u; p=u.urlparse(sys.argv[1]); "
    "c=h.HTTPConnection(p.netloc,timeout=30); "
    "c.request(\"GET\",p.path or \"/\",headers=dict(x.split(\":\",1) for x in sys.argv[2:])); "
    "print(c.getresponse().status)'"
)


def http_status(console, label, url, timeout, *headers):
    out = console.command(label, HTTP_STATUS + " " + shlex.quote(url) + "".join(" " + shlex.quote(h) for h in headers),
                          timeout)
    require(out.strip().isdigit(), f"{label} did not print an HTTP status: {out!r}")
    return int(out.strip())


def check_gateway(console, budgets):
    """공개 포트는 게이트웨이가 받고 llama-server 는 내부 포트에서만 듣는다. 키는 게이트웨이가 검사한다."""
    cmd = budgets["COMMAND_TIMEOUT"]
    conn = console.cli("connection", "connection", cmd)
    require(conn.get("server_state") == "running_cpu_fallback", "Connection info reports the wrong server state")
    require(conn.get("gateway") is True and conn.get("public_port") == 8080, "Gateway must be on with public port 8080")
    require(conn.get("local_url") == "http://127.0.0.1:8080/v1", "Connection info local URL is wrong")
    require(conn.get("key_count") == 0 and conn.get("key_required") is False,
            "A fresh image has no key and no LAN exposure, so no key is required")
    require(conn.get("addresses") == [], "LAN exposure is off, so no remote address may be advertised")
    gw = console.cli("gateway", "gateway", cmd)
    state = gw.get("state", {})
    require(gw.get("enabled") is True and state.get("listening") is True, "Gateway is not listening")
    require(state.get("backend_port") == 18080, "Gateway must forward to the internal port 18080")
    ports = console.command("listening-ports", "ss -H -ltn 'sport = :8080 or sport = :18080'", cmd)
    require("127.0.0.1:18080" in ports, "llama-server must listen on the internal port on 127.0.0.1")
    require("8080" in ports.replace("18080", ""), "The gateway must listen on the public port")

    base = "http://127.0.0.1:8080"
    require(http_status(console, "gateway-health", base + "/health", cmd) == 200, "/health must answer without a key")
    require(http_status(console, "gateway-models-no-key", base + "/v1/models", cmd) == 200,
            "Without any API key and LAN exposure, local requests need no key")

    created = console.cli("gateway-key-create", "apikey create e2e-gateway", cmd)
    key = created.get("key", "")
    require(key.startswith("dpx_") and created.get("restart_required") is False,
            "A new key must take effect without a restart when the gateway checks keys")
    time.sleep(3)  # 게이트웨이의 키 캐시는 2초다.
    require(http_status(console, "gateway-models-missing-key", base + "/v1/models", cmd) == 401,
            "Once a key exists, a request without a key must be refused")
    require(http_status(console, "gateway-models-wrong-key", base + "/v1/models", cmd,
                        "Authorization: Bearer dpx_not-a-real-key") == 401, "A wrong key must be refused")
    require(http_status(console, "gateway-models-bearer", base + "/v1/models", cmd,
                        "Authorization: Bearer " + key) == 200, "A valid Bearer key must be accepted")
    require(http_status(console, "gateway-models-x-api-key", base + "/v1/models", cmd, "X-Api-Key: " + key) == 200,
            "A valid X-Api-Key must be accepted")
    require(http_status(console, "gateway-health-no-key", base + "/health", cmd) == 200,
            "/health must stay open when keys exist")
    # 데몬 자신의 채팅 경로는 소켓 권한으로 보호되므로 키가 있어도 그대로 동작해야 한다.
    still = console.cli("chat-with-key", "chat --max-tokens 4 'gateway check'", budgets["CHAT_TIMEOUT"])
    require(isinstance(still.get("content"), str) and bool(still["content"].strip()), "Chat broke after a key was created")
    usage = console.cli("gateway-usage", "gateway", cmd)
    mine = [k for k in usage.get("stats", {}).get("keys", []) if k.get("label") == "e2e-gateway"]
    require(len(mine) == 1 and mine[0].get("requests", 0) >= 2, "Gateway did not count the key's requests")
    require(usage["stats"].get("unauthorized", 0) >= 2, "Gateway did not count the refused requests")
    require(key not in json.dumps(usage) and key not in json.dumps(conn), "A plaintext key appeared in an API answer")
    removed = console.cli("gateway-key-rm", "apikey rm " + created["id"], cmd)
    require(removed.get("deleted") is True, "The test key was not removed")


def firmware(name, default):
    path = Path(os.environ.get(name, default))
    if not path.is_file():
        raise Failure("usage", f"Firmware not found: {path}; set {name}")
    return path


def main():
    # 호스트 화면에는 ASCII 만 쓰고, 직렬 원문과 명령 결과 파일은 UTF-8 로 보존한다.
    sys.stdout.reconfigure(encoding="ascii", errors="backslashreplace")
    sys.stderr.reconfigure(encoding="ascii", errors="backslashreplace")
    parser = argparse.ArgumentParser(description="Diparnux UEFI CPU inference E2E")
    parser.add_argument("iso", type=Path)
    args = parser.parse_args()
    logs = Path(os.environ.get("E2E_LOG_DIR", "out/qemu-e2e"))
    # 제한된 umask를 쓰는 CI root 컨테이너에서도 새 결과 파일을 runner가 읽게 한다.
    # 새 실행의 권한만 지정하고 기존 실행 로그는 건드리지 않는다.
    os.umask(0o022)
    logs.mkdir(parents=True, exist_ok=True)
    log_root = logs
    while True:
        logs = log_root / f"run-{secrets.token_hex(4)}"
        try:
            logs.mkdir(mode=0o755)
            break
        except FileExistsError:
            continue
    print(f"cpu-inference: logs={logs}", flush=True)
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
        if not shutil.which(qemu):
            raise Failure("usage", f"QEMU not found: {qemu}")
        code = firmware("OVMF_CODE", "/usr/share/OVMF/OVMF_CODE_4M.fd")
        template = firmware("OVMF_VARS_TEMPLATE", "/usr/share/OVMF/OVMF_VARS_4M.fd")
        accel = os.environ.get("QEMU_ACCEL", "auto")
        if accel not in ("auto", "kvm", "tcg"):
            raise Failure("usage", "QEMU_ACCEL must be auto, kvm, or tcg")
        kvm = os.access("/dev/kvm", os.R_OK | os.W_OK)
        if accel == "kvm" and not kvm:
            raise Failure("usage", "QEMU_ACCEL=kvm requires read/write access to /dev/kvm")
        chosen = "kvm" if accel != "tcg" and kvm else "tcg"
        with tempfile.TemporaryDirectory(prefix="diparnux-e2e-") as work, ExitStack() as cleanups:
            variables = Path(work) / "OVMF_VARS.fd"
            shutil.copyfile(template, variables)
            while True:
                slow = chosen == "tcg"
                budgets = {name: seconds(name, value) for name, value in {
                    "BOOT_TIMEOUT": 1800 if slow else 180,
                    "COMMAND_TIMEOUT": 180 if slow else 60,
                    "PULL_TIMEOUT": 1800 if slow else 600,
                    "START_TIMEOUT": 900 if slow else 180,
                    "CHAT_TIMEOUT": 300 if slow else 180,
                    "E2E_TIMEOUT": 5400 if slow else 1800,
                }.items()}
                argv = [qemu, "-name", "diparnux-cpu-e2e", "-machine", "q35", "-accel", chosen,
                        "-cpu", "host" if chosen == "kvm" else "max", "-smp", "2", "-m", "4096M",
                        "-drive", f"if=pflash,format=raw,unit=0,readonly=on,file={qemu_value(code)}",
                        "-drive", f"if=pflash,format=raw,unit=1,file={qemu_value(variables)}",
                        "-cdrom", str(args.iso), "-nic", "user,model=virtio-net-pci,ipv6=off",
                        "-display", "none", "-monitor", "none", "-serial", "stdio", "-no-reboot"]
                result.update({"accel": chosen, "budgets": budgets})
                print(f"cpu-inference: accel={chosen} total_timeout={budgets['E2E_TIMEOUT']}s", flush=True)
                console = Console(argv, logs, start + budgets["E2E_TIMEOUT"])
                # 임시 펌웨어 디렉터리를 지우기 전에 QEMU 를 끝낸다.
                cleanups.callback(console.close)
                console.start()
                try:
                    scenario(console, budgets)
                except Failure as error:
                    # KVM 초기화 실패만 자동 TCG 재시도한다. 게스트 실패는 숨기지 않는다.
                    diagnostic = (logs / "qemu.log").read_text(errors="replace")
                    if (accel == "auto" and chosen == "kvm" and error.kind == "qemu_exit"
                            and not (logs / "serial.log").stat().st_size
                            and re.search(r"failed to initialize kvm|Could not access KVM|KVM is not supported", diagnostic, re.I)):
                        console.close()
                        console = None
                        chosen = "tcg"
                        shutil.copyfile(template, variables)
                        print("cpu-inference: KVM initialization failed; retrying with TCG", flush=True)
                        continue
                    raise
                break
        result.update({"outcome": "succeeded", "kind": "pass", "message": "Real CPU inference completed"})
        exit_code = 0
        print("PASS: UEFI login, HF SHA-256, CPU fallback, generated text, and clean stop", flush=True)
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
        if console:
            console.close()
        result["elapsed_seconds"] = round(time.monotonic() - start, 3)
        (logs / "result.json").write_text(json.dumps(result, indent=2) + "\n", encoding="ascii")
    return exit_code


if __name__ == "__main__":
    sys.exit(main())
