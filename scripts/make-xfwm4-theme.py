#!/usr/bin/env python3
"""xfwm4 테마 `Diparnux` 의 이미지를 만든다. 결과는 assets/brand/generated/xfwm4/ 에 쓰고 커밋한다.

실행은 Pillow 가 있는 컨테이너에서 한다(scripts/make-brand-assets.sh 가 함께 부른다).
색은 docs/design.md 의 흑백 팔레트만 쓴다. xfwm4 테마는 프레임 조각과 버튼 이미지로 이루어진다.
Debian 의 Default 테마와 같은 파일 이름과 크기를 쓴다(title-1~5: 4x29, 좌우 테두리 5x24, 아래 테두리 24x5,
모서리 8x29 와 16x16, 버튼 21x29). 모서리는 반지름 6px 의 둥근 모양이며 알파가 창 모양을 정한다.
"""

import os

from PIL import Image, ImageDraw

OUT = os.path.join(os.path.dirname(__file__), "..", "assets", "brand", "generated", "xfwm4")

S = 4  # 안티앨리어싱용 확대 배율
RADIUS = 6
TITLE_H = 29
BORDER = 5
FRAME_W, FRAME_H = 80, 100

STATES = {
    # 이름: (제목 줄 바탕, 테두리 바탕, 바깥 선, 글자와 기호)
    "active": ("#0d0d0d", "#0d0d0d", "#ebebeb", "#ebebeb"),
    "inactive": ("#1a1a1a", "#1a1a1a", "#3a3a3a", "#8e8e8e"),
}
HOVER_BG = "#2b2b2b"
PRESS_BG = "#3a3a3a"
HOVER_FG = "#ffffff"


def virtual_frame(title_bg, border_bg, line):
    """창 전체 모양을 한 장으로 그린다. 클라이언트 영역은 투명으로 비운다."""
    w, h = FRAME_W * S, FRAME_H * S
    img = Image.new("RGBA", (w, h), (0, 0, 0, 0))
    d = ImageDraw.Draw(img)
    d.rounded_rectangle((0, 0, w - 1, h - 1), radius=RADIUS * S, fill=line)
    d.rounded_rectangle((S, S, w - 1 - S, h - 1 - S), radius=(RADIUS - 1) * S, fill=border_bg)
    d.rectangle((S, S, w - 1 - S, TITLE_H * S - 1), fill=title_bg)
    # 위쪽 두 모서리는 위 선을 이어 그리기 위해 다시 둥근 모양으로 칠한다.
    d.rounded_rectangle((S, S, w - 1 - S, (TITLE_H + RADIUS) * S), radius=(RADIUS - 1) * S, fill=title_bg)
    d.rectangle((S, (TITLE_H - 1) * S, w - 1 - S, (TITLE_H + RADIUS) * S), fill=border_bg)
    d.rectangle((S, S * TITLE_H, w - 1 - S, h - 1 - S), fill=border_bg)
    # 클라이언트 영역을 비운다.
    d.rectangle((BORDER * S, TITLE_H * S, w - 1 - BORDER * S, h - 1 - BORDER * S), fill=(0, 0, 0, 0))
    return img.resize((FRAME_W, FRAME_H), Image.LANCZOS)


def frame_pieces(state):
    title_bg, border_bg, line, _fg = STATES[state]
    f = virtual_frame(title_bg, border_bg, line)
    w, h = FRAME_W, FRAME_H
    mid_x = w // 2
    mid_y = TITLE_H + (h - TITLE_H - BORDER) // 2
    return {
        f"top-left-{state}": f.crop((0, 0, 8, TITLE_H)),
        f"top-right-{state}": f.crop((w - 8, 0, w, TITLE_H)),
        f"left-{state}": f.crop((0, mid_y, BORDER, mid_y + 24)),
        f"right-{state}": f.crop((w - BORDER, mid_y, w, mid_y + 24)),
        f"bottom-{state}": f.crop((mid_x, h - BORDER, mid_x + 24, h)),
        f"bottom-left-{state}": f.crop((0, h - 16, 16, h)),
        f"bottom-right-{state}": f.crop((w - 16, h - 16, w, h)),
        **{f"title-{i}-{state}": f.crop((mid_x + 4 * i, 0, mid_x + 4 * i + 4, TITLE_H)) for i in range(1, 6)},
    }


