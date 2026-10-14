# 0.6.0 검증 기록: 두 번째 이름 변경, 새 로고, 릴리스 게시, OS 업데이트

- 확인 날짜: 2026-10-14
- 상태: 컨테이너 시험과 단위 시험은 통과했다. **CI 전체(ISO, QEMU 부팅·데스크톱·설치 E2E)와 릴리스 워크플로는 아직 돌지 않았다**(아래 3절). 실기는 미검증이다.

설계는 [phase-6-design.md](phase-6-design.md), 릴리스 절차는 [releases.md](releases.md) 에 있다.

## 1. 확인한 것(컨테이너와 단위 시험)

| 대상 | 방법 | 결과 |
|---|---|---|
| 추적 파일 경로와 내용의 이전 이름(둘)과 그 약어 | `make check-legacy-name`(`git ls-files`, `git grep -a -i`, 바이너리 포함, vendor 포함) | 0 건(612개 추적 파일) |
| 비밀 값과 **개인 정보**(로컬 홈 경로, 개인 이메일) | `make check-secrets`. 개인 정보 검사는 0.6.0 에서 추가했다. 옛 `AGENTS.md` 의 로컬 경로 줄을 실제로 잡아내는 것을 확인했다 | 통과 |
| 셸 스크립트 | `make lint`(shellcheck), `actionlint`(두 워크플로) | 통과 |
| Go | `go vet`, `gofmt`, `go test ./...`(Debian 13 컨테이너, 전체) | 통과 |
| Control Center | `make test-gui`(단위, 번역 목록, GTK 연기 시험) | 84개 통과 |
| 평평한 APT 저장소 | `make test-apt-repo-docker`: 실제 apt 가 서명을 검증하고, 옛 버전에서 올리고, `opereta-*` 밖의 패키지를 막고, 변조된 색인과 다른 키로 서명한 저장소를 거절한다(pool 형식 시험도 그대로 통과) | 통과 |
| 릴리스 자산 | `make test-release-docker`: 키가 없으면 ISO 만 모으고 경고, 키가 있으면 서명된 저장소와 공개 키를 모음, 공개 키 불일치·공개 키 누락·ISO 누락·ISO 손상은 거절, `SHA256SUMS` 가 모든 파일을 검증 | 통과 |
| 업데이트 엔진 | `make test-update-docker`(실제 apt/dpkg, 가짜 저장소) | 통과 |
| OS 버전·채널 | 엔진 단위 시험: 계획의 `os_from`/`os_to`, 상태 기록, 채널 읽기(켜짐, 꺼짐, 파일 없음, 여러 문단), `Info` 의 채널과 OS 버전, 채널만 실패해도 Debian 갱신은 계속하고 `channel_error` 를 남김, 다른 저장소가 실패하면 그대로 실패 | 통과 |
| 데스크톱 구성 | `make test-desktop-config-docker`(Xvfb 세션, lightdm 로그인 화면) | 통과 |
| 설치기 구성 | `make test-installer-config-docker` | 통과 |
| 네트워크 | `make test-network-docker` | 통과 |
| Vedecaps 호환 | `make test-vedecaps-docker`(실제 Vedecaps 0.1.5) | 통과. 단, 아래 2절의 알려진 한계 3개 |

## 2. 알려진 한계

| 항목 | 설명 |
|---|---|
| **고정한 Vedecaps 0.1.5 가 새 OS 이름을 모른다** | 연결, 요청, 키, https 는 동작한다. `vedecaps debug local` 의 서버 인식과 슬롯 수, TLS 안내의 명령 이름은 Vedecaps 안에 이전 이름이 있어 맞지 않는다. 시험은 이를 "KNOWN LIMIT" 로 출력한다. 해결은 Vedecaps(Deberopa) 릴리스가 새 이름을 알게 하는 것이다([phase-6-design.md](phase-6-design.md) 7절) |
| 첫 릴리스 전의 채널 | 저장소 이름이 바뀌기 전, 그리고 첫 릴리스가 게시되기 전에는 `releases/latest/download` 가 404 다. 이때 `opereta update --check` 는 Debian 과 NVIDIA 만 확인하고 채널을 받지 못했다고 알린다(`channel_error`). 갱신 자체는 막지 않는다 |
| 롤백과 이전 버전 | 평평한 저장소에는 이전 버전이 없다([updates.md](updates.md)) |
| 설치된 이전 버전 | 새 이미지로 새로 설치한다. 이전 버전에서 올라오는 경로는 없다(의도된 결정) |

