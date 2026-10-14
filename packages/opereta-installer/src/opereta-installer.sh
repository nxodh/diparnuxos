#!/bin/sh
# Opereta 그래픽 설치기(Calamares)를 라이브 데스크톱 세션에서 실행한다.
#
# Xfce(X11) 세션에서 root 로 띄운다. 라이브 사용자는 비밀번호 없이 sudo 를 쓸 수 있으므로
# polkit 설정은 필요 없고, root 가 사용자의 X 권한 파일(XAUTHORITY)을 읽어 같은 화면에 연결한다.
# 서버 모드(콘솔)에서는 opereta-install 을 쓴다.
set -eu

if [ ! -e /run/live ]; then
    echo "opereta-installer: the installer runs from the live session only" >&2
    exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
    uid="$(id -u)"
    runtime="${XDG_RUNTIME_DIR:-/run/user/$uid}"
    display="${DISPLAY:-}"
    if [ -z "$display" ]; then
        echo "opereta-installer: no desktop session was found." >&2
        echo "Start the desktop with 'opereta mode desktop' and run this again, or install from the console with 'sudo opereta-install run'." >&2
        exit 1
    fi
    xauth_file="${XAUTHORITY:-$HOME/.Xauthority}"
    exec sudo -n env "XDG_RUNTIME_DIR=$runtime" "DISPLAY=$display" "XAUTHORITY=$xauth_file" "$0" "$@"
fi

# UEFI 로 부팅하지 않았으면 부팅할 수 있는 시스템을 만들 수 없다. 설치를 시작하기 전에 알린다.
# (시험이나 특수한 경우에는 OPERETA_INSTALLER_ALLOW_BIOS=1 로 건너뛴다. 설치 단계는 여전히 UEFI 를 요구한다.)
if [ ! -d /sys/firmware/efi ] && [ "${OPERETA_INSTALLER_ALLOW_BIOS:-}" != 1 ]; then
    msg="Opereta needs UEFI mode. This computer was started in legacy BIOS (CSM) mode, so a bootable system cannot be installed.

Restart, choose the UEFI entry of the installation USB in the firmware boot menu (not the entry without 'UEFI'), or turn off CSM / legacy boot in the firmware settings."
    echo "opereta-installer: $msg" >&2
    if command -v python3 >/dev/null 2>&1; then
        python3 - "$msg" <<'PY' 2>/dev/null || true
import sys
import gi
gi.require_version("Gtk", "3.0")
from gi.repository import Gtk
d = Gtk.MessageDialog(message_type=Gtk.MessageType.ERROR, buttons=Gtk.ButtonsType.CLOSE, text="Opereta needs UEFI mode")
d.format_secondary_text(sys.argv[1])
d.run()
PY
    fi
    exit 1
fi

export QT_QPA_PLATFORM="${QT_QPA_PLATFORM:-xcb}"
exec calamares "$@"
