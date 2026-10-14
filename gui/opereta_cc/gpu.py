"""그래픽(GPU): 카드, 드라이버, CUDA 런타임 내려받기, GPU 점검.

CUDA 런타임은 이미지에 들어 있지 않고 이 컴퓨터가 NVIDIA 에서 직접 받는다(NVIDIA 라이선스가 재배포를 제한한다).
없으면 모델이 CPU 로 돈다. 그래서 이 페이지가 가장 위에서 상태와 받기 단추를 보여 준다.
"""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, confirm, run_async, text_button  # noqa: E402

EULA_URL = "https://docs.nvidia.com/cuda/eula/index.html"


class GpuPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Graphics"), icon_name="video-display-symbolic")
        self.ctx = ctx
        self._update = None
        self._poll = None
        self._gpu_rows = []

        self.rt_group = Adw.PreferencesGroup(title=_("CUDA runtime"))
        self.rt_group.set_description(
            _("GPU acceleration needs NVIDIA's CUDA runtime. It is not part of Opereta: this computer downloads it directly from NVIDIA.")
        )
        self.rt_row = action_row(_("GPU acceleration (CUDA runtime)"), "-")
        self.btn_install = text_button(_("Download from NVIDIA"), self.install_runtime, "suggested-action")
        self.btn_install.set_sensitive(False)
        self.rt_row.add_suffix(self.btn_install)
        self.rt_group.add(self.rt_row)
        self.restart_row = action_row(_("Restart the model server"), _("Restart it so the model uses the GPU after the runtime is installed."))
        self.restart_row.add_suffix(text_button(_("Restart"), self.restart_server))
        self.restart_row.set_visible(False)
        self.rt_group.add(self.restart_row)
        self.add(self.rt_group)

        self.cards_group = Adw.PreferencesGroup(title=_("Graphics cards"))
        self.add(self.cards_group)

        self.driver_group = Adw.PreferencesGroup(title=_("Driver"))
        self.driver_row = action_row(_("Driver version"), "-")
        self.cuda_row = action_row(_("CUDA supported by the driver"), "-")
        self.sb_row = action_row(_("Secure Boot"), "-")
        for row in (self.driver_row, self.cuda_row, self.sb_row):
            self.driver_group.add(row)
        self.add(self.driver_group)

        self.maint = Adw.PreferencesGroup(title=_("Maintenance"))
        self.maint.add(action_row(_("GPU check"), _("Run the GPU diagnostic again."), text_button(_("Run"), self.gpu_check)))
        self.add(self.maint)

        ctx.subscribe(self._on_status)
        self.connect("map", lambda *_a: self.refresh())

    def refresh(self):
        run_async(self.ctx.client.update_info, self._got_update, lambda _e: None)

    def _got_update(self, info):
        self._update = info
        c = logic.describe_cuda_runtime(info)
        busy = bool(info.get("running")) or (info.get("state") or {}).get("phase") == "running"
        self.rt_row.set_subtitle(GLib.markup_escape_text(c["text"]))
        self.btn_install.set_sensitive(c["can_install"] and not busy)
        self.btn_install.set_visible(not c["installed"])
        self.restart_row.set_visible(bool(c["installed"]) and bool(logic.device_rows(self.ctx.status)))
        if busy and self._poll is None:
            self.rt_row.set_subtitle(GLib.markup_escape_text(_("Downloading and installing…")))
            self._poll = GLib.timeout_add_seconds(3, self._tick)

    def _tick(self):
        def done(info):
            busy = bool(info.get("running")) or (info.get("state") or {}).get("phase") == "running"
            self._got_update(info)
            if not busy:
                self._poll = None

        def failed(_e):
            self._poll = None

        run_async(self.ctx.client.update_info, done, failed)
        return GLib.SOURCE_CONTINUE if self._poll is not None else GLib.SOURCE_REMOVE

    def _on_status(self, status, _error):
        if status is None:
            return
        gpu = status.get("gpu") or {}
        nvml = gpu.get("nvml") or {}
        rows = logic.device_rows(status)
        wanted = [(d["name"], logic.device_subtitle(d)) for d in rows] or [(_("No GPU"), _("No NVIDIA GPU was detected."))]
        if len(wanted) != len(self._gpu_rows):
            clear_group(self.cards_group, self._gpu_rows)
            for title, subtitle in wanted:
                row = action_row(title, subtitle)
                self.cards_group.add(row)
                self._gpu_rows.append(row)
        else:
            for row, (title, subtitle) in zip(self._gpu_rows, wanted):
                row.set_title(GLib.markup_escape_text(title))
                row.set_subtitle(GLib.markup_escape_text(subtitle))
        self.driver_row.set_subtitle(gpu.get("driver_version") or "-")
        self.cuda_row.set_subtitle(nvml.get("cuda_driver_version") or "-")
        self.sb_row.set_subtitle({"enabled": _("On"), "disabled": _("Off")}.get(gpu.get("secure_boot"), _("Unknown")))

    def install_runtime(self):
        # 본문은 마크업이다(약관 링크). 번역된 문장은 이스케이프해서 넣는다.
        esc = GLib.markup_escape_text
        link = '<a href="%s">%s</a>' % (EULA_URL, esc(_("NVIDIA CUDA license agreement")))
        body = "\n\n".join(
            [
                esc(_("The CUDA runtime is NVIDIA software and is not part of Opereta. This computer downloads it directly from NVIDIA under NVIDIA's license. The download is about 360 MiB.")),
                _("Read the license before you continue: %s") % link,
                esc(_("NVIDIA's license for GeForce products does not allow datacenter deployment. Check NVIDIA's terms before you use it that way.")),
            ]
        )
        confirm(self.ctx.window, _("Download the CUDA runtime from NVIDIA?"), body, _("Download"),
                lambda: self._start("cuda_runtime"), body_markup=True)

    def _start(self, action):
        def done(_r):
            self.ctx.toast(_("Download started"))
            self.btn_install.set_sensitive(False)
            self.rt_row.set_subtitle(GLib.markup_escape_text(_("Downloading and installing…")))
            if self._poll is None:
                self._poll = GLib.timeout_add_seconds(3, self._tick)

        run_async(lambda: self.ctx.client.update_start(action), done, self.ctx.toast_error)

    def restart_server(self):
        server = (self.ctx.status or {}).get("server") or {}
        model = server.get("model_id")
        if not model:
            self.ctx.toast(_("Start a model from the Server page."))
            return

        def work():
            self.ctx.client.stop()
            return self.ctx.client.start(model)

        run_async(work, lambda _r: self.ctx.toast(_("The model server is restarting")), self.ctx.toast_error)

    def gpu_check(self):
        self.ctx.toast(_("Running the GPU check…"))
        run_async(
            self.ctx.client.gpu_check,
            lambda r: (self.ctx.toast(_("GPU check finished (exit code %d)") % r.get("exit_code", -1)), self.ctx.refresh()),
            self.ctx.toast_error,
        )
