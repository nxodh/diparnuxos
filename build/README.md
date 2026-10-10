# build/

live-build 설정과 ISO 빌더 이미지를 둔다. ISO 빌드의 전체 흐름은 `scripts/` 에 있다.

## 구조와 파일 역할

```
build/
├── Dockerfile                          빌더 이미지 (debian:trixie). --platform linux/amd64 로 빌드한다.
├── auto/
│   ├── config                          lb config 의 auto 스크립트. lightlix.conf 를 읽어 lb config noauto 를 호출한다.
│   ├── build                           lb build 의 auto 스크립트 (noauto 로 위임)
│   └── clean                           lb clean 의 auto 스크립트 (noauto 로 위임)
└── config/
    ├── package-lists/
    │   └── lightlix.list.chroot        Phase 1 헤드리스 구성과 lightlix-nvidia
    ├── includes.chroot/                chroot 에 그대로 복사할 파일
    │   ├── etc/systemd/network/20-lightlix-wired.network   유선 인터페이스 DHCP 설정
    │   ├── etc/systemd/system/multi-user.target.wants/systemd-networkd.service   networkd 활성화 링크
    │   └── etc/tmpfiles.d/lightlix-resolv.conf             /etc/resolv.conf 를 resolved 스텁으로 연결
    └── packages.chroot/                local 패키지 저장소. scripts/build-debs.sh 가 .deb 를 넣는다. .gitkeep 만 커밋된다.
```

`auto/config` 가 만드는 `lb config` 옵션은 다음과 같다.

- 배포판 `trixie`, 아키텍처 `amd64`, 구성요소는 `DEBIAN_AREAS`
- 이미지는 `iso-hybrid`, 부트로더는 `grub-efi` 만 쓴다 (UEFI 전용, BIOS 부트로더를 쓰지 않는다). BIOS 부트로더가 없으면 live-build 가 hybrid MBR 을 넣지 않아 ISO 에 파티션 테이블이 없다. QEMU/OVMF 에서는 CD 와 USB 저장 장치 양쪽으로 부팅됐지만, 실기에서 USB 에 쓴 ISO 의 부팅은 미검증이다. Phase 1 의 실기 검증 전에 ESP 파티션을 덧붙일지 정한다.
- 펌웨어 자동 설치는 끈다 (`--firmware-chroot false`, `--firmware-binary false`). 켜 두면 non-free-firmware 의 펌웨어 패키지 35개(약 850 MB)가 들어온다. 실기에 필요한 펌웨어는 Phase 1 에서 패키지 목록에 이름으로 적는다.
- debian-installer 없음, backports 끔, security 와 updates 사용, apt recommends 끔
- 커널 패키지는 `linux-image` 와 `linux-headers`
- 미러는 `lightlix.conf` 값을 쓴다. `DEBIAN_SNAPSHOT` 이 있으면 snapshot 주소의 그 시점을 쓰고, apt 의 `Check-Valid-Until` 을 끈다.
- 라이브 부팅 파라미터에 `console=tty0 console=ttyS0,115200` 과 `hostname=lightlix` 를 넣는다.
- ISO 볼륨, 애플리케이션, 퍼블리셔 이름은 `Lightlix`, 이미지 이름은 `lightlix-<버전>` 이다.

`config/hooks/normal/` 의 훅은 세 개다.

- `9100-lightlix-os-release.hook.chroot`: live-build 가 Debian 내용으로 만들어 둔 `/etc/os-release` 를 Lightlix 내용으로 다시 만든다.
- `9500-lightlix-grub-timeout.hook.binary`: GRUB 메뉴에 5초 타임아웃을 넣는다. live-build 의 템플릿에는 타임아웃이 없어 메뉴에서 무한 대기한다.
- `9900-lightlix-nvidia.hook.chroot`: 이미지의 6.12 커널을 찾아 `dkms autoinstall -k`로 빌드한다. 모듈 파일·버전·MIT/GPL 라이선스, 금지 패키지 부재를 검사하고 initramfs를 갱신한다. 드라이버와 커널을 hold하고 계약서 전문을 복사한 뒤 DKMS 서명 키를 제거한다.

`scripts/prepare-nvidia.sh`가 `config/archives/`에 생성하는 `.deb`와 핀은 git에서 제외한다. NVIDIA 저장소는 `cuda-keyring`으로만 추가한다. 빌드 시작 시 `ca-certificates`를 debootstrap에 포함해 초기 HTTPS 저장소 접근에도 인증서 검증을 적용한다. `20-lightlix-dkms.conf.chroot`는 최초 패키지 설치 중 DKMS 병렬 작업 수를 2로 제한하며 최종 이미지의 apt 설정에는 남기지 않는다.

`make test-nvidia-image-docker`는 완성된 squashfs에서 패키지·모듈·계약서 전문과 키 제거를 다시 확인한다. `make test-qemu-docker`는 NVIDIA GPU가 없는 환경에서 로그인 프롬프트와 CPU 폴백 안내를 확인한다. 이 검사는 NVIDIA 실기 동작의 증거가 아니다.

## 옵션 검증 근거

`lb config` 옵션 이름과 허용 값은 trixie 의 live-build `1:20250505+deb13u1` 의 `lb_config(1)` 과 소스로 확인했다.
아키텍처 옵션은 복수형 `--architectures` 가 아니라 `--architecture` 이다.

## Dockerfile 의 패키지 선택

`--no-install-recommends` 를 쓰므로 필요한 패키지를 직접 적는다. `dpkg-dev`, `debhelper`, `build-essential` 은 `scripts/build-debs.sh` 가 `.deb` 를 만들 때 쓴다. `build-essential` 이 없으면 `dpkg-buildpackage` 가 "unmet build dependencies" 로 실패한다.
`xorriso`, `grub-efi-amd64-bin`, `mtools`, `dosfstools`, `squashfs-tools` 는 호스트에 두지 않는다. live-build 는 기본값(build-with-chroot=true)에서 이 도구들을 chroot 안에 설치해 쓴다.

## 빌드 방법

```sh
make iso            # Docker 안에서 빌드 (macOS 에서 쓴다)
make iso-native     # Debian 13 호스트에서 root 로 빌드
```

산출물은 `out/lightlix-<버전>-amd64.iso` 와 `.sha256` 이다. 작업은 `/var/tmp/lightlix-build` 에서 하며, 저장소 안에서는 `lb build` 를 돌리지 않는다.
