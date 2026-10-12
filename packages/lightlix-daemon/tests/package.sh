#!/usr/bin/env bash
# 실제 .deb의 내용, 의존성과 오프라인 설치 상태를 Debian 컨테이너에서 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: package check at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ $# == 1 && -f "$1" ]] || { echo 'usage: package.sh <lightlix-daemon.deb>' >&2; exit 2; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/lightlix.conf"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
dpkg-deb -x "$1" "$WORK/root"
dpkg-deb -e "$1" "$WORK/control"
[[ "$(dpkg-deb -f "$1" Package)" == lightlix-daemon ]]
[[ "$(dpkg-deb -f "$1" Version)" == "$LIGHTLIX_VERSION" ]]
[[ "$(dpkg-deb -f "$1" Architecture)" == "$(dpkg --print-architecture)" ]]
depends="$(dpkg-deb -f "$1" Depends)"
for dependency in libc6 systemd nftables ca-certificates; do
    dependency_pattern="(^|[,[:space:]])${dependency}([[:space:],(]|$)"
    [[ "$depends" =~ $dependency_pattern ]]
done
for dependency in lightlix-nvidia lightlix-llm; do
    [[ "$depends" == *"$dependency (= $LIGHTLIX_VERSION)"* ]]
done
for path in usr/sbin/lightlixd usr/bin/lightlix usr/lib/lightlix/check-gpu-permissions usr/lib/lightlix/lightlix-update usr/sbin/lightlix-install; do
    [[ -x "$WORK/root/$path" ]]
done
for path in etc/default/grub.d/20-lightlix.cfg usr/lib/systemd/system/lightlix-update-verify.service usr/lib/sysusers.d/lightlix.conf usr/lib/tmpfiles.d/lightlix-daemon.conf usr/share/lightlix/firewall-default.nft usr/share/doc/lightlix-daemon/go-nvml.LICENSE; do
    [[ -s "$WORK/root/$path" ]]
done
[[ "$("$WORK/root/usr/sbin/lightlixd" --version)" == "lightlixd $LIGHTLIX_VERSION" ]]
[[ "$("$WORK/root/usr/bin/lightlix" version)" == "lightlix $LIGHTLIX_VERSION" ]]
# trimpath와 buildvcs=false가 실제 ELF에 반영됐는지 Go 빌드 메타데이터로 확인한다.
for binary in lightlixd lightlix; do
    path="$WORK/root/usr/bin/$binary"
    [[ "$binary" != lightlixd ]] || path="$WORK/root/usr/sbin/$binary"
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
for unit in lightlixd.service lightlixd.socket lightlix-firewall-default.service lightlix-firewall.service lightlix-update-verify.service; do
    grep -Fq "$unit" "$WORK/control/postinst"
done
mkdir -p "$WORK/root/etc" "$WORK/root/run"
systemd-sysusers --root="$WORK/root" "$WORK/root/usr/lib/sysusers.d/lightlix.conf"
systemd-tmpfiles --root="$WORK/root" --create "$WORK/root/usr/lib/tmpfiles.d/lightlix-daemon.conf"
[[ "$(stat -c '%a' "$WORK/root/var/lib/lightlix")" == 750 ]]
[[ "$(stat -c '%a' "$WORK/root/var/lib/lightlix/models")" == 2750 ]]
llm_gid="$(awk -F: '$1 == "lightlix-llm" { print $3 }' "$WORK/root/etc/group")"
llm_uid="$(awk -F: '$1 == "lightlix-llm" { print $3 }' "$WORK/root/etc/passwd")"
[[ "$(stat -c '%g' "$WORK/root/var/lib/lightlix/models")" == "$llm_gid" ]]
grep -Eq '^video:[^:]*:[^:]*:.*lightlix-llm' "$WORK/root/etc/group"
grep -Eq '^render:[^:]*:[^:]*:.*lightlix-llm' "$WORK/root/etc/group"
# 실제 비특권 UID로 디렉터리 통과와 모델·키 읽기를 확인한다.
chmod 755 "$WORK"
(umask 0027; printf 'model\n' > "$WORK/root/var/lib/lightlix/models/test.gguf")
[[ "$(stat -c '%a:%g' "$WORK/root/var/lib/lightlix/models/test.gguf")" == "640:$llm_gid" ]]
printf 'key\n' > "$WORK/root/var/lib/lightlix/api-keys"
chmod 640 "$WORK/root/var/lib/lightlix/api-keys"
chown "0:$llm_gid" "$WORK/root/var/lib/lightlix/api-keys"
printf 'private\n' > "$WORK/root/var/lib/lightlix/api-keys.json"
chmod 600 "$WORK/root/var/lib/lightlix/api-keys.json"
for path in models/test.gguf api-keys; do
    setpriv --reuid="$llm_uid" --regid="$llm_gid" --clear-groups cat "$WORK/root/var/lib/lightlix/$path" >/dev/null
done
if setpriv --reuid="$llm_uid" --regid="$llm_gid" --clear-groups cat "$WORK/root/var/lib/lightlix/api-keys.json" >/dev/null 2>&1; then
    echo 'FAIL: llama user can read private API key metadata' >&2
    exit 1
fi
if setpriv --reuid=65534 --regid=65534 --clear-groups cat "$WORK/root/var/lib/lightlix/api-keys" >/dev/null 2>&1; then
    echo 'FAIL: unrelated user can read API keys' >&2
    exit 1
fi
systemctl --root="$WORK/root" enable lightlixd.service lightlixd.socket lightlix-firewall-default.service lightlix-firewall.service
[[ -L "$WORK/root/etc/systemd/system/multi-user.target.wants/lightlixd.service" ]]
[[ -L "$WORK/root/etc/systemd/system/sockets.target.wants/lightlixd.socket" ]]
[[ -L "$WORK/root/etc/systemd/system/sysinit.target.wants/lightlix-firewall-default.service" ]]
[[ -L "$WORK/root/etc/systemd/system/sysinit.target.wants/lightlix-firewall.service" ]]
[[ ! -e "$WORK/root/etc/systemd/system/multi-user.target.wants/lightlix-llama.service" ]]
echo 'PASS: real Debian package contents, dependencies, versions, accounts, permissions and enabled units'
