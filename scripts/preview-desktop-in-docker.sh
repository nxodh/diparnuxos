#!/usr/bin/env bash
# 데스크톱 화면을 컨테이너(Xvfb)에서 띄워 스크린샷을 만든다. Mac 호스트에는 아무것도 설치하지 않는다.
# 사용법: scripts/preview-desktop-in-docker.sh [출력 디렉터리] [테마...]   (기본: out/desktop-preview, Opereta Opereta-Dark)
# 결과: <출력 디렉터리>/desktop-<테마>.png 와 compositor-<테마>.txt(합성 관리자 시작 결과)
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
OUT="${1:-${ROOT}/out/desktop-preview}"
shift || true
THEMES=("$@")
[ "${#THEMES[@]}" -gt 0 ] || THEMES=(Opereta Opereta-Dark)
IMAGE="${OPERETA_PREVIEW_IMAGE:-opereta-desktop-preview}"

command -v docker >/dev/null 2>&1 || { echo "preview-desktop: docker is required" >&2; exit 2; }
mkdir -p "${OUT}"
docker build -q -t "${IMAGE}" "${ROOT}/tests/desktop-preview" >/dev/null
for theme in "${THEMES[@]}"; do
    docker run --rm -v "${ROOT}:/src:ro" -v "${OUT}:/out" -v "${ROOT}/tests/desktop-preview/inner.sh:/inner.sh:ro" \
        -e THEME="${theme}" "${IMAGE}" bash /inner.sh
done
echo "screenshots in ${OUT}"
