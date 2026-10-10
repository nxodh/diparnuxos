# 2026-10-10 검증 기록

- 확인 날짜: 2026-10-10
- 상태: 초안

> **이 환경(macOS)에서는 빌드와 부팅을 하지 못했다. 따라서 실기 동작은 미검증이다.** 아래 판정은 패키지 메타데이터, 소스, 공식 문서를 1차 출처에서 직접 확인한 결과다. 실제 하드웨어 결과가 아니다. 빌드, `ldd`, 부팅 테스트는 Phase 0 이후 Linux 환경에서 해야 한다.

이 문서는 사전 조사(research)와 검증(verification) 결과를 정리한다. 조사자와 검증자의 결과가 다르면 검증자를 따른다. `corrected` 또는 `refuted` 항목은 검증자가 준 `corrected_value`를 따른다.

판정 표기:

- **확인**: 주장이 1차 출처와 맞다(`confirmed`).
- **정정**: 주장이 대체로 맞지만 조건이나 값을 고쳐야 한다(`corrected`).
- **반박**: 주장이 틀렸다(`refuted`).
- **미검증**: 재확인하지 않았거나 이 환경에서 확인할 수 없다(`could_not_verify`).

---

## 1. 검증 항목 요약

검증자가 기록한 항목은 36개(번호 0~35)다. 아래 표의 판정은 검증 기록의 `status` 값을 옮긴 것이다.

