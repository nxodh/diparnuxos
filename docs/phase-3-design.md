# Phase 3 설계: 데스크톱, Control Center, 첫 부팅 마법사

- 확인 날짜: 2026-10-12
- 상태: 묶음 A~E는 구현되어 CI의 QEMU 검사를 통과했다. 묶음 F는 QEMU 참고 측정만 했고 실기 램 실측은 미실시다. 아래 "확인한 사실"은 Debian 13 컨테이너에서 실제로 조회한 값이다. 실제 결과는 `docs/verification-phase-3.md`에 있다.

범위는 SPEC의 Phase 3다. labwc 데스크톱, Lightlix Control Center(GTK4 + libadwaita, Python), 첫 부팅 마법사, 모드 전환을 만든다. 설치기·업데이트·롤백은 Phase 4다.

---

## 1. 이미 있는 것

- 데몬은 `GET/POST /v1/mode`로 서버/데스크톱 모드를 전환한다. 데스크톱 전환은 `greetd.service`가 설치되어 있어야 하며, 없으면 `desktop_not_installed`를 돌려준다(`daemon/internal/supervisor/system.go`).
- `lightlix-gpu-check`와 `/v1/status`가 GPU, 드라이버, Secure Boot 상태를 이미 돌려준다. 값은 `enabled`, `disabled`, `unknown` 중 하나다.
- 모델 검색·다운로드·기본 모델, 서버 시작·중지·로그·채팅, API 키, 설정, 리포트 API가 있다. `POST /v1/update`는 `not_implemented`다.
- GUI가 쓸 수 있는 인증 방식은 유닉스 소켓의 `lightlix` 그룹 권한이다. 라이브 사용자는 이미 이 그룹에 들어 있다.

## 2. 확인한 사실

Debian 13 컨테이너(arm64)의 `apt-cache`로 조회했다. amd64 크기는 다를 수 있으며 최종 값은 CI의 ISO에서 잰다.

| 패키지 | 버전 | 설치 크기 | 비고 |
|---|---|---|---|
| labwc | 0.8.3-1 | 745 KB | `xwayland`를 Depends로 가진다 |
| greetd | 0.10.3-4 | 1,265 KB | |
| tuigreet | 0.9.1-5 | 3,799 KB | 텍스트 greeter. 별도 컴포지터가 필요 없다 |
| gtkgreet + cage | 0.8-1 / 0.2.0-2 | 92 KB / 80 KB | GUI greeter. cage 컴포지터가 하나 더 든다 |
| foot | 1.21.0-2 | 766 KB | |
| waybar / yambar | 0.12.0-1 / 1.11.0-1 | 2,713 KB / 490 KB | sfwbar는 trixie에 없다. yambar는 오디오·미디어 라이브러리를 끌어온다 |
| python3-gi, gir1.2-gtk-4.0, gir1.2-adw-1 | 3.50.0 / 4.18.6 / 1.7.6 | 836 / 885 / 179 KB | libadwaita 1.7.6 |
| mokutil | 0.7.2-1 | 87 KB | |
| openssh-server | 10.0p1-7 | 3,488 KB | 현재 이미지에는 없다 |
| fonts-nanum / fonts-noto-cjk | 20200506-1 / 20240730 | 33 MB / 91 MB | 한국어 UI에는 한글 글꼴이 필요하다 |

패키지 개수(`--no-install-recommends` 시뮬레이션): labwc+greetd+foot 135개, 여기에 waybar를 더하면 229개, python3-gi+GTK4+libadwaita 144개, 세 묶음에 mesa-vulkan-drivers까지 더하면 221개다. 이 개수는 서로 겹치는 의존성을 포함한다.

## 3. 결정과 기본값

사용자가 바꾸지 않으면 아래 기본값으로 진행한다.

