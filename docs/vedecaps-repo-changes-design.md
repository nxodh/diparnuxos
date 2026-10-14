# Vedecaps 저장소 변경 설계서 (VE1 ~ VE4)

- 작성: 2026-10-14, Diparnux 0.5.3 개발 세션
- 대상 저장소: <https://github.com/nxodh/vedecaps> `main` `f187450`(Vedecaps 0.1.3)
- 상태: **제안서 겸 구현 지침.** 이 세션은 Vedecaps 저장소를 바꾸지 않았다. 아래 코드는 구현을 쉽게 하려고 쓴 **스케치**이며, 컴파일이나 실행을 해 보지 않았다.
- 근거: 위 커밋의 소스를 읽었다(파일과 함수 이름으로 가리킨다. 줄 번호는 바뀌므로 쓰지 않는다). Diparnux 쪽 사실은 이 저장소의 `phase-5-3` 브랜치 코드다.
- 관련 문서: [vedecaps-research.md](vedecaps-research.md)(조사), [phase-5-3-design.md](phase-5-3-design.md) 4절(제안 요약), [vedecaps.md](vedecaps.md)(사용법)

## 0. 요약

Diparnux 0.5.3 은 Vedecaps 를 **고치지 않고도** 동작한다(실제 Vedecaps 0.1.3 으로 5개 시나리오를 확인했다).
그래도 Vedecaps 쪽을 아래처럼 바꾸면 더 잘 맞는다.

| 번호 | 변경 | 한 줄 이유 | 크기 | 권장 |
|---|---|---|---|---|
| **VE1** | Diparnux 서버 인식, 키 환경 변수, 구성된 제공자의 모델 정보 보강 | 키가 있는 서버에서 감지가 조용히 실패하는 일과 컨텍스트를 잘못 가정하는 일을 없앤다 | 중 | 한다 |
| **VE3** | 제공자별 동시 요청 상한 `options.max_concurrency` | 서버 슬롯 수보다 많은 요청을 보내 429 로 되튀기는 일을 막는다 | 중 | 한다 |
| **VE4** | `vedecaps debug local` 에 도구 호출 시험과 슬롯 정보 | 에이전트가 안 되는 이유를 사용자가 바로 알게 한다 | 소 | 한다 |
| **VE2** | 제공자별 CA 인증서 `options.ca` | 자체 서명 HTTPS 게이트웨이를 믿게 한다 | 중 | Diparnux 가 TLS 를 넣을 때(0.5.4 이후)로 미룬다 |

VE5(자체 업데이트 억제)는 필요 없다. 설정 `autoupdate: false` 가 이미 있다.

**호환 원칙**

1. 새 설정 키를 모르는 Vedecaps 0.1.3 은 그 키를 **무시한다**(런타임이 설정을 JSON 스키마로 검증하지 않는다. 스키마는 편집기용이다). 그러므로 Diparnux 가 새 키를 써도 옛 Vedecaps 는 깨지지 않는다.
2. Diparnux 가 아닌 서버(진짜 llama.cpp, Ollama 등)에서는 동작이 **지금과 같아야** 한다. 새 동작은 Diparnux 표식이 있을 때, 또는 사용자가 새 키를 쓸 때만 켜진다.
3. 새 기능은 기본값이 현재 동작이다(`max_concurrency` 가 없으면 무제한).
4. 키는 새로 보낼 곳을 만들지 않는다. 지금 키가 가는 곳(구성된 제공자, 감지된 로컬 주소)에만 간다.

## 1. 현재 동작(근거)

### 1.1 llama.cpp 자동 감지 — `src/provider/local.ts` 의 `detectLlamaCpp`

- 주소 기본값은 `http://127.0.0.1:8080`(`detectLocalServers`).
- `GET {root}/props` 를 700 ms 안에 읽는다. **인증 헤더를 보내지 않는다.** 응답이 JSON 객체가 아니면 `undefined`(없음)다.
- `GET {root}/v1/models` 로 모델 id 를 읽는다. 비어 있으면 `props.model_alias`/`model_path` 마지막 조각을 쓴다.
- 컨텍스트는 `props.default_generation_settings.n_ctx`, 없으면 `props.n_ctx`. 비전 여부는 `props.modalities.vision`.
- 내부 `getJSON` 은 `!res.ok`(401 포함)이면 이유를 알리지 않고 `undefined` 를 돌려준다. **→ 게이트웨이가 키를 요구하면 서버가 "없는 것"처럼 보인다.**
- `src/cli/commands/debug.ts` 의 `debug local` 은 못 찾으면 "None found. Start one…" 만 안내한다. 401 이라는 사실은 사라진다.

### 1.2 구성된 제공자 — `src/provider/provider.ts`

- 사용자가 `provider.<id>.api` 로 직접 적은 제공자는 `addLocalProviders` 의 첫 단계에서 `discoverOpenAIModels` 로 `GET {api}/models` 만 읽는다(키가 있으면 Bearer 포함).
  이 함수는 `max_model_len`, `context_length`, `context_window`, `max_context_length` 중 하나를 컨텍스트로 쓴다. **llama.cpp 의 `/props` 는 읽지 않는다.**
- 모델에 컨텍스트가 없으면 `toModel` 이 **128,000 으로 가정**한다. 작은 로컬 모델에는 터무니없이 큰 값이라 압축 시점이 틀어진다.
  그래서 지금 `diparnux connect vedecaps` 는 `limit.context` 를 직접 적어 준다.
- 구성된 제공자의 `env` 는 설정 또는 카탈로그의 값이고, **자동 감지된** 제공자는 `env: []` 다. 즉 감지된 서버에는 키를 줄 길이 없다.

### 1.3 요청과 병렬 — `src/session/prompt.ts`, `src/provider/stream.ts`

