#!/usr/bin/env bash
# 이 검사는 컨테이너의 루트와 /dev를 바꾸므로 Docker 안에서만 실행한다.
set -Eeuo pipefail
trap 'echo "FAIL: integration check at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ -e /.dockerenv && "$(id -u)" == 0 ]] || { echo 'ERROR: run as root inside a disposable Docker container' >&2; exit 2; }
[[ $# == 1 && -f "$1" ]] || { echo 'usage: integration.sh <diparnux-daemon.deb>' >&2; exit 2; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
dpkg-deb -x "$1" /
systemd-sysusers /usr/lib/sysusers.d/diparnux.conf
systemd-tmpfiles --create /usr/lib/tmpfiles.d/diparnux-daemon.conf
mkdir -p /usr/lib/diparnux/llama /run/diparnux
# 별도 소스 빌드와 드라이버가 없는 arm64 검사에서는 의존 실행 파일만 스텁으로 둔다.
install -m 755 /bin/true /usr/lib/diparnux/llama/llama-server
install -m 755 /bin/true /usr/sbin/diparnux-gpu-check
install -m 644 "$ROOT/packages/diparnux-nvidia/src/diparnux-gpu-check.service" /usr/lib/systemd/system/
systemd-analyze verify /usr/lib/systemd/system/diparnux*.service /usr/lib/systemd/system/diparnuxd.socket
echo 'PASS: systemd unit validation with real daemon and mocked llama/GPU-check executables'

# 실제 nft 트랜잭션으로 잘못된 저장 파일이 기본 차단을 지우지 않는지 확인한다.
nft -c -f /usr/share/diparnux/firewall-default.nft
nft -f /usr/share/diparnux/firewall-default.nft
nft list table inet diparnux > /tmp/diparnux-firewall-before
printf 'table inet diparnux\ndelete table inet diparnux\ninvalid saved rules\n' > /tmp/diparnux-corrupt.nft
if nft -f /tmp/diparnux-corrupt.nft >/tmp/diparnux-corrupt.log 2>&1; then
    echo 'FAIL: corrupt saved rules unexpectedly succeeded' >&2
    exit 1
fi
nft list table inet diparnux > /tmp/diparnux-firewall-after
cmp /tmp/diparnux-firewall-before /tmp/diparnux-firewall-after
grep -q 'policy drop;' /tmp/diparnux-firewall-after
nft -f /usr/share/diparnux/firewall-default.nft
echo 'PASS: corrupt saved firewall transaction keeps the default drop policy'

# 권한 검사는 실제 비특권 사용자로 한다. 장치가 없는 CPU 경로와 접근 거부를 모두 확인한다.
runuser -u diparnux-llm -- /usr/lib/diparnux/check-gpu-permissions
touch /dev/nvidia0 /dev/nvidiactl /dev/nvidia-uvm
chmod 600 /dev/nvidia0 /dev/nvidiactl /dev/nvidia-uvm
if runuser -u diparnux-llm -- /usr/lib/diparnux/check-gpu-permissions >/tmp/diparnux-gpu-denied.log 2>&1; then
    echo 'FAIL: inaccessible GPU devices unexpectedly passed' >&2
    exit 1
fi
grep -q 'read/write access denied' /tmp/diparnux-gpu-denied.log
runuser -u diparnux-llm -- env LLAMA_ARG_N_GPU_LAYERS=0 /usr/lib/diparnux/check-gpu-permissions
chgrp video /dev/nvidia0 /dev/nvidiactl /dev/nvidia-uvm
chmod 660 /dev/nvidia0 /dev/nvidiactl /dev/nvidia-uvm
runuser -u diparnux-llm -- /usr/lib/diparnux/check-gpu-permissions
touch /dev/nvidia1
chmod 600 /dev/nvidia1
runuser -u diparnux-llm -- env CUDA_VISIBLE_DEVICES=0 /usr/lib/diparnux/check-gpu-permissions
if runuser -u diparnux-llm -- env CUDA_VISIBLE_DEVICES=2 /usr/lib/diparnux/check-gpu-permissions >/tmp/diparnux-gpu-selected.log 2>&1; then
    echo 'FAIL: selected missing GPU unexpectedly passed' >&2
    exit 1
fi
rm /dev/nvidia1
rm /dev/nvidia-uvm
if runuser -u diparnux-llm -- /usr/lib/diparnux/check-gpu-permissions >/tmp/diparnux-gpu-missing.log 2>&1; then
    echo 'FAIL: missing UVM device unexpectedly passed' >&2
    exit 1
fi
rm /dev/nvidia0 /dev/nvidiactl
echo 'PASS: GPU permission checks (absent, denied, CPU plan, group access, selected devices, missing UVM)'

# Debian live-config의 환경과 사용자 이름 선택을 사용해 훅을 두 번 실행한다.
useradd -m live-test
mkdir -p /var/lib/live/config
LIVE_CONFIG_CMDLINE='boot=live components username=live-test' sh "$ROOT/build/config/includes.chroot/lib/live/config/1190-diparnux-groups"
id -nG live-test | tr ' ' '\n' | grep -qx diparnux
LIVE_CONFIG_CMDLINE='boot=live components username=live-test' sh "$ROOT/build/config/includes.chroot/lib/live/config/1190-diparnux-groups"
echo 'PASS: custom live username joins diparnux group and repeated hook is safe'
