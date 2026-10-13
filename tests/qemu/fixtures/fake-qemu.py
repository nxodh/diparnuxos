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
autostart = True
daemon_restarts = 0
FAKE_KEY = "dpx_" + "f" * 40
key_created = False
gateway_requests = 0
gateway_refused = 0
fw_rule = False
ssh_key = False
webadmin_password = False
webadmin_on = False
vpn_on = False
vpn_peers = 0


def emit(text):
    # 실제 tty 처럼 CRLF 를 보내되 일부를 여러 read 로 쪼갠다.
    data = text.replace("\n", "\r\n")
    midpoint = len(data) // 2
    sys.stdout.write(data[:midpoint])
    sys.stdout.flush()
    sys.stdout.write(data[midpoint:])
    sys.stdout.flush()


def status():
    pid = 0 if stopped else (99 if mode == "replaced-pid" and daemon_restarts else 42)
    trained_context = mode == "trained-context-128"
    wrong_start_context = mode == "wrong-start-context" and not stopped and daemon_restarts == 0
    changed_context = (
        (mode == "lost-runtime-plan-off" and daemon_restarts == 1)
        or (mode == "lost-runtime-plan-on" and daemon_restarts == 2)
    )
    if changed_context:
        ctx, batch = 512, 64
    elif trained_context or wrong_start_context:
        ctx, batch = 128, 32
    else:
        ctx, batch = 256, 32
    return {"gpu": {"status": {"state": "no_gpu"}}, "server": {
        "state": "stopped" if stopped else "running_cpu_fallback", "pid": pid,
        "model_id": model, "host": "127.0.0.1", "lan_exposed": False,
        "plan": {"cpu_only": True, "ngl": "0", "ctx": ctx, "batch": batch},
        "offload": {"offloaded": 0},
        "warnings": ["No GPU; using CPU"],
    }}


