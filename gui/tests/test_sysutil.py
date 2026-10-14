import os
import sys
import unittest

HERE = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))
sys.path.insert(0, HERE)

from opereta_cc import sysutil  # noqa: E402

XRANDR = """Screen 0: minimum 320 x 200, current 1920 x 1080, maximum 16384 x 16384
eDP-1 connected primary 1920x1080+0+0 (normal left inverted right x axis y axis) 344mm x 193mm
   1920x1080    144.00*+  60.00 +
   1680x1050     59.88
   1280x720      60.00
HDMI-1 disconnected (normal left inverted right x axis y axis)
DP-1 connected 2560x1440+1920+0 (normal left inverted right x axis y axis) 597mm x 336mm
   2560x1440     59.95*+
   1920x1080     60.00
"""

PACTL = '''[{"name":"alsa_output.pci.analog","description":"Built-in Audio","mute":false,
 "volume":{"front-left":{"value":39321,"value_percent":"60%"},"front-right":{"value":39321,"value_percent":"60%"}}},
 {"name":"alsa_output.pci.analog.monitor","description":"Monitor of Built-in Audio","mute":false,"volume":{}},
 {"name":"bluez_sink.AA","description":"Headset","mute":true,"volume":{"mono":{"value_percent":"35%"}}}]'''

LSBLK = '''{"blockdevices":[{"name":"nvme0n1","size":1024209543168,"type":"disk","fstype":null,"label":null,"mountpoints":[null],
 "children":[{"name":"nvme0n1p1","size":104857600,"type":"part","fstype":"vfat","label":"EFI","mountpoints":["/boot/efi"]},
 {"name":"nvme0n1p3","size":498216206336,"type":"part","fstype":"BitLocker","label":null,"mountpoints":[null]},
 {"name":"nvme0n1p6","size":515396075520,"type":"part","fstype":"ext4","label":null,"mountpoints":["/"]}]},
 {"name":"zram0","size":4294967296,"type":"disk","fstype":"swap","label":null,"mountpoints":["[SWAP]"]}]}'''


class ParseTests(unittest.TestCase):
    def test_xrandr_keeps_connected_outputs_and_modes(self):
        out = sysutil.parse_xrandr(XRANDR)
        self.assertEqual([o["name"] for o in out], ["eDP-1", "DP-1"])
        self.assertTrue(out[0]["primary"])
        self.assertEqual(out[0]["current"], (1920, 1080))
        self.assertEqual(out[0]["modes"], [(1920, 1080), (1680, 1050), (1280, 720)])
        self.assertEqual(out[1]["current"], (2560, 1440))

    def test_pactl_skips_monitors_and_reads_percent(self):
        devs = sysutil.parse_pactl_devices(PACTL)
        self.assertEqual([d["name"] for d in devs], ["alsa_output.pci.analog", "bluez_sink.AA"])
        self.assertEqual(devs[0]["volume"], 60)
        self.assertTrue(devs[1]["mute"])
        self.assertEqual(sysutil.parse_pactl_devices("not json"), [])

    def test_bluetooth_output(self):
        self.assertFalse(sysutil.parse_bt_show("No default controller available\n")["available"])
        show = sysutil.parse_bt_show("Controller AA:BB:CC:DD:EE:FF (public)\n\tAlias: laptop\n\tPowered: yes\n\tDiscovering: no\n")
        self.assertTrue(show["available"] and show["powered"] and not show["discovering"])
        devs = sysutil.parse_bt_devices("Device 11:22:33:44:55:66 Keyboard K1\nDevice zz Bad\nNoise line\n")
        self.assertEqual(devs, [{"mac": "11:22:33:44:55:66", "name": "Keyboard K1"}])
        info = sysutil.parse_bt_info("Device 11:22:33:44:55:66\n\tPaired: yes\n\tConnected: no\n\tIcon: input-keyboard\n")
        self.assertTrue(info["paired"] and not info["connected"])
        self.assertTrue(sysutil.valid_mac("11:22:33:44:55:66"))
        self.assertFalse(sysutil.valid_mac("11:22:33:44:55"))
        self.assertFalse(sysutil.valid_mac("11:22:33:44:55:6;"))

    def test_bluetooth_command_rejects_a_bad_address(self):
        self.assertFalse(sysutil.bluetooth_command("connect", "11:22:33:44:55:zz"))

    def test_lsblk_flattens_disks_and_partitions(self):
        devs = sysutil.parse_lsblk(LSBLK)
        names = [d["name"] for d in devs]
        self.assertEqual(names, ["nvme0n1", "nvme0n1p1", "nvme0n1p3", "nvme0n1p6", "zram0"])
        efi = devs[1]
        self.assertEqual((efi["fstype"], efi["label"], efi["mounts"]), ("vfat", "EFI", ["/boot/efi"]))
        self.assertEqual(devs[2]["fstype"], "BitLocker")
        self.assertEqual(devs[4]["mounts"], [])  # 스왑은 마운트로 치지 않는다
        self.assertEqual(sysutil.parse_lsblk("{"), [])

    def test_missing_tools_do_not_raise(self):
        rc, out = sysutil.run(["opereta-no-such-tool-xyz"])
        self.assertEqual((rc, out), (127, ""))
        self.assertIsNone(sysutil.xfconf_get("nochannel", "/x") if not sysutil.have("xfconf-query") else None)


if __name__ == "__main__":
    unittest.main()
