# Vedecaps 개발 에이전트에게 넘기는 문서 (Diparnux 0.5.4 TLS 연동)

- 작성: 2026-10-14, Diparnux 쪽 세션
- 받는 쪽: `nxodh/vedecaps` 저장소에서 [vedecaps-repo-changes-design.md](vedecaps-repo-changes-design.md) 대로 다음 버전을 개발하는 에이전트
- 목적: Diparnux 0.5.4 가 게이트웨이에 **TLS(HTTPS)** 를 넣는다. 그때 Vedecaps 가 그 서버에 안전하게 접속하도록 **VE2(`options.ca`)** 를 비롯한
  필요한 변경을 **Diparnux 쪽과 같은 계약**으로 만들게 하는 것이다.
- 이 문서는 Diparnux 저장소(`nxodh/diparnuxos`)의 `docs/` 에 있다. **Vedecaps 저장소에 복사해 넣지 않아도 된다**(읽고 따르면 된다).
  Diparnux 저장소의 파일을 바꾸지 않는다. Diparnux 쪽은 Diparnux 세션이 맡는다.

## 버전 결정(사용자, 2026-10-14)

**VE2(`options.ca`)가 포함된 Vedecaps 릴리스의 버전은 `0.1.5` 로 한다.** Diparnux 0.5.4 는 이 `0.1.5` 를 이미지에 고정한다
(`diparnux.conf` 의 `VEDECAPS_VERSION="0.1.5"` 와 그 릴리스의 SHA-256).

| 릴리스 | 내용 | 번호 |
|---|---|---|
| 먼저 | VE1(Diparnux 인식, 키 환경 변수), VE3(`max_concurrency`), VE4(`debug local --check-tools`) | **0.1.4** (아직 릴리스하지 않았다면 이 번호로 낸다. 이미 나갔다면 그대로 둔다) |
| 그다음 | VE2(`options.ca`, 신뢰 규칙, TLS 오류 안내, 선택: CA 지문 표시)가 포함된 릴리스 | **0.1.5** |

- 이미 나간 번호를 다시 쓰거나 바꾸지 않는다.
- VE2 를 0.1.4 와 한 번에 내기로 바꾸면 사용자가 정한 번호(0.1.5)와 어긋난다. 그런 변경은 **사용자에게 먼저 확인**하고, 확인 전에는 위 표대로 두 번에 나눈다.
- 0.1.5 에서 번호를 올릴 때는 함께 바뀌는 곳을 한 번에 맞춘다: `package.json`, 소스의 `VERSION`(`src/global`), `CHANGELOG.md`(0.1.5 절), 번호를 쓰는 SDK 와 `ide/vscode`, 문서의 예시 번호.
- `CHANGELOG.md` 의 0.1.5 절에 VE2 항목이 반드시 있어야 한다(Diparnux 가 이 항목으로 고정 버전을 확인한다).

## 0. 먼저 확인할 것(작업 전에)

이 문서는 당신의 현재 진행 상태를 모른다. 아래를 먼저 확인하고 이후 절의 어느 부분이 남았는지 정한다.

1. `main` 의 최신 커밋과 `CHANGELOG.md` 의 Unreleased 항목: 설계서의 **PR A**(VE1 + VE4: Diparnux 인식, 키 환경 변수, `debug local --check-tools`)와
   **PR B**(VE3: `options.max_concurrency`)가 이미 들어갔는가, 그리고 **0.1.4 가 이미 릴리스되었는가**(위 "버전 결정").
2. 이 문서의 3절(VE2)은 아직 구현되지 않았다고 가정한다. 이미 있다면 4절의 계약과 어긋나는 곳만 맞춘다.
3. `npx tsc --noEmit`, `npx bun test`, `npx bun run script/scan-secrets.ts` 가 지금 통과하는지.

## 1. 읽을 순서

