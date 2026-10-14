"""서비스 페이지: 허용된 서비스의 상태, 시작·정지·다시 시작, 로그."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, confirm, icon_button, run_async, text_button  # noqa: E402

_ACTION_LABELS = {"start": "Start", "stop": "Stop", "restart": "Restart"}


class LogDialog(Adw.Dialog):
    """서비스 로그 마지막 줄을 보여 준다."""

    def __init__(self, ctx, unit, title):
        super().__init__(title=title, content_width=720, content_height=520)
        self.ctx = ctx
        self.unit = unit
        header = Adw.HeaderBar()
        header.pack_end(text_button(_("Refresh"), self.load))
        self.view = Gtk.TextView(editable=False, monospace=True, wrap_mode=Gtk.WrapMode.WORD_CHAR)
        scroller = Gtk.ScrolledWindow(vexpand=True, hexpand=True)
        scroller.set_child(self.view)
        tv = Adw.ToolbarView()
        tv.add_top_bar(header)
        tv.set_content(scroller)
        self.set_child(tv)
        self.load()

    def load(self):
        def show(result):
            self.view.get_buffer().set_text(result.get("log") or _("No log lines."))

        run_async(lambda: self.ctx.client.service_logs(self.unit, 300), show, self.ctx.toast_error)


class ServicesPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Services"), icon_name="system-run-symbolic")
        self.ctx = ctx
        self._rows = []
        self._timer = 0
        self.group = Adw.PreferencesGroup(
            title=_("Managed services"),
            description=_("Only these services can be controlled here. The Opereta daemon itself can be viewed but not stopped."),
        )
        self.group.set_header_suffix(text_button(_("Refresh"), self.reload))
        self.add(self.group)
        self.connect("map", lambda *_a: self._start())
        self.connect("unmap", lambda *_a: self._stop())

    def _start(self):
        self.reload()
        if not self._timer:
            self._timer = GLib.timeout_add_seconds(10, lambda: (self.reload(), GLib.SOURCE_CONTINUE)[1])

    def _stop(self):
        if self._timer:
            GLib.source_remove(self._timer)
            self._timer = 0

    def reload(self):
        run_async(self.ctx.client.services, self._render, lambda e: None)

    def _render(self, result):
        clear_group(self.group, self._rows)
        for svc in result.get("services") or []:
            sub = [logic.service_state_text(svc)]
            if svc.get("installed", True):
                sub.append(_("at boot: %s") % svc.get("enabled", "-"))
                if svc.get("memory_mib"):
                    sub.append(logic.fmt_mib(svc["memory_mib"]))
            row = action_row(svc.get("title") or svc.get("unit", "?"), " · ".join(sub))
            if svc.get("installed", True):
                row.add_suffix(icon_button("utilities-terminal-symbolic", _("Show the log"), lambda u=svc["unit"], t=svc.get("title", ""): self.show_log(u, t)))
                actions = svc.get("actions") or []
                for act in ("start", "stop", "restart"):
                    if act in actions:
                        row.add_suffix(text_button(_(_ACTION_LABELS[act]), lambda u=svc["unit"], a=act, t=svc.get("title", ""): self.act(u, a, t)))
                if "enable" in actions and "disable" in actions:
                    on = svc.get("enabled") == "enabled"
                    switch = Gtk.Switch(valign=Gtk.Align.CENTER, active=on, tooltip_text=_("Start at boot"))
                    switch.connect("notify::active", lambda sw, _p, u=svc["unit"]: self._boot(u, sw.get_active()))
                    row.add_suffix(switch)
            self.group.add(row)
            self._rows.append(row)

    def show_log(self, unit, title):
        LogDialog(self.ctx, unit, title or unit).present(self.ctx.window)

    def act(self, unit, action, title):
        def go():
            run_async(
                lambda: self.ctx.client.service_action(unit, action),
                lambda _r: (self.ctx.toast(_("Done")), self.reload()),
                lambda e: (self.ctx.toast_error(e), self.reload()),
            )

        if action == "stop":
            confirm(self.ctx.window, _("Stop this service?"), title or unit, _("Stop"), go, destructive=True)
        else:
            go()

    def _boot(self, unit, on):
        run_async(
            lambda: self.ctx.client.service_action(unit, "enable" if on else "disable"),
            lambda _r: (self.ctx.toast(_("Saved")), self.reload()),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )
