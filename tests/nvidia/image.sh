#!/usr/bin/env bash
# 완성된 ISO 의 squashfs 를 읽어 드라이버 구성과 키 제거가 실제 산출물에 반영됐는지 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: image check at line $LINENO: $BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/opereta.conf"
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
grep -qx "ID=$OPERETA_ID" "$WORK/os-release"
grep -Fqx -e "VERSION_ID=$OPERETA_VERSION" -e "VERSION_ID=\"$OPERETA_VERSION\"" "$WORK/os-release"

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
check_package opereta-nvidia "$OPERETA_VERSION"
check_package opereta-llm "$OPERETA_VERSION"
check_package opereta-daemon "$OPERETA_VERSION"
check_package cuda-keyring "$CUDA_KEYRING_VERSION"
check_package "nvidia-driver-pinning-$NVIDIA_BRANCH" "$NVIDIA_PIN_VERSION"
# CUDA 런타임은 이미지에 없다. NVIDIA CUDA EULA 가 재배포를 응용 전용으로만 허용하므로 사용자의 컴퓨터가 NVIDIA 저장소에서
# 직접 받아 설치한다(opereta gpu runtime install). 패키지도 파일도 없어야 하고, 받을 때 쓸 고정 버전은 설정에 있어야 한다.
for pkg in "cuda-cudart-$CUDA_PKG_SUFFIX" "libcublas-$CUDA_PKG_SUFFIX"; do
    if awk -v pkg="$pkg" 'BEGIN { RS="" } $0 ~ ("Package: " pkg "\n") && /Status: (install|hold) ok installed/ { found=1 } END { exit !found }' "$WORK/status"; then
        echo "FAIL: CUDA runtime is in the image: $pkg" >&2
        exit 1
    fi
done
cuda_files="$(grep -E 'lib(cudart|cublas|cublasLt)[.]so' "$WORK/files" || true)"
if [[ -n "$cuda_files" ]]; then
    echo "FAIL: CUDA runtime library files are in the image:" >&2
    echo "$cuda_files" | head -n 5 >&2
    exit 1
fi
unsquashfs -cat "$WORK/rootfs.squashfs" usr/share/opereta/nvidia.conf | grep -q "^CUDA_CUDART_VERSION=\"$CUDA_CUDART_VERSION\"" || { echo 'FAIL: nvidia.conf lacks the pinned CUDA runtime version' >&2; exit 1; }
echo 'PASS: the CUDA runtime is not in the image; its pinned version is recorded for the download'

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
grep -q 'squashfs-root/usr/sbin/opereta-gpu-check$' "$WORK/files"
for pkg in nvidia-driver-cuda nvidia-kernel-open-dkms firmware-nvidia-gsp; do
    unsquashfs -cat "$WORK/rootfs.squashfs" "usr/share/opereta/licenses/$pkg.copyright" > "$WORK/license"
    [[ -s "$WORK/license" ]]
done
# 재배포 조건이 "Intel 승인 제품과 함께"로 한정된 ice 특수 프로파일 두 개는 이미지에 없고, 기본 프로파일(ice-*.pkg)은 있다.
if grep -E 'intel/ice/ddp-(comms|wireless_edge)/' "$WORK/files" | grep -q -E 'ice_(comms|wireless_edge)-'; then
    echo 'FAIL: Intel ice comms/wireless_edge firmware is in the image' >&2
    exit 1
