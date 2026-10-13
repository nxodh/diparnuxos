"""메인 창: 페이지 전환, 연결 안내 배너, 토스트."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gtk  # noqa: E402

from . import logic  # noqa: E402
from .dashboard import DashboardPage  # noqa: E402
from .i18n import _  # noqa: E402
from .models import ModelsPage  # noqa: E402
from .network import NetworkPage  # noqa: E402
from .server import ServerPage  # noqa: E402
from .system import SystemPage  # noqa: E402
from .widgets import error_text  # noqa: E402
from .wizard import WizardWindow  # noqa: E402


class MainWindow(Adw.ApplicationWindow):
    def __init__(self, app, ctx):
        super().__init__(application=app, title=_("Diparnux Control Center"), default_width=980, default_height=700)
        self.ctx = ctx
        ctx.window = self

        self.toasts = Adw.ToastOverlay()
        ctx.toast_overlay = self.toasts

        self.banner = Adw.Banner(title="")
        self.banner.set_revealed(False)

        self.stack = Adw.ViewStack()
        self.stack.add_titled_with_icon(DashboardPage(ctx), "dashboard", _("Dashboard"), "utilities-system-monitor-symbolic")
        self.stack.add_titled_with_icon(ModelsPage(ctx), "models", _("Models"), "folder-download-symbolic")
        self.stack.add_titled_with_icon(ServerPage(ctx), "server", _("Server"), "network-server-symbolic")
        self.stack.add_titled_with_icon(NetworkPage(ctx), "network", _("Network"), "network-wireless-symbolic")
        self.stack.add_titled_with_icon(SystemPage(ctx, self.open_wizard), "system", _("System"), "preferences-system-symbolic")

        header = Adw.HeaderBar()
        switcher = Adw.ViewSwitcher(policy=Adw.ViewSwitcherPolicy.WIDE, stack=self.stack)
        header.set_title_widget(switcher)
        bar = Adw.ViewSwitcherBar(stack=self.stack)

        content = Gtk.Box(orientation=Gtk.Orientation.VERTICAL)
        content.append(self.banner)
        self.stack.set_vexpand(True)
        content.append(self.stack)

        view = Adw.ToolbarView()
        view.add_top_bar(header)
        view.set_content(content)
        view.add_bottom_bar(bar)
        self.toasts.set_child(view)
        self.set_content(self.toasts)

        # 창이 좁으면 하단 전환 막대를 쓴다.
        cond = Adw.BreakpointCondition.parse("max-width: 640sp")
        bp = Adw.Breakpoint.new(cond)
        bp.add_setter(switcher, "visible", False)
        bp.add_setter(bar, "reveal", True)
        self.add_breakpoint(bp)

        ctx.subscribe(self._on_status)
        # 활성이 아닌 창은 상태를 천천히 확인한다.
        self.connect("notify::is-active", lambda w, _p: self.ctx.set_active(w.is_active()))

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
