# Phase 2 사전 조사 기록 (2026-10-11)

- 확인 날짜: 2026-10-11
- 상태: 조사 기록. 탐색 에이전트가 1차 출처와 컨테이너 실행으로 확인한 내용이다. 실 NVIDIA GPU 에서 확인한 것은 없다.
- 이 문서의 "내", "우리" 는 조사자를 가리킨다. 아래 "정리" 절은 조사 당시의 작업 정리 기록이다.

저장소 파일은 수정하지 않았고, 원격에 쓰거나 호스트에 설치한 것도 없다. 확인 수준은 "확인됨"(1차 출처나 직접 실행으로 본 것), "추정"(근거는 있으나 직접 못 본 것), "미확인"으로 구분했다.

중단 전에 끝낸 D(Hugging Face)와 E(GGUF) 항목은 다시 조회하지 않았고, 그때 받은 결과를 아래에 합쳐 두었다. 핵심 질문(CUDA 포함 빌드 하나로 CUDA·GPU가 없는 시스템에서 CPU로 도는가)에 대한 결론과 근거는 "설계에 영향을 주는 발견" 1번에 있다.

## 실제로 돌려 본 것

모두 Docker `debian:trixie`에서 했다. llama.cpp는 `--platform linux/amd64`(에뮬레이션)로, go-nvml은 arm64 네이티브 컨테이너로 돌렸다.

- **llama.cpp v0.6.0 CPU 빌드 (두 번)**
  - 정해진 옵션(CUDA 제외)과 `-DLLAMA_BUILD_APP=OFF -DLLAMA_BUILD_EXAMPLES=OFF`를 주고 `cmake --build build --target llama-server`로 빌드했다.
  - 1차는 13분 3초, 2차는 `-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH='$ORIGIN'`을 더해 19분 3초가 걸렸다. 둘 다 에뮬레이션된 4코어이고, 1차는 user 42분 13초다.
  - 사용한 도구는 cmake 3.31.6, g++ 14.2.0, OpenSSL 3.5.7이다.
  - 의존성 목록·빌드 출력·`readelf`는 이 빌드에서 얻은 것이다.
- **미니 모델 실행 시험**
  - `ggml-org/tiny-llamas`의 `stories260K.gguf`(1,185,376바이트)를 받았다. sha256 `047bf464…c04b`가 HF가 알려 준 `lfs.oid`와 일치했다. D의 tree·resolve 응답이 맞다는 확인이다.
  - 이 모델로 위 빌드 결과물을 띄워 엔드포인트·플래그·router 모드를 확인했다.
- **go-nvml 프로브**: Go 1.24.4와 gcc 14.2.0로 `go build`와 실행을 해 보았다.
- **GGUF 헤더 파서**: 로컬 python3 `-I` 스크립트로 앞 16 MiB를 Range로 받아 파싱했다.
- **CUDA 없는 환경 모사**: 가짜 `libggml-cuda.so`를 만들어 로더 동작을 보았다. 아래 "설계에 영향을 주는 발견" 1번에 자세히 적었다.
- 진짜 `libggml-cuda.so`는 nvcc·CUDA 툴킷 없이는 빌드할 수 없어 만들지 못했다.

## A. llama.cpp v0.6.0

### A1. 태그와 tarball (확인됨)

- 태그 `v0.6.0`은 annotated 태그다.
  - 태그 객체: `69ecae922d2b60c1fa8c590279f4e2099af75111`
  - 역참조한 커밋: `d81235049384534c167caea52b85a694f6103d14`
  - 태그 메시지는 `Release v0.6.0`, tagger는 github-actions[bot], 날짜는 2026-10-05T16:56:17Z다.
  - 근거: `gh api repos/ggml-org/llama.cpp/git/ref/tags/v0.6.0`, 이어서 `git/tags/69ecae92…`.
- 릴리스 게시는 2026-10-05T16:56:22Z이고, 릴리스 본문의 "Nightly build"는 `b11429`다.
- tarball URL은 `https://github.com/ggml-org/llama.cpp/archive/refs/tags/v0.6.0.tar.gz`다.
  - 302로 `https://codeload.github.com/ggml-org/llama.cpp/tar.gz/refs/tags/v0.6.0`에 간다.
  - 파일명은 `llama.cpp-0.6.0.tar.gz`, 최상위 디렉터리는 `llama.cpp-0.6.0/`이다.
- 받은 파일은 37,874,349바이트, sha256 `09b36dba235fcac180efff18514da7038e65d8e5b9e4aefa59238468abfdec12`였다.
  - 이 해시는 한 번 잰 값이다. GitHub의 자동 생성 아카이브는 해시가 고정된다고 보장되지 않으니, 고정이 필요하면 커밋 SHA로 `git clone`하는 편이 안전하다 (추정).
- 루트 `CMakeLists.txt:14`의 `LLAMA_BUILD_IS_DEV`가 기본 ON이라 `--version`은 `version: 0.6.0-dev (build 0, commit unknown)`으로 나온다. 실제 실행 출력이다.

### A2. CMake 옵션 (확인됨: 소스 파일과 실제 configure·build)

**정해진 옵션은 모두 이름이 맞다.**

| 옵션 | 정의 위치 | 기본값 |
|---|---|---|
| `GGML_CUDA` | `ggml/CMakeLists.txt:199` | OFF |
| `GGML_NATIVE` | `ggml/CMakeLists.txt:123` | ON (cross-compile이거나 `SOURCE_DATE_EPOCH`가 있으면 OFF) |
| `GGML_BACKEND_DL` | `ggml/CMakeLists.txt:86` | OFF (`BUILD_SHARED_LIBS` 필요) |
| `GGML_CPU_ALL_VARIANTS` | `ggml/CMakeLists.txt:183` | OFF (`GGML_BACKEND_DL` 필요) |
| `LLAMA_BUILD_TESTS` | `CMakeLists.txt:132` | `${LLAMA_STANDALONE}` = ON |
| `LLAMA_BUILD_UI` | `CMakeLists.txt:137` | OFF |
| `LLAMA_USE_PREBUILT_UI` | `CMakeLists.txt:138` | ON |
| `CMAKE_CUDA_ARCHITECTURES` | `ggml/src/ggml-cuda/CMakeLists.txt:21` | 미지정 시 아래 목록 |

- `GGML_NATIVE=ON`이면 `GGML_BACKEND_DL`과 함께 쓸 때 오류가 난다 (`ggml-cpu/CMakeLists.txt:415`: "GGML_NATIVE is not compatible with GGML_BACKEND_DL").
- `CMAKE_CUDA_ARCHITECTURES`를 지정하지 않으면 CUDA 13 기준으로 `75-virtual;80-virtual;86-real;89-real;90-virtual;120a-real;121a-real`이 된다 (`ggml-cuda/CMakeLists.txt:43-68`).
  - 정해진 `86-real;89-real;120a-real`은 이 목록의 부분집합이다.
  - trixie의 cmake 3.31.6은 검증 정규식 `^([0-9]+a?(-real|-virtual)?(;…)*|all|all-major|native)$`로 `120a-real`을 받아들인다 (`CMakeCUDAArchitecturesValidate.cmake:8`).
  - `-real`만 있으므로 PTX가 없다. sm_86·89·120a 외의 GPU(예: sm_75, sm_80, sm_90)에서는 이 CUDA 백엔드가 쓸 커널이 없다. 이는 소스와 옵션만 보고 한 판단이고 실기에서는 확인하지 못했다 (추정).

**알아 둘 옵션 (모두 확인됨)**

- `LLAMA_CURL`은 더는 옵션이 아니다. `CMakeLists.txt:195`에 `llama_option_depr(WARNING LLAMA_CURL)`만 있고, 설정하면 경고만 나온다. libcurl은 필요 없다.
- `LLAMA_OPENSSL`(`CMakeLists.txt:144`)은 기본 ON이다.
  - `vendor/cpp-httplib/CMakeLists.txt:128-155`: OpenSSL을 찾고 3.0 이상이면 HTTPS 지원을 켠다.
  - 못 찾으면 `WARNING "OpenSSL not found, HTTPS support disabled"`만 내고 계속 진행한다.
  - 따라서 `libssl-dev`가 없는 빌드 환경이라면 에러 없이 HTTPS 없는 서버가 만들어진다 (추정: 모델을 `-hf`로 받는 기능이 막힌다는 부분은 직접 확인하지 못했다).
- `LLAMA_BUILD_APP`(`CMakeLists.txt:136`)이 새로 있고 기본 ON이다. `app/CMakeLists.txt`가 `llama`라는 통합 바이너리를 만든다. server만 필요하면 OFF로 한다.
- `GGML_OPENMP` 기본 ON이다. CPU 백엔드가 `libgomp.so.1`에 의존한다.
- `GGML_CUDA_NCCL` 기본 ON이지만 선택 사항이다. NCCL이 없으면 "NCCL not found, performance for multiple CUDA GPUs will be suboptimal" 상태 메시지만 낸다 (`ggml-cuda/CMakeLists.txt:184-192`).
- `GGML_CUDA_COMPRESSION_MODE` 기본은 `size`이고 CUDA 12.8 이상에서 적용된다.
- `GGML_CUDA_CCCL_VERSION`은 기본이 빈 값이라 CUDA 툴킷에 포함된 CCCL을 쓴다.
- `GGML_CUDA_FA_QUANTS` 기본값은 `q4_0-q4_0;q8_0-q8_0;f16-f16;bf16-bf16`이다 (`ggml/CMakeLists.txt:208`). 설계에 영향을 주는 발견 7번에 따로 적었다.
- `LLAMA_BUILD_UI=OFF`와 `LLAMA_USE_PREBUILT_UI=OFF`를 함께 줘도 빌드는 성공한다.
  - 실제 빌드에서 `CMake Warning at /src/scripts/ui-assets.cmake:584: UI: no assets available - building without an embedded UI.` 와 `-- UI: embedded 0 assets` 가 나왔다.

