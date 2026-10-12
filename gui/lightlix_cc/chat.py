"""채팅 페이지: 로드된 모델과 대화하고 초당 토큰 수를 보인다."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Gtk  # noqa: E402

from .i18n import _  # noqa: E402
from .widgets import run_async  # noqa: E402

MAX_TOKENS = 256


class ChatPage(Gtk.Box):
    title = None  # 윈도우가 ViewStack 에 넣을 때 쓴다

    def __init__(self, ctx):
        super().__init__(orientation=Gtk.Orientation.VERTICAL, spacing=8, margin_top=12, margin_bottom=12, margin_start=12, margin_end=12)
        self.ctx = ctx
        self._history = []
        self._busy = False

        self.view = Gtk.TextView(editable=False, wrap_mode=Gtk.WrapMode.WORD_CHAR, cursor_visible=False)
        self.view.add_css_class("card")
        self.view.set_left_margin(10)
        self.view.set_right_margin(10)
        self.view.set_top_margin(8)
        self.view.set_bottom_margin(8)
        scroller = Gtk.ScrolledWindow(vexpand=True, hscrollbar_policy=Gtk.PolicyType.NEVER)
        scroller.set_child(self.view)
        self._scroller = scroller
        self.append(scroller)

        self.speed = Gtk.Label(xalign=0, css_classes=["dim-label"])
        self.append(self.speed)

        row = Gtk.Box(spacing=8)
        self.entry = Gtk.Entry(hexpand=True, placeholder_text=_("Type a message"))
        self.entry.connect("activate", lambda *_a: self.send())
        self.send_btn = Gtk.Button(label=_("Send"), css_classes=["suggested-action"])
        self.send_btn.connect("clicked", lambda *_a: self.send())
        self.clear_btn = Gtk.Button(label=_("Clear"))
        self.clear_btn.connect("clicked", lambda *_a: self.clear())
        row.append(self.entry)
        row.append(self.send_btn)
        row.append(self.clear_btn)
        self.append(row)

    def _append(self, who, text):
        buf = self.view.get_buffer()
        buf.insert(buf.get_end_iter(), "%s\n%s\n\n" % (who, text))
        adj = self._scroller.get_vadjustment()
        adj.set_value(adj.get_upper())

    def clear(self):
        self._history = []
        self.view.get_buffer().set_text("")
        self.speed.set_text("")

    def send(self):
        text = self.entry.get_text().strip()
        if not text or self._busy:
            return
        self._busy = True
        self.send_btn.set_sensitive(False)
        self.entry.set_text("")
        self._history.append({"role": "user", "content": text})
        self._append(_("You"), text)
        self.speed.set_text(_("Waiting for the model…"))
        messages = list(self._history)

        def done(reply):
            self._busy = False
            self.send_btn.set_sensitive(True)
            content = reply.get("content", "")
            self._history.append({"role": "assistant", "content": content})
            self._append(_("Model"), content)
            self.speed.set_text(
                _("%(tokens)d tokens · %(speed).1f tokens/s")
                % {"tokens": reply.get("tokens_predicted", 0), "speed": reply.get("tokens_per_second", 0.0)}
            )

        def failed(exc):
            self._busy = False
            self.send_btn.set_sensitive(True)
            if self._history and self._history[-1]["role"] == "user":
                self._history.pop()
            self.speed.set_text("")
            self.ctx.toast_error(exc)

        run_async(lambda: self.ctx.client.chat(messages, MAX_TOKENS), done, failed)
