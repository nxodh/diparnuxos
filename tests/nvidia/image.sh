#!/usr/bin/env bash
# 완성된 ISO 의 squashfs 를 읽어 드라이버 구성과 키 제거가 실제 산출물에 반영됐는지 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: 이미지 검사 $LINENO 행: $BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/lightlix.conf"
[[ $# == 1 && -f "$1" ]] || { echo 'usage: image.sh <iso>' >&2; exit 2; }
for tool in xorriso unsquashfs sfdisk; do
    command -v "$tool" >/dev/null || { echo "ERROR: $tool 가 없다" >&2; exit 2; }
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
        END { exit !found }' "$WORK/status" || { echo "FAIL: $pkg=$version 패키지가 없다" >&2; exit 1; }
    echo "PASS: $pkg=$version"
}
driver="${NVIDIA_DRIVER_VERSION}-${NVIDIA_DEB_REVISION}"
for pkg in nvidia-kernel-open-dkms nvidia-driver nvidia-driver-cuda firmware-nvidia-gsp nvidia-persistenced; do
    check_package "$pkg" "$driver"
done
check_package lightlix-nvidia "$LIGHTLIX_VERSION"
check_package cuda-keyring "$CUDA_KEYRING_VERSION"
check_package "nvidia-driver-pinning-$NVIDIA_BRANCH" "$NVIDIA_PIN_VERSION"
check_package "cuda-cudart-$CUDA_PKG_SUFFIX" "$CUDA_CUDART_VERSION"
check_package "libcublas-$CUDA_PKG_SUFFIX" "$CUDA_CUBLAS_VERSION"

if awk 'BEGIN { RS="" } /Package: (nvidia-kernel-dkms|nvidia-kernel-source|cuda-drivers[^\n]*|nvidia-open|cuda-nvcc[^\n]*)\n/ && /Status: (install|hold) ok installed/ { bad=1 } END { exit !bad }' "$WORK/status"; then
    echo 'FAIL: 금지된 독점 모듈, 메타패키지 또는 nvcc 가 있다' >&2
    exit 1
fi
if grep -Eq 'squashfs-root/var/lib/dkms/mok\.(key|pub)( |$)' "$WORK/files"; then
    echo 'FAIL: DKMS 서명 키가 있다' >&2
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
# iwd 실행 파일과 활성화 링크, 무선 networkd 설정, wireless-regdb 의 regulatory.db(alternatives 심볼릭 링크)를 확인한다.
require_path() {
    local pattern="$1" label="$2"
    grep -Eq "$pattern" "$WORK/files" || { echo "FAIL: 구성 항목이 없다: $label" >&2; exit 1; }
}
require_path 'squashfs-root/(usr/)?lib/firmware/iwlwifi-cc-a0-[0-9]+[.]ucode$' 'AX200 Wi-Fi 펌웨어 (iwlwifi-cc-a0-*.ucode)'
require_path 'squashfs-root/(usr/)?lib/firmware/intel/ice/ddp/ice[.]pkg( |$)' 'Intel 이더넷 펌웨어 (intel/ice/ddp/ice.pkg)'
require_path 'squashfs-root/(usr/)?libexec/iwd$' 'iwd 실행 파일 (/usr/libexec/iwd)'
# iwd 는 D-Bus 시스템 버스가 있어야 뜬다. dbus 는 직접 적지 않고 systemd-resolved 의 Depends 로 들어오므로 여기서 지킨다.
require_path 'squashfs-root/(usr/)?bin/dbus-daemon$' 'D-Bus 시스템 버스 (dbus-daemon, iwd 가 필요로 한다)'
require_path 'squashfs-root/etc/systemd/system/multi-user[.]target[.]wants/iwd[.]service -> /lib/systemd/system/iwd[.]service$' 'iwd 서비스 활성화 링크'
require_path 'squashfs-root/etc/systemd/network/25-lightlix-wireless[.]network$' '무선 networkd 설정 (25-lightlix-wireless.network)'
require_path 'squashfs-root/(usr/)?lib/firmware/regulatory[.]db -> ' 'wireless-regdb 의 regulatory.db'
require_path 'squashfs-root/(usr/)?lib/firmware/regulatory[.]db-debian$' 'wireless-regdb 의 regulatory.db-debian'
echo 'PASS: 네트워크 항목이 있다 (Intel Wi-Fi 펌웨어, ice.pkg, iwd 와 활성화 링크, 무선 networkd 설정, regulatory.db)'

# 이미지 구성 검사(리뷰 M3, M4, minor 6, 12, 14 와 파티션 테이블)
check_fail() { echo "FAIL: $*" >&2; exit 1; }
installed_names() {
    awk 'BEGIN { RS=""; FS="\n" }
        /Status: (install|hold) ok installed/ {
            for (i = 1; i <= NF; i++) if ($i ~ /^Package: /) print substr($i, 10)
        }' "$WORK/status"
}
installed_names > "$WORK/installed"

# DHCP 클라이언트는 systemd-networkd 하나만 쓴다(M3).
for pkg in ifupdown dhcpcd-base; do
    grep -qx "$pkg" "$WORK/installed" && check_fail "$pkg 패키지가 이미지에 설치되어 있다(systemd-networkd 와 같은 NIC 를 두 클라이언트가 관리한다)"
done
echo 'PASS: ifupdown 과 dhcpcd-base 가 이미지에 없다'

# 시계 동기화(minor 14). 설치만으로 sysinit.target.wants 링크가 생겨 켜진다.
grep -qx 'systemd-timesyncd' "$WORK/installed" || check_fail 'systemd-timesyncd 가 설치되어 있지 않다'
require_path 'squashfs-root/etc/systemd/system/sysinit[.]target[.]wants/systemd-timesyncd[.]service -> /usr/lib/systemd/system/systemd-timesyncd[.]service$' 'systemd-timesyncd 자동 활성화 링크'
echo 'PASS: systemd-timesyncd 가 설치되어 자동 활성화 링크가 있다'

# 라이브 세션 저널은 램에만 쓴다(M4).
require_path 'squashfs-root/etc/systemd/journald[.]conf[.]d/lightlix[.]conf$' 'journald 드롭인 (lightlix.conf)'
unsquashfs -cat "$WORK/rootfs.squashfs" etc/systemd/journald.conf.d/lightlix.conf > "$WORK/journald.conf"
grep -qx '\[Journal\]' "$WORK/journald.conf" || check_fail 'journald 드롭인에 [Journal] 섹션이 없다'
grep -qx 'Storage=volatile' "$WORK/journald.conf" || check_fail 'journald 드롭인에 Storage=volatile 이 없다'
grep -qx 'RuntimeMaxUse=16M' "$WORK/journald.conf" || check_fail 'journald 드롭인에 RuntimeMaxUse=16M 이 없다'
echo 'PASS: journald 저널은 램에만 쓴다 (Storage=volatile, RuntimeMaxUse=16M)'

# apt 일일 타이머를 마스크한다(M4). 유닛은 apt 패키지가 제공한다(/usr/lib/systemd/system/apt-daily*.timer).
require_path 'squashfs-root/etc/systemd/system/apt-daily[.]timer -> /dev/null$' 'apt-daily.timer 마스크'
require_path 'squashfs-root/etc/systemd/system/apt-daily-upgrade[.]timer -> /dev/null$' 'apt-daily-upgrade.timer 마스크'
echo 'PASS: apt-daily 타이머 두 개가 마스크되어 있다'

# apt 목록을 이미지에 넣지 않는다(--apt-indices false), deb-src 를 활성화하지 않는다(--apt-source-archives false).
if grep -Eq 'squashfs-root/var/lib/apt/lists/[^ ]+_(Packages|Sources|Release|InRelease)$' "$WORK/files"; then
    check_fail 'apt 목록(_Packages 등)이 이미지에 있다'
fi
unsquashfs -d "$WORK/apt-etc" -f "$WORK/rootfs.squashfs" etc/apt >/dev/null 2>&1
[[ -d "$WORK/apt-etc/etc/apt" ]] || check_fail 'etc/apt 를 꺼내지 못했다'
if grep -REq '^[[:space:]]*deb-src([[:space:]]|$)|^Types:.*deb-src' "$WORK/apt-etc/etc/apt"; then
    check_fail '활성화된 deb-src 항목이 있다'
fi
echo 'PASS: apt 목록이 이미지에 없고 deb-src 가 활성화되어 있지 않다'

# systemd-resolved 의 멀티캐스트 이름 해석을 끈다(minor 14).
require_path 'squashfs-root/etc/systemd/resolved[.]conf[.]d/lightlix[.]conf$' 'resolved 드롭인 (lightlix.conf)'
unsquashfs -cat "$WORK/rootfs.squashfs" etc/systemd/resolved.conf.d/lightlix.conf > "$WORK/resolved.conf"
grep -qx '\[Resolve\]' "$WORK/resolved.conf" || check_fail 'resolved 드롭인에 [Resolve] 섹션이 없다'
grep -qx 'LLMNR=no' "$WORK/resolved.conf" || check_fail 'resolved 드롭인에 LLMNR=no 가 없다'
grep -qx 'MulticastDNS=no' "$WORK/resolved.conf" || check_fail 'resolved 드롭인에 MulticastDNS=no 가 없다'
echo 'PASS: systemd-resolved 의 LLMNR 과 MulticastDNS 가 꺼져 있다'

# 로그인 배너(minor 6). /etc/issue 첫 줄은 Lightlix 한 줄이고 agetty 이스케이프 \n \l 을 유지하며 ASCII 만 쓴다.
expected_banner="Lightlix ${LIGHTLIX_VERSION} (based on Debian ${DEBIAN_VERSION})"
unsquashfs -cat "$WORK/rootfs.squashfs" etc/issue > "$WORK/issue"
first_issue="$(head -n 1 "$WORK/issue")"
[[ "$first_issue" == "$expected_banner \\n \\l" ]] || check_fail "/etc/issue 첫 줄이 기대값과 다르다: $first_issue"
if printf '%s\n' "$first_issue" | LC_ALL=C grep -q '[^ -~]'; then
    check_fail '/etc/issue 첫 줄에 ASCII 가 아닌 문자가 있다'
fi
unsquashfs -cat "$WORK/rootfs.squashfs" etc/issue.net > "$WORK/issue.net"
[[ "$(head -n 1 "$WORK/issue.net")" == "$expected_banner" ]] || check_fail '/etc/issue.net 첫 줄이 기대값과 다르다'
if LC_ALL=C grep -q '[^ -~]' "$WORK/issue.net"; then
    check_fail '/etc/issue.net 에 ASCII 가 아닌 문자가 있다'
fi
echo "PASS: 로그인 배너 첫 줄이 '$expected_banner' 이고 ASCII 다"

# 금지 패키지: CUDA 개발 도구와 -dev 패키지가 최종 이미지에 섞이면 안 된다(minor 12).
# cuda-toolkit-<숫자> 는 -config-common 으로 끝나는 것만 허용한다.
bad_cuda="$(grep -E '^(cuda-nvcc-|cuda-minimal-build-|cuda-compiler-|cuda-libraries-dev-|cuda-cudart-dev-|libcublas-dev-|cuda-driver-dev-)' "$WORK/installed" || true)"
bad_toolkit="$(grep -E '^cuda-toolkit-[0-9]' "$WORK/installed" | grep -v -E -- '-config-common$' || true)"
if [[ -n "$bad_cuda$bad_toolkit" ]]; then
    check_fail "CUDA 개발 도구 패키지가 설치되어 있다: $(printf '%s %s' "$bad_cuda" "$bad_toolkit")"
fi
echo 'PASS: CUDA 개발 도구와 -dev 패키지가 설치되어 있지 않다'

# 파티션 테이블: ISO 에 GPT 와 EFI System 파티션이 있다(sfdisk 는 util-linux 가 아니라 fdisk 패키지에 있다).
sfdisk -d "$1" > "$WORK/ptable" 2>/dev/null || check_fail 'sfdisk 로 파티션 테이블을 읽지 못했다'
grep -qx 'label: gpt' "$WORK/ptable" || check_fail 'ISO 에 GPT 가 없다'
grep -qi 'type=C12A7328-F81F-11D2-BA4B-00A0C93EC93B' "$WORK/ptable" || check_fail 'GPT 에 EFI System 파티션이 없다'
echo 'PASS: ISO 에 GPT 와 EFI System 파티션이 있다'
echo 'PASS: 완성된 ISO 에 오픈 모듈, 라이선스 전문, GPU 진단이 있으며 독점 모듈과 빌드 서명 키는 없다'