**llama-server만 빌드하려면**

- `tools/CMakeLists.txt:24`에서 `LLAMA_BUILD_SERVER`가 켜져 있어야 `ui`·`cli`·`server` 하위 디렉터리가 추가된다. `LLAMA_BUILD_TOOLS`도 필요하다(둘 다 기본 ON).
- 권장 조합은 `-DLLAMA_BUILD_APP=OFF -DLLAMA_BUILD_EXAMPLES=OFF`에 `cmake --build build --target llama-server`다. 실제로 이렇게 빌드해서 성공했다.
- `--target llama-server`로만 빌드한 뒤 `cmake --install`을 하면 실패한다. 실제 출력은 다음과 같다.

```
CMake Error at build/tools/batched-bench/cmake_install.cmake:52 (file):
  file INSTALL cannot find "/src/build/bin/libllama-batched-bench-impl.so": No such file or directory.
```

  - `cmake --install`은 구성된 모든 타깃(batched-bench, quantize, perplexity 등)을 설치하려 하기 때문이다.
  - 해결은 전체를 빌드하거나, `build/bin`을 그대로 복사하는 것이다 (업스트림 Dockerfile도 `cp build/bin/*`를 쓴다).

**BUILD_SHARED_LIBS와 의존 라이브러리**

- `BUILD_SHARED_LIBS` 기본은 ON이다 (MINGW와 Emscripten 제외, `CMakeLists.txt:72-79`).
- 빌드된 `build/bin`(CPU 전용, 총 37,944 KB)의 파일은 다음과 같다. 크기는 바이트다.
  - `llama-server` 17,912바이트: 얇은 실행 파일로, `libllama-server-impl.so`에 링크된다.
  - `libllama-server-impl.so` 4,106,168: 버전 접미사 없음.
  - `libllama-common.so.0.6.0` 6,089,216, `libllama.so.0.6.0` 4,789,512, `libmtmd.so.0.6.0` 1,914,456 (SONAME은 각각 `.so.0`).
  - `libggml.so.0.26.0` 55,464, `libggml-base.so.0.26.0` 910,488.
  - `libggml-cpu-<변형>.so` 14개: 985,064~1,960,368바이트, 접미사 없음, SONAME 없음.
- 실행 파일과 라이브러리는 외부 의존이 다음뿐이다 (`readelf -d`, `ldd`): `libssl.so.3`, `libcrypto.so.3`, `libgomp.so.1`(CPU 변형), `libstdc++.so.6`, `libm`, `libgcc_s`, `libc`. `libz.so.1`과 `libzstd.so.1`은 libcrypto를 통해 들어온다.
  - 즉 런타임에는 `libssl3t64`, `libgomp1`이 필요하다 (trixie의 libssl3t64는 3.5.7-1~deb13u2).
- RPATH는 기본값 그대로면 빌드 디렉터리 절대경로다.
  - 1차 빌드의 모든 파일에서 `RUNPATH: [/src/build/bin:]`이었다.
  - 소스 전체에 `RPATH`를 건드리는 CMake 설정이 없음을 tarball에서 grep으로 확인했다.
  - 2차 빌드에서 `-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH='$ORIGIN'`을 주었더니 모든 `.so`와 `llama-server`가 `RUNPATH: [$ORIGIN]`이 되었다.
  - 이 상태에서 `build/bin`을 `/opt/flat`으로 복사하고 원본 소스 디렉터리를 옮겨 없앤 뒤 `/opt/flat/llama-server --version`이 정상 실행되었다 (환경변수 없음).

**GGML_BACKEND_DL=ON일 때의 백엔드 파일과 변형 (확인됨)**

- CPU 변형 14개: `x64`, `sse42`, `sandybridge`, `ivybridge`, `piledriver`, `haswell`, `skylakex`, `cannonlake`, `cascadelake`, `icelake`, `cooperlake`, `zen4`, `alderlake`, `sapphirerapids`. (`ggml/src/CMakeLists.txt:494-517`, 실제 빌드 산출물과 일치.)
- 파일명은 `libggml-cpu-<변형>.so`이고 접미사 없는 순수 `.so`다. CUDA 백엔드 이름도 같은 함수(`ggml_add_backend_library`)로 만들어지므로 `libggml-cuda.so`일 것이다 (추정, 진짜 빌드는 못 해 봄).
- `cmake --install`의 설치 위치는 `ggml_add_backend_library` 안에서 정해진다 (`ggml/src/CMakeLists.txt:388-391`).
  - `GGML_BACKEND_DIR`이 있으면 그 경로, 없으면 `${CMAKE_INSTALL_BINDIR}`(= `bin/`)다.
  - 그 외 라이브러리는 `lib`(`LIBDIR`)로 간다.
  - 위 실패한 install의 부분 출력에도 `/opt/ll/bin/libggml-cpu-*.so`와 `/opt/ll/lib/libggml-base.so.0.26.0`이 보인다.

**실행 시 백엔드 탐색 (`ggml/src/ggml-backend-reg.cpp:480-505`, 확인됨)**

탐색 경로는 순서대로 다음과 같다.

```
#ifdef GGML_BACKEND_DIR   search_paths.push_back(GGML_BACKEND_DIR)  // 컴파일 타임 정의
// default search paths: executable directory, current directory
search_paths.push_back(get_executable_path());   // /proc/self/exe 의 디렉터리
search_paths.push_back(cwd);
```

- 파일 매칭 규칙: `filename.find("libggml-<name>-") == 0 && ext == ".so"`.
  - 확장자가 정확히 `.so`여야 하므로 `libggml-cpu-haswell.so.0` 같은 이름은 매칭되지 않는다.
  - 변형마다 `dlopen` 후 `ggml_backend_score()`를 호출해 점수가 가장 높은 것을 고른다.
  - 점수가 0이거나 매칭이 없으면 접미사 없는 `libggml-<name>.so`를 시도한다.
- 환경 변수는 `GGML_BACKEND_PATH` 하나뿐이고, 범위 밖 백엔드 1개를 직접 로드하는 용도다 (`ggml-backend-reg.cpp:600-603`). 검색 디렉터리를 바꾸는 환경 변수는 없다.
- 로드 순서는 blas, zendnn, cann, cuda, hip, metal, rpc, sycl, vulkan, virtgpu, opencl, hexagon, musa, openvino, cpu다 (`ggml-backend-reg.cpp:585-599`).
- Release 빌드는 `NDEBUG`가 켜져 `silent=true`로 호출된다.
- `libllama`가 `llama_supports_gpu_offload()`·`llama_backend_init`에서 필요할 때 `ggml_backend_load_all()`을 부른다 (`src/llama.cpp:102-135`).
- 알려진 이슈 #28328(열림): CPU 변형 점수가 OS의 XSAVE 지원을 확인하지 않아, CPUID는 AVX를 알리지만 OS가 켜지 않은 환경(예: `clearcpuid=xsave` 게스트)에서 SIGILL이 날 수 있다. 이슈 본문 기준이며 직접 재현하지 않았다.

**핵심 질문의 소스 쪽 근거**는 아래 "설계에 영향을 주는 발견" 1번에 있다.

### A3. `.devops/cuda.Dockerfile` (v0.6.0, 133줄, 확인됨)

전문은 `https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/.devops/cuda.Dockerfile`이다. 구조와 핵심 줄만 옮긴다.

- 기본 인자: `UBUNTU_VERSION=24.04`, `CUDA_VERSION=12.8.1`, `GCC_VERSION=14`.
  - 빌드 베이스는 `docker.io/nvidia/cuda:${CUDA_VERSION}-devel-ubuntu${UBUNTU_VERSION}`.
  - 런타임 베이스는 `docker.io/nvidia/cuda:${CUDA_VERSION}-runtime-ubuntu${UBUNTU_VERSION}`.
  - `NODE_VERSION=24`.
- 웹 UI 단계: `FROM node:24 AS web`에서 `npm ci && npm run build`를 하고 `tools/ui/dist`를 빌드 단계로 복사한다.
- 빌드 단계:
  - 패키지는 `apt-get install -y gcc-14 g++-14 build-essential cmake python3 python3-pip git libssl-dev libgomp1`.
  - `ENV CC=gcc-14 CXX=g++-14 CUDAHOSTCXX=g++-14`.
