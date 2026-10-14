# Phase 4 설계: 설치기, 업데이트와 롤백, 자체 APT 저장소

- 확인 날짜: 2026-10-12
- 상태: 묶음 A~E 는 구현되어 컨테이너 시험을 통과했다. QEMU 설치 E2E 와 이미지 시험의 결과는 [검증 기록](verification-phase-4.md)에 있다. 아래 "확인한 사실"은 Debian 13 amd64 컨테이너와 공식 저장소 원문에서 조회한 값이다. 구현이 끝나면 결과를 `docs/verification-phase-4.md`에 적는다.

범위는 SPEC의 Phase 4다: Calamares 설치기, `opereta-update`(커널·드라이버 동시 갱신과 롤백), 자체 APT 저장소. 램 실측과 튜닝, 보안 점검, 사용자 문서는 Phase 5다.

---

## 1. 이미 있는 것

- 이미지는 live-build ISO이며 서버 모드(`multi-user.target`)로 부팅한다. `opereta-desktop`, Control Center, 첫 부팅 마법사, 계정 생성(`opereta user add`), SSH 제어가 있다.
- 커널(`linux-image-*`, `linux-headers-*`)과 NVIDIA 드라이버 계열, CUDA 런타임은 이미지 빌드 훅(`9900-opereta-nvidia.hook.chroot`)이 `apt-mark hold` 한다. `unattended-upgrades`는 설치하지 않는다.
- 드라이버 버전은 NVIDIA 저장소의 핀 패키지(`595-1`)와 apt preferences로 고정한다. DKMS 서명 키는 이미지에서 지운다.
- 데몬의 `POST /v1/update`는 `not_implemented`를 돌려준다. CLI에 `opereta update` 자리가 있다.
- 데몬이 하드닝(`ProtectSystem=full`) 때문에 `/etc`, `/boot`에 쓸 수 없어서, 권한이 필요한 일은 `systemd-run --pipe`로 하드닝 없는 헬퍼를 부르는 방식이 이미 정착되어 있다(계정, `opereta-user`).
- QEMU E2E 틀(직렬 콘솔 로그인, 명령 실행, 가상 디스플레이 캡처)과 CI의 ISO 빌드·부팅 검사가 있다.

## 2. 확인한 사실

Debian 13 컨테이너(`--platform linux/amd64`)의 `apt-cache`/`apt-get -d`로 2026-10-12에 조회했다.

| 항목 | 값 | 비고 |
|---|---|---|
| calamares | 3.3.14-1 | Qt6, KDE Frameworks 6, kpmcore 의존 |
| calamares + calamares-settings-debian | 292개 패키지, 내려받기 143 MB, 설치 598 MB | 빈 debian:trixie 기준. 이미 이미지에 있는 라이브러리는 겹친다 |
| calamares-settings-debian | 13.0.13-1 | Phase 1 검증이 지적한 대로 greetd와 브랜딩이 맞지 않아 쓰지 않는다 |
| grub-efi-amd64 / -signed / shim-signed | 2.12-9+deb13u2 / 1+2.12+9+deb13u2 / 1.51~1+deb13u1+16.1-2~deb13u1 | 31개, 내려받기 약 7.4 MB, 설치 57.7 MB |
| mokutil, efibootmgr, os-prober | 0.7.2-1, 18-2+b1, 1.83 | |
| apt-utils(apt-ftparchive), reprepro, aptly | 3.0.3, 5.4.6, 1.6.1 | 저장소 색인 생성 도구 후보 |
| NVIDIA 저장소의 같은 패키지 여러 버전 | 595.71.05-1, 595.91.07-1, 610.x, 615.x가 함께 있다 | 브랜치 안에서 이전 버전으로 되돌릴 수 있다. 595 브랜치에는 현재 595.91.07보다 새 것이 없다 |
| live-build `--uefi-secure-boot` | `auto`가 기본 | shim과 서명된 GRUB이 있으면 ISO의 EFI 이미지에 넣는다. 현재 ISO는 이미 이 경로다 |
| Calamares unpackfs | `sourcefs: squashfs`, `file`, `ext4` | `file`은 디렉터리를 rsync로 복사한다 |
| Calamares shellprocess | `${ROOT}`, `${USER}`, `${LANG}`, `gs[key]` 치환, 기본 타임아웃 30초 | 타깃 chroot 안에서 실행된다(`dontChroot: false`) |
| Calamares packagechooser | `method: legacy`면 전역 저장소 키를 쓰고 contextualprocess가 처리한다 | 서버/데스크톱 선택에 쓴다 |
| Debian의 calamares 기본 순서 | partition, mount, unpackfs, machineid, fstab, locale, keyboard, localecfg, users, displaymanager, networkcfg, hwclock, services-systemd, bootloader, packages, plymouthcfg, initramfscfg, initramfs, umount | packages 모듈이 `live-boot`, `live-config`를 제거한다 |