fi
grep -Eq 'intel/ice/ddp/ice-[0-9.]+[.]pkg' "$WORK/files" || { echo 'FAIL: the default Intel ice firmware profile is missing' >&2; exit 1; }
unsquashfs -cat "$WORK/rootfs.squashfs" etc/dpkg/dpkg.cfg.d/50-opereta-firmware | grep -q 'path-exclude=/usr/lib/firmware/intel/ice/ddp-comms' || { echo 'FAIL: the dpkg path-exclude for Intel ice firmware is missing' >&2; exit 1; }
echo 'PASS: Intel ice comms/wireless_edge firmware is excluded; the default profile is kept'
# 네트워크: Intel Wi-Fi 펌웨어(AX200 은 iwlwifi-cc-a0-*.ucode), Intel 이더넷 펌웨어(ice.pkg 는 심볼릭 링크),
# iwd 실행 파일과 활성화 링크, 무선 networkd 설정, wireless-regdb regulatory.db(alternatives 심볼릭 링크)를 확인한다.
require_path() {
    local pattern="$1" label="$2"
    grep -Eq "$pattern" "$WORK/files" || { echo "FAIL: missing image item: $label" >&2; exit 1; }
}
# 데몬과 제어 소켓, 기본 방화벽이 이미지에서 실제 활성화되는지 확인한다.
require_path 'squashfs-root/usr/sbin/operetad$' 'operetad binary'
require_path 'squashfs-root/usr/bin/opereta$' 'opereta CLI binary'
require_path 'squashfs-root/usr/lib/opereta/check-gpu-permissions$' 'GPU permission check'
require_path 'squashfs-root/usr/lib/opereta/opereta-user$' 'account helper (opereta-user)'
require_path 'squashfs-root/(usr/)?lib/live/config/1190-opereta-groups$' 'live user group hook'
require_path 'squashfs-root/usr/lib/sysusers[.]d/opereta[.]conf$' 'daemon sysusers configuration'
require_path 'squashfs-root/usr/lib/tmpfiles[.]d/opereta-daemon[.]conf$' 'daemon tmpfiles configuration'
require_path 'squashfs-root/usr/share/opereta/firewall-default[.]nft$' 'default firewall rules'
for entry in 'multi-user.target operetad.service' 'sockets.target operetad.socket' 'sysinit.target opereta-firewall-default.service' 'sysinit.target opereta-firewall.service' 'timers.target opereta-deberopa-update.timer'; do
    read -r target unit <<< "$entry"
    require_path "squashfs-root/etc/systemd/system/${target//./[.]}.wants/${unit//./[.]} -> /(usr/)?lib/systemd/system/${unit//./[.]}\$" "$unit enable link"
done
unsquashfs -cat "$WORK/rootfs.squashfs" etc/group > "$WORK/group"
grep -Eq '^opereta:[^:]*:[0-9]+:' "$WORK/group"
grep -Eq '^opereta-llm:[^:]*:[0-9]+:' "$WORK/group"
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/tmpfiles.d/opereta-daemon.conf > "$WORK/daemon-tmpfiles"
grep -qx 'd /var/lib/opereta 0750 root opereta-llm -' "$WORK/daemon-tmpfiles"
grep -qx 'd /var/lib/opereta/models 2750 root opereta-llm -' "$WORK/daemon-tmpfiles"
grep -qx 'd /usr/local/lib/opereta/deberopa 0755 root root -' "$WORK/daemon-tmpfiles"
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/system/opereta-llama.service > "$WORK/llama-unit"
grep -qx 'ExecStartPre=/usr/lib/opereta/check-gpu-permissions' "$WORK/llama-unit"
grep -qx 'SupplementaryGroups=video render' "$WORK/llama-unit"
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/system/opereta-firewall-default.service > "$WORK/firewall-unit"
if grep -q '^Condition' "$WORK/firewall-unit"; then
    echo 'FAIL: default firewall must apply even when saved rules exist' >&2
    exit 1
