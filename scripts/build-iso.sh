#!/usr/bin/env bash
# Debian 13 환경에서 root 로 Diparnux ISO 를 빌드한다.
# 순서: 작업 디렉터리로 복사 -> NVIDIA 저장소 준비 -> .deb 빌드 -> diparnux-llm .deb 복사 -> lb clean/config/build -> 검증 -> ESP 파티션 추가 -> out/ 복사.
# 저장소 안에서 lb build 를 돌리지 않는다. macOS Docker 의 바인드 마운트 위에서는 chroot 가 깨지기 때문이다.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
DIPARNUX_WORKDIR="${DIPARNUX_WORKDIR:-/var/tmp/diparnux-build}"
DIPARNUX_OUT="${DIPARNUX_OUT:-${ROOT}/out}"

die() {
    echo "build-iso: $*" >&2
    exit 1
}

if [ "$(id -u)" -ne 0 ]; then
    die "root 로 실행해야 한다 (live-build 는 chroot 와 mount 를 쓴다)."
fi

[ -r /etc/os-release ] || die "/etc/os-release 를 읽을 수 없다."
# shellcheck source=/dev/null
host_id="$(. /etc/os-release && echo "${ID:-}")"
# shellcheck source=/dev/null
host_version="$(. /etc/os-release && echo "${VERSION_ID:-}")"

[ -r "${ROOT}/diparnux.conf" ] || die "diparnux.conf 를 읽을 수 없다: ${ROOT}/diparnux.conf"
# shellcheck source=/dev/null
. "${ROOT}/diparnux.conf"

# diparnux-llm 은 make llama 가 만든 .deb 를 쓴다. 기본 위치는 build-in-docker.sh 가 마운트하는 /llama 이다.
# Debian 호스트에서 iso-native 로 빌드할 때는 DIPARNUX_LLAMA_DIR=<저장소>/out/llama 를 준다.
LLAMA_DIR="${DIPARNUX_LLAMA_DIR:-/llama}"
LLAMA_DEB="${LLAMA_DIR}/diparnux-llm_${DIPARNUX_VERSION}_amd64.deb"
[ -r "${LLAMA_DEB}" ] || die "diparnux-llm .deb not found: ${LLAMA_DEB}. Run 'make llama' first (or set DIPARNUX_LLAMA_DIR)."

if [ "${host_id}" != "debian" ] || [ "${host_version}" != "${DEBIAN_VERSION}" ]; then
    die "Debian ${DEBIAN_VERSION} 호스트에서만 빌드한다 (현재 ID=${host_id} VERSION_ID=${host_version})."
fi

for cmd in lb dpkg-buildpackage dpkg-parsechangelog sha256sum awk xorriso sfdisk mdir cmp msgfmt python3; do
    command -v "${cmd}" >/dev/null 2>&1 || die "${cmd} 가 없다 (build/README.md 의 빌더 도구 목록을 확인하라)."
done

tree="${DIPARNUX_WORKDIR}/tree"

# 이전 빌드의 chroot 에 mount 가 남아 있으면 rm -rf 가 호스트 파일시스템까지 건드릴 수 있으므로 멈춘다.
if grep -q -F " ${tree}" /proc/mounts; then
    die "이전 빌드의 mount 가 남아 있다: ${tree}. 먼저 lb clean --purge 로 정리하라."
fi

mkdir -p "${DIPARNUX_WORKDIR}"
rm -rf "${tree}"
mkdir -p "${tree}"
(
    cd "${ROOT}"
    tar --exclude=./.git --exclude=./out --exclude=./build/config/packages.chroot/'*.deb' -cf - .
) | (cd "${tree}" && tar -xf -)

export DIPARNUX_CONF="${tree}/diparnux.conf"

"${tree}/scripts/prepare-nvidia.sh"

"${tree}/scripts/build-debs.sh"

# diparnux-llm .deb 를 로컬 저장소(build/config/packages.chroot)에 넣는다. 이 디렉터리는 live-build 가 읽는다.
cp -f "${LLAMA_DEB}" "${tree}/build/config/packages.chroot/"

