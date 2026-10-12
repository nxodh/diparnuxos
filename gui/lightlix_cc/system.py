"""시스템 페이지: 모드 전환, SSH, 화면 테마, 업데이트, 진단 리포트."""

import json
import os

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import __version__, api  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, confirm, run_async, text_button  # noqa: E402

_SETTINGS_PATH = os.path.join(GLib.get_user_config_dir(), "lightlix-control-center", "settings.json")
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
        saved = load_settings().get("color_scheme", "default")
        self.scheme_combo.set_selected(_SCHEMES.index(saved) if saved in _SCHEMES else 0)
        apply_color_scheme(saved)
        self.scheme_combo.connect("notify::selected", self._on_scheme)
        look_group.add(self.scheme_combo)
        self.add(look_group)

        maint = Adw.PreferencesGroup(title=_("Maintenance"))
        maint.add(action_row(_("Update"), _("Install system updates."), text_button(_("Check"), self.update)))
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

    # --- 모드 ---
    def _on_status(self, status, error):
        if status is None:
            return
        mode = status.get("mode")
        self.version_row.set_subtitle("Control Center %s · lightlixd %s" % (__version__, status.get("version", "?")))
        if mode in ("server", "desktop") and mode != self._mode:
            self._mode = mode
            self._updating = True
            self.mode_combo.set_selected(0 if mode == "server" else 1)
            self._updating = False

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
        data = load_settings()
        data["color_scheme"] = name
        save_settings(data)

    # --- 유지보수 ---
    def update(self):
        def failed(exc):
            if isinstance(exc, api.DaemonError) and exc.code == "not_implemented":
                self.ctx.toast(_("Updates are not available in this release yet."))
            else:
                self.ctx.toast_error(exc)

        run_async(self.ctx.client.update, lambda _r: self.ctx.toast(_("Update started")), failed)

    def gpu_check(self):
        self.ctx.toast(_("Running the GPU check…"))
        run_async(
            self.ctx.client.gpu_check,
            lambda r: (self.ctx.toast(_("GPU check finished (exit code %d)") % r.get("exit_code", -1)), self.ctx.refresh()),
            self.ctx.toast_error,
        )

    def export_report(self):
        dialog = Gtk.FileDialog(title=_("Save diagnostic report"), initial_name="lightlix-report.txt")

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
