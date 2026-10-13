# Diparnux APT 저장소

- 확인 날짜: 2026-10-12
- 상태: 도구와 클라이언트 설정이 구현되어 있고 컨테이너에서 서명 검증을 확인했다. **서명 키와 호스팅 주소는 아직 정해지지 않았다**(사용자 결정). 정하기 전에는 클라이언트 소스가 꺼진 채로 나간다.

저장소는 `diparnux-*` 패키지(데몬, CLI, 데스크톱, Control Center, NVIDIA 정책, 설치기 설정, 키링)만 제공한다. Debian 과 NVIDIA 패키지는 각자의 저장소에서 받는다.

## 구조

```
dists/diparnux/{InRelease,Release,Release.gpg}
dists/diparnux/main/binary-amd64/{Packages,Packages.xz}
pool/main/<첫 글자>/<패키지>/<패키지>_<버전>_<아키텍처>.deb   (모든 버전을 남긴다)
```

- 색인은 `apt-ftparchive` 로 만들고 `gpg` 로 `Release` 를 서명한다(`InRelease`, `Release.gpg`).
- `Origin: Diparnux`, `Suite`/`Codename: diparnux`, 구성 요소 `main`, 아키텍처 `amd64`(`all` 패키지는 amd64 색인에 들어간다).
- 이전 버전을 pool 에 남기는 것은 롤백이 이전 버전을 설치할 수 있게 하기 위해서다.

## 클라이언트

`diparnux-archive-keyring` 패키지(이미지에 들어 있다)가 설치한다.

| 파일 | 내용 |
|---|---|
| `/usr/share/keyrings/diparnux-archive-keyring.pgp` | 공개 서명 키 |
| `/etc/apt/sources.list.d/diparnux.sources` | deb822 소스. `Signed-By` 로 위 키만 믿는다. 주소나 키가 없으면 `Enabled: no` |
| `/etc/apt/preferences.d/10-diparnux.pref` | `Origin: Diparnux` 의 패키지는 `diparnux-*` 만 받는다(우선순위 600, 그 밖에는 -1) |

## 키를 만들고 저장소를 만드는 절차 (사용자가 한다)

개인 키는 이 저장소에 올리지 않는다. 키 생성, 보관, 호스팅은 사용자가 정한다.

1. 키를 만든다. 개인 키는 저장소 밖의 디렉터리에 둔다.

   ```sh
   scripts/apt-keygen.sh ~/diparnux-apt-key        # 개인 키는 이 디렉터리, 공개 키는 keys/ 에 나온다
   ```

   스크립트가 만드는 키는 비밀번호가 비어 있다. 보관 방식에 맞게 `gpg --edit-key` 로 비밀번호를 걸거나 하드웨어 토큰으로 옮긴다. `keys/diparnux-archive-keyring.pgp`(공개 키)만 커밋한다.
2. 주소를 정한다: `diparnux.conf` 의 `DIPARNUX_APT_URL`(예: GitHub Pages 의 `https://nxodh.github.io/diparnuxos/apt`). 정하면 다음 이미지 빌드부터 소스가 켜진 채로 나간다.
3. 패키지를 만들고 저장소를 만든다.

   ```sh
   make deb
   DIPARNUX_APT_GNUPGHOME=~/diparnux-apt-key DIPARNUX_APT_KEY_ID=<지문> \
       scripts/build-apt-repo-in-docker.sh           # out/apt/tree 에 서명된 저장소가 나온다
   ```
4. `out/apt/tree` 를 정한 주소에 올린다(정적 파일 호스팅이면 된다).
5. 키를 바꿀 때는 새 키링 패키지를 먼저 배포한 뒤 새 키로 서명한다. 폐기한 키로 서명한 저장소는 클라이언트가 거절한다.

## 검증

`make test-apt-repo-docker`: 임시 키로 저장소를 만들고 apt 가 서명을 검증하며 설치와 다운그레이드를 하는지, 색인을 변조하면 거절하는지, 다른 키로 서명한 저장소를 거절하는지, `diparnux-*` 가 아닌 패키지를 받지 못하게 막는지 본다.

## 열린 결정

- 호스팅 위치(GitHub Pages 가 기본 후보)와 서명 키의 보관 방식.
- 프로젝트 라이선스(U7)가 정해지기 전에 패키지를 공개 저장소에 올려도 되는지. 라이선스가 없으면 기본적으로 모든 권리가 저자에게 있다.
