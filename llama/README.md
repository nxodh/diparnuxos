# llama (llama.cpp 빌드)

- 확인 날짜: 2026-10-10
- 상태: 초안. 빌드 스크립트 구현은 Phase 2 예정이다.

- **책임**: llama.cpp를 CUDA 지원으로 빌드하고 `llama-server`를 이미지에 넣는다. 내장 웹 UI 없이 빌드한다(사용자 확정 U6).
- **소스 버전**: 태그 `v0.6.0`(정식 릴리스, 2026-10-05). 검증 시점의 빌드 번호는 b11429다. `bNNNNN` 빌드 태그는 쓰지 않는다. 커밋 해시 고정은 Phase 2에서 채운다.
- **CUDA 런타임**: CUDA 13.2.2. 드라이버 브랜치 595에 맞춘다. 빌드용 `cuda-minimal-build-13-2`, `libcublas-dev-13-2`, `cuda-driver-dev-13-2`를 쓴다.
- **런타임 라이브러리(추정)**: `libcudart.so.13`, `libcublas.so.13`, `libcublasLt.so.13`, 드라이버의 `libcuda.so.1`. CMake의 링크 대상에서 추론했고 `ldd`로는 아직 확인하지 않았다.

## CMake 옵션

```
cmake -S <llama.cpp 소스 디렉터리> -B <빌드 디렉터리> \
  -DGGML_CUDA=ON \
  -DGGML_NATIVE=OFF \
  -DGGML_BACKEND_DL=ON \
  -DGGML_CPU_ALL_VARIANTS=ON \
  -DCMAKE_CUDA_ARCHITECTURES="86-real;89-real;120a-real" \
  -DLLAMA_BUILD_TESTS=OFF \
  -DLLAMA_BUILD_UI=OFF \
  -DLLAMA_USE_PREBUILT_UI=OFF
```

| 옵션 | 이유 |
|---|---|
| `GGML_CUDA=ON` | CUDA 백엔드를 켠다. 예전 `LLAMA_CUBLAS` 옵션은 쓰지 않는다. |
| `GGML_NATIVE=OFF` | v0.6.0에서 기본값이 ON이다. 그러면 `-march=native`가 붙어 빌드 머신 CPU 전용 바이너리가 된다. `SOURCE_DATE_EPOCH`가 있으면 기본값이 OFF로 바뀌어 SIMD(SSE4.2, AVX, AVX2, FMA, F16C)가 모두 꺼지므로, 어느 쪽이든 명시적으로 끈다. |
| `GGML_BACKEND_DL=ON` | 백엔드를 동적으로 불러올 수 있는 라이브러리로 빌드한다. `GGML_CPU_ALL_VARIANTS`와 함께 쓰는 업스트림 Dockerfile의 조합을 따른다. |
| `GGML_CPU_ALL_VARIANTS=ON` | CPU 변형을 여러 개 빌드한다. 업스트림 배포용 Dockerfile도 같은 조합을 쓴다. |
| `CMAKE_CUDA_ARCHITECTURES="86-real;89-real;120a-real"` | RTX 30(sm_86), RTX 40(sm_89), RTX 50(sm_120). CMake는 `120`을 `120a`로 바꾸고 접미사 없는 값에는 PTX까지 넣으므로 `120a-real`로 적는다. |
| `LLAMA_BUILD_TESTS=OFF` | 테스트 바이너리를 만들지 않는다. |
| `LLAMA_BUILD_UI=OFF` | 내장 웹 UI를 빌드하지 않는다. |
| `LLAMA_USE_PREBUILT_UI=OFF` | 기본값은 ON이며, 빌드 중 Hugging Face에서 `dist.tar.gz`를 내려받는다. 끄지 않으면 네트워크 없이 재현되지 않는다. |

- CUDA 13에서 Maxwell, Pascal, Volta는 지원 목록에서 빠졌다. `nvcc`의 기본 아키텍처는 sm_75다(조사 기록, nvcc 문서).
- 드라이버는 595.91.07이다. CUDA 13.4 기능은 R615 드라이버가 필요하므로 이 빌드에는 쓰지 않는다([docs/decisions.md](../docs/decisions.md) D4-1).

근거:
- https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/ggml/CMakeLists.txt (`GGML_NATIVE_DEFAULT`, `INS_ENB`, `120` → `120a`)
- https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/scripts/ui-assets.cmake (`LLAMA_USE_PREBUILT_UI` 기본 ON, Hugging Face 다운로드)
- https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/CMakeLists.txt (`LLAMA_BUILD_UI`, `LLAMA_USE_PREBUILT_UI` 옵션 정의)
- https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/ggml/src/ggml-cuda/CMakeLists.txt (링크 대상)
- https://github.com/ggml-org/llama.cpp/blob/master/docs/build.md (`GGML_CUDA` 옵션)
- https://api.github.com/repos/ggml-org/llama.cpp/releases/latest (v0.6.0, 2026-10-05)

열린 문제: 커밋 해시 고정, `ldd` 확인, 라이선스 원문 확인([docs/licenses.md](../docs/licenses.md) 4절).