- 모든 모델 호출은 `streamModel`(`src/provider/stream.ts`)을 지난다. 제목 생성은 `generateText` → `streamModel`.
- 한 턴의 도구 호출은 스트림 도중 `executions.push(execute(...))` 로 바로 시작되고 `Promise.all(executions)` 로 기다린다. `task`(서브에이전트) 호출은 각각 독립된 세션에서 자기 모델 호출을 시작한다.
- 첫 메시지에는 `generateTitle`(작은 모델, `AbortSignal.timeout(30_000)`)이 메인 요청과 **동시에** 나간다.
- 재시도: `ProviderError.retryable` 이고 `attempt < MAX_RETRIES`(6)이고 아직 도구 실행이 시작되지 않았을 때, 지연은 `Retry-After`, 없으면 `2000 * 2^(attempt-1)`, 상한 60 s. 상태는 `retry` 로 표시된다.
- **동시 요청 수를 제한하는 장치가 없다.** 서버가 거부(429)하면 위 재시도로 대응하는 구조다.

### 1.4 Diparnux 쪽 사실(`phase-5-3`)

- 모든 게이트웨이 응답에 `X-Diparnux: <버전>` 헤더가 붙는다(`daemon/internal/gateway/gateway.go`).
- `GET /diparnux/info` 는 키 없이 읽힌다(`daemon/cmd/diparnuxd/main.go`). 응답 예:

```json
{ "diparnux": "0.5.3", "server": "running", "model": "qwen2.5-coder-7b-instruct-q4_k_m.gguf", "ctx_per_slot": 16384, "slots": 2 }
```

  슬롯 수를 사용자가 정하지 않았으면 `slots` 대신 `"slots_auto": true` 가 온다(llama-server 가 자동으로 정하므로 값을 지어내지 않는다).
  서버가 꺼져 있으면 `server` 가 `stopped` 등이고 `model`·`ctx_per_slot` 이 없다. 모델 파일 경로나 설정은 담지 않는다.
- 바쁜 서버는 `429` + `Retry-After: 2` + `{"error":{"code":"too_many_requests","message":...}}` 를 낸다. Vedecaps 의 `error.message` 읽기와 `Retry-After` 존중이 이미 맞는다.
- 이 컴퓨터(루프백, 이 컴퓨터 인터페이스 주소)의 요청은 `local_no_key`(기본 켬)로 키 없이 통과한다. 끄거나 LAN 의 다른 컴퓨터에서 오면 키가 필요하다.
- agent 프로필은 GPU 가 있으면 슬롯을 2 로 고정한다. 프로필을 안 쓰면 llama-server 가 자동으로 4 슬롯(실측)을 만든다.

## 2. 목표와 비목표

**목표**

- G1. 키가 있는 Diparnux 서버에서도 감지·모델 목록·컨텍스트 감지가 된다.
- G2. Diparnux 서버가 알려 주는 슬롯 수와 슬롯당 컨텍스트를 Vedecaps 가 알고, 병렬도를 그에 맞출 수 있다.
- G3. 에이전트가 동작하지 않을 때(401, 도구 호출 안 됨, 컨텍스트 작음) `debug local` 한 번으로 원인이 보인다.
- G4. (VE2) 자체 서명 HTTPS 서버를 안전하게 믿을 수 있다.

**비목표**

- 제공자 이름 `diparnux` 를 Vedecaps 에 새로 내장하는 것. 지금은 설정이 만든다. 내장하면 카탈로그·우선순위(`PRIORITY`)·문서가 한꺼번에 바뀐다. 다만 VE1 로 `llamacpp` 감지가 Diparnux 를 알아보면 충분하다.
- Vedecaps 가 Diparnux 의 MCP 서버를 자동으로 켜는 것. 사용자의 `mcp` 설정(`diparnux connect vedecaps --mcp` 가 쓴다)이 하는 일이다.
- `/slots`, `/metrics` 를 Vedecaps 가 읽는 것. 상태 연계는 Diparnux 의 MCP 도구가 맡는다(`diparnux_server_stats`).
- 인증서 검증을 끄는 옵션. **만들지 않는다**(2.1 참조).

### 2.1 인증서 검증 끄기를 거절하는 이유

"자체 서명이라 안 되니 검증을 끄자"는 편하지만, 키가 평문과 다름없이 새고 중간자가 모델 응답에 도구 호출을 끼워 넣을 수 있다.
코딩 에이전트는 응답으로 **명령을 실행**한다. 그래서 VE2 는 "이 CA 를 믿어라"만 주고, "검증하지 마라"는 주지 않는다.

## 3. VE1 — Diparnux 서버 인식과 키

### 3.1 변경 요약

1. **키가 있는 서버 감지**: 감지 요청에 키가 있으면 `Authorization: Bearer` 를 붙인다. 키는 환경 변수 `DIPARNUX_API_KEY`, 그다음 `LLAMA_API_KEY`(llama-server 자신의 이름)에서 읽는다.
2. **조용한 실패 제거**: 401/403 이면 "서버가 있는데 키가 필요하다"를 기억해 `debug local` 이 알려 준다(감지 결과로는 여전히 "없음").
3. **Diparnux 표식 인식**: 응답 헤더 `X-Diparnux` 가 있거나 `/diparnux/info` 가 `diparnux` 문자열을 주면 Diparnux 서버로 본다. 버전, 슬롯 수, 슬롯당 컨텍스트, 상태를 읽어 둔다.
4. **구성된 제공자 보강**: `api` 로 직접 적은 제공자도 같은 `/diparnux/info` 를 읽어, 모델의 컨텍스트가 비어 있을 때 `ctx_per_slot` 을 쓴다(설정의 `limit.context` 가 항상 이긴다).

### 3.2 `/diparnux/info` 계약(Vedecaps 가 가정할 것)

