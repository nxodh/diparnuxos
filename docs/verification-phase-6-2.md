# 0.6.2 검증 기록: Deberopa 이름 변경

- 확인 날짜: 2026-10-14
- 상태: 단위 시험, 컨테이너 시험, **실제 Deberopa 0.2.0 으로 한 호환 시험**을 통과했다. CI 전체(ISO, 이미지 검사, QEMU E2E)는 아직 돌지 않았다.

| 대상 | 방법 | 결과 |
|---|---|---|
| 릴리스 계약 | `deberopa-linux-x64.tar.gz` 를 받아 `checksums.txt` 대조, 묶음 내용, `--version` | 일치(`0.2.0`, 묶음에 `deberopa` 하나) |
| 인식 문자열 | 바이너리의 문자열 검사(컨테이너) | 새 이름 인식 문자열 있음, 이전 OS 이름 0건 |
| 호환 시험 | `make test-deberopa-docker`(실제 Deberopa 0.2.0, 게이트웨이, 가짜 모델 서버) | **모든 시나리오 통과, 알려진 한계 없음**: 키 규칙, https 이름·IP, 안내의 `opereta tls ca`, `debug local` 이 "Opereta <버전> · 2 slots · 16,384 ctx per slot" |
| 패키지 | `opereta-deberopa` 를 amd64 컨테이너에서 빌드 | 통과(SHA-256 확인, `Replaces/Conflicts`, `/usr/bin/deberopa`) |
| Go | `go vet`, `gofmt`, `go test ./...` | 통과 |
| 제어 센터 | `make test-gui` | 88개 통과 |
| 이름 흔적, 비밀, 개인 정보, 린트 | `make lint check-legacy-name check-secrets` | 통과 |
| 이전 폴더 | 옛 폴더만 있을 때 새 폴더를 만들지 않고 옛 파일에 합침 | 통과 |

## 사용자 요청 추가분(Deberopa 자동 갱신, 디자인, Pretendard, 램 상한 해제)

| 대상 | 방법 | 결과 |
|---|---|---|
| Deberopa 자동 갱신: 로직 | `go test ./internal/agentupdate`(가짜 GitHub 서버) | 통과: 뒤처졌을 때 설치, 내리지 않음, 확인만, 자동 끔, 사전 릴리스·이상한 태그·체크섬 없음·체크섬 불일치·묶음에 파일 추가·경로 탈출·버전 불일치 거절(링크 불변), 네트워크 실패, 패키지가 따라잡으면 링크 제거, 사용자 파일 보존, 옛 버전 정리 |
| 같은 기능: 실제 릴리스 | `make test-deberopa-update-docker`(amd64 컨테이너, 실제 GitHub `nxodh/deberopa`) | 통과: 0.0.1 로 위장한 이미지 → `0.2.0` 설치, 링크, 일반 사용자 실행, 이미지 파일 불변, 두 번째 실행 `up_to_date`, `auto off` 면 `disabled`, 네트워크 실패는 `--auto` 에서 종료 코드 0 |
| 동작, CLI, 유닛 | `updatectl`, `cmd/opereta`, `dist` 단위 시험 | 통과(헬퍼 명령 매핑, 라이브 거절, `opereta deberopa`, 타이머 유닛 줄) |
| 데스크톱 구성 | `make test-desktop-config-docker`(amd64, Xvfb 의 Xfce 세션) | 통과: 두 테마와 xfwm4 이미지, 아이콘 테마, Pretendard fontconfig(`fc-match sans-serif` = Pretendard), UI 글꼴, 창 단추 배치 `CHM\|`, 터미널 색, 로그인 화면 |
| Control Center | `make test-gui` | 96개 통과(Deberopa 표시 논리, 색 구성표 전환, 팔레트 회색조 검사 포함) |
| 시각 확인 | arm64 컨테이너의 Xvfb + xfwm4(합성) + 패널 + Thunar + 터미널 + Control Center | 밝은/어두운 테마 모두에서 둥근 프레임, 왼쪽 원형 x, −, + 단추, 카드 모서리를 눈으로 확인(스크린샷). 창 컨트롤 기호는 PNG 배경으로 그려야 보였다 |
| 램 | `tests/qemu/desktop-session.py` | 상한 없이 기록만 한다. CI 의 첫 값은 아직 없다 |

## 확인하지 못한 것

- CI: 새 테마와 Pretendard 가 든 ISO 의 이미지 검사, QEMU 데스크톱 E2E, 설치 E2E, 램 기록값.
- 실제 하드웨어와 GPU 에서의 화면 모습(해상도, HiDPI, 글꼴 크기), Calamares 와 lightdm 화면의 실제 모양(컨테이너에서는 구성 파일만 확인했다).
- `opereta-deberopa-update.timer` 가 설치된 이미지에서 켜져 있는지(CI 이미지 검사가 확인한다).

- CI: 이미지 검사(`opereta-deberopa` 패키지, `/usr/bin/deberopa`, 예외 없는 이름 검사), QEMU 부팅·데스크톱·설치 E2E.
- 실제 GPU 에서 Deberopa 작업을 끝까지 돌리는 것.
