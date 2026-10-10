# NVIDIA 드라이버 구성

- 확인 날짜: 2026-10-11
- 상태: Phase 1 구현. NVIDIA 실 하드웨어는 미검증

> `lightlix-nvidia`와 live-build 훅으로 구현했다. 1~14절은 사전 조사 당시의 근거와 후속 실기 항목을 보존한다. 이번 결과는 [verification-phase-1.md](verification-phase-1.md)에 적는다. 빌드와 QEMU 성공은 NVIDIA 실기 동작을 뜻하지 않는다.

## Phase 1 구현

- 저장소는 `cuda-keyring` 패키지로만 추가한다. live-build의 `config/archives/*.deb` 경로에서 키와 브랜치 핀을 드라이버보다 먼저 설치한다. `.list`와 키를 중복 생성하지 않는다.
- `00-lightlix-nvidia.pref`로 독점 모듈을 차단하고 Debian의 DKMS를 선택한다. 실제 Origin이 `Debian`임을 `apt-cache policy`로 확인했다.
- `lightlix-nvidia`의 고정 의존성으로 세 드라이버와 CUDA 런타임을 한 번에 설치한다. 지정된 버전이 사라지면 다른 버전으로 넘어가지 않고 실패한다.
- 이미지의 각 6.12 커널에 DKMS를 빌드하고 모듈 파일·버전·`Dual MIT/GPL` 라이선스를 검사한다. initramfs 갱신 후 서명 키를 제거한다.
- 서버 모드는 `multi-user.target`이다. sleep, suspend, hibernate, hybrid-sleep, suspend-then-hibernate를 마스크한다. `nvidia-persistenced`는 GPU 진단 뒤 실행한다.
- `lightlix-gpu-check`는 GPU 없음, Secure Boot 상태에서 모듈 로드 실패, 일반 모듈 로드 실패, 독점 모듈, 버전 불일치, 미지원 GPU를 구분한다. 결과는 `/run/lightlix/gpu-status`에 기록한다.
- 커널과 드라이버는 hold한다. 안전한 동시 갱신과 롤백 도구는 Phase 4에서 구현한다. 수동으로 hold를 풀면 이 정책이 유지되지 않는다.
- 원본 계약서 전문은 `/usr/share/lightlix/licenses/`에 보존한다. 드라이버 패키지의 바이너리는 변경하지 않는다.

Lightlix는 RTX 30, 40, 50에서 NVIDIA 오픈 커널 모듈을 쓴다. 드라이버는 ISO에 포함하며, 바이너리는 수정하지 않는다(사용자 확정). 결정 근거는 [decisions.md](decisions.md)의 D3, D7, D8에 있다.

---

## 1. Production Branch 정의

- **브랜치 번호**: 595. nvidia.com이 표시하는 Production 브랜치 번호다.
- **실제 버전**: NVIDIA apt 저장소가 595 브랜치로 제공하는 최신인 **595.91.07-1**을 쓴다.
- **nvidia.com 최신 595.104.02는 쓰지 않는다**. nvidia.com의 Production 최신은 595.104.02(2026-09-22)다. 그러나 debian13, debian12, ubuntu2404 저장소 모두 595 최신은 595.91.07이다. 595.99.02와 595.104.02의 deb 로컬 저장소 설치 파일은 HTTP 404다. NVIDIA는 Production 릴리스를 모두 deb로 내지 않는다.
- **`.run` 설치기는 쓰지 않는다**.
- 저장소에 새 595 버전이 올라오면 이미지를 다시 빌드하고 릴리스 노트에 드라이버 버전을 적는다.

근거:
- https://www.nvidia.com/en-us/drivers/unix/ (Production 595.104.02, 2026-09-22)
- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz (저장소 595 최신 595.91.07-1)
- https://download.nvidia.com/XFree86/Linux-x86_64/ (디렉터리 목록)

---

## 2. 저장소와 키

- 저장소 주소: https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/
- 저장소 항목: `deb [signed-by=/usr/share/keyrings/cuda-archive-keyring.gpg] https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/ /`
- 키 패키지: `cuda-keyring` 1.1-1(Architecture: all). 설치하면 `/usr/share/keyrings/cuda-archive-keyring.gpg`와 `/etc/apt/sources.list.d/cuda-debian13-x86_64.list`가 생긴다.
- 저장소 `Packages.gz`는 2246개 항목이다. Release 날짜는 2026-10-09다.