| 순서 | 문서(Diparnux 저장소 `docs/`) | 무엇을 얻는가 |
|---|---|---|
| 1 | `vedecaps-repo-changes-design.md` | VE1~VE4 상세(현재 코드 근거, 변경 지점, 시험). 이미 따르는 중인 문서 |
| 2 | `phase-5-4-design.md` | 0.5.4 의 TLS 설계(리스너, 인증서 모델, 위협 모델). 특히 1.1, 4.1, 4.2, 5절 |
| 3 | `vedecaps.md` | 지금 Diparnux 와 Vedecaps 가 어떻게 연결되는지(사용자 관점) |
| 4 | `vedecaps-research.md` | 이전 조사. F1~F13 사실 목록 |

## 2. Diparnux 가 보장하는 TLS 계약

**이 절이 두 쪽의 약속이다.** 바뀌어야 하면 Diparnux 세션에 알린다(8절). 아래는 `phase-5-4-design.md` 의 **권장안**이며 사용자의 결정(Q1~Q9) 전에는 확정이 아니다.
확정 전 항목에는 "(가정)"을 붙였다. 가정이 바뀌어도 VE2 의 형식은 영향을 받지 않도록 3절을 썼다.

| # | 계약 | 이유 / Vedecaps 가 알아야 할 것 |
|---|---|---|
| C1 | **HTTP→HTTPS 리다이렉트를 하지 않는다.** | Vedecaps 는 리다이렉트를 오류로 처리한다(`redirect: "error"`). 이 동작을 **바꾸지 않는다** |
| C2 | **루프백 `http://127.0.0.1:8080` 은 평문 HTTP 로 남는다.** | 자동 감지(`detectLlamaCpp`)가 이 주소만 본다. 자동 감지에 TLS 를 넣을 필요가 없다 |
| C3 | HTTPS 는 **별도 포트**, 기본 **8444**(가정) 이다. 같은 포트에서 평문/TLS 를 섞지 않는다. | 포트는 설정마다 다를 수 있으므로 코드에 8444 를 박지 않는다 |
| C4 | 서버 인증서는 Diparnux 가 만든 **로컬 CA** 로 서명된다(가정: CA + 서버 인증서). 클라이언트는 **CA PEM 파일**을 받는다. | VE2 의 입력은 "CA PEM 파일 경로"이다. 서버 인증서를 직접 받는 안(자체 서명)이 되어도 같은 옵션으로 동작한다(5절) |
| C5 | 서버 인증서의 SAN 은 호스트 이름, `<호스트>.local`, 서버의 모든 IP 주소, `localhost`, `127.0.0.1`, `::1` 이다. | 이름이 맞지 않으면 검증이 실패해야 한다. **호스트 이름 검증을 끄지 않는다** |
| C6 | 키 알고리즘은 ECDSA P-256, TLS 1.2 이상. | Bun/BoringSSL 이 지원한다 |
| C7 | CA 에는 **이름 제약**이 있다(허용 DNS `.local` 과 서버 호스트 이름, 허용 IP 는 사설 대역과 루프백)(가정). | 클라이언트가 이 제약을 지켜도 위 SAN 은 통과해야 한다. Bun 에서 실제로 통과하는지 시험해 알려 달라(8절) |
| C8 | 서버 인증서는 **자동 갱신**된다(397일 수명, 만료 30일 전). CA 는 10년이다. | 클라이언트는 CA 만 신뢰하므로 갱신에 영향이 없다. 단 장기 실행 프로세스가 인증서를 캐시하지 않도록 매 연결마다 검증한다(기본 동작) |
| C9 | `/diparnux/info`, `X-Diparnux`, 키 규칙(`Authorization: Bearer`), 429 + `Retry-After`, 오류 본문 형식은 HTTP/HTTPS 에서 **같다.** | VE1 의 감지·인식 코드를 HTTPS 에서도 그대로 쓴다 |
| C10 | 이 컴퓨터의 LAN 주소로 접속해도 `local_no_key` 가 적용된다(출발지 주소 판정). | 키가 필요 없을 수 있다. 키가 있으면 그 키로 집계한다 |

## 3. 할 일: VE2 — `options.ca`

