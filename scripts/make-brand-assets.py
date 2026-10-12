#!/usr/bin/env python3
"""사용자가 준 로고(assets/brand/source/diparnux-logo.png)에서 이미지 자산을 만든다.

실행은 Pillow 가 있는 컨테이너에서 한다(scripts/make-brand-assets.sh). 결과는 assets/brand/generated/ 와
설치기 브랜딩 디렉터리(packages/diparnux-installer/src/branding)에 쓰고 커밋한다.
로고: 밝은 회색(#F0F0F0) 원 위에 검정(#000) 굵은 글자 D. 안쪽의 삼각형 구멍으로 원 색이 비친다. 색은 흑백 두 가지뿐이다.

원본은 정사각형이 아니고 가장자리에 반투명 얼룩이 있어서, 원의 중심과 반지름을 불투명 화소에서 구한 뒤
깨끗한 원 마스크로 다시 잘라 1024x1024 기준 이미지를 만든다.

만드는 것:
  diparnux-<N>.png            앱 아이콘(원) 16~512
  diparnux-mark-white.png     글자 D 만 흰색, 배경 투명(어두운 바탕용) 512
  diparnux-mark-black.png     글자 D 만 검정, 배경 투명 512
  wallpaper.png               어두운 바탕용 희미한 마크 256(가운데 배치)
  calamares-logo.png          Calamares 사이드바 로고 128
  calamares-welcome.png       Calamares 환영 화면 이미지 640x300(어두운 바탕에 원 로고)
  grub-background-1280x720.png 참고용(GRUB 배경은 쓰지 않는다. 색만 쓴다)
설치기 브랜딩: icon.png 128, logo.png 128, welcome.png 640x300
"""

import os
import sys

from PIL import Image, ImageDraw

HERE = os.path.dirname(__file__)
SRC = os.path.join(HERE, "..", "assets", "brand", "source", "diparnux-logo.png")
OUT = os.path.join(HERE, "..", "assets", "brand", "generated")
INSTALLER = os.path.join(HERE, "..", "packages", "diparnux-installer", "src", "branding")

BG_DARK = (13, 13, 13, 255)  # #0D0D0D
MARK_DIM = (58, 58, 58)  # 배경 마크의 색(#3A3A3A)
MASTER = 1024
SS = 4  # 마스크 안티앨리어싱용 확대 배율


def load_master():
    """원 영역을 찾아 깨끗한 원 마스크로 잘라 1024x1024 기준 이미지를 돌려준다."""
    im = Image.open(SRC).convert("RGBA")
    alpha = im.split()[3]
    # 완전히 불투명한 화소의 범위로 원의 중심과 반지름을 정한다(가장자리 얼룩을 무시한다).
    solid = alpha.point(lambda v: 255 if v >= 250 else 0)
    x0, y0, x1, y1 = solid.getbbox()
    cx, cy = (x0 + x1) / 2, (y0 + y1) / 2
    r = ((x1 - x0) + (y1 - y0)) / 4
    side = int(round(2 * r))
    box = (int(round(cx - side / 2)), int(round(cy - side / 2)), int(round(cx - side / 2)) + side, int(round(cy - side / 2)) + side)
    square = im.crop(box).resize((MASTER, MASTER), Image.LANCZOS)
    # 원 마스크를 확대해 그린 뒤 줄여서 부드러운 가장자리를 만든다. 원 안쪽은 완전히 불투명하게 둔다.
    mask = Image.new("L", (MASTER * SS, MASTER * SS), 0)
    ImageDraw.Draw(mask).ellipse((0, 0, MASTER * SS - 1, MASTER * SS - 1), fill=255)
    mask = mask.resize((MASTER, MASTER), Image.LANCZOS)
    rgb = square.convert("RGB")
    out = rgb.convert("RGBA")
    out.putalpha(mask)
    return out