## 3. 확인하지 못한 것

| 항목 | 이유 |
|---|---|
| CI: ISO 빌드, QEMU 부팅·데스크톱·설치 E2E | 푸시와 PR 로만 돈다. 결과는 PR 에 적는다. 이름이 바뀌어 CUDA 캐시 키가 바뀌므로 첫 실행은 CUDA 를 다시 컴파일한다(약 1시간) |
| 릴리스 워크플로(`release.yml`) | 태그를 푸시해야 돈다. 구문과 자산 모으기까지만 확인했다. 첫 실행에서 `verify`(CI 확인), `build`(캐시 복원, ISO), `publish`(릴리스 생성) 단계의 문제가 드러날 수 있다 |
| 서명 키로 실제 서명한 저장소 | 시험은 임시 키를 쓴다. 실제 키(지문은 [releases.md](releases.md))로 서명한 릴리스는 첫 릴리스에서 확인한다 |
| 실제 네트워크 경로 | `https://github.com/.../releases/latest/download` 의 리다이렉트를 apt 가 따라가는지는 첫 릴리스 뒤에 확인한다 |
| 화면의 새 로고 모양 | 자동 판정은 "비어 있지 않음"까지다. 로그인 화면, 데스크톱, Control Center, 설치기의 모양은 사람이 확인한다 |
| 실기 | 실기 설치, 실제 커널 갱신, 실제 GPU, VMware |

## 4. 이번에 알게 된 것

- **이름 길이 변화의 부작용**: 이름이 8자에서 7자가 되어 gofmt 정렬이 2개 파일에서 바뀌었고, 문자열 정렬 순서에 기대던 시험 1개가 깨졌다(`ollama` 가 `opereta` 보다 앞선다). 이름 변경 뒤에는 gofmt 와 전체 시험을 다시 돌린다.
- **약어도 이름이다**: API 키 접두사와 시험용 표지에 이전 이름의 약어가 남아 있었다. 이름 흔적 검사에 약어를 넣고 접두사를 새 약어로 바꿨다(발급된 키가 없는 이미지라 호환 문제는 없다).
- **연동 대상의 이름도 따라온다**: 이름을 바꾸면 그 이름을 외부 프로그램(Vedecaps)이 알아보던 기능이 조용히 사라진다. 이름 변경 시험에 "그 이름을 아는 외부 기능"을 따로 두고 알려진 한계로 드러냈다.
- **채널 하나의 실패가 전체를 막으면 안 된다**: Opereta 채널(GitHub)만 받지 못했을 때 Debian 과 NVIDIA 의 보안 갱신까지 막으면 채널을 추가한 것이 오히려 퇴보다. 채널만 실패하면 경고하고 계속한다.
- **개인 정보**: 로컬 경로와 개인 이메일이 추적 파일이나 문서에 섞여 들어오지 않도록 린트에 검사를 넣었다. git 이력의 커밋 작성자 이메일은 이미 공개되어 있다(아래 5절).

## 5. 개인 정보와 공개 저장소

- 서명 **개인 키**는 저장소 밖(`~/opereta-apt-key`)과 GitHub 비밀 값에만 있다. 추적 파일에는 공개 키(`keys/*.pgp`, `.asc`)와 지문만 있고, 공개 키의 사용자 ID 는 GitHub noreply 주소다.
- 0.6.0 의 커밋은 GitHub noreply 주소로 작성한다.
- **이미 공개된 것**: 이전 커밋 154개의 작성자 이메일은 개인 주소다. git 이력을 다시 쓰지 않는 방침(R7)이라 `main` 의 이력에는 남아 있다. 지우려면 이력 재작성과 강제 푸시가 필요하고, 그것은 사용자의 별도 결정이다. 앞으로의 노출을 막으려면 GitHub 계정 설정의 "Keep my email addresses private" 와 "Block command line pushes that expose my email" 를 켠다.
