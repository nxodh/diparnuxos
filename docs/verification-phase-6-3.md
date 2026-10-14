# 0.6.3 검증 기록

- 확인 날짜: 2026-10-15
- 상태: 단위 시험, 컨테이너 시험, 화면 하네스(Xvfb)로 확인했다. **실제 PC(MSI Stealth 15M, NVIDIA GPU)에서의 화면과 부팅은 사용자가 확인해야 한다.** CI 결과는 아래 표의 마지막 줄에 적는다.

| 대상 | 방법 | 결과 |
|---|---|---|
| 부팅 항목 해석과 진단 | `go test ./internal/boot`(사용자 PC 의 `efibootmgr -v` 출력을 시험 자료로 씀) | 통과: 항목 종류 분류, 펌웨어가 순서를 되돌린 경우, 이전 이름 폴더, 대상 해석, 복구 시 항목 직접 생성 |
| 설치기 | `go test ./internal/install` | 통과: UEFI 가 아니면 거절, 남의 `EFI/BOOT/BOOTX64.EFI` 보존, os-prober 켬, 항목 확인과 생성, Windows 가 있으면 RTC 현지 시간 |
| 명령과 동작 | `go test ./cmd/... ./internal/updatectl` | 통과: `opereta boot ...`, 헬퍼 명령 매핑, 대상 형식 거절, `boot` 필드 |
| Go 전체 | `go vet`, `gofmt`, `go test ./...`(Debian 13 컨테이너) | 통과(46 패키지) |
| 제어 센터 | `make test-gui`(110개: 표시 논리, 파서, 번역, GTK 스모크) | 통과. 부하가 큰 환경에서 시간 제한 시험 하나가 한 번 실패했다(네트워크 페이지 진단 줄, 다시 돌리면 통과, 로직 문제 아님) |
| 창 단추 | 컨테이너(Xvfb)에서 GTK3 CSD 앱(`gtk3-widget-factory`)과 xfwm4 | x, +, − 순서와 기호를 눈으로 확인. GTK3 가 `window-*-symbolic` 을 내장 그림에서 먼저 찾는 것을 재현해 CSS 배경으로 우회했다 |
| 창 가장자리 | 같은 환경, 합성 켬/끔 | CSD 창에만 그림자와 둥근 모서리를 주도록 CSS 를 고쳤다. **검은 선과 상자는 이 환경에서 재현되지 않았으므로 실기 확인이 필요하다** |
| 데스크톱 구성 | Xvfb: 상단 막대, picom(glx 시작 확인, 스크린샷은 xrender), plank 독, Papirus 기반 아이콘(회색 폴더), 터미널, Thunar | 구성 요소가 뜨고 모양이 의도대로 나옴(스크린샷). 소프트웨어 GLX 의 화면은 `scrot` 에 잡히지 않아 xrender 로 찍었다 |
| Firefox | 컨테이너에서 Firefox ESR 의 제목 줄 | 이 환경의 기본은 이미 시스템 제목 줄이었다. CSD 모드 재현은 하지 못했고 기본 설정 파일만 넣었다 |
| 화면 하네스 | `scripts/preview-desktop-in-docker.sh` | 저장소에 넣었다. 이미지를 처음 만들 때 오래 걸린다 |
| CI(ISO 빌드, 이미지 검사, QEMU 부팅·데스크톱·설치 E2E) | PR #17, 커밋 e662c05 | 통과: lint 전체, ISO 빌드, 이미지 검사(이전 이름 흔적 없음 포함), 부팅·USB·CPU 추론·데스크톱·설치 E2E(NVRAM 항목, `opereta boot status`, os-prober 설정 확인). 앞선 실행에서 Go 컴파일러가 조각으로 나눈 이전 이름 문자열을 바이너리에 합쳐 넣는 문제와 시험 인자 순서 문제를 찾아 고쳤다 |

## 확인하지 못한 것

- **실기**: 부팅 항목이 BIOS 목록에 보이는지(MSI 펌웨어는 설정 화면에 보이지 않을 수 있다 — 대신 `opereta boot next` 와 일회성 부팅 메뉴를 쓴다), picom 과 NVIDIA 오픈 드라이버의 동작, 창 가장자리 아티팩트, HiDPI, 블루투스 페어링, 디스플레이 해상도 변경, 소리 장치 전환.
- Windows(BitLocker)와 같은 디스크에 설치한 뒤의 두 시스템 부팅 전체 흐름. QEMU 시험은 Windows 비슷한 ESP 를 쓰지 않는다(`tests/qemu/install.py` 는 빈 디스크 설치 후 NVRAM 항목을 단언한다).
- Firefox 가 자체 제목 줄(CSD)을 그리는 경우의 단추 모양.
- Calamares 와 로그인 화면의 실제 모습(구성 파일 수준까지만 확인).
