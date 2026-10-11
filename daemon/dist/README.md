# daemon/dist: systemd 유닛과 설정 원본

- 확인 날짜: 2026-10-11
- 상태: `packages/lightlix-daemon`이 이 원본을 `.deb`에 설치하고 debhelper가 계정·디렉터리 생성과 유닛 활성화를 담당한다.
- 검사: `dist_test.go` 가 내용을 확인하고, 컨테이너(debian:trixie, systemd 257)에서 `systemd-analyze verify`,
  `systemd-sysusers`, `systemd-tmpfiles`, `nft -c` 로 확인했다. 결과는 작업 보고에 있다.

## 파일과 설치 경로

| 원본 파일 | 설치 경로 | 역할 |
|---|---|---|
| `lightlix.sysusers.conf` | `/usr/lib/sysusers.d/lightlix.conf` | 그룹 `lightlix`(소켓 접근), 사용자 `lightlix-llm`(llama-server 실행, 홈과 로그인 셸 없음). |
| `lightlix-daemon.tmpfiles.conf` | `/usr/lib/tmpfiles.d/lightlix-daemon.conf` | 상태 디렉터리는 0750, 모델 디렉터리는 2750 `root:lightlix-llm`, 설정 디렉터리는 0750 `root:root`다. `/run/lightlix` 는 `lightlix-nvidia` 가 만든다. |
| `lightlixd.socket` | `/usr/lib/systemd/system/lightlixd.socket` | 제어 소켓 `/run/lightlix/lightlixd.sock`(0660, 그룹 `lightlix`). 데몬이 소켓 활성화로 넘겨받는다. |
| `lightlixd.service` | `/usr/lib/systemd/system/lightlixd.service` | 데몬 `/usr/sbin/lightlixd`(root). 부팅 때 `multi-user.target` 에서 뜬다. 하드닝 옵션과 이유는 파일 안 주석에 있다. |
| `lightlix-llama.service` | `/usr/lib/systemd/system/lightlix-llama.service` | llama-server `/usr/lib/lightlix/llama/llama-server`(사용자 `lightlix-llm`). 설정은 `/run/lightlix/llama-server.env` 의 `LLAMA_ARG_*`. `Restart=no`, `[Install]` 없음(데몬이 띄움). |
| `lightlix-firewall.service` | `/usr/lib/systemd/system/lightlix-firewall.service` | 기본 규칙이 성공한 뒤 저장 파일이 있으면 `nft -f`로 덮어쓴다. 잘못된 파일의 트랜잭션은 기본 차단을 유지한다. |
| `lightlix-firewall-default.service` | `/usr/lib/systemd/system/lightlix-firewall-default.service` | `nftables.service` 뒤, `network-pre.target` 전에 항상 기본 차단을 적용한다. 데몬과 저장 규칙 유닛이 이 유닛을 요구한다. |
| `lightlix-gpu-permissions.sh` | `/usr/lib/lightlix/check-gpu-permissions` | llama 서비스 시작 전 전용 사용자로 GPU, 제어, UVM 노드의 읽기·쓰기 권한을 검사한다. GPU 노드가 없으면 CPU 경로를 허용한다. |
| `firewall-default.nft` | `/usr/share/lightlix/firewall-default.nft` | 모든 입력을 닫는 기본 규칙. `daemon/internal/firewall` 의 `Ruleset(Options{APIPort: 8080})` 출력과 바이트 단위로 같다. |

## 런타임에 생기는 파일(패키지가 만들지 않는다)

- `/run/lightlix/lightlixd.sock`: 제어 소켓. systemd 가 만든다.
- `/run/lightlix/llama-server.env`: 데몬이 llama 를 띄우기 전에 쓴다(`llamaargs.EnvFile`).
- `/var/lib/lightlix/firewall.nft`: 데몬이 저장한 방화벽 규칙. 기본 규칙 적용 뒤 덮어쓴다.
- `/var/lib/lightlix/models/`: 모델 파일, 사이드카(`<model>.gguf.json`), `.default`.
- `/etc/lightlix/lightlixd.json`: 관리자 설정(HF 토큰 포함, 파일 권한은 데몬이 정한다).

모델 디렉터리는 setgid로 새 파일에 `lightlix-llm` 그룹을 상속한다. 데몬의 `UMask=0027`은 새 다운로드 파일을 0640으로 만들며 모델 목록 사이드카의 0644도 상위 디렉터리로 제한한다.
API 키는 데몬이 0640으로 저장하고 `lightlix-llm` 그룹으로 바꾼다. 상태 디렉터리의 그룹 실행 권한으로 llama 사용자가 파일까지 접근한다.
`lightlix-llm` 사용자는 sysusers와 `SupplementaryGroups`로 `video`, `render`에 속한다. `/dev/nvidia*` 권한은 드라이버가 정하며 시작 전 검사가 누락·접근 거부를 영어로 표시한다.

## 확인하지 못한 것

- 실제 NVIDIA 장치 노드의 권한과 CUDA 초기화는 실 GPU에서 확인해야 한다. 그룹 소속과 시작 전 권한 검사는 실제 GPU 추론을 증명하지 않는다.
- `lightlix-llama.service` 의 `RestrictAddressFamilies` 와 `ProtectKernelTunables` 는 CUDA 초기화를 실 GPU 에서 돌려 보지 않았다.
