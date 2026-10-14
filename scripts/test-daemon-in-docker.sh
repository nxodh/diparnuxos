#!/usr/bin/env bash
# Go 데몬 모듈의 vet 과 단위 시험을 Debian 13 컨테이너에서 실행한다. Mac 호스트에는 Go 를 설치하지 않는다.
# 저장소는 읽기 전용으로 마운트하고 컨테이너 안의 복사본에서 돌리므로 gofmt 같은 변경은 저장소에 반영되지 않는다.
# 사용법: scripts/test-daemon-in-docker.sh [go test 인자...]   (기본: ./...)
#   서식 검사: OPERETA_GOFMT=1 scripts/test-daemon-in-docker.sh
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OPERETA_GO_TEST_IMAGE:-debian:trixie}"
ARGS="${*:-./...}"

command -v docker >/dev/null 2>&1 || { echo "test-daemon: docker is required" >&2; exit 2; }

docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive -e ARGS="${ARGS}" -e GOFMT="${OPERETA_GOFMT:-0}" "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends golang-go gcc libc6-dev ca-certificates iproute2 >/dev/null
rm -rf /work && mkdir /work && cp -a /src/daemon /src/opereta.conf /work/ && cd /work/daemon
export GOPROXY=off GOTOOLCHAIN=local CGO_ENABLED=1 GOFLAGS=-mod=vendor GOCACHE=/tmp/gocache
if [ "$GOFMT" = 1 ]; then
    out="$(gofmt -l ./internal ./cmd)"
    if [ -n "$out" ]; then echo "gofmt needed:" >&2; echo "$out" >&2; exit 1; fi
    echo "gofmt: ok"
fi
# NVML 헤더의 cgo 경고(deprecated)는 소음이므로 걸러서 보인다.
go vet ./... 2>&1 | grep -v "cgo-gcc-prolog\|nvml\.h\|DEPRECATED\|^ *[0-9]* |\|\^~\|note: declared\|In file included\|^#" || true
go vet ./... >/dev/null 2>&1 || { echo "go vet failed" >&2; go vet ./... 2>&1 | grep -v "cgo-gcc-prolog\|nvml.h\|DEPRECATED" | tail -20 >&2; exit 1; }
go test $ARGS
'
