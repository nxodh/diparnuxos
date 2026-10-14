"""시스템 페이지: 모드 전환, SSH, 화면 테마, 업데이트, 진단 리포트."""

import json
import os

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import __version__, api  # noqa: E402
from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .accounts import CreateAccountDialog  # noqa: E402
from .widgets import action_row, clear_group, confirm, icon_button, run_async, text_button  # noqa: E402

_SETTINGS_PATH = os.path.join(GLib.get_user_config_dir(), "opereta-control-center", "settings.json")
_SCHEMES = ("default", "light", "dark")


def load_settings():
    try:
        with open(_SETTINGS_PATH, encoding="utf-8") as f:
            data = json.load(f)
        return data if isinstance(data, dict) else {}
    except (OSError, ValueError):
        return {}


def save_settings(data):
    try:
        os.makedirs(os.path.dirname(_SETTINGS_PATH), exist_ok=True)
        with open(_SETTINGS_PATH, "w", encoding="utf-8") as f:
            json.dump(data, f)
    except OSError:
        pass


# 데스크톱(Xfce) 전체의 밝기. 제어 센터의 선택이 창 관리자와 GTK3 앱에도 적용되도록 xfconf 값을 바꾼다.
_OS_THEMES = {"light": "Opereta", "dark": "Opereta-Dark", "default": "Opereta"}

# 터미널 색(xfce4-terminal 은 시스템 기본값이 아니라 사용자 terminalrc 를 읽는다). 값은 packages 의 terminalrc 와 같다.
_TERMINAL_COLORS = {
    False: {
        "ColorForeground": "#1d1d1f",
        "ColorBackground": "#fbfbfb",
        "ColorCursor": "#1d1d1f",
        "ColorPalette": "#1d1d1f;#b3433f;#4f7a4f;#9a7b2e;#4a6f94;#7d5f94;#4a8189;#c8c8cc;#6e6e73;#c9605b;#68925f;#b8943f;#6389b0;#9a7bb0;#62a0a8;#ffffff",
    },
    True: {
        "ColorForeground": "#f2f2f4",
        "ColorBackground": "#1c1c1e",
        "ColorCursor": "#f2f2f4",
        "ColorPalette": "#2b2b2c;#e0726d;#7fb07f;#d3b76a;#79a0c9;#b496c8;#78b4bc;#c8c8cc;#6e6e73;#ef8c87;#98c698;#e3cc85;#93b6d9;#c8aedb;#92c8cf;#ffffff",
    },
}


def _set_terminal_colors(dark, home=None):
    """사용자의 terminalrc 가 있으면 색 줄만 바꾼다(다른 설정은 그대로 둔다)."""
    path = os.path.join(home or os.path.expanduser("~"), ".config", "xfce4", "terminal", "terminalrc")
    try:
        with open(path, encoding="utf-8") as f:
            lines = f.read().split("\n")
    except OSError:
        return
    want = dict(_TERMINAL_COLORS[bool(dark)])
    out = []
    for line in lines:
        key = line.split("=", 1)[0]
        if key in want:
            out.append("%s=%s" % (key, want.pop(key)))
        else:
            out.append(line)
    if want:  # 빠진 키는 [Configuration] 구역 끝에 더한다
        try:
            idx = max(i for i, line in enumerate(out) if line.strip())
        except ValueError:
            idx = -1
        out[idx + 1:idx + 1] = ["%s=%s" % kv for kv in want.items()]
    tmp = path + ".opereta-tmp"
    try:
        with open(tmp, "w", encoding="utf-8") as f:
            f.write("\n".join(out))
        os.replace(tmp, path)
    except OSError:
        pass


