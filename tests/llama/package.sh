#!/usr/bin/env bash
# opereta-llm .deb 를 CUDA 가 전혀 없는 깨끗한 Debian 컨테이너 안에서 검사한다.
# 컨테이너 안에서 실행한다(scripts/test-llama-in-docker.sh 가 호출). 사용법: package.sh <deb>
#
# CUDA 빌드 패키지(libggml-cuda.so 가 있는 .deb)는 CUDA 라이브러리가 없는 이 환경에서
# CPU 로 추론이 돌아야 한다. 이것이 "CUDA 모듈이 들어 있어도 CPU 로 돈다"의 증명이다.
set -euo pipefail

MODEL_NAME="stories260K.gguf"
MODEL_SHA256="047bf46455a544931cff6fef14d7910154c56afbc23ab1c5e56a72e69912c04b"
MODEL_URL="https://huggingface.co/ggml-org/tiny-llamas/resolve/main/${MODEL_NAME}"

die() {
    echo "package-test: FAIL: $*" >&2
    exit 1
}

[ "$#" -eq 1 ] || die "usage: package.sh <deb>"
DEB="$1"
[ -f "${DEB}" ] || die "no such file: ${DEB}"

export DEBIAN_FRONTEND=noninteractive
apt-get update -qq
apt-get install -y -qq --no-install-recommends libgomp1 libssl3t64 ca-certificates curl >/dev/null

# 전제: 이 컨테이너에는 CUDA 런타임이 없어야 한다.
if ldconfig -p | grep -Fq 'libcudart'; then
    die "precondition: a CUDA runtime is already installed in this container"
fi
echo "package-test: precondition ok (no libcudart in the linker cache)"

WORK="$(mktemp -d)"
MODEL_DIR="$(mktemp -d)"
SERVER_PID=""
cleanup() {
    if [ -n "${SERVER_PID}" ]; then
        kill "${SERVER_PID}" 2>/dev/null || true
        wait "${SERVER_PID}" 2>/dev/null || true
    fi
    rm -rf "${WORK}" "${MODEL_DIR}"
}
trap cleanup EXIT

# 1. 패키지 내용. 설치하지 않고 풀어서 본다.
dpkg-deb -x "${DEB}" "${WORK}/root"
LLAMA="${WORK}/root/usr/lib/opereta/llama"
[ -x "${LLAMA}/llama-server" ] || die "llama-server is missing or not executable"
CPU_COUNT="$(find "${LLAMA}" -maxdepth 1 -name 'libggml-cpu-*.so' | wc -l)"
[ "${CPU_COUNT}" -ge 1 ] || die "no libggml-cpu-*.so backend module"
echo "package-test: CPU backend modules: ${CPU_COUNT}"

# 원본 소스에서 모은 제3자 고지도 실행 파일과 함께 배포되어야 한다.
DOC="${WORK}/root/usr/share/doc/opereta-llm"
for notice in cpp-httplib hash-sha256 hash-xxhash nlohmann-json miniaudio stb-image sheredom-subprocess; do
    [ -s "${DOC}/vendor-licenses/${notice}.txt" ] || die "vendor notice is missing: ${notice}"
done
echo "package-test: vendor notices: 7"

HAS_CUDA=0
if [ -e "${LLAMA}/libggml-cuda.so" ]; then
    HAS_CUDA=1
fi

# 메타데이터: CUDA 빌드는 opereta-nvidia 에 의존해야 하고, CPU 빌드는 그렇지 않아야 한다.
DEPENDS="$(dpkg-deb -f "${DEB}" Depends)"
if [ "${HAS_CUDA}" -eq 1 ]; then
    grep -Fq 'opereta-nvidia' <<<"${DEPENDS}" || die "CUDA build does not depend on opereta-nvidia: ${DEPENDS}"
else
    grep -Fq 'opereta-nvidia' <<<"${DEPENDS}" && die "CPU-only build depends on opereta-nvidia: ${DEPENDS}"
