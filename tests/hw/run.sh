#!/usr/bin/env bash
# Lightlix 실 하드웨어 체크리스트 실행기 (Phase 1).
#
# 사용법: tests/hw/run.sh
#   대상 기기에서 Lightlix 로 부팅한 뒤 실행한다. 결과는 RESULT_TEMPLATE.md 에 옮겨 적는다.
#
# Phase 1 의 드라이버 항목을 검증한다. LLM 및 GUI 항목은 후속 단계에서 확인한다.
#   - os-release 의 ID=lightlix
#   - 커널 버전 (기록용)
#   - UEFI 부팅 여부
#   - Secure Boot 상태 (mokutil 이 있을 때, 기록용)
#   - NVIDIA 드라이버 로드 흔적 (/proc/driver/nvidia/version 이 있을 때)
#   - nvidia-smi 가 있을 때의 compute_cap (8.6 / 8.9 / 12.0 중 하나여야 함)
#   - GPU 진단 상태 /run/lightlix/gpu-status 의 LIGHTLIX_GPU_STATUS 와 USABLE_COUNT
#     (ready 면 PASS, no_gpu 면 SKIP, 그 밖이면 FAIL)
#   - ready 일 때 /dev/dri/card* 가 있는지 (없으면 FAIL)
#   - 로드된 드라이버 버전이 /usr/share/lightlix/nvidia.conf 의 고정 버전과 같은지
#     (드라이버가 로드되지 않았으면 SKIP)
#   - 무선 인터페이스(/sys/class/net/wl*)가 있으면 이름, 그때 iwd.service 가 active 인지
#     (무선 장치가 없으면 SKIP)
# 나머지는 SKIP 으로 출력한다.
#
# 출력: 항목마다 PASS / FAIL / SKIP 한 줄. FAIL 이 하나라도 있으면 1 로 끝난다.
#
# 테스트용 덮어쓰기 환경 변수
#   LIGHTLIX_OS_RELEASE          기본 /etc/os-release
#   LIGHTLIX_EFI_DIR             기본 /sys/firmware/efi
#   LIGHTLIX_NVIDIA_VERSION_FILE 기본 /proc/driver/nvidia/version
#   LIGHTLIX_NET_CLASS_DIR       기본 /sys/class/net
#   LIGHTLIX_GPU_STATUS_FILE     기본 /run/lightlix/gpu-status
#   LIGHTLIX_NVIDIA_CONF_FILE    기본 /usr/share/lightlix/nvidia.conf
#   LIGHTLIX_DRI_DIR             기본 /dev/dri

set -euo pipefail

OS_RELEASE_FILE="${LIGHTLIX_OS_RELEASE:-/etc/os-release}"
EFI_DIR="${LIGHTLIX_EFI_DIR:-/sys/firmware/efi}"
NVIDIA_VERSION_FILE="${LIGHTLIX_NVIDIA_VERSION_FILE:-/proc/driver/nvidia/version}"
NET_CLASS_DIR="${LIGHTLIX_NET_CLASS_DIR:-/sys/class/net}"
GPU_STATUS_FILE="${LIGHTLIX_GPU_STATUS_FILE:-/run/lightlix/gpu-status}"
NVIDIA_CONF_FILE="${LIGHTLIX_NVIDIA_CONF_FILE:-/usr/share/lightlix/nvidia.conf}"
DRI_DIR="${LIGHTLIX_DRI_DIR:-/dev/dri}"

# RTX 30 (Ampere), RTX 40 (Ada), RTX 50 (Blackwell) 의 compute capability.
readonly SUPPORTED_COMPUTE_CAPS='8.6 8.9 12.0'

FAIL_COUNT=0

# emit <PASS|FAIL|SKIP> <item> <detail>
emit() {
  local status="$1" item="$2" detail="${3:-}"
  printf '%-4s %-22s %s\n' "$status" "$item" "$detail"
  if [[ "$status" == "FAIL" ]]; then
    FAIL_COUNT=$((FAIL_COUNT + 1))
  fi
}

