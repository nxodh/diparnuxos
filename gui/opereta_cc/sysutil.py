"""데스크톱 설정 페이지가 쓰는 시스템 도구 호출과 출력 해석. GTK 를 쓰지 않아 단위 시험이 가능하다.

모든 호출은 실패해도 예외를 내지 않는다(명령이 없으면 종료 코드 127). 해석 함수는 순수 함수다.
"""

import json
import os
import re
import shutil
import subprocess


def run(cmd, timeout=10, env=None):
    """cmd(문자열 목록)를 실행해 (종료 코드, 표준 출력)을 돌려준다. 명령이 없거나 시간이 지나면 (127, "") 이나 (124, "")."""
    try:
        res = subprocess.run(cmd, capture_output=True, text=True, timeout=timeout, env=env, check=False)
        return res.returncode, res.stdout
    except FileNotFoundError:
        return 127, ""
    except subprocess.TimeoutExpired:
        return 124, ""
    except OSError:
        return 126, ""


def have(cmd):
    return shutil.which(cmd) is not None


# ---------------------------------------------------------------- xfconf

def xfconf_get(channel, prop):
    rc, out = run(["xfconf-query", "-c", channel, "-p", prop])
    return out.strip() if rc == 0 else None


def xfconf_set(channel, prop, kind, value):
    """값을 쓴다. 속성이 없으면 만든다(-n). 성공하면 True."""
    rc, _out = run(["xfconf-query", "-c", channel, "-p", prop, "-n", "-t", kind, "-s", str(value)])
    return rc == 0


# ---------------------------------------------------------------- 디스플레이(xrandr)

_OUTPUT_RE = re.compile(r"^(\S+) (connected|disconnected)(?: (primary))?(?: (\d+)x(\d+)\+\d+\+\d+)?")
_MODE_RE = re.compile(r"^\s+(\d+)x(\d+)\s+(.*)$")


def parse_xrandr(text):
    """`xrandr --query` 출력을 해석한다. 돌려주는 값: [{name, connected, primary, current, modes}]."""
    outputs = []
    cur = None
    for line in text.splitlines():
        m = _OUTPUT_RE.match(line)
        if m:
            cur = {
                "name": m.group(1),
                "connected": m.group(2) == "connected",
                "primary": bool(m.group(3)),
                "current": (int(m.group(4)), int(m.group(5))) if m.group(4) else None,
                "modes": [],
            }
            outputs.append(cur)
            continue
        m = _MODE_RE.match(line)
        if m and cur is not None:
            size = (int(m.group(1)), int(m.group(2)))
            rates = m.group(3)
            if "*" in rates and cur["current"] is None:
                cur["current"] = size
            if size not in cur["modes"]:
                cur["modes"].append(size)
    return [o for o in outputs if o["connected"]]


def display_outputs():
    rc, out = run(["xrandr", "--query"])
    return parse_xrandr(out) if rc == 0 else []


def set_resolution(output, size):
    rc, _out = run(["xrandr", "--output", output, "--mode", "%dx%d" % size])
    return rc == 0


# ---------------------------------------------------------------- 소리(pactl)

def _percent(volume):
    """pactl 의 volume 사전에서 첫 채널의 퍼센트를 읽는다."""
    for ch in (volume or {}).values():
        txt = str(ch.get("value_percent", "")).strip().rstrip("%")
        if txt.isdigit():
            return int(txt)
    return 0


def parse_pactl_devices(text):
    """`pactl --format=json list sinks|sources` 출력을 [{name, description, volume, mute}] 로 바꾼다."""
    try:
        data = json.loads(text)
    except ValueError:
        return []
    out = []
    for d in data if isinstance(data, list) else []:
        name = d.get("name", "")
        if not name or name.endswith(".monitor"):
            continue
        out.append({
            "name": name,
            "description": d.get("description") or name,
            "volume": _percent(d.get("volume")),
            "mute": bool(d.get("mute")),
        })
    return out


def audio_devices(kind):
    """kind 는 sinks 또는 sources 다. 돌려주는 값: (장치 목록, 기본 장치 이름)."""
    rc, out = run(["pactl", "--format=json", "list", kind])
    devices = parse_pactl_devices(out) if rc == 0 else []
    rc2, default = run(["pactl", "get-default-" + kind[:-1]])
    return devices, default.strip() if rc2 == 0 else ""


