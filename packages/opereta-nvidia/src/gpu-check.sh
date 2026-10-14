#!/usr/bin/env bash
# GPU 와 드라이버 상태를 진단해 /run/opereta/gpu-status 에 기록한다.
# 같은 내용을 화면(stdout, 콘솔)과 로그인 프롬프트 위(/run/issue.d), 로그인 뒤(/run/motd.d)에 쓴다.
#
# 종료 코드 (docs/drivers.md 의 "GPU 진단 계약"):
#   0  쓸 수 있는 GPU 가 하나 이상 있다 (ready)
#   1  NVIDIA GPU 가 없다 (no_gpu). CPU 폴백은 정상이다.
#   2  NVIDIA GPU 는 있지만 쓸 수 없다
#   3  스크립트 자체의 오류 (internal_error, config_missing)
#
# 화면과 상태 파일에 나가는 문구는 리눅스 텍스트 콘솔에서 읽히도록 ASCII 영어로 쓴다.
# 주석만 한국어로 쓴다. bash 3.2 에서도 돌도록 연관 배열과 빈 배열 확장을 쓰지 않는다.
set -eEuo pipefail
export LC_ALL=C
umask 022

CONF="${OPERETA_NVIDIA_CONF:-/usr/share/opereta/nvidia.conf}"
SYSFS="${OPERETA_SYSFS:-/sys}"
VERSION_FILE="${OPERETA_NVIDIA_VERSION_FILE:-/proc/driver/nvidia/version}"
RUNTIME="${OPERETA_RUNTIME_DIR:-/run/opereta}"
ISSUE_DIR="${OPERETA_ISSUE_DIR:-/run/issue.d}"
MOTD_DIR="${OPERETA_MOTD_DIR:-/run/motd.d}"
MOKUTIL="${OPERETA_MOKUTIL:-mokutil}"
NVIDIA_SMI="${OPERETA_NVIDIA_SMI:-nvidia-smi}"

# 진단 중 쌓이는 값. report 가 상태 파일과 화면 출력을 만든다.
STATE=internal_error
MESSAGE=""
WRITTEN=0
DETAIL=""
TEXT=""
GPU_PRESENT=0
PCI_COUNT=0
GPU_COUNT=0
USABLE_COUNT=0
USABLE_INDEXES=""
IGNORED=""
SECURE_BOOT=unknown
DRIVER_VERSION=""
GPU_KEYS=""
NVIDIA_DRIVER_VERSION=""

# 값이 셸 source 와 화면 출력을 깨지 않도록 출력 가능한 ASCII 만 남기고 " $ ` \ 를 지운다.
# 지울 문자는 8진수로 쓴다: " 042, $ 044, ` 140, \ 134.
clean() {
    printf '%s' "$1" | tr -cd '\40-\176' | tr -d '\042\044\140\134'
}

trim() {
    printf '%s' "$1" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//'
}

# 화면 상세 줄을 덧붙인다. 파이프 안에서 부르면 값이 사라지므로 리다이렉션으로만 읽는다.
add_line() {
    DETAIL="${DETAIL}$(clean "$1")"$'\n'
}

# 파일의 앞 다섯 줄을 화면 상세에 덧붙인다. 원인 확인용 오류문을 보여 준다.
add_file_head() {
    local file="$1" line="" count=0
    [[ -r "$file" ]] || return 0
    while IFS= read -r line || [[ -n "$line" ]]; do
        add_line "  $line"
        count=$((count + 1))
        if (( count >= 5 )); then
            break
        fi
    done < "$file"
    return 0
}

# Secure Boot 가 켜져 있었다면 모듈 실패 문구에 그 사실을 덧붙인다.
module_message() {
    if [[ "$SECURE_BOOT" == enabled ]]; then
        printf '%s Secure Boot is enabled.' "$1"
    else
        printf '%s' "$1"
    fi
}

