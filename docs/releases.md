# 릴리스 게시

- 확인 날짜: 2026-10-14
- **0.6.1 부터 ISO 도 올린다**(`OPERETA_PUBLISH_ISO="yes"`). ISO 에는 NVIDIA CUDA 런타임이 없고(사용자의 컴퓨터가 직접 받는다), ISO 에 들어간 모든 Debian 패키지의 **대응 소스**가 같은 릴리스에 함께 올라간다(GPL). 소스가 없으면 릴리스 자산 모으기가 거절한다. 근거와 남은 쟁점은 [licenses.md](licenses.md) 10절에 있다.
- 상태: 워크플로(`.github/workflows/release.yml`)와 자산 모으기 스크립트가 구현되어 있다. 자산 모으기는 컨테이너 시험(`make test-release-docker`)으로 확인했다. **워크플로 자체는 태그를 푸시해야 돌므로 아직 실행하지 않았다.**

0.6.0 부터 버전마다 태그를 게시하고 GitHub 릴리스를 만든다. 릴리스는 두 가지를 겸할 수 있다.

1. **OS 업데이트 저장소**(항상): 같은 릴리스의 `opereta-*` 패키지와 서명된 APT 색인(평평한 형식). 설치된 시스템이 `opereta update` 로 이 버전을 받는다([updates.md](updates.md)).
2. **설치 이미지**(`OPERETA_PUBLISH_ISO="yes"` 일 때): ISO, SHA-256 체크섬, 대응 소스 묶음(`opereta-<버전>-sources-NN.tar`, `.tsv`, `SOURCES-README.txt`), 이 프로젝트의 소스(`opereta-<버전>-source.tar.gz`), 패키지 목록(`opereta-<버전>-packages.tsv`).

## 릴리스에 올라가는 파일

| 파일 | 내용 |
|---|---|
| `opereta-<버전>-amd64.iso`, `.iso.sha256` | 설치 이미지와 체크섬(`OPERETA_PUBLISH_ISO="yes"` 일 때만) |
| `InRelease`, `Release`, `Release.gpg` | APT 색인의 서명 |
| `Packages`, `Packages.xz` | APT 색인 |
| `opereta-*.deb` | 이 버전의 패키지(이미지에 들어간 것과 같은 파일) |
| `opereta-archive-keyring.pgp` | 저장소 공개 키(수동 검증용) |
| `SHA256SUMS` | 위 모든 파일의 체크섬 |

ISO 는 GitHub 릴리스 자산 한도(2 GiB)보다 작아야 한다. 넘으면 자산 모으기가 거절한다.

## 한 번만 하는 준비: 서명 키

OS 업데이트 저장소는 서명되어야 한다. 서명되지 않은 저장소는 만들지 않는다. **개인 키는 이 저장소에 올리지 않는다.**

**현재 상태(2026-10-14)**: 키를 만들었다. 지문 `1C41E0929BAD89108AA5D463A644792257EA5B5A`, ed25519, 유효 기간 5년(2031-10-13). 공개 키는 `keys/` 에 커밋되어 있고 개인 키는 만든 컴퓨터의 `~/opereta-apt-key` 에 있으며 GitHub 비밀 값 `OPERETA_APT_SIGNING_KEY` 로 등록했다. 개인 키의 비밀번호는 비어 있다. **`~/opereta-apt-key` 를 안전한 곳에 백업한다.** 잃어버리면 새 키를 만들고 새 공개 키가 든 이미지를 먼저 내보내야 한다. 아래는 새로 만들거나 키를 바꿀 때의 절차다.

1. 키를 만든다(저장소 밖 디렉터리, gpg 는 컨테이너에서 돈다).

   ```sh
   bash scripts/apt-keygen.sh ~/opereta-apt-key
   ```

   개인 키는 `~/opereta-apt-key` 에 생기고 공개 키는 `keys/opereta-archive-keyring.pgp`(와 `.asc`, `.fingerprint`)에 나온다. 스크립트가 만든 키는 비밀번호가 비어 있다. 보관 방식에 맞게 비밀번호를 걸거나 하드웨어 토큰으로 옮긴다.
2. **공개 키를 커밋한다**(`keys/opereta-archive-keyring.pgp`). 이 키가 들어간 빌드부터 이미지의 저장소 소스가 켜진 채로 나간다. 이 키가 없는 이미지는 OS 업데이트를 받을 수 없다.
3. 개인 키를 GitHub 비밀 값으로 올린다.

   ```sh
   bash scripts/apt-secret-for-ci.sh ~/opereta-apt-key | gh secret set OPERETA_APT_SIGNING_KEY
   # 키에 비밀번호를 걸었다면
   printf '%s' '<비밀번호>' | gh secret set OPERETA_APT_SIGNING_PASSPHRASE
   ```

   스크립트는 개인 키가 터미널에 찍히지 않도록 표준 출력이 터미널이면 거절한다.
