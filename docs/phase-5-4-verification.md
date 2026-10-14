# 0.5.4 검증 범위 (개발 중)

- 상태: **개발 중.** TLS(묶음 T)는 구현했고 시험을 통과했다. Vedecaps 연동의 고정(W7)과 묶음 R(경량화, GPU 수정, 실기 수정)은 입력 대기다.
- 설계: [phase-5-4-design.md](phase-5-4-design.md). 사용: [tls.md](tls.md).

## 1. 확인한 것

| 항목 | 방법 | 결과 |
|---|---|---|
| CA 와 서버 인증서 | `certstore` 단위 시험(10개): CA 속성(`CA:TRUE`, `pathlen:0`, DNS 제약만, **IP 제약 없음**), 모든 SAN 이름 검증, 제약 밖 이름 거부, 주소 변경 시 서버 인증서만 재발급, 만료 전 갱신, 파일 권한 0600/0700, 실제 핸드셰이크(CA 있음/없음/갱신 후) | 통과 |
| 두 리스너 | 게이트웨이 시험: 평문과 TLS 가 같은 핸들러, 같은 `X-Diparnux`, **리다이렉트 없음**, 같은 키 규칙, TLS 끄면 TLS 리스너만 닫힘, 켜면 인증서 재준비, 인증서 실패 시 평문은 영향 없음(`-race`) | 통과 |
| 설정 | TLS 를 켜지 않은 설정 파일에는 TLS 키가 쓰이지 않음, `tls_port` 가 `port`·`internal_port`·SSH 포트와 같으면 거부, 이름 형식 | 통과 |
| 방화벽, 노출 점검 | TLS 가 떠 있으면 평문 포트를 LAN 에 열지 않고 TLS 포트를 연다(`lan_plain` 이면 둘 다). 노출 점검이 TLS 정상/평문 열림 경고/암호화 안 됨 안내를 낸다 | 통과 |
| API, CLI | `GET/PUT /v1/tls`, `POST /v1/tls/renew`, `GET /v1/tls/ca`. 켜기 사전 조건(LAN, 게이트웨이, 키, 포트 충돌), 거부된 요청은 설정을 바꾸지 않음, 개인 키가 응답에 없음. CLI 확인 요구(`off`, `plain on`, `renew --ca`는 `--yes` 없이 요청을 보내지 않음) | 통과 |
| 연결 정보, `connect vedecaps` | TLS 가 켜지면 LAN 주소와 mDNS 주소가 https 와 TLS 포트이고 `LocalURL` 은 평문 그대로. 예시(curl, Python, Vedecaps)에 CA. `options.ca` 는 https 주소에만, 키 값은 쓰지 않음, `--ca-out` | 통과 |
| 제어 센터 | 서버 페이지의 TLS 패널(스위치, 지문, 복사, 갱신), 한국어 번역, 논리 시험, API 클라이언트 시험 | 통과(GUI 80개) |
| QEMU E2E `check_tls` | 드라이버 시험(가짜 QEMU)이 새 단계를 포함해 통과. 실제 이미지에서의 실행은 CI 에서 확인한다(아래) | 드라이버 19개 통과 |

## 2. CI 에서 확인할 것(PR 의 실제 ISO E2E)

`check_tls` 가 실제 게스트에서 다음을 확인한다. 이 항목은 CI 결과가 나오면 아래 표를 채운다.

1. LAN 공개와 키가 있을 때 `tls on` 이 CA 와 서버 인증서를 만들고 8444 에서 듣는다.
2. 8080 이 `127.0.0.1` 에서만 듣는다.
3. CA 를 준 클라이언트는 이 컴퓨터의 LAN 주소로 https 접속이 된다(`/diparnux/info`, `X-Diparnux`, 리다이렉트 없음).
4. CA 가 없는 클라이언트와 다른 서버 이름은 인증서 검증에서 거부된다.
5. LAN 주소의 평문 8080 은 연결이 거부되고 `127.0.0.1:8080` 은 응답한다.
6. 서버 인증서 갱신 뒤에도 같은 CA 로 접속된다. `tls plain on --yes` 이면 평문이 LAN 에 다시 열린다.
7. 노출 점검과 연결 정보가 TLS 를 알린다. `connect vedecaps --remote` 가 https 와 `options.ca` 를 쓴다.
8. 끝나면 TLS 와 LAN 공개와 키를 원래대로 돌린다.

| 단계 | CI 결과 |
|---|---|
| `check_tls` | 대기 |

## 3. 확인하지 못한 것

- **다른 컴퓨터에서의 실제 접속**: Windows, macOS, 휴대폰, 브라우저. QEMU 안에서는 자기 자신의 LAN 주소로만 접속한다.
- **Vedecaps 0.1.5 와의 호환**: `options.ca` 를 믿는지, Bun 이 DNS 제약만 있는 CA 와 IP SAN 인증서를 통과시키는지는 Vedecaps 쪽 시험(TC11)과 W7 의 `make test-vedecaps-docker` 가 확인한다. **0.1.5 가 나오기 전에는 확인되지 않았다.**
- **시스템 저장소의 이름 제약**: Windows, macOS 가 CA 의 DNS 제약을 지키는지.
- **IPv6**: 리스너와 인증서 모두 IPv4 만 다룬다.
- **시계 오류**, **호스트 이름이 바뀌는 상황**, **여러 NIC 가 있는 컴퓨터**: 자동 재발급 논리는 단위 시험으로 확인했지만 실제 환경에서는 보지 않았다.

## 4. 남은 일

| 묶음 | 내용 | 대기하는 것 |
|---|---|---|
| W7 | `diparnux.conf` 의 Vedecaps 를 0.1.5 와 그 SHA-256 으로 고정, `make test-vedecaps-docker` 에 https 시나리오 | Vedecaps 0.1.5 릴리스 |
| 버전 | 0.5.4 로 올리기(`diparnux.conf`, GUI `__version__`, 패키지 changelog) | W7 |
| R-경량화 | 램 목표 확정과 줄이기 | 사용자의 목표 결정 |
| R-GPU | GPU 서버 시작 실패 원인 확인과 수정 | 사용자 로그 |
| R-실기 | 실기 검증에서 나온 수정 | 실기 검증 결과 |

## 5. 설계와 다르게 한 것

- webadmin 의 인증서 코드를 공통 패키지로 옮기지 않고 그대로 뒀다(`certstore` 는 새 패키지). 동작 변경 위험을 피하려는 선택이다.
- `tls on` 은 LAN 공개가 꺼져 있으면 거부한다(설계의 사전 조건과 같다). 설정 파일에 `tls` 만 켜 두는 경우(LAN 공개를 나중에 켬)는 LAN 이 켜질 때 활성화된다.
- `config set tls ...` 는 만들지 않고 `diparnux tls ...` 만 둔다.
