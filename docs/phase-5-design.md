# 0.5.0 설계: Xfce 데스크톱과 Diparnux 디자인

- 확인 날짜: 2026-10-12
- 상태: **설계 초안이며 구현을 시작했다.** 9절의 질문은 기본값으로 진행하고, 사용자가 다른 답을 주면 그때 바꾼다. "확인한 사실"은 Debian 13(trixie) amd64 컨테이너의 `apt-cache`와 시뮬레이션 설치로 2026-10-12에 조회한 값이다. 실기와 QEMU 동작은 아직 확인하지 않았다.

범위는 0.5.0 하나다. 데스크톱 환경을 labwc(Wayland)에서 **Xfce**로 바꾸고, Diparnux 로고와 통일된 디자인을 입히며, 일반 배포판에 가까운 사용성을 준다. 램 줄이기와 실측은 이 버전이 잘 동작한 다음의 별도 단계다(1절).

---

## 1. 사용자 요구와 합의

| 날짜 | 내용 |
|---|---|
| 2026-10-12 | 0.5.0은 데스크톱 환경을 Xfce로 한다. 디자인은 Diparnux 로고와 통일감 있게 최대한 반영한다. 현재 존재하는 리눅스 배포판에 근접한 사용성을 주되(우분투처럼 무거운 기능을 모두 구현할 필요는 없다) 컴퓨팅 자원은 최대한 덜 쓰게 한다. |
| 2026-10-12 | **램 목표(서버 600 MB, 데스크톱 900 MB)를 철회한다.** 일단 잘 동작하게 만들고, 그 다음 검토해서 최대한 경량화한다. |

이 설계가 따르는 규칙:

- 0.5.0의 합격 조건에 램 수치를 쓰지 않는다. 대신 **누수 검사**(8분 소크 시험의 프로세스별 증가량)는 유지한다. 수치 목표는 경량화 단계에서 실측을 바탕으로 새로 정한다.
- 그래도 자원을 덜 쓰는 방향은 버리지 않는다. 새 구성 요소는 필요한 것만 넣고, 상주 프로세스를 늘리는 항목은 이유를 적는다(4절).
- 실기 검증 전에는 "정식 지원"이라고 쓰지 않는다. 이 단계의 자동 검증은 QEMU(가상 GPU)까지이고 NVIDIA 실기 화면은 사용자 확인이 필요하다.

## 2. 지금 있는 것과 바뀌는 것

현재 데스크톱(0.4.1)은 labwc + greetd(tuigreet) + yambar + foot + swaybg + 직접 만든 Control Center다. Xfce로 바꾸면 아래가 움직인다.

| 영역 | 지금 | 0.5.0 |
|---|---|---|
| 디스플레이 서버 | Wayland(labwc, wlroots 0.18), Xwayland는 호환용 | **Xorg**(`xserver-xorg-core`) + xfwm4. 이유는 4절 X1 |
| 로그인 | greetd + tuigreet(텍스트) | lightdm + lightdm-gtk-greeter(그래픽, Diparnux 테마) |
| 패널·배경·파일 관리자·터미널 | yambar, swaybg, 없음, foot | xfce4-panel, xfdesktop, Thunar, xfce4-terminal |
| 세션 래퍼 | `diparnux-session`(pixman, `XDG_SESSION_TYPE=wayland`) | 필요 없음. Xorg는 가상 머신에서 `modesetting`으로 동작하는지 QEMU로 확인 |
| 모드 전환 | `diparnux mode desktop` → `graphical.target` → `greetd.service` | 같다. 확인 대상 유닛만 `lightdm.service`(별칭 `display-manager.service`)로 바뀐다 |
| 설치기(Calamares) | Wayland 위에서 `sudo`로 실행(`qt6-wayland`) | Xorg 위에서 실행. `qt6-wayland` 의존을 뺀다 |
| Control Center | GTK4/libadwaita, Wayland | 그대로. GTK4 앱은 X11에서도 동작한다. 창 아이콘·색만 새 테마에 맞춘다 |
| 네트워크 | iwd + `diparnux network`, 패널 스크립트 | 같은 스택을 유지하고 패널 플러그인(`xfce4-genmon-plugin`)으로 표시(4절 X5) |

