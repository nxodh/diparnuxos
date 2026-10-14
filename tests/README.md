# 테스트

Opereta 의 테스트는 진단 분기, 완성된 ISO 검사, QEMU 부팅, 실기 체크리스트로 나뉜다.

| 경로 | 목적 | 실행 위치 |
|---|---|---|
| `tests/nvidia/gpu-check.sh` | GPU 없이 진단 분기 확인 (명령 스텁) | macOS 또는 Linux |
| `tests/nvidia/image.sh` | 완성된 ISO의 드라이버·모듈·키·라이선스, DHCP 클라이언트 중복, 저널·타이머·해석 설정, 배너, 금지 CUDA 패키지, 파티션 테이블 확인 | Debian 컨테이너 |
| `tests/qemu/boot-smoke.sh` | ISO 가 UEFI 로 부팅되어 로그인 프롬프트와 GPU 진단 줄까지 직렬 콘솔에 나오는지 확인. `BOOT_MEDIA` 로 CD-ROM 또는 USB 저장 장치 부팅을 고른다 | Linux (KVM 있으면 KVM, 없으면 TCG) |
| `tests/qemu/cpu-inference.sh` | UEFI 직렬 로그인, 실제 Hugging Face 내려받기·SHA-256, 데몬 권한, CPU 추론·중지와 오류 응답 확인 | Linux QEMU/OVMF 또는 Debian Docker |
| `tests/qemu/test_cpu_inference.py` | 제어된 직렬 프로세스로 E2E 드라이버의 실패 판정과 정리 경로 확인. 실제 ISO 검증은 하지 않는다 | Python 3 표준 라이브러리 |
| `tests/hw/run.sh` | 실 기기에서 확인할 체크리스트 중 현재 단계에서 확인 가능한 항목 실행 | 실 기기의 Opereta 시스템 |
| `tests/hw/RESULT_TEMPLATE.md` | 실 기기 측정 결과를 옮겨 적는 양식 | 문서 |

`make lint` 는 셸 스크립트의 정적 검사를 수행한다. 검사 범위는 `Makefile` 에 정의된다.

## 1. ISO 부팅 스모크 테스트

### 목적

빌드된 ISO 가 QEMU 와 OVMF(UEFI 펌웨어)에서 부팅되고, 직렬 콘솔에 `opereta login:` 과 `Opereta GPU [no_gpu]:` 안내가 나오는지 확인한다.
이 프롬프트는 호스트 이름 `opereta` 에 의존한다. `Opereta GPU [no_gpu]:` 줄은 `/run/issue.d/*.issue` 를 agetty 가 로그인 프롬프트 위에 출력한 것이다. 직렬 포트의 getty 는 커널 명령줄의 `console=ttyS0,115200` 으로 systemd-getty-generator 가 만든다.

### 실행

```sh
make test-qemu          # 호스트의 QEMU 와 OVMF 로 실행
make test-qemu-docker   # Debian 컨테이너 안에서 실행 (호스트에 QEMU 가 없어도 된다. CI 가 쓰는 방식)
make test-qemu-usb-docker   # 같은 확인을 ISO 를 USB 저장 장치로 붙여 한다 (BOOT_MEDIA=usb)
# 또는 직접
tests/qemu/boot-smoke.sh out/opereta-<버전>-amd64.iso
BOOT_MEDIA=usb tests/qemu/boot-smoke.sh out/opereta-<버전>-amd64.iso
```

### 환경 변수

| 변수 | 기본값 | 설명 |
|---|---|---|
| `OVMF_CODE` | 후보 경로에서 탐색 | UEFI 코드 이미지(읽기 전용) |
| `OVMF_VARS_TEMPLATE` | 후보 경로에서 탐색 | UEFI 변수 템플릿. 실행마다 임시 사본을 쓴다 |
| `BOOT_MEDIA` | `cdrom` | `cdrom`: ISO 를 가상 광학 드라이브로 붙인다. `usb`: ISO 를 xHCI 컨트롤러의 읽기 전용 usb-storage 장치로 붙인다. 다른 값은 거부한다. `make test-qemu-docker` 와 `scripts/test-qemu-in-docker.sh` 도 이 값을 넘긴다 |
| `BOOT_TIMEOUT` | KVM 180초, TCG 900초 | 로그인 프롬프트를 기다리는 제한 시간 |
| `QEMU_BIN` | `qemu-system-x86_64` | QEMU 실행 파일 |