출처: Debian 13 `apt-cache`, https://raw.githubusercontent.com/calamares/calamares/v3.3.14/src/modules/ (unpackfs, shellprocess, contextualprocess, packagechooser 설정 설명), NVIDIA 저장소 `Packages.gz`(`opereta.conf`의 `NVIDIA_REPO_URL`), live-build 20250505의 `lb_config(1)`와 `binary_grub-efi`.

## 3. 결정과 기본값

사용자가 바꾸지 않으면 아래 기본값으로 진행한다. 사용자가 정해야 하는 것은 7절에 따로 모았다.

| 번호 | 항목 | 기본값 | 이유와 대안 |
|---|---|---|---|
| I1 | 설치기 | Calamares 3.3.14(Opereta 전용 설정 패키지 `opereta-installer`) + 서버 모드용 `opereta-install` | SPEC 결정. 헤드리스 서버에는 GUI가 없고, CI가 끝까지 검증할 수 있는 경로가 필요하다. 두 경로가 같은 마무리 단계(`finalize`)를 쓴다 |
| I2 | Calamares 실행 | 라이브 데스크톱 세션에서 `sudo`로 Wayland 위에 띄운다(`QT_QPA_PLATFORM=wayland`) | polkit/xhost를 새로 넣지 않는다. 라이브 사용자는 이미 비밀번호 없이 sudo를 쓴다. `qt6-wayland`를 추가한다 |
| I3 | 설치 대상에서 설치기 제거 | 설치 끝에 `calamares`와 의존성을 purge한다 | 설치된 시스템에 Qt6와 KF6 약 600 MB를 남기지 않는다. 대안은 남겨 두기(재설치 편의) |
| I4 | 파티션 | GPT, ESP 1 GiB(FAT32) + 루트 ext4(나머지). swap 파티션 없음(zram) | 단순하고 롤백 설계와 맞다. LUKS 암호화와 btrfs 스냅샷은 이번에 넣지 않는다(7절) |
| I5 | 디스크 선택지 | 디스크 전체 지우기, 수동 파티션 | "옆에 설치"(리사이즈)는 위험이 커서 뺀다 |
| I6 | 부트로더 | GRUB 2.12 + shim-signed + `grub-efi-amd64-signed`, 폴백 경로(`EFI/BOOT/BOOTX64.EFI`) 설치 | Secure Boot 기기에서 부팅하려면 서명된 체인이 필요하다. DKMS 모듈은 `opereta-mok`로 따로 등록한다 |
| I7 | 설치 직후 모드 | 서버 모드로 시작. 설치기 선택 화면에서 데스크톱을 고르면 `graphical.target` | 기존 정책(램 절약) 유지. 선택은 packagechooser + contextualprocess로 처리 |
| I8 | 업데이트 방식 | 사용자가 시작할 때만. 자동 설치 없음 | 텔레메트리와 무인 변경을 넣지 않는다는 SPEC 규칙. 확인만 하는 `--check`는 있다 |
| I9 | 커널 전환 | GRUB `saved` 기본값 + `grub-reboot`(한 번만 새 커널로 부팅) + 부팅 뒤 검증이 통과해야 `grub-set-default` | 새 커널이 부팅하지 못해도 다음 부팅은 저장된 이전 커널로 돌아간다 |
| I10 | 이전 커널 보존 | 현재 + 직전 1개(최소 1개) | SPEC 규칙 |
| I11 | 드라이버 브랜치 | 업데이트는 고정된 브랜치(595) 안에서만 | 브랜치 변경은 `opereta-nvidia` 패키지가 새 핀을 들고 와야 하며 별도 릴리스다 |
| I12 | 구현 언어 | 업데이트·설치 로직은 Go(`daemon` 모듈의 새 `cmd`와 `internal` 패키지) | 상태 JSON, 버전 비교, 가짜 Runner 단위 테스트가 쉽다. 계정 규칙을 `internal/accounts`와 공유한다 |

