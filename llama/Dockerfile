# llama.cpp 빌더 이미지. 내용은 llama/build.sh 가 컨테이너 안에서 빌드한다.
# nvidia/cuda 이미지는 Debian 변형이 없으므로 쓰지 않고, debian:<suite> 에 NVIDIA 저장소를 더한다.
# 값은 scripts/build-llama-in-docker.sh 가 opereta.conf 에서 읽어 --build-arg 로 넘긴다.
ARG DEBIAN_SUITE
FROM debian:${DEBIAN_SUITE}

ARG LLAMA_CUDA=ON
ARG CUDA_KEYRING_URL
ARG CUDA_KEYRING_SHA256
ARG CUDA_PKG_SUFFIX
ARG CUDA_MINIMAL_BUILD_VERSION
ARG CUDA_CUBLAS_VERSION

ENV DEBIAN_FRONTEND=noninteractive
ENV LLAMA_CUDA=${LLAMA_CUDA}
# CUDA 빌드에서 nvcc 를 찾게 한다. CUDA 를 끈 이미지에는 이 경로가 없고 쓰이지 않는다.
ENV PATH=/usr/local/cuda/bin:${PATH}

# 공통 빌드 도구. --no-install-recommends 를 쓰므로 필요한 것을 모두 명시한다.
# - cmake, make, g++: llama-server 빌드. make 는 cmake 의 Unix Makefiles 생성기가 쓴다.
# - git: 고정 커밋을 받는다. ca-certificates: https 소스 주소를 쓴다.
# - libssl-dev: llama-server 의 HTTPS 지원(없으면 CMake 가 경고만 내고 HTTPS 를 끈다).
# - debhelper, dpkg-dev: scripts 가 아니라 llama/pkg/debian 으로 .deb 를 만든다.
# - build-essential: dpkg-buildpackage 가 암묵적 빌드 의존성으로 확인한다(없으면 "unmet build dependencies").
# - binutils: readelf 로 RUNPATH 와 NEEDED 를 검사한다.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      ca-certificates \
      cmake \
      make \
      g++ \
      git \
      libssl-dev \
      binutils \
      debhelper \
      dpkg-dev \
      build-essential \
 && rm -rf /var/lib/apt/lists/*

# CUDA 빌드 패키지. LLAMA_CUDA=OFF 이면 이 단계를 건너뛴다(CPU 전용 스모크 빌드용).
# cuda-keyring 은 NVIDIA 저장소 항목과 키를 설치하며, 내려받은 파일은 sha256 으로 검증한다.
# 두 패키지만 정확한 버전으로 고정한다. 나머지 CUDA 의존 패키지는 이 버전의 Depends 를 따라 설치되며,
# 설치된 버전은 빌드 후 usr/share/doc/opereta-llm/build-info.txt 에 기록된다.
RUN if [ "${LLAMA_CUDA}" = "ON" ]; then \
      set -eux; \
      apt-get update; \
      apt-get install -y --no-install-recommends ca-certificates curl; \
      curl --fail --location --retry 5 --connect-timeout 30 -o /tmp/cuda-keyring.deb "${CUDA_KEYRING_URL}"; \
      echo "${CUDA_KEYRING_SHA256}  /tmp/cuda-keyring.deb" | sha256sum -c -; \
      dpkg -i /tmp/cuda-keyring.deb; \
      rm -f /tmp/cuda-keyring.deb; \
      apt-get update; \
      apt-get install -y --no-install-recommends \
        "cuda-minimal-build-${CUDA_PKG_SUFFIX}=${CUDA_MINIMAL_BUILD_VERSION}" \
        "libcublas-dev-${CUDA_PKG_SUFFIX}=${CUDA_CUBLAS_VERSION}"; \
      rm -rf /var/lib/apt/lists/*; \
    else \
      echo "llama/Dockerfile: LLAMA_CUDA=${LLAMA_CUDA}, skipping the CUDA repository and packages"; \
    fi

WORKDIR /
