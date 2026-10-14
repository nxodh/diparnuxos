#!/usr/bin/env bash
# gpu-check.sh 의 진단 분기를 명령 스텁으로 확인한다. 실제 드라이버는 쓰지 않는다. 실기 검증이 아니다.
# - 입력: 실제 gen-config.sh 가 만든 nvidia.conf 와 실제 gpu-check.sh
# - 스텁: timeout(-k 포함), mokutil, modprobe, nvidia-smi. nvidia-smi 는 오류문을 stdout 에 쓰고 rc=18 로 끝난다.
# - 확인: 종료 코드, 상태 파일 키(source 가능, 모드 0644), /run/issue.d 와 motd 의 첫 줄과 내용,
#         CPU 폴백 줄, 화면과 안내 파일에 ASCII 가 아닌 바이트가 없는지, 안내 파일에 백슬래시가 없는지.
# bash 3.2(macOS)에서도 돌도록 연관 배열을 쓰지 않는다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
GPU_CHECK="$ROOT/packages/opereta-nvidia/src/gpu-check.sh"
GEN_CONFIG="$ROOT/packages/opereta-nvidia/debian/gen-config.sh"
# shellcheck source=/dev/null
. "$ROOT/opereta.conf"

WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
CASE=""

# 실제 설정 생성기로 nvidia.conf 를 만든다. 패키지가 설치될 때와 같은 파일이다.
sh "$GEN_CONFIG" "$ROOT/opereta.conf" "$WORK/pkg"
CONF="$WORK/pkg/usr/share/opereta/nvidia.conf"
VERSION="$WORK/version"
STATUS="$WORK/run/gpu-status"
ISSUE="$WORK/issue.d/50-opereta-gpu.issue"
MOTD="$WORK/motd.d/50-opereta-gpu"
BACKSLASH="$(printf '\134')"
REQUIRED_KEYS="OPERETA_GPU_STATUS OPERETA_GPU_PRESENT OPERETA_GPU_PCI_COUNT OPERETA_GPU_COUNT OPERETA_GPU_USABLE_COUNT OPERETA_GPU_USABLE_INDEXES OPERETA_SECURE_BOOT OPERETA_DRIVER_VERSION OPERETA_GPU_MESSAGE"

mkdir -p "$WORK/bin" "$WORK/sys" "$WORK/run"
cat > "$WORK/bin/timeout" <<'EOF'
#!/bin/sh
# timeout -k KILL_AFTER DURATION CMD... 형태만 처리한다. 시간 제한은 걸지 않는다.
while [ "$1" = -k ]; do shift 2; done
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
if [ "$1" = nvidia-uvm ] && [ "${TEST_UVM_FAIL:-0}" = 1 ]; then
    echo "modprobe: ERROR: could not insert 'nvidia_uvm': Invalid argument" >&2
    exit 1
fi
if [ "$1" = nvidia-drm ] && [ "${TEST_DRM_FAIL:-0}" = 1 ]; then
    echo "modprobe: ERROR: could not insert 'nvidia_drm': Invalid argument" >&2
    exit 1
fi
if [ "$1" = nvidia ]; then
    if [ "${TEST_MODPROBE_FAIL:-0}" = 1 ]; then
        printf '%s\n' "$TEST_MODPROBE_MSG" >&2
        exit 1
    fi
    # 모듈이 올라오면 커널이 버전 파일을 만든다고 흉내 낸다.
    printf '%s\n' "$TEST_LOADED_LINE" > "$TEST_VERSION_FILE"
fi
exit 0
EOF
cat > "$WORK/bin/nvidia-smi" <<'EOF'
#!/bin/sh
echo 'nvidia-smi' >> "$TEST_TRACE"
# 실제 nvidia-smi 처럼 오류문을 stdout 에 쓰고 rc=18 로 끝난다.
if [ -n "${TEST_SMI_ERROR:-}" ]; then
    printf '%s\n' "$TEST_SMI_ERROR"
    exit 18
