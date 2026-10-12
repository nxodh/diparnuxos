"""Control Center 진입점."""

import argparse
import sys
import threading

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gio, GLib  # noqa: E402

from . import api, i18n  # noqa: E402
from .context import Context  # noqa: E402

APP_ID = "io.github.nxodh.lightlixos.ControlCenter"


class Application(Adw.Application):
    def __init__(self, options):
        # 세션 버스가 없어도 시작하도록 NON_UNIQUE 로 연다.
        super().__init__(application_id=APP_ID, flags=Gio.ApplicationFlags.NON_UNIQUE)
        self.options = options
        self.client = api.Client(options.socket)
        self.ctx = Context(self.client)
        self.window = None

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
    p = argparse.ArgumentParser(prog="lightlix-control-center")
    p.add_argument("--autostart", action="store_true", help="open only when the first-boot wizard is not done")
    p.add_argument("--wizard", action="store_true", help="open the first-boot wizard")
    p.add_argument("--socket", default=api.DEFAULT_SOCKET, help="path of the lightlixd socket")
    p.add_argument("--localedir", default=None, help="directory with translation catalogs")
    return p.parse_args(argv)


def main(argv=None):
    options = parse_args(sys.argv[1:] if argv is None else argv)
    i18n.setup(localedir=options.localedir)
    app = Application(options)
    return app.run([sys.argv[0]])
