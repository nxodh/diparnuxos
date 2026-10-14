# 디자인 시스템

- 확인 날짜: 2026-10-14(0.6.2: macOS Big Sur 풍의 현대적 디자인, 밝은/어두운 두 테마, Pretendard 글꼴)
- 기준: 프로젝트 소유자가 준 로고(`assets/brand/source/opereta-logo.png`, 1254×1254, 배경 투명). **밝은 회색(약 #F0F0F0) 원** 위에 **검정(#000) 굵은 둥근 "O"**. 왼쪽 위에 사선 홈이 있고 안쪽은 둥근 사각형 구멍이다. 로고 자산은 원본 색을 그대로 쓴다.
- 연동 에이전트(Deberopa)의 로고는 `assets/brand/source/deberopa-logo.png`(원 안의 "D")다. Control Center 의 에이전트 페이지 아이콘으로만 쓰고 OS 의 아이콘은 아니다.

## 원칙(0.6.2, 사용자 요청)

- **로고와 어울리는 모던한 모양.** 둥근 사각형이 로고의 모티프이므로, 가능한 모든 곳에서 각진 네모 대신 곡률이 있는 사각형을 쓴다.
- **색을 많이 쓰지 않는다.** 흰색·회색·검정만 쓰고 강조색을 따로 두지 않는다. 오류, 경고, 성공 표시처럼 의미가 있는 색은 libadwaita 의 기본 의미 색을 그대로 쓴다.
- **창 단추는 x, −, + 기호.** 닫기는 x, 최소화는 −, 최대화(전체 화면)는 +. macOS 처럼 왼쪽에 이 순서로 두고, 마우스를 올리지 않아도 기호가 항상 보인다(회색 원 안에 그린다).
- **글꼴은 Pretendard**(한글과 영문 모두). 제목 줄은 SemiBold.
- **램 사용량보다 미적 요소와 사용성이 앞선다**([decisions.md](decisions.md) U19). 창 합성, 그림자, 둥근 모서리를 켠다.

## 팔레트

두 테마(밝은 `Opereta`, 어두운 `Opereta-Dark`)가 같은 이름의 값을 쓴다. 값은 `scripts/make-theme.py` 의 `PALETTES` 가 기준이다.

| 이름 | 밝은 테마 | 어두운 테마 | 쓰는 곳 |
|---|---|---|---|
| window | `#F2F2F2` | `#1E1E1E` | 창 바탕, 배경 화면, 로그인 화면 |
| chrome | `#ECECEC` | `#2B2B2C` | 제목 줄, 패널, 메뉴 |
| surface | `#FFFFFF` | `#171717` | 입력창, 목록, 카드 |
| sidebar | `#E9E9E9` | `#252526` | 사이드바, Calamares 사이드바 |
| border / hairline | `#D2D2D5` / `#E2E2E5` | `#3B3B3D` / `#323234` | 창 테두리, 구분선 |
| text / text2 | `#1D1D1F` / `#6E6E73` | `#F2F2F4` / `#A1A1A6` | 본문 글자, 보조 글자 |
| accent | `#1D1D1F` | `#F2F2F4` | 선택, 주요 단추(흑백이므로 글자색과 같다) |
| select / hover | `#DFDFE2` / `#E6E6E9` | `#3B3B3E` / `#343436` | 선택, 마우스를 올린 상태 |

모서리 반지름: 창 12px, 카드와 메뉴 10~12px, 단추와 입력창 8px, 스위치와 창 단추는 완전한 원이다.

## 적용 범위

| 대상 | 내용 | 파일 |
|---|---|---|
| 앱 아이콘 | hicolor 16~512 원형 `opereta.png`, 데스크톱 파일 `Icon=opereta`, 시작 메뉴 단추(원 안의 O) | `packages/opereta-branding`, `whiskermenu/defaults.rc` |
| 배경 화면 | 밝은 테마는 `#F2F2F2` 단색 가운데에 희미한 O 마크(`wallpaper-light.png`), 어두운 테마는 `#0D0D0D` 단색에 `wallpaper.png`. 색 구성표를 바꾸면 Control Center 가 함께 바꾼다(사용자가 고른 그림은 그대로 둔다) | `opereta-branding`, `xfce4-desktop.xml`, `opereta-session-setup`, `system.py` |
| GTK3 테마 `Opereta`, `Opereta-Dark` | 둥근 모서리, 회색조 팔레트, Pretendard. Xfce 패널, Thunar 같은 GTK3 앱이 쓴다 | `opereta-desktop` (`themes/*/gtk-3.0/gtk.css`, `make-theme.py` 가 생성) |
| 창 관리자 테마 | xfwm4 제목 줄, 테두리, 창 단추(원 안의 x, −, +). 활성/비활성/눌림 이미지는 `scripts/make-theme.py` 가 생성한다. 단추 배치는 `CHM\|`(왼쪽에 닫기, 최소화, 최대화), 합성과 그림자를 켠다 | `themes/*/xfwm4`, `assets/brand/generated/xfwm4/{light,dark}`, `xfwm4.xml` |
| 아이콘 테마 `Opereta` | Adwaita 를 상속하고 `window-*-symbolic` 을 x, −, + 로 바꾼다 | `packages/opereta-desktop/src/icons/Opereta` |
| 글꼴 | Pretendard(`fonts-pretendard`, SIL OFL 1.1, [licenses.md](licenses.md) 11절). UI 글꼴 `Pretendard 10`, 제목 줄 `Pretendard SemiBold 10`, fontconfig 에서 `sans-serif`, `system-ui`, `Sans` 가 Pretendard 로 풀린다. 고정폭은 `Monospace` 그대로다 | `xsettings.xml`, `xfwm4.xml`, `fontconfig/60-opereta-pretendard.conf` |
| 패널 | 하단 한 줄(36px): 시작 메뉴, 작업 목록, 알림 영역, 네트워크, 소리, 전원, 시계, 세션 | `xdg/xfce4/panel/default.xml`, `genmon-5.rc`, `opereta-panel-net` |
| 터미널 | 밝은 테마 바탕 `#FBFBFB`, 글자 `#1D1D1F`. 어두운 테마를 고르면 Control Center 가 사용자의 `terminalrc` 색 줄만 바꾼다 | `xdg/xfce4/terminal/terminalrc`, `system.py` |
| 로그인 화면 | lightdm-gtk-greeter, `#F2F2F2` 단색 바탕, 원형 로고 아바타, 테마 `Opereta`, Pretendard | `lightdm/50-opereta-greeter.conf` |
| Control Center | libadwaita 위에 같은 팔레트와 둥근 모서리를 입힌다. 색 구성표는 시스템을 따름(기본, 밝음), 밝음, 어두움이며 고르면 데스크톱 테마도 함께 바뀐다 | `gui/opereta_cc/theme.py`, `system.py` |
| 설치기 | Calamares 로고, 밝은 환영 이미지, 밝은 사이드바(`#E9E9E9`, 현재 단계는 `#1D1D1F` 바탕), 슬라이드 | `packages/opereta-installer/src/branding` |
| 부트 메뉴 | GRUB 메뉴 색(라이브와 설치된 시스템). 텍스트 화면이므로 흑백 그대로 | `9500-...grub-timeout.hook.binary`, `10-opereta-colors.cfg` |
| 시스템 정보 | `os-release` 의 `LOGO=opereta`, `ANSI_COLOR="1;37"` | `opereta-base` |

### 기본값을 두는 방식

Debian 의 Xfce 패키지가 `/etc/xdg` 에 싣는 파일(`panel/default.xml`, `xsettings.xml` 등)은 건드리지 않는다. Opereta 의 기본값은 `/etc/xdg/opereta/xfce4/` 에 두고, `/etc/X11/Xsession.d/55opereta-xdg` 가 `XDG_CONFIG_DIRS` 맨 앞에 이 디렉터리를 둔다(`startxfce4` 는 미리 정해진 값을 그대로 쓰고 끝에 `/etc/xdg` 를 붙인다). 사용자가 바꾼 설정은 `~/.config` 에 저장되어 기본값보다 우선한다. 일부 구성 요소는 시스템 기본값을 읽지 않아 첫 로그인 스크립트(`opereta-session-setup`)가 사용자 설정으로 복사하거나 적용한다(터미널 색, 모니터별 배경 화면, 한글 입력기 프로필).

### 알려진 한계

- 폴더 같은 일반 아이콘은 Adwaita 를 그대로 쓰므로 파란색이다. `Opereta` 아이콘 테마는 Adwaita 를 상속하고 창 컨트롤(x, −, +)만 바꾼다. 흑백 아이콘 세트는 만들지 않았다(크기와 유지 비용).
- GTK4/libadwaita 앱(Control Center)은 GTK3 테마의 영향을 받지 않는다. 두 쪽은 같은 팔레트(`scripts/make-theme.py` 의 `PALETTES`, `gui/opereta_cc/theme.py` 의 `_PALETTES`)에서 따로 정의하며, 두 값이 같아야 한다.
- libadwaita 는 창 컨트롤의 기호를 내장 아이콘으로 그려 아이콘 테마로 바꿀 수 없다. Control Center 는 같은 모양의 PNG(`ctl-*-light|dark.png`)를 CSS 배경으로 그린다.
- 어두운 테마에서 이미 열려 있는 GTK3 앱은 다시 시작해야 새 테마를 읽는 경우가 있다.

## 이미지 다시 만들기

원본이 바뀌면 컨테이너에서 파생 이미지를 다시 만든다.

```sh
bash scripts/make-brand-assets.sh      # assets/brand/generated/, 설치기 브랜딩, 테마(GTK3, xfwm4 이미지, 아이콘)를 다시 쓴다
```

원본은 PNG 한 장(`assets/brand/source/opereta-logo.png`)이다. 원본은 정사각형이 아니고 가장자리에 반투명 얼룩이 있어서, 생성 스크립트가 불투명 화소에서 원의 중심과 반지름을 구해 깨끗한 원으로 다시 잘라 1024×1024 기준 이미지를 만든 뒤 모든 파생 이미지를 거기서 만든다.