fi
printf '%s\n' "$TEST_GPU_ROWS"
EOF
chmod +x "$WORK/bin/"*

export PATH="$WORK/bin:$PATH"
export TEST_TRACE="$WORK/trace"
export TEST_VERSION_FILE="$VERSION"
export OPERETA_SYSFS="$WORK/sys"
export OPERETA_NVIDIA_VERSION_FILE="$VERSION"
export OPERETA_RUNTIME_DIR="$WORK/run"
export OPERETA_ISSUE_DIR="$WORK/issue.d"
export OPERETA_MOTD_DIR="$WORK/motd.d"

die() {
    echo "FAIL: ${CASE:-setup}: $*" >&2
    exit 1
}

add_pci() {  # add_pci ADDRESS VENDOR CLASS
    mkdir -p "$WORK/sys/bus/pci/devices/$1"
    printf '%s\n' "$2" > "$WORK/sys/bus/pci/devices/$1/vendor"
    printf '%s\n' "$3" > "$WORK/sys/bus/pci/devices/$1/class"
}

# 모든 케이스의 기본값. 케이스마다 필요한 것만 바꾼다.
reset_case() {
    rm -rf "$WORK/sys/bus/pci/devices" "$WORK/run" "$WORK/issue.d" "$WORK/motd.d" "$WORK/none"
    mkdir -p "$WORK/sys/bus/pci/devices" "$WORK/run"
    printf 'NVIDIA UNIX Open Kernel Module %s\n' "$NVIDIA_DRIVER_VERSION" > "$VERSION"
    : > "$TEST_TRACE"
    add_pci 0000:01:00.0 0x10de 0x030000
    export TEST_SECURE_BOOT=disabled
    export TEST_MODPROBE_FAIL=0
    export TEST_MODPROBE_MSG="modprobe: ERROR: could not insert 'nvidia': Input/output error"
    export TEST_LOADED_LINE="NVIDIA UNIX Open Kernel Module $NVIDIA_DRIVER_VERSION"
    export TEST_UVM_FAIL=0
    export TEST_DRM_FAIL=0
    export TEST_SMI_ERROR=""
    export TEST_GPU_ROWS="0, NVIDIA GeForce RTX 3060, 8.6, $NVIDIA_DRIVER_VERSION"
    export OPERETA_MOKUTIL="$WORK/bin/mokutil"
    export OPERETA_NVIDIA_SMI="$WORK/bin/nvidia-smi"
    export OPERETA_NVIDIA_CONF="$CONF"
}

status_value() {  # status_value KEY: 상태 파일에서 값을 꺼낸다. 따옴표는 뗀다.
    sed -n "s/^$1=//p" "$STATUS" | tr -d '"' | head -n 1
}

expect_value() {  # expect_value KEY VALUE
    local got
    got="$(status_value "$1")"
    if [[ "$got" != "$2" ]]; then
        die "$1=$got, expected $2"
    fi
}

expect_output() {  # expect_output TEXT: 화면 출력에 문자열이 있어야 한다.
    if ! grep -qF -- "$1" "$WORK/output"; then
        cat "$WORK/output" >&2
        die "output does not contain: $1"
    fi
}

expect_trace() {  # expect_trace LINE: 드라이버 명령 기록에 줄이 있어야 한다.
    grep -qx -- "$1" "$TEST_TRACE" || die "trace does not contain: $1"
}

