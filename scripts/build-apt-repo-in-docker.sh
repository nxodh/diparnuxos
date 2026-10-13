#!/usr/bin/env bash
# 서명된 Diparnux APT 저장소를 Debian 13 컨테이너에서 만든다. 호스트에는 아무것도 설치하지 않는다.
#
#   DIPARNUX_APT_GNUPGHOME=/path/to/gnupg-home DIPARNUX_APT_KEY_ID=<fingerprint> \
#     scripts/build-apt-repo-in-docker.sh [debs-dir] [out-dir]
#
# 개인 키가 든 GNUPGHOME 은 읽기 전용으로 마운트하지 않고 복사본으로 쓴다(gpg 가 잠금 파일을 만든다).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "${ROOT}/diparnux.conf"
DEBS="${1:-${ROOT}/build/config/packages.chroot}"
OUT="${2:-${ROOT}/out/apt}"
GNUPGHOME_SRC="${DIPARNUX_APT_GNUPGHOME:-}"
KEY_ID="${DIPARNUX_APT_KEY_ID:-}"

die() { echo "build-apt-repo-in-docker: $*" >&2; exit 2; }
command -v docker >/dev/null 2>&1 || die "docker is required"
[[ -d "$DEBS" ]] || die "debs directory not found: $DEBS"
[[ -n "$GNUPGHOME_SRC" && -d "$GNUPGHOME_SRC" ]] || die "set DIPARNUX_APT_GNUPGHOME to a GnuPG home that holds the signing key"
[[ -n "$KEY_ID" ]] || die "set DIPARNUX_APT_KEY_ID to the signing key fingerprint"
mkdir -p "$OUT"
OUT="$(cd "$OUT" && pwd)"
DEBS="$(cd "$DEBS" && pwd)"

args=(--rm --init -v "${ROOT}/scripts:/scripts:ro" -v "${DEBS}:/debs:ro" -v "${OUT}:/out" -v "${GNUPGHOME_SRC}:/gnupg-src:ro"
    -e KEY_ID="$KEY_ID" -e DEBIAN_FRONTEND=noninteractive)
if [[ -n "${DIPARNUX_APT_PASSPHRASE_FILE:-}" ]]; then
    [[ -r "$DIPARNUX_APT_PASSPHRASE_FILE" ]] || die "cannot read DIPARNUX_APT_PASSPHRASE_FILE"
    args+=(-v "${DIPARNUX_APT_PASSPHRASE_FILE}:/passphrase:ro")
fi

docker run "${args[@]}" "debian:${DEBIAN_SUITE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends apt-utils gnupg xz-utils dpkg-dev >/dev/null
cp -a /gnupg-src /tmp/gnupg && chmod 700 /tmp/gnupg
extra=()
[ -r /passphrase ] && extra=(--passphrase-file /passphrase)
bash /scripts/build-apt-repo.sh --debs /debs --out /out/tree --key "$KEY_ID" --gnupghome /tmp/gnupg "${extra[@]}"
chown -R "$(stat -c %u:%g /out)" /out/tree
'
echo "build-apt-repo-in-docker: repository written to ${OUT}/tree"