| 번호 | 항목 | 기본값 | 이유와 대안 |
|---|---|---|---|
| D1 | 로그인 화면 | greetd + tuigreet | 컴포지터를 하나 덜 띄워 램이 적게 든다. 대안은 gtkgreet + cage |
| D2 | 자동 로그인 | 끈다 | 라이브 사용자 암호 입력을 유지한다. 설정으로 켤 수 있게 한다 |
| D3 | 패널 | yambar | 처음에는 넣지 않기로 했으나 사용자가 패널을 요청했다(2026-10-12). yambar는 쓰지 않는 오디오·미디어 모듈 때문에 pipewire, pulse, alsa, flac 등 35개 안팎의 라이브러리를 끌어온다(조회 확인). 램 목표를 완화했으므로 감수한다. 활성 창 제목, 사용 메모리, 시계를 보이고 시계를 누르면 Control Center가 열린다. 설정은 `yambar -C`로 검증한다 |
| D4 | 한글 글꼴 | fonts-nanum | noto-cjk보다 58 MB 작다. 글리프 범위와 ISO 크기는 CI에서 확인한다 |
| D5 | GUI와 데몬의 통신 | 표준 라이브러리 `http.client` + 유닉스 소켓 | `requests` 같은 추가 의존성을 넣지 않는다 |
| D6 | 첫 부팅 상태 저장 | 데몬이 `/var/lib/lightlix/firstboot.json`에 기록 | 라이브 세션에서는 재부팅하면 사라진다. Phase 4 설치 뒤에는 유지된다 |
| D7 | Secure Boot MOK | 감지와 안내 + `lightlix-mok` 보조 스크립트 | 라이브 ISO에서는 키가 재부팅 때 사라져 흐름이 끝까지 이어지지 않는다. 실제 검증은 설치 뒤에 가능하다 |

## 4. 작업 묶음

파일 범위가 겹치지 않아 병렬로 진행할 수 있다.

| 묶음 | 내용 | 소유 파일 | 검증 |
|---|---|---|---|
| A. 데몬 확장 | 첫 부팅 상태 API, VRAM별 추천 모델, SSH 켜기/끄기와 방화벽 연동, 모드 전환 안내 조건 | `daemon/internal/{api,supervisor,config,...}` | Debian 컨테이너의 `go vet`, `go test -race` |
| B. 데스크톱 기반 | labwc, greetd, foot, 패널 패키지와 설정, 한글 글꼴, 모드 전환 시 greetd 시작, SSH 패키지(기본 비활성) | `packages/lightlix-desktop`, `build/config/package-lists`, `tests/` | 이미지 정책 검사, QEMU에서 greetd 기동과 화면 캡처 |
| C. Control Center 뼈대 | `gui/` Python 패키지, 데몬 클라이언트, 대시보드·모델·서버·채팅·시스템 화면, gettext(ko/en), 다크/라이트 | `gui/**`, `packages/lightlix-control-center` | 컨테이너의 Python 단위 테스트(클라이언트·뷰 모델), 번역 카탈로그 검사 |
| D. 첫 부팅 마법사 | GPU 감지, 드라이버 검증, Secure Boot 처리, 추천 모델, API 공개 범위 설정 | `gui/**`(C 이후) | 데몬 목(fake)으로 단계 전이 테스트 |
| E. Secure Boot 보조 | 키 생성, DKMS 서명 설정, 모듈 재빌드, `mokutil --import` | `packages/lightlix-nvidia`, `docs/secure-boot.md` | 쉘 단위 검사, 스텁 테스트 |
| F. 측정과 문서 | 서버/데스크톱 모드 유휴 램 측정, `docs/memory.md`, 지원 표 갱신 | `docs/**` | QEMU 실측(실기 값이 아님을 명시) |

의존 순서: A와 B와 C는 동시에 시작한다. D는 A와 C 뒤, E는 독립이며, F는 B가 끝난 뒤다.

## 5. 위험

- **램 목표 700 MB(데스크톱 모드, llama-server 제외).** GTK4와 libadwaita, 소프트웨어 렌더링 환경에서 초과할 수 있다. 측정 전에는 달성을 주장하지 않는다. 초과하면 패널·greeter·글꼴 선택을 되돌아본다.
- **GUI 검증의 한계.** QEMU의 가상 GPU 화면 캡처로 기동 여부는 확인할 수 있지만, 실제 모니터에서의 표시와 입력, 한글 입력기는 실기가 필요하다.
- **Secure Boot.** QEMU의 OVMF로 SB 켜짐 상태 부팅은 만들 수 있으나, MokManager 화면의 상호작용을 자동화하기는 어렵다. 이 단계는 실기에서만 끝까지 확인된다.
- **iGPU 렌더링.** SPEC은 iGPU가 있으면 데스크톱을 iGPU로 그리고 NVIDIA VRAM을 추론 전용으로 남기라고 한다. 이를 `WLR_DRM_DEVICES` 등으로 지정하는 방식은 실기 확인 전이다.
- **llama-server와 모드 전환.** 서버가 도는 중에 모드를 바꾸면 현재 데몬은 `busy`를 돌려준다. GUI가 이 조건을 안내해야 한다.

## 6. 하지 않는 것

- GNOME, KDE 등 무거운 데스크톱 환경.
- 설치기, 업데이트, 롤백(Phase 4).
- 실기 검증 전의 "정식 지원" 표기.
