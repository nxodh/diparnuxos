# 0.6.0 설계: 두 번째 이름 변경(Opereta), 새 로고와 디자인, 릴리스 게시, OS 업데이트

- 확인 날짜: 2026-10-14
- 상태: 구현했고 컨테이너 시험을 통과했다. CI 결과는 [verification-phase-6.md](verification-phase-6.md) 에 적는다.

이 문서는 **이전 이름을 적지 않는다**(0.5.1 의 R4 를 이어 간다). 이전 이름은 "이전 이름"으로 표기한다.

## 1. 요구

사용자 지시(2026-10-14)다.

| 번호 | 내용 |
|---|---|
| R1 | OS 이름을 이전 이름에서 **Opereta** 로 바꾸고 시스템 전체에 적용한다. 0.5.1 과 같은 방식이다 |
| R2 | 새 로고(Drive `Opereta.png`)로 로고와 OS 디자인을 바꾼다 |
| R3 | 에이전트 Vedecaps 는 추후 **Deberopa** 로 이름이 바뀐다. Deberopa 로고(Drive `Deberopa.png`)를 준비해 둔다 |
| R4 | 이 대변경 뒤의 버전은 **0.6.0** 이다 |
| R5 | 이제부터 GitHub 에 태그를 게시하고, 릴리스를 만들고, ISO 파일도 게시한다. 단 법적 문제나 개인 정보 노출 위험이 있으면 하지 않는다(사용자 조건) |
| R6 | 0.6.0 에 OS 를 업데이트하는 기능을 넣는다 |
| R7 | 이전 이름의 흔적을 두지 않는다. 호환 별칭, 이전 경로, 이전 이름을 받는 `Replaces`/`Conflicts` 없음. git 이력은 다시 쓰지 않는다 |

## 2. 로고

| 이름 | 원본 | 내용 |
|---|---|---|
| Opereta | `assets/brand/source/opereta-logo.png`(1254×1254 RGBA, 배경 투명) | 밝은 회색(약 `#F0F0F0`) 원 위에 검정 굵은 둥근 "O". 왼쪽 위에 사선 홈이 있고 안쪽은 둥근 사각형 구멍이다. 흑백 두 가지뿐이라 기존 팔레트를 그대로 쓴다 |
| Deberopa | `assets/brand/source/deberopa-logo.png`(1265×1244 RGBA) | 이전까지 OS 로고였던 원형 "D". 사용자가 Drive 의 `Deberopa.png` 가 옛 로고 파일과 같은 이미지임을 확인했다(SHA-256 일치) |

생성 스크립트(`scripts/make-brand-assets.py`)는 불투명 화소에서 원의 중심과 반지름을 구해 깨끗한 원으로 다시 잘라 기준 이미지를 만든다. 새 원본은 가장자리의 반투명 얼룩이 커서 반지름을 1.5% 줄여 얼룩이 아이콘에 비치지 않게 했다.

## 3. 이름 규칙

| 이전 | 이후 | 쓰임 |
|---|---|---|
| 첫 글자만 대문자 형태 | `Opereta` | 표시 이름, 문서, 번역 |
| 소문자 형태 | `opereta` | 명령, 패키지, 경로, 그룹, 유닛, 모듈 |
| 대문자 형태 | `OPERETA` | 환경 변수, 상수 |
| 소문자 형태 + `os` | `operetaos` | 저장소, Go 모듈 경로, 애플리케이션 ID |

치환은 추적 파일의 내용과 파일·폴더 이름에 한 번의 스크립트로 적용했다(374개 파일 내용, 205개 경로). 스크립트는 이전 이름을 담으므로 저장소 밖에서 한 번만 실행했고 커밋하지 않았다.

이름 길이가 8자에서 7자가 되어 생긴 부작용: gofmt 정렬 2개 파일, 정렬 순서에 기대던 시험 1개(`ollama` 가 `opereta` 보다 앞선다)를 고쳤다.

## 4. 디자인

