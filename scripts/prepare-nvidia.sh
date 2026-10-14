#!/usr/bin/env bash
# live-build 가 드라이버보다 먼저 설치할 저장소 키와 브랜치 핀 패키지를 준비한다.
# 저장소 항목은 cuda-keyring 이 소유한다. 별도의 archives/*.list 는 만들지 않는다.
set -euo pipefail
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=/dev/null
. "${OPERETA_CONF:-${ROOT}/opereta.conf}"
DEST="${ROOT}/build/config/archives"
mkdir -p "${DEST}"
WORK="$(mktemp -d)"
trap 'rm -rf "${WORK}"' EXIT

fetch() {
    local file="$1" checksum="$2"
    curl --fail --location --retry 5 --connect-timeout 30 \
        "${NVIDIA_REPO_URL}/${file}" -o "${WORK}/${file}"
    printf '%s  %s\n' "${checksum}" "${WORK}/${file}" | sha256sum -c -
    cp "${WORK}/${file}" "${DEST}/${file}"
}
fetch "cuda-keyring_${CUDA_KEYRING_VERSION}_all.deb" "${CUDA_KEYRING_SHA256}"
fetch "nvidia-driver-pinning-${NVIDIA_BRANCH}_${NVIDIA_PIN_VERSION}_all.deb" "${NVIDIA_PIN_SHA256}"

# 최초 apt 트랜잭션에도 독점 모듈 차단을 적용한다.
sh "${ROOT}/packages/opereta-nvidia/debian/gen-config.sh" \
    "${OPERETA_CONF:-${ROOT}/opereta.conf}" "${WORK}/config"
cp "${WORK}/config/etc/apt/preferences.d/00-opereta-nvidia.pref" "${DEST}/00-opereta-nvidia.pref"
# 의존성의 DKMS postinst 가 패키지 설치 중 빌드할 때도 병렬 작업 수를 제한한다.
# chroot 전용 apt 설정은 빌드 후 제거되며, 최종 설정은 opereta-nvidia 가 소유한다.
cat > "${DEST}/20-opereta-dkms.conf.chroot" <<'EOF'
DPkg::Pre-Invoke { "mkdir -p /etc/dkms/framework.conf.d; echo 'parallel_jobs=2' > /etc/dkms/framework.conf.d/opereta.conf"; };
EOF
echo "prepare-nvidia: 저장소 키, 브랜치 고정, 독점 모듈 차단 준비 완료"