fi
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/system/opereta-firewall.service > "$WORK/firewall-unit"
grep -qx 'Requires=opereta-firewall-default.service' "$WORK/firewall-unit"
grep -qx 'After=local-fs.target opereta-firewall-default.service' "$WORK/firewall-unit"
echo 'PASS: daemon, CLI, enabled units, account permissions and firewall ordering'
LLAMA_ROOT="${LLAMA_INSTALL_DIR#/}"
require_path "squashfs-root/${LLAMA_ROOT}/llama-server\$" 'llama-server binary (opereta-llm)'
require_path "squashfs-root/${LLAMA_ROOT}/libggml-cuda[.]so\$" 'CUDA backend libggml-cuda.so (opereta-llm)'
require_path "squashfs-root/${LLAMA_ROOT}/libggml-cpu-[^/]+[.]so\$" 'CPU backend libggml-cpu-*.so (opereta-llm)'
require_path 'squashfs-root/(usr/)?lib/firmware/iwlwifi-cc-a0-[0-9]+[.]ucode$' 'AX200 Wi-Fi firmware (iwlwifi-cc-a0-*.ucode)'
require_path 'squashfs-root/(usr/)?lib/firmware/intel/ice/ddp/ice[.]pkg( |$)' 'Intel Ethernet firmware (intel/ice/ddp/ice.pkg)'
require_path 'squashfs-root/(usr/)?libexec/iwd$' 'iwd binary (/usr/libexec/iwd)'
# iwd 는 D-Bus 시스템 버스가 있어야 뜬다. dbus 는 직접 적지 않고 systemd-resolved 의 Depends 로 들어오므로 여기서 지킨다.
require_path 'squashfs-root/(usr/)?bin/dbus-daemon$' 'D-Bus system bus (dbus-daemon, required by iwd)'
require_path 'squashfs-root/etc/systemd/system/multi-user[.]target[.]wants/iwd[.]service -> /lib/systemd/system/iwd[.]service$' 'iwd service enable link'
require_path 'squashfs-root/etc/systemd/network/25-opereta-wireless[.]network$' 'wireless networkd configuration (25-opereta-wireless.network)'
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
require_path 'squashfs-root/etc/systemd/journald[.]conf[.]d/opereta[.]conf$' 'journald drop-in (opereta.conf)'
unsquashfs -cat "$WORK/rootfs.squashfs" etc/systemd/journald.conf.d/opereta.conf > "$WORK/journald.conf"
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
require_path 'squashfs-root/etc/systemd/resolved[.]conf[.]d/opereta[.]conf$' 'resolved drop-in (opereta.conf)'
unsquashfs -cat "$WORK/rootfs.squashfs" etc/systemd/resolved.conf.d/opereta.conf > "$WORK/resolved.conf"
grep -qx '\[Resolve\]' "$WORK/resolved.conf" || check_fail 'resolved drop-in lacks [Resolve]'
grep -qx 'LLMNR=no' "$WORK/resolved.conf" || check_fail 'resolved drop-in lacks LLMNR=no'
grep -qx 'MulticastDNS=yes' "$WORK/resolved.conf" || check_fail 'resolved drop-in lacks MulticastDNS=yes'
echo 'PASS: systemd-resolved has LLMNR off and mDNS on (the firewall opens 5353 only for LAN exposure)'

# 로그인 배너(minor 6). /etc/issue 첫 줄은 Opereta 한 줄이고 agetty 이스케이프 \n \l 을 유지하며 ASCII 만 쓴다.
expected_banner="Opereta ${OPERETA_VERSION} (based on Debian ${DEBIAN_VERSION})"
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

# Opereta 업데이트 저장소 소스는 이미지를 만드는 동안 apt 가 보지 않도록 /etc 에 두지 않고 템플릿과 tmpfiles 로 첫 부팅 때 만든다.
grep -Eq 'squashfs-root/usr/share/opereta/apt/opereta[.]sources$' "$WORK/files" || { echo 'FAIL: the Opereta apt source template is missing' >&2; exit 1; }
grep -Eq 'squashfs-root/usr/lib/tmpfiles[.]d/opereta-apt[.]conf$' "$WORK/files" || { echo 'FAIL: the tmpfiles rule for the apt source is missing' >&2; exit 1; }
if grep -Eq 'squashfs-root/etc/apt/sources[.]list[.]d/opereta[.]sources$' "$WORK/files"; then
    echo 'FAIL: the Opereta apt source is installed in /etc during the image build' >&2
    exit 1
