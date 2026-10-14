# 0.5.4 검증 범위 (개발 중)

- 상태: **개발 완료 직전.** TLS(묶음 T)와 Vedecaps 0.1.5 고정(W7)을 끝냈다. 램 목표는 확정했고 GPU 수정은 보류, 실기 수정은 실기 검증 결과 대기다.
- 설계: [phase-5-4-design.md](phase-5-4-design.md). 사용: [tls.md](tls.md).

## 1. 확인한 것

| 항목 | 방법 | 결과 |
|---|---|---|
| CA 와 서버 인증서 | `certstore` 단위 시험(10개): CA 속성(`CA:TRUE`, `pathlen:0`, DNS 제약만, **IP 제약 없음**), 모든 SAN 이름 검증, 제약 밖 이름 거부, 주소 변경 시 서버 인증서만 재발급, 만료 전 갱신, 파일 권한 0600/0700, 실제 핸드셰이크(CA 있음/없음/갱신 후) | 통과 |
| 두 리스너 | 게이트웨이 시험: 평문과 TLS 가 같은 핸들러, 같은 `X-Opereta`, **리다이렉트 없음**, 같은 키 규칙, TLS 끄면 TLS 리스너만 닫힘, 켜면 인증서 재준비, 인증서 실패 시 평문은 영향 없음(`-race`) | 통과 |
| 설정 | TLS 를 켜지 않은 설정 파일에는 TLS 키가 쓰이지 않음, `tls_port` 가 `port`·`internal_port`·SSH 포트와 같으면 거부, 이름 형식 | 통과 |
| 방화벽, 노출 점검 | TLS 가 떠 있으면 평문 포트를 LAN 에 열지 않고 TLS 포트를 연다(`lan_plain` 이면 둘 다). 노출 점검이 TLS 정상/평문 열림 경고/암호화 안 됨 안내를 낸다 | 통과 |
| API, CLI | `GET/PUT /v1/tls`, `POST /v1/tls/renew`, `GET /v1/tls/ca`. 켜기 사전 조건(LAN, 게이트웨이, 키, 포트 충돌), 거부된 요청은 설정을 바꾸지 않음, 개인 키가 응답에 없음. CLI 확인 요구(`off`, `plain on`, `renew --ca`는 `--yes` 없이 요청을 보내지 않음) | 통과 |
| 연결 정보, `connect vedecaps` | TLS 가 켜지면 LAN 주소와 mDNS 주소가 https 와 TLS 포트이고 `LocalURL` 은 평문 그대로. 예시(curl, Python, Vedecaps)에 CA. `options.ca` 는 https 주소에만, 키 값은 쓰지 않음, `--ca-out` | 통과 |
| 제어 센터 | 서버 페이지의 TLS 패널(스위치, 지문, 복사, 갱신), 한국어 번역, 논리 시험, API 클라이언트 시험 | 통과(GUI 80개) |
| QEMU E2E `check_tls` | 드라이버 시험(가짜 QEMU)과 실제 이미지(CI, 2절) | 드라이버 19개 통과, CI 통과 |

## 2. CI 에서 확인할 것(PR 의 실제 ISO E2E)

`check_tls` 가 실제 게스트에서 다음을 확인한다. 이 항목은 CI 결과가 나오면 아래 표를 채운다.

1. LAN 공개와 키가 있을 때 `tls on` 이 CA 와 서버 인증서를 만들고 8444 에서 듣는다.
2. 8080 이 `127.0.0.1` 에서만 듣는다.
3. CA 를 준 클라이언트는 이 컴퓨터의 LAN 주소로 https 접속이 된다(`/opereta/info`, `X-Opereta`, 리다이렉트 없음).
4. CA 가 없는 클라이언트와 다른 서버 이름은 인증서 검증에서 거부된다.
5. LAN 주소의 평문 8080 은 연결이 거부되고 `127.0.0.1:8080` 은 응답한다.
6. 서버 인증서 갱신 뒤에도 같은 CA 로 접속된다. `tls plain on --yes` 이면 평문이 LAN 에 다시 열린다.
7. 노출 점검과 연결 정보가 TLS 를 알린다. `connect vedecaps --remote` 가 https 와 `options.ca` 를 쓴다.
8. 끝나면 TLS 와 LAN 공개와 키를 원래대로 돌린다.

