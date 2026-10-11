# Phase 2 검증 기록

확인 날짜: 2026-10-11. 작업 브랜치는 `phase-2`, PR의 기준 브랜치는 `phase-1-fixes`다.
NVIDIA 실 하드웨어에서는 확인하지 않았다. 아래 로컬 결과와 실제 ISO·CUDA 경로의 CI 결과를 구분한다.

## 인수 시점과 구현

`1807605`까지 데몬 라이브러리, 공유 인터페이스, REST API, systemd 계층이 커밋되어 있었다.
Claude 워커가 남긴 CLI와 llama.cpp 파이프라인을 검토하고 이어서 패키징과 QEMU CPU 추론 검사를 구현했다.
CLI 구현은 `daemon/cmd/lightlix`, 유닉스 소켓 클라이언트는 `daemon/internal/client`에 있다.

인수 과정에서 모델 요청의 `PullError`와 Hugging Face 인증 실패가 모두 `internal`로 바뀌는 문제를 수정했다.
인증 실패, gated 저장소, 파일 선택 필요, 이름 충돌 등을 HTTP 상태와 오류 코드로 보존한다.
알 수 없는 내부 오류의 상세 내용은 API 응답에 노출하지 않는다. 회귀 검사는 `daemon/internal/api/errors_test.go`에 있다.

## 실제로 확인한 로컬 결과

### CLI와 Go

- Debian 13 arm64 컨테이너에서 CLI·클라이언트의 gofmt 확인과 `go vet -mod=vendor`가 통과했다.
- `go test -race -mod=vendor -count=1 ./cmd/lightlix/... ./internal/client/...`가 통과했다. 각각 2.325초, 3.647초였다.
- 실제 패키지 빌드가 실행한 전체 Go 테스트에서 CLI, API, 다운로드, 모델 관리, supervisor 등 모든 패키지가 통과했다. 새 API 오류 회귀 검사도 포함됐다.
- Claude CLI 워커의 실제 데몬 연동 결과도 검토했다. systemd·nftables가 없는 컨테이너에서 상태 조회와 오류 응답을 확인한 범위이며, 실제 추론 서비스 실행 성공을 뜻하지 않는다.

첫 전체 빌드 시도는 검사 도중 root가 API 파일을 복사해 의존성 입력이 바뀌면서 실패했다.
그 결과는 성공으로 계산하지 않는다. 이후 패키징 워커의 고정 사본에서 전체 Go 테스트가 통과했다.

### llama.cpp

이 결과는 완료된 llama 워커의 원본 빌드·검사 보고서를 회수하고 코드와 대조한 결과다.

- 고정 커밋의 네이티브 arm64 CPU 소스 빌드가 약 43분에 통과했다. 패키지 크기는 4,133,040바이트였고 CPU 백엔드 변형 여덟 개가 들어 있었다.
- 모든 ELF의 RUNPATH `$ORIGIN`, 빌드 트리를 제거한 뒤 복사본의 `--version`과 `--list-devices`, 헤더·컴파일러·정적 라이브러리 부재를 확인했다.
- 같은 입력의 캐시 재실행은 빌드를 건너뛰었고 약 1.8초였다.
- CUDA 없는 깨끗한 Debian 컨테이너에서 작은 GGUF를 실제로 내려받고 SHA-256을 확인한 뒤 `/health` 200과 비어 있지 않은 채팅 응답을 얻었다. 실제 추론을 포함한 패키지 검사는 약 17초에 통과했다.
- amd64 CUDA 빌더 이미지 설치와 CMake 구성은 통과했다. 이 검사는 CUDA 커널 전체 컴파일을 포함하지 않았다.
- 추가한 제3자 고지 추출을 고정 소스의 파일로 Debian 컨테이너에서 실행해 통과했다. 최종 `.deb`에 포함되는지는 CI가 검사한다.

CPU 스모크 패키지와 임시 llama 빌더 이미지는 원래 워커가 정리했다. 기존 `out/drive-upload`는 보존했다.

### 정적 검사