코드에서 이 구성에 걸린 곳(0.4.1 기준): `packages/diparnux-desktop/**`(전부 교체), `build/config/package-lists/diparnux.list.chroot`, `daemon/internal/supervisor/system.go`(`displayManagerUnit`)와 그 단위 시험, `daemon/internal/accounts/accounts.go`·`daemon/dist/diparnux-user.sh`(예약어에 `lightdm` 추가), `tests/qemu/desktop-session.py`(Wayland 확인 → X11 확인), `tests/nvidia/image.sh`, `scripts/test-desktop-config-in-docker.sh`, `.github/workflows/iso.yml`, `packages/diparnux-installer`(Wayland 의존과 실행기), `docs/design.md`, `docs/desktop-troubleshooting.md`, `docs/decisions.md`, `docs/memory.md`, `SPEC.md`.

## 3. 확인한 사실

Debian 13 컨테이너(`--platform linux/amd64`)에서 `apt-cache policy`와 `apt-get -s --no-install-recommends install`로 조회했다. 크기는 **빈 컨테이너에 처음부터 설치한 값**이라 이미지에 이미 있는 라이브러리와 겹친다. 이미지에 더해지는 양은 이보다 작다.

### 버전

| 패키지 | 버전 | 비고 |
|---|---|---|
| xfce4-session / xfwm4 / xfce4-panel / xfdesktop4 / xfce4-settings | 4.20.2 / 4.20.0 / 4.20.4 / 4.20.1 / 4.20.1 | Xfce 4.20 |
| thunar / xfce4-terminal / mousepad / ristretto | 4.20.2 / 1.1.4 / 0.6.3 / 0.13.3 | |
| lightdm / lightdm-gtk-greeter | 1.32.0 / 2.0.9 | `slick-greeter` 2.0.9도 있다 |
| xserver-xorg-core / xserver-xorg-input-libinput | 21.1.16 / 1.5.0 | NVIDIA의 `nvidia-driver`가 이미 `xserver-xorg-core`와 X 드라이버를 끌어온다([drivers.md](drivers.md)) |
| pipewire / wireplumber / pipewire-pulse | 1.4.2 / 0.5.8 / 1.4.2 | |
| network-manager / network-manager-gnome / iwd | 1.52.1 / 1.36.0 / 3.8 | 이미지에는 iwd만 있다 |
| xfce4-whiskermenu-plugin / xfce4-genmon-plugin / xfce4-pulseaudio-plugin | 2.9.2 / 4.1.1 / 0.4.9 | |
| xfce4-power-manager / xfce4-notifyd / xfce4-screenshooter / xfce4-taskmanager | 4.20.0 / 0.9.7 / 1.11.1 / 1.5.8 | |
| light-locker / xfce4-screensaver | 1.8.0 / 4.18.4 | |
| mate-polkit-bin / polkitd | 1.26.1 / 126 | `xfce-polkit`은 trixie에 없다 |
| firefox-esr / chromium | 153.4.0esr / 154.0.8037.92 | 둘 다 trixie(보안 저장소 포함) |
| fcitx5 / fcitx5-hangul / ibus-hangul | 5.1.12 / 5.1.6 / 1.5.5 | 한글 입력기 후보 |
| open-vm-tools-desktop / spice-vdagent | 후보(버전은 구현 때 기록) | VMware, QEMU 게스트 통합 |
| adwaita-icon-theme / papirus-icon-theme / elementary-xfce-icon-theme | 48.1 (13 MiB) / 182 MiB / 66 MiB | 설치 크기 |

### 묶음 크기(빈 컨테이너 기준, 겹침 포함)

