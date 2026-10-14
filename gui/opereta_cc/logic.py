"""GTK 에 의존하지 않는 표시 논리. 상태 해석, 형식 변환, 첫 부팅 마법사 단계 판단을 둔다.

문자열은 i18n._ 로 감싼다. 판단 결과는 (수준, 제목, 설명) 같은 단순한 값으로 돌려준다.
"""

from .i18n import _

# 수준 값. UI 가 아이콘과 색을 고른다.
OK = "ok"
INFO = "info"
WARN = "warning"
ERROR = "error"

# GPU 진단 상태별 안내. 키는 opereta-gpu-check 가 쓰는 상태 이름이다.
_GPU_STATE_TEXT = {
    "no_gpu": lambda: (INFO, _("No NVIDIA GPU found"), _("The model will run on the CPU. Inference is slower without a GPU.")),
    "module_unloaded": lambda: (ERROR, _("The NVIDIA driver is not loaded"), _("The GPU is present but the kernel module did not load. Check the driver report in the System page.")),
    "secure_boot": lambda: (WARN, _("Secure Boot blocks the NVIDIA driver"), _("Secure Boot is on, so the unsigned NVIDIA module cannot load. Turn Secure Boot off in the firmware settings, or enroll a key (see the Secure Boot step).")),
    "proprietary_module": lambda: (ERROR, _("A proprietary NVIDIA module is loaded"), _("Opereta needs the open kernel modules. Reinstall the image or remove the proprietary driver.")),
    "version_mismatch": lambda: (ERROR, _("Driver and library versions differ"), _("Reboot so the loaded kernel module matches the installed driver libraries.")),
    "driver_missing": lambda: (ERROR, _("The NVIDIA driver is missing"), _("The driver packages are not installed in this image.")),
    "query_failed": lambda: (ERROR, _("The GPU did not answer"), _("The driver is loaded but querying the GPU failed. Run the check again after a reboot.")),
    "unsupported_gpu": lambda: (WARN, _("This GPU is not supported"), _("Only RTX 30, 40 and 50 series GPUs are supported. The model will run on the CPU.")),
    "config_missing": lambda: (ERROR, _("The diagnostic configuration is missing"), _("The GPU check could not read its configuration. Reinstall the image.")),
    "internal_error": lambda: (ERROR, _("The GPU check failed"), _("The diagnostic script hit an internal error. Export the report from the System page.")),
}

_ARCH_NAMES = {"ampere": "Ampere", "ada": "Ada Lovelace", "blackwell": "Blackwell"}

_SERVER_STATE_TEXT = {
    "stopped": lambda: _("Stopped"),
    "starting": lambda: _("Starting"),
    "running": lambda: _("Running on GPU"),
    "running_cpu_fallback": lambda: _("Running on CPU"),
    "failed": lambda: _("Failed"),
}


def fmt_bytes(n):
    """바이트를 사람이 읽는 크기로 바꾼다. 음수나 None 은 대시를 돌려준다."""
    if n is None or n < 0:
        return "-"
    value = float(n)
    for unit in ("B", "KiB", "MiB", "GiB", "TiB"):
        if value < 1024.0 or unit == "TiB":
            return "%d B" % value if unit == "B" else "%.1f %s" % (value, unit)
        value /= 1024.0
    return "-"


def fmt_mib(n):
    if n is None or n < 0:
        return "-"
    if n >= 1024:
        return "%.1f GiB" % (n / 1024.0)
    return "%d MiB" % n


def fmt_rate(bytes_per_second):
    """초당 바이트를 1.5 MiB/s 처럼 쓴다."""
    try:
        n = float(bytes_per_second)
    except (TypeError, ValueError):
        return "-"
    for unit in ("B/s", "KiB/s", "MiB/s", "GiB/s"):
        if n < 1024 or unit == "GiB/s":
            return ("%d %s" % (n, unit)) if unit == "B/s" else ("%.1f %s" % (n, unit))
        n /= 1024.0
    return "-"


def port_text(port, port_end=0):
    return "%d-%d" % (port, port_end) if port_end and port_end != port else str(port)


