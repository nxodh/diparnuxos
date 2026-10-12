#!/usr/bin/env bash
# Debian 도구만 써서 UEFI 데스크톱 세션 E2E 를 실행한다. ISO 를 빌드하거나 호스트에 설치하지 않는다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "${ROOT}/diparnux.conf"

die() {
    echo "test-qemu-desktop-in-docker: $*" >&2
    exit 2
}

[[ $# -le 1 ]] || die "usage: $0 [iso-path]"
ISO="${1:-${ROOT}/out/diparnux-${DIPARNUX_VERSION}-amd64.iso}"
[[ -f "$ISO" ]] || die "ISO not found: $ISO"
command -v docker >/dev/null 2>&1 || die "docker is required"
docker info >/dev/null 2>&1 || die "Docker daemon is unavailable"

iso_dir="$(cd "$(dirname "$ISO")" && pwd)"
iso_name="$(basename "$ISO")"
log_dir="${E2E_LOG_DIR:-${ROOT}/out/qemu-desktop}"
mkdir -p "$log_dir"
log_dir="$(cd "$log_dir" && pwd)"
cid_file="$(mktemp "${TMPDIR:-/tmp}/diparnux-desktop-cid.XXXXXX")"
container_name="diparnux-qemu-desktop-${cid_file##*.}"
docker_pid=""
rm -f "$cid_file"

cleanup() {
    local rc=$?
    trap - EXIT INT TERM
    # CID 기록 전에 중단되어도 이번 실행의 고유 이름으로 컨테이너를 정리한다.
    docker stop --time 10 "$container_name" >/dev/null 2>&1 || true
    docker rm -f "$container_name" >/dev/null 2>&1 || true
    if [[ -n "$docker_pid" ]]; then
        kill "$docker_pid" >/dev/null 2>&1 || true
        wait "$docker_pid" 2>/dev/null || true
    fi
    rm -f "$cid_file"
    exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# ARM Mac 에서도 amd64 게스트를 TCG 로 실행한다. 호스트 네트워크나 특권 컨테이너는 필요 없다.
run_args=(--rm --init --platform linux/amd64 --name "$container_name" --cidfile "$cid_file"
    -v "${ROOT}/tests:/tests:ro" -v "${iso_dir}:/iso:ro" -v "${log_dir}:/logs"
    -e E2E_LOG_DIR=/logs)
if [[ -r /dev/kvm && -w /dev/kvm ]]; then
    run_args+=(--device /dev/kvm)
fi
for env_name in QEMU_ACCEL BOOT_TIMEOUT COMMAND_TIMEOUT START_TIMEOUT E2E_TIMEOUT; do
    if [[ -n "${!env_name:-}" ]]; then
        run_args+=(-e "${env_name}=${!env_name}")
    fi
done

echo "test-qemu-desktop-in-docker: logs=$log_dir"
docker run "${run_args[@]}" "debian:${DEBIAN_SUITE}" bash -euc '
    export DEBIAN_FRONTEND=noninteractive
    apt-get update -qq
    apt-get install -y -qq --no-install-recommends qemu-system-x86 ovmf python3 ca-certificates >/dev/null
    exec python3 /tests/qemu/desktop-session.py "/iso/$1"
' _ "$iso_name" &
docker_pid=$!
# wait 는 신호를 받으면 바로 trap 으로 진행하므로 긴 docker run 완료까지 기다리지 않는다.
wait "$docker_pid"
docker_pid=""
