# Lightlix

- 확인 날짜: 2026-10-10
- 상태: 초안. Phase 0 진행 중. **실 하드웨어에서 검증하지 않았다.**
- 저장소: https://github.com/nxodh/lightlixos

Lightlix는 Debian 13(trixie) 기반의 Linux 배포판이다. 램 사용을 최소로 줄이면서 로컬 LLM 추론 서버를 돌리는 것이 목표다. amd64 아키텍처와 UEFI 부팅만 지원한다는 것이 계획이다. 대상 GPU는 NVIDIA RTX 30, 40, 50 계열이다.

> Debian은 Software in the Public Interest, Inc.가 소유한 등록 상표다. Lightlix는 Debian과 제휴 관계가 없는 파생 배포판이며, Debian 프로젝트가 만들었거나 보증하지 않는다.

---

## 현재 상태

지금은 **Phase 0**이다. Phase 0 ISO에는 다음만 들어간다.

- 최소 Debian 13 시스템
- Lightlix 정체성(`os-release`)과 호스트 이름 `lightlix`
- 직렬 콘솔 로그인 프롬프트 `lightlix login:`
- 라이브 계정 `user`(비밀번호 `live`, sudo 가능). Debian live-config 의 기본값이며 SSH 서버는 들어 있지 않다.

다음은 아직 없다. NVIDIA 드라이버, CUDA, llama.cpp, GUI, 설치기, `lightlixd` 데몬. 이 기능들은 Phase 1 이후에 구현한다.

이 저장소의 결과는 실 하드웨어에서 검증하지 않았다. 빌드와 부팅 테스트도 Phase 0에서 CI와 QEMU로만 확인하는 것이 목표다. 실기 결과가 생기기 전까지 특정 GPU에서의 동작을 주장하지 않는다. 상태 표는 [docs/support-matrix.md](docs/support-matrix.md)에 있다.

---

## 대상 하드웨어

| 항목 | 대상 | 상태 |
|---|---|---|
| CPU 아키텍처 | amd64 | 계획 |
| 부팅 방식 | UEFI 전용 | 계획 |
| GPU | NVIDIA RTX 30(Ampere, 8.6), RTX 40(Ada, 8.9), RTX 50(Blackwell, 12.0) | 미검증 |
| 드라이버 모듈 | NVIDIA 오픈 커널 모듈 | 계획 |

RTX 50은 오픈 커널 모듈만 지원한다. Secure Boot가 켜진 상태에서는 NVIDIA 모듈이 올라오지 않을 수 있다. 자세한 내용은 [docs/secure-boot.md](docs/secure-boot.md)를 본다.

---

## 빌드

요구 사항과 명령은 Makefile 기준이다. 버전과 저장소 주소는 저장소 루트의 `lightlix.conf` 한 곳에서만 정의한다.

| 명령 | 내용 |
|---|---|
| `make iso` | Docker 안에서 ISO를 빌드한다. 호스트에 Debian 13이 없어도 된다. |
| `make iso-native` | Debian 13 호스트에서 root로 직접 ISO를 빌드한다. |
| `make deb` | `packages/` 아래 패키지를 `.deb`으로 빌드한다. |
| `make lint` | 셸 스크립트를 정적 검사한다. |
| `make test-qemu` | `tests/qemu/boot-smoke.sh`로 ISO가 부팅하는지 확인한다. 직렬 콘솔의 `lightlix login:` 프롬프트를 기준으로 성공을 판정한다. |
| `make clean` | 빌드 산출물을 지운다. |

산출물은 `out/lightlix-<version>-amd64.iso`와 같은 이름에 `.sha256`을 붙인 체크섬 파일이다. `out/`은 git에 넣지 않는다.

---

## 저장소 구조

```
lightlix.conf    저장소 URL과 버전 값을 정의하는 유일한 곳
Makefile         빌드, 검사, 테스트 타깃
scripts/         build-in-docker.sh, build-iso.sh, build-debs.sh
build/           live-build 설정(build/config/)과 빌더 Dockerfile. 빌드 작업은 저장소 밖 /var/tmp/lightlix-build 에서 한다
packages/        Lightlix 자체 패키지 소스
tests/           QEMU 부팅 확인(tests/qemu)과 실 하드웨어 체크리스트(tests/hw)
.github/         CI(ISO 빌드)
daemon/          lightlixd 데몬(Go). 설계 메모는 daemon/README.md
gui/             GTK4/libadwaita 앱. 설계 메모는 gui/README.md
cli/             명령행 도구. 설계 메모는 cli/README.md
llama/           llama.cpp 빌드 설정. 설계 메모는 llama/README.md
docs/            결정 기록, 드라이버, 라이선스, Secure Boot, 메모리, 지원 매트릭스, 검증 기록
out/             빌드 산출물(git 제외)
```

---

## 개발 단계 (초안)

아래 단계 구분은 이 문서 작성 시점의 계획이다. 확정 로드맵이 따로 있으면 그 기준으로 고친다.

| Phase | 내용 | 상태 |
|---|---|---|
| 0 | 저장소 뼈대, 빌드 스크립트, CI(ISO 빌드), 문서 초안, 최소 Debian 13 ISO와 `os-release` | 진행 중 |
| 1 | NVIDIA 드라이버(오픈 모듈), CUDA 런타임, llama.cpp 빌드와 `llama-server` 실행. Secure Boot의 빌드 쪽 처리(DKMS 키 제거, 모듈 서명) | 계획 |
| 2 | `lightlixd` 데몬과 CLI | 계획 |
| 3 | GUI(labwc, greetd, GTK4/libadwaita). Secure Boot의 기기별 MOK 흐름과 안내 | 계획 |
| 4 | Calamares 설치기(Lightlix 전용 설정) | 계획 |
| 5 | 실 하드웨어 검증, 지원 범위 확정, 릴리스 | 계획 |

---

## 문서

| 문서 | 내용 |
|---|---|
| [docs/decisions.md](docs/decisions.md) | 기술 결정 기록. 원래 결정, 2026-10-10 변경, 근거와 출처 |
| [docs/drivers.md](docs/drivers.md) | NVIDIA 드라이버 설치 계획(Phase 1 예정) |
| [docs/licenses.md](docs/licenses.md) | NVIDIA 드라이버 라이선스, CUDA EULA, 상표, 프로젝트 라이선스(미정) |
| [docs/secure-boot.md](docs/secure-boot.md) | Secure Boot와 커널 모듈 서명 설계(Phase 1, 3 예정) |
| [docs/memory.md](docs/memory.md) | 메모리 목표, 측정 방법 초안, 실측값(미측정) |
| [docs/support-matrix.md](docs/support-matrix.md) | GPU 아키텍처별 대상과 상태(전부 미검증) |
| [docs/verification-2026-10-10.md](docs/verification-2026-10-10.md) | 2026-10-10 검증 결과와 미검증 항목 |

구성 요소 설계 메모: [daemon/README.md](daemon/README.md), [gui/README.md](gui/README.md), [cli/README.md](cli/README.md), [llama/README.md](llama/README.md).

---

## 라이선스

프로젝트 자체 라이선스는 아직 정하지 않았다. 저장소에 LICENSE 파일이 없다. 포함되는 제3자 소프트웨어(NVIDIA 드라이버, CUDA 런타임, llama.cpp, Debian 패키지)는 각자의 라이선스를 따른다. 자세한 내용은 [docs/licenses.md](docs/licenses.md)에 있다.