| 항목 | 변경 |
|---|---|
| 아이콘, 시작 메뉴 단추, 로그인 아바타, 설치기 이미지, 배경 마크 | 새 로고(원 안의 둥근 O)로 다시 만들었다. 원형은 그대로다 |
| 모서리 | 6px 에서 **8px**(GTK3 테마, xfwm4 테마). 로고의 둥근 사각형 언어에 맞췄다. xfwm4 모서리 조각의 너비가 8 이라 8px 가 한계다 |
| 팔레트 | 그대로(흑백 두 색과 회색 단계) |
| 연동 에이전트 아이콘 | `deberopa-{32,64,128,256}.png` 를 생성한다. Control Center 의 에이전트 페이지에서 쓴다 |

## 5. 릴리스 게시(R5)

[releases.md](releases.md) 에 절차가 있다. 요점:

- `v<버전>` 태그를 푸시하면 `.github/workflows/release.yml` 이 돈다. 태그가 `opereta.conf` 의 버전과 같은지, `main` 에 있는지, 그 커밋의 `iso` 워크플로가 성공했는지 먼저 확인한다.
- ISO 를 만들고 부팅을 확인한 뒤 `scripts/make-release-assets.sh` 가 OS 업데이트 저장소(서명), 공개 키, 체크섬을 `out/release/` 에 모은다. **ISO 는 `OPERETA_PUBLISH_ISO="yes"` 일 때만 포함한다. 기본은 `no` 다**(아래). 별도의 `publish` 작업(쓰기 권한은 여기만)이 GitHub 릴리스를 만든다.
- 서명 키가 없으면 저장소를 만들지 않는다(서명되지 않은 저장소는 올리지 않는다). ISO 도 올리지 않으면 올릴 파일이 없으므로 거절한다.
- 이름에 `-` 가 든 태그는 prerelease 다. prerelease 는 "최신 릴리스" 주소에 잡히지 않으므로 업데이트 채널에 나가지 않는다.

### ISO 를 올리지 않기로 한 결정(R5 의 조건)

조사한 결과(상세는 [licenses.md](licenses.md) 10절) ISO 는 올리지 않는다. CUDA 런타임 번들링과 Intel 펌웨어의 재배포 조건 해석이 해소되지 않았고, GPL 소스 제공을 준비하지 않았으며, 프로젝트 라이선스가 정해지지 않았다. 반면 OS 업데이트 저장소의 패키지는 이 프로젝트의 코드와 MIT 소프트웨어뿐이라 올린다. 이 결정은 법률 자문이 아니라 문서 조사에 근거한다. 체크리스트를 끝내면 설정 한 줄로 ISO 를 켠다.

## 6. OS 업데이트(R6)

기존 업데이트 엔진(`opereta-update`: 새 커널 먼저 설치, DKMS 확인, `grub-reboot` 한 번 부팅, 확인 뒤 확정, 롤백)은 Opereta 패키지(`opereta-*`)도 갱신하지만 **새 버전을 받을 길이 없었다**. 자체 APT 저장소의 주소와 서명 키가 정해지지 않아 소스가 꺼져 있었다. 0.6.0 은 그 길을 GitHub 릴리스로 연다.

| 구성 | 내용 |
|---|---|
| 채널 | 릴리스 자산을 **평평한(flat) APT 저장소**로 쓴다. 클라이언트 소스는 `https://github.com/nxodh/operetaos/releases/latest/download` 의 `Suites: ./` 다. 디렉터리를 가질 수 없는 릴리스 자산에 맞춘 형식이다 |
| 서명 | `InRelease`/`Release.gpg` 를 저장소 서명 키로 서명한다. 이미지의 `opereta-archive-keyring` 이 공개 키를 갖고 `Signed-By` 로 그 키만 믿는다. 변조와 다른 키로 서명한 저장소는 apt 가 거절한다(시험으로 확인) |
| OS 버전 | `opereta-base` 의 버전이다(모든 `opereta-*` 가 같은 릴리스 버전을 쓴다). 갱신 계획에 `os_from`/`os_to` 로 들어가고 CLI(`opereta update --check`)와 Control Center 가 "Opereta 0.6.0 → 0.6.1" 로 보여 준다 |
| 채널 상태 | 소스가 꺼져 있으면(`Enabled: no` 또는 파일 없음) 확인 결과가 "최신"이어도 Debian 과 NVIDIA 저장소만 본 것이다. CLI 와 Control Center 가 이를 경고로 알린다 |
| 적용 | 기존 엔진 그대로다. 커널과 드라이버 안전 장치, 부팅 뒤 확인, 롤백을 같이 쓴다. OS 버전만 바뀌는 갱신은 재부팅이 필요 없다 |