## 4. 작업 묶음

| 묶음 | 내용 | 소유 파일 | 검증 |
|---|---|---|---|
| A. 업데이트 엔진 | `opereta-update`(check, apply, verify, rollback, status), 상태 파일, GRUB drop-in, 부팅 뒤 검증 유닛 | `daemon/internal/update`, `daemon/cmd/opereta-update`, `daemon/dist/*`, `packages/opereta-daemon` | Go 단위 테스트(가짜 apt/dkms/grub), 시나리오 표(5절) |
| B. 데몬·CLI·GUI 연결 | `POST/GET /v1/update`, 작업(job) 진행 표시, `opereta update [--check\|--rollback\|--reboot]`, Control Center 업데이트 쪽 | `daemon/internal/api`, `daemon/cmd/opereta`, `gui/**` | API·CLI·GUI 테스트 |
| C. 설치 백엔드 | `opereta-install`(디스크 검증, 분할, 포맷, 복사, chroot, GRUB, `finalize`), 대상 마무리 | `daemon/internal/install`, `daemon/cmd/opereta-install` | Go 단위 테스트, QEMU 설치 E2E |
| D. Calamares | `opereta-installer` 패키지(settings, 모듈 설정, 브랜딩, 실행기), 이미지 패키지 목록 | `packages/opereta-installer`, `build/config/package-lists` | 설정 구문 검사, QEMU에서 창 표시 확인 |
| E. APT 저장소 | 색인·서명 스크립트, `opereta-archive-keyring`, 클라이언트 소스·핀, 키 생성 절차 문서 | `scripts/build-apt-repo.sh`, `packages/opereta-archive-keyring`, `docs/apt-repository.md` | 컨테이너에서 임시 키로 저장소를 만들고 apt로 설치 |
| F. 문서·검증 | `docs/verification-phase-4.md`, `docs/installation.md`, `docs/updates.md`, 지원 표, 알려진 한계 | `docs/**` | |

의존 순서: A와 C는 독립이다. B는 A 뒤, D는 C의 `finalize` 뒤, E는 독립이며 A의 끝에서 클라이언트 설정을 맞춘다. 버전 번호(`opereta.conf`)는 CUDA 캐시 키에 들어가므로 마지막 커밋에서 올린다.

## 5. 업데이트 설계

`opereta-update`는 root로 도는 단일 Go 바이너리다. 데몬은 `systemd-run --pipe`로 이것을 부르고, 부팅 뒤 검증은 같은 바이너리를 systemd 유닛이 부른다. 데몬이 죽어 있어도 `sudo opereta-update apply`로 직접 쓸 수 있다.

### 상태

`/var/lib/opereta/update/state.json`: `idle`, `pending_boot`(새 커널 설치됨, 한 번 부팅 대기), `committed`, `failed`. 갱신 이력은 `/var/lib/opereta/update/history/<시각>.json`에 남기고 설치 전 버전 목록, 선택한 항목, 결과를 적는다.

### `apply` 순서

1. 사전 검사: 설치된 시스템인지(라이브 세션이면 `live_session`으로 거절), root, dpkg 잠금 없음, `/boot`와 `/`의 여유 공간, `apt-get update` 성공.
2. 계획: 관리 대상(커널 이미지·헤더, NVIDIA 오픈 모듈 계열, CUDA 런타임, `dkms`, `opereta-*`)의 현재·후보 버전을 비교해 보여 준다. 바꿀 것이 없으면 `up_to_date`로 끝난다.
3. 이력 기록, 관리 대상의 hold를 풀고(`trap`으로 항상 다시 건다) 한 번의 apt 트랜잭션으로 설치한다. 관리 대상이 아닌 패키지의 보안 갱신도 같은 명령에서 받는다(제거는 허용하지 않는다).
4. 검증: 새로 설치된 각 커널 K에 대해 `dkms status -k K`가 installed이고, `modinfo -k K nvidia`의 파일이 `updates/dkms/` 아래에 있으며 버전이 목표와 같고 라이선스가 `Dual MIT/GPL`이며, `/boot/initrd.img-K`가 있다.
5. 실패하면(DKMS 빌드 실패, postinst 실패) 새 커널 패키지를 purge하고 GRUB 항목을 다시 만들어 이전 커널을 기본으로 유지한다. 원인 로그 끝부분을 오류에 담는다(`dkms_failed`, `kernel_postinst_failed` 등). 이 경우 시스템은 갱신 전과 같다.
6. 성공하면 `grub-reboot <새 커널 항목>`을 걸고 상태를 `pending_boot`로 둔다. 재부팅은 사용자가 정한다(`--reboot`를 주면 `systemctl reboot`).