def apply_os_scheme(name):
    """GTK 테마, 창 관리자 테마, 배경 화면, 터미널 색을 밝은/어두운 테마에 맞춘다.

    xfconf-query 가 없거나 Xfce 세션이 아니면 아무것도 하지 않는다.
    """
    import shutil
    import subprocess

    exe = shutil.which("xfconf-query")
    if not exe:
        return
    dark = name == "dark"
    theme = _OS_THEMES.get(name, "Opereta")

    def run(*args, capture=False):
        try:
            res = subprocess.run([exe, *args], check=False, timeout=10, stdout=subprocess.PIPE if capture else subprocess.DEVNULL,
                                 stderr=subprocess.DEVNULL, text=True)
            return res.stdout if capture else ""
        except (OSError, subprocess.SubprocessError):
            return ""

    for channel, prop in (("xsettings", "/Net/ThemeName"), ("xfwm4", "/general/theme")):
        run("-c", channel, "-p", prop, "-s", theme)
    # GTK4 와 libadwaita 앱은 테마 이름이 아니라 이 값으로 어두운 모양을 고른다.
    run("-c", "xsettings", "-p", "/Gtk/ApplicationPreferDarkTheme", "-n", "-t", "bool", "-s", "true" if dark else "false")
    # 배경 화면: 모니터마다 있는 last-image 와 같은 폴더의 rgba1 을 바꾼다.
    wallpaper = "/usr/share/opereta/wallpaper.png" if dark else "/usr/share/opereta/wallpaper-light.png"
    shade = "0.050980" if dark else "0.949020"
    for prop in run("-c", "xfce4-desktop", "-l", capture=True).splitlines():
        prop = prop.strip()
        if prop.endswith("/last-image"):
            current = run("-c", "xfce4-desktop", "-p", prop, capture=True).strip()
            if current.startswith("/usr/share/opereta/wallpaper"):  # 사용자가 고른 그림은 바꾸지 않는다
                base = prop[: -len("last-image")]
                run("-c", "xfce4-desktop", "-p", prop, "-s", wallpaper)
                run("-c", "xfce4-desktop", "-p", base + "rgba1", "-n", "-a", "-t", "double", "-s", shade, "-t", "double", "-s", shade,
                    "-t", "double", "-s", shade, "-t", "double", "-s", "1.000000")
    _set_terminal_colors(dark)


def apply_color_scheme(name):
    mgr = Adw.StyleManager.get_default()
    mgr.set_color_scheme(
        {
            "light": Adw.ColorScheme.FORCE_LIGHT,
            "dark": Adw.ColorScheme.FORCE_DARK,
        }.get(name, Adw.ColorScheme.DEFAULT)
    )


