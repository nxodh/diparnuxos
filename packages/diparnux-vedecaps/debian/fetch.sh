#!/bin/sh
# Vedecaps 릴리스 실행 파일을 받아 SHA-256 을 확인하고 <out>/vedecaps 로 푼다.
# 사용법: fetch.sh <diparnux.conf> <out-dir>
# DIPARNUX_VEDECAPS_TARBALL 이 있으면 내려받지 않고 그 파일을 쓴다(오프라인 빌드와 시험용). 해시는 똑같이 확인한다.
set -eu

conf="$1"
out="$2"
# shellcheck source=/dev/null
. "$conf"
: "${VEDECAPS_VERSION:?VEDECAPS_VERSION is missing in diparnux.conf}"
: "${VEDECAPS_REPO:?VEDECAPS_REPO is missing in diparnux.conf}"
: "${VEDECAPS_SHA256:?VEDECAPS_SHA256 is missing in diparnux.conf}"

case "$VEDECAPS_SHA256" in
    *[!0-9a-f]*) echo "fetch-vedecaps: VEDECAPS_SHA256 is not a lowercase hex digest" >&2; exit 1 ;;
esac
[ "${#VEDECAPS_SHA256}" -eq 64 ] || { echo "fetch-vedecaps: VEDECAPS_SHA256 must be 64 hex characters" >&2; exit 1; }

mkdir -p "$out"
tarball="$out/vedecaps-linux-x64.tar.gz"
if [ -n "${DIPARNUX_VEDECAPS_TARBALL:-}" ]; then
    cp -f "$DIPARNUX_VEDECAPS_TARBALL" "$tarball"
else
    url="$VEDECAPS_REPO/releases/download/v$VEDECAPS_VERSION/vedecaps-linux-x64.tar.gz"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --retry 3 --output "$tarball" "$url"
fi

printf '%s  %s\n' "$VEDECAPS_SHA256" "$tarball" | sha256sum -c - >/dev/null || {
    echo "fetch-vedecaps: SHA-256 of vedecaps-linux-x64.tar.gz differs from VEDECAPS_SHA256 (v$VEDECAPS_VERSION)" >&2
    rm -f "$tarball"
    exit 1
}

# 묶음에는 vedecaps 하나만 있어야 한다. 다른 이름이 있으면 경로 탈출일 수 있으므로 거절한다.
names="$(tar -tzf "$tarball")"
[ "$names" = vedecaps ] || { echo "fetch-vedecaps: the archive must contain only 'vedecaps' (found: $names)" >&2; exit 1; }
tar -xzf "$tarball" -C "$out" vedecaps
chmod 0755 "$out/vedecaps"

# 이 빌더(x86-64)에서는 실행해 버전이 고정 값과 같은지 확인한다.
if [ "$(uname -m)" = x86_64 ]; then
    got="$("$out/vedecaps" --version)"
    [ "$got" = "$VEDECAPS_VERSION" ] || { echo "fetch-vedecaps: vedecaps reports '$got', expected '$VEDECAPS_VERSION'" >&2; exit 1; }
fi
echo "fetch-vedecaps: vedecaps $VEDECAPS_VERSION verified"
