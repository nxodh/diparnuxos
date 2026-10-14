# 0.6.2 설계: 코딩 에이전트 Vedecaps → Deberopa

- 확인 날짜: 2026-10-14
- 상태: 구현했고 실제 Deberopa 0.2.0 으로 호환 시험을 통과했다. CI 결과는 [verification-phase-6-2.md](verification-phase-6-2.md) 에 적는다.

## 1. 배경

사용자가 코딩 에이전트의 이름을 Vedecaps 에서 **Deberopa** 로 바꿨고 Deberopa 0.2.0 을 릴리스했다(`nxodh/deberopa`, 태그 `v0.2.0`, 2026-10-14).
이 릴리스는 [deberopa-agent-handoff-0.6.1.md](deberopa-agent-handoff-0.6.1.md) 의 계약을 지킨다. 확인한 것:

| 항목 | 확인 |
|---|---|
| 자산 | `deberopa-linux-x64.tar.gz`, 체크섬 `checksums.txt` 와 일치 |
| 묶음 | `deberopa` 하나만(0755), `deberopa --version` 은 `0.2.0` |
| Opereta 인식 | 바이너리에 `/opereta/info`, `X-Opereta`, `opereta tls ca/status/renew`, `opereta connect deberopa`, `OPERETA_API_KEY` 가 있음. 이전 OS 이름은 없음 |
| 이전 | 새 설정 폴더가 없고 옛 폴더가 있으면 첫 실행 때 설정, 로그인, 세션, 신뢰한 프로젝트를 복사. 옛 `VEDECAPS_*` 환경 변수는 이번 릴리스에서 읽고 경고 |

## 2. 변경

| 대상 | 0.6.1 | 0.6.2 |
|---|---|---|
| 에이전트 | Vedecaps 0.1.5(고정) | **Deberopa 0.2.0**(고정, SHA-256 확인) |
| 패키지 | `opereta-vedecaps` | `opereta-deberopa`(이전 이름의 패키지를 `Replaces`/`Conflicts`) |
| 실행 파일 | `/usr/bin/vedecaps` | `/usr/bin/deberopa` |
| 설정 설정 | `~/.config/vedecaps/vedecaps.json` | `~/.config/deberopa/deberopa.json` |
| 연결 명령 | `opereta connect vedecaps` | `opereta connect deberopa`(`vedecaps` 는 별칭이며 알림을 낸다) |
| 설정 조각의 CA 기본 경로 | `~/.config/vedecaps/opereta-ca.pem` | `~/.config/deberopa/opereta-ca.pem` |
| 제어 센터 | Vedecaps 페이지 | Deberopa 페이지(아이콘은 이미 있음) |
| `opereta.conf` | `VEDECAPS_*` | `DEBEROPA_*`(`DEBEROPA_RECOGNISES_OPERETA` 는 없어짐: 항상 엄격하게 본다) |
| 호환 시험 | `make test-vedecaps-docker`(알려진 한계 3개) | `make test-deberopa-docker`(**모두 엄격하게 통과**) |
| 이미지 이름 흔적 검사 | `/usr/bin/vedecaps` 하나만 예외 | **예외 없음** |

## 3. 옛 설정 폴더와 이전

Deberopa 는 새 폴더(`~/.config/deberopa`)가 **없을 때만** 옛 폴더(`~/.config/vedecaps`)를 복사한다. 그런데 `opereta connect deberopa --write` 가
새 폴더를 먼저 만들면 사용자의 옛 설정, 로그인, 세션이 옮겨지지 않는다. 그래서:

- 새 폴더가 없고 옛 폴더가 있으면 **옛 폴더의 옛 파일에 합친다**. Deberopa 가 첫 실행 때 함께 옮긴다. 출력에 그 사실을 알린다.
- 새 폴더가 있거나 둘 다 없으면 새 파일에 쓴다.
- 시험: `TestResolveConfigFile`, `TestConnectDeberopaWritesIntoTheOldFolderUntilDeberopaMigratesIt`(새 폴더가 만들어지지 않음, 사용자의 설정 보존).

## 4. 하지 않은 것

- 이전 이름의 패키지(`opereta-vedecaps`)에서 올라오는 별도 이전 경로는 만들지 않았다. 그 이름의 패키지는 공개된 적이 없다(0.6.0, 0.6.1 은 미공개). `Replaces`/`Conflicts` 로 시험 설치만 처리한다.
- 이전 이름의 Vedecaps 실행 파일이 새 릴리스로 스스로 올라가는 경로는 Deberopa 쪽 문제다(릴리스 노트에 적혀 있다).

## 5. 법적으로 확인되지 않은 것(소유자 몫)

- 이름 **Deberopa** 가 기존 상표와 충돌하는지 조사하지 않았다.
- 로고 원본에 AI 생성 도구의 출처 메타데이터가 있었다(Opereta 저장소에서는 제거했다). AI 생성물의 저작권 귀속은 불확실하다.

## 6. 사용자 요청 추가분(2026-10-14)

사용자가 0.6.2 에 세 가지를 더 요청했다. 결정 기록은 [decisions.md](decisions.md) U19.

### 6.1 Deberopa 자동 갱신

