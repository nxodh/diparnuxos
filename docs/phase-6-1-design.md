# 0.6.1 설계: CUDA 런타임을 llama-server 전용으로 옮긴다

- 확인 날짜: 2026-10-14
- 상태: 구현했고 컨테이너 시험을 통과했다. CI 결과는 [verification-phase-6-1.md](verification-phase-6-1.md) 에 적는다.

## 1. 요구

사용자 지시(2026-10-14): ISO 를 공개 배포하려면 NVIDIA CUDA EULA 에 위반되지 않아야 한다. "EULA 에 위반 안 되게끔 진행해".

## 2. 문제

CUDA EULA(1.1.2, 부속서 A)는 `libcudart`, `libcublas`, `libcublasLt` 의 재배포를 응용 프로그램과 함께 허용하지만 **그 응용만 접근**해야 한다고 정한다. 0.6.0 까지 이미지는 이 런타임을 NVIDIA 패키지(`cuda-cudart-13-2`, `libcublas-13-2`)로 `/usr/local/cuda-13.2` 에 설치하고 시스템 링커 경로에 등록했다. OS 의 어떤 프로그램도 쓸 수 있는 상태라 조건을 만족한다고 보기 어려웠다.

## 3. 변경

| 항목 | 0.6.0 | 0.6.1 |
|---|---|---|
| 런타임 위치 | `/usr/local/cuda-13.2/...`(NVIDIA 패키지, 시스템 전체) | `/usr/lib/opereta/llama/`(llama-server 옆) |
| 누가 가지나 | `opereta-nvidia` 가 NVIDIA 패키지를 끌어옴 | `opereta-llm` 이 파일을 직접 가짐 |
| 시스템 링커 | 등록됨 | 등록하지 않음(RUNPATH `$ORIGIN` 로만 찾음) |
| 파일 | NVIDIA 원본 | 같은 원본, 수정 없음(strip, patchelf 없음) |
| 계약서 | 원본 패키지의 copyright | 원본 copyright 를 `opereta-llm` 의 문서에도 둠 + `NOTICE.cuda-runtime` |
| 이미지의 NVIDIA 런타임 패키지 | 설치됨(핀, hold) | 설치하지 않음(핀 -1, 훅과 이미지 검사가 확인) |

NVIDIA 의 `libcublas`, `libcublasLt` 는 RUNPATH `$ORIGIN` 이라 같은 디렉터리의 서로를 찾는다. 파일을 고칠 필요도 `LD_LIBRARY_PATH` 도 필요 없다(원본 패키지에서 확인했다).

## 4. 검증 지점

| 시점 | 확인 |
|---|---|
| 빌드(`llama/build.sh`) | 설치된 NVIDIA 패키지 버전이 `opereta.conf` 의 고정값과 같다. 허용 목록 밖의 CUDA 라이브러리가 필요하면 실패. 복사본과 원본 `cmp`. 복사한 트리에서 `ld.so --inhibit-cache` 로 전용 디렉터리에서만 찾아지는지 (c0) |
| 패키지(`tests/llama/package.sh`) | 번들 파일, 계약서 전문, 매니페스트 해시, 전용 해석 |
| 규칙(`make test-llama-bundle-docker`) | 합성 라이브러리: 허용 목록만 복사, 복사본이 원본과 같음, `dpkg-buildpackage`(strip 포함)를 거쳐도 NVIDIA 라이브러리가 바뀌지 않고 우리 모듈은 strip 됨, 링커 경로에 아무것도 등록하지 않음, 목록 밖 라이브러리와 계약서 누락을 거절 |
| 이미지(`tests/nvidia/image.sh`, 훅) | NVIDIA 런타임 패키지가 없음, 세 라이브러리가 전용 디렉터리에만 있음, 시스템 경로에 없음, 계약서가 있음, 링커 캐시에 없음 |

## 5. 비용과 한계

- `opereta-llm` 의 `.deb` 가 약 360 MiB 커진다(실제 파일로 만든 시험 패키지 375 MB). ISO 는 같은 파일이 옮겨 간 것이라 거의 같다.
- `opereta-*` 패키지가 모두 같은 릴리스 버전을 쓰므로 OS 업데이트마다 이 패키지를 다시 받는다.
- **이 구성이 EULA 에 맞는다는 NVIDIA 의 확인은 없다.** 부속서 A 가 말하는 형태(응용과 함께, 응용만 접근, 수정 없음, 계약서 제공)에 맞췄을 뿐이다.
- 실제 GPU 에서의 추론은 확인하지 못한다. CI 는 라이브러리가 전용 디렉터리에서 적재되는지까지 본다.
- ISO 를 올리려면 GPL 소스 제공과 프로젝트 라이선스가 남아 있다([licenses.md](licenses.md) 10.3). 0.6.1 은 ISO 를 올리지 않는다.