2026-10-11 결정: `cuda-keyring` 패키지를 사용한다. 저장소 항목과 키를 직접 생성하는 방식은 병행하지 않는다.

근거:
- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/cuda-keyring_1.1-1_all.deb (내부 `cuda-debian13-x86_64.list` 확인)
- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz

---

## 3. 브랜치 고정 패키지 `nvidia-driver-pinning-595`

- 패키지: `nvidia-driver-pinning-595` 595-1(Architecture: all). 같은 저장소에 브랜치형 `590`(590-5), `595`(595-1), `610`(610-1, 610-2), `615`(615-1)가 있다.
- 의존성: `apt`. `Provides`, `Conflicts`, `Replaces`로 `nvidia-driver-pinning`과 서로 배타적이다.
- 하는 일: `/etc/apt/preferences.d/nvidia-driver-pin` 파일 하나를 설치한다. 이 파일은 다음 패키지를 `version 595*`에 Pin-Priority 1000으로 고정한다.
  - `src:nvidia-graphics-drivers:any`, `src:nvidia-kmod-open:any`
  - `nvidia-modprobe`, `nvidia-persistenced`, `nvidia-settings`, `nvidia-xconfig`
  - `cuda-drivers*`, `src:nvidia-open:any`
  - `nvidia-fabricmanager`, `nvidia-imex` 관련 블록
- 설치된 버전과 595 후보 사이의 다운그레이드 동작은 apt_preferences(5)로 확인이 필요하다(14절).

근거: https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-pinning-595_595-1_all.deb (data.tar.xz 안의 `etc/apt/preferences.d/nvidia-driver-pin`, control 파일)

---

## 4. 설치할 패키지 구성

| 패키지 | 버전 (저장소 기준) | 역할 | 비고 |
|---|---|---|---|
| `nvidia-kernel-open-dkms` | 595.91.07-1 | 오픈 커널 모듈 소스. DKMS로 빌드한다. | `Source: nvidia-kmod-open`. Depends: `dkms (>= 3.1.8)`, `firmware-nvidia-gsp (= 595.91.07-1)`, `g++`. Recommends: `nvidia-driver \| nvidia-driver-cuda`, `nvidia-kernel-support`. Conflicts: `nvidia-kernel-dkms`. |
| `nvidia-driver` | 595.91.07-1 | 데스크톱 구성(GL, EGL, Vulkan, X 드라이버 포함) | 절전 유닛(`nvidia-suspend`, `nvidia-resume`, `nvidia-hibernate`, `nvidia-powerd`)이 여기에만 있다. X 드라이버 `xserver-xorg-video-nvidia`를 끌어온다. |
| `nvidia-driver-cuda` | 595.91.07-1 | CUDA 컴퓨트 구성. `nvidia-smi`, `nvidia-cuda-mps`를 제공한다. | 디스플레이가 필요 없다. Depends로 `libcuda1`, `libnvidia-ml1`, `nvidia-persistenced`, `nvidia-kernel-support` 등을 끌어온다. |

- 설치 기준 구성은 `nvidia-kernel-open-dkms` + `nvidia-driver` + `nvidia-driver-cuda`이다. 검증자가 정정한 내용이다. 헤드리스 전용 조합(`nvidia-driver-cuda` + `nvidia-kernel-open-dkms`)은 쓰지 않는다. 이 조합에는 `libegl-nvidia0`, `libnvidia-egl-gbm1`, `libnvidia-egl-wayland21` 같은 EGL/GBM/Wayland 라이브러리와 절전 유닛이 없어서 labwc가 GPU로 그릴 수 없다.
- `nvidia-open` 메타패키지는 `nvidia-settings`와 `nvidia-xconfig`를 추가로 끌어오므로 쓰지 않는다. `cuda-drivers`도 쓰지 않는다.
- `nvidia-driver` 대신 `nvidia-driver-libs`만 쓰면 X 드라이버를 피할 수 있다. 다만 절전 처리(suspend, resume)를 직접 구성해야 한다. 이 대안은 Phase 1에서 판단한다.
- 전부 한 번의 apt 트랜잭션으로 설치한다.