이미지의 Deberopa 는 `opereta.conf` 의 고정 버전이라 새 릴리스가 나와도 다음 OS 릴리스까지 뒤처진다. 이 틈을 메운다.

| 항목 | 내용 |
|---|---|
| 확인 | GitHub `nxodh/deberopa` 의 `releases/latest`(초안과 사전 릴리스는 제외). 태그는 `vX.Y.Z` 만 인정하고 자산 `deberopa-linux-x64.tar.gz` 와 `checksums.txt` 가 있어야 한다 |
| 시점 | `opereta-deberopa-update.timer`: 부팅 10분 뒤, 이후 하루 한 번(최대 2시간 무작위 지연, `Persistent`). 수동은 `opereta deberopa check\|update` 와 Control Center |
| 조건 | 설치된 버전보다 **높을 때만** 바꾼다(내리지 않는다). 라이브 세션에서는 하지 않는다 |
| 검증 | SHA-256(`checksums.txt`)이 맞아야 하고, 묶음에는 `deberopa` 하나만 있어야 한다(다른 이름과 `..` 는 거절). 내려받은 파일은 root 가 아니라 **nobody 권한**(`setpriv`)으로만 실행해 `--version` 이 태그와 같은지 확인한다 |
| 설치 | 이미지의 `/usr/bin/deberopa` 는 건드리지 않는다. `/usr/local/lib/opereta/deberopa/versions/<버전>/deberopa` 에 두고 `/usr/local/bin/deberopa` 링크를 바꾼다(`PATH` 에서 앞선다). 이 경로는 모든 사용자가 실행하므로 0750 인 `/var/lib/opereta` 밖에 있다. 링크 교체는 원자적이며 검증이 끝나기 전에는 바꾸지 않는다. 사용자가 둔 `/usr/local/bin/deberopa` 가 링크가 아니면 건드리지 않는다 |
| 정리 | OS 업데이트로 이미지의 Deberopa 가 같거나 높아지면 링크와 내려받은 파일을 지운다. 이전 내려받은 버전은 새 버전을 설치한 뒤 지운다 |
| 실패 | 네트워크가 없거나 GitHub 가 응답하지 않아도 타이머 실행은 성공(0)으로 끝나고 상태 파일(`/var/lib/opereta/deberopa/state.json`)에만 남긴다. 이전 Deberopa 는 그대로 쓸 수 있다 |
| 끄기 | `opereta deberopa auto off`(설정 `settings.json`). 켜져 있는 것이 기본이다. Control Center 의 시스템 페이지에도 스위치가 있다 |
| 에이전트 설정 | 이미지에 싣는 Deberopa 설정(`opereta connect deberopa`)은 계속 `autoupdate:false` 다. 갱신은 Opereta 가 한다 |

**한계(알려 둠)**: 체크섬은 같은 GitHub 릴리스에서 받는다. 릴리스 계정이 탈취되면 막지 못한다. Opereta 가 따로 서명하는 것이 아니다. 더 강하게 하려면 Deberopa 릴리스에 분리 서명(예: minisign)을 도입해야 하며 Deberopa 저장소의 일이다.

구현: `daemon/internal/agentupdate`(로직, 단위 시험 14개), `opereta-update deberopa ...`(root 헬퍼), `updatectl` 의 동작 `deberopa_check`, `deberopa_update`, `deberopa_auto_on`, `deberopa_auto_off` 와 `GET /v1/update` 의 `deberopa` 필드, CLI `opereta deberopa`, Control Center. 실제 릴리스로의 시험은 `make test-deberopa-update-docker`.

### 6.2 디자인 전면 개편과 Pretendard

[design.md](design.md) 에 원칙, 팔레트, 적용 범위를 적었다. 요약:

- macOS Big Sur 풍: 둥근 사각형(창 12px, 단추 8px), 회색조, 얇은 구분선, 넉넉한 여백. 창 단추는 왼쪽에 x(닫기), −(최소화), +(최대화) 이고 기호가 항상 보인다.
- 두 테마: 밝은 `Opereta`(기본), 어두운 `Opereta-Dark`. Control Center 의 색 구성표가 데스크톱 테마, 배경 화면, 터미널 색을 함께 바꾼다.
- 생성: `scripts/make-theme.py` 가 GTK3 CSS, xfwm4 이미지 120장, 아이콘 테마를 만든다(`make-brand-assets.sh` 가 부른다).
- **Pretendard**: SIL OFL 1.1, Debian trixie main 의 `fonts-pretendard` 를 수정 없이 설치한다. 법적 검토는 [licenses.md](licenses.md) 11절이며 문서 기반이고 법률 자문이 아니다.
- GTK4/libadwaita 는 창 컨트롤 기호를 아이콘 테마로 바꿀 수 없어, 같은 모양의 PNG 를 CSS 배경으로 그린다.

### 6.3 램 상한 해제

0.5.4 에서 정한 유휴 램 상한(헤드리스 500, 데스크톱 800 MiB)을 풀었다. `tests/qemu/desktop-session.py` 는 값을 기록만 하고 실패시키지 않는다. 합성 창 관리자와 그림자 등 디자인에 필요한 것은 램을 이유로 빼지 않는다. 새 기준선은 0.6.2 의 첫 CI 값부터 [memory.md](memory.md) 에 적는다.