OVMF 후보 경로는 Debian 13(`ovmf` 패키지)과 Ubuntu 24.04(`ovmf` 패키지)의 파일 목록에서 확인한 `/usr/share/OVMF/OVMF_CODE_4M.fd` 와 `/usr/share/OVMF/OVMF_VARS_4M.fd` 만 넣었다. 다른 배포판은 환경 변수로 지정한다.

### 판정

- 성공: 제한 시간 안에 `opereta login:` 과 `Opereta GPU [no_gpu]:` 가 모두 직렬 로그에 나타나면 종료 코드 0.
- 실패: 직렬 로그에 `Opereta` 를 포함하고 ASCII 가 아닌 바이트가 있는 줄이 있으면 실패한다. 실기 텍스트 콘솔은 한글을 표시하지 못하기 때문이다.
- 실패: 프롬프트나 GPU 줄이 나오지 않고 제한 시간이 지나거나, 프롬프트 전에 QEMU 가 끝나면 로그 끝 80줄을 출력하고 종료 코드 1.
- 사용 오류(ISO 없음, 펌웨어나 QEMU 없음, 잘못된 인자)는 종료 코드 2.

### 가정

- GRUB 메뉴 타임아웃은 유한해야 한다. 스크립트는 키 입력을 보내지 않는다.
- 기본 OVMF 변수 템플릿에는 Secure Boot 키가 없으므로 Secure Boot 는 꺼진 상태로 부팅된다. Secure Boot 검증은 범위 밖이다.
- 네트워크는 없다(`-nic none`). 디스플레이도 없다(`-display none`). 메모리는 2 GiB.
- 이 테스트는 직렬 로그만 본다. 모니터(VGA) 표시는 검사하지 않는다.

## Phase 2 CPU 추론 E2E

`boot-smoke.sh` 다음 단계로, 완성된 **amd64 Phase 2 ISO**를 OVMF에서 CD-ROM으로 부팅해 `user` / `live`로 직렬 로그인한다.
`opereta --json status`, `model pull ggml-org/tiny-llamas --file stories260K.gguf`, `model default`, `serve plan`, `serve start`, `chat`, `serve stop`을 실제 라이브 사용자가 실행한다.
자동 계획을 확인하려고 `--ngl`은 지정하지 않는다. 작은 모델의 컨텍스트와 배치만 `--ctx 256 --batch 32`로 제한하고, 채팅은 16토큰으로 제한한다.
게스트에는 4096 MiB를 할당한다. 모델 다운로드 뒤에도 데몬의 기존 1 GiB 여유 공간 검사를 만족시키기 위한 E2E 환경 설정이다. 다운로드 직전 루트 파일시스템 용량·여유 공간과 게스트 메모리 정보를 로그에 남긴다.

```sh
# ISO 는 별도 빌드나 CI 산출물을 사용한다. 이 실행기는 ISO 를 만들지 않는다.
bash scripts/test-qemu-e2e-in-docker.sh out/opereta-<버전>-amd64.iso
# Linux 에 QEMU, OVMF, Python 3 이 이미 있을 때
bash tests/qemu/cpu-inference.sh out/opereta-<버전>-amd64.iso
# ISO 없이 드라이버와 Docker 실행기의 제어 흐름만 검사한다.
python3 -B tests/qemu/test_cpu_inference.py
```

### 검사 항목