드라이버만 바뀌고 커널이 같으면 새 모듈은 재부팅 뒤에만 적재된다. 그 사이 사용자 공간 라이브러리와 적재된 모듈의 버전이 어긋나므로 `reboot_required`를 표시한다.

### 부팅 뒤 `verify`

`opereta-update-verify.service`(oneshot, `opereta-gpu-check`와 `nvidia-persistenced` 뒤)가 상태가 `pending_boot`일 때만 돈다.

- 부팅된 커널이 계획한 새 커널과 같다(`/proc/cmdline`의 `BOOT_IMAGE`, 없으면 `uname -r`).
- GPU가 있으면 `opereta-gpu-check`가 0이고 `/proc/driver/nvidia/version`에 `Open Kernel Module`과 목표 버전이 있다. GPU가 없으면 모듈 파일 검사만 한다.
- 통과하면 `grub-set-default`로 새 커널을 기본으로 확정하고 `committed`로 두며 오래된 커널을 정리한다. 실패하면 `failed`로 두고 기본값은 바꾸지 않는다. 다음 재부팅은 이전 커널로 돌아간다.
- 새 커널이 부팅조차 못 하면 `grub-reboot`는 한 번만 유효하므로 다음 부팅이 이전 커널이다. 그때 verify가 "계획한 커널로 부팅되지 않음"을 `failed`로 기록한다.

### 롤백

`opereta update --rollback`: 이력에서 직전 커널과 드라이버 버전을 읽어, 드라이버가 달랐으면 같은 브랜치의 이전 버전을 NVIDIA 저장소에서 설치하고(없으면 `rollback_unavailable`과 이유를 보여 준다) 저장된 기본 커널을 이전 것으로 바꾼다. 재부팅이 필요하다. GRUB 메뉴에서 이전 커널을 직접 고르는 경로는 항상 남는다.

### GRUB 설정

`/etc/default/grub.d/20-opereta.cfg`에 `GRUB_DEFAULT=saved`, `GRUB_SAVEDEFAULT=false`, `GRUB_DISABLE_SUBMENU=y`, `GRUB_DISABLE_RECOVERY=true`를 둔다. Debian의 `/etc/default/grub`은 `grub.d/*.cfg`를 읽는다(설치 단계에서 확인). 항목 ID는 `gnulinux-<버전>-advanced-<루트 UUID>` 형식이다.

### 시나리오 표(SPEC의 "커널 업데이트 시나리오")

| 시나리오 | 기대 결과 | 검증 |
|---|---|---|
| 새 커널과 같은 브랜치의 새 드라이버, DKMS 성공 | `pending_boot`, 한 번 부팅, verify 통과 뒤 확정, 이전 커널 보존 | 단위 테스트 + QEMU(합성 패키지) |
| DKMS 빌드 실패 | 새 커널 purge, 기본 커널 변경 없음, `dkms_failed` | 단위 테스트 + QEMU(합성 패키지) |
| 새 커널이 부팅하지 못함 | 다음 부팅이 이전 커널, verify가 `failed` 기록 | 단위 테스트(상태 전이), 실기 확인 필요 |
| 부팅은 했으나 verify 실패 | 기본값 유지, 다음 재부팅은 이전 커널 | 단위 테스트 |
| 롤백 | 드라이버를 이전 버전으로, 기본 커널을 이전으로 | 단위 테스트, 저장소에 이전 버전이 있는 경우만 가능 |
| 디스크 부족, 네트워크 없음, dpkg 잠금 | 아무것도 바꾸지 않고 원인 코드 반환 | 단위 테스트 |

## 6. 설치기 설계

### 공통: 설치된 시스템에 필요한 것

