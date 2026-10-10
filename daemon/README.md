# lightlixd (데몬)

- 확인 날짜: 2026-10-10
- 상태: 초안. 구현은 Phase 2 예정이다.

- **책임(초안)**: 로컬 NVIDIA GPU의 상태를 NVML로 읽어 다른 구성 요소에 제공한다. 제공 방식(소켓, HTTP 등)과 세부 기능은 Phase 2에서 정한다.
- **언어와 빌드**: Go. Go 1.24(trixie `golang-go`)로 빌드한다. `go.mod`의 `go` 지시어는 1.20이다.
- **NVIDIA 바인딩**: `github.com/NVIDIA/go-nvml`을 **v0.13.4-1**로 고정한다.
- **링크 방식**: `CGO_ENABLED=1`로 빌드한 glibc 동적 링크 실행 파일이다. "단일 바이너리"는 파일이 하나라는 뜻이며 완전 정적 바이너리가 아니다.
- **NVML 라이브러리**: 실행 시 `libnvidia-ml.so.1`(패키지 `libnvidia-ml1`)을 `dlopen`으로 연다. 이 라이브러리가 없으면 `nvml.Init()`이 오류를 낸다.
- **요구 사항**: 드라이버가 없거나 아직 로드되지 않은 부팅에서도 데몬은 떠야 한다. NVML 초기화 오류는 "GPU 없음" 상태로 처리한다.
- **메모리 목표**: 유휴 상태에서 30 MB 이하([docs/memory.md](../docs/memory.md)).
- **구현 Phase**: 2.

근거: https://raw.githubusercontent.com/NVIDIA/go-nvml/v0.13.4-1/pkg/nvml/nvml.go (`#cgo linux LDFLAGS`, `defaultNvmlLibraryName = "libnvidia-ml.so.1"`). go-nvml README는 라이브러리가 없으면 런타임 오류가 난다고 적는다. Go 패키지 버전 — https://api.ftp-master.debian.org/madison?package=golang-go&s=trixie,trixie-updates,trixie-backports,trixie-security&text=on
