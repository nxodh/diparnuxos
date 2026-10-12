# Phase 4 검증 기록

- 확인 날짜: 2026-10-12
- 대상: 브랜치 `phase-4`([PR #6](https://github.com/nxodh/lightlixos/pull/6)), 이미지 버전 0.3.0(버전 올리기는 마지막 커밋에서 한다)
- 결론: 설치기, 업데이트·롤백 엔진, APT 저장소 도구가 컨테이너와 QEMU/KVM 가상 머신에서 확인되었다. **실기 설치, 실제 Debian 커널 갱신, MokManager 화면, VMware 같은 다른 가상화 환경은 확인하지 않았다.**

## 1. 실행 기록

| 실행 | 커밋 | 결과 | 드러난 문제와 조치 |
|---|---|---|---|
| [38220684958](https://github.com/nxodh/lightlixos/actions/runs/38220684958) | `74466ef` | lint 실패, 설치 E2E 실패 | 업데이트 통합 시험이 러너의 실제 `/proc/cmdline` 의 `BOOT_IMAGE` 를 읽어 가짜 커널 이름이 어긋났다(시험 환경 문제, `LIGHTLIX_UPDATE_PROC_CMDLINE` 로 분리). 설치기가 chroot 안에서 `lightlix-user` 를 부를 때 sshd 검사가 실패해 계정 생성이 거절되었다(헬퍼가 systemd 가 부팅하지 않은 환경에서는 sshd 검사와 reload 를 건너뛰도록 수정) |
| [38222697645](https://github.com/nxodh/lightlixos/actions/runs/38222697645) | `9ef8e18` | 설치 E2E 실패 | 설치, 디스크 부팅, 로그인까지 통과. ESP 의 `umask=0077` 때문에 시험이 파일을 읽지 못했다(시험 수정). 데스크톱 E2E 의 그래픽 설치기 창 표시는 이 실행에서 통과 |
| [38227113016](https://github.com/nxodh/lightlixos/actions/runs/38227113016) | `38b5229` | 설치 E2E 실패 | 설치된 시스템 검사 중 6개만 실패: 설치된 시스템의 `sudo` 는 비밀번호를 묻고, 일반 사용자의 PATH 에 `/usr/sbin` 이 없다(시험 수정) |
| [38229469272](https://github.com/nxodh/lightlixos/actions/runs/38229469272) | `c71bf67` | 설치 E2E 실패 | 설치된 시스템의 모든 검사와 재부팅 통과. 재부팅 뒤 표식 파일을 일반 사용자가 읽지 못했다(`/var/lib/lightlix` 권한, 시험 수정) |
| [38231805200](https://github.com/nxodh/lightlixos/actions/runs/38231805200) | `6e864ff` | **전체 성공**(lint, ISO 빌드, 이미지 정책, UEFI CD·USB 부팅, CPU 추론 E2E, 데스크톱 E2E, 설치 E2E) | — |

## 2. 컨테이너 시험(Mac 호스트에는 설치하지 않았다)

| 시험 | 내용 | 결과 |
|---|---|---|
| `make test-daemon` 계열 Go 시험 | `internal/update`(가짜 apt/dpkg/dkms/grub 시스템 24+개 시나리오), `internal/updatectl`, `internal/install`, API, 클라이언트, CLI | 통과 |
| `make test-update-docker` | **실제 apt 와 dpkg** 로 가짜 저장소(`file:`)에서 커널·드라이버 갱신, 부팅 뒤 확인, 롤백, 드라이버 실패 복구, 커널 DKMS 실패 시 이전 커널 유지 | 통과(amd64, arm64) |
| `make test-apt-repo-docker` | 서명된 저장소 생성, apt 서명 검증, 이전 버전 보존과 다운그레이드, `lightlix-*` 외 패키지 차단, 색인 변조·다른 키 거절 | 통과 |
| `make test-installer-config-docker` | 실제 Calamares 3.3.14 패키지의 모듈 존재, 인스턴스, 스키마(mount, unpackfs), 브랜딩 이미지, 전역 저장소 키 연결 | 통과. fstab 스키마는 업스트림이 낡아 건너뜀 |
| `make test-gui` | Control Center 단위·GTK 시험(업데이트 표시 논리 포함) | 47개 통과 |
| `tests/nvidia/user.sh` | 계정 헬퍼, chroot 처럼 systemd 가 없는 환경 | 통과 |

실제 apt 시험이 단위 시험이 놓친 것을 두 가지 잡았다: `--ignore-hold` 는 hold 된 모든 패키지를 올려 버리므로 커널만 설치할 때는 쓰면 안 된다. 갱신이 중간에 실패해 반쯤 설정된(half-configured) 패키지는 `dpkg-query` 의 설치 목록에서 빠지므로 되돌릴 대상을 따로 찾아야 한다.

## 3. QEMU/KVM 설치 E2E (`make test-qemu-install-docker`, CI 실행 38231805200)

OVMF UEFI, virtio 디스크 20 GiB(빈 qcow2), 램 3 GiB, 네트워크 NAT. 실기가 아니다.

| 단계 | 확인한 것 |
|---|---|
| 거절 경로 | 파티션 장치, 없는 디스크, `--yes` 없음, 약한 비밀번호가 모두 디스크를 바꾸기 전에 거절된다. 거절 뒤 디스크에 파일 시스템이 없다 |
| 설치(라이브 ISO) | 단계 `disk`, `format`, `mount`, `copy`, `configure`, `account`, `cleanup`, `initramfs`, `bootloader`, `done` 이 실행된다. 전체 약 1분 45초(KVM, 복사 약 80초). 비밀번호는 출력과 로그에 없다. 설치 뒤 대상은 모두 마운트 해제된다 |
| 디스크 부팅 | OVMF 가 NVRAM 의 `lightlix` 항목(`\EFI\lightlix\shimx64.efi`)으로 shim → GRUB → 커널을 부팅하고 설치한 계정으로 로그인한다. GPT 는 ESP 1 GiB(FAT32)와 루트(ext4) |
| 설치된 시스템 | `ID=lightlix`, 라이브가 아님, 루트 `/dev/vda2` ext4, ESP `/dev/vda1` vfat, 호스트 이름, 그룹(`lightlix`, `sudo`, `video`, `render`), 서버 모드와 `multi-user.target`, 계정 목록에 만든 계정만 있음, 기본 `user` 계정 없음, `live-boot`·`live-config`·`calamares`·`lightlix-installer` 제거됨, 저장된 GRUB 기본 항목, `20-lightlix.cfg`, NVIDIA 오픈 모듈 존재, 루트 계정 잠김, 실패한 서비스 없음 |
| 업데이트(설치된 시스템) | `lightlix update --status` 가 사용 가능(`live: false`, `idle`)이고 `lightlix update --check` 가 실제 저장소에 접속해 계획(패키지 2개)을 보여 준다 |
| 재부팅 | 표식 파일과 계정이 남는다 |
| Secure Boot | Microsoft 키가 든 OVMF 변수와 secboot 펌웨어로 **빈 NVRAM** 에서 부팅한다. 부팅 항목이 없어 `EFI/BOOT/BOOTX64.EFI`(shim 대체 경로)로 부팅한다. `mokutil --sb-state` 가 `SecureBoot enabled` 이고 커널 lockdown 은 `integrity`, 데몬이 시작한다 |

ESP 구성: `EFI/lightlix/{shimx64.efi,grubx64.efi,mmx64.efi,fbx64.efi,grub.cfg,BOOTX64.CSV}` 와 `EFI/BOOT/{BOOTX64.EFI,grubx64.efi,mmx64.efi,grub.cfg,BOOTX64.CSV}`.

참고 값(QEMU, 부팅 직후, 실기 아님): 설치된 서버 모드의 `MemAvailable` 은 MemTotal 약 2.84 GiB 에서 약 2.48 GiB 이므로 사용 약 360 MiB 다. 전체 OS 램의 판정 값이 아니다.

## 4. 그래픽 설치기

데스크톱 E2E(QEMU, 가상 디스플레이)에서 `lightlix-installer` 를 labwc 세션에 띄우고 화면을 캡처했다. Lightlix 브랜딩, 단계 목록(Welcome, Location, Keyboard, Partitions, Users, Mode, Summary, Install, Finish), 언어 선택, Next/Cancel 이 그려진다. 이때 요구 사항 확인 중("Waiting for 8 modules")이었다. **화면을 눌러 끝까지 설치하는 시험은 하지 않았다.** Calamares 가 만드는 계정과 우리의 마무리 단계(`target-prepare`, `target-bootloader`)의 이음매는 설정 검증과 단위 시험까지만 확인했다. 콘솔 설치기가 같은 마무리 단계를 실제로 실행한 것은 위 3절이다.

## 5. 확인하지 않은 것

- 실기 NVMe/SATA 설치, 노트북 펌웨어의 부팅 항목 처리, 다른 OS 와의 이중 부팅.
- 실제 Debian 커널 보안 갱신(두 번째 실제 커널이 CI 에 없다). 업데이트 엔진은 가짜 저장소의 합성 커널 패키지로 확인했다. 새 커널이 실제로 부팅하지 못하는 경우의 GRUB 동작은 상태 전이만 시험했다.
- NVIDIA 드라이버의 실제 DKMS 빌드 실패와 실제 드라이버 버전 갱신. NVIDIA 저장소가 이전 버전을 보관하는 것은 인덱스로 확인했다(2026-10-12: 595.71.05, 595.91.07).
- MokManager 화면에서의 키 등록과 등록 뒤 모듈 적재.
- VMware, VirtualBox, Hyper-V 등 QEMU 가 아닌 가상화 환경의 데스크톱 세션(아래).

## 6. 설치 후 알려진 문제와 조치

- **VMware 에서 데스크톱 세션이 로그인 직후 꺼진다는 보고**(사용자, 0.3.0 ISO, 2026-10-12). QEMU(virtio-gpu)에서는 통과했으므로 가상 GPU 에 따른 컴포지터 시작 문제로 본다. 원인은 사용자 로그 확인 전이라 확정하지 않았다. 대응으로 가상 머신에서는 소프트웨어 렌더러(`WLR_RENDERER=pixman`)와 소프트웨어 커서를 기본으로 쓰고 labwc 출력을 `$XDG_RUNTIME_DIR/labwc.log` 에 남기는 세션 래퍼(`lightlix-session`)를 넣었다. VMware 에서의 해결 여부는 확인하지 못했다.