- squashfs 안에 `grub-efi-amd64`, `grub-efi-amd64-signed`, `shim-signed`, `efibootmgr`, `dosfstools`, `e2fsprogs`, `rsync`, `parted`/`sfdisk`를 넣는다. 오프라인 설치가 되어야 한다.
- 복사 원본은 실행 중인 `/`가 아니라 마운트된 squashfs(`/run/live/rootfs/filesystem.squashfs`, 램에 올린 경우에도 존재)다. live-config가 부팅 때 만든 `user`가 대상에 섞이지 않는다. 경로는 실제 ISO에서 확인한다.
- 대상에서 `live-boot`, `live-config`, `calamares`(I3)를 제거하고 `update-initramfs -u -k all`을 다시 돌린다. `/etc/hostname`, `machine-id`, 로케일, 시간대, 키보드를 설정한다.
- 대상 마무리(`finalize`, chroot 안에서 실행): `/var/lib/opereta` 상태 디렉터리, 만든 계정을 `users.list`에 기록하고 `opereta`, `video`, `render`(관리자면 `sudo`) 그룹에 넣기, GRUB drop-in, 모드(서버/데스크톱), 첫 부팅 마법사 상태 초기화, 설치 로그를 `/var/log/opereta-install.log`에 남기기.

### `opereta-install`(헤드리스)

```
opereta-install --disk /dev/vda --hostname NAME --user NAME [--admin] --password-stdin \
                 [--mode server|desktop] [--locale ko_KR.UTF-8] [--timezone Asia/Seoul] \
                 [--keyboard kr] --yes
```

- 계정 이름·비밀번호 규칙은 `internal/accounts`와 같다(이름 `^[a-z][a-z0-9_-]{0,31}$`, 비밀번호 8~128자 출력 가능 ASCII).
- 안전 장치: 라이브 매체가 있는 디스크, 마운트된 파티션이 있는 디스크, 최소 크기(16 GiB) 미만인 디스크는 거절한다. `--yes`가 없으면 대화 환경에서는 디스크 경로를 그대로 입력해 확인하고, 비대화 환경에서는 거절한다.
- 단계: 분할(`sfdisk`) → 포맷 → 마운트 → 복사(`rsync -aHAXx`) → fstab(UUID) → chroot 마운트 → 로케일·호스트 이름 → 계정 → live 패키지 제거 → `finalize` → initramfs → `grub-install --uefi-secure-boot` + 폴백 → `update-grub` → 언마운트.
- 모든 단계의 출력은 JSON 줄(`{"step":...}`)과 로그 파일로 나가고, 실패하면 어느 단계에서 어떤 원인인지 코드(`disk_busy`, `copy_failed` 등)로 남긴다. 실패한 설치는 마운트를 풀고 디스크를 그대로 둔다.
- 비밀번호는 표준 입력으로만 받는다. 로그와 프로세스 목록에 남기지 않는다.

### Calamares 경로

- `opereta-installer`가 `/etc/calamares/settings.conf`와 모듈 설정, 브랜딩(Debian 로고 없음), `opereta-calamares` 실행기를 둔다. 순서: welcome, locale, keyboard, partition, users, packagechooser(서버/데스크톱), summary 뒤에 exec로 partition, mount, unpackfs, machineid, fstab, locale, keyboard, localecfg, users, networkcfg, hwclock, shellprocess(`opereta-finalize`), contextualprocess(모드), bootloader, initramfs, packages(live 패키지와 설치기 제거), umount.
- `displaymanager` 모듈은 넣지 않는다(Phase 1 검증의 지적: greetd 처리 코드가 `greeter` 사용자를 가정한다). greetd 설정은 이미 `opereta-desktop`이 둔다.
- `users`: 비밀번호 8~128자, 약한 비밀번호 거부, 루트 비밀번호 없음, 관리자 그룹 `sudo`. `defaultGroups`에 `opereta`, `video`, `render`를 넣는다.
- 첫 부팅 때 Control Center의 마법사가 이어진다(데스크톱을 골랐을 때).

## 7. APT 저장소 설계

