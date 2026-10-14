# 0.6.1 설계: ISO 공개 배포를 위한 정리(CUDA 런타임 직접 받기, 소스 제공, 펌웨어, 라이선스)

- 확인 날짜: 2026-10-14
- 상태: 구현했고 컨테이너 시험을 통과했다. CI 결과는 [verification-phase-6-1.md](verification-phase-6-1.md) 에 적는다.

## 1. 요구

사용자 지시(2026-10-14): ISO 를 공개 배포할 수 있게 법적 문제가 되는 것을 정리한다. "EULA 에 위반 안 되게끔", GPL 소스 제공은 (a)(소스를 같이 올림), CUDA 런타임과 Intel 파일은 뺄 수 있으면 뺀다, CUDA 런타임은 사용자가 직접 받게 한다.

## 2. 변경

| 쟁점 | 0.6.0 | 0.6.1 |
|---|---|---|
| **CUDA 런타임**(NVIDIA CUDA EULA: 재배포는 응용 전용) | 이미지에 NVIDIA 패키지로 설치 | **이미지에 없음.** 사용자의 컴퓨터가 NVIDIA 저장소에서 직접 받는다 |
| **GPL 소스 제공** | 준비 없음 | 이미지의 모든 Debian 패키지의 대응 소스를 같은 릴리스에 올린다(GPLv2 3a) |
| **Intel `ice_comms`, `ice_wireless_edge`**(재배포가 "Intel 승인 제품과 함께"로 한정) | 이미지에 있음 | 이미지에서 뺀다(dpkg path-exclude 포함). 기본 프로파일은 남긴다 |
| **프로젝트 라이선스** | 없음 | MIT(`LICENSE`) |
| **ISO 게시** | 안 함 | `OPERETA_PUBLISH_ISO="yes"`: ISO + 대응 소스 + 체크섬 + 업데이트 저장소 |

NVIDIA 드라이버(사용자 공간 `libcuda`, GSP 펌웨어)는 이미지에 둔다. 드라이버 라이선스의 재배포 조건(OSI 승인 커널과 함께, 수정 없이, 계약서 제공)을 지키고 있고 Debian 이 같은 드라이버 코드에서 만든 패키지를 배포한다. 직접 받게 하면 커널 모듈을 사용자 PC 에서 빌드하고 Secure Boot 서명을 다시 해야 해서 비용이 크다.

## 3. CUDA 런타임 직접 받기

| 구성 | 내용 |
|---|---|
| 동작 | 업데이트 엔진의 새 동작 `cuda_runtime`: 저장소 색인을 새로 받고 `cuda-cudart-13-2`, `libcublas-13-2` 를 고정 버전으로 설치하고 hold 한다 |
| 고정 버전 | `/usr/share/opereta/nvidia.conf`(`opereta.conf` 에서 생성). 이미지의 NVIDIA 저장소 설정(`cuda-keyring`)으로 받고 apt 가 서명을 확인한다 |
| 호출 | `opereta gpu runtime install`(CLI), Control Center 시스템 페이지, `POST /v1/update {"action":"cuda_runtime"}` |
| 동의 | CLI 는 터미널에서 NVIDIA 약관 주소(`https://docs.nvidia.com/cuda/eula/`)를 보여 주고 `y/N` 을 묻는다. 터미널이 아니면 `--yes` 가 필요하다. Control Center 는 같은 내용의 확인 창을 연다 |
| 상태 | `opereta gpu runtime status`, `GET /v1/update` 의 `cuda_runtime`(`installed`, `reason`, `packages`) |
| 라이브 세션 | 허용한다(재부팅하면 사라진다). 다른 업데이트 동작은 설치된 시스템에서만 한다 |
| 안전 | 다른 갱신이 재부팅 확인을 기다리면 거절(상태를 덮어쓰지 않음), 여유 공간 1.2 GiB 확인, 이미 설치되어 있으면 아무것도 하지 않음 |
| 런타임이 없을 때 | `libggml-cuda.so` 가 적재되지 않아 CPU 로 돈다. GPU 를 쓰려는 서버 시작은 `cuda_backend_unavailable` 로 멈추고 CLI 가 설치 명령을 안내한다 |

