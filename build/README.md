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
    │   └── lightlix.list.chroot        Phase 1 헤드리스 구성, lightlix-nvidia, Intel 펌웨어와 iwd
    ├── includes.chroot/                chroot 에 그대로 복사할 파일
    │   ├── etc/systemd/network/20-lightlix-wired.network   유선 인터페이스 DHCP 설정
    │   ├── etc/systemd/network/25-lightlix-wireless.network   무선 인터페이스(wl*) DHCP 설정 (인증은 iwd 가 맡는다)
    │   ├── etc/systemd/system/multi-user.target.wants/systemd-networkd.service   networkd 활성화 링크
    │   ├── etc/systemd/system/multi-user.target.wants/iwd.service               iwd 활성화 링크
    │   └── etc/tmpfiles.d/lightlix-resolv.conf             /etc/resolv.conf 를 resolved 스텁으로 연결
    └── packages.chroot/                local 패키지 저장소. scripts/build-debs.sh 가 .deb 를 넣는다. .gitkeep 만 커밋된다.
```

`auto/config` 가 만드는 `lb config` 옵션은 다음과 같다.

- 배포판 `trixie`, 아키텍처 `amd64`, 구성요소는 `DEBIAN_AREAS`
- 이미지는 `iso-hybrid`, 부트로더는 `grub-efi` 만 쓴다 (UEFI 전용, BIOS 부트로더를 쓰지 않는다). BIOS 부트로더가 없으면 live-build 가 hybrid MBR 을 넣지 않아 ISO 에 파티션 테이블이 없다. 그래서 `scripts/build-iso.sh` 가 lb 가 만든 ISO 에 EFI System Partition 을 GPT 파티션으로 덧붙인 뒤 `out/` 에 복사한다. 방식과 USB 에 쓰는 법은 아래 "ESP 파티션과 USB 쓰기" 절에 있다. BIOS 부팅은 지원하지 않는다.
- 펌웨어 자동 설치는 끈다 (`--firmware-chroot false`, `--firmware-binary false`). 켜 두면 non-free-firmware 의 펌웨어 패키지 35개(약 850 MB)가 들어온다. 현재 패키지 목록에는 Intel Wi-Fi(`firmware-iwlwifi`)와 Intel 이더넷(`firmware-intel-misc`) 펌웨어만 이름으로 넣었다. 다른 제조사(Realtek, Broadcom, MediaTek) 펌웨어는 없다.
- 무선 접속은 iwd 가 맡는다. iwd 는 D-Bus 시스템 버스를 쓰며, dbus 는 systemd-resolved 의 Depends 로 이미 들어온다. iwd 의 Recommends(`wireless-regdb`)는 꺼져 있으므로 목록에 직접 적었다. 패키지 postinst 는 새 설치에서 `iwd.service` 를 enable 하지 않으므로 `includes.chroot` 에 활성화 링크를 둔다. 이 링크는 `systemd-networkd` 와 같은 방식이다.
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
`xorriso`, `mtools`, `fdisk`(`sfdisk` 를 제공한다)는 `scripts/add-esp-partition.sh` 가 빌더 컨테이너에서 ISO 를 후처리할 때 쓴다. `grub-efi-amd64-bin`, `dosfstools`, `squashfs-tools` 는 넣지 않는다. live-build 가 기본값(build-with-chroot=true)에서 이 도구들을 chroot 안에 설치해 ISO 를 만들 때 쓰기 때문이다.

## ESP 파티션과 USB 쓰기

live-build 가 만든 ISO 는 UEFI 전용이라 파티션 테이블이 없다. `scripts/add-esp-partition.sh <입력.iso> <출력.iso>` 는 입력 ISO 를 고치지 않고 파티션 테이블을 가진 새 ISO 를 만든다. xorriso 1.5.6 (trixie) 으로 확인했다.

1. `-report_el_torito plain` 으로 UEFI El Torito 항목의 부트 이미지 경로를 얻는다 (live-build 빌드에서는 `/boot/grub/efi.img`). `-osirrox on -extract` 로 그 이미지를 꺼낸다.
2. `-append_partition 2 C12A7328-F81F-11D2-BA4B-00A0C93EC93B` 로 그 이미지를 덧붙여 파티션 2 를 EFI System 으로 만들고, `-boot_image any appended_part_as=gpt` 로 GPT 에 적는다.
3. `-boot_image any replay` 로 기존 El Torito 항목을 다시 만든다. ISO 9660 파일 트리와 볼륨 ID(`Lightlix`)는 입력과 같다.
4. `-changes_pending yes` 와 `-end` 로 쓴다. `-changes_pending` 을 주지 않으면 xorriso 가 "No image modifications pending" 으로 쓰기를 건너뛴다.

man page 근거 (`man xorriso`, 1.5.6):

- `-boot_image any appended_part_as=gpt`: "appended_part_as=gpt marks partitions from -append_partition in GPT rather than in MBR. In this case the MBR shows a single partition of type 0xee which covers the whole output data."
- `-append_partition`: "type_code may also be a type GUID ... It will be used if the partition is mentioned in GPT."
- `-boot_image any replay`: "It discards any existing -boot_image setting and executes the commands proposed by command -report_el_torito "cmd"."
- `-changes_pending`: "This is mainly useful for setting state "yes" despite no real changes were made."

쓰지 않은 옵션과 이유: `efi_path=--interval:appended_partition_N:all::` 는 El Torito 항목이 가리키는 이미지를 덧붙인 복사본으로 바꾼다. `replay` 로 기존 항목을 유지하므로 쓰지 않는다. `partition_table=on` 과 `partition_offset` 는 MBR 파티션 표나 두 번째 superblock 을 만들어 이 방식에 필요 없다. `-compliance` 는 필요 없다. 볼륨 ID `Lightlix` 는 소문자가 있어 xorriso 가 ECMA-119 규칙 위반 경고를 내지만, 입력과 같은 값을 그대로 둔다.

스크립트는 출력 ISO 를 다시 읽어 다음을 확인한다. 하나라도 어긋나면 출력을 지우고 실패한다.

- GPT 가 있고 EFI System 타입 파티션이 하나 있다 (`sfdisk -d`).
- ESP 영역이 원본 EFI 이미지와 바이트 단위로 같고 FAT 이며 `EFI/boot/bootx64.efi` 가 있다.
- `/live/filesystem.squashfs` 의 sha256 이 입력과 같다.
- El Torito UEFI 항목이 여전히 있고 같은 경로를 가리킨다.
- 볼륨 ID 가 입력과 같다.

알려진 경고: `sfdisk` 는 "The backup GPT table is not on the end of the device." 와 "GPT PMBR size mismatch" 를 출력한다. xorriso 기본 padding(300 KiB)이 GPT 계산 뒤에 붙기 때문이다. 기본 GPT 로 파티션 표를 읽는 데는 문제가 없었다. 실기 펌웨어에서의 영향은 미검증이다.

### USB 에 쓰기

Linux:

```sh
lsblk                                        # 대상 장치의 크기와 모델을 확인한다
sudo umount /dev/sdX1                        # 자동 마운트된 파티션이 있으면 푼다
sudo dd if=lightlix-<버전>-amd64.iso of=/dev/sdX bs=4M conv=fsync status=progress
```

macOS:

```sh
diskutil list                                # 외장 물리 디스크의 식별자를 확인한다
diskutil unmountDisk /dev/diskN
sudo dd if=lightlix-<버전>-amd64.iso of=/dev/rdiskN bs=4m
```

> 경고: `of=` 에 잘못된 장치를 지정하면 그 디스크의 내용이 모두 지워진다. `/dev/sdX`, `/dev/diskN` 은 자리표시자다. 시스템 디스크를 지정하지 않는다. 크기와 모델을 확인한 뒤에 실행한다.

### 검증 상태

아래는 `lightlix-0.1.0-amd64.iso` 를 입력으로 스크립트를 돌린 출력에 대한 결과다.

- QEMU + OVMF 에서 `-cdrom` 으로 부팅해 `lightlix login:` 과 GPU 없는 CPU 폴백 안내를 확인했다 (`tests/qemu/boot-smoke.sh`, TCG, 약 113 초).
- QEMU + OVMF 에서 USB 저장 장치(xHCI + `usb-storage`)로 부팅해 같은 문구를 확인했다 (TCG, 약 116 초). 직렬 로그에 `BdsDxe: starting Boot0001 ... USB(0x0,0x0)` 가 있다. 이 줄에는 파티션 노드가 없다.
- 같은 이미지에서 파티션 2 의 타입만 Basic Data 로 바꾼 복사본도 같은 방식으로 부팅됐다 (약 223 초). 따라서 QEMU 의 USB 부팅 성공은 ESP 파티션으로 부팅했다는 증거가 아니다. ESP 가 실기 펌웨어에서 필요한지는 확인하지 못했다.
- 실기 USB 부팅은 아직 미검증이다. 위 확인은 QEMU 에서만 한 것이다.

## 빌드 방법

```sh
make iso            # Docker 안에서 빌드 (macOS 에서 쓴다)
make iso-native     # Debian 13 호스트에서 root 로 빌드
```

산출물은 `out/lightlix-<버전>-amd64.iso` 와 `.sha256` 이다. 작업은 `/var/tmp/lightlix-build` 에서 하며, 저장소 안에서는 `lb build` 를 돌리지 않는다.
