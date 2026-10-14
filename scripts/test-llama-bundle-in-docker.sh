#!/usr/bin/env bash
# CUDA 런타임 번들링을 합성 라이브러리로 컨테이너에서 확인한다(tests/llama/bundle.sh). 호스트에는 아무것도 설치하지 않는다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OPERETA_BUNDLE_TEST_IMAGE:-debian:trixie}"
command -v docker >/dev/null 2>&1 || { echo "test-llama-bundle: docker is required" >&2; exit 2; }
docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends gcc libc6-dev binutils >/dev/null
bash /src/tests/llama/bundle.sh
'