def glyph_alpha(master):
    """글자 D(검정)의 불투명도(0~255)를 돌려준다. 원 색(밝은 회색)은 0, 검정은 255, 원 밖은 0 이다."""
    lum = master.convert("RGB").convert("L")
    circle = master.split()[3]
    out = Image.new("L", master.size, 0)
    px_l, px_c, px_o = lum.load(), circle.load(), out.load()
    for y in range(master.size[1]):
        for x in range(master.size[0]):
            c = px_c[x, y]
            if c == 0:
                continue
            a = max(0, min(255, int((240 - px_l[x, y]) * 255 / 240)))
            px_o[x, y] = a * c // 255
    return out


def mark(master, rgb):
    """글자만 rgb 색으로, 나머지는 투명한 RGBA 이미지를 만든다. 글자 영역으로 잘라 여백을 둔다."""
    a = glyph_alpha(master)
    box = a.getbbox()
    layer = Image.new("RGBA", master.size, tuple(rgb) + (0,))
    layer.putalpha(a)
    layer = layer.crop(box)
    pad = int(max(layer.size) * 0.18)
    canvas = Image.new("RGBA", (layer.size[0] + 2 * pad, layer.size[1] + 2 * pad), tuple(rgb) + (0,))
    canvas.paste(layer, (pad, pad), layer)
    return canvas


def fit(img, size):
    return img.resize((size, size), Image.LANCZOS) if img.size[0] == img.size[1] else _fit_rect(img, size)


def _fit_rect(img, size):
    w, h = img.size
    scale = size / max(w, h)
    nw, nh = max(1, round(w * scale)), max(1, round(h * scale))
    canvas = Image.new("RGBA", (size, size), (0, 0, 0, 0))
    resized = img.resize((nw, nh), Image.LANCZOS)
    canvas.paste(resized, ((size - nw) // 2, (size - nh) // 2), resized)
    return canvas


def on_dark(img, size, canvas_size):
    canvas = Image.new("RGBA", canvas_size, BG_DARK)
    scaled = img.resize((size, size), Image.LANCZOS)
    canvas.paste(scaled, ((canvas_size[0] - size) // 2, (canvas_size[1] - size) // 2), scaled)
    return canvas


def main():
    os.makedirs(OUT, exist_ok=True)
    master = load_master()
    for n in (16, 24, 32, 48, 64, 96, 128, 256, 512):
        fit(master, n).save(os.path.join(OUT, f"diparnux-{n}.png"), optimize=True)
    white = mark(master, (255, 255, 255))
    black = mark(master, (0, 0, 0))
    fit(white, 512).save(os.path.join(OUT, "diparnux-mark-white.png"), optimize=True)
    fit(black, 512).save(os.path.join(OUT, "diparnux-mark-black.png"), optimize=True)

    dim = mark(master, MARK_DIM)
    fit(dim, 256).save(os.path.join(OUT, "wallpaper.png"), optimize=True)

    logo128 = fit(master, 128)
    logo128.save(os.path.join(OUT, "calamares-logo.png"), optimize=True)
    welcome = on_dark(master, 220, (640, 300))
    welcome.save(os.path.join(OUT, "calamares-welcome.png"), optimize=True)

    bg = on_dark(master, 260, (1280, 720))
    bg.convert("RGB").save(os.path.join(OUT, "grub-background-1280x720.png"), optimize=True)

    # 설치기(Calamares) 브랜딩 이미지는 패키지 안에 같은 내용을 둔다.
    os.makedirs(INSTALLER, exist_ok=True)
    logo128.save(os.path.join(INSTALLER, "icon.png"), optimize=True)
    logo128.save(os.path.join(INSTALLER, "logo.png"), optimize=True)
    welcome.save(os.path.join(INSTALLER, "welcome.png"), optimize=True)

    print("assets written to", os.path.abspath(OUT))
    for name in sorted(os.listdir(OUT)):
        path = os.path.join(OUT, name)
        if os.path.isfile(path):
            print(" ", name, os.path.getsize(path))


if __name__ == "__main__":
    sys.exit(main())
