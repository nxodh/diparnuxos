# 지원 매트릭스

- 확인 날짜: 2026-10-11
- 상태: 초안. 모든 행은 **미검증**이다.

> 실 하드웨어에서 검증하기 전에는 어떤 GPU에서도 동작을 주장하지 않는다. 이 문서의 행은 대상 범위와 확인 근거를 적는 자리이며, 검증 결과가 아니다. 상태가 바뀌려면 실기 테스트 기록(날짜, 이미지 버전, 드라이버 버전, 결과)이 있어야 한다.

CUDA 모듈을 포함한 패키지의 CPU 추론은 [CI 38131367776](https://github.com/nxodh/operetaos/actions/runs/38131367776)에서 CUDA 없는 Debian 컨테이너로 확인했다. [CI 38142350756](https://github.com/nxodh/operetaos/actions/runs/38142350756)(`035dfb4`)에서는 GPU 없는 QEMU/KVM VM의 live ISO가 모델 다운로드, CPU 서버 시작, 채팅, 데몬 재시작 뒤 채택, 정상 중지까지 통과했다. 이 두 결과는 CPU 경로이며 어떤 GPU에서의 동작도 입증하지 않는다. 아래 GPU 행은 모두 실기 미검증이다.

---

## 1. GPU 아키텍처별 대상

| 아키텍처 | Compute capability | 대표 GeForce 제품 (nvcc 문서 기준) | CUDA 아키텍처 플래그 | 드라이버 모듈 | 상태 | 비고 |
|---|---|---|---|---|---|---|
| Ampere | 8.6 | RTX 3090 Ti ~ RTX 3050 | `86-real` | 오픈 커널 모듈 | 미검증 | 595.91.07 supportedchips 목록에 RTX 3050~3090 Ti가 있다(검증 기록 check 12). |
| Ada Lovelace | 8.9 | RTX 4090 ~ RTX 4050 | `89-real` | 오픈 커널 모듈 | 미검증 | 595.91.07 supportedchips 목록에 RTX 4060~4090 D가 있다. |
| Blackwell | 12.0 | RTX 5090, 5080, 5070 Ti, 5070, 5060 Ti, 5060, 5050 | `120a-real` | 오픈 커널 모듈만 지원 | 미검증 | 독점 모듈은 Blackwell을 지원하지 않는다(595.91.07 README). RTX 50은 Secure Boot와 드라이버 문제에 특히 민감하다([secure-boot.md](secure-boot.md) 5절). |
| CPU 폴백 | — | GPU 없음 | — (`GGML_CPU_ALL_VARIANTS=ON`) | 해당 없음 | 미검증 | GPU가 없거나 드라이버가 올라오지 않을 때 llama.cpp CPU 변형으로 동작하는지 확인한다. |

- 위 제품 목록은 nvcc 문서와 595.91.07 supportedchips 목록을 조사 기록에서 옮긴 것이다. 제품을 하나씩 대조한 목록이 아니다.
- Turing(sm_75)보다 오래된 아키텍처와 RTX 20 계열은 대상 범위에 넣지 않는다.
- CUDA 13에서 Maxwell, Pascal, Volta 지원이 제거되었다. nvcc 기본 아키텍처는 sm_75다(nvcc 문서, 조사 기록).

RTX 3060 Laptop GPU도 대상에 포함한다. NVIDIA 595.91.07의 [공식 지원 목록](https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/supportedchips.html)에 해당 제품이 있으며, GPU 진단은 `Laptop GPU` 접미사가 있는 이름도 허용한다. 명령 스텁으로 이 이름의 판별을 확인했으며, 노트북 실기는 미검증이다. 하이브리드 그래픽/Optimus 구성은 제조사별로 다르므로 내장 GPU와 NVIDIA GPU의 인식, CUDA 실행, 화면 출력, 전원 관리를 따로 확인해야 한다.

근거:
- https://docs.nvidia.com/cuda/cuda-compiler-driver-nvcc/index.html (CUDA GPU 표와 허용 아키텍처 목록: `sm_86`, `sm_89`, `sm_120`)
- https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kernel_open.html ("Blackwell and later are only supported by the open kernel modules")
- https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/ (supportedchips 목록)

---

## 2. 검증 항목별 상태

| 대상 | 드라이버 | 부팅 | 화면(Xfce, Xorg) | CUDA 추론 (`llama-server`) | 상태 |
|---|---|---|---|---|---|
| RTX 30 (8.6) | 595.91.07 | 미검증 | 미검증 | 미검증 | 미검증 |
| RTX 3060 Laptop GPU (8.6) | 595.91.07 | 미검증 | 미검증 | 미검증 | 미검증 |
| RTX 40 (8.9) | 595.91.07 | 미검증 | 미검증 | 미검증 | 미검증 |
| RTX 50 (12.0) | 595.91.07 | 미검증 | 미검증 | 미검증 | 미검증 |
| CPU 폴백 | 없음 | 미검증 | 미검증 | 미검증 | 미검증 |

- "드라이버" 열의 595.91.07은 Phase 1 CI 이미지에서 설치를 확인한 버전이다. 실기에 로드된 버전은 해당 기기의 테스트 때 기록한다.
- 검증 방법과 수용 기준은 [drivers.md](drivers.md) 12절에 있다.

---

## 3. 상태 기준

| 상태 | 뜻 |
|---|---|
| 미검증 | 기본값. 실기 테스트 기록이 없다. |
| 검증 중 | 테스트가 시작되었으나 수용 기준을 아직 통과하지 못했다. |
| 확인됨 | 테스트 기록(날짜, 이미지 버전, 드라이버 버전, 결과)이 있고 수용 기준을 통과했다. |
| 실패 | 테스트에서 수용 기준을 통과하지 못했다. 원인과 기록을 함께 적는다. |

현재 모든 행은 "미검증"이다. 이 문서에서 "확인됨"으로 바꾸기 전에는 해당 GPU를 지원한다고 적지 않는다.

---

## 4. 열린 문제

- RTX 30과 40에서 오픈 커널 모듈의 지원 범위를 supportedchips 표에서 다시 확인한다. 이번 조사는 두 계열이 목록에 있다는 사실만 확인했다.
- 각 행의 실기 테스트 계획(장비, 담당, 날짜)을 정한다.
- CPU 폴백에서 llama.cpp의 CPU 변형이 실제로 고르게 선택되는지 확인한다.
- llama.cpp v0.6.0의 제3자 보고 (조사 기록 A6, 2026-10-11). 아래 항목은 모두 제3자 사용자가 쓴 이슈다. 이 빌드(v0.6.0 커밋 `d812350`)에서 재현하지 않았다. 대상 GPU가 RTX 50인 실기에서 확인할 검증 항목으로 둔다.
  - [#28581](https://github.com/ggml-org/llama.cpp/issues/28581) (열림): RTX 5060 Ti(sm_120), CUDA 13.2 `nvcc V13.2.78`에서 IQ3_S 양자화가 쓰레기 출력을 낸다. 이 패키지의 nvcc는 13.2.86이므로 같은 증상인지 모른다. 관련 닫힌 이슈 #21255는 NVIDIA 쪽 답이 "컴파일러 변경"이라는 내용뿐이다.
  - [#26901](https://github.com/ggml-org/llama.cpp/issues/26901) (닫힘, stale, 해결 아님): Blackwell dGPU(RTX 5080/5070 Ti)가 iGPU로 분류되어 다중 GPU 분할이 깨진다. v0.6.0 소스 `ggml-cuda.cu:5205`에 `prop.integrated ? IGPU : GPU` 코드가 그대로 있다.
  - [#25060](https://github.com/ggml-org/llama.cpp/issues/25060) (열림): RTX 5090, CUDA 13.3, 드라이버 580.17에서 SOFT_MAX 실패. `sharedMemPerBlockOptin` 값이 이상하게 읽힌다는 보고다. 보고자의 CUDA와 드라이버가 이 패키지(CUDA 13.2, 드라이버 595)와 다르다.
  - [#27330](https://github.com/ggml-org/llama.cpp/issues/27330) (열림): RTX 5090 Laptop(sm_120)에서 CUDA graph로 GPU 채널이 멈춘다. 보고자의 우회는 `GGML_CUDA_DISABLE_GRAPHS=1`이다. v0.6.0의 `ggml-cuda.cu` getenv 목록에서 이 변수를 찾지 못했다(다른 파일에 있는지는 미확인).
  - [#28328](https://github.com/ggml-org/llama.cpp/issues/28328) (열림): CPU 변형 점수가 OS의 XSAVE 지원을 확인하지 않아, 그런 환경에서 SIGILL이 날 수 있다. 이슈 본문 기준이며 직접 재현하지 않았다.

## 5. MSI Stealth 15M 실기 확인 계획

사용자가 RTX 3060 노트북으로 MSI Stealth 15M을 제시했다. 정확한 하위 모델과 실제 장치 ID는 아직 확인하지 못했다. MSI 사양에서 [A11UEK](https://kr.msi.com/Laptop/Stealth-15M-A11UX/Specification)와 [B12UE](https://www.msi.com/Laptop/Stealth-15M-B12UX/Specification)는 모두 RTX 3060 Laptop GPU와 VRAM 6GB를 사용한다. GPU 자체는 위 대상 범위에 들어간다. 이 확인은 Opereta 실기 부팅 성공을 뜻하지 않는다.

- Phase 1: Secure Boot를 끈 UEFI 부팅, 오픈 모듈 로드, `nvidia-smi`의 이름·버전·8.6 조회, `/run/opereta/gpu-status`를 확인한다.
- 현재 ISO에는 Intel Wi-Fi 펌웨어(`firmware-iwlwifi`)와 무선 접속 데몬 iwd를 넣었다. 실기 접속은 미검증이다. 노트북의 실제 Wi-Fi 칩(AX200 추정)은 `lspci`로 확인해야 한다. GPU 드라이버 대상 여부와 노트북의 네트워크 사용 가능 여부를 따로 기록한다.
- Phase 2: CUDA 실행과 모델 GPU 오프로드를 확인한다. VRAM 6GB는 모델과 KV 캐시가 함께 사용하는 한도이므로 모델 크기만으로 실행 가능성을 판단하지 않는다.
- Phase 3: 내장 GPU의 내부 화면 출력과 NVIDIA GPU의 추론을 함께 검증한다. USB-C/HDMI 출력은 포트별 배선을 확인한다.
- 서버 모드에서는 절전 관련 target을 마스크한다. 배터리 사용과 덮개 닫기 동작은 아직 튜닝하거나 검증하지 않았다.
