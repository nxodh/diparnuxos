"""유선 설정 그룹: DHCP 또는 고정 IP, DNS, 호스트 이름, 깨우기, 연결 점검, 확인 대기 중인 변경."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, confirm, error_text, run_async, text_button  # noqa: E402

_MODES = ("dhcp", "static")


class WiredSettings:
    """NetworkPage 에 그룹을 붙이고 데몬의 /v1/network/config 로 채운다."""

    def __init__(self, page, ctx):
        self.page = page
        self.ctx = ctx
        self._updating = False
        self._editors = []
        self._pending_key = None

        # 확인 대기 중인 변경
        self.pending_group = Adw.PreferencesGroup(title=_("Change waiting for your confirmation"))
        self.pending_group.set_visible(False)
        self.pending_row = action_row(_("Keep the new settings?"), "")
        buttons = Gtk.Box(spacing=6, valign=Gtk.Align.CENTER)
        buttons.append(text_button(_("Keep"), self.keep, "suggested-action"))
        buttons.append(text_button(_("Undo"), self.undo))
        self.pending_row.add_suffix(buttons)
        self.pending_group.add(self.pending_row)
        page.add(self.pending_group)

        # 유선 어댑터 설정
        self.wired_group = Adw.PreferencesGroup(
            title=_("Wired settings"),
            description=_("Change how a wired adapter gets its address. If you cannot confirm the change in time, the old settings come back by themselves."),
        )
        page.add(self.wired_group)
        self._wired_rows = []

        # 이름과 깨우기
        self.name_group = Adw.PreferencesGroup(title=_("This computer on the network"))
        self.host_row = Adw.EntryRow(title=_("Host name"))
        self.host_row.set_show_apply_button(True)
        self.host_row.connect("apply", lambda *_a: self.apply_hostname())
        self.name_group.add(self.host_row)
        self.wol_row = Adw.SwitchRow(title=_("Wake on LAN"))
        self.wol_row.set_subtitle(_("Let another computer wake this one with a magic packet. It applies from the next start and needs support in the adapter and the firmware."))
        self.wol_row.connect("notify::active", self._on_wol)
        self.name_group.add(self.wol_row)
        page.add(self.name_group)

        # 점검
        self.diag_group = Adw.PreferencesGroup(title=_("Check the connection"))
        self.diag_row = action_row(_("Test the address, route, name lookup and internet"), "")
        self.diag_btn = text_button(_("Run the check"), self.diagnose)
        self.diag_row.add_suffix(self.diag_btn)
        self.diag_group.add(self.diag_row)
        page.add(self.diag_group)
        self._diag_rows = []

    # --- 읽기 ---
    def reload(self):
        run_async(self.ctx.client.network_config, self._render, lambda e: None)

    def _render(self, cfg):
        self._updating = True
        self.host_row.set_text(cfg.get("hostname") or "")
        self.wol_row.set_active(bool(cfg.get("wake_on_lan")))
        self._updating = False

        pending = cfg.get("pending")
        self.pending_group.set_visible(bool(pending))
        if pending:
            self.pending_row.set_subtitle(
                _("The change to %(iface)s is applied. It is undone at %(when)s unless you keep it.")
                % {"iface": pending.get("interface", "?"), "when": pending.get("deadline", "")}
            )

        ifaces = cfg.get("interfaces") or []
        key = repr([(i.get("interface"), i.get("mode"), tuple(i.get("addresses") or []), i.get("gateway"), tuple(i.get("dns") or [])) for i in ifaces])
        if key == self._pending_key:
            return
        self._pending_key = key
        clear_group(self.wired_group, self._wired_rows)
        if not ifaces:
            row = action_row(_("No wired adapter"), _("Wi-Fi addresses are assigned automatically."))
            self.wired_group.add(row)
            self._wired_rows.append(row)
        for it in ifaces:
            exp = self._editor(it)
            self.wired_group.add(exp)
            self._wired_rows.append(exp)

    def _editor(self, it):
        name = it.get("interface", "?")
        exp = Adw.ExpanderRow(title=name, subtitle=self._summary(it))
        mode = Adw.ComboRow(title=_("Address"))
        mode.set_model(Gtk.StringList.new([_("Automatic (DHCP)"), _("Fixed address")]))
        mode.set_selected(1 if it.get("mode") == "static" else 0)
        address = Adw.EntryRow(title=_("Address with prefix, such as 192.168.0.50/24"))
        address.set_text(", ".join(it.get("addresses") or []))
        gateway = Adw.EntryRow(title=_("Gateway"))
        gateway.set_text(it.get("gateway") or "")
        dns = Adw.EntryRow(title=_("DNS servers, separated by commas"))
        dns.set_text(", ".join(it.get("dns") or []))
        domains = Adw.EntryRow(title=_("Search domains, separated by commas"))
        domains.set_text(", ".join(it.get("domains") or []))

        def sync(*_a):
            static = mode.get_selected() == 1
            address.set_visible(static)
            gateway.set_visible(static)

        mode.connect("notify::selected", sync)
        sync()
        for r in (mode, address, gateway, dns, domains):
            exp.add_row(r)
        apply_btn = text_button(
            _("Apply"),
            lambda: self.apply(name, _MODES[mode.get_selected()], address.get_text(), gateway.get_text(), dns.get_text(), domains.get_text()),
            "suggested-action",
        )
        exp.add_row(action_row("", "", apply_btn))
        return exp

    @staticmethod
    def _summary(it):
        if it.get("mode") == "static":
            text = _("Fixed address %s") % ", ".join(it.get("addresses") or [])
            if it.get("gateway"):
                text += " · " + _("gateway %s") % it["gateway"]
            return text
        return _("Automatic (DHCP)")

    # --- 동작 ---
    def apply(self, name, mode, address, gateway, dns, domains):
        addresses = logic.split_list(address) if mode == "static" else []
        if mode == "static" and not addresses:
            self.ctx.toast(_("Enter an address with its prefix length, such as 192.168.0.50/24."))
            return

        def go():
            def done(result):
                self.ctx.toast(_("Applied. Keep the change within %d seconds or it is undone.") % result.get("confirm_seconds", 60), timeout=8)
                self._pending_key = None
                self.reload()

            def failed(exc):
                self.ctx.toast(error_text(exc), timeout=8)
                self.reload()

            run_async(
                lambda: self.ctx.client.set_wired(name, mode, addresses, gateway.strip(), logic.split_list(dns), logic.split_list(domains)),
                done,
                failed,
            )

        if mode == "static":
            confirm(
                self.ctx.window,
                _("Change the address of %s?") % name,
                _("You may lose the connection to this computer. If you do not keep the change within a minute it is undone automatically."),
                _("Apply"),
                go,
            )
        else:
            go()

    def keep(self):
        run_async(self.ctx.client.confirm_network, lambda _r: (self.ctx.toast(_("Kept")), self.reload()), self.ctx.toast_error)

    def undo(self):
        run_async(self.ctx.client.revert_network, lambda _r: (self.ctx.toast(_("Undone")), self._reset_and_reload()), self.ctx.toast_error)

    def _reset_and_reload(self):
        self._pending_key = None
        self.reload()

    def apply_hostname(self):
        name = self.host_row.get_text().strip().lower()
        if not name:
            return
        run_async(
            lambda: self.ctx.client.set_hostname(name),
            lambda _r: self.ctx.toast(_("Host name changed to %s") % name),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def _on_wol(self, row, _pspec):
        if self._updating:
            return
        want = row.get_active()
        run_async(
            lambda: self.ctx.client.set_wol(want),
            lambda _r: self.ctx.toast(_("Saved. It applies from the next start.")),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def diagnose(self):
        self.diag_btn.set_sensitive(False)
        self.diag_row.set_subtitle(_("Checking…"))

        def done(result):
            self.diag_btn.set_sensitive(True)
            self.diag_row.set_subtitle("")
            clear_group(self.diag_group, self._diag_rows)
            titles = {
                "address": _("Address"),
                "route": _("Route to the network"),
                "gateway": _("Gateway answers"),
                "dns": _("Name lookup"),
                "internet": _("Internet"),
            }
            for ch in result.get("checks") or []:
                state = _("skipped") if ch.get("skipped") else (_("OK") if ch.get("ok") else _("Problem"))
                row = action_row(titles.get(ch.get("name"), ch.get("name", "?")), "%s · %s" % (state, GLib.markup_escape_text(ch.get("detail", ""))))
                self.diag_group.add(row)
                self._diag_rows.append(row)

        def failed(exc):
            self.diag_btn.set_sensitive(True)
            self.diag_row.set_subtitle("")
            self.ctx.toast_error(exc)

        run_async(self.ctx.client.diagnose, done, failed)
