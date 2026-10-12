# 디자인 시스템

- 확인 날짜: 2026-10-13(0.5.1: 이름과 로고 교체)
- 기준: 프로젝트 소유자가 준 로고(`assets/brand/source/diparnux-logo.png`, 1265×1244, 배경 투명). **밝은 회색(#F0F0F0) 원** 위에 **검정(#000) 굵은 글자 D**. D 안쪽의 삼각형 구멍으로 원 색이 비친다. 로고 자산은 원본 색을 그대로 쓰고, 화면 요소의 글자와 강조는 아래 팔레트의 값을 쓴다.

## 원칙

- **흑백 두 색과 회색 단계만 쓴다.** 강조색을 따로 두지 않는다. 오류, 경고, 성공 표시처럼 의미가 있는 색은 libadwaita 의 기본 의미 색을 그대로 쓴다.
- 글자는 굵은 산세리프(`NanumGothic`, `DejaVu Sans`)로 간결하게 쓴다. 장식을 넣지 않는다.
- 이미지는 작게 둔다: 배경 이미지는 작은 마크 하나이고, GRUB 은 그림 없이 색만 쓴다.

## 팔레트

| 이름 | 값 | 쓰는 곳 |
|---|---|---|
| 검정 | `#000000` | 로고의 D, 밝은 바탕의 강조 글자 |
| 바탕(어둡게) | `#0D0D0D` | 패널, 창 제목 줄(활성), 메뉴, 배경, Calamares 사이드바 |
| 그래파이트 | `#1A1A1A` / `#2B2B2B` / `#3A3A3A` | 비활성 제목 줄, 호버, 테두리 |
| 회색 | `#8E8E8E` | 비활성 글자 |
| 밝은 회색 | `#EBEBEB` | 활성 테두리, 본문 글자(어두운 바탕). 로고의 원(`#F0F0F0`)과 거의 같은 값이다 |
| 흰색 | `#FFFFFF` | 어두운 바탕 위의 마크 |

## 적용 범위

0.5.0 에서 데스크톱을 Xfce 로 바꾸면서 labwc, yambar, tuigreet 용 테마를 Xfce 용으로 옮겼다. 팔레트는 같다.

| 대상 | 내용 | 파일 |
|---|---|---|
| 앱 아이콘 | hicolor 16~512 원형 `diparnux.png`, 데스크톱 파일 `Icon=diparnux`, 시작 메뉴 단추(원 안의 D) | `packages/diparnux-branding`, `whiskermenu/defaults.rc` |
| 배경 화면 | `#0D0D0D` 단색 가운데에 희미한 D 마크(`#3A3A3A`, `xfdesktop`, 중앙 배치) | `diparnux-branding`, `xfce4-desktop.xml`, `diparnux-session-setup` |
| GTK3 테마 `Diparnux` | libgtk-3 의 Adwaita 어두운 스타일에 `@define-color` 로 팔레트를 입히고 모서리를 6px 로 둥글게 한다 | `diparnux-desktop` (`themes/Diparnux/gtk-3.0/gtk.css`) |
| 창 관리자 테마 `Diparnux` | xfwm4 제목 줄, 테두리, 버튼. 이미지는 `scripts/make-xfwm4-theme.py` 가 팔레트로 생성한다. 활성 바깥 선 `#EBEBEB`, 비활성 `#3A3A3A`, 반지름 6px | `diparnux-desktop` (`xfwm4/themerc`), `assets/brand/generated/xfwm4` |
| 패널 | 하단 한 줄(36px): 시작 메뉴, 작업 목록, 알림 영역, 네트워크, 소리, 전원, 시계, 세션 | `xdg/xfce4/panel/default.xml`, `genmon-5.rc`, `diparnux-panel-net` |
| 터미널 | 바탕 `#0D0D0D`, 글자 `#EBEBEB`, 흑백에 가까운 16색 | `xdg/xfce4/terminal/terminalrc` |
| 로그인 화면 | lightdm-gtk-greeter, 단색 바탕, 원형 로고 아바타(`diparnux-logo-512.png`), 테마 `Diparnux` | `lightdm/50-diparnux-greeter.conf` |
| Control Center | 강조색을 회색조로(밝은 테마 `#1A1A1A`, 어두운 테마 `#EBEBEB`), 아이콘. 처음 값은 어두운 테마 | `gui/diparnux_cc/theme.py`, `system.py` |
| 설치기 | Calamares 로고, 환영 이미지, 사이드바 색, 슬라이드 | `packages/diparnux-installer/src/branding` |
| 부트 메뉴 | GRUB 메뉴 색(라이브와 설치된 시스템) | `9500-...grub-timeout.hook.binary`, `10-diparnux-colors.cfg` |
| 시스템 정보 | `os-release` 의 `LOGO=diparnux`, `ANSI_COLOR="1;37"` | `diparnux-base` |

### 기본값을 두는 방식

Debian 의 Xfce 패키지가 `/etc/xdg` 에 싣는 파일(`panel/default.xml`, `xsettings.xml` 등)은 건드리지 않는다. Diparnux 의 기본값은 `/etc/xdg/diparnux/xfce4/` 에 두고, `/etc/X11/Xsession.d/55diparnux-xdg` 가 `XDG_CONFIG_DIRS` 맨 앞에 이 디렉터리를 둔다(`startxfce4` 는 미리 정해진 값을 그대로 쓰고 끝에 `/etc/xdg` 를 붙인다). 사용자가 바꾼 설정은 `~/.config` 에 저장되어 기본값보다 우선한다. 일부 구성 요소는 시스템 기본값을 읽지 않아 첫 로그인 스크립트(`diparnux-session-setup`)가 사용자 설정으로 복사하거나 적용한다(터미널 색, 모니터별 배경 화면, 한글 입력기 프로필).

### 이번에 알게 된 한계

- 아이콘 테마는 Adwaita 이므로 폴더 아이콘이 파란색이다. 흑백에 맞춘 아이콘 테마는 만들지 않았다(크기와 유지 비용). 경량화 단계에서 다시 본다.
- GTK4/libadwaita 앱(Control Center)은 GTK3 테마의 영향을 받지 않는다. 두 쪽의 색은 같은 팔레트에서 따로 정의한다.

## 이미지 다시 만들기

원본이 바뀌면 컨테이너에서 파생 이미지를 다시 만든다.

```sh
bash scripts/make-brand-assets.sh      # assets/brand/generated/, 설치기 브랜딩, xfwm4 테마 이미지를 다시 쓴다
```

원본은 PNG 한 장(`assets/brand/source/diparnux-logo.png`)이다. 원본은 정사각형이 아니고 가장자리에 반투명 얼룩이 있어서, 생성 스크립트가 불투명 화소에서 원의 중심과 반지름을 구해 깨끗한 원으로 다시 잘라 1024×1024 기준 이미지를 만든 뒤 모든 파생 이미지를 거기서 만든다.
