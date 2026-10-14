import os
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)

try:
    import gi  # noqa: F401
    from opereta_cc import system, theme  # noqa: E402
except ImportError as exc:  # GTK 가 없는 빌드 환경(패키지 빌드)에서는 건너뛴다
    raise unittest.SkipTest("GTK bindings are not available: %s" % exc)


class TerminalColorTests(unittest.TestCase):
    def _rc(self, home, text):
        d = os.path.join(home, ".config", "xfce4", "terminal")
        os.makedirs(d)
        path = os.path.join(d, "terminalrc")
        with open(path, "w", encoding="utf-8") as f:
            f.write(text)
        return path

    def test_only_color_lines_change(self):
        with tempfile.TemporaryDirectory() as home:
            path = self._rc(home, "[Configuration]\nFontName=Monospace 11\nColorBackground=#fbfbfb\nColorForeground=#1d1d1f\n")
            system._set_terminal_colors(True, home)
            body = open(path, encoding="utf-8").read()
            self.assertIn("FontName=Monospace 11", body)
            self.assertIn("ColorBackground=#1c1c1e", body)
            self.assertIn("ColorForeground=#f2f2f4", body)
            self.assertIn("ColorPalette=", body)  # 빠진 키는 더해진다
            system._set_terminal_colors(False, home)
            self.assertIn("ColorBackground=#fbfbfb", open(path, encoding="utf-8").read())

    def test_missing_file_is_ignored(self):
        with tempfile.TemporaryDirectory() as home:
            system._set_terminal_colors(True, home)
            self.assertFalse(os.path.exists(os.path.join(home, ".config")))


class ThemeCssTests(unittest.TestCase):
    def test_palettes_are_gray_and_cover_both_schemes(self):
        for dark in (False, True):
            css = theme.build_css(dark)
            self.assertIn("@define-color window_bg_color", css)
            self.assertIn("border-radius", css)
        # 회색조만 쓴다: 팔레트 값은 R=G=B 에 가까워야 한다(macOS 처럼 아주 약한 푸른 기운은 허용).
        for pal in theme._PALETTES.values():
            for name, value in pal.items():
                r, g, b = (int(value[i:i + 2], 16) for i in (1, 3, 5))
                self.assertLessEqual(max(r, g, b) - min(r, g, b), 8, "%s=%s is not near-gray" % (name, value))


if __name__ == "__main__":
    unittest.main()