def glyph(name, d, fg):
    """21x29 버튼 영역(확대 좌표)에 기호를 그린다. 기호는 약 9x9 이고 가운데에서 약간 아래에 둔다."""
    cx, cy = int(10.5 * S), int(15 * S)
    r = int(4.5 * S)
    lw = int(1.6 * S)
    if name == "close":
        d.line((cx - r, cy - r, cx + r, cy + r), fill=fg, width=lw)
        d.line((cx - r, cy + r, cx + r, cy - r), fill=fg, width=lw)
    elif name == "hide":
        d.line((cx - r, cy + r, cx + r, cy + r), fill=fg, width=lw)
    elif name == "maximize":
        d.rectangle((cx - r, cy - r, cx + r, cy + r), outline=fg, width=lw)
    elif name == "maximize-toggled":
        o = int(2 * S)
        d.rectangle((cx - r + o, cy - r - o + o, cx + r, cy + r - o), outline=fg, width=lw)
        d.rectangle((cx - r, cy - r + o + o, cx + r - o, cy + r), outline=fg, width=lw)
    elif name == "menu":
        d.polygon([(cx - r, cy - int(1.5 * S)), (cx + r, cy - int(1.5 * S)), (cx, cy + int(3 * S))], fill=fg)
    elif name == "shade":
        d.line((cx - r, cy - r + S, cx + r, cy - r + S), fill=fg, width=lw)
    elif name == "shade-toggled":
        d.line((cx - r, cy - r + S, cx + r, cy - r + S), fill=fg, width=lw)
        d.polygon([(cx - r, cy - int(1 * S)), (cx + r, cy - int(1 * S)), (cx, cy + int(4 * S))], fill=fg)
    elif name == "stick":
        d.ellipse((cx - int(3 * S), cy - int(3 * S), cx + int(3 * S), cy + int(3 * S)), outline=fg, width=lw)
    elif name == "stick-toggled":
        d.ellipse((cx - int(3 * S), cy - int(3 * S), cx + int(3 * S), cy + int(3 * S)), fill=fg)


def button(name, state, kind):
    title_bg, _border_bg, line, fg = STATES[state]
    w, h = 21 * S, TITLE_H * S
    img = Image.new("RGBA", (w, h), title_bg)
    d = ImageDraw.Draw(img)
    # 제목 줄 위 선과 이어지게 맨 위 한 줄은 선 색이다.
    d.rectangle((0, 0, w, S - 1), fill=line)
    if kind in ("prelight", "pressed"):
        bg = HOVER_BG if kind == "prelight" else PRESS_BG
        d.rounded_rectangle((int(1.5 * S), int(4 * S), w - int(1.5 * S), h - int(4 * S)), radius=4 * S, fill=bg)
        fg = HOVER_FG
    glyph(name, d, fg)
    return img.resize((21, TITLE_H), Image.LANCZOS)


def main():
    os.makedirs(OUT, exist_ok=True)
    pieces = {}
    for state in STATES:
        pieces.update(frame_pieces(state))
    names = ["close", "hide", "maximize", "maximize-toggled", "menu", "shade", "shade-toggled", "stick", "stick-toggled"]
    for n in names:
        for kind in ("active", "inactive", "prelight", "pressed"):
            state = "inactive" if kind == "inactive" else "active"
            pieces[f"{n}-{kind}"] = button(n, state, kind)
    for name, im in pieces.items():
        im.save(os.path.join(OUT, name + ".png"), optimize=True)
    print(f"make-xfwm4-theme: wrote {len(pieces)} images to {os.path.relpath(OUT)}")


if __name__ == "__main__":
    main()