| 규칙 | 내용 |
|---|---|
| 호출 | `GET {root}/diparnux/info`, 인증 헤더 없음, 타임아웃 700 ms(감지)/1500 ms(debug) |
| 인식 | 상태 200, JSON 객체, `typeof diparnux === "string"` |
| `server` | 문자열. `running`, `running_cpu` 만 "쓸 수 있음". 그 밖(`stopped`, `starting`, `error` 등)은 표시만 한다 |
| `model` | 문자열(없을 수 있음). `/v1/models` 의 id 와 같다 |
| `ctx_per_slot` | 양의 정수(없을 수 있음) |
| `slots` / `slots_auto` | `slots` 는 양의 정수, `slots_auto: true` 면 모른다 |
| 알 수 없는 필드 | 무시(앞으로 필드가 늘 수 있다) |
| 실패 | 404, 타임아웃, JSON 아님 → "Diparnux 아님". **오류로 만들지 않는다**(진짜 llama.cpp 는 이 경로가 없다) |

값 검증: 숫자는 `Number.isInteger` 이고 `1 ≤ n ≤ 1_000_000`(터무니없는 값은 무시), 문자열은 길이 200 이하로 자른다.
서버가 주는 문자열은 화면에 찍기 전에 제어 문자를 제거한다(`debug local` 은 사용자 터미널에 쓴다).

### 3.3 코드 변경 지점

**`src/provider/local.ts`**

```ts
// 새 타입
export interface DiparnuxInfo {
  version: string
  state?: string
  model?: string
  ctxPerSlot?: number
  slots?: number
  slotsAuto?: boolean
}

export interface LocalServer {
  // ... 기존 필드
  /** Diparnux 가 알려 준 값. 진짜 llama.cpp 등에는 없다. */
  diparnux?: DiparnuxInfo
  /** 감지 주소가 401/403 으로 거절됐을 때만 채운다. */
}

export interface DetectFailure {
  id: LocalServer["id"]
  baseURL: string
  status: 401 | 403
}
```

- `getJSON` 을 `probe(url, timeoutMs, init)` 로 일반화한다. 반환은 `{ ok, status, json?, headers? }`. 기존 `getJSON` 은 `probe` 위의 얇은 래퍼로 남겨 호출부(Ollama, LM Studio, vLLM)를 건드리지 않는다.
- 새 함수 `localAPIKey(env = process.env)`: `DIPARNUX_API_KEY`, `LLAMA_API_KEY` 순서로 비어 있지 않은 첫 값. 값은 `registerSecret` 으로 등록해 로그·오류에서 가린다.
- `detectLlamaCpp(baseURL, timeoutMs, key?)`:
  1. `/props` 를 키와 함께 요청한다. 401/403 이면 `DetectFailure` 를 모아 두고 `undefined`.
  2. 성공하면 `/v1/models` 도 같은 헤더로.
  3. `X-Diparnux` 헤더가 있거나 `/diparnux/info` 가 인식되면 `server.diparnux` 를 채운다.
  4. `diparnux.ctxPerSlot` 이 있고 `props` 의 컨텍스트와 다르면 **작은 값**을 `context` 로 쓴다(과대 추정이 압축 시점을 틀리게 하므로 작은 쪽이 안전하다). 두 값은 `debug local` 에 모두 보인다.
  5. 모델 `details` 에 `Diparnux 0.5.3 · 2 slots` 를 넣는다(이미 있는 `details` 표시 칸을 쓴다).
- `detectLocalServers` 는 실패 목록을 돌려줄 길이 필요하다. 반환 형식을 바꾸지 않으려면 모듈 안에 `lastDetectFailures()` 를 두고 `debug` 가 읽게 한다(감지는 한 프로세스에서 한 번이다).

**`src/provider/provider.ts`**

- `addLocalProviders` 의 구성된 제공자 단계에서, `provider.baseURL` 의 루트에 `/diparnux/info` 를 한 번 시도한다(Diparnux 가 아니면 404 로 끝난다. 호스팅 API 에는 보내지 않는다 → 아래 조건).
  - **조건**: `provider.local === true`(`isLocalURL`)일 때만. 호스팅 API(OpenAI 등)에 존재하지 않는 경로를 두드리지 않는다.
  - 결과로 모델의 컨텍스트가 비어 있으면 `ctx_per_slot` 으로 채운다. 이미 값이 있거나 `limit.context` 가 설정되어 있으면 건드리지 않는다.
- 자동 감지된 제공자의 `env` 는 `["DIPARNUX_API_KEY", "LLAMA_API_KEY"]` 로 둔다. 키가 환경에 있으면 `apiKey`/`source: "env"` 가 되어 요청에도 쓰인다(지금은 감지된 제공자가 `source: "local"`, 키 없음).
- 키는 **감지된 주소(기본 127.0.0.1:8080)와 사용자가 적은 주소로만** 간다. 새로운 대상은 없다.

**`src/cli/commands/debug.ts`** — VE4 와 함께 3절 아래 5.2 에서 다룬다.

**`config.schema.json`** — VE1 은 새 설정 키가 없다.

### 3.4 보안 검토

| 위험 | 대응 |
|---|---|
| 키가 엉뚱한 곳으로 새는가 | 보내는 곳은 이미 키가 가던 곳(구성된 제공자, 감지된 루프백 주소)이다. 호스팅 API 에는 `/diparnux/info` 를 보내지 않는다. 감지 요청도 `redirect: "error"` 라 리다이렉트로 다른 호스트에 키가 가지 않는다 |
| `/diparnux/info` 응답이 악의적 | 값 검증(3.2). 숫자 범위 제한, 문자열 길이 제한과 제어 문자 제거. 이 값으로 **실행하는 것은 없다**. 컨텍스트와 표시 문구만 바뀐다 |
| 컨텍스트를 작게 거짓 보고 | 최악이 "자주 압축"이다. 설정의 `limit.context` 로 덮을 수 있다 |
| 키가 `debug` 출력에 찍힘 | `registerSecret` 과 `redact` 를 통과한다(AGENTS 규칙). 출력에는 키가 있다/없다만 쓴다 |