설계서(`vedecaps-repo-changes-design.md` 6절)를 아래처럼 구체화한다. **아래가 우선**이다.

### 3.1 설정 형식

```jsonc
{
  "provider": {
    "diparnux": {
      "api": "https://diparnux.local:8444/v1",
      "options": { "ca": "~/.config/vedecaps/diparnux-ca.pem" }
    }
  }
}
```

| 규칙 | 내용 |
|---|---|
| 값 | PEM 파일 경로 문자열, 또는 경로 문자열 배열. 인라인 PEM(`-----BEGIN CERTIFICATE-----` 으로 시작)도 허용 |
| 경로 해석 | `~` 는 홈. 상대 경로는 **그 설정 파일이 있는 폴더 기준**(전역 설정이면 전역 설정 폴더). `VEDECAPS_CONFIG_CONTENT` 로 준 설정의 상대 경로는 현재 디렉터리 기준 |
| 읽는 시점 | 제공자를 만들 때(`provider.ts` 의 `build()`). 매 요청마다 다시 읽지 않는다. 설정 다시 읽기(`resetProviders`)에서 다시 읽는다 |
| 파일 없음, 읽기 실패, PEM 이 아님 | 그 제공자를 **사용 불가**로 두고 이유를 사용자에게 알린다(`debug` 와 오류 메시지). **기본 신뢰 저장소로 조용히 폴백하지 않는다** |
| 기본 신뢰 저장소와의 관계 | Bun 의 `tls.ca` 가 기본 저장소를 **대체**하는지 **추가**하는지 시험으로 확인하고 문서에 쓴다. 대체라면 더 안전하다(그 제공자는 그 CA 만 믿는다). 추가라면 사용자가 알도록 문서화 |
| 호스트 이름 검증 | 항상 켠다. `rejectUnauthorized: false`, 검증 끄기, `NODE_TLS_REJECT_UNAUTHORIZED` 의 안내를 **만들지 않는다**(설계서 2.1절). 이미 환경 변수가 영향을 준다면 문서에 위험을 적는다 |
| 환경 변수로 CA 주기 | 이번 범위 밖. 필요하면 후속(예: `VEDECAPS_CA_FILE`) |

### 3.2 적용 대상(그 제공자로 가는 **모든** `fetch`)

| 위치 | 변경 |
|---|---|
| `src/provider/openai.ts` `streamOpenAI` | `fetch(url, { ..., tls })` |
| `src/provider/ollama.ts` | 같음(Ollama 가 HTTPS 뒤에 있을 때) |
| `src/provider/anthropic.ts` | SDK 의 `fetch` 옵션으로 전달 가능한지 확인. 불가하면 문서에 "OpenAI 호환과 Ollama 만 지원" |
| `src/provider/local.ts` `getJSON`, `discoverOpenAIModels`, 감지 함수 | 구성된 제공자의 목록·정보 조회에 같은 `tls`. 자동 감지(루프백 평문)에는 필요 없다 |
| VE1 의 `/diparnux/info` 조회, VE4 의 도구 호출 시험 | 같은 `tls` |

구현 방법은 **제공자별 `tls` 객체를 만들어 `ProviderInfo` 에 두는 것**(`ProviderInfo.tls?: { ca: string[] }`)이 가장 작다. 호출부는 `...(provider.tls ? { tls: provider.tls } : {})` 로 넘긴다.

### 3.3 신뢰(`src/config/trust.ts`) — 필수

`options.ca` 는 **어떤 서버를 믿을지**를 정한다. 신뢰하지 않은 프로젝트의 설정이 이것을 바꾸면, 공격자의 서버를 믿게 만들 수 있다.

- `stripUntrusted` 에서 `provider.*.options.ca` 를 제거하고 `blocked` 에 `provider.<id>.ca (decides which servers are trusted)` 를 남긴다.
- `test/trust.test.ts` 에 시험을 더한다(신뢰하지 않은 프로젝트의 `ca` 가 무시되고, 전역 설정의 `ca` 는 유지).
- AGENTS.md 의 규칙(요청 경로나 키 경로에 영향을 주는 새 키는 `stripUntrusted` 에 넣는다)과 같다.