인수 초기 파일에서 `make lint`, GPU 진단 스텁 23개, Docker shellcheck 0.9.0과 actionlint가 통과했다.
라이선스 스크립트 추가 뒤 shellcheck 0.9.0도 통과했다. 패키징·E2E 변경 뒤 전체 shellcheck 0.9.0과 actionlint도 통과했다.

### 패키징과 시스템 통합

- 실제 arm64 `lightlix-daemon` 패키지는 2,790,088바이트였다. 버전 주입, glibc와 같은 버전의 Lightlix 런타임 의존성, 설치 파일과 생성된 maintscript를 확인했다.
- sysusers를 tmpfiles보다 먼저 실행하는 순서, 비특권 추론 사용자의 모델·키 읽기, 다른 사용자의 키 접근 거부, 제어 소켓 그룹과 서비스 활성화 링크를 검사해 통과했다.
- 실제 패키지의 유닛은 `systemd-analyze verify` 종료 0이었다. 별도 llama-server와 GPU 진단 실행 파일은 이 컨테이너 검사에서 스텁으로 대체했다.
- 실제 nft 기본 규칙을 적용한 뒤 잘못된 저장 파일 적용을 시도했다. 실패 전후 테이블이 같았고 drop 정책이 남았다.
- GPU 권한 검사와 사용자 이름을 바꾼 live-config 훅은 fixture로 통과했다. 실제 NVIDIA 장치 접근을 증명하지 않는다.
- 최종 `make test-daemon`의 전체 Go build, vet, test가 종료 0이었다. 임시 컨테이너와 해당 워커의 이미지 태그는 제거했다.

패키지 검증 중 Go의 압축 DWARF를 처리하지 못하는 `dh_dwz`, compat 13의 sysusers 순서, 라이선스 파일 자동 압축 문제를 실제로 발견하고 수정했다.
이 로컬 패키지 검사는 버전 변경 전의 결과이며 최종 버전의 amd64 패키지는 ISO CI가 다시 빌드·검사한다.

### QEMU 드라이버

root가 데몬 RSS 기록을 추가하고 fixture의 준비 표시 경쟁을 수정한 뒤 Debian 컨테이너에서 고정 사본의 테스트 10개가 32.684초에 모두 통과했다.
정상 흐름, 권한·서비스·해시·생성 응답 오류, QEMU 종료, 명령·직렬·전체 시간 초과, 신호 정리와 KVM 초기화 실패의 TCG 재시도를 검사했다.
실제 ISO와 모델 추론은 이 fixture에서 실행하지 않았다.

## CI에서 실제로 확인할 항목

- amd64 CUDA 모듈 전체 컴파일과 런타임 의존성, CUDA 모듈이 들어 있는 패키지의 CPU 추론
- 최종 ISO의 데몬·CLI·llama 패키지 설치, 모델·API 키 권한, 서비스 활성화와 기본 방화벽
- OVMF CD와 USB 저장 장치 부팅
- 라이브 사용자로 로그인한 뒤 Hugging Face 다운로드, 실제 파일 SHA-256, 기본 모델 저장, 자동 CPU 계획, 실제 생성과 정상 중지
- 데몬 소켓의 그룹 권한, 접근 거부, 서비스 오류 부재, 유휴 데몬 RSS 기록

QEMU E2E는 `out/qemu-e2e/run-*`에 직렬 원문, 단계별 응답, 명령 종료 코드, `result.json`을 남긴다.
제어된 fixture는 드라이버의 판정·시간 제한·정리만 검사하며 실제 ISO의 추론 성공으로 계산하지 않는다.

## 남은 실기 검증

RTX 3060 Laptop을 포함한 RTX 30/40/50의 NVIDIA 모듈 로드, CUDA 오프로드, 장치 권한, 토큰 속도, 온도와 전력은 실기가 필요하다.
Secure Boot, 실기 Wi-Fi·USB 부팅, 장시간 유휴 램 목표도 아직 확인하지 않았다.
GUI·설치기·업데이트는 후속 Phase이며 이 작업에서 시작하지 않는다.