fi
echo 'PASS: the Opereta apt source is created at first boot, not during the image build'

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
check_package opereta-desktop "$OPERETA_VERSION"
check_package opereta-control-center "$OPERETA_VERSION"
# 0.5.3: 고정 버전의 Deberopa 실행 파일과 에이전트용 스킬이 들어 있다.
check_package opereta-deberopa "$OPERETA_VERSION"
require_path 'squashfs-root/usr/bin/deberopa$' 'Deberopa executable'
require_path 'squashfs-root/usr/share/opereta/deberopa-version$' 'Deberopa version marker'
require_path 'squashfs-root/usr/share/opereta/skills/opereta/SKILL[.]md$' 'Opereta skill for coding agents'
for pkg in python3-gi gir1.2-gtk-4.0 gir1.2-adw-1 libadwaita-1-0; do
    grep -qx "$pkg" "$WORK/installed" || check_fail "$pkg is not installed"
done
require_path 'squashfs-root/usr/sbin/opereta-mok$' 'Secure Boot machine key helper (opereta-mok)'
grep -qx mokutil "$WORK/installed" || check_fail 'mokutil is not installed'
require_path 'squashfs-root/usr/bin/opereta-control-center$' 'Control Center launcher'
require_path 'squashfs-root/usr/lib/opereta/control-center/opereta_cc/app[.]py$' 'Control Center application'
require_path 'squashfs-root/usr/share/locale/ko/LC_MESSAGES/opereta-control-center[.]mo$' 'Control Center Korean catalog'
for pkg in xfce4-session xfwm4 xfce4-panel xfdesktop4 thunar xfce4-terminal lightdm lightdm-gtk-greeter xserver-xorg-core \
    pipewire wireplumber firefox-esr fcitx5-hangul fonts-nanum fonts-pretendard openssh-server os-prober efibootmgr picom plank papirus-icon-theme xfce4-windowck-plugin bluez; do
    grep -qx "$pkg" "$WORK/installed" || check_fail "$pkg is not installed"
done
# 0.5.0 에서 교체한 Wayland 구성과 무거운 데스크톱 구성 요소는 넣지 않는다.
for pkg in labwc greetd tuigreet foot yambar swaybg waybar gnome-shell plasma-desktop gdm3 sddm; do
    grep -qx "$pkg" "$WORK/installed" && check_fail "$pkg must not be installed"
done
require_path 'squashfs-root/etc/xdg/opereta/xfce4/panel/default[.]xml$' 'Xfce panel layout'
require_path 'squashfs-root/etc/xdg/opereta/xfce4/xfconf/xfce-perchannel-xml/xsettings[.]xml$' 'Xfce appearance defaults'
require_path 'squashfs-root/etc/X11/Xsession[.]d/55opereta-xdg$' 'XDG_CONFIG_DIRS fragment'
require_path 'squashfs-root/usr/share/themes/Opereta/gtk-3[.]0/gtk[.]css$' 'GTK theme Opereta'
require_path 'squashfs-root/usr/share/themes/Opereta/xfwm4/themerc$' 'xfwm4 theme Opereta'
require_path 'squashfs-root/usr/share/themes/Opereta-Dark/gtk-3[.]0/gtk[.]css$' 'GTK theme Opereta-Dark'
require_path 'squashfs-root/usr/share/themes/Opereta-Dark/xfwm4/themerc$' 'xfwm4 theme Opereta-Dark'
require_path 'squashfs-root/usr/share/icons/Opereta/index[.]theme$' 'icon theme Opereta (window controls)'
require_path 'squashfs-root/etc/fonts/conf[.]d/60-opereta-pretendard[.]conf$' 'Pretendard fontconfig alias'
require_path 'squashfs-root/usr/share/lightdm/lightdm[.]conf[.]d/50-opereta[.]conf$' 'lightdm configuration'
require_path 'squashfs-root/usr/share/lightdm/lightdm-gtk-greeter[.]conf[.]d/50-opereta[.]conf$' 'greeter configuration'
# 라이브 자동 로그인은 live-config 가 부팅 때 /etc/lightdm/lightdm.conf 에 쓴다. 이미지의 파일에는 자동 로그인이 켜져 있으면 안 된다
# (설치된 시스템은 이 파일을 그대로 복사한다).
unsquashfs -cat "$WORK/rootfs.squashfs" etc/lightdm/lightdm.conf > "$WORK/lightdm.conf"
if grep -Eq '^[[:space:]]*autologin-user=.+' "$WORK/lightdm.conf"; then
    check_fail 'autologin is enabled in the image lightdm.conf'
