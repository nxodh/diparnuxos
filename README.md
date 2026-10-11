# Lightlix

- 확인 날짜: 2026-10-11
- 상태: [Phase 2 CI 38131367776](https://github.com/nxodh/lightlixos/actions/runs/38131367776), head `b525ed9`, 전체 결과는 실패다. CUDA 패키지 빌드·패키지 CPU 추론 검사, ISO·이미지 정책 검사, UEFI CD·USB 부팅은 통과했다. 라이브 사용자 E2E는 모델 다운로드 전에 `insufficient_space`로 실패했고 QEMU 로그 업로드도 실패했다. **실 GPU, Secure Boot, Wi-Fi, 유휴 전체 램은 미검증이다.** 자세한 출력은 [Phase 2 검증 기록](docs/verification-phase-2.md)에 있다.
- 저장소: https://github.com/nxodh/lightlixos

Lightlix는 Debian 13(trixie) 기반의 Linux 배포판이다. 램 사용을 최소로 줄이면서 로컬 LLM 추론 서버를 돌리는 것이 목표다. amd64 아키텍처와 UEFI 부팅만 지원한다는 것이 계획이다. 대상 GPU는 NVIDIA RTX 30, 40, 50 계열이다.

> Debian은 Software in the Public Interest, Inc.가 소유한 등록 상표다. Lightlix는 Debian과 제휴 관계가 없는 파생 배포판이며, Debian 프로젝트가 만들었거나 보증하지 않는다.

---

## 현재 상태

현재 구현 범위는 **Phase 2 헤드리스 추론 서버 ISO**다. ISO에는 다음이 들어간다.

- 최소 Debian 13 시스템
- Lightlix 정체성(`os-release`)과 호스트 이름 `lightlix`
- 직렬 콘솔 로그인 프롬프트 `lightlix login:`
- 라이브 계정 `user`(비밀번호 `live`, sudo 가능). Debian live-config 의 기본값이며 SSH 서버는 들어 있지 않다.
- NVIDIA 공식 저장소, 지정 버전의 오픈 커널 모듈과 사용자 공간 드라이버
- CUDA 런타임(`cudart`, `cublas`). CUDA 컴파일러와 툴킷은 포함하지 않는다.
- 부팅 시 GPU 진단 결과를 영어 한 줄로 로그인 프롬프트 위에 표시(모니터와 직렬 콘솔 모두), 원인별 CPU 폴백 안내, 서버 모드의 절전 비활성화
- Intel Wi-Fi 펌웨어(`firmware-iwlwifi`)와 Intel 이더넷 펌웨어(`firmware-intel-misc`), 무선 접속 데몬 iwd. 실기 접속은 미검증이다.
- llama.cpp의 `llama-server`와 CPU·CUDA 백엔드 모듈(`lightlix-llm`, `/usr/lib/lightlix/llama`). amd64 CUDA 패키지 빌드와 CPU 검사는 CI에서 통과했지만, CUDA 경로의 실 GPU 오프로드는 아직 확인하지 않았다. 빌드 방법과 검증 범위는 [llama/README.md](llama/README.md)에 있다.
- `lightlixd` 데몬과 `lightlix` CLI. 모델 조회·다운로드, SHA-256 검사, 실행 계획 계산, 서버 시작·중지, 채팅, 설정과 API 키 관리를 제공한다.
- systemd 소켓 활성화, 별도 추론 서비스 사용자, 기본 nftables 방화벽. 추론 API는 기본적으로 localhost에서만 열린다.

모델은 이미지에 포함하지 않는다. 라이브 사용자 `user`가 `lightlix` 명령으로 모델을 내려받고 추론 서버를 시작한다.
자동 시작 설정은 기본적으로 켜져 있지만 기본 모델이 없는 새 ISO에서는 추론 서버를 시작하지 않는다.
라이브 세션의 모델과 설정은 재부팅하면 사라지므로 영구 설치를 대신하지 않는다.
GUI와 설치기는 후속 Phase에 구현한다. 실제 사용 명령은 [CLI 문서](cli/README.md), 서비스와 API는 [데몬 문서](daemon/README.md)에 있다.

라이브 ISO에서 NVIDIA 모듈을 사용하려면 **Secure Boot를 끈다.** 빌드 중 생성한 DKMS 키는 배포하지 않는다. 기기별 MOK 등록은 아직 구현하지 않았다. 부팅 시 GPU 상태는 영어 한 줄(`Lightlix GPU [<상태>]: ...`)로 로그인 프롬프트 위에 표시된다. 모니터와 직렬 콘솔 모두에 나온다. 한글은 텍스트 콘솔에서 표시되지 않으므로 화면 문구는 영어다. 자세한 원인은 `sudo journalctl -k`로 확인한다. 실기 화면 표시는 아직 확인하지 않았다.

GPU 진단은 `sudo lightlix-gpu-check`로 다시 실행한다. 결과는 `/run/lightlix/gpu-status`에 기록된다. 라이선스 전문은 이미지의 `/usr/share/lightlix/licenses/`에서 읽을 수 있다.

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

요구 사항과 명령은 Makefile 기준이다. 버전과 저장소 주소는 저장소 루트의 `lightlix.conf` 한 곳에서만 정의한다.

| 명령 | 내용 |
|---|---|
| `make iso` | Docker 안에서 ISO를 빌드한다. 호스트에 Debian 13이 없어도 된다. |
| `make iso-native` | Debian 13 호스트에서 root로 직접 ISO를 빌드한다. |
| `make deb` | `packages/` 아래 패키지를 `.deb`으로 빌드한다. |
| `make llama` | llama.cpp 고정 커밋을 Docker 안에서 빌드해 `out/llama/`에 `lightlix-llm` `.deb`을 만든다. 입력이 같으면 건너뛴다. CUDA 빌드는 CI에서 돌리고 로컬에서는 오래 걸린다. |
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
| `make clean` | 빌드 산출물을 지운다. `out/llama/`는 남긴다. |
| `make clean-llama` | `out/llama/`(llama.cpp 빌드 산출물)를 지운다. |

`make iso`는 `make llama`에 의존한다. 로컬 CPU 전용 스모크 빌드는 `LLAMA_CUDA=OFF LLAMA_PLATFORM=linux/arm64 make llama`로 한다. 산출물은 검사 뒤 지워야 한다([llama/README.md](llama/README.md)).
Phase 2의 실제 실행 결과와 미검증 범위는 [검증 기록](docs/verification-phase-2.md)에 적는다.

산출물은 `out/lightlix-<version>-amd64.iso`와 같은 이름에 `.sha256`을 붙인 체크섬 파일이다. `out/`은 git에 넣지 않는다.

---

## 저장소 구조

```
lightlix.conf    저장소 URL과 버전 값을 정의하는 유일한 곳
Makefile         빌드, 검사, 테스트 타깃
scripts/         build-in-docker.sh, build-iso.sh, build-debs.sh, build-llama-in-docker.sh, test-llama-in-docker.sh
build/           live-build 설정(build/config/)과 빌더 Dockerfile. 빌드 작업은 저장소 밖 /var/tmp/lightlix-build 에서 한다
packages/        Lightlix 자체 패키지 소스
tests/           QEMU 부팅 확인(tests/qemu), 실 하드웨어 체크리스트(tests/hw), lightlix-llm 패키지 검사(tests/llama)
.github/         CI(ISO 빌드)
daemon/          lightlixd 데몬(Go). 설계 메모는 daemon/README.md
gui/             GTK4/libadwaita 앱. 설계 메모는 gui/README.md
cli/             명령행 도구 사용법. 구현은 daemon/cmd/lightlix
llama/           llama.cpp 빌드 설정과 Dockerfile, build.sh, lightlix-llm 패키징(pkg/debian). 설계 메모는 llama/README.md
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
| 2 | llama.cpp CUDA/CPU 소스 빌드, `lightlixd`, CLI, 추론 서버 | CUDA 패키지·ISO·UEFI CD/USB는 CI에서 확인. 라이브 사용자 추론 CI는 `insufficient_space`로 실패했으며 실 GPU 검증은 미실시 |
| 3 | GUI(labwc, greetd, GTK4/libadwaita). Secure Boot의 기기별 MOK 흐름과 안내 | 계획 |
| 4 | Calamares 설치기, 커널·드라이버 동시 업데이트와 롤백, 자체 APT 저장소 | 계획 |
| 5 | 실 하드웨어 검증, 지원 범위 확정, 릴리스 | 계획 |

---

## 문서

| 문서 | 내용 |
|---|---|
| [docs/decisions.md](docs/decisions.md) | 기술 결정 기록. 원래 결정, 2026-10-10 변경, 근거와 출처 |
| [docs/drivers.md](docs/drivers.md) | NVIDIA 드라이버 구성과 Phase 1 검증 |
| [docs/verification-phase-1.md](docs/verification-phase-1.md) | 이번 단계에서 실제로 확인한 내용과 남은 한계 |
| [docs/licenses.md](docs/licenses.md) | NVIDIA 드라이버 라이선스, CUDA EULA, 상표, 프로젝트 라이선스(미정) |
| [docs/secure-boot.md](docs/secure-boot.md) | Secure Boot와 커널 모듈 서명 설계(Phase 1, 3 예정) |
| [docs/memory.md](docs/memory.md) | 메모리 목표, 측정 방법 초안, 실측값(미측정) |
| [docs/support-matrix.md](docs/support-matrix.md) | GPU 아키텍처별 대상과 상태(전부 미검증) |
| [docs/verification-2026-10-10.md](docs/verification-2026-10-10.md) | 2026-10-10 검증 결과와 미검증 항목 |

구성 요소 설계 메모: [daemon/README.md](daemon/README.md), [gui/README.md](gui/README.md), [cli/README.md](cli/README.md), [llama/README.md](llama/README.md).

---

## 라이선스

프로젝트 자체 라이선스는 아직 정하지 않았다. 저장소에 LICENSE 파일이 없다. 포함되는 제3자 소프트웨어(NVIDIA 드라이버, CUDA 런타임, llama.cpp, Debian 패키지)는 각자의 라이선스를 따른다. 자세한 내용은 [docs/licenses.md](docs/licenses.md)에 있다.
