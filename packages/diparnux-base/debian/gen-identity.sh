#!/bin/sh
# Diparnux 정체성 파일(/usr/lib/os-release, /etc/dpkg/origins/<ID>)을 diparnux.conf 에서 생성한다.
# 패키지 소스에는 값을 적지 않고, 빌드할 때 이 스크립트가 conf 를 읽어 쓴다.
# 사용법: gen-identity.sh <diparnux.conf 경로> <staging 디렉터리>
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: gen-identity.sh <diparnux.conf> <staging-dir>" >&2
    exit 2
fi

conf="$1"
dest="$2"

if [ ! -r "$conf" ]; then
    echo "gen-identity: diparnux.conf 를 읽을 수 없다: $conf" >&2
    exit 1
fi

# shellcheck source=/dev/null
. "$conf"

mkdir -p "$dest/usr/lib" "$dest/etc/dpkg/origins"

cat > "$dest/usr/lib/os-release" <<EOF
PRETTY_NAME="$DIPARNUX_NAME $DIPARNUX_VERSION"
NAME="$DIPARNUX_NAME"
LOGO=diparnux
ANSI_COLOR="1;37"
VERSION_ID="$DIPARNUX_VERSION"
VERSION="$DIPARNUX_VERSION ($DEBIAN_SUITE)"
ID=$DIPARNUX_ID
ID_LIKE=debian
VERSION_CODENAME=$DEBIAN_SUITE
HOME_URL="$DIPARNUX_HOME_URL"
SUPPORT_URL="$DIPARNUX_HOME_URL/issues"
BUG_REPORT_URL="$DIPARNUX_HOME_URL/issues"
EOF
chmod 0644 "$dest/usr/lib/os-release"

# dpkg origin 필드는 deb-origin(5) 의 Vendor, Vendor-URL, Bugs, Parent 를 쓴다.
cat > "$dest/etc/dpkg/origins/$DIPARNUX_ID" <<EOF
Vendor: $DIPARNUX_NAME
Vendor-URL: $DIPARNUX_HOME_URL
Bugs: $DIPARNUX_HOME_URL/issues
Parent: Debian
EOF
chmod 0644 "$dest/etc/dpkg/origins/$DIPARNUX_ID"
