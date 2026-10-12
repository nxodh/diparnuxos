// Package update 는 커널과 NVIDIA 드라이버를 함께 갱신하고 되돌리는 diparnux-update 의 로직이다.
//
// 이 패키지는 root 로 도는 헬퍼(cmd/diparnux-update)가 쓴다. 데몬은 하드닝 때문에 /boot 와 /etc 에 쓸 수 없어 직접 쓰지 않는다.
// 모든 외부 명령은 platform.Runner 로 실행하므로 실제 apt, dkms, grub 없이 테스트할 수 있다.
//
// 흐름은 설계서(docs/phase-4-design.md 5절)와 같다. 새 커널을 먼저 설치해 DKMS 빌드를 확인하고, 통과했을 때만
// grub-reboot 로 한 번 부팅해 본 뒤, 부팅 뒤 검증이 통과해야 기본 커널을 바꾼다.
package update

import "fmt"

// Phase 는 state.json 의 phase 값이다.
const (
	// PhaseIdle 은 진행 중인 갱신이 없다.
	PhaseIdle = "idle"
	// PhaseRunning 은 apply 나 rollback 이 실행 중이다.
	PhaseRunning = "running"
	// PhasePendingBoot 는 설치가 끝났고 재부팅 뒤 검증을 기다린다.
	PhasePendingBoot = "pending_boot"
	// PhaseCommitted 는 부팅 뒤 검증이 통과해 확정됐다.
	PhaseCommitted = "committed"
	// PhaseFailed 는 마지막 작업이 실패했다. Code 에 원인이 있다.
	PhaseFailed = "failed"
	// PhaseRolledBack 은 롤백이 끝났고 재부팅을 기다린다.
	PhaseRolledBack = "rolled_back"
)

// 오류 코드다. CLI 와 API 가 그대로 사용자에게 보여 준다.
const (
	CodeLiveSession       = "live_session"
	CodeBusy              = "busy"
	CodePendingBoot       = "pending_reboot"
	CodeDiskSpace         = "disk_space"
	CodeNetwork           = "network"
	CodeDpkgLocked        = "dpkg_locked"
	CodeUnexpectedRemoval = "unexpected_removal"
	CodeKernelInstall     = "kernel_install_failed"
	CodeDKMS              = "dkms_failed"
	CodeUpgrade           = "upgrade_failed"
	CodeGrub              = "grub_failed"
	CodeRollbackNA        = "rollback_unavailable"
	CodeVerify            = "verify_failed"
	CodeNoPending         = "no_pending_update"
	CodeInternal          = "internal"
)

// Error 는 코드를 가진 오류다.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// PkgVersion 은 패키지 이름과 설치된 버전이다.
type PkgVersion struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// Change 는 계획에 들어 있는 패키지 변경 하나다.
type Change struct {
	Name string `json:"name"`
	// From 은 설치된 버전이다. 새로 설치되면 비어 있다.
	From string `json:"from,omitempty"`
	To   string `json:"to"`
	New  bool   `json:"new,omitempty"`
}

// Plan 은 apt 시뮬레이션으로 얻은 갱신 계획이다.
type Plan struct {
	// Changes 는 시뮬레이션이 설치하거나 올릴 패키지 전부다.
	Changes []Change `json:"changes"`
	// Removals 는 지워지는 패키지다. 관리 대상 밖이면 거절한다.
	Removals []string `json:"removals,omitempty"`
	// Kernels 는 새로 설치될 커널 릴리스(예: 6.12.112+deb13-amd64)다.
	Kernels []string `json:"kernels,omitempty"`
	// KernelPackages 는 새 커널과 함께 먼저 설치할 패키지 이름이다.
	KernelPackages []string `json:"kernel_packages,omitempty"`
	// DriverFrom 과 DriverTo 는 nvidia-kernel-open-dkms 의 설치 버전과 후보 버전이다. 바뀌지 않으면 DriverTo 는 비어 있다.
	DriverFrom string `json:"driver_from,omitempty"`
	DriverTo   string `json:"driver_to,omitempty"`
	// PolicyFrom 과 PolicyTo 는 diparnux-nvidia 의 설치 버전과 후보 버전이다. 드라이버 핀이 이 패키지에 들어 있어서
	// 드라이버를 올리려면 먼저 이 패키지를 올려야 한다.
	PolicyFrom string `json:"policy_from,omitempty"`
	PolicyTo   string `json:"policy_to,omitempty"`
	// UpToDate 가 참이면 할 일이 없다.
	UpToDate bool `json:"up_to_date"`
}

// State 는 /var/lib/diparnux/update/state.json 의 내용이다.
type State struct {
	Phase     string `json:"phase"`
	Operation string `json:"operation,omitempty"`
	Step      string `json:"step,omitempty"`
	Started   string `json:"started,omitempty"`
	Finished  string `json:"finished,omitempty"`
	Code      string `json:"code,omitempty"`
	Message   string `json:"message,omitempty"`
	// RebootRequired 는 새 구성을 쓰려면 재부팅이 필요하다는 뜻이다.
	RebootRequired bool  `json:"reboot_required"`
	Plan           *Plan `json:"plan,omitempty"`
	// NewKernel 은 한 번 부팅해 볼 커널 릴리스다. 커널이 바뀌지 않으면 비어 있다.
	NewKernel string `json:"new_kernel,omitempty"`
	// PreviousKernel 은 갱신 전에 실행 중이던 커널 릴리스다. 롤백 대상이다.
	PreviousKernel string `json:"previous_kernel,omitempty"`
	// PreviousDefault 는 갱신 전의 GRUB 저장 항목이다.
	PreviousDefault string `json:"previous_default,omitempty"`
	// DriverFrom 과 DriverTo 는 갱신 전과 후의 드라이버 버전이다.
	DriverFrom string `json:"driver_from,omitempty"`
	DriverTo   string `json:"driver_to,omitempty"`
	// GPUBefore 는 갱신 전 GPU 진단의 종료 코드다(0 사용 가능, 1 GPU 없음, 2 GPU 는 있으나 못 씀, 3 진단 오류).
	// 갱신 전부터 못 쓰던 GPU(예: Secure Boot 가 모듈을 막음)는 재부팅 뒤 확인에서 갱신의 실패로 보지 않는다.
	GPUBefore *int `json:"gpu_before,omitempty"`
	// Before 는 갱신 전에 설치되어 있던 관리 대상 패키지와 버전이다. 롤백이 읽는다.
	Before []PkgVersion `json:"before,omitempty"`
	// Events 는 마지막 작업의 진행 기록이다.
	Events []Event `json:"events,omitempty"`
}

// Event 는 진행 상황 한 줄이다.
type Event struct {
	Time    string `json:"time"`
	Step    string `json:"step"`
	Message string `json:"message,omitempty"`
}

// Status 는 Status 호출의 결과다.
type Status struct {
	State
	Live          bool   `json:"live"`
	RunningKernel string `json:"running_kernel,omitempty"`
	GrubDefault   string `json:"grub_default,omitempty"`
	// Kernels 는 설치된 커널 릴리스다. 오래된 순서다.
	Kernels []string `json:"kernels,omitempty"`
}

// CheckResult 는 마지막 check 의 결과다. check.json 에 저장되어 데몬과 GUI 가 읽는다.
type CheckResult struct {
	Time  string `json:"time"`
	Plan  *Plan  `json:"plan,omitempty"`
	Error *Error `json:"error,omitempty"`
}
