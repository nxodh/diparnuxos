"""Control Center 진입점."""

import argparse
import os
import sys
import threading

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gio, GLib  # noqa: E402

from . import api, i18n  # noqa: E402
from . import theme  # noqa: E402
from .context import Context  # noqa: E402

APP_ID = "io.github.nxodh.operetaos.ControlCenter"


def session_bus_available():
    """세션 버스가 있으면 참이다. 없으면 같은 앱을 여러 번 띄워도 서로 알 수 없다."""
    if os.environ.get("DBUS_SESSION_BUS_ADDRESS"):
        return True
    runtime = os.environ.get("XDG_RUNTIME_DIR")
    return bool(runtime) and os.path.exists(os.path.join(runtime, "bus"))


class Application(Adw.Application):
    def __init__(self, options):
        # 세션 버스가 있으면 하나의 창만 쓰고(다시 열면 기존 창을 앞으로 가져온다), 없으면 NON_UNIQUE 로 연다.
        self.unique = session_bus_available()
        flags = Gio.ApplicationFlags.DEFAULT_FLAGS if self.unique else Gio.ApplicationFlags.NON_UNIQUE
        super().__init__(application_id=APP_ID, flags=flags)
        self.options = options
        self.client = api.Client(options.socket)
        self.ctx = Context(self.client)
        self.window = None
        # 이미 떠 있는 창에 쪽을 열라고 알리는 동작이다(두 번째 실행이 보낸다).
        for name, typ, handler in (("show-page", GLib.VariantType.new("s"), self._on_show_page), ("open-wizard", None, self._on_open_wizard)):
            action = Gio.SimpleAction.new(name, typ)
            action.connect("activate", handler)
            self.add_action(action)

    def _on_show_page(self, _action, param):
        page = param.get_string()
        if self.window is not None:
            self.window.show_page(page)
            self.window.present()
        else:
            self.options.page = page

    def _on_open_wizard(self, _action, _param):
        if self.window is not None:
            self.window.open_wizard()
            self.window.present()
        else:
            self.options.wizard = True

    def do_startup(self):
        Adw.Application.do_startup(self)
        # 아이콘 이름과 로고의 흑백 색을 적용한다.
        theme.apply()

    def do_activate(self):
        if self.window is not None:
            self.window.present()
            return
        if self.options.autostart:
            self._autostart()
            return
        self._open_main(wizard=self.options.wizard)

    def _open_main(self, wizard=False):
        from .window import MainWindow

        self.window = MainWindow(self, self.ctx)
        if getattr(self.options, "page", None):
            self.window.show_page(self.options.page)
        self.window.present()
        self.ctx.start_polling()
        if wizard:
            self.window.open_wizard()

    def _autostart(self):
        """세션을 시작할 때 부른다. 마법사를 이미 마쳤으면 창을 만들지 않고 끝낸다."""
        self.hold()

        def check():
            try:
                state = self.client.firstboot()
            except Exception:  # 데몬이 아직 안 떴거나 권한이 없으면 조용히 끝낸다
                state = {"done": True}
            GLib.idle_add(decide, state)

        def decide(state):
            if state.get("done"):
                self.release()
                return GLib.SOURCE_REMOVE
            self.release()
            self._open_main(wizard=True)
            return GLib.SOURCE_REMOVE

        threading.Thread(target=check, daemon=True).start()


def parse_args(argv):
    p = argparse.ArgumentParser(prog="opereta-control-center")
    p.add_argument("--autostart", action="store_true", help="open only when the first-boot wizard is not done")
    p.add_argument("--wizard", action="store_true", help="open the first-boot wizard")
    p.add_argument("--page", default=None, help="page to open: dashboard, models, server, network or system")
    p.add_argument("--socket", default=api.DEFAULT_SOCKET, help="path of the operetad socket")
    p.add_argument("--localedir", default=None, help="directory with translation catalogs")
    return p.parse_args(argv)


def main(argv=None):
    options = parse_args(sys.argv[1:] if argv is None else argv)
    i18n.setup(localedir=options.localedir)
    app = Application(options)
    if app.unique:
        app.register(None)
        # 이미 실행 중이면 그 창에 요청을 넘기고 이 프로세스는 끝난다(run 이 activate 를 보낸다).
        if app.get_is_remote():
            if options.page:
                app.activate_action("show-page", GLib.Variant("s", options.page))
            if options.wizard:
                app.activate_action("open-wizard", None)
            if options.autostart:
                return 0
    return app.run([sys.argv[0]])
