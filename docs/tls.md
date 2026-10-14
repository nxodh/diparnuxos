# 게이트웨이 TLS (0.5.4)

- 상태: 구현됨(0.5.4 개발 브랜치). Go 시험, 컨테이너 시험, QEMU E2E(`check_tls`)로 확인했다. **다른 컴퓨터에서의 실제 접속(Windows, macOS, 휴대폰, 브라우저)은 미검증이다.**
- 설계와 결정: [phase-5-4-design.md](phase-5-4-design.md). 검증 범위: [phase-5-4-verification.md](phase-5-4-verification.md).

## 무엇을 하나

LAN 공개를 켜면 다른 컴퓨터가 `http://서버:8080` 으로 접속한다. 이 길은 **암호화되어 있지 않아서** 같은 네트워크의 누군가가 API 키와 대화를 볼 수 있고,
응답을 바꿔 코딩 에이전트에 도구 호출을 끼워 넣을 수도 있다. TLS 를 켜면 암호화된 길 `https://서버:8444` 가 생기고 LAN 의 평문 길은 닫힌다.

| | 평문 포트 8080 | TLS 포트 8444 |
|---|---|---|
| TLS 꺼짐 | 이 컴퓨터만(LAN 공개를 켜면 LAN 도) | 없음 |
| TLS 켜짐 | **이 컴퓨터만**(127.0.0.1) | LAN |
| TLS 켜짐 + `lan_plain` | 이 컴퓨터와 LAN(암호화 안 됨) | LAN |

- 이 컴퓨터의 `http://127.0.0.1:8080` 은 항상 그대로 쓴다. 같은 컴퓨터의 Vedecaps 자동 감지와 기존 도구가 바뀌지 않는다.
- 두 포트는 같은 게이트웨이를 쓴다. API 키 규칙, 429, `/opereta/info` 가 같다. **리다이렉트는 하지 않는다**(Vedecaps 는 리다이렉트를 오류로 본다).
- 이 컴퓨터에서 온 요청은 `local_no_key` 로 키 없이 통과한다(TLS 포트도 같다).

## 켜기

사전 조건: LAN 공개와 게이트웨이가 켜져 있고 API 키가 하나 이상 있다.

```
opereta apikey create laptop        # 키는 만들 때 한 번만 보인다
opereta config set lan on
opereta tls on                      # CA 와 서버 인증서를 만들고 8444 에서 듣는다
opereta tls                         # 상태, 지문, 만료일, 이름
```

제어 센터의 서버 페이지에도 "암호화(TLS)" 스위치, CA 지문, "CA 인증서 복사", "인증서 갱신"이 있다.

## 다른 컴퓨터에서 쓰기

1. 이 컴퓨터: `opereta tls ca --out opereta-ca.pem` 과 `opereta tls fingerprint`
2. 파일을 그 컴퓨터로 복사한다(이미 믿는 경로: USB, `scp`).
3. 그 컴퓨터에서 지문을 계산해 비교한다: `openssl x509 -in opereta-ca.pem -noout -fingerprint -sha256`
4. 접속한다.

```
curl --cacert opereta-ca.pem -H 'Authorization: Bearer <키>' https://opereta.local:8444/v1/models
```

```python
import httpx
from openai import OpenAI
client = OpenAI(base_url="https://opereta.local:8444/v1", api_key="<키>", http_client=httpx.Client(verify="opereta-ca.pem"))
```

**클라이언트에서 인증서 검사를 끄지 않는다.** 검사를 끄면 암호화의 이점이 없다(중간자가 같은 연결을 가로챌 수 있다).

### Vedecaps

```
opereta connect vedecaps --remote --ca-out opereta-ca.pem
```