- cmake 명령 (48-49행, 그대로):
```
cmake -B build -DGGML_NATIVE=OFF -DGGML_CUDA=ON -DGGML_BACKEND_DL=ON -DGGML_CPU_ALL_VARIANTS=ON -DLLAMA_BUILD_TESTS=OFF ${CMAKE_ARGS} -DCMAKE_EXE_LINKER_FLAGS=-Wl,--allow-shlib-undefined . && \
cmake --build build --config Release -j$(nproc)
```
  - `CUDA_DOCKER_ARCH`가 default가 아니면 `-DCMAKE_CUDA_ARCHITECTURES=…`를 추가한다.
  - 산출물은 `find build -name "*.so*" -exec cp -P {} /app/lib`와 `cp build/bin/* /app/full`로 모은다.
- 런타임 베이스: `apt-get install -y libgomp1 curl ffmpeg`.
- server 단계:
  - `ENV LLAMA_ARG_HOST=0.0.0.0`
  - `COPY --from=build /app/full/llama /app/full/llama-server /app`
  - `HEALTHCHECK CMD [ "curl", "-f", "http://localhost:8080/health" ]`
  - `ENTRYPOINT [ "/app/llama-server" ]`
- 업스트림도 `GGML_BACKEND_DL=ON`과 `GGML_CPU_ALL_VARIANTS=ON`을 쓰고 CUDA 백엔드를 한 이미지에 넣는다. 이미지 하나로 CPU 변형 선택과 CUDA를 같이 지원하는 구조다.
- 업스트림 런타임 이미지는 라이브러리를 `/app` 한 디렉터리에 평평하게 둔다. 그 상태에서 공유 라이브러리를 찾는 방법(RUNPATH가 빌드 경로라면 무엇으로 찾는지)은 Dockerfile만으로는 알 수 없다 (미확인).

### A4. llama-server 옵션과 엔드포인트 (`tools/server/README.md`, `common/arg.cpp`, 확인됨)

| 항목 | 정확한 이름 / 환경변수 / 기본값 |
|---|---|
| 호스트 | `--host HOST` / `LLAMA_ARG_HOST` / `127.0.0.1` (쉼표로 여러 주소, `.sock`로 끝나는 UNIX 소켓 가능) |
| 포트 | `--port PORT` / `LLAMA_ARG_PORT` / `8080` |
| API 키 | `--api-key KEY` / `LLAMA_API_KEY` (쉼표로 여러 개), `--api-key-file FNAME` / `LLAMA_ARG_API_KEY_FILE` (한 줄에 하나, `#`로 시작하는 줄은 주석, `arg.cpp:3513`) |
| GPU 레이어 | `-ngl, --gpu-layers, --n-gpu-layers N` / `LLAMA_ARG_N_GPU_LAYERS` / 값은 정수, `auto`, `all`; 기본 `auto`(-1), `all`은 -2 (`arg.cpp:2792-2806`) |
| 컨텍스트 | `-c, --ctx-size N` / `LLAMA_ARG_CTX_SIZE` / `0`(모델에서 읽음) |
| 배치 | `-b, --batch-size` / `LLAMA_ARG_BATCH` / 2048, `-ub, --ubatch-size` / `LLAMA_ARG_UBATCH` / 512 |
| KV 캐시 양자화 | `-ctk, --cache-type-k` / `LLAMA_ARG_CACHE_TYPE_K`, `-ctv, --cache-type-v` / `LLAMA_ARG_CACHE_TYPE_V`; 허용값 f32, f16, bf16, q8_0, q4_0, q4_1, iq4_nl, q5_0, q5_1; 기본 f16 |
| 다중 GPU | `-ts, --tensor-split N0,N1,…` / `LLAMA_ARG_TENSOR_SPLIT`; `-mg, --main-gpu INDEX` / `LLAMA_ARG_MAIN_GPU` / 0; `-sm, --split-mode {none,layer,row,tensor}` / `LLAMA_ARG_SPLIT_MODE` / layer (tensor는 실험적) |
| 장치 | `-dev, --device <dev1,dev2,..>` / `LLAMA_ARG_DEVICE` (`none`이면 오프로드 안 함); `--list-devices` (출력하고 종료, 환경변수 없음) |
| 로드 방식 | `-lm, --load-mode MODE` / `LLAMA_ARG_LOAD_MODE` / `auto`(기본), `none`, `mmap`, `mlock`, `mmap+mlock`, `dio` |
| 별칭 | `-a, --alias STRING` / `LLAMA_ARG_ALIAS` (쉼표로 여러 개) |
| 메트릭 | `--metrics` / `LLAMA_ARG_ENDPOINT_METRICS` / 비활성 |
| 로그 | `--log-file FNAME` / `LLAMA_ARG_LOG_FILE`; `--log-jsonl, --no-log-jsonl` / `LLAMA_ARG_LOG_JSONL` (stdout에 JSONL, 색 끔); `--log-colors [on|off|auto]`; `--log-timestamps`; `-lv, --verbosity N` / `LLAMA_ARG_LOG_VERBOSITY` / 3; `--log-disable` |
| 메모리 맞춤 | `-fit, --fit [on|off]` / `LLAMA_ARG_FIT` / on; `-fitt, --fit-target MiB,…` / 1024; `-fitc, --fit-ctx` / 4096 |
| 슬롯 | `-np, --parallel N` / `LLAMA_ARG_N_PARALLEL` / -1(자동). 자동이면 서버가 `n_parallel=4`, `kv_unified=true`로 정한다 (`tools/server/server.cpp:166-171`) |

- **`--mlock`과 `--no-mmap`은 v0.6.0에 없다.** `-lm/--load-mode`로 대체되었다.
  - 실제 실행: `error: invalid argument: --mlock`, `error: invalid argument: --no-mmap`.
  - Diparnux 설정에 이 두 플래그가 있으면 `load_mode`로 바꿔야 한다.
- `-ngl all`과 `-ngl auto`는 문제없이 받아들여진다. GPU 백엔드가 없으면 세 줄 경고만 나오고 계속 실행된다 (A5).
- **router 모드**
  - `-m`, `-hf`, `-dr` 중 아무것도 주지 않으면 router가 된다 (`server.cpp:151-153`).
  - 옵션은 `--models-dir PATH`(`LLAMA_ARG_MODELS_DIR`), `--models-preset PATH`(INI, `LLAMA_ARG_MODELS_PRESET`), `--models-max N`(기본 4, 0이면 무제한, `LLAMA_ARG_MODELS_MAX`), `--models-autoload`(기본 켬)다.
  - 모델 출처는 캐시(`LLAMA_CACHE`), `--models-dir`, preset이다.
  - 실제 실행: `--models-dir /models`로 띄우니 `/health`가 `{"status":"ok"} HTTP 200`, `GET /props`가 `{"role":"router","max_instances":4,"models_autoload":true,…}`, `GET /models`가 `status.value":"unloaded"`인 항목 1개를 돌려주었다. 로그에는 `security: router mode enabled - do not expose to untrusted environments`가 찍혔다.
  - 관리용 엔드포인트: `POST /models/load`, `POST /models/unload`, `GET /models/sse`, `POST /models`(다운로드), `DELETE /models` (`server.cpp:256-260`).
- **엔드포인트와 인증** (`server.cpp:263-322`, `server-http.cpp:251-269`)
  - `/health`와 `/v1/health`는 공개다. 공개 목록에는 이 둘과 내장 UI 자산뿐이다.
  - `/v1/models`, `/models`, `/props`, `/metrics`, `/v1/chat/completions` 등은 키가 필요하다. `Authorization: Bearer <키>`나 `X-Api-Key: <키>`를 받는다.
  - 실제 실행: `--api-key secret`일 때 `/health`와 `/v1/health`는 키 없이 200, `/v1/models`와 `/metrics`는 키 없이 `{"error":{"message":"Invalid API Key","type":"authentication_error","code":401}} HTTP 401`, 키를 주면 200. `/metrics`는 `--metrics`가 있어야 열리며 `x-api-key` 헤더로도 통과했다.
  - `/props` 응답의 키: `default_generation_settings`, `total_slots`, `model_alias`, `model_ftype`, `model_path`, `modalities`, `endpoint_slots`, `endpoint_props`, `endpoint_metrics`, `ui`, `chat_template`, `build_info`, `is_sleeping` 등 (`server-context.cpp:4955-4990`). **GPU나 오프로드 정보는 없다.**
  - `/v1/models`의 `meta`: `vocab_type`, `n_vocab`, `n_ctx`, `n_ctx_train`, `n_embd`, `n_params`, `size`, `ftype`.
  - `/metrics`는 `llamacpp:prompt_tokens_total`, `llamacpp:tokens_predicted_total`, `llamacpp:requests_processing` 등 토큰·요청 카운터다 (README 표 + 실제 출력). 메모리나 GPU 지표는 없다.
  - 모델 로딩 중에는 다른 경로가 503을 돌려준다 (`/health`는 `{"error":{"code":503,"message":"Loading model",…}}`, README).
  - `POST /v1/chat/completions` 실제 호출이 응답 JSON을 돌려주었다.
- 이 빌드의 로그 줄 형식은 `0.00.353.884 I srv  llama_server: listening on http://127.0.0.1:8089`처럼 `경과시간 레벨 모듈 메시지`다. 레벨 문자는 I, W 등이다.

### A5. 오프로드 로그와 경고

- 오프로드 결과 로그 (확인됨, `src/llama-model.cpp:1913-1927`, 함수 `llama_model_base::load_tensors`):