### 3.4 오류 안내(작지만 사용자에게 중요)

인증서 오류가 나면 지금은 "Could not reach … : <원시 오류>" 로 보인다. TLS 오류를 알아보고 **해결 방법**을 덧붙인다.

| 상황(Bun 의 오류 코드는 시험으로 확인) | 안내 |
|---|---|
| 자체 서명/알 수 없는 CA(`SELF_SIGNED_CERT_IN_CHAIN`, `UNABLE_TO_VERIFY_LEAF_SIGNATURE` 계열) | "The server's certificate is not trusted. Set `provider.<id>.options.ca` to the CA file (for Diparnux: `diparnux tls ca`)." |
| 이름 불일치(`ERR_TLS_CERT_ALTNAME_INVALID`) | "The certificate does not cover `<호스트>`. Use the name or address listed by `diparnux tls status`." |
| 만료(`CERT_HAS_EXPIRED`) | "The server certificate has expired. Renew it on the server (`diparnux tls renew`) and check the clock on both machines." |
| `ca` 파일 문제 | 3.1 의 "사용 불가" 메시지 |

메시지는 `redact` 를 통과시키고, 실패 원인이 TLS 인지 판단하지 못하면 기존 메시지를 그대로 둔다(추측해서 틀린 안내를 주지 않는다).

### 3.5 `debug local` 확장(선택, 권장)

- 구성된 HTTPS 제공자의 서버 정보 줄에 `TLS: trusted by <ca 파일 이름>` 또는 오류 요약을 보여 준다.
- (Q6, 선택) CA 파일의 SHA-256 지문을 표시한다. 사용자가 서버의 `diparnux tls fingerprint` 와 눈으로 비교하게 한다. 형식은 `AA:BB:CC:...` 대문자 콜론 구분
  (Diparnux 가 같은 형식으로 출력한다). Bun 의 `node:crypto` 의 `X509Certificate`(`fingerprint256`)를 쓰면 된다.

### 3.6 스키마·문서

- `config.schema.json`: `provider.*.options` 에 `ca`(string 또는 string 배열, 설명에 "PEM 파일 경로, 상대 경로는 설정 파일 기준"). `additionalProperties: false` 이므로 추가하지 않으면 편집기가 경고한다.
- `README.md`, `README.en.md`: 로컬/원격 서버 절에 HTTPS 와 `options.ca`, 인증서 오류 해결표.
- `CHANGELOG.md`: Unreleased.
- `docs/development-status.md`: 구현·검증 범위. **실제 Diparnux 서버와의 HTTPS 시험은 Diparnux 쪽 CI 가 한다**고 적는다.

## 4. 시험 계획

`test/` 에 추가(모의 서버는 `Bun.serve({ tls: { cert, key } })`).

| # | 시나리오 | 기대 |
|---|---|---|
| TC1 | CA 지정 + 서버 인증서가 그 CA 로 발급, 이름 일치 | 요청 성공, 스트리밍 SSE 가 온전함 |
| TC2 | CA 없음(자체 CA 서버) | 인증서 오류 + 3.4 의 안내, 요청 본문이 서버에 도달하지 않음 |
| TC3 | 다른 CA 지정 | 거부 |
| TC4 | 이름 불일치(SAN 에 없는 호스트로 접속) | 거부 + 이름 안내 |
| TC5 | 만료된 서버 인증서 | 거부 + 만료 안내 |
| TC6 | `ca` 경로가 없음 / PEM 이 아님 | 제공자 사용 불가 + 이유, **기본 저장소로 폴백하지 않음** |
| TC7 | 상대 경로가 설정 파일 폴더 기준 | 다른 작업 디렉터리에서 실행해도 같은 결과 |
| TC8 | 신뢰하지 않은 프로젝트 설정의 `ca` | 제거되고 `blocked` 에 표시. 전역 설정의 `ca` 는 유지 |
| TC9 | `ca` 배열 두 개(옛 CA, 새 CA) | 둘 중 어느 쪽으로 서명되어도 통과(CA 교체 기간) |
| TC10 | 리다이렉트 응답(`301 → https`) | 여전히 오류(동작 변화 없음), 키가 두 번째 요청에 실리지 않음 |
| TC11 | 이름 제약이 있는 CA 로 서명된 서버 인증서(허용 이름) | 통과. 제약 밖 이름의 서버 인증서는 거부(Bun 의 동작을 기록) |
| TC12 | 키(`Authorization`)가 오류 메시지, `debug` 출력, 로그에 없다 | `test/secrets.test.ts` 방식 |
| TC13 | VE1/VE3/VE4 가 HTTPS 제공자에서도 동작(감지 후 `/diparnux/info`, 동시성 상한, 도구 호출 시험) | 같은 결과 |

