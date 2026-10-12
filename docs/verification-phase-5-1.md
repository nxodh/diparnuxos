# 0.5.1 검증 기록: Diparnux 이름과 새 로고

- 확인 날짜: 2026-10-13
- 상태: 컨테이너 시험과 단위 시험은 통과했다. **CI 결과는 이 문서의 3절에 적는다(실행 후).** 실기와 VMware 는 미검증이다.
- 설계: [phase-5-1-design.md](phase-5-1-design.md)

## 1. 이번에 확인한 것

| 항목 | 방법 | 결과 |
|---|---|---|
| 추적 파일 경로와 내용의 이전 이름 | `scripts/check-legacy-name.sh`(`git ls-files`, `git grep -a -i`, 바이너리 포함, vendor 포함) | 0 건(459개 추적 파일) |
| Go | `go build`, `go vet`, `go test ./...`(모듈 경로 `github.com/nxodh/diparnuxos/daemon`) | 통과 |
| Control Center | `make test-gui` | 52개 통과 |
| 셸 | `make lint`(shellcheck) | 통과 |
| 설치기 설정 | `scripts/test-installer-config-in-docker.sh` | 통과 |
| 업데이트 흐름 | `scripts/test-update-in-docker.sh`(실제 apt/dpkg, 가짜 저장소) | 통과 |
| APT 저장소 | `scripts/test-apt-repo-in-docker.sh` | 통과 |
| 네트워크 | `scripts/test-network-in-docker.sh`(실제 `busctl` 과 가짜 iwd) | 통과 |
| Xfce 세션과 로그인 화면 | `scripts/test-desktop-config-in-docker.sh`: 패키지 빌드·설치, Xvfb 세션, Xephyr 로그인 화면 | 통과. 화면 캡처에서 시작 메뉴 단추, 로그인 아바타, 배경 마크가 새 로고(원 안의 D)로 표시됨 |
| 새 로고 자산 | `scripts/make-brand-assets.sh`(Pillow 컨테이너) | 아이콘 16~512(원형), 마크(흰색, 검정), 배경 마크, 설치기 이미지, GRUB 참고 이미지, xfwm4 이미지 60장 생성. 원형 아이콘과 환영 이미지를 눈으로 확인함 |

## 2. 시험 중에 알게 된 것

- **추적된 바이트코드**: `tests/qemu/__pycache__/*.pyc` 가 이전 커밋에서 실수로 추적되고 있었고 이전 이름이 들어 있었다. 추적에서 빼고 `.gitignore` 에 넣었다. 흔적 검사는 이런 바이너리까지 본다.
- **원본 로고의 가장자리**: 원본에 반투명 얼룩이 있어 단순 crop 으로는 투명 배경 가장자리가 지저분하다. 원 마스크로 다시 잘라 해결했다.
- **macOS 의 bash 3.2**: `mapfile` 이 없어 흔적 검사를 `git grep` 으로 구현했다. 빈 배열 확장(`set -u`)도 `${arr[@]+...}` 형태로 써야 한다.
- **Firefox 실험**: 이전 실행에서 "Profile Missing" 이 나온 것은 시험이 `HOME` 을 지정하지 않은 탓이다. 시험에서 `HOME` 을 지정하도록 고쳤다.

## 3. CI 결과

(실행 후 기록한다)

## 4. 아직 확인하지 않은 것

| 항목 | 이유 |
|---|---|
| QEMU 데스크톱·설치 E2E 와 이미지 검사의 새 항목(이전 이름 없음) | CI 대기 |
| 로고의 모양 | 사람이 확인해야 한다(자동 판정은 "비어 있지 않음"까지) |
| 설치된 시스템의 이전 이름 흔적 | 설치 E2E 에서 확인하지만 전용 단계는 없다. 이미지 검사와 라이브 세션 검사가 같은 파일을 본다 |
| 실기, NVIDIA 하이브리드 화면, VMware, 한글 입력 | 실기 필요(0.5.0 과 같다) |
