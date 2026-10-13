# 네트워크와 Wi-Fi

- 확인 날짜: 2026-10-13
- 상태: 구현됨(0.4.1). 데몬 논리는 실제 `busctl`과 가짜 iwd 서비스로 확인했고, 화면과 CLI는 가짜 데몬으로 확인했다. **실제 Wi-Fi 칩과 공유기에서의 연결은 미검증이다.**

유선은 systemd-networkd 가 DHCP 로 맡고, 무선은 iwd 가 맡는다. 사용자가 쓰는 곳은 세 가지다.

| 방법 | 명령 또는 위치 |
|---|---|
| Control Center | 네트워크 페이지(어댑터, Wi-Fi 목록, 연결, 비밀번호 입력) |
| CLI | `diparnux network`, `diparnux network wifi list\|scan\|connect <ssid>\|disconnect\|forget <ssid>\|on\|off` |
| 패널 | 오른쪽 위의 `Wi-Fi <이름>`, `Wired`, `Offline` 표시. 누르면 네트워크 페이지가 열린다 |

```sh
diparnux network                          # 어댑터, 주소, 게이트웨이, Wi-Fi 상태
diparnux network wifi scan                # 검색을 시작한다
diparnux network wifi list                # * 연결됨, + 저장됨
diparnux network wifi connect "내 Wi-Fi"   # 비밀번호를 터미널에서 묻는다
printf '%s\n' "$PW" | diparnux network wifi connect "내 Wi-Fi" --password-stdin
```

## 동작 방식

- 데몬(`diparnuxd`)이 iwd 의 D-Bus(`net.connman.iwd`)를 `busctl --json=short` 로 부른다. GUI 와 CLI 는 데몬의 `/v1/network/...` 만 쓴다.
- 비밀번호는 요청 본문으로만 오가며 명령 줄, 로그, 응답에 남지 않는다. 데몬이 iwd 설정 파일(`/var/lib/iwd/<SSID>.psk`, 모드 0600)을 쓰고 iwd 가 읽을 때까지 기다린 뒤 연결한다. 연결에 실패하면 이번에 만든 파일을 지운다.
- 이미 저장된 네트워크는 비밀번호 없이 다시 연결한다. 공개(open) 네트워크는 비밀번호가 필요 없다.
- 지원하지 않는 것: 기업용 802.1X, WEP, 숨은 SSID, 프록시. 유선 고정 IP 와 VPN 은 아래 0.5.2 절과 [원격 접속](remote-access.md)에 있다.
- SSID 에 한글이 있어도 목록에 나온다. 텍스트 콘솔의 CLI 출력은 ASCII 만 쓰므로 한글은 `?` 로 나온다(정확한 이름은 `--json`).

## 문제 해결

| 증상 | 확인 |
|---|---|
| "The wireless service is not running" | `systemctl status iwd` |
| "No wireless adapter was found" | `lspci -nn \| grep -i network`, `rfkill list`, 칩의 펌웨어. 이미지에는 Intel 펌웨어만 들어 있다 |
| 목록이 비어 있다 | 검색을 누르고 몇 초 뒤 새로 고침. Wi-Fi 가 꺼져 있는지(`rfkill`, 전원 스위치) 확인 |
| 연결이 실패한다 | 비밀번호 확인. 5 GHz 채널과 지역 설정(`wireless-regdb`는 들어 있다) |
| 유선인데 오프라인이다 | `networkctl status`, DHCP 서버, 케이블 |

## 유선 설정, 호스트 이름, 깨우기, 진단(0.5.2)

- 확인 날짜: 2026-10-14. 상태: 구현됨. 조각 파일 생성·되돌리기는 시험으로 확인했다. **실기 어댑터에서 고정 IP 적용과 Wake-on-LAN 은 미검증이다.**

```sh
diparnux network config                                         # 유선 설정, 호스트 이름, 깨우기
diparnux network set enp3s0 --address 192.168.0.50/24 --gateway 192.168.0.1 --dns 1.1.1.1
diparnux network confirm                                        # 60초 안에 확인하지 않으면 되돌린다
diparnux network set enp3s0 --dhcp
diparnux network hostname diparnux-01                           # 호스트 이름과 /etc/hosts
diparnux network wol on                                         # 다음 시작부터 적용
diparnux network diagnose                                       # 주소, 경로, 게이트웨이, DNS, 인터넷
```

- 유선 설정은 `/etc/systemd/network/10-diparnux-<이름>.network` 조각으로 쓴다(권한 있는 헬퍼). DHCP 이고 DNS 재정의가 없으면 조각을 두지 않는다(이미지 기본값).
- **확인 또는 자동 복구**: 변경은 적용한 뒤 `--confirm` 초(기본 60) 안에 `network confirm` 을 부르지 않으면 이전 설정으로 돌아온다. 되돌릴 정보는 디스크에도 남으므로 그 사이에 데몬이 다시 시작되어도 되돌린다. 무선은 DHCP 만 지원한다.
- **mDNS**: `<호스트 이름>.local` 로 찾을 수 있다(resolved 의 mDNS 응답자, 새 패키지 없음). UDP 5353 은 LAN 공개를 켰을 때만 방화벽이 연다.
