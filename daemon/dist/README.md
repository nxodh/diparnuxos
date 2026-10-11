# daemon/dist: systemd 유닛과 설정 원본

- 확인 날짜: 2026-10-11
- 상태: 원본 파일이다. 패키징(`lightlix-daemon` .deb 에 넣기)과 유닛 활성화는 다른 작업이 한다.
- 검사: `dist_test.go` 가 내용을 확인하고, 컨테이너(debian:trixie, systemd 257)에서 `systemd-analyze verify`,
  `systemd-sysusers`, `systemd-tmpfiles`, `nft -c` 로 확인했다. 결과는 작업 보고에 있다.

## 파일과 설치 경로

| 원본 파일 | 설치 경로 | 역할 |
|---|---|---|
| `lightlix.sysusers.conf` | `/usr/lib/sysusers.d/lightlix.conf` | 그룹 `lightlix`(소켓 접근), 사용자 `lightlix-llm`(llama-server 실행, 홈과 로그인 셸 없음). |
| `lightlix-daemon.tmpfiles.conf` | `/usr/lib/tmpfiles.d/lightlix-daemon.conf` | `/var/lib/lightlix`, `/var/lib/lightlix/models`, `/etc/lightlix` 를 0755 root 로 만든다. `/run/lightlix` 는 `lightlix-nvidia` 가 만든다. |
| `lightlixd.socket` | `/usr/lib/systemd/system/lightlixd.socket` | 제어 소켓 `/run/lightlix/lightlixd.sock`(0660, 그룹 `lightlix`). 데몬이 소켓 활성화로 넘겨받는다. |
| `lightlixd.service` | `/usr/lib/systemd/system/lightlixd.service` | 데몬 `/usr/sbin/lightlixd`(root). 부팅 때 `multi-user.target` 에서 뜬다. 하드닝 옵션과 이유는 파일 안 주석에 있다. |
| `lightlix-llama.service` | `/usr/lib/systemd/system/lightlix-llama.service` | llama-server `/usr/lib/lightlix/llama/llama-server`(사용자 `lightlix-llm`). 설정은 `/run/lightlix/llama-server.env` 의 `LLAMA_ARG_*`. `Restart=no`, `[Install]` 없음(데몬이 띄움). |
| `lightlix-firewall.service` | `/usr/lib/systemd/system/lightlix-firewall.service` | `/var/lib/lightlix/firewall.nft` 가 있으면 `nft -f` 로 적용한다. `sysinit.target` 에 묶이고 `network-pre.target` 보다 먼저 돈다. |
| `lightlix-firewall-default.service` | `/usr/lib/systemd/system/lightlix-firewall-default.service` | `firewall.nft` 가 없을 때만 `/usr/share/lightlix/firewall-default.nft` 를 적용한다. 두 방화벽 유닛은 `ConditionPathExists` 로 서로 배타적이다. |
| `firewall-default.nft` | `/usr/share/lightlix/firewall-default.nft` | 모든 입력을 닫는 기본 규칙. `daemon/internal/firewall` 의 `Ruleset(Options{APIPort: 8080})` 출력과 바이트 단위로 같다. |

## 런타임에 생기는 파일(패키지가 만들지 않는다)

- `/run/lightlix/lightlixd.sock`: 제어 소켓. systemd 가 만든다.
- `/run/lightlix/llama-server.env`: 데몬이 llama 를 띄우기 전에 쓴다(`llamaargs.EnvFile`).
- `/var/lib/lightlix/firewall.nft`: 데몬이 저장한 방화벽 규칙. 있으면 기본 규칙 대신 쓴다.
- `/var/lib/lightlix/models/`: 모델 파일, 사이드카(`<model>.gguf.json`), `.default`.
- `/etc/lightlix/lightlixd.json`: 관리자 설정(HF 토큰 포함, 파일 권한은 데몬이 정한다).

## 확인하지 못한 것

- `/dev/nvidia*` 장치 노드의 권한이 `lightlix-llm` 사용자에게 열려 있는지(udev 규칙 또는 그룹)는 이 저장소에서 찾지 못했다. 실 GPU 에서 확인해야 한다.
- `lightlix-llama.service` 의 `RestrictAddressFamilies` 와 `ProtectKernelTunables` 는 CUDA 초기화를 실 GPU 에서 돌려 보지 않았다.
- 방화벽 저장 파일이 깨졌을 때 `lightlix-firewall.service` 가 실패하면 기본 규칙도 적용되지 않는다. 이 동작은 설계 결정이 필요하다.
