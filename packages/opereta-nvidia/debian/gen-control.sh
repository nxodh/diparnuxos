#!/bin/sh
# 패키지의 고정 의존성을 빌드 설정에서 만든다.
set -eu
# shellcheck source=/dev/null
. "$1"
driver="${NVIDIA_DRIVER_VERSION}-${NVIDIA_DEB_REVISION}"
# 키링과 pinning 패키지는 키 교체와 핀 갱신을 막지 않도록 최소 버전으로만 묶는다. 드라이버는 정확히 묶는다.
# CUDA 런타임(cudart, cublas)은 이미지에 넣지 않는다. NVIDIA CUDA EULA 가 재배포를 응용 전용으로만 허용하므로
# 사용자의 컴퓨터가 NVIDIA 저장소에서 직접 받아 설치한다(opereta gpu runtime install, docs/licenses.md 2.3, 10.5).
depends="cuda-keyring (>= ${CUDA_KEYRING_VERSION}), nvidia-driver-pinning-${NVIDIA_BRANCH} (>= ${NVIDIA_PIN_VERSION}), nvidia-kernel-open-dkms (= ${driver}), nvidia-driver (= ${driver}), nvidia-driver-cuda (= ${driver})"
sed "s/^Depends:.*/Depends: \${misc:Depends}, bash, coreutils, initramfs-tools, kmod, mokutil, ${depends}/" debian/control > debian/control.new
mv debian/control.new debian/control
