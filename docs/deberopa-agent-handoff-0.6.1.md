# Deberopa 개발 에이전트에게 넘기는 문서 (Vedecaps 의 이름 변경과 Opereta 연동)

- 작성: 2026-10-14, Opereta 쪽 세션
- 받는 쪽: `nxodh/vedecaps` 저장소를 개발하는 에이전트(이 문서를 읽고 바로 시작할 수 있게 썼다)
- 목적: 코딩 에이전트 **Vedecaps 를 Deberopa 로 이름을 바꾸고**, Opereta(모델 서버 운영체제) 0.6.x 와 같은 계약으로 동작하게 하는 첫 Deberopa 릴리스를 만든다.
- 이 문서는 Opereta 저장소(`nxodh/operetaos`, 브랜치 `phase-6-1`)의 `docs/` 에 있다. **Deberopa 저장소에 복사해 넣지 않아도 된다**(읽고 따르면 된다).
  **Opereta 저장소의 파일은 바꾸지 않는다.** Opereta 쪽은 Opereta 세션이 맡는다.

## 0. 먼저 알아 둘 것

| 사실 | 내용 |
|---|---|
| 제품 이름 | **Vedecaps → Deberopa**. 사용자가 정했다(2026-10-14) |
| 로고 | 지금 Vedecaps 의 원형 "D" 로고가 Deberopa 로고다. 원본: Opereta 저장소 `assets/brand/source/deberopa-logo.png`(1265×1244, 메타데이터 제거). 크기별 원형 아이콘: `assets/brand/generated/deberopa-{32,64,128,256}.png` |
| OS 의 이름 | 모델 서버 OS 는 **Opereta** 다(이전 이름은 더 이상 없다). Vedecaps 0.1.5 는 **이전 OS 이름**으로 OS 를 알아본다. 이것을 새 이름으로 바꾸는 것이 이 작업의 핵심이다 |
| 현재 상태 | Opereta 는 Vedecaps **0.1.5** 를 이미지에 고정한다. 그 버전은 새 OS 이름을 몰라서 Opereta 의 호환 시험에 "알려진 한계" 3개가 있다(아래 3절) |
| 호환 | 이전 OS 이름은 이제 존재하지 않으므로 **이전 OS 이름을 알아보는 코드는 지운다**(별칭을 두지 않는다) |

## 1. 사용자(소유자)에게 먼저 확인할 결정

아래는 **당신이 정하지 않는다.** 사용자에게 묻고 답을 받은 뒤에 진행한다. 답이 없으면 괄호 안의 기본안으로 진행하되 그 사실을 결과에 적는다.

| # | 결정 | 기본안 |
|---|---|---|
| D1 | 첫 Deberopa 릴리스의 **버전 번호** | `0.2.0`(이름이 바뀌는 첫 릴리스. 이미 나간 번호를 다시 쓰지 않는다) |
| D2 | GitHub 저장소 이름을 `nxodh/deberopa` 로 **바꿀지, 언제 바꿀지** | 릴리스 직전, 사용자가 직접 또는 지시한 뒤에만(`gh repo rename` 은 외부에 보이는 작업이다) |
| D3 | 이름만 바뀐 **마지막 Vedecaps 릴리스**(예: 0.1.6)를 먼저 낼지 | 내지 않는다. 바로 Deberopa 로 간다 |
| D4 | 설정 폴더 이름(`~/.config/vedecaps/` → `~/.config/deberopa/`)을 바꾸고 옛 폴더를 읽는 **이전(migration)** 을 넣을지 | 바꾼다. 새 폴더가 없고 옛 폴더가 있으면 옛 설정을 읽는다(쓰기는 새 폴더에만) |
| D5 | 환경 변수 접두사 `VEDECAPS_*` → `DEBEROPA_*` | 바꾼다. 한 번의 릴리스 동안 옛 이름도 읽고 사용 중단 경고를 낸다 |

## 2. 읽을 순서

