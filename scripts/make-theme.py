#!/usr/bin/env python3
"""Opereta 데스크톱 테마(밝은 Opereta, 어두운 Opereta-Dark)를 만든다. 결과는 저장소에 커밋한다.

실행은 Pillow 가 있는 컨테이너에서 한다(scripts/make-brand-assets.sh 가 함께 부른다).

만드는 것:
  packages/opereta-desktop/src/themes/<이름>/gtk-3.0/gtk.css   GTK3 테마(Xfce 패널, Thunar 같은 GTK3 앱)
  packages/opereta-desktop/src/themes/<이름>/xfwm4/themerc      창 관리자 테마 설정
  packages/opereta-desktop/src/themes/<이름>/index.theme
  assets/brand/generated/xfwm4/<light|dark>/*.png             창 관리자 테마 이미지(제목 줄, 테두리, 버튼)
  packages/opereta-desktop/src/icons/Opereta/                  창 컨트롤 아이콘(x, -, +). GTK4 앱(제어 센터)이 쓴다

디자인 원칙(docs/design.md): 회색조만 쓴다. 각진 모서리 대신 곡률이 있는 사각형을 쓴다. 창 버튼은 x(닫기), -(최소화),
+(최대화) 기호이고 항상 보인다. 글꼴은 Pretendard.
"""

import os
import sys

from PIL import Image, ImageDraw

ROOT = os.path.normpath(os.path.join(os.path.dirname(__file__), ".."))
THEMES = os.path.join(ROOT, "packages", "opereta-desktop", "src", "themes")
ICONS = os.path.join(ROOT, "packages", "opereta-desktop", "src", "icons", "Opereta")
XFWM = os.path.join(ROOT, "assets", "brand", "generated", "xfwm4")

S = 8  # 안티앨리어싱용 확대 배율
R = 12  # 창 모서리 반지름
TITLE_H = 34
BORDER = 3
BTN_W = 26  # 버튼 이미지 너비(원은 14px, 나머지는 여백)
BTN_D = 14  # 버튼 원의 지름

PALETTES = {
    "light": dict(
        name="Opereta", dark=False, gtk_base="gtk-contained.css",
        window="#f2f2f2", chrome="#ececec", chrome_inactive="#f5f5f5", surface="#ffffff", sidebar="#e9e9e9",
        border="#d2d2d5", hairline="#e2e2e5", text="#1d1d1f", text2="#6e6e73", disabled="#a8a8ad",
        accent="#1d1d1f", accent_fg="#ffffff", select="#dfdfe2", hover="#e6e6e9", press="#d9d9dd",
        btn_bg="#ffffff", btn_border="#cfcfd3", tooltip="#2b2b2d", tooltip_fg="#f2f2f4",
        circle="#d8d8db", circle_hover="#c4c4c8", circle_press="#adadb1", circle_inactive="#e6e6e8",
        glyph="#46464a", glyph_inactive="#a5a5aa", glyph_hover="#1d1d1f",
    ),
    "dark": dict(
        name="Opereta-Dark", dark=True, gtk_base="gtk-contained-dark.css",
        window="#1e1e1e", chrome="#2b2b2c", chrome_inactive="#242425", surface="#171717", sidebar="#252526",
        border="#3b3b3d", hairline="#323234", text="#f2f2f4", text2="#a1a1a6", disabled="#6a6a6f",
        accent="#f2f2f4", accent_fg="#1d1d1f", select="#3b3b3e", hover="#343436", press="#3f3f42",
        btn_bg="#323234", btn_border="#454548", tooltip="#f2f2f4", tooltip_fg="#1d1d1f",
        circle="#4a4a4d", circle_hover="#5f5f63", circle_press="#727277", circle_inactive="#353537",
        glyph="#e6e6ea", glyph_inactive="#7d7d82", glyph_hover="#ffffff",
    ),
}


def rgb(h):
    h = h.lstrip("#")
    return tuple(int(h[i:i + 2], 16) for i in (0, 2, 4))


# ---------------------------------------------------------------- xfwm4 이미지

def virtual_frame(p, active):
    """창 전체 모양을 한 장으로 그린다. 클라이언트 영역은 투명으로 비운다."""
    w, h = 120 * S, 100 * S
    img = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    line = rgb(p["border"]) if active else rgb(p["hairline"])
    body = rgb(p["chrome"]) if active else rgb(p["chrome_inactive"])
    d.rounded_rectangle((0, 0, w - 1, h - 1), radius=R * S, fill=line)
    d.rounded_rectangle((S, S, w - 1 - S, h - 1 - S), radius=(R - 1) * S, fill=body)
    # 제목 줄 아래에 한 줄의 가는 선을 둔다.
    d.rectangle((S, (TITLE_H - 1) * S, w - 1 - S, TITLE_H * S - 1), fill=rgb(p["hairline"]))
    # 클라이언트 영역을 비운다.
    d.rectangle((BORDER * S, TITLE_H * S, w - 1 - BORDER * S, h - 1 - BORDER * S), fill=(0, 0, 0, 0))
    return img.resize((120, 100), Image.LANCZOS)


