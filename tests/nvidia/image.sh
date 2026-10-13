#!/usr/bin/env bash
# 완성된 ISO 의 squashfs 를 읽어 드라이버 구성과 키 제거가 실제 산출물에 반영됐는지 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: image check at line $LINENO: $BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/lightlix.conf"
[[ $# == 1 && -f "$1" ]] || { echo 'usage: image.sh <iso>' >&2; exit 2; }
for tool in xorriso unsquashfs sfdisk; do
    command -v "$tool" >/dev/null || { echo "ERROR: missing tool: $tool" >&2; exit 2; }
done
WORK="$(mktemp -d)"
trap 'rm -rf "$WORK"' EXIT
xorriso -osirrox on -indev "$1" -extract /live/filesystem.squashfs "$WORK/rootfs.squashfs" >/dev/null 2>&1
unsquashfs -ll "$WORK/rootfs.squashfs" > "$WORK/files"
unsquashfs -cat "$WORK/rootfs.squashfs" var/lib/dpkg/status > "$WORK/status"
unsquashfs -cat "$WORK/rootfs.squashfs" etc/os-release > "$WORK/os-release"
grep -qx "ID=$LIGHTLIX_ID" "$WORK/os-release"
grep -Fqx -e "VERSION_ID=$LIGHTLIX_VERSION" -e "VERSION_ID=\"$LIGHTLIX_VERSION\"" "$WORK/os-release"

check_package() {
    local pkg="$1" version="$2"
    awk -v pkg="$pkg" -v version="$version" '
        BEGIN { RS=""; FS="\n" }
        { name=""; actual=""; installed=0
          for (i=1;i<=NF;i++) {
            if ($i ~ /^Package: /) name=substr($i,10)
            if ($i ~ /^Version: /) actual=substr($i,10)
            if ($i ~ /^Status: (install|hold) ok installed$/) installed=1
          }
          if (name==pkg && actual==version && installed) found=1
        }
        END { exit !found }' "$WORK/status" || { echo "FAIL: missing package: $pkg=$version" >&2; exit 1; }
    echo "PASS: $pkg=$version"
}
driver="${NVIDIA_DRIVER_VERSION}-${NVIDIA_DEB_REVISION}"
for pkg in nvidia-kernel-open-dkms nvidia-driver nvidia-driver-cuda firmware-nvidia-gsp nvidia-persistenced; do
    check_package "$pkg" "$driver"
done
check_package lightlix-nvidia "$LIGHTLIX_VERSION"
check_package lightlix-llm "$LIGHTLIX_VERSION"
check_package lightlix-daemon "$LIGHTLIX_VERSION"
check_package cuda-keyring "$CUDA_KEYRING_VERSION"
check_package "nvidia-driver-pinning-$NVIDIA_BRANCH" "$NVIDIA_PIN_VERSION"
check_package "cuda-cudart-$CUDA_PKG_SUFFIX" "$CUDA_CUDART_VERSION"
check_package "libcublas-$CUDA_PKG_SUFFIX" "$CUDA_CUBLAS_VERSION"

if awk 'BEGIN { RS="" } /Package: (nvidia-kernel-dkms|nvidia-kernel-source|cuda-drivers[^\n]*|nvidia-open|cuda-nvcc[^\n]*)\n/ && /Status: (install|hold) ok installed/ { bad=1 } END { exit !bad }' "$WORK/status"; then
    echo 'FAIL: prohibited proprietary module, metapackage or nvcc found' >&2
    exit 1
fi
if grep -Eq 'squashfs-root/var/lib/dkms/mok\.(key|pub)( |$)' "$WORK/files"; then
    echo 'FAIL: DKMS signing key found' >&2
    exit 1
fi
for module in nvidia nvidia-modeset nvidia-uvm nvidia-drm; do
    grep -Eq "squashfs-root/(usr/)?lib/modules/6[.]12[.][^/]+/updates/dkms/${module}[.]ko([.](xz|zst|gz))?$" "$WORK/files"
done
grep -q 'squashfs-root/usr/sbin/lightlix-gpu-check$' "$WORK/files"
for pkg in nvidia-driver-cuda nvidia-kernel-open-dkms firmware-nvidia-gsp "cuda-cudart-$CUDA_PKG_SUFFIX" "libcublas-$CUDA_PKG_SUFFIX"; do
    unsquashfs -cat "$WORK/rootfs.squashfs" "usr/share/lightlix/licenses/$pkg.copyright" > "$WORK/license"
    [[ -s "$WORK/license" ]]
done
# 네트워크: Intel Wi-Fi 펌웨어(AX200 은 iwlwifi-cc-a0-*.ucode), Intel 이더넷 펌웨어(ice.pkg 는 심볼릭 링크),
# iwd 실행 파일과 활성화 링크, 무선 networkd 설정, wireless-regdb regulatory.db(alternatives 심볼릭 링크)를 확인한다.
require_path() {
    local pattern="$1" label="$2"
    grep -Eq "$pattern" "$WORK/files" || { echo "FAIL: missing image item: $label" >&2; exit 1; }
}
# 데몬과 제어 소켓, 기본 방화벽이 이미지에서 실제 활성화되는지 확인한다.
require_path 'squashfs-root/usr/sbin/lightlixd$' 'lightlixd binary'
require_path 'squashfs-root/usr/bin/lightlix$' 'lightlix CLI binary'
require_path 'squashfs-root/usr/lib/lightlix/check-gpu-permissions$' 'GPU permission check'
require_path 'squashfs-root/usr/lib/lightlix/lightlix-user$' 'account helper (lightlix-user)'
require_path 'squashfs-root/(usr/)?lib/live/config/1190-lightlix-groups$' 'live user group hook'
require_path 'squashfs-root/usr/lib/sysusers[.]d/lightlix[.]conf$' 'daemon sysusers configuration'
require_path 'squashfs-root/usr/lib/tmpfiles[.]d/lightlix-daemon[.]conf$' 'daemon tmpfiles configuration'
require_path 'squashfs-root/usr/share/lightlix/firewall-default[.]nft$' 'default firewall rules'
for entry in 'multi-user.target lightlixd.service' 'sockets.target lightlixd.socket' 'sysinit.target lightlix-firewall-default.service' 'sysinit.target lightlix-firewall.service'; do
    read -r target unit <<< "$entry"
    require_path "squashfs-root/etc/systemd/system/${target//./[.]}.wants/${unit//./[.]} -> /(usr/)?lib/systemd/system/${unit//./[.]}\$" "$unit enable link"
done
unsquashfs -cat "$WORK/rootfs.squashfs" etc/group > "$WORK/group"
grep -Eq '^lightlix:[^:]*:[0-9]+:' "$WORK/group"
grep -Eq '^lightlix-llm:[^:]*:[0-9]+:' "$WORK/group"
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/tmpfiles.d/lightlix-daemon.conf > "$WORK/daemon-tmpfiles"
grep -qx 'd /var/lib/lightlix 0750 root lightlix-llm -' "$WORK/daemon-tmpfiles"
grep -qx 'd /var/lib/lightlix/models 2750 root lightlix-llm -' "$WORK/daemon-tmpfiles"
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/system/lightlix-llama.service > "$WORK/llama-unit"
grep -qx 'ExecStartPre=/usr/lib/lightlix/check-gpu-permissions' "$WORK/llama-unit"
grep -qx 'SupplementaryGroups=video render' "$WORK/llama-unit"
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/system/lightlix-firewall-default.service > "$WORK/firewall-unit"
if grep -q '^Condition' "$WORK/firewall-unit"; then
    echo 'FAIL: default firewall must apply even when saved rules exist' >&2
    exit 1
fi
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/system/lightlix-firewall.service > "$WORK/firewall-unit"
grep -qx 'Requires=lightlix-firewall-default.service' "$WORK/firewall-unit"
grep -qx 'After=local-fs.target lightlix-firewall-default.service' "$WORK/firewall-unit"
echo 'PASS: daemon, CLI, enabled units, account permissions and firewall ordering'
LLAMA_ROOT="${LLAMA_INSTALL_DIR#/}"
require_path "squashfs-root/${LLAMA_ROOT}/llama-server\$" 'llama-server binary (lightlix-llm)'
require_path "squashfs-root/${LLAMA_ROOT}/libggml-cuda[.]so\$" 'CUDA backend libggml-cuda.so (lightlix-llm)'
require_path "squashfs-root/${LLAMA_ROOT}/libggml-cpu-[^/]+[.]so\$" 'CPU backend libggml-cpu-*.so (lightlix-llm)'
require_path 'squashfs-root/(usr/)?lib/firmware/iwlwifi-cc-a0-[0-9]+[.]ucode$' 'AX200 Wi-Fi firmware (iwlwifi-cc-a0-*.ucode)'
require_path 'squashfs-root/(usr/)?lib/firmware/intel/ice/ddp/ice[.]pkg( |$)' 'Intel Ethernet firmware (intel/ice/ddp/ice.pkg)'
require_path 'squashfs-root/(usr/)?libexec/iwd$' 'iwd binary (/usr/libexec/iwd)'
# iwd 는 D-Bus 시스템 버스가 있어야 뜬다. dbus 는 직접 적지 않고 systemd-resolved 의 Depends 로 들어오므로 여기서 지킨다.
require_path 'squashfs-root/(usr/)?bin/dbus-daemon$' 'D-Bus system bus (dbus-daemon, required by iwd)'
require_path 'squashfs-root/etc/systemd/system/multi-user[.]target[.]wants/iwd[.]service -> /lib/systemd/system/iwd[.]service$' 'iwd service enable link'
require_path 'squashfs-root/etc/systemd/network/25-lightlix-wireless[.]network$' 'wireless networkd configuration (25-lightlix-wireless.network)'
require_path 'squashfs-root/(usr/)?lib/firmware/regulatory[.]db -> ' 'wireless-regdb regulatory.db'
require_path 'squashfs-root/(usr/)?lib/firmware/regulatory[.]db-debian$' 'wireless-regdb regulatory.db-debian'
echo 'PASS: network firmware, iwd and enable link, networkd configuration and regulatory.db found'

# 이미지 구성 검사(리뷰 M3, M4, minor 6, 12, 14 와 파티션 테이블)
check_fail() { echo "FAIL: $*" >&2; exit 1; }
installed_names() {
    awk 'BEGIN { RS=""; FS="\n" }
        /Status: (install|hold) ok installed/ {
            for (i = 1; i <= NF; i++) if ($i ~ /^Package: /) print substr($i, 10)
        }' "$WORK/status"
}
installed_names > "$WORK/installed"
grep -qx nftables "$WORK/installed" || check_fail 'nftables is not installed'

# DHCP 클라이언트는 systemd-networkd 하나만 쓴다(M3).
for pkg in ifupdown dhcpcd-base; do
    grep -qx "$pkg" "$WORK/installed" && check_fail "$pkg is installed (conflicts with systemd-networkd DHCP)"
done
echo 'PASS: ifupdown and dhcpcd-base are absent'

# 시계 동기화(minor 14). 설치만으로 sysinit.target.wants 링크가 생겨 켜진다.
grep -qx 'systemd-timesyncd' "$WORK/installed" || check_fail 'systemd-timesyncd is not installed'
require_path 'squashfs-root/etc/systemd/system/sysinit[.]target[.]wants/systemd-timesyncd[.]service -> /usr/lib/systemd/system/systemd-timesyncd[.]service$' 'systemd-timesyncd enable link'
echo 'PASS: systemd-timesyncd is installed and enabled'

# 라이브 세션 저널은 램에만 쓴다(M4).
require_path 'squashfs-root/etc/systemd/journald[.]conf[.]d/lightlix[.]conf$' 'journald drop-in (lightlix.conf)'
unsquashfs -cat "$WORK/rootfs.squashfs" etc/systemd/journald.conf.d/lightlix.conf > "$WORK/journald.conf"
grep -qx '\[Journal\]' "$WORK/journald.conf" || check_fail 'journald drop-in lacks [Journal]'
grep -qx 'Storage=volatile' "$WORK/journald.conf" || check_fail 'journald drop-in lacks Storage=volatile'
grep -qx 'RuntimeMaxUse=16M' "$WORK/journald.conf" || check_fail 'journald drop-in lacks RuntimeMaxUse=16M'
echo 'PASS: volatile journald storage (Storage=volatile, RuntimeMaxUse=16M)'

# apt 일일 타이머를 마스크한다(M4). 유닛은 apt 패키지가 제공한다(/usr/lib/systemd/system/apt-daily*.timer).
require_path 'squashfs-root/etc/systemd/system/apt-daily[.]timer -> /dev/null$' 'apt-daily.timer mask'
require_path 'squashfs-root/etc/systemd/system/apt-daily-upgrade[.]timer -> /dev/null$' 'apt-daily-upgrade.timer mask'
echo 'PASS: both apt-daily timers are masked'

# apt 목록을 이미지에 넣지 않는다(--apt-indices false), deb-src 를 활성화하지 않는다(--apt-source-archives false).
if grep -Eq 'squashfs-root/var/lib/apt/lists/[^ ]+_(Packages|Sources|Release|InRelease)$' "$WORK/files"; then
    check_fail 'apt package lists are present in the image'
fi
unsquashfs -d "$WORK/apt-etc" -f "$WORK/rootfs.squashfs" etc/apt >/dev/null 2>&1
[[ -d "$WORK/apt-etc/etc/apt" ]] || check_fail 'failed to extract etc/apt'
if grep -REq '^[[:space:]]*deb-src([[:space:]]|$)|^Types:.*deb-src' "$WORK/apt-etc/etc/apt"; then
    check_fail 'active deb-src entry found'
fi
echo 'PASS: apt lists are absent and deb-src is disabled'

# systemd-resolved 의 멀티캐스트 이름 해석을 끈다(minor 14).
require_path 'squashfs-root/etc/systemd/resolved[.]conf[.]d/lightlix[.]conf$' 'resolved drop-in (lightlix.conf)'
unsquashfs -cat "$WORK/rootfs.squashfs" etc/systemd/resolved.conf.d/lightlix.conf > "$WORK/resolved.conf"
grep -qx '\[Resolve\]' "$WORK/resolved.conf" || check_fail 'resolved drop-in lacks [Resolve]'
grep -qx 'LLMNR=no' "$WORK/resolved.conf" || check_fail 'resolved drop-in lacks LLMNR=no'
grep -qx 'MulticastDNS=no' "$WORK/resolved.conf" || check_fail 'resolved drop-in lacks MulticastDNS=no'
echo 'PASS: systemd-resolved LLMNR and MulticastDNS are disabled'

# 로그인 배너(minor 6). /etc/issue 첫 줄은 Lightlix 한 줄이고 agetty 이스케이프 \n \l 을 유지하며 ASCII 만 쓴다.
expected_banner="Lightlix ${LIGHTLIX_VERSION} (based on Debian ${DEBIAN_VERSION})"
unsquashfs -cat "$WORK/rootfs.squashfs" etc/issue > "$WORK/issue"
first_issue="$(head -n 1 "$WORK/issue")"
[[ "$first_issue" == "$expected_banner \\n \\l" ]] || check_fail "unexpected /etc/issue first line: $first_issue"
if printf '%s\n' "$first_issue" | LC_ALL=C grep -q '[^ -~]'; then
    check_fail 'non-ASCII character in /etc/issue first line'
fi
unsquashfs -cat "$WORK/rootfs.squashfs" etc/issue.net > "$WORK/issue.net"
[[ "$(head -n 1 "$WORK/issue.net")" == "$expected_banner" ]] || check_fail 'unexpected /etc/issue.net first line'
if LC_ALL=C grep -q '[^ -~]' "$WORK/issue.net"; then
    check_fail 'non-ASCII character in /etc/issue.net'
fi
echo "PASS: ASCII login banner: '$expected_banner'"

# 금지 패키지: CUDA 개발 도구와 -dev 패키지가 최종 이미지에 섞이면 안 된다(minor 12).
# cuda-toolkit-<숫자> 는 -config-common 으로 끝나는 것만 허용한다.
bad_cuda="$(grep -E '^(cuda-nvcc-|cuda-minimal-build-|cuda-compiler-|cuda-libraries-dev-|cuda-cudart-dev-|libcublas-dev-|cuda-driver-dev-)' "$WORK/installed" || true)"
bad_toolkit="$(grep -E '^cuda-toolkit-[0-9]' "$WORK/installed" | grep -v -E -- '-config-common$' || true)"
if [[ -n "$bad_cuda$bad_toolkit" ]]; then
    check_fail "CUDA development package found: $(printf '%s %s' "$bad_cuda" "$bad_toolkit")"
fi
echo 'PASS: CUDA development tools and -dev packages are absent'

# 파티션 테이블: ISO 에 GPT 와 EFI System 파티션이 있다(sfdisk 는 util-linux 가 아니라 fdisk 패키지에 있다).
sfdisk -d "$1" > "$WORK/ptable" 2>/dev/null || check_fail 'sfdisk could not read the partition table'
grep -qx 'label: gpt' "$WORK/ptable" || check_fail 'GPT is missing from the ISO'
grep -qi 'type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B' "$WORK/ptable" || check_fail 'EFI System partition is missing from GPT'
echo 'PASS: GPT and EFI System partition found'
# Phase 3: 데스크톱 구성 요소는 설치하되 기본 부팅 대상은 서버 모드(multi-user.target)로 둔다.
check_package lightlix-desktop "$LIGHTLIX_VERSION"
check_package lightlix-control-center "$LIGHTLIX_VERSION"
for pkg in python3-gi gir1.2-gtk-4.0 gir1.2-adw-1 libadwaita-1-0; do
    grep -qx "$pkg" "$WORK/installed" || check_fail "$pkg is not installed"
done
require_path 'squashfs-root/usr/sbin/lightlix-mok$' 'Secure Boot machine key helper (lightlix-mok)'
grep -qx mokutil "$WORK/installed" || check_fail 'mokutil is not installed'
require_path 'squashfs-root/usr/bin/lightlix-control-center$' 'Control Center launcher'
require_path 'squashfs-root/usr/lib/lightlix/control-center/lightlix_cc/app[.]py$' 'Control Center application'
require_path 'squashfs-root/usr/share/locale/ko/LC_MESSAGES/lightlix-control-center[.]mo$' 'Control Center Korean catalog'
for pkg in xfce4-session xfwm4 xfce4-panel xfdesktop4 thunar xfce4-terminal lightdm lightdm-gtk-greeter xserver-xorg-core \
    pipewire wireplumber firefox-esr fcitx5-hangul fonts-nanum openssh-server; do
    grep -qx "$pkg" "$WORK/installed" || check_fail "$pkg is not installed"
done
# 0.5.0 에서 교체한 Wayland 구성과 무거운 데스크톱 구성 요소는 넣지 않는다.
for pkg in labwc greetd tuigreet foot yambar swaybg waybar gnome-shell plasma-desktop gdm3 sddm; do
    grep -qx "$pkg" "$WORK/installed" && check_fail "$pkg must not be installed"
done
require_path 'squashfs-root/etc/xdg/lightlix/xfce4/panel/default[.]xml$' 'Xfce panel layout'
require_path 'squashfs-root/etc/xdg/lightlix/xfce4/xfconf/xfce-perchannel-xml/xsettings[.]xml$' 'Xfce appearance defaults'
require_path 'squashfs-root/etc/X11/Xsession[.]d/55lightlix-xdg$' 'XDG_CONFIG_DIRS fragment'
require_path 'squashfs-root/usr/share/themes/Lightlix/gtk-3[.]0/gtk[.]css$' 'GTK theme Lightlix'
require_path 'squashfs-root/usr/share/themes/Lightlix/xfwm4/themerc$' 'xfwm4 theme Lightlix'
require_path 'squashfs-root/usr/share/lightdm/lightdm[.]conf[.]d/50-lightlix[.]conf$' 'lightdm configuration'
require_path 'squashfs-root/usr/share/lightdm/lightdm-gtk-greeter[.]conf[.]d/50-lightlix[.]conf$' 'greeter configuration'
# 라이브 자동 로그인은 live-config 가 부팅 때 /etc/lightdm/lightdm.conf 에 쓴다. 이미지의 파일에는 자동 로그인이 켜져 있으면 안 된다
# (설치된 시스템은 이 파일을 그대로 복사한다).
unsquashfs -cat "$WORK/rootfs.squashfs" etc/lightdm/lightdm.conf > "$WORK/lightdm.conf"
if grep -Eq '^[[:space:]]*autologin-user=.+' "$WORK/lightdm.conf"; then
    check_fail 'autologin is enabled in the image lightdm.conf'
fi
# 기본 부팅 대상이 graphical.target 이면 부팅하자마자 데스크톱이 떠서 서버 모드의 램 목표를 깨뜨린다.
if grep -Eq 'squashfs-root/etc/systemd/system/default[.]target -> .*graphical[.]target$' "$WORK/files"; then
    check_fail 'default.target is graphical.target; the image must boot in server mode'
fi
echo 'PASS: Xfce desktop packages and Lightlix configuration found; no Wayland stack or heavy desktop installed'

# SSH 서버는 설치하되 꺼 두고, 호스트 키는 이미지에 없어야 한다.
if grep -Eq 'squashfs-root/etc/ssh/ssh_host_' "$WORK/files"; then
    check_fail 'SSH host keys are baked into the image'
fi
for unit in ssh.service ssh.socket; do
    if grep -Eq "squashfs-root/etc/systemd/system/(multi-user|sockets)[.]target[.]wants/${unit//./[.]}( |\$)" "$WORK/files"; then
        check_fail "$unit is enabled in the image"
    fi
done
echo 'PASS: SSH server is installed but disabled, and no host keys are baked into the image'
echo 'PASS: open modules, licenses and GPU diagnostics found; proprietary modules and build signing keys absent'