| 순서 | 문서(Opereta 저장소 `docs/`) | 무엇을 얻는가 |
|---|---|---|
| 1 | `vedecaps.md` | 지금 Opereta 와 Vedecaps 가 어떻게 연결되는지(사용자 관점) |
| 2 | `vedecaps-agent-handoff-0.5.4.md` | 이전 인계 문서. TLS 계약(C1~C10)과 VE2(`options.ca`)의 형식. 이미 0.1.5 에 반영됐다 |
| 3 | `vedecaps-repo-changes-design.md` | VE1~VE4 상세(현재 코드 근거, 변경 지점, 시험) |
| 4 | `phase-6-design.md` 7절 | 이 작업이 Opereta 쪽에서 어떻게 소비되는지 |

## 3. 할 일

### 3.1 이름 변경

| 대상 | 변경 |
|---|---|
| 실행 파일 | `vedecaps` → `deberopa` |
| 패키지 이름, `package.json`, `VERSION`, README, 문서, 설치 스크립트, 셸 자동 완성, 도움말 | `Vedecaps` → `Deberopa`, `vedecaps` → `deberopa` |
| 설정 | `~/.config/vedecaps/vedecaps.json` → `~/.config/deberopa/deberopa.json`(D4). 프로젝트 설정 파일 이름도 같은 규칙 |
| 환경 변수 | `VEDECAPS_CONFIG_CONTENT` 등 → `DEBEROPA_*`(D5) |
| 업데이트 | 자체 업데이트(`autoupdate`)의 저장소 주소, 자산 이름, 확인 로직을 새 이름으로 맞춘다. **옛 `vedecaps` 실행 파일이 새 릴리스를 받으려다 깨지지 않는지** 확인한다 |
| 로고 | README, 터미널 배너(있다면), VS Code 확장(`ide/vscode`)의 아이콘을 위 로고로 바꾼다 |
| 라이선스 | MIT 그대로. 저작권 표시의 제품 이름만 바꾼다 |

### 3.2 Opereta 인식(핵심)

Opereta 0.6.x 의 게이트웨이 계약이다. Vedecaps 0.1.5 는 **이전 OS 이름**의 정보 주소와 응답 헤더를 쓴다. 아래 새 값으로 바꾼다.

| 항목 | 새 값 | 비고 |
|---|---|---|
| 정보 주소 | `GET /opereta/info` | 키 없이 읽는다. 버전, 서버 상태, 모델 이름, 요청당 컨텍스트(`ctx_per_slot`), 슬롯 수(`slots`)를 낸다 |
| 응답 헤더 | `X-Opereta: <버전>` | 모든 응답에 붙는다(HTTP 와 HTTPS 모두) |
| 이름 표시 | `Opereta <버전>` | `debug local` 이 서버를 이렇게 표시한다 |
| 슬롯 수 | 정보 주소의 `slots` | `debug local` 이 "N slots" 로 보여 주고, 동시 요청 조절(`max_concurrency`)의 근거로 쓴다 |
| TLS 안내의 명령 | `opereta tls ca`(CA 파일 얻기), `opereta tls status`(주소 확인), `opereta tls renew`(갱신) | 안내 문구에 이 이름을 쓴다 |
| 연결 명령 안내 | `opereta connect deberopa` | 설정 조각을 만드는 Opereta 명령. 아래 3.4 참고 |
| 제공자 ID | `opereta` | 설정의 `provider.opereta` 와 모델 이름 `opereta/<모델>` |
| 키 환경 변수 | `OPERETA_API_KEY` | 설정이 `{env:OPERETA_API_KEY}` 로 가리킨다. 키 접두사는 `opr_` 다(형식을 검증하지 않는다) |

