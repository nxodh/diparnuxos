#!/usr/bin/env bash
# Diparnux ISO UEFI 부팅 스모크 테스트.
#
# 사용법: tests/qemu/boot-smoke.sh <iso경로>
#
# 동작
#   QEMU + OVMF(UEFI 펌웨어) 로 ISO 를 부팅하고 직렬 콘솔 출력을 로그 파일로 받는다.
#   제한 시간 안에 "diparnux login:" 과 "Diparnux GPU [no_gpu]:" 가 직렬 로그에 나오면 성공(0).
#   "Diparnux GPU [no_gpu]:" 줄은 /run/issue.d/*.issue 를 agetty 가 로그인 프롬프트 위에 출력한 것이다.
#   그렇지 않으면 로그 끝부분을 출력하고 실패(1).
#   사용법이 잘못되었거나 ISO/펌웨어/QEMU 를 찾지 못하면 2 로 끝난다.
#
# 환경 변수
#   BOOT_MEDIA          cdrom(기본값): ISO 를 가상 광학 드라이브로 붙인다.
#                       usb: ISO 를 xHCI 컨트롤러의 usb-storage 장치로 붙인다. 실기의 USB 기록 매체와 가깝다.
#   OVMF_CODE           UEFI 코드 이미지(읽기 전용). 없으면 아래 후보 경로에서 찾는다.
#   OVMF_VARS_TEMPLATE  UEFI 변수 저장소 템플릿. 없으면 아래 후보 경로에서 찾는다.
#                       실행할 때마다 임시 디렉터리에 쓰기 가능한 사본을 만들어 쓴다.
#   BOOT_TIMEOUT        제한 시간(초). 기본값은 KVM 180, TCG 900.
#   QEMU_BIN            QEMU 실행 파일. 기본값 qemu-system-x86_64.
#
# 가정
#   - KVM: /dev/kvm 에 쓸 수 있으면 -enable-kvm 을 쓴다. 아니면 TCG(소프트웨어 에뮬레이션)로
#     돌리고 제한 시간을 늘린다.
#   - GRUB 메뉴 대기: 이 스크립트는 키 입력을 보내지 않는다. 빌드가 GRUB 메뉴에 timeout=5 를
#     넣으므로(build/config/hooks/normal/9500-diparnux-grub-timeout.hook.binary) 5초 뒤 기본
#     항목으로 부팅한다. 이 훅이 빠지면 메뉴에서 멈춰 제한 시간 뒤 실패한다.
#   - 기본 OVMF_VARS 템플릿(Debian/Ubuntu 의 OVMF_VARS_4M.fd)에는 Secure Boot 키가
#     등록되어 있지 않으므로 Secure Boot 가 꺼진 상태로 부팅한다. Secure Boot 검증은 이
#     테스트의 범위가 아니다.
#   - 네트워크와 디스플레이는 없다(-nic none, -display none).
#   - 직렬 로그에 "Diparnux" 를 포함하고 ASCII 가 아닌 바이트가 있는 줄이 있으면 실패한다. 실기 텍스트 콘솔은
#     한글을 표시하지 못하므로 화면에 나오는 Diparnux 문구는 ASCII 여야 한다.
#   - 이 테스트는 직렬 로그만 본다. 모니터(VGA) 표시는 검사하지 않는다. 모니터 표시는 자동 검사 범위 밖이며 미검증이다.
#
# 의존성: qemu-system-x86 (QEMU), ovmf (OVMF 펌웨어). Debian 13 과 Ubuntu 24.04 에서 확인한 경로만 후보로 둔다.

set -euo pipefail

readonly PROMPT='diparnux login:'
readonly GPU_WARNING='Diparnux GPU [no_gpu]:'
readonly BOOT_MEDIA="${BOOT_MEDIA:-cdrom}"
readonly QEMU_BIN="${QEMU_BIN:-qemu-system-x86_64}"
readonly MEMORY='2G'
readonly OVMF_CODE_CANDIDATES=(
  /usr/share/OVMF/OVMF_CODE_4M.fd
)
readonly OVMF_VARS_CANDIDATES=(
  /usr/share/OVMF/OVMF_VARS_4M.fd
)

