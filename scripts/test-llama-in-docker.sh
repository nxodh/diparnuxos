#!/usr/bin/env bash
# out/llama/ 의 opereta-llm .deb 를 깨끗한 debian:<suite> 컨테이너에서 검사한다(tests/llama/package.sh).
# 호스트에는 아무것도 설치하지 않는다. 컨테이너는 실행 뒤 --rm 으로 지워진다.
#
# 사용법: scripts/test-llama-in-docker.sh [path/to/opereta-llm_<version>_<arch>.deb]
# 인자가 없으면 out/llama/ 에서 현재 OPERETA_VERSION 의 .deb 를 찾는다(하나여야 한다).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() {
    echo "test-llama-in-docker: ERROR: $*" >&2
    exit 1
}

# shellcheck source=/dev/null
. "${ROOT}/opereta.conf"

if [ "$#" -gt 1 ]; then
    die "usage: test-llama-in-docker.sh [deb]"
fi

if [ "$#" -eq 1 ]; then
    DEB="$1"
else
    shopt -s nullglob
    CANDIDATES=("${ROOT}/out/llama/opereta-llm_${OPERETA_VERSION}_"*.deb)
    shopt -u nullglob
    [ "${#CANDIDATES[@]}" -eq 1 ] || die "expected one opereta-llm .deb in out/llama/, found ${#CANDIDATES[@]}. Run make llama first."
    DEB="${CANDIDATES[0]}"
fi
[ -f "${DEB}" ] || die "no such file: ${DEB}"

DEB_ABS="$(cd "$(dirname "${DEB}")" && pwd)/$(basename "${DEB}")"
DEB_NAME="$(basename "${DEB_ABS}")"
ARCH="${DEB_NAME##*_}"
ARCH="${ARCH%.deb}"
case "${ARCH}" in
    amd64) PLATFORM=linux/amd64 ;;
    arm64) PLATFORM=linux/arm64 ;;
    *) die "unsupported package architecture in file name: ${ARCH}" ;;
esac

if ! command -v docker >/dev/null 2>&1; then
    die "docker command not found. Install Docker Desktop or Docker Engine and run this again."
fi
if ! docker info >/dev/null 2>&1; then
    die "cannot connect to the docker daemon. Check that Docker is running."
fi

docker run --rm \
    --platform "${PLATFORM}" \
    -v "$(dirname "${DEB_ABS}"):/pkg:ro" \
    -v "${ROOT}/tests/llama:/tests:ro" \
    "debian:${DEBIAN_SUITE}" \
    bash /tests/package.sh "/pkg/${DEB_NAME}"

echo "test-llama-in-docker: PASS: ${DEB_NAME} (${PLATFORM})"
