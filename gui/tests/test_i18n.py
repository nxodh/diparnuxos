import gettext
import os
import re
import shutil
import subprocess
import sys
import tempfile
import unittest

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)

from diparnux_cc import i18n  # noqa: E402

PO = os.path.join(HERE, "po", "ko.po")
POT = os.path.join(HERE, "po", "diparnux-control-center.pot")


def read(path):
    with open(path, encoding="utf-8") as f:
        return f.read()


def msgids(path):
    text = read(path)
    out = []
    for block in text.split("\n\n"):
        m = re.search(r'^msgid ((?:".*"\n?)+)', block, re.M)
        if m:
            ident = "".join(re.findall(r'^"(.*)"$', m.group(1), re.M))
            if ident:
                out.append(ident)
    return out


class CatalogTests(unittest.TestCase):
    def test_every_pot_entry_is_translated_in_korean(self):
        pot, ko = set(msgids(POT)), set(msgids(PO))
        self.assertFalse(pot - ko, "untranslated: %s" % sorted(pot - ko)[:5])
        self.assertFalse(ko - pot, "stale: %s" % sorted(ko - pot)[:5])

    def test_pot_matches_source_strings(self):
        # 소스에 새 문자열이 생기면 .pot 을 다시 만들어야 한다(xgettext, 아래 README 참고).
        wanted = set()
        pattern = re.compile(r'\b_\(\s*"((?:[^"\\]|\\.)*)"', re.S)
        for name in os.listdir(os.path.join(HERE, "diparnux_cc")):
            if name.endswith(".py") and name != "i18n.py":
                src = read(os.path.join(HERE, "diparnux_cc", name))
                wanted.update(m.group(1) for m in pattern.finditer(src))
        have = {i.replace('\\"', '"') for i in msgids(POT)}
        missing = sorted(w for w in wanted if w not in have)
        self.assertFalse(missing, "strings missing from the .pot: %s" % missing[:5])

    @unittest.skipUnless(shutil.which("msgfmt"), "msgfmt is required")
    def test_korean_catalog_compiles_and_loads(self):
        with tempfile.TemporaryDirectory() as tmp:
            target = os.path.join(tmp, "ko", "LC_MESSAGES")
            os.makedirs(target)
            subprocess.run(
                ["msgfmt", "--check", "-o", os.path.join(target, i18n.DOMAIN + ".mo"), PO],
                check=True,
            )
            i18n.setup(localedir=tmp, languages=["ko"])
            try:
                self.assertEqual(i18n._("Dashboard"), "대시보드")
                self.assertEqual(
                    i18n._("%(used)s used of %(total)s") % {"used": "1 GiB", "total": "4 GiB"},
                    "4 GiB 중 1 GiB 사용",
                )
            finally:
                i18n.setup(localedir="/nonexistent")

    def test_missing_catalog_falls_back_to_english(self):
        i18n.setup(localedir="/nonexistent", languages=["ko"])
        self.assertEqual(i18n._("Dashboard"), "Dashboard")


if __name__ == "__main__":
    unittest.main()
