"""첫 부팅 마법사: GPU 감지 -> 드라이버 검증 -> Secure Boot -> 추천 모델 -> API 공개 범위."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .accounts import CreateAccountDialog  # noqa: E402
from .widgets import action_row, error_text, info_dialog, level_icon, run_async, text_button  # noqa: E402

_STEP_TITLES = {
    "gpu": lambda: _("Graphics card"),
    "driver": lambda: _("NVIDIA driver"),
    "secure_boot": lambda: _("Secure Boot"),
    "account": lambda: _("Account"),
    "model": lambda: _("Model"),
    "api": lambda: _("Network access"),
}


class WizardWindow(Adw.Window):
    def __init__(self, ctx, parent=None, on_finished=None):
        super().__init__(title=_("Diparnux setup"), modal=parent is not None, default_width=560, default_height=520)
        if parent is not None:
            self.set_transient_for(parent)
        self.ctx = ctx
        self._on_finished = on_finished
        self._index = 0
        self._results = {}
        self._data = {}

        view = Adw.ToolbarView()
        header = Adw.HeaderBar()
        view.add_top_bar(header)
        self.set_content(view)

        self.stack = Gtk.Stack(vexpand=True, transition_type=Gtk.StackTransitionType.SLIDE_LEFT_RIGHT)
        self.title_label = Gtk.Label(css_classes=["title-1"], margin_top=18)
        self.progress = Gtk.Label(css_classes=["dim-label"])
        self.body = Adw.PreferencesPage()
        self.body.set_vexpand(True)

        self.back_btn = text_button(_("Back"), self.back)
        self.next_btn = text_button(_("Next"), self.next, "suggested-action")
        buttons = Gtk.Box(spacing=8, halign=Gtk.Align.END, margin_top=8, margin_bottom=12, margin_start=12, margin_end=12)
        buttons.append(self.back_btn)
        buttons.append(self.next_btn)

        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        box.append(self.title_label)
        box.append(self.progress)
        box.append(self.body)
        box.append(buttons)
        view.set_content(box)

        self._show_loading()
        self._load()

    # --- 데이터 ---
    def _load(self):
        def fetch():
            c = self.ctx.client
            out = {"status": c.status()}
            for key, fn in (
                ("recommendations", c.recommendations),
                ("models", c.models),
                ("config", c.config),
                ("keys", c.api_keys),
                ("users", c.users),
            ):
                try:
                    out[key] = fn()
                except Exception as exc:  # noqa: BLE001 - 일부 실패해도 마법사는 계속한다
                    out[key] = None
                    out.setdefault("errors", []).append(error_text(exc))
            return out

        run_async(fetch, self._loaded, self._load_failed)

    def _loaded(self, data):
        self._data = data
        self._index = 0
        self._render()

    def _load_failed(self, exc):
        self._clear_body()
        group = Adw.PreferencesGroup(title=_("Cannot reach the Diparnux daemon"))
        group.add(action_row(error_text(exc), _("Close this window and try again later.")))
        self.body.add(group)
        self._groups = [group]
        self.next_btn.set_label(_("Close"))
        self.next_btn.set_sensitive(True)
        self.back_btn.set_visible(False)
        self._index = -1

    def _show_loading(self):
        self.title_label.set_text(_("Checking this machine…"))
        self.progress.set_text("")
        self.next_btn.set_sensitive(False)
        self.back_btn.set_sensitive(False)

    # --- 단계 계산 ---
    def _evaluate(self, step):
        status = self._data.get("status")
        if step == "gpu":
            return logic.evaluate_gpu(status)
        if step == "driver":
            return logic.evaluate_driver(status)
        if step == "secure_boot":
            return logic.evaluate_secure_boot(status)
        if step == "account":
            return logic.evaluate_account(((self._data.get("users") or {}).get("users")) or [])
        if step == "model":
            models = ((self._data.get("models") or {}).get("models")) or []
            return logic.evaluate_model(self._data.get("recommendations"), models)
        keys = ((self._data.get("keys") or {}).get("keys")) or []
        return logic.evaluate_api(self._data.get("config"), len(keys))

    # --- 화면 ---
    def _clear_body(self):
        # PreferencesPage 의 그룹을 모두 지운다.
        for group in list(getattr(self, "_groups", [])):
            self.body.remove(group)
        self._groups = []

    def _add_group(self, group):
        self.body.add(group)
        self._groups.append(group)

    def _render(self):
        self._clear_body()
        step = logic.STEPS[self._index]
        result = self._evaluate(step)
        self._results[step] = result
        self.title_label.set_text(_STEP_TITLES[step]())
        self.progress.set_text(_("Step %(n)d of %(total)d") % {"n": self._index + 1, "total": len(logic.STEPS)})

        group = Adw.PreferencesGroup()
        row = action_row(result.title, result.detail)
        row.add_prefix(level_icon(result.level))
        group.add(row)
        self._add_group(group)

        if step == "driver" and (self._data.get("status") or {}).get("gpu", {}).get("status"):
            g2 = Adw.PreferencesGroup()
            g2.add(action_row(_("Check again"), _("Run the GPU diagnostic once more."), text_button(_("Run"), self._recheck)))
            self._add_group(g2)
        if step == "account":
            self._account_extras()
        if step == "model":
            self._model_extras()
        if step == "api":
            self._api_extras()

        self.back_btn.set_visible(True)
        self.back_btn.set_sensitive(self._index > 0)
        self.next_btn.set_sensitive(True)
        last = self._index == len(logic.STEPS) - 1
        self.next_btn.set_label(_("Finish") if last else _("Next"))

    def _account_extras(self):
        group = Adw.PreferencesGroup()
        group.add(
            action_row(
                _("Create an account"),
                _("Choose your own user name and password for the console, the login screen and SSH."),
                text_button(_("Create account"), self._create_account, "suggested-action"),
            )
        )
        self._add_group(group)

    def _create_account(self):
        def created(_name):
            # 새 계정을 반영해 단계를 다시 그린다.
            run_async(
                self.ctx.client.users,
                lambda r: (self._data.update({"users": r}), self._render()),
                self.ctx.toast_error,
            )

        CreateAccountDialog(self.ctx, on_created=created).present(self)

    def _use_extras(self):
        """모델을 어떻게 쓸지 고른다. 코딩 에이전트(Vedecaps 등)는 요청마다 긴 컨텍스트가 필요하다."""
        cfg = self._data.get("config") or {}
        overrides = dict(cfg.get("overrides") or {})
        group = Adw.PreferencesGroup(title=_("How will you use the model?"))
        self._use = Adw.ComboRow(title=_("Use"))
        self._use.set_subtitle(_("Coding agents such as Vedecaps need a long context for each request. You can change this later in the Server page."))
        self._use.set_model(Gtk.StringList.new([_("Chat"), _("Coding agent")]))
        self._use.set_selected(1 if overrides.get("profile") == "agent" else 0)
        self._use_busy = False
        self._use.connect("notify::selected", self._on_use)
        group.add(self._use)
        self._add_group(group)

    def _on_use(self, row, _pspec):
        if self._use_busy:
            return
        cfg = self._data.get("config") or {}
        overrides = dict(cfg.get("overrides") or {})
        if row.get_selected() == 1:
            overrides["profile"] = "agent"
        else:
            overrides.pop("profile", None)

        def done(result):
            if isinstance(result.get("config"), dict):
                self._data["config"] = result["config"]

        def revert(exc):
            self.ctx.toast_error(exc)
            self._use_busy = True
            row.set_selected(0 if row.get_selected() == 1 else 1)
            self._use_busy = False

        run_async(lambda: self.ctx.client.set_config(overrides=overrides), done, revert)

    def _model_extras(self):
        self._use_extras()
        rec = self._data.get("recommendations") or {}
        idx = rec.get("recommended", -1)
        models = rec.get("models") or []
        installed = ((self._data.get("models") or {}).get("models")) or []
        if installed or not (isinstance(idx, int) and 0 <= idx < len(models)):
            return
        m = models[idx]
        group = Adw.PreferencesGroup()
        group.add(
            action_row(
                _("Download %s now") % m.get("name", ""),
                _("%(size)s from Hugging Face. You can also do this later in the Models page.") % {"size": logic.fmt_mib(m.get("size_mib", -1))},
                text_button(_("Download"), lambda: self._download(m)),
            )
        )
        self._add_group(group)

    def _download(self, m):
        run_async(
            lambda: self.ctx.client.pull(m["repo"], m["file"]),
            lambda _r: self.ctx.toast(_("Download started. You can follow it in the Models page.")),
            self.ctx.toast_error,
        )

    def _api_extras(self):
        cfg = self._data.get("config") or {}
        group = Adw.PreferencesGroup(title=_("Who can use the API"))
        self._lan = Adw.SwitchRow(title=_("Allow other computers to connect"))
        self._lan.set_subtitle(_("Needs an API key. Leave this off if you only use this machine."))
        self._lan.set_active(bool(cfg.get("lan_exposed")))
        self._lan_busy = False
        self._lan.connect("notify::active", self._on_lan)
        group.add(self._lan)
        self._add_group(group)

    def _on_lan(self, row, _pspec):
        if self._lan_busy:
            return
        wanted = row.get_active()
        keys = ((self._data.get("keys") or {}).get("keys")) or []

        def revert(exc):
            self.ctx.toast_error(exc)
            self._lan_busy = True
            row.set_active(not wanted)
            self._lan_busy = False

        def apply():
            def done(result):
                self._data["config"] = result.get("config")
                self._render()

            run_async(lambda: self.ctx.client.set_config(lan_exposed=wanted), done, revert)

        if wanted and not keys:
            # 키가 없으면 먼저 하나 만든다. 데몬은 키 없이 LAN 공개를 거부한다.
            def made(created):
                self._data["keys"] = {"keys": [{"id": created.get("id"), "label": created.get("label")}]}
                info_dialog(
                    self,
                    _("Your new API key"),
                    "%s\n\n%s" % (created.get("key", ""), _("Copy it now. It is shown only once.")),
                )
                apply()

            run_async(lambda: self.ctx.client.create_key("first-boot"), made, revert)
        else:
            apply()

    def _recheck(self):
        self.ctx.toast(_("Running the GPU check…"))

        def done(_r):
            self._load()

        run_async(self.ctx.client.gpu_check, done, self.ctx.toast_error)

    # --- 이동 ---
    def back(self):
        if self._index > 0:
            self._index -= 1
            self._render()

    def next(self):
        if self._index < 0:
            self.close()
            return
        if self._index < len(logic.STEPS) - 1:
            self._index += 1
            self._render()
            return
        self._finish()

    def _finish(self):
        skipped = logic.skipped_steps([self._results[s] for s in logic.STEPS if s in self._results])
        self.next_btn.set_sensitive(False)

        def done(_r):
            if self._on_finished:
                self._on_finished()
            self.close()

        def failed(exc):
            self.next_btn.set_sensitive(True)
            self.ctx.toast_error(exc)

        run_async(lambda: self.ctx.client.complete_firstboot(skipped), done, failed)