def set_default_device(kind, name):
    rc, _o = run(["pactl", "set-default-" + kind[:-1], name])
    return rc == 0


def set_volume(kind, name, percent):
    rc, _o = run(["pactl", "set-%s-volume" % kind[:-1], name, "%d%%" % max(0, min(150, int(percent)))])
    return rc == 0


def set_mute(kind, name, mute):
    rc, _o = run(["pactl", "set-%s-mute" % kind[:-1], name, "1" if mute else "0"])
    return rc == 0


# ---------------------------------------------------------------- 블루투스(bluetoothctl)

_MAC_RE = re.compile(r"^[0-9A-Fa-f]{2}(:[0-9A-Fa-f]{2}){5}$")


def valid_mac(mac):
    return bool(_MAC_RE.match(mac or ""))


def parse_bt_show(text):
    """`bluetoothctl show` 출력. 어댑터가 없으면 available 이 거짓이다."""
    if "Controller" not in text:
        return {"available": False, "powered": False, "discovering": False, "name": ""}
    info = {"available": True, "powered": False, "discovering": False, "name": ""}
    for line in text.splitlines():
        line = line.strip()
        if line.startswith("Powered:"):
            info["powered"] = line.split(":", 1)[1].strip() == "yes"
        elif line.startswith("Discovering:"):
            info["discovering"] = line.split(":", 1)[1].strip() == "yes"
        elif line.startswith("Alias:"):
            info["name"] = line.split(":", 1)[1].strip()
    return info


def parse_bt_devices(text):
    """`bluetoothctl devices` 의 "Device AA:BB:... 이름" 줄들을 [{mac, name}] 로 바꾼다."""
    out = []
    for line in text.splitlines():
        parts = line.strip().split(None, 2)
        if len(parts) >= 2 and parts[0] == "Device" and valid_mac(parts[1]):
            out.append({"mac": parts[1].upper(), "name": parts[2] if len(parts) > 2 else parts[1]})
    return out


def parse_bt_info(text):
    info = {"paired": False, "connected": False, "trusted": False, "icon": ""}
    for line in text.splitlines():
        line = line.strip()
        for key in ("Paired", "Connected", "Trusted"):
            if line.startswith(key + ":"):
                info[key.lower()] = line.split(":", 1)[1].strip() == "yes"
        if line.startswith("Icon:"):
            info["icon"] = line.split(":", 1)[1].strip()
    return info


def classify_bt_unavailable(show_rc, rfkill_out, service_state):
    """어댑터를 읽지 못한 이유를 코드로 돌려준다: no_tool, timeout, rfkill_hard, rfkill_soft, no_adapter, service_stopped.

    show_rc 는 `bluetoothctl show` 의 종료 코드, rfkill_out 은 `rfkill list bluetooth` 출력, service_state 는
    `systemctl is-active bluetooth` 의 출력이다. 어댑터가 하나도 없으면 rfkill 도 아무것도 나열하지 않는다.
    """
    if show_rc == 127:
        return "no_tool"
    if show_rc == 124:
        return "timeout"
    low = (rfkill_out or "").lower()
    if "hard blocked: yes" in low:
        return "rfkill_hard"
    if "soft blocked: yes" in low:
        return "rfkill_soft"
    if not low.strip():
        return "no_adapter"
    if (service_state or "").strip() != "active":
        return "service_stopped"
    return "no_adapter"


def bluetooth_state():
    """(어댑터 정보, 기기 목록) 을 돌려준다. 기기마다 paired, connected 를 채운다. 어댑터를 못 읽으면 adapter["reason"] 에 이유가 있다."""
    rc, out = run(["bluetoothctl", "show"], timeout=8)
    adapter = parse_bt_show(out) if rc == 0 else {"available": False, "powered": False, "discovering": False, "name": ""}
    devices = []
    if adapter["available"]:
        _rc, listing = run(["bluetoothctl", "devices"], timeout=8)
        for d in parse_bt_devices(listing):
            _rc, info = run(["bluetoothctl", "info", d["mac"]], timeout=8)
            d.update(parse_bt_info(info))
            devices.append(d)
    else:
        _rc, rf = run(["rfkill", "list", "bluetooth"], timeout=4)
        _rc, svc = run(["systemctl", "is-active", "bluetooth"], timeout=4)
        adapter["reason"] = classify_bt_unavailable(rc, rf, svc)
    return adapter, devices


