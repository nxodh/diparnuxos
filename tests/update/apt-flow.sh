#!/usr/bin/env bash
# 실제 apt 와 dpkg 로 lightlix-update 의 갱신, 검증, 롤백, 실패 복구 흐름을 확인한다.
# 가짜 패키지(.deb)와 로컬 저장소를 만들고, dkms, modinfo, grub 도구만 스텁으로 둔다.
# 컨테이너 안에서만 실행한다(scripts/test-update-in-docker.sh). 호스트의 apt 를 건드리지 않도록 /.dockerenv 를 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: update flow at line $LINENO: $BASH_COMMAND" >&2' ERR

[[ -f /.dockerenv ]] || { echo "apt-flow: run this inside the test container" >&2; exit 2; }
export DEBIAN_FRONTEND=noninteractive LC_ALL=C

FAKE=/var/fake
REPO=/repo
BUILD=/build
mkdir -p "$FAKE/kernels" "$FAKE/modules" /boot/grub "$REPO" "$BUILD"
OLD=6.12.111+deb13-amd64
NEW=6.12.112+deb13-amd64
NEWER=6.12.113+deb13-amd64
ID_PREFIX=gnulinux
UUID=fakeuuid

pass() { echo "ok: $*"; }
die() { echo "FAIL: $*" >&2; exit 1; }
expect_eq() { [[ "$1" == "$2" ]] || die "$3: got '$1', want '$2'"; pass "$3"; }

# 부팅된 커널을 흉내 낸다. 실제 /proc/cmdline 은 이 컨테이너를 띄운 호스트의 값이라 쓰지 않는다.
export LIGHTLIX_UPDATE_PROC_CMDLINE="$FAKE/cmdline"
set_running() {
    echo "$1" > "$FAKE/running"
    echo "BOOT_IMAGE=/boot/vmlinuz-$1 root=/dev/fake ro quiet" > "$FAKE/cmdline"
}

# ---- 도구 스텁 ----
install -m 0755 /dev/stdin /usr/local/bin/uname <<'EOF'
#!/bin/sh
if [ "$1" = "-r" ]; then cat /var/fake/running; else exec /bin/uname "$@"; fi
EOF
install -m 0755 /dev/stdin /usr/sbin/modinfo <<'EOF'
#!/bin/sh
rel=$2; field=$4; mod=$5
f=/var/fake/modules/$rel
[ -s "$f" ] || { echo "modinfo: ERROR: Module $mod not found." >&2; exit 1; }
case $field in
  filename) echo "/lib/modules/$rel/updates/dkms/$mod.ko.xz";;
  version) cat "$f";;
  license) echo "Dual MIT/GPL";;
esac
EOF
install -m 0755 /dev/stdin /usr/sbin/dkms <<'EOF'
#!/bin/sh
exit 0
EOF
install -m 0755 /dev/stdin /usr/sbin/grub-editenv <<'EOF'
#!/bin/sh
env=/var/fake/grubenv
touch "$env"
case "$1" in
  list) cat "$env";;
  -) shift; if [ "$1" = unset ]; then sed -i "/^$2=/d" "$env"; fi;;
