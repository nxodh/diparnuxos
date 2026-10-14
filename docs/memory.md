# 메모리 예산과 측정 계획

- 확인 날짜: 2026-10-12
- 상태: 수정 전(0.1.0)의 QEMU 실측값이 있다. 수정 후(0.1.2) 실측값은 미측정이다. 드라이버를 로드한 실기 값은 아직 없다.

Opereta의 목표는 램을 최소로 쓰면서 로컬 LLM 추론 서버를 돌리는 것이다. 이 문서는 목표치, 측정 방법 초안, 실측값 표를 둔다. 실측값은 QEMU에서 잰 수정 전 값이 있다(3절). 드라이버를 로드한 실기 값은 아직 없다.

---

## 1. 목표치

> **2026-10-14(0.6.2): 램 상한을 해제했다(사용자, [decisions.md](decisions.md) U19).** 미적 요소와 사용성이 성능만큼 중요하므로, 디자인을 고칠 때 램 사용량을 이유로 제한하지 않는다(창 합성, 그림자, 글꼴 등). CI 의 E2E 는 유휴 램을 기록만 하고 실패시키지 않는다. 아래의 500/800 MiB 는 0.5.4 에 확정했던 참고 기준선이며 더 이상 상한이 아니다. 새 기준선은 0.6.2 의 첫 CI 값부터 이 문서에 적는다.
>
> **(해제 전) 2026-10-14: 새 목표를 확정했다(사용자): 유휴 램 상한은 헤드리스(서버 모드) 500 MiB, 데스크톱 모드 800 MiB 이다.** 측정은 `MemTotal - MemAvailable`, 로그인 직후, 추론 요청 없음(`llama-server` 제외)이다.
> CI 의 QEMU 기준선은 헤드리스 약 374 MiB, 데스크톱 약 679 MiB 이고(3.3절), 데스크톱 E2E 가 이 상한을 넘으면 실패한다(`tests/qemu/desktop-session.py`). 실기(드라이버, NIC)의 값은 아직 없어 확정된 것은 상한이며 달성 여부는 실기에서 재야 한다.
>
> **2026-10-12: 아래 램 목표(헤드리스 600 MB, GUI 900 MB)는 사용자가 철회했다**([decisions.md](decisions.md) U13). 0.5.0(Xfce)은 먼저 동작하게 만들고, 그다음 경량화 단계에서 실측을 바탕으로 새 목표를 정한다. 표의 수치는 철회 전 값이며 참고용이다. `llama-server` 제외와 `operetad` 30 MB 이하는 따로 정해지기 전까지 참고로만 둔다.

| 구분 | 상태 | 목표 (유휴 기준) |
|---|---|---|
| 헤드리스 | 그래픽 세션 없음, 부팅 후 multi-user 상태 | ~~500 MiB 이하~~ 상한 없음(0.6.2 에서 해제, 기록만) |
| 데스크톱(Xfce, lightdm) | 로그인 직후, Control Center 열림 | ~~800 MiB 이하~~ 상한 없음(0.6.2 에서 해제, 기록만) |
| `operetad` | 데몬 프로세스 하나 | 30 MB 이하 |
| `llama-server` | 추론 서버 | **목표에서 제외.** 모델 크기와 컨텍스트에 따라 크게 달라진다. |

- 목표는 유휴 상태(추론 요청이 없을 때)에서 잰다.
- 사용자가 2026-10-12에 목표를 완화했다. 헤드리스는 300 MB에서 400 MB로, 이어서 600 MB로, GUI는 700 MB에서 900 MB로 바꿨다. 이 문서는 목표와 실측을 모두 MiB(1024 기준)로 비교한다. 십진 MB와의 차이(약 4.9%)는 이제 목표 여유보다 작다.
- 무선 접속 데몬 iwd가 상주 프로세스로 추가됐다. 유휴 램에 미치는 영향은 미측정이다.
- `llama-server`는 모델을 올리는 순간 램과 VRAM을 크게 쓰므로, 메모리 예산에서 빼고 따로 본다.
- 유휴 기준 시간은 부팅 후 안정화 시간 이후로 한다. 값은 Phase 1 측정 방법이 정해지면 구체적인 시간을 적는다.

---

## 2. 측정 방법 (초안)

측정 도구와 옵션은 아직 확인하지 않았다. 아래 방법은 초안이며, 쓰기 전에 각 명령을 실제 이미지에서 `--help`나 man 페이지로 확인한다.

### 2.1 시스템 전체

- `/proc/meminfo`의 `MemTotal`, `MemAvailable`로 쓰는 양을 계산한다. 사용량 = `MemTotal - MemAvailable`로 본다.
- 부팅 후 안정화 시간을 기다린 뒤 여러 번 샘플링하고 중앙값을 쓴다.

### 2.2 서비스 단위

- systemd 서비스의 메모리 사용량은 `systemctl show`의 메모리 속성으로 본다. 속성 이름은 man 페이지 `systemd.resource-control`로 확인한다.
- 서비스가 cgroup에서 쓰는 양을 기준으로 한다.

### 2.3 프로세스 단위

