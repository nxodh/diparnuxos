"""자원 페이지: CPU, 메모리, 디스크, 네트워크 속도, 온도, GPU. 페이지가 보이는 동안에만 읽는다."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, run_async  # noqa: E402

REFRESH_SECONDS = 3


class ResourcesPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Resources"), icon_name="utilities-system-monitor-symbolic")
        self.ctx = ctx
        self._timer = 0
        self._busy = False
        self._rows = {}
        self.system = Adw.PreferencesGroup(title=_("This computer"))
        self.cpu_row = action_row(_("Processor"), "-")
        self.mem_row = action_row(_("Memory"), "-")
        self.up_row = action_row(_("Running for"), "-")
        for r in (self.cpu_row, self.mem_row, self.up_row):
            self.system.add(r)
        self.add(self.system)
        self.disks_group = Adw.PreferencesGroup(title=_("Disks"))
        self.net_group = Adw.PreferencesGroup(title=_("Network"))
        self.temp_group = Adw.PreferencesGroup(title=_("Temperatures"))
        self.gpu_group = Adw.PreferencesGroup(title=_("Graphics card"))
        self.top_group = Adw.PreferencesGroup(title=_("Using the most memory"))
        for g in (self.disks_group, self.net_group, self.temp_group, self.gpu_group, self.top_group):
            self.add(g)
            self._rows[g] = []
        self.connect("map", lambda *_a: self._start())
        self.connect("unmap", lambda *_a: self._stop())

    def _start(self):
        self.refresh()
        if not self._timer:
            self._timer = GLib.timeout_add_seconds(REFRESH_SECONDS, lambda: (self.refresh(), GLib.SOURCE_CONTINUE)[1])

    def _stop(self):
        if self._timer:
            GLib.source_remove(self._timer)
            self._timer = 0

    def refresh(self):
        if self._busy:
            return
        self._busy = True

        def done(result):
            self._busy = False
            self.render(result)

        run_async(self.ctx.client.resources, done, lambda e: setattr(self, "_busy", False))

    def _fill(self, group, items):
        clear_group(group, self._rows[group])
        group.set_visible(bool(items))
        for title, sub in items:
            row = action_row(title, sub)
            group.add(row)
            self._rows[group].append(row)

    def render(self, result):
        s = result.get("system") or {}
        cpu = s.get("cpu") or {}
        mem = s.get("memory") or {}
        self.cpu_row.set_subtitle(
            _("%(used).1f%% of %(cores)d cores · load %(l1).2f %(l5).2f %(l15).2f")
            % {"used": cpu.get("used_percent", 0.0), "cores": cpu.get("cores", 0), "l1": cpu.get("load1", 0.0), "l5": cpu.get("load5", 0.0), "l15": cpu.get("load15", 0.0)}
        )
        text = _("%(used)s used of %(total)s") % {"used": logic.fmt_mib(mem.get("used_mib", -1)), "total": logic.fmt_mib(mem.get("total_mib", -1))}
        if mem.get("swap_total_mib"):
            text += " · " + _("swap %s") % logic.fmt_mib(mem.get("swap_used_mib", 0))
        self.mem_row.set_subtitle(text)
        self.up_row.set_subtitle(logic.fmt_uptime(s.get("uptime_seconds")))
        self._fill(
            self.disks_group,
            [
                (d.get("mount", "?"), _("%(used)s used of %(total)s (%(pct)d%%)") % {"used": logic.fmt_mib(d.get("used_mib", 0)), "total": logic.fmt_mib(d.get("total_mib", 0)), "pct": d.get("used_percent", 0)})
                for d in s.get("disks") or []
            ],
        )
        self._fill(
            self.net_group,
            [
                (n.get("name", "?"), _("in %(rx)s · out %(tx)s") % {"rx": logic.fmt_rate(n.get("rx_bytes_per_second", 0)), "tx": logic.fmt_rate(n.get("tx_bytes_per_second", 0))})
                for n in s.get("network") or []
            ],
        )
        self._fill(self.temp_group, [(t.get("name", "?"), "%.1f °C" % t.get("celsius", 0.0)) for t in s.get("temperatures") or []])
        gpus = result.get("gpus") or []
        self._fill(
            self.gpu_group,
            [
                (
                    g.get("name", "GPU %s" % g.get("index")),
                    _("%(used)s of %(total)s · %(busy)d%% busy · %(temp)d °C · %(power).0f W")
                    % {
                        "used": logic.fmt_mib(g.get("memory_used_mib", 0)),
                        "total": logic.fmt_mib(g.get("memory_total_mib", 0)),
                        "busy": g.get("util_gpu_percent", 0),
                        "temp": g.get("temperature_c", 0),
                        "power": (g.get("power_mw", 0) or 0) / 1000.0,
                    },
                )
                for g in gpus
            ],
        )
        self._fill(
            self.top_group,
            [("%s (%s)" % (p.get("name", "?"), p.get("pid", "")), logic.fmt_mib(p.get("rss_mib", 0))) for p in s.get("top_processes") or []],
        )
