# Vedecaps 조사 기록 (0.5.3 준비)

- 조사 날짜: 2026-10-14
- 대상: <https://github.com/nxodh/vedecaps> `main` `f187450`(0.1.3, 2026-10-13 16:30 UTC) 전체 소스 읽기
- 방법: 저장소를 임시 폴더에 복제해 README(한/영), AGENTS.md, CHANGELOG, `docs/`, `config.schema.json`, `src/` 주요 모듈(제공자, 설정, 신뢰, 서버, ACP, 플러그인, MCP, 업데이트, 시스템 프롬프트), 설치 스크립트, CI·릴리스 워크플로, SDK, VS Code 확장, 시험 구조를 읽었다.
- **실행해 본 것은 없다.** 아래 "확인함"은 코드와 문서로 확인한 사실이고, 동작은 0.5.3 구현 중 시험으로 확인한다(5절).

## 1. 한 줄 요약

Vedecaps 는 터미널에서 코드를 읽고 고치고 명령을 실행하는 **AI 코딩 에이전트**다(OpenCode 계열). TypeScript 로 쓰고 Bun 으로 단일 실행 파일을 만든다. Linux(glibc·musl, x64·arm64), Windows 11 x64, macOS(x64·arm64)에서 돈다. 라이선스 MIT, 저장소 공개. 모델은 **외부에서 받아 쓴다**(Anthropic, OpenAI 호환 API 전반, Ollama, LM Studio, **llama.cpp**, vLLM). 즉 Opereta 가 만드는 AI 서버(llama.cpp `llama-server` + 게이트웨이)는 Vedecaps 의 모델 쪽 상대이고, Vedecaps 는 Opereta 서버의 대표적인 **클라이언트**다.

| 항목 | 값 |
|---|---|
| 이름·버전 | `vedecaps` 0.1.3(태그 v0.1.0~v0.1.3, 모두 2026-10-13) |
| 언어·런타임 | TypeScript(strict), Bun 1.4 로 컴파일, UI 는 Ink 8(React 19) |
| 크기 | 릴리스 압축 파일 약 31~45 MB(linux-x64 42 MB, windows-x64 45 MB) |
| 시험 | Bun 테스트 292개(34개 파일, assertion 1,825개), Windows·Ubuntu·Debian 13·Alpine CI |
| 의존성 | `@anthropic-ai/sdk`, `diff`, `ignore`, `ink`, `jsonc-parser`, `react`, `string-width` |
| 개발 상태 문서 | `docs/development-status.md`(0.1.2·0.1.3 구현과 검증 범위, 남은 범위) |

## 2. 구조와 기능(확인함)

### 2.1 코드 배치(`AGENTS.md`)

`src/index.ts`·`src/cli/`(명령줄), `src/tui/`(Ink UI), `src/server/`(HTTP API), `src/session/`(에이전트 루프 `prompt.ts`, 저장, 압축, 되돌리기, 공유), `src/provider/`(모델 어댑터), `src/tool/`(도구), `src/permission/`, `src/agent/`, `src/command/`, `src/skill/`, `src/mcp/`, `src/lsp/`, `src/format/`, `src/snapshot/`, `src/update/`, `src/git/`, `src/plugin/`, `src/acp/`, `src/config/trust.ts`, `sdk/{js,python,go}`, `ide/vscode`, `action.yml`.

### 2.2 에이전트 루프와 도구

- 에이전트: `build`(기본, 모든 도구), `plan`(읽기 전용), 서브에이전트 `general`, `explore`. 사용자 정의는 `.vedecaps/agent/*.md`.
- 도구: `bash`, `read`, `edit`, `multiedit`, `write`, `patch`(Begin/End Patch, 원자적), `glob`, `grep`, `list`, `webfetch`, `websearch`(Exa), `todowrite/todoread`, `task`(서브에이전트 위임), `skill`.
- **한 턴에 도구 호출을 병렬로 실행한다**(`Promise.all(executions)`, `src/session/prompt.ts`). 서브에이전트(`task`)도 나란히 돈다. 첫 메시지에서는 제목 생성용 `small_model` 요청(`generateTitle`)이 메인 요청과 동시에 나간다. → 서버 쪽에서는 **동시 요청이 2개 이상** 자연스럽게 생긴다.
- 권한: `allow/ask/deny`, 패턴 와일드카드, `external_directory`, `doom_loop`(같은 호출 3회 반복). `vedecaps run` 은 `ask` 를 거부한다.
- 세션: 디스크 저장, 이어하기, `/undo`(별도 스냅샷 저장소), 자동 압축(`shouldCompact`: 컨텍스트의 1/4 와 출력 한도 중 작은 값을 남기고 압축), 포크, JSON·Markdown 내보내기·가져오기, 읽기 전용 HTML 공유.
- 프롬프트 캐시를 의식한다: 시스템 프롬프트와 도구 목록은 결정적이다(타임스탬프 없음, 날짜만). → llama.cpp 의 prompt cache 와 궁합이 좋다.
- 시스템 프롬프트는 약 4,800 토큰, **작은 모델·작은 컨텍스트용 압축 프롬프트와 핵심 도구 7개(`bash read edit write glob grep list`)는 약 1,600 토큰**(`src/session/system.ts`). 컴팩트 조건: 로컬 서버이고(컨텍스트 ≤ 8,192 이거나 파라미터 ≤ 4B).