def source_label(source):
    """방화벽 출발지 값을 사용자 문장으로 바꾼다."""
    if source in (None, "", "any"):
        return _("any address")
    if source == "lan":
        return _("the local network")
    return source


def rule_subtitle(rule):
    return "%s %s · %s" % (rule.get("protocol", "tcp"), port_text(rule.get("port", 0), rule.get("port_end", 0)), source_label(rule.get("source")))


def split_list(text):
    """쉼표나 공백으로 나눈 목록. 빈 항목은 뺀다."""
    return [p for p in (text or "").replace(",", " ").split() if p]


def service_state_text(service):
    active = service.get("active") or "-"
    sub = service.get("sub") or ""
    if not service.get("installed", True):
        return _("not installed")
    return "%s (%s)" % (active, sub) if sub and sub != active else active


def exposure_level_icon(level):
    return {"ok": "emblem-ok-symbolic", "info": "dialog-information-symbolic", "warning": "dialog-warning-symbolic", "error": "dialog-error-symbolic"}.get(
        level, "dialog-information-symbolic"
    )


def fmt_count(n):
    """큰 수를 1.2K, 3.4M 처럼 줄여 쓴다."""
    n = int(n or 0)
    if n >= 1000000:
        return "%.1fM" % (n / 1000000.0)
    if n >= 1000:
        return "%.1fK" % (n / 1000.0)
    return str(n)


def key_usage_text(key):
    """게이트웨이의 키별 사용량을 한 줄로 쓴다."""
    last = key.get("last_used") or ""
    if last.startswith("0001-"):
        last = ""
    parts = [
        _("%(requests)s requests") % {"requests": fmt_count(key.get("requests"))},
        _("%(errors)s errors") % {"errors": fmt_count(key.get("errors"))},
        _("%(tokens)s tokens") % {"tokens": fmt_count((key.get("prompt_tokens") or 0) + (key.get("completion_tokens") or 0))},
        _("last used %(when)s") % {"when": last.replace("T", " ").rstrip("Z")} if last else _("never used"),
    ]
    if key.get("prefix"):
        parts.insert(0, key["prefix"] + "…")
    return " · ".join(parts)


def tls_ca_subtitle(tls):
    """TLS 패널의 CA 행에 보일 문구다. 지문이 없으면 아직 만들지 않은 것이다(번역은 호출하는 쪽이 한다)."""
    tls = tls or {}
    fp = tls.get("ca_fingerprint") or ""
    if not fp:
        return ""
    until = (tls.get("ca_not_after") or "")[:10]
    return fp if not until else "%s · %s" % (fp, until)


def tls_listening_text(tls):
    """리스너 상태를 한 단어로 만든다: off, error, listening <포트>, waiting."""
    tls = tls or {}
    if not tls.get("enabled"):
        return "off"
    if tls.get("listener_error"):
        return "error"
    if tls.get("listening"):
        return "listening %s" % tls.get("port", "")
    return "waiting"


def server_state_label(state):
    fn = _SERVER_STATE_TEXT.get(state)
    return fn() if fn else str(state or "-")


def arch_label(arch):
    if not arch:
        return "-"
    if arch.startswith("unknown"):
        return _("Unknown architecture")
    return _ARCH_NAMES.get(arch, arch)


def gpu_banner(status):
    """GPU 가 쓸 수 없는 상태일 때 보여 줄 (수준, 제목, 설명). 준비됐으면 None."""
    gpu = (status or {}).get("gpu") or {}
    file_status = gpu.get("status")
    if file_status is None:
        reason = gpu.get("status_error") or ""
        if reason:
            return (INFO, _("GPU status is not available yet"), _("The GPU check has not produced a result: %s") % reason)
        return (INFO, _("GPU status is not available yet"), _("The GPU check has not run."))
    state = file_status.get("state", "")
    if state == "ready":
        return None
    maker = _GPU_STATE_TEXT.get(state)
    if maker is None:
        return (WARN, _("Unknown GPU state: %s") % state, file_status.get("message", ""))
    return maker()


