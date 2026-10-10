# Phase 1 검증 기록

- 확인 날짜: 2026-10-11
- 상태: 구현과 로컬 빌드 검증 진행 중. NVIDIA 실 하드웨어는 미검증
- 저장소: https://github.com/nxodh/lightlixos

## 패키지 확인

NVIDIA 공식 debian13/x86_64 저장소의 `Packages.gz`를 직접 내려받아 버전과 SHA-256을 확인했다. 고정 값은 `lightlix.conf`에 정의한다.

| 패키지 | 확인한 버전 |
|---|---|
| nvidia-kernel-open-dkms, nvidia-driver, nvidia-driver-cuda | 595.91.07-1 |
| firmware-nvidia-gsp, nvidia-persistenced | 595.91.07-1 |
| nvidia-driver-pinning-595 | 595-1 |
| cuda-keyring | 1.1-1 |
| cuda-cudart-13-2 | 13.2.86-1 |
| libcublas-13-2 | 13.4.1.3-1 |
| Debian dkms | 3.2.2-1~deb13u1 |
| linux-image-amd64 / linux-headers-amd64 설치 후보 | 6.12.111-1 |

검사 컨테이너에서 `apt-cache policy`와 `apt-get -s --no-install-recommends install`을 실행했다. Lightlix 핀 적용 후 DKMS는 Debian 버전(1001), 오픈 모듈은 지정된 버전(1001)이 후보였다. 독점 모듈은 모든 버전이 -1이며 후보가 없었다. 세 드라이버와 CUDA 런타임의 단일 트랜잭션 의존성 해석은 성공했다.

Debian main과 security의 Origin은 `Debian`, NVIDIA 저장소는 `NVIDIA`였다. Debian DKMS의 의존성에서 `dpkg-dev`와 `make | build-essential`을 확인했다. 실제 DKMS 스크립트가 `/etc/dkms/framework.conf.d/*.conf`의 `parallel_jobs`를 읽는 것도 확인했다.

## 명령과 형식 근거

- `dkms autoinstall -k <이미지 커널>`과 `modinfo`의 `Dual MIT/GPL` 구분은 공식 문서와 패키지에서 확인했다.
- `mokutil --sb-state`는 trixie man page에서 확인했다.
- `--debootstrap-options`는 trixie의 `lb config --help`에서 확인했다. 초기 HTTPS 접근에 필요한 인증서를 포함한다.
- live-build의 `chroot_archives` 소스에서 핀 복사와 `archives/*.deb` 초기 설치 순서를 확인했다.
- 실제 NVIDIA `nvidia-smi` 바이너리에 `compute_cap` 문자열이 있다. GPU 없는 컨테이너에서 `--help-query-gpu`는 NVML 초기화 오류로 끝났으므로 실제 조회 성공은 실기에서 확인해야 한다.

## 실행 결과

| 검사 | 결과 |
|---|---|
| make lint (shellcheck) | 통과 |
| make test-gpu-check | 11개 진단 시나리오 통과. RTX 3060 Laptop GPU 이름 포함. 명령 스텁 사용, 실기 검증 아님 |
| actionlint | 통과 |
| apt 설치 시뮬레이션과 핀 정책 | 통과 |
| 로컬 make iso (arm64 Mac, amd64 Docker) | 진행 중 |
| 완성된 ISO의 패키지·모듈·키·라이선스 검사 | 대기 |
| QEMU/OVMF 로그인 및 GPU 없는 CPU 폴백 안내 | 대기 |
| phase-1 브랜치의 GitHub Actions | 대기 |

1차 CI 실행 [38068362474](https://github.com/nxodh/lightlixos/actions/runs/38068362474)는 전체 ISO 빌드와 이미지 커널의 오픈 모듈·버전·라이선스 검증, DKMS 키 제거에 성공했다. 이후 ISO 검사에서 `VERSION_ID="0.1.0"`의 따옴표를 허용하지 않은 테스트 코드가 실패했다. 검사 코드를 고쳤으며 최종 ISO 검사와 QEMU 부팅은 재실행으로 확인한다.

## 남은 검증과 위험

- RTX 30/40/50의 모듈 로드, 실제 `nvidia-smi`와 `compute_cap`, CUDA 실행, DRM 장치 생성은 실기 미검증이다. `tests/hw/run.sh`와 결과 양식으로 기록한다.
- Secure Boot를 켠 부팅과 기기별 MOK 등록은 미검증이다. 빌드 키는 배포하지 않으며 현재 라이브 사용은 Secure Boot 끄기를 전제로 한다.
- USB에 기록한 ISO의 실기 UEFI 부팅, DHCP/DNS, zram, 라이브 계정 로그인, 유휴 램 목표는 미검증이다.
- 네트워크·CPU 펌웨어의 실기 목록은 미정이다. 자동으로 모든 펌웨어를 넣지 않으며 현재 NVIDIA GSP 펌웨어를 드라이버 의존성으로 포함한다.
- 커널·드라이버는 hold하지만 안전한 갱신과 롤백 도구는 Phase 4에서 구현한다.
- CUDA 런타임 재배포 조건의 OS 번들링 해석 위험은 [licenses.md](licenses.md)에 기록되어 있다.
- Debian snapshot 전체 빌드와 비트 단위 재현성은 미검증이다. 패키지 버전 고정만으로 전체 ISO의 재현성을 주장하지 않는다.
- LLM 엔진·데몬·CLI·GUI·설치기는 구현하지 않았다. Phase 2는 사용자 승인 이후 시작한다.

## 1차 출처

- https://developer.download.nvidia.com/compute/cuda/repos/debian13/x86_64/Packages.gz
- https://docs.nvidia.com/datacenter/tesla/driver-installation-guide/latest/debian.html
- https://download.nvidia.com/XFree86/Linux-x86_64/595.91.07/README/kernel_open.html
- https://manpages.debian.org/trixie/dkms/dkms.8.en.html
- https://manpages.debian.org/trixie/mokutil/mokutil.1.en.html
- https://deb.debian.org/debian/dists/trixie/main/binary-amd64/Packages.xz
- https://security.debian.org/debian-security/dists/trixie-security/main/binary-amd64/Packages.xz
- trixie live-build의 `/usr/lib/live/build/chroot_archives`, `lb config --help`