def frame_pieces(p, state):
    f = virtual_frame(p, state == "active")
    w, h = f.size
    cw = R + 2  # 위쪽 모서리 조각의 너비
    mid_x = w // 2
    mid_y = TITLE_H + (h - TITLE_H - BORDER) // 2
    pieces = {
        f"top-left-{state}": f.crop((0, 0, cw, TITLE_H)),
        f"top-right-{state}": f.crop((w - cw, 0, w, TITLE_H)),
        f"left-{state}": f.crop((0, mid_y, BORDER, mid_y + 24)),
        f"right-{state}": f.crop((w - BORDER, mid_y, w, mid_y + 24)),
        f"bottom-{state}": f.crop((mid_x, h - BORDER, mid_x + 24, h)),
        f"bottom-left-{state}": f.crop((0, h - (R + 4), R + 4, h)),
        f"bottom-right-{state}": f.crop((w - (R + 4), h - (R + 4), w, h)),
    }
    for i in range(1, 6):
        pieces[f"title-{i}-{state}"] = f.crop((mid_x + 4 * i, 0, mid_x + 4 * i + 4, TITLE_H))
    return pieces


def draw_glyph(d, name, cx, cy, fg, lw):
    r = int(3.0 * S)
    if name == "close":  # x
        d.line((cx - r, cy - r, cx + r, cy + r), fill=fg, width=lw)
        d.line((cx - r, cy + r, cx + r, cy - r), fill=fg, width=lw)
    elif name == "hide":  # -
        d.line((cx - r - 1, cy, cx + r + 1, cy), fill=fg, width=lw)
    elif name in ("maximize", "maximize-toggled"):  # +
        d.line((cx - r - 1, cy, cx + r + 1, cy), fill=fg, width=lw)
        d.line((cx, cy - r - 1, cx, cy + r + 1), fill=fg, width=lw)
    elif name in ("shade", "shade-toggled"):  # 가로선(펼친 상태는 아래 점)
        d.line((cx - r, cy - int(1.5 * S), cx + r, cy - int(1.5 * S)), fill=fg, width=lw)
        if name == "shade-toggled":
            d.line((cx - r, cy + int(1.5 * S), cx + r, cy + int(1.5 * S)), fill=fg, width=lw)
    elif name == "stick":
        d.ellipse((cx - 2 * S, cy - 2 * S, cx + 2 * S, cy + 2 * S), outline=fg, width=lw)
    elif name == "stick-toggled":
        d.ellipse((cx - 2 * S, cy - 2 * S, cx + 2 * S, cy + 2 * S), fill=fg)
    elif name == "menu":  # 아래쪽 화살표
        d.line((cx - r, cy - S, cx, cy + S), fill=fg, width=lw)
        d.line((cx, cy + S, cx + r, cy - S), fill=fg, width=lw)


def button(p, name, kind):
    """BTN_W x TITLE_H 이미지에 지름 BTN_D 의 원과 기호를 그린다. 원 밖은 제목 줄 색이다."""
    active = kind != "inactive"
    title_bg = rgb(p["chrome"]) if active else rgb(p["chrome_inactive"])
    w, h = BTN_W * S, TITLE_H * S
    img = Image.new("RGBA", (w, h), title_bg + (255,))
    d = ImageDraw.Draw(img)
    # 제목 줄 아래의 선과 이어진다.
    d.rectangle((0, (TITLE_H - 1) * S, w, h), fill=rgb(p["hairline"]))
    fill = {"active": p["circle"], "inactive": p["circle_inactive"], "prelight": p["circle_hover"], "pressed": p["circle_press"]}[kind]
    fg = {"active": p["glyph"], "inactive": p["glyph_inactive"], "prelight": p["glyph_hover"], "pressed": p["glyph_hover"]}[kind]
    cx, cy = w // 2, (TITLE_H - 1) * S // 2
    rad = BTN_D * S // 2
    d.ellipse((cx - rad, cy - rad, cx + rad, cy + rad), fill=rgb(fill))
    draw_glyph(d, name, cx, cy, rgb(fg), int(1.5 * S))
    return img.resize((BTN_W, TITLE_H), Image.LANCZOS)


