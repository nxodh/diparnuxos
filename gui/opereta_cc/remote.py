"""원격 접속 페이지: SSH 고급 설정과 공개 키, 원격 데스크톱, VPN, 원격 관리 웹 페이지."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
gi.require_version("Gdk", "4.0")
from gi.repository import Adw, Gdk, Gtk  # noqa: E402

from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, confirm, icon_button, run_async, text_button  # noqa: E402

_SOURCES = ("lan", "any")


class TextDialog(Adw.Dialog):
    """한 번만 보이는 설정(VPN 클라이언트 설정)을 보여 주고 복사하게 한다."""

    def __init__(self, ctx, title, intro, text):
        super().__init__(title=title, content_width=560, content_height=460)
        self.ctx = ctx
        self.text = text
        header = Adw.HeaderBar()
        header.pack_end(text_button(_("Copy"), self.copy, "suggested-action"))
        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=8, margin_top=12, margin_bottom=12, margin_start=12, margin_end=12)
        box.append(Gtk.Label(label=intro, wrap=True, xalign=0))
        view = Gtk.TextView(editable=False, monospace=True, wrap_mode=Gtk.WrapMode.WORD_CHAR)
        view.get_buffer().set_text(text)
        scroller = Gtk.ScrolledWindow(vexpand=True)
        scroller.set_child(view)
        box.append(scroller)
        tv = Adw.ToolbarView()
        tv.add_top_bar(header)
        tv.set_content(box)
        self.set_child(tv)

    def copy(self):
        Gdk.Display.get_default().get_clipboard().set(self.text)
        self.ctx.toast(_("Copied"))


class RemotePage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Remote access"), icon_name="network-workgroup-symbolic")
        self.ctx = ctx
        self._updating = False
        self._key_rows = []
        self._peer_rows = []
        self._host_rows = []
        self._log_rows = []
        self._users = []

        # --- SSH ---
        self.ssh_group = Adw.PreferencesGroup(
            title=_("SSH"),
            description=_("Open a terminal on this computer from another computer. Turn the server on or off in System."),
        )
        self.ssh_state = action_row(_("Server"), "-")
        self.ssh_group.add(self.ssh_state)
        self.ssh_port_row = Adw.SpinRow.new_with_range(1, 65535, 1)
        self.ssh_port_row.set_title(_("Port"))
        self.ssh_port_row.set_value(22)
        apply_port = text_button(_("Apply"), self.apply_ssh_port)
        self.ssh_port_row.add_suffix(apply_port)
        self.ssh_group.add(self.ssh_port_row)
        self.ssh_pw_row = Adw.SwitchRow(title=_("Allow password login"))
        self.ssh_pw_row.set_subtitle(_("Turn it off to accept public keys only. At least one key must be registered first."))
        self.ssh_pw_row.connect("notify::active", self._on_ssh_password)
        self.ssh_group.add(self.ssh_pw_row)
        self.add(self.ssh_group)

        self.keys_group = Adw.PreferencesGroup(title=_("Public keys"))
        self.user_row = Adw.ComboRow(title=_("Account"))
        self.user_row.set_model(Gtk.StringList.new([]))
        self.user_row.connect("notify::selected", lambda *_a: self.load_keys())
        self.keys_group.add(self.user_row)
        self.key_entry = Adw.EntryRow(title=_("Paste a public key (ssh-ed25519 AAAA…) and press Enter"))
        self.key_entry.set_show_apply_button(True)
        self.key_entry.connect("apply", lambda *_a: self.add_key())
        self.keys_group.add(self.key_entry)
        self.add(self.keys_group)

        self.host_group = Adw.PreferencesGroup(
            title=_("Server fingerprints"),
            description=_("Compare one of these with the fingerprint your SSH client shows the first time you connect."),
        )
        self.add(self.host_group)

        self.log_group = Adw.PreferencesGroup(title=_("Recent logins"))
        self.log_group.set_header_suffix(text_button(_("Refresh"), self.load_log))
        self.add(self.log_group)

        # --- 원격 데스크톱 ---
        self.rdp_group = Adw.PreferencesGroup(
            title=_("Remote desktop"),
            description=_("Open this computer's desktop from another computer with an RDP client. It is off by default."),
        )
        self.rdp_row = Adw.SwitchRow(title=_("Allow remote desktop"))
        self.rdp_row.connect("notify::active", self._on_rdp)
        self.rdp_group.add(self.rdp_row)
        self.rdp_source = Adw.ComboRow(title=_("Who may connect"))
        self.rdp_source.set_model(Gtk.StringList.new([_("The local network"), _("Any address")]))
        self.rdp_source.connect("notify::selected", self._on_rdp_source)
        self.rdp_group.add(self.rdp_source)
        self.rdp_note = action_row(_("Note"), "")
        self.rdp_note.set_visible(False)
        self.rdp_group.add(self.rdp_note)
        self.add(self.rdp_group)

        # --- VPN ---
        self.vpn_group = Adw.PreferencesGroup(
            title=_("VPN (WireGuard)"),
            description=_("Reach this computer from outside your home network. Devices that join are trusted like the local network."),
        )
        self.vpn_row = Adw.SwitchRow(title=_("Turn the VPN on"))
        self.vpn_row.connect("notify::active", self._on_vpn)
        self.vpn_group.add(self.vpn_row)
        self.vpn_info = action_row(_("This computer"), "-")
        self.vpn_group.add(self.vpn_info)
        self.add(self.vpn_group)

        self.peers_group = Adw.PreferencesGroup(title=_("VPN devices"))
        self.peer_entry = Adw.EntryRow(title=_("Name of a new device, such as phone"))
        self.peer_entry.set_show_apply_button(True)
        self.peer_entry.connect("apply", lambda *_a: self.add_peer())
        self.peers_group.add(self.peer_entry)
        self.add(self.peers_group)

        # --- 웹 관리 ---
        self.web_group = Adw.PreferencesGroup(
            title=_("Admin web page"),
            description=_("Manage this computer from another computer's browser. It has its own password and is off by default."),
        )
        self.web_row = Adw.SwitchRow(title=_("Turn the admin web page on"))
        self.web_row.connect("notify::active", self._on_web)
        self.web_group.add(self.web_row)
        self.web_pw = Adw.PasswordEntryRow(title=_("New web admin password (at least 10 characters)"))
        self.web_pw.set_show_apply_button(True)
        self.web_pw.connect("apply", lambda *_a: self.set_web_password())
        self.web_group.add(self.web_pw)
        self.web_info = action_row(_("Address"), "-")
        self.web_group.add(self.web_info)
        self.add(self.web_group)

        self.connect("map", lambda *_a: self.reload())

    # --- 읽기 ---
    def reload(self):
        run_async(self.ctx.client.ssh_advanced, self._render_ssh, lambda e: None)
        run_async(self.ctx.client.users, self._render_users, lambda e: None)
        run_async(self.ctx.client.remote_desktop, self._render_rdp, lambda e: None)
        run_async(self.ctx.client.vpn, self._render_vpn, lambda e: None)
        run_async(self.ctx.client.webadmin, self._render_web, lambda e: None)
        self.load_log()

    def _render_ssh(self, a):
        self._updating = True
        self.ssh_state.set_subtitle(_("On") if a.get("enabled") else _("Off"))
        self.ssh_port_row.set_value(a.get("port", 22))
        configured = a.get("password_login_configured")
        self.ssh_pw_row.set_active(True if configured is None else bool(configured))
        self._updating = False
        clear_group(self.host_group, self._host_rows)
        for k in a.get("host_keys") or []:
            row = action_row(k.get("fingerprint", "?"), k.get("type", ""))
            row.add_suffix(icon_button("edit-copy-symbolic", _("Copy"), lambda t=k.get("fingerprint", ""): self._copy(t)))
            self.host_group.add(row)
            self._host_rows.append(row)
        if not a.get("host_keys"):
            row = action_row(_("No host keys yet"), _("They are created the first time SSH is turned on."))
            self.host_group.add(row)
            self._host_rows.append(row)

    def _render_users(self, result):
        # SystemPage 의 계정 목록과 달리 키를 관리할 수 있는 로그인 계정은 데몬이 판단한다. 여기서는 만든 계정과 live 계정을 보인다.
        names = [u.get("name") for u in result.get("users") or [] if u.get("name")]
        for fallback in ("user",):
            if fallback not in names:
                names.append(fallback)
        if names != self._users:
            self._users = names
            self.user_row.set_model(Gtk.StringList.new(names))
        self.load_keys()

    def _selected_user(self):
        idx = self.user_row.get_selected()
        return self._users[idx] if 0 <= idx < len(self._users) else ""

    def load_keys(self):
        user = self._selected_user()
        if not user:
            return
        run_async(lambda: self.ctx.client.ssh_keys(user), self._render_keys, lambda e: self._render_keys({"keys": []}))

    def _render_keys(self, result):
        clear_group(self.keys_group, self._key_rows)
        keys = result.get("keys") or []
        if not keys:
            row = action_row(_("No keys for this account"), "")
            self.keys_group.add(row)
            self._key_rows.append(row)
        for k in keys:
            row = action_row(k.get("comment") or k.get("type", "?"), "%s · %s" % (k.get("type", ""), k.get("fingerprint", "")))
            row.add_suffix(icon_button("user-trash-symbolic", _("Remove"), lambda fp=k.get("fingerprint", ""): self.remove_key(fp), "destructive-action"))
            self.keys_group.add(row)
            self._key_rows.append(row)

    def load_log(self):
        run_async(lambda: self.ctx.client.ssh_log(30), self._render_log, lambda e: self._render_log({"events": []}))

    def _render_log(self, result):
        clear_group(self.log_group, self._log_rows)
        events = result.get("events") or []
        if not events:
            row = action_row(_("Nothing in the recent log"), "")
            self.log_group.add(row)
            self._log_rows.append(row)
        for e in events[:20]:
            ok = e.get("result") == "accepted"
            title = ("%s · %s" % (e.get("user", "?"), _("signed in") if ok else _("failed")))
            row = action_row(title, "%s · %s · %s" % (e.get("from", "?"), e.get("method", ""), e.get("time", "")))
            self.log_group.add(row)
            self._log_rows.append(row)

    def _render_rdp(self, st):
        self._updating = True
        self.rdp_row.set_active(bool(st.get("enabled")))
        self.rdp_row.set_sensitive(bool(st.get("installed")))
        self.rdp_source.set_selected(1 if st.get("source") == "any" else 0)
        self._updating = False
        note = st.get("note") or ""
        if st.get("enabled") and not st.get("active"):
            note = _("It is turned on but the service is not running.")
        self.rdp_note.set_visible(bool(note))
        self.rdp_note.set_subtitle(note)

    def _render_vpn(self, v):
        self._updating = True
        self.vpn_row.set_active(bool(v.get("enabled")))
        self._updating = False
        if v.get("enabled"):
            self.vpn_info.set_subtitle("%s · %d/udp\n%s" % (v.get("address", ""), v.get("listen_port", 0), _("Public key: %s") % v.get("public_key", "")))
        else:
            self.vpn_info.set_subtitle(_("Off"))
        clear_group(self.peers_group, self._peer_rows)
        for p in v.get("peers") or []:
            row = action_row(p.get("name", "?"), "%s · %s" % (", ".join(p.get("allowed_ips") or []), p.get("public_key", "")))
            row.add_suffix(icon_button("user-trash-symbolic", _("Remove"), lambda pk=p.get("public_key", ""), nm=p.get("name", ""): self.remove_peer(pk, nm), "destructive-action"))
            self.peers_group.add(row)
            self._peer_rows.append(row)
        self.peer_entry.set_sensitive(bool(v.get("enabled")))

    def _render_web(self, w):
        self._updating = True
        self.web_row.set_active(bool(w.get("enabled")))
        self._updating = False
        lines = list(w.get("urls") or [])
        if w.get("cert_fingerprint"):
            lines.append(_("Certificate: %s") % w["cert_fingerprint"])
        if not w.get("password_set"):
            lines.insert(0, _("Set a password first."))
        self.web_info.set_subtitle("\n".join(lines) if lines else "-")

    def _copy(self, text):
        Gdk.Display.get_default().get_clipboard().set(text)
        self.ctx.toast(_("Copied"))

    # --- 동작 ---
    def apply_ssh_port(self):
        port = int(self.ssh_port_row.get_value())
        run_async(
            lambda: self.ctx.client.set_ssh_advanced(port=port),
            lambda _r: (self.ctx.toast(_("SSH port changed to %d") % port), self.reload()),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def _on_ssh_password(self, row, _pspec):
        if self._updating:
            return
        want = row.get_active()
        run_async(
            lambda: self.ctx.client.set_ssh_advanced(password_login=want),
            lambda _r: self.ctx.toast(_("Saved")),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def add_key(self):
        user, key = self._selected_user(), self.key_entry.get_text().strip()
        if not user or not key:
            return

        def done(_r):
            self.key_entry.set_text("")
            self.ctx.toast(_("Key added"))
            self.load_keys()

        run_async(lambda: self.ctx.client.add_ssh_key(user, key), done, self.ctx.toast_error)

    def remove_key(self, fingerprint):
        user = self._selected_user()

        def go():
            run_async(lambda: self.ctx.client.remove_ssh_key(user, fingerprint), lambda _r: self.load_keys(), self.ctx.toast_error)

        confirm(self.ctx.window, _("Remove this key?"), _("The computer that uses it can no longer sign in."), _("Remove"), go, destructive=True)

    def _on_rdp(self, row, _pspec):
        if self._updating:
            return
        want = row.get_active()
        src = _SOURCES[self.rdp_source.get_selected()]
        run_async(
            lambda: self.ctx.client.set_remote_desktop(want, src),
            lambda r: (self._render_rdp(r), self.ctx.toast(_("Saved"))),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def _on_rdp_source(self, row, _pspec):
        if self._updating or not self.rdp_row.get_active():
            return
        src = _SOURCES[row.get_selected()]
        run_async(
            lambda: self.ctx.client.set_remote_desktop(True, src),
            lambda _r: self.ctx.toast(_("Saved")),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def _on_vpn(self, row, _pspec):
        if self._updating:
            return
        want = row.get_active()
        run_async(
            lambda: self.ctx.client.set_vpn(enabled=want),
            lambda v: (self._render_vpn(v), self.ctx.toast(_("Saved"))),
            lambda e: (self.ctx.toast_error(e), self.reload()),
        )

    def add_peer(self):
        name = self.peer_entry.get_text().strip()
        if not name:
            return

        def done(result):
            self.peer_entry.set_text("")
            self.reload()
            cfg = result.get("client_config")
            if cfg:
                TextDialog(
                    self.ctx,
                    _("Settings for %s") % name,
                    _("Open this in the WireGuard app on the device. It contains the device's private key and is shown only once."),
                    cfg,
                ).present(self.ctx.window)

        run_async(lambda: self.ctx.client.add_vpn_peer(name), done, self.ctx.toast_error)

    def remove_peer(self, public_key, name):
        def go():
            run_async(lambda: self.ctx.client.remove_vpn_peer(public_key), lambda _r: self.reload(), self.ctx.toast_error)

        confirm(self.ctx.window, _("Remove this device?"), name, _("Remove"), go, destructive=True)

    def _on_web(self, row, _pspec):
        if self._updating:
            return
        want = row.get_active()

        def failed(exc):
            self.ctx.toast_error(exc)
            self.reload()

        run_async(lambda: self.ctx.client.set_webadmin(enabled=want), lambda w: (self._render_web(w), self.ctx.toast(_("Saved"))), failed)

    def set_web_password(self):
        pw = self.web_pw.get_text()
        if not pw:
            return

        def done(_r):
            self.web_pw.set_text("")
            self.ctx.toast(_("Password saved. Everyone is signed out of the web page."))
            self.reload()

        run_async(lambda: self.ctx.client.set_webadmin_password(pw), done, self.ctx.toast_error)