| # | 주장 | 판정 | 근거 요약 | 출처 |
|---|---|---|---|---|
| 0 | Production Branch 최신은 595.104.02(2026-09-22) | 확인 | 드라이버 조회 API 응답(Release 595, IsRecommended 1), `latest.txt` | https://www.nvidia.com/en-us/drivers/unix/ |
| 1 | New Feature Branch 최신은 615.78.08(2026-10-07), 더 새로운 Beta 없음 | 확인 | NFB 조회 615.78.08, Beta 조회는 595.45.04(2026-03-05) | https://download.nvidia.com/XFree86/Linux-x86_64/ |
| 2 | 저장소 595 최신은 595.91.07, 595.104.02는 없음 | 확인 | `nvidia-open` 버전 목록에 595.91.07-1까지. 595.99.02와 595.104.02의 deb는 404 | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz |
| 3 | debian13 저장소와 `cuda-keyring` 1.1-1 | 확인 | `Packages.gz` 2246개 항목, Release 2026-10-09, 키 패키지 내 `.list` 확인 | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/cuda-keyring_1.1-1_all.deb |
| 4 | `nvidia-driver-pinning-<branch>` 패키지가 있고 브랜치는 590, 595, 610, 615 | 확인 | 595-1 deb의 `nvidia-driver-pin`: 4블록, Pin-Priority 1000 | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-pinning-595_595-1_all.deb |
| 5 | 독점 모듈은 Pin-Priority -1로 차단하면 되고 Debian 550.x는 `595*`에 걸려 밀린다 | **정정** | apt 3.0.3은 버전마다 먼저 읽힌 핀 하나만 적용. 차단 파일은 `nvidia-driver-pin`보다 먼저 읽혀야 한다. Debian 550.x는 기본 우선순위 500으로 남는다. | https://salsa.debian.org/apt-team/apt/-/raw/3.0.3/apt-pkg/policy.cc |
| 6 | 오픈 모듈 실제 이름은 `nvidia-open`과 `nvidia-kernel-open-dkms`, 595 최신 595.91.07-1 | 확인 | `nvidia-open`의 Depends, `nvidia-kernel-open-dkms`의 Source는 `nvidia-kmod-open`. `nvidia-open-<branch>` 패키지는 없다 | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz |
| 7 | 의존성만 따라가면 오픈 모듈이 선택된다 | **정정** | `nvidia-driver`와 `libcuda1`은 독점 `nvidia-kernel-dkms`를 첫 대안으로 둔다. 오픈 모듈을 같은 트랜잭션에 명시해야 한다. | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz |
| 8 | 컴퓨트 전용 패키지는 `nvidia-driver-cuda`이고 `nvidia-smi` 패키지는 더미 | 확인 | `nvidia-driver-cuda`가 `nvidia-smi`를 제공. `nvidia-smi` 패키지는 transitional dummy. 폐쇄 25개, 241.5 MiB | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-cuda_595.91.07-1_amd64.deb |
| 9 | `nvidia-driver-cuda` + `nvidia-kernel-open-dkms` 조합이 맞다(헤드리스 전제) | **정정** | 컴퓨트 조합에는 EGL, GBM, Wayland 라이브러리와 절전 유닛이 없다. GUI 기준은 `nvidia-kernel-open-dkms` + `nvidia-driver` + `nvidia-driver-cuda`. | https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/latest/debian.html |
| 10 | NVIDIA 가이드대로 Debian `contrib`를 켜야 하고 겹치는 것은 `nvidia-kernel-dkms` 정도다 | **정정** | 의존성 이름 38개가 모두 trixie `main`에 있다. 같은 이름 64개가 겹친다(예: `dkms`는 NVIDIA 3.4.3-1, Debian 3.2.2-1~deb13u1). `contrib` 불필요. | https://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.xz |
| 11 | 595 브랜치는 nvidia-drm modeset과 fbdev가 기본 활성 | 확인 | `kms.html` 595.91.07 문구, 소스 `nv_drm_modeset_module_param = true` | https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kms.html |
| 12 | RTX 50(Blackwell)은 오픈 커널 모듈만 지원 | 확인 | `kernel_open.html`: "Blackwell and later are only supported by the open kernel modules" | https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kernel_open.html |
| 13 | Debian 13에는 오픈 커널 모듈 패키지가 없고 550 독점 DKMS 구조다 | **반박** | `contrib`에 `nvidia-open-kernel-dkms` 550.163.01-2가 있다. 550은 RTX 50을 지원하지 않으므로 NVIDIA 저장소가 필요하다는 결론은 유지. | https://api.ftp-master.debian.org/madison?package=nvidia-open-kernel-dkms&s=trixie,trixie-backports&text=on |
| 14 | 최신 CUDA 안정판은 13.4.2(2026년 9월) | 확인 | 아카이브의 Latest Release. 정확한 날짜는 확인하지 못함 | https://developer.nvidia.com/cuda-toolkit-archive |
| 15 | debian13 저장소의 툴킷은 13.1 ~ 13.4만 있고 12.x는 없음 | 확인 | `cuda-toolkit-13-1` ~ `13-4`. `cuda-compat-13-2`는 595.45.04 ~ 595.91.07 | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz |
| 16 | CUDA 13.4는 sm_86, sm_89, sm_120을 지원 | 확인 | nvcc 허용 목록과 GPU 표(8.6, 8.9, 12.0) | https://docs.nvidia.com/cuda/cuda-compiler-driver-nvcc/index.html |
| 17 | CUDA 13.4의 대응 드라이버는 R615, 13.x 마이너 호환 최소는 580 | 확인 | 릴리스 노트 Table 2, Table 3. 13.4의 새 기능은 R615 이상이 필요. 595는 13.2에 대응 | https://docs.nvidia.com/cuda/cuda-toolkit-release-notes/index.html |
| 18 | CUDA 13.4는 Debian 13을 공식 지원 | 확인 | 설치 가이드 지원 표. 검증 OS는 Debian 13.6, 커널 6.12.94, GCC 14.2.0, glibc 2.41 | https://docs.nvidia.com/cuda/cuda-installation-guide-linux/index.html |
| 19 | 런타임 패키지는 `cuda-cudart-13-4`, `libcublas-13-4`, `cuda-nvrtc-13-4` | 확인 | `Provides`에 `libcudart.so.13`, `libcublas.so.13`. md5 목록에 cuBLAS 라이브러리 | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/libcublas-13-4_13.8.0.4-1_amd64.deb |
| 20 | llama.cpp CUDA 백엔드는 cudart, cublas, cuda_driver에 링크 | 확인 | CMake의 링크 대상. 실제 `ldd`는 이 환경에서 얻지 못함 | https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/ggml/src/ggml-cuda/CMakeLists.txt |
| 21 | llama.cpp 최신 정식 릴리스는 v0.6.0(2026-10-05), 빌드 태그는 bNNNNN prerelease | 확인 | `releases/latest`의 v0.6.0, prerelease false. 빌드 번호는 b11429 | https://api.github.com/repos/ggml-org/llama.cpp/releases/latest |
| 22 | CUDA 빌드는 `GGML_CUDA=ON`, `CMAKE_CUDA_ARCHITECTURES`에 86;89;120 | **정정** | `120`은 `120a`로 바뀌고 PTX가 함께 들어간다. `GGML_NATIVE` 기본값 때문에 CPU 쪽이 빌드 머신 전용이 되거나 `SOURCE_DATE_EPOCH`가 있으면 SIMD가 꺼진다. | https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/ggml/CMakeLists.txt |
| 23 | llama.cpp 소스 빌드는 네트워크 없이 재현 가능 | **반박** | `LLAMA_USE_PREBUILT_UI`가 기본 ON. Hugging Face에서 `dist.tar.gz`를 받는다. | https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/scripts/ui-assets.cmake |
| 24 | CUDA EULA Attachment A에 `libcudart.so`, `libcublas.so`, `libcublasLt.so`가 재배포 가능 | 확인 | 같은 문서의 조건 1.1.2, 2.3. 법률 해석은 하지 않음 | https://docs.nvidia.com/cuda/eula/index.html |
| 25 | 드라이버 라이선스 1.1(d)는 OSI 승인 커널용 배포를 무수정, 계약서 제공 조건으로 허용 | 확인 | 패키지 동봉 `copyright`. 웹 원문은 따로 받지 않음 | https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-cuda_595.91.07-1_amd64.deb |
| 26 | trixie의 labwc 0.8.3-1, greetd 0.10.3-4, calamares 3.3.14-1, systemd-zram-generator 1.2.1-2 | 확인 | madison과 dists 인덱스 | https://api.ftp-master.debian.org/madison?package=labwc&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on |
| 27 | trixie 커널 6.12.107-1, trixie-security 6.12.111-1, backports 7.2.6 | 확인 | madison과 trixie-security `Packages`. Debian 13.7 릴리스(2026-09-12) | https://api.ftp-master.debian.org/madison?package=linux-image-amd64&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on |
| 28 | 드라이버가 없어도 라이브와 첫 부팅 그래픽 세션이 뜬다 | **반박** | 6.12.111 설정에 `SIMPLEDRM`과 `SYSFB_SIMPLEFB`가 꺼져 있다. nouveau는 Ada까지만 안다. Blackwell은 6.16에서 추가. RTX 50은 드라이버가 필요하다. | https://security.debian.org/debian-security/pool/updates/main/l/linux/linux-headers-6.12.111+deb13-amd64_6.12.111-1_amd64.deb |
| 29 | Secure Boot가 켜져 있어도 DKMS 모듈이 로드된다 | **반박** | `CONFIG_MODULE_SIG`, `CONFIG_SECURITY_LOCKDOWN_LSM`, `CONFIG_LOCK_DOWN_IN_EFI_SECURE_BOOT`가 켜져 있다. DKMS가 키를 자동 생성한다. | https://raw.githubusercontent.com/dell/dkms/v3.2.2/README.md |
| 30 | Calamares가 greetd를 알아서 설정하거나 `calamares-settings-debian`을 그대로 쓸 수 있다 | **반박** | displaymanager 목록에 greetd가 없다. greetd를 넣으면 Calamares가 `greeter` 사용자와 고정 명령으로 `config.toml`을 덮어쓴다. | https://raw.githubusercontent.com/calamares/calamares/v3.3.14/src/modules/displaymanager/main.py |
| 31 | trixie의 labwc가 NVIDIA Wayland에 필요한 explicit sync를 지원 | **반박** | 0.9.0에서 `drm-syncobj` 추가. 0.8.3 소스에는 `syncobj`가 없다. 실제 화면 영향은 실기 테스트가 필요. | https://raw.githubusercontent.com/labwc/labwc/0.20.2/NEWS.md |
| 32 | go-nvml로 Go 단일 바이너리를 만들 수 있다 | **정정** | cgo를 쓰므로 glibc 동적 링크 바이너리다. 실행 시 `libnvidia-ml.so.1`을 `dlopen`한다. NVML이 없으면 런타임 오류. | https://raw.githubusercontent.com/NVIDIA/go-nvml/v0.13.4-1/pkg/nvml/nvml.go |
| 33 | live-build로 NVIDIA 저장소, 핀, 헤더를 넣을 수 있다 | 확인 | `lb_config` 옵션(`--linux-packages`, `--archive-areas`), `config/archives` 확장자 규칙, `SOURCE_DATE_EPOCH` 전달. `lb build` 전체 실행은 하지 않음 | https://deb.debian.org/debian/pool/main/l/live-build/live-build_20250505+deb13u1_all.deb |
| 34 | snapshot.debian.org 날짜 고정, Debian 상표 정책, GitHub 이름 조사 결과 | **미검증** | 검증자는 재확인하지 않았다. 조사 결과를 그대로 두되 검증된 것으로 취급하지 않는다. `wiki.debian.org`와 `git.kernel.org`는 봇 차단 페이지가 내려와 다른 출처로 대신했다. | — |
| 35 | 실기 동작(R595에서 CUDA 13.4.2 실행, labwc의 NVIDIA 화면 품질, chroot DKMS 빌드와 live-build 전체 빌드, RTX 50의 드라이버 없는 부팅) | **미검증** | 이 환경은 macOS라 빌드, `ldd`, 부팅을 할 수 없었다. 위 항목은 메타데이터, 소스, 공식 문서로만 확인했다. | — |

