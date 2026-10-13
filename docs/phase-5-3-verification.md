# 0.5.3 검증 기록

0.5.3 은 Vedecaps 와의 연결성, 호환성, 함께 쓸 때의 시너지를 다룬다. 사용 방법은 [vedecaps.md](vedecaps.md), 설계는 [phase-5-3-design.md](phase-5-3-design.md).
이 문서는 **무엇을 어떻게 확인했고 무엇을 확인하지 못했는지**만 적는다. 실기 검증 전에는 "정식 지원"이라 쓰지 않는다.

## 1. 구현 항목과 확인 방법

| 항목 | 내용 | 확인 |
|---|---|---|
| C1 `local_no_key` | 이 컴퓨터의 요청은 키 없이 통과, LAN 은 키 필요 | Go 게이트웨이 시험(루프백, 인터페이스 주소, 키를 내면 집계) |
| C3 | `X-Diparnux` 헤더, 키 없는 `GET /diparnux/info`(경로 비노출) | Go 게이트웨이 시험, QEMU E2E(`check_vedecaps`) |
| C4 | 429 에 `Retry-After: 2` | Go 게이트웨이 시험 |
| H1 agent 프로필 | 요청당 컨텍스트 최대 32768, GPU 에서 2 슬롯, 경고 | autoconf 단위 시험(여유 있음, 부족, CPU, 사용자 값 우선, 검증), 감독자 시험(저장된 프로필 반영), GUI 시험 |
| H2 도구 호출 확인 | `POST /v1/server/toolcheck`, `diparnux serve toolcheck` | API 시험(성공, 글만 답함, 잘못된 인수, 객체 인수, 템플릿 오류 500, 서버 정지), CLI 시험, E2E(결과 형식) |
| H3 캐시 | agent 에서 `--cache-reuse 256`, 캐시 메모리 상한 전체의 1/8(512 MiB ~ 8 GiB) | autoconf·llamaargs·감독자 시험. 효과는 **측정하지 않았다**(5절) |
| H4 추천 | 도구 호출 지원과 8B 이상에 "코딩 에이전트용" 표시 | 목록 일관성 시험, GUI |
| `connect vedecaps` | 설정 조각 출력, `--write` 병합(.bak, 0600, 다른 설정 보존, 주석 있는 파일 거절), `--remote`, `--mcp`, 스킬 링크 | CLI 시험(임시 HOME 과 XDG, 기존 설정 보존, 거절, 되풀이 실행), `internal/vedecaps` 시험 |
| `diparnux mcp` | stdio MCP(initialize, ping, tools/list, tools/call), 읽기 전용 기본, `--allow-control` | `internal/mcpserver` 시험(버전 협상, 오류, 크기 제한, 패닉 격리), CLI 시험(제어 도구 차단), E2E |
| 스킬 | `/usr/share/diparnux/skills/diparnux`, skel 링크 | dist 시험(frontmatter, 명령 목록, 키 없음), 패키지 시험 스크립트 |
| 제어 센터 | Vedecaps 페이지, 서버 페이지 용도·키 면제, 마법사 용도, 번역 | GUI 시험 77개 |
| `diparnux-vedecaps` | 고정 버전과 SHA-256, 빌드 때 확인 | 컨테이너에서 .deb 를 실제로 만들고 `vedecaps --version` 실행. 잘못된 해시, 이름이 다른 묶음은 거절됨을 확인 |
| 이미지 점검 | `/usr/bin/vedecaps` 와 스킬 존재 | `build-iso.sh`, `tests/nvidia/image.sh` 에 항목 추가(ISO 는 CI 에서 확인) |
| SSE | 청크가 모이지 않고 바로 전달됨 | Go 시험(두 번째 청크를 막아 두고 첫 청크 도착을 확인) |

## 2. E2E(QEMU CPU)에서 새로 보는 것

`tests/qemu/cpu-inference.py` 의 `check_vedecaps` 와 `check_gateway` 의 거절 본문 검사:

- `/props` 의 `default_generation_settings.n_ctx`, `total_slots`
- `/v1/models` 의 모델 id 와 `X-Diparnux` 헤더, `/diparnux/info` 의 버전·요청당 컨텍스트·슬롯 수(경로 비노출)
- `stream: true` 와 `stream_options.include_usage` 의 SSE(`text/event-stream`, 여러 청크, usage 청크, `[DONE]`)
- 도구가 붙은 요청이 거절되지 않는다
- 키가 있을 때 키 없는 요청: 401 과 JSON 본문의 `error.message`
- `diparnux connect vedecaps --mcp` 가 낸 설정의 주소, 모델, `limit.context` 가 서버의 값과 같다
- `diparnux mcp` 가 읽기 전용 도구만 낸다
- `serve toolcheck` 가 알려진 결과 코드와 설명을 낸다(작은 시험 모델은 통과하지 않아도 된다)

