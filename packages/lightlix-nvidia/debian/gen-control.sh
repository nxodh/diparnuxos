#!/bin/sh
# 패키지의 고정 의존성을 빌드 설정에서 만든다.
set -eu
# shellcheck source=/dev/null
. "$1"
driver="${NVIDIA_DRIVER_VERSION}-${NVIDIA_DEB_REVISION}"
depends="cuda-keyring (= ${CUDA_KEYRING_VERSION}), nvidia-driver-pinning-${NVIDIA_BRANCH} (= ${NVIDIA_PIN_VERSION}), nvidia-kernel-open-dkms (= ${driver}), nvidia-driver (= ${driver}), nvidia-driver-cuda (= ${driver}), cuda-cudart-${CUDA_PKG_SUFFIX} (= ${CUDA_CUDART_VERSION}), libcublas-${CUDA_PKG_SUFFIX} (= ${CUDA_CUBLAS_VERSION})"
sed "s/^Depends:.*/Depends: \${misc:Depends}, bash, coreutils, initramfs-tools, kmod, mokutil, ${depends}/" debian/control > debian/control.new
mv debian/control.new debian/control
