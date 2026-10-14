// Package updatectl 은 데몬이 opereta-update 헬퍼를 시작하고 그 상태를 읽는 얇은 계층이다.
//
// 갱신은 데몬의 하드닝 밖에서, 데몬이 다시 시작되어도 이어지는 별도 transient 유닛으로 돈다.
// 데몬은 유닛을 띄우고(Start) 헬퍼가 남긴 state.json 과 check.json 을 읽기만 한다(Info).
package updatectl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/agentupdate"
	"github.com/nxodh/operetaos/daemon/internal/boot"
	"github.com/nxodh/operetaos/daemon/internal/platform"
	"github.com/nxodh/operetaos/daemon/internal/update"
)

// 기본값이다.
const (
	DefaultHelper   = "/usr/lib/opereta/opereta-update"
	DefaultUnit     = "opereta-update-run.service"
	DefaultLiveDir  = "/run/live"
	DefaultKernelRe = "/proc/sys/kernel/osrelease"
)

// 요청할 수 있는 동작이다.
const (
	ActionCheck    = "check"
	ActionApply    = "apply"
	ActionRollback = "rollback"
	// ActionCUDARuntime 은 CUDA 런타임(cudart, cublas)을 NVIDIA 저장소에서 받아 설치한다.
	ActionCUDARuntime = "cuda_runtime"
	// 코딩 에이전트 Deberopa 의 갱신이다. OS 갱신과 달리 재부팅이 필요 없다.
	ActionDeberopaCheck   = "deberopa_check"
	ActionDeberopaUpdate  = "deberopa_update"
	ActionDeberopaAutoOn  = "deberopa_auto_on"
	ActionDeberopaAutoOff = "deberopa_auto_off"
	// 부팅 항목 동작이다. boot_next 와 boot_order 는 "boot_next:opereta" 처럼 대상을 뒤에 붙인다.
	ActionBootNext      = "boot_next"
	ActionBootOrder     = "boot_order"
	ActionBootClearNext = "boot_clear_next"
	ActionBootRepair    = "boot_repair"
	ActionBootCleanup   = "boot_cleanup"
	ActionBootAutoOn    = "boot_auto_on"
	ActionBootAutoOff   = "boot_auto_off"
)

// helperArgs 는 동작에 해당하는 헬퍼 명령 인자다.
var helperArgs = map[string][]string{
	ActionCheck:           {"check"},
	ActionApply:           {"apply"},
	ActionRollback:        {"rollback"},
	ActionCUDARuntime:     {"cuda-runtime"},
	ActionDeberopaCheck:   {"deberopa", "check"},
	ActionDeberopaUpdate:  {"deberopa", "apply"},
	ActionDeberopaAutoOn:  {"deberopa", "auto", "on"},
	ActionDeberopaAutoOff: {"deberopa", "auto", "off"},
	ActionBootClearNext:   {"boot", "clear-next"},
	ActionBootRepair:      {"boot", "repair"},
	ActionBootCleanup:     {"boot", "cleanup"},
	ActionBootAutoOn:      {"boot", "auto", "on"},
	ActionBootAutoOff:     {"boot", "auto", "off"},
}

// helperFor 는 동작 문자열에서 헬퍼 인자를 만든다. "boot_next:opereta" 같은 대상이 붙는 동작도 처리한다.
func helperFor(action string) ([]string, bool) {
	if a, ok := helperArgs[action]; ok {
		return a, true
	}
	name, target, found := strings.Cut(action, ":")
	if !found || !boot.ValidTarget(target) {
		return nil, false
	}
	switch name {
	case ActionBootNext:
		return []string{"boot", "next", strings.ToLower(target)}, true
	case ActionBootOrder:
		return []string{"boot", "order", strings.ToLower(target)}, true
	}
	return nil, false
}

// 오류 코드다.
const (
	CodeInvalidAction = "invalid_action"
	CodeUnavailable   = "update_unavailable"
)