### 3.5 시험(`test/local.test.ts` 에 추가, 모의 서버는 `Bun.serve` 로)

| # | 시나리오 | 기대 |
|---|---|---|
| T1.1 | `/props` 가 키 없이 401, 키 있으면 200 | `DIPARNUX_API_KEY` 가 있으면 감지됨, 없으면 `lastDetectFailures()` 에 401 |
| T1.2 | 키 값이 요청 헤더에 정확히 한 번 | 모의 서버가 받은 `authorization` 이 `Bearer <값>` |
| T1.3 | `X-Diparnux` 헤더 + `/diparnux/info` 정상 | `server.diparnux.version/slots/ctxPerSlot` 채움, `details` 에 슬롯 표시 |
| T1.4 | `/diparnux/info` 404(진짜 llama.cpp) | 지금과 같은 결과(회귀 없음) |
| T1.5 | `/diparnux/info` 가 문자열 `slots`, 음수, 1e12, 제어 문자 | 해당 필드 무시, 표시 안전 |
| T1.6 | `props` n_ctx 32768, `ctx_per_slot` 16384 | `context` 는 16384 |
| T1.7 | 구성된 제공자(`api` 로컬 주소, 컨텍스트 없음) | `ctx_per_slot` 으로 채움 |
| T1.8 | 같은 제공자에 `limit.context` 지정 | 설정 값 유지 |
| T1.9 | 구성된 제공자가 호스팅 주소 | `/diparnux/info` 요청이 **나가지 않음**(모의 DNS 로 확인) |
| T1.10 | 리다이렉트 응답 | 오류 처리, 키가 두 번째 호스트로 안 감 |
| T1.11 | 키 값이 로그·오류·`debug` 출력에 없다 | 비밀 가림 시험(`test/secrets.test.ts` 방식) |

### 3.6 수용 기준

- 키가 켜진 모의 Diparnux 에서 `vedecaps debug local` 이 모델, 슬롯, 컨텍스트를 보여 준다.
- 진짜 llama.cpp 에서 출력이 0.1.3 과 **같다**(스냅숏 비교).
- `npx tsc --noEmit`, `npx bun test`, `npx bun run script/scan-secrets.ts` 통과.

## 4. VE3 — 동시 요청 상한 `options.max_concurrency`

### 4.1 왜 필요한가

- 서버는 슬롯이 정해져 있다(agent 프로필 2, 자동 4). 게이트웨이는 슬롯과 대기열이 모두 차면 429 를 낸다.
- Vedecaps 는 한 턴에 서브에이전트 여러 개와 제목 생성을 동시에 보낸다. 슬롯보다 많으면 429 → 지연 후 재시도(2 s 단위)로 풀리긴 하지만,
  재시도 중에는 상태가 `retry` 로 보이고 지연이 낭비되며, **도구 실행이 이미 시작된 뒤에는 재시도가 아예 안 된다**(`!started` 조건) → 오류로 끝난다.
- 클라이언트가 스스로 상한을 지키면 서버를 429 로 두드리지 않고 순서대로 처리된다.

### 4.2 설정

```jsonc
{
  "provider": {
    "diparnux": {
      "api": "http://127.0.0.1:8080/v1",
      "options": { "max_concurrency": 2 }
    }
  }
}
```

| 규칙 | 내용 |
|---|---|
| 위치 | `provider.<id>.options.max_concurrency`(정수 1~64) |
| 기본 | 없음 = 무제한(지금 동작) |
| 잘못된 값(0, 음수, 소수, 문자열) | 무시하고 경고 로그(`debug` 에 표시). 무제한으로 둔다 |
| 자동값 | VE1 로 Diparnux 가 `slots`(고정) 를 알려 주고 사용자가 `max_concurrency` 를 적지 않았으면 **`slots`** 를 쓴다. `slots_auto` 이면 자동값을 쓰지 않는다(무제한) |
| 우선순위 | 사용자가 적은 값 > 자동값 > 무제한 |

자동값을 쓰는 이유: Diparnux 는 슬롯 수를 아는데 사용자가 두 군데에 같은 숫자를 적게 하는 것은 틀리기 쉽다.
다만 **적은 값이 이긴다**. 작은 값으로 줄이는 것은 항상 안전하고 더 큰 값을 적었다면 사용자 의도다.

### 4.3 구현

**새 파일 `src/provider/limiter.ts`**

```ts
export type Priority = "normal" | "low"

export interface Limiter {
  readonly max: number
  acquire(signal?: AbortSignal, priority?: Priority): Promise<() => void>
  /** 진행 중 수와 기다리는 수(상태 표시와 시험용) */
  stats(): { active: number; waiting: number }
}

export function createLimiter(max: number): Limiter {
  let active = 0
  const normal: Waiter[] = []
  const low: Waiter[] = []
  // acquire: 자리가 있으면 즉시, 없으면 큐에 넣고 signal 로 취소되면 큐에서 뺀다.
  // release: active-- 후 normal 큐를 먼저, 비어 있으면 low 큐의 앞에서 하나를 깨운다. 중복 호출은 무시한다.
  // ...
}

const limiters = new Map<string, Limiter>()
/** 제공자 id + baseURL 로 한 인스턴스를 공유한다(같은 서버에 대한 상한이다). */
export function limiterFor(key: string, max: number): Limiter { /* max 가 바뀌면 새로 만든다 */ }
```

**`src/provider/stream.ts`**