def device_rows(status):
    """대시보드가 그릴 GPU 목록을 만든다. NVML 값이 있으면 NVML 을, 없으면 진단 파일을 쓴다."""
    gpu = (status or {}).get("gpu") or {}
    nvml = gpu.get("nvml") or {}
    rows = []
    for d in nvml.get("devices") or []:
        power = d.get("power_mw", -1)
        rows.append(
            {
                "index": d.get("index", 0),
                "name": d.get("name", "?"),
                "arch": arch_label(d.get("arch")),
                "compute_cap": d.get("compute_cap") or "-",
                "vram_used": d.get("memory_used_mib", -1),
                "vram_total": d.get("memory_total_mib", -1),
                "temperature_c": d.get("temperature_c", -1),
                "power_w": (power / 1000.0) if power is not None and power >= 0 else -1,
                "util_gpu": d.get("util_gpu_percent", -1),
            }
        )
    if rows:
        return rows
    file_status = gpu.get("status") or {}
    for g in file_status.get("gpus") or []:
        rows.append(
            {
                "index": g.get("index", 0),
                "name": g.get("name", "?"),
                "arch": "-",
                "compute_cap": g.get("compute_cap") or "-",
                "vram_used": -1,
                "vram_total": -1,
                "temperature_c": -1,
                "power_w": -1,
                "util_gpu": -1,
            }
        )
    return rows


def device_subtitle(row):
    parts = [row["arch"], "CC %s" % row["compute_cap"]]
    if row["vram_total"] >= 0:
        parts.append("%s / %s" % (fmt_mib(row["vram_used"]), fmt_mib(row["vram_total"])))
    if row["temperature_c"] >= 0:
        parts.append("%d °C" % row["temperature_c"])
    if row["power_w"] >= 0:
        parts.append("%.0f W" % row["power_w"])
    if row["util_gpu"] >= 0:
        parts.append("%d %%" % row["util_gpu"])
    return " · ".join(parts)


def memory_summary(status):
    mem = (status or {}).get("memory")
    if not mem:
        return None
    total, avail = mem.get("total_mib", -1), mem.get("available_mib", -1)
    if total <= 0:
        return None
    return {"total": total, "used": max(total - avail, 0), "available": avail}


def progress_fraction(job):
    total = job.get("bytes_total") or 0
    if total <= 0:
        return 0.0
    return max(0.0, min(1.0, job.get("bytes_done", 0) / float(total)))


def job_is_final(job):
    return job.get("state") in ("done", "failed", "canceled")


def _failure_hints():
    return {
        "gpu_offload_failed": _("The model did not load on the GPU as planned. Lower the context or the number of simultaneous requests, or free GPU memory."),
        "cuda_backend": _("The CUDA backend did not load. Check the GPU in the Dashboard and run the GPU check."),
        "health_timeout": _("The server did not become ready in time. A large model can take a while to load; check the server log below."),
        "server_exited": _("The server stopped by itself. The log below shows the last lines it wrote."),
        "unit_start_failed": _("The system could not start the server unit. Check the server log below."),
    }


def server_failure_notes(server):
    """서버의 마지막 오류와 자동 재시작 진행을 사용자가 읽을 문장으로 바꾼다."""
    server = server or {}
    out = []
    last = server.get("last_error")
    if last:
        code = last.get("code", "")
        message = (last.get("message") or "").strip().replace("\n", " ")
        if len(message) > 400:
            message = message[:400] + "…"
        out.append("%s: %s" % (code, message) if message else code)
        hint = _failure_hints().get(code)
        if hint:
            out.append(hint)
    restart = server.get("restart")
    if restart:
        if restart.get("gave_up"):
            out.append(_("The server stopped and %d automatic restarts failed. Start it again yourself after fixing the cause.") % restart.get("max", 0))
        else:
            out.append(
                _("Automatic restart: attempt %(done)d of %(max)d.") % {"done": restart.get("attempt", 0), "max": restart.get("max", 0)}
            )
    return out


def server_advice(status):
    """데스크톱 모드에서 큰 모델을 돌릴 때 서버 모드를 권장할 근거를 모은다."""
    if (status or {}).get("mode") != "desktop":
        return []
    server = (status or {}).get("server") or {}
    plan = server.get("plan") or {}
    out = list(plan.get("advice") or [])
    return out


