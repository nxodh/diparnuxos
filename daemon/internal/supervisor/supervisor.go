// Package supervisor는 llama-server 유닛을 시작하고 멈추고 감시한다.
//
// 시작은 입력 검증, 기존 유닛 중지와 메모리 회수, 계획, 환경 파일 기록, systemctl start, health와 오프로드 검증 순서로 진행한다.
// GPU를 쓰도록 계획했는데 오프로드가 확인되지 않으면 유닛을 멈추고 실패로 끝낸다. CPU로 조용히 떨어지지 않는다.
// 한 번에 하나의 수명 주기 작업(Start, Stop, SetMode, 감시 점검)만 돈다.
//
// 모든 시스템 접근(명령, NVML, 시간, 대기, HTTP)은 Options로 주입받는다. 전역 상태는 없다.
package supervisor

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/apikeys"
	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/firewall"
	"github.com/nxodh/diparnuxos/daemon/internal/models"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// 기본값이다. Options에서 비워 두면 이 값을 쓴다.
const (
	defaultGPUStatusPath = "/run/diparnux/gpu-status"
	defaultEnvFilePath   = "/run/diparnux/llama-server.env"
	defaultServerBinary  = "/usr/lib/diparnux/llama/llama-server"
	defaultUnit          = "diparnux-llama.service"
	defaultFirewallPath  = "/var/lib/diparnux/firewall.nft"
	defaultProcRoot      = "/proc"
	defaultHealthTimeout = 10 * time.Minute
	defaultPollInterval  = 2 * time.Second
	defaultReclaimTime   = 30 * time.Second
	defaultReclaimPoll   = 500 * time.Millisecond
)

// 내부 상수다.
const (
	// startSlack은 Start 전체 시간 제한에 더하는 여유다(계획과 사전 검사 시간).
	startSlack = 2 * time.Minute
	// watchInterval은 실행 중인 유닛을 살펴보는 간격이다.
	watchInterval = 30 * time.Second
	// defaultLogLines와 maxLogLines는 Logs의 기본값과 상한이다.
	defaultLogLines = 200
	maxLogLines     = 2000
	// maxCommandOutput은 일반 명령 출력의 상한이다. maxJournalBytes는 저널 읽기의 상한이다.
	maxCommandOutput = 64 << 10
	maxJournalBytes  = 4 << 20
	// excerptBytes는 오류 메시지에 넣는 저널 발췌의 크기다.
	excerptBytes = 1536
	// healthRequestTimeout은 /health 한 번의 시간 제한이다.
	healthRequestTimeout = 3 * time.Second
	// unknownRAMMiB는 메모리 정보를 못 읽었을 때 쓰는 값이다. RAM 경고를 사실상 끈다.
	unknownRAMMiB = int64(1) << 40
	// 명령별 시간 제한이다.
	shortTimeout       = 10 * time.Second
	journalTimeout     = 30 * time.Second
	restartTimeout     = 2 * time.Minute
	stopTimeout        = 2 * time.Minute
	listDevicesTimeout = 60 * time.Second
	modeChangeTimeout  = 5 * time.Minute
	firewallTimeout    = 15 * time.Second
)

// State는 llama-server 유닛의 수명 주기 상태다.
type State string

// 상태 값이다.
const (
	StateStopped    State = "stopped"
	StateStarting   State = "starting"
	StateRunning    State = "running"
	StateRunningCPU State = "running_cpu_fallback"
	StateFailed     State = "failed"
)

// Offload는 GPU 오프로드 검증 결과다.
type Offload struct {
	// Requested 는 계획한 NGL 값("all", "auto", 정수 문자열, "0")이다.
	Requested string `json:"requested"`
	// Offloaded 와 Total 은 로그의 "offloaded N/M layers to GPU" 값이다. 로그가 없으면 0이다.
	Offloaded int `json:"offloaded"`
	Total     int `json:"total"`
	// Verified 는 GPU 오프로드나 CPU 전용 런타임 설정을 검증했다는 뜻이다.
	Verified bool `json:"verified"`
	// Note 는 VRAM 검사 결과 등 부가 설명이다.
	Note string `json:"note,omitempty"`
}

