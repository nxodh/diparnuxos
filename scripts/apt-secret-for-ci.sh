#!/usr/bin/env bash
# 서명 개인 키(scripts/apt-keygen.sh 가 만든 GNUPGHOME)를 ASCII 로 표준 출력에 낸다. GitHub 비밀 값에 바로 넣는 데 쓴다.
#
#   bash scripts/apt-secret-for-ci.sh ~/opereta-apt-key | gh secret set OPERETA_APT_SIGNING_KEY
#
# 개인 키가 터미널에 그대로 찍히지 않도록 반드시 파이프로 넘긴다(표준 출력이 터미널이면 거절한다).
# gpg 는 Docker 컨테이너에서 실행하므로 호스트에는 아무것도 설치하지 않는다.
set -euo pipefail

HOME_DIR="${1:-}"
die() { echo "apt-secret-for-ci: $*" >&2; exit 2; }
[[ -n "$HOME_DIR" && -d "$HOME_DIR" ]] || die "usage: $0 /path/to/private-gnupg-home | gh secret set OPERETA_APT_SIGNING_KEY"
[[ ! -t 1 ]] || die "refusing to print a private key to a terminal; pipe it into gh secret set"
command -v docker >/dev/null 2>&1 || die "docker is required"
HOME_DIR="$(cd "$HOME_DIR" && pwd)"

docker run --rm --init -v "${HOME_DIR}:/gnupg:ro" -e DEBIAN_FRONTEND=noninteractive debian:trixie bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends gnupg >/dev/null
cp -a /gnupg /tmp/gnupg && chmod 700 /tmp/gnupg
export GNUPGHOME=/tmp/gnupg
fpr="$(gpg --batch --with-colons --list-secret-keys | awk -F: "/^fpr:/ {print \$10; exit}")"
[ -n "$fpr" ] || { echo "no secret key found" >&2; exit 1; }
gpg --batch --pinentry-mode loopback --passphrase "" --armor --export-secret-keys "$fpr"
'
