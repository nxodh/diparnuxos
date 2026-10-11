// Package platform 은 데몬의 여러 패키지가 함께 쓰는 타입과 인터페이스를 모아 둔다.
//
// 시스템에 닿는 부분(명령 실행, NVML, 내려받기 작업)을 인터페이스로 두어,
// supervisor 와 api 가 실제 systemd, GPU, 네트워크 없이 테스트될 수 있게 한다.
// 이 패키지는 다른 내부 패키지를 import 하지 않는다.
package platform

import (
	"context"
	"time"
)

// Command 는 실행할 외부 명령 하나를 나타낸다. 셸을 거치지 않고 인자 배열로 실행한다.
type Command struct {
	// Name 은 실행 파일 경로나 이름이다.
	Name string
	// Args 는 인자 목록이다(Name 제외).
	Args []string
	// Env 는 추가할 환경 변수("KEY=VALUE")다. 비어 있으면 데몬의 환경을 그대로 쓴다.
	Env []string
	// Timeout 이 0 보다 크면 그 시간이 지났을 때 프로세스를 끝낸다.
	Timeout time.Duration
	// MaxOutput 은 stdout 과 stderr 각각에서 읽어 둘 최대 바이트 수다. 0 이면 구현의 기본 상한을 쓴다.
	MaxOutput int
}

// Result 는 명령 실행 결과다. 종료 코드가 0 이 아닌 것은 오류가 아니다(Run 의 error 는 실행 자체의 실패다).
type Result struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner 는 외부 명령(systemctl, journalctl, nft, llama-server, lightlix-gpu-check)을 실행한다.
type Runner interface {
	// Run 은 명령을 끝까지 실행한다. 실행 파일이 없거나 시작하지 못했거나 시간 초과면 error 를 돌려준다.
	Run(ctx context.Context, cmd Command) (Result, error)
}

// GPUProcess 는 GPU 메모리를 쓰는 프로세스 하나다.
type GPUProcess struct {
	PID     uint32
	UsedMiB int64
}

// GPUDevice 는 NVML 로 읽은 GPU 한 장의 현재 상태다.
type GPUDevice struct {
	// Index 는 NVML index 다. nvidia-smi 의 index 와 같다.
	Index int
	Name  string
	UUID  string
	// Arch 는 "ampere", "ada", "blackwell" 또는 "unknown(<n>)" 이다.
	Arch string
	// ComputeCap 은 "8.6" 같은 문자열이다. 읽지 못하면 빈 값이다.
	ComputeCap  string
	MemTotalMiB int64
	MemFreeMiB  int64
	MemUsedMiB  int64
	// 아래 값은 읽지 못하면 -1 이다.
	TemperatureC   int
	PowerMilliwatt int
	UtilGPUPercent int
	UtilMemPercent int
	// Processes 는 이 GPU 에서 연산 컨텍스트를 가진 프로세스다. 읽지 못하면 nil 이고 ProcessesKnown 이 false 다.
	Processes      []GPUProcess
	ProcessesKnown bool
}

// GPUSnapshot 은 NVML 조회 한 번의 결과다.
type GPUSnapshot struct {
	// Available 이 false 면 NVML 을 쓸 수 없다(라이브러리 없음, 드라이버 미적재 등). Reason 에 이유가 있다.
	Available bool
	Reason    string
	// DriverVersion 은 "595.91.07" 같은 문자열이다.
	DriverVersion string
	NVMLVersion   string
	// CUDADriverVersion 은 "13.2" 같은 문자열이다. 읽지 못하면 빈 값이다.
	CUDADriverVersion string
	Devices           []GPUDevice
}

// GPUProber 는 NVML 상태를 읽는다. 구현은 NVML 초기화가 실패한 뒤에는 다른 NVML 함수를 부르지 않아야 한다.
// 초기화에 실패한 채로 NVML 함수를 부르면 프로세스가 복구할 수 없이 종료된다.
type GPUProber interface {
	// Snapshot 은 오류를 돌려주지 않는다. NVML 을 쓸 수 없으면 Available=false 인 값을 돌려준다.
	Snapshot(ctx context.Context) GPUSnapshot
}

// MemInfo 는 시스템 메모리 상태다.
type MemInfo struct {
	TotalMiB     int64
	AvailableMiB int64
}

// JobState 는 내려받기 작업의 상태다.
type JobState string

// 내려받기 작업의 상태 값.
const (
	JobQueued   JobState = "queued"
	JobRunning  JobState = "running"
	JobDone     JobState = "done"
	JobFailed   JobState = "failed"
	JobCanceled JobState = "canceled"
)

// PullRequest 는 모델 내려받기 요청이다.
type PullRequest struct {
	// Repo 는 "owner/name" 형태의 Hugging Face 저장소다.
	Repo string
	// File 은 저장소 안의 GGUF 파일 경로다. 비어 있으면 저장소에 GGUF 가 하나뿐일 때만 그 파일을 고른다.
	File string
	// Revision 이 비어 있으면 "main" 이다.
	Revision string
}

// Job 은 내려받기 작업 하나의 현재 상태다.
type Job struct {
	ID         string
	State      JobState
	Repo       string
	File       string
	Revision   string
	BytesDone  int64
	BytesTotal int64
	// ModelID 는 끝났을 때 로컬 모델 ID(파일 이름)다.
	ModelID string
	// License 는 저장소가 밝힌 라이선스다. 없으면 빈 값이다.
	License string
	// ErrorCode 는 실패했을 때의 짧은 코드(예: "gated", "not_found", "insufficient_space", "checksum_mismatch")다.
	ErrorCode string
	Error     string
	Started   time.Time
	Finished  time.Time
}

// Jobs 는 모델 내려받기 작업을 관리한다.
type Jobs interface {
	// StartPull 은 작업을 만들고 바로 돌려준다. 내려받기는 백그라운드에서 진행된다.
	StartPull(ctx context.Context, req PullRequest) (Job, error)
	Get(id string) (Job, bool)
	List() []Job
	Cancel(id string) error
}
