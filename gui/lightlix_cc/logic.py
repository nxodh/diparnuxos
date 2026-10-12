"""GTK 에 의존하지 않는 표시 논리. 상태 해석, 형식 변환, 첫 부팅 마법사 단계 판단을 둔다.

문자열은 i18n._ 로 감싼다. 판단 결과는 (수준, 제목, 설명) 같은 단순한 값으로 돌려준다.
"""

from .i18n import _

# 수준 값. UI 가 아이콘과 색을 고른다.
OK = "ok"
INFO = "info"
WARN = "warning"
ERROR = "error"

# GPU 진단 상태별 안내. 키는 lightlix-gpu-check 가 쓰는 상태 이름이다.
_GPU_STATE_TEXT = {
    "no_gpu": lambda: (INFO, _("No NVIDIA GPU found"), _("The model will run on the CPU. Inference is slower without a GPU.")),
    "module_unloaded": lambda: (ERROR, _("The NVIDIA driver is not loaded"), _("The GPU is present but the kernel module did not load. Check the driver report in the System page.")),
    "secure_boot": lambda: (WARN, _("Secure Boot blocks the NVIDIA driver"), _("Secure Boot is on, so the unsigned NVIDIA module cannot load. Turn Secure Boot off in the firmware settings, or enroll a key (see the Secure Boot step).")),
    "proprietary_module": lambda: (ERROR, _("A proprietary NVIDIA module is loaded"), _("Lightlix needs the open kernel modules. Reinstall the image or remove the proprietary driver.")),
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
        return StepResult("gpu", INFO, _("No NVIDIA GPU found"), _("You can still use Lightlix on the CPU."))
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
                _("Turn Secure Boot off in the firmware settings and reboot. On an installed system you can enroll a machine key instead: run 'sudo lightlix-mok enroll' in a terminal."),
            )
        return StepResult("secure_boot", INFO, _("Secure Boot is on"), _("The NVIDIA driver loaded, so no action is needed."))
    if sb == "disabled":
        return StepResult("secure_boot", OK, _("Secure Boot is off"), _("The NVIDIA module can load without a signature."))
    return StepResult("secure_boot", INFO, _("Secure Boot state is unknown"), _("This machine may not use UEFI Secure Boot."))


_NAME_FIRST = "abcdefghijklmnopqrstuvwxyz"
_NAME_REST = _NAME_FIRST + "0123456789_-"
_RESERVED = {
    "root", "user", "nobody", "daemon", "bin", "sys", "sync", "games", "man", "lp", "mail", "news", "uucp",
    "proxy", "www-data", "backup", "list", "irc", "sshd", "messagebus", "polkitd", "greetd", "tuigreet",
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
    if name in _RESERVED or name.startswith("lightlix") or name.startswith("systemd"):
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