| 묶음 | 패키지 수 | 설치 크기 |
|---|---|---|
| 현재 데스크톱(labwc, greetd, tuigreet, foot, yambar, swaybg, xwayland, dbus-user-session) | 184 | 약 324 MiB |
| 0.5.0 기본 후보(Xfce 핵심, lightdm, Xorg, 오디오, 앱, 테마 아이콘) | 431 | 약 803 MiB |
| 위 + firefox-esr | 434 | 약 1362 MiB(+559 MiB) |
| 위 + fcitx5, fcitx5-hangul, GTK3 프런트엔드 | 449 | 약 829 MiB(+26 MiB) |
| 위 + open-vm-tools-desktop | 453 | 약 823 MiB(+20 MiB) |
| 위 + spice-vdagent | 432 | 약 803 MiB(+0 MiB) |
| 위 + NetworkManager + nm-applet | 446 | 약 824 MiB(+21 MiB) |

설치 크기는 디스크와 ISO 크기에 영향을 주며, 부팅 후 램과는 다르다. 램은 상주 프로세스로 결정되고 경량화 단계에서 잰다.

### Xfce Wayland에 대해

- Xfce 4.20은 Wayland를 부분적으로만 지원한다. `startxfce4 --wayland`가 있고 `xfce4-session`이 labwc용 `labwc-rc.xml`과 `xfce-wayland.desktop` 세션을 싣고 있지만, 창 관리자 xfwm4는 Wayland를 지원하지 않아 labwc 같은 별도 컴포지터가 필요하다.
- 즉 "Xfce on Wayland"는 labwc 위에 Xfce 구성 요소를 얹는 실험 경로다. 이 경로는 labwc 0.8.3의 explicit sync 부재(D6)와 NVIDIA 문제를 그대로 안고 있다([decisions.md](decisions.md) D6).

### 하이브리드 GPU

대상 노트북(MSI Stealth 15M 등)은 인텔 내장 GPU와 NVIDIA GPU를 함께 가진다. Xorg에서는 내장 GPU가 화면을 맡고 NVIDIA GPU는 연산용(CUDA)과 선택적 오프로드로 쓰는 구성이 기본이다. 이 경우 LLM이 쓸 VRAM을 데스크톱이 가져가지 않는다는 장점이 있다. 반대로 외부 모니터가 NVIDIA 출력에 직접 물린 노트북에서는 추가 설정이 필요할 수 있다. 이것은 **실기에서만 확인된다**.

## 4. 결정과 기본값

사용자가 바꾸지 않으면 아래 기본값으로 진행한다. 사용자가 정해야 하는 것은 9절에 따로 모았다.

