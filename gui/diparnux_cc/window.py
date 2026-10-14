"""메인 창: 왼쪽 목록으로 페이지 전환, 연결 안내 배너, 토스트."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GObject, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .connection import ConnectionPage  # noqa: E402
from .vedecaps import VedecapsPage  # noqa: E402
from .dashboard import DashboardPage  # noqa: E402
from .firewall import FirewallPage  # noqa: E402
from .i18n import _  # noqa: E402
from .models import ModelsPage  # noqa: E402
from .network import NetworkPage  # noqa: E402
from .remote import RemotePage  # noqa: E402
from .resources import ResourcesPage  # noqa: E402
from .server import ServerPage  # noqa: E402
from .services import ServicesPage  # noqa: E402
from .system import SystemPage  # noqa: E402
from .widgets import error_text  # noqa: E402
from .wizard import WizardWindow  # noqa: E402


class MainWindow(Adw.ApplicationWindow):
    def __init__(self, app, ctx):
        super().__init__(application=app, title=_("Diparnux Control Center"), default_width=1040, default_height=720)
        self.ctx = ctx
        ctx.window = self

        self.toasts = Adw.ToastOverlay()
        ctx.toast_overlay = self.toasts

        self.banner = Adw.Banner(title="")
        self.banner.set_revealed(False)

        self.stack = Gtk.Stack(transition_type=Gtk.StackTransitionType.CROSSFADE, vexpand=True, hexpand=True)
        self._entries = []  # (이름, 제목, 아이콘)
        for name, title, icon, page in (
            ("dashboard", _("Dashboard"), "utilities-system-monitor-symbolic", DashboardPage(ctx)),
            ("models", _("Models"), "folder-download-symbolic", ModelsPage(ctx)),
            ("server", _("Server"), "network-server-symbolic", ServerPage(ctx)),
            ("connection", _("Connection"), "network-transmit-receive-symbolic", ConnectionPage(ctx)),
            ("vedecaps", _("Vedecaps"), "utilities-terminal-symbolic", VedecapsPage(ctx)),
            ("network", _("Network"), "network-wireless-symbolic", NetworkPage(ctx)),
            ("firewall", _("Firewall"), "security-high-symbolic", FirewallPage(ctx)),
            ("remote", _("Remote access"), "network-workgroup-symbolic", RemotePage(ctx)),
            ("services", _("Services"), "system-run-symbolic", ServicesPage(ctx)),
            ("resources", _("Resources"), "utilities-system-monitor-symbolic", ResourcesPage(ctx)),
            ("system", _("System"), "preferences-system-symbolic", SystemPage(ctx, self.open_wizard)),
        ):
            self.stack.add_titled(page, name, title)
            self._entries.append((name, title, icon))

        # 사이드바: 아이콘과 제목이 있는 목록. 좁은 창에서는 접히고 헤더의 단추로 연다.
        self.sidebar_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.SINGLE)
        self.sidebar_list.add_css_class("navigation-sidebar")
        self._rows = {}
        for name, title, icon in self._entries:
            box = Gtk.Box(spacing=12, margin_top=6, margin_bottom=6, margin_start=6, margin_end=6)
            box.append(Gtk.Image.new_from_icon_name(icon))
            box.append(Gtk.Label(label=title, xalign=0, hexpand=True))
            row = Gtk.ListBoxRow()
            row.set_child(box)
            row.page_name = name
            self.sidebar_list.append(row)
            self._rows[name] = row
        self.sidebar_list.connect("row-selected", self._on_row_selected)
        side_scroller = Gtk.ScrolledWindow(hscrollbar_policy=Gtk.PolicyType.NEVER, vexpand=True)
        side_scroller.set_child(self.sidebar_list)
        side_header = Adw.HeaderBar()
        side_header.set_show_end_title_buttons(False)
        side_header.set_title_widget(Gtk.Label(label=_("Diparnux"), css_classes=["heading"]))
        side_view = Adw.ToolbarView()
        side_view.add_top_bar(side_header)
        side_view.set_content(side_scroller)

        self.split = Adw.OverlaySplitView(sidebar_width_fraction=0.22, min_sidebar_width=190, max_sidebar_width=260)
        self.split.set_sidebar(side_view)

        self.toggle = Gtk.ToggleButton(icon_name="sidebar-show-symbolic", tooltip_text=_("Show the page list"))
        self.toggle.set_visible(False)
        self.toggle.bind_property("active", self.split, "show-sidebar", GObject.BindingFlags.BIDIRECTIONAL | GObject.BindingFlags.SYNC_CREATE)
        self.page_title = Gtk.Label(label="", css_classes=["heading"])
        header = Adw.HeaderBar()
        header.pack_start(self.toggle)
        header.set_title_widget(self.page_title)

        content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        content.append(self.banner)
        content.append(self.stack)
        content_view = Adw.ToolbarView()
        content_view.add_top_bar(header)
        content_view.set_content(content)
        self.split.set_content(content_view)

        self.toasts.set_child(self.split)
        self.set_content(self.toasts)

        # 창이 좁으면 사이드바를 접는다.
        bp = Adw.Breakpoint.new(Adw.BreakpointCondition.parse("max-width: 720sp"))
        bp.add_setter(self.split, "collapsed", True)
        bp.add_setter(self.toggle, "visible", True)
        self.add_breakpoint(bp)

        self.stack.connect("notify::visible-child-name", lambda *_a: self._sync_selection())
        self._sync_selection()

        ctx.subscribe(self._on_status)
        # 활성이 아닌 창은 상태를 천천히 확인한다.
        self.connect("notify::is-active", lambda w, _p: self.ctx.set_active(w.is_active()))

    # --- 사이드바 ---
    def _on_row_selected(self, _box, row):
        if row is None:
            return
        name = row.page_name
        if self.stack.get_visible_child_name() != name:
            self.stack.set_visible_child_name(name)
        if self.split.get_collapsed():
            self.split.set_show_sidebar(False)

    def _sync_selection(self):
        name = self.stack.get_visible_child_name()
        row = self._rows.get(name)
        if row is not None and self.sidebar_list.get_selected_row() is not row:
            self.sidebar_list.select_row(row)
        for n, title, _icon in self._entries:
            if n == name:
                self.page_title.set_label(title)

    def show_page(self, name):
        """이름으로 페이지를 연다. 모르는 이름은 무시한다."""
        if self.stack.get_child_by_name(name) is not None:
            self.stack.set_visible_child_name(name)

    def _on_status(self, status, error):
        if error is not None:
            self.banner.set_title(error_text(error))
            self.banner.set_revealed(True)
            return
        banner = logic.gpu_banner(status)
        if banner is None:
            self.banner.set_revealed(False)
            return
        _level, title, text = banner
        self.banner.set_title("%s — %s" % (title, text))
        self.banner.set_revealed(True)

    def open_wizard(self, on_finished=None):
        WizardWindow(self.ctx, parent=self, on_finished=on_finished).present()
