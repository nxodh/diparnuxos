#!/usr/bin/env bash
# 서명된 Opereta APT 저장소를 Debian 13 컨테이너에서 만든다. 호스트에는 아무것도 설치하지 않는다.
#
#   OPERETA_APT_GNUPGHOME=/path/to/gnupg-home OPERETA_APT_KEY_ID=<fingerprint> \
#     scripts/build-apt-repo-in-docker.sh [debs-dir] [out-dir]
#
# 개인 키가 든 GNUPGHOME 은 읽기 전용으로 마운트하지 않고 복사본으로 쓴다(gpg 가 잠금 파일을 만든다).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "${ROOT}/opereta.conf"
DEBS="${1:-${ROOT}/build/config/packages.chroot}"
OUT="${2:-${ROOT}/out/apt}"
GNUPGHOME_SRC="${OPERETA_APT_GNUPGHOME:-}"
KEY_ID="${OPERETA_APT_KEY_ID:-}"
# 저장소 형식. opereta.conf 의 OPERETA_APT_LAYOUT 이 flat 이면 GitHub 릴리스 자산에 올릴 평평한 저장소를 만든다.
LAYOUT="${OPERETA_APT_LAYOUT:-flat}"

die() { echo "build-apt-repo-in-docker: $*" >&2; exit 2; }
command -v docker >/dev/null 2>&1 || die "docker is required"
[[ -d "$DEBS" ]] || die "debs directory not found: $DEBS"
[[ -n "$GNUPGHOME_SRC" && -d "$GNUPGHOME_SRC" ]] || die "set OPERETA_APT_GNUPGHOME to a GnuPG home that holds the signing key"
[[ -n "$KEY_ID" ]] || die "set OPERETA_APT_KEY_ID to the signing key fingerprint"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
DEBS="$(cd "$DEBS" && pwd)"

[[ "$LAYOUT" == flat || "$LAYOUT" == pool ]] || die "OPERETA_APT_LAYOUT must be flat or pool: $LAYOUT"
args=(--rm --init -e LAYOUT="$LAYOUT" -v "${ROOT}/scripts:/scripts:ro" -v "${DEBS}:/debs:ro" -v "${OUT}:/out" -v "${GNUPGHOME_SRC}:/gnupg-src:ro"
    -e KEY_ID="$KEY_ID" -e DEBIAN_FRONTEND=noninteractive)
if [[ -n "${OPERETA_APT_PASSPHRASE_FILE:-}" ]]; then
    [[ -r "$OPERETA_APT_PASSPHRASE_FILE" ]] || die "cannot read OPERETA_APT_PASSPHRASE_FILE"
    args+=(-v "${OPERETA_APT_PASSPHRASE_FILE}:/passphrase:ro")
fi

docker run "${args[@]}" "debian:${DEBIAN_SUITE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends apt-utils gnupg xz-utils dpkg-dev >/dev/null
cp -a /gnupg-src /tmp/gnupg && chmod 700 /tmp/gnupg
extra=()
[ -r /passphrase ] && extra=(--passphrase-file /passphrase)
[ "$LAYOUT" = flat ] && extra+=(--flat)
bash /scripts/build-apt-repo.sh --debs /debs --out /out/tree --key "$KEY_ID" --gnupghome /tmp/gnupg "${extra[@]}"
chown -R "$(stat -c %u:%g /out)" /out/tree
'
echo "build-apt-repo-in-docker: repository written to ${OUT}/tree"
