# 방화벽 규칙

- 확인 날짜: 2026-10-14
- 상태: 구현됨(0.5.2). 규칙 생성은 고정 출력으로, 적용은 QEMU E2E 에서 `nft list` 로 확인했다. 실기 네트워크에서의 동작은 미검증이다.

기본 정책은 **들어오는 연결 차단**이다. 이 컴퓨터가 시작한 연결의 응답, 루프백, ICMP, DHCP 는 항상 통과한다. 허용 규칙만 있고 차단 규칙은 없다.

## 열린 포트의 두 종류

| 종류 | 예 | 바꾸는 법 |
|---|---|---|
| 기능이 여는 포트(읽기 전용) | 모델 서버 API(LAN 공개), SSH(포트 설정 따름), mDNS 5353(LAN 공개), WireGuard UDP, 원격 데스크톱 3389, 웹 관리 | 해당 기능을 켜고 끈다 |
| 사용자 규칙 | 이름, 프로토콜(tcp·udp), 포트 또는 범위, 출발지(`any`, `lan`, CIDR), 사용 여부 | `opereta firewall` 또는 제어 센터 방화벽 페이지 |

`lan` 은 `10/8`, `172.16/12`, `192.168/16`, `169.254/16`, IPv6 `fc00::/7`·`fe80::/10` 이다. VPN(`wg0`)은 켜져 있을 때 들어오는 연결을 모두 신뢰한다.

```sh
opereta firewall                                   # 기능이 여는 포트와 내 규칙
opereta firewall add webui tcp 3000 --from lan
opereta firewall add range udp 6000-6010 --from 192.168.1.0/24
opereta firewall disable r1 ; opereta firewall enable r1 ; opereta firewall rm r1
```

## 안전 장치

- 새 규칙 파일은 `nft -c`(문법 검사)를 통과해야 적용한다. 실패하면 이전 규칙과 저장된 파일이 그대로이고 방금 만든 규칙은 되돌린다.
- 이름은 인쇄 가능한 ASCII 64자 이하이고 따옴표, 역슬래시, `#` 를 쓸 수 없다. 출발지는 주소 범위만 받는다(`0.0.0.0/0` 은 `any` 를 쓴다). 규칙은 최대 64개.
- 규칙은 `/var/lib/opereta/firewall-rules.json`(0600)에 있고 데몬이 `table inet opereta` 만 바꾼다. 다른 테이블은 건드리지 않는다.
