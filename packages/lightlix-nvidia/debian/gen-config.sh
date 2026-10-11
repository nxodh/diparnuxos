#!/bin/sh
# 최초 apt 트랜잭션과 최종 패키지에서 같은 정책을 쓴다.
set -eu
# shellcheck source=/dev/null
. "$1"
dest="$2"
mkdir -p "${dest}/etc/apt/preferences.d" "${dest}/etc/modprobe.d" \
    "${dest}/etc/systemd/system" "${dest}/usr/share/lightlix" \
    "${dest}/etc/dkms/framework.conf.d"
mkdir -p "${dest}/etc/systemd/system/nvidia-persistenced.service.d" "${dest}/usr/lib/tmpfiles.d"
driver="${NVIDIA_DRIVER_VERSION}-${NVIDIA_DEB_REVISION}"
# 진단 유닛과 데몬이 같이 쓰는 런타임 디렉터리다. 유닛의 RuntimeDirectory 는 멈출 때 지우므로 쓰지 않는다.
cat > "${dest}/usr/lib/tmpfiles.d/lightlix.conf" <<'EOF'
# 부팅 때 /run/lightlix 를 만든다. 나이 제한이 없어서 자동으로 지우지 않는다.
d /run/lightlix 0755 root root -
EOF
cat > "${dest}/etc/apt/preferences.d/00-lightlix-nvidia.pref" <<EOF
# 이 파일은 NVIDIA 브랜치 핀보다 먼저 읽혀야 한다.
Package: nvidia-kernel-dkms nvidia-kernel-source cuda-drivers* nvidia-open
Pin: version *
Pin-Priority: -1

Package: dkms dh-dkms
Pin: release o=Debian
Pin-Priority: 1001

Package: src:nvidia-graphics-drivers:any src:nvidia-kmod-open:any nvidia-modprobe nvidia-persistenced firmware-nvidia-gsp
Pin: version ${driver}
Pin-Priority: 1001

Package: src:nvidia-graphics-drivers:any src:nvidia-kmod-open:any nvidia-modprobe nvidia-persistenced firmware-nvidia-gsp
Pin: version *
Pin-Priority: -1

Package: cuda-cudart-${CUDA_PKG_SUFFIX}
Pin: version ${CUDA_CUDART_VERSION}
Pin-Priority: 1001

Package: libcublas-${CUDA_PKG_SUFFIX}
Pin: version ${CUDA_CUBLAS_VERSION}
Pin-Priority: 1001
EOF
cat > "${dest}/etc/modprobe.d/lightlix-nouveau.conf" <<'EOF'
# NVIDIA 오픈 모듈만 사용한다.
blacklist nouveau
options nouveau modeset=0
EOF
echo 'parallel_jobs=2' > "${dest}/etc/dkms/framework.conf.d/lightlix.conf"
cat > "${dest}/etc/systemd/system/nvidia-persistenced.service.d/lightlix-gpu.conf" <<'EOF'
# 진단이 모듈을 올린 뒤 실행한다. GPU 가 없으면 불필요한 실패를 만들지 않는다.
[Unit]
After=lightlix-gpu-check.service
ConditionPathExists=/proc/driver/nvidia/version
EOF
for target in sleep suspend hibernate hybrid-sleep suspend-then-hibernate; do
    ln -s /dev/null "${dest}/etc/systemd/system/${target}.target"
done
cat > "${dest}/usr/share/lightlix/nvidia.conf" <<EOF
NVIDIA_DRIVER_VERSION="${NVIDIA_DRIVER_VERSION}"
NVIDIA_DRIVER_DEB_VERSION="${driver}"
CUDA_CUDART_PACKAGE="cuda-cudart-${CUDA_PKG_SUFFIX}"
CUDA_CUDART_VERSION="${CUDA_CUDART_VERSION}"
CUDA_CUBLAS_PACKAGE="libcublas-${CUDA_PKG_SUFFIX}"
CUDA_CUBLAS_VERSION="${CUDA_CUBLAS_VERSION}"
EOF
