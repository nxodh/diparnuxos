#!/usr/bin/env bash
# 완성된 ISO 의 squashfs 를 읽어 드라이버 구성과 키 제거가 실제 산출물에 반영됐는지 확인한다.
set -Eeuo pipefail
trap 'echo "FAIL: 이미지 검사 $LINENO 행: $BASH_COMMAND" >&2' ERR
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
# shellcheck source=/dev/null
. "$ROOT/lightlix.conf"
[[ $# == 1 && -f "$1" ]] || { echo 'usage: image.sh <iso>' >&2; exit 2; }
for tool in xorriso unsquashfs; do
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
    grep -Eq "$pattern" "$WORK/files" || { echo "FAIL: 네트워크 항목이 없다: $label" >&2; exit 1; }
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
echo 'PASS: 완성된 ISO 에 오픈 모듈, 라이선스 전문, GPU 진단이 있으며 독점 모듈과 빌드 서명 키는 없다'