# run_case NAME STATE RC: gpu-check 를 실행하고 종료 코드, 상태 파일, 안내 파일을 확인한다.
run_case() {
    local name="$1" state="$2" want_rc="$3" rc=0 first="" line="" f
    CASE="$name"
    rm -f "$STATUS" "$ISSUE" "$MOTD"
    bash "$GPU_CHECK" > "$WORK/output" 2>&1 || rc=$?
    if [[ "$rc" != "$want_rc" ]]; then
        cat "$WORK/output" >&2
        die "exit $rc, expected $want_rc"
    fi
    [[ -f "$STATUS" ]] || die "gpu-status was not written"
    for key in $REQUIRED_KEYS; do
        grep -q "^$key=" "$STATUS" || die "$key is missing from gpu-status"
    done
    expect_value OPERETA_GPU_STATUS "$state"
    # shellcheck source=/dev/null
    ( . "$STATUS" ) || die "gpu-status cannot be sourced by sh"
    if [[ -z "$(find "$STATUS" -perm 644)" ]]; then
        die "gpu-status mode is not 0644"
    fi

    first="$(sed -n 1p "$WORK/output")"
    if [[ "$first" != "Opereta GPU [$state]: "* ]]; then
        die "first output line is '$first'"
    fi
    [[ -f "$ISSUE" && -f "$MOTD" ]] || die "issue or motd file was not written"
    first="$(sed -n 1p "$ISSUE")"
    if [[ "$first" != "Opereta GPU [$state]: "* ]]; then
        die "issue first line is '$first'"
    fi
    cmp -s "$ISSUE" "$MOTD" || die "issue and motd files differ"
    if grep -qF -- "$BACKSLASH" "$ISSUE" "$MOTD"; then
        die "issue or motd file contains a backslash (agetty escape)"
    fi
    if [[ "$rc" == 1 || "$rc" == 2 ]]; then
        grep -qx 'Opereta: running in CPU fallback mode.' "$ISSUE" || die "CPU fallback line missing in issue"
        grep -qx 'Opereta: running in CPU fallback mode.' "$WORK/output" || die "CPU fallback line missing in output"
    elif grep -q 'CPU fallback' "$WORK/output" "$ISSUE"; then
        die "CPU fallback line printed for exit $rc"
    fi
    for f in "$WORK/output" "$ISSUE" "$MOTD" "$STATUS"; do
        line="$(LC_ALL=C tr -d '\11\12\40-\176' < "$f")"
        if [[ -n "$line" ]]; then
            die "non-ASCII byte in $f"
        fi
    done
    echo "PASS: $name (exit $rc, $state)"
}

# 1. GPU 없음. NVIDIA 오디오 장치(class 0x0403)는 GPU 로 세지 않는다. 드라이버 명령은 실행하지 않는다.
reset_case
rm -rf "$WORK/sys/bus/pci/devices"
mkdir -p "$WORK/sys/bus/pci/devices"
add_pci 0000:00:1f.3 0x10de 0x040300
run_case no_gpu no_gpu 1
expect_value OPERETA_GPU_PRESENT 0
expect_value OPERETA_GPU_PCI_COUNT 0
if [[ -s "$TEST_TRACE" ]]; then
    die "driver commands ran although no GPU is present"
fi

# 2. Secure Boot 가 켜져 있고 modprobe 가 서명 거부 -> secure_boot
reset_case
rm -f "$VERSION"
export TEST_SECURE_BOOT=enabled TEST_MODPROBE_FAIL=1
export TEST_MODPROBE_MSG="modprobe: ERROR: could not insert 'nvidia': Key was rejected by service"
run_case secure_boot secure_boot 2
expect_value OPERETA_SECURE_BOOT enabled
expect_output 'Turn off Secure Boot'
expect_output 'UEFI'

# 3. Secure Boot 가 꺼져 있고 일반 로드 실패 -> module_unloaded, Secure Boot 문구 없음
reset_case
rm -f "$VERSION"
export TEST_MODPROBE_FAIL=1
run_case module_unloaded_sb_off module_unloaded 2
expect_value OPERETA_SECURE_BOOT disabled
if grep -q 'Secure Boot is enabled' "$ISSUE"; then
    die "Secure Boot note added although Secure Boot is disabled"
fi