# ---------------------------------------------------------------- 첫 부팅 마법사

STEPS = ("gpu", "driver", "secure_boot", "account", "model", "api")


class StepResult:
    """마법사 한 단계의 판단. level 이 ERROR 이면 계속은 가능하지만 건너뜀으로 기록한다."""

    def __init__(self, step, level, title, detail=""):
        self.step = step
        self.level = level
        self.title = title
        self.detail = detail

    def __repr__(self):
        return "StepResult(%r, %r, %r)" % (self.step, self.level, self.title)


def _file_status(status):
    return ((status or {}).get("gpu") or {}).get("status")


def evaluate_gpu(status):
    fs = _file_status(status)
    if fs is None:
        return StepResult("gpu", INFO, _("GPU status is not available yet"), _("Run the GPU check to detect your GPU."))
    if not fs.get("present"):
        return StepResult("gpu", INFO, _("No NVIDIA GPU found"), _("You can still use Opereta on the CPU."))
    gpus = fs.get("gpus") or []
    names = ", ".join(g.get("name", "?") for g in gpus) or "-"
    usable = fs.get("usable_count", 0)
    if usable > 0:
        return StepResult(
            "gpu", OK, _("GPU detected"), _("%(names)s (%(usable)d of %(total)d usable)") % {"names": names, "usable": usable, "total": len(gpus)}
        )
    return StepResult("gpu", WARN, _("GPU detected but not usable"), names)


def evaluate_driver(status):
    fs = _file_status(status)
    if fs is None:
        return StepResult("driver", INFO, _("Driver status is not available yet"))
    state = fs.get("state", "")
    if state == "ready":
        version = ((status or {}).get("gpu") or {}).get("driver_version") or fs.get("driver_version") or "-"
        open_module = ((status or {}).get("gpu") or {}).get("open_module")
        detail = _("Driver %s") % version
        if open_module == "yes":
            detail += " · " + _("open kernel module")
        return StepResult("driver", OK, _("The NVIDIA driver works"), detail)
    if state == "no_gpu":
        return StepResult("driver", INFO, _("No driver check needed"), _("There is no NVIDIA GPU."))
    level, title, detail = gpu_banner(status)
    return StepResult("driver", level, title, detail)


def evaluate_secure_boot(status):
    gpu = (status or {}).get("gpu") or {}
    sb = gpu.get("secure_boot") or ((gpu.get("status") or {}).get("secure_boot")) or "unknown"
    state = ((gpu.get("status") or {}).get("state")) or ""
    if sb == "enabled":
        if state == "secure_boot":
            return StepResult(
                "secure_boot",
                WARN,
                _("Secure Boot is on and blocks the driver"),
                _("Turn Secure Boot off in the firmware settings and reboot. On an installed system you can enroll a machine key instead: run 'sudo opereta-mok enroll' in a terminal."),
            )
        return StepResult("secure_boot", INFO, _("Secure Boot is on"), _("The NVIDIA driver loaded, so no action is needed."))
    if sb == "disabled":
        return StepResult("secure_boot", OK, _("Secure Boot is off"), _("The NVIDIA module can load without a signature."))
    return StepResult("secure_boot", INFO, _("Secure Boot state is unknown"), _("This machine may not use UEFI Secure Boot."))


_NAME_FIRST = "abcdefghijklmnopqrstuvwxyz"
_NAME_REST = _NAME_FIRST + "0123456789_-"
_RESERVED = {
    "root", "user", "nobody", "daemon", "bin", "sys", "sync", "games", "man", "lp", "mail", "news", "uucp",
    "proxy", "www-data", "backup", "list", "irc", "sshd", "messagebus", "polkitd", "greetd", "tuigreet", "lightdm",
}


def check_username(name):
    """사용자 이름 규칙을 검사한다. 맞으면 None, 아니면 사용자에게 보일 문장을 돌려준다. 데몬의 규칙과 같다."""
    if not name:
        return _("Enter a user name.")
    if len(name) > 32:
        return _("The user name can have at most 32 characters.")
    if name[0] not in _NAME_FIRST:
        return _("The user name must start with a lowercase letter.")
    if any(ch not in _NAME_REST for ch in name):
        return _("Use only lowercase letters, digits, _ and - in the user name.")
    if name in _RESERVED or name.startswith("opereta") or name.startswith("systemd"):
        return _("This user name is reserved. Choose another one.")
    return None


