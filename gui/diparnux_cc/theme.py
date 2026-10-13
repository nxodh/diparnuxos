"""Diparnux 의 색과 아이콘. 로고(흑백 두 가지)에 맞춰 강조색을 회색조로 바꾼다."""

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gdk, Gtk  # noqa: E402

ICON_NAME = "diparnux"

# libadwaita 의 이름 있는 색을 덮어쓴다. 밝은 테마는 검정 강조, 어두운 테마는 밝은 회색 강조다.
_LIGHT = """
@define-color accent_bg_color #1a1a1a;
@define-color accent_fg_color #ffffff;
@define-color accent_color #1a1a1a;
"""
_DARK = """
@define-color accent_bg_color #ebebeb;
@define-color accent_fg_color #000000;
@define-color accent_color #ebebeb;
"""

_provider = None


def _load(css):
    global _provider
    display = Gdk.Display.get_default()
    if display is None:
        return
    if _provider is None:
        _provider = Gtk.CssProvider()
        # 사용자 테마보다 높고 응용 프로그램 CSS 보다 낮지 않은 우선순위다.
        Gtk.StyleContext.add_provider_for_display(display, _provider, Gtk.STYLE_PROVIDER_PRIORITY_APPLICATION)
    _provider.load_from_string(css)


def _sync(manager, _pspec=None):
    _load(_DARK if manager.get_dark() else _LIGHT)


def apply():
    """앱 시작 때 한 번 부른다. 아이콘 이름을 정하고 밝은/어두운 테마에 맞게 강조색을 바꾼다."""
    Gtk.Window.set_default_icon_name(ICON_NAME)
    manager = Adw.StyleManager.get_default()
    _sync(manager)
    manager.connect("notify::dark", _sync)
