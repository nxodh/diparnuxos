"""데스크톱 설정 페이지: 모양, 디스플레이, 소리, 블루투스, 키보드, 저장 공간.

각 페이지는 xfconf-query, xrandr, pactl, bluetoothctl, lsblk 같은 기존 도구를 부르는 얇은 층이다. 도구가 없거나
Xfce 세션이 아니면 그 사실을 보여 주고 아무것도 바꾸지 않는다. 느린 호출은 스레드에서 한다.
"""

import os

import gi

gi.require_version("Gtk", "4.0")
gi.require_version("Adw", "1")
from gi.repository import Adw, Gio, GLib, Gtk  # noqa: E402

from . import logic, sysutil  # noqa: E402
from .i18n import _  # noqa: E402
from .system import _SCHEMES, apply_color_scheme, apply_os_scheme, load_settings, save_settings  # noqa: E402
from .widgets import action_row, clear_group, confirm, run_async, text_button  # noqa: E402

# 글자 크기(Gtk/FontName)와 화면 배율(Xft/DPI) 선택지. 값은 (이름, 설정값).
_TEXT_SIZES = (("Small", 9), ("Normal", 10), ("Large", 12), ("Larger", 14))
_SCALES = (("100%", 96, 1), ("125%", 120, 1), ("150%", 144, 1), ("200%", 192, 2))
_FONT = "Pretendard"


class AppearancePage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Appearance"), icon_name="preferences-desktop-theme-symbolic")
        self.ctx = ctx
        look = Adw.PreferencesGroup(title=_("Look"))
        self.scheme_combo = Adw.ComboRow(title=_("Color scheme"))
        self.scheme_combo.set_model(Gtk.StringList.new([_("Follow the system"), _("Light"), _("Dark")]))
        # 처음 값은 시스템을 따른다. 데스크톱의 기본은 밝은 테마다. 사용자가 바꾸면 그 값을 따른다.
        saved = load_settings().get("color_scheme", "default")
        self.scheme_combo.set_selected(_SCHEMES.index(saved) if saved in _SCHEMES else 0)
        apply_color_scheme(saved)
        self.scheme_combo.connect("notify::selected", self._on_scheme)
        look.add(self.scheme_combo)

        self.size_combo = Adw.ComboRow(title=_("Text size"))
        self.size_combo.set_model(Gtk.StringList.new([_("Small"), _("Normal"), _("Large"), _("Larger")]))
        self.size_combo.set_selected(1)
        self._size_ready = False
        self.size_combo.connect("notify::selected", self._on_size)
        look.add(self.size_combo)

        self.effects_row = Adw.SwitchRow(title=_("Window effects"))
        self.effects_row.set_subtitle(_("Soft shadows, rounded corners and transparency. Turn them off on a slow computer."))
        self.effects_row.set_active(self._effects_enabled())
        self.effects_row.connect("notify::active", self._on_effects)
        look.add(self.effects_row)
        self.add(look)

        wall = Adw.PreferencesGroup(title=_("Desktop picture"))
        self.wall_row = action_row(_("Picture"), _("Choose an image for the desktop."))
        self.wall_row.add_suffix(text_button(_("Choose…"), self.choose_wallpaper))
        self.wall_row.add_suffix(text_button(_("Default"), self.reset_wallpaper))
        wall.add(self.wall_row)
        self.add(wall)
        self.connect("map", lambda *_a: self._load_size())

    # --- 색 구성표 ---
    def _on_scheme(self, combo, _pspec):
        name = _SCHEMES[combo.get_selected()]
        apply_color_scheme(name)
        run_async(lambda: apply_os_scheme(name))
        data = load_settings()
        data["color_scheme"] = name
        save_settings(data)

    # --- 글자 크기 ---
    def _load_size(self):
        cur = sysutil.xfconf_get("xsettings", "/Gtk/FontName") or ""
        parts = cur.rsplit(" ", 1)
        try:
            pt = int(parts[1])
        except (IndexError, ValueError):
            pt = 10
        idx = min(range(len(_TEXT_SIZES)), key=lambda i: abs(_TEXT_SIZES[i][1] - pt))
        self._size_ready = False
        self.size_combo.set_selected(idx)
        self._size_ready = True

    def _on_size(self, combo, _pspec):
        if not self._size_ready:
            return
        pt = _TEXT_SIZES[combo.get_selected()][1]
        run_async(lambda: sysutil.xfconf_set("xsettings", "/Gtk/FontName", "string", "%s %d" % (_FONT, pt)))

    # --- 창 효과 ---
    @staticmethod
    def _flag_path():
        return os.path.join(GLib.get_user_config_dir(), "opereta", "effects-off")

    def _effects_enabled(self):
        return not os.path.exists(self._flag_path())

    def _on_effects(self, row, _pspec):
        path = self._flag_path()
        try:
            if row.get_active():
                if os.path.exists(path):
                    os.remove(path)
            else:
                os.makedirs(os.path.dirname(path), exist_ok=True)
                open(path, "w", encoding="utf-8").close()
        except OSError:
            return
        run_async(lambda: sysutil.run(["opereta-compositor", "restart"]))

    # --- 배경 화면 ---
    def choose_wallpaper(self):
        dialog = Gtk.FileDialog(title=_("Choose a picture"))
        flt = Gtk.FileFilter()
        flt.set_name(_("Images"))
        flt.add_mime_type("image/png")
        flt.add_mime_type("image/jpeg")
        filters = Gio.ListStore.new(Gtk.FileFilter)
        filters.append(flt)
        dialog.set_filters(filters)

        def done(dlg, result):
            try:
                f = dlg.open_finish(result)
            except GLib.Error:
                return
            path = f.get_path()
            run_async(lambda: sysutil.set_wallpaper(path),
                      lambda n: self.ctx.toast(_("The desktop picture was changed") if n else _("The picture could not be applied")))

        dialog.open(self.ctx.window, None, done)

    def reset_wallpaper(self):
        scheme = load_settings().get("color_scheme", "default")
        path = "/usr/share/opereta/wallpaper.png" if scheme == "dark" else "/usr/share/opereta/wallpaper-light.png"
        run_async(lambda: sysutil.set_wallpaper(path),
                  lambda n: self.ctx.toast(_("The desktop picture was changed") if n else _("The picture could not be applied")))


class DisplayPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Displays"), icon_name="video-display-symbolic")
        self.ctx = ctx
        self.out_group = Adw.PreferencesGroup(title=_("Screens"))
        self.add(self.out_group)
        self._rows = []
        self._outputs = []
        self._updating = False
        scale = Adw.PreferencesGroup(title=_("Size of the interface"))
        scale.set_description(_("A larger size makes text and controls bigger. Open programs may need to be restarted."))
        self.scale_combo = Adw.ComboRow(title=_("Interface scale"))
        self.scale_combo.set_model(Gtk.StringList.new([s[0] for s in _SCALES]))
        self.scale_combo.connect("notify::selected", self._on_scale)
        scale.add(self.scale_combo)
        self.add(scale)
        self.connect("map", lambda *_a: self.refresh())

    def refresh(self):
        run_async(sysutil.display_outputs, self._got, lambda _e: None)
        dpi = sysutil.xfconf_get("xsettings", "/Xft/DPI")
        try:
            dpi = int(dpi)
        except (TypeError, ValueError):
            dpi = 96
        idx = min(range(len(_SCALES)), key=lambda i: abs(_SCALES[i][1] - dpi))
        self._updating = True
        self.scale_combo.set_selected(idx)
        self._updating = False

    def _got(self, outputs):
        self._outputs = outputs
        clear_group(self.out_group, self._rows)
        if not outputs:
            row = action_row(_("Not available"), _("This needs the desktop session and the xrandr tool."))
            self.out_group.add(row)
            self._rows.append(row)
            return
        for o in outputs:
            modes = sorted(o["modes"], key=lambda m: (-m[0], -m[1]))
            combo = Adw.ComboRow(title=o["name"], subtitle=GLib.markup_escape_text(_("Resolution")))
            combo.set_model(Gtk.StringList.new(["%d × %d" % m for m in modes]))
            if o["current"] in modes:
                combo.set_selected(modes.index(o["current"]))
            combo._modes = modes
            combo._output = o["name"]
            combo._current = o["current"]
            combo.connect("notify::selected", self._on_mode)
            self.out_group.add(combo)
            self._rows.append(combo)

    def _on_mode(self, combo, _pspec):
        if self._updating or combo.get_selected() >= len(combo._modes):
            return
        size = combo._modes[combo.get_selected()]
        if size == combo._current:
            return
        previous = combo._current

        def applied(ok):
            if not ok:
                self.ctx.toast(_("The resolution could not be changed"))
                return
            # 화면이 안 보이는 설정으로 갇히지 않도록 15초 안에 확인하지 않으면 되돌린다.
            state = {"done": False}

            def revert():
                if not state["done"] and previous:
                    run_async(lambda: sysutil.set_resolution(combo._output, previous), lambda _r: self.refresh())
                return GLib.SOURCE_REMOVE

            def keep():
                state["done"] = True
                combo._current = size

            def cancel():
                state["done"] = True
                if previous:
                    run_async(lambda: sysutil.set_resolution(combo._output, previous), lambda _r: self.refresh())

            timer = GLib.timeout_add_seconds(15, revert)
            confirm(self.ctx.window, _("Keep this resolution?"),
                    _("The previous resolution comes back in 15 seconds if you do nothing."), _("Keep"),
                    lambda: (GLib.source_remove(timer) if not state["done"] else None, keep()), on_cancel=lambda: (GLib.source_remove(timer), cancel()))

        run_async(lambda: sysutil.set_resolution(combo._output, size), applied)

    def _on_scale(self, combo, _pspec):
        if self._updating:
            return
        _name, dpi, factor = _SCALES[combo.get_selected()]

        def work():
            sysutil.xfconf_set("xsettings", "/Xft/DPI", "int", dpi)
            sysutil.xfconf_set("xsettings", "/Gdk/WindowScalingFactor", "int", factor)

        run_async(work)


class SoundPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Sound"), icon_name="audio-volume-high-symbolic")
        self.ctx = ctx
        self._updating = False
        self._devs = {"sinks": [], "sources": []}
        self._widgets = {}
        for kind, title in (("sinks", _("Output")), ("sources", _("Input"))):
            group = Adw.PreferencesGroup(title=title)
            combo = Adw.ComboRow(title=_("Device"))
            combo.set_model(Gtk.StringList.new([]))
            combo.connect("notify::selected", lambda c, _p, k=kind: self._on_device(k, c))
            scale = Gtk.Scale.new_with_range(Gtk.Orientation.HORIZONTAL, 0, 150, 1)
            scale.set_hexpand(True)
            scale.set_draw_value(True)
            scale.connect("value-changed", lambda s, k=kind: self._on_volume(k, s))
            vol_row = Adw.ActionRow(title=_("Volume"))
            vol_row.add_suffix(scale)
            scale.set_size_request(260, -1)
            mute = Adw.SwitchRow(title=_("Mute"))
            mute.connect("notify::active", lambda r, _p, k=kind: self._on_mute(k, r))
            for w in (combo, vol_row, mute):
                group.add(w)
            self.add(group)
            self._widgets[kind] = (group, combo, scale, mute)
        self.connect("map", lambda *_a: self.refresh())

    def refresh(self):
        def work():
            return {k: sysutil.audio_devices(k) for k in ("sinks", "sources")}

        run_async(work, self._got, lambda _e: None)

    def _got(self, data):
        self._updating = True
        for kind, (devices, default) in data.items():
            group, combo, scale, mute = self._widgets[kind]
            self._devs[kind] = devices
            group.set_sensitive(bool(devices))
            combo.set_model(Gtk.StringList.new([d["description"] for d in devices] or [_("No device")]))
            names = [d["name"] for d in devices]
            idx = names.index(default) if default in names else 0
            combo.set_selected(idx)
            if devices:
                scale.set_value(devices[idx]["volume"])
                mute.set_active(devices[idx]["mute"])
        self._updating = False

    def _current(self, kind):
        group, combo, _s, _m = self._widgets[kind]
        i = combo.get_selected()
        devs = self._devs[kind]
        return devs[i] if 0 <= i < len(devs) else None

    def _on_device(self, kind, combo):
        if self._updating:
            return
        d = self._current(kind)
        if d is None:
            return
        _g, _c, scale, mute = self._widgets[kind]
        run_async(lambda: sysutil.set_default_device(kind, d["name"]))
        self._updating = True
        scale.set_value(d["volume"])
        mute.set_active(d["mute"])
        self._updating = False

    def _on_volume(self, kind, scale):
        d = self._current(kind)
        if self._updating or d is None:
            return
        v = int(scale.get_value())
        d["volume"] = v
        run_async(lambda: sysutil.set_volume(kind, d["name"], v))

    def _on_mute(self, kind, row):
        d = self._current(kind)
        if self._updating or d is None:
            return
        d["mute"] = row.get_active()
        run_async(lambda: sysutil.set_mute(kind, d["name"], row.get_active()))


class BluetoothPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Bluetooth"), icon_name="bluetooth-symbolic")
        self.ctx = ctx
        self._updating = False
        self._rows = []
        self.top = Adw.PreferencesGroup(title=_("Bluetooth"))
        self.power_row = Adw.SwitchRow(title=_("Bluetooth"))
        self.power_row.connect("notify::active", self._on_power)
        self.top.add(self.power_row)
        self.note = action_row("", "")
        self.note.set_visible(False)
        self.top.add(self.note)
        self.scan_row = action_row(_("Search for devices"), _("Put the device in pairing mode, then search."))
        self.scan_btn = text_button(_("Search"), self.scan)
        self.scan_row.add_suffix(self.scan_btn)
        self.top.add(self.scan_row)
        self.add(self.top)
        self.dev_group = Adw.PreferencesGroup(title=_("Devices"))
        self.add(self.dev_group)
        self.connect("map", lambda *_a: self.refresh())

    def refresh(self):
        run_async(sysutil.bluetooth_state, self._got, lambda _e: None)

    def _got(self, state):
        adapter, devices = state
        self._updating = True
        self.power_row.set_sensitive(adapter["available"])
        self.power_row.set_active(adapter["powered"])
        self._updating = False
        self.note.set_visible(not adapter["available"])
        if not adapter["available"]:
            self.note.set_title(GLib.markup_escape_text(_("No Bluetooth adapter")))
            self.note.set_subtitle(GLib.markup_escape_text(_("This computer has no Bluetooth adapter, or the Bluetooth service is not running.")))
        self.scan_row.set_sensitive(adapter["available"] and adapter["powered"])
        clear_group(self.dev_group, self._rows)
        for d in devices:
            state_text = _("Connected") if d["connected"] else (_("Paired") if d["paired"] else _("Not paired"))
            row = action_row(d["name"], "%s · %s" % (state_text, d["mac"]))
            if d["connected"]:
                row.add_suffix(text_button(_("Disconnect"), lambda m=d["mac"]: self._cmd("disconnect", m)))
            elif d["paired"]:
                row.add_suffix(text_button(_("Connect"), lambda m=d["mac"]: self._cmd("connect", m), "suggested-action"))
            else:
                row.add_suffix(text_button(_("Pair"), lambda m=d["mac"]: self._pair(m), "suggested-action"))
            if d["paired"]:
                row.add_suffix(text_button(_("Forget"), lambda m=d["mac"]: self._cmd("remove", m)))
            self.dev_group.add(row)
            self._rows.append(row)

    def _on_power(self, row, _pspec):
        if self._updating:
            return
        on = row.get_active()
        run_async(lambda: sysutil.bluetooth_command("power", "on" if on else "off"), lambda _r: self.refresh())

    def scan(self):
        self.scan_btn.set_sensitive(False)
        self.ctx.toast(_("Searching for devices…"))

        def done(_r):
            self.scan_btn.set_sensitive(True)
            self.refresh()

        run_async(lambda: sysutil.bluetooth_scan(8), done, lambda e: (self.scan_btn.set_sensitive(True), self.ctx.toast_error(e)))

    def _cmd(self, verb, mac):
        def done(ok):
            if not ok:
                self.ctx.toast(_("The command failed"))
            self.refresh()

        run_async(lambda: sysutil.bluetooth_command(verb, mac), done)

    def _pair(self, mac):
        def work():
            if not sysutil.bluetooth_command("pair", mac, timeout=45):
                return False
            sysutil.bluetooth_command("trust", mac)
            return sysutil.bluetooth_command("connect", mac)

        def done(ok):
            self.ctx.toast(_("Paired") if ok else _("Pairing failed. Check that the device is in pairing mode."))
            self.refresh()

        run_async(work, done)


