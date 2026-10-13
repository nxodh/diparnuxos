"""네트워크 페이지: 연결 상태, Wi-Fi 목록과 연결."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, confirm, error_text, run_async, text_button  # noqa: E402
from .wired import WiredSettings  # noqa: E402

# 페이지가 보이는 동안 상태를 다시 읽는 간격(초).
REFRESH_SECONDS = 5


class WifiPasswordDialog(Adw.Dialog):
    """보안 Wi-Fi 의 비밀번호를 받아 연결한다. 비밀번호는 이 대화상자 밖에 저장하지 않는다."""

    def __init__(self, ctx, ssid, on_connected=None):
        super().__init__(title=_("Connect to %s") % ssid, content_width=420)
        self.ctx = ctx
        self.ssid = ssid
        self._on_connected = on_connected
        self._busy = False

        header = Adw.HeaderBar()
        header.set_show_end_title_buttons(False)
        header.set_show_start_title_buttons(False)
        cancel = Gtk.Button(label=_("Cancel"))
        cancel.connect("clicked", lambda *_a: self.close())
        self.connect_btn = Gtk.Button(label=_("Connect"), css_classes=["suggested-action"])
        self.connect_btn.connect("clicked", lambda *_a: self._submit())
        header.pack_start(cancel)
        header.pack_end(self.connect_btn)

        group = Adw.PreferencesGroup()
        self.pw_row = Adw.PasswordEntryRow(title=_("Password"))
        self.pw_row.connect("changed", lambda *_a: self._validate())
        self.pw_row.connect("entry-activated", lambda *_a: self._submit())
        group.add(self.pw_row)
        self.message = Gtk.Label(xalign=0, wrap=True, margin_top=6)
        self.message.add_css_class("error")
        self.spinner = Gtk.Spinner()

        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6, margin_top=12, margin_bottom=12, margin_start=12, margin_end=12)
        box.append(group)
        box.append(self.message)
        box.append(self.spinner)
        view = Adw.ToolbarView()
        view.add_top_bar(header)
        view.set_content(box)
        self.set_child(view)
        self.set_default_widget(self.connect_btn)
        self._validate(quiet=True)

    def _validate(self, quiet=False):
        problem = logic.check_wifi_password(self.pw_row.get_text())
        self.connect_btn.set_sensitive(problem is None and not self._busy)
        touched = bool(self.pw_row.get_text())
        self.message.set_text(problem if (problem and touched and not quiet) else "")

    def _submit(self):
        if self._busy or logic.check_wifi_password(self.pw_row.get_text()) is not None:
            return
        self._busy = True
        self.connect_btn.set_sensitive(False)
        self.pw_row.set_sensitive(False)
        self.spinner.start()
        password = self.pw_row.get_text()

        def done(_r):
            self._busy = False
            self.spinner.stop()
            self.ctx.toast(_("Connected to %s") % self.ssid)
            if self._on_connected:
                self._on_connected()
            self.close()

        def failed(exc):
            self._busy = False
            self.spinner.stop()
            self.pw_row.set_sensitive(True)
            self.message.set_text(error_text(exc))
            self._validate(quiet=True)

        run_async(lambda: self.ctx.client.wifi_connect(self.ssid, password), done, failed)


class NetworkPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Network"), icon_name="network-wireless-symbolic")
        self.ctx = ctx
        self._timer = 0
        self._busy = False
        self._iface_rows = []
        self._wifi_rows = []
        self._signature = None
        self._updating = False

        self.status_group = Adw.PreferencesGroup(title=_("Connection"))
        self.summary_row = action_row(_("Internet"), "-")
        self.status_group.add(self.summary_row)
        self.add(self.status_group)

        self.iface_group = Adw.PreferencesGroup(title=_("Adapters"))
        self.add(self.iface_group)

        self.wired = WiredSettings(self, ctx)

        self.wifi_group = Adw.PreferencesGroup(title=_("Wi-Fi"))
        self.wifi_group.set_header_suffix(text_button(_("Scan"), self.scan))
        self.power_row = Adw.SwitchRow(title=_("Wi-Fi"))
        self.power_row.connect("notify::active", self._on_power)
        self.wifi_group.add(self.power_row)
        self.note_row = action_row(_("Note"), "")
        self.note_row.set_visible(False)
        self.wifi_group.add(self.note_row)
        self.add(self.wifi_group)

        self.networks_group = Adw.PreferencesGroup(title=_("Available networks"))
        self.add(self.networks_group)

        # 페이지가 보일 때만 주기적으로 읽는다.
        self.connect("map", lambda *_a: self._start())
        self.connect("unmap", lambda *_a: self._stop())

    # --- 갱신 ---
    def _start(self):
        if self._timer:
            return
        self.refresh()
        self._timer = GLib.timeout_add_seconds(REFRESH_SECONDS, self._tick)

    def _stop(self):
        if self._timer:
            GLib.source_remove(self._timer)
            self._timer = 0

    def _tick(self):
        self.refresh()
        return GLib.SOURCE_CONTINUE

    def refresh(self):
        if self._busy:
            return
        self._busy = True

        def work():
            net = self.ctx.client.network()
            nets = []
            if (net.get("wifi") or {}).get("available") and (net.get("wifi") or {}).get("powered"):
                try:
                    nets = self.ctx.client.wifi_networks().get("networks") or []
                except Exception:  # noqa: BLE001 - 목록을 못 읽어도 상태는 보인다
                    nets = []
            return net, nets

        self.wired.reload()

        def done(result):
            self._busy = False
            self._render(*result)

        def failed(exc):
            self._busy = False
            self.summary_row.set_subtitle(GLib.markup_escape_text(error_text(exc)))

        run_async(work, done, failed)

    def _render(self, net, nets):
        info = logic.describe_network(net)
        self.summary_row.set_subtitle(GLib.markup_escape_text(info["summary"]))

        # 어댑터 목록은 내용이 바뀔 때만 다시 만든다.
        ifaces = net.get("interfaces") or []
        iface_sig = tuple((i.get("name"), i.get("state"), tuple(i.get("addresses") or [])) for i in ifaces)
        if iface_sig != getattr(self, "_iface_sig", None):
            self._iface_sig = iface_sig
            clear_group(self.iface_group, self._iface_rows)
            for it in ifaces:
                kind = {"ethernet": _("Wired"), "wifi": _("Wi-Fi")}.get(it.get("kind"), _("Other"))
                state = {"up": _("Up"), "down": _("Down")}.get(it.get("state"), _("Unknown"))
                addr = ", ".join(it.get("addresses") or []) or "-"
                row = action_row("%s · %s" % (it.get("name", ""), kind), "%s · %s" % (state, addr))
                self.iface_group.add(row)
                self._iface_rows.append(row)
            if not ifaces:
                row = action_row(_("No network adapters"), "")
                self.iface_group.add(row)
                self._iface_rows.append(row)

        wifi = net.get("wifi") or {}
        self._updating = True
        self.power_row.set_visible(bool(wifi.get("available")))
        self.power_row.set_active(bool(wifi.get("powered")))
        self._updating = False
        self.note_row.set_visible(bool(info["wifi_note"]))
        self.note_row.set_subtitle(GLib.markup_escape_text(info["wifi_note"]))
        self.networks_group.set_visible(bool(wifi.get("available")) and bool(wifi.get("powered")))

        sig = tuple((n.get("ssid"), n.get("security"), n.get("signal") // 10, n.get("connected"), n.get("known")) for n in nets)
        if sig == self._signature:
            return
        self._signature = sig
        clear_group(self.networks_group, self._wifi_rows)
        if not nets:
            row = action_row(_("No networks found"), _("Press Scan to search again."))
            self.networks_group.add(row)
            self._wifi_rows.append(row)
            return
        for n in nets:
            self._add_network_row(n)

    def _add_network_row(self, n):
        ssid = n.get("ssid", "")
        security = n.get("security")
        parts = [logic.signal_label(n.get("signal"))]
        parts.append(_("Open") if security == "open" else _("Secured"))
        if n.get("connected"):
            parts.insert(0, _("Connected"))
        elif n.get("known"):
            parts.append(_("Saved"))
        row = action_row(ssid, " · ".join(p for p in parts if p))
        box = Gtk.Box(spacing=6, valign=Gtk.Align.CENTER)
        if n.get("connected"):
            box.append(text_button(_("Disconnect"), self.disconnect))
        else:
            box.append(text_button(_("Connect"), lambda s=ssid, sec=security, k=n.get("known"): self.connect_to(s, sec, k), "suggested-action"))
        if n.get("known"):
            box.append(text_button(_("Forget"), lambda s=ssid: self.forget(s)))
        row.add_suffix(box)
        self.networks_group.add(row)
        self._wifi_rows.append(row)

    # --- 동작 ---
    def scan(self):
        def done(_r):
            self.ctx.toast(_("Searching for networks…"))
            # 검색 결과가 나올 시간을 주고 다시 읽는다.
            GLib.timeout_add_seconds(4, lambda: (self.refresh(), GLib.SOURCE_REMOVE)[1])

        run_async(self.ctx.client.wifi_scan, done, self.ctx.toast_error)

    def connect_to(self, ssid, security, known):
        if security == "8021x":
            self.ctx.toast(_("Enterprise networks are not supported here."))
            return
        if security == "psk" and not known:
            WifiPasswordDialog(self.ctx, ssid, on_connected=self._after_change).present(self.ctx.window)
            return
        self.ctx.toast(_("Connecting to %s…") % ssid)

        def done(_r):
            self.ctx.toast(_("Connected to %s") % ssid)
            self._after_change()

        run_async(lambda: self.ctx.client.wifi_connect(ssid, ""), done, self.ctx.toast_error)

    def disconnect(self):
        run_async(self.ctx.client.wifi_disconnect, lambda _r: self._after_change(), self.ctx.toast_error)

    def forget(self, ssid):
        confirm(
            self.ctx.window,
            _("Forget this network?"),
            _("The saved password of %s is deleted.") % ssid,
            _("Forget"),
            lambda: run_async(lambda: self.ctx.client.wifi_forget(ssid), lambda _r: self._after_change(), self.ctx.toast_error),
            destructive=True,
        )

    def _on_power(self, row, _pspec):
        if self._updating:
            return
        on = row.get_active()
        run_async(lambda: self.ctx.client.wifi_power(on), lambda _r: self._after_change(), self.ctx.toast_error)

    def _after_change(self):
        self._signature = None
        self.refresh()