def make_xfwm4(key, p):
    out = os.path.join(XFWM, key)
    os.makedirs(out, exist_ok=True)
    pieces = {}
    for state in ("active", "inactive"):
        pieces.update(frame_pieces(p, state))
    for n in ("close", "hide", "maximize", "maximize-toggled", "menu", "shade", "shade-toggled", "stick", "stick-toggled"):
        for kind in ("active", "inactive", "prelight", "pressed"):
            pieces[f"{n}-{kind}"] = button(p, n, kind)
    for name, im in pieces.items():
        im.save(os.path.join(out, name + ".png"), optimize=True)
    return len(pieces)


# ---------------------------------------------------------------- 창 관리자 설정

def themerc(p):
    return f"""# Opereta 창 관리자 테마({p['name']}). 이미지는 scripts/make-theme.py 가 만든다.
active_text_color={p['text']}
inactive_text_color={p['text2']}
active_text_shadow_color={p['chrome']}
inactive_text_shadow_color={p['chrome_inactive']}
active_border_color={p['border']}
inactive_border_color={p['hairline']}
active_color_1={p['chrome']}
active_color_2={p['chrome']}
active_hilight_1={p['hairline']}
active_hilight_2={p['hairline']}
active_mid_1={p['chrome']}
active_mid_2={p['chrome']}
active_shadow_1={p['border']}
active_shadow_2={p['border']}
inactive_color_1={p['chrome_inactive']}
inactive_color_2={p['chrome_inactive']}
inactive_hilight_1={p['hairline']}
inactive_hilight_2={p['hairline']}
inactive_mid_1={p['chrome_inactive']}
inactive_mid_2={p['chrome_inactive']}
inactive_shadow_1={p['hairline']}
inactive_shadow_2={p['hairline']}
button_offset=10
button_spacing=2
full_width_title=true
frame_border_top=0
maximized_offset=0
show_app_icon=false
shadow_delta_height=0
shadow_delta_width=0
shadow_delta_x=0
shadow_delta_y=-6
shadow_opacity=45
title_horizontal_offset=0
title_shadow_active=false
title_shadow_inactive=false
title_vertical_offset_active=0
title_vertical_offset_inactive=0
"""


def index_theme(p):
    return f"""[Desktop Entry]
Type=X-GNOME-Metatheme
Name={p['name']}
Comment=Opereta {'어두운' if p['dark'] else '밝은'} 데스크톱 테마
Encoding=UTF-8

[X-GNOME-Metatheme]
GtkTheme={p['name']}
MetacityTheme={p['name']}
IconTheme=Opereta
"""


# ---------------------------------------------------------------- GTK3 CSS

