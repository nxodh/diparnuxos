# gui (데스크톱 환경)

- 확인 날짜: 2026-10-10
- 상태: 초안. 구현은 Phase 3 예정이다.

- **책임(초안)**: labwc 위에서 뜨는 GTK4/libadwaita Python 앱과 기본 데스크톱 구성. 사용자가 로컬 LLM 서버 상태를 보고 조작하는 화면을 맡는다.
- **컴포지터**: labwc 0.8.3-1(trixie main, wlroots 0.18 기반).
- **그리터**: greetd 0.10.3-4(trixie main). 그리터 프로그램은 후보만 확인했다(`gtkgreet` 0.8-1, `tuigreet` 0.9.1-5). 선택은 Phase 3에서 한다. greetd 기본 사용자는 `_greetd`다.
- **패널**: waybar 0.12.0-1. sfwbar는 trixie에 없다.
- **툴킷**: GTK 4.18.6, libadwaita(`gir1.2-adw-1`) 1.7.6, `python3-gi` 3.50.0.
- **수용 기준**: RTX 30, 40, 50에서 화면 깜빡임, 프레임 순서, XWayland 창 표시 문제가 없어야 한다. labwc 0.8.3에는 explicit sync(`drm-syncobj`)가 없어서 문제가 나올 수 있다. 문제가 나오면 labwc 0.9 이상을 백포트한다([docs/decisions.md](../docs/decisions.md) D6).
- **설치기와의 관계**: Calamares는 설치 프로그램이며 이 앱이 아니다. 라이브 세션에서는 XWayland 창으로 뜬다(Phase 4).
- **구현 Phase**: 3.