// Status는 llama-server의 현재 상태다. Plan 은 API 계층이 JSON으로 바꾼다.
type Status struct {
	State   State  `json:"state"`
	ModelID string `json:"model_id"`
	Host    string `json:"host"`
	Port    int    `json:"port"`
	// BackendPort 는 llama-server 가 실제로 여는 포트다. 게이트웨이를 쓰면 내부 포트(127.0.0.1)이고 Port 는 공개 포트다.
	BackendPort      int              `json:"backend_port"`
	LANExposed       bool             `json:"lan_exposed"`
	PID              int              `json:"pid"`
	UnitUnverifiable bool             `json:"unit_unverifiable,omitempty"`
	Since            time.Time        `json:"since"`
	Plan             *autoconf.Output `json:"-"`
	Offload          Offload          `json:"offload"`
	Warnings         []string         `json:"warnings"`
	LastError        *Error           `json:"last_error,omitempty"`
	// Restart 는 서버가 예상 밖으로 멈춘 뒤 데몬이 다시 시작하는 중일 때의 진행 상황이다.
	Restart *RestartInfo `json:"restart,omitempty"`
}

// StartRequest는 Start와 StartAsync의 입력이다. ModelID 가 비어 있으면 기본 모델을 쓴다.
type StartRequest struct {
	ModelID   string
	Overrides autoconf.Overrides
}

// startJob 는 시작 한 번의 검증된 입력과 이미 기록한 키 스냅샷 경로다.
type startJob struct {
	model      models.Model
	ov         autoconf.Overrides
	cfg        config.Config
	apiKeyFile string
}

// cpuRuntimeEvidence는 시작 또는 채택한 CPU 런타임의 PID와 환경 증거다.
type cpuRuntimeEvidence struct {
	pid uint32
	env map[string]string
}

// Options는 Supervisor의 의존성과 설정이다. 비워 둔 값은 기본값을 쓴다.
type Options struct {
	// Runner 는 systemctl, journalctl, nft, llama-server 를 실행한다. 필수다.
	Runner platform.Runner
	// GPU 는 NVML 스냅샷을 읽는다. nil 이면 NVML을 쓸 수 없는 것으로 본다.
	GPU platform.GPUProber
	// Store 는 모델 디렉터리다.
	Store models.Store
	// Keys 는 API 키 저장소다. nil 이면 키가 없는 것으로 본다.
	Keys *apikeys.Store
	// Config 는 현재 설정을 돌려준다. 필수다.
	Config func() config.Config
	// MemInfo 는 메모리 상태를 돌려준다. nil 이면 RAM 검사를 건너뛴다.
	MemInfo func() (platform.MemInfo, error)
	// GPUStatusPath 는 GPU 진단 상태 파일이다. 기본 /run/diparnux/gpu-status.
	GPUStatusPath string
	// EnvFilePath 는 systemd EnvironmentFile 경로다. 기본 /run/diparnux/llama-server.env.
	EnvFilePath string
	// ServerBinary 는 llama-server 실행 파일이다.
	ServerBinary string
	// Unit 은 llama-server systemd 유닛이다. 기본 diparnux-llama.service.
	Unit string
	// FirewallPath 는 생성한 nftables 규칙 파일의 경로다. 기본 /var/lib/diparnux/firewall.nft.
	FirewallPath string
	// FirewallExtras 는 규칙 파일에 더할 것을 돌려준다: 켜진 기능이 여는 포트와 사용자 규칙. nil 이면 둘 다 없다.
	FirewallExtras func() firewall.Extras
	// HTTP 는 /health 확인에 쓰는 클라이언트다. nil 이면 기본 클라이언트를 쓴다.
	HTTP *http.Client
	// Now 는 현재 시각이다. nil 이면 time.Now 를 쓴다.
	Now func() time.Time
	// HealthTimeout 은 /health 가 200 이 될 때까지 기다리는 시간이다. 기본 10분.
	HealthTimeout time.Duration
	// PollInterval 은 /health 와 유닛 상태를 확인하는 간격이다.
	PollInterval time.Duration
	// Sleep 은 ctx 가 끝나거나 d 가 지나면 돌아온다. nil 이면 실제 타이머를 쓴다.
	Sleep func(ctx context.Context, d time.Duration) error
	// Logger 는 진행 로그를 남긴다. nil 이면 stderr에 쓴다.
	Logger *log.Logger
	// KeySnapshotDir 는 시작별 API 키 파일의 디렉터리다. 비어 있으면 Keys.KeyFile의 디렉터리를 쓴다.
	KeySnapshotDir string
	// ChownKeyFile 은 키 스냅샷을 llama-server 실행 그룹이 읽도록 설정한다.
	ChownKeyFile func(path string) error
	// KeySnapshotGID 는 서비스 그룹의 GID다. 0이면 테스트 편의를 위해 소유 그룹 검사를 건너뛴다.
	KeySnapshotGID uint32
	// ProcRoot 는 실행 중 프로세스의 실행 파일과 환경을 읽는 루트다. 기본 /proc.
	ProcRoot string
	// ReclaimTimeout 과 ReclaimPollInterval 은 이전 유닛의 프로세스 회수 제한이다.
	ReclaimTimeout      time.Duration
	ReclaimPollInterval time.Duration
}