근거:
- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz (패키지 메타데이터, `nvidia-open` 595.91.07-1 Depends)
- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-cuda_595.91.07-1_amd64.deb (`nvidia-smi`, `nvidia-cuda-mps` 경로, 폐쇄 25개 패키지, 내려받기 241.5 MiB)
- https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/latest/debian.html (권장 명령: 컴퓨트 전용 `nvidia-driver-cuda nvidia-kernel-open-dkms`, 데스크톱 전용 `nvidia-driver nvidia-kernel-open-dkms`)

---

## 5. 설치 순서

1. `nvidia-driver-pinning-595`를 설치한다. 드라이버 패키지보다 먼저 설치한다. NVIDIA 가이드도 이 순서를 권장한다.
2. 독점 모듈 차단 핀 파일(6절)을 `nvidia-driver-pin`보다 앞서 읽히는 이름으로 둔다.
3. 한 번의 apt 트랜잭션으로 설치한다.

   ```
   nvidia-kernel-open-dkms nvidia-driver nvidia-driver-cuda
   ```

4. 이미지 빌드 중에 DKMS가 모듈을 빌드하는지 확인한다(10절).

근거: https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/latest/debian.html (설치 절차와 `apt install nvidia-driver-pinning-<branch>` 순서)

---

## 6. 독점 모듈 차단 핀

독점 모듈 패키지(`nvidia-kernel-dkms`, `nvidia-kernel-source`, `cuda-drivers*`)는 같은 NVIDIA 저장소에 있다. 오픈 모듈과 충돌하므로 막아야 한다. 막는 방법은 apt Pin-Priority -1이다.

제안 파일 이름: `/etc/apt/preferences.d/00-lightlix-nvidia.pref`

```
Package: nvidia-kernel-dkms nvidia-kernel-source cuda-drivers*
Pin: version *
Pin-Priority: -1
```

**이 파일이 `nvidia-driver-pin`보다 먼저 읽혀야 하는 이유**: apt는 패키지 버전마다 처음 맞는 핀 하나만 적용한다. 그 핀이 `nvidia-driver-pin`(우선순위 1000)이면 595.* 버전은 차단되지 않고 우선순위 1000을 받는다. 차단 파일은 정렬상 앞서 읽히는 preferences.d 파일에 있어야 한다. `/etc/apt/preferences`나 `nvidia-driver-pin`보다 먼저 정렬되는 이름을 쓴다. 이 동작은 apt 3.0.3의 `policy.cc`(`CreatePin`, `ReadPinFile` → `ReadPinDir` 순서)로 확인했다.

- 참고: Debian 550.x 패키지는 `595*` 조건에 걸리지 않는다. 기본 우선순위 500이 남아 1000 우선순위의 595 패키지에 밀린다.
- 참고: 차단 핀은 빌드 검증에 넣는다. 설치된 이미지에서 `nvidia-kernel-dkms`가 없는지 확인한다.

근거:
- https://salsa.debian.org/apt-team/apt/-/raw/3.0.3/apt-pkg/policy.cc
- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/nvidia-driver-pinning-595_595-1_all.deb

---

## 7. dkms 출처 핀 (제안)

NVIDIA 저장소의 `dkms`와 `dh-dkms`는 3.4.3-1이고, Debian main은 3.2.2-1~deb13u1이다. NVIDIA 쪽이 버전이 높아서 핀이 없으면 NVIDIA 쪽이 선택된다. `nvidia-kernel-open-dkms`는 `dkms (>= 3.1.8)`만 요구하며, 빌드도 Debian `dh_dkms/3.2.2-1~deb13u1`으로 되어 있어 Debian 쪽 `dkms`로 충분하다.

검증자 제안 (Phase 1에서 확인):

```
Package: dkms dh-dkms
Pin: release o=Debian
Pin-Priority: 1001
```

- `o=Debian`이 Debian main의 Origin 문자열과 맞는지는 검증하지 않았다. NVIDIA 저장소의 Origin은 `NVIDIA`다(Label: `NVIDIA CUDA`). Debian 쪽 Origin 문자열은 Phase 1에서 `apt-cache policy`로 확인한다.

근거: https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz , https://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.xz

---

## 8. Debian archive area