### 2.3 모델 제공자와 로컬 서버(Opereta 와 직접 맞닿는 부분)

`src/provider/{provider,local,openai,compat,ollama,anthropic,models}.ts`

- 카탈로그는 models.dev 에서 받아 하루 캐시한다. 목록에 없는 모델 ID 도 그대로 쓴다.
- OpenAI 호환 어댑터(`openai.ts`): `POST {baseURL}/chat/completions`, `stream: true`, `stream_options.include_usage`, `Authorization: Bearer <key>`, SSE 파싱, `delta.reasoning_content`/`reasoning` 분리, `tool_calls` 조각 이어붙이기, `finish_reason` 매핑. 리다이렉트는 **오류로 처리**한다(`redirect: "error"`, 키 유출 방지). 오류 본문에서 `error.message` 를 읽는다. `Retry-After` 를 존중하고 429·5xx·네트워크 오류를 재시도한다(최대 6회, 지연 상한 60초).
- **llama.cpp 자동 감지**(`detectLlamaCpp`): `http://127.0.0.1:8080` 에서 `GET /props`(llama.cpp 서버에만 있다고 가정)와 `GET /v1/models` 를 700 ms 안에 읽는다. 컨텍스트는 `props.default_generation_settings.n_ctx`(없으면 `props.n_ctx`), 비전은 `props.modalities.vision`. 감지되면 제공자 ID `llamacpp` 로 `/models` 에 나타난다. 키 없이 읽는다 → **게이트웨이가 키를 요구하면(`401`) 감지가 조용히 실패한다.**
- 다른 서버는 설정으로 직접 추가한다: `provider.<id>.api`(또는 `options.baseURL`), `options.apiKey`/`env`/`headers`, `models.<id>.limit.context|output`, `tool_call`, `options.compact_prompt` 등. 모델을 적지 않으면 `GET {api}/models`(키가 있으면 Bearer 포함)에서 목록을 가져온다.
- 로컬 판정(`isLocalURL`): 루프백, 사설망(10/8, 172.16/12, 192.168/16, 169.254/16), `.local`, `.localhost`, `host.docker.internal`, ULA/링크 로컬 IPv6. 로컬이면 컴팩트 프롬프트 규칙과 컨텍스트 사용량 보정이 적용된다. → `opereta.local` 이나 LAN 주소는 로컬로 취급된다.
- **텍스트로 쓴 도구 호출 복구**(`compat.ts`): Hermes/Qwen `<tool_call>`, `<function=…>`, Mistral `[TOOL_CALLS]`, Llama `<|python_tag|>`, JSON 코드 블록. `<think>` 태그 분리.
- Ollama 는 네이티브 API 와 `num_ctx` 를 쓴다(11434 로 보이면). 나머지는 OpenAI 호환.
- 컨텍스트 창은 서버가 알려 준 값이 아니면 설정의 `limit.context`, 없으면 128,000 으로 가정한다(`toModel`). llama.cpp 감지 모델은 감지한 `n_ctx` 를 쓴다.

### 2.4 설정과 신뢰(`src/config/{config,trust}.ts`)