판정 개수: 확인 22개, 정정 6개(5, 7, 9, 10, 22, 32), 반박 6개(13, 23, 28, 29, 30, 31), 미검증 2개(34, 35). 합계 36개다.

---

## 2. 검증자가 제안한 결정 변경

검증자는 decision_changes 12건을 제안했고, 사용자가 전부 수용했다(U2). 다만 CUDA 런타임의 첫 부팅 설치 제안은 사용자 결정 U3에 따라 ISO 포함으로 바꿨다. 아래는 그 요약이다. 자세한 내용은 [decisions.md](decisions.md)에 있다.

| 결정 | 심각도 | 요지 | 문서 반영 |
|---|---|---|---|
| 8. NVIDIA 구성 요소를 첫 부팅에 설치 | must_change | 드라이버는 ISO에 넣는다. CUDA 런타임은 검증자가 첫 부팅 설치를 제안했으나 사용자가 ISO 포함으로 정했다. | D8, U3, U4 |
| 3(및 2). Secure Boot 방침 없음 | must_change | DKMS 개인키 제거, MOK 흐름 또는 Secure Boot 끄기 요구, 라이브 안내 | D9, [secure-boot.md](secure-boot.md) |
| 4. llama.cpp 빌드 옵션 | must_change | `GGML_NATIVE=OFF` 등 확정 옵션, 웹 UI 자산 처리 | D4-2, [llama/README.md](../llama/README.md) |
| 4. CUDA 버전 | should_change | 드라이버 브랜치에 맞춰 13.2.2 | D4-1, U5 |
| 3. 오픈 커널 모듈 패키지 구성 | should_change | 설치 조합과 차단 핀 | D3-1, D3-2, [drivers.md](drivers.md) |
| 3. NVIDIA 저장소 추가 방식 | should_change | `main` + `non-free-firmware`, dkms 핀 | D3-3, D3-4 |
| 3. Production Branch 고정 | clarify | 브랜치 595, 실제 버전은 저장소 최신 | D3-5 |
| 7. `nvidia-drm.modeset=1` | should_change | 삭제 | D7 |
| 2. 설치기는 Calamares | should_change | Lightlix 전용 설정 | D2 |
| 6. GUI는 labwc + greetd + GTK4 | clarify | labwc explicit sync 시험 | D6, [drivers.md](drivers.md) 13절 |
| 5. lightlixd는 Go 단일 바이너리 | clarify | 동적 링크, NVML 없이 기동 | D5 |
| 1. 베이스 Debian 13, 커널 6.12 | clarify | 헤더, trixie-security | D1 |