찾는 방법: Vedecaps 저장소에서 OS 를 이름으로 알아보는 코드를 찾는다(`/info` 경로 조회, `x-` 로 시작하는 응답 헤더 비교, TLS 오류 안내 문구의 명령 이름, 제공자 이름 `provider.<id>` 의 기본값). 이전 OS 이름을 직접 쓰는 문자열은 모두 위 새 값으로 바꾸고 **이전 이름은 남기지 않는다**.

### 3.3 동작 계약(바꾸지 않는다)

0.5.4 의 계약 C1~C10(`vedecaps-agent-handoff-0.5.4.md` 2절)은 그대로 유효하다. 특히:

- HTTP→HTTPS 리다이렉트를 따르지 않는다(`redirect: "error"`).
- `options.ca` 는 PEM 경로(또는 배열, 인라인 PEM)이며 신뢰하지 않은 프로젝트의 설정에서는 무시한다.
- 호스트 이름 검증을 끄는 방법을 만들지 않는다.
- 429 + `Retry-After` 를 존중한다.
- 루프백 `http://127.0.0.1:8080` 의 자동 감지는 평문 HTTP 다.
- `local_no_key`: 이 컴퓨터의 요청은 키 없이 통과한다. 키가 있으면 그 키로 집계된다.

### 3.4 Opereta 가 만드는 설정(읽을 수 있어야 한다)

`opereta connect <에이전트> --write --mcp` 가 사용자의 전역 설정에 다음을 **합쳐** 쓴다. 에이전트 이름은 Deberopa 릴리스 뒤에 Opereta 세션이 바꾼다. 형식은 지금과 같다.

```jsonc
{
  "autoupdate": false,                       // 패키지로 설치된 경우에만
  "model": "opereta/<모델>",                 // 기본 모델이 없을 때만
  "provider": {
    "opereta": {
      "api": "http://127.0.0.1:8080/v1",     // 원격이면 https://<이름>:8444/v1 + options.ca
      "name": "Opereta",
      "protocol": "openai",
      "options": { "max_concurrency": 2 },   // VE3, 선택
      "models": { "<모델>": { "limit": { "context": 16384, "output": 4096 }, "name": "<모델>" } }
    }
  },
  "mcp": { "opereta": { "type": "local", "command": ["opereta", "mcp"] } }   // --mcp 일 때
}
```

- Opereta 는 스킬 폴더 링크 `~/.agents/skills/opereta` 도 만든다(에이전트 공통 경로다. 이름을 바꾸지 않는다).
- 파일 형식(주석 없는 JSON)과 키 이름은 **지금과 같게 유지**한다. 바뀌는 것이 있으면 4절의 회신에 적는다.

### 3.5 릴리스 형식(Opereta 의 이미지 빌드가 소비한다)

Opereta 의 빌드는 아래를 **그대로** 기대한다. 어긋나면 이미지 빌드가 멈춘다(의도된 안전 장치다).

| 항목 | 요구 |
|---|---|
| 자산 이름 | `deberopa-linux-x64.tar.gz`(x86-64, glibc) |
| 묶음 내용 | **`deberopa` 하나만**(다른 이름이 있으면 거절한다). 실행 권한 0755 |
| 버전 출력 | `deberopa --version` 이 **버전 문자열만**(예: `0.2.0`) 출력하고 0 으로 끝난다 |
| 체크섬 | 릴리스 자산 `checksums.txt` 에 위 파일의 SHA-256(소문자 16진수 64자) |
| 릴리스 주소 | `https://github.com/<소유자>/<저장소>/releases/download/v<버전>/deberopa-linux-x64.tar.gz` |
| 변경 이력 | `CHANGELOG.md` 의 해당 버전 절에 **"이름 변경"** 과 **"Opereta 인식"** 항목이 있어야 한다 |

저장소 이름이 바뀌면(D2) 이전 주소는 GitHub 가 리다이렉트한다. 릴리스 자산 주소는 새 이름 기준으로 안내한다.

## 4. 시험과 완료 조건

### 4.1 당신의 저장소에서

