# 0.5.0 검증 기록: Xfce 데스크톱과 Diparnux 디자인

- 확인 날짜: 2026-10-13
- 상태: 컨테이너 시험(Xvfb 세션, Xephyr 로그인 화면), 단위 시험, **CI 전체(QEMU 데스크톱 E2E, 설치 E2E, 이미지 검사 포함, 실행 38282482910)가 통과했다**(4절). 실기, NVIDIA 화면, VMware는 미검증이다.
- 설계: [phase-5-design.md](phase-5-design.md). 램 목표는 철회되어 이 단계의 합격 조건이 아니다([decisions.md](decisions.md) U13).

## 1. 이번에 확인한 것

| 항목 | 방법 | 결과 |
|---|---|---|
| 패키지 Depends 해석 | trixie 컨테이너에서 `diparnux-branding`과 `diparnux-desktop` 을 `dpkg-buildpackage` 로 빌드하고 `apt-get install ./*.deb` | 통과(패키지 이름 오류 하나를 고쳤다: 전원 관리 패널 플러그인은 `xfce4-power-manager-plugins` 패키지다) |
| Debian 파일을 덮지 않는다 | `dpkg -S` 로 `/etc/xdg/xfce4/panel/default.xml`, `xsettings.xml` 의 소유 패키지 확인 | 통과(Diparnux 파일은 `/etc/xdg/diparnux/` 아래) |
| `XDG_CONFIG_DIRS` 우선순위 | Xvfb 에서 `/etc/X11/Xsession startxfce4` 로 세션을 띄우고 `xfconf-query` 로 값 확인 | 통과: 세션 프로세스의 `XDG_CONFIG_DIRS=/etc/xdg/diparnux:/etc/xdg`, `/Net/ThemeName=Diparnux`, `/Net/IconThemeName=Adwaita`, xfwm4 테마 `Diparnux`, 패널 플러그인 9개 |
| 세션 프로세스 | 같은 세션 | `xfwm4`, `xfce4-panel`, `xfdesktop` 가 뜬다 |
| 화면(캡처) | `scrot` 으로 확인 | GTK3 테마 `Diparnux`(어두운 창, 둥근 모서리), xfwm4 테마(제목 줄, 닫기·최대화·최소화 단추, 활성 창의 밝은 바깥 선), 하단 패널, 배경 마크가 표시됨. 터미널은 흑백 색으로 시작함 |
| 로그인 화면 | `lightdm --test-mode`(Xephyr 안) | 통과: `lightdm-gtk-greeter` 가 시작하고 어두운 테마, 로고 아바타, 시계, 사용자 목록이 표시됨. 시험은 greeter 프로세스가 뜨는지를 판정한다 |
| 설치된 시스템의 자동 로그인 | 컨테이너에 설치한 `/etc/lightdm/lightdm.conf` 확인 | 자동 로그인이 꺼져 있음(라이브 세션에서는 live-config 가 부팅 때만 켠다) |
| 첫 로그인 설정 | 같은 세션 | `diparnux-session-setup` 이 표식 파일을 만들고 터미널 색 설정을 복사함 |
| 데몬 | `go vet ./...`, `go test ./...` | 통과(모드 전환의 유닛 이름은 `lightdm.service`) |
| Control Center | `make test-gui` | 52개 통과 |
| 셸 | `make lint`(shellcheck, 새 스크립트 포함) | 통과 |
| 설치기 설정 | `scripts/test-installer-config-in-docker.sh` | 통과 |

## 2. 시험 중에 알게 된 것

- **genmon 의 설정은 xfconf 가 아니다.** `xfce4-genmon-plugin` 은 `genmon-<플러그인 번호>.rc` 에서 명령을 읽는다. 패널 배치 XML 에 속성을 적으면 "(genmon)XXX" 가 표시된다. `genmon-5.rc` 를 `/etc/xdg/diparnux/xfce4/panel/` 에 둔다.
- **xfce4-terminal 은 시스템 기본값(`/etc/xdg/.../terminalrc`)을 읽지 않는다.** 첫 로그인 스크립트가 사용자 설정으로 복사한다.
- **xfdesktop 의 배경은 모니터 이름별 속성이다.** 흔한 이름만 XML 에 두고 나머지는 첫 로그인 스크립트가 `xrandr` 의 연결된 출력 이름으로 채운다. 단색 바탕(`rgba1`)도 함께 지정하지 않으면 투명 PNG 뒤가 흰색이 된다.
- **lightdm-gtk-greeter 는 시스템 D-Bus 가 없으면 즉시 종료한다.** 컨테이너 시험은 시스템 버스를 먼저 띄운다.
- 작은 배경 이미지를 greeter 의 `background` 로 쓰면 화면 크기로 늘어나 흐려진다. 단색(`#0d0d0d`)으로 바꿨다.
- 컨테이너(Docker)에서는 `CAP_SYS_PTRACE` 가 없어 다른 사용자의 `/proc/<pid>/environ` 을 root 가 읽지 못한다. 시험은 해당 사용자로 읽는다.