- 게스트가 `x86_64` UEFI이며 로그인 사용자가 root가 아닌 `user`인지, 현재 세션에 `opereta` 그룹이 있는지 확인한다.
- 제어 소켓이 실제 소켓이고 `root:opereta`, `0660`인지 확인한다. `nobody`의 접근 거부와 없는 소켓의 CLI 종료 코드 3도 검사한다.
- `operetad.socket`, `operetad.service`, 기본 방화벽, GPU 진단 유닛이 활성 상태인지 확인한다. llama 유닛은 시작 전·중지 후 inactive, 추론 중 active/running이어야 하며 데몬 PID와 systemd MainPID가 같아야 한다.
- 모든 관련 유닛은 loaded이어야 하며 failed나 오류 Result를 거부한다. 저장된 방화벽 유닛은 첫 부팅에 파일이 없어 inactive일 수 있다.
- 없는 모델의 `no_model`, 시작 전·중지 후 채팅의 `server_not_running`을 종료 코드 1과 오류 코드로 검사한다.
- Hugging Face 다운로드 작업의 완료 상태·바이트 수와 모델 목록의 저장소·SHA-256·완전성을 검사한다. 게스트에서 실제 GGUF 파일에 `sha256sum`을 실행해 고정 해시와 다시 대조한다.
- 모델의 기본값 저장을 확인하고 CPU 계획과 실행 상태가 `running_cpu_fallback`인지, NGL=0·오프로드 0·CPU 경고·loopback 바인딩인지 확인한다.
- CLI JSON의 `content`에 공백 이외의 생성 텍스트가 있고 토큰 수·초당 토큰이 양수인지 확인한다. 중지 뒤 PID가 0이고 서버 오류가 남지 않아야 한다.
- 실행 중 `autostart`를 끄고 켠 뒤 데몬을 각각 재시작한다. CLI가 데몬 연결 불가를 반환할 때만 1초 간격으로 최대 10회 재시도한다. 연결 뒤 상태 오류는 즉시 실패하고, 같은 모델·llama PID·256 컨텍스트·32 배치와 양수 생성 결과를 확인한다.