fi
echo "package-test: Depends: ${DEPENDS}"

# 2. CUDA 빌드: 모듈은 CUDA 런타임에 링크되어 있고, 이 환경에는 그 라이브러리가 없어 not found 여야 한다.
if [ "${HAS_CUDA}" -eq 1 ]; then
    echo "package-test: CUDA module found, checking its dependencies"
    LDD_OUT="$(ldd "${LLAMA}/libggml-cuda.so" || true)"
    echo "${LDD_OUT}"
    grep -Fq 'libcudart.so.13 => not found' <<<"${LDD_OUT}" || die "libggml-cuda.so does not link libcudart.so.13 as not found"
fi

# 3. 버전과 장치 목록. 둘 다 0으로 끝나야 한다.
"${LLAMA}/llama-server" --version || die "llama-server --version failed"
DEVICES_OUT="$("${LLAMA}/llama-server" --list-devices 2>&1)" || die "llama-server --list-devices failed"
echo "${DEVICES_OUT}"
grep -Fq 'Available devices:' <<<"${DEVICES_OUT}" || die "--list-devices did not print 'Available devices:'"

# 4. 실제 추론. 작은 공개 모델을 내려받아 sha256 을 확인한 뒤 CPU 로 서버를 띄운다.
curl -fsSL --retry 3 --connect-timeout 30 -o "${MODEL_DIR}/${MODEL_NAME}" "${MODEL_URL}"
echo "${MODEL_SHA256}  ${MODEL_DIR}/${MODEL_NAME}" | sha256sum -c - >/dev/null || die "model sha256 mismatch"
echo "package-test: model sha256 verified: ${MODEL_NAME}"

PORT=$((20000 + RANDOM % 20000))
SERVER_LOG="${WORK}/llama-server.log"
"${LLAMA}/llama-server" -m "${MODEL_DIR}/${MODEL_NAME}" --host 127.0.0.1 --port "${PORT}" -ngl 0 >"${SERVER_LOG}" 2>&1 &
SERVER_PID=$!

HEALTH_OK=0
for _ in $(seq 1 120); do
    if ! kill -0 "${SERVER_PID}" 2>/dev/null; then
        cat "${SERVER_LOG}" >&2
        die "llama-server exited before /health returned 200"
    fi
    if curl -fsS --max-time 2 "http://127.0.0.1:${PORT}/health" >/dev/null 2>&1; then
        HEALTH_OK=1
        break
    fi
    sleep 1
done
if [ "${HEALTH_OK}" -ne 1 ]; then
    cat "${SERVER_LOG}" >&2
    die "/health did not return 200 within 120 seconds"
fi
echo "package-test: /health returned 200 on port ${PORT}"

RESP="$(curl -fsS --max-time 120 -H 'Content-Type: application/json' \
    -d '{"messages":[{"role":"user","content":"Once upon a time"}],"max_tokens":16,"temperature":0}' \
    "http://127.0.0.1:${PORT}/v1/chat/completions")" || { cat "${SERVER_LOG}" >&2; die "chat completion request failed"; }
echo "package-test: response: ${RESP}"
grep -Fq '"choices"' <<<"${RESP}" || die "response has no choices"
grep -Eq '"content":"[^"]+"' <<<"${RESP}" || die "response content is empty"

# 서버 로그에 CUDA 초기화 실패가 나왔다면 기록으로 남긴다. 장치가 없으므로 실패는 정상이다.
if grep -Fq 'ggml_cuda_init' "${SERVER_LOG}"; then
    echo "package-test: server log mentions ggml_cuda_init (expected without a GPU):"
    grep -F 'ggml_cuda_init' "${SERVER_LOG}" | head -n 3
fi

echo "package-test: PASS: $(basename "${DEB}") (CUDA module: $([ "${HAS_CUDA}" -eq 1 ] && echo yes || echo no))"
