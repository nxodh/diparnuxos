#!/bin/sh
# 설치된 시스템에서 Secure Boot 가 NVIDIA 모듈을 막을 때 기기 키를 등록하는 보조 도구다.
#
# DKMS 는 서명 키가 없으면 /var/lib/dkms/mok.key, mok.pub 을 이 기기에서 만든다. 이미지에서는 키를 지웠으므로
# 이 키는 기기마다 다르다. 이 도구는 (1) 모듈을 그 키로 다시 빌드해 서명하고 (2) 공개키를 MOK 로 가져오기 예약한다.
# 실제 등록은 다음 재부팅의 MokManager 화면에서 사용자가 비밀번호를 입력해 마친다.
#
# 라이브 세션에서는 재부팅하면 만든 키가 사라지므로 거절한다.
# 환경 변수는 테스트용이다: LIGHTLIX_MOK_CMDLINE, LIGHTLIX_MOK_KEY_DIR, LIGHTLIX_MOK_KERNEL.
set -u

KEY_DIR="${LIGHTLIX_MOK_KEY_DIR:-/var/lib/dkms}"
CMDLINE="${LIGHTLIX_MOK_CMDLINE:-/proc/cmdline}"
KERNEL="${LIGHTLIX_MOK_KERNEL:-$(uname -r)}"
CERT="${KEY_DIR}/mok.pub"
KEY="${KEY_DIR}/mok.key"

say() { printf '%s\n' "$*"; }
die() { printf 'lightlix-mok: %s\n' "$*" >&2; exit 1; }

usage() {
    say 'usage: lightlix-mok status | enroll'
    say '  status  show the Secure Boot state and whether the machine key is enrolled'
    say '  enroll  rebuild the NVIDIA modules with this machine key and schedule its enrollment'
}

is_live() {
    grep -Eq '(^| )boot=live( |$)' "$CMDLINE" 2>/dev/null
}

sb_state() {
    if ! command -v mokutil >/dev/null 2>&1; then
        echo unknown
        return
    fi
    out="$(mokutil --sb-state 2>&1 || true)"
    case "$out" in
        *"SecureBoot enabled"*) echo enabled ;;
        *"SecureBoot disabled"*) echo disabled ;;
        *) echo unknown ;;
    esac
}

key_state() {
    if [ ! -f "$CERT" ]; then
        echo missing
        return
    fi
    if ! command -v mokutil >/dev/null 2>&1; then
        echo unknown
        return
    fi
    out="$(mokutil --test-key "$CERT" 2>&1 || true)"
    case "$out" in
        *"is already enrolled"*) echo enrolled ;;
        *"is not enrolled"*) echo not_enrolled ;;
        *) echo unknown ;;
    esac
}

cmd_status() {
    say "secure_boot=$(sb_state)"
    say "machine_key=$(key_state)"
    if is_live; then
        say 'session=live'
    else
        say 'session=installed'
    fi
}

# 설치된 nvidia DKMS 항목마다 현재 커널용으로 다시 빌드하고 설치한다.
rebuild_modules() {
    versions="$(dkms status 2>/dev/null | sed -n 's|^\(nvidia[a-z-]*\)/\([^,]*\),.*|\1 \2|p' | sort -u)"
    [ -n "$versions" ] || die 'no NVIDIA DKMS module is registered'
    echo "$versions" | while read -r name ver; do
        say "Rebuilding ${name} ${ver} for ${KERNEL}"
        dkms build --force -m "$name" -v "$ver" -k "$KERNEL" || exit 1
        dkms install --force -m "$name" -v "$ver" -k "$KERNEL" || exit 1
    done || die 'rebuilding the NVIDIA modules failed'
}

cmd_enroll() {
    [ "$(id -u)" -eq 0 ] || die 'run this as root (sudo lightlix-mok enroll)'
    if is_live; then
        die 'this is a live session; the key would be lost at reboot. Install Lightlix first, or turn Secure Boot off.'
    fi
    command -v dkms >/dev/null 2>&1 || die 'dkms is not installed'
    command -v mokutil >/dev/null 2>&1 || die 'mokutil is not installed'
    case "$(sb_state)" in
        enabled) ;;
        disabled) say 'Secure Boot is off; no key is needed.'; exit 0 ;;
        *) die 'cannot read the Secure Boot state (is this a UEFI system?)' ;;
    esac
    if [ "$(key_state)" = enrolled ]; then
        say 'The machine key is already enrolled. Reboot if the NVIDIA driver is still blocked.'
        exit 0
    fi

    rebuild_modules
    if [ ! -f "$CERT" ] || [ ! -f "$KEY" ]; then
        die "DKMS did not create the machine key in ${KEY_DIR}"
    fi

    say 'Scheduling the key for enrollment. Choose a one-time password and remember it.'
    mokutil --import "$CERT" || die 'mokutil --import failed'
    say ''
    say 'Next steps:'
    say '  1. Reboot.'
    say '  2. In the blue "Perform MOK management" screen choose "Enroll MOK", then "Continue".'
    say '  3. Enter the password you just chose, then reboot again.'
}

case "${1:-}" in
    status) cmd_status ;;
    enroll) cmd_enroll ;;
    -h|--help|help) usage ;;
    *) usage >&2; exit 2 ;;
esac
