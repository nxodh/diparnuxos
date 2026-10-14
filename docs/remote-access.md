# 원격 접속: SSH 고급, 원격 데스크톱, VPN, 웹 관리

- 확인 날짜: 2026-10-14
- 상태: 구현됨(0.5.2). Go 시험과 QEMU E2E(키 등록, 웹 관리 로그인과 허용 목록, VPN 상태, 원격 데스크톱 꺼짐)로 확인했다. **다른 컴퓨터에서의 실제 접속(RDP 클라이언트, WireGuard 피어, 브라우저)은 미검증이다.**

새 기능은 모두 **기본으로 꺼져 있다**. 켜기 전에는 포트가 열리지 않고 프로세스가 뜨지 않는다.

## SSH 고급

```sh
opereta ssh advanced              # 포트, 비밀번호 로그인 설정, 키 수, 서버 지문
opereta ssh port 2222             # sshd 설정과 방화벽이 함께 바뀐다
opereta ssh keys add alice --file ~/.ssh/id_ed25519.pub
opereta ssh keys list alice ; opereta ssh keys rm alice SHA256:...
opereta ssh password off          # 공개 키가 하나도 없으면 거부된다
opereta ssh log                   # 최근 접속과 실패
```

- 설정은 `/etc/ssh/sshd_config.d/40-opereta-ssh.conf` 한 파일이다. 쓰기 전에 `sshd -t` 로 검사하고 실패하면 이전 파일로 되돌린다.
- 포트를 바꿀 때는 새 포트를 방화벽에 먼저 연 뒤 sshd 를 다시 읽는다. 연결된 세션은 끊기지 않는다.
- 공개 키는 `ssh-ed25519`, `ssh-rsa`, `ecdsa-*`, `sk-*` 형식만 받고 옵션이 붙은 줄은 거부한다. 홈의 `authorized_keys` 는 권한 있는 헬퍼(`opereta-admin`)가 쓴다.

## 원격 데스크톱(RDP)

```sh
opereta remote-desktop on --from lan      # xrdp 시작, 3389/tcp 를 LAN 에만 연다
opereta remote-desktop off
```

이미지에 `xrdp` 와 `xorgxrdp` 가 들어 있고 서비스는 꺼져 있다. 켜면 서비스를 시작하고 부팅 때 시작하며 포트를 정한 범위(`lan`, `any`, CIDR)에만 연다.

## VPN(WireGuard)

```sh
opereta vpn on                              # wg0, 기본 10.8.0.1/24, UDP 51820
opereta vpn peer add phone --out phone.conf # 기기의 키는 여기서 만들고 설정은 한 번만 준다
opereta vpn peer add laptop --public-key <기기가 만든 공개 키>
opereta vpn peer rm phone ; opereta vpn off
```

- 인터페이스는 systemd-networkd(`50-opereta-wg0.netdev`/`.network`)가 만든다. 새 패키지는 없다(WireGuard 는 커널에 있다).
- 서버 개인 키는 `/var/lib/opereta/wireguard.json`(0600)과 `.netdev`(root:systemd-network 0640)에만 있고 API 로 돌려주지 않는다. 기기용 개인 키는 설정 파일에 담아 **한 번만** 보여 주고 저장하지 않는다.
- VPN 이 켜져 있는 동안 UDP 포트가 열리고 `wg0` 의 들어오는 연결을 모두 신뢰한다. 모델 API 를 VPN 으로 쓰려면 LAN 공개도 켜야 한다(공개 주소로 듣는다).

## 원격 관리 웹 페이지

```sh
opereta webadmin password        # 새 비밀번호(10자 이상)를 두 번 입력한다
opereta webadmin on --port 8443 --from lan
```

다른 컴퓨터의 브라우저에서 `https://<이 컴퓨터>:8443/` 로 접속한다. 서버 시작·정지, API 키, 연결 정보, 자원, 서비스 재시작을 볼 수 있다.

위협 모델과 제한:

- **자체 서명 인증서**(ECDSA P-256)를 데몬이 만든다. 브라우저 경고가 뜨면 `opereta webadmin` 의 지문과 비교한다.
- 전용 비밀번호는 PBKDF2-HMAC-SHA256(60만 회)로 해시해 `0600` 파일에 둔다. 같은 주소에서 5번 틀리면 30초 동안 막고, 이후 틀릴 때마다 두 배로(최대 15분) 늘린다. 세션은 30분 유휴, 8시간 최대이고 비밀번호를 바꾸면 모두 끝난다.
- 쿠키는 `HttpOnly; Secure; SameSite=Strict`. 상태를 바꾸는 요청은 `X-Opereta-Admin` 헤더와 같은 출처를 요구한다. 응답에 CSP, `X-Frame-Options: DENY` 등을 붙인다.
- 소켓 API 중 **허용 목록**의 요청만 통과한다. 계정, SSH 키, 방화벽, VPN, 원격 데스크톱, 업데이트, 웹 관리 설정은 이 길로 바꿀 수 없다. 비밀번호를 알아낸 공격자의 피해를 서버 운영으로 한정하기 위해서다.
- 새 프로세스를 만들지 않고 데몬 안에서 돈다. 꺼져 있으면 포트가 닫혀 있다.