// Supervisor는 llama-server 유닛의 시작, 중지, 감시를 맡는다.
type Supervisor struct {
	opts Options

	// sem 은 수명 주기 작업을 한 번에 하나만 돌리는 세마포어다(채널 용량 1).
	sem chan struct{}
	// firewallMu 는 설정 읽기부터 nft 적용까지 방화벽 갱신 전체를 직렬화한다.
	firewallMu sync.Mutex

	// mu 는 아래 필드를 보호한다. 시스템 명령을 부를 때는 잡지 않는다.
	mu          sync.Mutex
	st          Status
	cancel      context.CancelFunc  // 진행 중인 Start 를 끊는 함수, 없으면 nil
	done        chan struct{}       // 진행 중이거나 마지막 Start 의 종료 신호
	runErr      *Error              // 마지막 Start 의 결과 오류
	priorStatus *Status             // 시작 도중 이전 서버가 남은 경우 복원할, 검증된 서버 정보
	cpuRuntime  *cpuRuntimeEvidence // 검증된 CPU 프로세스의 런타임 증거
	// pendingReclaimPIDs 는 systemd 가 MainPID 를 해제했지만 NVML 에서 아직
	// 보이는 이전 서버 PID다. 취소된 시작 다음 재시도에서도 VRAM 계획 전에 회수한다.
	pendingReclaimPIDs []uint32
	// restart 는 자동 재시작의 진행 상황이다(restart.go).
	restart restartState
}

// New는 Supervisor를 만든다. 비워 둔 옵션은 기본값으로 채운다. 필수 의존성이 없어도 패닉하지 않고 오류로 보고한다.
func New(opts Options) *Supervisor {
	if opts.GPUStatusPath == "" {
		opts.GPUStatusPath = defaultGPUStatusPath
	}
	if opts.EnvFilePath == "" {
		opts.EnvFilePath = defaultEnvFilePath
	}
	if opts.ServerBinary == "" {
		opts.ServerBinary = defaultServerBinary
	}
	if opts.Unit == "" {
		opts.Unit = defaultUnit
	}
	if opts.FirewallPath == "" {
		opts.FirewallPath = defaultFirewallPath
	}
	if opts.KeySnapshotDir == "" && opts.Keys != nil && opts.Keys.KeyFile != "" {
		opts.KeySnapshotDir = filepath.Dir(opts.Keys.KeyFile)
	}
	if opts.ProcRoot == "" {
		opts.ProcRoot = defaultProcRoot
	}
	if opts.ReclaimTimeout <= 0 {
		opts.ReclaimTimeout = defaultReclaimTime
	}
	if opts.ReclaimPollInterval <= 0 {
		opts.ReclaimPollInterval = defaultReclaimPoll
	}
	if opts.HTTP == nil {
		opts.HTTP = &http.Client{}
	}
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.HealthTimeout <= 0 {
		opts.HealthTimeout = defaultHealthTimeout
	}
	if opts.PollInterval <= 0 {
		opts.PollInterval = defaultPollInterval
	}
	if opts.Sleep == nil {
		opts.Sleep = defaultSleep
	}
	if opts.Logger == nil {
		opts.Logger = log.New(os.Stderr, "supervisor: ", log.LstdFlags)
	}
	return &Supervisor{
		opts: opts,
		sem:  make(chan struct{}, 1),
		st:   Status{State: StateStopped, Warnings: []string{}},
	}
}

