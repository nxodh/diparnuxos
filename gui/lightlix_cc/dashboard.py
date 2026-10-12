"""대시보드: GPU, 드라이버, 메모리, 서버 상태."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group  # noqa: E402


class DashboardPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Dashboard"), icon_name="utilities-system-monitor-symbolic")
        self.ctx = ctx

        self.gpu_group = Adw.PreferencesGroup(title=_("Graphics cards"))
        self.driver_group = Adw.PreferencesGroup(title=_("Driver"))
        self.system_group = Adw.PreferencesGroup(title=_("System"))
        self.server_group = Adw.PreferencesGroup(title=_("LLM server"))
        for group in (self.gpu_group, self.driver_group, self.system_group, self.server_group):
            self.add(group)

        self._gpu_rows = []
        self.driver_row = action_row(_("Driver version"), "-")
        self.module_row = action_row(_("Kernel module"), "-")
        self.cuda_row = action_row(_("CUDA version"), "-")
        self.sb_row = action_row(_("Secure Boot"), "-")
        for row in (self.driver_row, self.module_row, self.cuda_row, self.sb_row):
            self.driver_group.add(row)

        self.ram_row = action_row(_("Memory"), "-")
        self.ram_bar = Gtk.LevelBar(min_value=0, max_value=1, value=0, valign=Gtk.Align.CENTER)
        self.ram_bar.set_size_request(120, -1)
        self.ram_row.add_suffix(self.ram_bar)
        self.mode_row = action_row(_("Session mode"), "-")
        self.system_group.add(self.ram_row)
        self.system_group.add(self.mode_row)

        self.state_row = action_row(_("State"), "-")
        self.model_row = action_row(_("Model"), "-")
        self.offload_row = action_row(_("GPU offload"), "-")
        for row in (self.state_row, self.model_row, self.offload_row):
            self.server_group.add(row)

        ctx.subscribe(self.update)

    def update(self, status, error):
        if status is None:
            return
        gpu = status.get("gpu") or {}

        clear_group(self.gpu_group, self._gpu_rows)
        rows = logic.device_rows(status)
        if not rows:
            row = action_row(_("No GPU"), _("No NVIDIA GPU was detected."))
            self.gpu_group.add(row)
            self._gpu_rows.append(row)
        for d in rows:
            row = action_row(d["name"], logic.device_subtitle(d))
            self.gpu_group.add(row)
            self._gpu_rows.append(row)

        nvml = gpu.get("nvml") or {}
        self.driver_row.set_subtitle(gpu.get("driver_version") or "-")
        open_module = gpu.get("open_module")
        self.module_row.set_subtitle(
            {"yes": _("NVIDIA open kernel module"), "no": _("Proprietary module")}.get(open_module, _("Unknown"))
        )
        self.cuda_row.set_subtitle(nvml.get("cuda_driver_version") or "-")
        self.sb_row.set_subtitle(
            {"enabled": _("On"), "disabled": _("Off")}.get(gpu.get("secure_boot"), _("Unknown"))
        )

        mem = logic.memory_summary(status)
        if mem:
            self.ram_row.set_subtitle(
                _("%(used)s used of %(total)s") % {"used": logic.fmt_mib(mem["used"]), "total": logic.fmt_mib(mem["total"])}
            )
            self.ram_bar.set_value(mem["used"] / float(mem["total"]))
        else:
            self.ram_row.set_subtitle("-")
            self.ram_bar.set_value(0)
        self.mode_row.set_subtitle(
            {"desktop": _("Desktop mode"), "server": _("Server mode")}.get(status.get("mode"), _("Unknown"))
        )

        server = status.get("server") or {}
        self.state_row.set_subtitle(logic.server_state_label(server.get("state")))
        self.model_row.set_subtitle(server.get("model_id") or "-")
        offload = server.get("offload") or {}
        if offload.get("total"):
            text = _("%(n)d of %(total)d layers on the GPU") % {"n": offload.get("offloaded", 0), "total": offload["total"]}
        else:
            text = offload.get("note") or "-"
        self.offload_row.set_subtitle(text)
