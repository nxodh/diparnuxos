#!/usr/bin/env bash
# lightlix-mok 를 명령 스텁으로 확인한다. 실제 dkms, mokutil, UEFI 는 쓰지 않는다.
set -Eeuo pipefail
trap 'echo "FAIL: mok test at line $LINENO: $BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/packages/lightlix-nvidia/src/lightlix-mok.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BIN="$WORK/bin"
KEYS="$WORK/keys"
mkdir -p "$BIN" "$KEYS"
LOG="$WORK/calls.log"

# 스텁: 환경 변수 STUB_* 로 동작을 정한다. 호출은 LOG 에 남긴다.
cat > "$BIN/mokutil" <<'STUB'
#!/bin/sh
echo "mokutil $*" >> "$STUB_LOG"
case "$1" in
    --sb-state) echo "${STUB_SB:-SecureBoot enabled}" ;;
    --test-key) echo "${STUB_KEY:-$2 is not enrolled}" ;;
    --import) [ "${STUB_IMPORT_FAIL:-0}" = 1 ] && exit 1; exit 0 ;;
esac
STUB
cat > "$BIN/dkms" <<'STUB'
#!/bin/sh
echo "dkms $*" >> "$STUB_LOG"
case "$1" in
    status) printf '%s\n' "${STUB_DKMS_STATUS:-nvidia/595.91.07, 6.12.99-test, x86_64: installed}" ;;
    build) [ "${STUB_BUILD_FAIL:-0}" = 1 ] && exit 1
           [ "${STUB_NO_KEY:-0}" = 1 ] || { : > "$STUB_KEYS/mok.key"; : > "$STUB_KEYS/mok.pub"; } ;;
    install) exit 0 ;;
esac
STUB
cat > "$BIN/id" <<'STUB'
#!/bin/sh
if [ "$1" = -u ]; then echo "${STUB_UID:-0}"; else /usr/bin/id "$@"; fi
STUB
chmod 755 "$BIN"/*

export PATH="$BIN:$PATH" STUB_LOG="$LOG" STUB_KEYS="$KEYS"
export LIGHTLIX_MOK_KEY_DIR="$KEYS" LIGHTLIX_MOK_KERNEL=6.12.99-test
echo 'quiet splash' > "$WORK/cmdline-installed"
echo 'boot=live components quiet' > "$WORK/cmdline-live"
run() { LIGHTLIX_MOK_CMDLINE="$1" sh "$SCRIPT" "${@:2}"; }
# 호출 기록에 패턴이 없어야 한다.
not_called() {
    if grep -q -- "$1" "$LOG"; then
        echo "FAIL: unexpected call: $1" >&2
        exit 1
    fi
}

# status
out="$(run "$WORK/cmdline-installed" status)"
grep -qx 'secure_boot=enabled' <<< "$out"
grep -qx 'machine_key=missing' <<< "$out"
grep -qx 'session=installed' <<< "$out"
out="$(run "$WORK/cmdline-live" status)"
grep -qx 'session=live' <<< "$out"
echo 'PASS: status'

# 라이브 세션은 거절한다.
: > "$LOG"
if run "$WORK/cmdline-live" enroll >/dev/null 2>"$WORK/err"; then echo 'FAIL: enroll must fail in a live session' >&2; exit 1; fi
grep -q 'live session' "$WORK/err"
not_called 'dkms build'
echo 'PASS: live session is refused'

# root 가 아니면 거절한다.
if STUB_UID=1000 run "$WORK/cmdline-installed" enroll >/dev/null 2>"$WORK/err"; then echo 'FAIL: enroll must need root' >&2; exit 1; fi
grep -q 'as root' "$WORK/err"
echo 'PASS: root is required'

# Secure Boot 가 꺼져 있으면 아무것도 하지 않는다.
: > "$LOG"
out="$(STUB_SB='SecureBoot disabled' run "$WORK/cmdline-installed" enroll)"
grep -q 'no key is needed' <<< "$out"
not_called 'dkms build'
echo 'PASS: nothing to do when Secure Boot is off'

# 이미 등록된 키면 다시 만들지 않는다.
: > "$KEYS/mok.pub"; : > "$KEYS/mok.key"
: > "$LOG"
out="$(STUB_KEY="$KEYS/mok.pub is already enrolled" run "$WORK/cmdline-installed" enroll)"
grep -q 'already enrolled' <<< "$out"
not_called 'dkms build'
rm -f "$KEYS/mok.pub" "$KEYS/mok.key"
echo 'PASS: an enrolled key is not rebuilt'

# 정상 흐름: 빌드, 설치, 가져오기 순서
: > "$LOG"
out="$(run "$WORK/cmdline-installed" enroll)"
grep -q 'Enroll MOK' <<< "$out"
build_line="$(grep -n '^dkms build --force -m nvidia -v 595.91.07 -k 6.12.99-test$' "$LOG" | cut -d: -f1)"
install_line="$(grep -n '^dkms install --force -m nvidia -v 595.91.07 -k 6.12.99-test$' "$LOG" | cut -d: -f1)"
import_line="$(grep -n "^mokutil --import $KEYS/mok.pub\$" "$LOG" | cut -d: -f1)"
[[ -n "$build_line" && -n "$install_line" && -n "$import_line" ]]
(( build_line < install_line && install_line < import_line ))
echo 'PASS: modules are rebuilt and installed before the key import'

# 빌드 실패는 가져오기를 예약하지 않는다.
rm -f "$KEYS/mok.pub" "$KEYS/mok.key"
: > "$LOG"
if STUB_BUILD_FAIL=1 run "$WORK/cmdline-installed" enroll >/dev/null 2>"$WORK/err"; then echo 'FAIL: build failure must fail' >&2; exit 1; fi
not_called 'mokutil --import'
echo 'PASS: build failure schedules nothing'

# DKMS 가 키를 만들지 않았으면 실패한다.
: > "$LOG"
if STUB_NO_KEY=1 run "$WORK/cmdline-installed" enroll >/dev/null 2>"$WORK/err"; then echo 'FAIL: missing key must fail' >&2; exit 1; fi
grep -q 'did not create the machine key' "$WORK/err"
not_called 'mokutil --import'
echo 'PASS: missing machine key is an error'

# NVIDIA DKMS 항목이 없으면 실패한다.
: > "$LOG"
if STUB_DKMS_STATUS='other/1.0, 6.12.99-test, x86_64: installed' run "$WORK/cmdline-installed" enroll >/dev/null 2>"$WORK/err"; then echo 'FAIL: no nvidia module must fail' >&2; exit 1; fi
grep -q 'no NVIDIA DKMS module' "$WORK/err"
echo 'PASS: no NVIDIA module is an error'
echo 'PASS: lightlix-mok'