def check_password(name, password, confirm):
    """비밀번호 규칙과 확인 입력을 검사한다. 맞으면 None."""
    if len(password) < 8:
        return _("The password must have at least 8 characters.")
    if len(password) > 128:
        return _("The password can have at most 128 characters.")
    if any(ord(ch) < 0x20 or ord(ch) > 0x7E for ch in password):
        return _("Use only printable ASCII characters in the password.")
    if password == name:
        return _("The password must differ from the user name.")
    if password != confirm:
        return _("The two passwords do not match.")
    return None


def evaluate_account(users):
    """users 는 GET /v1/users 의 users 목록이다."""
    if users:
        names = ", ".join(u.get("name", "?") for u in users[:3])
        return StepResult("account", OK, _("A login account exists"), names)
    return StepResult(
        "account",
        INFO,
        _("Create your own login account"),
        _("Without one you can only use the default live account. Choose a user name and a password."),
    )


def evaluate_model(recommendations, installed_models):
    """installed_models 는 GET /v1/models 의 models 목록이다."""
    if installed_models:
        names = ", ".join(m.get("id", "?") for m in installed_models[:3])
        return StepResult("model", OK, _("A model is already installed"), names)
    rec = (recommendations or {}).get("recommended", -1)
    models = (recommendations or {}).get("models") or []
    if isinstance(rec, int) and 0 <= rec < len(models):
        m = models[rec]
        return StepResult(
            "model", INFO, _("Recommended model: %s") % m.get("name", m.get("repo", "?")), "%s · %s" % (m.get("params", ""), fmt_mib(m.get("size_mib", -1)))
        )
    return StepResult("model", WARN, _("No recommended model fits this machine"), _("Search Hugging Face for a smaller model."))


def evaluate_api(config, key_count):
    if config and config.get("lan_exposed"):
        if key_count > 0:
            return StepResult("api", INFO, _("The API is open to your network"), _("An API key is required to connect."))
        return StepResult("api", ERROR, _("The API is open without a key"), _("Create an API key before opening the API to the network."))
    return StepResult("api", OK, _("The API is available on this machine only"), _("Other computers cannot connect."))


def skipped_steps(results):
    """ERROR 나 WARN 으로 끝난 단계는 건너뜀으로 기록한다."""
    return [r.step for r in results if r.level in (WARN, ERROR)]


# 갱신 단계 이름(opereta-update 의 이벤트 step)을 사용자 말로 바꾼다.
_UPDATE_STEPS = {
    "refresh": lambda: _("Refreshing the package lists"),
    "plan": lambda: _("Planning the update"),
    "grub": lambda: _("Preparing the boot entry"),
    "kernel": lambda: _("Installing and building the new kernel"),
    "upgrade": lambda: _("Installing the updates"),
    "verify": lambda: _("Verifying the driver build"),
    "cleanup": lambda: _("Removing the failed kernel"),
    "recover": lambda: _("Restoring the previous versions"),
    "driver": lambda: _("Restoring the previous driver"),
    "done": lambda: _("Finishing"),
}