### 알려진 한계

- 평평한 저장소에는 이전 버전을 남기는 pool 이 없다. 롤백이 드라이버 정책 패키지(`opereta-nvidia`)의 이전 버전이 필요한데 이번 갱신에서 그 패키지가 바뀌었다면 `rollback_unavailable` 로 거절한다(아무것도 바꾸지 않는다). GRUB 메뉴에서 이전 커널을 고르는 길은 남는다.
- 서명 키는 사용자가 만든다. 키(`keys/opereta-archive-keyring.pgp`)가 커밋되기 전의 이미지는 소스가 꺼진 채로 나간다. 그 이미지로는 OS 업데이트를 받을 수 없다.
- `releases/latest/download` 는 저장소 이름이 바뀐 뒤에만 이 이름으로 열린다(이전 주소의 리다이렉트는 GitHub 가 한다).

## 7. Deberopa(R3)

Deberopa 는 아직 릴리스가 없다. 0.6.0 은 **Vedecaps 의 이름, 고정 버전, 내려받는 주소, SHA-256 을 바꾸지 않는다**(바꾸면 고정 버전을 내려받지 못하고 실제 Vedecaps 로 하는 호환 시험이 깨진다). 사용자가 Vedecaps 를 Deberopa 로 바꾸면 한 번의 치환 묶음으로 따라간다.

### 고정한 Vedecaps 0.1.5 가 새 OS 이름을 모른다

실제 Vedecaps 0.1.5 로 시험하니(`make test-vedecaps-docker`) 요청과 응답, 키 규칙, https(`options.ca`)는 모두 동작한다. 그러나 Vedecaps 가 **OS 를 이름으로 알아보는 세 가지**는 Vedecaps 안에 이전 이름이 들어 있어 새 이름과 맞지 않는다.

참고: 이 때문에 고정한 `/usr/bin/vedecaps` 바이너리 안에는 이전 OS 이름 문자열이 남아 있다. 이미지의 이전 이름 검사(`tests/nvidia/image.sh`)는 이 파일 하나만 예외로 두고 근거를 적는다. Deberopa 릴리스로 바꾸면 예외를 지운다.

| Vedecaps 기능 | 지금 동작 |
|---|---|
| `vedecaps debug local` 이 서버를 "Opereta <버전>" 으로 알아보고 슬롯 수를 보여 줌 | 알아보지 못한다(정보 주소와 응답 헤더 이름이 다르다). 서버는 일반 OpenAI 호환 서버로 보인다 |
| TLS 오류 안내의 명령 이름 | CA 를 어디서 얻는지는 안내하지만 명령 이름이 이전 이름이다(`... tls ca`) |
| 슬롯 수로 동시 요청 조절 | 정보 주소를 읽지 못하므로 쓰지 못한다. `opereta connect vedecaps` 가 설정에 쓰는 `max_concurrency` 가 대신 적용된다 |

Opereta 쪽에서 고칠 수 없다(이전 이름을 응답에 남기면 이름 흔적 방침 R7 에 어긋난다). **Vedecaps 가 새 이름(정보 주소 `/opereta/info`, 응답 헤더 `X-Opereta`, `opereta tls ca` 안내)을 아는 릴리스가 필요하다.** Deberopa 릴리스에 함께 넣는 것이 자연스럽다. 시험은 이 세 항목을 "알려진 한계"로 출력하고, `opereta.conf` 의 `VEDECAPS_RECOGNISES_OPERETA` 에 그 버전을 적으면 다시 실패로 본다.