def result(command):
    global autostart, daemon_restarts, stopped, default, key_created, gateway_requests, gateway_refused
    global fw_rule, ssh_key, webadmin_password, webadmin_on, vpn_on, vpn_peers
    if command.startswith("stty "):
        return "", 0
    if command.startswith("attempt=0; while [ \"$attempt\" -lt 10 ]; do"):
        return json.dumps(status()), 0
    if command.startswith("id -u"):
        return "1000\nuser\nuser diparnux\nx86_64", 1 if mode == "efi-check-failure" else 0
    if command.startswith("df -B1 -P /"):
        return (
            "Filesystem 1-blocks Used Available Capacity Mounted on\n"
            "overlay 4294967296 524288000 3770679296 13% /\n"
            "MemTotal:        4026532 kB\n"
            "MemFree:         3900000 kB\n"
            "MemAvailable:    3800000 kB\n"
            "SwapTotal:             0 kB\n"
            "SwapFree:              0 kB\n"
            "overlay overlay rw,relatime\n"
        ), 0
    if command.startswith("test -S"):
        return ("666 root root" if mode == "bad-permissions" else "660 root diparnux"), 0
    if command.startswith("pid=$(systemctl show diparnuxd.service"):
        return "Name:\tdiparnuxd\nVmSize:\t1200000 kB\nVmRSS:\t14000 kB", 0
    if command.startswith("systemctl show"):
        unit = command.split()[2]
        active = "inactive" if unit == "diparnux-firewall.service" or (unit == "diparnux-llama.service" and stopped) else "active"
        sub = "running" if unit == "diparnux-llama.service" and not stopped else "dead"
        pid = "42" if unit == "diparnux-llama.service" and not stopped else "0"
        if mode == "failed-unit" and unit == "diparnuxd.service":
            active = "failed"
        return f"LoadState=loaded\nActiveState={active}\nSubState={sub}\nResult=success\nMainPID={pid}", 0
    if command == "sudo -n systemctl restart diparnuxd.service":
        daemon_restarts += 1
        if mode == "no-adoption" and daemon_restarts == 1:
            stopped = True
        return "", 0
    if command.startswith("sudo -n -u nobody"):
        return "diparnux: permission denied: add your user to the 'diparnux' group or run with sudo", 3
    if command.startswith("DIPARNUX_SOCKET="):
        return "diparnux: diparnuxd is not running (systemctl status diparnuxd)", 3
    if command.startswith("sudo -n sha256sum"):
        return ("0" * 64 if mode == "bad-sha" else sha) + "  /var/lib/diparnux/models/" + model, 0
    if command.startswith("sudo -n journalctl"):
        return "Diparnux test journal", 0
    if command.startswith("sudo -n nft list table inet diparnux"):
        return "table inet diparnux {\n\tchain input {\n\t\tiif lo accept\n" + (
            "\t\tip saddr { 10.0.0.0/8 } tcp dport 39999 accept\n" if fw_rule else "") + "\t}\n}", 0
    if command.startswith("ss -H -ltn 'sport = :18443'"):
        return ("LISTEN 0 4096 *:18443 *:*" if webadmin_on else ""), 0
    if command.startswith("systemctl is-enabled xrdp.service"):
        return "disabled", 0
    if command.startswith("rm -f /tmp/e2e_key /tmp/e2e_key.pub; ssh-keygen"):
        return "", 0
    if command.startswith("printf '%s\\n' 'e2e-correct-horse-battery' | diparnux --json webadmin password"):
        webadmin_password = True
        return json.dumps({"password_set": True}), 0
    if command.startswith("python3 -I -c 'import json,http.client as h; c=h.HTTPConnection(\"127.0.0.1\",8080,timeout=120)"):
        return json.dumps({
            "props": [200, 512, 1], "models": [200, [model], "0.5.3"],
            "info": [200, {"diparnux": "0.5.3", "server": "running", "model": model, "ctx_per_slot": 512, "slots": 1}],
            "sse": [200, "text/event-stream", 6, True, True], "tools": 200}), 0
    if command.startswith("python3 -I -c 'import json,http.client as h; c=h.HTTPConnection(\"127.0.0.1\",8080,timeout=30)"):
        return json.dumps([401, "application/json", "an API key is required"]), 0
    if command == "diparnux connect vedecaps --mcp":
        return json.dumps({
            "provider": {"diparnux": {"name": "Diparnux", "protocol": "openai", "api": "http://127.0.0.1:8080/v1",
                                      "models": {model: {"name": model, "limit": {"context": 512, "output": 128}}}}},
            "model": "diparnux/" + model,
            "mcp": {"diparnux": {"type": "local", "command": ["diparnux", "mcp"], "enabled": True}}}, indent=2), 0
    if command.startswith("printf '%s\\n' '{\"jsonrpc\""):
        return "\n".join([
            json.dumps({"jsonrpc": "2.0", "id": 1, "result": {"protocolVersion": "2025-06-18"}}),
            json.dumps({"jsonrpc": "2.0", "id": 2, "result": {"tools": [{"name": "diparnux_status"}, {"name": "diparnux_connection"}]}})]), 0
    if command == "diparnux --json serve toolcheck":
        return json.dumps({"ok": False, "code": "no_tool_call", "message": "the model answered in text",
                           "finish_reason": "stop", "latency_ms": 900}), 1
    if command.startswith("python3 -I -c 'import ssl,json,http.client"):
        return "401 200 200 403", 0
    if command.startswith("diparnux vpn peer add e2e-peer --out"):
        vpn_peers += 1
        return "peer e2e-peer added with address 10.8.0.2/32\nclient configuration written to /tmp/e2e-peer.conf", 0
    if command.startswith("stat -c %a /tmp/e2e-peer.conf"):
        return "600", 0
    if command.startswith("for i in 1 2 3 4 5 6 7 8; do ip -br link show wg0"):
        return "wg0 UNKNOWN 10.8.0.1/24", 0
    if command.startswith("rm -f /tmp/e2e-peer.conf"):
        return "", 0
    if command.startswith("ss -H -ltn"):
        return "LISTEN 0 4096 127.0.0.1:8080 0.0.0.0:*\nLISTEN 0 4096 127.0.0.1:18080 0.0.0.0:*", 0
    if command.startswith("python3 -I -c "):
        # 게이트웨이 상태 코드 확인: python3 -I -c '<script>' URL [헤더...]
        words = shlex.split(command)
        url, headers = words[4], words[5:]
        if url.endswith("/health"):
            return "200", 0
        if not key_created:
            gateway_requests += 1
            return "200", 0
        if any(h in ("Authorization: Bearer " + FAKE_KEY, "X-Api-Key: " + FAKE_KEY) for h in headers):
            gateway_requests += 1
            return "200", 0
        gateway_refused += 1
        return "401", 0
    args = command.removeprefix("diparnux --json ")
    if args.startswith("config set autostart "):
        autostart = args.rsplit(" ", 1)[1] == "on"
        return json.dumps({"config": {"autostart": autostart}, "restart_required": False}), 0
    if args == "status":
        return json.dumps(status()), 0
    if args == "firewall":
        return json.dumps({"policy": "drop", "builtin": [], "rules": ([{"id": "r1", "name": "e2e-rule", "protocol": "tcp", "port": 39999, "source": "lan", "enabled": True}] if fw_rule else []), "max_rule": 64}), 0
    if args.startswith("firewall add e2e-rule "):
        fw_rule = True
        return json.dumps({"id": "r1", "name": "e2e-rule", "protocol": "tcp", "port": 39999, "source": "lan", "enabled": True}), 0
    if args.startswith("firewall rm "):
        fw_rule = False
        return json.dumps({"id": "r1", "deleted": True}), 0
    if args == "services":
        return json.dumps({"services": [
            {"unit": "diparnuxd.service", "title": "Diparnux daemon", "installed": True, "active": "active", "actions": []},
            {"unit": "ssh.service", "title": "SSH server", "installed": True, "active": "inactive", "actions": ["start", "stop", "restart"]}]}), 0
    if args == "services stop diparnuxd.service":
        return "diparnux: Diparnux daemon cannot be stopped from here (action_not_allowed)", 1
    if args == "services stop cron.service":
        return "diparnux: this service is not managed here (service_not_allowed)", 1
    if args == "resources":
        return json.dumps({"system": {"memory": {"total_mib": 3900, "available_mib": 3300, "used_mib": 600}, "cpu": {"cores": 2}}, "gpus": []}), 0
    if args.startswith("exposure"):
        return json.dumps({"level": "ok", "findings": [{"level": "ok", "title": "The model server is reachable only from this computer", "detail": ""}]}), 0
    if args == "network config":
        return json.dumps({"hostname": "diparnux", "wake_on_lan": False, "pending": None, "interfaces": []}), 0
    if args.startswith("network diagnose"):
        return json.dumps({"checks": [{"name": "address", "ok": True, "detail": "enp0s3 10.0.2.15/24"}]}), 0
    if args == "remote-desktop":
        return json.dumps({"installed": True, "enabled": False, "active": False, "port": 3389, "source": "lan"}), 0
    if args == "ssh advanced":
        return json.dumps({"enabled": False, "port": 22, "key_count": 1 if ssh_key else 0, "host_keys": []}), 0
    if args.startswith("ssh keys add user"):
        ssh_key = True
        return json.dumps({"key": {"type": "ssh-ed25519", "fingerprint": "SHA256:e2e"}, "added": True}), 0
    if args == "ssh keys list user":
        return json.dumps({"user": "user", "keys": ([{"type": "ssh-ed25519", "fingerprint": "SHA256:e2e", "comment": "e2e@test"}] if ssh_key else [])}), 0
    if args.startswith("ssh keys rm user "):
        ssh_key = False
        return json.dumps({"deleted": True}), 0
    if args.startswith("webadmin on"):
        if not webadmin_password:
            return "diparnux: set the web admin password first (password_required)", 1
        webadmin_on = True
        return json.dumps({"enabled": True, "running": True, "port": 18443, "source": "lan", "password_set": True, "cert_fingerprint": "AA:BB"}), 0
    if args == "webadmin off":
        webadmin_on = False
        return json.dumps({"enabled": False, "running": False, "port": 18443, "source": "lan", "password_set": True}), 0
    if args == "vpn on":
        vpn_on = True
        return json.dumps({"enabled": True, "interface": "wg0", "address": "10.8.0.1/24", "listen_port": 51820, "public_key": "A" * 43 + "=", "peers": []}), 0
    if args == "vpn":
        return json.dumps({"enabled": vpn_on, "interface": "wg0", "peers": [{"name": "e2e-peer"}] * vpn_peers}), 0
    if args == "vpn off":
        vpn_on = False
        return json.dumps({"enabled": False, "peers": []}), 0
    if args == "connection":
        return json.dumps({"server_state": "stopped" if stopped else "running_cpu_fallback", "model_id": model,
                           "gateway": True, "public_port": 8080, "lan_exposed": False,
                           "key_count": 1 if key_created else 0, "key_required": key_created,
                           "local_url": "http://127.0.0.1:8080/v1", "addresses": [], "warnings": [], "examples": {}}), 0
    if args == "gateway":
        keys = [{"id": "k1", "label": "e2e-gateway", "prefix": "dpx_ffff", "requests": gateway_requests}] if key_created else []
        return json.dumps({"enabled": True, "public_port": 8080, "state": {"listening": True, "addr": "127.0.0.1:8080", "backend_port": 18080},
                           "stats": {"requests": gateway_requests, "unauthorized": gateway_refused, "keys": keys}}), 0
    if args == "apikey create e2e-gateway":
        key_created = True
        return json.dumps({"id": "k1", "label": "e2e-gateway", "prefix": "dpx_ffff", "key": FAKE_KEY, "restart_required": False}), 0
    if args == "apikey rm k1":
        key_created = False
        return json.dumps({"id": "k1", "deleted": True, "restart_required": False}), 0
    if args == "serve start e2e-missing.gguf":
        return "diparnux: model was not found (no_model)", 1
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
                          "repo": repo, "size_bytes": 1185376, "incomplete": False,
                          **({"context_length": 128} if mode == "trained-context-128" else {})}]}), 0
    if args.startswith("model default "):
        default = model
        return json.dumps({"default": default}), 0
    if args.startswith("serve plan "):
        trained_context = mode == "trained-context-128"
        ctx = 128 if trained_context else 256
        warnings = ["CPU fallback"]
        rationale = ""
        if trained_context:
            warnings.append("ctx override 256 ignored: allowed range is 256-128")
            rationale = "CPU plan uses the model training context of 128 tokens"
        return json.dumps({"model_id": model, "plan": {"ngl": "0", "ctx": ctx, "batch": 32,
                          "cpu_only": True, "warnings": warnings, "rationale": rationale},
                          "input": {"ctx_train": 128 if trained_context else 2048}}), 0
    if args.startswith("serve start "):
        stopped = False
        return json.dumps(status()), 0
    if args.startswith("chat "):
        if stopped:
            return "diparnux: the server is not running (server_not_running)", 1
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
    emit("diparnux login: ")
    time.sleep(60)
emit("Diparnux GPU [no_gpu]: No GPU found\ndiparnux login: ")
if sys.stdin.readline().strip() != "user":
    sys.exit(98)
emit("Password: ")
if sys.stdin.readline().strip() != "live":
    sys.exit(99)
emit("\nuser@diparnux:~$ ")
for line in sys.stdin:
    # 입력 에코도 일부러 보내 마커 파서가 명령 텍스트를 응답으로 보지 않는지 확인한다.
    emit(line)
    token = re.search(r"'([0-9a-f]+)_BEGIN__'", line).group(1)
    parts = shlex.split(line)
    command = parts[parts.index("-c") + 1].removesuffix(";")
    if mode == "serial-timeout" and command.startswith("diparnux --json model pull"):
        time.sleep(60)
    output, rc = result(command)
    emit(f"\n__DPX_{token}_BEGIN__\n{output}\n__DPX_{token}_END__:{rc}\n")
