#!/usr/bin/env bash
# APT 저장소 빌드 스크립트와 클라이언트 서명 검증을 Debian 13 컨테이너에서 확인한다. 호스트에는 아무것도 설치하지 않는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${LIGHTLIX_APT_TEST_IMAGE:-debian:trixie}"
command -v docker >/dev/null 2>&1 || { echo "test-apt-repo: docker is required" >&2; exit 2; }

docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends apt-utils gnupg xz-utils dpkg-dev ca-certificates >/dev/null
bash /src/tests/apt-repo/test.sh
'
