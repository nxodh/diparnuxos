#!/usr/bin/env bash
# llama/Dockerfile 로 빌더 이미지를 만들고, 컨테이너 안에서 llama/build.sh 를 실행해
# opereta-llm .deb 를 out/llama/ 에 만든다. 저장소는 읽기 전용으로 마운트하고, 쓰이는 곳은 out/llama/ 뿐이다.
#
# 환경 변수:
#   LLAMA_PLATFORM  빌드 플랫폼. 기본 linux/amd64. 스모크 빌드는 linux/arm64 를 준다.
#   LLAMA_CUDA      ON(기본) 또는 OFF. OFF 는 CUDA 저장소와 패키지를 건너뛰는 CPU 전용 스모크 빌드다.
#
# 빌드 키가 out/llama/BUILD_KEY 와 같고 .deb 가 있으면 빌드를 건너뛴다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
PLATFORM="${LLAMA_PLATFORM:-linux/amd64}"
LLAMA_CUDA="${LLAMA_CUDA:-ON}"

die() {
    echo "build-llama-in-docker: ERROR: $*" >&2
    exit 1
}

# shellcheck source=/dev/null
. "${ROOT}/opereta.conf"

case "${PLATFORM}" in
    linux/amd64) ARCH=amd64 ;;
    linux/arm64) ARCH=arm64 ;;
    *) die "LLAMA_PLATFORM must be linux/amd64 or linux/arm64 (got: ${PLATFORM})" ;;
esac
case "${LLAMA_CUDA}" in
    ON | OFF) ;;
    *) die "LLAMA_CUDA must be ON or OFF (got: ${LLAMA_CUDA})" ;;
esac

if ! command -v docker >/dev/null 2>&1; then
    die "docker command not found. Install Docker Desktop or Docker Engine and run this again."
fi
if ! docker info >/dev/null 2>&1; then
    die "cannot connect to the docker daemon. Check that Docker is running."
fi

sha256_stdin() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum | cut -d' ' -f1
    else
        shasum -a 256 | cut -d' ' -f1
    fi
}

# 빌드 키: llama/ 아래 파일(README.md 제외), opereta.conf, 그리고 CUDA 여부와 플랫폼.
# README.md 는 빌드 결과에 영향을 주지 않으므로 뺀다.
KEY="$(
    {
        cd "${ROOT}"
        find llama -type f ! -name README.md | LC_ALL=C sort | while IFS= read -r f; do
            printf '%s  %s\n' "$(sha256_stdin < "${f}")" "${f}"
        done
        printf '%s  %s\n' "$(sha256_stdin < opereta.conf)" opereta.conf
        printf 'LLAMA_CUDA=%s\nLLAMA_PLATFORM=%s\n' "${LLAMA_CUDA}" "${PLATFORM}"
    } | sha256_stdin
)"

OUT="${ROOT}/out/llama"
DEB="${OUT}/opereta-llm_${OPERETA_VERSION}_${ARCH}.deb"

if [ -f "${OUT}/BUILD_KEY" ] && [ "$(cat "${OUT}/BUILD_KEY")" = "${KEY}" ] && [ -f "${DEB}" ]; then
    echo "build-llama-in-docker: up to date (BUILD_KEY ${KEY}), skipping: ${DEB}"
    exit 0
fi

mkdir -p "${OUT}"
# 이전 키를 먼저 지운다. 빌드가 실패하면 키 파일이 없으므로 다음 실행에서 다시 빌드한다.
rm -f "${OUT}/BUILD_KEY"

IMAGE_TAG="opereta-llm-builder:${DEBIAN_SUITE}-${ARCH}-cuda${LLAMA_CUDA}"

docker build \
    --platform "${PLATFORM}" \
    --build-arg DEBIAN_SUITE="${DEBIAN_SUITE}" \
    --build-arg LLAMA_CUDA="${LLAMA_CUDA}" \
    --build-arg CUDA_KEYRING_URL="${NVIDIA_REPO_URL}/cuda-keyring_${CUDA_KEYRING_VERSION}_all.deb" \
    --build-arg CUDA_KEYRING_SHA256="${CUDA_KEYRING_SHA256}" \
    --build-arg CUDA_PKG_SUFFIX="${CUDA_PKG_SUFFIX}" \
    --build-arg CUDA_MINIMAL_BUILD_VERSION="${CUDA_MINIMAL_BUILD_VERSION}" \
    --build-arg CUDA_CUBLAS_VERSION="${CUDA_CUBLAS_VERSION}" \
    -t "${IMAGE_TAG}" \
    -f "${ROOT}/llama/Dockerfile" \
    "${ROOT}/llama"

# 컨테이너 안에서는 빌드 트리(/var/tmp)를 쓰고, 호스트와 맞닿는 곳은 /out 뿐이다.
docker run --rm \
    --platform "${PLATFORM}" \
    -v "${ROOT}:/src:ro" \
    -v "${OUT}:/out" \
    -e OPERETA_SRC=/src \
    -e OPERETA_OUT=/out \
    -e LLAMA_WORK=/var/tmp/llama-build \
    -e LLAMA_CUDA="${LLAMA_CUDA}" \
    -e LLAMA_BUILD_KEY="${KEY}" \
    "${IMAGE_TAG}" \
    bash /src/llama/build.sh

[ -f "${DEB}" ] || die "build finished but ${DEB} is missing"
echo "build-llama-in-docker: done: ${DEB}"
echo "build-llama-in-docker: BUILD_KEY ${KEY}"