```
LLAMA_LOG_INFO("%s: offloading output layer to GPU\n", __func__);
LLAMA_LOG_INFO("%s: offloading %d repeating layers to GPU\n", __func__, n_repeating);
LLAMA_LOG_INFO("%s: offloaded %d/%d layers to GPU\n", __func__, std::min(n_gpu_layers, max_offloadable_layers), max_backend_supported_layers);
```

  - 이 블록은 `llama_supports_gpu_offload()`가 참일 때만 실행된다.
  - 분모는 `n_layer_all + 1`(출력 레이어 포함)이다.
  - 같은 함수가 장치별 버퍼 크기도 `%s: %12s model buffer size = %8.2f MiB`로 찍는다 (1930-1935행).
- CUDA 초기화 로그 (`ggml-cuda.cu:267, 331-345`): `ggml_cuda_init: found N CUDA devices (Total VRAM: … MiB):` 다음에 `  Device 0: <이름>, compute capability X.Y, VMM: yes, VRAM: … MiB`.
- GPU를 요청했는데 쓸 GPU 백엔드가 없을 때의 경고 (`common/arg.cpp:2799-2803`):
```
warning: no usable GPU found, --gpu-layers option will be ignored
warning: one possible reason is that llama.cpp was compiled without GPU support
warning: consult docs/build.md for compilation instructions
```
  - CPU만 있는 빌드에서 `-ngl 99`, `-ngl all`, `-ngl auto`로 실행했을 때 stderr 첫 세 줄에 실제로 나왔다. 서버는 종료되지 않고 정상 실행된다.
  - `--split-mode`와 `--main-gpu`에도 비슷한 경고가 있다 (`arg.cpp:2823, 2865`).
- `/props`와 `/metrics`에는 오프로드된 레이어 수나 사용 장치 정보가 **없다** (소스와 실제 응답 모두 확인).
- 이 실행에서 서버가 낸 로그에는 `load_tensors:`·`model buffer size` 줄이 하나도 보이지 않았다.
  - 서버 표준 출력 처음 25줄은 `srv`, `cmn`, `slot`, `load:` 계열뿐이었다.
  - GPU가 없으니 `offloaded N/M` 줄이 안 나오는 것은 소스와 맞다. 그러나 `model buffer size` 줄(GPU 유무와 무관한 INFO)이 왜 안 보이는지는 확인하지 못했다 (미확인).
  - GPU가 있는 실기에서는 로그 문구에 의존하지 말고 `--list-devices` 출력과 NVML로 확인하는 편이 안전하다.
- `--list-devices` 출력 형식 (`common/arg.cpp:1151-1169`, 소스): `Available devices:` 다음에 `  CUDA0: <설명> (<total> MiB, <free> MiB free)`. 장치가 없으면 `  (none)`. 실제 CPU 전용 실행에서 `Available devices:\n  (none)`이 나왔다.

### A6. 릴리스 노트와 이슈의 알려진 문제

- v0.6.0 릴리스 노트(`https://github.com/ggml-org/llama.cpp/releases/tag/v0.6.0`)에는 CUDA 13.x, Blackwell(sm_120), `GGML_BACKEND_DL`에 대한 "알려진 문제" 항목이 없다 (본문 전체를 grep한 결과). CUDA 관련 항목은 커널 최적화·수정이다 (예: "CUDA: Make CCCL configurable + pin it to 3.4.3 for CI jobs (#29792)").
- 이슈 검색(`gh search issues`)으로 찾은 것이다. 모두 제3자 사용자가 쓴 글이며 v0.6.0 태그에서 재현해 본 것이 아니다.

| 이슈 | 상태 | 내용 |
|---|---|---|
| #28581 | 열림 | RTX 5060 Ti(sm_120), CUDA 13.2 `nvcc V13.2.78`에서 IQ3_S 양자화가 쓰레기 출력. `test-backend-ops` MUL_MAT 오류. 우리 패키지의 nvcc는 13.2.86이므로 같은 증상인지 모른다 |
| #21255 | 닫힘(stale) | "CUDA 13.2 breaks some quants (IQ3_S)". NVIDIA 쪽 댓글이 "컴파일러 변경 때문"이라고만 답했고 해결 여부는 불분명 |
| #26901 | 닫힘(stale, 해결 아님) | Blackwell dGPU(RTX 5080/5070 Ti)가 iGPU로 분류되어 다중 GPU 분할이 깨짐. v0.6.0 소스 `ggml-cuda.cu:5205`는 여전히 `prop.integrated ? IGPU : GPU`다 |
| #25060 | 열림 | RTX 5090, CUDA 13.3, 드라이버 580.17에서 SOFT_MAX 실패. `sharedMemPerBlockOptin` 값이 이상하게 읽힌다는 보고 |
| #27330 | 열림 | RTX 5090 Laptop(sm_120)에서 CUDA graph로 GPU 채널 hang. 보고자의 우회는 `GGML_CUDA_DISABLE_GRAPHS=1`이다. v0.6.0의 `ggml-cuda.cu` getenv 목록에서 이 변수를 찾지 못했다 (다른 파일에 있는지는 미확인) |
| #28328 | 열림 | 위 A2에 적은 XSAVE 점수 문제 |
| #30001 | 열림 | 기본 빌드는 `q8_0-q4_0` FlashAttention 커널이 없어 f16 변환으로 떨어져 느리다는 기능 요청 |

## B. CUDA 13.2 빌드 환경

데이터 출처: `https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz` (2246개 패키지). `Size`는 바이트, `Installed-Size`는 KiB다.

### B1. 패키지, 버전, 크기, 의존

| 패키지 | 13.2.2에 해당하는 버전 | Size | Installed-Size | Depends |
|---|---|---|---|---|
| `cuda-minimal-build-13-2` | `13.2.2-1` | 2,556 | 7 | cuda-compiler-13-2 (>= 13.2.2), cuda-cudart-dev-13-2 (>= 13.2.86), cuda-culibos-dev-13-2 (>= 13.2.86), cuda-cccl-13-2 (>= 13.2.86), cuda-profiler-api-13-2 (>= 13.2.86) |
| `cuda-nvcc-13-2` | `13.2.86-1` | 31,097,174 | 137,298 | cuda-cudart-dev-13-2, build-essential, libnvvm-13-2, cuda-crt-13-2, libnvptxcompiler-13-2 |
| `cuda-cudart-dev-13-2` | `13.2.86-1` | 1,449,350 | 6,738 | cuda-cudart-13-2 (>= 13.2.86), cuda-cccl-13-2, cuda-culibos-dev-13-2, cuda-driver-dev-13-2 (>= 13.2.86) |
| `libcublas-dev-13-2` | `13.4.1.3-1` | 432,889,808 | 893,755 | cuda-culibos-dev-13-2, libcublas-13-2 (>= 13.4.1.3) |
| `cuda-driver-dev-13-2` | `13.2.86-1` | 30,882 | 157 | (없음) |
| `cuda-crt-13-2` | `13.2.86-1` | 83,128 | 967 | cuda-cudart-dev-13-2, build-essential |
| `libnvvm-13-2` | `13.2.86-1` | 45,459,030 | 143,449 | (미조회) |
| `cuda-toolkit-13-2-config-common` | `13.2.86-1` | 17,422 | 76 | (없음) |

- 버전 선별 근거: `cuda-toolkit-13-2 13.2.2-1`의 Depends가 `cuda-compiler-13-2 (>= 13.2.2)`, `cuda-cudart-dev-13-2`에 대응하는 `cuda-documentation-13-2 (>= 13.2.86)`, `cuda-nvml-dev-13-2 (>= 13.2.86)`이고, `cuda-libraries-dev-13-2 13.2.2-1`이 `cuda-cudart-dev-13-2 (>= 13.2.86)`와 `libcublas-dev-13-2 (>= 13.4.1.3)`를 요구한다.
  - 같은 이름에 `cuda-nvcc-13-2`는 13.2.51(13.2.0), 13.2.78(13.2.1), 13.2.86(13.2.2) 세 버전이 있다.
  - `libcublas-dev-13-2`와 `libcublas-13-2`는 13.3.0.5, 13.4.0.1, 13.4.1.3 세 버전이 있다. 가장 높은 13.4.1.3이 13.2.2가 요구하는 값이고, 정해진 `libcublas-13-2 13.4.1.3-1`과 일치한다.
- `cuda-nvvm-13-2`라는 패키지는 이 저장소에 없다. nvvm은 `libnvvm-13-2`다.
- `libcublas-dev-13-2`는 `cuda-minimal-build-13-2`가 끌어오지 않는다. ggml-cuda CMake가 `CUDA::cublas`를 링크하므로(`ggml-cuda/CMakeLists.txt:175`) 따로 설치해야 한다.
- 이 패키지 안의 `libcublas.so` 등 파일 내용은 직접 목록을 보지 않았다 (미확인).

**`cuda-minimal-build-13-2 (13.2.2-1)` 의존 폐쇄** (저장소 안 패키지만, "버전 제약을 만족하는 가장 높은 버전" 규칙으로 계산한 19개)

