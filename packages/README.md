# packages/

Diparnux 가 직접 만드는 Debian 패키지의 소스를 둔다. 각 디렉터리는 `debian/` 을 가진 Debian 소스 패키지이며, 소스 형식은 `3.0 (native)` 이다.
빌드 결과 `.deb` 는 `build/config/packages.chroot/` 에 들어가고, live-build 가 ISO 를 만들 때 로컬 저장소로 쓴다.

## 패키지 목록

| 패키지 | 상태 | 내용 |
|---|---|---|
| `diparnux-base` | 있음 (Phase 0) | `/usr/lib/os-release` 와 `/etc/dpkg/origins/diparnux` 를 제공한다. |
| `diparnux-nvidia` | 있음 (Phase 1) | 고정된 NVIDIA 오픈 모듈·드라이버·CUDA 런타임 의존성, apt 정책, 서버 모드, 부팅 진단 |
| `diparnux-llm` | 빌드 설정 있음 (Phase 2) | llama.cpp 의 `llama-server`와 백엔드 모듈. 이 패키지의 소스는 `packages/` 가 아니라 [llama/](../llama/README.md)에 있다. `make llama` 로 빌드한다. |
| `diparnux-daemon` | 있음 (Phase 2) | `diparnuxd`, CLI `diparnux`, systemd 유닛, sysusers·tmpfiles 설정과 nftables 기본 차단 |
| `diparnux-desktop` | 예정 | GUI 구성 요소 |
| `diparnux-branding` | 예정 | 배경, 로고 등 브랜딩 자산 |

### diparnux-base

- `/usr/lib/os-release` 는 `base-files` 가 소유한 파일이다. 설치 전(`preinst`)에 `dpkg-divert` 로 `/usr/lib/os-release.debian` 으로 비켜 두고, 제거할 때(`postrm`의 `remove`) 되돌린다.
- `/etc/os-release` 는 패키지가 건드리지 않는다. 일반 Debian 에서는 `base-files` 의 심볼릭 링크라 그대로 따라온다. live-build 는 이 링크를 Debian 내용의 일반 파일로 바꿔 두므로, ISO 에서는 `build/config/hooks/normal/9100-diparnux-os-release.hook.chroot` 가 Diparnux 내용으로 다시 만든다.
- 파일 값은 패키지 소스에 적지 않는다. `debian/gen-identity.sh` 가 `diparnux.conf` 를 읽어 빌드할 때 만든다.
- `debian/changelog` 의 버전은 `diparnux.conf` 의 `DIPARNUX_VERSION` 과 같아야 한다. 다르면 `scripts/build-debs.sh` 가 실패한다.

## 빌드 방법

`diparnux-nvidia`의 드라이버 의존성과 apt 정책도 `diparnux.conf`에서 생성한다. `scripts/prepare-nvidia.sh`는 체크섬이 고정된 `cuda-keyring`과 브랜치 핀 패키지를 `build/config/archives/`에 준비한다. live-build는 이를 드라이버 설치 전에 넣는다. 저장소 항목과 키의 소유자는 `cuda-keyring`이며 별도 `.list`나 키를 중복 생성하지 않는다.

Debian 13 (trixie) 빌더에서 다음 패키지가 필요하다: `dpkg-dev`, `debhelper`, `build-essential`, `golang-go`, `gcc`, `libc6-dev`. `build-essential` 은 `dpkg-buildpackage` 가 암묵적 빌드 의존성으로 확인하므로 없으면 빌드가 실패한다. Mac에서는 저장소를 읽기 전용으로 마운트한 Docker 컨테이너에서 사본을 빌드하며 호스트에 도구를 설치하지 않는다.

```sh
make deb
```

또는 직접 실행한다.

```sh
scripts/build-debs.sh
```

`scripts/build-debs.sh` 는 `packages/*/debian` 이 있는 디렉터리마다 패키지를 임시 디렉터리에 복사하고 `dpkg-buildpackage -us -uc -b` 를 실행한다. 결과 `.deb` 만 `build/config/packages.chroot/` 로 옮기므로 저장소 안에 `.buildinfo` 나 `.changes` 가 남지 않는다.

`DIPARNUX_CONF` 환경 변수로 다른 설정 파일 경로를 줄 수 있다. 기본값은 저장소 루트의 `diparnux.conf` 이다.

### diparnux-daemon

`scripts/build-debs.sh`가 `daemon/` 전체를 패키지의 임시 소스 디렉터리에 복사한다. 원본 Go 모듈과 `dist/` 파일을 중복 관리하지 않는다.
두 바이너리는 vendoring한 의존성만 사용하며 cgo를 켠다. `-trimpath`와 `-buildvcs=false`로 호스트 경로와 VCS 메타데이터를 넣지 않고 버전을 설정 파일에서 읽는다.
런타임 의존성은 같은 버전의 `diparnux-nvidia`, `diparnux-llm`, `systemd`, `nftables`, `ca-certificates`와 `dh_shlibdeps`가 계산한 glibc다.

debhelper가 sysusers·tmpfiles 실행과 유닛 활성화를 담당한다. 부팅 때 `diparnuxd.service`, `diparnuxd.socket`, 두 방화벽 유닛을 시작하며 `diparnux-llama.service`는 데몬이 시작한다.
패키지 설치 시 서비스는 바로 시작하지 않는다. 기본 차단은 `nftables.service` 뒤에 적용해 배포판 기본 유닛의 `flush ruleset`과 순서를 고정하고, 저장 규칙은 기본 차단 성공 뒤에 적용한다.
라이브 사용자는 `build/config/includes.chroot/lib/live/config/1190-diparnux-groups`가 user-setup 뒤에 `diparnux` 그룹에 추가한다. 설치 시스템의 일반 사용자 그룹 설정은 후속 설치기 작업이다.

`packages/diparnux-daemon/tests/package.sh <deb>`는 실제 패키지를 풀어 파일·의존성·버전·cgo 빌드 정보를 검사하고, 임시 루트에서 sysusers·tmpfiles·유닛 활성화를 확인한다.
이 검사는 해당 아키텍처의 Debian 컨테이너에서 Go와 systemd 도구를 설치한 뒤 실행한다. arm64 검증은 Mac용 네이티브 빌드 검사이며 최종 amd64 ISO나 실제 GPU 동작 검증을 대신하지 않는다.

### diparnux-llm 은 여기서 빌드하지 않는다

`diparnux-llm` 은 CUDA 개발 도구가 필요하다. `make deb` 는 CUDA 툴체인이 없는 빌더에서도 돌아야 하므로, 이 패키지는 `packages/*/debian` 대상에서 뺐다. `llama/pkg/debian/` 의 소스를 `llama/build.sh` 가 Docker 빌더 안에서 빌드한다. 자세한 내용은 [llama/README.md](../llama/README.md)를 본다.
