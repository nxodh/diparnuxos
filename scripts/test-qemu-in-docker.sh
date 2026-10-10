#!/usr/bin/env bash
# Debian 컨테이너 안에 QEMU 와 OVMF 를 설치하고 tests/qemu/boot-smoke.sh 를 실행한다.
# 호스트에 QEMU 가 없거나(macOS), 호스트 배포판의 저장소를 쓰고 싶지 않을 때(CI 러너) 쓴다.
# 사용법: scripts/test-qemu-in-docker.sh [iso경로]
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

die() {
    echo "test-qemu-in-docker: $*" >&2
    exit 1
}

# shellcheck source=/dev/null
. "${ROOT}/lightlix.conf"

ISO="${1:-${ROOT}/out/lightlix-${LIGHTLIX_VERSION}-amd64.iso}"
[ -f "${ISO}" ] || die "ISO 가 없다: ${ISO} (먼저 make iso 를 실행하라)."

if ! command -v docker >/dev/null 2>&1; then
    die "docker 명령이 없다."
fi
if ! docker info >/dev/null 2>&1; then
    die "docker 데몬에 연결할 수 없다. Docker 가 실행 중인지 확인하라."
fi

iso_dir="$(cd "$(dirname "${ISO}")" && pwd)"
iso_name="$(basename "${ISO}")"

run_args=(--rm -v "${ROOT}/tests:/tests:ro" -v "${iso_dir}:/iso:ro")
# /dev/kvm 이 있으면 컨테이너에 넘겨 KVM 으로 돌린다. 없으면 boot-smoke.sh 가 TCG 로 돌린다.
if [ -e /dev/kvm ]; then
    run_args+=(--device /dev/kvm)
fi
if [ -n "${BOOT_TIMEOUT:-}" ]; then
    run_args+=(-e "BOOT_TIMEOUT=${BOOT_TIMEOUT}")
fi

docker run "${run_args[@]}" "debian:${DEBIAN_SUITE}" bash -euc '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends qemu-system-x86 ovmf >/dev/null
    exec /tests/qemu/boot-smoke.sh "/iso/$1"
' _ "${iso_name}"