check_os_release() {
  if [[ ! -r "$OS_RELEASE_FILE" ]]; then
    emit FAIL os-release "not readable: $OS_RELEASE_FILE"
    return 0
  fi
  # os-release 는 셸 형식이다. 서브셸에서 읽어 현재 셸 변수를 오염시키지 않는다.
  local id version
  # shellcheck source=/dev/null
  id="$(. "$OS_RELEASE_FILE" && printf '%s' "${ID:-}")"
  # shellcheck source=/dev/null
  version="$(. "$OS_RELEASE_FILE" && printf '%s' "${VERSION_ID:-}")"
  if [[ "$id" == "lightlix" ]]; then
    emit PASS os-release "ID=lightlix VERSION_ID=${version:-unset}"
  else
    emit FAIL os-release "ID=${id:-unset} (expected lightlix)"
  fi
}

check_kernel() {
  emit PASS kernel "$(uname -r)"
}

check_uefi() {
  if [[ -d "$EFI_DIR" ]]; then
    emit PASS uefi-boot "$EFI_DIR present"
  else
    emit FAIL uefi-boot "$EFI_DIR missing (Lightlix requires UEFI boot)"
  fi
}

check_secure_boot() {
  if ! command -v mokutil >/dev/null 2>&1; then
    emit SKIP secure-boot "mokutil not installed"
    return 0
  fi
  local sb
  sb="$(mokutil --sb-state 2>&1 || true)"
  case "$sb" in
    *"SecureBoot enabled"*)  emit PASS secure-boot "enabled (recorded, not a pass/fail criterion)" ;;
    *"SecureBoot disabled"*) emit PASS secure-boot "disabled (recorded, not a pass/fail criterion)" ;;
    *)                       emit SKIP secure-boot "unrecognized mokutil output: $(printf '%s' "$sb" | head -n 1)" ;;
  esac
}

check_nvidia_driver_file() {
  if [[ -r "$NVIDIA_VERSION_FILE" ]]; then
    emit PASS nvidia-driver-file "$(head -n 1 "$NVIDIA_VERSION_FILE")"
    if grep -q 'Open Kernel Module' "$NVIDIA_VERSION_FILE"; then
      emit PASS open-kernel-module 'Open Kernel Module'
    else
      emit FAIL open-kernel-module '오픈 모듈이 아니다'
    fi
  else
    emit FAIL nvidia-driver-file 'NVIDIA 드라이버가 로드되지 않았다'
    emit FAIL open-kernel-module '모듈 버전 파일이 없다'
  fi
}

check_nvidia_smi_and_compute_cap() {
  if ! command -v nvidia-smi >/dev/null 2>&1; then
    emit FAIL nvidia-smi 'nvidia-smi 가 없다'
    emit FAIL compute-cap 'nvidia-smi 가 없다'
    return 0
  fi
  local caps line cc bad=0 count=0 summary=''
  if ! caps="$(nvidia-smi --query-gpu=compute_cap --format=csv,noheader,nounits 2>&1)"; then
    emit FAIL nvidia-smi 'GPU 조회 실패'
    emit FAIL compute-cap "nvidia-smi query failed: $(printf '%s' "$caps" | head -n 1)"
    return 0
  fi
  emit PASS nvidia-smi 'GPU 조회 성공'
  while IFS= read -r line; do
    cc="${line//[[:space:]]/}"
    [[ -n "$cc" ]] || continue
    count=$((count + 1))
    case " $SUPPORTED_COMPUTE_CAPS " in
      *" $cc "*) summary="${summary}${summary:+, }${cc}" ;;
      *)         summary="${summary}${summary:+, }${cc}(unexpected)"; bad=$((bad + 1)) ;;
    esac
  done <<<"$caps"

  if (( count == 0 )); then
    emit FAIL compute-cap "nvidia-smi reported no GPU"
  elif (( bad > 0 )); then
    emit FAIL compute-cap "$summary (expected one of: $SUPPORTED_COMPUTE_CAPS)"
  else
    emit PASS compute-cap "$summary"
  fi
}

# gpu-status 를 source 해 키 값을 꺼낸다. 파일이 없거나 키가 없으면 빈 값이다.
gpu_status_value() {  # gpu_status_value KEY
  [[ -r "$GPU_STATUS_FILE" ]] || return 0
  # shellcheck source=/dev/null
  ( . "$GPU_STATUS_FILE" && printf '%s' "${!1:-}" ) || true
}

