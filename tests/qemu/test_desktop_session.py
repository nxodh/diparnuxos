#!/usr/bin/env python3
"""desktop-session.py 의 화면 판정 보조 함수를 확인한다. QEMU 는 필요 없다."""

import importlib.util
import os
from pathlib import Path
import struct
import sys
import tempfile
import unittest
import zlib

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("desktop_session", HERE / "desktop-session.py")
ds = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ds)


def ppm(width, height, pixel_fn):
    body = bytearray()
    for y in range(height):
        for x in range(width):
            body += bytes(pixel_fn(x, y))
    return b"P6\n%d %d\n255\n" % (width, height) + bytes(body)


class ImageTests(unittest.TestCase):
    def write(self, data):
        fd, path = tempfile.mkstemp(suffix=".ppm")
        os.close(fd)
        Path(path).write_bytes(data)
        self.addCleanup(os.unlink, path)
        return path

    def test_black_screen_is_blank(self):
        w, h, px = ds.read_ppm(self.write(ppm(64, 48, lambda x, y: (0, 0, 0))))
        info = ds.describe_image(w, h, px)
        self.assertEqual((info["width"], info["height"], info["colors"]), (64, 48, 1))
        self.assertEqual(info["lit_fraction"], 0.0)

    def test_text_like_screen_has_colors_and_lit_pixels(self):
        def pixel(x, y):
            return (230, 230, 230) if (x // 4 + y // 4) % 5 == 0 else (10, 10, 40)

        w, h, px = ds.read_ppm(self.write(ppm(160, 100, pixel)))
        info = ds.describe_image(w, h, px)
        self.assertGreaterEqual(info["colors"], 2)
        self.assertGreater(info["lit_fraction"], 0.05)

    def test_rejects_non_ppm_and_truncated(self):
        with self.assertRaises(ds.Failure):
            ds.read_ppm(self.write(b"GIF89a"))
        with self.assertRaises(ds.Failure):
            ds.read_ppm(self.write(b"P6\n10 10\n255\n" + b"\x00" * 10))

    def test_png_is_valid(self):
        w, h = 40, 30
        px = b"".join(bytes((x * 5 % 256, y * 7 % 256, 90)) for y in range(h) for x in range(w))
        out = Path(tempfile.mkdtemp()) / "x.png"
        ds.write_png(out, w, h, px)
        data = out.read_bytes()
        self.assertTrue(data.startswith(b"\x89PNG\r\n\x1a\n"))
        # IHDR 의 폭과 높이는 반으로 줄어든다.
        width, height = struct.unpack(">II", data[16:24])
        self.assertEqual((width, height), (20, 15))
        idat = data[data.index(b"IDAT") + 4:data.index(b"IEND") - 8]
        self.assertEqual(len(zlib.decompress(idat)), 15 * (1 + 20 * 3))


class JsonTests(unittest.TestCase):
    def test_skips_console_noise_before_the_object(self):
        text = "[   18.58] systemd-gpt-auto-generator[1234]: EFI loader partition unknown\n{\n  \"mode\": \"desktop\"\n}"
        self.assertEqual(ds.json_object(text, "x"), {"mode": "desktop"})

    def test_plain_object(self):
        self.assertEqual(ds.json_object('{\n  "mode": "server"\n}', "x"), {"mode": "server"})

    def test_missing_object_is_a_failure(self):
        with self.assertRaises(ds.Failure) as cm:
            ds.json_object("no json here", "x")
        self.assertEqual(cm.exception.kind, "invalid_json")


class MonitorTests(unittest.TestCase):
    def test_type_text_rejects_unsupported_characters(self):
        m = ds.Monitor("/nonexistent")
        with self.assertRaises(ds.Failure):
            m.type_text("A")

    def test_missing_socket_times_out_as_monitor_failure(self):
        m = ds.Monitor("/nonexistent/monitor.sock", timeout=0.2)
        with self.assertRaises(ds.Failure) as cm:
            m.connect(wait=0.5)
        self.assertEqual(cm.exception.kind, "monitor")


if __name__ == "__main__":
    unittest.main()
