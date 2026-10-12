#!/bin/sh
# Lightlix 그래픽 설치기(Calamares)를 라이브 데스크톱 세션에서 실행한다.
#
# Xfce(X11) 세션에서 root 로 띄운다. 라이브 사용자는 비밀번호 없이 sudo 를 쓸 수 있으므로
# polkit 설정은 필요 없고, root 가 사용자의 X 권한 파일(XAUTHORITY)을 읽어 같은 화면에 연결한다.
# 서버 모드(콘솔)에서는 lightlix-install 을 쓴다.
set -eu

if [ ! -e /run/live ]; then
    echo "lightlix-installer: the installer runs from the live session only" >&2
    exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
    uid="$(id -u)"
    runtime="${XDG_RUNTIME_DIR:-/run/user/$uid}"
    display="${DISPLAY:-}"
    if [ -z "$display" ]; then
        echo "lightlix-installer: no desktop session was found." >&2
        echo "Start the desktop with 'lightlix mode desktop' and run this again, or install from the console with 'sudo lightlix-install run'." >&2
        exit 1
    fi
    xauth_file="${XAUTHORITY:-$HOME/.Xauthority}"
    exec sudo -n env "XDG_RUNTIME_DIR=$runtime" "DISPLAY=$display" "XAUTHORITY=$xauth_file" "$0" "$@"
fi

export QT_QPA_PLATFORM="${QT_QPA_PLATFORM:-xcb}"
exec calamares "$@"
