#!/usr/bin/env bash
# 데스크톱 설정 파일을 실제 프로그램의 검증 기능으로 확인한다(yambar -C). 화면이 없어서 xvfb 를 쓴다.
# Mac 호스트에는 아무것도 설치하지 않는다. 저장소는 읽기 전용으로 마운트한다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
IMAGE="${LIGHTLIX_DESKTOP_TEST_IMAGE:-debian:trixie}"
command -v docker >/dev/null 2>&1 || { echo "test-desktop-config: docker is required" >&2; exit 2; }

docker run --rm --init -v "${ROOT}:/src:ro" -e DEBIAN_FRONTEND=noninteractive "${IMAGE}" bash -euc '
apt-get update -qq >/dev/null
apt-get install -y -qq --no-install-recommends yambar fonts-nanum xvfb xauth >/dev/null
# 잘못된 설정은 실패해야 검증이 의미가 있다.
printf "bar:\n  height: 26\n  location: top\n  background: 000000ff\n  left:\n    - nonsense:\n        content:\n          string: {text: x}\n" > /tmp/bad.yml
if xvfb-run -a yambar -C -c /tmp/bad.yml >/dev/null 2>&1; then echo "FAIL: yambar accepted an invalid module" >&2; exit 1; fi
xvfb-run -a yambar -C -c /src/packages/lightlix-desktop/src/yambar.yml
echo "ok: yambar configuration"
# 세션 래퍼와 패널 스크립트는 POSIX sh 문법이어야 한다.
for f in /src/packages/lightlix-desktop/src/lightlix-session /src/packages/lightlix-desktop/src/labwc-autostart.sh /src/packages/lightlix-desktop/src/lightlix-panel-net; do
  dash -n "$f" 2>/dev/null || sh -n "$f"
  echo "ok: syntax $(basename "$f")"
done
# 패널 네트워크 스크립트: lightlix 명령이 없으면 Offline 한 블록을 내야 한다(10초 대기 전에 한 블록만 읽는다).
out="$(timeout 3 sh /src/packages/lightlix-desktop/src/lightlix-panel-net | head -n 2)"
[ "$out" = "$(printf "net|string|Offline\n")" ] || { echo "FAIL: panel script output: $out" >&2; exit 1; }
echo "ok: panel script"
'
