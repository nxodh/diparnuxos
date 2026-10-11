# lightlixd (데몬)

- 확인 날짜: 2026-10-11
- 상태: Phase 2 데몬과 CLI 구현, Debian 패키징 구성이 있다. 실제 ISO 부팅과 GPU 추론 결과는 별도 검증한다.

- **책임**: 유닉스 소켓 REST API로 GPU 상태, 모델 내려받기, llama-server 제어, API 키와 방화벽을 관리한다. CLI는 같은 모듈의 `cmd/lightlix`다.
- **언어와 빌드**: Go. Go 1.24(trixie `golang-go`)로 빌드한다. `go.mod`의 `go` 지시어도 1.24다.
- **NVIDIA 바인딩**: `github.com/NVIDIA/go-nvml`을 **v0.13.4-1**로 고정한다.
- **링크 방식**: `CGO_ENABLED=1`로 빌드한 glibc 동적 링크 실행 파일이다. "단일 바이너리"는 파일이 하나라는 뜻이며 완전 정적 바이너리가 아니다.
- **NVML 라이브러리**: 실행 시 `libnvidia-ml.so.1`(패키지 `libnvidia-ml1`)을 `dlopen`으로 연다. 이 라이브러리가 없으면 `nvml.Init()`이 오류를 낸다.
- **요구 사항**: 드라이버가 없거나 아직 로드되지 않은 부팅에서도 데몬은 떠야 한다. NVML 초기화 오류는 "GPU 없음" 상태로 처리한다.
- **메모리 목표**: 유휴 상태에서 30 MB 이하([docs/memory.md](../docs/memory.md)).
- **구현 Phase**: 2.

`make deb`는 [lightlix-daemon 패키지](../packages/README.md)를 만들며 `lightlixd`를 `/usr/sbin`, CLI `lightlix`를 `/usr/bin`에 설치한다.
두 바이너리는 `CGO_ENABLED=1`, `-mod=vendor`, `-trimpath`, `-buildvcs=false`로 빌드한다. 모듈을 내려받지 않으며 버전은 `lightlix.conf`에서 `-X main.version`으로 주입한다.
`make test-daemon`은 같은 Go 모듈 전체를 빌드하고 `go vet`, `go test -count=1`을 실행한다. Mac에는 도구를 설치하지 않고 Debian Docker 컨테이너 안에서 실행한다.

계정과 서비스 원본은 [dist/README.md](dist/README.md)에 있다. 모델 디렉터리는 `root:lightlix-llm` 2750이며 데몬의 `UMask=0027`과 함께 새 모델에 읽기 그룹을 상속한다.
API 키 파일은 데몬이 0640으로 저장하고 `lightlix-llm` 그룹으로 바꾼다. 설정과 키 메타데이터는 0600이며 일반 사용자가 읽지 못한다.
`lightlix` 그룹은 데몬을 통해 모델·설정·서비스를 관리할 권한을 준다. 라이브 사용자는 live-config 훅으로 이 그룹에 들어간다.

기본 방화벽은 저장 파일 유무와 무관하게 먼저 적용한다. 저장 파일이 잘못되어 `nft -f` 트랜잭션이 실패하면 기본 차단 규칙이 유지된다.
llama 서비스는 `lightlix-llm` 사용자로 실행하며 시작 전 `/dev/nvidia*`의 읽기·쓰기 권한을 검사한다. GPU가 없는 경우 CPU 경로를 허용한다.
실 NVIDIA 장치에서의 CUDA 초기화, GPU 오프로드와 하드닝 옵션 조합은 미검증이다.

근거: https://raw.githubusercontent.com/NVIDIA/go-nvml/v0.13.4-1/pkg/nvml/nvml.go (`#cgo linux LDFLAGS`, `defaultNvmlLibraryName = "libnvidia-ml.so.1"`). go-nvml README는 라이브러리가 없으면 런타임 오류가 난다고 적는다. Go 패키지 버전 — https://api.ftp-master.debian.org/madison?package=golang-go&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on
