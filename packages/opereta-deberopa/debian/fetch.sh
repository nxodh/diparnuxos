#!/bin/sh
# Deberopa 릴리스 실행 파일을 받아 SHA-256 을 확인하고 <out>/deberopa 로 푼다.
# 사용법: fetch.sh <opereta.conf> <out-dir>
# OPERETA_DEBEROPA_TARBALL 이 있으면 내려받지 않고 그 파일을 쓴다(오프라인 빌드와 시험용). 해시는 똑같이 확인한다.
set -eu

conf="$1"
out="$2"
# shellcheck source=/dev/null
. "$conf"
: "${DEBEROPA_VERSION:?DEBEROPA_VERSION is missing in opereta.conf}"
: "${DEBEROPA_REPO:?DEBEROPA_REPO is missing in opereta.conf}"
: "${DEBEROPA_SHA256:?DEBEROPA_SHA256 is missing in opereta.conf}"

case "$DEBEROPA_SHA256" in
    *[!0-9a-f]*) echo "fetch-deberopa: DEBEROPA_SHA256 is not a lowercase hex digest" >&2; exit 1 ;;
esac
[ "${#DEBEROPA_SHA256}" -eq 64 ] || { echo "fetch-deberopa: DEBEROPA_SHA256 must be 64 hex characters" >&2; exit 1; }

mkdir -p "$out"
tarball="$out/deberopa-linux-x64.tar.gz"
if [ -n "${OPERETA_DEBEROPA_TARBALL:-}" ]; then
    cp -f "$OPERETA_DEBEROPA_TARBALL" "$tarball"
else
    url="$DEBEROPA_REPO/releases/download/v$DEBEROPA_VERSION/deberopa-linux-x64.tar.gz"
    curl --fail --silent --show-error --location --proto '=https' --tlsv1.2 --retry 3 --output "$tarball" "$url"
fi

printf '%s  %s\n' "$DEBEROPA_SHA256" "$tarball" | sha256sum -c - >/dev/null || {
    echo "fetch-deberopa: SHA-256 of deberopa-linux-x64.tar.gz differs from DEBEROPA_SHA256 (v$DEBEROPA_VERSION)" >&2
    rm -f "$tarball"
    exit 1
}

# 묶음에는 deberopa 하나만 있어야 한다. 다른 이름이 있으면 경로 탈출일 수 있으므로 거절한다.
names="$(tar -tzf "$tarball")"
[ "$names" = deberopa ] || { echo "fetch-deberopa: the archive must contain only 'deberopa' (found: $names)" >&2; exit 1; }
tar -xzf "$tarball" -C "$out" deberopa
chmod 0755 "$out/deberopa"

# 이 빌더(x86-64)에서는 실행해 버전이 고정 값과 같은지 확인한다.
if [ "$(uname -m)" = x86_64 ]; then
    got="$("$out/deberopa" --version)"
    [ "$got" = "$DEBEROPA_VERSION" ] || { echo "fetch-deberopa: deberopa reports '$got', expected '$DEBEROPA_VERSION'" >&2; exit 1; }
fi
echo "fetch-deberopa: deberopa $DEBEROPA_VERSION verified"