GTK_CSS = """/* {name} GTK3 테마. scripts/make-theme.py 가 만든다. 직접 고치지 않는다(값은 PALETTES 에서 바꾼다).
 * libgtk-3 에 내장된 Adwaita 스타일을 가져와 색 이름과 모양(곡률, 여백)만 바꾼다. 팔레트는 docs/design.md 에 있다. */
@import url("resource:///org/gtk/libgtk/theme/Adwaita/{gtk_base}");

@define-color theme_bg_color {window};
@define-color theme_fg_color {text};
@define-color theme_base_color {surface};
@define-color theme_text_color {text};
@define-color theme_selected_bg_color {select};
@define-color theme_selected_fg_color {text};
@define-color theme_tooltip_bg_color {tooltip};
@define-color theme_tooltip_fg_color {tooltip_fg};
@define-color insensitive_bg_color {window};
@define-color insensitive_fg_color {disabled};
@define-color insensitive_base_color {surface};
@define-color theme_unfocused_bg_color {window};
@define-color theme_unfocused_fg_color {text2};
@define-color theme_unfocused_base_color {surface};
@define-color theme_unfocused_text_color {text2};
@define-color theme_unfocused_selected_bg_color {hover};
@define-color theme_unfocused_selected_fg_color {text};
@define-color borders {border};
@define-color unfocused_borders {hairline};
@define-color content_view_bg {surface};
@define-color text_view_bg {surface};

* {{
    outline-style: none;
}}

/* 창 */
window.background {{
    background-color: @theme_bg_color;
    color: @theme_fg_color;
}}
window.csd, window.csd decoration {{
    border-radius: 12px;
}}
decoration {{
    box-shadow: 0 6px 24px alpha(black, 0.28), 0 0 0 1px {border};
}}
headerbar, .titlebar {{
    background-image: none;
    background-color: {chrome};
    border-bottom: 1px solid {hairline};
    box-shadow: none;
    border-radius: 12px 12px 0 0;
    min-height: 40px;
    padding: 0 8px;
}}
headerbar .title {{
    font-weight: 600;
}}

/* 버튼 */
button {{
    background-image: none;
    background-color: {btn_bg};
    color: @theme_fg_color;
    border: 1px solid {btn_border};
    border-radius: 8px;
    box-shadow: none;
    padding: 5px 12px;
    min-height: 24px;
    text-shadow: none;
}}
button:hover {{
    background-color: {hover};
}}
button:active, button:checked {{
    background-color: {press};
}}
button:disabled {{
    color: @insensitive_fg_color;
    background-color: {window};
}}
button.flat, headerbar button, toolbar button {{
    background-color: transparent;
    border-color: transparent;
}}
button.flat:hover, headerbar button:hover, toolbar button:hover {{
    background-color: {hover};
}}
button.suggested-action, headerbar button.suggested-action {{
    background-color: {accent};
    color: {accent_fg};
    border-color: {accent};
}}
button.suggested-action:hover {{
    background-color: alpha({accent}, 0.88);
}}
button.circular {{
    border-radius: 999px;
}}

/* 입력 */
entry, spinbutton, combobox button, textview text {{
    background-color: {surface};
    border: 1px solid {btn_border};
    border-radius: 8px;
    box-shadow: none;
    min-height: 24px;
    padding: 4px 8px;
}}
entry:focus, spinbutton:focus-within {{
    border-color: {accent};
    box-shadow: 0 0 0 2px alpha({accent}, 0.18);
}}
switch {{
    border-radius: 999px;
    background-color: {border};
    border: none;
    box-shadow: none;
    min-height: 24px;
    min-width: 44px;
}}
switch:checked {{
    background-color: {accent};
}}
switch slider {{
    border-radius: 999px;
    background-color: {surface};
    border: none;
    box-shadow: 0 1px 3px alpha(black, 0.3);
    min-height: 20px;
    min-width: 20px;
    margin: 2px;
}}
scale trough {{
    border-radius: 999px;
    background-color: {border};
    min-height: 6px;
}}
scale highlight {{
    border-radius: 999px;
    background-color: {accent};
}}
scale slider {{
    border-radius: 999px;
    background-color: {surface};
    border: 1px solid {btn_border};
    box-shadow: 0 1px 3px alpha(black, 0.25);
    min-width: 18px;
    min-height: 18px;
}}
progressbar trough {{
    border-radius: 999px;
    background-color: {border};
    min-height: 6px;
}}
progressbar progress {{
    border-radius: 999px;
    background-color: {accent};
}}
check, radio {{
    border-radius: 6px;
    background-image: none;
    background-color: {surface};
    border: 1px solid {btn_border};
    box-shadow: none;
}}
radio {{
    border-radius: 999px;
}}
check:checked, radio:checked {{
    background-color: {accent};
    color: {accent_fg};
    border-color: {accent};
}}

/* 메뉴와 팝오버 */
menu, .menu, .popup, popover.background {{
    background-color: {surface};
    border: 1px solid {border};
    border-radius: 10px;
    padding: 4px;
    box-shadow: 0 8px 28px alpha(black, 0.25);
}}
menuitem {{
    border-radius: 6px;
    padding: 5px 10px;
    min-height: 22px;
}}
menuitem:hover {{
    background-color: {select};
}}
popover.background > contents {{
    border-radius: 10px;
}}
tooltip, tooltip.background {{
    border-radius: 8px;
    background-color: {tooltip};
    color: {tooltip_fg};
    border: none;
}}

/* 목록과 사이드바 */
list, treeview.view, iconview {{
    background-color: {surface};
}}
row, treeview.view:selected, iconview:selected {{
    border-radius: 8px;
}}
row:selected, treeview.view:selected, iconview:selected {{
    background-color: {select};
    color: @theme_fg_color;
}}
.sidebar, placessidebar, placessidebar list {{
    background-color: {sidebar};
}}
placessidebar row {{
    border-radius: 8px;
    margin: 1px 6px;
}}
scrollbar slider {{
    border-radius: 999px;
    background-color: alpha({text2}, 0.55);
    min-width: 6px;
    min-height: 6px;
}}
scrollbar trough {{
    background-color: transparent;
}}
notebook > header {{
    background-color: {window};
    border-color: {hairline};
}}
notebook > header > tabs > tab {{
    border-radius: 8px 8px 0 0;
    padding: 4px 12px;
}}
notebook > header > tabs > tab:checked {{
    background-color: {surface};
}}
selection, *:selected {{
    background-color: {select};
    color: @theme_fg_color;
}}

/* Xfce 패널과 시작 메뉴 */
.xfce4-panel, #XfcePanelWindow {{
    background-color: {chrome};
    border-top: 1px solid {border};
    color: @theme_fg_color;
}}
.xfce4-panel button, #XfcePanelWindow button {{
    background-color: transparent;
    border: 1px solid transparent;
    border-radius: 8px;
    padding: 2px 8px;
    min-height: 22px;
}}
.xfce4-panel button:hover, #XfcePanelWindow button:hover {{
    background-color: {hover};
}}
.xfce4-panel button:checked, #XfcePanelWindow button:checked {{
    background-color: {select};
}}
#whiskermenu-window {{
    border-radius: 12px;
    border: 1px solid {border};
}}
"""