# GPU 한 장의 판정 결과를 상태 파일 키로 쌓는다. add_gpu INDEX NAME CAP SUPPORTED REASON
add_gpu() {
    GPU_KEYS="${GPU_KEYS}OPERETA_GPU_${1}_NAME=\"$(clean "$2")\""$'\n'
    GPU_KEYS="${GPU_KEYS}OPERETA_GPU_${1}_COMPUTE_CAP=$(clean "$3")"$'\n'
    GPU_KEYS="${GPU_KEYS}OPERETA_GPU_${1}_SUPPORTED=$4"$'\n'
    GPU_KEYS="${GPU_KEYS}OPERETA_GPU_${1}_REASON=$5"$'\n'
}

# 상태 파일을 임시 파일에 쓴 뒤 mv 로 바꿔 끼운다. 셸에서 source 할 수 있는 KEY=VALUE 형식이다.
write_status() {
    local tmp="$RUNTIME/gpu-status.new"
    {
        printf 'OPERETA_GPU_STATUS=%s\n' "$STATE"
        printf 'OPERETA_GPU_PRESENT=%s\n' "$GPU_PRESENT"
        printf 'OPERETA_GPU_PCI_COUNT=%s\n' "$PCI_COUNT"
        printf 'OPERETA_GPU_COUNT=%s\n' "$GPU_COUNT"
        printf 'OPERETA_GPU_USABLE_COUNT=%s\n' "$USABLE_COUNT"
        printf 'OPERETA_GPU_USABLE_INDEXES="%s"\n' "$USABLE_INDEXES"
        printf 'OPERETA_SECURE_BOOT=%s\n' "$SECURE_BOOT"
        printf 'OPERETA_DRIVER_VERSION=%s\n' "$DRIVER_VERSION"
        printf 'OPERETA_GPU_MESSAGE="%s"\n' "$(clean "$MESSAGE")"
        printf '%s' "$GPU_KEYS"
    } > "$tmp" && chmod 644 "$tmp" && mv -f "$tmp" "$RUNTIME/gpu-status"
}

# 로그인 프롬프트와 로그인 뒤 안내 파일에 같은 내용을 쓴다. write_text DIR NAME
write_text() {
    local tmp="$RUNTIME/.$2.tmp"
    mkdir -p "$1" && printf '%s' "$TEXT" > "$tmp" && chmod 644 "$tmp" && mv -f "$tmp" "$1/$2"
}

# report STATE MESSAGE RC: 상태를 기록하고 화면과 안내 파일에 쓴 뒤 종료한다.
report() {
    local state="$1" message="$2" rc="$3"
    set +e
    WRITTEN=1
    STATE="$state"
    MESSAGE="$message"
    TEXT="Opereta GPU [$state]: $(clean "$message")"$'\n'"$DETAIL"
    if [[ "$rc" == 1 || "$rc" == 2 ]]; then
        TEXT="${TEXT}Opereta: running in CPU fallback mode."$'\n'
    fi
    if ! write_status; then
        echo 'opereta-gpu-check: cannot write /run/opereta/gpu-status' >&2
        rc=3
    fi
    if ! write_text "$ISSUE_DIR" 50-opereta-gpu.issue || ! write_text "$MOTD_DIR" 50-opereta-gpu; then
        echo 'opereta-gpu-check: cannot write the login notice files' >&2
        rc=3
    fi
    printf '%s' "$TEXT"
    exit "$rc"
}

# ERR 트랩: 예상하지 못한 명령 실패를 internal_error 로 기록하고 3 으로 끝낸다.
# shellcheck disable=SC2317,SC2329
on_error() {
    if (( WRITTEN )); then
        exit 3
    fi
    report internal_error "internal error at line $1. See journalctl -u opereta-gpu-check.service." 3
}

# EXIT 트랩: 상태를 쓰지 못하고 끝나는 경우를 막는 안전망이다.
# shellcheck disable=SC2317,SC2329
on_exit() {
    local rc=$?
    if (( WRITTEN == 0 )); then
        report internal_error "stopped unexpectedly with exit status $rc." 3
    fi
}

trap 'on_error $LINENO' ERR
trap on_exit EXIT

mkdir -p "$RUNTIME"