```ts
export function streamModel(input: StreamInput): AsyncGenerator<StreamEvent> {
  const inner = streamModelUnlimited(input)          // 기존 본문을 이름만 바꾼다
  const max = input.provider.maxConcurrency
  if (!max) return inner
  return limited(inner, limiterFor(`${input.provider.id}@${input.provider.baseURL}`, max), input.signal, input.priority)
}

async function* limited(inner: AsyncGenerator<StreamEvent>, limiter: Limiter, signal?: AbortSignal, priority?: Priority) {
  const release = await limiter.acquire(signal, priority)   // 기다리는 동안 abort 되면 AbortError
  try {
    yield* inner
  } finally {
    release()                                              // 정상, 오류, 호출자의 return() 모두에서 반드시 해제
    await inner.return?.(undefined as never).catch(() => {})
  }
}
```

- `StreamInput` 에 `priority?: "normal" | "low"` 를 더하고, `generateText`(제목, 요약)는 `"low"` 로 보낸다. 슬롯이 1 개일 때 제목이 본작업을 앞지르지 않게 한다.
- `ProviderInfo` 에 `maxConcurrency?: number` 를 더하고 `provider.ts` 의 `build()` 가 `options.max_concurrency` 를 검증해 채운다(4.2 규칙).
- Anthropic 프로토콜 제공자에도 같은 래퍼가 적용된다(키가 `streamModel` 에 있으므로 자연스럽다). 문서에는 "모든 제공자"라고 쓴다.

### 4.4 교착과 공정성 검토

- **교착 없음**: 슬롯을 쥔 요청은 서브에이전트의 결과를 기다리지 않는다. 부모의 스트림은 도구 실행과 독립적으로 끝난다(도구 실행은 스트림 도중 시작되지만 스트림은 도구 결과를 기다리지 않는다). 부모가 `finish` 를 받으면 슬롯을 놓고, 그 뒤 자식 요청이 들어간다.
  - 확인 시험: `max_concurrency: 1` 에서 부모가 `task` 를 2 개 호출하는 시나리오가 끝까지 간다(T3.4).
- **취소**: 기다리는 동안 사용자가 중단하면 즉시 `AbortError`, 큐에서 빠진다(자리를 차지한 채 남지 않는다).
- **누수 방지**: 소비자가 제너레이터를 중간에 닫아도(`for await` 를 `break`) `finally` 로 해제된다. 해제는 한 번만 일어난다.
- **재시도와의 상호작용**: 429 를 받아 `sleep(delay)` 하는 동안에는 슬롯을 쥐지 않는다(요청이 끝난 상태). 재시도 때 다시 `acquire` 한다.
- **기아**: `normal` 이 계속 들어오면 `low`(제목)는 밀린다. 제목 생성은 30 s 타임아웃이 있고 실패해도 본문 앞 60 자로 대체되므로 허용한다.
- **압축**: 컨텍스트 압축 요청은 `normal` 이다(본작업이 막혀 있으므로).

### 4.5 사용자에게 보이는 것

- 기다림이 길어질 때(예: 1 s 초과) 상태 줄에 "waiting for a free model slot (2 busy)" 를 낸다. 구현은 기존 `setStatus` 의 `retry` 형식을 쓰지 말고 `busy` 에 사유 문자열을 더하는 방식이 가장 작다(TUI 변경은 선택).
- `vedecaps debug` 의 "Providers with credentials" 줄에 `max 2` 를 덧붙인다.

### 4.6 시험(`test/local.test.ts` 또는 새 `test/limiter.test.ts`)

| # | 시나리오 | 기대 |
|---|---|---|
| T3.1 | `max=2`, 요청 5 개 동시 | 모의 서버가 본 **동시 진행 최대치 2** |
| T3.2 | 대기 중 abort | 큐에서 빠지고 다음 요청이 진행. `active` 가 새지 않음 |
| T3.3 | 스트림 도중 소비자가 닫음, 오류로 종료 | 슬롯 해제됨(`stats().active === 0`) |
| T3.4 | `max=1`, 부모가 `task` 2 개 | 끝까지 진행(교착 없음) |
| T3.5 | `priority: low` 와 `normal` 이 대기 | `normal` 이 먼저 |
| T3.6 | 잘못된 값 `0`, `-1`, `1.5`, `"2"` | 무제한으로 동작, 경고 |
| T3.7 | 자동값: 모의 `/diparnux/info slots: 3`, 설정 없음 | 상한 3 |
| T3.8 | 자동값 vs 설정 `max_concurrency: 1` | 1 |
| T3.9 | `slots_auto: true` | 무제한 |
| T3.10 | 429 + `Retry-After` 재시도 | 대기 중 슬롯 미점유, 재시도가 정상 |

### 4.7 수용 기준

- `max=2` 에서 서브에이전트 4 개를 쓰는 모의 세션이 429 를 **한 번도 받지 않고** 끝난다.
- 설정이 없으면 기존 시험 292 개가 그대로 통과한다.

## 5. VE4 — `debug local` 의 도구 호출 시험과 슬롯 정보

### 5.1 변경 요약

- 서버 줄에 Diparnux 표식과 슬롯, 슬롯당 컨텍스트, 서버 상태를 보여 준다(VE1 의 값).
- 새 옵션 `--check-tools`: 모델 호출을 **한 번** 보내 도구 호출이 되는지 본다. 기본은 꺼 둔다(생성을 일으키므로 사용자가 명시할 때만).
- 401/403 으로 거절된 서버가 있으면 그 사실과 조치를 안내한다(1.1 의 "조용한 실패" 해소).

### 5.2 출력 예