// defaultSleep은 실제 타이머로 기다린다.
func defaultSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// Status는 현재 상태의 사본을 돌려준다.
func (s *Supervisor) Status() Status {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := copyStatus(s.st)
	out.Restart = s.restart.info()
	return out
}

// copyStatus는 Status 를 복사한다. Warnings 슬라이스를 따로 복사하고 나머지 포인터는 읽기 전용으로 공유한다.
func copyStatus(st Status) Status {
	out := st
	out.Warnings = append([]string{}, st.Warnings...)
	return out
}

// StartAsync는 입력을 검사하고 시작을 백그라운드에서 시작한다. 바로 starting 상태를 돌려준다.
// 입력 오류(no_model, api_key_required, invalid_overrides)와 진행 중 작업(busy)은 여기서 바로 돌려준다.
func (s *Supervisor) StartAsync(req StartRequest) (Status, error) {
	if err := autoconf.ValidateOverrides(req.Overrides); err != nil {
		return Status{}, errorf(CodeInvalidOverrides, "%v", err)
	}
	m, err := s.findModel(req.ModelID)
	if err != nil {
		return Status{}, err
	}
	cfg := s.opts.Config()
	if err := cfg.Validate(); err != nil {
		return Status{}, errorf(CodeInvalidSettings, "invalid daemon settings: %v", err)
	}
	keySnapshot, keyErr := s.keySnapshot()
	if keyErr != nil {
		return Status{}, errorf(CodeInternal, "read API key snapshot: %v", keyErr)
	}
	if err := validateCredentialSnapshot(keySnapshot); err != nil {
		return Status{}, errorf(CodeInternal, "API key snapshot is malformed")
	}
	if cfg.LANExposed && len(keySnapshot) == 0 {
		return Status{}, errorf(CodeAPIKeyRequired, "LAN exposure requires at least one API key; create a key first")
	}

	select {
	case s.sem <- struct{}{}:
	default:
		return Status{}, errorf(CodeBusy, "another server operation is in progress")
	}

	apiKeyFile := ""
	// 게이트웨이를 쓰면 키 확인을 데몬이 하므로 llama-server 에 키 파일을 주지 않는다.
	if len(keySnapshot) > 0 && !cfg.Gateway {
		var snapshotErr error
		apiKeyFile, snapshotErr = s.writeKeySnapshot(keySnapshot)
		if snapshotErr != nil {
			<-s.sem
			return Status{}, errorf(CodeEnvFile, "write API key snapshot: %v", snapshotErr)
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.opts.HealthTimeout+startSlack)
	done := make(chan struct{})
	host := "127.0.0.1"
	if cfg.LANExposed {
		host = "0.0.0.0"
	}

	s.mu.Lock()
	s.cancel = cancel
	s.done = done
	s.runErr = nil
	prior := copyStatus(s.st)
	if prior.State == StateRunning || prior.State == StateRunningCPU || prior.PID > 0 || prior.UnitUnverifiable {
		s.priorStatus = &prior
	} else {
		s.priorStatus = nil
	}
	s.st = Status{
		State:       StateStarting,
		ModelID:     m.ID,
		Host:        host,
		Port:        cfg.Port,
		BackendPort: cfg.BackendPort(),
		LANExposed:  cfg.LANExposed,
		Since:       s.opts.Now(),
		Warnings:    []string{},
	}
	if s.priorStatus != nil {
		s.st.UnitUnverifiable = true
		s.st.PID = prior.PID
	}
	snap := copyStatus(s.st)
	s.mu.Unlock()

	job := startJob{model: m, ov: req.Overrides, cfg: cfg, apiKeyFile: apiKeyFile}
	go func() {
		runErr := s.run(ctx, job)
		cancel()
		s.mu.Lock()
		s.runErr = runErr
		s.cancel = nil
		s.mu.Unlock()
		close(done)
		<-s.sem
	}()
	return snap, nil
}

// Wait은 진행 중인 Start 가 끝날 때까지 기다린다. 끝난 뒤의 상태와 오류를 돌려준다.
// ctx 가 먼저 끝나면 현재 상태를 돌려주고 wait_cancelled 오류를 낸다. 시작 작업 자체는 계속된다.
func (s *Supervisor) Wait(ctx context.Context) (Status, error) {
	s.mu.Lock()
	done := s.done
	s.mu.Unlock()
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			return s.Status(), errorf(CodeWaitCancelled, "stopped waiting: %v", ctx.Err())
		}
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.runErr != nil {
		return copyStatus(s.st), s.runErr
	}
	return copyStatus(s.st), nil
}

