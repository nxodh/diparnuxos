#!/usr/bin/env bash
# lightlix-installer 의 Calamares 설정을 실제 Calamares 패키지가 있는 Debian 13 컨테이너에서 검사한다.
# Mac 호스트에는 아무것도 설치하지 않는다. 저장소는 읽기 전용으로 마운트한다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${LIGHTLIX_INSTALLER_TEST_IMAGE:-debian:trixie}"

command -v docker >/dev/null 2>&1 || { echo "test-installer-config: docker is required" >&2; exit 2; }

docker run --rm --init --platform linux/amd64 -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends calamares python3-yaml python3-jsonschema >/dev/null
python3 -B /src/packages/lightlix-installer/tests/config.py /src/packages/lightlix-installer/src
'
