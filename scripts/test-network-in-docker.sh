#!/usr/bin/env bash
# 네트워크 패키지를 실제 busctl 과 가짜 iwd(D-Bus 서비스)로 시험한다. Debian 13 컨테이너 안에서만 바뀐다.
# Mac 호스트에는 아무것도 설치하지 않는다. 저장소는 읽기 전용으로 마운트한다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${DIPARNUX_NETWORK_TEST_IMAGE:-debian:trixie}"
command -v docker >/dev/null 2>&1 || { echo "test-network: docker is required" >&2; exit 2; }

docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends golang-go gcc libc6-dev ca-certificates python3-gi dbus-daemon systemd iproute2 >/dev/null
mkdir /work && cp -a /src/daemon /work/daemon
dbus-daemon --session --address=unix:path=/tmp/diparnux-test-bus --fork >/dev/null
python3 /src/tests/network/fake_iwd.py unix:path=/tmp/diparnux-test-bus > /tmp/fake-iwd.log 2>&1 &
for i in $(seq 1 50); do grep -q ready /tmp/fake-iwd.log 2>/dev/null && break; sleep 0.2; done
grep -q ready /tmp/fake-iwd.log || { cat /tmp/fake-iwd.log; exit 1; }
cd /work/daemon
export DIPARNUX_TEST_BUS=unix:path=/tmp/diparnux-test-bus
CGO_ENABLED=1 GOPROXY=off GOTOOLCHAIN=local GOFLAGS=-mod=vendor go test -buildvcs=false -count=1 -tags integration -run Live -v ./internal/network
'