| 패키지 | 버전 | Size | Installed-Size(KiB) |
|---|---|---|---|
| cuda-cccl-13-2 | 13.2.86-1 | 1,261,306 | 16,283 |
| cuda-compiler-13-2 | 13.2.2-1 | 2,548 | 7 |
| cuda-crt-13-2 | 13.2.86-1 | 83,128 | 967 |
| cuda-cudart-13-2 | 13.2.86-1 | 179,488 | 837 |
| cuda-cudart-dev-13-2 | 13.2.86-1 | 1,449,350 | 6,738 |
| cuda-culibos-dev-13-2 | 13.2.86-1 | 23,926 | 110 |
| cuda-cuobjdump-13-2 | 13.2.86-1 | 278,452 | 837 |
| cuda-cuxxfilt-13-2 | 13.2.86-1 | 59,710 | 288 |
| cuda-driver-dev-13-2 | 13.2.86-1 | 30,882 | 157 |
| cuda-minimal-build-13-2 | 13.2.2-1 | 2,556 | 7 |
| cuda-nvcc-13-2 | 13.2.86-1 | 31,097,174 | 137,298 |
| cuda-nvprune-13-2 | 13.2.86-1 | 526,204 | 1,689 |
| cuda-profiler-api-13-2 | 13.2.86-1 | 19,578 | 92 |
| cuda-tileiras-13-2 | 13.2.86-1 | 26,088,658 | 92,550 |
| cuda-toolkit-13-2-config-common | 13.2.86-1 | 17,422 | 76 |
| cuda-toolkit-13-config-common | 13.4.92-1 | 17,466 | 80 |
| cuda-toolkit-config-common | 13.4.92-1 | 17,450 | 80 |
| libnvptxcompiler-13-2 | 13.2.86-1 | 14,288,322 | 103,482 |
| libnvvm-13-2 | 13.2.86-1 | 45,459,030 | 143,449 |

- 저장소 밖 의존: `build-essential`(Debian 패키지).
- 합계: 다운로드 120,902,650바이트(115.3 MiB), 설치 505,027 KiB(약 493 MiB).
- 크기 계산에 쓴 가정: `cuda-toolkit-config-common`과 `cuda-toolkit-13-config-common`은 두 버전 모두 같은 자리에 있어 크기 차이가 무시할 만하다. apt가 어느 버전을 고르는지는 확인하지 않았다.
- `cuda-tileiras-13-2`(26 MB), `cuda-cuobjdump`, `cuda-cuxxfilt`, `cuda-nvprune`은 `cuda-compiler-13-2 13.2.2-1`이 끌어온다. 이 네 개를 빼면 다운로드가 약 26.95 MB 줄어든다 (계산값, 빼고 빌드되는지는 확인하지 않았다).
- **cublas 포함 합계**: `libcublas-dev-13-2`(432,889,808 B, 893,755 KiB)와 `libcublas-13-2`(375,280,050 B, 550,082 KiB)를 더하면 다운로드 929,072,508 B(약 886 MiB), 설치 1,948,864 KiB(약 1.86 GiB)다.
- GitHub Actions 표준 러너(`ubuntu-24.04`, 공개 저장소)는 CPU 4, RAM 16 GB, SSD 14 GB다 (`docs.github.com` 러너 문서). 사설 저장소는 2 CPU, 8 GB다. 빌드 환경 1.86 GiB는 14 GB 안에 들어간다.
  - 다만 이 저장소 `iso.yml`의 `iso` 잡도 같은 러너에서 `make iso`를 돌리므로 ISO 작업 공간과 합친 총량은 계산하지 못했다 (미확인).
  - CUDA 컴파일 시간은 측정하지 못했다 (미확인). CPU 전용 llama-server 빌드가 4코어 amd64 에뮬레이션에서 13~19분이었다는 참고값만 있다.

### B2. 호스트 컴파일러와 trixie 버전

- CUDA 13.2 지원 GCC는 `6.x - 15.x`다 (`https://docs.nvidia.com/cuda/archive/13.2.2/cuda-installation-guide-linux/index.html`, Table 3: "x86_64 6.x - 15.x"). 같은 문서의 Table 1에 `Debian 13 debian13 amd64`가 있다.
- trixie 버전 (madison API, 확인됨)

| 패키지 | 버전 |
|---|---|
| gcc | `4:14.2.0-1` (gcc-14 `14.2.0-19`) |
| g++ | `4:14.2.0-1` (g++-14 `14.2.0-19`) |
| cmake | `3.31.6-2` |
| ninja-build | `1.12.1-1` (amd64는 `1.12.1-1`, arm64는 `1.12.1-1+b1`) |
| golang-go | `2:1.24~2` (golang-1.24 `1.24.4-1`) |
| libssl-dev | `3.5.7-1~deb13u2` |
| libgomp1 | `14.2.0-19` |

- gcc 14는 지원 범위 6~15 안에 있다. `stable-backports`에는 `golang-go 2:1.27~1~bpo13+1`도 올라와 있다.

### B3. `nvidia/cuda` 이미지의 Debian 변형 (확인됨)

- Docker Hub 태그 API(`https://hub.docker.com/v2/repositories/nvidia/cuda/tags?name=…`)에서 `debian`을 포함한 태그가 0개였다. Debian 13 변형은 없다.
- 13.2 태그는 `rockylinux8/9/10`, `oraclelinux8/9`, `ubuntu22.04/24.04`, `ubi8/9/10`, `opensuse15`, `amzn2023` 등 다른 배포판뿐이다.
- 조회 시점에 `13.2.2` 태그도 0개였고, 13.2의 최신은 `13.2.1`(2026-04-20)이었다. 13.4.2 태그는 있다 (2026-09-29).

### B4. 런타임 패키지가 설치하는 경로 (`.deb`를 받아 `tar`로 목록 확인, 확인됨)

- `cuda-cudart-13-2_13.2.86-1`
  - `/usr/local/cuda-13.2/targets/x86_64-linux/lib/libcudart.so.13.2.86` (773,920 B)와 심볼릭 링크 `libcudart.so.13`.
  - `/usr/local/cuda-13.2/lib64 -> targets/x86_64-linux/lib`.
  - `Provides: libcudart.so.13 (= 13.2.86)`.
  - Depends: `cuda-toolkit-config-common | cuda-cudart-11-2, cuda-toolkit-13-config-common | cuda-cudart-11-2, cuda-toolkit-13-2-config-common`.
- `libcublas-13-2_13.4.1.3-1`
  - `libcublas.so.13.4.1.3`(54,198,912 B), `libcublasLt.so.13.4.1.3`(508,260,928 B), `libnvblas.so.13.4.1.3`(737,960 B)와 각 `.so.13` 링크. 위치는 같은 `/usr/local/cuda-13.2/targets/x86_64-linux/lib/`다.
  - 즉 런타임에서 `libcublasLt` 하나가 약 485 MiB다.
- `ld.so.conf.d` 파일은 라이브러리 패키지가 아니라 config-common 패키지가 설치한다.
  - `cuda-toolkit-config-common`: `/etc/ld.so.conf.d/000_cuda.conf`, 내용 `/usr/local/cuda/targets/x86_64-linux/lib`.
  - `cuda-toolkit-13-config-common`: `/etc/ld.so.conf.d/987_cuda-13.conf`, 내용 `/usr/local/cuda-13/targets/x86_64-linux/lib`.
  - 위 두 설정이 가리키는 `/usr/local/cuda`와 `/usr/local/cuda-13`은 `cuda-toolkit-13-2-config-common`의 postinst가 `update-alternatives --install /usr/local/cuda cuda /usr/local/cuda-13.2 132`와 `update-alternatives --install /usr/local/cuda-13 cuda-13 /usr/local/cuda-13.2 132`, 이어서 `ldconfig`로 만든다.
  - 따라서 chroot에서 설치할 때 postinst가 실행되지 않으면 ldconfig가 CUDA 라이브러리를 찾지 못한다.
- `cuda-driver-dev-13-2`는 스텁 `/usr/local/cuda-13.2/targets/x86_64-linux/lib/stubs/libcuda.so`(74,464 B)와 `/usr/lib/pkgconfig/cuda-13.2.pc`만 넣는다. 실제 `libcuda.so.1`은 NVIDIA 드라이버 패키지에서 온다.

## C. go-nvml v0.13.4-1

### C1. 모듈과 태그 (확인됨)

- 모듈 경로: `github.com/NVIDIA/go-nvml`. 패키지는 `github.com/NVIDIA/go-nvml/pkg/nvml`.
- `go.mod`: `go 1.20`, require `github.com/google/uuid v1.6.0`, `github.com/stretchr/testify v1.12.1`, 간접 의존 `go.yaml.in/yaml/v3 v3.0.5`.
- 태그 객체(PGP 서명): `251a2f6126430d09460f64465939939e05e6a844`. 커밋: `1a1e86da474d340d7cf58274588f4a4afc8772e7`. tagger Tariq Ibrahim, 2026-10-06T15:28:38Z.
- `go get github.com/NVIDIA/go-nvml@v0.13.4-1`이 Go 1.24.4에서 정상 해석되었다 (`go: added github.com/NVIDIA/go-nvml v0.13.4-1`).

### C2. API (`pkg/nvml/zz_generated.api.go`, 확인됨)