class KeyboardPage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Keyboard"), icon_name="input-keyboard-symbolic")
        self.ctx = ctx
        self._updating = False
        info = Adw.PreferencesGroup(title=_("Typing languages"))
        self.layout_row = action_row(_("Keyboard layout"), "-")
        info.add(self.layout_row)
        info.add(action_row(_("Switch between English and Korean"),
                            _("Press the Hangul key, the right Alt key, Shift+Space or Ctrl+Space.")))
        self.im_row = action_row(_("Input method settings"), _("Add languages or change the switching keys."))
        self.im_btn = text_button(_("Open"), self.open_im)
        self.im_row.add_suffix(self.im_btn)
        info.add(self.im_row)
        self.add(info)
        rep = Adw.PreferencesGroup(title=_("Key repeat"))
        self.delay_row = Adw.SpinRow.new_with_range(100, 1000, 10)
        self.delay_row.set_title(_("Delay before repeating (ms)"))
        self.rate_row = Adw.SpinRow.new_with_range(5, 100, 1)
        self.rate_row.set_title(_("Repeats per second"))
        for r in (self.delay_row, self.rate_row):
            r.connect("notify::value", self._on_repeat)
            rep.add(r)
        self.add(rep)
        self.connect("map", lambda *_a: self.refresh())

    def refresh(self):
        self.layout_row.set_subtitle(GLib.markup_escape_text(sysutil.keyboard_layout() or "-"))
        self._updating = True
        for row, prop, default in ((self.delay_row, "/Default/KeyRepeat/Delay", 500), (self.rate_row, "/Default/KeyRepeat/Rate", 20)):
            v = sysutil.xfconf_get("keyboards", prop)
            try:
                row.set_value(int(v))
            except (TypeError, ValueError):
                row.set_value(default)
        self._updating = False
        self.im_btn.set_sensitive(sysutil.have("fcitx5-configtool"))

    def open_im(self):
        try:
            Gio.Subprocess.new(["fcitx5-configtool"], Gio.SubprocessFlags.NONE)
        except GLib.Error:
            self.ctx.toast(_("The input method settings could not be opened"))

    def _on_repeat(self, _row, _pspec):
        if self._updating:
            return
        d, r = int(self.delay_row.get_value()), int(self.rate_row.get_value())

        def work():
            sysutil.xfconf_set("keyboards", "/Default/KeyRepeat/Delay", "int", d)
            sysutil.xfconf_set("keyboards", "/Default/KeyRepeat/Rate", "int", r)

        run_async(work)


class StoragePage(Adw.PreferencesPage):
    def __init__(self, ctx):
        super().__init__(title=_("Storage"), icon_name="drive-harddisk-symbolic")
        self.ctx = ctx
        self.group = Adw.PreferencesGroup(title=_("Disks"))
        self.add(self.group)
        self.models_group = Adw.PreferencesGroup(title=_("Model files"))
        self.models_row = action_row(_("Downloaded models"), "-")
        self.models_group.add(self.models_row)
        self.add(self.models_group)
        self._rows = []
        self.connect("map", lambda *_a: self.refresh())

    def refresh(self):
        run_async(sysutil.block_devices, self._got, lambda _e: None)
        run_async(self.ctx.client.models, self._models, lambda _e: None)

    def _models(self, data):
        items = (data or {}).get("models") or []
        total = sum(int(m.get("size_bytes") or 0) for m in items)
        self.models_row.set_subtitle(GLib.markup_escape_text(
            _("%(n)d models, %(size)s") % {"n": len(items), "size": logic.fmt_bytes(total)}))

    def _got(self, devices):
        clear_group(self.group, self._rows)
        if not devices:
            row = action_row(_("Not available"), _("The disk list could not be read."))
            self.group.add(row)
            self._rows.append(row)
            return
        for d in devices:
            kind = _("Disk") if d["type"] == "disk" else _("Partition")
            parts = [kind, logic.fmt_bytes(d["size"])]
            if d["fstype"]:
                parts.append(d["fstype"])
            if d["mounts"]:
                u = sysutil.usage(d["mounts"][0])
                if u:
                    parts.append(_("%(used)s used of %(total)s") % {"used": logic.fmt_bytes(u[0]), "total": logic.fmt_bytes(u[1])})
                parts.append(d["mounts"][0])
            title = d["name"] + ((" · " + d["label"]) if d["label"] else "")
            row = action_row(title, " · ".join(parts))
            if d["type"] == "part":
                row.set_margin_start(16)
            self.group.add(row)
            self._rows.append(row)