- Debian 쪽은 `main`과 `non-free-firmware`만 켠다. `contrib`와 `non-free`는 켜지 않는다.
- 595.91.07 의존성 폐쇄(Depends와 Pre-Depends, amd64 기준)에서 NVIDIA 저장소 밖의 이름 38개(예: `adduser`, `dpkg-dev`, `g++`, `kmod`, `libdrm2`, `libegl1`, `libgbm1`, `libwayland-client0`, `xserver-xorg-core`)는 모두 trixie `main`에 있다.
- `firmware-nvidia-gsp`는 `non-free-firmware`에 550.163.01-2가 있다. 그러나 NVIDIA 쪽 `nvidia-kernel-open-dkms`가 `firmware-nvidia-gsp (= 595.91.07-1)`을 요구하므로 NVIDIA 저장소 패키지가 실제로 쓰인다.
- NVIDIA 가이드는 `add-apt-repository contrib`를 요구하지만, 이 구성에는 필요하지 않다. 가이드를 그대로 따르면 550 계열 패키지가 섞일 수 있다.

근거: https://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.xz (검증 기록 check 10), https://api.ftp-master.debian.org/madison?package=nvidia-driver&s=trixie,trixie-backports&text=on (Debian 550.163.01-2, `stable/non-free`)

---

## 9. `nvidia-drm` modeset 기본값

- 595 브랜치는 DRM KMS를 기본으로 켠다. 595.45.04부터 595.104.02까지 README는 "NVIDIA's DRM KMS support is enabled by default"라고 적는다.
- 저장소 595.91.07-1 소스: `nv_drm_modeset_module_param = true`, `nv_drm_fbdev_module_param = true`.
- 따라서 커널 명령줄에 `nvidia-drm.modeset=1`을 넣지 않는다(D7).
- 테스트 항목: greetd가 뜨기 전에 `nvidia-drm`이 올라와 `/dev/dri/card*`가 있는지 확인한다.
- 590.44.01 이하 브랜치에서는 KMS가 기본 비활성이다. 브랜치를 내리는 경우 이 절을 다시 본다.

근거: https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kms.html (검증 기록 check 11). 595.104.02 README(`kms.html`, `commonproblems.html`)도 같은 문구를 적는다.

---

## 10. chroot에서 DKMS 빌드

- live-build는 chroot 안에서 패키지를 설치한다. DKMS는 chroot 안에서 `nvidia-kernel-open-dkms` 모듈을 빌드한다.
- chroot 안의 `uname -r`은 호스트 커널을 가리킨다. 그래서 DKMS가 어느 커널용으로 빌드할지 **이미지에 들어가는 커널 버전을 명시해야 한다**. 이미지 커널은 security 저장소의 6.12.111+deb13-amd64 기준이다(D1).
- 커널 헤더는 `linux-headers-amd64`로 넣는다. 이 패키지가 이미지 커널과 같은 헤더를 가져와야 한다.
- DKMS 키 문제: DKMS는 키가 없으면 `/var/lib/dkms/mok.key`와 `mok.pub`을 자동 생성해서 모듈에 서명한다. 이 키는 모든 설치본에 똑같이 들어간다. **이미지에서 이 키 파일들을 제거해야 한다**(D9, [secure-boot.md](secure-boot.md)).
- DKMS 빌드에 필요한 `g++`, `make`, `dpkg-dev`가 최종 이미지에 남는다. `make`와 `dpkg-dev`가 이 빌드 경로에 필요하다는 점은 작업 지시에 따른 것이고, 이번 조사에서 의존 관계를 직접 확인하지는 않았다. `g++`는 `nvidia-kernel-open-dkms`의 Depends로 확인했다. 이 비용은 [memory.md](memory.md)에 적는다.
- 보안 업데이트마다 커널 ABI 이름이 바뀌면 DKMS를 다시 빌드해야 한다. 설치된 시스템에서 빌드가 실패하면 이전 커널로 부팅하는 복구 경로가 필요하다.
- 명령 옵션(`dkms`의 커널 지정 방식 등)은 이번 조사에서 확인하지 않았다. Phase 1에서 `dkms --help`나 man 페이지로 확인한다.

근거:
- https://raw.githubusercontent.com/dell/dkms/v3.2.2/README.md (키 자동 생성)
- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz (`nvidia-kernel-open-dkms` Depends)
- https://security.debian.org/debian-security/dists/trixie-security/main/binary-amd64/Packages.xz (커널 6.12.111-1)

---

## 11. CUDA 패키지 (참고)

