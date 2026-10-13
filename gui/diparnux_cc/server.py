"""서버 페이지: 시작/중지, 실행 계획, LAN 공개, API 키, 로그."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import (  # noqa: E402
    action_row,
    clear_group,
    confirm,
    icon_button,
    info_dialog,
    run_async,
    text_button,
)


class ServerPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Server"), icon_name="network-server-symbolic")
        self.ctx = ctx
        self._models = []
        self._plan_rows = []
        self._key_rows = []
        self._updating = False
        self._overrides = {}

        self.state_group = Adw.PreferencesGroup(title=_("Server"))
        self.state_row = action_row(_("State"), "-")
        self.warn_row = action_row(_("Notes"), "")
        self.warn_row.set_visible(False)
        self.state_group.add(self.state_row)
        self.state_group.add(self.warn_row)
        self.add(self.state_group)

        self.run_group = Adw.PreferencesGroup(title=_("Run a model"))
        self.model_combo = Adw.ComboRow(title=_("Model"))
        self.model_combo.set_model(Gtk.StringList.new([]))
        self.run_group.add(self.model_combo)
        self.parallel_row = Adw.SpinRow.new_with_range(1, 16, 1)
        self.parallel_row.set_title(_("Simultaneous requests"))
        self.parallel_row.set_subtitle(
            _("How many requests the server answers at the same time. Each one gets its own context, so more requests need more memory. Restart the server to apply.")
        )
        self.parallel_row.connect("notify::value", self._on_parallel)
        self.run_group.add(self.parallel_row)
        buttons = Gtk.Box(spacing=8, halign=Gtk.Align.END, margin_top=6)
        buttons.append(text_button(_("Show plan"), self.show_plan))
        self.start_btn = text_button(_("Start"), self.start, "suggested-action")
        self.stop_btn = text_button(_("Stop"), self.stop, "destructive-action")
        buttons.append(self.start_btn)
        buttons.append(self.stop_btn)
        self.run_group.add(buttons)
        self.add(self.run_group)

        self.plan_group = Adw.PreferencesGroup(title=_("Plan"))
        self.plan_group.set_visible(False)
        self.add(self.plan_group)

        self.net_group = Adw.PreferencesGroup(title=_("Network"))
        self.lan_row = Adw.SwitchRow(title=_("Allow other computers to connect"))
        self.lan_row.set_subtitle(_("Requires an API key. The firewall opens the server port only while this is on."))
        self.lan_row.connect("notify::active", self._on_lan)
        self.net_group.add(self.lan_row)
        self.add(self.net_group)

        self.keys_group = Adw.PreferencesGroup(title=_("API keys"))
        self.key_entry = Adw.EntryRow(title=_("New key label"))
        self.key_entry.set_show_apply_button(True)
        self.key_entry.connect("apply", lambda *_a: self.create_key())
        self.keys_group.add(self.key_entry)
        self.add(self.keys_group)

        self.log_group = Adw.PreferencesGroup(title=_("Server log"))
        refresh = text_button(_("Refresh"), self.load_logs)
        self.log_group.set_header_suffix(refresh)
        self.log_view = Gtk.TextView(editable=False, monospace=True, wrap_mode=Gtk.WrapMode.WORD_CHAR)
        self.log_view.add_css_class("card")
        scroller = Gtk.ScrolledWindow(min_content_height=200, hscrollbar_policy=Gtk.PolicyType.NEVER)
        scroller.set_child(self.log_view)
        self.log_group.add(scroller)
        self.add(self.log_group)

        ctx.subscribe(self.update)
        self.reload()

    # --- 갱신 ---
    def reload(self):
        run_async(self.ctx.client.models, self._on_models, self.ctx.toast_error)
        run_async(self.ctx.client.config, self._on_config, lambda e: None)
        self.load_keys()
        self.load_logs()

    def _on_models(self, result):
        self._models = [m.get("id") for m in (result.get("models") or []) if not m.get("incomplete")]
        self.model_combo.set_model(Gtk.StringList.new(self._models))
        default = result.get("default")
        if default in self._models:
            self.model_combo.set_selected(self._models.index(default))

    def _on_config(self, cfg):
        self._updating = True
        self.lan_row.set_active(bool(cfg.get("lan_exposed")))
        self._overrides = dict(cfg.get("overrides") or {})
        self.parallel_row.set_value(int(self._overrides.get("parallel") or 1))
        self._updating = False

    def update(self, status, error):
        if status is None:
            return
        server = status.get("server") or {}
        state = server.get("state")
        label = logic.server_state_label(state)
        if server.get("model_id"):
            label += " · " + server["model_id"]
        self.state_row.set_subtitle(label)
        notes = list(server.get("warnings") or [])
        last = server.get("last_error")
        if last:
            notes.append("%s: %s" % (last.get("code", ""), last.get("message", "")))
        notes.extend(logic.server_advice(status))
        self.warn_row.set_visible(bool(notes))
        self.warn_row.set_subtitle("\n".join(notes))
        running = state in ("starting", "running", "running_cpu_fallback")
        self.start_btn.set_sensitive(not running and bool(self._models))
        self.stop_btn.set_sensitive(running)

    def _selected_model(self):
        idx = self.model_combo.get_selected()
        if 0 <= idx < len(self._models):
            return self._models[idx]
        return ""

    # --- 동작 ---
    def show_plan(self):
        model = self._selected_model()
        run_async(lambda: self.ctx.client.plan(model), self._render_plan, self.ctx.toast_error)

    def _render_plan(self, result):
        clear_group(self.plan_group, self._plan_rows)
        plan = result.get("plan") or {}
        inp = result.get("input") or {}
        self.plan_group.set_visible(True)
        place = _("CPU only") if plan.get("cpu_only") else _("GPU offload")
        slots = plan.get("parallel") or 1
        ctx_text = "ctx %s" % plan.get("ctx")
        if slots > 1:
            ctx_text = _("%(slots)d slots × ctx %(slot_ctx)s") % {"slots": slots, "slot_ctx": plan.get("slot_ctx")}
        head = action_row(
            "%s · %s · batch %s" % (place, ctx_text, plan.get("batch")),
            _("Estimated VRAM %(vram)s · RAM %(ram)s")
            % {"vram": logic.fmt_mib(plan.get("est_vram_mib", -1)), "ram": logic.fmt_mib(plan.get("est_ram_mib", -1))},
        )
        self.plan_group.add(head)
        self._plan_rows.append(head)
        for title, items in ((_("Why"), plan.get("rationale")), (_("Warnings"), plan.get("warnings")), (_("Advice"), plan.get("advice"))):
            if items:
                row = action_row(title, "\n".join(items))
                self.plan_group.add(row)
                self._plan_rows.append(row)
        if inp.get("desktop_mode") and plan.get("advice"):
            self.ctx.toast(_("This model is large for desktop mode. Consider switching to server mode."), timeout=8)

    def start(self):
        model = self._selected_model()
        if not model:
            self.ctx.toast(_("Download a model first."))
            return
        self.start_btn.set_sensitive(False)

        def done(_r):
            self.ctx.toast(_("Server is starting"))
            self.ctx.refresh()

        def failed(exc):
            self.ctx.toast_error(exc)
            self.ctx.refresh()

        run_async(lambda: self.ctx.client.start(model, wait=False), done, failed)

    def stop(self):
        self.stop_btn.set_sensitive(False)
        run_async(
            self.ctx.client.stop,
            lambda _r: (self.ctx.toast(_("Server stopped")), self.ctx.refresh()),
            lambda e: (self.ctx.toast_error(e), self.ctx.refresh()),
        )

    def _on_parallel(self, row, _pspec):
        if self._updating:
            return
        count = int(row.get_value())
        overrides = dict(self._overrides)
        if count > 1:
            overrides["parallel"] = count
        else:
            overrides.pop("parallel", None)
        self._overrides = overrides

        def done(result):
            self.ctx.toast(
                _("Restart the server for the new number of simultaneous requests to take effect.")
                if result.get("restart_required")
                else _("Saved")
            )

        run_async(lambda: self.ctx.client.set_config(overrides=overrides), done, self.ctx.toast_error)

    def _on_lan(self, row, _pspec):
        if self._updating:
            return
        wanted = row.get_active()

        def done(result):
            self.ctx.toast(
                _("Restart the server for the network setting to take effect.")
                if result.get("restart_required")
                else _("Network setting saved")
            )

        def failed(exc):
            self.ctx.toast_error(exc)
            self._updating = True
            row.set_active(not wanted)
            self._updating = False

        run_async(lambda: self.ctx.client.set_config(lan_exposed=wanted), done, failed)

    # --- API 키 ---
    def load_keys(self):
        run_async(self.ctx.client.api_keys, self._render_keys, lambda e: None)

    def _render_keys(self, result):
        clear_group(self.keys_group, self._key_rows)
        keys = result.get("keys") or []
        if not keys:
            row = action_row(_("No API keys"))
            self.keys_group.add(row)
            self._key_rows.append(row)
        for k in keys:
            row = action_row(k.get("label", "?"), "%s… · %s" % (k.get("prefix", ""), k.get("created", "")))
            row.add_suffix(icon_button("user-trash-symbolic", _("Delete"), lambda kid=k["id"]: self.delete_key(kid), "destructive-action"))
            self.keys_group.add(row)
            self._key_rows.append(row)

    def create_key(self):
        label = self.key_entry.get_text().strip()
        if not label:
            return

        def done(result):
            self.key_entry.set_text("")
            self.load_keys()
            info_dialog(
                self.ctx.window,
                _("Your new API key"),
                "%s\n\n%s" % (result.get("key", ""), _("Copy it now. It is shown only once.")),
            )

        run_async(lambda: self.ctx.client.create_key(label), done, self.ctx.toast_error)

    def delete_key(self, key_id):
        def go():
            run_async(lambda: self.ctx.client.delete_key(key_id), lambda _r: self.load_keys(), self.ctx.toast_error)

        confirm(self.ctx.window, _("Delete this API key?"), _("Clients using it will stop working."), _("Delete"), go, destructive=True)

    # --- 로그 ---
    def load_logs(self):
        def show(result):
            self.log_view.get_buffer().set_text(result.get("log", ""))

        run_async(lambda: self.ctx.client.logs(200), show, lambda e: None)
