#!/usr/bin/env bash
# Docker 빌더 이미지를 만들고, 컨테이너 안에서 scripts/build-iso.sh 를 실행해 ISO 를 빌드한다.
# 저장소는 읽기 전용으로 마운트하고, 호스트에 쓰이는 곳은 out/ 뿐이다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLATFORM="linux/amd64"

# shellcheck source=/dev/null
. "${ROOT}/opereta.conf"
IMAGE_TAG="opereta-builder:${DEBIAN_SUITE}"

die() {
    echo "build-in-docker: $*" >&2
    exit 1
}

if ! command -v docker >/dev/null 2>&1; then
    die "docker 명령이 없다. Docker Desktop 또는 Docker Engine 을 설치한 뒤 다시 실행하라."
fi

if ! docker info >/dev/null 2>&1; then
    die "docker 데몬에 연결할 수 없다. Docker 가 실행 중인지 확인하라."
fi

mkdir -p "${ROOT}/out" "${ROOT}/out/llama"

docker build \
    --platform "${PLATFORM}" \
    --build-arg DEBIAN_SUITE="${DEBIAN_SUITE}" \
    -t "${IMAGE_TAG}" \
    -f "${ROOT}/build/Dockerfile" \
    "${ROOT}/build"

# --privileged: live-build 가 chroot 안에서 mount 와 debootstrap 을 쓰기 때문이다.
# out/llama 는 make llama 가 만든 opereta-llm .deb 를 담으며, 컨테이너에서는 읽기 전용이다.
docker run --rm --privileged \
    --platform "${PLATFORM}" \
    -v "${ROOT}:/src:ro" \
    -v "${ROOT}/out:/out" \
    -v "${ROOT}/out/llama:/llama:ro" \
    -e OPERETA_OUT=/out \
    -e OPERETA_WORKDIR=/var/tmp/opereta-build \
    "${IMAGE_TAG}" \
    /src/scripts/build-iso.sh

echo "build-in-docker: 완료. 산출물은 ${ROOT}/out/ 에 있다."
