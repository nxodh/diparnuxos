#!/usr/bin/env bash
# 실제 드라이버 대신 작은 명령 스텁으로 진단 분기와 CPU 폴백 안내를 확인한다.
# GPU 없이도 버전 불일치, Secure Boot, 여러 GPU 중 미지원 장치를 검증한다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/lightlix.conf"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
mkdir -p "$WORK/bin" "$WORK/sys/bus/pci/devices"
export LIGHTLIX_SYSFS="$WORK/sys"
export LIGHTLIX_NVIDIA_VERSION_FILE="$WORK/version"
export LIGHTLIX_NVIDIA_CONF="$ROOT/lightlix.conf"
export LIGHTLIX_RUNTIME_DIR="$WORK/run"
export TEST_TRACE="$WORK/trace"
export TEST_SECURE_BOOT=disabled TEST_MODPROBE_FAIL=0 TEST_QUERY_FAIL=0
export TEST_GPU_ROWS="NVIDIA GeForce RTX 3060, 8.6, $NVIDIA_DRIVER_VERSION"
export PATH="$WORK/bin:$PATH"
cat > "$WORK/bin/timeout" <<'EOF'
#!/bin/sh
shift
exec "$@"
EOF
cat > "$WORK/bin/mokutil" <<'EOF'
#!/bin/sh
printf 'SecureBoot %s\n' "$TEST_SECURE_BOOT"
EOF
cat > "$WORK/bin/modprobe" <<'EOF'
#!/bin/sh
echo "modprobe $*" >> "$TEST_TRACE"
exit "$TEST_MODPROBE_FAIL"
EOF
cat > "$WORK/bin/nvidia-smi" <<'EOF'
#!/bin/sh
echo 'nvidia-smi' >> "$TEST_TRACE"
if [ "$TEST_QUERY_FAIL" = 1 ]; then
    echo 'Failed to initialize NVML: Driver/library version mismatch' >&2
    exit 1
fi
printf '%s\n' "$TEST_GPU_ROWS"
EOF
chmod +x "$WORK/bin/"*

check() {
    local expected="$1" expected_rc="$2" rc=0
    : > "$TEST_TRACE"
    bash "$ROOT/packages/lightlix-nvidia/src/gpu-check.sh" > "$WORK/output" 2>&1 || rc=$?
    if [[ "$rc" != "$expected_rc" ]] || ! grep -qx "LIGHTLIX_GPU_STATUS=$expected" "$WORK/run/gpu-status"; then
        cat "$WORK/output" >&2
        echo "FAIL: expected=$expected rc=$expected_rc actual_rc=$rc" >&2
        exit 1
    fi
    if [[ "$expected_rc" == 1 ]]; then
        grep -q 'CPU 폴백 대상' "$WORK/output"
    fi
    echo "PASS: $expected"
}

check no_gpu 1
[[ ! -s "$TEST_TRACE" ]] || { echo 'FAIL: GPU 가 없는데 드라이버 명령을 실행했다' >&2; exit 1; }
mkdir -p "$WORK/sys/bus/pci/devices/0000:01:00.0"
echo 0x10de > "$WORK/sys/bus/pci/devices/0000:01:00.0/vendor"
echo 0x030000 > "$WORK/sys/bus/pci/devices/0000:01:00.0/class"
TEST_SECURE_BOOT=enabled TEST_MODPROBE_FAIL=1
check secure_boot 1
TEST_SECURE_BOOT=disabled TEST_MODPROBE_FAIL=1
check module_unloaded 1
TEST_MODPROBE_FAIL=0
echo 'NVIDIA UNIX Kernel Module' > "$WORK/version"
check proprietary_module 1
echo "NVIDIA UNIX Open Kernel Module $NVIDIA_DRIVER_VERSION" > "$WORK/version"
check ready 0
TEST_GPU_ROWS="NVIDIA GeForce RTX 3060 Laptop GPU, 8.6, $NVIDIA_DRIVER_VERSION"
check ready 0
TEST_GPU_ROWS="NVIDIA GeForce RTX 3060, 8.6, $NVIDIA_DRIVER_VERSION
NVIDIA GeForce RTX 5090, 12.0, $NVIDIA_DRIVER_VERSION"
check ready 0
TEST_GPU_ROWS="NVIDIA GeForce RTX 3060, 8.6, $NVIDIA_DRIVER_VERSION
NVIDIA GeForce RTX 2080, 7.5, $NVIDIA_DRIVER_VERSION"
check unsupported_gpu 1
TEST_GPU_ROWS='NVIDIA GeForce RTX 3060, 8.6, wrong-version'
check version_mismatch 1
TEST_QUERY_FAIL=1
check version_mismatch 1
echo 'NVIDIA UNIX Open Kernel Module wrong-version' > "$WORK/version"
check version_mismatch 1
echo 'gpu-check: 모든 진단 분기 테스트 통과 (실기 검증 아님)'