---

## 3. 조사 단계의 미확인 항목

조사 기록(research)에 남은 미확인 항목은 다음과 같다. 검증자는 이 항목들을 다루지 않았다. 조사자가 미확인으로 남겼지만 검증자가 해소한 항목은 목록 아래에 따로 적는다.

- 595.104.02가 저장소에 언제 올라올지 확인하지 못했다.
- 615.78.08과 595.104.02의 릴리스 노트 원문은 직접 조회하지 않았다.
- CUDA 13.4.2의 정확한 릴리스 날짜는 월 단위(2026년 9월)만 확인했다.
- CUDA 13.4(R615)의 Linux 전용 최소 드라이버 숫자는 확인하지 못했다.
- llama-server의 실제 `ldd` 결과는 이 환경에서 확인하지 못했다.
- CUDA 13과 llama.cpp의 알려진 이슈는 키워드 검색 기반이라 누락이 있을 수 있다. 이슈 #29890(`Available devices: (none)`, b11351 이후)의 원인은 확인하지 못했다.

검증자가 해소한 항목:

- `nvidia-smi` 바이너리는 `nvidia-driver-cuda` 패키지의 `usr/bin/nvidia-smi`에 있다.
- 595.45.04와 595.58.03의 README도 DRM KMS가 기본 활성이라고 적는다.
- `libcublas-13-4` 패키지에 `libcublasLt`가 들어 있다.
- sfwbar가 공식 Debian 아카이브(sid 포함)에 있는지는 확정하지 못했다. trixie에는 없다는 것만 확인했다.
- `snapshot.debian.org` 기반 `live-build` 전체 빌드는 실행하지 않았다. URL 형식과 리다이렉트만 확인했다.
- GitHub 계정 `lightlix`의 주체와 활동은 조회하지 않았다.
- `tracker.debian.org`는 직접 조회하지 않았다.
- trixie-updates와 trixie-backports의 대부분 패키지는 개별 확인하지 않았다. trixie-security는 main의 `binary-amd64`만 확인했다.
- arm64 저장소, Debian 12 저장소, Debian non-free 저장소의 `nvidia-driver` 대안은 조사 범위 밖이다.
- 드라이버 라이선스의 개정일은 찾지 못했다.

