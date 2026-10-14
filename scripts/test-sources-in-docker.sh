#!/usr/bin/env bash
# 소스 수집기를 컨테이너에서 확인한다(tests/sources/test.sh). 네트워크가 필요하다. 호스트에는 아무것도 설치하지 않는다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "${ROOT}/opereta.conf"
command -v docker >/dev/null 2>&1 || { echo "test-sources: docker is required" >&2; exit 2; }
docker run --rm --init -v "${ROOT}:/src:ro" \
    -e DEBIAN_SUITE="${DEBIAN_SUITE}" -e DEBIAN_AREAS="${DEBIAN_AREAS}" -e DEBIAN_MIRROR="${DEBIAN_MIRROR}" \
    -e DEBIAN_SECURITY_MIRROR="${DEBIAN_SECURITY_MIRROR}" -e DEBIAN_SNAPSHOT="${DEBIAN_SNAPSHOT:-}" -e DEBIAN_SNAPSHOT_URL="${DEBIAN_SNAPSHOT_URL}" \
    "debian:${DEBIAN_SUITE}" bash /src/tests/sources/test.sh