드라이버 회귀 18개(`python3 -I tests/qemu/test_cpu_inference.py`)가 가짜 QEMU 로 통과한다. 새 검사가 실제로 쓰이는지는
고의로 값을 틀리게 한 가짜로 실패하는 것을 확인했다. 실제 ISO 위의 실행은 CI 에서 한다.

## 2.1 실제 Vedecaps 실행 파일로 확인한 것

`make test-vedecaps-docker`(`tests/vedecaps/`): `diparnux.conf` 에 고정한 Vedecaps 0.1.3 실행 파일을 Debian 13 컨테이너에서 실제로 돌린다.
뒤쪽은 **실제 Diparnux 게이트웨이 코드**(`daemon/internal/gateway`)와 가짜 llama-server(Python)다. 설정 파일은 `connect vedecaps` 가 만드는 모양이다.

| 시나리오 | 확인 |
|---|---|
| A 키 없음 | `/props` 로 서버 감지, SSE 스트리밍 응답이 화면에 나옴, 도구 14개가 요청에 실림 |
| B 키가 있고 `local_no_key` 켬 | 키 없는 로컬 요청이 통과 |
| C 키가 있고 `local_no_key` 끔, 키를 안 보냄 | Vedecaps 가 게이트웨이의 `error.message`("a valid API key is required")를 그대로 보여 줌 |
| D 키 필요, 환경 변수의 맞는 키 | `{env:DIPARNUX_API_KEY}` 가 풀려 통과, 모델 서버에는 키가 전달되지 않음 |
| E 키 필요, 틀린 키 | 거절 |

같은 방법으로 확인한 것(수동 실행, 실제 `vedecaps debug`):

- `connect vedecaps --mcp` 가 만든 설정을 Vedecaps 0.1.3 이 읽는다(`Providers with credentials: diparnux openai via config 1 models`). `config.schema.json` 검증도 통과한다.
- `~/.agents/skills/diparnux` 가 스킬로 잡힌다(`Skills diparnux`).
- `diparnux mcp` 에 Vedecaps 가 연결해 도구 9개를 읽는다(`MCP servers diparnux connected (9 tools)`).

이 시험은 CI 의 `iso` 잡에서도 CUDA 빌드 전에 돈다. Vedecaps 를 올릴 때 호환이 깨지면 여기서 먼저 드러난다.

## 3. 시험 실행 결과(이 세션, 로컬 컨테이너)

| 시험 | 결과 |
|---|---|
| Go 전체(`go test ./...`, Debian 컨테이너) | 통과 |
| GUI(`make test-gui`) | 77개 통과 |
| QEMU 드라이버 회귀 | 18개 통과 |
| `make check-secrets`, `make check-legacy-name` | 통과 |
| `diparnux-vedecaps` .deb 빌드와 실행 | 통과(Vedecaps 0.1.3, glibc 의존만) |
| `make test-vedecaps-docker`(실제 Vedecaps) | 5개 시나리오 통과 |
| actionlint(워크플로) | 통과 |

CI 결과는 PR 에서 확인한다.

## 4. 확인하지 못한 것

- **실제 GPU 에서 코딩 모델로 Vedecaps 작업**(편집·테스트 루프, 서브에이전트 병렬, 컨텍스트 압축)을 끝까지 돌려 본 적이 없다.
- agent 프로필의 컨텍스트(32768)와 슬롯(2)이 VRAM 크기별로 맞는지는 실측 전이다. 계획이 메모리를 잘못 어림할 수 있다.
- `--cache-reuse 256` 이 Vedecaps 의 요청 모양에서 실제로 시간을 줄이는지, 모델 구조(SWA 등)에 따라 llama-server 가 이 옵션을 끄는지 확인하지 못했다.
- 추천 목록의 "도구 호출 지원"은 모델 카드 기준이다. 이 컴퓨터에서의 동작은 `serve toolcheck` 로만 알 수 있다.
- 원격 컴퓨터(LAN/VPN)에서의 Vedecaps 접속은 시험하지 않았다. 평문 HTTP 이다.
- 실기 ISO 부팅 후 `/usr/bin/vedecaps` 의 첫 실행.

## 5. 사용자 결정 대기

- Q6: Vedecaps 저장소(`nxodh/vedecaps`)에 대한 변경 제안 VE1~VE4([phase-5-3-design.md](phase-5-3-design.md) 4절)를 누가 하는가. 이 세션은 Vedecaps 저장소를 바꾸지 않았다.
- TLS(Q3): 0.5.4 이후로 미뤘다.
- 새 설치의 기본 프로필(Q5): chat 을 유지하고 첫 부팅 마법사에서 고르게 했다.
