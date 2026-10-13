#!/usr/bin/env python3
"""사용자가 준 로고(assets/brand/source/lightlix-logo-1024.png)에서 이미지 자산을 만든다.

실행은 Pillow 가 있는 컨테이너에서 한다(scripts/make-brand-assets.sh). 결과는 assets/brand/generated/ 에 쓰고 커밋한다.
로고: 밝은 회색(#EBEBEB) 둥근 사각형 위에 검은 굵은 글자 LT / IX. 색은 흑백 두 가지뿐이다.

만드는 것:
  lightlix-<N>.png            앱 아이콘(둥근 사각형) 16~512
  lightlix-mark-white.png     글자만 흰색, 배경 투명(어두운 바탕용) 512
  lightlix-mark-black.png     글자만 검정, 배경 투명 512
  wallpaper.png               어두운 바탕용 희미한 마크 256(가운데 배치)
  calamares-logo.png          Calamares 사이드바 로고 128
  calamares-welcome.png       Calamares 환영 화면 이미지 640x300(어두운 바탕에 흰 마크)
  grub-background-1280x720.png 참고용(GRUB 배경은 쓰지 않는다. 색만 쓴다)
"""

import os
import sys

from PIL import Image

SRC = os.path.join(os.path.dirname(__file__), "..", "assets", "brand", "source", "lightlix-logo-1024.png")
OUT = os.path.join(os.path.dirname(__file__), "..", "assets", "brand", "generated")

BG_DARK = (13, 13, 13, 255)  # #0D0D0D
MARK_DIM = (58, 58, 58, 255)  # 배경 마크의 색(#3A3A3A)


def load():
    im = Image.open(SRC).convert("RGBA")
    if im.size != (1024, 1024):
        raise SystemExit(f"unexpected logo size {im.size}")
    return im


def glyph_alpha(im):
    """검은 글자의 불투명도(0~255)를 돌려준다. 배경(#EBEBEB)은 0, 글자(#000)는 255 가 된다."""
    lum = im.convert("L")
    # 둥근 사각형 밖(투명)은 0 으로 둔다.
    alpha = im.split()[3]
    out = Image.new("L", im.size, 0)
    px_l, px_a, px_o = lum.load(), alpha.load(), out.load()
    for y in range(im.size[1]):
        for x in range(im.size[0]):
            if px_a[x, y] < 128:
                continue
            v = px_l[x, y]
            # 235 -> 0, 0 -> 255
            px_o[x, y] = max(0, min(255, int((235 - v) * 255 / 235)))
    return out


def mark(im, rgb):
    """글자만 rgb 색으로, 나머지는 투명한 RGBA 이미지를 만든다. 글자 영역으로 잘라 여백을 둔다."""
    a = glyph_alpha(im)
    box = a.getbbox()
    layer = Image.new("RGBA", im.size, rgb + (0,))
    layer.putalpha(a)
    layer = layer.crop(box)
    pad = int(max(layer.size) * 0.18)
    canvas = Image.new("RGBA", (layer.size[0] + 2 * pad, layer.size[1] + 2 * pad), rgb + (0,))
    canvas.paste(layer, (pad, pad), layer)
    return canvas


def fit(img, size):
    return img.resize((size, size), Image.LANCZOS)


def on_dark(mark_img, size, bg=BG_DARK):
    """어두운 정사각형 바탕 가운데에 마크를 놓는다."""
    canvas = Image.new("RGBA", (size, size), bg)
    m = mark_img.copy()
    scale = size * 0.5 / max(m.size)
    m = m.resize((max(1, int(m.size[0] * scale)), max(1, int(m.size[1] * scale))), Image.LANCZOS)
    canvas.paste(m, ((size - m.size[0]) // 2, (size - m.size[1]) // 2), m)
    return canvas


def main():
    os.makedirs(OUT, exist_ok=True)
    im = load()
    for n in (16, 24, 32, 48, 64, 96, 128, 256, 512):
        fit(im, n).save(os.path.join(OUT, f"lightlix-{n}.png"), optimize=True)
    white = mark(im, (255, 255, 255))
    black = mark(im, (0, 0, 0))
    fit(white, 512).save(os.path.join(OUT, "lightlix-mark-white.png"), optimize=True)
    fit(black, 512).save(os.path.join(OUT, "lightlix-mark-black.png"), optimize=True)

    dim = mark(im, MARK_DIM[:3])
    fit(dim, 256).save(os.path.join(OUT, "wallpaper.png"), optimize=True)

    fit(im, 128).save(os.path.join(OUT, "calamares-logo.png"), optimize=True)
    welcome = Image.new("RGBA", (640, 300), BG_DARK)
    wm = white.resize((220, 220), Image.LANCZOS)
    welcome.paste(wm, ((640 - 220) // 2, (300 - 220) // 2), wm)
    welcome.save(os.path.join(OUT, "calamares-welcome.png"), optimize=True)

    bg = Image.new("RGBA", (1280, 720), BG_DARK)
    wm2 = white.resize((260, 260), Image.LANCZOS)
    bg.paste(wm2, ((1280 - 260) // 2, (720 - 260) // 2), wm2)
    bg.convert("RGB").save(os.path.join(OUT, "grub-background-1280x720.png"), optimize=True)
    print("assets written to", os.path.abspath(OUT))
    for name in sorted(os.listdir(OUT)):
        print(" ", name, os.path.getsize(os.path.join(OUT, name)))


if __name__ == "__main__":
    sys.exit(main())