def write_gtk(p):
    d = os.path.join(THEMES, p["name"])
    os.makedirs(os.path.join(d, "gtk-3.0"), exist_ok=True)
    os.makedirs(os.path.join(d, "xfwm4"), exist_ok=True)
    with open(os.path.join(d, "gtk-3.0", "gtk.css"), "w", encoding="utf-8") as f:
        f.write(GTK_CSS.format(**p))
    with open(os.path.join(d, "xfwm4", "themerc"), "w", encoding="utf-8") as f:
        f.write(themerc(p))
    with open(os.path.join(d, "index.theme"), "w", encoding="utf-8") as f:
        f.write(index_theme(p))


# ---------------------------------------------------------------- 아이콘(창 컨트롤)

def svg(body):
    return ('<?xml version="1.0" encoding="UTF-8"?>\n'
            '<svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 16 16">'
            f'<g fill="none" stroke="#000" stroke-width="1.8" stroke-linecap="round">{body}</g></svg>\n')


def write_icons():
    base = os.path.join(ICONS, "scalable", "ui")
    os.makedirs(base, exist_ok=True)
    glyphs = {
        "window-close-symbolic": '<path d="M4.5 4.5l7 7M11.5 4.5l-7 7"/>',
        "window-minimize-symbolic": '<path d="M3.5 8h9"/>',
        "window-maximize-symbolic": '<path d="M3.5 8h9M8 3.5v9"/>',
        "window-restore-symbolic": '<path d="M3.5 8h9M8 3.5v9"/>',
    }
    for name, body in glyphs.items():
        with open(os.path.join(base, name + ".svg"), "w", encoding="utf-8") as f:
            f.write(svg(body))
    # GTK4(libadwaita)는 내장 기호가 먼저라 아이콘 테마로 바꿀 수 없다. 제어 센터는 이 고정 색 PNG(24px, 12px 크기로
    # 쓰는 2배 이미지)를 CSS 배경으로 쓴다. SVG 가 아니라 PNG 인 것은 그림 적재 모듈(librsvg)에 기대지 않기 위해서다.
    for scheme in ("light", "dark"):
        color = rgb(PALETTES[scheme]["glyph"])
        for key in ("close", "minimize", "maximize"):
            s_, size = 8, 24
            im = Image.new("RGBA", (size * s_, size * s_), (0, 0, 0, 0))
            d = ImageDraw.Draw(im)
            c = size * s_ // 2
            r = int(size * s_ * 0.27)
            lw = int(size * s_ * 0.11)
            if key == "close":
                d.line((c - r, c - r, c + r, c + r), fill=color, width=lw)
                d.line((c - r, c + r, c + r, c - r), fill=color, width=lw)
            elif key == "minimize":
                d.line((c - r, c, c + r, c), fill=color, width=lw)
            else:
                d.line((c - r, c, c + r, c), fill=color, width=lw)
                d.line((c, c - r, c, c + r), fill=color, width=lw)
            im.resize((size, size), Image.LANCZOS).save(os.path.join(base, f"ctl-{key}-{scheme}.png"), optimize=True)
    with open(os.path.join(ICONS, "index.theme"), "w", encoding="utf-8") as f:
        f.write("""[Icon Theme]
Name=Opereta
Comment=Opereta 아이콘(Adwaita 를 바탕으로 창 컨트롤만 x, -, + 기호로 바꾼다)
Inherits=Adwaita,hicolor
Directories=scalable/ui

[scalable/ui]
Context=UI
Size=16
MinSize=8
MaxSize=512
Type=Scalable
""")


def main():
    total = 0
    for key, p in PALETTES.items():
        total += make_xfwm4(key, p)
        write_gtk(p)
    write_icons()
    print(f"make-theme: {len(PALETTES)} themes, {total} xfwm4 images, window control icons")


if __name__ == "__main__":
    sys.exit(main())