- `operetad`의 RSS와 PSS를 본다. RSS는 공유 라이브러리를 중복 계산하므로 PSS를 함께 본다.
- PSS를 읽는 방법은 `/proc/<pid>/smaps_rollup`을 쓴다(확인 필요).

### 2.4 측정 조건

- 측정 전에 같은 이미지, 같은 부팅 조건을 쓴다.
- 헤드리스 측정은 그래픽 세션이 뜨기 전에 한다.
- GUI 측정은 labwc와 greetd 세션이 완전히 뜬 뒤에 한다.
- 측정 때 `llama-server`를 띄우지 않는다.

### 2.5 기록

측정 결과는 [이 문서의 실측 표](#3-실측값)에 날짜, 이미지 버전, 조건과 함께 적는다.

---

## 3. 실측값

| 항목 | 목표 | 실측 | 측정 날짜 | 이미지 버전 | 조건 |
|---|---|---|---|---|---|
| 헤드리스 유휴 | 600 MB 이하 (당시 목표 300 MB) | 234 MiB (부팅 3분, NIC 없음). 3시간 52분 방치 후 319 MiB로 목표를 넘었다. | 2026-10-11 | 0.1.0 (수정 전) | QEMU 10.0.13 TCG, vCPU 1, 메모리 2G, GPU 없음, 0.1.0 ISO, 수정 전 |
| 헤드리스 유휴 (드라이버 로드) | 600 MB 이하 | 미측정 | — | — | 실기 NVIDIA GPU 필요 |
| GUI 유휴 | 900 MB 이하 | 약 634 MiB (Control Center 창과 패널 포함, 단일 값) | 2026-10-12 | 0.2.0 + Phase 3 | QEMU/KVM, vCPU 2, 메모리 4 GiB, GPU 없음, 부팅 직후. 실기 값이 아니다 |
| `operetad` 유휴 | 30 MB 이하 | 미측정 | — | — | — |
| 헤드리스 유휴 (수정 후) | 600 MB 이하 | 약 374~384 MiB (목표 이내) | 2026-10-12 | 0.2.0 + Phase 3 | QEMU/KVM, vCPU 2, 메모리 4 GiB, GPU 없음, 로그인 직후 5초, `operetad` 실행 중. 0.1.0 측정과 조건이 달라 직접 비교할 수 없다 |

### 3.1 수정 전 측정 기록 (0.1.0, QEMU)

모든 값은 QEMU 10.0.13(TCG, arm64 호스트, q35, vCPU 1, 메모리 2G, GPU 없음)의 라이브 부팅 값이다.

| 조건 | used (MiB) | 비고 |
|---|---|---|
| NIC 없음, 부팅 3분(로그인 후 약 100초) | 234 | free 1684, shared 23, buff/cache 202, available 1731 |
| NIC 있음, 부팅 3분 | 251, 257 | 두 번 부팅. ifupdown과 dhcpcd가 같은 NIC를 관리했다 |
| Secure Boot 켬, 4.5분 | 276 | modprobe 시도 후 |
| NIC 없음, 3시간 52분 방치 | 319 | shared 66, Shmem 75.9 MB |
| `apt-get update` 직후 | 379 | shared 122 |

- 3시간 52분 방치 시점의 RSS 상위(kB): systemd-resolved 14136, systemd 13992, systemd-networkd 11212, systemd-journald 10080, systemd-udevd 9864, systemd-logind 8592, dbus-daemon 4420.
- 드라이버를 로드한 상태의 유휴 램은 잴 수 없었다. NIC가 있을 때 목표까지 여유는 43~49 MB다.
- zram: `/dev/zram0` lz4 983M, 우선순위 100, 사용 0.

### 3.2 목표를 넘은 원인과 0.1.2의 수정

- 3시간 52분 방치 뒤 overlay(tmpfs) 74 MiB 중 `/var/cache/apt`가 46 MiB, `/var/log/journal`이 21 MiB였다. 라이브 세션에서 `/var/log/journal`은 램(overlay tmpfs)에 있다. 저널 상한은 `System Journal ... max 98.2M`이었다.
- apt 일일 타이머 `apt-daily.timer`와 `apt-daily-upgrade.timer`가 활성이어서 apt 목록과 캐시를 램에 채웠다.
- ISO에는 apt 목록 153 MB와 캐시 94 MB가 들어 있었다.

0.1.2에서 바꾼 것:

- journald를 `Storage=volatile`과 `RuntimeMaxUse=16M`으로 제한했다(`build/config/includes.chroot/etc/systemd/journald.conf.d/opereta.conf`).
- apt-daily와 apt-daily-upgrade 타이머를 `/dev/null`로 마스크했다. 갱신은 Phase 4의 `opereta-update`가 맡는다.
- `--apt-indices false`로 apt 목록을 이미지에서 뺐다. `--apt-source-archives false`로 deb-src를 끈다(`build/auto/config`).
- ifupdown과 dhcpcd-base를 제거해 DHCP 클라이언트를 systemd-networkd 하나로 줄였다. 이것이 램에 주는 영향은 측정하지 않았다.

**0.1.2의 유휴 램 실측값은 미측정이다.** 같은 조건(QEMU, 3시간 방치)으로 다시 재야 목표 달성을 말할 수 있다.

### 3.3 0.5.2 기준선(CI QEMU, 2026-10-14, 이미지 0.5.2)

CI 실행 38453616075 의 로그에서 가져온 값이다. 조건은 QEMU(KVM 없는 TCG 가능), UEFI, GPU 없음, 메모리 4 GiB(설치된 시스템은 3 GiB), 로그인 직후이며 `llama-server` 는 돌지 않는다.
**실기 값이 아니다.** 라이브 이미지는 squashfs 페이지 캐시와 tmpfs 위 overlay 를 쓰므로 설치된 시스템과 다를 수 있다.

| 상태 | 사용량(MemTotal - MemAvailable) | 비고 |
|---|---|---|
| 라이브, 서버 모드(헤드리스) | 약 374 MiB | 383,360 kB |
| 설치된 시스템, 서버 모드 | 약 374 MiB | 382,684 kB(3 GiB VM) |
| 라이브, 데스크톱 모드(Xfce 세션, Control Center 열림) | 약 679 MiB | 695,004 kB |
| 서버 모드로 되돌린 뒤 | 약 551 MiB | 데스크톱 구성 요소가 내려가도 페이지 캐시가 남아 있다 |

0.5.4 CI(실행 38512753440, 같은 조건): 서버 모드 371 MiB, 데스크톱 모드 703 MiB. 확정한 상한(500/800 MiB) 이내이며 데스크톱은 0.5.2 보다 약 24 MiB 많다(한 번의 측정).

데스크톱 모드의 프로세스별 RSS(kB, 8분 측정 동안 거의 변하지 않았다):

| 프로세스 | RSS | 비고 |
|---|---|---|
| opereta-control-center | 약 215,000 | Python + GTK4 + libadwaita. 가장 크다 |
| Xorg | 약 150,000 | |
| xfdesktop | 약 36,000~44,000 | |
| operetad | 약 36,000 | 0.1.x 의 30 MB 참고 목표를 조금 넘는다. 시작 직후 33~34 MB |
| xfce4-panel | 약 31,700 | |
| fcitx5 | 약 31,000 | |
| xfwm4 | 약 30,700 | |

누수 점검(8분): 어느 프로세스도 늘지 않았다(operetad +68 kB, 나머지 0).

**새 램 목표(2026-10-14 사용자가 제안대로 확정)**: 이 값은 "먼저 동작하게, 경량화는 그다음" 방침에 따라 정한 기준선이며 목표가 아니다. 제안은 다음과 같다.

- 헤드리스 서버 모드: 500 MiB 이하를 상한으로 둔다(현재 약 374 MiB. 실기는 드라이버와 NIC 로 더 크다).
- 데스크톱 모드: 800 MiB 이하(현재 약 679 MiB).
- 줄일 곳은 Control Center(약 215 MB)와 Xorg 다. 후보: Control Center 의 페이지를 처음 열 때 만들기(지금은 시작 때 모두 만든다), `fcitx5` 는 필요할 때만.
- 이 값들은 0.6.2 에서 해제되었다(위 안내). CI 의 데스크톱 E2E 는 값을 기록만 한다.

---

## 4. DKMS 때문에 최종 이미지에 들어가는 빌드 도구

- NVIDIA 오픈 커널 모듈은 DKMS로 빌드한다. 이 때문에 빌드 도구가 최종 이미지에 남는다.
- `nvidia-kernel-open-dkms`의 Depends에 `g++`가 있다(검증 기록 기준). `dkms`도 Depends에 있다(3.1.8 이상).
- 2026-10-11에 Debian `dkms` 메타데이터의 `Depends: kmod, dpkg-dev, make | build-essential, patch`를 확인했다. 따라서 `make`와 `dpkg-dev`도 최종 이미지에 들어간다.
- Docker 메모리가 약 4 GB인 환경을 고려해 DKMS 병렬 작업 수는 2로 둔다. 의존성 설치 중에도 같은 값을 적용한다. 이는 컴파일 메모리 설정이며 유휴 램 목표 달성을 뜻하지 않는다.
- 이 도구들은 램을 직접 쓰지 않지만 디스크와 페이지 캐시를 차지한다. 헤드리스 예산 계산에서는 디스크 점유를 따로 적는다.
- 설치된 시스템에서 DKMS를 제거하면 보안 업데이트로 커널이 바뀔 때 모듈을 다시 빌드할 수 없다. 그래서 DKMS를 남기는 것을 기본으로 한다([drivers.md](drivers.md) 10절).
- 빌드 도구를 줄이는 방법은 Phase 1 이후 검토한다. 줄이면 재빌드 경로가 깨지지 않는지 확인해야 한다.

근거: https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz (`nvidia-kernel-open-dkms` Depends), https://raw.githubusercontent.com/dell/dkms/v3.2.2/README.md (DKMS 동작)

---

## 5. 열린 문제

- 측정 명령과 옵션 확인(2절).
- 유휴 기준 시간 정하기(1절).
- 빌드 도구를 최종 이미지에서 줄이는 방법 검토(4절).