CI 실행 38506021343(PR #13, 헤드 `4b1294d`): lint, ISO 빌드와 부팅, CPU 추론 E2E 전체 통과.

| 단계 | CI 결과 |
|---|---|
| `check_tls` (위 1~8) | **통과**. 실제 이미지에서 `tls on`, 포트 점검, https 접속(CA 있음/없음/다른 이름), 평문 닫힘, 노출 점검, 연결 정보, `connect vedecaps`, 서버 인증서 갱신, `lan_plain` 을 모두 실행했고 어느 단계도 실패하지 않았다 |

주의: 이 E2E 는 게스트가 **자기 자신의 LAN 주소**로 접속한다. 다른 컴퓨터에서의 접속이 아니다(3절).

## 3. 확인하지 못한 것

- **다른 컴퓨터에서의 실제 접속**: Windows, macOS, 휴대폰, 브라우저. QEMU 안에서는 자기 자신의 LAN 주소로만 접속한다.
- **Vedecaps 0.1.5 와의 호환**: 4절에서 확인했다(실제 Vedecaps 0.1.5, 실제 `certstore` 의 CA). 남은 것은 다른 컴퓨터에서의 접속이다.
- **시스템 저장소의 이름 제약**: Windows, macOS 가 CA 의 DNS 제약을 지키는지.
- **IPv6**: 리스너와 인증서 모두 IPv4 만 다룬다.
- **시계 오류**, **호스트 이름이 바뀌는 상황**, **여러 NIC 가 있는 컴퓨터**: 자동 재발급 논리는 단위 시험으로 확인했지만 실제 환경에서는 보지 않았다.

## 4. Vedecaps 0.1.5 고정과 호환 (W7)

릴리스 보고(`vedecaps-0.1.5-release-report.md`)를 읽고 진행했다. **보고의 체크섬은 직접 확인했다**: 릴리스에서 `vedecaps-linux-x64.tar.gz` 와
`checksums.txt` 를 내려받아 SHA-256 이 `46e645ba…439016f` 로 일치했다(보고, `checksums.txt`, 파일 해시 세 값이 같다).

| 항목 | 결과 |
|---|---|
| `opereta.conf` | `VEDECAPS_VERSION="0.1.5"`, `VEDECAPS_SHA256="46e645ba6e8d7a98189d78e224c2c31704953dc6314907a2065455fe1439016f"`. `fetch.sh` 가 같은 값으로 `verified` |
| 호환 시험 `make test-vedecaps-docker`(실제 Vedecaps 0.1.5, Debian 13 x86-64 컨테이너) | 기존 5개 시나리오 통과 + 새 시나리오 통과 |
| F: https, 이름(`box.local`)과 CA | 통과 |
| F2: https, **IP 주소**(`127.0.0.1`)와 CA — **DNS 제약만 있는 CA 와 IP SAN 인증서의 조합** | 통과(이 조합이 Bun 에서 통과하는지가 열린 질문이었다. 실제 `certstore` 의 CA 로 확인했다) |
| G: CA 를 적지 않음 | 거부, 오류가 `opereta tls ca` 를 안내 |
| H: 다른 CA | 거부, 같은 안내 |
| J: `vedecaps debug local` | `Opereta 0.5.4 · server running · 2 slots · 16,384 ctx per slot` 로 Opereta 서버를 알아봄(VE1) |

보고서의 "계약에서 어긋난 것"에 대한 응답:

1. **CA 이름 제약은 DNS 만**: 이미 반영했다(`certstore`, U1 이 IP 제약이 없음을 확인).
2. **명령 이름**: Vedecaps 의 오류 안내가 가리키는 `opereta tls ca`, `opereta tls status`(= `opereta tls`), `opereta tls renew` 가 모두 있다.
3. **지문 형식**: `AA:BB:…` 대문자 콜론 형식이 `opereta tls ca --out`, `opereta tls fingerprint`, 연결 정보와 같다.
4. **https 는 자동 감지하지 않음**: 계약 C2 대로다. `connect vedecaps --remote` 가 `api` 에 https 주소와 `options.ca` 를 쓴다.
5. **`.local`·사설 IP 가 아닌 이름**: `options.ca` 를 적으면 Vedecaps 가 로컬로 취급한다. `opereta tls names` 로 더한 이름을 쓰는 경우 `ca` 를 적어야 한다(`connect vedecaps` 는 https 주소에 항상 `options.ca` 를 쓴다).

확인하지 못한 것(보고서와 같다): 실제 Opereta 0.5.4 서버에 대한 **다른 컴퓨터에서의** https, 서버 인증서 자동 갱신 중의 연결, 시계 어긋남, Debian·Alpine 의 x64 에서의 Vedecaps TLS 시험.

## 5. 경량화(묶음 R)와 GPU

- **램 목표 확정(2026-10-14, 사용자)**: 헤드리스 500 MiB, 데스크톱 800 MiB(상한). 기준선 374/679 MiB 로 이미 이내이며, 데스크톱 E2E 가 상한을 넘으면 실패하도록 했다(`tests/qemu/desktop-session.py`). 자세한 것은 [memory.md](memory.md).
  추가로 줄이는 작업(Control Center 페이지 지연 생성, `fcitx5` 필요 시 기동)은 **하지 않았다**: 상한을 이미 만족하고, 기준선이 QEMU 값이라 실기 측정 없이 구조를 바꾸면 효과를 확인할 수 없다.
- **GPU 서버 시작 실패**: 사용자가 **보류**했다(2026-10-14). 로그를 받기 전에는 추측으로 고치지 않는다.
- **실기 검증에서 나온 수정**: 실기 검증 결과가 아직 없다.

## 6. 남은 일

| 항목 | 상태 |
|---|---|
| 버전 0.5.4 | 올렸다(`opereta.conf`, GUI, 모든 packages changelog) |
| CI(ISO 빌드, 부팅, E2E, 설치기) | 실행 38512753440(헤드 `9b4e920`) 전체 통과: lint, ISO 빌드와 부팅, CPU 추론 E2E(`check_tls` 포함), `test-vedecaps-docker`(Vedecaps 0.1.5), 데스크톱 E2E, 설치기 E2E, 제어 센터 시험 |

CI 에서 잰 유휴 램(데스크톱 E2E, QEMU): **서버 모드 371 MiB(상한 500), 데스크톱 모드 703 MiB(상한 800).**
0.5.2 기준선(374/679 MiB)과 비교하면 서버 모드는 같고 데스크톱 모드는 약 24 MiB 늘었다. 한 번의 측정이라 변동인지 0.5.3·0.5.4 의 제어 센터 변경(Vedecaps 페이지, TLS 패널)의 영향인지는 구분하지 못했다. 여유는 97 MiB 다.
| 다른 컴퓨터에서의 실제 접속, Windows·macOS | 실기 검증 대기 |
| R-GPU | 보류 |
| R-실기 | 실기 검증 결과 대기 |

## 7. 설계와 다르게 한 것

- webadmin 의 인증서 코드를 공통 패키지로 옮기지 않고 그대로 뒀다(`certstore` 는 새 패키지). 동작 변경 위험을 피하려는 선택이다.
- `tls on` 은 LAN 공개가 꺼져 있으면 거부한다(설계의 사전 조건과 같다). 설정 파일에 `tls` 만 켜 두는 경우(LAN 공개를 나중에 켬)는 LAN 이 켜질 때 활성화된다.
- `config set tls ...` 는 만들지 않고 `opereta tls ...` 만 둔다.
