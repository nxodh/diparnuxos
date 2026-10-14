#!/usr/bin/env bash
# Deberopa 자동 갱신(opereta-update deberopa)을 실제 GitHub 릴리스로 컨테이너에서 확인한다. Mac 호스트에는 아무것도 설치하지 않는다.
#   1. 이미지의 Deberopa 가 뒤처진 상태(0.0.1 로 위장한 스크립트)에서 apply 하면 최신 릴리스를 받아 체크섬과 묶음을 확인하고
#      /usr/local/bin/deberopa 링크로 설치한다. 그 실행 파일의 --version 이 최신이다.
#   2. 한 번 더 부르면 다시 받지 않는다(up_to_date).
#   3. 자동 갱신을 끄면 --auto 는 아무것도 하지 않는다.
#   4. 이미지 패키지의 파일은 바뀌지 않는다.
# 네트워크가 필요하다. 실행 파일이 x86-64 이므로 플랫폼을 고정한다(Apple Silicon 에서는 에뮬레이션이라 느리다).
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${OPERETA_DEBEROPA_TEST_IMAGE:-debian:trixie}"
command -v docker >/dev/null 2>&1 || { echo "test-deberopa-update: docker is required" >&2; exit 2; }

docker run --rm --init --platform linux/amd64 -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends golang-go ca-certificates util-linux >/dev/null
rm -rf /work && mkdir /work && cp -a /src/daemon /src/opereta.conf /work/ && cd /work/daemon
export GOPROXY=off GOTOOLCHAIN=local CGO_ENABLED=0 GOFLAGS=-mod=vendor GOCACHE=/tmp/gocache
go build -o /usr/lib/opereta-update ./cmd/opereta-update

# 이미지의 /usr/bin/deberopa 를 뒤처진 버전으로 흉내 낸다(실제 파일 대신 스크립트).
printf "#!/bin/sh\necho 0.0.1\n" > /usr/bin/deberopa && chmod 755 /usr/bin/deberopa
before="$(sha256sum /usr/bin/deberopa)"
mkdir -p /usr/local/bin /var/lib/opereta/deberopa /usr/local/lib/opereta/deberopa
chmod 755 /var /usr/local/lib/opereta /usr/local/lib/opereta/deberopa
export OPERETA_DEBEROPA_DIR=/var/lib/opereta/deberopa OPERETA_DEBEROPA_STORE=/usr/local/lib/opereta/deberopa

echo "== check"
out="$(/usr/lib/opereta-update deberopa check --json)"; echo "$out"
echo "$out" | grep -q "\"status\":\"update_available\"" || { echo "FAIL: expected update_available"; exit 1; }
[ ! -e /usr/local/bin/deberopa ] || { echo "FAIL: check installed something"; exit 1; }

echo "== apply"
out="$(/usr/lib/opereta-update deberopa apply --auto --json)"; echo "$out"
echo "$out" | grep -q "\"status\":\"updated\"" || { echo "FAIL: expected updated"; exit 1; }
latest="$(echo "$out" | sed -n "s/.*\"latest\":\"\([^\"]*\)\".*/\1/p")"
[ -n "$latest" ] || { echo "FAIL: no latest version in output"; exit 1; }
got="$(/usr/local/bin/deberopa --version)"
[ "$got" = "$latest" ] || { echo "FAIL: /usr/local/bin/deberopa reports $got, expected $latest"; exit 1; }
test -L /usr/local/bin/deberopa
# 일반 사용자도 실행할 수 있다(PATH 에서 이 링크가 이미지의 파일보다 앞선다).
useradd -m tester
su tester -c "PATH=/usr/local/bin:/usr/bin command -v deberopa | grep -qx /usr/local/bin/deberopa"
[ "$(su tester -c "deberopa --version")" = "$latest" ] || { echo "FAIL: a normal user cannot run the downloaded deberopa"; exit 1; }
[ "$(sha256sum /usr/bin/deberopa)" = "$before" ] || { echo "FAIL: the image file was changed"; exit 1; }
echo "ok: updated to $latest; image file untouched"

echo "== second run does nothing"
out="$(/usr/lib/opereta-update deberopa apply --auto --json)"
echo "$out" | grep -q "\"status\":\"up_to_date\"" || { echo "FAIL: expected up_to_date"; echo "$out"; exit 1; }

echo "== auto off"
/usr/lib/opereta-update deberopa auto off --json | grep -q "\"auto\":false"
out="$(/usr/lib/opereta-update deberopa apply --auto --json)"
echo "$out" | grep -q "\"status\":\"disabled\"" || { echo "FAIL: expected disabled"; echo "$out"; exit 1; }

echo "== network failure is not fatal with --auto"
/usr/lib/opereta-update deberopa auto on --json >/dev/null
rm -f /usr/local/bin/deberopa
echo "127.0.0.1 api.github.com" >> /etc/hosts
/usr/lib/opereta-update deberopa apply --auto --json >/tmp/net.json; rc=$?
cat /tmp/net.json
[ "$rc" = 0 ] || { echo "FAIL: --auto exited $rc on a network failure"; exit 1; }
grep -q "\"status\":\"error\"" /tmp/net.json || { echo "FAIL: expected an error status"; exit 1; }
echo "PASS: Deberopa auto-update"
'
