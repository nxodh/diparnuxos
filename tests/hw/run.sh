#!/usr/bin/env bash
# Lightlix 실 하드웨어 체크리스트 실행기 (Phase 0 골격).
#
# 사용법: tests/hw/run.sh
#   대상 기기에서 Lightlix 로 부팅한 뒤 실행한다. 결과는 RESULT_TEMPLATE.md 에 옮겨 적는다.
#
# Phase 0 에는 NVIDIA 드라이버와 LLM 스택이 없으므로 실제로 확인 가능한 항목만 수행한다.
#   - os-release 의 ID=lightlix
#   - 커널 버전 (기록용)
#   - UEFI 부팅 여부
#   - Secure Boot 상태 (mokutil 이 있을 때, 기록용)
#   - NVIDIA 드라이버 로드 흔적 (/proc/driver/nvidia/version 이 있을 때)
#   - nvidia-smi 가 있을 때의 compute_cap (8.6 / 8.9 / 12.0 중 하나여야 함)
# 나머지는 SKIP 으로 출력한다.
#
# 출력: 항목마다 PASS / FAIL / SKIP 한 줄. FAIL 이 하나라도 있으면 1 로 끝난다.
#
# 테스트용 덮어쓰기 환경 변수
#   LIGHTLIX_OS_RELEASE          기본 /etc/os-release
#   LIGHTLIX_EFI_DIR             기본 /sys/firmware/efi
#   LIGHTLIX_NVIDIA_VERSION_FILE 기본 /proc/driver/nvidia/version

set -euo pipefail

OS_RELEASE_FILE="${LIGHTLIX_OS_RELEASE:-/etc/os-release}"
EFI_DIR="${LIGHTLIX_EFI_DIR:-/sys/firmware/efi}"
NVIDIA_VERSION_FILE="${LIGHTLIX_NVIDIA_VERSION_FILE:-/proc/driver/nvidia/version}"

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
  else
    emit SKIP nvidia-driver-file "no NVIDIA driver loaded (Phase 1 이후 구현)"
  fi
}

check_nvidia_smi_and_compute_cap() {
  if ! command -v nvidia-smi >/dev/null 2>&1; then
    emit SKIP nvidia-smi "not installed (Phase 1 이후 구현)"
    emit SKIP compute-cap "nvidia-smi not installed (Phase 1 이후 구현)"
    return 0
  fi
  emit PASS nvidia-smi "$(command -v nvidia-smi)"

  local caps line cc bad=0 count=0 summary=''
  if ! caps="$(nvidia-smi --query-gpu=compute_cap --format=csv,noheader,nounits 2>&1)"; then
    emit FAIL compute-cap "nvidia-smi query failed: $(printf '%s' "$caps" | head -n 1)"
    return 0
  fi
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

# Phase 0 범위 밖 항목. SKIP 사유는 "Phase N 에서 구현" 형태로 적는다.
skip_later() {
  emit SKIP "$1" "$2"
}

check_os_release
check_kernel
check_uefi
check_secure_boot
check_nvidia_driver_file
skip_later open-kernel-module "Phase 1 에서 구현"
check_nvidia_smi_and_compute_cap
skip_later model-load "Phase 1 이후(LLM 스택) 에서 구현"
skip_later gpu-offload "Phase 1 이후(LLM 스택) 에서 구현"
skip_later tokens-per-sec "Phase 1 이후(LLM 스택) 에서 구현"

if (( FAIL_COUNT > 0 )); then
  echo "RESULT: FAIL ($FAIL_COUNT check(s) failed)"
  exit 1
fi
echo "RESULT: OK (no failures; SKIP items are not yet implemented)"