주소는 `https://…:8444/v1`, 설정에는 `options.ca`(CA 파일 경로)와 `{env:OPERETA_API_KEY}` 가 들어간다. CA 파일을 Vedecaps 를 실행하는 컴퓨터의
`~/.config/vedecaps/opereta-ca.pem`(또는 `--ca PATH` 로 정한 경로)에 둔다. **`options.ca` 를 믿는 것은 Vedecaps 0.1.5 이상이다.**
이전 버전은 이 키를 무시하므로 인증서 오류가 난다. 자세한 사용은 [vedecaps.md](vedecaps.md).

## 인증서

- **로컬 CA**: ECDSA P-256, 유효 10년, `CA:TRUE`, `pathlen:0`. **DNS 이름 제약**만 있다(`.local`, `localhost`, 이 컴퓨터의 호스트 이름). **IP 이름 제약은 없다**:
  Bun 은 CA 에 IP 제약이 있고 서버 인증서에 IP SAN 이 있으면 검증하지 못한다(`UNSUPPORTED_CONSTRAINT_TYPE`). 그 대가로 CA 개인 키가 유출되면
  임의의 IP 주소용 인증서를 만들 수 있다. DNS 이름은 제약 밖을 위조할 수 없다.
- **서버 인증서**: 397일, 만료 30일 전과 주소가 바뀔 때 자동 갱신. SAN 은 호스트 이름, `<호스트>.local`, `localhost`, 이 컴퓨터의 IPv4 주소,
  `127.0.0.1`, `::1`, 그리고 `opereta tls names` 로 더한 이름(CA 를 만들 때 포함된 이름만 들어간다. 다른 이름은 `renew --ca` 가 필요하다).
- 갱신은 새 연결부터 반영된다(리스너를 다시 열지 않는다). CA 가 같으면 클라이언트는 아무것도 다시 하지 않아도 된다.
- 파일: `/var/lib/opereta/tls/{ca.pem, ca-key.pem, server.pem, server-key.pem}`. 개인 키는 `0600`, 디렉터리는 `0700`. 개인 키는 API, 로그, 보고서, MCP 에 나오지 않는다.

```
opereta tls renew                   # 서버 인증서만 다시 만든다. CA 는 그대로
opereta tls renew --ca --yes        # CA 까지. 모든 클라이언트가 새 CA 를 다시 받아야 한다
opereta tls names ai.lan            # 인증서에 더 넣을 이름(CA 에 포함된 도메인만)
```

## 끄기와 평문

```
opereta tls off --yes               # LAN 의 평문이 다시 열린다
opereta tls plain on --yes          # TLS 를 켠 채 평문도 LAN 에 열어 둔다(옛 클라이언트용. 암호화 안 됨)
opereta tls plain off
```

노출 점검(`opereta exposure`)이 상태를 알려 준다: TLS 가 켜져 있고 평문이 닫혀 있으면 정상, `lan_plain` 이면 경고, TLS 가 꺼져 있으면 암호화되지 않았다는 안내.

## 설정 파일과 되돌리기

`tls`, `tls_port`, `lan_plain`, `tls_names` 는 켜지 않으면 설정 파일에 쓰이지 않는다. TLS 를 켜지 않은 설정 파일은 0.5.3 과 같아서 이전 버전으로 되돌려도 읽힌다.
TLS 를 켠 뒤에는 이전 버전이 이 키들을 모르는 필드로 거부하므로, 되돌리기 전에 `opereta tls off --yes` 로 끈다(키가 파일에서 사라진다).

## 한계

- IPv6: 게이트웨이는 IPv4(`0.0.0.0`)만 듣는다. 인증서에는 IPv6 주소를 넣지 않는다.
- Windows, macOS 의 시스템 저장소가 CA 의 DNS 이름 제약을 지키는지는 확인하지 않았다(curl, Python, Bun 은 CA 파일을 직접 지정한다).
- 서버 컴퓨터의 시계가 많이 틀리면 인증서가 "아직 유효하지 않음"이나 "만료"로 보인다.
- 원격 관리 웹(webadmin)은 별도의 자체 서명 인증서를 쓴다(그대로 둔다).
