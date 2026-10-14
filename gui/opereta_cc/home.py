"""홈: 서버, 그래픽, 업데이트, 시작 항목의 상태를 한눈에 보여 주고 해당 페이지로 연결한다."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .i18n import _  # noqa: E402
from .widgets import action_row, clear_group, level_icon, run_async  # noqa: E402


class HomePage(Adw.PreferencesPage):
    """카드는 눌러서 해당 페이지로 간다. 업데이트 정보는 페이지가 보일 때와 30초마다 다시 읽는다."""

    def __init__(self, ctx, open_page):
        super().__init__(title=_("Home"), icon_name="go-home-symbolic")
        self.ctx = ctx
        self._open_page = open_page
        self._update = None
        self._rows = []
        self.group = Adw.PreferencesGroup(title=_("Overview"))
        self.group.set_description(_("The state of this computer at a glance. Select a row to open its page."))
        self.add(self.group)
        self.hint = Adw.PreferencesGroup()
        self.hint_row = action_row("", "")
        self.hint_row.set_visible(False)
        self.hint.add(self.hint_row)
        self.add(self.hint)
        ctx.subscribe(self._on_status)
        self.connect("map", lambda *_a: self.refresh())
        self._timer = GLib.timeout_add_seconds(30, self._tick)

    def _tick(self):
        if self.get_mapped():
            self.refresh()
        return GLib.SOURCE_CONTINUE

    def refresh(self):
        run_async(self.ctx.client.update_info, self._got_update, lambda _e: None)

    def _got_update(self, info):
        self._update = info
        self._render()

    def _on_status(self, _status, _error):
        self._render()

    def _render(self):
        cards = logic.home_cards(self.ctx.status, self._update)
        clear_group(self.group, self._rows)
        for c in cards:
            row = action_row(c["title"], c["text"], activatable=bool(c["page"]))
            row.add_prefix(level_icon(c["level"]))
            if c["page"]:
                row.add_suffix(Gtk.Image.new_from_icon_name("go-next-symbolic"))
                row.connect("activated", lambda _r, page=c["page"]: self._open_page(page))
            self.group.add(row)
            self._rows.append(row)
        banner = logic.gpu_runtime_banner(self.ctx.status, self._update)
        self.hint_row.set_visible(bool(banner))
        if banner:
            self.hint_row.set_title(GLib.markup_escape_text(_("Download the CUDA runtime")))
            self.hint_row.set_subtitle(GLib.markup_escape_text(banner))
