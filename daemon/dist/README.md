# daemon/dist: systemd 유닛과 설정 원본

- 확인 날짜: 2026-10-11
- 상태: `packages/opereta-daemon`이 이 원본을 `.deb`에 설치하고 debhelper가 계정·디렉터리 생성과 유닛 활성화를 담당한다.
- 검사: `dist_test.go` 가 내용을 확인하고, 컨테이너(debian:trixie, systemd 257)에서 `systemd-analyze verify`,
  `systemd-sysusers`, `systemd-tmpfiles`, `nft -c` 로 확인했다. 결과는 작업 보고에 있다.

## 파일과 설치 경로

| 원본 파일 | 설치 경로 | 역할 |
|---|---|---|
| `opereta.sysusers.conf` | `/usr/lib/sysusers.d/opereta.conf` | 그룹 `opereta`(소켓 접근), 사용자 `opereta-llm`(llama-server 실행, 홈과 로그인 셸 없음). |
| `opereta-daemon.tmpfiles.conf` | `/usr/lib/tmpfiles.d/opereta-daemon.conf` | 상태 디렉터리는 0750, 모델 디렉터리는 2750 `root:opereta-llm`, 설정 디렉터리는 0750 `root:root`다. `/run/opereta` 는 `opereta-nvidia` 가 만든다. |
| `operetad.socket` | `/usr/lib/systemd/system/operetad.socket` | 제어 소켓 `/run/opereta/operetad.sock`(0660, 그룹 `opereta`). 데몬이 소켓 활성화로 넘겨받는다. |
| `operetad.service` | `/usr/lib/systemd/system/operetad.service` | 데몬 `/usr/sbin/operetad`(root). 부팅 때 `multi-user.target` 에서 뜬다. 하드닝 옵션과 이유는 파일 안 주석에 있다. |
| `opereta-llama.service` | `/usr/lib/systemd/system/opereta-llama.service` | llama-server `/usr/lib/opereta/llama/llama-server`(사용자 `opereta-llm`). 설정은 `/run/opereta/llama-server.env` 의 `LLAMA_ARG_*`. `Restart=no`, `[Install]` 없음(데몬이 띄움). |
| `opereta-firewall.service` | `/usr/lib/systemd/system/opereta-firewall.service` | 기본 규칙이 성공한 뒤 저장 파일이 있으면 `nft -f`로 덮어쓴다. 잘못된 파일의 트랜잭션은 기본 차단을 유지한다. |
| `opereta-firewall-default.service` | `/usr/lib/systemd/system/opereta-firewall-default.service` | `nftables.service` 뒤, `network-pre.target` 전에 항상 기본 차단을 적용한다. 데몬과 저장 규칙 유닛이 이 유닛을 요구한다. |
| `opereta-gpu-permissions.sh` | `/usr/lib/opereta/check-gpu-permissions` | llama 서비스 시작 전 전용 사용자로 GPU, 제어, UVM 노드의 읽기·쓰기 권한을 검사한다. GPU 노드가 없으면 CPU 경로를 허용한다. |
| `skills/opereta/SKILL.md` | `/usr/share/opereta/skills/opereta/SKILL.md` | 코딩 에이전트(Vedecaps)용 스킬. 이 컴퓨터의 모델 서버 상태를 읽고 문제를 짚는 방법을 알려 준다. `/etc/skel/.agents/skills/opereta` 링크로 새 계정에 보인다. |
| `firewall-default.nft` | `/usr/share/opereta/firewall-default.nft` | 모든 입력을 닫는 기본 규칙. `daemon/internal/firewall` 의 `Ruleset(Options{APIPort: 8080})` 출력과 바이트 단위로 같다. |

## 런타임에 생기는 파일(패키지가 만들지 않는다)

- `/run/opereta/operetad.sock`: 제어 소켓. systemd 가 만든다.
- `/run/opereta/llama-server.env`: 데몬이 llama 를 띄우기 전에 쓴다(`llamaargs.EnvFile`).
- `/var/lib/opereta/firewall.nft`: 데몬이 저장한 방화벽 규칙. 기본 규칙 적용 뒤 덮어쓴다.
- `/var/lib/opereta/models/`: 모델 파일, 사이드카(`<model>.gguf.json`), `.default`.
- `/etc/opereta/operetad.json`: 관리자 설정(HF 토큰 포함, 파일 권한은 데몬이 정한다).

모델 디렉터리는 setgid로 새 파일에 `opereta-llm` 그룹을 상속한다. 데몬의 `UMask=0027`은 새 다운로드 파일을 0640으로 만들며 모델 목록 사이드카의 0644도 상위 디렉터리로 제한한다.
API 키는 데몬이 0640으로 저장하고 `opereta-llm` 그룹으로 바꾼다. 상태 디렉터리의 그룹 실행 권한으로 llama 사용자가 파일까지 접근한다.
`opereta-llm` 사용자는 sysusers와 `SupplementaryGroups`로 `video`, `render`에 속한다. `/dev/nvidia*` 권한은 드라이버가 정하며 시작 전 검사가 누락·접근 거부를 영어로 표시한다.

## 확인하지 못한 것

- 실제 NVIDIA 장치 노드의 권한과 CUDA 초기화는 실 GPU에서 확인해야 한다. 그룹 소속과 시작 전 권한 검사는 실제 GPU 추론을 증명하지 않는다.
- `opereta-llama.service` 의 `RestrictAddressFamilies` 와 `ProtectKernelTunables` 는 CUDA 초기화를 실 GPU 에서 돌려 보지 않았다.