4. 키를 바꿀 때는 새 공개 키를 가진 이미지(키링 패키지)를 먼저 내보낸 뒤 새 키로 서명한다. 폐기한 키로 서명한 저장소는 설치된 시스템이 거절한다.

워크플로는 서명 키와 `keys/opereta-archive-keyring.pgp` 가 같은 키인지 확인하고, 다르면 실패한다(다르면 설치된 시스템이 저장소를 믿지 못한다).

## 릴리스를 만드는 순서

1. 작업 브랜치에서 버전을 올린다(`opereta.conf` 의 `OPERETA_VERSION`, 모든 `packages/*/debian/changelog`, `gui/opereta_cc/__init__.py`). `build-debs.sh` 는 버전이 다르면 실패한다.
2. PR 을 열어 CI(`iso` 워크플로: 린트, ISO, 부팅과 설치 E2E)를 통과시킨다.
3. `main` 에 병합한다(사용자가 한다).
4. `main` 의 그 커밋에서 CI 가 성공한 것을 확인하고 태그를 게시한다.

   ```sh
   git switch main && git pull
   git tag -a v0.6.0 -m "Opereta 0.6.0"
   git push origin v0.6.0
   ```
5. 릴리스 워크플로가 돈다.
   - `verify`: 태그가 `opereta.conf` 의 버전과 같은지, `main` 에 있는지, 그 커밋의 `iso` 워크플로가 성공했는지 확인한다.
   - `build`: ISO 를 만들고(`make iso`) 부팅을 확인하고 자산을 모은다(`scripts/make-release-assets.sh`). CUDA 산출물은 `main` 의 CI 가 남긴 캐시를 쓰고, 없으면 다시 컴파일한다(약 1시간).
   - `publish`: 릴리스를 만들고 자산을 올린다. 쓰기 권한이 있는 작업은 이것뿐이다.
6. 릴리스 페이지에서 ISO 체크섬과 노트를 확인한다.

실패한 뒤 다시 올릴 때는 Actions 에서 `release` 워크플로를 수동 실행하고 태그를 입력한다. 릴리스가 이미 있으면 빠진 자산만 올린다.

### prerelease

이름에 `-` 가 든 태그(`v0.7.0-rc1`)는 prerelease 로 게시된다. GitHub 의 "최신 릴리스" 주소(`releases/latest`)는 prerelease 를 건너뛰므로 **업데이트 채널에는 나가지 않는다**. 시험 빌드는 이렇게 낸다.

## 받은 파일 확인

```sh
sha256sum -c SHA256SUMS --ignore-missing
```

## 업데이트 채널의 동작

- 설치된 시스템의 소스(`/etc/apt/sources.list.d/opereta.sources`)는 `https://github.com/nxodh/operetaos/releases/latest/download` 를 가리킨다. GitHub 가 이 주소를 가장 최근의 정식 릴리스 자산으로 넘겨 준다.
- 릴리스를 게시하는 순간부터 설치된 시스템의 `opereta update --check` 가 새 버전을 보여 준다.
- 평평한 저장소에는 이전 버전이 없다. 그래서 드라이버 정책 패키지가 바뀐 갱신의 롤백은 이전 버전을 받을 수 없을 때 거절한다([updates.md](updates.md)).
- 릴리스를 지우거나 prerelease 로 바꾸면 "최신 릴리스"가 이전 릴리스로 돌아간다. 이미 새 버전으로 올린 시스템은 다운그레이드하지 않는다(apt 가 올리지 않는다).

## ISO 에 들어 있는 제3자 소프트웨어

- **NVIDIA 드라이버**: 사용자 공간 라이브러리와 GSP 펌웨어, 오픈 커널 모듈(MIT/GPL). 재배포 조건(OSI 승인 커널과 함께, 수정 없이, 계약서 제공)을 지킨다([licenses.md](licenses.md) 1절).
- **CUDA 런타임은 없다**: 사용자의 컴퓨터가 NVIDIA 에서 받는다(`opereta gpu runtime install`). 이 프로젝트는 재배포하지 않는다.
- **Intel 펌웨어**: Wi-Fi(`iwlwifi`)와 이더넷 기본 프로파일(`ice`). 재배포가 "Intel 승인 제품과 함께"로 한정된 두 파일은 뺐다.
- **GPL 소프트웨어(Debian)**: 대응 소스를 같은 릴리스에 올린다. 소스 수집은 릴리스 워크플로가 하고, 실패하면 릴리스가 실패한다.
- **CI 아티팩트**: `iso` 워크플로의 ISO 아티팩트(7일)는 별개다. 같은 구성이므로 같은 조건이 적용된다.
- **프로젝트 라이선스**: MIT(`LICENSE`).
- **검증 범위**: 실기 설치, 실제 커널 갱신, 실제 GPU 는 미검증이다([support-matrix.md](support-matrix.md)). 릴리스 노트가 이를 가리킨다.
