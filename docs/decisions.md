# Opereta 기술 결정 기록

- 확인 날짜: 2026-10-10
- 상태: 초안

이 문서는 Opereta의 기술 결정을 번호별로 모은다. 각 항목은 세 부분으로 나뉜다.

- **원래 결정**: 최초 사양에 적힌 결정(검증 기록에 적힌 요약 기준).
- **2026-10-10 변경**: 검증자가 제안하고 사용자가 수용한 변경. 사용자가 따로 확정한 내용이 검증자 제안과 다르면 사용자 결정을 따른다.
- **근거**: 출처 URL. 조사와 검증은 1차 출처(NVIDIA 저장소, Debian 아카이브, 업스트림 소스)를 직접 조회했다. 다만 빌드와 부팅은 macOS 환경에서 하지 못해 실기 동작은 확인하지 않았다. 자세한 판정은 [verification-2026-10-10.md](verification-2026-10-10.md)를 본다.

심각도 표기:

- `must_change`: 원래 결정대로 구현하면 동작하지 않는다.
- `should_change`: 바꾸는 편이 낫다고 검증자가 판단한 항목.
- `clarify`: 결정 방향은 유지하되 구체적인 기준이 필요한 항목.
- `사용자 확정`: 사용자가 직접 정한 항목.

원래 사양의 조직 이름은 이 저장소 결정으로 대체되었다. 현재 저장소는 https://github.com/nxodh/operetaos 이다.

### Phase 2 검증 상태 메모 (2026-10-11)

