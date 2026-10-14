# 0.6.1 검증 기록: CUDA 런타임의 전용 번들링

- 확인 날짜: 2026-10-14
- 상태: 컨테이너 시험(합성 라이브러리와 실제 NVIDIA 파일)을 통과했다. **CI(CUDA 컴파일, ISO, 이미지 검사, QEMU E2E)는 아직 돌지 않았다.** 실기 GPU 는 미검증이다.

설계는 [phase-6-1-design.md](phase-6-1-design.md) 에 있다.

## 1. 확인한 것

| 대상 | 방법 | 결과 |
|---|---|---|
| NVIDIA 라이브러리의 구성 | NVIDIA 저장소의 `cuda-cudart-13-2` 13.2.86-1, `libcublas-13-2` 13.4.1.3-1 을 받아 `readelf` | `libcublas.so.13`, `libcublasLt.so.13` 의 RUNPATH 가 `$ORIGIN`. `libcudart.so.13` 은 시스템 라이브러리만 필요. 두 패키지의 `copyright` 에 계약서 전문(2026-01-26 판) |
| 번들링 규칙 | `make test-llama-bundle-docker`: 합성 라이브러리 | 통과(6개 항목) |
| 실제 파일로 확인 | amd64 컨테이너에서 실제 `libcudart`, `libcublas`, `libcublasLt` 로 번들링, `verify-cuda-bundle.sh`, `dpkg-buildpackage` | 세 파일이 전용 디렉터리에서만 찾아지고, 패키징 뒤 원본과 바이트 단위로 같다(`cmp`). `.deb` 375 MB |
| 흔적, 비밀, 개인 정보 | `make check-legacy-name check-secrets lint` | 통과 |

## 2. 확인하지 못한 것

| 항목 | 이유 |
|---|---|
| CUDA 컴파일과 실제 `libggml-cuda.so` | CI 에서만 돈다(약 1시간). 이 환경은 Apple Silicon 이라 CUDA 빌더 이미지를 돌릴 수 없다 |
| 이미지 검사(NVIDIA 런타임 패키지 부재, 전용 디렉터리, 링커 캐시) | ISO 가 있어야 한다. CI 의 `test-nvidia-image-docker` |
| 실제 GPU 추론 | GPU 가 없다. 사용자 PC 에서만 확인할 수 있다 |
| NVIDIA 의 확인 | 받지 않았다. 문의 창구는 `nvidia-compute-license-questions@nvidia.com` |