// Start는 StartAsync 후 Wait 한다.
func (s *Supervisor) Start(ctx context.Context, req StartRequest) (Status, error) {
	if _, err := s.StartAsync(req); err != nil {
		return Status{}, err
	}
	return s.Wait(ctx)
}

// Stop은 진행 중인 Start 를 끊고 유닛을 멈춘다. 끝나면 상태는 stopped 다.
func (s *Supervisor) Stop(ctx context.Context) error {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	s.mu.Unlock()

	select {
	case s.sem <- struct{}{}:
	case <-ctx.Done():
		return errorf(CodeWaitCancelled, "stopped waiting for the running operation: %v", ctx.Err())
	}
	defer func() { <-s.sem }()

	if e := s.stopAndReclaim(ctx); e != nil {
		return s.recordFailedState(ctx, e)
	}
	if err := s.cleanupKeySnapshots(""); err != nil {
		return s.recordFailedState(ctx, errorf(CodeEnvFile, "remove inactive API key snapshots: %v", err))
	}
	s.mu.Lock()
	s.st = Status{State: StateStopped, Warnings: []string{}}
	s.priorStatus = nil
	s.restart = restartState{} // 사용자가 멈췄으므로 자동 재시작 순서를 끝낸다.
	s.mu.Unlock()
	return nil
}

// Logs는 유닛 저널의 마지막 n줄을 돌려준다. n 이 0이면 기본값(200)이고 상한은 2000이다.
func (s *Supervisor) Logs(ctx context.Context, lines int) (string, error) {
	if lines == 0 {
		lines = defaultLogLines
	}
	if lines < 1 || lines > maxLogLines {
		return "", errorf(CodeInvalidLines, "lines must be 1-%d, got %d", maxLogLines, lines)
	}
	res, err := s.exec(ctx, platform.Command{
		Name:      "journalctl",
		Args:      []string{"-u", s.opts.Unit, "-n", strconv.Itoa(lines), "-o", "cat", "--no-pager"},
		Timeout:   journalTimeout,
		MaxOutput: maxJournalBytes,
	})
	if err != nil {
		return "", errorf(CodeLogsFailed, "journalctl: %v", err)
	}
	if res.ExitCode != 0 {
		return "", errorf(CodeLogsFailed, "journalctl exited %d: %s", res.ExitCode, excerpt(string(res.Stderr), 512))
	}
	return string(res.Stdout), nil
}

// Initialize 는 API 소켓을 열기 전에 기존 유닛 상태를 조정한다.
func (s *Supervisor) Initialize(ctx context.Context) error {
	_, err := s.reconcileActive(ctx)
	return err
}

