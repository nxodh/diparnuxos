# 설치

- 확인 날짜: 2026-10-12
- 상태: 구현됨, **QEMU 가상 디스크 설치만 확인했다**([검증 기록](verification-phase-4.md)). 실기 설치는 미검증이다.

Diparnux ISO 는 라이브 시스템이다. 디스크에 설치하는 방법은 둘이다.

| 방법 | 어디서 | 명령 |
|---|---|---|
| 그래픽 설치기(Calamares) | 라이브 데스크톱 세션 | `diparnux-installer`(메뉴의 "Install Diparnux") |
| 콘솔 설치기 | 서버 모드 콘솔, 직렬 콘솔, SSH | `sudo diparnux-install run` |

두 방법은 같은 마무리 단계를 쓴다: 라이브 전용 패키지(live-boot, live-config)와 설치기 제거, 기본 모드 설정, initramfs 재생성, shim 으로 GRUB 설치, 저장된 기본 부팅 항목 고정.

## 요구 사항

- UEFI 부팅(BIOS 는 지원하지 않는다). 디스크는 GPT 로 만든다.
- 디스크 16 GiB 이상, 램 1.5 GiB 이상(설치기). 모델 파일을 쓰려면 더 필요하다.
- 설치는 네트워크를 쓰지 않는다. 필요한 패키지(서명된 부트로더 포함)가 이미지 안에 있다.
- 지원하는 디스크 이름: `/dev/sdX`, `/dev/vdX`, `/dev/nvmeXnY`, `/dev/mmcblkX`(파티션이 아닌 디스크 전체).

## 디스크 구성

설치기는 **선택한 디스크 전체를 지운다.** 만들어지는 구성은 다음과 같다.

| 파티션 | 크기 | 파일 시스템 | 마운트 |
|---|---|---|---|
| 1 | 1 GiB | FAT32 (EFI System) | `/boot/efi` |
| 2 | 나머지 | ext4 | `/` |

swap 파티션은 만들지 않는다(zram). 디스크 암호화(LUKS), LVM, btrfs 스냅샷은 이번 단계에서 지원하지 않는다. 롤백은 파일 시스템 스냅샷이 아니라 커널과 드라이버 단위다([updates.md](updates.md)).

그래픽 설치기에서는 수동 파티션도 고를 수 있다. Calamares 3.3.14 에는 "옆에 설치"(리사이즈)와 "교체" 선택지를 숨기는 설정이 없어서 화면에 나온다. 이 두 방식과 수동 파티션은 Diparnux 가 시험하지 않았다. 확인한 구성은 "디스크 지우기"뿐이다.

## 콘솔 설치기

```sh
sudo diparnux-install run                      # 터미널에서 값을 묻는다
printf '%s\n' "$PASSWORD" | sudo diparnux-install run \
    --disk /dev/nvme0n1 --hostname mybox --user alice --password-stdin \
    --mode server --locale ko_KR.UTF-8 --timezone Asia/Seoul --keyboard kr --yes
```

- `--yes` 가 없으면 터미널에서 디스크 경로를 다시 입력해야 한다. 비대화 환경에서는 `--yes` 가 필요하다.
- 비밀번호는 표준 입력의 첫 줄이나 터미널 입력으로만 받는다. 인자, 환경 변수, 파일, 로그에 남지 않는다.
- 사용자 이름과 비밀번호 규칙은 `diparnux user add` 와 같다(이름은 소문자로 시작하는 소문자·숫자·`_`·`-` 32자 이하, 비밀번호는 8~128자의 출력 가능한 ASCII 이고 이름과 달라야 한다).
- `--mode server|desktop` 은 설치 직후 기본 모드다. 나중에 `diparnux mode` 로 바꾼다.
- `--console serial` 은 커널과 GRUB 콘솔을 `ttyS0,115200` 에도 둔다.
- `--json` 이면 진행이 JSON 줄로 나온다. 마지막 줄이 결과(`{"ok":true,...}`)다.
- 진행 기록은 `/var/log/diparnux-install.log`(라이브 시스템 쪽)에 남는다. 비밀번호는 들어 있지 않다.

설치기가 거절하는 경우: 마운트된 파티션이나 swap 이 있는 디스크(라이브 USB 가 있는 디스크 포함), 16 GiB 보다 작은 디스크, 읽기 전용 디스크, 파티션 장치(`/dev/sda1`), 규칙에 맞지 않는 이름. 거절은 디스크를 건드리기 전에 일어난다.

## 설치 뒤

- 설치된 시스템에는 기본 `user`/`live` 계정이 없다. 설치 때 만든 계정만 있다(관리자 `sudo` 그룹, `diparnux`, `video`, `render` 그룹).
- 루트 계정은 잠겨 있다.
- SSH 는 꺼져 있다. 켜면 이 방식으로 만든 계정만 비밀번호로 들어올 수 있다(`diparnux ssh on`).
- 첫 부팅 마법사는 데스크톱 모드의 첫 로그인에서 이어진다.
- 설치기(Calamares 와 의존 라이브러리)는 설치된 시스템에서 지워진다.
- Secure Boot 가 켜진 기기에서 NVIDIA 모듈을 쓰려면 한 번 `sudo diparnux-mok` 로 키를 등록한다([secure-boot.md](secure-boot.md)). 이 흐름은 설치된 시스템에서만 끝까지 간다.

## 설치기에서 비밀번호가 거절될 때

0.5.1 이미지의 그래픽 설치기에서 사용자 탭의 비밀번호가 "암호가 사전 검사에 실패했습니다 - error loading dictionary" 로 거절되면, 이미지에 비밀번호 사전 파일(cracklib)이 없어서 어떤 비밀번호도 거절되는 문제다. 0.5.2 부터는 설치기가 사전 검사를 끈다(규칙은 8~128자의 출력 가능한 ASCII).

0.5.1 이미지에서 지금 설치하려면 다음 중 하나를 쓴다.

```sh
# 방법 1: 네트워크가 있을 때. 사전을 설치한 뒤 같은 창에서 비밀번호를 다시 입력한다.
sudo apt-get update
sudo apt-get install -y cracklib-runtime wamerican

# 방법 2: 콘솔 설치기(그래픽 설치기를 쓰지 않는다. 같은 설치 결과다).
sudo diparnux-install run
```

## 알려진 한계

- 실기에서 설치해 본 적이 없다. 노트북 펌웨어가 부팅 항목(NVRAM)을 받아들이지 않으면 설치기는 항목 등록 없이 파일만 설치하고 대체 경로(`EFI/BOOT/BOOTX64.EFI`)에 의존한다. 이 경우 펌웨어 메뉴에서 디스크를 직접 고른다.
- 다른 운영체제와의 이중 부팅은 시험하지 않았다. GRUB 의 os-prober 는 설치하지 않는다.
- 그래픽 설치기의 전체 흐름(화면을 눌러 끝까지 설치)은 자동 시험하지 않았다. 자동 시험은 설정이 Calamares 모듈과 맞는지, 라이브 데스크톱에서 창이 뜨는지, 콘솔 설치기의 전체 흐름까지다.
