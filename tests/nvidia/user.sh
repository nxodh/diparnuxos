#!/usr/bin/env bash
# lightlix-user 헬퍼를 명령 스텁으로 확인한다. 실제 useradd, chpasswd 는 쓰지 않는다.
set -Eeuo pipefail
trap 'echo "FAIL: user test at line $LINENO: $BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
SCRIPT="$ROOT/daemon/dist/lightlix-user.sh"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
BIN="$WORK/bin"
mkdir -p "$BIN"
LOG="$WORK/calls.log"
export STUB_LOG="$LOG" STUB_PASSWD="$WORK/passwd"
: > "$STUB_PASSWD"

cat > "$BIN/useradd" <<'STUB'
#!/bin/sh
echo "useradd $*" >> "$STUB_LOG"
[ "${STUB_USERADD_FAIL:-0}" = 1 ] && exit 9
for last; do :; done
echo "$last" >> "$STUB_PASSWD"
STUB
cat > "$BIN/chpasswd" <<'STUB'
#!/bin/sh
cat >> "$STUB_LOG.stdin"
echo "chpasswd" >> "$STUB_LOG"
[ "${STUB_CHPASSWD_FAIL:-0}" = 1 ] && exit 1
exit 0
STUB
cat > "$BIN/userdel" <<'STUB'
#!/bin/sh
echo "userdel $*" >> "$STUB_LOG"
grep -vx "$1" "$STUB_PASSWD" > "$STUB_PASSWD.new" || true
mv "$STUB_PASSWD.new" "$STUB_PASSWD"
STUB
cat > "$BIN/getent" <<'STUB'
#!/bin/sh
[ "$1" = passwd ] && grep -qx "$2" "$STUB_PASSWD"
STUB
cat > "$BIN/systemctl" <<'STUB'
#!/bin/sh
echo "systemctl $*" >> "$STUB_LOG"
case "$1" in is-active) [ "${STUB_SSH_ACTIVE:-0}" = 1 ] ;; esac
STUB
cat > "$BIN/ssh-keygen" <<'STUB'
#!/bin/sh
echo "ssh-keygen $*" >> "$STUB_LOG"
[ "${STUB_KEYGEN_FAIL:-0}" = 1 ] && exit 1
exit 0
STUB
cat > "$BIN/sshd" <<'STUB'
#!/bin/sh
echo "sshd $*" >> "$STUB_LOG"
[ "${STUB_SSHD_FAIL:-0}" = 1 ] && exit 1
exit 0
STUB
chmod 755 "$BIN"/*
export PATH="$BIN:$PATH"
export LIGHTLIX_USER_LIST="$WORK/users.list" LIGHTLIX_SSHD_DROPIN="$WORK/sshd/50-lightlix.conf"

run() { sh "$SCRIPT" "$@"; }
last_line() { tail -n 1 <<< "$1"; }
# 계정 목록 파일에 이름이 없어야 한다.
not_listed() {
    if awk -v n="$1" '$1 == n { f = 1 } END { exit !f }' "$WORK/users.list"; then
        echo "FAIL: $1 is still listed" >&2
        exit 1
    fi
}
# 파일에 비밀번호가 남지 않아야 한다.
no_secret_in() {
    if grep -q 'correct-horse-9' "$@"; then
        echo 'FAIL: the password was written to a file or argument list' >&2
        exit 1
    fi
}
not_called() {
    if grep -q -- "$1" "$LOG"; then echo "FAIL: unexpected call: $1" >&2; exit 1; fi
}
: > "$LOG"

# 잘못된 이름
for bad in '' 'Root' '1abc' 'a b' 'root' 'user' 'lightlixd' 'x;y' 'averyveryveryveryveryverylongusername1'; do
    out="$(printf 'password123\n' | run add "$bad" 2>/dev/null || true)"
    [[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:invalid_username' ]] || { echo "FAIL: name '$bad' was not rejected: $out" >&2; exit 1; }
done
not_called useradd
echo 'PASS: invalid user names are rejected'

# 잘못된 비밀번호
for pw in '' 'short' 'alicealice'; do
    out="$(printf '%s\n' "$pw" | run add alicealice 2>/dev/null || true)"
    [[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:invalid_password' ]] || { echo "FAIL: password '$pw' was not rejected: $out" >&2; exit 1; }
done
not_called useradd
echo 'PASS: weak passwords are rejected'

# 정상 생성(관리자 아님)
out="$(printf 'correct-horse-9\n' | run add alice)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=ok' ]]
grep -q '^useradd --create-home --shell /bin/bash --groups lightlix,video,render --comment Lightlix user alice$' "$LOG"
grep -qx 'alice:correct-horse-9' "$STUB_LOG.stdin"
awk '$1=="alice" && $2==0' "$WORK/users.list" | grep -q .
grep -qx 'PasswordAuthentication yes' "$WORK/sshd/50-lightlix.conf"
grep -qx 'AllowUsers alice' "$WORK/sshd/50-lightlix.conf"
grep -qx 'PermitRootLogin no' "$WORK/sshd/50-lightlix.conf"
# 비밀번호가 인자나 목록 파일에 남지 않는다.
no_secret_in "$LOG" "$WORK/users.list" "$WORK/sshd/50-lightlix.conf"
echo 'PASS: account creation, ssh policy, and no password leak'

# 관리자 계정
out="$(printf 'another-secret-1\n' | run add bob --admin)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=ok' ]]
grep -q -- '--groups lightlix,video,render,sudo --comment Lightlix user bob$' "$LOG"
grep -qx 'AllowUsers alice bob' "$WORK/sshd/50-lightlix.conf"
echo 'PASS: admin account joins sudo and the policy lists both'

# 중복
out="$(printf 'correct-horse-9\n' | run add alice 2>/dev/null || true)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:user_exists' ]]
echo 'PASS: duplicate account is rejected'

# 비밀번호 설정 실패는 계정을 되돌린다.
: > "$LOG"
out="$(printf 'correct-horse-9\n' | STUB_CHPASSWD_FAIL=1 run add carol 2>/dev/null || true)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:account_failed' ]]
grep -q '^userdel carol$' "$LOG"
not_listed carol
echo 'PASS: password failure rolls the account back'

# useradd 실패
out="$(printf 'correct-horse-9\n' | STUB_USERADD_FAIL=1 run add dave 2>/dev/null || true)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:account_failed' ]]
echo 'PASS: useradd failure is reported'

# ssh 가 켜져 있으면 reload 한다.
: > "$LOG"
out="$(printf 'correct-horse-9\n' | STUB_SSH_ACTIVE=1 run add erin)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=ok' ]]
grep -q '^systemctl reload ssh.service$' "$LOG"
grep -q '^sshd -t$' "$LOG"
echo 'PASS: a running ssh server is reloaded after validation'

# sshd 가 설정을 거부하면 드롭인을 지운다.
out="$(printf 'correct-horse-9\n' | STUB_SSH_ACTIVE=1 STUB_SSHD_FAIL=1 run add frank 2>/dev/null || true)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:ssh_config' ]]
[[ ! -e "$WORK/sshd/50-lightlix.conf" ]]
echo 'PASS: a rejected ssh configuration is removed'

# 제거
: > "$LOG"
out="$(run remove bob)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=ok' ]]
grep -q '^userdel bob$' "$LOG"
not_listed bob
echo 'PASS: remove deletes the account and keeps the others'

# 관리하지 않는 계정은 지우지 않는다.
echo someone >> "$STUB_PASSWD"
out="$(run remove someone 2>/dev/null || true)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:user_not_found' ]]
not_called 'userdel someone'
echo 'PASS: unmanaged accounts are never removed'

# 마지막 계정을 지우면 드롭인도 지운다.
names="$(awk '{print $1}' "$WORK/users.list")"
while read -r u; do
    [[ -n "$u" ]] && run remove "$u" >/dev/null
done <<< "$names"
[[ ! -e "$WORK/sshd/50-lightlix.conf" ]]
echo 'PASS: no managed account means no ssh policy'
# ssh-prepare: 호스트 키를 만들고 정책을 맞춘다.
: > "$LOG"
printf 'correct-horse-9\n' | run add grace >/dev/null
: > "$LOG"
out="$(run ssh-prepare)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=ok' ]]
grep -q '^ssh-keygen -A$' "$LOG"
grep -qx 'AllowUsers grace' "$WORK/sshd/50-lightlix.conf"
out="$(STUB_KEYGEN_FAIL=1 run ssh-prepare 2>/dev/null || true)"
[[ "$(last_line "$out")" == 'LIGHTLIX_USER_RESULT=error:ssh_keys' ]]
echo 'PASS: ssh-prepare creates host keys and applies the policy'
echo 'PASS: lightlix-user'