usage() {
  echo "usage: $0 <iso-path>" >&2
  exit 2
}

# 첫 번째로 존재하는 파일을 출력한다. 없으면 실패한다.
first_existing_file() {
  local candidate
  for candidate in "$@"; do
    if [[ -f "$candidate" ]]; then
      printf '%s\n' "$candidate"
      return 0
    fi
  done
  return 1
}

[[ $# -eq 1 ]] || usage
ISO="$1"
[[ -f "$ISO" ]] || { echo "ERROR: ISO not found: $ISO" >&2; exit 2; }
case "$BOOT_MEDIA" in
  cdrom|usb) ;;
  *) echo "ERROR: BOOT_MEDIA must be cdrom or usb: $BOOT_MEDIA" >&2; exit 2 ;;
esac

if ! command -v "$QEMU_BIN" >/dev/null 2>&1; then
  echo "ERROR: QEMU not found: $QEMU_BIN (Debian/Ubuntu: apt install qemu-system-x86)" >&2
  exit 2
fi

WORKDIR="$(mktemp -d "${TMPDIR:-/tmp}/diparnux-boot-smoke.XXXXXX")"
LOG="$WORKDIR/serial.log"
QEMU_OUT="$WORKDIR/qemu.out"
QEMU_PID=""

cleanup() {
  local rc=$?
  if [[ -n "$QEMU_PID" ]] && kill -0 "$QEMU_PID" 2>/dev/null; then
    kill "$QEMU_PID" 2>/dev/null || true
    wait "$QEMU_PID" 2>/dev/null || true
  fi
  rm -rf "$WORKDIR"
  exit "$rc"
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

# 실패 시 로그 끝부분을 보여 주고 1 로 끝난다.
fail() {
  echo "FAIL: $*" >&2
  echo "--- serial log (last 80 lines) ---" >&2
  tail -n 80 "$LOG" >&2 2>/dev/null || true
  if [[ -s "$QEMU_OUT" ]]; then
    echo "--- qemu output (last 40 lines) ---" >&2
    tail -n 40 "$QEMU_OUT" >&2 || true
  fi
  exit 1
}

# UEFI 펌웨어 찾기
if [[ -n "${OVMF_CODE:-}" ]]; then
  [[ -f "$OVMF_CODE" ]] || { echo "ERROR: OVMF_CODE not found: $OVMF_CODE" >&2; exit 2; }
else
  OVMF_CODE="$(first_existing_file "${OVMF_CODE_CANDIDATES[@]}")" || {
    echo "ERROR: no OVMF code image found; set OVMF_CODE (apt install ovmf)" >&2
    exit 2
  }
fi

if [[ -n "${OVMF_VARS_TEMPLATE:-}" ]]; then
  [[ -f "$OVMF_VARS_TEMPLATE" ]] || { echo "ERROR: OVMF_VARS_TEMPLATE not found: $OVMF_VARS_TEMPLATE" >&2; exit 2; }
else
  OVMF_VARS_TEMPLATE="$(first_existing_file "${OVMF_VARS_CANDIDATES[@]}")" || {
    echo "ERROR: no OVMF vars template found; set OVMF_VARS_TEMPLATE (apt install ovmf)" >&2
    exit 2
  }
fi

# 변수 저장소는 쓰기 가능한 임시 사본을 쓴다(원본은 건드리지 않는다).
VARS_COPY="$WORKDIR/OVMF_VARS.fd"
cp "$OVMF_VARS_TEMPLATE" "$VARS_COPY"
chmod u+w "$VARS_COPY"

# 가속 방식 선택
if [[ -w /dev/kvm ]]; then
  ACCEL_NAME='kvm'
  ACCEL_ARGS=(-enable-kvm -cpu host)
  DEFAULT_TIMEOUT=180
else
  ACCEL_NAME='tcg'
  ACCEL_ARGS=(-cpu max)
  DEFAULT_TIMEOUT=900
fi

BOOT_TIMEOUT="${BOOT_TIMEOUT:-$DEFAULT_TIMEOUT}"
if ! [[ "$BOOT_TIMEOUT" =~ ^[0-9]+$ ]] || (( BOOT_TIMEOUT < 1 )); then
  echo "ERROR: BOOT_TIMEOUT must be a positive integer (seconds): $BOOT_TIMEOUT" >&2
  exit 2
fi

# 매체 선택. usb 는 xHCI 컨트롤러에 ISO 를 읽기 전용 usb-storage 로 붙인다(qemu-system-x86_64 -device help 로 확인).
if [[ "$BOOT_MEDIA" == usb ]]; then
  MEDIA_ARGS=(
    -device "qemu-xhci,id=xhci"
    -drive "if=none,id=stick,format=raw,readonly=on,file=$ISO"
    -device "usb-storage,bus=xhci.0,drive=stick"
  )
else
  MEDIA_ARGS=(-cdrom "$ISO")
fi

# -no-reboot: 커널 패닉 등으로 재부팅하면 QEMU 가 바로 끝나 실패로 판정된다.
# -serial file: 직렬 콘솔을 로그 파일로 받는다. 사람용 모니터는 끈다.
QEMU_ARGS=(
  -name diparnux-boot-smoke
  -machine q35
  "${ACCEL_ARGS[@]}"
  -m "$MEMORY"
  -drive "if=pflash,format=raw,unit=0,readonly=on,file=$OVMF_CODE"
  -drive "if=pflash,format=raw,unit=1,file=$VARS_COPY"
  "${MEDIA_ARGS[@]}"
  -nic none
  -display none
  -monitor none
  -serial "file:$LOG"
  -no-reboot
)

echo "boot-smoke: iso=$ISO media=$BOOT_MEDIA accel=$ACCEL_NAME timeout=${BOOT_TIMEOUT}s"
echo "boot-smoke: firmware=$OVMF_CODE vars-template=$OVMF_VARS_TEMPLATE"

"$QEMU_BIN" "${QEMU_ARGS[@]}" >"$QEMU_OUT" 2>&1 &
QEMU_PID=$!

# 직렬 로그에서 "Diparnux" 를 포함하는 줄 중 ASCII 가 아닌 바이트가 있는지 본다. tr 로 ASCII 를 지운 뒤 남는 것이 있는지 확인한다.
has_non_ascii_diparnux_line() {
  local lines="$WORKDIR/diparnux-lines"
  grep -a 'Diparnux' "$LOG" > "$lines" 2>/dev/null || true
  [[ -s "$lines" ]] || return 1
  LC_ALL=C tr -d '\000-\177' < "$lines" | LC_ALL=C grep -q .
}

start=$SECONDS
while :; do
  # 프롬프트를 먼저 확인한다. QEMU 가 프롬프트를 쓰고 바로 끝나는 경우도 성공으로 본다.
  if grep -q -F "$PROMPT" "$LOG" 2>/dev/null && grep -q -F "$GPU_WARNING" "$LOG" 2>/dev/null; then
    if has_non_ascii_diparnux_line; then
      fail "직렬 로그의 Diparnux 줄에 ASCII 가 아닌 바이트가 있다(실기 텍스트 콘솔은 표시하지 못한다)"
    fi
    echo "PASS: found '$PROMPT' and '$GPU_WARNING' after $((SECONDS - start))s (media=$BOOT_MEDIA)"
    exit 0
  fi
  if ! kill -0 "$QEMU_PID" 2>/dev/null; then
    fail "QEMU exited before the login prompt appeared (after $((SECONDS - start))s)"
  fi
  if (( SECONDS - start >= BOOT_TIMEOUT )); then
    fail "no '$PROMPT' or '$GPU_WARNING' within ${BOOT_TIMEOUT}s (accel=$ACCEL_NAME)"
  fi
  sleep 2
done