```
Probing local model servers (Ollama, LM Studio, llama.cpp, vLLM)...

llama.cpp  http://127.0.0.1:8080/v1  (openai protocol)
  Diparnux 0.5.3 · server running · 2 slots · 16,384 ctx per slot
  qwen2.5-coder-7b-instruct-q4_k_m.gguf   16,384 ctx   tools
  tool call check: ok (structured tool_calls, 1.8 s)

Use one with:  vedecaps -m llamacpp/qwen2.5-coder-7b-instruct-q4_k_m.gguf
```

키가 필요한 경우:

```
llama.cpp  http://127.0.0.1:8080  answered 401: this server needs an API key.
  Set DIPARNUX_API_KEY (or LLAMA_API_KEY) and run this again.
```

경고 규칙:

| 조건 | 문구 |
|---|---|
| `slots` 가 있고 컨텍스트/슬롯 < 8192 | `! 16K 미만` 계열의 기존 경고를 슬롯 기준으로 표시(요청당 컨텍스트가 작다) |
| `slots_auto` | `slots: chosen by the server; set max_concurrency if you see 429` |
| `server` 가 `running` 이 아님 | `! the server is <state>; start it with: diparnux serve start` |
| 도구 호출 시험 실패 | 5.4 의 판정표 |

### 5.3 도구 호출 시험 요청(Diparnux 의 `serve toolcheck` 와 같은 모양)

Diparnux `daemon/internal/api/toolcheck.go` 의 요청과 **같은 본문**을 쓰면 두 쪽의 결과가 서로 설명된다.

```json
{
  "model": "<감지한 모델 id>",
  "messages": [{ "role": "user", "content": "What time is it in Seoul right now? Use the get_time tool." }],
  "tools": [{ "type": "function", "function": { "name": "get_time", "description": "Get the current time in a city.",
    "parameters": { "type": "object", "properties": { "city": { "type": "string", "description": "City name" } }, "required": ["city"] } } }],
  "tool_choice": "auto", "max_tokens": 128, "temperature": 0, "stream": false
}
```

### 5.4 판정 — Diparnux 의 시험과 **다른 점**

Diparnux `toolcheck` 는 구조화된 `tool_calls` 만 인정한다. 그러나 **Vedecaps 는 텍스트로 쓴 도구 호출도 복구해서 쓴다**(`src/provider/compat.ts`: Hermes/Qwen `<tool_call>`, Mistral, Llama, JSON 코드 블록).
그래서 Vedecaps 쪽 시험은 세 단계로 판정한다.

| 결과 | 조건 | 의미 |
|---|---|---|
| `ok` | 응답에 `tool_calls` 가 있고 이름이 `get_time`, 인수가 JSON 객체 | 서버가 구조화된 도구 호출을 낸다 |
| `recovered` | `tool_calls` 는 없으나 `parseTextToolCalls`(compat) 가 호출을 복구함 | **Vedecaps 에서는 동작한다.** 다만 Diparnux 의 `serve toolcheck` 는 실패로 본다(차이를 설명한다) |
| `failed` | 둘 다 아님(글로만 답함, 잘못된 도구, 깨진 인수) | 에이전트가 파일을 못 읽고 고친다. 모델의 채팅 틀에 도구 지원이 없다 |

시험은 `streamModel` 이 아니라 **래퍼를 거치지 않은 원 어댑터**로 한 번만 보낸다(복구 층이 결과를 가리지 않게). 비스트리밍 요청이므로 `openai.ts` 의 `buildBody` 를 그대로 쓰지 않고 시험 전용 `fetch` 를 쓴다(헤더·키·`redirect: "error"` 규칙은 같다).
Ollama 프로토콜 서버는 이 단계를 건너뛰고 "native API: not checked" 로 표시한다(도구 처리 경로가 다르다).

### 5.5 시험

| # | 시나리오 | 기대 |
|---|---|---|
| T4.1 | 모의 서버가 `tool_calls` 반환 | `ok` |
| T4.2 | 텍스트 `<tool_call>{...}</tool_call>` 반환 | `recovered` |
| T4.3 | 글로만 반환 | `failed` + 조치 문구 |
| T4.4 | 401 | 키 안내 |
| T4.5 | `--check-tools` 없이 | 모델 호출이 **0 번**(모의 서버가 `/chat/completions` 를 받지 않음) |
| T4.6 | `slots_auto: true` | 해당 문구, 숫자를 지어내지 않음 |
| T4.7 | 진짜 llama.cpp(표식 없음) | 슬롯 줄 없음, 기존 출력과 동일 |

## 6. VE2 — 제공자별 CA 인증서(`options.ca`) — Diparnux TLS 와 함께

**Diparnux 는 0.5.3 에서 TLS 를 넣지 않았다(0.5.4 이후)**. 그때까지는 VPN(WireGuard) 안내만 있다. 이 절은 그때 바로 쓸 수 있게 미리 정해 둔다.

### 6.1 설정

```jsonc
"provider": { "diparnux": { "api": "https://diparnux.local:8443/v1",
  "options": { "ca": "~/.config/vedecaps/diparnux-ca.pem" } } }
```

| 규칙 | 내용 |
|---|---|
| 값 | PEM 파일 경로 문자열 또는 문자열 배열. 상대 경로는 **설정 파일이 있는 폴더 기준**. `~` 는 홈. 인라인 PEM 문자열(`-----BEGIN CERTIFICATE-----`로 시작)도 허용 |
| 적용 대상 | 그 제공자로 가는 **모든** `fetch`: 스트리밍 요청(`openai.ts`, `ollama.ts`), 목록(`discoverOpenAIModels`), 감지(`local.ts`), VE1 의 `/diparnux/info`, VE4 의 시험 |
| 구현 | Bun 의 `fetch(url, { tls: { ca } })`. 기본 신뢰 저장소에 **더해** 주는지 대체하는지 Bun 동작을 시험으로 확인한다(대체라면 사용자 CA 만 믿게 되어 더 안전하다. 어느 쪽이든 문서에 쓴다) |
| 파일이 없거나 PEM 이 아님 | 그 제공자를 **사용 불가**로 두고 이유를 알린다. 조용히 기본 저장소로 폴백하지 않는다(보안 의도가 사라지므로) |
| 검증 끄기 | 만들지 않는다(2.1) |
| 지문 고정 `ca_sha256` | 이번 범위 밖. 필요하면 후속 |