패키지 수준 함수는 `libnvml` 전역 인스턴스에 묶인 변수다 (`nvml.Init = libnvml.Init` 등). 반환형은 모두 `nvml.Return`이고 `nvml.SUCCESS`와 비교한다.

| 호출 | 시그니처 |
|---|---|
| `nvml.Init()` | `() Return` |
| `nvml.Shutdown()` | `() Return` |
| `nvml.DeviceGetCount()` | `() (int, Return)` |
| `nvml.DeviceGetHandleByIndex(i)` | `(int) (Device, Return)` |
| `nvml.SystemGetDriverVersion()` | `() (string, Return)` |
| `nvml.SystemGetNVMLVersion()` | `() (string, Return)` |
| `nvml.SystemGetCudaDriverVersion()` | `() (int, Return)` (`_v2`도 있음) |
| `Device.GetName()` | `() (string, Return)` |
| `Device.GetUUID()` | `() (string, Return)` |
| `Device.GetMemoryInfo()` | `() (Memory, Return)`: `Memory{Total, Free, Used uint64}` |
| `Device.GetMemoryInfo_v2()` | `() (Memory_v2, Return)`: `Memory_v2{Version uint32; Total, Reserved, Free, Used uint64}` |
| `Device.GetTemperature(sensor)` | `(TemperatureSensors) (uint32, Return)`; `nvml.TEMPERATURE_GPU` 등 |
| `Device.GetPowerUsage()` | `() (uint32, Return)` |
| `Device.GetUtilizationRates()` | `() (Utilization, Return)`: `Utilization{Gpu, Memory uint32}` |
| `Device.GetCudaComputeCapability()` | `() (int, int, Return)`: major, minor |
| `Device.GetArchitecture()` | `() (DeviceArchitecture, Return)`; 상수 `DEVICE_ARCH_AMPERE=7`, `DEVICE_ARCH_ADA=8`, `DEVICE_ARCH_BLACKWELL=10` (`const.go`) |

- 이 외에도 `Device.GetIndex()`, `GetPciInfo()`, `GetFanSpeed()`, `GetPowerManagementLimit()`, `GetClockInfo(ClockType)`, `GetComputeRunningProcesses()`가 `Device` 인터페이스에 있다.
- `GetPowerUsage` 값의 단위는 이 조사에서 `nvml.h` 주석으로 확인하지 못했다 (미확인).
- `Init()` 동작 (`pkg/nvml/init.go:21-26`, 직접 실행해서 확인):
  - `libnvidia-ml.so.1`을 `dlopen`(`RTLD_LAZY|RTLD_GLOBAL`)으로 열지 못하면 `ERROR_LIBRARY_NOT_FOUND`(= 12)를 돌려준다. 패닉은 없다.
  - 라이브러리가 있어도 드라이버가 안 올라와 있으면 `ERROR_DRIVER_NOT_LOADED`(= 9) 상수가 정의되어 있다 (이 경로는 실행해 보지 못함).
  - 실제 출력: `Init() = 12 (ERROR_LIBRARY_NOT_FOUND) ; ret==SUCCESS:false`. 이어 `Shutdown() after failed Init = ERROR_UNINITIALIZED`.
  - **Init이 실패한 뒤 다른 NVML 함수를 부르면 프로세스가 죽는다.** `nvml.DeviceGetCount()`를 불렀더니 Go 패닉이 아니라 로더 오류로 즉시 종료되었다.
    - 출력: `./probe1: symbol lookup error: ./probe1: undefined symbol: nvmlDeviceGetCount`.
    - 원인은 링크 옵션 `-Wl,--export-dynamic -Wl,--unresolved-symbols=ignore-in-object-files` (`nvml.go`의 `#cgo linux LDFLAGS`)이다. 복구할 수 없다.
- 인터페이스와 mock:
  - `nvml.Interface`(`zz_generated.api.go:435`)와 `nvml.Device`(`:848`)가 있다. `nvml.New(opts...)`가 `Interface`를 돌려준다.
  - `pkg/nvml/mock`은 moq가 만든 `mock.Interface`와 `mock.Device` 구조체다. `InitFunc`, `DeviceGetCountFunc`, `GetNameFunc`, `GetMemoryInfoFunc` 같은 필드에 함수를 넣어 쓴다 (`var _ nvml.Interface = &Interface{}`).
  - 실행 확인: `&mock.Interface{InitFunc: …, DeviceGetCountFunc: …}`로 만든 mock에서 `DeviceGetCount()`가 1을 돌려주었다. GPU가 없는 컨테이너다.
  - `mock/dgxa100`, `dgxh100`, `dgxh200`, `dgxb200`가 미리 만든 서버 구성이고 모두 데이터센터 GPU다. RTX 30/40/50 구성은 없다.
  - README는 GPU 구성이 `internal/shared/gpus/`에 있어 외부에서 import 못 한다고 쓰는데, 같은 태그의 파일 트리에는 `pkg/nvml/mock/gpus/`가 있다. 어느 쪽이 맞는지는 import해 보지 않았다 (미확인).

### C3. cgo (확인됨)

- **cgo가 필수다.**
  - `CGO_ENABLED=0 go build`는 실패한다 (`pkg/nvml/zz_generated.api.go:849:24: undefined: Return` 등 오류 다수).
  - `CGO_ENABLED=1`에서는 gcc 14.2.0으로 성공했다 (`deprecated` 경고만 나옴).
  - mock도 `nvml` 패키지를 import하므로 단위 테스트 빌드에도 cgo와 C 컴파일러가 필요하다.
- 빌드에 NVIDIA 헤더나 라이브러리는 필요 없다. `pkg/nvml/nvml.h`가 저장소에 들어 있고 cgo CFLAGS는 `-DNVML_NO_UNVERSIONED_FUNC_DEFS=1`뿐이다.
- 만들어진 바이너리의 `ldd`는 `libc.so.6`과 로더만 보였다. `libnvidia-ml`에는 링크되지 않고 실행 시 `dlopen`한다.
- 이 시험은 `linux/arm64` 컨테이너(네이티브)에서 했다. amd64에서는 돌려 보지 않았다.

## D. Hugging Face Hub API (공개 저장소 `ggml-org/gemma-3-1b-it-GGUF`로 실제 호출, 확인됨)

**D1. 파일 목록, 크기, sha256**

- `GET https://huggingface.co/api/models/{repo}/tree/{rev}`는 `[{type, oid, size, path, lfs?, xetHash?}]`를 돌려준다.
  - LFS 파일은 `lfs: {oid, size, pointerSize}`가 붙는다. `lfs.oid`가 sha256이다.
  - 예: `gemma-3-1b-it-Q4_K_M.gguf`는 `size 806058240`, `lfs.oid "8ccc5cd1f1b3602548715ae25a66ed73fd5dc68a210412eea643eb20eb75a135"`, `oid`(git blob SHA-1)는 `8e4ef4c4400d282149c3baaad288021eb01121ed`였다.
  - LFS가 아닌 `README.md`와 `.gitattributes`에는 `lfs`가 없다.
- 쿼리 파라미터는 OpenAPI(`https://huggingface.co/.well-known/openapi.json`)에 `expand`, `recursive`, `limit`, `cursor`로 나와 있다. `?expand=true`를 주면 `lastCommit`과 `securityFileStatus`가 더 붙는다. `POST /api/models/{repo}/paths-info/{rev}`(본문 `{"paths":[…]}`)도 같은 형태를 돌려준다.
- `GET /api/models/{repo}?blobs=true`는 `siblings[]`에 `{rfilename, blobId, size, lfs:{sha256,size,pointerSize}}`를 준다. 여기서는 필드 이름이 `lfs.sha256`이다.
- 모델 정보 응답(`GET /api/models/{repo}`)에는 `sha`(리비전 커밋), `gated`, `private`, `disabled`, `tags`, `cardData`, `gguf`, `siblings` 등이 있다.
  - `gguf` 객체는 `{total, architecture, context_length, chat_template, bos_token, eos_token, totalFileSize}`다. 아키텍처와 컨텍스트 길이는 GGUF를 받기 전에 이것으로 알 수 있다. `block_count`와 `file_type`은 없다.

**D2. 다운로드**

- URL 형식: `https://huggingface.co/{repo}/resolve/{rev}/{path}`. `rev`는 `main`이나 커밋 SHA 모두 동작했다.
- 첫 요청은 `302`로 서명된 CDN URL로 보낸다 (`us.aws.cdn.hf.co/xet-bridge-us/…`, `Expires`와 `Signature` 포함). 리다이렉트를 따라가야 한다.
- 첫 응답(리다이렉트)의 헤더: `x-repo-commit: f9c28bcd85737ffc5aef028638d3341d49869c27`, `x-linked-size: 806058240`, `x-linked-etag: "8ccc5cd1…eb75a135"`(= sha256), `accept-ranges: bytes`, `x-xet-hash: 107078f2…`, `link: …rel="xet-auth", …rel="xet-reconstruction-info"`. 이 헤더들은 `access-control-expose-headers`에도 나열되어 있다.
- 최종 CDN 응답은 `200`, `content-length: 806058240`이고 `etag`가 xet 해시(`"107078f2…"`)다. **최종 응답의 ETag는 sha256이 아니다.** sha256은 `X-Linked-Etag`에서 얻는다.
- 이어받기: `curl -L -r 0-15`가 `206`, `content-range: bytes 0-15/806058240`을 돌려주었다. `-r 100-103`도 `206`이다. 첫 hop은 `302`이고 Range는 CDN hop에서 처리된다.
- 받은 16바이트: `4747 5546 0300 0000 5401 0000 0000 0000` (GGUF 매직).
- 직접 시험: 1.2 MB 모델을 받아 sha256이 `lfs.oid`와 일치함을 확인했다.