cd "${tree}/build"
lb clean
lb config
lb build

shopt -s nullglob
isos=("${tree}"/build/*.iso)
shopt -u nullglob
[ "${#isos[@]}" -eq 1 ] || die "ISO 파일이 하나여야 하는데 ${#isos[@]} 개를 찾았다."

iso_name="diparnux-${DIPARNUX_VERSION}-amd64.iso"

# 검증 1: ISO 파일 크기
[ -s "${isos[0]}" ] || die "ISO 파일 크기가 0 이다: ${isos[0]}"

# 검증 2: chroot 의 os-release 정체성.
# /etc/os-release 가 우선이므로 두 파일을 모두 본다 (live-build 가 /etc 쪽을 일반 파일로 바꿔 두기 때문이다).
for osrel in "${tree}/build/chroot/etc/os-release" "${tree}/build/chroot/usr/lib/os-release"; do
    [ -r "${osrel}" ] || die "chroot 에 os-release 가 없다: ${osrel}"
    grep -qx "ID=${DIPARNUX_ID}" "${osrel}" || die "os-release 에 ID=${DIPARNUX_ID} 가 없다: ${osrel}"
    grep -qx 'ID_LIKE=debian' "${osrel}" || die "os-release 에 ID_LIKE=debian 이 없다: ${osrel}"
done

# 검증 3: diparnux-base 설치 여부 (dpkg 상태 파일에서 확인한다)
status="${tree}/build/chroot/var/lib/dpkg/status"
[ -r "${status}" ] || die "chroot 의 dpkg 상태 파일이 없다: ${status}"
if ! awk 'BEGIN { RS = ""; found = 0 }
    /(^|\n)Package: diparnux-base\n/ && /(^|\n)Status: install ok installed(\n|$)/ { found = 1 }
    END { exit (found ? 0 : 1) }' "${status}"; then
    die "diparnux-base 가 설치되지 않았다 (${status})."
fi

# 바이너리 단계의 임시 패키지 설치 뒤에도 빌드 서명 키가 다시 생기지 않았는지 확인한다.
for key in mok.key mok.pub; do
    [ ! -e "${tree}/build/chroot/var/lib/dkms/${key}" ] || die "DKMS 서명 키가 이미지에 남았다: ${key}"
done
[ -f "${tree}/build/chroot/usr/sbin/diparnux-gpu-check" ] || die 'GPU 진단 스크립트가 없다.'

# 검증 4: diparnux-llm 의 llama-server 와 CUDA 백엔드 모듈이 chroot 에 있다.
llama_in_chroot="${tree}/build/chroot${LLAMA_INSTALL_DIR}"
[ -x "${llama_in_chroot}/llama-server" ] || die "llama-server is missing from the chroot: ${llama_in_chroot}/llama-server"
[ -f "${llama_in_chroot}/libggml-cuda.so" ] || die "libggml-cuda.so is missing from the chroot: ${llama_in_chroot}/libggml-cuda.so"

# lb 가 만든 ISO 에 EFI System Partition 을 GPT 파티션으로 덧붙인다 (BIOS 부트로더는 쓰지 않는다).
# 결과 파일은 add-esp-partition.sh 가 자체 검증을 마친 뒤에만 남는다.
esp_iso="${DIPARNUX_WORKDIR}/${iso_name}"
rm -f "${esp_iso}"
"${tree}/scripts/add-esp-partition.sh" "${isos[0]}" "${esp_iso}"

mkdir -p "${DIPARNUX_OUT}"
cp -f "${esp_iso}" "${DIPARNUX_OUT}/${iso_name}"
rm -f "${esp_iso}"
(
    cd "${DIPARNUX_OUT}"
    sha256sum "${iso_name}" > "${iso_name}.sha256"
    sha256sum -c "${iso_name}.sha256"
)

echo "build-iso: 완료: ${DIPARNUX_OUT}/${iso_name}"
echo "build-iso: 체크섬: ${DIPARNUX_OUT}/${iso_name}.sha256"