### 6.2 신뢰(`src/config/trust.ts`)

`options.ca` 는 **어떤 서버를 믿을지**를 정하므로 `stripUntrusted` 의 제공자 항목(`api`, `env`, `baseURL`, `headers`)과 같은 부류다.
신뢰하지 않은 프로젝트 설정의 `provider.*.options.ca` 는 제거하고 `blocked` 에 `provider.<id>.ca (decides which servers are trusted)` 를 남긴다.
AGENTS.md 의 "새 설정 키 중 실행이나 요청 경로에 영향을 주는 것은 `stripUntrusted` 에 넣는다" 규칙을 따른다.

### 6.3 시험과 시험용 인증서

- 모의 서버를 `Bun.serve({ tls: { cert, key } })` 로 띄우고, CA 없음(실패), 올바른 CA(성공), 틀린 CA(실패), 만료/호스트명 불일치(실패)를 본다.
- 시험용 개인 키를 저장소에 두면 비밀 검사(`script/scan-secrets.ts`)에 걸린다. 선택지:
  (a) 시험 시작 때 `node:crypto` 로 키를 만들고 인증서는 `selfsigned` 계열 의존성 없이 직접 만들기 어렵다 → 비현실적,
  (b) **시험 전용 인증서·키를 `test/fixtures/tls/` 에 두고 비밀 검사 허용 목록에 그 경로만 추가**(공개 저장소에 올려도 의미 없는 만료 없는 시험용 키임을 파일 머리에 명시).
  권장은 (b). 이 결정은 Vedecaps 저장소 소유자가 한다.

## 7. 공통 사항

### 7.1 문서·스키마·변경 기록

| 파일 | 변경 |
|---|---|
| `config.schema.json` | `provider.*.options` 에 `max_concurrency`(integer, 1~64), (VE2 때) `ca`(string 또는 string 배열). `additionalProperties: false` 이므로 **키를 추가하지 않으면 편집기가 경고**한다 |
| `README.md`, `README.en.md` | 로컬 모델 절에 "키가 있는 서버", "동시 요청 상한", `debug local --check-tools` |
| `CHANGELOG.md` | Unreleased 에 항목(VE1, VE3, VE4 각각) |
| `docs/development-status.md` | 구현·검증 범위와 **검증하지 못한 것**(실제 Diparnux 서버 대상 시험은 Diparnux 쪽 CI) |
| `src/config/trust.ts` | VE2 만 해당(6.2). VE1/VE3/VE4 는 신뢰 규칙 변경 없음. 단 `max_concurrency` 는 요청 경로를 바꾸지 않으므로 stripUntrusted 대상이 아니다 |

### 7.2 코드 규칙 점검표(AGENTS.md)

- 2 칸 들여쓰기, 세미콜론 없음, 큰따옴표, 확장자 없는 상대 import, 타입은 `import type`.
- 제품 코드에서 POSIX 도구 호출 금지, 경로는 `path`, 시험에 `/tmp` 하드코딩 금지.
- UI 는 단색(굵게, 흐리게, 반전). 폭 계산은 `string-width`.
- 키·토큰·개인 이메일·절대 경로를 올리지 않는다. 시험 값은 `sk-test-not-a-real-key` 같은 가짜.
- 시스템 프롬프트·도구 목록은 결정적이어야 한다(프롬프트 캐시). **이 변경은 둘 다 건드리지 않는다**(VE4 의 시험 요청은 별도 호출이다).
- 로컬 모델 동작 변경은 모의 서버만이 아니라 실제 서버로도 확인한다(`docker run` 의 llama.cpp 또는 Diparnux).

### 7.3 PR 분할과 순서

| PR | 내용 | 순서 | 이유 |
|---|---|---|---|
| A | `probe`/`localAPIKey` 리팩터 + VE1 + VE4 | 1 | VE4 가 VE1 의 값을 보여 준다. 같은 파일(`local.ts`, `debug.ts`)을 건드린다 |
| B | VE3 `limiter` + `streamModel` 래퍼 + 자동값(A 의 슬롯 정보 사용) | 2 | A 가 있어야 자동값이 의미가 있다. 독립적으로도 `max_concurrency` 수동 값은 동작한다 |
| C | VE2 `ca` + 신뢰 규칙 + 시험용 인증서 | Diparnux TLS 와 같은 시기 | Diparnux 쪽 TLS 가 없으면 쓸 곳이 없다 |

A + B(VE1, VE3, VE4)를 **0.1.4**, C(VE2)가 포함된 릴리스를 **0.1.5** 로 한다(사용자 결정 2026-10-14). 각 PR 은 `npx tsc --noEmit`, `npx bun test`, `scan-secrets` 를 통과해야 한다.

### 7.4 Diparnux 쪽 후속 작업(Vedecaps 릴리스 뒤)