**D3. 라이선스와 gated**

- 라이선스는 `cardData.license`(예: `"gemma"`)와 `tags`의 `license:gemma` 둘 다 있다. 라이선스가 없는 저장소는 둘 다 없다 (`ggml-org/models-moved`가 `cardData.license`가 None, `license:*` 태그 없음).
- `gated`는 `false`, `"auto"`, `"manual"` 중 하나다. `google/gemma-3-1b-it`는 `"gated":"manual"`이었다.
  - 토큰 없이 그 파일을 resolve하면 `401`이고 `x-error-code: GatedRepo`, `x-error-message: Access to model google/gemma-3-1b-it is restricted. You must have access to it and be authenticated…`, `www-authenticate: Bearer realm=…`가 왔다.

**D4. GGUF 검색**

- `GET https://huggingface.co/api/models?search=gemma-3-1b&filter=gguf&sort=downloads&direction=-1&limit=3`이 동작했다 (JSON 배열).
- 다음 페이지는 응답 `Link: <…cursor=…>; rel="next"` 헤더의 커서로 받는다.
- 파라미터 설명은 huggingface_hub 문서(v2.2.0 `list_models`)에 있다: `filter`, `author`, `gated`, `search`("모델 id에 포함되는 문자열"), `sort`(`created_at`, `downloads`, `last_modified`, `likes`, `trending_score`), `limit`, `expand`, `full`.
- 목록 응답은 기본에서 `gguf`·`cardData`·`gated`를 주지 않는다. `expand[]=gguf&expand[]=gated&expand[]=cardData&expand[]=tags&expand[]=downloads`로 요청하니 모델별로 `gated`, `cardData.license`, `gguf.architecture`, `gguf.context_length`가 들어왔다 (예: `ggml-org/Qwen3.8-27B-GGUF`, apache-2.0, 아키텍처 qwen35, 컨텍스트 262144). 문서에 따르면 `expand`는 `full`·`cardData`와 같이 쓸 수 없다. `full=true`로 요청했을 때는 `gguf`·`cardData`가 오지 않았다.
- `/api/models` 검색 엔드포인트 자체는 OpenAPI 파일에 문서화되어 있지 않다. 위 내용은 hf_hub 문서와 실제 호출에서 얻은 것이다.

**D5. 속도 제한 (`https://huggingface.co/docs/hub/rate-limits`)**

- 모든 값은 5분 고정 창 기준이다. 제한은 Hub API, Resolvers(URL에 `/resolve/`가 들어가는 것), Pages 세 묶음으로 나뉜다. 초과하면 `429 Too Many Requests`가 온다.
- 익명(IP별): API 500, Resolvers 3,000, Pages 100. 무료 계정: 1,000, 5,000, 200. PRO: 2,500, 12,000, 400. 문서에 "September '25 기준"이고 익명·무료 값은 "subject to change"라고 적혀 있다.
- 헤더는 IETF 초안 형식이다: `RateLimit: "api|pages|resolvers";r=<남은 횟수>;t=<초기화까지 초>`, `RateLimit-Policy: "fixed window";"…";q=<총량>;w=<창 길이(초)>`. 실제 응답에서 확인했다: API는 `"api";r=494;t=141`과 `"fixed window";"api";q=500;w=300`, resolve는 `"resolvers";r=2999;t=152`와 `q=3000;w=300`.
- 문서는 항상 `HF_TOKEN`을 쓰고, 가능하면 API 호출 대신 resolve 호출을 쓰라고 권한다.

## E. GGUF 메타데이터 (`https://github.com/ggml-org/ggml/blob/master/docs/gguf.md`, 확인됨)

- 헤더 (리틀 엔디언 기본): `uint32 magic`(바이트 `47 47 55 46`, "GGUF"), `uint32 version`(현재 3), `uint64 tensor_count`, `uint64 metadata_kv_count`, 그 뒤 `metadata_kv[]`.
- 각 KV: `gguf_string_t key`(`uint64 len` + UTF-8, 키는 최대 65535바이트), `uint32 value_type`, 값. 문자열 값도 `uint64 len` + 바이트다.
- 값 타입 번호: UINT8=0, INT8=1, UINT16=2, INT16=3, UINT32=4, INT32=5, FLOAT32=6, BOOL=7, STRING=8, ARRAY=9, UINT64=10, INT64=11, FLOAT64=12. 배열은 `uint32 원소타입, uint64 개수`, 이어서 원소들이다.
- 키 이름: `general.architecture`(string, 필수), `general.name`, `general.license`(SPDX 식), `general.file_type`(uint32 열거; 예: 1=MOSTLY_F16, 2=MOSTLY_Q4_0, 7=MOSTLY_Q8_0, 15=MOSTLY_Q4_K_M, 18=MOSTLY_Q6_K), `general.quantization_version`, `general.alignment`(없으면 32), `{arch}.block_count`, `{arch}.context_length`, `{arch}.embedding_length`.
  - 스펙은 개수·길이는 uint64가 관례지만 uint32도 쓰이니 읽는 쪽이 둘 다 받으라고 한다.
  - 실제 gemma3 파일에서는 이들이 uint32였다.
- 실제 파일로 확인: `gemma-3-1b-it-Q4_K_M.gguf`의 앞 16 MiB를 Range로 받아 파싱했다. 헤더는 version 3, tensor_count 340, kv_count 38이었다.
- 키별 파일 내 바이트 오프셋 (시작 위치):

| 키 | 값 | 오프셋 |
|---|---|---|
| `general.architecture` | `gemma3` | 24 |
| `general.name` | `Gemma 3 1b It` | 107 |
| `general.license` | `gemma` | 273 |
| `gemma3.context_length` | 32768 | 621 |
| `gemma3.embedding_length` | 1152 | 658 |
| `gemma3.block_count` | 26 | 697 |
| `tokenizer.ggml.tokens` | 문자열 배열 262,144개 | 1,179 ~ 4,413,742 (4.4 MB) |
| `tokenizer.ggml.scores` | 부동소수 배열 | 4,413,742 ~ 5,462,363 |
| `tokenizer.ggml.token_type` | 정수 배열 | 5,462,363 ~ 6,510,988 |
| `tokenizer.chat_template` | 문자열 | 6,511,250 ~ 6,512,825 |
| `general.quantization_version` | 2 | 6,512,869 |
| `general.file_type` | 15 (Q4_K_M) | 6,512,913 |

- **앞부분만 읽어서 얻을 수 있는가**: 아키텍처, 이름, 라이선스, 컨텍스트 길이, 임베딩 크기, 레이어 수는 이 파일에서 첫 731바이트 안에 있었다. 그러나 `general.file_type`은 약 6.5 MB 뒤에 있었다.
  - 스펙은 KV 순서를 보장하지 않는다. 그래서 일정한 크기(예: 64 KiB)를 받아 순차 파싱하되, 필요한 키가 없고 큰 배열을 만나면 더 읽어야 한다.
  - 문자열 배열은 길이를 따라가야 하므로 건너뛰려면 어차피 내용을 읽어야 한다. 고정 크기 원소 배열만 오프셋 계산으로 건너뛸 수 있다.
  - 양자화 종류만 필요하면 파일명이나 HF `gguf` 필드가 아닌 곳(파일명 규칙 등)에서 추정하는 편이 싸다. 이는 추정이다.

## 설계에 영향을 주는 발견

