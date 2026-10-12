# Lightlix ISO 빌더 이미지.
# 플랫폼은 빌드할 때 지정하지 않고, scripts/build-in-docker.sh 가 --platform linux/amd64 로 실행한다.
# DEBIAN_SUITE 는 scripts/build-in-docker.sh 가 lightlix.conf 값으로 넘긴다.
ARG DEBIAN_SUITE
FROM debian:${DEBIAN_SUITE}

ENV DEBIAN_FRONTEND=noninteractive

# --no-install-recommends 를 쓰므로 필요한 것을 모두 명시한다.
# - live-build: lb 명령. Depends 로 cpio 와 debootstrap 을 끌어온다.
# - debian-archive-keyring, gpgv: debootstrap 이 Release 서명을 검증할 때 쓴다 (Recommends 라서 명시한다).
# - mount: debootstrap 이 chroot 를 준비할 때 쓴다 (Recommends 라서 명시한다).
# - ca-certificates: snapshot.debian.org 나 https 미러를 쓸 때 필요하다.
# - xz-utils: 압축된 패키지 인덱스를 풀 때 쓴다.
# - dpkg-dev, debhelper: scripts/build-debs.sh 가 dpkg-buildpackage 로 .deb 를 만들 때 쓴다.
# - build-essential: dpkg-buildpackage 가 암묵적 빌드 의존성으로 확인한다 (없으면 "unmet build dependencies" 로 실패한다).
# - golang-go, gcc: vendoring한 go-nvml과 두 Go 실행 파일을 cgo로 빌드한다.
# - python3, gettext: lightlix-control-center 패키지가 빌드 의존성으로 요구한다(테스트 실행과 msgfmt 번역 카탈로그 컴파일).
# - xorriso, mtools, fdisk: scripts/add-esp-partition.sh 가 lb 가 만든 ISO 에 ESP 파티션을 덧붙일 때 빌더 컨테이너에서 쓴다.
#   xorriso 는 xorriso 명령, mtools 는 mdir/mcopy, fdisk 패키지는 sfdisk 를 제공한다 (dpkg -S 로 확인).
#   cmp, dd, sha256sum 은 essential 패키지라 기본 이미지에 있다.
# - grub-efi-amd64-bin, dosfstools, squashfs-tools 는 넣지 않는다. 기본값(build-with-chroot=true)에서 live-build 가
#   chroot 안에 설치해 ISO 를 만들 때 쓰기 때문이다.
RUN apt-get update \
 && apt-get install -y --no-install-recommends \
      live-build \
      debootstrap \
      debian-archive-keyring \
      gpgv \
      mount \
      ca-certificates \
      curl \
      xz-utils \
      dpkg-dev \
      debhelper \
      build-essential \
      golang-go \
      gcc \
      python3 \
      gettext \
      xorriso \
      mtools \
      fdisk \
 && rm -rf /var/lib/apt/lists/*
