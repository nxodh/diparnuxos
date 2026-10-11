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
- `lightlix-gpu-check`는 GPU 없음, Secure Boot 상태에서 모듈 로드 실패, 일반 모듈 로드 실패, 독점 모듈, 버전 불일치, 미지원 GPU를 구분한다. 결과는 `/run/lightlix/gpu-status`에 기록한다. 종료 코드와 상태 파일 키는 아래 "GPU 진단 계약" 절이 정한다.
- 커널과 드라이버는 hold한다. hold된 패키지는 49개(커널 5, NVIDIA 42, CUDA 2)이고, 갱신 명령(`lightlix-update`)이 생기는 Phase 4 전까지 `apt upgrade`로 커널 보안 수정이 적용되지 않는다. 안전한 동시 갱신과 롤백 도구도 Phase 4에서 구현한다. 수동으로 hold를 풀면 이 정책이 유지되지 않는다.
- 원본 계약서 전문은 `/usr/share/lightlix/licenses/`에 보존한다. 드라이버 패키지의 바이너리는 변경하지 않는다.

## GPU 진단 계약

`lightlix-gpu-check`(`/usr/sbin/lightlix-gpu-check`, 유닛 `lightlix-gpu-check.service`)의 종료 코드와 `/run/lightlix/gpu-status`는 Phase 2 데몬이 읽는 계약이다. 아래 값을 바꾸면 데몬도 같이 바꿔야 한다.

| 종료 코드 | 뜻 | `LIGHTLIX_GPU_STATUS` |
|---|---|---|
| 0 | 쓸 수 있는 GPU가 하나 이상 있다 | `ready` |
| 1 | NVIDIA GPU가 없다. CPU 폴백은 정상이다 | `no_gpu` |
| 2 | NVIDIA GPU는 있지만 쓸 수 없다 | `module_unloaded`, `secure_boot`, `proprietary_module`, `version_mismatch`, `driver_missing`, `query_failed`, `unsupported_gpu` |
| 3 | 스크립트 자체의 오류 | `internal_error`, `config_missing` |

유닛은 `SuccessExitStatus=1 2`이므로 종료 1과 2에서는 failed가 되지 않고, 종료 3에서는 failed가 된다.

판정 규칙:

- GPU 하나를 쓸 수 있으려면 compute_cap이 8.6, 8.9, 12.0 중 하나이고 이름이 `GeForce RTX (30|40|50)xx`에 맞아야 한다. compute_cap을 먼저 보고, 그다음 이름을 본다(`REASON`은 `compute_cap`, `name`, `ok` 중 하나).
- 쓸 수 있는 GPU가 하나라도 있으면 `ready`다. 미지원 GPU는 `SUPPORTED=0`으로 기록하고 화면에 무시한다는 한 줄을 낸다. `ready`일 때만 nvidia-uvm과 nvidia-drm을 올린다.
- 쓸 수 있는 GPU가 0개면 `unsupported_gpu`(종료 2)다.
- PCI NVIDIA 디스플레이 장치(class `0x03xx`) 수가 nvidia-smi 행 수보다 많으면 경고 한 줄을 내고, 판정은 위 규칙대로 한다.
- `secure_boot`는 Secure Boot가 켜져 있고 modprobe 오류문에 `Key was rejected by service`가 있을 때만이다. 그 밖의 모듈 로드 실패는 `module_unloaded`이고, Secure Boot가 켜져 있었다면 메시지에 그 사실을 덧붙인다.
- nvidia-smi는 오류문을 stdout에 쓴다(Phase 1 검사에서 stderr는 비어 있었다). 스크립트는 표준 출력과 오류 출력을 함께 받는다. `driver/library version mismatch` 또는 `API mismatch`가 있으면 `version_mismatch`, 그 밖의 실패는 `query_failed`다. 오류 내용은 화면에 출력한다.
- `nvidia.conf`를 읽을 수 없거나 고정 버전이 비어 있으면 버전 검사를 건너뛰지 않고 `config_missing`(종료 3)으로 끝낸다.
- 예상하지 못한 명령 실패는 `trap ... ERR`로 잡아 `internal_error`(종료 3)로 끝낸다.
- `timeout`은 `-k 5`를 붙인다. 시간 제한 합계는 최대 약 110초이고, 유닛의 `TimeoutStartSec`은 120초다. 유닛은 로그인 프롬프트보다 먼저 끝나므로 진단이 길어지면 로그인 프롬프트도 그만큼 늦는다.