고정 모델 해시는 `047bf46455a544931cff6fef14d7910154c56afbc23ab1c5e56a72e69912c04b`다.
2026-10-11에 [Hugging Face 원본 파일 페이지](https://huggingface.co/ggml-org/tiny-llamas/blob/main/stories260K.gguf)와 기존 패키지 테스트에서 대조했다.
모델의 `main` 내용이 바뀌면 테스트는 해시 불일치로 실패하므로 원본을 검토한 뒤에만 해시를 갱신한다.

정상 추론·설정 명령은 라이브 사용자로 실행한다. **게스트 안에서만** 모델 파일 해시·system journal을 읽거나 `nobody` 접근을 시도하고, 실행 중 데몬을 재시작할 때 `sudo -n`을 쓴다.
호스트에는 도구를 설치하지 않으며, 호스트 sudo나 특권 Docker 컨테이너를 요구하지 않는다. 게스트에 Python·jq를 추가하지 않는다.
네트워크는 Docker 브리지와 QEMU `-nic user,model=virtio-net-pci,ipv6=off` NAT를 쓴다. 모델을 호스트에서 주입하거나 다운로드 응답을 대신하지 않는다.
DNS, DHCP, HTTPS, Hugging Face가 실제로 동작해야 통과한다. 네트워크 오류나 서비스 오류를 SKIP으로 처리하지 않는다.

### 시간 제한과 가속

| 변수 | KVM 기본값 | TCG 기본값 | 설명 |
|---|---:|---:|---|
| `QEMU_ACCEL` | `auto` | `auto` | `auto`, `kvm`, `tcg`. auto는 사용 가능한 KVM을 고르고, 장치가 없거나 KVM 초기화가 실패하면 TCG를 쓴다. 강제 kvm은 실패를 그대로 보고한다 |
| `BOOT_TIMEOUT` | 180초 | 1800초 | 로그인 단계마다 기다리는 상한. 전체 실행 상한도 함께 적용한다 |
| `COMMAND_TIMEOUT` | 60초 | 180초 | 상태·권한·systemd 검사 등 일반 명령 |
| `PULL_TIMEOUT` | 600초 | 1800초 | 실제 다운로드 작업 완료 |
| `START_TIMEOUT` | 180초 | 900초 | CLI가 서버 준비 상태를 기다리는 상한 |
| `CHAT_TIMEOUT` | 180초 | 300초 | 짧은 생성 요청 |
| `E2E_TIMEOUT` | 1800초 | 5400초 | 전체 드라이버 상한 |
| `E2E_LOG_DIR` | `out/qemu-e2e` | 동일 | 실행마다 고유한 `run-*` 하위 폴더를 만들고 로그를 보존한다 |

양의 정수인 초 단위만 허용한다. 명령은 게스트 `timeout`으로도 제한하고, 직렬 완료 마커는 종료 신호 처리를 위해 15초까지 추가로 기다린다.
TCG 기본값은 ARM Mac의 중첩 에뮬레이션을 고려한 유한 상한이며 실제 소요 시간은 아직 측정하지 않았다.
외부 제한 시간을 늘려도 CLI·데몬에 내장된 API·모델 로드·채팅 제한은 바뀌지 않는다. 내부 제한에 걸리면 원래 오류를 보고한다.
네이티브 실행은 `QEMU_BIN`, `OVMF_CODE`, `OVMF_VARS_TEMPLATE`도 지정할 수 있다. Docker 실행기는 컨테이너의 Debian 기본 파일을 쓴다.
OVMF 변수는 임시 사본만 쓰고, Secure Boot·USB E2E·GPU 오프로드는 이 테스트의 검사 범위에 포함되지 않는다.

### 로그와 실패 판정

실행 디렉터리에 `serial.log`(직렬 원문), `qemu.log`(QEMU 표준 오류), `qemu-command.jsonl`(실제 QEMU 인자),
`commands.jsonl`(게스트 명령·기대 종료 코드·실제 종료 코드), 단계별 `.txt`, `result.json`을 남긴다.
성공·실패·신호 중단 모두 QEMU 프로세스를 회수하고 임시 OVMF 사본을 지운다. 실행별 새 로그 디렉터리는 0755, 로그 파일은 0644로 만들어 Docker의 root 프로세스가 작성해도 CI runner가 읽을 수 있다. 기존 실행 로그의 권한은 바꾸지 않는다. Docker 실행기도 trap으로 이번 컨테이너와 CID 임시 파일을 지운다.
로그 디렉터리와 기존 `out/drive-upload` 등 다른 산출물은 지우지 않는다.

- 종료 코드 0: 모든 검사 통과.
- 종료 코드 1: 추론·상태·JSON·권한 검사 실패 또는 시간 초과.
- 종료 코드 2: ISO·도구·펌웨어 없음, 잘못된 설정 등 사용 오류.
- 신호 중단: SIGINT 130, SIGTERM 143.

`result.json`의 `kind`로 QEMU 조기 종료(`qemu_exit`), 직렬 대기 초과(`serial_timeout`), 전체 제한 초과(`total_timeout`),
게스트 timeout(`command_timeout`), 그 밖의 명령 종료(`command_exit`), JSON 오류(`invalid_json`), 검사 불일치(`assertion`)를 구분한다.
실패를 성공 표시로 덮거나 자동으로 재시도하지 않는다. `auto`에서 게스트 출력 전 KVM 초기화 실패만 TCG로 한 번 재시도한다.

**실제 ISO CPU 추론 E2E는 아직 미검증이다.** 이 작업에서는 ISO를 빌드하거나 부팅하지 않는다.
제어된 fixture 결과는 드라이버 검증에 한정되며, CI에서 Phase 2 ISO를 만든 뒤 실제 E2E를 실행하고 항상 로그를 업로드해야 한다.

## 2. 실 하드웨어 체크리스트 (`tests/hw/run.sh`)

### 목적

실 기기에서 부팅한 뒤 확인해야 할 항목을 한 번에 점검한다. 전체 체크리스트의 순서는 다음과 같다.

1. 부팅 (os-release `ID=opereta`)
2. 오픈 커널 모듈 로드 (`/proc/driver/nvidia/version` 에 `Open Kernel Module`)
3. `nvidia-smi` 동작
4. compute_cap 확인 (8.6 / 8.9 / 12.0 과 대조)
5. 모델 로드
6. GPU 오프로드 확인
7. 토큰/초 기록

### 실행

```sh
tests/hw/run.sh
```

`mokutil --sb-state` 가 인식할 수 없는 출력을 내면 secure-boot 항목은 SKIP 이다. `nvidia-smi` 가 있는데 compute_cap 쿼리가 실패하면 compute-cap 항목은 원인 메시지와 함께 FAIL 이다. 권한 요구 여부는 실 기기에서 아직 확인하지 않았다.

### Phase 1 에서 수행하는 것

| 항목 | 판정 |
|---|---|
| os-release 의 `ID=opereta` | PASS 또는 FAIL |
| 커널 버전 | PASS (기록용) |
| UEFI 부팅 (`/sys/firmware/efi` 존재) | PASS 또는 FAIL |
| Secure Boot 상태 (`mokutil --sb-state`, mokutil 이 있을 때) | PASS(기록용) 또는 SKIP |
| `/proc/driver/nvidia/version` 존재와 Open Kernel Module | PASS 또는 FAIL |
| `nvidia-smi` 조회 성공 | PASS 또는 FAIL |
| compute_cap (`nvidia-smi` 가 있을 때) | PASS 또는 FAIL |

모델 로드, GPU 오프로드, 토큰/초는 Phase 2에서 구현하므로 SKIP이다. GPU 없는 QEMU에서는 이 실기 체크리스트를 통과할 수 없다.

### 출력과 종료 코드

항목마다 `PASS`, `FAIL`, `SKIP` 중 하나를 한 줄로 출력한다. FAIL 이 하나라도 있으면 종료 코드 1, 없으면 0.

테스트용 환경 변수: `OPERETA_OS_RELEASE`, `OPERETA_EFI_DIR`, `OPERETA_NVIDIA_VERSION_FILE`.

### 결과 기록

`tests/hw/RESULT_TEMPLATE.md` 를 복사해 기기 정보와 항목별 결과, 토큰/초를 적는다. 측정하지 않은 항목은 비워 둔다.

## 3. Phase 1 자동 검증 범위

### 자동 검증됨 (CI 와 로컬 실행)

- 셸 스크립트 린트 (`make lint`)
- GPU 진단의 11개 시나리오 (`make test-gpu-check`, RTX 3060 Laptop GPU 이름 포함, 실기 검증 아님)
- ISO 빌드 성공 여부 (`make iso`, CI 의 `iso` 잡)
- ISO 의 UEFI 부팅과 직렬 콘솔 로그인 프롬프트, CD-ROM 과 USB 저장 장치 두 경우 (`make test-qemu-docker`, `make test-qemu-usb-docker`, KVM 이 없으면 TCG)
- 아티팩트 업로드 (ISO 와 `.sha256`)
- 완성된 ISO의 고정 패키지·모듈 파일·계약서 전문과 서명 키 제거 (`make test-nvidia-image-docker`)
- 완성된 ISO의 DHCP 클라이언트 제거, 저널 램 설정, apt 타이머 마스크, apt 목록과 deb-src 없음, resolved 설정, 로그인 배너, 금지 CUDA 개발 패키지, GPT 와 EFI System 파티션
- GPU 없는 QEMU에서 CPU 폴백 안내 (추론 엔진 실행은 아직 구현하지 않음)

### 자동 검증되지 않음 (후속 단계 또는 실 하드웨어 필요)

- 실제 NVIDIA GPU 에서의 드라이버 로드, `nvidia-smi`, compute_cap 값
- 오픈 커널 모듈, CUDA, llama.cpp, 모델 로드, GPU 오프로드, 토큰/초
- Secure Boot 가 켜진 상태의 부팅 (QEMU 테스트는 Secure Boot 가 꺼진 OVMF 변수 템플릿을 쓴다)
- 실 기기 UEFI 부팅 경로와 하드웨어 호환성
- 모니터(VGA) 화면의 표시 내용. 직렬 로그만 자동 검사한다.
- 실기 USB 기록 매체의 부팅. QEMU 의 usb-storage 로만 확인한다.
- GUI, 설치기

위 항목은 실 하드웨어에서 `tests/hw/run.sh` 와 `RESULT_TEMPLATE.md` 로 확인한다. 한 번의 결과가 특정 기기를 "지원"한다는 뜻은 아니다.
