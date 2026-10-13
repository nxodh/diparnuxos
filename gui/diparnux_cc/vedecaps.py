"""Vedecaps 페이지: 코딩 에이전트 Vedecaps 를 이 컴퓨터의 모델 서버에 연결하고 상태를 확인한다."""

import shutil
import subprocess

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
gi.require_version("Gdk", "4.0")
from gi.repository import Adw, Gdk, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, icon_button, info_dialog, run_async, text_button  # noqa: E402

# 에이전트가 한 요청에 쓸 수 있는 컨텍스트가 이보다 작으면 자주 기록을 줄여야 한다(데몬의 계획 기준과 같다).
GOOD_CONTEXT = 16384


def find_vedecaps():
    """Vedecaps 실행 파일 경로. 없으면 빈 문자열."""
    return shutil.which("vedecaps") or ""


def run_connect():
    """`diparnux connect vedecaps --write --mcp` 를 현재 사용자로 실행해 (성공 여부, 출력) 을 돌려준다.

    시험이 이 함수를 바꾼다. 키 값은 이 명령이 다루지 않는다(환경 변수 이름만 쓴다).
    """
    exe = shutil.which("diparnux")
    if not exe:
        return False, _("The diparnux command was not found.")
    try:
        done = subprocess.run(
            [exe, "connect", "vedecaps", "--write", "--mcp"], capture_output=True, text=True, timeout=60, check=False
        )
    except (OSError, subprocess.SubprocessError) as exc:
        return False, str(exc)
    text = (done.stdout or "").strip()
    if done.stderr.strip():
        text = (text + "\n\n" + done.stderr.strip()).strip()
    return done.returncode == 0, text


class VedecapsPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Vedecaps"), icon_name="utilities-terminal-symbolic")
        self.ctx = ctx
        self._info = {}
        self._overrides = {}
        self._example_rows = []

        self.intro = Adw.PreferencesGroup(
            title=_("Coding agent"),
            description=_("Vedecaps is a coding agent for the terminal. This computer runs the model it uses."),
        )
        self.program_row = action_row(_("Vedecaps"), "-")
        self.server_row = action_row(_("Model server"), "-")
        self.context_row = action_row(_("Context per request"), "-")
        self.profile_row = action_row(_("Use"), "-")
        self.profile_btn = text_button(_("Use for coding agents"), self.use_agent_profile)
        self.profile_row.add_suffix(self.profile_btn)
        for row in (self.program_row, self.server_row, self.context_row, self.profile_row):
            self.intro.add(row)
        self.intro.set_header_suffix(text_button(_("Refresh"), self.reload))
        self.add(self.intro)

        self.connect_group = Adw.PreferencesGroup(
            title=_("Connect"),
            description=_("Writes the model, the context size and the Diparnux tools into your Vedecaps settings. Nothing else in them changes."),
        )
        self.connect_row = action_row(_("Set up Vedecaps"), _("Adds Diparnux as a provider and as a source of tools."))
        self.connect_btn = text_button(_("Set up"), self.connect, "suggested-action")
        self.connect_row.add_suffix(self.connect_btn)
        self.connect_group.add(self.connect_row)
        self.tools_row = action_row(_("Tool calls"), _("Checks that the model calls tools, which coding agents need."))
        self.tools_btn = text_button(_("Check"), self.check_tools)
        self.tools_row.add_suffix(self.tools_btn)
        self.connect_group.add(self.tools_row)
        self.add(self.connect_group)

        self.example_group = Adw.PreferencesGroup(
            title=_("Settings"),
            description=_("What Vedecaps needs to know, in case you want to add it by hand."),
        )
        self.add(self.example_group)

        ctx.subscribe(self._on_status)
        self.reload()

    # --- 갱신 ---
    def reload(self):
        path = find_vedecaps()
        self.program_row.set_subtitle(path or _("Not installed on this computer"))
        run_async(self.ctx.client.connection, self._render_connection, lambda e: None)
        run_async(self.ctx.client.config, self._render_config, lambda e: None)

    def _on_status(self, status, error):
        if status is None:
            return
        state = (status.get("server") or {}).get("state")
        if state != getattr(self, "_last_state", None):
            self._last_state = state
            self.reload()

    def _render_connection(self, info):
        self._info = info
        state = logic.server_state_label(info.get("server_state"))
        if info.get("model_id"):
            state += " · " + info["model_id"]
        self.server_row.set_subtitle(state)
        slot_ctx = int(info.get("slot_ctx") or 0)
        if slot_ctx <= 0:
            self.context_row.set_subtitle(_("Known when the server runs"))
        elif slot_ctx < GOOD_CONTEXT:
            self.context_row.set_subtitle(
                _("%(ctx)d tokens. Vedecaps works better with at least %(good)d: choose “Use for coding agents”.")
                % {"ctx": slot_ctx, "good": GOOD_CONTEXT}
            )
        else:
            self.context_row.set_subtitle(_("%d tokens") % slot_ctx)

        for row in self._example_rows:
            self.example_group.remove(row)
        self._example_rows = []
        text = (info.get("examples") or {}).get("vedecaps")
        if text:
            row = Adw.ExpanderRow(title=_("vedecaps.json"))
            body = Gtk.Label(label=text, xalign=0, selectable=True, wrap=True, margin_top=8, margin_bottom=8, margin_start=12, margin_end=12)
            body.add_css_class("monospace")
            inner = Adw.ActionRow()
            inner.set_child(body)
            row.add_row(inner)
            row.add_suffix(icon_button("edit-copy-symbolic", _("Copy"), lambda t=text: self.copy(t)))
            self.example_group.add(row)
            self._example_rows.append(row)

    def _render_config(self, cfg):
        self._overrides = dict(cfg.get("overrides") or {})
        agent = self._overrides.get("profile") == "agent"
        self.profile_row.set_subtitle(_("Coding agent") if agent else _("Chat"))
        self.profile_btn.set_visible(not agent)

    # --- 동작 ---
    def copy(self, text):
        Gdk.Display.get_default().get_clipboard().set(text)
        self.ctx.toast(_("Copied"))

    def use_agent_profile(self):
        overrides = dict(self._overrides)
        overrides["profile"] = "agent"

        def done(result):
            self._overrides = overrides
            self.profile_row.set_subtitle(_("Coding agent"))
            self.profile_btn.set_visible(False)
            self.ctx.toast(
                _("Restart the server for the new use to take effect.") if result.get("restart_required") else _("Saved")
            )

        run_async(lambda: self.ctx.client.set_config(overrides=overrides), done, self.ctx.toast_error)

    def connect(self):
        self.connect_btn.set_sensitive(False)

        def done(result):
            self.connect_btn.set_sensitive(True)
            ok, text = result
            info_dialog(self.ctx.window, _("Vedecaps is set up") if ok else _("Setting up Vedecaps failed"), text)

        run_async(run_connect, done, lambda e: (self.connect_btn.set_sensitive(True), self.ctx.toast_error(e)))

    def check_tools(self):
        self.tools_btn.set_sensitive(False)

        def done(result):
            self.tools_btn.set_sensitive(True)
            if result.get("ok"):
                self.tools_row.set_subtitle(_("The model calls tools correctly."))
            else:
                self.tools_row.set_subtitle(result.get("message") or _("The model does not call tools."))

        def failed(exc):
            self.tools_btn.set_sensitive(True)
            self.ctx.toast_error(exc)

        run_async(self.ctx.client.tool_check, done, failed)