상태 파일 `/run/lightlix/gpu-status`는 모드 0644이고, 임시 파일에 쓴 뒤 `mv`로 교체한다. 셸에서 source 할 수 있는 `KEY=VALUE` 형식이다. 값에 공백이 있으면 큰따옴표로 감싸고, 값 안의 `"`, `$`, `` ` ``, `\`는 지운다. 어떤 종료 경로에서도 아래 키를 모두 쓴다. 해당 없으면 빈 값이나 0이다.

| 키 | 값 |
|---|---|
| `LIGHTLIX_GPU_STATUS` | 위 표의 상태 중 하나 |
| `LIGHTLIX_GPU_PRESENT` | NVIDIA PCI 디스플레이 장치가 있으면 1, 없으면 0 |
| `LIGHTLIX_GPU_PCI_COUNT` | NVIDIA PCI 디스플레이 장치 수 |
| `LIGHTLIX_GPU_COUNT` | nvidia-smi가 보고한 GPU 수(조회 전이면 0) |
| `LIGHTLIX_GPU_USABLE_COUNT` | 쓸 수 있는 GPU 수. `ready`일 때만 0보다 크다(판정을 통과했더라도 `nvidia-uvm`이나 `nvidia-drm` 로드에 실패하면 0으로 되돌린다) |
| `LIGHTLIX_GPU_USABLE_INDEXES` | 쓸 수 있는 GPU의 nvidia-smi index, 쉼표 구분 |
| `LIGHTLIX_SECURE_BOOT` | `enabled`, `disabled`, `unknown`(mokutil이 없거나 해석하지 못함) |
| `LIGHTLIX_DRIVER_VERSION` | 로드된 모듈의 버전. 모르면 빈 값 |
| `LIGHTLIX_GPU_MESSAGE` | 화면 메시지의 한 줄 요약(ASCII) |
| `LIGHTLIX_GPU_<i>_NAME` | GPU 이름. `<i>`는 nvidia-smi index |
| `LIGHTLIX_GPU_<i>_COMPUTE_CAP` | compute_cap |
| `LIGHTLIX_GPU_<i>_SUPPORTED` | 1 또는 0 |
| `LIGHTLIX_GPU_<i>_REASON` | `ok`, `compute_cap`, `name` |

화면과 안내 파일:

- 모든 출력 문구는 ASCII 영어다. 리눅스 텍스트 콘솔에는 한글 글리프가 없다.
- 첫 줄은 `Lightlix GPU [<state>]: <message>` 형식으로 고정한다. 같은 내용을 `/run/issue.d/50-lightlix-gpu.issue`(로그인 프롬프트 위)와 `/run/motd.d/50-lightlix-gpu`(로그인 뒤)에도 쓴다.
- 종료 1과 2에서는 `Lightlix: running in CPU fallback mode.` 줄을 덧붙인다.
- `secure_boot`에서는 UEFI 설정에서 Secure Boot를 끄고 다시 부팅하라는 방법을 적는다.
- 안내 파일에는 백슬래시가 없어야 한다. agetty가 `\`를 이스케이프로 해석한다.
- 확인한 경로(trixie, 컨테이너):
  - agetty(8, util-linux 2.41.5): "agetty checks for the files /run/issue and /run/issue.d"이고 `.issue` 확장자 파일을 버전 순서로 출력한다. `agetty --show-issue`로 `/etc/issue` 뒤에 `/run/issue.d/*.issue` 내용이 나오는 것을 확인했다.
  - pam_motd(libpam-doc 문서, libpam-modules 1.7.0): 기본 경로에 `/run/motd.d/`가 있다. Debian의 `/etc/pam.d/login`은 `pam_motd.so motd=/run/motd.dynamic`과 `pam_motd.so noupdate` 두 줄을 쓴다. 두 번째 줄은 `motd=`나 `motd_dir=`이 아니므로 `/run/motd.d/`를 막지 않는다. pamtester로 이 두 줄을 그대로 쓴 서비스에서 `/run/motd.d/` 내용이 나오는 것을 확인했다. 로그인 뒤에 나오며 프롬프트 위에는 나오지 않는다. 실기 미검증이다.
  - systemd.exec(5, systemd 257): `ProtectSystem=full`은 `/usr`, `/boot`, `/efi`를 읽기 전용으로 하고, `full`이면 `/etc`도 읽기 전용으로 한다. `/run`은 바뀌지 않으므로 `/run/issue.d`, `/run/motd.d`, `/run/lightlix`에 쓸 수 있다.
- 유닛은 `RuntimeDirectory`를 쓰지 않는다. 유닛이 멈출 때 `/run/lightlix`를 지우면 데몬 소켓과 같이 쓸 수 없기 때문이다. 디렉터리는 `/usr/lib/tmpfiles.d/lightlix.conf`(`d /run/lightlix 0755 root root -`)가 부팅 때 만든다. 스크립트의 `mkdir -p`도 그대로 둔다.

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
- `nvidia-driver` 대신 `nvidia-driver-libs`만 쓰면 X 드라이버를 피할 수 있다. 다만 절전 처리(suspend, resume)를 직접 구성해야 한다. 이 대안은 채택하지 않았다. 이미지는 `nvidia-driver`를 쓰므로 X 드라이버 패키지도 함께 설치되고, 절전 유닛도 그대로 쓴다.
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

적용한 핀:

```
Package: dkms dh-dkms
Pin: release o=Debian
Pin-Priority: 1001
```

- `o=Debian`은 Debian 쪽 `dkms`와 맞는다. Phase 1 검사에서 `apt-cache policy`는 Debian `dkms`(`3.2.2-1~deb13u1`)에 우선순위 1001을, NVIDIA 쪽 `dkms`(`3.4.3-1`)에 500을 보였다. NVIDIA 저장소의 Origin은 `NVIDIA`다(Label: `NVIDIA CUDA`).

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

- 해결: Debian 쪽 dkms 핀의 Origin은 `Debian`이다(7절).
- 미확인: 이미지 훅(`9900-lightlix-nvidia.hook.chroot`)이 쓰는 `dkms autoinstall -k <커널>`의 `-k`는 dkms 3.2.2 man 페이지의 `autoinstall` 항목에서 확인하지 못했다. CI 로그에서는 6.12.111+deb13-amd64 대상 빌드가 성공했다(10절).
- 결정: 절전 처리는 `nvidia-driver`를 쓴다(4절).
- 미확인: `make`와 `dpkg-dev`가 DKMS 빌드 경로에 필요한지 의존 관계 확인 (10절).
- 미확인: labwc 0.9 백포트 시 필요한 wlroots 버전 확인 (13절).
- 595.104.02가 저장소에 올라오면 595 버전을 갱신한다(1절).
- 미확인: 브랜치 고정 핀의 다운그레이드 동작 확인(3절, `apt_preferences(5)`).