def describe_update(info):
    """GET /v1/update 응답을 표시용 값으로 바꾼다.

    돌려주는 사전: level, title, detail, busy, reboot, can_check, can_install, can_rollback.
    """
    out = {
        "level": INFO,
        "title": _("Update status is not available"),
        "detail": "",
        "busy": False,
        "reboot": False,
        "can_check": False,
        "can_install": False,
        "can_rollback": False,
    }
    if not isinstance(info, dict):
        return out
    state = info.get("state") or {}
    phase = state.get("phase") or "idle"
    if info.get("live"):
        out["title"] = _("Updates are only available on an installed system")
        out["detail"] = _("This is a live session. Install Opereta to receive kernel and driver updates.")
        return out
    if not info.get("available"):
        out["level"] = WARN
        out["title"] = _("The update helper is not installed")
        return out

    if info.get("running") or phase == "running":
        step = state.get("step") or ""
        fn = _UPDATE_STEPS.get(step)
        out["busy"] = True
        out["title"] = _("Updating")
        out["detail"] = fn() if fn else step
        return out

    out["can_check"] = True
    out["can_install"] = phase != "pending_boot"
    out["can_rollback"] = bool(info.get("rollback_available")) and phase != "pending_boot"

    if phase == "pending_boot":
        out["level"] = WARN
        out["reboot"] = True
        out["title"] = _("Restart to finish the update")
        if state.get("new_kernel"):
            out["detail"] = _("The new kernel %s starts once. It becomes the default only after it passes the boot check.") % state["new_kernel"]
        else:
            out["detail"] = _("The new driver is loaded after the restart.")
        return out
    if phase == "failed":
        out["level"] = ERROR
        out["title"] = _("The last update failed")
        out["detail"] = state.get("message") or state.get("code") or ""
        return out
    if phase == "rolled_back":
        out["level"] = WARN
        out["reboot"] = True
        out["title"] = _("Rolled back")
        out["detail"] = _("Restart to use the previous kernel and driver.")
        return out

    check = info.get("last_check") or {}
    plan = check.get("plan")
    if check.get("error"):
        out["level"] = WARN
        out["title"] = _("The last check failed")
        out["detail"] = check["error"].get("message") or check["error"].get("code") or ""
    elif plan is None:
        out["title"] = _("Not checked yet")
        out["detail"] = _("Check for updates to see what is available.")
    elif plan.get("up_to_date"):
        out["level"] = OK
        out["title"] = _("Everything is up to date")
        channel = info.get("channel") or {}
        if check.get("channel_error"):
            # 채널만 닿지 않았다. Debian 과 NVIDIA 쪽은 확인했지만 새 Opereta 버전은 볼 수 없었다.
            out["level"] = WARN
            out["detail"] = _("The Opereta update server could not be reached, so new Opereta versions were not checked.")
        elif channel and not channel.get("enabled"):
            # 채널이 꺼져 있으면 "최신"은 Debian 과 NVIDIA 저장소만 본 결과다.
            out["level"] = WARN
            out["detail"] = _("The Opereta update channel is off in this build, so new Opereta versions are not shown.")
        elif info.get("os_version"):
            out["detail"] = _("Installed: Opereta %s") % info["os_version"]
    else:
        count = len(plan.get("changes") or [])
        parts = []
        if plan.get("os_to"):
            out["title"] = _("Opereta %s is available") % plan["os_to"]
            parts.append(_("Opereta %(old)s to %(new)s") % {"old": plan.get("os_from") or info.get("os_version") or "?", "new": plan["os_to"]})
            parts.append(_("%d package updates are available") % count)
        else:
            out["title"] = _("%d package updates are available") % count
        kernels = plan.get("kernels") or []
        if kernels:
            parts.append(_("New kernel %s") % kernels[-1])
        if plan.get("driver_to"):
            parts.append(_("NVIDIA driver %(old)s to %(new)s") % {"old": plan.get("driver_from") or "?", "new": plan["driver_to"]})
        out["detail"] = " · ".join(parts)
    return out


def describe_cuda_runtime(info):
    """GET /v1/update 응답의 cuda_runtime 을 표시용 값으로 바꾼다. 돌려주는 사전: text, installed, can_install."""
    out = {"text": _("Not available"), "installed": False, "can_install": False}
    if not isinstance(info, dict):
        return out
    rt = info.get("cuda_runtime") or {}
    if rt.get("installed"):
        out["installed"] = True
        out["text"] = _("Installed. Restart the model server to use the GPU.")
        return out
    if rt.get("reason") == "no_settings":
        out["text"] = _("This image has no CUDA runtime settings.")
        return out
    out["text"] = _("Not installed. GPU acceleration needs the CUDA runtime from NVIDIA; without it the model runs on the CPU.")
    # 설치된 시스템과 라이브 세션 모두에서 받을 수 있다(라이브는 재부팅하면 사라진다). 갱신이 도는 중에는 막는다.
    out["can_install"] = not (info.get("running") or (info.get("state") or {}).get("phase") == "running")
    return out