### 4.1 시험용 인증서

AGENTS.md 는 키를 올리지 못하게 한다. 시험 전용 인증서는 예외가 필요하다. 아래를 권한다(설계서 6.3 의 (b)).

- 오프라인에서 한 번 만든 **시험 전용 CA, 서버 인증서(SAN: `localhost`, `127.0.0.1`, `::1`), 이름 불일치용·만료용 인증서**를 `test/fixtures/tls/` 에 둔다.
  만료되지 않는 시험 인증서가 시간이 지나 시험을 깨뜨리지 않도록 유효기간을 길게(예: 100년) 잡고, 만료용은 이미 지난 기간으로 만든다.
- 각 PEM 파일 맨 위에 주석으로 "TEST ONLY. Not a real key."를 쓴다.
- `script/scan-secrets.ts` 의 허용 목록에 **그 디렉터리 경로만** 추가한다(패턴을 느슨하게 만들지 않는다).
- 새로 만들 때의 명령은 같은 디렉터리의 `README.md` 에 적어 재현 가능하게 한다.

이 결정은 Vedecaps 저장소 소유자의 몫이다. 다른 방법이 있으면 그것을 따라도 된다.

## 5. 계약이 바뀔 때 영향(대비)

| 변경 | VE2 영향 |
|---|---|
| 서버 인증서를 직접 자체 서명(CA 없음)으로 바꾼다 | 영향 없음. 사용자가 서버 인증서 PEM 을 `ca` 에 넣으면 같은 방식으로 신뢰한다. 단 BoringSSL 이 CA 가 아닌 자체 서명 인증서를 신뢰 기준(anchor)으로 받는지 TC 로 확인한다 |
| HTTPS 포트가 8444 가 아니다 | 영향 없음(설정의 `api` 가 포트를 가진다) |
| 이름 제약을 빼고 발급 | 영향 없음 |
| TLS 가 루프백에도 걸린다(Q 에서 기각된 안 C) | 자동 감지가 깨진다. 이 경우 `detectLlamaCpp` 에 https 시도를 더해야 한다. **현재 계약(C2)에서는 필요 없다** |

## 6. 하지 말 것

- **인증서 검증을 끄는 옵션이나 안내를 만들지 않는다**(`insecure`, `rejectUnauthorized`, 환경 변수 우회 포함).
- 리다이렉트를 따라가도록 바꾸지 않는다(C1).
- 자동 감지(`detectLocalServers`)의 기본 주소를 HTTPS 로 바꾸지 않는다(C2).
- 키, 개인 키, 개인 이메일, 절대 경로를 저장소에 올리지 않는다. 시험 인증서는 4.1 의 방식으로만.
- 시스템 프롬프트와 도구 목록을 바꾸지 않는다(프롬프트 캐시, AGENTS.md).
- Diparnux 저장소의 파일을 바꾸지 않는다. 이 문서도 그대로 둔다(수정이 필요하면 8절로 알린다).
- 사용자의 컴퓨터(Mac)에 새 도구를 설치하지 않는다(Docker 를 쓴다). 시험 인증서 생성에 `openssl` 이 필요하면 컨테이너 안에서 만든다.

## 7. 수용 기준

