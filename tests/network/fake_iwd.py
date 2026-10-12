#!/usr/bin/env python3
"""iwd 의 D-Bus 인터페이스 일부를 흉내 내는 시험용 서비스. 실제 busctl 이 내는 JSON 의 모양을 확인하는 데 쓴다.

실제 iwd 와 같은 이름(net.connman.iwd)과 객체 경로, 인터페이스, 속성을 노출한다.
  /                         ObjectManager
  /net/connman/iwd/0        Adapter
  /net/connman/iwd/0/3      Device + Station (wlan0)
  /net/connman/iwd/0/3/..   Network (Name, Type, Device, Connected, KnownNetwork)
  /net/connman/iwd/..       KnownNetwork
사용: fake_iwd.py ADDRESS   (예: unix:path=/tmp/bus)
"""

import sys

import gi

gi.require_version("Gio", "2.0")
from gi.repository import Gio, GLib  # noqa: E402

BUS_NAME = "net.connman.iwd"
DEVICE = "/net/connman/iwd/0/3"
NETWORKS = [
    # (ssid, type, strength*100 dBm, known, connected)
    ("HomeWiFi", "psk", -4200, True, True),
    ("CafeFree", "open", -6100, False, False),
    ("Office 5G", "psk", -7300, False, False),
]

IFACES = {
    "net.connman.iwd.Device": """<node><interface name="net.connman.iwd.Device">
        <property name="Name" type="s" access="read"/><property name="Address" type="s" access="read"/>
        <property name="Powered" type="b" access="readwrite"/><property name="Adapter" type="o" access="read"/>
        <property name="Mode" type="s" access="read"/></interface></node>""",
    "net.connman.iwd.Station": """<node><interface name="net.connman.iwd.Station">
        <method name="Scan"/><method name="Disconnect"/>
        <method name="GetOrderedNetworks"><arg name="networks" type="a(on)" direction="out"/></method>
        <property name="State" type="s" access="read"/><property name="Scanning" type="b" access="read"/>
        <property name="ConnectedNetwork" type="o" access="read"/></interface></node>""",
    "net.connman.iwd.Network": """<node><interface name="net.connman.iwd.Network">
        <method name="Connect"/>
        <property name="Name" type="s" access="read"/><property name="Connected" type="b" access="read"/>
        <property name="Device" type="o" access="read"/><property name="Type" type="s" access="read"/>
        <property name="KnownNetwork" type="o" access="read"/></interface></node>""",
    "net.connman.iwd.KnownNetwork": """<node><interface name="net.connman.iwd.KnownNetwork">
        <method name="Forget"/>
        <property name="Name" type="s" access="read"/><property name="Type" type="s" access="read"/>
        <property name="Hidden" type="b" access="read"/><property name="AutoConnect" type="b" access="readwrite"/>
        </interface></node>""",
    "net.connman.iwd.Adapter": """<node><interface name="net.connman.iwd.Adapter">
        <property name="Name" type="s" access="read"/><property name="Powered" type="b" access="readwrite"/>
        </interface></node>""",
}


def net_path(ssid):
    return DEVICE + "/" + "".join("%02x" % b for b in ssid.encode()) + "_psk"


def known_path(ssid, kind):
    return "/net/connman/iwd/" + "".join("%02x" % b for b in ssid.encode()) + "_" + kind


class Fake:
    def __init__(self):
        self.powered = True
        self.state = "connected"
        self.networks = [list(n) for n in NETWORKS]
        self.calls = []

    # 속성 값
    def props(self, path):
        out = {}
        if path == "/net/connman/iwd/0":
            out["net.connman.iwd.Adapter"] = {"Name": GLib.Variant("s", "phy0"), "Powered": GLib.Variant("b", True)}
        elif path == DEVICE:
            conn = next((n for n in self.networks if n[4]), None)
            out["net.connman.iwd.Device"] = {
                "Name": GLib.Variant("s", "wlan0"), "Address": GLib.Variant("s", "aa:bb:cc:dd:ee:ff"),
                "Powered": GLib.Variant("b", self.powered), "Adapter": GLib.Variant("o", "/net/connman/iwd/0"),
                "Mode": GLib.Variant("s", "station"),
            }
            station = {"State": GLib.Variant("s", self.state), "Scanning": GLib.Variant("b", False)}
            if conn:
                station["ConnectedNetwork"] = GLib.Variant("o", net_path(conn[0]))
            out["net.connman.iwd.Station"] = station
        else:
            for ssid, kind, _s, known, conn in self.networks:
                if path == net_path(ssid):
                    p = {"Name": GLib.Variant("s", ssid), "Connected": GLib.Variant("b", bool(conn)),
                         "Device": GLib.Variant("o", DEVICE), "Type": GLib.Variant("s", kind)}
                    if known:
                        p["KnownNetwork"] = GLib.Variant("o", known_path(ssid, kind))
                    out["net.connman.iwd.Network"] = p
                if known and path == known_path(ssid, kind):
                    out["net.connman.iwd.KnownNetwork"] = {
                        "Name": GLib.Variant("s", ssid), "Type": GLib.Variant("s", kind),
                        "Hidden": GLib.Variant("b", False), "AutoConnect": GLib.Variant("b", True)}
        return out

    def all_paths(self):
        paths = ["/net/connman/iwd/0", DEVICE]
        for ssid, kind, _s, known, _c in self.networks:
            paths.append(net_path(ssid))
            if known:
                paths.append(known_path(ssid, kind))
        return paths


