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
