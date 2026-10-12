"""GTK 보조 함수. 네트워크 호출은 작업 스레드에서 하고 결과는 GLib 주 루프로 돌려받는다."""

import threading

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, GLib, Gtk  # noqa: E402

from . import api  # noqa: E402
from .i18n import _  # noqa: E402


def run_async(fn, on_done=None, on_error=None):
    """fn 을 스레드에서 실행하고 결과를 주 루프에서 on_done 으로, 예외를 on_error 로 넘긴다."""

    def invoke(callback, arg):
        try:
            callback(arg)
        except Exception:  # 닫힌 창 등으로 콜백이 실패해도 앱을 죽이지 않는다
            import traceback

            traceback.print_exc()
        return GLib.SOURCE_REMOVE

    def worker():
        try:
            result = fn()
        except Exception as exc:  # noqa: BLE001 - 모든 실패를 사용자에게 보인다
            if on_error is not None:
                GLib.idle_add(invoke, on_error, exc)
            return
        if on_done is not None:
            GLib.idle_add(invoke, on_done, result)

    threading.Thread(target=worker, daemon=True).start()


def error_text(exc):
    """예외를 사용자에게 보일 문장으로 바꾼다."""
    if isinstance(exc, api.DaemonUnavailable):
        if exc.code == "permission_denied":
            return _("Permission denied. Add your user to the 'lightlix' group and log in again.")
        return _("The Lightlix daemon is not running.")
    if isinstance(exc, api.DaemonError):
        return exc.message or exc.code
    return str(exc)


def clear_group(group, rows):
    """group 에 넣었던 행을 모두 지우고 목록을 비운다."""
    for row in rows:
        group.remove(row)
    rows.clear()


def action_row(title, subtitle="", suffix=None, activatable=False):
    row = Adw.ActionRow(title=GLib.markup_escape_text(title), subtitle=GLib.markup_escape_text(subtitle))
    row.set_subtitle_lines(0)
    row.set_title_lines(0)
    if suffix is not None:
        row.add_suffix(suffix)
    row.set_activatable(activatable)
    return row


def icon_button(icon_name, tooltip, callback, css=None):
    btn = Gtk.Button(icon_name=icon_name, tooltip_text=tooltip, valign=Gtk.Align.CENTER)
    btn.add_css_class("flat")
    if css:
        btn.add_css_class(css)
    btn.connect("clicked", lambda *_a: callback())
    return btn


def text_button(label, callback, css=None):
    btn = Gtk.Button(label=label, valign=Gtk.Align.CENTER)
    if css:
        btn.add_css_class(css)
    btn.connect("clicked", lambda *_a: callback())
    return btn


def confirm(parent, heading, body, accept_label, on_accept, destructive=False, on_cancel=None):
    """확인 대화상자를 연다. 수락하면 on_accept() 를, 취소하거나 닫으면 on_cancel() 을 부른다."""
    dialog = Adw.AlertDialog(heading=heading, body=body)
    dialog.add_response("cancel", _("Cancel"))
    dialog.add_response("accept", accept_label)
    if destructive:
        dialog.set_response_appearance("accept", Adw.ResponseAppearance.DESTRUCTIVE)
    else:
        dialog.set_response_appearance("accept", Adw.ResponseAppearance.SUGGESTED)
    dialog.set_default_response("cancel")
    dialog.set_close_response("cancel")

    def on_response(_dialog, response):
        if response == "accept":
            on_accept()
        elif on_cancel is not None:
            on_cancel()

    dialog.connect("response", on_response)
    dialog.present(parent)


def info_dialog(parent, heading, body):
    dialog = Adw.AlertDialog(heading=heading, body=body)
    dialog.add_response("ok", _("OK"))
    dialog.set_default_response("ok")
    dialog.present(parent)
    return dialog


LEVEL_ICONS = {
    "ok": "emblem-ok-symbolic",
    "info": "dialog-information-symbolic",
    "warning": "dialog-warning-symbolic",
    "error": "dialog-error-symbolic",
}


def level_icon(level):
    return Gtk.Image.new_from_icon_name(LEVEL_ICONS.get(level, "dialog-information-symbolic"))
