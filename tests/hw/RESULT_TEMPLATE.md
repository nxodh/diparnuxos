# 실 하드웨어 테스트 결과

> 이 문서는 `tests/hw/run.sh` 의 출력을 옮겨 적는 양식이다.
> 실 하드웨어에서 측정한 값만 기록하고, 측정하지 않은 항목은 SKIP 또는 빈칸으로 둔다.
> 한 번의 기록이 특정 기기의 "검증 완료"를 뜻하지는 않는다.

## 1. 기기 정보

| 항목 | 값 |
|---|---|
| 측정 날짜 | |
| 기기 모델 (메인보드/노트북) | |
| CPU | |
| 메모리 | |
| GPU 모델 | |
| 노트북 하위 모델 (예: Stealth 15M A11UEK/B12UE) | |
| Wi-Fi 칩 (`lspci` 결과, 예: AX200 은 PCI ID 8086:2723) | |
| VRAM / 내장 GPU / 그래픽 모드 | |
| GPU 아키텍처 (RTX 30 / 40 / 50) | |
| compute_cap (nvidia-smi) | |
| NVIDIA 드라이버 버전 | |
| 드라이버 종류 (오픈 커널 모듈 / 독점 모듈) | |
| 커널 버전 (`uname -r`) | |
| Lightlix 버전 (`os-release` 의 VERSION_ID) | |
| UEFI 부팅 여부 | |
| Secure Boot 상태 | |
| ISO 파일명과 SHA-256 | |

## 2. 항목별 결과

결과 칸에는 `PASS`, `FAIL`, `SKIP` 중 하나를 적는다. SKIP 은 사유를 비고에 적는다.

| 순서 | 항목 | 기대값 | 결과 | 비고 |
|---|---|---|---|---|
| 1 | 부팅 (os-release `ID=lightlix`) | `ID=lightlix` | | |
| 2 | UEFI 부팅 | `/sys/firmware/efi` 존재 | | |
| 3 | Secure Boot 상태 (기록용) | 기록만 함 | | |
| 4 | 오픈 커널 모듈 로드 | `/proc/driver/nvidia/version` 에 `Open Kernel Module` | | Phase 1 |
| 5 | nvidia-smi 동작 | GPU 목록이 나옴 | | Phase 1 |
| 6 | compute_cap | `8.6`, `8.9`, `12.0` 중 하나 | | RTX 30 / 40 / 50 |
| 7 | 모델 로드 | 지정 GGUF 모델이 메모리에 올라감 | | LLM 스택 |
| 8 | GPU 오프로드 | 모델 레이어가 GPU 에 올라감 | | LLM 스택 |
| 9 | 토큰/초 측정 | 아래 3번 표에 기록 | | LLM 스택 |
| 10 | 무선 인터페이스와 iwd | `wl*` 인터페이스가 있고 `iwd.service` 가 active (무선 장치가 없으면 SKIP) | | Wi-Fi |
| 11 | GPU 진단 상태 (`gpu-status`의 `LIGHTLIX_GPU_STATUS`) | `ready` (`no_gpu`는 SKIP, 그 밖의 상태는 FAIL로 상태를 적는다) | | `docs/drivers.md` GPU 진단 계약 |
| 12 | 쓸 수 있는 GPU 수 (`LIGHTLIX_GPU_USABLE_COUNT`) | `ready` 일 때 1 이상 | | `ready` 가 아니면 SKIP |
| 13 | DRM 장치 | `ready` 일 때 `/dev/dri/card*` 가 있다 | | `nvidia-drm` 로드 확인 |
| 14 | 로드된 드라이버 버전과 고정 버전 | `/proc/driver/nvidia/version` 의 버전이 `/usr/share/lightlix/nvidia.conf` 의 `NVIDIA_DRIVER_VERSION` 과 같다 | | 드라이버가 없으면 SKIP |

## 3. 토큰/초 측정

| 모델 | 양자화 | 컨텍스트 길이 | GPU 오프로드 레이어 수 | 프롬프트 토큰/초 | 생성 토큰/초 | 측정 방법 (명령) |
|---|---|---|---|---|---|---|
| | | | | | | |

## 4. 특이사항과 남은 문제

- 노트북: 내부 화면, USB-C/HDMI 출력, Wi-Fi, AC/배터리, 덮개 닫기 동작을 각각 기록한다. 아직 구현되지 않은 항목은 SKIP으로 둔다.

## 5. 실행 로그

`tests/hw/run.sh` 의 전체 출력을 붙인다.

```text

```
