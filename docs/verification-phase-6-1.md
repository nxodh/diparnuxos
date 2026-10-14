# 0.6.1 검증 기록: CUDA 런타임 직접 받기, 소스 제공, 펌웨어, 라이선스

- 확인 날짜: 2026-10-14
- 상태: 컨테이너 시험과 단위 시험을 통과했다. **CI(CUDA 컴파일, ISO, 이미지 검사, QEMU E2E)와 실제 소스 수집은 아직 돌지 않았다.** 실제 GPU 는 미검증이다.

설계는 [phase-6-1-design.md](phase-6-1-design.md) 에 있다.

## 1. 확인한 것

| 대상 | 방법 | 결과 |
|---|---|---|
| 업데이트 엔진의 `cuda_runtime` | `update` 패키지 시험(가짜 apt/dpkg) | 통과: 설치와 hold, 두 번째 실행은 설치하지 않음, 라이브 허용, 재부팅 대기 거절(상태를 덮어쓰지 않음), 네트워크·저장소에 없음·여유 공간·설정 없음의 오류 코드 |
| 데몬 | `updatectl` 시험 | 통과: `cuda_runtime` 이 헬퍼 인자 `cuda-runtime` 으로 시작되고 라이브 세션에서도 시작됨. 다른 동작은 라이브 세션에서 거절. `Info` 에 `cuda_runtime` |
| CLI | `cmd/opereta` 시험 | 통과: `gpu runtime status`, 터미널이 아니면 `--yes` 없이 거절(다운로드가 시작되지 않음), `--yes` 로 시작 |
| Control Center | `make test-gui` | 88개 통과(CUDA 런타임 논리 4개 포함) |
| 소스 수집기 | `make test-sources-docker`(실제 Debian 저장소의 hello) | 통과: 정확한 버전의 `.dsc` 와 upstream 소스, 프로젝트·NVIDIA 패키지 건너뛰기와 이유 기록, 없는 버전은 실패, 한도보다 큰 패키지는 알림 |
| 릴리스 자산 | `make test-release-docker` | 통과: ISO 를 올릴 때 소스가 없으면 거절, 있으면 소스와 목록이 함께 모임 |
| Intel 펌웨어 훅 | 가짜 `firmware` 트리에서 실행 | `ice_comms`, `ice_wireless_edge` 만 지워지고 `ice-*.pkg`, `ice_lag` 는 남음. `path-exclude` 4줄 |
| 흔적, 비밀, 개인 정보, 린트 | `make lint check-legacy-name check-secrets` | 통과 |

## 2. 확인하지 못한 것

| 항목 | 이유 |
|---|---|
| CUDA 컴파일, `libggml-cuda.so` 가 런타임 없이도 CPU 로 도는지 | CI 에서만(약 1시간). 이 환경은 Apple Silicon 이다 |
| 이미지 검사(런타임 패키지와 파일이 없음, Intel 파일이 없음, nvidia.conf) | ISO 가 있어야 한다. CI 의 `test-nvidia-image-docker` |
| 실제 NVIDIA 저장소에서 런타임을 받는 경로 | 시험은 가짜 apt 다. 실제 설치는 CI 에서도 하지 않는다(GPU 가 없고 약 360 MiB). 첫 실기에서 확인한다 |
| 실제 소스 수집(이미지 전체) | ISO 의 패키지 목록이 있어야 한다. 릴리스 빌드에서 처음 돈다. 크기와 시간은 그때 측정한다 |
| 실제 GPU 추론 | GPU 가 없다 |

## 3. 이번에 알게 된 것

- **번들링을 만들었다가 버렸다**: 처음에는 NVIDIA 라이브러리를 `opereta-llm` 의 전용 디렉터리에 원본 그대로 넣는 방식(EULA 부속서 A 의 "응용 전용")을 만들어 합성 라이브러리와 실제 NVIDIA 파일로 확인했다. 그러나 사용자가 "직접 받게 하면?"을 물었고, 직접 받으면 재배포 자체가 사라져 법적 쟁점이 더 단순해서 방식을 바꿨다. 번들링 코드와 시험은 삭제했다(저장소 이력에는 남아 있다).
- **드라이버에는 CUDA 런타임이 없다**: NVIDIA 의 `nvidia-driver-cuda` 는 `libcuda` 등을 주고 `cudart`/`cublas` 는 별도의 툴킷 패키지다.
- **GPL 소스는 정확한 버전이어야 한다**: 저장소에서 최신 소스만 받으면 이미지의 버전과 달라질 수 있다. apt 로 `src=버전` 을 지정하고, 사라졌으면 snapshot 에서 받아 `.dsc` 해시로 확인한다.
