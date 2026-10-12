"""로그인 계정 만들기 대화상자와 계정 목록 보조 함수."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import error_text, run_async  # noqa: E402


class CreateAccountDialog(Adw.Dialog):
    """사용자 이름과 비밀번호를 입력받아 데몬에 계정을 만든다. 비밀번호는 이 대화상자 밖에 저장하지 않는다."""

    def __init__(self, ctx, on_created=None):
        super().__init__(title=_("Create account"), content_width=440)
        self.ctx = ctx
        self._on_created = on_created
        self._busy = False

        header = Adw.HeaderBar()
        header.set_show_end_title_buttons(False)
        header.set_show_start_title_buttons(False)
        cancel = Gtk.Button(label=_("Cancel"))
        cancel.connect("clicked", lambda *_a: self.close())
        self.create_btn = Gtk.Button(label=_("Create"), css_classes=["suggested-action"])
        self.create_btn.connect("clicked", lambda *_a: self._submit())
        header.pack_start(cancel)
        header.pack_end(self.create_btn)

        group = Adw.PreferencesGroup()
        group.set_description(
            _("This account works at the console, at the login screen and, when SSH is on, over the network.")
        )
        self.name_row = Adw.EntryRow(title=_("User name"))
        self.pw_row = Adw.PasswordEntryRow(title=_("Password"))
        self.pw2_row = Adw.PasswordEntryRow(title=_("Repeat the password"))
        self.admin_row = Adw.SwitchRow(title=_("Administrator"))
        self.admin_row.set_subtitle(_("Allow this account to use sudo."))
        self.admin_row.set_active(True)
        for row in (self.name_row, self.pw_row, self.pw2_row, self.admin_row):
            group.add(row)
        for row in (self.name_row, self.pw_row, self.pw2_row):
            row.connect("changed", lambda *_a: self._validate())
        self.pw2_row.connect("entry-activated", lambda *_a: self._submit())

        self.message = Gtk.Label(xalign=0, wrap=True, margin_top=6)
        self.message.add_css_class("error")

        box = Gtk.Box(orientation=Gtk.Orientation.VERTICAL, spacing=6, margin_top=12, margin_bottom=12, margin_start=12, margin_end=12)
        box.append(group)
        box.append(self.message)

        view = Adw.ToolbarView()
        view.add_top_bar(header)
        view.set_content(box)
        self.set_child(view)
        self.set_default_widget(self.create_btn)
        self._validate(quiet=True)

    def problem(self):
        """입력의 첫 문제를 문장으로 돌려준다. 문제가 없으면 None."""
        name = self.name_row.get_text()
        return logic.check_username(name) or logic.check_password(
            name, self.pw_row.get_text(), self.pw2_row.get_text()
        )

    def _validate(self, quiet=False):
        problem = self.problem()
        self.create_btn.set_sensitive(problem is None and not self._busy)
        # 아직 비밀번호를 치지 않았다면 오류를 보이지 않는다.
        touched = bool(self.name_row.get_text() or self.pw_row.get_text() or self.pw2_row.get_text())
        self.message.set_text(problem if (problem and touched and not quiet) else "")

    def _submit(self):
        if self._busy or self.problem() is not None:
            return
        self._busy = True
        self.create_btn.set_sensitive(False)
        name = self.name_row.get_text()
        password = self.pw_row.get_text()
        admin = self.admin_row.get_active()

        def done(_result):
            self._busy = False
            self.ctx.toast(_("Account %s created") % name)
            if self._on_created:
                self._on_created(name)
            self.close()

        def failed(exc):
            self._busy = False
            self.message.set_text(error_text(exc))
            self.create_btn.set_sensitive(self.problem() is None)

        run_async(lambda: self.ctx.client.create_user(name, password, admin), done, failed)
