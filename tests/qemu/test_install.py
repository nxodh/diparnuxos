#!/usr/bin/env python3
"""install.py 의 출력 해석 보조 함수를 확인한다. QEMU 는 필요 없다."""

import importlib.util
from pathlib import Path
import unittest

HERE = Path(__file__).resolve().parent
spec = importlib.util.spec_from_file_location("install_e2e", HERE / "install.py")
mod = importlib.util.module_from_spec(spec)
spec.loader.exec_module(mod)


class JsonTests(unittest.TestCase):
    def test_last_json_line_is_the_result(self):
        text = '{"time":"t","step":"disk","message":"x"}\nnoise\n{"ok":true,"disk":"/dev/vda"}\n'
        self.assertEqual(mod.json_object(text, "install"), {"ok": True, "disk": "/dev/vda"})

    def test_log_lines_between_events_are_ignored(self):
        text = "[   12.3] systemd: something\n{\"ok\":false,\"error\":{\"code\":\"disk_busy\"}}\n[ 13.0] kernel: more"
        self.assertEqual(mod.json_object(text, "x")["error"]["code"], "disk_busy")

    def test_missing_json_is_a_failure(self):
        with self.assertRaises(mod.Failure):
            mod.json_object("nothing here", "x")

    def test_pretty_printed_object(self):
        text = 'boot noise\n{\n  "available": true,\n  "live": false\n}\n'
        self.assertEqual(mod.multiline_json(text, "x"), {"available": True, "live": False})


class ArgvTests(unittest.TestCase):
    def test_installed_boot_has_no_cdrom(self):
        argv = mod.argv_for("qemu", "tcg", "/c", "/v", "/d")
        self.assertNotIn("-cdrom", argv)
        self.assertIn("-no-reboot", argv)

    def test_install_boot_boots_the_cdrom_first(self):
        argv = mod.argv_for("qemu", "kvm", "/c", "/v", "/d", iso="/x.iso")
        self.assertIn("-cdrom", argv)
        self.assertEqual(argv[argv.index("-boot") + 1], "order=d")
        self.assertEqual(argv[argv.index("-cpu") + 1], "host")


if __name__ == "__main__":
    unittest.main()
