"""시작(부팅) 항목: 지금 부팅한 항목, 다음에 시작할 시스템, 복구.

일부 펌웨어(예: MSI 노트북)는 재부팅 때마다 부팅 순서를 자체 순서로 되돌린다. 그런 컴퓨터에서도 "다음 한 번만"
(BootNext)은 지켜지므로 이 페이지는 순서를 바꾸는 대신 다음 시작 대상을 고르게 한다.
"""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import logic, sysutil  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, confirm, run_async, text_button  # noqa: E402


class BootPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Startup"), icon_name="system-reboot-symbolic")
        self.ctx = ctx
        self._info = None
        self._entry_keys = []
        self._rows = []
        self._updating = False
        self._poll = None

        self.state_group = Adw.PreferencesGroup(title=_("Startup"))
        self.summary_row = action_row(_("This start"), "-")
        self.state_group.add(self.summary_row)
        self.add(self.state_group)

        self.problem_group = Adw.PreferencesGroup(title=_("Things to know"))
        self.add(self.problem_group)

        self.next_group = Adw.PreferencesGroup(title=_("Next start"))
        self.next_group.set_description(_("Choose which system starts the next time. This does not change the permanent order."))
        self.next_combo = Adw.ComboRow(title=_("Start next time"))
        self.next_combo.set_model(Gtk.StringList.new([]))
        self.next_group.add(self.next_combo)
        self.restart_row = action_row(_("Restart now"), _("Restarts the computer and starts the system chosen above."))
        self.restart_row.add_suffix(text_button(_("Restart"), self.restart_now, "suggested-action"))
        self.next_group.add(self.restart_row)
        self.add(self.next_group)

        self.auto_group = Adw.PreferencesGroup(title=_("Always start Opereta"))
        self.auto_row = Adw.SwitchRow(title=_("Start Opereta again after shutting down"))
        self.auto_row.set_subtitle(
            _("Sets the next start to Opereta every time you shut down or restart from Opereta. Turn this off before you start from an installation USB drive.")
        )
        self.auto_row.connect("notify::active", self._on_auto)
        self.auto_group.add(self.auto_row)
        self.add(self.auto_group)

        self.fix_group = Adw.PreferencesGroup(title=_("Repair"))
        self.fix_group.add(action_row(_("Repair the boot entry"), _("Reinstall the boot loader and create the 'opereta' entry."),
                                      text_button(_("Repair"), self.repair)))
        self.cleanup_row = action_row(_("Remove old entries"), _("Remove folders and boot entries left by an earlier name of this system."),
                                      text_button(_("Remove"), self.cleanup))
        self.fix_group.add(self.cleanup_row)
        self.add(self.fix_group)

        self.connect("map", lambda *_a: self.refresh())

    # --- 읽기 ---
    def refresh(self):
        run_async(self.ctx.client.update_info, self._got, lambda _e: None)

    def _got(self, info):
        self._info = info
        d = logic.describe_boot(info)
        self.summary_row.set_subtitle(GLib.markup_escape_text(d["summary"]))
        clear_group(self.problem_group, self._rows)
        notes = list(d["problems"])
        if d["bitlocker"]:
            notes.append(("bitlocker", _("This disk has BitLocker volumes. Keep the Windows recovery key at hand when you change startup settings.")))
        for code, text in notes:
            row = action_row("", text)
            row.add_prefix(Gtk.Image.new_from_icon_name("dialog-warning-symbolic" if code != "bitlocker" else "dialog-information-symbolic"))
            self.problem_group.add(row)
            self._rows.append(row)
        self.problem_group.set_visible(bool(notes))
        self._updating = True
        self._entry_keys = [e["num"] for e in d["entries"]]
        self.next_combo.set_model(Gtk.StringList.new([e["label"] for e in d["entries"]]))
        # 기본 선택: 지금 부팅하지 않은 첫 항목(보통 반대편 시스템)
        sel = 0
        for i, e in enumerate(d["entries"]):
            if not e["current"] and e["kind"] in ("opereta", "windows"):
                sel = i
                break
        self.next_combo.set_selected(sel)
        self.auto_row.set_active(d["auto"])
        self._updating = False
        self.next_group.set_sensitive(d["usable"])
        self.auto_group.set_sensitive(d["usable"])
        self.cleanup_row.set_visible(any(p[0] == "legacy_entries" for p in d["problems"]))
        # 데몬은 라이브 세션과 UEFI 가 아닌 부팅에서 복구와 정리를 거절하므로 누를 수 없게 한다.
        self.fix_group.set_sensitive(d["usable"])

    # --- 동작 ---
    def _act(self, action, done_text, after=None):
        def done(_r):
            self.ctx.toast(done_text)
            if after is not None:
                after()
            GLib.timeout_add_seconds(4, lambda: (self.refresh(), GLib.SOURCE_REMOVE)[1])

        run_async(lambda: self.ctx.client.update_start(action), done, self.ctx.toast_error)

    def _selected_entry(self):
        i = self.next_combo.get_selected()
        if 0 <= i < len(self._entry_keys):
            return self._entry_keys[i]
        return ""

    def _set_next(self, then):
        num = self._selected_entry()
        if not num:
            return
        self._act("boot_next:" + num.lower(), _("The next start is set"), then)

    def restart_now(self):
        num = self._selected_entry()
        if not num:
            return

        def go():
            confirm(self.ctx.window, _("Restart now?"), _("Unsaved work in open programs will be lost."), _("Restart"),
                    lambda: run_async(lambda: sysutil.run(["systemctl", "reboot"]), None, self.ctx.toast_error), destructive=True)

        self._set_next(go)

    def _on_auto(self, row, _pspec):
        if self._updating:
            return
        self._act("boot_auto_on" if row.get_active() else "boot_auto_off",
                  _("Saved"))

    def repair(self):
        confirm(self.ctx.window, _("Repair the boot entry?"),
                _("The boot loader is installed again. If Windows uses BitLocker, keep its recovery key at hand."), _("Repair"),
                lambda: self._act("boot_repair", _("Repair started")))

    def cleanup(self):
        confirm(self.ctx.window, _("Remove old entries?"),
                _("Folders and boot entries named after an earlier name of this system are deleted from the EFI partition."), _("Remove"),
                lambda: self._act("boot_cleanup", _("Cleanup started")), destructive=True)