드라이버 자체는 이 문서의 대상이지만, ISO에는 CUDA 런타임도 들어간다(사용자 확정 U3). 패키지 목록은 다음과 같다. 검증 기록 기준 버전이다.

| 용도 | 패키지 | 버전 |
|---|---|---|
| 빌드 | `cuda-minimal-build-13-2`, `libcublas-dev-13-2`, `cuda-driver-dev-13-2` | 13.2.2 |
| 런타임(ISO 포함) | `cuda-cudart-13-2` | 13.2.86-1 |
| 런타임(ISO 포함) | `libcublas-13-2` | 13.4.1.3-1 |

- 런타임 두 패키지의 내려받기 크기는 약 358 MiB, 설치 후 538 MiB다(검증 기록 기준).
- 595 드라이버에 맞추려면 CUDA 13.2를 쓴다. 13.4.2를 쓰면 제한 기능 모드가 된다(decisions.md D4-1).
- 재배포 조건과 남은 해석 위험은 [licenses.md](licenses.md)에 있다.

근거: https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz , https://docs.nvidia.com/cuda/cuda-toolkit-release-notes/index.html (Table 2)

---

## 12. 런타임 검증 항목 (Phase 1 수용 기준)

이번 조사에서는 아래 항목을 실제로 확인하지 못했다. 전부 실기 확인이 필요하다.

1. RTX 30, 40, 50 각각에서 `nvidia-smi`가 GPU를 보고한다.
2. `nvidia-drm`이 올라와 `/dev/dri/card*`가 생긴다. greetd보다 먼저 생긴다.
3. 드라이버 없이 RTX 50 부팅 시 화면이 나오지 않는다는 조사 결과를 실기로 확인한다(D8).
4. CUDA 13.2.2 응용이 595.91.07 드라이버에서 실행된다. `llama-server`가 GPU로 추론한다.
5. labwc가 RTX 30, 40, 50에서 깜빡임이나 프레임 순서 문제 없이 뜬다(아래 13절).
6. Calamares가 XWayland 창으로 뜬다.
7. 절전 복귀(suspend/resume) 후 화면과 CUDA가 살아 있다. `nvidia-driver` 패키지의 절전 유닛을 쓸 때만 해당한다.
8. Secure Boot가 켜진 상태의 동작(D9, [secure-boot.md](secure-boot.md)).
9. `nvidia-kernel-dkms`가 설치되지 않았다.
10. 커널 업데이트 후 DKMS가 재빌드되고 모듈이 로드된다.

---

## 13. labwc 0.8.3의 explicit sync 위험

- trixie의 labwc는 0.8.3-1이다. labwc 0.9.0(2025-07-11)에서 `drm-syncobj` 프로토콜(explicit sync)이 추가되었다. 0.8.3 소스 tarball에는 `syncobj` 문자열이 없다.
- NVIDIA 드라이버 README는 Wayland에서 explicit sync를 DRM syncobj로 설명한다. 그래서 0.8.3은 NVIDIA에서 화면 문제가 생길 가능성이 있다.
- 실제 영향은 실기에서만 확인할 수 있다. 문제가 나오면 labwc 0.9 이상을 백포트하는 대안을 쓴다. 이때 필요한 wlroots 버전은 이번 조사에서 확인하지 않았다.
- 자세한 내용은 [decisions.md](decisions.md)의 D6을 본다.

근거: https://raw.githubusercontent.com/labwc/labwc/0.20.2/NEWS.md (0.9.0의 `drm-syncobj`), https://api.ftp-master.debian.org/madison?package=labwc&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on (labwc 0.8.3-1)

---

## 14. 열린 문제

- Debian 쪽 dkms 핀의 Origin 문자열 확인 (7절).
- `dkms`의 커널 지정 명령 옵션 확인 (10절).
- 절전 처리 방식 결정 (`nvidia-driver` 대 `nvidia-driver-libs`, 4절).
- `make`와 `dpkg-dev`가 DKMS 빌드 경로에 필요한지 의존 관계 확인 (10절).
- labwc 0.9 백포트 시 필요한 wlroots 버전 확인 (13절).
- 595.104.02가 저장소에 올라오면 595 버전을 갱신한다(1절).
- 브랜치 고정 핀의 다운그레이드 동작 확인(3절, `apt_preferences(5)`).
