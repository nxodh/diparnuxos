# Diparnux

- 확인 날짜: 2026-10-11
- 상태: Phase 2의 [CI 38142350756](https://github.com/nxodh/diparnuxos/actions/runs/38142350756)(`035dfb4`)이 전체 성공했다. QEMU/KVM의 실제 ISO에서 live 사용자가 모델을 내려받아 SHA-256을 확인하고, CPU로 서버를 시작해 채팅하고, 데몬 재시작 뒤 같은 서버를 채택하고, 정상 중지하는 흐름이 통과했다. 앞선 세 실행은 각각 공간 부족, E2E assertion 오류, CPU MainPID 런타임 확인 실패로 끝났고 [검증 기록](docs/verification-phase-2.md)에 남겼다.
- 검증 범위: 이 결과는 GPU 없는 VM의 CPU 경로다. **실 NVIDIA 오프로드, Secure Boot, 실기 Wi-Fi·USB 부팅, 유휴 전체 OS 램은 미검증이다.** 실기 지원을 주장하지 않는다.
- 저장소: https://github.com/nxodh/diparnuxos

Diparnux는 Debian 13(trixie) 기반의 Linux 배포판이다. 램 사용을 최소로 줄이면서 로컬 LLM 추론 서버를 돌리는 것이 목표다. amd64 아키텍처와 UEFI 부팅만 지원한다는 것이 계획이다. 대상 GPU는 NVIDIA RTX 30, 40, 50 계열이다.

> Debian은 Software in the Public Interest, Inc.가 소유한 등록 상표다. Diparnux는 Debian과 제휴 관계가 없는 파생 배포판이며, Debian 프로젝트가 만들었거나 보증하지 않는다.

---

## 현재 상태

현재 구현 범위는 **Phase 2 헤드리스 추론 서버 ISO**다. ISO에는 다음이 들어간다.

- 최소 Debian 13 시스템
- Diparnux 정체성(`os-release`)과 호스트 이름 `diparnux`
- 직렬 콘솔 로그인 프롬프트 `diparnux login:`
- 라이브 계정 `user`(비밀번호 `live`, sudo 가능). Debian live-config 의 기본값이며 SSH 서버는 들어 있지 않다.
- NVIDIA 공식 저장소, 지정 버전의 오픈 커널 모듈과 사용자 공간 드라이버
- CUDA 런타임(`cudart`, `cublas`). CUDA 컴파일러와 툴킷은 포함하지 않는다.
- 부팅 시 GPU 진단 결과를 영어 한 줄로 로그인 프롬프트 위에 표시(모니터와 직렬 콘솔 모두), 원인별 CPU 폴백 안내, 서버 모드의 절전 비활성화
- Intel Wi-Fi 펌웨어(`firmware-iwlwifi`)와 Intel 이더넷 펌웨어(`firmware-intel-misc`), 무선 접속 데몬 iwd. 실기 접속은 미검증이다.
- llama.cpp의 `llama-server`와 CPU·CUDA 백엔드 모듈(`diparnux-llm`, `/usr/lib/diparnux/llama`). amd64 CUDA 패키지 빌드와 CPU 검사는 CI에서 통과했지만, CUDA 경로의 실 GPU 오프로드는 아직 확인하지 않았다. 빌드 방법과 검증 범위는 [llama/README.md](llama/README.md)에 있다.
- `diparnuxd` 데몬과 `diparnux` CLI. 모델 조회·다운로드, SHA-256 검사, 실행 계획 계산, 서버 시작·중지, 채팅, 설정과 API 키 관리를 제공한다.
- systemd 소켓 활성화, 별도 추론 서비스 사용자, 기본 nftables 방화벽. 추론 API는 기본적으로 localhost에서만 열린다.

모델은 이미지에 포함하지 않는다. 라이브 사용자 `user`가 `diparnux` 명령으로 모델을 내려받고 추론 서버를 시작한다.
자동 시작 설정은 기본적으로 켜져 있지만 기본 모델이 없는 새 ISO에서는 추론 서버를 시작하지 않는다.
라이브 세션의 모델과 설정은 재부팅하면 사라지므로 영구 설치를 대신하지 않는다.
GUI와 설치기는 후속 Phase에 구현한다. 실제 사용 명령은 [CLI 문서](cli/README.md), 서비스와 API는 [데몬 문서](daemon/README.md)에 있다.

라이브 ISO에서 NVIDIA 모듈을 사용하려면 **Secure Boot를 끈다.** 빌드 중 생성한 DKMS 키는 배포하지 않는다. 기기별 MOK 등록은 아직 구현하지 않았다. 부팅 시 GPU 상태는 영어 한 줄(`Diparnux GPU [<상태>]: ...`)로 로그인 프롬프트 위에 표시된다. 모니터와 직렬 콘솔 모두에 나온다. 한글은 텍스트 콘솔에서 표시되지 않으므로 화면 문구는 영어다. 자세한 원인은 `sudo journalctl -k`로 확인한다. 실기 화면 표시는 아직 확인하지 않았다.

GPU 진단은 `sudo diparnux-gpu-check`로 다시 실행한다. 결과는 `/run/diparnux/gpu-status`에 기록된다. 라이선스 전문은 이미지의 `/usr/share/diparnux/licenses/`에서 읽을 수 있다.

NVIDIA 실 하드웨어에서 검증하지 않았다. ISO 빌드와 GPU 없는 QEMU 부팅 결과는 [검증 기록](docs/verification-phase-1.md)에 적는다. 실기 결과가 생기기 전까지 특정 GPU에서의 동작을 주장하지 않는다. 노트북을 포함한 대상과 상태는 [docs/support-matrix.md](docs/support-matrix.md)에 있다.

### 네트워크

유선과 무선 모두 systemd-networkd가 DHCP를 맡는다. 이미지에서 ifupdown과 dhcpcd-base를 뺐으므로 같은 NIC를 두 DHCP 클라이언트가 관리하지 않는다. 무선의 인증과 연결은 iwd가 맡는다. 실기에서 Wi-Fi 접속은 아직 확인하지 않았다. 장치 이름은 기기마다 다르므로 `wlan0`으로 가정하지 않고 먼저 확인한다.

```sh
iwctl device list                     # 무선 장치 이름을 Name 열에서 확인한다
iwctl station <장치> scan             # 주변 네트워크를 검색한다
iwctl station <장치> get-networks     # 검색 결과를 보여 준다
iwctl station <장치> connect <SSID>   # 연결한다. 암호를 물으면 입력한다
```

iwd의 D-Bus 정책(`iwd-dbus.conf`)상 `iwctl`은 root와 `sudo`·`netdev` 그룹 사용자에게 허용된다. 권한 오류가 나면 `sudo iwctl`로 실행한다.

---

## 대상 하드웨어

| 항목 | 대상 | 상태 |
|---|---|---|
| CPU 아키텍처 | amd64 | 계획 |
| 부팅 방식 | UEFI 전용 | 계획 |
| GPU | NVIDIA RTX 30(Ampere, 8.6), RTX 40(Ada, 8.9), RTX 50(Blackwell, 12.0) | 미검증 |
| 드라이버 모듈 | NVIDIA 오픈 커널 모듈 | 계획 |

RTX 50은 오픈 커널 모듈만 지원한다. Secure Boot가 켜진 상태에서는 NVIDIA 모듈이 올라오지 않을 수 있다. 자세한 내용은 [docs/secure-boot.md](docs/secure-boot.md)를 본다.

---

## 빌드

요구 사항과 명령은 Makefile 기준이다. 버전과 저장소 주소는 저장소 루트의 `diparnux.conf` 한 곳에서만 정의한다.

| 명령 | 내용 |
|---|---|
| `make iso` | Docker 안에서 ISO를 빌드한다. 호스트에 Debian 13이 없어도 된다. |
| `make iso-native` | Debian 13 호스트에서 root로 직접 ISO를 빌드한다. |
| `make deb` | `packages/` 아래 패키지를 `.deb`으로 빌드한다. |
| `make llama` | llama.cpp 고정 커밋을 Docker 안에서 빌드해 `out/llama/`에 `diparnux-llm` `.deb`을 만든다. 입력이 같으면 건너뛴다. CUDA 빌드는 CI에서 돌리고 로컬에서는 오래 걸린다. |
| `make test-llama-docker` | `out/llama/`의 `.deb`을 CUDA가 없는 Debian 컨테이너에서 검사한다. CPU 추론 테스트를 포함한다. |
| `make lint` | 셸 스크립트를 정적 검사한다. |
| `make test-gpu-check` | GPU 없이 진단 분기를 테스트한다. 실기 검증은 아니다. |
| `make test-daemon` | Debian의 Go와 gcc 환경에서 데몬·CLI를 빌드하고 vet, 테스트를 실행한다. Mac에서는 Docker 안에서 실행한다. |
| `make test-nvidia-image-docker` | 완성된 ISO의 오픈 모듈, 드라이버 구성, 라이선스, 서명 키 제거를 확인한다. |
| `make test-qemu` | QEMU/OVMF로 로그인 프롬프트와 GPU 없는 CPU 폴백 안내를 확인한다. |
| `make test-qemu-docker` | 동일한 부팅 확인을 Debian 컨테이너에서 한다. |
| `make test-qemu-usb-docker` | ISO를 USB 저장 장치로 붙여 같은 부팅 확인을 한다. 광학 드라이브가 없는 실기와 가깝다. |
| `make test-qemu-e2e-docker` | 라이브 사용자로 로그인해 실제 모델 다운로드·SHA-256·CPU 추론·중지를 검사한다. |
| `make test-qemu-e2e-driver` | ISO 없이 E2E 드라이버의 오류 판정과 정리를 검사한다. |
| `make test-update-docker` | 가짜 저장소에서 실제 apt/dpkg 로 `diparnux-update` 의 갱신, 롤백, 실패 복구를 확인한다([docs/updates.md](docs/updates.md)). |
| `make test-apt-repo-docker` | 서명된 자체 APT 저장소를 만들고 apt 의 서명 검증과 변조 거절을 확인한다([docs/apt-repository.md](docs/apt-repository.md)). |
| `make test-installer-config-docker` | Calamares 설정이 실제 Calamares 패키지의 모듈과 스키마에 맞는지 확인한다. |
| `make test-qemu-install-docker` | ISO 로 빈 가상 디스크에 설치하고 디스크 부팅과 Secure Boot(shim) 부팅까지 확인한다([docs/installation.md](docs/installation.md)). |
| `make clean` | 빌드 산출물을 지운다. `out/llama/`는 남긴다. |
| `make clean-llama` | `out/llama/`(llama.cpp 빌드 산출물)를 지운다. |

`make iso`는 `make llama`에 의존한다. 로컬 CPU 전용 스모크 빌드는 `LLAMA_CUDA=OFF LLAMA_PLATFORM=linux/arm64 make llama`로 한다. 산출물은 검사 뒤 지워야 한다([llama/README.md](llama/README.md)).
Phase 2의 실제 실행 결과와 미검증 범위는 [검증 기록](docs/verification-phase-2.md)에 적는다.

산출물은 `out/diparnux-<version>-amd64.iso`와 같은 이름에 `.sha256`을 붙인 체크섬 파일이다. `out/`은 git에 넣지 않는다.

---

## 저장소 구조

```
diparnux.conf    저장소 URL과 버전 값을 정의하는 유일한 곳
Makefile         빌드, 검사, 테스트 타깃
scripts/         build-in-docker.sh, build-iso.sh, build-debs.sh, build-llama-in-docker.sh, test-llama-in-docker.sh
build/           live-build 설정(build/config/)과 빌더 Dockerfile. 빌드 작업은 저장소 밖 /var/tmp/diparnux-build 에서 한다
packages/        Diparnux 자체 패키지 소스
tests/           QEMU 부팅 확인(tests/qemu), 실 하드웨어 체크리스트(tests/hw), diparnux-llm 패키지 검사(tests/llama)
.github/         CI(ISO 빌드)
daemon/          diparnuxd 데몬(Go). 설계 메모는 daemon/README.md
gui/             GTK4/libadwaita 앱. 설계 메모는 gui/README.md
cli/             명령행 도구 사용법. 구현은 daemon/cmd/diparnux
llama/           llama.cpp 빌드 설정과 Dockerfile, build.sh, diparnux-llm 패키징(pkg/debian). 설계 메모는 llama/README.md
docs/            결정 기록, 드라이버, 라이선스, Secure Boot, 메모리, 지원 매트릭스, 검증 기록
out/             빌드 산출물(git 제외)
```

---

## 개발 단계

각 단계의 빌드와 테스트를 확인한 뒤 다음 단계의 승인을 받는다.

| Phase | 내용 | 상태 |
|---|---|---|
| 0 | 저장소 뼈대, 최소 Debian 13 ISO, CI, 문서 초안 | 완료. main의 CI 빌드와 QEMU 부팅 통과 |
| 1 | 헤드리스 ISO, NVIDIA 오픈 모듈, CUDA 런타임, 부팅 진단, DKMS 키 제거 | 구현 및 CI 검증 완료. NVIDIA 실기 검증 대기 |
| 2 | llama.cpp CUDA/CPU 소스 빌드, `diparnuxd`, CLI, 추론 서버 | 구현 및 CI 검증 완료(CUDA 패키지, ISO, UEFI CD/USB, live CPU E2E). 실 GPU 오프로드는 실기 검증 대기 |
| 3 | GUI(labwc, greetd, GTK4/libadwaita). Secure Boot의 기기별 MOK 흐름과 안내 | 구현 및 CI 검증 완료(QEMU 가상 화면에서 모드 전환, greetd, labwc, Control Center). 실기 모니터·입력·Secure Boot MOK 검증 대기. 데스크톱은 0.5.0에서 Xfce로 교체됨 |
| 4 | Calamares 설치기, 커널·드라이버 동시 업데이트와 롤백, 자체 APT 저장소 | 구현 및 CI 검증 완료(0.4.0, `main`에 병합). 0.4.1(Wi-Fi 화면, 로고 디자인, 설치기 polkit 수정)은 [PR #8](https://github.com/nxodh/diparnuxos/pull/8)에서 검토 중. 실기 설치·실제 커널 갱신 검증 대기. 저장소 호스팅과 서명 키는 사용자 결정 대기. [검증 기록](docs/verification-phase-4.md) |
| 4.5 | 0.5.0: Xfce(Xorg) 데스크톱, lightdm, 어두운 흑백 디자인. 램 목표는 철회했다(먼저 동작, 경량화는 그다음) | 구현 및 CI 검증 완료([PR #9](https://github.com/nxodh/diparnuxos/pull/9), QEMU 가상 화면에서 자동 로그인, 로그인 화면, Xfce 세션, Control Center, 설치기 창). 설계는 [phase-5-design.md](docs/phase-5-design.md). 실기, NVIDIA 하이브리드 화면, VMware 검증 대기 |
| 4.6 | 0.5.1: OS 이름 Diparnux, 새 로고와 디자인. 이전 이름의 흔적을 두지 않는다 | 구현 및 CI 검증 완료, `main` 에 병합됨. 설계는 [phase-5-1-design.md](docs/phase-5-1-design.md) |
| 4.7 | 0.5.2: 경량화 일부, 설치기 비밀번호 오류 수정, 상시 가동, 한/영 전환, AI 서버 운영(게이트웨이, 동시 요청), 네트워크·방화벽·원격 접속 | 구현 완료([PR #11](https://github.com/nxodh/diparnuxos/pull/11)). 설계는 [phase-5-2-design.md](docs/phase-5-2-design.md), 검증 범위는 [phase-5-2-verification.md](docs/phase-5-2-verification.md) |
| 4.8 | 0.5.3: Vedecaps 와의 연결성, 호환성, 시너지(`connect vedecaps`, MCP, 스킬, agent 프로필, 이미지에 포함) | 구현 완료(브랜치 `phase-5-3`). 설계는 [phase-5-3-design.md](docs/phase-5-3-design.md), 사용은 [vedecaps.md](docs/vedecaps.md), 검증 범위는 [phase-5-3-verification.md](docs/phase-5-3-verification.md). 실제 GPU 에서의 Vedecaps 작업은 검증 대기 |
| 4.9 | 0.5.4: 게이트웨이 TLS(HTTPS), 로컬 CA, Vedecaps 의 https 연동(`options.ca`) | 개발 중(브랜치 `phase-5-4`). 설계는 [phase-5-4-design.md](docs/phase-5-4-design.md), 사용은 [tls.md](docs/tls.md), 검증 범위는 [phase-5-4-verification.md](docs/phase-5-4-verification.md). 실제 다른 컴퓨터에서의 접속과 Vedecaps 0.1.5 와의 호환은 검증 대기 |
| 5 | 경량화(실측과 목표 재설정), 실 하드웨어 검증, 지원 범위 확정, 릴리스 | 계획 |

---

## 문서

| 문서 | 내용 |
|---|---|
| [docs/decisions.md](docs/decisions.md) | 기술 결정 기록. 원래 결정, 2026-10-10 변경, 근거와 출처 |
| [docs/installation.md](docs/installation.md) | 설치(그래픽, 콘솔), 디스크 구성, 설치 뒤, 한계 |
| [docs/network.md](docs/network.md) | 인터넷 연결과 Wi-Fi(GUI, CLI, 패널) |
| [docs/ai-server.md](docs/ai-server.md) | AI 서버 운영: 게이트웨이, 연결 정보, 동시 요청, 서버 현황, 자동 재시작, 모델 가져오기 |
| [docs/remote-access.md](docs/remote-access.md) | 원격 접속: SSH 고급, 원격 데스크톱, VPN, 원격 관리 웹 페이지 |
| [docs/firewall.md](docs/firewall.md) | 방화벽 규칙(기능이 여는 포트, 사용자 규칙, 안전 장치) |
| [docs/design.md](docs/design.md) | 로고 기반 디자인 시스템(흑백 팔레트, 적용 범위) |
| [docs/updates.md](docs/updates.md) | 커널·드라이버 업데이트, 부팅 뒤 확인, 롤백, 오류 코드 |
| [docs/apt-repository.md](docs/apt-repository.md) | 자체 APT 저장소 구조, 키 생성 절차, 클라이언트 설정 |
| [docs/phase-4-design.md](docs/phase-4-design.md) | Phase 4 설계 |
| [docs/phase-5-design.md](docs/phase-5-design.md) | 0.5.0 설계(Xfce 데스크톱, 어두운 흑백 디자인, 램 목표 철회) |
| [docs/phase-5-1-design.md](docs/phase-5-1-design.md) | 0.5.1 설계(OS 이름 Diparnux, 새 로고, 흔적 검사) |
| [docs/phase-5-2-design.md](docs/phase-5-2-design.md) | 0.5.2 설계(경량화, 설치기 수정, AI 서버 운영과 원격 접속) |
| [docs/phase-5-3-design.md](docs/phase-5-3-design.md) | 0.5.3 설계(Vedecaps 와의 연결성, 호환성, 시너지) |
| [docs/phase-5-4-design.md](docs/phase-5-4-design.md) | 0.5.4 설계(게이트웨이 TLS, Vedecaps 2차 연동) |
| [docs/tls.md](docs/tls.md) | 게이트웨이 TLS: 켜기, CA 배포, 인증서, 끄기와 되돌리기 |
| [docs/vedecaps.md](docs/vedecaps.md) | Vedecaps 와 함께 쓰기: 연결, MCP, 스킬, agent 프로필, 키와 보안 |
| [docs/phase-5-3-verification.md](docs/phase-5-3-verification.md) | 0.5.3 검증 범위와 확인하지 못한 것 |
| [docs/vedecaps-research.md](docs/vedecaps-research.md) | Vedecaps 조사 기록 |
| [docs/security.md](docs/security.md) | 비밀 정보 취급 규칙(공개 저장소에 올리면 안 되는 것, 자동 검사, 실수로 올렸을 때) |
| [docs/power.md](docs/power.md) | 상시 가동(덮개를 닫거나 오래 두어도 절전·종료로 서버가 멈추지 않게 하는 설정) |
| [docs/desktop-troubleshooting.md](docs/desktop-troubleshooting.md) | 데스크톱 모드 문제 해결(로그인 화면, 세션, 입력기, 브라우저) |
| [docs/drivers.md](docs/drivers.md) | NVIDIA 드라이버 구성과 Phase 1 검증 |
| [docs/verification-phase-1.md](docs/verification-phase-1.md) | 이번 단계에서 실제로 확인한 내용과 남은 한계 |
| [docs/verification-phase-2.md](docs/verification-phase-2.md) | Phase 2 CI 실행별 결과, 패키지/fixture/ISO 검증 범위와 미검증 항목 |
| [docs/licenses.md](docs/licenses.md) | NVIDIA 드라이버 라이선스, CUDA EULA, 상표, 프로젝트 라이선스(미정) |
| [docs/secure-boot.md](docs/secure-boot.md) | Secure Boot와 커널 모듈 서명 설계(Phase 1, 3 예정) |
| [docs/memory.md](docs/memory.md) | 메모리 목표, 측정 방법 초안, 실측값(미측정) |
| [docs/support-matrix.md](docs/support-matrix.md) | GPU 아키텍처별 대상과 상태(전부 미검증) |
| [docs/verification-2026-10-10.md](docs/verification-2026-10-10.md) | 2026-10-10 검증 결과와 미검증 항목 |

구성 요소 설계 메모: [daemon/README.md](daemon/README.md), [gui/README.md](gui/README.md), [cli/README.md](cli/README.md), [llama/README.md](llama/README.md).

---

## 라이선스

프로젝트 자체 라이선스는 아직 정하지 않았다. 저장소에 LICENSE 파일이 없다. 포함되는 제3자 소프트웨어(NVIDIA 드라이버, CUDA 런타임, llama.cpp, Debian 패키지)는 각자의 라이선스를 따른다. 자세한 내용은 [docs/licenses.md](docs/licenses.md)에 있다.
