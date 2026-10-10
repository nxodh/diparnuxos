# packages/

Lightlix 가 직접 만드는 Debian 패키지의 소스를 둔다. 각 디렉터리는 `debian/` 을 가진 Debian 소스 패키지이며, 소스 형식은 `3.0 (native)` 이다.
빌드 결과 `.deb` 는 `build/config/packages.chroot/` 에 들어가고, live-build 가 ISO 를 만들 때 로컬 저장소로 쓴다.

## 패키지 목록

| 패키지 | 상태 | 내용 |
|---|---|---|
| `lightlix-base` | 있음 (Phase 0) | `/usr/lib/os-release` 와 `/etc/dpkg/origins/lightlix` 를 제공한다. |
| `lightlix-nvidia` | 예정 (Phase 1) | NVIDIA 드라이버 설치 |
| `lightlix-llm` | 예정 (Phase 1) | CUDA 와 llama.cpp, 추론 서버 |
| `lightlix-desktop` | 예정 | GUI 구성 요소 |
| `lightlix-branding` | 예정 | 배경, 로고 등 브랜딩 자산 |

### lightlix-base

- `/usr/lib/os-release` 는 `base-files` 가 소유한 파일이다. 설치 전(`preinst`)에 `dpkg-divert` 로 `/usr/lib/os-release.debian` 으로 비켜 두고, 제거할 때(`postrm`의 `remove`) 되돌린다.
- `/etc/os-release` 는 패키지가 건드리지 않는다. 일반 Debian 에서는 `base-files` 의 심볼릭 링크라 그대로 따라온다. live-build 는 이 링크를 Debian 내용의 일반 파일로 바꿔 두므로, ISO 에서는 `build/config/hooks/normal/9100-lightlix-os-release.hook.chroot` 가 Lightlix 내용으로 다시 만든다.
- 파일 값은 패키지 소스에 적지 않는다. `debian/gen-identity.sh` 가 `lightlix.conf` 를 읽어 빌드할 때 만든다.
- `debian/changelog` 의 버전은 `lightlix.conf` 의 `LIGHTLIX_VERSION` 과 같아야 한다. 다르면 `scripts/build-debs.sh` 가 실패한다.

## 빌드 방법

Debian 13 (trixie) 호스트에서 다음 패키지가 필요하다: `dpkg-dev`, `debhelper`, `build-essential`. `build-essential` 은 `dpkg-buildpackage` 가 암묵적 빌드 의존성으로 확인하므로 없으면 빌드가 실패한다.

```sh
make deb
```

또는 직접 실행한다.

```sh
scripts/build-debs.sh
```

`scripts/build-debs.sh` 는 `packages/*/debian` 이 있는 디렉터리마다 패키지를 임시 디렉터리에 복사하고 `dpkg-buildpackage -us -uc -b` 를 실행한다. 결과 `.deb` 만 `build/config/packages.chroot/` 로 옮기므로 저장소 안에 `.buildinfo` 나 `.changes` 가 남지 않는다.

`LIGHTLIX_CONF` 환경 변수로 다른 설정 파일 경로를 줄 수 있다. 기본값은 저장소 루트의 `lightlix.conf` 이다.