# 4. Secure Boot 가 켜져 있지만 서명 거부가 아닌 오류 -> module_unloaded, Secure Boot 사실을 덧붙인다
reset_case
rm -f "$VERSION"
export TEST_SECURE_BOOT=enabled TEST_MODPROBE_FAIL=1
run_case module_unloaded_sb_on module_unloaded 2
expect_output 'Secure Boot is enabled'

# 5. mokutil 없음 -> Secure Boot unknown. 서명 거부 문구여도 secure_boot 이 아니다.
reset_case
rm -f "$VERSION"
export OPERETA_MOKUTIL="$WORK/none/mokutil" TEST_MODPROBE_FAIL=1
export TEST_MODPROBE_MSG="modprobe: ERROR: could not insert 'nvidia': Key was rejected by service"
run_case no_mokutil module_unloaded 2
expect_value OPERETA_SECURE_BOOT unknown

# 6. 독점 모듈 -> proprietary_module
reset_case
printf 'NVIDIA UNIX Kernel Module %s\n' "$NVIDIA_DRIVER_VERSION" > "$VERSION"
run_case proprietary_module proprietary_module 2

# 7. 로드된 모듈 버전이 고정 버전과 다름 -> version_mismatch
reset_case
echo 'NVIDIA UNIX Open Kernel Module wrong-version' > "$VERSION"
run_case version_mismatch_module version_mismatch 2
expect_value OPERETA_DRIVER_VERSION ""

# 8. 모듈을 올려 버전 파일이 생기는 경로 -> ready. nvidia-uvm 과 nvidia-drm 도 올라간다.
reset_case
rm -f "$VERSION"
run_case module_load_ok ready 0
expect_trace 'modprobe nvidia'
expect_trace 'modprobe nvidia-uvm'
expect_trace 'modprobe nvidia-drm'

# 9. 정상 ready: RTX 3060 (compute_cap 8.6)
reset_case
run_case ready ready 0
expect_value OPERETA_GPU_USABLE_COUNT 1
expect_value OPERETA_GPU_USABLE_INDEXES 0
expect_value OPERETA_DRIVER_VERSION "$NVIDIA_DRIVER_VERSION"
expect_value OPERETA_GPU_0_NAME "NVIDIA GeForce RTX 3060"
expect_value OPERETA_GPU_0_COMPUTE_CAP 8.6
expect_value OPERETA_GPU_0_SUPPORTED 1
expect_value OPERETA_GPU_0_REASON ok

# 10. 노트북 이름과 compute_cap 8.9 -> ready
reset_case
export TEST_GPU_ROWS="0, NVIDIA GeForce RTX 4060 Laptop GPU, 8.9, $NVIDIA_DRIVER_VERSION"
run_case ready_laptop_8_9 ready 0
expect_value OPERETA_GPU_0_COMPUTE_CAP 8.9

# 11. 지원 GPU 와 미지원 GPU 가 섞임 -> ready. 미지원은 무시하고 USABLE_INDEXES 에는 지원 장치만 든다.
reset_case
add_pci 0000:02:00.0 0x10de 0x030000
add_pci 0000:03:00.0 0x10de 0x030000
export TEST_GPU_ROWS="0, NVIDIA GeForce RTX 3060, 8.6, $NVIDIA_DRIVER_VERSION
1, NVIDIA GeForce GTX 1650, 7.5, $NVIDIA_DRIVER_VERSION
2, NVIDIA GeForce RTX 5090, 12.0, $NVIDIA_DRIVER_VERSION"
run_case mixed_supported_unsupported ready 0
expect_value OPERETA_GPU_COUNT 3
expect_value OPERETA_GPU_USABLE_COUNT 2
expect_value OPERETA_GPU_USABLE_INDEXES "0,2"
expect_value OPERETA_GPU_1_SUPPORTED 0
expect_value OPERETA_GPU_1_REASON compute_cap
expect_value OPERETA_GPU_2_SUPPORTED 1
expect_output 'ignoring unsupported GPU(s) at index 1.'
expect_trace 'modprobe nvidia-uvm'