check_gpu_status() {
  if [[ ! -r "$GPU_STATUS_FILE" ]]; then
    emit SKIP gpu-status "$GPU_STATUS_FILE not found (gpu-check did not record a state)"
    emit SKIP gpu-usable 'gpu-status not found'
    return 0
  fi
  local state usable message
  state="$(gpu_status_value LIGHTLIX_GPU_STATUS)"
  usable="$(gpu_status_value LIGHTLIX_GPU_USABLE_COUNT)"
  message="$(gpu_status_value LIGHTLIX_GPU_MESSAGE)"
  case "$state" in
    ready)  emit PASS gpu-status "ready (usable GPUs: ${usable:-0})" ;;
    no_gpu) emit SKIP gpu-status "no_gpu: $message" ;;
    *)      emit FAIL gpu-status "${state:-unknown}: $message" ;;
  esac
  if [[ "$state" != ready ]]; then
    emit SKIP gpu-usable 'gpu-status is not ready'
  elif (( ${usable:-0} >= 1 )); then
    emit PASS gpu-usable "$usable GPU(s) usable"
  else
    emit FAIL gpu-usable 'ready but USABLE_COUNT is 0'
  fi
}

check_dri_card() {
  local state
  state="$(gpu_status_value LIGHTLIX_GPU_STATUS)"
  if [[ "$state" != ready ]]; then
    emit SKIP dri-card "gpu-status is not ready (${state:-none})"
    return 0
  fi
  if ls "$DRI_DIR"/card* >/dev/null 2>&1; then
    emit PASS dri-card "card device(s) present in $DRI_DIR"
  else
    emit FAIL dri-card "no card device in $DRI_DIR (nvidia-drm did not create a DRM device)"
  fi
}

check_driver_pin() {
  if [[ ! -r "$NVIDIA_VERSION_FILE" ]]; then
    emit SKIP driver-version-pin 'NVIDIA driver is not loaded'
    return 0
  fi
  if [[ ! -r "$NVIDIA_CONF_FILE" ]]; then
    emit FAIL driver-version-pin "not readable: $NVIDIA_CONF_FILE"
    return 0
  fi
  local pinned loaded
  # shellcheck source=/dev/null
  pinned="$(. "$NVIDIA_CONF_FILE" && printf '%s' "${NVIDIA_DRIVER_VERSION:-}")"
  loaded="$(head -n 1 "$NVIDIA_VERSION_FILE" | grep -o '[0-9][0-9]*\.[0-9][0-9]*\.[0-9][0-9]*' | head -n 1 || true)"
  if [[ -n "$pinned" && "$loaded" == "$pinned" ]]; then
    emit PASS driver-version-pin "loaded $loaded = pinned $pinned"
  else
    emit FAIL driver-version-pin "loaded '${loaded:-unknown}', pinned '${pinned:-unset}'"
  fi
}

check_wifi() {
  local iface='' dev state
  for dev in "$NET_CLASS_DIR"/wl*; do
    if [[ -e "$dev" ]]; then
      iface="${dev##*/}"
      break
    fi
  done
  if [[ -z "$iface" ]]; then
    emit SKIP wifi-interface '무선 장치 없음'
    emit SKIP iwd-service '무선 장치 없음'
    return 0
  fi
  emit PASS wifi-interface "$iface"
  if ! command -v systemctl >/dev/null 2>&1; then
    emit FAIL iwd-service 'systemctl 이 없다'
    return 0
  fi
  state="$(systemctl is-active iwd.service 2>/dev/null || true)"
  if [[ "$state" == "active" ]]; then
    emit PASS iwd-service 'active'
  else
    emit FAIL iwd-service "${state:-unknown} (expected active)"
  fi
}

# Phase 1 범위 밖 항목. SKIP 사유는 "Phase N 에서 구현" 형태로 적는다.
skip_later() {
  emit SKIP "$1" "$2"
}

check_os_release
check_kernel
check_uefi
check_secure_boot
check_nvidia_driver_file
check_nvidia_smi_and_compute_cap
check_gpu_status
check_dri_card
check_driver_pin
check_wifi
skip_later model-load 'Phase 2 에서 구현'
skip_later gpu-offload 'Phase 2 에서 구현'
skip_later tokens-per-sec 'Phase 2 에서 구현'

if (( FAIL_COUNT > 0 )); then
  echo "RESULT: FAIL ($FAIL_COUNT check(s) failed)"
  exit 1
fi
echo "RESULT: OK (no failures; SKIP items are not yet implemented)"