fi
# 상시 가동: 덮개를 닫거나 오래 두어도 절전과 종료로 서버가 멈추지 않게 하는 systemd 설정이 이미지에 있다.
require_path 'squashfs-root/(usr/)?lib/systemd/logind[.]conf[.]d/50-opereta-always-on[.]conf$' 'always-on logind drop-in'
require_path 'squashfs-root/(usr/)?lib/systemd/sleep[.]conf[.]d/50-opereta-always-on[.]conf$' 'always-on sleep drop-in'
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/logind.conf.d/50-opereta-always-on.conf > "$WORK/always-on-logind"
for key in HandleLidSwitch HandleLidSwitchExternalPower HandleLidSwitchDocked HandleSuspendKey HandleHibernateKey IdleAction; do
    grep -qx "$key=ignore" "$WORK/always-on-logind" || check_fail "always-on logind drop-in does not set $key=ignore"
done
unsquashfs -cat "$WORK/rootfs.squashfs" usr/lib/systemd/sleep.conf.d/50-opereta-always-on.conf > "$WORK/always-on-sleep"
for key in AllowSuspend AllowHibernation AllowSuspendThenHibernate AllowHybridSleep; do
    grep -qx "$key=no" "$WORK/always-on-sleep" || check_fail "always-on sleep drop-in does not set $key=no"
done
echo 'PASS: always-on drop-ins are in the image'

# 0.5.1 부터 옛 이름의 흔적이 없어야 한다(0.6.0 에서 이전 이름이 둘이 되었다). 패턴은 조각으로 나누어 이 파일 자신이 걸리지 않게 한다.
legacy="light""lix\|dipar""nux"
if grep -i -q -- "$legacy" "$WORK/files"; then
    echo "FAIL: the image contains paths with the old name:" >&2
    grep -i -- "$legacy" "$WORK/files" | head -n 10 >&2
    exit 1
fi
if grep -i -q -- "$legacy" "$WORK/status"; then
    check_fail 'the dpkg status database mentions the old name'
fi
rm -rf "$WORK/legacy-check"
unsquashfs -d "$WORK/legacy-check" -f "$WORK/rootfs.squashfs" etc usr/lib/systemd usr/lib/os-release usr/share/applications usr/share/opereta usr/lib/opereta usr/bin usr/sbin >/dev/null 2>&1 || true
if grep -r -a -i -l -- "$legacy" "$WORK/legacy-check" 2>/dev/null | head -n 5 | grep -q .; then
    echo "FAIL: image files contain the old name:" >&2
    grep -r -a -i -l -- "$legacy" "$WORK/legacy-check" | head -n 10 >&2
    exit 1
fi
echo 'PASS: no trace of the old name in the image paths, package database and configuration'
# 기본 부팅 대상이 graphical.target 이면 부팅하자마자 데스크톱이 떠서 서버 모드의 램 목표를 깨뜨린다.
if grep -Eq 'squashfs-root/etc/systemd/system/default[.]target -> .*graphical[.]target$' "$WORK/files"; then
    check_fail 'default.target is graphical.target; the image must boot in server mode'
fi
echo 'PASS: Xfce desktop packages and Opereta configuration found; no Wayland stack or heavy desktop installed'

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