# 12. 미지원 GPU 하나뿐 -> unsupported_gpu (2). nvidia-uvm 과 nvidia-drm 은 올리지 않는다.
reset_case
export TEST_GPU_ROWS="0, NVIDIA GeForce RTX 2080, 7.5, $NVIDIA_DRIVER_VERSION"
run_case unsupported_single unsupported_gpu 2
expect_value OPERETA_GPU_0_REASON compute_cap
if grep -q 'nvidia-uvm' "$TEST_TRACE"; then
    die "nvidia-uvm was loaded without a usable GPU"
fi

# 13. 이름은 맞지 않고 compute_cap 만 맞는 GPU -> REASON=name
reset_case
export TEST_GPU_ROWS="0, NVIDIA RTX A4000, 8.6, $NVIDIA_DRIVER_VERSION"
run_case reason_name unsupported_gpu 2
expect_value OPERETA_GPU_0_REASON name

# 14. nvidia-smi 행의 드라이버 버전이 고정 버전과 다름 -> version_mismatch
reset_case
export TEST_GPU_ROWS="0, NVIDIA GeForce RTX 3060, 8.6, wrong-version"
run_case version_mismatch_smi version_mismatch 2

# 15. nvidia-smi 가 드라이버/라이브러리 불일치를 stdout 에 쓴다 -> version_mismatch, 오류문을 화면에 낸다
reset_case
export TEST_SMI_ERROR='Failed to initialize NVML: Driver/library version mismatch'
run_case smi_library_mismatch version_mismatch 2
expect_output 'Driver/library version mismatch'

# 16. nvidia-smi 일반 조회 실패 -> query_failed, 오류문을 화면에 낸다
reset_case
export TEST_SMI_ERROR='Failed to initialize NVML: Unknown Error'
run_case smi_query_failed query_failed 2
expect_output 'Unknown Error'

# 17. nvidia-smi 없음 -> driver_missing
reset_case
export OPERETA_NVIDIA_SMI="$WORK/none/nvidia-smi"
run_case driver_missing driver_missing 2

# 18. nvidia-smi 가 0행 -> query_failed
reset_case
export TEST_GPU_ROWS=""
run_case zero_rows query_failed 2

# 19. nvidia-uvm 로드 실패 -> module_unloaded
reset_case
export TEST_UVM_FAIL=1
run_case uvm_load_fail module_unloaded 2
expect_output 'nvidia-uvm failed to load'

# 20. nvidia-drm 로드 실패 -> module_unloaded
reset_case
export TEST_DRM_FAIL=1
run_case drm_load_fail module_unloaded 2
expect_output 'nvidia-drm failed to load'

# 21. PCI 장치 2개, nvidia-smi 1행 -> ready. 경고 한 줄을 낸다.
reset_case
add_pci 0000:02:00.0 0x10de 0x030200
run_case pci_more_than_rows ready 0
expect_value OPERETA_GPU_PCI_COUNT 2
expect_value OPERETA_GPU_COUNT 1
expect_output 'warning: PCI shows 2 NVIDIA display device(s), but nvidia-smi lists 1.'

# 22. nvidia.conf 없음 -> config_missing (3). 버전 검사를 건너뛰지 않고 끝낸다.
reset_case
export OPERETA_NVIDIA_CONF="$WORK/absent/nvidia.conf"
run_case config_missing config_missing 3
if [[ -s "$TEST_TRACE" ]]; then
    die "driver commands ran although nvidia.conf is missing"
fi

# 23. 예상하지 못한 명령 실패 -> internal_error (3). 설정 파일 끝의 false 가 실패를 재현한다.
reset_case
{ cat "$CONF"; echo 'false'; } > "$WORK/broken.conf"
export OPERETA_NVIDIA_CONF="$WORK/broken.conf"
run_case internal_error internal_error 3
expect_output 'internal error at line'

echo "gpu-check: 23 scenarios passed (stub-based, not a hardware test)"