fake = Fake()


def managed_objects():
    result = {}
    for path in fake.all_paths():
        result[path] = fake.props(path)
    return GLib.Variant("(a{oa{sa{sv}}})", (result,))


def handle_object_manager(conn, sender, path, iface, method, params, invocation):
    if method == "GetManagedObjects":
        invocation.return_value(managed_objects())
    else:
        invocation.return_dbus_error("org.freedesktop.DBus.Error.UnknownMethod", method)


def handle_object(conn, sender, path, iface, method, params, invocation):
    fake.calls.append((path, iface, method))
    if iface == "net.connman.iwd.Station" and method == "GetOrderedNetworks":
        items = [(net_path(n[0]), n[2]) for n in sorted(fake.networks, key=lambda n: -n[2])]
        invocation.return_value(GLib.Variant("(a(on))", (items,)))
    elif iface == "net.connman.iwd.Station" and method == "Scan":
        invocation.return_value(None)
    elif iface == "net.connman.iwd.Station" and method == "Disconnect":
        for n in fake.networks:
            n[4] = False
        fake.state = "disconnected"
        invocation.return_value(None)
    elif iface == "net.connman.iwd.Network" and method == "Connect":
        target = next((n for n in fake.networks if net_path(n[0]) == path), None)
        if target is None:
            invocation.return_dbus_error("net.connman.iwd.Failed", "no such network")
            return
        if target[1] == "psk" and not target[3]:
            invocation.return_dbus_error("net.connman.iwd.NoAgent", "No Agent registered")
            return
        for n in fake.networks:
            n[4] = False
        target[4] = True
        fake.state = "connected"
        invocation.return_value(None)
    elif iface == "net.connman.iwd.KnownNetwork" and method == "Forget":
        for n in fake.networks:
            if known_path(n[0], n[1]) == path:
                n[3] = False
                n[4] = False
        invocation.return_value(None)
    else:
        invocation.return_dbus_error("org.freedesktop.DBus.Error.UnknownMethod", method)


def get_property(conn, sender, path, iface, prop):
    return fake.props(path).get(iface, {}).get(prop)


def set_property(conn, sender, path, iface, prop, value):
    if path == DEVICE and iface == "net.connman.iwd.Device" and prop == "Powered":
        fake.powered = value.get_boolean()
        return True
    return False


def on_bus(conn, name):
    om = Gio.DBusNodeInfo.new_for_xml("""<node><interface name="org.freedesktop.DBus.ObjectManager">
        <method name="GetManagedObjects"><arg name="objects" type="a{oa{sa{sv}}}" direction="out"/></method>
        </interface></node>""")
    conn.register_object("/", om.interfaces[0], handle_object_manager, None, None)
    paths = {}
    for p in ["/net/connman/iwd/0", DEVICE]:
        paths[p] = []
    for ssid, kind, *_ in NETWORKS:
        paths[net_path(ssid)] = ["net.connman.iwd.Network"]
        paths[known_path(ssid, kind)] = ["net.connman.iwd.KnownNetwork"]
    paths["/net/connman/iwd/0"] = ["net.connman.iwd.Adapter"]
    paths[DEVICE] = ["net.connman.iwd.Device", "net.connman.iwd.Station"]
    for path, ifaces in paths.items():
        for name_ in ifaces:
            info = Gio.DBusNodeInfo.new_for_xml(IFACES[name_])
            conn.register_object(path, info.interfaces[0], handle_object, get_property, set_property)


def main():
    address = sys.argv[1]
    loop = GLib.MainLoop()
    conn = Gio.DBusConnection.new_for_address_sync(
        address, Gio.DBusConnectionFlags.AUTHENTICATION_CLIENT | Gio.DBusConnectionFlags.MESSAGE_BUS_CONNECTION, None, None)
    on_bus(conn, BUS_NAME)
    Gio.bus_own_name_on_connection(conn, BUS_NAME, Gio.BusNameOwnerFlags.NONE, None, None)
    print("fake iwd ready", flush=True)
    loop.run()


if __name__ == "__main__":
    main()