1. **CUDA를 포함해 한 번 빌드한 결과물이 CUDA 라이브러리·GPU가 없는 시스템에서도 CPU로 동작하는가: 동작한다고 판단한다. 별도 CPU 전용 빌드는 필요 없다.**
   - **소스 근거**
     - `GGML_BACKEND_DL=ON`이면 CUDA 백엔드는 `add_library(ggml-cuda MODULE …)`이고 실행 파일은 이 라이브러리에 링크되지 않는다 (`ggml/src/CMakeLists.txt:381-385`).
     - 로드는 `dlopen(path, RTLD_NOW | RTLD_LOCAL)`이다 (`ggml-backend-dl.cpp`). `libcudart.so.13` 등이 없으면 `dlopen`이 실패해 `nullptr`을 돌려주고, Release(`NDEBUG`)에서는 `silent=true`라 오류 메시지도 없다 (`ggml-backend-reg.cpp:221-228, 578-599`). 이후 CPU 백엔드를 로드하고 계속 진행한다.
     - `libcudart.so.13`은 있는데 GPU나 드라이버가 없어 `cudaGetDeviceCount`가 실패하면, `ggml_cuda_init: failed to initialize CUDA: <오류>`를 ERROR로 한 줄 찍고 장치 0개로 계속한다 (`ggml-cuda.cu:221-228`). 이 경로는 종료하지 않는다. 이 시나리오는 직접 실행해 보지 못했다.
   - **직접 실행한 시험**
     - 위 CPU 빌드 결과를 `/opt/flat`에 복사하고, 존재하지 않는 `libcudart.so.13`에 `DT_NEEDED`가 걸린 가짜 `libggml-cuda.so`를 같은 디렉터리에 두었다 (`gcc -shared … -Wl,--no-as-needed -L/tmp/stub -l:libcudart.so.13` 후 스텁 삭제).
     - `ldd /opt/flat/libggml-cuda.so`는 `libcudart.so.13 => not found`였다.
     - 이 상태에서 `llama-server --list-devices`는 오류 메시지 없이 `Available devices:\n  (none)`을 출력하고 종료 코드 0이었다.
     - 같은 디렉터리에서 `llama-server -m stories260K.gguf -ngl 99 --api-key secret --metrics` 서버를 띄워 `/health` 200, 인증, `/v1/chat/completions`가 모두 정상이었다.
   - **한계**: 진짜 `libggml-cuda.so`(nvcc로 빌드)로는 시험하지 못했다. 진짜 파일의 `NEEDED` 목록(`libcudart.so.13`, `libcublas.so.13`, `libcuda.so.1` 등)은 CMake 소스에서 추정했다 (`ggml-cuda/CMakeLists.txt:175, 181`). CUDA 포함 이미지를 만든 뒤 `ldd`와 GPU 없는 환경에서의 실행으로 한 번 더 확인해야 한다.
   - **데몬이 알아 둘 것**: 이 상황에서 stderr에 `warning: no usable GPU found, --gpu-layers option will be ignored`가 3줄 나온다 (실행으로 확인). 실패가 아니다. 데몬은 이 경고 대신 NVML·`--list-devices`로 GPU 유무를 판단해야 한다.

2. **정해진 CMake 옵션은 이름이 모두 맞다.** 다만 v0.6.0에서 바뀐 점이 있다.
   - `LLAMA_CURL`은 없어졌다(경고만 남음). libcurl은 필요 없다.
   - `LLAMA_BUILD_APP`(기본 ON)을 OFF로 해야 통합 `llama` 바이너리와 그 의존 라이브러리 빌드를 피한다.
   - `LLAMA_BUILD_UI`와 `LLAMA_USE_PREBUILT_UI`를 모두 끄면 UI 없이 성공하며 CMake 경고가 나온다. 이 경고를 CI가 오류로 취급하지 않도록 주의.
   - `libssl-dev`(OpenSSL 3.0 이상)가 빌드 환경에 있어야 HTTPS가 켜진다. 없으면 에러 없이 비활성이다.

3. **`--mlock`과 `--no-mmap` 플래그가 v0.6.0에서 사라졌다** (`error: invalid argument`). `-lm/--load-mode`(`auto`, `none`, `mmap`, `mlock`, `mmap+mlock`, `dio`)를 써야 한다. `-ngl`은 정수·`auto`·`all`을 받고 기본은 `auto`이며, `-fit`이 기본 `on`이라 메모리에 맞춰 미지정 인자를 조정한다.

4. **설치와 배치**
   - `--target llama-server`로만 빌드하면 `cmake --install`이 실패한다. 전체를 빌드해서 설치하거나 `build/bin`을 통째로 복사해야 한다.
   - 기본 RUNPATH는 빌드 디렉터리의 절대경로다. 옮겨 쓰려면 `-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON -DCMAKE_INSTALL_RPATH='$ORIGIN'`이 동작함을 확인했다. 이 경우 실행 파일, 모든 `.so`, 백엔드 모듈을 한 디렉터리에 두는 구조가 된다.
   - 백엔드 모듈은 실행 파일 옆(또는 cwd, 컴파일 타임 `GGML_BACKEND_DIR`)에서만 찾고, 확장자가 정확히 `.so`여야 한다. 따로 `lib/`로 옮기면 못 찾는다. 검색 디렉터리를 바꾸는 환경 변수는 없다.
   - 런타임 필수 패키지는 `libssl3t64`, `libgomp1`, `libstdc++6`이다.

5. **CUDA 빌드 환경 크기**: `cuda-minimal-build-13-2` 폐쇄가 다운로드 115 MiB·설치 493 MiB, 여기에 `libcublas-dev-13-2`(413 MiB 다운로드, 873 MiB 설치)와 `libcublas-13-2`(358 MiB 다운로드, 537 MiB 설치)를 더하면 다운로드 약 886 MiB, 설치 약 1.86 GiB다. 14 GB 디스크의 GitHub 러너에서 이 자체는 문제가 되지 않을 크기다. 다만 CUDA 컴파일 시간과 ISO 빌드와의 합산은 측정하지 못했다.

6. **ISO 런타임 크기와 설치 방식**
   - `libcublasLt.so.13.4.1.3`이 약 485 MiB이고 `libcublas-13-2`가 설치 약 537 MiB다.
   - `ld.so.conf.d` 항목이 `/usr/local/cuda`와 `/usr/local/cuda-13`을 가리키므로, `cuda-toolkit-13-2-config-common` postinst(`update-alternatives` + `ldconfig`)가 chroot 설치 중에 실행되어야 한다.

7. **KV 캐시 양자화 조합 제한**: CUDA 빌드는 기본 `GGML_CUDA_FA_QUANTS="q4_0-q4_0;q8_0-q8_0;f16-f16;bf16-bf16"`만 FlashAttention 벡터 커널로 컴파일한다. 다른 조합(예: K=q8_0, V=q4_0, 또는 q5_x, q4_1)은 f16 변환으로 떨어지고 경고 `no FlashAttention vector kernel compiled for K/V types … converting K and V to f16 instead (slow)`가 나온다 (`ggml-cuda/fattn.cu:505`). `-ctv`가 양자화 타입이면 FlashAttention이 필수다 (`src/llama-context.cpp:3845-3850`). Diparnux가 `-ctk/-ctv` 설정을 노출하려면 허용 조합을 이 목록에 맞추거나 `GGML_CUDA_FA_QUANTS`를 넓혀야 한다 (바이너리 크기가 늘어난다는 이슈 #30001 보고가 있으나 크기는 측정하지 못했다).

8. **go-nvml**
   - `CGO_ENABLED=1`과 gcc가 필요하다 (mock 테스트 포함). NVIDIA 헤더·라이브러리는 필요 없다.
   - `Init()`이 `ERROR_LIBRARY_NOT_FOUND`를 돌려주면 이후 NVML 함수는 호출하면 안 된다. 호출하면 프로세스가 복구 불가능하게 죽는다 (`undefined symbol: nvmlDeviceGetCount`). 모든 NVML 호출 앞에 Init 성공 여부 게이트를 두어야 한다.
   - GPU 없이 테스트하려면 `mock.Interface`에 함수를 직접 채우면 된다 (RTX용 사전 구성은 없다).

9. **오프로드 상태 관측**: `/props`와 `/metrics`에는 장치·레이어 수 정보가 없다. 로그의 `offloaded N/M layers to GPU` 줄은 GPU가 있는 실기에서만 확인할 수 있고, 이 조사에서는 확인하지 못했다. GPU 사용 여부는 `--list-devices` 출력(`CUDA0: …`)과 NVML의 메모리 사용량으로 보는 편이 안전하다.

10. **인증 예외**: `/health`와 `/v1/health`만 키 없이 열려 있다. `/v1/models`, `/props`, `/metrics`는 키가 필요하다. 데몬의 상태 점검은 `/health`로, 모델 정보 조회는 키를 붙여서 해야 한다.

11. **Blackwell과 CUDA 13.2 관련 미해결 보고**
    - #28581(IQ3_S 쓰레기 출력, CUDA 13.2 nvcc 13.2.78)은 열려 있다.
    - #26901(Blackwell dGPU가 iGPU로 분류됨)은 stale로 닫혔을 뿐 해결이 아니고, v0.6.0 소스에 그 분류 코드가 그대로 있다.
    - 실기(RTX 50)에서 직접 확인하지 못했으므로, 대상 GPU에서 실제 모델로 검증하는 단계가 필요하다.

12. **Hugging Face**: 최종 CDN 응답의 `ETag`는 sha256이 아니라 xet 해시다. 무결성 검증용 sha256은 tree의 `lfs.oid`나 첫 hop의 `X-Linked-Etag`에서 얻는다. 익명 resolve 한도는 IP당 5분에 3,000회, API는 500회다 (문서상 변경될 수 있음). 모델 목록과 GGUF 요약은 `expand[]` 파라미터로 한 번에 받는 편이 호출 수가 적다.

## 정리

- 내 작업 컨테이너 `c967c1a41b5d`(이름 `ll-test2`)를 삭제했다.
- 임시 디렉터리를 모두 삭제했다. `/tmp/ll_work`와 `mktemp -d`로 만든 6개(tarball, Packages, deb, go-nvml 소스, HF 응답, 시험 모델).
- 다른 에이전트의 컨테이너 `b9dfd484e6d3`(`dpx-review-tools`)은 건드리지 않았고 그대로 실행 중이다.
- 하네스가 큰 출력을 `~/.claude/projects/…/tool-results/`에 자동 보관한 파일은 내가 만든 것이 아니라 지우지 않았다.
