#!/usr/bin/env bash
# Control Center 의 단위 테스트와 GTK 스모크 테스트를 Debian 13 컨테이너(xvfb)에서 실행한다.
# Mac 호스트에는 아무것도 설치하지 않는다. 저장소는 읽기 전용으로 마운트한다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OPERETA_GUI_TEST_IMAGE:-debian:trixie}"

command -v docker >/dev/null 2>&1 || { echo "test-gui: docker is required" >&2; exit 2; }

docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends python3 python3-gi gir1.2-gtk-4.0 gir1.2-adw-1 \
    xvfb xauth libgl1-mesa-dri gettext >/dev/null
cp -r /src/gui /gui
cd /gui
export GDK_BACKEND=x11 GSK_RENDERER=cairo NO_AT_BRIDGE=1
xvfb-run -a python3 -B -m unittest discover -s tests -t . -v
'