def bluetooth_command(*args, timeout=30):
    """bluetoothctl 에 한 가지 명령을 준다. MAC 인자는 형식을 확인한다. 성공하면 True."""
    for a in args[1:]:
        if re.match(r"^[0-9A-Fa-f:]{17}$", a) and not valid_mac(a):
            return False
    rc, out = run(["bluetoothctl", *args], timeout=timeout)
    return rc == 0 and "Failed" not in out


def bluetooth_scan(seconds=8):
    rc, _out = run(["bluetoothctl", "--timeout", str(int(seconds)), "scan", "on"], timeout=seconds + 6)
    return rc == 0


# ---------------------------------------------------------------- 이 컴퓨터

def os_release(path="/etc/os-release"):
    info = {}
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                k, _sep, v = line.strip().partition("=")
                if k:
                    info[k] = v.strip().strip('"')
    except OSError:
        pass
    return info


def cpu_model(path="/proc/cpuinfo"):
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                if line.startswith("model name"):
                    return line.split(":", 1)[1].strip()
    except OSError:
        pass
    return ""


def mem_total_kib(path="/proc/meminfo"):
    try:
        with open(path, encoding="utf-8") as f:
            for line in f:
                if line.startswith("MemTotal:"):
                    return int(line.split()[1])
    except (OSError, ValueError, IndexError):
        pass
    return 0


def uptime_seconds(path="/proc/uptime"):
    try:
        with open(path, encoding="utf-8") as f:
            return int(float(f.read().split()[0]))
    except (OSError, ValueError, IndexError):
        return 0


def parse_lsblk(text):
    """`lsblk -J -b -o NAME,SIZE,TYPE,FSTYPE,LABEL,MOUNTPOINTS` 출력에서 디스크와 파티션의 평평한 목록을 만든다."""
    try:
        data = json.loads(text)
    except ValueError:
        return []
    out = []

    def walk(items, parent):
        for d in items or []:
            kind = d.get("type") or ""
            if kind in ("disk", "part"):
                mounts = [m for m in (d.get("mountpoints") or []) if m and m != "[SWAP]"]
                out.append({
                    "name": d.get("name", ""),
                    "type": kind,
                    "size": int(d.get("size") or 0),
                    "fstype": d.get("fstype") or "",
                    "label": d.get("label") or "",
                    "mounts": mounts,
                    "parent": parent,
                })
            walk(d.get("children"), d.get("name", ""))

    walk(data.get("blockdevices"), "")
    return out


def block_devices():
    rc, out = run(["lsblk", "-J", "-b", "-o", "NAME,SIZE,TYPE,FSTYPE,LABEL,MOUNTPOINTS"])
    return parse_lsblk(out) if rc == 0 else []


def usage(path):
    try:
        u = shutil.disk_usage(path)
        return u.used, u.total
    except OSError:
        return None


# ---------------------------------------------------------------- 키보드

def keyboard_layout():
    rc, out = run(["setxkbmap", "-query"])
    if rc != 0:
        return ""
    for line in out.splitlines():
        if line.startswith("layout:"):
            return line.split(":", 1)[1].strip()
    return ""


def wallpaper_properties():
    """xfdesktop 의 last-image 속성 목록(모니터마다 하나)."""
    rc, out = run(["xfconf-query", "-c", "xfce4-desktop", "-l"])
    if rc != 0:
        return []
    return [p.strip() for p in out.splitlines() if p.strip().endswith("/last-image")]


def set_wallpaper(path):
    """모든 모니터의 배경 화면을 path 로 바꾼다. 바꾼 속성 수를 돌려준다."""
    if not os.path.isfile(path):
        return 0
    n = 0
    props = wallpaper_properties()
    for prop in props:
        rc, _o = run(["xfconf-query", "-c", "xfce4-desktop", "-p", prop, "-s", path])
        if rc == 0:
            n += 1
            style = prop[: -len("last-image")] + "image-style"
            run(["xfconf-query", "-c", "xfce4-desktop", "-p", style, "-n", "-t", "int", "-s", "5"])
    return n