def live_session():
    """라이브 세션(램에 올린 이미지로 부팅)인지 본다. /run/live 는 live-boot 가 만든다."""
    import os

    return os.path.exists("/run/live")


def live_storage_summary(path="/"):
    """라이브 세션에서 루트 파일 시스템(램 기반)의 사용량을 돌려준다. (used_bytes, total_bytes) 또는 None."""
    import shutil

    try:
        usage = shutil.disk_usage(path)
    except OSError:
        return None
    if usage.total <= 0:
        return None
    return usage.used, usage.total


# 네트워크 오류 코드별 안내.
_NETWORK_REASONS = {
    "no_wifi_device": lambda: _("No wireless adapter was found."),
    "iwd_unavailable": lambda: _("The wireless service is not running."),
}


def describe_network(net):
    """GET /v1/network 응답을 표시용 값으로 바꾼다. 돌려주는 사전: online, summary, wifi_available, wifi_note."""
    out = {"online": False, "summary": _("Network status is not available"), "wifi_available": False, "wifi_note": ""}
    if not isinstance(net, dict):
        return out
    out["online"] = bool(net.get("online"))
    wifi = net.get("wifi") or {}
    if out["online"]:
        # 연결된 인터페이스와 주소를 한 줄로 보인다.
        for it in net.get("interfaces") or []:
            if it.get("gateway") and it.get("addresses"):
                kind = {"ethernet": _("Wired"), "wifi": _("Wi-Fi")}.get(it.get("kind"), it.get("name", ""))
                ssid = wifi.get("connected_ssid") if it.get("kind") == "wifi" else ""
                label = "%s · %s" % (kind, ssid) if ssid else kind
                out["summary"] = _("Connected: %(label)s (%(address)s)") % {"label": label, "address": it["addresses"][0]}
                break
        else:
            out["summary"] = _("Connected")
    else:
        out["summary"] = _("Not connected to the internet")
    out["wifi_available"] = bool(wifi.get("available"))
    if not out["wifi_available"]:
        fn = _NETWORK_REASONS.get(wifi.get("reason"))
        out["wifi_note"] = fn() if fn else _("Wi-Fi is not available.")
    elif not wifi.get("powered"):
        out["wifi_note"] = _("Wi-Fi is turned off.")
    return out


def signal_label(percent):
    """신호 세기(0~100)를 짧은 말로 바꾼다."""
    if percent is None:
        return ""
    if percent >= 70:
        return _("Strong")
    if percent >= 40:
        return _("Good")
    return _("Weak")


def check_wifi_password(password):
    """Wi-Fi 비밀번호가 WPA 규칙(8~63자의 출력 가능한 ASCII 또는 64자리 16진수)에 맞는지 본다. 문제가 없으면 None."""
    if password == "":
        return _("Enter the password of the network.")
    if len(password) == 64 and all(c in "0123456789abcdefABCDEF" for c in password):
        return None
    if len(password) < 8:
        return _("The password must be at least 8 characters.")
    if len(password) > 63:
        return _("The password must be at most 63 characters.")
    if any(ord(c) < 0x20 or ord(c) > 0x7E for c in password):
        return _("Use only letters, digits and symbols from the keyboard (no Korean or accented characters).")
    return None


def fmt_uptime(seconds):
    """초를 "3d 4h 12m" 같은 짧은 문자열로 바꾼다. 알 수 없으면 "-"."""
    try:
        seconds = int(seconds)
    except (TypeError, ValueError):
        return "-"
    if seconds <= 0:
        return "-"
    d, h, m = seconds // 86400, (seconds % 86400) // 3600, (seconds % 3600) // 60
    if d:
        return "%dd %dh %dm" % (d, h, m)
    if h:
        return "%dh %dm" % (h, m)
    return "%dm" % m


def power_failed_checks(state):
    """상시 가동 검사에서 통과하지 못한 항목을 "이름=값" 목록으로 돌려준다."""
    out = []
    for check in state.get("checks") or []:
        if not check.get("ok"):
            out.append("%s=%s" % (check.get("name", "?"), check.get("got") or "unknown"))
    return out
