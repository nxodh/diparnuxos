#!/bin/sh
# Opereta 정체성 파일(/usr/lib/os-release, /etc/dpkg/origins/<ID>)을 opereta.conf 에서 생성한다.
# 패키지 소스에는 값을 적지 않고, 빌드할 때 이 스크립트가 conf 를 읽어 쓴다.
# 사용법: gen-identity.sh <opereta.conf 경로> <staging 디렉터리>
set -eu

if [ "$#" -ne 2 ]; then
    echo "usage: gen-identity.sh <opereta.conf> <staging-dir>" >&2
    exit 2
fi

conf="$1"
dest="$2"

if [ ! -r "$conf" ]; then
    echo "gen-identity: opereta.conf 를 읽을 수 없다: $conf" >&2
    exit 1
fi

# shellcheck source=/dev/null
. "$conf"

mkdir -p "$dest/usr/lib" "$dest/etc/dpkg/origins"

cat > "$dest/usr/lib/os-release" <<EOF
PRETTY_NAME="$OPERETA_NAME $OPERETA_VERSION"
NAME="$OPERETA_NAME"
LOGO=opereta
ANSI_COLOR="1;37"
VERSION_ID="$OPERETA_VERSION"
VERSION="$OPERETA_VERSION ($DEBIAN_SUITE)"
ID=$OPERETA_ID
ID_LIKE=debian
VERSION_CODENAME=$DEBIAN_SUITE
HOME_URL="$OPERETA_HOME_URL"
SUPPORT_URL="$OPERETA_HOME_URL/issues"
BUG_REPORT_URL="$OPERETA_HOME_URL/issues"
EOF
chmod 0644 "$dest/usr/lib/os-release"

# dpkg origin 필드는 deb-origin(5) 의 Vendor, Vendor-URL, Bugs, Parent 를 쓴다.
cat > "$dest/etc/dpkg/origins/$OPERETA_ID" <<EOF
Vendor: $OPERETA_NAME
Vendor-URL: $OPERETA_HOME_URL
Bugs: $OPERETA_HOME_URL/issues
Parent: Debian
EOF
chmod 0644 "$dest/etc/dpkg/origins/$OPERETA_ID"