// Error 는 코드를 가진 오류다. update.Error 의 코드도 같은 이름으로 전달한다.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Controller 는 갱신 헬퍼를 시작하고 상태를 읽는다.
type Controller struct {
	Runner platform.Runner
	// Helper 는 헬퍼 경로다. 비어 있으면 DefaultHelper 다.
	Helper string
	// StateDir 은 헬퍼의 상태 디렉터리다. 비어 있으면 update.DefaultStateDir 다.
	StateDir string
	// Unit 은 transient 유닛 이름이다.
	Unit string
	// LiveDir 이 있으면 라이브 세션이다.
	LiveDir string
	// OSRelease 는 실행 중인 커널 릴리스를 읽는 경로다.
	OSRelease string
	// OSReleaseFile 은 Opereta 버전(VERSION_ID)을 읽는 os-release 경로다. 비어 있으면 /etc/os-release 다.
	OSReleaseFile string
	// SourcesFile 은 Opereta 패키지 저장소 소스 파일이다. 비어 있으면 update.DefaultChannelFile 이다.
	SourcesFile string
	// NvidiaConf 는 CUDA 런타임의 고정 버전을 적은 파일이다. 비어 있으면 update.DefaultNvidiaConf 다.
	NvidiaConf string
	// DeberopaDir 은 Deberopa 갱신의 상태 디렉터리다. 비어 있으면 agentupdate.DefaultDir 다.
	DeberopaDir string
	// BootEFIDir, BootESPDir, BootStateDir 은 부팅 상태를 읽는 경로다. 비어 있으면 boot 패키지의 기본값이다.
	BootEFIDir, BootESPDir, BootStateDir string
	// HelperExists 는 테스트가 바꾼다. nil 이면 파일이 있는지 본다.
	HelperExists func(path string) bool
}

func (c *Controller) helper() string {
	if c.Helper != "" {
		return c.Helper
	}
	return DefaultHelper
}

func (c *Controller) stateDir() string {
	if c.StateDir != "" {
		return c.StateDir
	}
	return update.DefaultStateDir
}

func (c *Controller) unit() string {
	if c.Unit != "" {
		return c.Unit
	}
	return DefaultUnit
}

func (c *Controller) liveDir() string {
	if c.LiveDir != "" {
		return c.LiveDir
	}
	return DefaultLiveDir
}

func (c *Controller) osRelease() string {
	if c.OSRelease != "" {
		return c.OSRelease
	}
	return DefaultKernelRe
}

func (c *Controller) osReleaseFile() string {
	if c.OSReleaseFile != "" {
		return c.OSReleaseFile
	}
	return "/etc/os-release"
}

// readVersionID 는 os-release 의 VERSION_ID 를 돌려준다. 없으면 빈 문자열이다.
func readVersionID(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(line), "VERSION_ID="); ok {
			return strings.Trim(strings.TrimSpace(v), `"'`)
		}
	}
	return ""
}

func (c *Controller) helperExists() bool {
	if c.HelperExists != nil {
		return c.HelperExists(c.helper())
	}
	fi, err := os.Stat(c.helper())
	return err == nil && !fi.IsDir()
}

// Info 는 GET /v1/update 의 응답이다.
type Info struct {
	// Available 은 이 시스템에서 갱신을 쓸 수 있다는 뜻이다. 헬퍼가 없거나 라이브 세션이면 거짓이다.
	Available bool `json:"available"`
	// Reason 은 Available 이 거짓일 때의 이유 코드다(live_session, update_unavailable).
	Reason string `json:"reason,omitempty"`
	Live   bool   `json:"live"`
	// Running 은 갱신 유닛이 지금 돌고 있다는 뜻이다.
	Running       bool   `json:"running"`
	RunningKernel string `json:"running_kernel,omitempty"`
	// OSVersion 은 설치된 Opereta 버전이다(os-release 의 VERSION_ID).
	OSVersion string `json:"os_version,omitempty"`
	// Channel 은 Opereta 패키지 저장소(OS 업데이트 채널)의 상태다. 꺼져 있으면 새 Opereta 버전은 보이지 않는다.
	Channel update.Channel `json:"channel"`
	// CUDARuntime 은 GPU 가속에 필요한 CUDA 런타임의 설치 상태다. 이미지에는 들어 있지 않고 NVIDIA 에서 직접 받는다.
	CUDARuntime update.CUDARuntime `json:"cuda_runtime"`
	// Deberopa 는 코딩 에이전트의 설치 버전, 최신 버전, 자동 갱신 설정이다.
	Deberopa agentupdate.Info `json:"deberopa"`
	// Boot 는 UEFI 부팅 항목, 부팅 순서, 문제 진단이다.
	Boot      boot.Info           `json:"boot"`
	State     update.State        `json:"state"`
	LastCheck *update.CheckResult `json:"last_check,omitempty"`
	// RollbackAvailable 은 되돌릴 수 있는 갱신 기록이 있다는 뜻이다.
	RollbackAvailable bool `json:"rollback_available"`
}

