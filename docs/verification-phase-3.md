# Phase 3 검증 기록

확인 날짜: 2026-10-12. 작업 브랜치는 `phase-3`, PR은 [#5](https://github.com/nxodh/lightlixos/pull/5)다.
이 문서는 로컬 검사, 컨테이너 검사, GitHub Actions의 실제 ISO 검사를 구분한다. 실기 하드웨어에서는 아무것도 확인하지 않았다.

## 실제 CI 결과

[CI 38189927062](https://github.com/nxodh/lightlixos/actions/runs/38189927062)(head `0056570`)가 전체 성공했다.

| 단계 | 결과 |
|---|---|
| lint (셸 lint, GPU 진단 스텁, QEMU fixture, Control Center 테스트 33개, Go 빌드·테스트) | 성공 |
| `lightlix-llm` 패키지 검사 | 성공 |
| ISO 빌드, NVIDIA 모듈과 이미지 정책 검사 | 성공. `lightlix-desktop`, `lightlix-control-center`, `labwc`, `greetd`, `tuigreet`, `foot`, `fonts-nanum`, `openssh-server`, `mokutil` 설치와 greetd 설정 확인 |
| UEFI 부팅(CD, USB 저장 장치) | 성공 |
| CPU 추론 E2E | 성공 |
| 데스크톱 세션 E2E | 성공. 아래 상세 |

앞선 두 실행의 실패는 모두 테스트 환경 문제였고 제품 결함이 아니었다.

- 38187389114: 빌더 이미지에 `python3`와 `gettext`가 없어 `lightlix-control-center`를 빌드하지 못했다. 빌더 이미지에 추가해 고쳤다.
- 38187565545: 모드 전환은 성공했지만 직렬 콘솔에 섞인 systemd 로그 줄 때문에 드라이버가 JSON 파싱에 실패했다. 드라이버를 고쳤다.
- 38188676327: 로그인 직후 5초 시점의 화면이 검은색(커서만)이어서 실패했다. 창이 더 늦게 그려졌으며, 창이 뜰 때까지 기다리게 드라이버를 고친 뒤 통과했다.

## 데스크톱 세션 E2E가 확인한 것

QEMU 10, KVM, UEFI, virtio-vga, 직렬 콘솔과 모니터 소켓으로 실행한다. 로그와 화면 캡처는 CI 아티팩트 `lightlix-qemu-e2e-logs`의 `qemu-desktop/`에 있다.

1. 이미지는 서버 모드(`lightlix mode`가 `server`, 기본 부팅 대상 `multi-user.target`)로 부팅하고 greetd는 실행 중이 아니다.
2. `systemctl cat greetd.service`에 `--config /etc/greetd/lightlix.toml` 드롭인이 적용되어 있다. 한글 글꼴(`fc-list :lang=ko`)이 있다.
3. `lightlix mode desktop`이 `desktop`을 돌려주고 greetd와 tuigreet가 실행되며 기본 부팅 대상이 `graphical.target`로 바뀐다.
4. 가상 모니터 캡처(1280x800)에 tuigreet 로그인 화면("Authenticate into Lightlix", "Username:")이 그려진다.
5. QEMU 모니터로 보낸 키 입력으로 로그인하면 `labwc`와 `lightlix-control-center --autostart`가 사용자 세션에서 실행된다.
6. 가상 모니터 캡처에 Control Center 대시보드 창이 그려진다("No NVIDIA GPU found" 안내 배너와 CPU 폴백 문구 포함).
7. `lightlix mode server`가 `server`를 돌려주고 labwc와 greetd가 내려가며 기본 부팅 대상이 `multi-user.target`으로 돌아온다.

## 측정값 (참고용)

같은 실행에서 얻은 값이다. QEMU/KVM, vCPU 2, 메모리 4 GiB, GPU 없음, 부팅 직후 짧은 시간 뒤의 값이며 **실기 값이 아니다**. `used`는 `MemTotal - MemAvailable`이다.

| 상태 | used | 비고 |
|---|---|---|
| 서버 모드 (로그인 직후 5초) | 약 374 MiB | 라이브 세션, `lightlixd` 실행 중 |
| 데스크톱 모드, Control Center 창이 열린 상태 | 약 618 MiB | labwc RSS 약 170 MiB, Control Center RSS 약 296 MiB |
| 서버 모드로 되돌린 뒤 | 약 393 MiB | |

서버 모드 값은 목표 300 MB보다 크다. 이전 측정(0.1.0, 2 GiB TCG, NIC 없음, 234 MiB)과 조건이 달라 같은 기준으로 비교할 수 없다. 이 값이 목표를 넘는 원인은 확인하지 않았다. 목표 달성 여부는 Phase 5의 실측에서 판단한다. 데스크톱 값은 목표 700 MB 안이지만 창을 연 채 측정한 단일 값이다.

## 로컬에서 확인한 것

- Debian 13 컨테이너의 Go `gofmt`, `go vet`, `go test -race`: api, supervisor, config, firstboot, recommend, cmd 통과.
- `make test-gui`: API 클라이언트 9개, 표시 논리 15개, 번역 카탈로그 4개, GTK 스모크 3개(가짜 데몬에 대한 세 창과 마법사 전 단계).
- `tests/nvidia/mok.sh`: `lightlix-mok`의 분기를 명령 스텁으로 검사.
- 패키지 빌드: `lightlix-desktop`, `lightlix-control-center`(.mo 포함). `greetd` 드롭인은 systemd 257.13의 `systemd-analyze verify`를 통과했다.
- shellcheck 0.9.0, actionlint.

## 확인하지 못한 것

- **실기 하드웨어**: 실제 모니터 표시, 입력 장치, 한글 입력기, iGPU 렌더링 지정, 절전.
- **실제 NVIDIA 오프로드**, Secure Boot가 켜진 상태의 부팅.
- **`lightlix-mok`의 실제 동작**: `mokutil --import`와 MokManager 화면, 재부팅 뒤 모듈 로드. 설치기(Phase 4) 이후에 확인할 수 있다.
- **첫 부팅 마법사의 영속성**: 라이브 세션에서는 재부팅하면 완료 기록이 사라진다. 설치 뒤 유지되는지는 Phase 4에서 확인한다.
- **SSH**: 켜기와 끄기의 실제 접속, 호스트 키 생성. 명령 스텁 수준만 확인했다.
- 모델 내려받기 진행률과 채팅 화면의 사용성은 가짜 데몬으로만 확인했다.
- 화면 캡처 판정은 "무언가 그려졌다"만 본다. 레이아웃과 글꼴 품질은 사람이 캡처를 봐야 한다.
