"""이 컴퓨터: 시스템 이름과 버전, 하드웨어, 가동 시간."""

import os
import socket

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib  # noqa: E402

from . import __version__, logic, sysutil  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, run_async  # noqa: E402


class AboutPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("This computer"), icon_name="computer-symbolic")
        self.ctx = ctx
        self.sys_group = Adw.PreferencesGroup(title=_("System"))
        self.hw_group = Adw.PreferencesGroup(title=_("Hardware"))
        self.add(self.sys_group)
        self.add(self.hw_group)
        self.rows = {}
        for group, key, title in (
            (self.sys_group, "os", _("Operating system")),
            (self.sys_group, "kernel", _("Kernel")),
            (self.sys_group, "host", _("Host name")),
            (self.sys_group, "cc", _("Control Center")),
            (self.sys_group, "agent", _("Deberopa")),
            (self.hw_group, "cpu", _("Processor")),
            (self.hw_group, "ram", _("Memory")),
            (self.hw_group, "gpu", _("Graphics")),
            (self.hw_group, "uptime", _("Running for")),
        ):
            row = action_row(title, "-")
            group.add(row)
            self.rows[key] = row
        self.connect("map", lambda *_a: self.refresh())
        ctx.subscribe(lambda _s, _e: self._gpu())

    def _set(self, key, text):
        self.rows[key].set_subtitle(GLib.markup_escape_text(text or "-"))

    def refresh(self):
        osr = sysutil.os_release()
        self._set("os", osr.get("PRETTY_NAME") or osr.get("NAME") or "")
        self._set("kernel", os.uname().release)
        self._set("host", socket.gethostname())
        self._set("cc", __version__)
        self._set("cpu", sysutil.cpu_model())
        kib = sysutil.mem_total_kib()
        self._set("ram", logic.fmt_mib(kib // 1024) if kib else "")
        secs = sysutil.uptime_seconds()
        self._set("uptime", logic.fmt_uptime(secs) if secs else "")
        self._gpu()
        run_async(self.ctx.client.update_info, self._agent, lambda _e: None)

    def _agent(self, info):
        d = (info or {}).get("deberopa") or {}
        self._set("agent", d.get("installed") or _("Not installed"))
        self._set("os", self.rows["os"].get_subtitle() and (sysutil.os_release().get("PRETTY_NAME") or ""))

    def _gpu(self):
        rows = logic.device_rows(self.ctx.status)
        self._set("gpu", ", ".join(d["name"] for d in rows) if rows else _("No NVIDIA GPU was detected."))