esac
EOF
install -m 0755 /dev/stdin /usr/sbin/grub-set-default <<'EOF'
#!/bin/sh
env=/var/fake/grubenv
touch "$env"; sed -i '/^saved_entry=/d' "$env"; echo "saved_entry=$1" >> "$env"
EOF
install -m 0755 /dev/stdin /usr/sbin/grub-reboot <<'EOF'
#!/bin/sh
env=/var/fake/grubenv
touch "$env"; sed -i '/^next_entry=/d' "$env"; echo "next_entry=$1" >> "$env"
EOF
install -m 0755 /dev/stdin /usr/sbin/update-grub <<'EOF'
#!/bin/sh
out=/boot/grub/grub.cfg
: > "$out"
for img in /boot/vmlinuz-*; do
  [ -e "$img" ] || continue
  rel=${img##*/vmlinuz-}
  echo "menuentry 'Lightlix, with Linux $rel' --class debian \$menuentry_id_option 'gnulinux-$rel-advanced-fakeuuid' {" >> "$out"
  echo "}" >> "$out"
done
EOF
install -m 0755 /dev/stdin /usr/sbin/lightlix-gpu-check <<'EOF'
#!/bin/sh
exit 1
EOF

# ---- 가짜 패키지와 저장소 ----
# mkdeb NAME VERSION SOURCE DEPENDS [PREF_VERSION]  (POSTINST, POSTRM 환경 변수로 스크립트를 준다)
mkdeb() {
    local name=$1 ver=$2 src=$3 deps=$4 pin=${5:-}
    local d="$BUILD/${name}_${ver}"
    rm -rf "$d"
    mkdir -p "$d/DEBIAN"
    {
        echo "Package: $name"
        echo "Version: $ver"
        echo "Architecture: all"
        echo "Maintainer: Test <test@example.invalid>"
        echo "Source: $src"
        [[ -n "$deps" ]] && echo "Depends: $deps"
        echo "Description: fake $name"
    } > "$d/DEBIAN/control"
    if [[ -n "${POSTINST:-}" ]]; then printf '%s\n' "$POSTINST" > "$d/DEBIAN/postinst"; chmod 0755 "$d/DEBIAN/postinst"; fi
    if [[ -n "${POSTRM:-}" ]]; then printf '%s\n' "$POSTRM" > "$d/DEBIAN/postrm"; chmod 0755 "$d/DEBIAN/postrm"; fi
    if [[ -n "$pin" ]]; then
        mkdir -p "$d/etc/apt/preferences.d"
        printf 'Package: nvidia-kernel-open-dkms nvidia-driver\nPin: version %s\nPin-Priority: 1001\n\nPackage: nvidia-kernel-open-dkms nvidia-driver\nPin: version *\nPin-Priority: -1\n' "$pin" \
            > "$d/etc/apt/preferences.d/00-lightlix-nvidia.pref"
    fi
    dpkg-deb --build "$d" "$REPO/${name}_${ver}_all.deb" > /dev/null
}

kernel_image() { # REL VERSION
    local rel=$1 ver=$2
    POSTINST="#!/bin/sh
set -e
[ \"\$1\" = configure ] || exit 0
if [ -e /var/fake/dkms-fail-$rel ]; then echo 'dkms: autoinstall for kernel $rel failed' >&2; exit 1; fi
touch /var/fake/kernels/$rel
echo vmlinuz > /boot/vmlinuz-$rel
echo initrd > /boot/initrd.img-$rel
drv=\$(dpkg-query -W -f '\${Version}' nvidia-kernel-open-dkms 2>/dev/null || true)
if [ -n \"\$drv\" ]; then echo \"\${drv%-*}\" > /var/fake/modules/$rel; fi
update-grub" \
    POSTRM="#!/bin/sh
case \"\$1\" in remove|purge)
  rm -f /boot/vmlinuz-$rel /boot/initrd.img-$rel /var/fake/kernels/$rel /var/fake/modules/$rel
  update-grub;;
esac
exit 0" \
    mkdeb "linux-image-$rel" "$ver" linux-signed-amd64 ""
}

publish_kernel() { # REL VERSION
    kernel_image "$1" "$2"
    mkdeb "linux-headers-$1" "$2" linux ""
    mkdeb linux-image-amd64 "$2" linux-signed-amd64 "linux-image-$1 (= $2)"
    mkdeb linux-headers-amd64 "$2" linux "linux-headers-$1 (= $2)"
}

publish_driver() { # DRIVER_VERSION(without revision) POLICY_VERSION
    local ver="$1-1"
    POSTINST="#!/bin/sh
set -e
[ \"\$1\" = configure ] || exit 0
ver=\$(dpkg-query -W -f '\${Version}' nvidia-kernel-open-dkms)
if [ \"\$(cat /var/fake/driver-fail 2>/dev/null)\" = \"\$ver\" ]; then echo 'dkms: nvidia build failed' >&2; exit 1; fi
for k in /var/fake/kernels/*; do
  [ -e \"\$k\" ] || continue
  echo \"\${ver%-*}\" > /var/fake/modules/\$(basename \"\$k\")
done" \
    mkdeb nvidia-kernel-open-dkms "$ver" nvidia-graphics-drivers ""
    mkdeb nvidia-driver "$ver" nvidia-graphics-drivers ""
    mkdeb lightlix-nvidia "$2" lightlix-nvidia "nvidia-kernel-open-dkms (= $ver), nvidia-driver (= $ver)" "$ver"
}

reindex() {
    (cd "$REPO" && apt-ftparchive packages . > Packages && gzip -kf Packages && apt-ftparchive release . > Release)
    apt-get update -q > /dev/null
}

# 컨테이너의 Debian 저장소는 끄고 가짜 저장소만 쓴다. 진짜 linux-image-amd64 와 섞이면 안 된다.
rm -f /etc/apt/sources.list /etc/apt/sources.list.d/*.sources /etc/apt/sources.list.d/*.list
echo "deb [trusted=yes] file:$REPO ./" > /etc/apt/sources.list.d/fake.list
publish_kernel "$OLD" 6.12.111-1
publish_driver 595.91.07 0.3.0
mkdeb fake-tool 1.0-1 fake-tool ""
reindex

# ---- 초기 설치 ----
set_running "$OLD"
apt-get install -y -q --no-install-recommends lightlix-nvidia linux-image-amd64 linux-headers-amd64 fake-tool
mapfile -t managed < <(dpkg-query -W -f '${binary:Package}\n' | grep -E '^(linux-(image|headers)-|nvidia-)')
apt-mark hold "${managed[@]}" > /dev/null
grub-set-default "$ID_PREFIX-$OLD-advanced-$UUID"
expect_eq "$(dpkg-query -W -f '${Version}' nvidia-kernel-open-dkms)" 595.91.07-1 "initial driver"
expect_eq "$(cat "$FAKE/modules/$OLD")" 595.91.07 "initial module"

# 같은 저장소에 새 버전이 올라온다.
publish_kernel "$NEW" 6.12.112-1
publish_driver 595.100.01 0.4.0
mkdeb fake-tool 2.0-1 fake-tool ""
reindex

# 전제 확인: 일반 apt 는 hold 와 핀 때문에 커널과 드라이버를 올리지 않는다.
if apt-get -s dist-upgrade | grep -E '^Inst (nvidia|linux-image|lightlix-nvidia)'; then
    die "plain apt would have changed the pinned and held packages"
fi
pass "plain apt keeps the held packages"

LU=/usr/local/bin/lightlix-update
last_json() { tail -n 1; }
# run_json LABEL ARGS... : lightlix-update 를 실행해 마지막 JSON 줄을 돌려준다. 종료 코드와 상관없이 출력 전체를 실패 때 보여 준다.
run_json() {
    local out rc=0
    out="$("$LU" "$@" --json 2>&1)" || rc=$?
    LAST_RC=$rc
    LAST_OUT="$out"
    printf '%s\n' "$out" | tail -n 1
}
dump_last() { echo "--- last output (exit $LAST_RC):" >&2; printf '%s\n' "$LAST_OUT" | tail -n 40 >&2; }

# ---- check ----
plan="$(run_json check)"
expect_eq "$(jq -r .ok <<<"$plan")" true "check ok"
expect_eq "$(jq -r '.plan.kernels[0]' <<<"$plan")" "$NEW" "check plans the new kernel"
expect_eq "$(jq -r .plan.driver_to <<<"$plan")" 595.100.01-1 "check plans the new driver"

# ---- apply ----
res="$(run_json apply)"
[[ "$(jq -r .ok <<<"$res")" == true ]] || dump_last
expect_eq "$(jq -r .ok <<<"$res")" true "apply ok"
expect_eq "$(jq -r .state.phase <<<"$res")" pending_boot "apply phase"
expect_eq "$(dpkg-query -W -f '${Version}' nvidia-kernel-open-dkms)" 595.100.01-1 "driver upgraded"
expect_eq "$(dpkg-query -W -f '${db:Status-Status}' "linux-image-$NEW")" installed "new kernel installed"
expect_eq "$(dpkg-query -W -f '${db:Status-Status}' "linux-image-$OLD")" installed "old kernel kept"
expect_eq "$(cat "$FAKE/modules/$NEW")" 595.100.01 "new kernel module is built for the new driver"
expect_eq "$(cat "$FAKE/modules/$OLD")" 595.100.01 "old kernel module is rebuilt too"
grep -qx "saved_entry=$ID_PREFIX-$OLD-advanced-$UUID" "$FAKE/grubenv" || die "saved default changed before verification"
grep -qx "next_entry=$ID_PREFIX-$NEW-advanced-$UUID" "$FAKE/grubenv" || die "boot-once entry was not set"
pass "grub selection"
apt-mark showhold | grep -qx "linux-image-$NEW" || die "the new kernel image is not held"
apt-mark showhold | grep -qx "linux-image-amd64" || die "the meta package is not held"
pass "holds restored"
if [[ -e /usr/bin/needrestart ]]; then die "unexpected needrestart"; fi

# ---- 재부팅 뒤 검증 ----
set_running "$NEW"
res="$(run_json verify)"
expect_eq "$(jq -r .state.phase <<<"$res")" committed "verify commits"
grep -qx "saved_entry=$ID_PREFIX-$NEW-advanced-$UUID" "$FAKE/grubenv" || die "the saved default was not switched"
pass "saved default switched after verification"

# ---- 롤백 ----
set_running "$OLD"
res="$(run_json rollback)"
expect_eq "$(jq -r .ok <<<"$res")" true "rollback ok"
expect_eq "$(jq -r .state.phase <<<"$res")" rolled_back "rollback phase"
expect_eq "$(dpkg-query -W -f '${Version}' nvidia-kernel-open-dkms)" 595.91.07-1 "driver restored"
expect_eq "$(dpkg-query -W -f '${Version}' lightlix-nvidia)" 0.3.0 "policy package restored"
grep -qx "saved_entry=$ID_PREFIX-$OLD-advanced-$UUID" "$FAKE/grubenv" || die "rollback did not select the previous kernel"
expect_eq "$(cat "$FAKE/modules/$NEW")" 595.91.07 "module of the new kernel follows the restored driver"
pass "rollback"

# ---- 드라이버 갱신이 실패하면 이전 버전으로 복구한다 ----
echo 595.100.01-1 > "$FAKE/driver-fail"
res="$(run_json apply)"
expect_eq "$(jq -r .ok <<<"$res")" false "driver failure is reported"
expect_eq "$(jq -r .error.code <<<"$res")" dkms_failed "driver failure code"
expect_eq "$(dpkg-query -W -f '${Version}' nvidia-kernel-open-dkms)" 595.91.07-1 "driver restored after failure"
expect_eq "$(dpkg-query -W -f '${db:Status-Status}' nvidia-kernel-open-dkms)" installed "driver is configured again"
rm -f "$FAKE/driver-fail"

# ---- 새 커널의 DKMS 가 실패하면 이전 커널을 유지한다 ----
publish_kernel "$NEWER" 6.12.113-1
reindex
touch "$FAKE/dkms-fail-$NEWER"
if [[ -n "${DEBUG_APT:-}" ]]; then
    for flags in "" "--ignore-hold" "--allow-change-held-packages" "--ignore-hold --allow-change-held-packages"; do
        echo "--- simulate install [$flags]"
        # shellcheck disable=SC2086
        apt-get -s -y -q --no-install-recommends $flags install "linux-headers-$NEWER" "linux-image-$NEWER" | grep -E '^Inst'
    done
fi
res="$(run_json apply)"
expect_eq "$(jq -r .ok <<<"$res")" false "kernel failure is reported"
expect_eq "$(jq -r .error.code <<<"$res")" dkms_failed "kernel failure code"
if dpkg-query -W -f '${db:Status-Status}' "linux-image-$NEWER" 2>/dev/null | grep -q .; then
    status="$(dpkg-query -W -f '${db:Status-Status}' "linux-image-$NEWER" 2>/dev/null)"
    [[ "$status" == "not-installed" || "$status" == "config-files" ]] || die "failed kernel is still present: $status"
fi
[[ ! -e /boot/vmlinuz-$NEWER ]] || die "failed kernel image remains in /boot"
grep -qx "saved_entry=$ID_PREFIX-$OLD-advanced-$UUID" "$FAKE/grubenv" || die "the saved default moved after a failed kernel"
grep -q '^next_entry=' "$FAKE/grubenv" && die "a failed kernel must not be selected for the next boot"
if ! apt-mark showhold | grep -qx "linux-image-amd64"; then
    echo "--- holds:"; apt-mark showhold
    echo "--- packages:"; dpkg -l 'linux-*' 'nvidia-*' | tail -n +6
    echo "--- last state:"; cat /var/lib/lightlix/update/state.json
    die "hold lost after the failed update"
fi
pass "failed kernel is removed and the previous kernel stays the default"

echo "update flow: all checks passed"