- 설정 병합 순서: 전역(`~/.config/vedecaps/` 또는 Windows `%APPDATA%\vedecaps\`) → `VEDECAPS_CONFIG` → 프로젝트 `vedecaps.json(c)`(루트에서 현재 디렉터리까지) → `.vedecaps/` → `VEDECAPS_CONFIG_CONTENT`. `{env:NAME}`, `{file:경로}` 치환.
- **프로젝트 신뢰**: 신뢰하지 않은 프로젝트의 `provider.*.api/env/baseURL/headers`, MCP, 포매터·LSP 명령, 플러그인, `allow` 규칙은 무시된다. **전역 설정과 `VEDECAPS_CONFIG_CONTENT` 는 걸러지지 않는다.** → Opereta 가 제공자 설정을 둘 곳은 *사용자 전역 설정* 또는 환경 변수여야 한다(프로젝트에 두면 무시됨).
- 키 저장: `vedecaps auth login` → 데이터 디렉터리 `auth.json`(소유자만). 로그·내보내기에서 가려진다(`util/redact.ts`).
- 경로 재정의: `VEDECAPS_HOME`(테스트용), `XDG_*`.

### 2.5 확장 지점

| 방법 | 내용 | 비고 |
|---|---|---|
| **플러그인** | ES 모듈 default export 함수 → `tool`(모델용 도구), `tool.execute.before/after`, `chat.system`, `event` 훅. `~/.config/vedecaps/plugin/*.js`, 설정 `plugin`, 신뢰한 프로젝트, 정확한 버전의 npm 패키지 | 컴파일된 실행 파일에서 Node/Bun 없이 동작. 사용자 권한으로 실행됨 |
| **MCP** | 로컬 stdio(`command`, `environment`, `cwd`)와 원격(Streamable HTTP, SSE, `headers`). 도구는 `서버_도구` 로 노출. OAuth 미지원(헤더로 토큰) | 프로토콜 `2025-06-18`, 도구 호출 타임아웃 5분 |
| **스킬** | `SKILL.md` 디렉터리. `.vedecaps/skill`, `.claude/skills`, `.agents/skills`(프로젝트·홈) | 다른 에이전트와 형식 공유 |
| **명령** | `.vedecaps/command/*.md`(`$ARGUMENTS`, `` !`셸` ``, `@파일`) | |
| **지침 파일** | `AGENTS.md`/`CLAUDE.md`/`CONTEXT.md`(프로젝트), `~/.config/vedecaps/AGENTS.md`(전역), 설정 `instructions`(URL 가능) | 세션마다 시스템 프롬프트에 들어감 |
| **HTTP 서버** | `vedecaps serve`(기본 `127.0.0.1:4096`, `VEDECAPS_SERVER_PASSWORD` 로 Basic 인증), 세션·메시지·권한·SSE 이벤트 | 외부 노출 시 암호 필수 |
| **ACP** | `vedecaps acp`(stdin/stdout JSON-RPC, Zed 등) | 프로세스당 프로젝트 하나 |
| **SDK** | JS(`@vedecaps/sdk`), Python 3.10+, Go 1.22+ — 의존성 없음, 서버 실행 도우미 | 원격 연결은 HTTPS 요구, 레지스트리 미게시 |
| **GitHub Action** | `nxodh/vedecaps@v0.1.3`, 입력 `prompt/model/agent/config/commit/pull-request…` | |
| **VS Code 확장** | 터미널에서 열기, `@경로#L10-20` 삽입 | `.vsix` 릴리스 자산 |

### 2.6 설치와 업데이트

- 설치: `install.sh`(Linux/macOS, SHA-256 확인, 기본 `~/.vedecaps/bin`, PATH 프로필 수정), `install.ps1`(Windows). 환경 변수 `VEDECAPS_VERSION`, `VEDECAPS_INSTALL_DIR`, `VEDECAPS_NO_MODIFY_PATH`, `VEDECAPS_RELEASE_URL`(릴리스 호스트 대체).
- 릴리스 자산: `vedecaps-{linux,darwin}-{x64,arm64}[-musl].tar.gz`, `vedecaps-windows-x64.zip`, `checksums.txt`, `vedecaps-vscode-<버전>.vsix`.
- 자체 업데이트 `vedecaps upgrade`: 체크섬 확인, 새 바이너리가 `--version` 으로 같은 버전을 말하는지 확인한 뒤 교체. **실행 파일에 쓰기 권한이 없으면 실패**한다(시스템 경로에 root 소유로 두면 자체 업데이트가 안 된다).
- Alpine 은 `libstdc++`·`libgcc` 가 필요하다. Debian 13 은 별도 요구 없음(CI 에서 깨끗한 `debian:13` 컨테이너에서 스모크 시험).

## 3. 보안 태도(확인함)

신뢰하지 않은 입력(프로젝트 설정, PR 설명, 도구 출력)을 데이터로만 취급, 리다이렉트 거부, 응답 비밀 가리기, 세션 공유에서 도구 출력 제외, 플러그인 설치 스크립트 차단과 잠금 파일, LSP 설치는 동의와 체크섬. `AGENTS.md` 규칙: 저장소는 공개이므로 키·개인 이메일·절대 경로를 올리지 않고 `redact` 를 통한다. 이 태도는 Opereta(`docs/security.md`)와 같은 방향이다.

## 4. Opereta 와 만나는 지점 — 사실 정리

| # | 사실 | 근거 | 영향 |
|---|---|---|---|
| F1 | Vedecaps 는 `127.0.0.1:8080` 의 llama.cpp 를 `/props`+`/v1/models` 로 자동 감지한다 | `local.ts` | Opereta 의 공개 포트(기본 8080)와 같다. **키가 있으면 감지가 실패**한다 |
| F2 | 감지한 컨텍스트는 `default_generation_settings.n_ctx` 다 | `local.ts` | `--parallel` 이면 슬롯당 컨텍스트가 그 값이다 |
| F3 | 컴팩트 프롬프트는 컨텍스트 ≤ 8,192 에서 켜진다 | `system.ts` | Opereta 자동 계획의 컨텍스트 상한이 8,192(`ctxCap`)라 **기본 계획은 항상 컴팩트 모드**에 걸린다 |
| F4 | 도구 호출이 병렬이고 서브에이전트가 있다 | `prompt.ts`, `task.ts` | `parallel ≥ 2` 가 유용하다 |
| F5 | 429 는 `Retry-After` 를 존중해 재시도한다 | `openai.ts`, `types.ts` | 게이트웨이의 429 에 `Retry-After` 가 없으면 지수 대기를 쓴다 |
| F6 | 오류 본문 `error.message` 를 읽는다 | `openai.ts` | 게이트웨이 오류 형식(`{"error":{"code","message","type"}}`)과 호환된다 |
| F7 | 리다이렉트를 오류로 본다 | `openai.ts` | 게이트웨이가 리다이렉트를 내지 않아야 한다(현재 없음) |
| F8 | 스트리밍 SSE, `include_usage` 를 쓴다 | `openai.ts` | 게이트웨이의 토큰 집계(응답 끝 usage)와 맞는다 |
| F9 | `.local`·사설 IP 는 로컬 서버로 취급한다 | `isLocalURL` | `opereta.local` 사용 가능 |
| F10 | 제공자 설정을 프로젝트에 두면 신뢰 전에는 무시된다 | `trust.ts` | 전역 설정/환경 변수로 줘야 한다 |
| F11 | 원격 URL 에 평문 HTTP 를 쓸 수 있고 자체 서명 HTTPS 는 CA 설정이 따로 필요하다 | `openai.ts`(fetch) | 게이트웨이는 지금 평문 HTTP 다. LAN 밖은 VPN(WireGuard) 권장 |
| F12 | 자체 업데이트는 쓰기 가능한 경로가 필요하다 | `update.ts` | 이미지에 넣을 때는 업데이트 경로를 Opereta 가 맡아야 한다 |
| F13 | 모델 서버 `/slots`, `/metrics` 같은 상태는 읽지 않는다 | — | 상태 연계는 Opereta 쪽 도구(플러그인·MCP)로만 가능 |

## 5. 아직 모르는 것 / 구현 중 확인할 것

1. 이 저장소가 빌드한 llama.cpp(`v0.6.0`, 커밋 `d812350`)의 `llama-server` 가 기본 설정에서 **구조화된 `tool_calls`** 를 돌려주는지(Jinja 템플릿 기본값). 모델 템플릿에 따라 다르다.
2. 게이트웨이를 거친 상태에서 Vedecaps `debug local` 이 감지하는지(키 없음, 루프백).
3. `--parallel 2` 에서 Vedecaps 의 서브에이전트 병렬 호출 시 처리량과 슬롯당 컨텍스트가 충분한지.
4. 실제 모델(예: Qwen3-Coder 계열)로 편집·테스트 루프가 도는지(GPU 필요, 실기).
5. Windows 11 의 Vedecaps → Opereta LAN 접속(mDNS `.local` 해석, 방화벽, 키 헤더).

## 6. 출처

- 저장소: <https://github.com/nxodh/vedecaps>, 릴리스 v0.1.0~v0.1.3, 커밋 40개(최신 `f187450`).
- 읽은 파일: `README.md`, `README.en.md`, `AGENTS.md`, `CHANGELOG.md`, `docs/development-status.md`, `docs/oauth-policy.md`, `config.schema.json`, `action.yml`, `install.sh`, `package.json`, `src/provider/*`, `src/config/*`, `src/session/{prompt,system,compaction}.ts`, `src/server/server.ts`, `src/acp/server.ts`, `src/plugin/index.ts`, `src/mcp/client.ts`, `src/skill/skill.ts`, `src/update/update.ts`, `src/cli/commands/debug.ts`, `.github/workflows/ci.yml`.
