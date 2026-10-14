#!/bin/sh
# 패키지의 고정 의존성을 빌드 설정에서 만든다.
set -eu
# shellcheck source=/dev/null
. "$1"
driver="${NVIDIA_DRIVER_VERSION}-${NVIDIA_DEB_REVISION}"
# 키링과 pinning 패키지는 키 교체와 핀 갱신을 막지 않도록 최소 버전으로만 묶는다. 드라이버는 정확히 묶는다.
# CUDA 런타임(cudart, cublas)은 NVIDIA 패키지로 설치하지 않는다. opereta-llm 이 NVIDIA CUDA EULA 의 조건에 맞춰
# llama-server 전용 디렉터리에 원본 그대로 가지고 있다(docs/licenses.md 2.3, 10.5).
depends="cuda-keyring (>= ${CUDA_KEYRING_VERSION}), nvidia-driver-pinning-${NVIDIA_BRANCH} (>= ${NVIDIA_PIN_VERSION}), nvidia-kernel-open-dkms (= ${driver}), nvidia-driver (= ${driver}), nvidia-driver-cuda (= ${driver})"
sed "s/^Depends:.*/Depends: \${misc:Depends}, bash, coreutils, initramfs-tools, kmod, mokutil, ${depends}/" debian/control > debian/control.new
mv debian/control.new debian/control
