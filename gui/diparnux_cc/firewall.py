"""방화벽 페이지: 기능이 여는 포트(읽기 전용)와 사용자 규칙."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, confirm, icon_button, run_async, text_button  # noqa: E402

_PROTOCOLS = ("tcp", "udp")
_SOURCES = ("lan", "any", "custom")


class FirewallPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Firewall"), icon_name="security-high-symbolic")
        self.ctx = ctx
        self._builtin_rows = []
        self._rule_rows = []

        self.policy_group = Adw.PreferencesGroup(
            title=_("Incoming connections"),
            description=_("Everything that is not listed here is blocked. Answers to connections this computer starts always work."),
        )
        self.add(self.policy_group)

        self.builtin_group = Adw.PreferencesGroup(
            title=_("Opened by features"),
            description=_("These ports open and close with the feature that needs them."),
        )
        self.add(self.builtin_group)

        self.rules_group = Adw.PreferencesGroup(title=_("Your rules"))
        self.add(self.rules_group)

        add_group = Adw.PreferencesGroup(title=_("Add a rule"))
        self.name_row = Adw.EntryRow(title=_("Name"))
        self.proto_row = Adw.ComboRow(title=_("Protocol"))
        self.proto_row.set_model(Gtk.StringList.new(["TCP", "UDP"]))
        self.port_row = Adw.EntryRow(title=_("Port or range, such as 3000 or 6000-6010"))
        self.source_row = Adw.ComboRow(title=_("Who may connect"))
        self.source_row.set_model(Gtk.StringList.new([_("The local network"), _("Any address"), _("An address range")]))
        self.cidr_row = Adw.EntryRow(title=_("Address range, such as 192.168.1.0/24"))
        self.cidr_row.set_visible(False)
        self.source_row.connect("notify::selected", lambda *_a: self.cidr_row.set_visible(self.source_row.get_selected() == 2))
        for row in (self.name_row, self.proto_row, self.port_row, self.source_row, self.cidr_row):
            add_group.add(row)
        self.add_btn = text_button(_("Add rule"), self.add_rule, "suggested-action")
        add_group.add(action_row("", "", self.add_btn))
        self.add(add_group)

        self.connect("map", lambda *_a: self.reload())
        self.reload()

    def reload(self):
        run_async(self.ctx.client.firewall, self._render, self.ctx.toast_error)

    def _render(self, fw):
        clear_group(self.builtin_group, self._builtin_rows)
        clear_group(self.rules_group, self._rule_rows)
        built = fw.get("builtin") or []
        if not built:
            row = action_row(_("Nothing is open"), _("Turn on LAN exposure, SSH or another feature to open its port."))
            self.builtin_group.add(row)
            self._builtin_rows.append(row)
        for o in built:
            sub = "%s · %s" % (logic.rule_subtitle(o), o.get("reason", ""))
            row = action_row(o.get("name", "?"), sub)
            self.builtin_group.add(row)
            self._builtin_rows.append(row)
        rules = fw.get("rules") or []
        if not rules:
            row = action_row(_("No rules yet"), _("Add one below, for example to let a web interface on this computer be reached."))
            self.rules_group.add(row)
            self._rule_rows.append(row)
        for r in rules:
            row = Adw.ActionRow(title=r.get("name", "?"), subtitle=logic.rule_subtitle(r))
            switch = Gtk.Switch(valign=Gtk.Align.CENTER, active=bool(r.get("enabled")))
            switch.connect("notify::active", lambda sw, _p, rule=r: self._toggle(rule, sw.get_active()))
            row.add_suffix(switch)
            row.add_suffix(icon_button("user-trash-symbolic", _("Delete"), lambda rid=r["id"], nm=r.get("name", ""): self.delete(rid, nm), "destructive-action"))
            self.rules_group.add(row)
            self._rule_rows.append(row)

    def _toggle(self, rule, on):
        updated = dict(rule)
        updated["enabled"] = on
        run_async(
            lambda: self.ctx.client.update_firewall_rule(updated),
            lambda _r: self.ctx.toast(_("Rule saved")),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def delete(self, rule_id, name):
        def go():
            run_async(
                lambda: self.ctx.client.delete_firewall_rule(rule_id),
                lambda _r: (self.ctx.toast(_("Rule deleted")), self.reload()),
                self.ctx.toast_error,
            )

        confirm(self.ctx.window, _("Delete this rule?"), name, _("Delete"), go, destructive=True)

    def add_rule(self):
        name = self.name_row.get_text().strip()
        port_text = self.port_row.get_text().strip()
        try:
            first, _sep, last = port_text.partition("-")
            port, port_end = int(first), int(last) if last else 0
        except ValueError:
            self.ctx.toast(_("Enter a port number or a range such as 6000-6010."))
            return
        source = _SOURCES[self.source_row.get_selected()]
        if source == "custom":
            source = self.cidr_row.get_text().strip()
        if not name or not source:
            self.ctx.toast(_("Give the rule a name."))
            return
        proto = _PROTOCOLS[self.proto_row.get_selected()]

        def done(_r):
            self.name_row.set_text("")
            self.port_row.set_text("")
            self.ctx.toast(_("Rule added"))
            self.reload()

        run_async(lambda: self.ctx.client.add_firewall_rule(name, proto, port, port_end, source), done, self.ctx.toast_error)