- `npx tsc --noEmit`, `npx bun test`, `npx bun run script/scan-secrets.ts` 가 통과한다.
- 이름 변경 시험: `vedecaps` 라는 이름이 코드와 문서에 남아 있지 않다(CHANGELOG 의 과거 항목과 이전 설정 폴더를 읽는 이전 코드는 예외로 두고 이유를 적는다).
- Opereta 인식 시험: 가짜 서버가 `GET /opereta/info` 와 `X-Opereta` 를 내면 `debug local` 이 "Opereta <버전>" 과 슬롯 수를 보여 준다. 이전 OS 이름의 정보 주소에는 반응하지 않는다.
- TLS 오류 안내 시험: 안내 문구에 `opereta tls ca`, `opereta tls status`, `opereta tls renew` 가 들어 있다.
- 이전 시험: 옛 설정 폴더만 있을 때 새 실행 파일이 그 설정을 읽는다(D4).

### 4.2 Opereta 의 호환 시험이 기대하는 출력

Opereta 세션이 첫 Deberopa 릴리스를 고정하고 `make test-vedecaps-docker` 를 돌린다. 이 시험은 실제 실행 파일을 Opereta 게이트웨이와 가짜 모델 서버에 붙인다.

| 시나리오 | 명령 | 기대 |
|---|---|---|
| 요청 | `deberopa run -m opereta/fake.gguf "say hi"`(환경에 `OPERETA_API_KEY`) | 모델의 답이 출력된다. 모델 서버 로그에 `GET /props`, `stream=True`, 도구 목록 |
| 키 | 키 없음/틀림/맞음 | 게이트웨이의 `error.message` 가 그대로 보인다("a valid API key is required") |
| https | `options.ca` 있음/없음/다른 CA | 있으면 성공(이름과 IP 모두), 없으면 실패하고 안내에 `tls ca` 와 `opereta tls ca` |
| 인식 | `deberopa debug local` | 출력에 `Opereta <버전>` 과 `2 slots`(가짜 서버의 값) |

## 5. 하지 말 것

- Opereta 저장소의 파일을 바꾸거나 Opereta 의 릴리스를 건드리지 않는다.
- 사용자 확인 없이 GitHub 저장소 이름을 바꾸거나 릴리스를 만들지 않는다.
- 이전 OS 이름을 알아보는 코드를 남기지 않는다(별칭, 호환 분기 모두).
- 호스트 이름 검증을 끄는 옵션을 만들지 않는다.
- 비밀 값(API 키, 토큰)과 개인 정보(개인 이메일, 로컬 경로)를 저장소에 올리지 않는다. 커밋 작성자는 GitHub noreply 주소를 쓴다.

## 6. 회신(작업이 끝나면 사용자에게 보고하고 Opereta 세션이 읽을 수 있게 남긴다)

아래를 한 곳(릴리스 노트 또는 `docs/` 의 짧은 문서)에 적는다.

1. 릴리스 버전, 태그, 커밋, `deberopa-linux-x64.tar.gz` 의 SHA-256(`checksums.txt` 와 같은 값).
2. 결정 D1~D5 의 최종 값(사용자가 정한 것과 기본안으로 진행한 것을 구분).
3. 설정 폴더와 파일 이름, 환경 변수 이름, 이전(migration) 동작.
4. Opereta 인식이 쓰는 정보 주소, 헤더, 안내 명령(3.2 의 표와 다르면 그 차이).
5. 3.4 의 설정 형식에서 바뀐 키가 있으면 그 목록.
6. 알려진 한계와 확인하지 못한 것(실제 GPU 서버 시험 여부 포함).

## 7. 법적으로 확인되지 않은 것(소유자 몫)

- 이름 **Deberopa** 가 기존 상표와 충돌하는지 조사하지 않았다.
- 로고 원본에 AI 생성 도구의 출처 메타데이터가 있었다(Opereta 저장소에서는 제거했다). AI 생성물의 저작권 귀속은 불확실하다.
