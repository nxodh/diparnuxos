# 라이선스와 재배포 조건

- 확인 날짜: 2026-10-10
- 상태: 초안

> 이 문서는 법률 자문이 아니다. 라이선스 원문을 인용하고 출처를 적는다. 해석이 필요한 부분은 해석 위험으로 표시한다. 최종 판단은 법률 검토나 NVIDIA 문의로 확인해야 한다(NVIDIA 라이선스 질문 창구는 조사 기록에 있는 `nvidia-compute-license-questions`다).

Lightlix ISO에는 다음 제3자 소프트웨어가 들어간다. NVIDIA 드라이버, CUDA 런타임, Intel 무선·이더넷 펌웨어(9절), llama.cpp와 Debian 패키지다. 프로젝트 자체 라이선스는 아직 정하지 않았다.

---

## 1. NVIDIA 드라이버 라이선스

### 1.1 재배포 조건 원문

출처: NVIDIA 드라이버 패키지에 들어 있는 `usr/share/doc/nvidia-driver-cuda/copyright` (`nvidia-driver-cuda` 595.91.07-1). 웹 원문은 따로 받지 않고 패키지 동봉본으로 확인했다.

> d. Distribute the SOFTWARE provided for use with operating system kernels distributed under the terms of an OSI-approved open source license ..., provided that (i) the binary files thereof are not modified in any way (except for uncompressing of compressed files) and (ii) this Agreement is provided to each SOFTWARE recipient.

한국어 요약: OSI 승인 오픈소스 라이선스로 배포되는 운영체제 커널과 함께 쓰도록 드라이버를 배포할 수 있다. 조건은 두 가지다. (i) 바이너리 파일을 어떤 방식으로도 수정하지 않는다(압축 해제는 허용). (ii) 이 라이선스 계약서를 드라이버를 받는 각 수령자에게 제공한다.

### 1.2 그 밖의 제한 조항

출처: 같은 `copyright` 파일 (검증 기록 check 25).

> 2.8 You agree that GeForce or Titan SOFTWARE: (i) is licensed for use only on GeForce or Titan hardware products you own, and (ii) is not licensed for datacenter deployment.

한국어 요약: GeForce 또는 Titan 소프트웨어는 소유한 GeForce 또는 Titan 하드웨어에서만 쓸 수 있고, **데이터센터 배포용으로 허가되지 않는다.**

- 이 프로젝트의 대상 하드웨어(RTX 30, 40, 50)는 GeForce 제품이다. 따라서 이 조항이 적용된다.
- 로컬 LLM 추론 서버를 데이터센터에 배포하는 용도에는 맞지 않을 수 있다. 이 프로젝트는 개인과 소규모 환경을 대상으로 한다고 적고, 데이터센터 배포 여부는 사용자가 판단한다. 이 문서의 해석은 법률 자문이 아니다.
- 자세한 조건은 원문 전체를 읽어야 한다.

### 1.3 오픈 커널 모듈의 라이선스

출처: 오픈 커널 모듈 패키지의 `copyright` (검증 기록 check 25).

- 오픈 커널 모듈은 "the resulting Linux kernel module is dual licensed as MIT/GPLv2"라고 적혀 있다. 즉 커널 모듈 소스는 MIT 또는 GPLv2 이중 라이선스다.
- 사용자 공간 라이브러리와 GSP 펌웨어(`firmware-nvidia-gsp`)는 위의 독점 라이선스 대상이다.

### 1.4 ISO에 포함하는 결정과 근거

- 드라이버를 ISO에 포함하는 것은 사용자 확정 U4다([decisions.md](decisions.md)의 D8).
- 근거는 1.1의 (i), (ii) 조건이다. 바이너리를 수정하지 않고 라이선스 원문을 함께 제공하면 배포가 허용된다는 것이 이 결정의 전제다.
- Phase 1은 원본 패키지의 `copyright`와 `/usr/share/lightlix/licenses/`에 전문을 보존한다. 콘솔 로그인 안내(`/etc/issue`)에 경로를 표시한다. 설치 화면은 Phase 4에서 구현한다.
- 바이너리를 `.run`에서 풀거나 수정하지 않는다. 저장소 `.deb`을 그대로 쓴다.

### 1.5 남은 해석 위험

- 1.1 (d)는 "OSI 승인 라이선스로 배포되는 운영체제 커널과 함께" 쓰는 경우를 말한다. Debian의 Linux 커널은 GPL-2.0이므로 조건에 해당한다고 읽힌다. 다만 이 해석은 조사자의 판단이다.

---

## 2. CUDA EULA

### 2.1 재배포 가능 파일

출처: https://docs.nvidia.com/cuda/eula/index.html (Last updated: January 26, 2026). 검증 기록 check 24.