// Autostart 는 조정된 상태가 stopped 일 때만 설정된 기본 모델을 백그라운드에서 시작한다.
func (s *Supervisor) Autostart(ctx context.Context) {
	st := s.Status()
	if st.State == StateRunning || st.State == StateRunningCPU {
		s.logf("autostart skipped: an active llama-server was already adopted")
		return
	}
	if st.State != StateStopped {
		s.logf("autostart skipped: server state is %s", st.State)
		return
	}
	cfg := s.opts.Config()
	if !cfg.Autostart {
		s.logf("autostart skipped: autostart is disabled in the config")
		return
	}
	id, err := s.opts.Store.Default()
	if err != nil {
		s.logf("autostart skipped: cannot read the default model: %v", err)
		return
	}
	if id == "" {
		s.logf("autostart skipped: no default model is set")
		return
	}
	if _, err := s.Start(ctx, StartRequest{}); err != nil {
		s.logf("autostart failed: %v", err)
		return
	}
	s.logf("autostart finished")
}

// Watch는 실행 중인 유닛이 살아 있는지 30초마다 확인한다. 죽었으면 failed(server_exited)로 바꾸고,
// 설정(auto_restart)이 켜져 있으면 대기 시간을 늘려 가며 다시 시작한다(restart.go). ctx 가 끝나면 돌아온다.
func (s *Supervisor) Watch(ctx context.Context) {
	for {
		if err := s.opts.Sleep(ctx, watchInterval); err != nil {
			return
		}
		s.watchOnce(ctx)
		s.restartTick(ctx)
	}
}

// watchOnce는 한 번 확인한다. 시작이나 중지가 진행 중이면 이번 차례는 건너뛴다.
func (s *Supervisor) watchOnce(ctx context.Context) {
	select {
	case s.sem <- struct{}{}:
	default:
		return
	}
	defer func() { <-s.sem }()

	st := s.Status()
	if st.State != StateRunning && st.State != StateRunningCPU {
		return
	}
	state, err := s.unitState(ctx)
	if err != nil {
		s.recordFailedState(ctx, errorf(CodeUnitCheck, "watch cannot read unit state: %v", err))
		return
	}
	pid, err := s.mainPID(ctx)
	if err != nil {
		s.recordFailedState(ctx, errorf(CodeUnitCheck, "watch cannot read unit MainPID: %v", err))
		return
	}
	if state == "active" && pid > 0 && int(pid) == st.PID {
		return
	}
	if (state == "inactive" || state == "failed") && pid == 0 {
		s.mu.Lock()
		s.st.State = StateFailed
		s.st.PID = 0
		s.st.UnitUnverifiable = false
		s.st.LastError = errorf(CodeServerExited, "unit is %s; the server is no longer running", state)
		s.priorStatus = nil
		s.mu.Unlock()
		s.logf("watch: server exited (unit is %s)", state)
		return
	}
	s.recordFailedState(ctx, errorf(CodeUnitCheck, "watch found unit state %q with MainPID %d inconsistent with the managed server", state, pid))
}

// findModel은 ID 의 모델을 찾는다. ID 가 비어 있으면 기본 모델을 쓴다.
func (s *Supervisor) findModel(id string) (models.Model, *Error) {
	if id == "" {
		def, err := s.opts.Store.Default()
		if err != nil {
			return models.Model{}, errorf(CodeInternal, "read the default model: %v", err)
		}
		if def == "" {
			return models.Model{}, errorf(CodeNoModel, "no model was given and no default model is set")
		}
		id = def
	}
	list, err := s.opts.Store.List()
	if err != nil {
		return models.Model{}, errorf(CodeInternal, "list models: %v", err)
	}
	for _, m := range list {
		if m.ID == id {
			return m, nil
		}
	}
	return models.Model{}, errorf(CodeNoModel, "model %q was not found", id)
}

