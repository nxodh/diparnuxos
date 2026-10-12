#!/usr/bin/env bash
# 추적 파일의 경로와 내용에 옛 이름이 남아 있지 않은지 검사한다(대소문자 무관). 0.5.1 부터 옛 이름의 흔적을 두지 않는다.
# 이 스크립트 자신이 걸리지 않도록 패턴을 두 조각으로 나누어 만든다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT}"
pattern="light""lix"

fail=0

# 1) 경로(파일과 폴더 이름)
paths="$(git ls-files | grep -i -- "${pattern}" || true)"
if [ -n "${paths}" ]; then
    echo "check-legacy-name: tracked paths contain the old name:" >&2
    echo "${paths}" | head -n 20 >&2
    fail=1
fi

# 2) 내용(텍스트와 바이너리 모두). 서드파티 vendor 도 포함한다. git grep 은 추적 파일만 본다.
hits="$(git grep -a -l -i -e "${pattern}" || true)"
if [ -n "${hits}" ]; then
    echo "check-legacy-name: tracked files contain the old name:" >&2
    echo "${hits}" | head -n 20 >&2
    fail=1
fi

if [ "${fail}" -ne 0 ]; then
    exit 1
fi
echo "check-legacy-name: no trace of the old name in $(git ls-files | wc -l | tr -d ' ') tracked files"
