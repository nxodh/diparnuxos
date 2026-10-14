# Diparnux 0.5.4 설계서: 게이트웨이 TLS, Vedecaps 2차 연동, 경량화와 실기 수정

- 작성: 2026-10-14
- 기준 코드: `main` 의 0.5.2 + `phase-5-3`(0.5.3, PR #12). 0.5.3 이 병합되어 있다고 가정한다.
- 상태: **설계 초안. 사용자 결정(10절) 전에는 구현하지 않는다.** 아래 코드와 설정 이름은 제안이며 실행해 본 것이 아니다.
- **사용자 결정(2026-10-14)**: Q7 은 "Vedecaps 릴리스를 기다린다"(TLS 만 먼저 내지 않는다), Q8 은 "경량화와 GPU 수정도 0.5.4 에 넣는다"(0.5.5 로 분리하지 않는다). 이 문서는 그 결정대로 고쳤다.
- 관련: [phase-5-3-design.md](phase-5-3-design.md), [vedecaps.md](vedecaps.md), [remote-access.md](remote-access.md),
  [vedecaps-repo-changes-design.md](vedecaps-repo-changes-design.md)(Vedecaps 저장소 변경 VE1~VE4, VE2 가 이 문서와 짝),
  [vedecaps-agent-handoff-0.5.4.md](vedecaps-agent-handoff-0.5.4.md)(Vedecaps 개발 에이전트에게 넘기는 문서)

## 0. 요약

0.5.3 까지 모델 서버 게이트웨이는 **평문 HTTP** 다. 같은 컴퓨터와 믿는 LAN 에서는 괜찮지만, LAN 의 다른 컴퓨터가 보내는 API 키와
프롬프트, 모델 응답이 그대로 지나가고, 응답에 도구 호출을 끼워 넣는 중간자 공격도 막지 못한다. 코딩 에이전트는 응답으로 **명령을 실행**하므로
이것은 단순한 도청 문제가 아니다. 0.5.4 의 중심은 이것을 고치는 것이다.

| 묶음 | 내용 | 상태 |
|---|---|---|
| **T** | 게이트웨이 TLS: 별도 HTTPS 포트, 작은 로컬 CA 와 서버 인증서, CA 배포, 방화벽·노출 점검 | 이 문서의 본론 |
| **V** | Vedecaps 2차 연동: HTTPS 접속(`options.ca`), 새 Vedecaps 버전 고정, 호환 시험 | Vedecaps 쪽 VE2 와 짝 |
| **R** | 실기 검증에서 나온 수정, 경량화(램 목표), GPU 서버 시작 실패 | **0.5.4 에 포함**(사용자 결정). 자료·결정 대기 항목이 있다(9절) |

**핵심 결정(권장)**

1. **HTTPS 는 별도 포트(기본 8444)**, 루프백 평문 HTTP `127.0.0.1:8080` 은 그대로 둔다. 같은 컴퓨터의 Vedecaps 자동 감지와 기존 도구가 바뀌지 않는다.
2. TLS 를 켜면 **LAN 쪽 평문 HTTP 는 닫는다**(옵션으로만 유지). 평문과 TLS 를 둘 다 LAN 에 열어 두면 의미가 없다.
3. 인증서는 **작은 로컬 CA(10년) + 서버 인증서(397일, 자동 갱신)** 다. 클라이언트에는 CA 하나만 한 번 배포하면 되고, IP 나 이름이 바뀌어도 다시 배포하지 않는다.
4. **리다이렉트를 하지 않는다.** Vedecaps 는 리다이렉트를 오류로 본다(키 유출 방지).
5. **인증서 검증을 끄라고 안내하지 않는다.** CA 를 믿게 하는 방법만 제공한다.

**하지 않는 것**: 공인 인증서(Let's Encrypt) 발급, 상호 TLS(클라이언트 인증서), 게이트웨이 앞단 외부 프록시, HTTP/3. 3절의 위협 모델 밖이다.

## 1. 현재 상태(근거)

| 사실 | 근거 |
|---|---|
| 게이트웨이는 데몬 안에서 도는 하나의 `http.Server` 이고, `Manager.reconcile` 이 `net.Listen("tcp", addr)` 한 번으로 연다. 주소는 LAN 공개가 꺼져 있으면 `127.0.0.1:Port`, 켜져 있으면 `0.0.0.0:Port` | `daemon/internal/gateway/gateway.go`, `daemon/cmd/diparnuxd/main.go` 의 `Wanted` |
| 모델 서버(llama-server)는 `127.0.0.1:InternalPort(18080)` 에서만 듣고, 게이트웨이가 키를 확인한 뒤 넘긴다 | `config.Config.InternalPort`, `Manager.Backend` |
| 방화벽은 `APIPort: cfg.Port, APIOpen: cfg.LANExposed` 로 API 포트를 LAN 에 연다 | `daemon/internal/supervisor/system.go` |
| 이 컴퓨터의 요청은 `local_no_key`(기본 켬) 로 키 없이 통과하고, 판정은 요청 출발지 주소로 한다 | `gateway.go` 의 `isLocal` |
| `/diparnux/info`, `X-Diparnux` 헤더가 키 없이 읽힌다 | 0.5.3 |
| **TLS 부품이 이미 있다**: 원격 관리 웹(webadmin)은 데몬이 만든 자체 서명 ECDSA P-256 인증서(10년), 이름 집합 `covers` 검사, 지문 표시, 주소가 바뀌면 재발급을 쓴다 | `daemon/internal/webadmin/webadmin.go` 의 `ensureCert`, `hostNames`, `covers`, `fingerprint` |
| 연결 정보(`/v1/connection`)와 `connect vedecaps` 가 만드는 주소는 모두 `http://` 이다 | `daemon/internal/api/connection.go`, `daemon/internal/vedecaps` |
| `remote-access.md` 의 안내는 "평문 HTTP 이므로 같은 LAN 이나 VPN 안에서만" | `docs/vedecaps.md` 2절 |
| webadmin 의 기본 예시 포트가 8443 이다 | `docs/remote-access.md` |
| 게이트웨이는 IPv4(`0.0.0.0`)만 듣는다 | `main.go` 의 `host := "0.0.0.0"` |

### 1.1 Vedecaps 쪽 제약(0.1.3 소스, 커밋 `f187450`)

| 제약 | 영향 |
|---|---|
| `src/provider/openai.ts`, `src/provider/local.ts` 가 `redirect: "error"` 로 요청한다 | 게이트웨이가 `http` → `https` 리다이렉트를 내면 기존 연결이 **깨진다** |
| 자동 감지가 `http://127.0.0.1:8080` 만 본다(`detectLocalServers`) | 루프백을 TLS 로 바꾸면 자동 감지가 깨진다 → 루프백은 평문 유지 |
| HTTPS 주소의 인증서는 Bun 의 기본 신뢰 저장소로만 검증하고 CA 옵션이 없다 | 자체 CA 를 믿게 하려면 **VE2(`options.ca`)** 가 필요하다 |
| `isLocalURL` 이 `.local`, 사설 IP 를 로컬로 본다(컴팩트 프롬프트 규칙) | `https://diparnux.local:8444` 도 로컬로 취급된다. 변경 불필요 |
| `provider.*.api/env/baseURL/headers` 는 신뢰하지 않은 프로젝트 설정에서 제거된다 | `ca` 도 같은 부류로 제거해야 한다(VE2 의 신뢰 규칙) |

## 2. 목표와 비목표

**목표**

- G1. LAN 의 다른 컴퓨터가 API 키와 대화를 **암호화된 연결**로 보낸다.
- G2. 클라이언트가 서버를 **확인**한다(CA 배포 후 위조 서버, 중간자를 거부).
- G3. 같은 컴퓨터의 기존 사용법(`http://127.0.0.1:8080`, Vedecaps 자동 감지)이 **바뀌지 않는다.**
- G4. 켜기·끄기·CA 배포·갱신이 `diparnux` 명령, 제어 센터, MCP 도구(읽기)로 가능하다.
- G5. 인증서가 만료되거나 주소가 바뀌어도 사용자가 손대지 않아도 된다(자동 갱신).

**비목표**

- 공인 CA 인증서, ACME, 도메인 소유 증명.
- 클라이언트 인증서(상호 TLS). API 키가 이미 클라이언트 인증이다.
- 루프백 트래픽의 암호화(같은 컴퓨터 안의 통신이므로 TLS 의 이점이 없고 호환만 깨진다).
- webadmin 의 인증서 방식 변경(그대로 둔다. 4.4절에서 공통 부품만 정리한다).

## 3. 위협 모델

| 위협 | TLS 전 | TLS 후 |
|---|---|---|
| LAN 의 누군가가 패킷을 엿봄(키, 프롬프트, 코드) | 가능 | 막힘 |
| LAN 의 누군가가 응답을 바꿈(도구 호출 주입) | 가능 | 클라이언트가 CA 를 확인하면 막힘 |
| 같은 이름(`diparnux.local`)을 사칭하는 서버(mDNS 위조) | 가능 | CA 로 발급받지 못하므로 거부됨 |
| 공격자가 CA 개인 키를 얻음 | 해당 없음 | **위험**. 아래 대응 |
| 서버 컴퓨터 자체가 침해됨 | 위험 | 위험(TLS 로 해결되지 않음) |
| 키 값이 로그에 남음 | 이미 가림 | 같음 |

**CA 개인 키 대응**: (1) 파일은 `root:root 0600`, 데몬만 읽는다. (2) CA 에 **DNS 이름 제약**(허용 DNS: `.local`, 이 컴퓨터의 호스트 이름)을
넣어, 키를 얻어도 공개 사이트 인증서를 위조할 수 없게 한다. (3) CA 를 재발급하면 이전 CA 는 클라이언트에서 지워야 한다는 안내(`diparnux tls renew-ca`)를 낸다.
이름 제약을 클라이언트 쪽 Windows, macOS 시스템 저장소가 모두 지키는지는 **확인하지 않았다**(curl, Python 은 지킨다고 알려져 있다. 실기 검증 항목).

**IP 제약은 넣지 않는다(Vedecaps 개발 에이전트의 확인, 2026-10-14)**: Vedecaps 가 쓰는 Bun 은 CA 에 IP 주소 이름 제약이 있고 서버 인증서에 IP SAN 이 있으면
`UNSUPPORTED_CONSTRAINT_TYPE` 으로 검증에 실패한다(Bun 의 한계). 그래서 CA 의 이름 제약은 **DNS 이름만** 쓴다. 서버 인증서의 SAN 에는 IP 를 그대로 넣는다.
대가: IP 에는 제약이 없으므로 CA 개인 키가 유출되면 임의의 IP 주소용 인증서를 만들 수 있다. DNS 이름(`.local`, 호스트 이름 밖)의 위조는 여전히 막힌다.
이 한계를 줄이는 방어는 CA 개인 키의 보호(0600, 데몬만, 보고서와 API 에 나오지 않음)와 `pathlen:0` 이다.

## 4. 설계

### 4.1 리스너 구조(결정 T1)

| 안 | 구조 | 장점 | 단점 | 판단 |
|---|---|---|---|---|
| **A. 별도 TLS 포트** | `127.0.0.1:8080` 평문, `0.0.0.0:8444` TLS. TLS 가 켜지면 8080 은 루프백만 | 루프백 호환 유지, 구현 단순, 평문 LAN 노출 제거 | 포트가 하나 늘어 방화벽과 안내 변경 | **권장** |
| B. 같은 포트 판별 | 첫 바이트(0x16)로 TLS/평문을 구분해 8080 에서 둘 다 | 포트 하나 | 평문이 LAN 에 남음(옵션으로 거부 가능하나 복잡), 비표준, 오류 모호 | 비권장 |
| C. 전부 TLS | 루프백 포함 8080 이 TLS | 가장 단순한 모델 | Vedecaps 자동 감지와 기존 도구가 깨짐 | 비권장 |

안 A 의 동작:

| `lan_exposed` | `tls` | 8080(평문) | 8444(TLS) |
|---|---|---|---|
| 꺼짐 | 꺼짐 | 127.0.0.1 | 없음 |
| 꺼짐 | 켜짐 | 127.0.0.1 | **없음**(LAN 공개가 아니면 TLS 가 필요 없다) |
| 켜짐 | 꺼짐 | 0.0.0.0 (지금과 같음) | 없음 |
| 켜짐 | 켜짐 | **127.0.0.1** (LAN 평문 닫음) | 0.0.0.0 |
| 켜짐 | 켜짐 + `lan_plain` 켬 | 0.0.0.0 | 0.0.0.0 |

두 리스너는 **같은 핸들러**(`Gateway.Handler(backend)`)를 쓴다. 키 규칙, `local_no_key`, 429, `/diparnux/info` 가 같다.
`isLocal` 은 요청 출발지 주소로 판정하므로 TLS 리스너에서도 그대로 맞다(이 컴퓨터의 LAN 주소로 접속해도 로컬로 판정한다).

### 4.2 인증서 모델(결정 T2)

| 안 | 내용 | 장점 | 단점 | 판단 |
|---|---|---|---|---|
| **A. 로컬 CA + 서버 인증서** | CA(ECDSA P-256, `CA:TRUE`, `pathlen:0`, DNS 이름 제약(IP 제약 없음), 10년) → 서버 인증서(397일) | 클라이언트는 CA 한 번만 믿으면 되고, IP/이름이 바뀌어도 재배포 없음 | CA 개인 키 관리 필요 | **권장** |
| B. 자체 서명 서버 인증서 | webadmin 방식 그대로 | 부품이 이미 있음, 개인 키 하나 | 주소가 바뀌면 클라이언트가 **다시 믿어야** 한다. DHCP 환경에서 자주 | 차선(구현이 가장 작음) |

권장은 A. 다만 B 로 시작해서 A 로 올리는 길도 있다(`ca.pem` 대신 `server.pem` 을 배포하는 차이뿐이라 클라이언트 설정 형식이 같다).

**이름(SAN)**: `localhost`, `127.0.0.1`, `::1`, 이 컴퓨터의 호스트 이름, `<호스트>.local`, 지금 인터페이스의 모든 IPv4/IPv6 주소, 설정에서 추가한 이름(`tls_names`).
주소가 바뀌면 서버 인증서를 자동으로 다시 발급한다(CA 는 그대로). webadmin 의 `hostNames`, `covers` 를 공통 부품으로 쓴다.

**수명**: 서버 인증서 397일(macOS/iOS 가 신뢰하는 서버 인증서 최대 수명에 맞춘다), 만료 30일 전에 자동 갱신. CA 는 10년, 만료 1년 전부터 `diparnux tls status` 와 제어 센터가 경고한다.

**알고리즘**: ECDSA P-256 (webadmin 과 같음). 최소 TLS 1.2, 1.3 우선(Go 기본). 오래된 클라이언트와의 호환을 위해 1.2 를 허용한다.

### 4.3 저장 위치와 권한

| 파일 | 경로(제안) | 권한 | 내용 |
|---|---|---|---|
| CA 인증서 | `/var/lib/diparnux/tls/ca.pem` | 0644 | 배포용 공개 인증서 |
| CA 개인 키 | `/var/lib/diparnux/tls/ca-key.pem` | **0600 root** | 데몬만. API 로 돌려주지 않는다 |
| 서버 인증서 | `/var/lib/diparnux/tls/server.pem` | 0644 | |
| 서버 개인 키 | `/var/lib/diparnux/tls/server-key.pem` | **0600 root** | |

원칙: 개인 키는 API 응답, 로그, 보고서(`diparnux report`), MCP 도구, 제어 센터 어디에도 나오지 않는다. 시험은 가짜 키만 쓴다(`docs/security.md` 의 규칙).
`diparnux report` 의 비밀 가림 목록에 새 파일 이름을 추가한다.

### 4.4 코드 구조(제안)

| 변경 | 내용 |
|---|---|
| 새 패키지 `daemon/internal/certstore` | webadmin 의 `ensureCert`, `hostNames`, `covers`, `fingerprint` 를 공통으로 옮기고 CA 발급을 더한다. webadmin 은 이 패키지를 쓰도록 바꾼다(동작 변경 없음, 기존 시험이 회귀를 잡는다) |
| `gateway.Manager` | 두 번째 리스너 필드 `tlsSrv`, `Wanted` 가 TLS 주소를 함께 돌려주도록 확장. `reconcile` 이 두 리스너를 각각 맞춘다. `tls.Config{MinVersion: TLS12, GetCertificate: store.GetServerCert}`(갱신된 인증서를 재시작 없이 반영) |
| `config.Config` | `TLS bool`, `TLSPort int`(기본 8444), `LANPlain bool`(기본 false), `TLSNames []string`. `Validate`: `tls_port` 는 `port`, `internal_port`, webadmin 포트, SSH 포트와 달라야 한다 |
| `supervisor/system.go` | 방화벽 옵션에 `TLSPort`, `TLSOpen` 추가. `APIOpen` 은 `lan_plain` 또는 TLS 꺼짐일 때만 평문 포트를 연다 |
| `exposure` | TLS 켜짐/꺼짐에 따른 경고: LAN 공개인데 TLS 꺼짐이면 "평문"을 경고(지금도 있는 경고에 근거 추가), TLS 켜짐이면 해소. `lan_plain` 켬이면 "평문이 LAN 에 열려 있음" |
| `api` | `GET /v1/tls`(상태, 지문, 만료, 이름, CA 경로), `POST /v1/tls/enable`, `/disable`, `/renew`, `GET /v1/tls/ca`(CA PEM, 인증 필요 없음, 공개 값) |
| `cmd/diparnux` | `diparnux tls status|on|off|ca|fingerprint|renew|renew-ca`, `diparnux config set tls on` 는 같은 일 |
| `connection.go` | `https_url`, `https_mdns_url`, CA 지문을 연결 정보에 더한다(키는 없음) |
| `daemon/internal/vedecaps` | `connect vedecaps --remote` 가 TLS 켜짐이면 `https://` 주소와 `options.ca` 를 쓴다(5절) |
| `mcpserver` | `diparnux_connection` 도구가 TLS 정보를 낸다(읽기 전용 유지) |
| GUI | 서버 페이지에 TLS 스위치와 지문·만료 표시, Vedecaps 페이지의 "설정 조각" 이 https 주소를 쓴다 |
| 문서 | `remote-access.md`, `vedecaps.md`, `ai-server.md`, `security.md`, `firewall.md` |

### 4.5 CLI 예

```
diparnux tls status               # 켜짐/꺼짐, 포트, CA 지문, 서버 인증서 만료, 이름
diparnux tls on                   # CA 와 서버 인증서를 만들고 8444 에서 듣는다. LAN 공개와 키가 필요하다
diparnux tls ca --out diparnux-ca.pem   # 다른 컴퓨터에 복사할 CA(공개 값)
diparnux tls fingerprint          # CA 의 SHA-256 지문. 복사한 CA 와 비교한다
diparnux tls renew                # 서버 인증서를 지금 다시 발급
diparnux tls renew-ca             # CA 를 다시 만든다(클라이언트에 새 CA 를 다시 배포해야 한다고 경고)
diparnux tls off
```

`tls on` 의 사전 조건: LAN 공개(`lan on`)가 켜져 있고 API 키가 하나 이상 있다. 아니면 이유와 다음 명령을 알려 주고 거부한다(TLS 만 켜고 키가 없으면 의미가 없다).
`tls off` 는 평문 LAN 포트를 다시 열기 때문에 `--yes` 확인을 요구하고, 영향(LAN 에서 평문이 다시 열림)을 알린다.

### 4.6 키와 `local_no_key`

변경 없음. 키는 TLS 위로만 LAN 을 지난다(`lan_plain` 이 꺼져 있을 때). `local_no_key` 는 출발지 주소 기준이므로 TLS 리스너에서도 같다.

### 4.7 업그레이드와 기본값

| 상황 | 동작 |
|---|---|
| 0.5.3 에서 올라온 컴퓨터 | `tls` 기본 **꺼짐**(동작 변화 없음). LAN 공개가 켜져 있으면 `status` 와 제어 센터가 "TLS 를 켜면 평문을 닫을 수 있다"고 알린다 |
| 새 설치 | 기본 꺼짐. 첫 부팅 마법사의 "LAN 공개" 선택 단계에서 TLS 를 함께 권한다(Q1) |
| 롤백(0.5.3 이미지) | 새 설정 키를 모르고 무시한다. 8080 이 다시 `0.0.0.0` 이므로 LAN 평문이 열린다. 릴리스 노트에 적는다 |

## 5. Vedecaps 연동(2차)

### 5.1 접속 시나리오

| 시나리오 | 방식 | Vedecaps 변경 필요 |
|---|---|---|
| 같은 컴퓨터 | `http://127.0.0.1:8080/v1`(자동 감지 또는 설정) | 없음 |
| LAN 의 다른 컴퓨터, TLS 켬 | `https://<이름>.local:8444/v1` + `options.ca` + `DIPARNUX_API_KEY` | **VE2** |
| LAN, TLS 꺼짐 | 평문(경고), 지금과 같음 | 없음 |
| VPN 안 | 평문 HTTP 가능(터널이 암호화) | 없음 |

### 5.2 `connect vedecaps --remote` 의 출력(제안)

```jsonc
{
  "provider": {
    "diparnux": {
      "name": "Diparnux",
      "api": "https://diparnux.local:8444/v1",
      "env": ["DIPARNUX_API_KEY"],
      "options": { "ca": "~/.config/vedecaps/diparnux-ca.pem", "max_concurrency": 2 },
      "models": { "<모델>": { "limit": { "context": 16384 } } }
    }
  }
}
```

- `--ca-out <파일>`: CA PEM 을 파일로 쓴다(다른 컴퓨터로 복사). 지문을 함께 출력해 사용자가 비교하게 한다.
- 설정의 `options.ca` 는 **파일 경로**만 쓴다. CA 내용을 설정에 인라인으로 넣지 않는다(설정은 자주 공유되고, 인라인 PEM 은 읽기 어렵다).
- `max_concurrency` 는 VE3 가 있는 Vedecaps 에서만 쓰이고, 옛 Vedecaps 는 무시한다.
- Vedecaps 가 `options.ca` 를 모르는 버전이면 인증서 오류가 난다. `connect vedecaps` 는 설치된 Vedecaps 의 `--version` 을 읽을 수 있으면 부족할 때 경고한다(0.1.3 이하).

### 5.3 CA 배포 방법(사용자 안내)

1. 서버에서 `diparnux tls ca --out diparnux-ca.pem` 과 `diparnux tls fingerprint`.
2. 파일을 클라이언트로 복사한다(USB, `scp` 등 이미 믿는 경로).
3. 클라이언트에서 지문을 계산해 서버의 지문과 비교한다(`openssl x509 -in diparnux-ca.pem -noout -fingerprint -sha256` 또는 Vedecaps 가 지문을 보여 주는 기능을 VE4 에 더할지 결정, Q6).
4. Vedecaps 설정의 `options.ca` 경로를 맞춘다.

## 6. 시험 계획

### 6.1 Go 단위·통합(`make test` 의 일부)

| # | 시험 | 기대 |
|---|---|---|
| U1 | CA 발급: `CA:TRUE`, `pathlen:0`, DNS 이름 제약, **IP 이름 제약이 없음**, 수명 | 속성 일치 |
| U2 | 서버 인증서가 CA 로 검증되고 SAN 에 호스트·`.local`·각 IP 를 포함 | `x509.Verify` 통과 |
| U3 | 이름 제약 밖의 DNS 이름(`example.com`) 발급 시도 | 검증 실패(CA 가 공개 사이트 위조에 쓰이지 않음) |
| U4 | 주소 집합이 바뀌면 서버 인증서만 재발급 | CA 지문 동일, 서버 지문 변경 |
| U5 | 만료 30일 전 갱신 | 시계를 주입해 확인 |
| U6 | 파일 권한 | 개인 키 0600, 디렉터리 0700 |
| U7 | 리스너 표(4.1) 의 모든 조합 | 각 조합의 주소와 포트 |
| U8 | HTTP→HTTPS 리다이렉트 없음 | 평문 요청이 3xx 를 받지 않음 |
| U9 | 두 리스너에서 키 규칙·429·`/diparnux/info` 동일 | 같은 응답 |
| U10 | `Validate` 의 포트 충돌 | 거부 |
| U11 | 개인 키가 API, 로그, 보고서에 없다 | 비밀 가림 시험 |
| U12 | webadmin 이 공통 부품으로 바뀐 뒤 기존 시험 | 모두 통과(회귀 없음) |

### 6.2 QEMU E2E(`check_tls`, 새 단계)

1. LAN 공개와 키를 켜고 `diparnux tls on`.
2. 게스트 안에서 `curl --cacert ca.pem https://<게스트 LAN 주소>:8444/diparnux/info` 가 성공한다.
3. CA 없이는 실패한다(`curl` 이 검증 오류). 틀린 CA 로도 실패한다.
4. `curl http://<게스트 LAN 주소>:8080/...` 는 **연결 거부**, `curl http://127.0.0.1:8080/...` 는 성공한다.
5. `lan_plain` 을 켜면 LAN 주소의 8080 이 다시 응답한다.
6. 주소를 바꿔(보조 주소 추가) 서버 인증서가 자동 재발급되고 CA 는 같다.
7. 재부팅 뒤 같은 CA 가 유지된다.

### 6.3 Vedecaps 호환(`make test-vedecaps-docker`)

- 새 시나리오: 실제 Vedecaps(VE2 포함 버전)가 `https://` + `options.ca` 로 게이트웨이에 접속, 스트리밍 응답을 받는다.
- 반대 시나리오: `options.ca` 없음 → 인증서 오류와 해결 안내(VE4 의 안내가 있다면 그 문구), `options.ca` 가 다른 CA → 거부.
- 리다이렉트 시나리오: 평문 포트가 리다이렉트를 하지 않는다는 것을 실제 Vedecaps 로 확인(기존 연결 유지).

### 6.4 다른 클라이언트(수동, 실기)

OpenAI Python SDK(`httpx` 의 `verify=ca.pem`), `curl`, 브라우저(CA 설치 후), Windows 11 Vedecaps. 이름 제약의 지원 여부를 포함한다.

## 7. 구현 순서(작업 묶음)

| 묶음 | 내용 | 선행 |
|---|---|---|
| W0 | 이 설계서 결정(10절), Vedecaps 쪽과 **TLS 계약 합의**([vedecaps-agent-handoff-0.5.4.md](vedecaps-agent-handoff-0.5.4.md) 2절) | — |
| W1 | `certstore` 패키지(webadmin 부품 이전, CA 발급, 시험 U1~U6, U12) | W0 |
| W2 | `gateway.Manager` 두 리스너, config, 방화벽, 노출 점검(U7~U10) | W1 |
| W3 | API, CLI, MCP, 연결 정보 | W2 |
| W4 | 제어 센터(서버 페이지, Vedecaps 페이지), 번역 | W3 |
| W5 | `connect vedecaps` 의 https·`options.ca`·`--ca-out` | W3, Vedecaps VE2 형식 합의 |
| W6 | E2E `check_tls`, 문서, 버전 0.5.4 | W2~W5 |
| W7 | Vedecaps 새 릴리스 고정(`diparnux.conf` 버전·SHA-256), 호환 시험 | Vedecaps 릴리스 |
| W8 | **경량화**: 램 목표 확정 후 줄이기(9절), 기준선 재측정 | 사용자의 램 목표 결정 |
| W9 | **GPU 서버 시작 실패** 원인 확인과 수정(9절) | 사용자 로그 |
| W10 | **실기 검증에서 나온 수정**(9절) | 실기 검증 결과 |

**릴리스 순서(결정 Q7)**: Vedecaps(VE2 포함)가 먼저 나와야 0.5.4 가 그 버전을 이미지에 고정할 수 있다. **0.5.4 는 Vedecaps 릴리스를 기다린 뒤 낸다.**
고정할 버전은 **Vedecaps 0.1.5**(VE2 포함, 사용자 결정 2026-10-14)다. `diparnux.conf` 의 `VEDECAPS_VERSION` 을 `0.1.5` 와 그 릴리스의 SHA-256 으로 바꾼다.
TLS 만 먼저 내지 않는다. 기다리는 동안 W1~W6 과 W8~W10 을 진행하고, W7 은 릴리스가 나온 뒤 마지막에 한다.
따라서 0.5.4 의 일정은 (1) Vedecaps 릴리스, (2) 램 목표 결정, (3) GPU 로그, (4) 실기 검증 결과에 달려 있다. 이 넷이 늦으면 0.5.4 도 늦는다(8절).

## 8. 위험과 한계

| 위험 | 내용 | 완화 |
|---|---|---|
| 이름 제약 지원 차이 | **Bun 은 CA 의 IP 제약을 처리하지 못한다**(`UNSUPPORTED_CONSTRAINT_TYPE`, 확인됨) → IP 제약을 넣지 않기로 했다. 일부 클라이언트(시스템 저장소)가 제약을 무시하거나 거부할 수 있다 | 실기 검증, 문제 시 CA 이름 제약 없이 발급하는 `--no-name-constraints` 대신 B 안(자체 서명 서버 인증서)을 쓸 수 있게 둔다 |
| 시계 오류 | 서버 시계가 틀리면 인증서가 "아직 유효하지 않음/만료"로 보인다. 노트북은 RTC 가 없는 가상 환경이 있다 | `NotBefore` 를 1시간 앞당긴다(webadmin 과 같음). `status` 가 시계를 점검 목록에 둔다 |
| 사용자가 평문을 되돌림 | `tls off`, `lan_plain` | 확인 요구, 노출 점검 경고가 계속 남는다 |
| 포트 충돌 | 8444 를 다른 서비스가 쓴다 | 시작 시 `cannot listen` 을 상태에 노출(지금 게이트웨이와 같음), 포트 변경 설정 |
| 갱신 중 연결 | 인증서가 바뀔 때 진행 중 스트림 | `GetCertificate` 가 새 연결에만 새 인증서를 준다. 진행 중 연결은 유지 |
| CA 분실·교체 | 클라이언트마다 다시 배포 | `renew-ca` 경고와 배포 안내, 지문 비교 |
| mDNS 이름 | 이름 해석이 안 되는 클라이언트(일부 Windows) | IP 도 SAN 에 있으므로 IP 주소로 접속 가능. 문서에 안내 |
| IPv6 | 게이트웨이가 IPv4 만 듣는다 | 0.5.4 범위 밖으로 둔다(SAN 에는 IPv6 주소를 넣되 듣지는 않음. Q5) |
| 일정 의존 | 0.5.4 가 Vedecaps 릴리스, 램 목표 결정, GPU 로그, 실기 검증 결과를 모두 기다린다(Q7, Q8) | 7절처럼 기다리지 않아도 되는 작업(W1~W6)부터 진행하고, 막힌 항목은 상태를 문서에 적어 둔다. 필요하면 사용자가 범위를 다시 정한다 |
| 실기 미검증 | 전부 QEMU 와 컨테이너에서만 확인 가능 | "정식 지원" 표기 금지, `phase-5-4-verification.md` 에 범위 명시 |

## 9. 묶음 R(0.5.4 에 포함, 사용자 결정 2026-10-14)

| 후보 | 상태 | 필요한 것 |
|---|---|---|
| 경량화: 헤드리스 ≤ 500 MiB, 데스크톱 ≤ 800 MiB | 목표 제안만 있음(`docs/memory.md` 3.3절). 기준선은 CI QEMU 값(헤드리스 약 374 MiB, 데스크톱 약 679 MiB) | **사용자의 목표 결정**. 줄일 곳은 Control Center(약 215 MB), Xorg(약 150 MB). 후보는 `phase-5-2-design.md` 의 아직 적용하지 않은 항목과 3.3절의 Control Center 페이지 지연 생성, `fcitx5` 필요 시 기동 |
| GPU 모델 서버 시작 실패 | 원인 불명. 로그 없이 추측으로 고치지 않는다 | **사용자 로그**: `sudo journalctl -u diparnux-llama -b -o cat --no-pager \| tail -60`, `-u diparnuxd`, llama-server 직접 실행 결과 |
| 실기 검증에서 나온 수정 | 아직 없음. 검증 항목은 `phase-5-3-verification.md` 와 `vedecaps.md` 9절 | 실기 검증 결과(GPU 코딩 모델 작업, agent 프로필 값, 원격 Vedecaps, 덮개·한글·Wi-Fi 등) |
| Vedecaps 새 버전 연동(D1~D5) | Vedecaps 릴리스 대기(다른 세션에서 개발 중) | `vedecaps-repo-changes-design.md` 7.4절 |

R 의 항목은 입력이 오기 전에는 구현 범위를 정할 수 없다. 입력이 오면 이 절을 갱신하고 W8~W10 의 상세를 `phase-5-4-verification.md` 에 적는다.

## 10. 결정이 필요한 것

| 번호 | 질문 | 기본값(제안) |
|---|---|---|
| Q1 | 새 설치의 TLS 기본값: 꺼짐 / LAN 공개를 켤 때 함께 켬 | LAN 공개를 켜는 단계에서 **함께 권하되 기본 켬** |
| Q2 | 인증서 모델: 로컬 CA + 서버 인증서(A) / 자체 서명 서버 인증서(B) | **A** |
| Q3 | HTTPS 포트 기본값 | **8444**(webadmin 의 8443 과 구별) |
| Q4 | TLS 를 켠 뒤, LAN 의 다른 컴퓨터가 쓰던 **평문 주소**(`http://서버:8080`)를 계속 열어 둘까, 닫을까. (아래 "Q4 설명" 참고) | **닫는다**(`lan_plain` 으로만 연다) |
| Q5 | IPv6 리스너를 0.5.4 에 넣는다 | **넣지 않는다** |
| Q6 | Vedecaps 의 CA 지문 표시 기능(VE4 확장)을 요청한다 | **요청한다**(선택 사항) |
| Q7 | Vedecaps 릴리스가 늦을 때: TLS 만 먼저 낸다 / 릴리스를 기다린다 | **결정함: 릴리스를 기다린다**(TLS 만 먼저 내지 않는다) |
| Q8 | R 묶음의 범위(경량화, GPU 수정)를 0.5.4 에 넣는다 / 0.5.5 로 분리 | **결정함: 0.5.4 에 넣는다**(분리하지 않는다) |
| Q9 | CA 에 이름 제약을 넣는다 | **DNS 이름 제약만 넣는다**(IP 제약은 Bun 의 한계로 넣지 않는다). 클라이언트 문제가 나오면 해제 옵션 |

### Q4 설명 (쉬운 말로)

지금(0.5.3)은 LAN 공개를 켜면 다른 컴퓨터가 `http://서버:8080` 으로 접속합니다. 이 길은 **암호화되어 있지 않아서** 같은 네트워크의 누군가가 API 키와 대화를 볼 수 있습니다.

0.5.4 에서 TLS 를 켜면 암호화된 새 길 `https://서버:8444` 가 **추가로** 생깁니다. 그러면 기존의 평문 길을 어떻게 할지가 문제입니다.

| 선택 | 결과 | 장단점 |
|---|---|---|
| **닫는다(제안)** | LAN 에서는 `https://서버:8444` 로만 접속된다 | 평문이 없어 안전하다. 대신 옛 주소(`http://서버:8080`)로 접속하던 다른 컴퓨터의 도구는 끊기고 주소를 바꿔야 한다 |
| 열어 둔다 | 평문과 암호화 길이 둘 다 LAN 에 열려 있다 | 옛 설정이 계속 동작한다. 하지만 누군가 평문 길을 쓰면 여전히 엿볼 수 있어 TLS 를 켠 의미가 줄어든다 |

**어느 쪽이든 같은 컴퓨터 안(`127.0.0.1:8080`)은 그대로 평문으로 동작합니다.** 같은 컴퓨터의 Vedecaps 와 자동 감지는 영향이 없습니다.
"닫는다"를 고르더라도 필요하면 설정(`lan_plain`)으로 평문 길을 다시 열 수 있게 하는 안입니다(명령 이름은 구현 때 4.5절에 맞춰 정합니다).

---

## 11. 이 문서의 검증 범위

- 1절의 "현재 상태"는 `main`(0.5.2)과 `phase-5-3` 코드, 문서에서 확인했다. 파일 이름과 함수 이름은 그 기준이다.
- Vedecaps 쪽 사실(1.1절)은 `f187450`(0.1.3) 소스를 읽어 확인했다. 실행 확인은 0.5.3 에서 한 5개 시나리오뿐이다.
- 4절 이후의 코드, 설정 이름, 포트, 수명은 **제안**이며 구현하거나 실행하지 않았다.
- 클라이언트별 이름 제약 지원, Bun `tls` 옵션의 신뢰 저장소 동작(기본에 더하는지 대체하는지), Windows 동작은 **확인하지 않았다.**
