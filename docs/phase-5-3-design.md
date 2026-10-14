# 0.5.3 설계 초안: Vedecaps 와의 연결성, 호환성, 시너지

- 작성: 2026-10-14. 상태: **구현됨**(0.5.3, 브랜치 `phase-5-3`). 이 문서는 설계 시점의 제안을 그대로 두고, 결정과 구현 결과는 6절과 [vedecaps.md](vedecaps.md), [phase-5-3-verification.md](phase-5-3-verification.md) 에 있다.
- 근거: [vedecaps-research.md](vedecaps-research.md)(Vedecaps 0.1.3 조사)
- 사용자 지시(2026-10-14): "0.5.3 버전은 Vedecaps 와의 연결성, 호환성, Opereta 와 Vedecaps 를 함께 사용했을 때의 시너지 등이 주요 목표".
- 0.5.2(PR #11)가 끝난 뒤에 착수한다. 0.5.2 의 게이트웨이, 연결 정보, 병렬 슬롯, 서버 현황, 원격 접속 기능이 이 작업의 바탕이다.

## 1. 두 제품의 관계

| | Opereta | Vedecaps |
|---|---|---|
| 역할 | AI **서버** 운영체제: llama.cpp 서버, 게이트웨이, 키, 방화벽, 원격 관리 | AI **코딩 에이전트**: 터미널 UI, 도구, 세션, 에디터 연동 |
| 모델 | 로컬 GGUF 만 | 클라우드 API 와 로컬 서버 모두 |
| 만나는 곳 | OpenAI 호환 `/v1/chat/completions`(게이트웨이, 기본 8080) | OpenAI 호환 클라이언트, llama.cpp 자동 감지 |

Vedecaps 가 Opereta 서버를 **모델 제공자**로 쓰는 것이 기본 연결이다. 사용 모양은 셋이다.

1. **같은 컴퓨터**: Opereta 컴퓨터에서 Vedecaps 를 실행한다(`127.0.0.1:8080`).
2. **LAN/VPN 의 다른 컴퓨터**: Windows 11 이나 Debian 노트북의 Vedecaps 가 `opereta.local:8080` 에 키로 접속한다.
3. **에이전트가 서버를 운영**: Opereta 컴퓨터(SSH 또는 같은 컴퓨터)의 Vedecaps 가 `opereta` 명령으로 모델 서버를 시작·정지하고 자원을 확인한다.

## 2. 목표(제안)

| 번호 | 목표 | 수용 기준 |
|---|---|---|
| V1 | **연결**: 키를 넣은 채로도 Vedecaps 가 Opereta 서버를 바로 찾고 쓴다 | 설정 한 줄 또는 `opereta connect vedecaps` 한 번으로 `vedecaps -m opereta/<모델>` 이 동작 |
| V2 | **호환**: 에이전트 작업에 맞는 서버 설정(컨텍스트, 병렬, 도구 호출)을 Opereta 가 알아서 고른다 | "코딩 에이전트" 프리셋으로 시작한 서버에서 Vedecaps 가 컴팩트 모드 없이 도구 호출 루프를 끝낸다 |
| V3 | **시너지**: Opereta 가 Vedecaps 사용량·상태를 보여 주고, Vedecaps 가 Opereta 를 운영할 수 있다 | 제어 센터에서 키별 사용량과 슬롯 점유가 보이고, 에이전트가 승인 아래 서버를 제어 |
| V4 | **함께 설치**: Opereta 이미지에서 Vedecaps 를 바로 쓸 수 있다 | 새로 설치한 시스템에서 `vedecaps --version` 과 첫 실행이 추가 설치 없이 동작 |
| V5 | **검증**: 두 제품이 함께 도는 시험을 CI 에 둔다 | Vedecaps 실행 파일(고정 버전, 체크섬)이 Opereta E2E 의 게이트웨이를 통해 응답을 받음 |

## 3. 제안 항목

### 3.1 V1 연결

**C1. 루프백 키 면제(기본 켬).** 지금 게이트웨이는 키가 하나라도 있으면 루프백 요청에도 키를 요구한다. 내부 포트(18080)는 이미 로컬 프로세스에 열려 있으므로 이 요구는 보안 이득이 없고, Vedecaps 의 llama.cpp 자동 감지(`/props`, 키 없이 요청)를 막는다. 루프백 출발지는 키 없이 통과시키고 LAN 출발지만 키를 요구한다. 설정 `local_no_key`(기본 true), 끌 수 있다. 사용량 집계에는 "키 없음(로컬)"로 남긴다. *영향: 보안 모델 변경 → 결정 Q1.*

**C2. `opereta connect vedecaps`(CLI)와 `GET /v1/connection` 의 `examples.vedecaps`.** 현재 연결 정보 예시(curl, OpenAI Python, 환경 변수)에 Vedecaps 용을 더한다. 출력은 사용자 전역 설정에 병합할 `provider` 조각이다.

```jsonc
{
  "provider": {
    "opereta": {
      "name": "Opereta",
      "api": "http://opereta.local:8080/v1",
      "options": { "apiKey": "{env:OPERETA_API_KEY}" },   // 키는 환경 변수. 평문을 파일에 쓰지 않는다
      "models": { "<모델 ID>": { "limit": { "context": 32768, "output": 8192 }, "tool_call": true } }
    }
  },
  "model": "opereta/<모델 ID>"
}
```

- `limit.context` 는 서버 계획의 **슬롯당 컨텍스트**(`slot_ctx`)로 채운다(Vedecaps 압축 판단의 기준이다).
- `--write` 는 `~/.config/vedecaps/vedecaps.json` 에 병합한다(기존 항목 보존, `.bak`). 프로젝트 설정에는 쓰지 않는다(신뢰 전에는 무시되기 때문).
- 키는 출력과 파일에 쓰지 않는다. 필요하면 `opereta apikey create vedecaps-<호스트>` 를 안내한다.

**C3. 감지용 공개 정보 `GET /opereta/info`(루프백과 LAN, 키 불필요, 최소 정보).** 게이트웨이가 응답 헤더 `X-Opereta: <버전>` 을 붙이고, 이 경로가 `{version, model, ctx_per_slot, slots, tool_calls: bool}` 만 돌려준다. Vedecaps 쪽에서 "Opereta 서버" 로 라벨링하고 컨텍스트·병렬 값을 신뢰할 수 있게 한다(Vedecaps 변경은 4절). 모델 경로, 설정, 키는 내지 않는다.

**C4. 오류·재시도 호환.** 게이트웨이의 429 와 503 에 `Retry-After` 를 붙이고(현재 동시 요청 제한은 꺼져 있다), llama.cpp 오류 본문은 변형 없이 통과시킨다(컨텍스트 초과 문구가 Vedecaps 의 `OVERFLOW` 정규식에 맞아 자동 압축이 돈다).

**C5. 전송 보안.** 키가 LAN 에서 평문 HTTP 로 오간다. 우선순위: (a) LAN 밖은 WireGuard(0.5.2 구현)만 안내, (b) 선택 기능 "게이트웨이 TLS"(자체 CA, 클라이언트는 `NODE_EXTRA_CA_CERTS` 로 신뢰). Vedecaps 가 CA 파일을 설정으로 받지 않으므로 (b)는 Vedecaps 변경과 함께 결정(Q3).

### 3.2 V2 호환(서버 쪽 최적화)

**H1. 코딩 에이전트 프리셋.** 자동 계획(`autoconf`)의 컨텍스트 상한이 8,192 라 기본 계획은 Vedecaps 의 컴팩트 프롬프트(≤ 8,192)에 걸려 도구와 지침이 줄어든다. 프리셋 `agent`: 슬롯당 컨텍스트 16,384 이상(여유가 있으면 32,768), `parallel` 2(메인 + 서브에이전트/제목 생성), KV 캐시 q8_0 우선, VRAM 이 모자라면 컨텍스트를 줄이는 대신 **계획이 경고**하고 슬롯을 1 로 낮춘다. `opereta serve start --profile agent`, 제어 센터 서버 페이지의 프로필 선택, `config set profile agent|chat`. 계획의 "근거"에 이유를 적는다.

**H2. 도구 호출 확인.** 서버가 구조화된 `tool_calls` 를 내는지 시작 직후 한 번 확인한다(작은 시험 요청, 모델이 도구를 못 쓰면 경고). 지원하지 않는 모델에는 "에이전트용으로 적합하지 않음" 표시. *(llama.cpp `v0.6.0` 빌드의 기본 동작 확인 필요 — 조사 5절의 1번.)*

**H3. 프롬프트 캐시 친화.** Vedecaps 시스템 프롬프트는 결정적이다. 슬롯 선택이 접두사 유사도를 쓰도록 `--slot-prompt-similarity`, 캐시 재사용(`--cache-reuse`) 같은 llama-server 옵션을 점검해 계획에 넣을지 시험으로 정한다(효과를 재고 결정).

**H4. 추천 모델에 "에이전트용" 표시.** 추천 목록(`internal/recommend`)에서 도구 호출과 16K 이상 컨텍스트를 지원하는 모델을 표시한다. 크기는 VRAM 계획과 함께 보인다.

### 3.3 V3 시너지

**S1. 사용량의 출처 표시.** 게이트웨이 키별 통계(요청, 토큰, 마지막 사용)는 0.5.2 에서 구현했다. 키 이름 규칙(`vedecaps-<호스트>`)과 연결 페이지의 안내만 더한다. 도구별 구분은 하지 않는다(요청 본문을 읽지 않는 원칙).

**S2. Opereta MCP 서버(`opereta mcp`).** stdio MCP 서버를 `opereta` CLI 에 더한다. 도구는 읽기 전용 기본: `opereta_status`, `opereta_connection`, `opereta_models`, `opereta_resources`, `opereta_exposure`, `opereta_serve_stats`. 제어 도구(`serve_start/stop`, `services restart`)는 별도 플래그(`--allow-control`)에서만 노출하고, Vedecaps 권한(`ask`)이 사람 승인을 받게 한다. Vedecaps 설정: `"mcp": { "opereta": { "type": "local", "command": ["opereta", "mcp"] } }`. 새 상주 프로세스 없음(요청 때 실행).

**S3. 스킬과 전역 지침 배포.** `/usr/share/opereta/skills/opereta/SKILL.md`(명령 목록, 안전 수칙, 확인 순서)를 만들고 새 사용자 홈의 `~/.agents/skills/` 에 연결(`/etc/skel`). `~/.config/vedecaps/AGENTS.md` 는 환경 요약(이 컴퓨터는 Opereta, 서버 포트, 명령 위치)을 담는다. MCP 없이도 에이전트가 `opereta` CLI 를 `bash` 도구로 올바르게 쓴다.

**S4. 제어 센터 "Vedecaps" 페이지.** 설치 여부와 버전, 연결 설정 조각(복사), "설정 쓰기", "터미널에서 열기"(`xfce4-terminal -x vedecaps`), 연결 확인(현재 서버로 `vedecaps run` 한 번이 아니라 `/props`+`/v1/models` 점검), 에이전트용 프리셋 한 번에 적용.

**S5. 첫 부팅 마법사 단계.** 모델 단계 뒤에 "코딩 에이전트(Vedecaps) 연결" — 건너뛸 수 있다.

### 3.4 V4 함께 설치

두 가지 방식 중 선택한다(Q2).

| 방식 | 내용 | 장점 | 단점 |
|---|---|---|---|
| **A. 이미지에 포함** | `opereta-vedecaps` 패키지: 고정 버전의 `vedecaps-linux-x64.tar.gz`(약 42 MB)를 빌드 때 받아 SHA-256 확인 후 `/usr/bin/vedecaps` 로 설치. 버전은 `opereta.conf` 의 `VEDECAPS_VERSION`, `VEDECAPS_SHA256` | 오프라인 설치, 첫 실행부터 동작, 버전 일치 시험 가능 | ISO +약 42 MB, 자체 업데이트(`vedecaps upgrade`)는 root 소유 경로라 실패 → Opereta 업데이트 경로로만 갱신 |
| **B. 요청 때 설치** | 제어 센터 단추/`opereta install vedecaps`: 공식 `install.sh` 를 체크섬 확인과 함께 사용자 홈(`~/.vedecaps/bin`)에 설치 | 이미지가 작고 Vedecaps 가 스스로 업데이트 | 네트워크 필요, 첫 사용 단계가 늘어남 |

권장: **A(포함) + 자체 업데이트 안내**. 사용자가 두 제품의 작성자라 버전 짝을 고정해 CI 에서 함께 시험할 수 있다. Alpine 은 해당 없음(Debian 13 glibc 빌드).

### 3.5 V5 검증

- **E2E(QEMU)**: 게이트웨이 단계 뒤에 고정 버전 Vedecaps 실행 파일을 게스트로 넣어(또는 이미지에 포함된 것을 사용) `vedecaps debug local`(감지 확인)과 `vedecaps run --format json` 한 번(모델은 이미 받은 작은 시험 모델; 도구 호출이 안 되면 응답만 확인)을 돈다. 시험 모델이 도구를 못 쓰면 H2 의 "경고" 경로를 확인하는 것으로 한정한다.
- **호환 시험(Go)**: 게이트웨이가 `/props`, `/v1/models`, `/opereta/info`, 스트리밍 SSE, 429/401 오류 본문을 Vedecaps 가 읽는 모양으로 내는지(고정 본보기 JSON 비교).
- **Vedecaps 쪽(사용자 저장소)**: 4절 변경의 단위 시험.
- 실기(GPU) 확인 항목: 실제 코딩 모델로 편집·테스트 루프, 서브에이전트 병렬, 컨텍스트 압축.

## 4. Vedecaps 저장소에 필요한 변경(제안만, 이 저장소에서 하지 않는다)

Vedecaps 는 사용자의 별도 저장소(`nxodh/vedecaps`)다. 아래는 0.5.3 의 이점을 극대화하기 위한 제안이며, 어느 것도 Opereta 쪽 변경의 선행 조건이 아니다.
자세한 설계(현재 동작 근거, 코드 변경 지점, 시험, 수용 기준, PR 분할)는 [vedecaps-repo-changes-design.md](vedecaps-repo-changes-design.md) 에 있다.

| 번호 | 변경 | 이유 |
|---|---|---|
| VE1 | 감지에 Opereta 라벨(`X-Opereta` 헤더, `/opereta/info`)과 키 환경 변수(`OPERETA_API_KEY`) 지원 | 키가 있는 서버를 키 없이 감지하려는 실패를 없앤다 |
| VE2 | 제공자별 CA 번들 옵션(`options.ca`) | 자체 서명 HTTPS 게이트웨이 신뢰 |
| VE3 | 병렬 요청 상한 설정(`options.max_concurrency`) | 슬롯 수에 맞춰 서브에이전트 병렬도를 조절 |
| VE4 | `vedecaps debug local` 에 "도구 호출 시험"과 슬롯 정보 표시 | 서버 H2 와 같은 확인을 클라이언트에서도 |
| VE5 | 시스템 경로 설치용 업데이트 억제 옵션(`VEDECAPS_NO_SELF_UPDATE`) | 이미지에 포함한 실행 파일의 자체 업데이트 실패 메시지 제거 |

## 5. 작업 묶음과 순서(제안)

| 묶음 | 내용 |
|---|---|
| W0 | 0.5.2 마무리(제어 센터 연결, 번역, E2E, 문서, 버전 0.5.2) 후 병합 |
| W1 | 호환 점검: 게이트웨이가 Vedecaps 감지 모양을 내는지 시험, C1, C4, H2 |
| W2 | 프리셋 `agent`(H1), 계획·CLI·제어 센터 반영 |
| W3 | `opereta connect vedecaps`, `/v1/connection` 예시, C3(`/opereta/info`) |
| W4 | S2 MCP 서버, S3 스킬·지침 |
| W5 | V4 포함 패키지(A), S4 페이지, S5 마법사 단계 |
| W6 | V5 E2E, 문서(`docs/vedecaps.md`), 검증 기록, 버전 0.5.3 |

## 6. 결정이 필요한 것

> **결정(2026-10-14)**: 사용자가 "바로 착수"를 지시해 아래 기본값대로 구현했다. 사용자가 바꾸라고 하면 바꾼다. Q6(Vedecaps 저장소 변경)은 결정 전이라 하지 않았다.
> VE5 는 필요 없어졌다: Vedecaps 의 설정 `autoupdate: false` 가 이미 있어 `connect vedecaps` 가 패키지 설치본에만 그 값을 넣는다.

| 번호 | 질문 | 기본값(제안) |
|---|---|---|
| Q1 | 루프백 요청은 키 없이 허용한다(C1). LAN 요청만 키 요구 | **허용**(끌 수 있음) |
| Q2 | Vedecaps 를 이미지에 포함한다(A) vs 요청 때 설치(B) | **A** |
| Q3 | 게이트웨이 TLS 를 0.5.3 에 넣는가(C5-b). Vedecaps 의 CA 옵션(VE2)과 짝 | **0.5.3 에서는 VPN 안내만**, TLS 는 0.5.4 이후 |
| Q4 | 에이전트에 제어 도구(서버 시작·정지)를 MCP 로 노출한다(S2) | **읽기 전용 기본, 제어는 플래그** |
| Q5 | 프리셋 이름과 기본값: `agent` 를 새 설치의 기본 프로필로 하는가, 기존 `chat`(현재 동작) 유지인가 | **기존 유지, 첫 부팅 마법사에서 선택** |
| Q6 | Vedecaps 저장소 변경(VE1~VE5)을 사용자가 직접 하는가, 이 세션이 PR 을 만드는가 | **사용자 결정** |

## 7. 위험과 한계

- Vedecaps 는 0.1.x 로 빠르게 바뀐다(하루 사이 0.1.0 → 0.1.3). 고정 버전과 체크섬으로 묶고, 호환 시험이 깨지면 알 수 있게 한다.
- 실기 GPU 에서 코딩 모델을 돌려 본 적이 없다. 프리셋의 컨텍스트와 병렬 값은 VRAM 크기별 실측 전에는 **계획값**이다.
- 루프백 키 면제(C1)는 같은 컴퓨터의 모든 프로세스가 서버를 쓸 수 있다는 뜻이다. 이미 내부 포트가 열려 있으므로 새 위험은 아니지만 "키 없는 로컬 접근 허용"을 문서에 명시한다.
- 평문 HTTP 키 전송(C5): VPN 밖의 사용을 권하지 않는다.
