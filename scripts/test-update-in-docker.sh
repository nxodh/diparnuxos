#!/usr/bin/env bash
# lightlix-update 의 통합 시험을 Debian 13 컨테이너에서 실행한다. 실제 apt 와 dpkg 를 쓰지만 컨테이너 안에서만 바뀐다.
# Mac 호스트에는 아무것도 설치하지 않는다. 저장소는 읽기 전용으로 마운트한다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${LIGHTLIX_UPDATE_TEST_IMAGE:-debian:trixie}"

command -v docker >/dev/null 2>&1 || { echo "test-update: docker is required" >&2; exit 2; }

docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive -e DEBUG_APT="${DEBUG_APT:-}" "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends golang-go gcc libc6-dev ca-certificates apt-utils dpkg-dev jq >/dev/null
mkdir /work
cp -a /src/daemon /work/daemon
cd /work/daemon
CGO_ENABLED=1 GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-mod=vendor go build -buildvcs=false -o /usr/local/bin/lightlix-update ./cmd/lightlix-update
bash /src/tests/update/apt-flow.sh
'
