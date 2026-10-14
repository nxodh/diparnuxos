#!/usr/bin/env bash
# 릴리스 자산을 모으는 스크립트를 Debian 13 컨테이너에서 확인한다. 호스트에는 아무것도 설치하지 않는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OPERETA_RELEASE_TEST_IMAGE:-debian:trixie}"
command -v docker >/dev/null 2>&1 || { echo "test-release: docker is required" >&2; exit 2; }

docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends apt-utils gnupg xz-utils dpkg-dev coreutils findutils ca-certificates >/dev/null
bash /src/tests/release/test.sh
'