| # | 작업 | 위치 |
|---|---|---|
| D1 | `VEDECAPS_VERSION` 과 `VEDECAPS_SHA256` 을 새 릴리스의 `checksums.txt` 값으로 갱신 | `diparnux.conf` |
| D2 | `diparnux connect vedecaps` 가 `options.max_concurrency` 를 슬롯 수(고정일 때)로 쓰도록 한다. **설치된 Vedecaps 가 새 키를 모를 때도 무해**하므로 버전 조건 없이 써도 된다. 다만 JSON 스키마 시험이 새 키를 알도록 스키마 사본을 갱신 | `daemon/internal/vedecaps` |
| D3 | `make test-vedecaps-docker` 시나리오 추가: 키 필요 서버 감지, 표식·슬롯 인식, 동시 요청 최대치, `--check-tools` 판정 | `tests/vedecaps/`, `daemon/internal/gateway/harness` |
| D4 | `docs/vedecaps.md` 의 7절(문제 해결)과 4절에서 "Vedecaps 쪽 변경 필요" 문구 정리, `docs/phase-5-3-design.md` 4절 상태 갱신 | 문서 |
| D5 | 호환 시험에서 Vedecaps 가 새 키를 쓰는지 `vedecaps debug` 로 확인하는 단계를 `check_vedecaps`(E2E)에 추가 | `tests/qemu` |

### 7.5 롤백

- 각 변경은 기본값이 현재 동작이므로, 문제가 생기면 사용자가 새 설정을 지우면 된다.
- VE1 의 Diparnux 인식은 환경 변수나 설정으로 끌 수 있게 한다(`VEDECAPS_DISABLE_LOCAL_DETECT` 가 이미 감지를 통째로 끈다. 별도 스위치는 만들지 않는다).
- 릴리스에서 문제가 나오면 Diparnux 는 `diparnux.conf` 의 고정 버전을 이전 값으로 되돌리면 된다(이미지에 고정되어 있어 사용자 컴퓨터가 자동으로 바뀌지 않는다).

## 8. 위험과 한계

| 위험 | 내용 | 완화 |
|---|---|---|
| 실제 서버 미검증 | 이 설계는 소스 읽기와 Diparnux 쪽 시험에 근거한다. 실제 GPU 에서 코딩 모델로 병렬 작업을 돌려 본 적이 없다 | `max_concurrency` 기본이 무제한이라 해가 없고, 효과 측정은 실기 검증 항목으로 남긴다 |
| 슬롯과 컨텍스트 해석 | llama-server 의 `n_ctx` 가 슬롯당인지 전체인지는 빌드와 옵션에 따라 다르다 | Diparnux 가 `ctx_per_slot` 을 따로 알려 주고, 두 값이 다르면 작은 값을 쓴다 |
| 제목 생성의 우선순위 | 슬롯 1 개에서 제목이 늦어질 수 있다 | `low` 우선순위, 실패 시 본문 앞 60 자 대체가 이미 있다 |
| Bun `tls` 옵션의 플랫폼 차이 | Windows 에서 CA 처리가 다를 수 있다 | VE2 시험을 Windows·Ubuntu·Debian·Alpine CI 에서 모두 돌린다 |
| Vedecaps 0.1.x 의 빠른 변화 | 하루 사이 0.1.0 → 0.1.3 | Diparnux 는 고정 버전을 쓰고 `make test-vedecaps-docker` 로 짝을 확인한다 |
| 스키마 | `additionalProperties: false` 라 새 키가 스키마에 없으면 편집기 경고 | 7.1 에서 스키마를 같이 바꾼다 |

## 9. 열린 질문(Vedecaps 저장소 소유자 결정)

1. 구현 주체: 사용자가 직접 / 이 세션이 `nxodh/vedecaps` 에 브랜치와 PR 을 만든다(병합은 하지 않는다) / 하지 않는다. (설계서의 Q6)
2. `max_concurrency` 의 이름: `max_concurrency` 로 제안했다. 다른 이름을 원하면 정한다.
3. 감지된 llama.cpp 에서 슬롯 자동값을 **기본으로** 쓸지(제안), 사용자가 켜야 쓸지.
4. `debug local --check-tools` 를 기본 끄고 옵션으로 둘지(제안), 항상 실행할지.
5. VE2 의 시험용 인증서 처리(6.3 의 (b) 제안).
6. 버전: **결정함** — VE1·VE3·VE4 는 0.1.4, VE2 포함 릴리스는 0.1.5.

## 10. 작업 목록(구현자용 체크리스트)

**PR A (VE1 + VE4)**
- [ ] `local.ts`: `probe` 도입, `getJSON` 래퍼 유지
- [ ] `local.ts`: `localAPIKey`, `detectLlamaCpp` 키·표식·실패 기록
- [ ] `local.ts`: `DiparnuxInfo` 파싱과 값 검증
- [ ] `provider.ts`: 감지 제공자 `env`, 구성된 제공자(로컬만) 컨텍스트 보강
- [ ] `debug.ts`: 서버 줄, 401 안내, `--check-tools`, 판정 3단계
- [ ] 시험 T1.1~T1.11, T4.1~T4.7
- [ ] README, CHANGELOG, development-status

**PR B (VE3)**
- [ ] `limiter.ts` 와 단위 시험
- [ ] `stream.ts` 래퍼, `StreamInput.priority`, `generateText` 의 `low`
- [ ] `provider.ts`: `options.max_concurrency` 검증, 자동값
- [ ] 스키마, `debug` 표시
- [ ] 시험 T3.1~T3.10

**PR C (VE2, 나중)**
- [ ] `options.ca` 읽기, 모든 `fetch` 에 적용
- [ ] `trust.ts` 와 시험
- [ ] 시험용 인증서 처리 결정과 반영

## 11. 이 문서의 검증 범위

- 소스는 `f187450` 을 읽어 확인했다. 함수·파일 이름은 그 커밋 기준이다.
- 제안 코드는 스케치이며 컴파일, 실행하지 않았다.
- `/diparnux/info` 응답 모양은 Diparnux `phase-5-3` 코드(`daemon/cmd/diparnuxd/main.go`)에서 확인했다.
- Bun 의 `fetch` `tls` 옵션 동작과 Windows 동작은 확인하지 않았다(VE2 구현 때 확인한다).