| 번호 | 항목 | 기본값 | 이유와 대안 |
|---|---|---|---|
| X1 | 디스플레이 서버 | Xorg + xfwm4(X11 세션). Wayland 세션은 0.5.0에 넣지 않는다 | Xfce 4.20의 Wayland는 실험적이다(3절). Xorg는 NVIDIA 오픈 모듈과 `xserver-xorg-video-nvidia`가 이미 있는 검증된 경로다. 대안은 labwc 위의 Xfce(실험, NVIDIA 위험) |
| X2 | 로그인 화면 | lightdm + lightdm-gtk-greeter, Diparnux 배경·로고·색 | 가볍고 설정 파일로 테마를 입힐 수 있다. 대안: slick-greeter(더 화려함, 설정 키가 다름), sddm(Qt, 무거움) |
| X3 | 패키지 구성 | 메타패키지 `xfce4`를 쓰지 않고 구성 요소를 개별로 지정, `--no-install-recommends` | `xfce4`의 Recommends는 `xorg`(영상 드라이버 대량), `desktop-base`, `tango-icon-theme` 등을 끌어온다 |
| X4 | 오디오 | PipeWire + WirePlumber + pipewire-pulse, 패널에 `xfce4-pulseaudio-plugin`, 믹서는 `pavucontrol` | 최신 배포판의 기본 구성이다. `pipewire-pulse`가 PulseAudio 호환 서버를 대신한다 |
| X5 | 네트워크 | **iwd + systemd-networkd 유지.** 패널에 `xfce4-genmon-plugin`으로 상태(`diparnux-panel-net`)를 보이고, 클릭하면 Control Center의 네트워크 쪽을 연다 | 서버 모드와 데스크톱 모드가 한 스택을 쓰고, 0.4.1에서 만든 데몬 API·CLI·GUI를 그대로 쓴다. 한계: 유선 고정 IP, VPN, 기업용 Wi-Fi(802.1X), 핫스팟은 GUI가 없다. 대안: NetworkManager + nm-applet(+21 MiB 설치, 데몬 백엔드 교체 필요) |
| X6 | 파일 관리자 | Thunar + `thunar-volman` + `gvfs` + `udisks2` + `tumbler` | USB 자동 마운트, 휴지통, 썸네일 등 일반적인 동작에 필요하다 |
| X7 | 터미널·편집기·기타 앱 | xfce4-terminal, mousepad, ristretto(이미지), xarchiver(압축), xfce4-screenshooter, xfce4-taskmanager, xfce4-appfinder | 일상 작업의 최소 구성. 오피스·미디어 플레이어는 넣지 않는다 |
| X8 | 애플리케이션 메뉴 | `xfce4-whiskermenu-plugin`(검색 가능한 시작 메뉴) | 일반 배포판의 사용성에 가깝다(+1 MiB) |
| X9 | 인증 에이전트 | `mate-polkit-bin` | Xfce 권장 구성. `polkitd`는 설치기 때문에 이미 필요하다 |
| X10 | 화면 잠금·전원 | `light-locker`(lightdm 연동), `xfce4-power-manager`, `xfce4-notifyd` | `xfce4-screensaver`는 의존성이 커서 후보에서 뺐다(약 345 MiB, 겹침 포함) |
| X11 | 테마 | 아래 6절. GTK3 `Diparnux` 테마, xfwm4 `Diparnux` 테마, 아이콘은 Adwaita, 배경과 로고는 `diparnux-branding` | 새 패키지 없이 `adwaita-icon-theme`(13 MiB)를 쓴다. 대형 아이콘 테마(Papirus 182 MiB)는 쓰지 않는다 |
| X12 | 설정 기본값 배포 | `/etc/xdg/xfce4/xfconf/xfce-perchannel-xml/*.xml`과 `/etc/xdg/xfce4/panel/default.xml`로 시스템 기본값을 둔다 | 사용자가 바꾸면 사용자 설정이 우선한다. 새 사용자는 첫 로그인부터 Diparnux 모양이다 |
| X13 | 라이브 세션 | 자동 로그인(`user`), 설치된 시스템은 로그인 화면 | 일반 라이브 이미지의 동작이다. 라이브에서만 켜지는 방법은 7절 |
| X14 | 서버 모드 | 변하지 않는다. 기본 부팅 대상 `multi-user.target`, X 서버와 lightdm은 뜨지 않는다 | SPEC의 서버 우선 정책 |

### 이번에 넣지 않는 것

- Wayland 세션, NetworkManager, 블루투스(`blueman`), 프린터(CUPS), 오피스 제품군, 미디어 플레이어, 소프트웨어 센터(GUI 패키지 관리자), 방화벽 GUI. 필요하면 `apt`로 설치한다. 소프트웨어 센터가 필요한지는 9절에서 묻는다.
- 브라우저는 기본값에서 **넣을지 말지가 사용자 결정**이다(9절 Y1).

## 5. 작업 묶음

