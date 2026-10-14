#!/usr/bin/env bash
# Opereta APT 저장소 서명 키를 만든다. 개인 키는 이 저장소 밖(GNUPGHOME)에 두고, 공개 키만 keys/ 에 내보낸다.
#
#   scripts/apt-keygen.sh /path/to/private-gnupg-home
#
# 이 스크립트는 키를 만들고 공개 키를 내보낼 뿐 개인 키를 어디에도 올리지 않는다. 개인 키의 보관(비밀번호 관리자,
# 하드웨어 토큰, CI 비밀 저장소)과 호스팅은 사용자가 정한다. 자세한 절차는 docs/apt-repository.md 에 있다.
# gpg 는 Docker 컨테이너에서 실행하므로 호스트에는 아무것도 설치하지 않는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
HOME_DIR="${1:-}"
KEYS_DIR="${OPERETA_KEYS_DIR:-${ROOT}/keys}"
die() { echo "apt-keygen: $*" >&2; exit 2; }
[[ -n "$HOME_DIR" ]] || die "usage: $0 /path/to/private-gnupg-home"
command -v docker >/dev/null 2>&1 || die "docker is required"
[[ ! -e "$HOME_DIR" || -z "$(ls -A "$HOME_DIR" 2>/dev/null)" ]] || die "$HOME_DIR already exists and is not empty"
case "$(cd "$(dirname "$HOME_DIR")" && pwd)/$(basename "$HOME_DIR")" in
    "$ROOT"/*) die "the private key must not be stored inside the repository" ;;
esac
mkdir -p "$HOME_DIR"
chmod 700 "$HOME_DIR"
HOME_DIR="$(cd "$HOME_DIR" && pwd)"
mkdir -p "$KEYS_DIR"
KEYS_DIR="$(cd "$KEYS_DIR" && pwd)"

docker run --rm --init -v "${HOME_DIR}:/gnupg" -v "${KEYS_DIR}:/keys" -e DEBIAN_FRONTEND=noninteractive debian:trixie bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends gnupg >/dev/null
export GNUPGHOME=/gnupg
chmod 700 /gnupg
gpg --batch --pinentry-mode loopback --passphrase "" --quick-generate-key "Opereta Archive Signing Key <noreply@users.noreply.github.com>" ed25519 sign 5y
fpr="$(gpg --batch --with-colons --list-secret-keys | awk -F: "/^fpr:/ {print \$10; exit}")"
gpg --batch --export "$fpr" > /keys/opereta-archive-keyring.pgp
gpg --batch --armor --export "$fpr" > /keys/opereta-archive-keyring.asc
echo "$fpr" > /keys/opereta-archive-keyring.fingerprint
chown -R "$(stat -c %u:%g /keys)" /keys
echo "key fingerprint: $fpr"
'
echo "apt-keygen: the public key was written to ${KEYS_DIR}/opereta-archive-keyring.pgp (binary) and .asc (armored)."
echo "apt-keygen: the private key is in $HOME_DIR. It has an empty passphrase; protect the directory or add a passphrase with gpg --edit-key."