## 3. 아직 확인하지 않은 것

| 항목 | 이유 |
|---|---|
| QEMU 데스크톱 E2E(자동 로그인, 로그인 화면, 계정 로그인, Control Center, 설치기 창, 소크, Firefox) | CI 대기. 새 시나리오이며 첫 실행에서 실패할 수 있다 |
| 이미지 검사(`tests/nvidia/image.sh`)의 새 항목 | CI 대기 |
| 소크 시험의 Xorg·xfwm4·xfce4-panel·xfdesktop 증가량 상한 | 첫 측정 전의 넉넉한 값이다. 측정 뒤 조인다 |
| ISO 크기 | 첫 CI 빌드에서 측정한다(0.4.0 은 약 1.5 GB) |
| 라이브 자동 로그인 | live-config 가 쓰는 `autologin-user=` 와 PAM 의 자동 로그인(`pam_permit`)을 컨테이너에서는 시험하지 못했다. QEMU 시나리오가 확인한다 |
| NVIDIA 하이브리드 노트북에서 Xorg 화면 | 실기 필요 |
| VMware 에서 Xorg `modesetting` 과 `open-vm-tools-desktop` | 사용자 확인 필요 |
| 한글 입력(fcitx5) | 키 입력 자동화가 없어 확인하지 못했다. 환경 변수와 프로필 생성만 구성했다 |
| Wi-Fi 상태 클릭 동작(패널 → Control Center 네트워크 쪽) | 구성만 했다. QEMU 에서 `diparnux-panel-net` 출력 형식만 확인한다 |

## 4. CI 실행에서 알게 된 것(2026-10-13)

- **첫 실행(38269109496)**: 새 푸시로 취소되었다.
- **두 번째 실행(38270708124)**: 데스크톱 E2E 의 `greeter-running` 에서 실패했다. `pgrep -x lightdm-gtk-greeter` 는 이름이 15자를 넘어 항상 실패한다. `pgrep -f` 로 고쳤다. 실패 전까지 서버 모드, 자동 로그인으로 Xfce 세션과 패널, Control Center 창이 뜨는 것(화면 캡처), 서버 모드 복귀는 통과했다.
- **세 번째 실행(38278565296)**: 로그인 화면 단계에서 실패했다. 원인: live-config 의 `0100-lightdm` 구성 요소가 lightdm 이 설치되어 있으면 부팅 때 `/etc/lightdm/lightdm.conf` 의 `autologin-user=` 를 라이브 사용자로 채운다. 그래서 자체 자동 로그인 끄기 파일과 무관하게 자동 로그인이 일어나 로그인 화면이 나오지 않았다. 자체 보조 프로그램(조각 파일, `nopasswdlogin` 그룹, 서비스 드롭인)은 live-config 와 중복이어서 제거했다. Debian 의 `lightdm-autologin` PAM 은 `pam_permit` 만 쓰므로 그룹이 필요 없다.
- 시험에 프레임 해시를 넣어 이전 세션의 마지막 프레임이 남아 있는지 가리고, 로그인 직전과 실패 시점에 lightdm, greeter, Xorg 로그를 남기게 했다.

## 5. 통과한 CI 실행의 결과(실행 38282482910, 2026-10-13)

- 작업 두 개(lint, ISO 빌드와 부팅 시험)가 모두 성공했다. 데스크톱 세션 E2E 는 약 591초가 걸렸다.
- **로그인 화면**: 자동 로그인을 끈 뒤 lightdm-gtk-greeter 가 그려지고(화면 캡처), 직접 만든 계정으로 로그인해 `xfce4-session`, `xfwm4`, `xfce4-panel`, `xfdesktop`, `xfsettingsd` 가 뜬다. 세션 환경은 `XDG_CURRENT_DESKTOP=XFCE`, `XDG_CONFIG_DIRS=/etc/xdg/<이름>:...` 이다.
- **소크 시험(8분, 1분 간격 8표본, kB)**: `diparnuxd` 36680 → 37256, Control Center 295276 → 333480(첫 표본 이후 +4 MB), Xorg 149484 → 150076, xfwm4 30608(변화 없음), xfce4-panel 31808(변화 없음), xfdesktop 44336(변화 없음), MemAvailable 3202876 → 3185668. 모든 상한 안이다. **이 값은 램 목표가 아니라 누수 검사의 기록이며**, 경량화 단계의 출발점이다(QEMU, 라이브 세션, 4 GiB).
- **ISO 크기**: 1,661,882,719 바이트(약 1.55 GiB). 0.4.1 은 약 1.5 GB 였다.
- **Firefox 실험**: 창은 뜨지만 "Profile Missing" 대화상자가 나왔다. 시험이 `HOME` 을 지정하지 않고 실행한 탓이며(0.5.1 에서 고쳤다) 제품 문제는 아니다. 실제 사용 중의 Firefox 동작은 실기에서 확인해야 한다.