---

## 4. snapshot.debian.org 사용법 (미검증)

> 이 절은 조사자의 결과다. 검증자가 재확인하지 않았다. 미검증으로 취급한다.

- URL 형식: `https://snapshot.debian.org/archive/debian/<YYYYMMDDTHHMMSSZ>/`. 보안 저장소는 `https://snapshot.debian.org/archive/debian-security/<timestamp>/`다(suite는 `trixie-security`).
- 현재도 동작한다는 조사 결과다. `.../archive/debian/20260912T000000Z/dists/trixie/Release`가 302로 리다이렉트된 뒤 200을 준다(curl에 `-L` 필요).
- 주의: 타임스탬프는 그 시점 이전의 가장 가까운 스냅샷을 가리킨다. `20260912T000000Z`는 Debian 13.7 릴리스(2026-09-12 07:55 UTC) 직전의 13.6 상태다.
- 스냅샷의 Release에는 `Valid-Until`이 남아 있다. 기한이 지나면 apt가 `Release file expired`로 거부한다. 해결책은 `[check-valid-until=no]` 또는 `apt -o Acquire::Check-Valid-Until=false`다.
- 이 값은 `lightlix.conf`의 `DEBIAN_SNAPSHOT`으로 받는다(형식 `YYYYMMDDTHHMMSSZ`, 비어 있으면 미러 사용). 이 문서는 값을 정의하지 않는다.
- 출처: https://snapshot.debian.org/ . 조사 기록에 따르면 trixie 본 저장소 Release에는 `Valid-Until`이 없고, trixie-updates, trixie-backports, trixie-security에만 있다.

---

## 5. 검증이 안 된 것 (요약)

1. 모든 실기 동작(35번). 드라이버 빌드, 부팅, labwc 화면, CUDA 실행.
2. snapshot.debian.org 사용법(34번과 4절).
3. Debian 상표 정책 해석(34번). 조사자 한 명의 결과이며 검증자가 재확인하지 않았다. 자세한 내용은 [licenses.md](licenses.md) 5절.
4. GitHub 이름 관련 조사(34번). 조사 시점에 원래 사양의 조직 이름 후보는 HTTP 404였고, 같은 이름의 `lightlix` 계정은 HTTP 200이다. 현재 저장소 결정으로 대체되어 이 조직 이름은 쓰지 않는다.
