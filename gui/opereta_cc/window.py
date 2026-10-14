"""메인 창: 왼쪽 목록으로 페이지 전환, 연결 안내 배너, 토스트."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GObject, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .about import AboutPage  # noqa: E402
from .boot import BootPage  # noqa: E402
from .connection import ConnectionPage  # noqa: E402
from .desktop import AppearancePage, BluetoothPage, DisplayPage, KeyboardPage, SoundPage, StoragePage  # noqa: E402
from .gpu import GpuPage  # noqa: E402
from .home import HomePage  # noqa: E402
from .deberopa import DeberopaPage  # noqa: E402
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
        super().__init__(application=app, title=_("Opereta Control Center"), default_width=1040, default_height=720)
        self.ctx = ctx
        ctx.window = self

        self.toasts = Adw.ToastOverlay()
        ctx.toast_overlay = self.toasts

        self.banner = Adw.Banner(title="")
        self.banner.set_revealed(False)
        # GPU 가 있는데 CUDA 런타임이 없을 때 알리는 배너. 모델이 CPU 로 도는 이유를 모르고 지나치기 쉽다.
        self.gpu_banner = Adw.Banner(title="", button_label=_("Open Graphics"))
        self.gpu_banner.set_revealed(False)
        self.gpu_banner.connect("button-clicked", lambda *_a: self.show_page("gpu"))
        self._update_info = None

        self.stack = Gtk.Stack(transition_type=Gtk.StackTransitionType.CROSSFADE, vexpand=True, hexpand=True)
        self._entries = []  # (이름, 제목, 아이콘). 묶음 제목 줄은 ("", 제목, "") 로 넣는다.
        # 묶음: 개요, AI, 모양, 하드웨어, 네트워크, 시스템. AI 묶음은 기능을 줄이지 않고 그대로 둔다.
        groups = (
            (_("Overview"), (
                ("home", _("Home"), "go-home-symbolic", lambda: HomePage(ctx, self.show_page)),
                ("about", _("This computer"), "computer-symbolic", lambda: AboutPage(ctx)),
            )),
            (_("AI"), (
                ("dashboard", _("Dashboard"), "utilities-system-monitor-symbolic", lambda: DashboardPage(ctx)),
                ("models", _("Models"), "folder-download-symbolic", lambda: ModelsPage(ctx)),
                ("server", _("Server"), "network-server-symbolic", lambda: ServerPage(ctx)),
                ("connection", _("Connection"), "network-transmit-receive-symbolic", lambda: ConnectionPage(ctx)),
                ("deberopa", _("Deberopa"), "utilities-terminal-symbolic", lambda: DeberopaPage(ctx)),
                ("resources", _("Resources"), "utilities-system-monitor-symbolic", lambda: ResourcesPage(ctx)),
            )),
            (_("Look"), (
                ("appearance", _("Appearance"), "preferences-desktop-theme-symbolic", lambda: AppearancePage(ctx)),
            )),
            (_("Hardware"), (
                ("display", _("Displays"), "video-display-symbolic", lambda: DisplayPage(ctx)),
                ("sound", _("Sound"), "audio-volume-high-symbolic", lambda: SoundPage(ctx)),
                ("bluetooth", _("Bluetooth"), "bluetooth-symbolic", lambda: BluetoothPage(ctx)),
                ("keyboard", _("Keyboard"), "input-keyboard-symbolic", lambda: KeyboardPage(ctx)),
                ("gpu", _("Graphics"), "video-card-symbolic", lambda: GpuPage(ctx)),
            )),
            (_("Network"), (
                ("network", _("Network"), "network-wireless-symbolic", lambda: NetworkPage(ctx)),
                ("firewall", _("Firewall"), "security-high-symbolic", lambda: FirewallPage(ctx)),
                ("remote", _("Remote access"), "network-workgroup-symbolic", lambda: RemotePage(ctx)),
            )),
            (_("System"), (
                ("storage", _("Storage"), "drive-harddisk-symbolic", lambda: StoragePage(ctx)),
                ("boot", _("Startup"), "system-reboot-symbolic", lambda: BootPage(ctx)),
                ("services", _("Services"), "system-run-symbolic", lambda: ServicesPage(ctx)),
                ("system", _("System"), "preferences-system-symbolic", lambda: SystemPage(ctx, self.open_wizard)),
            )),
        )
        for group_title, items in groups:
            self._entries.append(("", group_title, ""))
            for name, title, icon, make in items:
                self.stack.add_titled(make(), name, title)
                self._entries.append((name, title, icon))

        # 사이드바: 아이콘과 제목이 있는 목록. 좁은 창에서는 접히고 헤더의 단추로 연다.
        self.sidebar_list = Gtk.ListBox(selection_mode=Gtk.SelectionMode.SINGLE)
        self.sidebar_list.add_css_class("navigation-sidebar")
        self._rows = {}
        for name, title, icon in self._entries:
            if not name:  # 묶음 제목 줄은 고를 수 없다
                head = Gtk.Label(label=title, xalign=0, margin_top=14, margin_bottom=4, margin_start=12)
                head.add_css_class("caption-heading")
                head.add_css_class("dim-label")
                hrow = Gtk.ListBoxRow(selectable=False, activatable=False)
                hrow.set_child(head)
                self.sidebar_list.append(hrow)
                continue
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
        side_header.set_title_widget(Gtk.Label(label=_("Opereta"), css_classes=["heading"]))
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
        content.append(self.gpu_banner)
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
            if n and n == name:
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
        else:
            _level, title, text = banner
            self.banner.set_title("%s — %s" % (title, text))
            self.banner.set_revealed(True)
        self._refresh_runtime_banner(status)

    def _refresh_runtime_banner(self, status):
        """GPU 가 있고 CUDA 런타임이 없으면 배너를 띄운다. 업데이트 정보는 30초에 한 번만 다시 읽는다."""
        import time

        from .widgets import run_async

        now = time.monotonic()
        if now - getattr(self, "_runtime_checked", 0) >= 30 or self._update_info is None:
            self._runtime_checked = now

            def got(info):
                self._update_info = info
                self._apply_runtime_banner(status)

            run_async(self.ctx.client.update_info, got, lambda _e: None)
            return
        self._apply_runtime_banner(status)

    def _apply_runtime_banner(self, status):
        text = logic.gpu_runtime_banner(status, self._update_info)
        if text:
            self.gpu_banner.set_title(text)
        self.gpu_banner.set_revealed(bool(text))

    def open_wizard(self, on_finished=None):
        WizardWindow(self.ctx, parent=self, on_finished=on_finished).present()