| 묶음 | 내용 | 소유 파일 | 검증 |
|---|---|---|---|
| A. 패키지와 이미지 | `diparnux-desktop` Depends 교체, 패키지 목록, 서비스 활성화(`lightdm`은 기본 부팅 대상을 바꾸지 않고 설치) | `packages/diparnux-desktop/debian`, `build/config/package-lists` | 이미지 검사(`tests/nvidia/image.sh`), QEMU |
| B. 세션 기본값 | xfconf XML(세션, 창 관리자, 데스크톱, 설정, 전원, Thunar), 패널 배치, 자동 시작 항목 | `packages/diparnux-desktop/src` | 컨테이너에서 XML 구문 검사, Xvfb에서 패널 시작 확인 |
| C. 로그인 | lightdm 설정, greeter 테마, 라이브 자동 로그인(live-config), 설치된 시스템의 로그인 화면 | `packages/diparnux-desktop/src`, `packages/diparnux-branding` | QEMU에서 greeter 화면 캡처, 자동 로그인·로그인 시험 |
| D. 디자인 | GTK3 테마, xfwm4 테마, 아이콘 지정, 배경, greeter 이미지, Control Center와의 색 통일 | `assets/brand`, `scripts/make-brand-assets.py`, `packages/diparnux-branding`, `gui/diparnux_cc/theme.py` | Xvfb에서 화면 캡처와 사람 눈 확인, QEMU 화면 캡처 |
| E. 통합 | 모드 전환 유닛 이름, 계정 예약어, 설치기 Xorg 실행, 네트워크 패널 표시, 한국어 문구 | `daemon/internal/supervisor`, `daemon/internal/accounts`, `daemon/dist/diparnux-user.sh`, `packages/diparnux-installer`, `gui/po` | Go 단위 시험, GUI 시험, 설치 E2E |
| F. 시험과 CI | `desktop-session.py`를 X11로 바꾸고, 소크 시험의 대상 프로세스를 Xorg·xfwm4·xfce4-panel·lightdm으로 바꾸며, 컨테이너 시험 추가 | `tests/qemu`, `scripts`, `.github/workflows/iso.yml` | CI 전체 |
| G. 문서 | `docs/design.md`(Xfce 디자인), `docs/desktop-troubleshooting.md`, `docs/decisions.md`(램 목표 철회, Xfce 결정), `docs/memory.md`, `docs/verification-phase-5.md`, `SPEC.md` 변경 표시 | `docs/**`, `SPEC.md` | |

의존 순서: A와 B가 먼저이고 C, D는 B 위에서 병행한다. E는 A 뒤다. 버전 번호(`diparnux.conf`)는 CUDA 캐시 키에 들어가므로 마지막 커밋에서 올린다. 브랜치는 `phase-5`다. PR #8(0.4.1)이 `main`에 병합되기 전에는 `phase-4.1` 위에서 작업하고, 병합 뒤 `main`으로 다시 맞춘다.

## 6. 디자인 설계

0.4.1의 흑백 팔레트(`docs/design.md`)를 이어 간다. 로고는 흑백 두 가지 색이다(0.5.1 에서 로고를 바꿨다. 현재 로고는 `docs/design.md` 를 본다).

| 요소 | 값 |
|---|---|
| 바탕(창, 패널) | #1A1A1A, 더 어두운 바닥 #0D0D0D |
| 강조 면, 호버 | #2B2B2B, #3A3A3A |
| 보조 글자 | #8E8E8E |
| 글자와 강조 | #EBEBEB(로고 회색), 순백 #FFFFFF은 선택 상태에만 |
| 모서리 | 둥근 모서리 6px(창, 버튼) |