| 항목 | 바꿀 곳 |
|---|---|
| 설정 | `opereta.conf` 의 `VEDECAPS_VERSION`, `VEDECAPS_REPO`, `VEDECAPS_SHA256`(릴리스 `checksums.txt` 와 함께) |
| 패키지 | `packages/opereta-vedecaps`(패키지 이름, `fetch.sh`, 실행 파일 이름) |
| 명령 | `opereta connect vedecaps`, `opereta mcp` 의 안내 문구, `daemon/internal/vedecaps` |
| 화면 | Control Center 의 에이전트 페이지(`gui/opereta_cc/vedecaps.py`)와 번역. 아이콘은 이미 있다(`deberopa-*.png`) |
| 시험 | `make test-vedecaps-docker`(`tests/vedecaps`), E2E 의 에이전트 단계 |
| 문서 | [vedecaps.md](vedecaps.md) 와 이 목록 |

## 8. GitHub

| 항목 | 방침 |
|---|---|
| 저장소 이름 | `nxodh/operetaos` 로 변경한다(이전 규칙 `<이름>os` 를 이어 간다). 0.6.0 이 CI 를 통과한 뒤 `main` 병합 직전에, 실행 전에 한 번 알린다 |
| 원격 주소 | 변경 직후 `git remote set-url origin` 으로 갱신한다 |
| 이전 주소 | GitHub 가 새 주소로 리다이렉트한다. 이전 이름의 외부 흔적이며 제거할 수 없다 |
| 이력 | 다시 쓰지 않는다 |
| 로컬 폴더 | 이전 이름의 로컬 폴더는 Claude 프로젝트 메모리 경로에 쓰이므로 사용자가 직접 옮긴다 |
| `main` 병합과 태그 | 사용자가 말할 때만 한다 |

## 9. 수용 기준

1. 이전 이름(두 개)이 추적 파일(경로와 내용), 빌드한 이미지, 라이브 세션, 설치된 시스템에 없다(`make check-legacy-name`, 이미지 검사, 라이브 세션 검사).
2. 기존 시험이 모두 통과한다.
3. `os-release` 가 `NAME="Opereta"`, `ID=opereta` 이고 ISO 이름이 `opereta-0.6.0-amd64.iso` 이다.
4. 로그인 화면, 데스크톱, Control Center, 설치기의 캡처에 새 로고가 표시된다(자동 판정은 "비어 있지 않음"까지, 모양은 사람이 확인한다).
5. 평평한 저장소를 실제 apt 가 서명 검증하고 올리며, 변조와 다른 키를 거절한다(`make test-apt-repo-docker`).
6. 릴리스 자산 스크립트가 서명 키 유무, 공개 키 불일치, ISO 누락과 손상을 올바르게 처리한다(`make test-release-docker`).

## 10. 한계

| 한계 | 설명 |
|---|---|
| 설치된 이전 버전과 비호환 | 의도된 결정이다. 새 이미지로 새로 설치한다. 계정 그룹, 상태 디렉터리, 서비스 이름이 모두 바뀌었다 |
| 릴리스 워크플로는 아직 실행해 보지 않았다 | 태그를 푸시해야 돈다. 구문(`actionlint`)과 자산 모으기 스크립트(컨테이너 시험)까지만 확인했다. 첫 실행에서 드러나는 문제는 검증 기록에 적는다 |
| ISO 공개 배포 | 올리지 않는다(위). 켜는 조건은 [licenses.md](licenses.md) 10.3 이다 |
| 실기 | 실기 설치, 실제 커널 갱신, 실제 GPU, 새 업데이트 채널의 실제 네트워크 경로는 미검증이다 |
