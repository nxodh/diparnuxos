#!/usr/bin/env bash
# 실제 .deb의 내용, 의존성과 오프라인 설치 상태를 Debian 컨테이너에서 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: package check at line $LINENO: $BASH_COMMAND" >&2' ERR
[[ $# == 1 && -f "$1" ]] || { echo 'usage: package.sh <opereta-daemon.deb>' >&2; exit 2; }
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/opereta.conf"
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
dpkg-deb -x "$1" "$WORK/root"
dpkg-deb -e "$1" "$WORK/control"
[[ "$(dpkg-deb -f "$1" Package)" == opereta-daemon ]]
[[ "$(dpkg-deb -f "$1" Version)" == "$OPERETA_VERSION" ]]
[[ "$(dpkg-deb -f "$1" Architecture)" == "$(dpkg --print-architecture)" ]]
depends="$(dpkg-deb -f "$1" Depends)"
for dependency in libc6 systemd nftables ca-certificates; do
    dependency_pattern="(^|[,[:space:]])${dependency}([[:space:],(]|$)"
    [[ "$depends" =~ $dependency_pattern ]]
done
for dependency in opereta-nvidia opereta-llm; do
    [[ "$depends" == *"$dependency (= $OPERETA_VERSION)"* ]]
done
for path in usr/sbin/operetad usr/bin/opereta usr/lib/opereta/check-gpu-permissions usr/lib/opereta/opereta-update usr/lib/opereta/opereta-admin usr/sbin/opereta-install; do
    [[ -x "$WORK/root/$path" ]]
done
for path in etc/default/grub.d/20-opereta.cfg usr/lib/systemd/system/opereta-update-verify.service usr/lib/sysusers.d/opereta.conf usr/lib/tmpfiles.d/opereta-daemon.conf usr/share/opereta/firewall-default.nft usr/share/opereta/skills/opereta/SKILL.md usr/share/doc/opereta-daemon/go-nvml.LICENSE; do
    [[ -s "$WORK/root/$path" ]]
done
# 새 계정의 에이전트 스킬 목록에 보이도록 skel 에 링크가 있어야 한다.
[[ "$(readlink "$WORK/root/etc/skel/.agents/skills/opereta")" == /usr/share/opereta/skills/opereta ]]
[[ "$("$WORK/root/usr/sbin/operetad" --version)" == "operetad $OPERETA_VERSION" ]]
[[ "$("$WORK/root/usr/bin/opereta" version)" == "opereta $OPERETA_VERSION" ]]
# trimpath와 buildvcs=false가 실제 ELF에 반영됐는지 Go 빌드 메타데이터로 확인한다.
for binary in operetad opereta; do
    path="$WORK/root/usr/bin/$binary"
    [[ "$binary" != operetad ]] || path="$WORK/root/usr/sbin/$binary"
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
for unit in operetad.service operetad.socket opereta-firewall-default.service opereta-firewall.service opereta-update-verify.service; do
    grep -Fq "$unit" "$WORK/control/postinst"
done
mkdir -p "$WORK/root/etc" "$WORK/root/run"
systemd-sysusers --root="$WORK/root" "$WORK/root/usr/lib/sysusers.d/opereta.conf"
systemd-tmpfiles --root="$WORK/root" --create "$WORK/root/usr/lib/tmpfiles.d/opereta-daemon.conf"
[[ "$(stat -c '%a' "$WORK/root/var/lib/opereta")" == 750 ]]
[[ "$(stat -c '%a' "$WORK/root/var/lib/opereta/models")" == 2750 ]]
llm_gid="$(awk -F: '$1 == "opereta-llm" { print $3 }' "$WORK/root/etc/group")"
llm_uid="$(awk -F: '$1 == "opereta-llm" { print $3 }' "$WORK/root/etc/passwd")"
[[ "$(stat -c '%g' "$WORK/root/var/lib/opereta/models")" == "$llm_gid" ]]
grep -Eq '^video:[^:]*:[^:]*:.*opereta-llm' "$WORK/root/etc/group"
grep -Eq '^render:[^:]*:[^:]*:.*opereta-llm' "$WORK/root/etc/group"
# 실제 비특권 UID로 디렉터리 통과와 모델·키 읽기를 확인한다.
chmod 755 "$WORK"
(umask 0027; printf 'model\n' > "$WORK/root/var/lib/opereta/models/test.gguf")
[[ "$(stat -c '%a:%g' "$WORK/root/var/lib/opereta/models/test.gguf")" == "640:$llm_gid" ]]
printf 'key\n' > "$WORK/root/var/lib/opereta/api-keys"
chmod 640 "$WORK/root/var/lib/opereta/api-keys"
chown "0:$llm_gid" "$WORK/root/var/lib/opereta/api-keys"
printf 'private\n' > "$WORK/root/var/lib/opereta/api-keys.json"
chmod 600 "$WORK/root/var/lib/opereta/api-keys.json"
for path in models/test.gguf api-keys; do
    setpriv --reuid="$llm_uid" --regid="$llm_gid" --clear-groups cat "$WORK/root/var/lib/opereta/$path" >/dev/null
done
if setpriv --reuid="$llm_uid" --regid="$llm_gid" --clear-groups cat "$WORK/root/var/lib/opereta/api-keys.json" >/dev/null 2>&1; then
    echo 'FAIL: llama user can read private API key metadata' >&2
    exit 1
fi
if setpriv --reuid=65534 --regid=65534 --clear-groups cat "$WORK/root/var/lib/opereta/api-keys" >/dev/null 2>&1; then
    echo 'FAIL: unrelated user can read API keys' >&2
    exit 1
fi
systemctl --root="$WORK/root" enable operetad.service operetad.socket opereta-firewall-default.service opereta-firewall.service
[[ -L "$WORK/root/etc/systemd/system/multi-user.target.wants/operetad.service" ]]
[[ -L "$WORK/root/etc/systemd/system/sockets.target.wants/operetad.socket" ]]
[[ -L "$WORK/root/etc/systemd/system/sysinit.target.wants/opereta-firewall-default.service" ]]
[[ -L "$WORK/root/etc/systemd/system/sysinit.target.wants/opereta-firewall.service" ]]
[[ ! -e "$WORK/root/etc/systemd/system/multi-user.target.wants/opereta-llama.service" ]]
echo 'PASS: real Debian package contents, dependencies, versions, accounts, permissions and enabled units'
