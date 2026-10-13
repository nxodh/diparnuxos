"""연결 페이지: 다른 컴퓨터가 이 컴퓨터의 모델 서버에 접속하는 데 필요한 주소, 예시, 키별 사용량."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
gi.require_version("Gdk", "4.0")
from gi.repository import Adw, Gdk, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, icon_button, run_async, text_button  # noqa: E402


class ConnectionPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Connection"), icon_name="network-transmit-receive-symbolic")
        self.ctx = ctx
        self._addr_rows = []
        self._example_rows = []
        self._usage_rows = []

        self.intro_group = Adw.PreferencesGroup(
            title=_("Use this computer as an AI server"),
            description=_("Other computers, such as Orca, Open WebUI or an editor plugin, connect with an OpenAI-compatible URL and an API key."),
        )
        self.state_row = action_row(_("Server"), "-")
        self.key_row = action_row(_("API keys"), "-")
        self.notes_row = action_row(_("Notes"), "")
        self.notes_row.set_visible(False)
        for row in (self.state_row, self.key_row, self.notes_row):
            self.intro_group.add(row)
        refresh = text_button(_("Refresh"), self.reload)
        self.intro_group.set_header_suffix(refresh)
        self.add(self.intro_group)

        self.addr_group = Adw.PreferencesGroup(
            title=_("Addresses"),
            description=_("Use one of these as the base URL on the other computer."),
        )
        self.add(self.addr_group)

        self.example_group = Adw.PreferencesGroup(
            title=_("Examples"),
            description=_("Replace <your API key> with a key you created. A key is shown only once, when it is created."),
        )
        self.add(self.example_group)

        self.check_group = Adw.PreferencesGroup(title=_("Check"))
        self.check_row = action_row(_("Send a short request to the server"), _("Checks that the model answers."))
        self.check_btn = text_button(_("Check"), self.check)
        self.check_row.add_suffix(self.check_btn)
        self.check_group.add(self.check_row)
        self.add(self.check_group)

        self.usage_group = Adw.PreferencesGroup(
            title=_("Usage per key"),
            description=_("Counted by the gateway since the daemon started."),
        )
        self.add(self.usage_group)

        ctx.subscribe(self._on_status)
        self.reload()

    # --- 갱신 ---
    def reload(self):
        run_async(self.ctx.client.connection, self._render_connection, lambda e: None)
        run_async(self.ctx.client.gateway, self._render_usage, lambda e: None)

    def _on_status(self, status, error):
        # 서버 상태가 바뀌면 주소 목록의 경고도 달라지므로 다시 읽는다.
        if status is None:
            return
        state = (status.get("server") or {}).get("state")
        if state != getattr(self, "_last_state", None):
            self._last_state = state
            self.reload()

    def _render_connection(self, info):
        state = logic.server_state_label(info.get("server_state"))
        if info.get("model_id"):
            state += " · " + info["model_id"]
        self.state_row.set_subtitle(state)
        count = info.get("key_count", 0)
        required = _("required") if info.get("key_required") else _("not required")
        self.key_row.set_subtitle("%d · %s" % (count, required))
        warnings = list(info.get("warnings") or [])
        self.notes_row.set_visible(bool(warnings))
        self.notes_row.set_subtitle("\n".join(warnings))

        clear_group(self.addr_group, self._addr_rows)
        local = info.get("local_url", "")
        entries = []
        for a in info.get("addresses") or []:
            entries.append((a.get("url", ""), "%s · %s" % (a.get("interface", ""), a.get("family", ""))))
        if info.get("mdns_url"):
            entries.append((info["mdns_url"], _("by name")))
        if local:
            entries.append((local, _("this computer only")))
        for url, sub in entries:
            row = action_row(url, sub)
            row.add_suffix(icon_button("edit-copy-symbolic", _("Copy"), lambda t=url: self.copy(t)))
            self.addr_group.add(row)
            self._addr_rows.append(row)

        clear_group(self.example_group, self._example_rows)
        titles = (
            ("settings", _("Settings for an app")),
            ("curl", _("curl")),
            ("openai_python", _("OpenAI Python client")),
            ("environment", _("Environment variables")),
        )
        examples = info.get("examples") or {}
        for key, title in titles:
            text = examples.get(key)
            if not text:
                continue
            row = Adw.ExpanderRow(title=title)
            body = Gtk.Label(label=text, xalign=0, selectable=True, wrap=True, margin_top=8, margin_bottom=8, margin_start=12, margin_end=12)
            body.add_css_class("monospace")
            inner = Adw.ActionRow()
            inner.set_child(body)
            row.add_row(inner)
            row.add_suffix(icon_button("edit-copy-symbolic", _("Copy"), lambda t=text: self.copy(t)))
            self.example_group.add(row)
            self._example_rows.append(row)

    def _render_usage(self, gw):
        clear_group(self.usage_group, self._usage_rows)
        keys = (gw.get("stats") or {}).get("keys") or []
        if not keys:
            row = action_row(_("No API keys"))
            self.usage_group.add(row)
            self._usage_rows.append(row)
            return
        for k in keys:
            row = action_row(k.get("label") or k.get("id", "?"), logic.key_usage_text(k))
            self.usage_group.add(row)
            self._usage_rows.append(row)
        stats = gw.get("stats") or {}
        total = action_row(
            _("Total"),
            _("%(requests)d requests · %(errors)d errors · %(unauthorized)d refused")
            % {"requests": stats.get("requests", 0), "errors": stats.get("errors", 0), "unauthorized": stats.get("unauthorized", 0)},
        )
        self.usage_group.add(total)
        self._usage_rows.append(total)

    # --- 동작 ---
    def copy(self, text):
        clipboard = Gdk.Display.get_default().get_clipboard()
        clipboard.set(text)
        self.ctx.toast(_("Copied"))

    def check(self):
        self.check_btn.set_sensitive(False)

        def done(result):
            self.check_btn.set_sensitive(True)
            self.check_row.set_subtitle(_("The server answered."))
            self.ctx.toast(_("The server answered."))

        def failed(exc):
            self.check_btn.set_sensitive(True)
            self.ctx.toast_error(exc)

        run_async(lambda: self.ctx.client.chat([{"role": "user", "content": "ping"}], max_tokens=1), done, failed)
