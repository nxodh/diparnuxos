# 테스트

Lightlix 의 테스트는 진단 분기, 완성된 ISO 검사, QEMU 부팅, 실기 체크리스트로 나뉜다.

| 경로 | 목적 | 실행 위치 |
|---|---|---|
| `tests/nvidia/gpu-check.sh` | GPU 없이 진단 분기 확인 (명령 스텁) | macOS 또는 Linux |
| `tests/nvidia/image.sh` | 완성된 ISO의 드라이버·모듈·키·라이선스, DHCP 클라이언트 중복, 저널·타이머·해석 설정, 배너, 금지 CUDA 패키지, 파티션 테이블 확인 | Debian 컨테이너 |
| `tests/qemu/boot-smoke.sh` | ISO 가 UEFI 로 부팅되어 로그인 프롬프트와 GPU 진단 줄까지 직렬 콘솔에 나오는지 확인. `BOOT_MEDIA` 로 CD-ROM 또는 USB 저장 장치 부팅을 고른다 | Linux (KVM 있으면 KVM, 없으면 TCG) |
| `tests/hw/run.sh` | 실 기기에서 확인할 체크리스트 중 현재 단계에서 확인 가능한 항목 실행 | 실 기기의 Lightlix 시스템 |
| `tests/hw/RESULT_TEMPLATE.md` | 실 기기 측정 결과를 옮겨 적는 양식 | 문서 |

`make lint` 는 셸 스크립트의 정적 검사를 수행한다. 검사 범위는 `Makefile` 에 정의된다.

## 1. ISO 부팅 스모크 테스트

### 목적

빌드된 ISO 가 QEMU 와 OVMF(UEFI 펌웨어)에서 부팅되고, 직렬 콘솔에 `lightlix login:` 과 `Lightlix GPU [no_gpu]:` 안내가 나오는지 확인한다.
이 프롬프트는 호스트 이름 `lightlix` 에 의존한다. `Lightlix GPU [no_gpu]:` 줄은 `/run/issue.d/*.issue` 를 agetty 가 로그인 프롬프트 위에 출력한 것이다. 직렬 포트의 getty 는 커널 명령줄의 `console=ttyS0,115200` 으로 systemd-getty-generator 가 만든다.

### 실행

```sh
make test-qemu          # 호스트의 QEMU 와 OVMF 로 실행
make test-qemu-docker   # Debian 컨테이너 안에서 실행 (호스트에 QEMU 가 없어도 된다. CI 가 쓰는 방식)
make test-qemu-usb-docker   # 같은 확인을 ISO 를 USB 저장 장치로 붙여 한다 (BOOT_MEDIA=usb)
# 또는 직접
tests/qemu/boot-smoke.sh out/lightlix-<버전>-amd64.iso
BOOT_MEDIA=usb tests/qemu/boot-smoke.sh out/lightlix-<버전>-amd64.iso
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

- 성공: 제한 시간 안에 `lightlix login:` 과 `Lightlix GPU [no_gpu]:` 가 모두 직렬 로그에 나타나면 종료 코드 0.
- 실패: 직렬 로그에 `Lightlix` 를 포함하고 ASCII 가 아닌 바이트가 있는 줄이 있으면 실패한다. 실기 텍스트 콘솔은 한글을 표시하지 못하기 때문이다.
- 실패: 프롬프트나 GPU 줄이 나오지 않고 제한 시간이 지나거나, 프롬프트 전에 QEMU 가 끝나면 로그 끝 80줄을 출력하고 종료 코드 1.
- 사용 오류(ISO 없음, 펌웨어나 QEMU 없음, 잘못된 인자)는 종료 코드 2.

### 가정

- GRUB 메뉴 타임아웃은 유한해야 한다. 스크립트는 키 입력을 보내지 않는다.
- 기본 OVMF 변수 템플릿에는 Secure Boot 키가 없으므로 Secure Boot 는 꺼진 상태로 부팅된다. Secure Boot 검증은 범위 밖이다.
- 네트워크는 없다(`-nic none`). 디스플레이도 없다(`-display none`). 메모리는 2G.
- 이 테스트는 직렬 로그만 본다. 모니터(VGA) 표시는 검사하지 않는다.

## 2. 실 하드웨어 체크리스트 (`tests/hw/run.sh`)

### 목적

실 기기에서 부팅한 뒤 확인해야 할 항목을 한 번에 점검한다. 전체 체크리스트의 순서는 다음과 같다.

1. 부팅 (os-release `ID=lightlix`)
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
| os-release 의 `ID=lightlix` | PASS 또는 FAIL |
| 커널 버전 | PASS (기록용) |
| UEFI 부팅 (`/sys/firmware/efi` 존재) | PASS 또는 FAIL |
| Secure Boot 상태 (`mokutil --sb-state`, mokutil 이 있을 때) | PASS(기록용) 또는 SKIP |
| `/proc/driver/nvidia/version` 존재와 Open Kernel Module | PASS 또는 FAIL |
| `nvidia-smi` 조회 성공 | PASS 또는 FAIL |
| compute_cap (`nvidia-smi` 가 있을 때) | PASS 또는 FAIL |

모델 로드, GPU 오프로드, 토큰/초는 Phase 2에서 구현하므로 SKIP이다. GPU 없는 QEMU에서는 이 실기 체크리스트를 통과할 수 없다.

### 출력과 종료 코드

항목마다 `PASS`, `FAIL`, `SKIP` 중 하나를 한 줄로 출력한다. FAIL 이 하나라도 있으면 종료 코드 1, 없으면 0.

테스트용 환경 변수: `LIGHTLIX_OS_RELEASE`, `LIGHTLIX_EFI_DIR`, `LIGHTLIX_NVIDIA_VERSION_FILE`.

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