// Info 는 현재 갱신 상태를 읽는다.
func (c *Controller) Info(ctx context.Context) (Info, error) {
	var out Info
	if _, err := os.Stat(c.liveDir()); err == nil {
		out.Live = true
		out.Reason = update.CodeLiveSession
	} else if !c.helperExists() {
		out.Reason = CodeUnavailable
	} else {
		out.Available = true
	}
	if b, err := os.ReadFile(c.osRelease()); err == nil {
		out.RunningKernel = strings.TrimSpace(string(b))
	}
	out.OSVersion = readVersionID(c.osReleaseFile())
	out.Channel = update.ReadChannel(c.SourcesFile)
	out.Deberopa = (&agentupdate.Updater{Dir: c.DeberopaDir}).ReadInfo()
	if c.Runner != nil {
		bc := &boot.Controller{Runner: c.Runner, EFIDir: c.BootEFIDir, ESPDir: c.BootESPDir, StateDir: c.BootStateDir}
		out.Boot = bc.Status(ctx)
	}
	if c.Runner != nil {
		eng := &update.Engine{Cfg: update.Config{NvidiaConf: c.NvidiaConf}, Run: c.Runner}
		out.CUDARuntime = eng.CUDARuntimeStatus(ctx)
	}
	st, err := readJSON[update.State](filepath.Join(c.stateDir(), "state.json"))
	switch {
	case err == nil:
		out.State = *st
	case errors.Is(err, fs.ErrNotExist):
		out.State = update.State{Phase: update.PhaseIdle}
	default:
		return out, fmt.Errorf("updatectl: read state: %w", err)
	}
	if ck, err := readJSON[update.CheckResult](filepath.Join(c.stateDir(), "check.json")); err == nil {
		out.LastCheck = ck
	}
	if fi, err := os.Stat(filepath.Join(c.stateDir(), "rollback.json")); err == nil && fi.Size() > 0 {
		out.RollbackAvailable = true
	}
	out.Running = c.active(ctx)
	// 헬퍼가 비정상 종료해 running 으로 남은 상태는 실제 유닛이 없으면 실패로 보여 준다.
	if out.State.Phase == update.PhaseRunning && !out.Running {
		out.State.Phase = update.PhaseFailed
		if out.State.Code == "" {
			out.State.Code = update.CodeInternal
			out.State.Message = "the update was interrupted"
		}
	}
	return out, nil
}

func readJSON[T any](path string) (*T, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var v T
	if err := json.Unmarshal(b, &v); err != nil {
		return nil, err
	}
	return &v, nil
}

// active 는 갱신 유닛이 돌고 있는지 본다.
func (c *Controller) active(ctx context.Context) bool {
	res, err := c.Runner.Run(ctx, platform.Command{Name: "systemctl", Args: []string{"is-active", "--quiet", c.unit()}, Timeout: 10 * time.Second})
	return err == nil && res.ExitCode == 0
}

// Start 는 갱신 동작을 transient 유닛으로 시작하고 바로 돌려준다.
func (c *Controller) Start(ctx context.Context, action string, reboot bool) error {
	hargs, known := helperFor(action)
	if !known {
		return &Error{CodeInvalidAction, fmt.Sprintf("action must be check, apply, rollback, cuda_runtime, deberopa_check|update|auto_on|auto_off or boot_next:<target>|boot_order:<target>|boot_clear_next|boot_repair|boot_cleanup|boot_auto_on|boot_auto_off, not %q", action)}
	}
	// CUDA 런타임은 라이브 세션에서도 받을 수 있다(재부팅하면 사라진다). 나머지 동작은 설치된 시스템에서만 한다.
	if _, err := os.Stat(c.liveDir()); err == nil && action != ActionCUDARuntime {
		return &Error{update.CodeLiveSession, "this is a live session; updates apply to an installed system"}
	}
	if !c.helperExists() {
		return &Error{CodeUnavailable, "the update helper is not installed"}
	}
	if c.active(ctx) {
		return &Error{update.CodeBusy, "an update operation is already running"}
	}
	if action == ActionApply {
		if info, err := c.Info(ctx); err == nil && info.State.Phase == update.PhasePendingBoot {
			return &Error{update.CodePendingBoot, "the previous update is waiting for a reboot; reboot first so it can be verified"}
		}
	}
	args := []string{"--unit=" + strings.TrimSuffix(c.unit(), ".service"), "--collect", "--service-type=exec", "--quiet",
		"--description=Opereta update (" + action + ")", c.helper()}
	args = append(args, hargs...)
	args = append(args, "--json")
	if reboot && (action == ActionApply || action == ActionRollback) {
		args = append(args, "--reboot")
	}
	res, err := c.Runner.Run(context.WithoutCancel(ctx), platform.Command{Name: "systemd-run", Args: args, Timeout: 30 * time.Second, MaxOutput: 8 << 10})
	if err != nil {
		return &Error{CodeUnavailable, fmt.Sprintf("systemd-run: %v", err)}
	}
	if res.ExitCode != 0 {
		return &Error{CodeUnavailable, fmt.Sprintf("systemd-run exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))}
	}
	return nil
}
