"""Opereta 의 색, 모양, 아이콘. 회색조만 쓰고 곡률이 있는 사각형을 쓴다(docs/design.md).

팔레트 값은 scripts/make-theme.py 의 PALETTES 와 같아야 한다(데스크톱 GTK3 테마와 이 앱이 같은 모양이 되도록).
"""

import os

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gdk, Gtk  # noqa: E402

ICON_NAME = "opereta"

# 창 컨트롤(x, -, +)의 기호 파일. opereta-desktop 의 아이콘 테마가 설치한다. 없으면 libadwaita 기본 기호를 쓴다.
_ICON_DIR = "/usr/share/icons/Opereta/scalable/ui"

_PALETTES = {
    False: dict(  # 밝은 테마
        window="#f2f2f2", chrome="#ececec", surface="#ffffff", sidebar="#e9e9e9", border="#d2d2d5", hairline="#e2e2e5",
        text="#1d1d1f", text2="#6e6e73", accent="#1d1d1f", accent_fg="#ffffff", select="#dfdfe2", hover="#e6e6e9",
        circle="#d8d8db", circle_hover="#c4c4c8",
    ),
    True: dict(  # 어두운 테마
        window="#1e1e1e", chrome="#2b2b2c", surface="#171717", sidebar="#252526", border="#3b3b3d", hairline="#323234",
        text="#f2f2f4", text2="#a1a1a6", accent="#f2f2f4", accent_fg="#1d1d1f", select="#3b3b3e", hover="#343436",
        circle="#4a4a4d", circle_hover="#5f5f63",
    ),
}

_CSS = """
@define-color window_bg_color {window};
@define-color window_fg_color {text};
@define-color view_bg_color {surface};
@define-color view_fg_color {text};
@define-color headerbar_bg_color {chrome};
@define-color headerbar_fg_color {text};
@define-color headerbar_border_color {hairline};
@define-color headerbar_backdrop_color {window};
@define-color card_bg_color {surface};
@define-color card_fg_color {text};
@define-color sidebar_bg_color {sidebar};
@define-color sidebar_fg_color {text};
@define-color sidebar_backdrop_color {sidebar};
@define-color popover_bg_color {surface};
@define-color popover_fg_color {text};
@define-color dialog_bg_color {window};
@define-color dialog_fg_color {text};
@define-color accent_bg_color {accent};
@define-color accent_fg_color {accent_fg};
@define-color accent_color {accent};

window, .background {{
    font-family: "Pretendard", "Noto Sans CJK KR", "NanumGothic", sans-serif;
}}

/* 창 컨트롤: x(닫기), -(최소화), +(최대화). 왼쪽에 둥근 원으로 둔다. */
windowcontrols button {{
    min-width: 14px;
    min-height: 14px;
    padding: 5px;
    margin: 0 3px;
    border-radius: 999px;
    background-image: none;
    background-color: {circle};
    box-shadow: none;
}}
windowcontrols button:hover {{
    background-color: {circle_hover};
}}
windowcontrols button image {{
    opacity: 0;
}}
{icon_rules}

headerbar {{
    min-height: 44px;
    border-radius: 12px 12px 0 0;
    box-shadow: none;
}}
headerbar .title {{
    font-weight: 600;
}}

/* 둥근 사각형 */
button {{
    border-radius: 8px;
    min-height: 26px;
    padding: 4px 14px;
}}
button.circular, button.pill {{
    border-radius: 999px;
}}
entry, .card, .boxed-list, preferencesgroup > box > box > list {{
    border-radius: 12px;
}}
entry {{
    border-radius: 8px;
    min-height: 28px;
}}
popover > contents, menu, .menu {{
    border-radius: 12px;
}}
modelbutton, row {{
    border-radius: 8px;
}}
row.activatable:selected, row:selected {{
    background-color: {select};
    color: {text};
}}
.navigation-sidebar row {{
    margin: 1px 8px;
    min-height: 34px;
}}
.navigation-sidebar row:selected {{
    background-color: {select};
    color: {text};
}}
switch {{
    border-radius: 999px;
}}
switch slider {{
    border-radius: 999px;
}}
progressbar trough, progressbar progress, levelbar block {{
    border-radius: 999px;
}}
preferencesgroup > box > label.heading {{
    font-weight: 600;
}}
.dim-label, .caption, .subtitle {{
    color: {text2};
}}
"""


def _icon_rules(dark):
    """창 컨트롤의 기호(x, -, +)를 버튼 배경 그림으로 그린다. 파일이 없으면 libadwaita 기본 기호를 쓴다."""
    scheme = "dark" if dark else "light"
    rules = []
    for cls in ("close", "minimize", "maximize"):
        path = os.path.join(_ICON_DIR, "ctl-%s-%s.png" % (cls, scheme))
        if os.path.exists(path):
            rules.append(
                'windowcontrols button.%s { background-image: url("file://%s"); background-repeat: no-repeat; '
                "background-position: center; background-size: 12px 12px; }" % (cls, path)
            )
    return "\n".join(rules)


def build_css(dark):
    icons = _icon_rules(dark)
    css = _CSS.format(icon_rules=icons, **_PALETTES[bool(dark)])
    if not icons:
        # 기호 파일이 없으면 기본 기호가 보이도록 숨기지 않는다.
        css = css.replace("windowcontrols button image {\n    opacity: 0;\n}", "")
    return css


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
    _load(build_css(manager.get_dark()))


def apply():
    """앱 시작 때 한 번 부른다. 아이콘 이름을 정하고 밝은/어두운 테마에 맞게 색과 모양을 바꾼다."""
    Gtk.Window.set_default_icon_name(ICON_NAME)
    manager = Adw.StyleManager.get_default()
    _sync(manager)
    manager.connect("notify::dark", _sync)