- Attachment A의 Linux 행에 다음 파일이 재배포 가능 파일로 있다.
  - CUDA Runtime: `libcudart.so`, `libcudart_static.a`, `libcudadevrt.a`
  - CUDA BLAS Library: `libcublas.so`, `libcublasLt.so`, `libcublas_static.a`, `libcublasLt_static.a`
  - NVRTC: `libnvrtc.so`, `libnvrtc-builtins.so`(및 정적 파일)

### 2.2 재배포 조건 원문

출처: 같은 EULA 문서.

> 1.1.2: Your application must have material additional functionality, beyond the included portions of the SDK. The distributable portions of the SDK shall only be accessed by your application.

한국어 요약: 응용 프로그램은 SDK의 포함 부분 외에 실질적인 추가 기능을 가져야 한다. 재배포 가능한 SDK 부분은 그 응용 프로그램만 접근할 수 있어야 한다.

> 2.3: ... may be copied and redistributed ... provided that the object code files are not modified in any way

한국어 요약: 리눅스용 재배포 가능 파일은 객체 코드를 수정하지 않는 조건으로 복사하고 재배포할 수 있다.

추가 조건(조사 기록 기준, 검증자 재확인 없음): 재배포 약관이 EULA와 일관되어야 한다. SDK를 단독 제품으로 배포하거나 재라이선스할 수 없다.

### 2.3 ISO 포함 결정과 해석 위험

- 사용자 확정 U3에 따라 `cudart`와 `cublas`를 첫 부팅 설치가 아니라 ISO에 넣는다.
- **해석 위험**: "응용 프로그램이 실질적인 추가 기능을 가진다"와 "재배포 부분은 그 응용 프로그램만 접근한다"는 조건을 OS 이미지 번들링이 만족하는지 불명확하다. llama-server와 함께 들어가 있으면 응용이 접근하는 것으로 읽을 수 있다. 하지만 OS 이미지가 다른 프로그램에도 같은 라이브러리를 제공하면 이 조건에 어긋난다고 볼 수도 있다. 이 해석은 법률 자문을 받지 않았다.
- 조사 기록은 첫 부팅 설치 방식이 이 조건을 피한다고 적는다. 검증자도 같은 위험을 지적했다. 사용자는 이 위험을 알고 ISO 포함을 선택했다. 결정문에 해석 위험이 남았다는 사실을 계속 적어 둔다.
- **Phase 2 추가 사실**: 0.2.0부터 ISO에 CUDA 런타임을 쓰는 응용(llama-server)이 함께 들어간다. 이 항목은 사실만 적으며 법적 판단은 쓰지 않는다.
- 권장 조치: 배포 전에 법률 검토를 받거나 NVIDIA에 문의한다.

### 2.4 CUDA 버전과 라이선스

- CUDA 13.2와 13.4 사이의 라이선스 차이는 이번 조사에서 확인하지 않았다. 이 문서의 인용은 2026-01-26 개정 EULA 기준이다.
- EULA 문서의 개정일은 2026-01-26이다. 조사 시점(2026-10-10)과 차이가 있을 수 있다.

---

## 3. GeForce 소프트웨어의 데이터센터 배치 제한

1.2에서 인용한 2.8 조항이다. 이 프로젝트의 대상 GPU가 GeForce이므로 이 조항을 문서에 명시한다. 이 조항 때문에 프로젝트 자체의 사용 범위를 제한하지는 않는다. 사용자가 데이터센터에 배포할 때 조건을 확인해야 한다는 뜻이다.

---

## 4. llama.cpp

