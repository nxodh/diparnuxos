#!/usr/bin/env bash
# 실제 Vedecaps 실행 파일(opereta.conf 의 고정 버전, SHA-256 확인)을 Opereta 게이트웨이와 가짜 모델 서버에 붙여
# 연결과 키 규칙이 설명대로 동작하는지 Debian 13 컨테이너에서 확인한다. Mac 호스트에는 아무것도 설치하지 않는다.
# 사용법: scripts/test-vedecaps-in-docker.sh
# 오프라인 시험: OPERETA_VEDECAPS_TARBALL=<vedecaps-linux-x64.tar.gz 경로> 를 주면 내려받지 않는다(해시는 확인한다).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OPERETA_VEDECAPS_TEST_IMAGE:-debian:trixie}"

command -v docker >/dev/null 2>&1 || { echo "test-vedecaps: docker is required" >&2; exit 2; }

mounts=(-v "${ROOT}:/src:ro")
if [ -n "${OPERETA_VEDECAPS_TARBALL:-}" ]; then
    [ -r "${OPERETA_VEDECAPS_TARBALL}" ] || { echo "test-vedecaps: cannot read ${OPERETA_VEDECAPS_TARBALL}" >&2; exit 2; }
    mounts+=(-v "${OPERETA_VEDECAPS_TARBALL}:/offline/vedecaps-linux-x64.tar.gz:ro" -e OPERETA_VEDECAPS_TARBALL=/offline/vedecaps-linux-x64.tar.gz)
fi

# Vedecaps 실행 파일이 x86-64 이므로 플랫폼을 고정한다(Apple Silicon 에서는 에뮬레이션이라 느리다).
docker run --rm --init --platform linux/amd64 "${mounts[@]}" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends golang-go ca-certificates curl python3 >/dev/null
rm -rf /work && mkdir /work && cp -a /src/daemon /src/opereta.conf /work/ && cd /work/daemon
export GOPROXY=off GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-mod=vendor GOCACHE=/tmp/gocache
go build -o /bin/gwharness ./internal/gateway/harness
mkdir /v /t && cp /src/tests/vedecaps/fake_llama.py /t/ && cp /src/tests/vedecaps/run.sh /t/
# 패키지와 같은 고정 값과 같은 확인 절차를 쓴다.
sh /src/packages/opereta-vedecaps/debian/fetch.sh /src/opereta.conf /v
# opereta.conf 의 VEDECAPS_RECOGNISES_OPERETA: Vedecaps 가 새 OS 이름을 아는 첫 버전. 비어 있으면 그 항목은 알려진 한계로 본다.
VEDECAPS_RECOGNISES_OPERETA="$(sed -n "s/^VEDECAPS_RECOGNISES_OPERETA=\"\(.*\)\"/\1/p" /src/opereta.conf)"
export VEDECAPS_RECOGNISES_OPERETA
sh /t/run.sh
'
