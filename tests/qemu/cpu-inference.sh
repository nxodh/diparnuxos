#!/usr/bin/env bash
# UEFI ISO 에 직렬 로그인해 실제 CPU 추론을 검사한다. 실행과 정리는 Python 드라이버가 맡는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
if [[ $# -ne 1 ]]; then
    echo "usage: $0 <iso-path>" >&2
    exit 2
fi
if ! command -v python3 >/dev/null 2>&1; then
    echo "ERROR: python3 is required" >&2
    exit 2
fi
export E2E_LOG_DIR="${E2E_LOG_DIR:-${ROOT}/out/qemu-e2e}"
# exec 로 신호를 드라이버에 직접 전달한다. QEMU 종료와 임시 OVMF 사본 제거는 finally 에서 한다.
exec python3 "${ROOT}/tests/qemu/cpu-inference.py" "$1"
