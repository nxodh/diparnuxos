# Vedecaps 와 함께 쓰기

Vedecaps 는 터미널에서 쓰는 코딩 에이전트다([nxodh/vedecaps](https://github.com/nxodh/vedecaps)).
Diparnux 는 모델 서버를 운영하는 운영체제다. 0.5.3 부터 둘이 함께 동작하도록 이 컴퓨터의 서버를 Vedecaps 의 모델 제공자로 쓰게 하고,
Vedecaps 가 서버 상태를 읽고 문제를 짚을 수 있게 한다. 조사 내용은 [vedecaps-research.md](vedecaps-research.md),
설계와 결정은 [phase-5-3-design.md](phase-5-3-design.md), 검증 범위는 [phase-5-3-verification.md](phase-5-3-verification.md) 에 있다.

> **검증 범위**: 단위·통합 시험과 QEMU 의 CPU E2E 로 확인했다. 실제 GPU 에서 코딩 모델로 Vedecaps 작업을 끝까지 돌려 본 것은 아직 없다.
> 컨텍스트와 병렬 값은 계획값이며 VRAM 별 실측 전의 기본이다.

## 1. 설치

이미지(0.5.3 이상)에 `diparnux-vedecaps` 패키지가 들어 있다. `/usr/bin/vedecaps` 에 Vedecaps 0.1.5 의 릴리스 실행 파일이 있다(0.5.3 은 0.1.3 이었다).
버전과 SHA-256 은 `diparnux.conf` 의 `VEDECAPS_VERSION`, `VEDECAPS_SHA256` 에 고정되어 있고, 이미지를 만들 때 받은 파일의 해시가 다르면 빌드가 멈춘다.
Vedecaps 의 자체 업데이트는 쓰지 않는다. 이 실행 파일은 일반 사용자가 바꿀 수 없으므로, 업데이트는 Diparnux 업데이트로 온다.

## 2. 연결

같은 컴퓨터에서는 설정 없이도 동작한다. Vedecaps 는 `127.0.0.1:8080` 의 llama.cpp 서버를 스스로 찾고,
Diparnux 는 이 컴퓨터에서 온 요청에 API 키를 요구하지 않는다(`local_no_key`, 기본 켬. 아래 5절).

설정을 만들어 두면 모델 이름과 요청당 컨텍스트 크기를 Vedecaps 가 정확히 알고, 도구와 스킬이 함께 연결된다.

```
diparnux connect vedecaps                   # 설정 조각만 출력한다(파일을 건드리지 않는다)
diparnux connect vedecaps --write --mcp     # Vedecaps 전역 설정에 합친다
diparnux connect vedecaps --remote          # 다른 컴퓨터의 Vedecaps 에 쓸 설정(이 컴퓨터의 LAN 주소)
```

`--write` 는 `~/.config/vedecaps/vedecaps.json`(`XDG_CONFIG_HOME` 이 있으면 그 아래)에 다음만 합친다.

- `provider.diparnux`: 주소, 모델, `limit.context`(요청 하나의 컨텍스트), 키가 필요하면 `{env:DIPARNUX_API_KEY}`
- `model`: 설정에 기본 모델이 **없을 때만** `diparnux/<모델>` 로 넣는다. 다른 기본 모델이 있으면 그대로 두고 알린다(`--make-default` 로 바꾼다)
- `autoupdate: false`: 패키지로 설치된 Vedecaps 이고 사용자가 정한 값이 없을 때만
- `--mcp` 를 주면 `mcp.diparnux`(읽기 전용 도구, 3절)

그 밖의 설정과 다른 제공자는 건드리지 않는다. 기존 파일은 `.bak` 로 한 부 남기고, 새 파일은 사용자만 읽는 권한(0600)이다.
기존 파일에 주석이나 끝 쉼표가 있어 안전하게 고쳐 쓸 수 없으면 아무것도 바꾸지 않고, 합칠 조각을 출력한다.
`--write` 는 스킬 링크(`~/.agents/skills/diparnux`)도 만든다. 이미 있으면 그대로 둔다.

**키는 파일에 쓰지 않는다.** 키가 필요한 주소(다른 컴퓨터, `--remote`)에서는 설정이 환경 변수 이름만 가리킨다.
키는 만들 때 한 번만 보인다(`diparnux apikey create vedecaps`). Vedecaps 를 실행하는 쪽에서 `export DIPARNUX_API_KEY=...` 로 넣는다.

### 다른 컴퓨터에서 쓰기

1. 이 컴퓨터: `diparnux config set lan on`, `diparnux apikey create vedecaps`
2. 다른 컴퓨터: `diparnux connect vedecaps --remote` 의 출력을 그 컴퓨터의 Vedecaps 설정에 넣고 `DIPARNUX_API_KEY` 를 정한다
   (Diparnux 가 아닌 컴퓨터라면 이 컴퓨터에서 출력을 복사해 간다)
3. LAN 의 다른 컴퓨터는 **TLS**(`diparnux tls on`)를 켜서 https 로 접속하는 것을 권한다. [tls.md](tls.md) 참고. TLS 가 꺼져 있으면 평문 HTTP 이므로 같은 LAN 이나 VPN(`diparnux vpn`) 안에서만 쓴다. [remote-access.md](remote-access.md) 참고
   - TLS 가 켜져 있으면 `connect vedecaps --remote` 는 https 주소와 `options.ca` 를 쓴다. CA 파일을 `--ca-out` 으로 받아 그 컴퓨터에 둔다. `options.ca` 는 **Vedecaps 0.1.5 이상**이 믿는다.

## 3. 에이전트가 서버를 들여다보는 길

**MCP 서버**: `diparnux mcp` 는 표준 입출력으로 말하는 MCP 서버다. Vedecaps 설정의
`"mcp": {"diparnux": {"type": "local", "command": ["diparnux", "mcp"]}}` 가 이것이다(`connect --mcp` 가 넣는다).

| 도구 | 내용 |
|---|---|
| `diparnux_status` | 버전, 모드, GPU, 메모리, 서버 상태 |
| `diparnux_server_stats` | 슬롯 점유, 초당 토큰, 대기 요청, 게이트웨이 집계 |
| `diparnux_connection` | 주소, 키 필요 여부, 모델 이름, 요청당 컨텍스트 |
| `diparnux_models` | 설치된 모델과 기본 모델 |
| `diparnux_plan` | 모델의 실행 계획과 근거(`profile` 인수 가능) |
| `diparnux_resources` | CPU, 메모리, 디스크, 온도, GPU |
| `diparnux_exposure` | 이 컴퓨터가 네트워크에 내놓는 것과 경고 |
| `diparnux_toolcheck` | 모델이 도구를 제대로 부르는지 |
| `diparnux_logs` | 서버 로그의 마지막 줄 |

기본으로는 읽기 전용 도구만 낸다. `diparnux mcp --allow-control` 이면 `diparnux_server_start` 와 `diparnux_server_stop` 이 더해진다
(설정 조각에는 이 플래그를 넣지 않는다. 쓰려면 사용자가 직접 설정에 적는다). 모든 도구는 데몬의 유닉스 소켓 API 로만 말하므로 호출한 사용자의 권한을 넘지 않는다.
방화벽, 계정, 네트워크 설정, 업데이트를 바꾸는 도구는 없다. 데몬 응답에는 API 키나 토큰이 들어 있지 않다.

**스킬**: `/usr/share/diparnux/skills/diparnux/SKILL.md`. 새 계정은 `/etc/skel/.agents/skills/diparnux` 링크로 바로 보고,
기존 계정은 `diparnux connect vedecaps --write` 가 링크를 만든다. 스킬은 서버 상태를 읽는 명령과 흔한 문제의 원인(요청당 컨텍스트, 슬롯, 401, 429,
도구 호출 실패)을 알려 주고, 사용자가 요청하지 않은 변경 명령을 실행하지 않도록 한다. 스킬은 목록의 설명만 매 요청에 실리고 본문은 필요할 때만 읽힌다.

## 4. 코딩 에이전트에 맞는 서버 설정

에이전트는 시스템 프롬프트와 도구 정의, 파일 내용으로 요청마다 긴 컨텍스트를 쓰고, 서브에이전트와 도구 호출이 겹쳐 요청을 병렬로 보낸다.
기본(chat) 계획은 짧은 대화에 맞춰져 있다. **agent 프로필**은 에이전트에 맞춘다.

| | chat(기본) | agent |
|---|---|---|
| 요청당 컨텍스트 | 최대 8192(하한 2048) | **최대 32768** 까지 시도, 하한 8192 |
| 슬롯 | 서버 기본(자동). llama-server 가 슬롯을 여러 개(실측 4) 만들고 하나의 컨텍스트를 나눠 쓴다 | GPU 가 있으면 **2 로 고정**, 각 슬롯이 컨텍스트를 따로 가진다 |
| 프롬프트 캐시 | 기본 | 조각 단위 재사용(`--cache-reuse 256`) |
| 경고 | | 요청당 16384 미만이면 기록을 자주 줄인다고 알림 |

```
diparnux serve plan --profile agent      # 계획과 근거만 본다
diparnux serve start --profile agent
diparnux config set profile agent        # 저장(다음 시작부터). chat 으로 되돌린다
```

제어 센터의 서버 페이지와 Vedecaps 페이지, 첫 부팅 마법사에서도 고른다. 직접 준 `--ctx`, `--parallel` 이 프로필보다 우선한다.

공통으로 바뀐 것: 프롬프트 캐시의 메모리 상한을 전체 메모리의 1/8(512 MiB ~ 8 GiB)로 묶는다. llama-server 기본값은 8 GiB 라서
작은 컴퓨터에서 상시 가동 중 캐시가 메모리를 다 쓸 수 있다.

**도구 호출 확인**: `diparnux serve toolcheck`. 도구 하나를 준 작은 요청을 보내 모델이 `tool_calls` 를 올바른 JSON 인수로 돌려주는지 본다.
코딩 에이전트는 이것이 되어야 파일을 읽고 고친다. 되지 않으면 채팅 틀(chat template)에 도구 지원이 없는 모델이다.
llama-server 는 고정 커밋에서 jinja 틀을 기본으로 쓰므로 별도 옵션은 필요 없다. 추천 모델 목록에는 도구 호출을 지원하고 8B 이상인 모델에
"코딩 에이전트용" 표시가 있다(모델 카드 기준이며, 이 컴퓨터에서의 동작은 위 확인으로 본다).

## 5. 키와 보안

- **이 컴퓨터의 요청은 키 없이 통과한다**(`local_no_key`, 기본 켬). 게이트웨이가 받는 요청의 출발지가 루프백이거나 이 컴퓨터의 인터페이스 주소이면 통과하고,
  키를 내면 그 키로 사용량을 집계한다. 내부 포트(18080)는 이미 로컬 프로세스에 열려 있으므로 새 위험은 아니다.
  같은 컴퓨터의 모든 프로세스가 모델 서버를 쓸 수 있다는 뜻이므로, 여러 사람이 쓰는 컴퓨터에서는 끈다:
  `diparnux config set local-no-key off`.
- LAN 의 다른 컴퓨터는 항상 키가 필요하다. 키가 없으면 LAN 공개 자체가 거부된다.
- 응답마다 `X-Diparnux: <버전>` 헤더가 붙고, 키 없이 읽는 `GET /diparnux/info` 가 버전, 서버 상태, 모델 이름, 요청당 컨텍스트, 슬롯 수를 낸다.
  모델 파일 경로나 설정은 담지 않는다.
- 서버가 바쁠 때 429 에 `Retry-After: 2` 가 붙는다. Vedecaps 는 이 값을 존중해 다시 시도한다.
- 이 저장소의 어떤 파일에도 키를 쓰지 않는다. 시험은 가짜 값만 쓴다.

## 6. 제어 센터

사이드바의 **Vedecaps** 페이지: 실행 파일 위치, 서버 상태, 요청당 컨텍스트(작으면 agent 용도를 권한다), 용도(chat/agent),
"Vedecaps 설정"(= `connect vedecaps --write --mcp`), 도구 호출 확인, 설정 조각 보기와 복사.
서버 페이지에는 용도 선택과 "이 컴퓨터에서는 키 없이 사용" 스위치가 있다.

## 7. 문제 해결

| 증상 | 원인과 조치 |
|---|---|
| Vedecaps 가 서버를 못 찾는다 | `diparnux status` 로 서버가 도는지 본다. 키만 있고 `local_no_key` 가 꺼져 있으면 감지가 실패한다. `connect vedecaps --write` 로 설정을 만든다 |
| 401 | 이 주소는 키가 필요하다. `DIPARNUX_API_KEY` 를 확인한다 |
| 429 | 모든 슬롯과 대기열이 찼다. 잠시 뒤 다시 시도되며, 병렬 작업이 많으면 `--parallel` 을 늘린다(메모리 필요) |
| "컨텍스트가 가득 찼다", 기록을 자주 줄인다 | 요청당 컨텍스트가 작다. agent 프로필, 더 작은 모델, 더 적은 슬롯을 쓴다 |
| 도구를 부르지 않고 글로만 답한다 | `diparnux serve toolcheck`. 도구 지원이 있는 모델로 바꾼다(추천 목록의 "코딩 에이전트용") |
| 느리다 | `diparnux serve stats` 로 슬롯 점유와 속도를 본다. CPU 폴백이면 `diparnux status` 의 GPU 상태를 본다 |

## 8. 호환 시험

`make test-vedecaps-docker`: 고정 버전의 실제 Vedecaps 를 컨테이너에서 Diparnux 게이트웨이와 가짜 모델 서버에 붙여 감지, 스트리밍, 키 규칙을 확인한다.
CI 에서도 돈다. Vedecaps 버전을 올릴 때(`diparnux.conf`)는 이 시험을 먼저 돌린다.

## 9. 한계

- Vedecaps 는 0.1.x 로 빠르게 바뀐다. 이미지는 고정 버전을 쓰며, 새 버전으로 올릴 때는 `diparnux.conf` 의 값과 E2E 호환 시험을 함께 갱신한다.
- TLS 는 0.5.4 에서 들어왔다([tls.md](tls.md)). 켜지 않으면 게이트웨이는 평문 HTTP 다. 이 저장소가 이미지에 고정한 Vedecaps 가 0.1.5 보다 낮으면 `options.ca` 를 무시하므로 https 접속에는 0.1.5 이상이 필요하다.
- 이미지에 들어 있는 실행 파일은 glibc 의 x86-64 용이다. 다른 구조는 지원하지 않는다.
- `--cache-reuse` 와 요청당 컨텍스트 값은 실측 전의 계획값이다.