## 4. 대응 소스(GPL)

| 단계 | 내용 |
|---|---|
| 목록 | `make iso` 가 이미지에 설치된 패키지 목록(`out/package-manifest.tsv`)을 만든다. 열: 패키지, 버전, 아키텍처, 소스 패키지, 소스 버전, 관리자 |
| 수집 | `make sources`(`scripts/collect-sources.py`, 컨테이너): Debian 소스 패키지를 **정확한 버전**으로 받는다. apt(서명 검증)를 먼저, 저장소에서 사라졌으면 snapshot.debian.org 에서 받아 `.dsc` 의 SHA-256 으로 확인한다. 이 프로젝트의 패키지(관리자 Opereta Developers)와 NVIDIA 저장소의 독점 패키지는 이유와 함께 건너뛴다 |
| 실패 | 하나라도 받지 못하면 실패한다(조용히 건너뛰지 않음). 소스 패키지 하나가 묶음 한도(1800 MiB)보다 크면 알린다 |
| 묶음 | `opereta-<버전>-sources-NN.tar`(각 2 GiB 미만, 소스 패키지는 쪼개지 않음), `opereta-<버전>-sources.tsv`(소스 → 묶음 → 바이너리 패키지), `SOURCES-README.txt` |
| 게시 | `scripts/make-release-assets.sh` 는 ISO 를 올릴 때 소스 묶음이 없으면 **거절**한다. 이 프로젝트의 소스 `opereta-<버전>-source.tar.gz` 와 패키지 목록 `opereta-<버전>-packages.tsv` 도 함께 올린다 |
| 오픈 모듈 | NVIDIA 오픈 커널 모듈(MIT/GPL)의 소스는 DKMS 패키지가 이미지의 `/usr/src` 에 가지고 있다 |

## 5. 검증 지점

| 대상 | 시험 |
|---|---|
| 엔진 | `update` 패키지 시험(설치, 이미 설치됨, 라이브 허용, 재부팅 대기 거절, 네트워크, 저장소에 없음, 여유 공간, 설정 없음) |
| 데몬과 CLI | `updatectl`(동작 이름, 헬퍼 인자 `cuda-runtime`, 라이브 세션), CLI(`status`, `install` 의 확인, `--yes`) |
| Control Center | 논리 시험, 번역 목록, 연기 시험 |
| 소스 수집 | `make test-sources-docker`(실제 Debian 패키지 hello, 건너뛰기, 실패, 한도) |
| 릴리스 자산 | `make test-release-docker`(소스가 없으면 ISO 를 올리지 않음, 소스와 목록이 함께 모임) |
| 이미지 | `tests/nvidia/image.sh`(CUDA 런타임 패키지와 파일이 **없음**, 고정 버전 기록, Intel 특수 프로파일이 없고 기본 프로파일은 있음), 이미지 훅 |
| 펌웨어 훅 | 가짜 트리에서 실행: 두 파일만 지워지고 기본과 `ice_lag` 는 남음 |

## 6. 한계

- 이 문서와 licenses.md 는 법률 자문이 아니다.
- 실제 GPU 에서 받은 런타임으로 추론이 도는지는 확인하지 못한다(CI 에 GPU 가 없다).
- 소스 수집은 Debian 저장소와 snapshot 에 접속해야 한다. 릴리스 빌드(CI)에서 실행한다.
- 소스 묶음 크기는 실제 이미지로 측정하기 전이다(수 GB 로 추정).
- ISO 를 처음 공개할 때까지 `OPERETA_PUBLISH_ISO="yes"` 의 실제 게시는 태그 푸시로만 일어난다(`main` 병합 뒤, 사용자 지시).
