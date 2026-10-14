#!/usr/bin/env bash
# 실제 .deb의 내용, 의존성과 오프라인 설치 상태를 Debian 컨테이너에서 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: package check at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ $# == 1 && -f "$1" ]] || { echo 'usage: package.sh <diparnux-daemon.deb>' >&2; exit 2; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/diparnux.conf"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
dpkg-deb -x "$1" "$WORK/root"
dpkg-deb -e "$1" "$WORK/control"
[[ "$(dpkg-deb -f "$1" Package)" == diparnux-daemon ]]
[[ "$(dpkg-deb -f "$1" Version)" == "$DIPARNUX_VERSION" ]]
[[ "$(dpkg-deb -f "$1" Architecture)" == "$(dpkg --print-architecture)" ]]
depends="$(dpkg-deb -f "$1" Depends)"
for dependency in libc6 systemd nftables ca-certificates; do
    dependency_pattern="(^|[,[:space:]])${dependency}([[:space:],(]|$)"
    [[ "$depends" =~ $dependency_pattern ]]
done
for dependency in diparnux-nvidia diparnux-llm; do
    [[ "$depends" == *"$dependency (= $DIPARNUX_VERSION)"* ]]
done
for path in usr/sbin/diparnuxd usr/bin/diparnux usr/lib/diparnux/check-gpu-permissions usr/lib/diparnux/diparnux-update usr/lib/diparnux/diparnux-admin usr/sbin/diparnux-install; do
    [[ -x "$WORK/root/$path" ]]
done
for path in etc/default/grub.d/20-diparnux.cfg usr/lib/systemd/system/diparnux-update-verify.service usr/lib/sysusers.d/diparnux.conf usr/lib/tmpfiles.d/diparnux-daemon.conf usr/share/diparnux/firewall-default.nft usr/share/diparnux/skills/diparnux/SKILL.md usr/share/doc/diparnux-daemon/go-nvml.LICENSE; do
    [[ -s "$WORK/root/$path" ]]
done
# 새 계정의 에이전트 스킬 목록에 보이도록 skel 에 링크가 있어야 한다.
[[ "$(readlink "$WORK/root/etc/skel/.agents/skills/diparnux")" == /usr/share/diparnux/skills/diparnux ]]
[[ "$("$WORK/root/usr/sbin/diparnuxd" --version)" == "diparnuxd $DIPARNUX_VERSION" ]]
[[ "$("$WORK/root/usr/bin/diparnux" version)" == "diparnux $DIPARNUX_VERSION" ]]
# trimpath와 buildvcs=false가 실제 ELF에 반영됐는지 Go 빌드 메타데이터로 확인한다.
for binary in diparnuxd diparnux; do
    path="$WORK/root/usr/bin/$binary"
    [[ "$binary" != diparnuxd ]] || path="$WORK/root/usr/sbin/$binary"
    go version -m "$path" > "$WORK/buildinfo"
    grep -Fq 'CGO_ENABLED=1' "$WORK/buildinfo"
    grep -Fq -- '-trimpath=true' "$WORK/buildinfo"
    if grep -Eq 'vcs\.|/src/|/work/|/Users/' "$WORK/buildinfo"; then
        echo 'FAIL: VCS metadata or build paths found in the Go binary' >&2
        exit 1
    fi
done
# debhelper가 계정과 디렉터리를 만든 뒤 유닛을 활성화하는 postinst를 생성한다.
grep -q systemd-sysusers "$WORK/control/postinst"
grep -q systemd-tmpfiles "$WORK/control/postinst"
awk '/systemd-sysusers/ { users=NR } /systemd-tmpfiles/ { if (!users || users>=NR) exit 1; checked=1 } END { if (!checked) exit 1 }' "$WORK/control/postinst"
for unit in diparnuxd.service diparnuxd.socket diparnux-firewall-default.service diparnux-firewall.service diparnux-update-verify.service; do
    grep -Fq "$unit" "$WORK/control/postinst"
done
mkdir -p "$WORK/root/etc" "$WORK/root/run"
systemd-sysusers --root="$WORK/root" "$WORK/root/usr/lib/sysusers.d/diparnux.conf"
systemd-tmpfiles --root="$WORK/root" --create "$WORK/root/usr/lib/tmpfiles.d/diparnux-daemon.conf"
[[ "$(stat -c '%a' "$WORK/root/var/lib/diparnux")" == 750 ]]
[[ "$(stat -c '%a' "$WORK/root/var/lib/diparnux/models")" == 2750 ]]
llm_gid="$(awk -F: '$1 == "diparnux-llm" { print $3 }' "$WORK/root/etc/group")"
llm_uid="$(awk -F: '$1 == "diparnux-llm" { print $3 }' "$WORK/root/etc/passwd")"
[[ "$(stat -c '%g' "$WORK/root/var/lib/diparnux/models")" == "$llm_gid" ]]
grep -Eq '^video:[^:]*:[^:]*:.*diparnux-llm' "$WORK/root/etc/group"
grep -Eq '^render:[^:]*:[^:]*:.*diparnux-llm' "$WORK/root/etc/group"
# 실제 비특권 UID로 디렉터리 통과와 모델·키 읽기를 확인한다.
chmod 755 "$WORK"
(umask 0027; printf 'model\n' > "$WORK/root/var/lib/diparnux/models/test.gguf")
[[ "$(stat -c '%a:%g' "$WORK/root/var/lib/diparnux/models/test.gguf")" == "640:$llm_gid" ]]
printf 'key\n' > "$WORK/root/var/lib/diparnux/api-keys"
chmod 640 "$WORK/root/var/lib/diparnux/api-keys"
chown "0:$llm_gid" "$WORK/root/var/lib/diparnux/api-keys"
printf 'private\n' > "$WORK/root/var/lib/diparnux/api-keys.json"
chmod 600 "$WORK/root/var/lib/diparnux/api-keys.json"
for path in models/test.gguf api-keys; do
    setpriv --reuid="$llm_uid" --regid="$llm_gid" --clear-groups cat "$WORK/root/var/lib/diparnux/$path" >/dev/null
done
if setpriv --reuid="$llm_uid" --regid="$llm_gid" --clear-groups cat "$WORK/root/var/lib/diparnux/api-keys.json" >/dev/null 2>&1; then
    echo 'FAIL: llama user can read private API key metadata' >&2
    exit 1
fi
if setpriv --reuid=65534 --regid=65534 --clear-groups cat "$WORK/root/var/lib/diparnux/api-keys" >/dev/null 2>&1; then
    echo 'FAIL: unrelated user can read API keys' >&2
    exit 1
fi
systemctl --root="$WORK/root" enable diparnuxd.service diparnuxd.socket diparnux-firewall-default.service diparnux-firewall.service
[[ -L "$WORK/root/etc/systemd/system/multi-user.target.wants/diparnuxd.service" ]]
[[ -L "$WORK/root/etc/systemd/system/sockets.target.wants/diparnuxd.socket" ]]
[[ -L "$WORK/root/etc/systemd/system/sysinit.target.wants/diparnux-firewall-default.service" ]]
[[ -L "$WORK/root/etc/systemd/system/sysinit.target.wants/diparnux-firewall.service" ]]
[[ ! -e "$WORK/root/etc/systemd/system/multi-user.target.wants/diparnux-llama.service" ]]
echo 'PASS: real Debian package contents, dependencies, versions, accounts, permissions and enabled units'