// keySnapshot 은 Store 가 mutex 안에서 검증한 키 바이트의 복사본을 돌려준다.
func (s *Supervisor) keySnapshot() ([]byte, error) {
	if s.opts.Keys == nil {
		return nil, nil
	}
	return s.opts.Keys.Snapshot()
}

// exec는 주입된 Runner로 명령을 실행한다. Runner 가 없으면 오류다.
func (s *Supervisor) exec(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	if s.opts.Runner == nil {
		return platform.Result{}, errors.New("no command runner is configured")
	}
	return s.opts.Runner.Run(ctx, cmd)
}

// stopUnit은 유닛을 멈춘다. 이미 멈춰 있어도 성공이다.
func (s *Supervisor) stopUnit(ctx context.Context) *Error {
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{"stop", s.opts.Unit},
		Timeout:   stopTimeout,
		MaxOutput: maxCommandOutput,
	})
	if err != nil {
		return errorf(CodeUnitStopFailed, "systemctl stop: %v", err)
	}
	if res.ExitCode != 0 {
		return errorf(CodeUnitStopFailed, "systemctl stop exited %d: %s", res.ExitCode, excerpt(string(res.Stderr), 512))
	}
	return nil
}

// unitState는 systemctl is-active 의 출력(active, inactive, failed 등)을 돌려준다.
// is-active 는 비활성일 때 0이 아닌 종료 코드를 내므로 종료 코드가 아니라 출력을 본다.
func (s *Supervisor) unitState(ctx context.Context) (string, error) {
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{"is-active", s.opts.Unit},
		Timeout:   shortTimeout,
		MaxOutput: 4 << 10,
	})
	if err != nil {
		return "", err
	}
	out := strings.TrimSpace(string(res.Stdout))
	if out == "" {
		return "", fmt.Errorf("systemctl is-active printed no state (exit %d)", res.ExitCode)
	}
	return out, nil
}

// mainPID는 유닛의 MainPID 를 돌려준다. 0 이면 메인 프로세스가 없다.
func (s *Supervisor) mainPID(ctx context.Context) (uint32, error) {
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{"show", "-p", "MainPID", "--value", s.opts.Unit},
		Timeout:   shortTimeout,
		MaxOutput: 1 << 10,
	})
	if err != nil {
		return 0, err
	}
	if res.ExitCode != 0 {
		return 0, fmt.Errorf("systemctl show exited %d: %s", res.ExitCode, excerpt(string(res.Stderr), 256))
	}
	pid, err := strconv.ParseUint(strings.TrimSpace(string(res.Stdout)), 10, 32)
	if err != nil {
		return 0, fmt.Errorf("unexpected MainPID output: %w", err)
	}
	return uint32(pid), nil
}

// journalTail은 저널 끝부분 40줄의 발췌를 돌려준다. 오류 메시지에 쓴다.
func (s *Supervisor) journalTail(ctx context.Context) string {
	res, err := s.exec(ctx, platform.Command{
		Name:      "journalctl",
		Args:      []string{"-u", s.opts.Unit, "-n", "40", "-o", "cat", "--no-pager"},
		Timeout:   journalTimeout,
		MaxOutput: maxJournalBytes,
	})
	if err != nil {
		return "(journal unavailable: " + err.Error() + ")"
	}
	return excerpt(string(res.Stdout), excerptBytes)
}

// logf는 로거에 한 줄을 남긴다.
func (s *Supervisor) logf(format string, args ...any) {
	s.opts.Logger.Printf(format, args...)
}

// now는 현재 시각이다.
func (s *Supervisor) now() time.Time {
	return s.opts.Now()
}

// excerpt는 문자열의 끝 max 바이트를 돌려준다. 잘렸으면 앞에 "..."을 붙인다.
func excerpt(v string, max int) string {
	v = strings.TrimSpace(v)
	if len(v) > max {
		return "..." + v[len(v)-max:]
	}
	return v
}

// writeFileAtomic은 같은 디렉터리의 임시 파일에 쓰고 fsync 후 rename 한다.
func writeFileAtomic(file string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(file)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(file)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, file); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, file, err)
	}
	committed = true
	return nil
}