class SystemPage(Adw.PreferencesPage):
    def __init__(self, ctx, open_wizard):
        super().__init__(title=_("System"), icon_name="preferences-system-symbolic")
        self.ctx = ctx
        self._open_wizard = open_wizard
        self._updating = False
        self._mode = None

        mode_group = Adw.PreferencesGroup(title=_("Session mode"))
        mode_group.set_description(
            _("Server mode runs without the desktop and leaves more memory for the model.")
        )
        self.mode_combo = Adw.ComboRow(title=_("Mode"))
        self.mode_combo.set_model(Gtk.StringList.new([_("Server mode (no desktop)"), _("Desktop mode")]))
        self.mode_combo.connect("notify::selected", self._on_mode)
        mode_group.add(self.mode_combo)
        self.add(mode_group)

        self._user_rows = []
        self.users_group = Adw.PreferencesGroup(title=_("Accounts"))
        self.users_group.set_description(
            _("Create your own account with a user name and a password. It replaces the default live account for daily use.")
        )
        self.users_group.set_header_suffix(text_button(_("Create account"), self.create_account, "suggested-action"))
        self.add(self.users_group)

        power_group = Adw.PreferencesGroup(title=_("Always on"))
        self.power_row = Adw.SwitchRow(title=_("Keep the computer on"))
        self.power_row.set_subtitle(_("Never suspend or shut down when the lid is closed or after a long idle time."))
        self.power_row.connect("notify::active", self._on_power)
        power_group.add(self.power_row)
        self.power_info = action_row(_("Power status"), "")
        power_group.add(self.power_info)
        self.power_note = action_row(_("Note"), "")
        self.power_note.set_visible(False)
        power_group.add(self.power_note)
        self.add(power_group)
        self._power_loaded = 0.0

        ssh_group = Adw.PreferencesGroup(title=_("Remote access"))
        self.ssh_row = Adw.SwitchRow(title=_("SSH server"))
        self.ssh_row.connect("notify::active", self._on_ssh)
        ssh_group.add(self.ssh_row)
        self.ssh_note = action_row(_("Note"), "")
        self.ssh_note.set_visible(False)
        ssh_group.add(self.ssh_note)
        self.add(ssh_group)

        look_group = Adw.PreferencesGroup(title=_("Appearance"))
        self.scheme_combo = Adw.ComboRow(title=_("Color scheme"))
        self.scheme_combo.set_model(Gtk.StringList.new([_("Follow the system"), _("Light"), _("Dark")]))
        # 처음 값은 시스템을 따른다. 데스크톱의 기본은 밝은 테마다. 사용자가 바꾸면 그 값을 따른다.
        saved = load_settings().get("color_scheme", "default")
        self.scheme_combo.set_selected(_SCHEMES.index(saved) if saved in _SCHEMES else 0)
        apply_color_scheme(saved)
        self.scheme_combo.connect("notify::selected", self._on_scheme)
        look_group.add(self.scheme_combo)
        self.add(look_group)

        upd_group = Adw.PreferencesGroup(title=_("Updates"))
        self._update_poll = None
        self._update_row = action_row(_("System updates"), _("Install the kernel, NVIDIA driver and system updates together."))
        buttons = Gtk.Box(spacing=6, valign=Gtk.Align.CENTER)
        self._btn_check = text_button(_("Check"), lambda: self.start_update("check"))
        self._btn_install = text_button(_("Install"), self.install_update, "suggested-action")
        self._btn_rollback = text_button(_("Roll back"), self.rollback_update)
        for b in (self._btn_check, self._btn_install, self._btn_rollback):
            b.set_sensitive(False)
            buttons.append(b)
        self._update_row.add_suffix(buttons)
        upd_group.add(self._update_row)
        # GPU 가속에 필요한 CUDA 런타임은 이미지에 없다. NVIDIA 에서 직접 받는다(라이선스 확인 뒤).
        self._cuda_row = action_row(_("GPU acceleration (CUDA runtime)"), "-")
        self._btn_cuda = text_button(_("Download from NVIDIA"), self.install_cuda_runtime, "suggested-action")
        self._btn_cuda.set_valign(Gtk.Align.CENTER)
        self._btn_cuda.set_sensitive(False)
        self._cuda_row.add_suffix(self._btn_cuda)
        upd_group.add(self._cuda_row)
        # 코딩 에이전트 Deberopa 는 하루에 한 번 최신 릴리스를 확인해 뒤처졌으면 자동으로 갱신한다.
        self._agent_row = action_row(_("Deberopa (coding agent)"), "-")
        agent_buttons = Gtk.Box(spacing=6, valign=Gtk.Align.CENTER)
        self._btn_agent_check = text_button(_("Check"), lambda: self.start_deberopa("deberopa_check"))
        self._btn_agent_update = text_button(_("Update"), lambda: self.start_deberopa("deberopa_update"), "suggested-action")
        for b in (self._btn_agent_check, self._btn_agent_update):
            b.set_sensitive(False)
            agent_buttons.append(b)
        self._agent_row.add_suffix(agent_buttons)
        upd_group.add(self._agent_row)
        self._agent_auto = Adw.SwitchRow(title=_("Update Deberopa automatically"))
        self._agent_auto.set_subtitle(_("Checks for a new release once a day and installs it. The downloaded file is verified first."))
        self._agent_auto_updating = False
        self._agent_auto.connect("notify::active", self._on_agent_auto)
        upd_group.add(self._agent_auto)
        self.add(upd_group)

        maint = Adw.PreferencesGroup(title=_("Maintenance"))
        maint.add(
            action_row(_("GPU check"), _("Run the GPU diagnostic again."), text_button(_("Run"), self.gpu_check))
        )
        maint.add(
            action_row(
                _("Diagnostic report"),
                _("Save a report with GPU, driver and server details. Tokens are removed."),
                text_button(_("Export"), self.export_report),
            )
        )
        maint.add(
            action_row(_("Setup wizard"), _("Walk through the first-boot checks again."), text_button(_("Open"), self.rerun_wizard))
        )
        self.add(maint)

        about = Adw.PreferencesGroup(title=_("About"))
        self.version_row = action_row(_("Version"), "Control Center %s" % __version__)
        about.add(self.version_row)
        self.add(about)

        ctx.subscribe(self._on_status)
        run_async(ctx.client.ssh, self._on_ssh_state, lambda e: None)
        self.load_power()
        self.load_users()
        self.refresh_update()

    # --- 계정 ---
    def load_users(self):
        run_async(self.ctx.client.users, self._render_users, lambda e: None)

    def _render_users(self, result):
        clear_group(self.users_group, self._user_rows)
        users = result.get("users") or []
        if not users:
            row = action_row(_("No accounts created yet"), _("Only the default live account exists."))
            self.users_group.add(row)
            self._user_rows.append(row)
        for u in users:
            sub = _("Administrator") if u.get("admin") else _("Standard account")
            row = action_row(u.get("name", "?"), sub)
            row.add_suffix(
                icon_button("user-trash-symbolic", _("Delete account"), lambda n=u.get("name"): self.delete_account(n), "destructive-action")
            )
            self.users_group.add(row)
            self._user_rows.append(row)

    def create_account(self):
        def created(_name):
            self.load_users()
            run_async(self.ctx.client.ssh, self._on_ssh_state, lambda e: None)

        CreateAccountDialog(self.ctx, on_created=created).present(self.ctx.window)

    def delete_account(self, name):
        def go():
            def done(_r):
                self.ctx.toast(_("Account deleted"))
                self.load_users()
                run_async(self.ctx.client.ssh, self._on_ssh_state, lambda e: None)

            run_async(lambda: self.ctx.client.delete_user(name), done, self.ctx.toast_error)

        confirm(
            self.ctx.window,
            _("Delete this account?"),
            _("The account %s is removed. Its home directory and files are kept.") % name,
            _("Delete"),
            go,
            destructive=True,
        )

    # --- 모드 ---
    def _on_status(self, status, error):
        if status is None:
            return
        mode = status.get("mode")
        self.version_row.set_subtitle("Control Center %s · operetad %s" % (__version__, status.get("version", "?")))
        if mode in ("server", "desktop") and mode != self._mode:
            self._mode = mode
            self._updating = True
            self.mode_combo.set_selected(0 if mode == "server" else 1)
            self._updating = False
        # 상시 가동 상태는 자주 바뀌지 않는다. 데몬이 매번 logind 에 묻지 않도록 1분에 한 번만 읽는다.
        import time

        if time.monotonic() - self._power_loaded > 60:
            self.load_power()

    def _on_mode(self, combo, _pspec):
        if self._updating:
            return
        wanted = "server" if combo.get_selected() == 0 else "desktop"
        if wanted == self._mode:
            return
        body = (
            _("Switching to server mode closes the desktop session and this window.")
            if wanted == "server"
            else _("Switching to desktop mode starts the graphical login.")
        )

        def set_combo(mode):
            self._updating = True
            combo.set_selected(0 if mode == "server" else 1)
            self._updating = False

        def go():
            previous = self._mode

            def done(_r):
                self._mode = wanted
                self.ctx.toast(_("Mode changed"))

            def failed(exc):
                self.ctx.toast_error(exc)
                set_combo(previous)

            run_async(lambda: self.ctx.client.set_mode(wanted), done, failed)

        confirm(
            self.ctx.window,
            _("Change the session mode?"),
            body,
            _("Change"),
            go,
            on_cancel=lambda: set_combo(self._mode),
        )

    # --- 상시 가동 ---
    def load_power(self):
        import time

        self._power_loaded = time.monotonic()
        run_async(self.ctx.client.power, self._on_power_state, lambda e: None)

    def _on_power_state(self, state):
        self._updating = True
        self.power_row.set_active(bool(state.get("always_on")))
        parts = []
        lid = state.get("lid")
        if lid in ("open", "closed"):
            parts.append(_("Lid: %s") % (_("open") if lid == "open" else _("closed")))
        if state.get("has_battery"):
            percent = state.get("battery_percent", -1)
            source = _("battery") if state.get("on_battery") else _("AC power")
            parts.append(_("Power: %(source)s") % {"source": source} + (" %d%%" % percent if percent >= 0 else ""))
        up = logic.fmt_uptime(state.get("uptime_seconds"))
        if up != "-":
            parts.append(_("Up for %s") % up)
        failed = logic.power_failed_checks(state)
        if failed:
            parts.append(_("Not effective: %s") % ", ".join(failed))
        self.power_info.set_subtitle(" · ".join(parts) or "-")
        notes = [n for n in (state.get("notes") or []) if n]
        self.power_note.set_visible(bool(notes))
        self.power_note.set_subtitle("\n".join(notes))
        self._updating = False

    def _on_power(self, row, _pspec):
        if self._updating:
            return
        wanted = row.get_active()

        def failed(exc):
            self.ctx.toast_error(exc)
            self.load_power()

        run_async(lambda: self.ctx.client.set_power(wanted), self._on_power_state, failed)

    # --- SSH ---
    def _on_ssh_state(self, state):
        self._updating = True
        self.ssh_row.set_active(bool(state.get("enabled")))
        installed = bool(state.get("installed"))
        self.ssh_row.set_sensitive(installed)
        sub = _("Opens port 22 in the firewall.") if installed else _("The SSH server is not installed in this image.")
        self.ssh_row.set_subtitle(sub)
        warning = state.get("warning") or ""
        self.ssh_note.set_visible(bool(warning))
        self.ssh_note.set_subtitle(warning)
        self._updating = False

    def _on_ssh(self, row, _pspec):
        if self._updating:
            return
        wanted = row.get_active()

        def failed(exc):
            self.ctx.toast_error(exc)
            run_async(self.ctx.client.ssh, self._on_ssh_state, lambda e: None)

        run_async(lambda: self.ctx.client.set_ssh(wanted), self._on_ssh_state, failed)

    # --- 테마 ---
    def _on_scheme(self, combo, _pspec):
        name = _SCHEMES[combo.get_selected()]
        apply_color_scheme(name)
        apply_os_scheme(name)
        data = load_settings()
        data["color_scheme"] = name
        save_settings(data)

    # --- 업데이트 ---
    def refresh_update(self):
        run_async(self.ctx.client.update_info, self._on_update_info, lambda e: None)

    def _on_update_info(self, info):
        d = logic.describe_update(info)
        text = d["title"] if not d["detail"] else "%s\n%s" % (d["title"], d["detail"])
        self._update_row.set_subtitle(GLib.markup_escape_text(text))
        self._btn_check.set_sensitive(d["can_check"])
        self._btn_install.set_sensitive(d["can_install"])
        self._btn_rollback.set_sensitive(d["can_rollback"])
        c = logic.describe_cuda_runtime(info)
        self._cuda_row.set_subtitle(GLib.markup_escape_text(c["text"]))
        self._btn_cuda.set_sensitive(c["can_install"] and not d["busy"])
        self._btn_cuda.set_visible(not c["installed"])
        a = logic.describe_deberopa(info)
        self._agent_row.set_subtitle(GLib.markup_escape_text(a["text"]))
        self._btn_agent_check.set_sensitive(a["can_check"])
        self._btn_agent_update.set_sensitive(a["can_update"])
        self._btn_agent_update.set_visible(a["can_update"])
        if self._agent_auto.get_active() != a["auto"]:
            self._agent_auto_updating = True
            self._agent_auto.set_active(a["auto"])
            self._agent_auto_updating = False
        if d["busy"] and self._update_poll is None:
            self._update_poll = GLib.timeout_add_seconds(3, self._poll_update)

    def _poll_update(self):
        def done(info):
            d = logic.describe_update(info)
            self._on_update_info(info)
            if not d["busy"]:
                self._update_poll = None

        def failed(_exc):
            self._update_poll = None

        run_async(self.ctx.client.update_info, done, failed)
        return GLib.SOURCE_CONTINUE if self._update_poll is not None else GLib.SOURCE_REMOVE

    def start_update(self, action):
        def done(_r):
            self.ctx.toast(_("Update started"))
            self._btn_check.set_sensitive(False)
            self._btn_install.set_sensitive(False)
            self._btn_rollback.set_sensitive(False)
            self._update_row.set_subtitle(GLib.markup_escape_text(_("Updating")))
            if self._update_poll is None:
                self._update_poll = GLib.timeout_add_seconds(3, self._poll_update)

        run_async(lambda: self.ctx.client.update_start(action), done, self.ctx.toast_error)

    def start_deberopa(self, action):
        def done(_r):
            self.ctx.toast(_("Checking Deberopa…") if action == "deberopa_check" else _("Updating Deberopa…"))
            self._btn_agent_check.set_sensitive(False)
            self._btn_agent_update.set_sensitive(False)
            self._agent_row.set_subtitle(GLib.markup_escape_text(_("Working…")))
            if self._update_poll is None:
                self._update_poll = GLib.timeout_add_seconds(3, self._poll_agent)

        run_async(lambda: self.ctx.client.update_start(action), done, self.ctx.toast_error)

    def _poll_agent(self):
        """갱신 유닛이 끝날 때까지 상태를 다시 읽는다. 끝나면 표시를 새로 고친다."""

        def done(info):
            self._on_update_info(info)
            if not (info.get("running") or (info.get("state") or {}).get("phase") == "running"):
                self._update_poll = None

        def failed(_exc):
            self._update_poll = None

        run_async(self.ctx.client.update_info, done, failed)
        return GLib.SOURCE_CONTINUE if self._update_poll is not None else GLib.SOURCE_REMOVE

    def _on_agent_auto(self, row, _pspec):
        if self._agent_auto_updating:
            return
        action = "deberopa_auto_on" if row.get_active() else "deberopa_auto_off"
        run_async(lambda: self.ctx.client.update_start(action), lambda _r: None, self.ctx.toast_error)

    def install_update(self):
        confirm(
            self.ctx.window,
            _("Install updates?"),
            _("The kernel and the NVIDIA driver are updated together. A restart is needed afterwards."),
            _("Install"),
            lambda: self.start_update("apply"),
        )

    def install_cuda_runtime(self):
        # 본문은 마크업이다(약관 링크). 번역된 문장은 이스케이프해서 넣는다.
        esc = GLib.markup_escape_text
        link = '<a href="https://docs.nvidia.com/cuda/eula/index.html">%s</a>' % esc(_("NVIDIA CUDA license agreement"))
        body = "\n\n".join(
            [
                esc(_("The CUDA runtime is NVIDIA software and is not part of Opereta. This computer downloads it directly from NVIDIA under NVIDIA's license. The download is about 360 MiB.")),
                _("Read the license before you continue: %s") % link,
                esc(_("NVIDIA's license for GeForce products does not allow datacenter deployment. Check NVIDIA's terms before you use it that way.")),
            ]
        )
        confirm(
            self.ctx.window,
            _("Download the CUDA runtime from NVIDIA?"),
            body,
            _("Download"),
            lambda: self.start_update("cuda_runtime"),
            body_markup=True,
        )

    def rollback_update(self):
        confirm(
            self.ctx.window,
            _("Roll back the last update?"),
            _("The previous kernel and driver are restored. A restart is needed afterwards."),
            _("Roll back"),
            lambda: self.start_update("rollback"),
            destructive=True,
        )

    # --- 유지보수 ---
    def gpu_check(self):
        self.ctx.toast(_("Running the GPU check…"))
        run_async(
            self.ctx.client.gpu_check,
            lambda r: (self.ctx.toast(_("GPU check finished (exit code %d)") % r.get("exit_code", -1)), self.ctx.refresh()),
            self.ctx.toast_error,
        )

    def export_report(self):
        dialog = Gtk.FileDialog(title=_("Save diagnostic report"), initial_name="opereta-report.txt")

        def chosen(dlg, result):
            try:
                gfile = dlg.save_finish(result)
            except GLib.Error:
                return  # 사용자가 취소했다
            path = gfile.get_path()

            def write(text):
                with open(path, "w", encoding="utf-8") as f:
                    f.write(text)
                return path

            run_async(
                lambda: write(self.ctx.client.report()),
                lambda p: self.ctx.toast(_("Report saved to %s") % p),
                self.ctx.toast_error,
            )

        dialog.save(self.ctx.window, None, chosen)

    def rerun_wizard(self):
        def done(_r):
            self._open_wizard()

        run_async(self.ctx.client.reset_firstboot, done, self.ctx.toast_error)