# 1. Secure Boot 상태. mokutil 이 없거나 해석할 수 없으면 unknown 이다.
if command -v "$MOKUTIL" >/dev/null 2>&1; then
    sb="$("$MOKUTIL" --sb-state 2>&1 || true)"
    case "$sb" in
        *'SecureBoot enabled'*) SECURE_BOOT=enabled ;;
        *'SecureBoot disabled'*) SECURE_BOOT=disabled ;;
    esac
fi
add_line "Opereta Secure Boot: $SECURE_BOOT"

# 2. PCI 디스플레이 장치(class 0x03xx)만 센다. NVIDIA 오디오와 USB 장치는 GPU 로 세지 않는다.
for device in "$SYSFS"/bus/pci/devices/*; do
    [[ -r "$device/vendor" && -r "$device/class" ]] || continue
    [[ "$(cat "$device/vendor")" == 0x10de ]] || continue
    case "$(cat "$device/class")" in
        0x03*) PCI_COUNT=$((PCI_COUNT + 1)) ;;
    esac
done
if (( PCI_COUNT > 0 )); then
    GPU_PRESENT=1
fi

# 3. 고정 드라이버 버전. 읽을 수 없으면 버전 검사를 건너뛰지 않고 config_missing 으로 끝낸다.
if [[ ! -r "$CONF" ]]; then
    report config_missing "Cannot read the NVIDIA config file: $CONF" 3
fi
# shellcheck source=/dev/null
. "$CONF"
if [[ -z "${NVIDIA_DRIVER_VERSION:-}" ]]; then
    report config_missing "NVIDIA_DRIVER_VERSION is not set in $CONF" 3
fi

if (( PCI_COUNT == 0 )); then
    report no_gpu 'No NVIDIA GPU was found on the PCI bus.' 1
fi

# 4. 오픈 커널 모듈 로드. 서명 거부 문구가 있고 Secure Boot 가 켜져 있을 때만 secure_boot 이다.
if [[ ! -r "$VERSION_FILE" ]]; then
    load_log="$RUNTIME/module-load.log"
    if ! timeout -k 5 30 modprobe nvidia > "$load_log" 2>&1; then
        add_file_head "$load_log"
        if [[ "$SECURE_BOOT" == enabled ]] && grep -Fq 'Key was rejected by service' "$load_log"; then
            add_line 'To use the NVIDIA GPU: 1) restart and open the UEFI/BIOS setup,'
            add_line '2) turn off Secure Boot, 3) save and boot Opereta again.'
            report secure_boot 'Secure Boot is on and blocked the NVIDIA kernel module. Turn off Secure Boot in UEFI setup and boot again.' 2
        fi
        report module_unloaded "$(module_message 'NVIDIA kernel module failed to load. See journalctl -k.')" 2
    fi
fi
[[ -r "$VERSION_FILE" ]] || report module_unloaded "$(module_message 'NVIDIA module version file is missing.')" 2

if ! grep -q 'Open Kernel Module' "$VERSION_FILE"; then
    report proprietary_module 'The loaded NVIDIA module is not the open kernel module. Proprietary modules are not supported.' 2
fi
DRIVER_VERSION="$(head -n 1 "$VERSION_FILE" | grep -o '[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*' | head -n 1 || true)"
if ! grep -Fq "$NVIDIA_DRIVER_VERSION" "$VERSION_FILE"; then
    report version_mismatch "Loaded NVIDIA module does not match the pinned driver $NVIDIA_DRIVER_VERSION. Restore the matching version and reboot." 2
fi

# 5. nvidia-smi 로 GPU 별 판정. 표준 출력과 오류 출력을 함께 받는다. nvidia-smi 는 오류문을 stdout 에 쓴다.
if ! command -v "$NVIDIA_SMI" >/dev/null 2>&1; then
    report driver_missing 'nvidia-smi was not found. Check the NVIDIA driver packages.' 2
fi
query="$RUNTIME/gpu-query.txt"
query_rc=0
timeout -k 5 20 "$NVIDIA_SMI" --query-gpu=index,name,compute_cap,driver_version --format=csv,noheader,nounits > "$query" 2>&1 || query_rc=$?
if (( query_rc != 0 )); then
    add_file_head "$query"
    if grep -Eqi 'driver/library version mismatch|API mismatch' "$query"; then
        report version_mismatch 'The kernel module and the user-space NVIDIA library versions differ. Restore the same version and reboot.' 2
    fi
    report query_failed "nvidia-smi GPU query failed with exit status $query_rc. See journalctl -k." 2
fi

while IFS= read -r raw || [[ -n "$raw" ]]; do
    [[ -n "$raw" ]] || continue
    IFS=, read -r idx name cap version <<< "$raw"
    idx="$(trim "$idx")"
    name="$(trim "$name")"
    cap="$(trim "$cap")"
    version="$(trim "$version")"
    if [[ ! "$idx" =~ ^[0-9]+$ || -z "$cap" || -z "$version" ]]; then
        add_line "Unexpected nvidia-smi output: $raw"
        report query_failed 'nvidia-smi returned output that could not be parsed. See journalctl -k.' 2
    fi
    if [[ "$version" != "$NVIDIA_DRIVER_VERSION" ]]; then
        report version_mismatch "nvidia-smi reports driver $version, but the image is pinned to $NVIDIA_DRIVER_VERSION." 2
    fi
    # 판정: compute_cap 을 먼저 보고, 그다음 이름이 GeForce RTX 30/40/50 인지 본다.
    reason=ok
    case "$cap" in
        8.6|8.9|12.0) ;;
        *) reason=compute_cap ;;
    esac
    if [[ "$reason" == ok && ! "$name" =~ GeForce\ RTX\ (30|40|50)[0-9][0-9] ]]; then
        reason=name
    fi
    supported=0
    if [[ "$reason" == ok ]]; then
        supported=1
    fi
    GPU_COUNT=$((GPU_COUNT + 1))
    add_gpu "$idx" "$name" "$cap" "$supported" "$reason"
    add_line "Opereta GPU $idx: $name (compute_cap $cap, driver $version, supported=$supported, reason=$reason)"
    if (( supported == 1 )); then
        USABLE_COUNT=$((USABLE_COUNT + 1))
        USABLE_INDEXES="${USABLE_INDEXES:+$USABLE_INDEXES,}$idx"
    else
        IGNORED="${IGNORED:+$IGNORED,}$idx"
    fi
done < "$query"

if (( GPU_COUNT == 0 )); then
    report query_failed 'nvidia-smi did not report any GPU.' 2
fi
if (( PCI_COUNT > GPU_COUNT )); then
    add_line "Opereta GPU: warning: PCI shows $PCI_COUNT NVIDIA display device(s), but nvidia-smi lists $GPU_COUNT."
fi
if [[ -n "$IGNORED" ]] && (( USABLE_COUNT > 0 )); then
    add_line "Opereta GPU: ignoring unsupported GPU(s) at index $IGNORED."
fi
if (( USABLE_COUNT == 0 )); then
    report unsupported_gpu 'No supported GPU. Targets are GeForce RTX 30, 40 and 50 series with compute_cap 8.6, 8.9 or 12.0.' 2
fi

# 6. 쓸 수 있는 GPU 가 있을 때만 CUDA 와 DRM 모듈을 올린다.
for module in nvidia-uvm nvidia-drm; do
    module_log="$RUNTIME/$module.log"
    if ! timeout -k 5 20 modprobe "$module" > "$module_log" 2>&1; then
        add_file_head "$module_log"
        # 계약: 쓸 수 있는 GPU 수는 ready 일 때만 0 보다 크다. 데몬은 이 값만 보고 판단할 수 있어야 한다.
        USABLE_COUNT=0
        USABLE_INDEXES=""
        report module_unloaded "$(module_message "$module failed to load. See journalctl -k.")" 2
    fi
done

report ready "NVIDIA open kernel module is loaded. $USABLE_COUNT of $GPU_COUNT GPU(s) usable." 0