- 색인 도구는 `apt-ftparchive`(`apt-utils`)다. 상태 DB가 필요 없고 입력(`out/debs`)에서 결정적으로 같은 결과가 나온다. 스위트 이름은 `opereta`, 구성 요소는 `main`, 아키텍처는 `amd64`와 `all`.
- 서명: `Release`를 키로 서명해 `InRelease`와 `Release.gpg`를 만든다. 개인 키는 저장소에 두지 않는다. 스크립트는 `GNUPGHOME`과 키 ID를 환경 변수로 받는다.
- 클라이언트: `opereta-archive-keyring` 패키지가 `/usr/share/keyrings/opereta-archive-keyring.pgp`와 `/etc/apt/sources.list.d/opereta.sources`(deb822, `Signed-By`)를 둔다. 주소는 `opereta.conf`의 `OPERETA_APT_URL`이며, 비어 있으면 소스 파일은 `Enabled: no`로 생성된다.
- 우선순위: `opereta-*` 패키지만 이 저장소에서 받도록 `apt preferences`로 한정한다. Debian이나 NVIDIA 저장소 패키지를 이 저장소가 덮어쓰지 못하게 한다.
- CI는 임시 키로 저장소를 만들어 컨테이너의 `apt`가 서명을 검증하고 패키지를 설치하는지 확인한다. 실제 서명 키와 호스팅은 사용자 결정이다(아래).

## 8. 위험

- **ISO 크기와 설치 시간.** Calamares와 Qt6가 설치 598 MB(압축 후 약 140 MB)를 더한다. 서버 모드 램은 이미지 용량과 무관하지만, 데스크톱 세션에서 Calamares가 도는 동안은 별도의 램을 쓴다(설치 중이므로 램 목표 대상이 아니다). 실제 크기는 CI의 ISO에서 잰다.
- **실 하드웨어 설치.** QEMU 가상 디스크 설치는 CI가 확인하지만, 실제 NVMe/SATA에서의 분할, NVRAM 항목, 노트북 펌웨어의 부팅 순서 문제는 실기에서만 알 수 있다.
- **Secure Boot.** shim → GRUB → 커널 체인은 OVMF의 Secure Boot 상태에서 시험할 수 있다. MokManager 화면의 키 등록은 자동화하기 어렵고 실기 검증이 필요하다.
- **합성 커널 시험의 한계.** CI에는 두 번째 실제 커널이 없어, 업데이트 E2E는 설치된 커널을 다른 이름으로 복사한 합성 패키지로 DKMS 성공·실패와 GRUB 상태 전이를 시험한다. 실제 Debian 커널 갱신은 실기에서 한 번 확인해야 한다.
- **드라이버 롤백.** NVIDIA 저장소가 이전 버전을 계속 보관한다는 보장은 없다. 없으면 롤백은 실패 이유를 알려 주고 커널만 되돌린다.
- **자동 재부팅 금지.** 데몬이 사용자 허락 없이 재부팅하지 않는다.
- **공개 아티팩트.** CI 아티팩트에는 NVIDIA 드라이버와 CUDA 런타임이 든 ISO가 있다(M6, 사용자 결정 대기).

## 9. 사용자가 정해야 하는 것

| 번호 | 질문 | 기본값 |
|---|---|---|
| Q1 | APT 저장소를 어디에 올릴까? | GitHub Pages(저장소 `nxodh/operetaos`의 Pages). 대안은 별도 서버. 정하기 전에는 클라이언트 소스가 꺼진 채로 나간다 |
| Q2 | 저장소 서명 키는 누가 만들고 어디에 보관할까? | 사용자가 로컬에서 만들고(절차는 `docs/apt-repository.md`) 개인 키는 저장소에 올리지 않는다. 공개 키만 `keys/`에 둔다 |
| Q3 | 설치된 시스템에서 Calamares를 지울까? | 지운다(I3) |
| Q4 | 디스크 암호화(LUKS)와 btrfs 스냅샷 롤백을 이번 단계에 넣을까? | 넣지 않는다. 롤백은 커널·드라이버 단위이고 파일 시스템 스냅샷이 아니다 |
| Q5 | 프로젝트 자체 라이선스(U7)를 저장소 공개 전에 정할까? | 정하기 전에는 패키지 저장소를 공개하지 않는 편이 안전하다 |
| Q6 | 기본 `user`/`live` 계정을 설치된 시스템에서는 아예 만들지 않아도 되나? | 만들지 않는다(설치기가 사용자가 만든 계정만 둔다) |