이 문서는 기술 결정을 기록하며, 아래 D4의 CI 링크는 해당 패키지 검사 결과의 출처다. [CI 38131367776](https://github.com/nxodh/operetaos/actions/runs/38131367776)의 CUDA 패키지 빌드와 CPU 컨테이너 추론은 실제 NVIDIA 오프로드를 입증하지 않는다. 후속 ISO E2E는 38138886386에서 잘못된 context assertion, 38140520091에서 CPU MainPID 런타임 증거 확인으로 끝났고, [CI 38142350756](https://github.com/nxodh/operetaos/actions/runs/38142350756)(`035dfb4`)에서 live CPU E2E가 전체 통과했다. 이 통과도 GPU 없는 VM의 CPU 경로이며 실제 GPU 오프로드는 확인되지 않았다. 실행별 구분과 남은 검증은 [Phase 2 검증 기록](verification-phase-2.md)에 있다.

---

## U. 사용자가 확정한 결정

| 번호 | 결정 | 근거 |
|---|---|---|
| U1 | 저장소는 https://github.com/nxodh/operetaos 로 한다. | 사용자 확정. 원래 사양의 조직 이름을 대체한다. |
| U2 | 검증자가 제안한 decision_changes를 모두 수용한다. 단 U3과 충돌하는 부분은 U3을 따른다. | 사용자 확정. 항목별 내용은 D1~D9에 적는다. |
| U3 | CUDA 런타임(`cudart`, `cublas`)은 첫 부팅 때 설치하지 않고 ISO에 포함한다. | 사용자 확정. 검증자는 CUDA 런타임만 첫 부팅 설치를 제안했으나 사용자가 ISO 포함으로 정했다. CUDA EULA 1.1.2 조건을 어떻게 읽을지에 대한 위험이 남는다(아래 D8과 [licenses.md](licenses.md) 참고). 법률 자문을 받은 결과가 아니다. |
| U4 | NVIDIA 드라이버도 ISO에 포함한다. 바이너리는 수정하지 않고 라이선스 전문을 함께 제공한다. | 사용자 확정. 근거는 D8 참고. |
| U5 | 버전은 다음과 같이 고정한다. CUDA 13.2.2(드라이버 브랜치 595에 대응), llama.cpp v0.6.0, go-nvml v0.13.4-1, NVIDIA 드라이버 595.91.07(저장소의 595 최신). | 사용자 확정. 근거는 D3, D4, D5 참고. |
| U6 | llama.cpp는 내장 웹 UI 없이 빌드한다. | 사용자 확정. 웹 UI 자산은 빌드 중 외부에서 내려받는 기본 동작이 있다(D4 참고). |
| U7 | 프로젝트 자체 라이선스는 아직 정하지 않는다. LICENSE 파일은 만들지 않는다. | 사용자 확정. [licenses.md](licenses.md) 참고. |
| U8 | **(U13에서 철회됨)** 램 목표를 완화한다(2026-10-12). 헤드리스 서버 모드 유휴 램은 300 MB에서 600 MB로(처음 400 MB, 이어서 200 MB 더), 데스크톱 모드는 700 MB에서 900 MB로 바꾼다. | 사용자 확정. 목표와 실측은 MiB 기준으로 비교한다. Phase 3의 QEMU 참고 측정은 서버 약 374~384 MiB, 데스크톱 약 618~621 MiB(창을 연 채)로 목표 안이다. 실기와 장시간 값은 Phase 5에서 다시 잰다([memory.md](memory.md)). |
| U9 | Phase 4 는 사용자가 "Phase 4 를 새 브랜치에서 시작"하라고 지시했고(2026-10-12), 설계서의 기본값으로 진행한다(Q1~Q6 은 사용자 결정 대기). | 사용자 지시. 근거와 기본값은 [phase-4-design.md](phase-4-design.md) 3절과 9절. |
| U10 | 업데이트는 사용자가 시작할 때만 한다. 새 커널은 `grub-reboot` 로 한 번만 부팅해 보고, 부팅 뒤 확인이 통과해야 기본 커널을 바꾼다. 드라이버는 `opereta-nvidia` 패키지를 통해서만 올라가며 같은 브랜치(595) 안에서만 바뀐다. | 설계 기본값. SPEC 의 "성공했을 때만 기본 부팅 커널을 바꾸고 실패하면 이전 커널을 유지한다"를 부팅 뒤 확인까지 확장했다. [updates.md](updates.md). |
| U11 | 설치는 GPT(ESP 1 GiB FAT32 + ext4 루트)로 디스크 전체를 쓴다. 암호화, LVM, swap 파티션, btrfs 스냅샷은 이번 단계에서 지원하지 않는다. 설치된 시스템에서 Calamares 는 지운다. | 설계 기본값(Q3, Q4). [installation.md](installation.md). |
| U12 | 자체 APT 저장소는 `apt-ftparchive` 와 `gpg` 로 만들고, 호스팅 주소와 서명 키는 사용자가 정한다. 개인 키는 저장소에 두지 않는다. | 설계 기본값(Q1, Q2). [apt-repository.md](apt-repository.md). |
| U13 | 램 목표(U8의 서버 600 MB, 데스크톱 900 MB)를 철회한다(2026-10-12). 먼저 잘 동작하게 만들고, 그다음 검토해서 최대한 경량화한다. | 사용자 확정. 0.5.0의 합격 조건에 램 수치를 쓰지 않는다. 누수 검사(소크 시험)는 유지한다. 수치 목표는 경량화 단계에서 실측으로 새로 정한다. [memory.md](memory.md). |
| U14 | 0.5.0은 데스크톱 환경을 Xfce로 바꾸고 Opereta 로고와 통일된 디자인을 적용한다. 일반 배포판에 가까운 사용성을 주되 무거운 기능은 제외한다. SPEC의 "labwc + greetd" 결정을 대체한다. | 사용자 지시(2026-10-12). 설계 기본값과 질문은 [phase-5-design.md](phase-5-design.md). |
| U15 | 0.5.1 에서 OS 이름과 로고를 바꿨다. 이전 이름의 흔적을 두지 않는다(호환 별칭, 이전 경로, 이전 이름 `Replaces`/`Conflicts` 없음). GitHub 저장소 이름도 바꾸고 git 이력은 다시 쓰지 않는다. 0.6.0(U16)에서 같은 규칙으로 한 번 더 바뀌었다. | 사용자 지시(2026-10-13). [phase-5-1-design.md](phase-5-1-design.md). 이전 이름의 설치본은 새 이미지로 새로 설치한다. |
| U16 | 0.6.0 에서 OS 이름을 **Opereta** 로 한 번 더 바꾸고 새 로고와 8px 모서리 디자인을 쓴다(U15 와 같은 규칙: 이전 이름의 흔적 없음, 이력은 다시 쓰지 않음). 저장소 이름은 `nxodh/operetaos`(기본안). Vedecaps 의 Deberopa 로의 이름 변경은 Deberopa 릴리스가 생길 때까지 하지 않는다. 0.6.0 부터 태그를 게시하고 GitHub 릴리스를 만들며, 릴리스 자산을 OS 업데이트 저장소(평평한 APT 형식)로 쓴다. **ISO 는 올리지 않는다**: 사용자가 "법적 문제나 개인 정보 노출 위험이 있으면 하지 말라"고 했고, 조사 결과 재배포 조건 해석, GPL 소스 제공, 프로젝트 라이선스가 정리되지 않았다(licenses.md 10절). | 사용자 지시(2026-10-14). [phase-6-design.md](phase-6-design.md), [releases.md](releases.md). 저장소 이름은 사용자가 기본안(`operetaos`)으로 확정했다(2026-10-14). Deberopa 보류는 기본안이다. ISO 공개 배포(M6)와 프로젝트 라이선스(U7)는 여전히 정해지지 않았고 ISO 는 올리지 않는다. |

---

## D1. 베이스 배포판과 커널

- **원래 결정**: Debian 13(trixie) 기반. 커널은 `linux-image-amd64`(6.12). backports 커널은 쓰지 않는다.
- **2026-10-10 변경**: 결정은 유지한다(`clarify`). 다음 세 가지를 구체화한다.
  - 라이브 이미지와 설치된 시스템 모두에 `linux-headers-amd64`를 넣는다. live-build에서는 `--linux-packages "linux-image linux-headers"` 설정으로 지정한다.
  - 빌드에 `trixie-security`를 포함한다. main의 커널은 6.12.107-1이고, security 저장소는 6.12.111-1이다. 보안 업데이트가 올라오면 커널 ABI 이름이 바뀌므로 DKMS 모듈을 다시 빌드해야 한다. 재빌드가 실패하면 이전 커널로 부팅하는 복구 경로가 필요하다.
  - backports 커널 7.2.6-1~bpo13+1은 쓰지 않는다. NVIDIA 595.99.02 릴리스 노트에 Linux 7.0 이후 관련 수정이 있으므로, 595.91.07과 backports 커널을 조합하는 것은 피하는 것이 맞다는 검증자 판단을 따른다.
- **근거**:
  - trixie main 메타패키지: 6.12.107-1 — https://api.ftp-master.debian.org/madison?package=linux-image-amd64&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on
  - trixie-security: linux-image-amd64 6.12.111-1 — https://security.debian.org/debian-security/dists/trixie-security/main/binary-amd64/Packages.xz
  - 커널 6.12.111 헤더 설정(`CONFIG_DRM_SIMPLEDRM is not set` 등) — https://security.debian.org/debian-security/pool/updates/main/l/linux/linux-headers-6.12.111+deb13-amd64_6.12.111-1_amd64.deb
  - live-build 옵션 `--linux-packages`, `--archive-areas`, `--security` — https://deb.debian.org/debian/pool/main/l/live-build/live-build_20250505+deb13u1_all.deb
  - NVIDIA 595.91.07 README의 최소 커널은 4.15 — https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kernel_open.html

---

## D2. 설치기

- **원래 결정**: 설치기는 Calamares를 쓴다.
- **2026-10-10 변경**: Calamares 자체는 유지한다(`should_change`). 다만 Debian 기본 설정 패키지(`calamares-settings-debian`)를 그대로 쓰지 않고 Opereta 전용 설정 패키지를 만든다.
  - `displaymanager` 모듈 목록에는 greetd를 넣지 않는다. Calamares 3.3.14의 greetd 처리 코드는 그리터 사용자를 `greeter`로 가정하고 설정 파일을 다시 쓴다. 그러나 Debian의 greetd 기본 사용자는 `_greetd`이므로, 그대로 쓰면 설치된 시스템의 그리터가 깨진다. 대신 완성된 `/etc/greetd/config.toml`을 squashfs에 넣는다.
  - 라이브 전용 자동 로그인 설정은 `shellprocess` 단계에서 지운다.
  - 오프라인 설치를 위해 부트로더 패키지(`grub-efi`, `grub-efi-amd64-signed`, `shim-signed`)를 squashfs에 미리 넣는다. 기본 설정은 타깃에서 `apt-get -y install grub-efi`를 실행한다. 세 패키지의 존재와 버전은 이번 조사에서 확인하지 않았다.
  - 런처가 `xhost`와 `pkexec`를 쓰므로 `x11-xserver-utils`, XWayland, polkit 규칙을 라이브 세션에 넣는다.
  - 브랜딩에 Debian 로고를 쓰지 않는다.
- **근거**:
  - Calamares 3.3.14 displaymanager 모듈: https://raw.githubusercontent.com/calamares/calamares/v3.3.14/src/modules/displaymanager/main.py
  - Debian 기본 설정 패키지 `calamares-settings-debian` 13.0.13-1: displaymanager 목록은 slim, sddm, lightdm, gdm, mdm, lxdm, kdm이고 greetd가 없다. 런처는 `xhost +si:localuser:root; pkexec calamares`를 실행한다. 설치 후 `apt-get -y install grub-efi`를 실행하며 브랜딩은 debian이다(검증 기록 check 30).
  - Calamares 패키지 버전 3.3.14-1, trixie main — https://api.ftp-master.debian.org/madison?package=calamares&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on

---

## D3. NVIDIA 드라이버

원래 결정은 "오픈 커널 모듈만 사용, 패키지 구성과 강제 방법은 미정"이다. 아래 세부 항목은 [drivers.md](drivers.md)에 정리한다.

### D3-1. 패키지 구성 (`should_change`)

- **2026-10-10 변경**: 설치 조합은 `nvidia-kernel-open-dkms` + `nvidia-driver` + `nvidia-driver-cuda`로 하고, 세 패키지를 한 번의 apt 트랜잭션으로 설치한다. `nvidia-open` 메타패키지는 `nvidia-settings`와 `nvidia-xconfig`까지 끌어오므로 쓰지 않는다. `cuda-drivers`도 쓰지 않는다.
- **근거**: NVIDIA 저장소 Packages 인덱스 — https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz . 
  - `nvidia-open` 595.91.07-1 Depends: `nvidia-kernel-open-dkms`, `nvidia-driver`, `nvidia-driver-cuda`, `nvidia-settings`, `nvidia-xconfig`.
  - NVIDIA 설치 가이드: https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/latest/debian.html

### D3-2. 독점 모듈 차단 (`should_change`)

- **원래 결정**: 오픈 커널 모듈만 쓴다. 강제 방법은 정하지 않았다.
- **조사자 주장**: 독점 모듈은 Pin-Priority -1로 막으면 된다.
- **2026-10-10 변경**: 차단 핀은 `nvidia-driver-pin`보다 먼저 읽히는 파일에 둔다. apt는 버전마다 먼저 읽힌 핀 하나만 적용하므로, 뒤에 읽히는 파일은 595.* 버전에 효과가 없다. 차단 대상은 `nvidia-kernel-dkms`, `nvidia-kernel-source`, `cuda-drivers*`이다.
- **Debian 550.x 패키지**: 조사자 주장과 달리 `595*` 조건에 걸리는 것이 아니라 기본 우선순위 500으로 남아, 1000 우선순위의 595 패키지에 밀린다. 검증 기록 check 5의 정정 내용이다.
- **근거**: apt 3.0.3 `apt-pkg/policy.cc`의 `CreatePin` — https://salsa.debian.org/apt-team/apt/-/raw/3.0.3/apt-pkg/policy.cc . 저장소 `nvidia-driver-pinning-595` 595-1의 `nvidia-driver-pin` 파일 — https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-pinning-595_595-1_all.deb

### D3-3. dkms 출처 핀 (`should_change`, 제안)

- **원래 결정**: 없음.
- **2026-10-10 변경**: NVIDIA 저장소의 `dkms`는 3.4.3-1이고 Debian main의 `dkms`는 3.2.2-1~deb13u1이다. 핀이 없으면 NVIDIA 쪽 `dkms`가 선택된다. 검증자는 Debian 쪽을 고정하는 핀을 제안했다. `Pin: release o=Debian`과 `Pin-Priority: 1001`은 검증자의 제안이며, 실제 Origin 문자열은 Phase 1에서 `apt-cache policy`로 확인해야 한다.
- **근거**: NVIDIA 저장소 `dkms`와 `dh-dkms`(3.4.3-1) 및 Debian main(3.2.2-1~deb13u1) — https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz , https://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.xz

### D3-4. Debian archive area (`should_change`)

- **2026-10-10 변경**: Debian 쪽은 `main`과 `non-free-firmware`만 켠다. `contrib`와 `non-free`는 켜지 않는다. 가이드가 `add-apt-repository contrib`를 요구하지만, 595.91.07의 의존성 폐쇄에서 저장소 밖 이름 38개는 모두 trixie `main`에 있다. `contrib`를 켜면 550 계열 동명 패키지가 섞인다.
- **근거**: 검증 기록 check 10, https://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.xz . `firmware-nvidia-gsp`는 non-free-firmware 쪽 550.163.01-2가 있지만, NVIDIA 쪽 `firmware-nvidia-gsp (= 595.91.07-1)`을 요구하므로 NVIDIA 저장소 패키지를 써야 한다(https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz).

### D3-5. 브랜치 정의 (`clarify`)

- **원래 결정**: Production Branch 고정.
- **2026-10-10 변경**: "Production Branch 고정"은 다음과 같이 정의한다. 브랜치 번호는 nvidia.com이 표시하는 Production 브랜치 번호(현재 595)를 쓰고, 실제 버전은 NVIDIA apt 저장소가 그 브랜치에서 제공하는 최신(현재 595.91.07-1)이다. nvidia.com의 Production 최신 595.104.02(2026-09-22)는 저장소에 올라와 있지 않다(deb 파일 404). `.run` 설치기로 따로 넣지 않는다.
- **근거**: https://www.nvidia.com/en-us/drivers/unix/ , https://download.nvidia.com/XFree86/Linux-x86_64/ , https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz

---

## D4. CUDA와 llama.cpp

### D4-1. CUDA 버전 (`should_change`, 사용자 확정으로 13.2.2)

- **원래 결정**: "CUDA Toolkit 12.8 이상 중 최신 안정판".
- **2026-10-10 변경**: 드라이버 브랜치에 맞춘 CUDA 마이너 버전을 쓴다. 사용자 확정(U5)에 따라 CUDA 13.2.2를 쓴다.
  - 빌드 패키지(검증 기록 기준): `cuda-minimal-build-13-2`, `libcublas-dev-13-2`, `cuda-driver-dev-13-2`(13.2.2).
  - 런타임 패키지(검증 기록 기준): `cuda-cudart-13-2` 13.2.86-1, `libcublas-13-2` 13.4.1.3-1. 내려받기 약 358 MiB, 설치 후 538 MiB.
- **이유**: CUDA 릴리스 노트 Table 2는 CUDA 13.2를 R595에, CUDA 13.4를 R615에 대응시킨다. 13.4.2를 쓰면 595 드라이버에서는 제한 기능 모드로 돈다(`cudaErrorCallRequiresNewerDriver`가 날 수 있고, PTX JIT를 쓸 수 없으며, 아키텍처를 명시해야 한다). 저장소에서도 `cuda-compat-13-2`는 595.45.04~595.91.07이고, `cuda-compat-13-4`는 615.71.09다.
- **13.4.2를 쓰려면**: 드라이버 580 이상에 의존하는 마이너 호환에 기대는 것이므로, 결정문에 그 제약을 적고 595 실기 테스트를 통과 조건으로 두어야 한다.
- **저장소 범위**: debian13 저장소에는 `cuda-toolkit-13-1`부터 `13-4`까지만 있다. 12.x 툴킷은 없다. 이것이 원래 결정의 "12.8 이상" 문구를 고친 이유다.
- **근거**:
  - https://docs.nvidia.com/cuda/cuda-toolkit-release-notes/index.html (Table 2, Table 3)
  - https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz
  - 최신 안정판 13.4.2(2026년 9월) — https://developer.nvidia.com/cuda-toolkit-archive
  - 런타임 패키지 `libcublas-13-4` 13.8.0.4-1 md5 목록 — https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/libcublas-13-4_13.8.0.4-1_amd64.deb

### D4-2. llama.cpp 빌드 옵션 (`must_change`)

- **원래 결정**: `GGML_CUDA=ON`, `CMAKE_CUDA_ARCHITECTURES`를 `86;89;120`으로 준다.
- **2026-10-10 변경**: 옵션 이름은 맞지만 그대로는 부족하다. 확정 옵션은 다음과 같다.

```
-DGGML_CUDA=ON
-DGGML_NATIVE=OFF
-DGGML_BACKEND_DL=ON
-DGGML_CPU_ALL_VARIANTS=ON
-DCMAKE_CUDA_ARCHITECTURES="86-real;89-real;120a-real"
-DLLAMA_BUILD_TESTS=OFF
-DLLAMA_BUILD_EXAMPLES=OFF
-DLLAMA_BUILD_APP=OFF
-DLLAMA_BUILD_UI=OFF
-DLLAMA_USE_PREBUILT_UI=OFF
-DCMAKE_BUILD_WITH_INSTALL_RPATH=ON
-DCMAKE_INSTALL_RPATH='$ORIGIN'
```

- **2026-10-11 Phase 2 반영**: 위 목록은 `llama/build.sh`가 실제로 쓰는 옵션과 같다. 추가된 네 줄의 이유는 다음과 같다.
  - `LLAMA_BUILD_EXAMPLES=OFF`, `LLAMA_BUILD_APP=OFF`: `llama-server`만 필요하다. `LLAMA_BUILD_APP`은 v0.6.0에서 새로 생긴 옵션이고 기본값이 ON이며, 통합 `llama` 바이너리와 그 의존 라이브러리를 만든다. 실제 빌드(에뮬레이션 amd64)에서 `--target llama-server`만 빌드하고 `build/bin`을 복사하는 방식으로 성공했다.
  - `CMAKE_BUILD_WITH_INSTALL_RPATH=ON`, `CMAKE_INSTALL_RPATH='$ORIGIN'`: 기본 RUNPATH는 빌드 디렉터리 절대 경로다. 이 두 옵션으로 모든 `.so`와 `llama-server`의 RUNPATH가 `$ORIGIN`이 되어, 실행 파일과 라이브러리를 한 디렉터리에 두면 어디서든 실행된다(조사 기록 A2). 빌드 중 `llama/build.sh`가 이를 `readelf`로 검사한다.
  - `LLAMA_CUDA=OFF` 스모크 빌드에서는 `GGML_CUDA`와 `CMAKE_CUDA_ARCHITECTURES`를 뺀다.

- **이유**:
  - `GGML_NATIVE`는 v0.6.0에서 기본값이 ON이다. 그러면 `-march=native`가 붙어 빌드 머신 CPU 전용 바이너리가 된다. 반대로 `SOURCE_DATE_EPOCH` 환경 변수가 있으면 기본값이 OFF로 바뀌고 SSE4.2, AVX, AVX2, FMA, F16C가 모두 꺼진다. live-build는 chroot 명령마다 `SOURCE_DATE_EPOCH`를 넘기므로 이 경우가 실제로 생긴다. `GGML_NATIVE=OFF`로 고정한다.
  - `GGML_BACKEND_DL=ON`과 `GGML_CPU_ALL_VARIANTS=ON`은 CPU 변형을 여러 개 만들고 실행 시 고르게 한다. 업스트림 배포용 Dockerfile도 같은 조합을 쓴다.
  - CMake는 `120`을 `120a`로 바꾸고 접미사 없는 값에는 PTX까지 넣는다. 그래서 `120a-real`로 적는다. trixie의 CMake 3.31.6은 `120a-real`을 받는다.
  - 웹 UI(`LLAMA_BUILD_UI`, `LLAMA_USE_PREBUILT_UI`)는 기본 설정에서 빌드 중 Hugging Face에서 `dist.tar.gz`를 내려받는다. 그래서 네트워크 없이 빌드되지 않는다. 사용자 확정(U6)에 따라 UI를 끈다.
- **근거**:
  - https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/ggml/CMakeLists.txt (`GGML_NATIVE_DEFAULT`, `INS_ENB`, `120` → `120a` 변환). `GGML_NATIVE`가 켜졌을 때 `ggml-cpu`가 `-march=native`를 붙이는 동작은 검증 기록 check 22가 같은 파일과 함께 확인했다.
  - https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/scripts/ui-assets.cmake (`LLAMA_USE_PREBUILT_UI` 기본 ON, Hugging Face Bucket 다운로드)
  - https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/CMakeLists.txt (`LLAMA_BUILD_UI` 기본 OFF)

### D4-3. llama.cpp 소스 버전 (`must_change`의 일부, 사용자 확정)

- **2026-10-10 확인**: GitHub의 정식 최신 릴리스는 v0.6.0(2026-10-05)이다. 매 빌드는 `bNNNNN` 태그로 올라오는 prerelease다. 검증 시점의 v0.6.0 빌드 번호는 b11429다.
- **결정**: `v0.6.0`을 쓴다.
- **2026-10-11 Phase 2 반영**: 커밋 해시를 `opereta.conf`의 `LLAMA_CPP_COMMIT="d81235049384534c167caea52b85a694f6103d14"`로 고정했다. 빌드는 태그 이름이 아니라 이 커밋을 `git fetch --depth 1`로 받고, 받은 커밋이 같은지 확인한다. 자동 생성 tarball은 해시가 고정되지 않으므로 쓰지 않는다. 확인: `gh api repos/ggml-org/llama.cpp/git/ref/tags/v0.6.0`(태그 객체 `69ecae92…`)과 `git/tags/69ecae92…`의 역참조.
- **근거**: https://api.github.com/repos/ggml-org/llama.cpp/releases/latest , https://github.com/ggml-org/llama.cpp/releases

### D4-4. 런타임 라이브러리와 재배포 (`사용자 확정`)

- llama.cpp CUDA 백엔드는 `CUDA::cudart`, `CUDA::cublas`, `CUDA::cuda_driver`에 링크한다(`GGML_CUDA_NO_VMM`이 아닐 때 `cuda_driver`가 추가된다). 역사적 패키지 근거인 [Phase 2 CI 38131367776](https://github.com/nxodh/operetaos/actions/runs/38131367776)에서 amd64 CUDA 패키지 빌드와 `NEEDED`·`ldd` 검사가 통과했다. 검사는 `llama/build.sh`와 `tests/llama/package.sh`에 있다. 이 결과는 이후 live ISO E2E 실패를 없애거나 실 GPU 오프로드를 증명하지 않는다.
- ISO에 `cudart`와 `cublas`를 넣는 것은 사용자 확정 U3이다. 재배포 근거와 남은 해석 위험은 [licenses.md](licenses.md)에 적는다.
- **근거**: https://raw.githubusercontent.com/ggml-org/llama.cpp/v0.6.0/ggml/src/ggml-cuda/CMakeLists.txt , https://docs.nvidia.com/cuda/eula/index.html

### D4-5. 빌드 한 벌과 CPU 폴백 (2026-10-11, Phase 2)

- **결정**: 별도 CPU 전용 빌드를 만들지 않는다. CUDA를 포함한 빌드 한 벌이 `llama-server`, `libggml-cpu-*.so`(CPU 변형 여러 개), `libggml-cuda.so`를 함께 만든다. CUDA 백엔드는 실행 중에 `dlopen`되므로, CUDA 라이브러리나 GPU가 없으면 로드에 실패하고 CPU 백엔드로 돈다.
- **근거**: 조사 기록 `docs/verification-phase-2-research.md`의 "설계에 영향을 주는 발견" 1번. 소스에서 `GGML_BACKEND_DL=ON`이면 CUDA 백엔드가 `MODULE`이고 실행 파일에 링크되지 않음을 확인했다(`ggml/src/CMakeLists.txt:381-385`). `dlopen`이 실패하면 Release 빌드는 조용히 넘어간다(`ggml-backend-reg.cpp:221-228, 578-599`). CUDA 없는 환경에서 가짜 `libggml-cuda.so`를 로드해 `--list-devices`와 `/v1/chat/completions`가 정상임을 직접 확인했다.
- **실제 확인**: 로컬에서는 CPU 전용 스모크 빌드만 실행했다. 역사적 패키지 검사인 [CI 38131367776](https://github.com/nxodh/operetaos/actions/runs/38131367776)의 `make test-llama-docker`는 nvcc로 만든 CUDA 모듈을 가진 `.deb`에서 `libcudart.so.13 => not found`와 실제 CPU 추론을 확인해 통과했다. 이는 CUDA 없는 컨테이너 결과다. live ISO의 CPU E2E는 CI 38142350756에서 통과했지만, GPU가 있는 기기의 CUDA 오프로드는 확인되지 않았다.

---

## D5. operetad 데몬

- **원래 결정**: Go 단일 바이너리, NVIDIA `go-nvml` 사용.
- **2026-10-10 변경**: `clarify`. "단일 바이너리"의 뜻을 다음과 같이 좁힌다.
  - 실행 파일은 하나지만 완전 정적 바이너리가 아니다. `CGO_ENABLED=1`로 빌드한 glibc 동적 링크 파일이다.
  - 실행 시 `libnvidia-ml.so.1`을 `dlopen`으로 연다. 이 라이브러리가 없으면 `nvml.Init()`이 오류를 낸다. 드라이버가 없거나 아직 로드되지 않은 부팅에서도 데몬은 떠야 하므로, 이 오류를 정상 상태로 처리한다.
  - `go-nvml`은 v0.13.4-1로 고정한다(사용자 확정 U5). Go는 trixie의 `golang-go` 2:1.24~2로 빌드한다. `go.mod`의 `go` 지시어는 1.20이다.
- **근거**: https://raw.githubusercontent.com/NVIDIA/go-nvml/v0.13.4-1/pkg/nvml/nvml.go (`#cgo linux LDFLAGS: -Wl,--export-dynamic -Wl,--unresolved-symbols=ignore-in-object-files`, `defaultNvmlLibraryName = "libnvidia-ml.so.1"`). README는 라이브러리가 없으면 런타임 오류가 난다고 적는다. Go 패키지 버전 — https://api.ftp-master.debian.org/madison?package=golang-go&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on

---

## D6. GUI

- **원래 결정**: labwc + greetd + GTK4/libadwaita Python 앱.
- **2026-10-10 변경**: 결정은 유지한다(`clarify`). 다음을 구체화한다.
  - trixie의 labwc는 0.8.3-1(wlroots 0.18 기반)이다. labwc 0.9.0(2025-07-11)에서 `drm-syncobj` 프로토콜(explicit sync)이 추가되었는데, 0.8.3 소스에는 관련 코드가 없다. NVIDIA 드라이버의 Wayland 경로는 explicit sync를 쓰므로, RTX 30/40/50에서 깜빡임, 프레임 순서, XWayland 창(Calamares) 표시를 실기로 시험한다. 이것을 수용 기준에 넣는다.
  - 문제가 나오면 labwc 0.9 이상을 자체 백포트하는 대안을 쓴다. 이때 필요한 wlroots 버전은 이번 조사에서 확인하지 않았다.
  - 그리터 greetd는 trixie main의 0.10.3-4다(trixie-backports에 0.10.3-7~bpo13+1). 기본 설정의 사용자는 `_greetd`이고, 그래픽 그리터는 `video` 그룹이 필요할 수 있다는 주석이 있다. 그리터용 컴포지터 설정과 장치 권한을 명시한다.
  - 패널은 waybar 0.12.0-1을 쓴다. sfwbar는 trixie에 없다. (Phase 3에서 yambar 1.11.0-1로 바꿨다. 아래 phase-3-design D3 참고)
  - 그리터 프로그램은 후보만 확인했다(`gtkgreet` 0.8-1, `tuigreet` 0.9.1-5, `nwg-hello` 0.3.0-1, `wlgreet` 0.5.0-3). 선택은 Phase 3에서 한다.
  - GTK4 4.18.6+ds-2, libadwaita(`gir1.2-adw-1`) 1.7.6-1~deb13u1, `python3-gi` 3.50.0-4+b1이 trixie main에 있다.
- **근거**:
  - https://api.ftp-master.debian.org/madison?package=labwc&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on (labwc 0.8.3-1)
  - https://api.ftp-master.debian.org/madison?package=greetd&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on (greetd 0.10.3-4)
  - https://raw.githubusercontent.com/labwc/labwc/0.20.2/NEWS.md (0.9.0에서 `drm-syncobj` 추가)
  - NVIDIA 드라이버 README의 Wayland 알려진 문제 항목 "Explicit Sync는 DRM Syncobj 타임라인 동기화 지점을 쓴다"는 설명(검증 기록 check 31이 인용, URL은 검증 기록에 없음)
  - https://packages.debian.org/trixie/sfwbar (trixie에서 "Package not available in this suite")
  - https://api.ftp-master.debian.org/madison?package=gir1.2-adw-1&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on

---

## D7. `nvidia-drm.modeset=1` 커널 파라미터

- **원래 결정**: 커널 명령줄에 `nvidia-drm.modeset=1`을 추가한다.
- **2026-10-10 변경**: 삭제한다(`should_change`). 595.45.04부터 모든 595 릴리스의 README는 DRM KMS 지원이 기본으로 켜져 있다고 적는다. 저장소 595.91.07-1 소스에서도 `nv_drm_modeset_module_param = true`이고 `nv_drm_fbdev_module_param = true`다. 패키지의 modprobe 설정에도 이 값을 바꾸는 옵션이 없다.
- **남는 문제**: `nvidia-drm`은 udev가 자동으로 올린다. 그러나 trixie 커널에는 `simpledrm`이 없으므로, 컴포지터가 `nvidia-drm`보다 먼저 뜨면 DRM 장치가 없다. 그래서 greetd가 뜨기 전에 `/dev/dri/card*`가 있는지를 테스트 항목으로 두고, 필요하면 `modules-load.d` 등으로 조기 로드를 건다.
- **다시 넣는 조건**: 드라이버 브랜치를 590 이하로 내리면 다시 검토한다. 590.44.01 README는 KMS를 기본 비활성으로 적는다.
- **근거**: https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kms.html ; 저장소 소스 — https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz (`nvidia-open` / `nvidia-kernel-open-dkms` 595.91.07-1).

---

## D8. NVIDIA 구성 요소를 ISO에 포함

- **원래 결정**: 재배포가 불확실한 NVIDIA 구성 요소는 ISO에 넣지 않고 첫 부팅 때 설치한다.
- **2026-10-10 변경**: `must_change`. 이 원래 결정은 그대로 구현하면 RTX 50에서 화면이 나오지 않는다.
  - 근거 1: trixie 6.12.111 커널 설정에 `# CONFIG_DRM_SIMPLEDRM is not set`, `# CONFIG_SYSFB_SIMPLEFB is not set`, `CONFIG_FB_EFI=y`가 있다. 전용 GPU 드라이버가 없으면 DRM 장치가 생기지 않는다.
  - 근거 2: 같은 커널의 nouveau는 Ada(`0x192`~`0x197`)까지만 알고 Blackwell(`0x1b2`~`0x1b7`)은 6.16에서 들어왔다. 그래서 RTX 50은 NVIDIA 드라이버 없이는 KMS를 쓸 수 없다. labwc와 Calamares를 띄울 수 없다.
  - 근거 3: NVIDIA 드라이버 라이선스 1.1(d)는 OSI 승인 커널과 함께 배포하는 경우 바이너리를 수정하지 않고, 이 계약서를 각 수령자에게 제공하면 배포를 허용한다. 따라서 드라이버를 불확실한 구성 요소로 분류할 근거가 약하다.
- **사용자 확정(U3, U4)**: 드라이버 전체를 ISO에 넣는다. CUDA 런타임(`cudart`, `cublas`)도 ISO에 넣는다. 검증자는 CUDA 런타임만 첫 부팅 설치를 제안했으나 사용자가 ISO 포함으로 정했다.
- **남는 위험**: CUDA EULA 1.1.2의 "애플리케이션이 실질적인 추가 기능을 가져야 하고, 재배포 부분은 그 애플리케이션만 접근해야 한다"는 조건을 OS 이미지 번들링이 만족하는지는 해석이 필요하다. 이 문서는 법률 자문이 아니다. 자세한 내용은 [licenses.md](licenses.md)에 적는다.
- **드라이버 없는 변형 ISO**: 검증자는 드라이버를 뺀 변형을 만들려면 DRM 장치 없이 동작하는 텍스트 콘솔 설치 흐름을 따로 설계하고 RTX 50 실기에서 검증하라고 제안했다. 이번 결정에서는 채택하지 않는다.
- **근거**:
  - https://security.debian.org/debian-security/pool/updates/main/l/linux/linux-headers-6.12.111+deb13-amd64_6.12.111-1_amd64.deb (커널 설정과 nouveau 소스 — 검증 기록 check 28)
  - https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-cuda_595.91.07-1_amd64.deb (라이선스 1.1(d), 2.8 — `usr/share/doc/nvidia-driver-cuda/copyright`)
  - https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kernel_open.html ("Blackwell and later are only supported by the open kernel modules")

---

## D9. Secure Boot 처리 (`must_change`)

2026-10-11 인수인계 지시에 따라 빌드 키 제거와 부팅 시 텍스트 안내를 Phase 1에서 구현한다. 현재 라이브 ISO는 Secure Boot 끄기를 전제로 하며, 기기별 MOK 등록은 후속 단계에서 제공한다. 아래는 원래 조사 근거다. 실제 검증 결과는 [verification-phase-1.md](verification-phase-1.md)에 기록한다.

- **원래 결정**: 없음. 최초 사양에 Secure Boot 방침이 없다.
- **검증자 제안**:
  - 이미지 빌드 중 DKMS가 만든 `/var/lib/dkms/mok.key`와 `mok.pub`은 ISO에 남기지 않는다. DKMS는 키가 없으면 자동 생성한다. chroot에서 빌드하면 모든 사용자에게 같은 개인키가 배포되는 셈이므로 반드시 제거한다.
  - 설치된 시스템에서 기기별 키를 새로 만들어 모듈을 다시 서명하고 `mokutil --import`로 등록하는 흐름을 둔다. 또는 v1에서는 "Secure Boot 끄기"를 요구 사항으로 적고, 부팅 시 상태를 감지해 안내한다.
  - 라이브 세션에서 Secure Boot가 켜져 있으면 NVIDIA 모듈이 올라오지 않는다는 전제로 텍스트 안내를 띄운다. 리뷰 B1에 따라 안내는 영어 한 줄로 바꾸고 `/run/issue.d`로 로그인 프롬프트 위에 출력하도록 했다. 실기 화면 표시는 미검증이다.
- **근거**:
  - Debian 커널 설정 `CONFIG_MODULE_SIG=y`, `CONFIG_SECURITY_LOCKDOWN_LSM=y`, `CONFIG_LOCK_DOWN_IN_EFI_SECURE_BOOT=y` — https://security.debian.org/debian-security/pool/updates/main/l/linux/linux-headers-6.12.111+deb13-amd64_6.12.111-1_amd64.deb (검증 기록 check 29)
  - DKMS 자동 키 생성 — https://raw.githubusercontent.com/dell/dkms/v3.2.2/README.md
  - NVIDIA 드라이버 README의 "새 키로 서명한 모듈은 키를 신뢰하기 전까지 서명 강제 커널에 로드할 수 없다"는 문장(검증 기록 check 29가 인용, URL은 검증 기록에 없음)
  - live-build `--uefi-secure-boot` 기본값 `auto`(검증 기록 check 29)
  - 구현 상세는 [secure-boot.md](secure-boot.md)에 적는다.
- **결정 필요**: 사용자가 "v1은 Secure Boot 끄기 요구"로 확정할지, 기기별 MOK 흐름을 v1에 넣을지 정해야 한다.

---

## 사용자 확정과 미결정 항목 요약

| 항목 | 상태 |
|---|---|
| 저장소, CUDA 13.2.2, llama.cpp v0.6.0, go-nvml v0.13.4-1, 드라이버 595.91.07 | 사용자 확정 |
| CUDA 런타임과 드라이버를 ISO에 포함 | 사용자 확정 |
| llama.cpp 웹 UI 없음 | 사용자 확정 |
| 프로젝트 라이선스 | 미정. LICENSE 파일 없음 |
| Secure Boot 방침 (D9) | Phase 1 빌드 키 제거와 텍스트 안내 구현. 현재 라이브 사용은 Secure Boot 끄기 전제 |
| 모델 라이선스 표시 방침 | 미확정. [licenses.md](licenses.md) 참고 |
| labwc 0.8.3 explicit sync 문제 대응 (D6) | 실기 시험 후 결정 |
| 그리터 프로그램 선택 (D6) | Phase 3에서 결정 |
| dkms 핀의 Origin 문자열 (D3-3) | 미확인. 리뷰는 apt 핀으로 dkms 후보가 Debian 저장소 3.2.2-1~deb13u1(우선순위 1001)인 것을 확인했다. Origin 문자열 자체는 0.1.2 수정에서 확인하지 않았다 |