- **GTK3 테마 `Diparnux`**: libgtk-3에 내장된 Adwaita 어두운 스타일시트를 가져와(`@import url("resource:///org/gtk/libgtk/theme/Adwaita/gtk-contained-dark.css")`) 색 정의(`@define-color`)와 버튼·입력·메뉴 몇 가지만 덮어쓴다. 별도 테마 패키지를 받지 않는다. *구현 때 GTK 3.24에서 이 가져오기가 동작하는지 Xvfb 화면으로 확인한다.*
- **xfwm4 테마 `Diparnux`**: xfwm4 테마는 `themerc`와 이미지(XPM/PNG)로 이루어진다. `scripts/make-brand-assets.py`가 같은 팔레트로 제목 표시줄, 버튼(닫기·최대화·최소화), 테두리 이미지를 생성한다. 기본 `Default` 테마를 복사해 색만 바꾸는 방법은 시도하지 않는다(라이선스와 유지 비용).
- **아이콘**: Adwaita(심볼릭 흑백 아이콘이 많다), 브랜드 아이콘은 이미 `diparnux-branding`의 hicolor에 있다.
- **로그인 화면**: lightdm-gtk-greeter가 읽는 `background`, `theme-name`, `icon-theme-name`, `font-name`, `indicators`, `clock-format`과 로고 이미지(`default-user-image`)를 설정 파일로 정한다.
- **배경 화면**: 기존 `wallpaper.png`. xfdesktop은 모니터 이름별 설정이라 시스템 기본값을 한 번에 줄 수 없을 수 있다. 첫 로그인 때 `xfconf-query`로 모든 모니터에 적용하는 자동 시작 스크립트로 해결하며 화면 캡처로 확인한다.
- **패널**: 하단 한 줄. 왼쪽부터 시작 메뉴(Whisker) | 작업 목록 | 알림 영역(입력기 표시 등) | 네트워크 상태 | 소리 | 전원·배터리 | 시계 | 세션(잠금, 로그아웃, 다시 시작, 끄기).
- **Control Center**: 이미 흑백 테마(`theme.py`)다. GTK4 앱이라 GTK3 테마의 영향을 받지 않으므로, 두 테마의 색 값은 같은 팔레트에서 만든다.
- **글꼴**: `fonts-nanum`과 `fonts-dejavu-core`를 유지한다. 기본 UI 글꼴과 크기는 구현 때 화면으로 정한다.

## 7. 구현 상세 중 먼저 확인할 것

| 항목 | 이유 | 확인 방법 |
|---|---|---|
| 라이브에서만 자동 로그인 | 설치된 시스템에는 자동 로그인이 있으면 안 된다. squashfs에 설정을 넣으면 설치본에 복사된다 | **구현 결정(2026-10-13)**: 직접 만들지 않고 live-config 의 `0100-lightdm` 구성 요소에 맡긴다. 그 구성 요소가 부팅 때 `/etc/lightdm/lightdm.conf` 의 `autologin-user=` 를 채우며 `noautologin` 부팅 파라미터를 지원한다. 처음에 만든 자체 보조 프로그램은 live-config 와 겹쳐 로그인 화면 시험을 깨뜨려 제거했다. 이미지의 파일에는 자동 로그인이 꺼져 있음을 이미지 검사가 확인한다 |
| lightdm 설정 읽는 경로 | 어떤 디렉터리의 조각을 읽는지 | 컨테이너에서 `lightdm --show-config`(가능하면)와 문서 확인 |
| xfdesktop 기본 배경 | 모니터 이름별 키 | Xvfb와 QEMU 화면 |
| Xorg가 QEMU/VMware 가상 GPU에서 동작 | `modesetting` 드라이버가 virtio, bochs, vmwgfx에서 뜨는지 | QEMU 화면 캡처. VMware는 사용자 확인 |
| NVIDIA 하이브리드 | 3절 | 실기 |
| `qt6-wayland` 제거 후 Calamares | Xorg에서 정상 표시 | QEMU 창 표시 확인 |
| 서버 모드에 X 관련 서비스가 안 뜸 | 서버 모드 보호 | `systemctl` 상태 검사를 시험에 추가 |

## 8. 시험과 수용 기준

램 수치는 합격 조건이 아니다(1절). 합격 조건은 아래와 같다.

1. 이미지 검사: 새 패키지와 설정 파일이 있고 `labwc`, `greetd`, `tuigreet`, `yambar`, `foot`, `swaybg`가 없다.
2. QEMU: 서버 모드로 부팅하고 X 서버가 없으며, `diparnux mode desktop` 뒤 lightdm greeter가 화면에 나온다(화면 캡처로 비어 있지 않음과 로고 색을 확인). 로그인하면 xfwm4, xfce4-panel, xfdesktop이 뜨고 Control Center가 창으로 열린다. `diparnux mode server`로 돌아온다.
3. 라이브: 자동 로그인되어 바탕 화면에 도달한다. 설치 E2E: 설치된 시스템의 `lightdm.conf` 에는 자동 로그인이 없다.
4. 세션 환경: `XDG_SESSION_TYPE=x11`, `XDG_CURRENT_DESKTOP=XFCE`, DBus 세션, polkit 에이전트, PipeWire가 떠 있다.
5. 소크 시험(8분): 프로세스별 증가량 상한은 유지한다. 대상은 `diparnuxd`, Control Center, Xorg, xfwm4, xfce4-panel, xfdesktop, lightdm. 상한 값은 첫 측정 뒤 정한다.
6. 설치기 창이 Xorg에서 그려진다(설치까지는 하지 않는 기존 검사).
7. 사용자 실기 확인(자동화 불가): 실제 모니터와 입력, NVIDIA 하이브리드 노트북에서의 화면, Wi-Fi, 소리, USB 자동 마운트, 한글 입력.