- 확인 날짜: 2026-10-11 (Phase 2)
- 소스: `ggml-org/llama.cpp` 태그 `v0.6.0`, 커밋 `d81235049384534c167caea52b85a694f6103d14`(`lightlix.conf`의 `LLAMA_CPP_COMMIT`).
- 라이선스: MIT. 원문은 같은 커밋의 `LICENSE` 파일이다. 파일 크기 1,078바이트, sha256 `94f29bbed6a22c35b992c5c6ebf0e7c92f13b836b90f36f461c9cf2f0f1d010d`(2026-10-11에 `https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/LICENSE`에서 받아 잰 값).
- 저작권 표시: `Copyright (c) 2023-2026 The ggml authors`.
- 패키지에서의 위치: `lightlix-llm`의 `/usr/share/doc/lightlix-llm/copyright`에 DEP-5 형식으로 원문을 그대로 넣었다(각 줄 앞에 공백 한 칸을 붙이고, 빈 줄은 ` .`으로 적는다. 이는 DEP-5 들여쓰기 규칙이다). 원문 파일은 `/usr/share/doc/lightlix-llm/LICENSE.llama.cpp`에도 있다.
- 같은 고정 커밋의 `vendor/CMakeLists.txt`와 서버·mtmd의 링크 구성을 확인했다. cpp-httplib(MIT), SHA-256(공개 도메인), xxHash(BSD-2-Clause), nlohmann JSON(MIT), miniaudio(공개 도메인 또는 MIT-0), stb_image(MIT 또는 공개 도메인), sheredom subprocess(공개 도메인)의 원문 고지를 `/usr/share/doc/lightlix-llm/vendor-licenses/`에 넣는다. JSON 헤더 안의 저작권 표시도 모두 보존한다. 빌드 스크립트가 같은 소스에서 추출하며 누락·빈 파일을 거부한다.
- 근거: [고정 소스의 vendor 구성](https://github.com/ggml-org/llama.cpp/blob/d81235049384534c167caea52b85a694f6103d14/vendor/CMakeLists.txt), 그 아래 각 LICENSE 파일과 `json.hpp`, `miniaudio.h`, `stb_image.h`, `subprocess.h`의 고지다.
- 웹 UI 자산: 사용자 확정 U6에 따라 내장 UI 없이 빌드한다(`LLAMA_BUILD_UI=OFF`, `LLAMA_USE_PREBUILT_UI=OFF`). 기본 설정에서 내려받는 `dist.tar.gz`는 이미지에 들어가지 않는다([decisions.md](decisions.md)의 D4-2).
- 빌드 도구(nvcc 등 CUDA 툴킷)는 최종 이미지에 들어가지 않는다. CUDA 런타임(`cudart`, `cublas`)만 `lightlix-nvidia`가 설치한다(2절 참고).
- 근거: https://github.com/ggml-org/llama.cpp/releases (v0.6.0 정식 릴리스), https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/LICENSE

---

## 5. Debian 상표 정책

출처: https://www.debian.org/trademark (정책 v2.0, 2013-01-19 게시).

**이 항목은 조사자 한 명의 결과다. 검증자는 이 정책을 재확인하지 않았다.**

- 허용되는 사용: 사실에 맞는 서술, 호환성 설명, 공정한 지칭(nominative fair use), 제품과 Debian의 관계를 오해 없이 설명하는 용도. 이 조사 결과에 따르면 "Debian 13 기반"이라는 사실 표기는 허락 없이 가능하다.
- 허락이 필요한 사용: Debian 상표를 회사나 제품 이름에 쓰는 것, Debian 상표와 혼동될 만한 이름, 도메인 이름에 쓰는 것, 후원이나 승인이 있는 것처럼 암시하는 것.
- 요구되는 표기(조사 결과): 프로젝트가 Debian의 일부가 아니고 Debian이 만들었거나 보증하지 않는다는 점을 분명히 한다. Debian 상표는 SPI(Software in the Public Interest, Inc.)가 소유한다는 점을 적는다. 웹사이트와 인쇄물에 제휴 부인 문구를 넣는다. 예시 문구는 "X PROJECT is not affiliated with Debian. Debian is a registered trademark owned by SPI"이다.
- 상표를 주변 단어와 구별한다(이탤릭, 볼드 등). 정확한 형태로 쓰고, 다른 단어와 붙여 쓰지 않는다.

이 프로젝트의 적용: README에 "Debian은 Software in the Public Interest, Inc.가 소유한 등록 상표다. Lightlix는 Debian과 제휴 관계가 없는 파생 배포판이며, Debian 프로젝트가 만들었거나 보증하지 않는다."라고 적는다. 상표 소유자와 제휴 부인 문구는 2026-10-11에 https://www.debian.org/trademark 원문("Debian is a registered trademark of Software in the Public Interest, Inc.")과 대조했다. 그 밖의 조건은 법률 검토 때 함께 확인한다.

---

## 6. 프로젝트 이름과 상표

- 프로젝트 이름은 Lightlix다.
- 조사 결과 GitHub 계정 `lightlix`는 이미 존재한다(HTTP 200). 이름 충돌 여부나 상표 등록 여부는 조사하지 않았다.
- 원래 사양이 쓰려던 조직 이름 후보는 GitHub에서 HTTP 404였다. 그러나 현재 저장소는 https://github.com/nxodh/lightlixos 로 확정되었으므로 이 조직 이름은 쓰지 않는다.

---

## 7. 모델 라이선스 표시 방침 (초안, 미확정)

- LLM 모델 파일의 라이선스는 모델마다 다르다. Lightlix는 모델 라이선스를 바꾸지 않는다.
- 초안: 사용자가 모델을 받을 때 그 모델의 라이선스 문서를 CLI와 GUI에 보여 준다. 라이선스 원문과 링크를 함께 표시한다.
- 초안: 현재 계획에는 모델 파일을 ISO에 넣는 항목이 없다. 이 가정은 확정되지 않았다. 모델을 번들하면 이 절을 다시 쓴다.
- 이 방침은 Phase 2 이후에 확정한다. 구현하지 않는다.

---

## 8. 프로젝트 자체 라이선스 (미정)

- 프로젝트 자체 라이선스는 아직 정하지 않았다(사용자 확정 U7).
- 저장소에 `LICENSE` 파일을 만들지 않는다.
- 제3자 구성 요소의 라이선스는 위 절에 따라 그대로 유지된다.

---

## 9. Intel 무선·이더넷 펌웨어

- 확인 날짜: 2026-10-11
- 대상: `firmware-iwlwifi` 20250410-2, `firmware-intel-misc` 20250410-2 (trixie, non-free-firmware)
- 출처: 두 패키지의 `usr/share/doc/<패키지>/copyright`. Debian 저장소 동봉본을 `dpkg-deb -x`로 풀어 확인했다. 웹 원문은 따로 받지 않았다. 두 파일은 내용이 같다(`cmp`로 확인). 두 파일은 linux-firmware 저작권 문서 전체(6,755줄)다.
- 원문은 줄바꿈이 있으나 아래 인용에서는 이어 붙이고 목록 기호를 `*`로 표시했다.

> Redistribution and use in binary form, without modification, are permitted provided that the following conditions are met: * Redistributions must reproduce the above copyright notice and the following disclaimer in the documentation and/or other materials provided with the distribution. * Neither the name of Intel Corporation nor the names of its suppliers may be used to endorse or promote products derived from this software without specific prior written permission. * No reverse engineering, decompilation, or disassembly of this software is permitted.

### 9.1 Wi-Fi 펌웨어 (`iwlwifi-*`)

- 적용 조건: `Files: intel/ibt-* iwlwifi-*`, 라이선스 `binary-redist-Intel`. AX200용 `iwlwifi-cc-a0-*.ucode`도 이 항목에 속한다.
- 한국어 요약: 수정하지 않은 바이너리 형태로 재배포할 수 있다. 조건은 세 가지다. (1) 저작권 고지와 면책 문구를 배포물에 함께 넣는다. (2) Intel의 이름을 파생물의 보증이나 홍보에 쓰지 않는다. (3) 역공학, 디컴파일, 디스어셈블을 하지 않는다. 특허 라이선스는 원문상 "Intel이 소유하거나 통제하는 특허 중 이 소프트웨어를 단독으로, 또는 승인된 오픈소스 라이선스의 운영체제와 결합해 사용하는 데 필요한 범위"로 제한된다. 하드웨어 자체는 라이선스되지 않는다.
- 이 빌드의 상태: 빌드는 Debian 패키지를 그대로 설치한다. 저장소에는 펌웨어 파일을 두지 않았으므로 펌웨어를 수정하지 않는다.

### 9.2 이더넷 펌웨어 (`intel/ice/`)

- `intel/ice/ddp/ice-*.pkg`(`ice.pkg`의 대상)는 `binary-redist-Intel` 조건으로 9.1과 같다.
- `intel/ice/ddp-comms/ice_comms-*.pkg`와 `intel/ice/ddp-wireless_edge/ice_wireless_edge-*.pkg`는 `binary-redist-Intel-Ice-enhanced` 조건이다. 원문은 9.1과 같은 조항에 다음 한정이 붙어 있다: "Redistribution and use in binary form, without modification, are permitted **solely operating in conjunction with Intel authorized products**, provided that the following conditions are met". 한국어로는 "Intel 승인 제품과 함께 동작하는 경우에만" 재배포가 허용된다는 뜻이다. 이 한정이 Lightlix 이미지에 어떻게 적용되는지는 해석이 필요하며, 이번에 판단하지 않았다.
- `intel/ice/ddp-lag/ice_lag-1.3.1.0.pkg`는 `firmware-intel-misc`에 들어 있지만, copyright 파일에 이 경로를 다루는 항목이 없다. 이 파일의 라이선스 조건은 **미확인**이다.

### 9.3 범위와 미확인 항목

- 이 절은 Lightlix가 쓰는 Wi-Fi 펌웨어와 이더넷 펌웨어 중 위 파일만 다룬다. `firmware-intel-misc`에는 `e100/`, `hfi1_*`, `qat_*`, `intel/ish/`, `intel/vpu/` 파일도 들어 있다. `e100/*`는 `BSD-3-clause-Intel`, `hfi1_*`는 `binary-redist-Intel-narrower-patent`, `qat_*.bin`은 `binary-redist-Intel-1`로 적혀 있다. `intel/vpu/*`는 `binary-redist-Intel`, `intel/ish/*`의 조건 이름은 이번에 확인하지 않았다. 이 파일들의 원문은 읽지 않았다(미확인).
- 이 절은 법률 자문이 아니다. "승인 제품"과 "수정"의 범위처럼 해석이 필요한 부분은 법률 검토나 Intel 문의로 확인해야 한다.