- `npx tsc --noEmit`, `npx bun test`, `npx bun run script/scan-secrets.ts` 통과(기존 시험 포함, 회귀 없음).
- TC1~TC13 통과. 특히 TC6(조용한 폴백 없음), TC8(신뢰 규칙), TC10(리다이렉트 동작 유지).
- Windows, Ubuntu, Debian 13, Alpine CI 에서 TLS 시험이 통과한다(플랫폼별 CA 처리 차이 확인). 하나라도 실패하면 그 플랫폼은 제외하지 말고 원인과 함께 알린다.
- 설정이 `config.schema.json` 을 통과한다.
- 문서(README 한/영, CHANGELOG, development-status)가 갱신되어 있다.

## 8. Diparnux 세션에 알릴 것(릴리스 때)

릴리스가 나오면 다음을 한 번에 전달한다. 이 값으로 Diparnux 가 `diparnux.conf` 의 버전을 고정하고 호환 시험을 돌린다.

```
Vedecaps 릴리스 보고
- 버전: **0.1.5** (VE2 포함 릴리스. 다른 번호이면 이유)
- 릴리스 URL:
- linux-x64 tar.gz 의 SHA-256(checksums.txt 의 값):
- 포함된 변경: VE1 / VE2 / VE3 / VE4 (각각 있음/없음)
- 새 설정 키: options.ca, options.max_concurrency (형식, 기본값)
- Bun 의 tls.ca 동작: 기본 저장소에 더함 / 대체함 (확인한 방법)
- 이름 제약 CA 로 서명된 서버 인증서 통과 여부(TC11):
- 플랫폼별 TLS 시험 결과: Windows / Ubuntu / Debian / Alpine
- 계약(2절)에서 어긋난 것, 바뀌었으면 하는 것:
- 확인하지 못한 것:
```

계약과 어긋나는 점을 발견하면 **구현하기 전에** 이 보고 형식의 "어긋난 것" 칸으로 알린다(Diparnux 쪽이 먼저 바꿀 수도 있다).

**일정 참고**: 사용자는 Diparnux 0.5.4 를 **Vedecaps 0.1.5(VE2 포함)가 나온 뒤에 낸다**고 정했다(TLS 만 먼저 내지 않는다). 그래서 Diparnux 0.5.4 의 완성 시점이 이 릴리스에 달려 있다.
0.1.5 를 낼 수 있는 상태가 되면 위 보고를 바로 보내 달라. 0.1.4(VE1·VE3·VE4)만 먼저 나갔을 때도 그 릴리스의 보고를 따로 보내 달라(Diparnux 는 0.1.5 를 기다리지만, 0.1.4 로 호환 시험을 먼저 해 둘 수 있다).

## 9. 열린 질문과 기본값

| 질문 | 기본값 |
|---|---|
| `options.ca` 의 인라인 PEM 허용 | 허용(경로 우선) |
| 지문 표시(3.5) | 하는 것을 권장(선택) |
| 환경 변수 `VEDECAPS_CA_FILE` | 이번에는 하지 않음 |
| Anthropic 프로토콜 제공자의 `ca` | SDK 에서 가능하면 지원, 아니면 문서에 한계 |
| 버전 번호 | **결정됨**: VE1·VE3·VE4 는 0.1.4, VE2 포함 릴리스는 **0.1.5**(맨 위 "버전 결정") |

## 10. 이 문서의 검증 범위

- Vedecaps 소스 사실(`redirect: "error"`, 자동 감지 주소, `stripUntrusted` 의 제공자 항목)은 `f187450`(0.1.3)을 읽어 확인했다.
- 2절의 계약은 Diparnux 0.5.4 의 **설계안**이며 구현되지 않았다. "(가정)" 항목은 사용자 결정 전이다.
- Bun `fetch` 의 `tls` 옵션 동작, BoringSSL 의 이름 제약과 자체 서명 anchor 처리, Windows·Alpine 의 차이는 **확인하지 않았다.** 이것이 3, 4절 시험이 확인하는 대상이다.