## 9. 사용자가 정해야 하는 것

기본값은 괄호 안이다. 답이 없으면 기본값으로 진행한다.

| 번호 | 질문 | 영향 |
|---|---|---|
| Y1 | 브라우저를 이미지에 넣을까요? (기본: **firefox-esr를 넣는다**) | 설치 크기 +559 MiB(겹침 포함, ISO는 압축되어 이보다 작다). 안 넣으면 사용자가 직접 설치해야 하고, 라이브 세션은 apt 목록이 없어 설치가 어렵다(0.4.0에서 겪은 문제). 대안: chromium(+약 312 MiB 본체) |
| Y2 | 한글 입력기(fcitx5 + fcitx5-hangul)를 넣을까요? (기본: **넣는다**) | +26 MiB, 상주 프로세스 하나. 안 넣으면 GUI에서 한글을 입력할 수 없다 |
| Y3 | VMware/QEMU 게스트 통합(`open-vm-tools-desktop`, `spice-vdagent`)을 넣을까요? (기본: **넣는다**) | VMware에서 해상도 자동 조절과 클립보드를 쓸 수 있다. 실기에서 서비스가 시작되지 않는지는 구현 때 유닛의 `ConditionVirtualization`으로 확인한다 |
| Y4 | 네트워크는 iwd 유지(X5)로 갈까요, NetworkManager로 바꿀까요? (기본: **iwd 유지**) | NetworkManager는 VPN, 유선 설정, 기업용 Wi-Fi를 GUI로 제공하지만 데몬 네트워크 백엔드를 다시 써야 한다 |
| Y5 | 라이브 세션을 자동 로그인으로 할까요? (기본: **자동 로그인**) | 0.4.x에서는 tuigreet에서 수동 로그인이었다 |
| Y6 | 소프트웨어 센터(GUI 패키지 관리자)가 필요합니까? (기본: **넣지 않는다**) | 일반 배포판 사용성에서 가장 큰 차이다. 가벼운 것으로 `synaptic`(약 10 MiB)이 있다. 넣을지는 경량화 단계에서 다시 볼 수 있다 |

## 10. 위험

| 위험 | 대응 |
|---|---|
| Xorg와 NVIDIA 하이브리드 화면 구성을 QEMU에서 재현할 수 없다 | 자동 검증은 가상 GPU까지다. 실기 체크리스트에 항목을 추가하고 문서에 한계를 적는다 |
| xfwm4 테마 이미지를 직접 생성하는 작업의 시각 품질 | Xvfb 화면 캡처로 확인하고, 만족스럽지 않으면 Greybird 기반으로 색만 바꾸는 대안을 쓴다 |
| GTK3 `@import resource://` 가져오기가 동작하지 않을 가능성 | 대안: `adwaita-icon-theme`와 함께 `gnome-themes-extra`의 Adwaita-dark를 상속 |
| Xfce 설정이 xfconf 사용자 설정에 덮인다 | 시스템 기본값만 두고, 첫 로그인 스크립트는 한 번만 실행하도록 표식을 둔다 |
| 이미지 크기 증가 | 1.5 GB ISO가 얼마나 커지는지 첫 CI 빌드에서 측정해 문서에 적는다 |
| 0.4.1(PR #8)이 아직 `main`에 병합되지 않았다 | `phase-5`는 `phase-4.1` 위에서 시작한다. 병합 순서는 사용자가 정한다 |
