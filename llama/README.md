# llama (llama.cpp 빌드와 opereta-llm 패키지)

- 확인 날짜: 2026-10-11
- 상태: [CI 38142350756](https://github.com/nxodh/operetaos/actions/runs/38142350756)(`035dfb4`)에서 amd64 CUDA 패키지 빌드, CUDA 없는 환경의 CPU 패키지 추론, ISO 이미지 정책, UEFI CD·USB 부팅, live 사용자 CPU E2E(다운로드·SHA·시작·채팅·데몬 재시작 채택·정지)가 통과했다. **실 NVIDIA GPU 오프로드는 미검증이다.** 앞선 실패 실행의 내용은 [Phase 2 검증 기록](../docs/verification-phase-2.md)에 있다.

## 책임

llama.cpp의 `llama-server`를 고정 커밋에서 소스로 빌드하고, 결과를 `opereta-llm` `.deb`으로 만든다. 이 패키지는 `/usr/lib/opereta/llama`에 설치된다. 내장 웹 UI는 넣지 않는다(사용자 확정 U6).

이 디렉터리의 파일:

| 경로 | 역할 |
|---|---|
| `Dockerfile` | 빌더 이미지. `debian:<DEBIAN_SUITE>` 기반이다. `LLAMA_CUDA=ON`이면 NVIDIA 저장소와 CUDA 빌드 패키지를 더한다. |
| `build.sh` | 컨테이너 안에서 실행한다. 소스 고정, CMake 빌드, 모으기, 검증, `.deb` 생성을 한다. |
| `pkg/debian/` | `control`, `rules`, `copyright`(헤더), `source/format`. `build.sh`가 `changelog`와 `control`의 의존성 줄, `copyright`의 라이선스 원문을 채운다. |

호스트에서 쓰는 스크립트는 `scripts/build-llama-in-docker.sh`(빌드), `scripts/test-llama-in-docker.sh`(패키지 검사), `tests/llama/package.sh`(컨테이너 안의 검사 본체)다.

## 빌드는 한 벌이다

CPU 전용 빌드를 따로 만들지 않는다. CUDA 백엔드는 `GGML_BACKEND_DL=ON`에서 `dlopen`되는 모듈(`libggml-cuda.so`)이 된다. 실행 파일에 링크되지 않는다. CUDA 라이브러리나 GPU가 없으면 로드가 실패하고, Release 빌드는 그 실패를 조용히 넘긴 뒤 CPU 백엔드로 돈다. 근거는 [조사 기록](../docs/verification-phase-2-research.md)의 "설계에 영향을 주는 발견" 1번과 [decisions.md](../docs/decisions.md)의 D4-5다.

## 고정 값

`opereta.conf`만 정의한다.

- `LLAMA_CPP_TAG="v0.6.0"`: 사람이 읽는 이름이다.
- `LLAMA_CPP_COMMIT="d81235049384534c167caea52b85a694f6103d14"`: 빌드가 실제로 쓰는 값이다. 태그 객체 `69ecae92…`를 역참조한 커밋이다(`gh api repos/ggml-org/llama.cpp/git/ref/tags/v0.6.0`).
- 소스는 `git fetch --depth 1 <repo> <commit>`로 받고, `git rev-parse HEAD`가 고정 값과 같은지 확인한다. 자동 생성 tarball은 해시가 고정되지 않으므로 쓰지 않는다.
- 빌드된 `llama-server --version`은 `version: 0.6.0-dev (build N, commit d812350)`로 나온다. `LLAMA_BUILD_IS_DEV`가 기본값(ON)이기 때문이다. 이 값은 그대로 두었다. 빌드 커밋은 `build-info.txt`에 기록된다.

## CMake 옵션과 이유

`build.sh`가 주는 옵션이다.

| 옵션 | 값 | 이유 |
|---|---|---|
| `CMAKE_BUILD_TYPE` | `Release` | 최적화 빌드다. |
| `GGML_NATIVE` | `OFF`(항상 명시) | 기본값 ON은 `-march=native`를 붙여 빌드 머신 CPU 전용 바이너리를 만든다. `SOURCE_DATE_EPOCH`가 있으면 기본값이 OFF로 바뀌어 SIMD가 꺼진다. 어느 쪽이든 명시한다. |
| `GGML_BACKEND_DL` | `ON` | 백엔드를 `dlopen` 모듈로 만든다. CUDA 백엔드를 실행 파일과 분리하는 전제다. |
| `GGML_CPU_ALL_VARIANTS` | `ON` | CPU 변형을 여러 개 만들어 실행 시 고르게 한다. |
| `GGML_CUDA` | `ON`(CUDA 빌드만) | CUDA 백엔드를 켠다. |
| `CMAKE_CUDA_ARCHITECTURES` | `86-real;89-real;120a-real`(CUDA 빌드만) | RTX 30(sm_86), RTX 40(sm_89), RTX 50(sm_120). `-real`이라 PTX가 없다. 그래서 다른 아키텍처(sm_75, sm_80, sm_90 등)용 CUDA 커널은 없다. |
| `LLAMA_BUILD_TESTS` | `OFF` | 테스트 바이너리를 만들지 않는다. |
| `LLAMA_BUILD_EXAMPLES` | `OFF` | 예제를 만들지 않는다. |
| `LLAMA_BUILD_APP` | `OFF` | 통합 `llama` 바이너리와 그 의존 라이브러리를 만들지 않는다. v0.6.0에서 새로 생긴 옵션이고 기본값이 ON이다. |
| `LLAMA_BUILD_UI` | `OFF` | 내장 웹 UI를 만들지 않는다. |
| `LLAMA_USE_PREBUILT_UI` | `OFF` | 기본값 ON은 빌드 중 Hugging Face에서 `dist.tar.gz`를 내려받는다. 끄면 빌드에 네트워크가 필요 없다. |
| `CMAKE_BUILD_WITH_INSTALL_RPATH` | `ON` | 빌드 트리의 RUNPATH를 설치 RUNPATH로 바꾼다. |
| `CMAKE_INSTALL_RPATH` | `$ORIGIN` | 모든 `.so`와 `llama-server`가 자기 디렉터리에서 라이브러리를 찾는다. 빌드 경로가 남지 않는다. |

CUDA 빌드는 환경 변수 `CUDACXX`로 nvcc를 가리킨다. `build.sh`가 `command -v nvcc`로 찾는다.

`cmake --install`은 쓰지 않는다. `--target llama-server`만 빌드하면 install이 다른 타깃의 산출물을 찾다가 실패한다. 대신 `build/bin`에서 필요한 파일만 모은다.

## 빌드 흐름 (`build.sh`)

1. 소스: 고정 커밋을 받고 HEAD가 같은지 확인한다.
2. CMake 구성과 `cmake --build ... --target llama-server`.
3. 모으기: `build/bin`의 `llama-server`와 `*.so*`를 심볼릭 링크를 보존해 `pkgroot/usr/lib/opereta/llama/`에 평평하게 복사한다. `LICENSE`는 `usr/share/doc/opereta-llm/LICENSE.llama.cpp`에 복사한다. `copy-vendor-licenses.sh`가 고정 소스에서 제3자 고지 일곱 개를 추출해 문서 디렉터리에 넣는다.
4. 검증. 하나라도 어긋나면 종료 코드가 0이 아니다.
   - (a) 모든 ELF 파일의 RUNPATH가 `[$ORIGIN]`이다. 로컬 실행 결과는 ELF 15개였다.
   - (b) CUDA 빌드면 `libggml-cuda.so`가 있고, 그 `NEEDED`에 `libcudart.so.13`과 `libcublas.so.13`이 있다. CPU 빌드면 `libggml-cuda.so`가 없어야 한다.
   - 빌드 디렉터리를 지운 뒤 스테이징 트리를 다른 경로로 복사한다.
   - (c) 복사한 트리에서 `llama-server --version`이 0으로 끝난다.
   - (d) `llama-server --list-devices`가 0으로 끝나고 `Available devices:`를 출력한다. 빌드 컨테이너에는 GPU가 없으므로 `(none)`이 정상이다.
   - (e) 스테이징 트리에 `nvcc`, `*.a`, `*.h`, `*.hpp`, `*.cuh`가 없다.
   - `libggml-cpu-*.so`가 하나 이상 있어야 한다.
5. `usr/share/doc/opereta-llm/build-info.txt`를 쓴다. 커밋, CMake 옵션, 도구 버전, `llama-server --version`, CUDA 빌드면 `nvcc`와 설치된 CUDA 패키지 버전, 그리고 모든 ELF의 `readelf -d`와 `ldd` 출력을 담는다.
6. 패키지: `pkg/debian`을 임시 소스 트리로 복사하고 `staging/`에 스테이징 트리를 넣은 뒤 `dpkg-buildpackage -us -uc -b`로 `.deb`을 만든다. `.deb`은 `out/llama/`에 옮기고 `BUILD_KEY`를 쓴다.

## .deb 패키징

- 이름: `opereta-llm_<OPERETA_VERSION>_<arch>.deb`. `Architecture: any`, 소스 형식 `3.0 (native)`.
- `Depends`: `${shlibs:Depends}, ${misc:Depends}`에 CUDA 빌드면 `, opereta-nvidia`를 더한다. `shlibs` 계산 결과는 CPU 스모크 빌드에서 `libc6, libgcc-s1, libgomp1, libssl3t64, libstdc++6`이었다.
- `rules`
  - `override_dh_shlibdeps`: `dh_shlibdeps -X libggml-cuda.so`. CUDA 라이브러리(`libcudart.so.13` 등)에는 dpkg shlibs 정보가 없어서 자동 계산이 실패할 수 있다. CUDA 의존성은 `opereta-nvidia`가 맡는다. CI의 실제 amd64 CUDA 패키지 생성과 의존성 검사가 통과했다.
  - `override_dh_makeshlibs`: 비워 둔다. 이 라이브러리들은 `/usr/lib/opereta/llama`의 비공개 라이브러리다. 이것을 `shlibs`에 등록하면 다른 패키지가 시스템 제공 라이브러리로 볼 수 있다. 빌드 중 `shlibs`와 ldconfig 트리거가 생겨서 이 처리를 넣었다.
  - `override_dh_strip`: `dh_strip --no-automatic-dbgsym`. `-dbgsym` 패키지를 만들지 않는다.
  - `override_dh_dwz`: 비운다. 디버그 정보가 없어 dwz가 경고만 냈다.
  - `override_dh_compress`: `dh_compress -X build-info.txt`. 기록 파일을 압축하지 않는다.
- `copyright`: DEP-5 헤더에 llama.cpp의 MIT 라이선스 원문을 그대로 넣었다(각 줄 앞에 공백 한 칸, 빈 줄은 ` .`). 저작권은 `2023-2026 The ggml authors`다.
  - 제3자 고지는 `vendor-licenses/*.txt`에 원문으로 배포하며 압축하지 않는다. 구성 요소와 출처는 [라이선스 문서](../docs/licenses.md) 4절에 있다.

## 빌더 이미지

- `nvidia/cuda` 이미지에는 Debian 변형이 없어서 쓰지 않는다. `debian:<DEBIAN_SUITE>`에 NVIDIA 저장소를 더한다.
- CUDA 저장소 키는 `cuda-keyring_1.1-1_all.deb`를 내려받아 `CUDA_KEYRING_SHA256`으로 검증한 뒤 `dpkg -i`로 설치한다. 방식은 `scripts/prepare-nvidia.sh`와 같다.
- 설치 패키지(CUDA 빌드): `cuda-minimal-build-13-2=13.2.2-1`, `libcublas-dev-13-2=13.4.1.3-1`. 두 패키지만 정확한 버전으로 고정한다. 나머지 CUDA 의존 패키지는 이 버전들의 `Depends`를 따라 설치된다.
- 공통 도구: `cmake`(3.31.6), `make`, `g++`(14.2.0), `git`, `libssl-dev`, `binutils`, `debhelper`, `dpkg-dev`, `build-essential`(`dpkg-buildpackage`가 암묵적 빌드 의존성으로 확인한다. 없으면 실패한다).
- `ENV PATH=/usr/local/cuda/bin:...`: `/usr/local/cuda`는 update-alternatives가 `/usr/local/cuda-13.2`를 가리키게 만든다.
- 확인한 버전(amd64 CUDA 이미지, 컴파일 없이 이미지와 구성 단계만 실행): `nvcc` 13.2.86(`/usr/local/cuda/bin/nvcc`), `cmake` 3.31.6, `g++` 14.2.0, `cuda-minimal-build-13-2` 13.2.2-1, `libcublas-dev-13-2` 13.4.1.3-1, `cuda-cudart-dev-13-2` 13.2.86-1, `cuda-nvcc-13-2` 13.2.86-1, `libcublas-13-2` 13.4.1.3-1.
- 같은 이미지에서 `cmake` 구성(컴파일 없음)을 실행했다. `GGML_CUDA=ON`과 `86-real;89-real;120a-real`로 구성이 끝났고 `Including CUDA backend`가 출력되었다.
- 크기: CUDA 빌더 이미지 약 4.0 GB, CPU 스모크 이미지 약 0.94 GB.

## 호스트 스크립트와 빌드 키

`scripts/build-llama-in-docker.sh`는 다음을 한다.

- `LLAMA_PLATFORM`(기본 `linux/amd64`, 또는 `linux/arm64`)과 `LLAMA_CUDA`(기본 `ON`, 또는 `OFF`)를 읽는다.
- 빌드 키를 계산한다. 입력은 `llama/` 아래 파일(`README.md` 제외), `opereta.conf`, `LLAMA_CUDA`, `LLAMA_PLATFORM`이다. README를 뺀 이유는 설명 수정 때문에 CUDA 재빌드가 일어나지 않게 하기 위해서다. `LLAMA_CUDA`와 플랫폼을 넣은 이유는 두 빌드가 같은 파일 이름(`opereta-llm_<ver>_<arch>.deb`)을 공유하기 때문이다.
- `out/llama/BUILD_KEY`가 같고 `.deb`이 있으면 건너뛴다. 실측: 두 번째 실행은 1.8초에 건너뛰었다.
- 빌드 전에 `BUILD_KEY`를 지운다. 빌드가 실패하면 다음 실행에서 다시 빌드한다.

CI의 `actions/cache` 키도 같은 입력을 쓴다: `hashFiles('llama/**', '!llama/README.md', 'opereta.conf')`. `!` 제외 패턴은 docs.github.com의 expressions 문서 예시(`hashFiles('/lib/**/*.rb', '!/lib/foo/*.rb')`)로 확인했다. 문서에 문법 설명은 없다.

## 패키지 검사 (`tests/llama/package.sh`)

`scripts/test-llama-in-docker.sh`가 `debian:<suite>` 컨테이너를 `.deb`의 아키텍처 플랫폼으로 띄운다. 컨테이너는 `--rm`으로 지운다. 컨테이너 안에서 다음을 한다.

1. `libgomp1 libssl3t64 ca-certificates curl`만 설치한다. 전제: `ldconfig -p`에 `libcudart`가 없어야 한다.
2. `dpkg-deb -x`로 푼다. `llama-server`가 실행 가능해야 하고 `libggml-cpu-*.so`가 하나 이상 있어야 한다.
3. CUDA 빌드(`libggml-cuda.so`가 있을 때)는 `Depends`에 `opereta-nvidia`가 있어야 하고, `ldd libggml-cuda.so`에 `libcudart.so.13 => not found`가 보여야 한다. CPU 빌드는 `opereta-nvidia`에 의존하면 안 된다.
4. `llama-server --version`과 `--list-devices`가 0으로 끝나고 `Available devices:`가 있어야 한다.
5. 실제 추론: `ggml-org/tiny-llamas`의 `stories260K.gguf`(1,185,376바이트, sha256 `047bf464…c04b`)를 받아 sha256을 확인하고, `-ngl 0`으로 `127.0.0.1`의 임의 포트에 띄운다. `/health`가 200이 될 때까지 기다린 뒤 `/v1/chat/completions`가 `choices`와 비어 있지 않은 `content`를 돌려줘야 한다. 서버는 `trap`으로 정리한다.

로컬 arm64 스모크 빌드 결과(`opereta-llm_0.1.2_arm64.deb`, 4,133,040바이트)의 내용은 다음과 같다.

- `llama-server` 67,736바이트, `libllama-server-impl.so` 2,837,952바이트, `libllama.so.0.6.0` 3,692,384바이트, `libllama-common.so.0.6.0` 4,085,600바이트, `libmtmd.so.0.6.0` 1,580,536바이트, `libggml.so.0.26.0` 67,976바이트, `libggml-base.so.0.26.0` 727,216바이트.
- `libggml-cpu-*.so` 8개(armv8.0_1, armv8.2_1·2·3, armv8.6_1·2, armv9.2_1·2). 각 약 0.92~0.99 MB.
- 심볼릭 링크: `libggml*.so`, `libllama*.so`, `libmtmd.so`의 `.so`와 `.so.N` 링크.
- `usr/share/doc/opereta-llm/`: `copyright`, `changelog.gz`, `LICENSE.llama.cpp`(1,078바이트), `build-info.txt`(39,361바이트).
- control: `Depends: libc6 (>= 2.38), libgcc-s1 (>= 3.0), libgomp1 (>= 6), libssl3t64 (>= 3.3.0), libstdc++6 (>= 14)`. `shlibs`와 `triggers`는 없다.

## ISO 연결

- `scripts/build-in-docker.sh`는 `out/llama`를 컨테이너의 `/llama`에 읽기 전용으로 마운트한다.
- `scripts/build-iso.sh`는 `${OPERETA_LLAMA_DIR:-/llama}/opereta-llm_${OPERETA_VERSION}_amd64.deb`를 `build/config/packages.chroot/`로 복사한다. 없으면 `make llama`를 먼저 실행하라고 실패한다. Debian 호스트에서 `iso-native`로 빌드할 때는 `OPERETA_LLAMA_DIR=<저장소>/out/llama`를 준다.
- `build/config/package-lists/opereta.list.chroot`에 `opereta-llm`을 추가했다.
- `build-iso.sh`는 chroot에 `llama-server`와 `libggml-cuda.so`가 있는지 확인한다.
- `tests/nvidia/image.sh`는 `opereta-llm`이 설치되었는지, `llama-server`, `libggml-cuda.so`, `libggml-cpu-*.so`가 있는지 확인한다. ISO 빌드와 이미지 검사는 CI가 한다(로컬에서는 하지 않았다).

## 로컬에서 할 수 있는 것과 없는 것

- CUDA 빌드 전체는 로컬에서 하지 않는다. CI의 첫 전체 CUDA 패키지 빌드 단계는 44분 7초였다(빌더 준비·컴파일·패키징 포함). Docker VM 메모리는 약 4 GB이고, 로컬 컴파일이 메모리를 넘칠 수 있다는 점은 실측하지 않았다.
- 로컬 CPU 스모크 빌드(arm64 Mac, 네이티브 arm64 컨테이너): `LLAMA_CUDA=OFF LLAMA_PLATFORM=linux/arm64 make llama`. 실측 약 43분(컴파일 단계가 대부분). 이어서 `make test-llama-docker`(실측 약 17초, 추론 포함). 검사가 끝나면 `make clean-llama`로 산출물을 지운다. 아키텍처가 다른 `.deb`이 `out/llama`에 남으면 `make iso`와 CI가 혼동하기 때문이다.
- amd64 빌드는 로컬 arm64 Mac에서 에뮬레이션으로 돌아간다. 조사 기록에 따르면 에뮬레이션된 4코어에서 CPU 전용 llama-server 빌드가 13~19분이었다. 이 값은 CUDA 빌드의 시간을 말해 주지 않는다. 로컬에서는 스모크 빌드만 한다.

## 검증 상태

| 항목 | 상태 |
|---|---|
| 소스 고정 커밋 확인 | 확인됨(로컬 arm64 빌드) |
| CPU 전용 빌드와 검사 (a)~(e), `--version`, `--list-devices` | 확인됨(로컬 arm64) |
| `.deb` 생성과 파일 목록 | 확인됨(로컬 arm64) |
| BUILD_KEY 건너뛰기 | 확인됨(로컬) |
| 패키지 검사와 CPU 추론 | 확인됨(로컬 arm64) |
| CUDA 빌더 이미지(nvcc, cmake, g++, CUDA 패키지 버전) | 확인됨(로컬 amd64 이미지, 컴파일 없음) |
| CUDA 구성 단계(CMake, `GGML_CUDA=ON`) | 확인됨(로컬, 컴파일 없음) |
| CUDA 모듈 컴파일과 `libggml-cuda.so`의 `NEEDED` | 확인됨(CI amd64, 38131367776) |
| CUDA `.deb`의 `dh_shlibdeps -X libggml-cuda.so`와 `opereta-nvidia` 의존성 | 확인됨(CI amd64, 38131367776) |
| CUDA 패키지 검사(`ldd`의 `libcudart.so.13 => not found`, 추론) | 확인됨(CI amd64, 38131367776) |
| ISO 안의 `opereta-llm`과 이미지 검사 | 확인됨(CI 38131367776: 0.2.0 포함, 이미지 정책 검사 통과) |
| live ISO CPU E2E 전체(다운로드·SHA·시작·채팅·데몬 재시작 채택·정지) | 확인됨(CI 38142350756, GPU 없는 QEMU/KVM VM) |
| 실 NVIDIA GPU에서의 오프로드와 추론 | **미확인**(실기 필요) |

## 남은 문제와 한계

- CUDA 아키텍처는 sm_86, sm_89, sm_120a만 있다(`-real`, PTX 없음). RTX 20과 그 이전, 데이터센터 GPU(sm_80, sm_90 등)용 CUDA 커널은 없다. 대상 범위는 [support-matrix.md](../docs/support-matrix.md)에 있다.
- `GGML_CUDA_FA_QUANTS`는 기본값이다. KV 캐시 양자화 조합 중 기본 목록에 없는 것은 f16으로 변환되어 느리다([조사 기록](../docs/verification-phase-2-research.md)의 발견 7번).
- llama.cpp v0.6.0의 제3자 보고(IQ3_S 출력 오류, Blackwell iGPU 분류, CUDA graph hang 등)는 이 빌드에서 재현하지 않았다. 실기 검증 항목으로 [support-matrix.md](../docs/support-matrix.md)에 적었다.
- 제3자 고지 추출과 최종 CUDA `.deb`의 고지 파일 포함 검사가 통과했다.
- 첫 CI CUDA 패키지 빌드 단계는 44분 7초였다. CI의 `iso` job 제한 시간은 180분이다.
- `--version`의 `0.6.0-dev`는 `LLAMA_BUILD_IS_DEV` 기본값 때문이다. 버전 표시를 바꾸려면 옵션을 바꾸고 `build-info`와 함께 기록해야 한다.
- 로컬 arm64 스모크 빌드 산출물은 검사 후 지웠다. CI가 같은 키로 amd64 `.deb`을 만든다.

## 근거

- 태그 `v0.6.0`과 커밋 `d81235…`: `gh api repos/ggml-org/llama.cpp/git/ref/tags/v0.6.0`
- LICENSE(MIT): https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/LICENSE
- CMake 옵션: https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/ggml/CMakeLists.txt , https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/CMakeLists.txt
- 모델: https://huggingface.co/api/models/ggml-org/tiny-llamas/tree/main (`stories260K.gguf`의 `lfs.oid`)
- CUDA 저장소 키: https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/cuda-keyring_1.1-1_all.deb
- 조사 기록: [verification-phase-2-research.md](../docs/verification-phase-2-research.md) A2, A4, B1, B4, 발견 1번과 7번
