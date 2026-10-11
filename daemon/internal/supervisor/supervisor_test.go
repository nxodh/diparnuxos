package supervisor

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/apikeys"
	"github.com/nxodh/lightlixos/daemon/internal/autoconf"
	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

const (
	testServerBinary = "/fake/llama-server"
	testUnit         = "lightlix-llama.service"
	offloadOK        = "load_tensors: offloaded 33/33 layers to GPU\n"
	deviceOK         = "Available devices:\n  CUDA0: NVIDIA GeForce RTX 4090 (24564 MiB, 23000 MiB free)\n"
	testPID          = 4242
)

// sysFake는 systemctl, journalctl, nft, llama-server 를 흉내 낸다. 호출 기록을 남긴다.
type sysFake struct {
	mu            sync.Mutex
	calls         []platform.Command
	active        string   // is-active 의 기본 출력
	activeSeq     []string // 있으면 is-active 가 이 순서로 답한다. 마지막 값이 반복된다.
	restartExit   int
	stopExit      int
	pid           string
	journal       string
	listDevices   string
	defaultTarget string
	greetd        bool
	nftExit       int
	nftStderr     string
}

func (f *sysFake) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, cmd)
	out := func(s string) (platform.Result, error) { return platform.Result{Stdout: []byte(s)}, nil }
	exit := func(code int, stderr string) (platform.Result, error) {
		return platform.Result{ExitCode: code, Stderr: []byte(stderr)}, nil
	}
	if cmd.Name == testServerBinary {
		return out(f.listDevices)
	}
	switch cmd.Name {
	case "systemctl":
		switch cmd.Args[0] {
		case "restart":
			if f.restartExit == 0 {
				f.active = "active"
			}
			return exit(f.restartExit, "restart failed")
		case "is-active":
			if len(f.activeSeq) > 0 {
				v := f.activeSeq[0]
				if len(f.activeSeq) > 1 {
					f.activeSeq = f.activeSeq[1:]
				}
				return out(v + "\n")
			}
			return out(f.active + "\n")
		case "stop":
			f.active = "inactive"
			return exit(f.stopExit, "stop failed")
		case "show":
			return out(f.pid + "\n")
		case "get-default":
			return out(f.defaultTarget + "\n")
		case "set-default":
			f.defaultTarget = cmd.Args[1]
			return exit(0, "")
		case "isolate":
			return exit(0, "")
		case "list-unit-files":
			if f.greetd && len(cmd.Args) > 1 && cmd.Args[1] == "greetd.service" {
				return out("greetd.service enabled enabled\n")
			}
			return out("")
		}
	case "journalctl":
		return out(f.journal)
	case "nft":
		return exit(f.nftExit, f.nftStderr)
	}
	return platform.Result{}, errors.New("unexpected command " + cmd.Name)
}

// count는 name 명령이 첫 인자 first 로 불린 횟수를 돌려준다. first 가 빈 문자열이면 이름만 본다.
func (f *sysFake) count(name, first string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Name == name && (first == "" || (len(c.Args) > 0 && c.Args[0] == first)) {
			n++
		}
	}
	return n
}

// gpuFake는 고정된 NVML 스냅샷을 돌려준다.
type gpuFake struct{ snap platform.GPUSnapshot }

func (g gpuFake) Snapshot(ctx context.Context) platform.GPUSnapshot { return g.snap }

// clock은 가짜 시각이다. Sleep 은 실제로 기다리지 않고 시각만 앞으로 옮긴다.
// block 이 있으면 ctx 가 끝나거나 block 이 닫힐 때까지 기다린다.
type clock struct {
	mu    sync.Mutex
	t     time.Time
	block chan struct{}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) sleep(ctx context.Context, d time.Duration) error {
	if c.block != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.block:
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
	return nil
}

// fixture는 테스트 한 건의 환경이다.
type fixture struct {
	sup     *Supervisor
	sys     *sysFake
	clk     *clock
	health  *atomic.Int32
	cfg     config.Config
	keys    *apikeys.Store
	dir     string
	gpuPath string
	envPath string
	fwPath  string
}

// newFixture는 GPU 상태가 ready 인 환경을 만든다. gpuState 가 빈 문자열이 아니면 그 상태를 쓴다.
func newFixture(t *testing.T, snap platform.GPUSnapshot, gpuState string) *fixture {
	t.Helper()
	dir := t.TempDir()
	health := &atomic.Int32{}
	health.Store(http.StatusOK)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(int(health.Load()))
	}))
	t.Cleanup(srv.Close)
	port := srv.Listener.Addr().(*net.TCPAddr).Port

	if err := os.WriteFile(filepath.Join(dir, "llama.gguf"), ggufFile("llama", 32, 8192, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	gpuPath := filepath.Join(dir, "gpu-status")
	writeStatus(t, gpuPath, gpuState)
	if err := (models.Store{Dir: dir}).SetDefault("llama.gguf"); err != nil {
		t.Fatal(err)
	}

	f := &fixture{
		sys:     &sysFake{active: "inactive", pid: strconv.Itoa(testPID), journal: offloadOK, listDevices: deviceOK, defaultTarget: "multi-user.target"},
		clk:     &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)},
		health:  health,
		dir:     dir,
		gpuPath: gpuPath,
		envPath: filepath.Join(dir, "llama-server.env"),
		fwPath:  filepath.Join(dir, "firewall.nft"),
		keys: &apikeys.Store{
			KeyFile:  filepath.Join(dir, "api-keys"),
			MetaFile: filepath.Join(dir, "api-keys.json"),
		},
	}
	f.cfg = config.Default()
	f.cfg.Port = port
	f.cfg.ModelsDir = dir
	f.sup = New(Options{
		Runner:        f.sys,
		GPU:           gpuFake{snap: snap},
		Store:         models.Store{Dir: dir},
		Keys:          f.keys,
		Config:        func() config.Config { return f.cfg },
		MemInfo:       func() (platform.MemInfo, error) { return platform.MemInfo{TotalMiB: 65536, AvailableMiB: 32768}, nil },
		GPUStatusPath: gpuPath,
		EnvFilePath:   f.envPath,
		ServerBinary:  testServerBinary,
		Unit:          testUnit,
		FirewallPath:  f.fwPath,
		HTTP:          srv.Client(),
		Now:           f.clk.now,
		HealthTimeout: 30 * time.Second,
		PollInterval:  10 * time.Second,
		Sleep:         f.clk.sleep,
		Logger:        log.New(io.Discard, "", 0),
	})
	return f
}

// readyGPU는 RTX 4090 한 장이 쓸 수 있는 NVML 스냅샷이다. 메인 PID 가 프로세스 목록에 있다.
func readyGPU() platform.GPUSnapshot {
	return platform.GPUSnapshot{
		Available:     true,
		DriverVersion: "595.91.07",
		Devices: []platform.GPUDevice{{
			Index:          0,
			Name:           "NVIDIA GeForce RTX 4090",
			MemTotalMiB:    24564,
			MemFreeMiB:     23000,
			MemUsedMiB:     1564,
			ProcessesKnown: true,
			Processes:      []platform.GPUProcess{{PID: testPID, UsedMiB: 1400}},
		}},
	}
}

// writeStatus는 gpustatus 형식의 상태 파일을 쓴다. state 가 ready 면 GPU 한 장, 그 밖이면 GPU 없음이다.
func writeStatus(t *testing.T, path, state string) {
	t.Helper()
	var b strings.Builder
	if state == "ready" {
		b.WriteString("LIGHTLIX_GPU_STATUS=ready\nLIGHTLIX_GPU_PRESENT=1\nLIGHTLIX_GPU_PCI_COUNT=1\nLIGHTLIX_GPU_COUNT=1\n")
		b.WriteString("LIGHTLIX_GPU_USABLE_COUNT=1\nLIGHTLIX_GPU_USABLE_INDEXES=0\nLIGHTLIX_SECURE_BOOT=disabled\n")
		b.WriteString("LIGHTLIX_DRIVER_VERSION=595.91.07\nLIGHTLIX_GPU_MESSAGE=\"GPU ready\"\n")
		b.WriteString("LIGHTLIX_GPU_0_NAME=\"GeForce RTX 4090\"\nLIGHTLIX_GPU_0_COMPUTE_CAP=8.9\nLIGHTLIX_GPU_0_SUPPORTED=1\nLIGHTLIX_GPU_0_REASON=ok\n")
	} else {
		b.WriteString("LIGHTLIX_GPU_STATUS=no_gpu\nLIGHTLIX_GPU_PRESENT=0\nLIGHTLIX_GPU_PCI_COUNT=0\nLIGHTLIX_GPU_COUNT=0\n")
		b.WriteString("LIGHTLIX_GPU_USABLE_COUNT=0\nLIGHTLIX_GPU_USABLE_INDEXES=\nLIGHTLIX_SECURE_BOOT=unknown\n")
		b.WriteString("LIGHTLIX_DRIVER_VERSION=\nLIGHTLIX_GPU_MESSAGE=\"no NVIDIA GPU\"\n")
	}
	if err := os.WriteFile(path, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ggufFile은 general.architecture 와 세 개의 <arch>.* 정수 키가 든 최소 GGUF 헤더를 만든다.
func ggufFile(arch string, block, ctx, emb uint32) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString("GGUF")
	binary.Write(&b, le, uint32(3))
	binary.Write(&b, le, uint64(0))
	binary.Write(&b, le, uint64(4))
	key := func(k string) {
		binary.Write(&b, le, uint64(len(k)))
		b.WriteString(k)
	}
	key("general.architecture")
	binary.Write(&b, le, uint32(8))
	binary.Write(&b, le, uint64(len(arch)))
	b.WriteString(arch)
	for _, kv := range []struct {
		k string
		v uint32
	}{{".block_count", block}, {".context_length", ctx}, {".embedding_length", emb}} {
		key(arch + kv.k)
		binary.Write(&b, le, uint32(4))
		binary.Write(&b, le, kv.v)
	}
	return b.Bytes()
}

// codeOf는 오류의 코드를 돌려준다. 코드가 없으면 빈 문자열이다.
func codeOf(err error) string {
	var se *Error
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// startSync는 동기 시작을 부른다.
func (f *fixture) startSync(req StartRequest) (Status, error) {
	return f.sup.Start(context.Background(), req)
}

// wantCode는 오류 코드를 확인한다.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if codeOf(err) != code {
		t.Fatalf("error code = %q, want %q (err: %v)", codeOf(err), code, err)
	}
}

// wantStopped는 실패 뒤에 systemctl stop 이 불렸고 상태가 failed 인지 확인한다.
func wantStoppedAfterFailure(t *testing.T, f *fixture, st Status, code string) {
	t.Helper()
	if f.sys.count("systemctl", "stop") == 0 {
		t.Fatalf("no systemctl stop call after failure %s", code)
	}
	if st.State != StateFailed {
		t.Fatalf("state = %s, want failed", st.State)
	}
	if st.LastError == nil || st.LastError.Code != code {
		t.Fatalf("last error = %+v, want code %s", st.LastError, code)
	}
}

func TestStartGPUSuccess(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	st, err := f.startSync(StartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.State != StateRunning {
		t.Fatalf("state = %s, want running", st.State)
	}
	if !st.Offload.Verified || st.Offload.Offloaded != 33 || st.Offload.Total != 33 {
		t.Fatalf("offload = %+v", st.Offload)
	}
	if st.PID != testPID {
		t.Fatalf("pid = %d", st.PID)
	}
	if st.Plan == nil || st.Plan.NGL != "all" {
		t.Fatalf("plan NGL = %+v", st.Plan)
	}
	if !strings.Contains(st.Offload.Note, "VRAM check: PID") {
		t.Fatalf("note = %q", st.Offload.Note)
	}
	if f.sys.count("systemctl", "stop") != 0 {
		t.Fatal("successful start must not stop the unit")
	}
	env, err := os.ReadFile(f.envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `LLAMA_ARG_N_GPU_LAYERS="all"`) {
		t.Fatalf("env file lacks NGL: %s", env)
	}
	if !strings.Contains(string(env), `LLAMA_ARG_HOST="127.0.0.1"`) {
		t.Fatalf("env file lacks loopback host: %s", env)
	}
	info, err := os.Stat(f.envPath)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("env file mode = %o, want 0644", info.Mode().Perm())
	}
}

func TestStartCPUFallback(t *testing.T) {
	f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
	st, err := f.startSync(StartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.State != StateRunningCPU {
		t.Fatalf("state = %s, want running_cpu_fallback", st.State)
	}
	joined := strings.Join(st.Warnings, "\n")
	if !strings.Contains(joined, "no_gpu") || !strings.Contains(joined, "no NVIDIA GPU") {
		t.Fatalf("warnings lack GPU state and message: %q", joined)
	}
	if f.sys.count(testServerBinary, "") != 0 {
		t.Fatal("CPU plan must not run the CUDA pre-check")
	}
}

func TestStartGPUStatusMissingIsCPUWithWarning(t *testing.T) {
	f := newFixture(t, platform.GPUSnapshot{}, "ready")
	if err := os.Remove(f.gpuPath); err != nil {
		t.Fatal(err)
	}
	st, err := f.startSync(StartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.State != StateRunningCPU {
		t.Fatalf("state = %s", st.State)
	}
	if !strings.Contains(strings.Join(st.Warnings, "\n"), "GPU check has not run") {
		t.Fatalf("warnings = %q", st.Warnings)
	}
}

func TestStartCUDABackendMissing(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.listDevices = "Available devices:\n  (none)\n"
	st, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeCUDABackend)
	wantStoppedAfterFailure(t, f, st, CodeCUDABackend)
	if f.sys.count("systemctl", "restart") != 0 {
		t.Fatal("pre-check failure must not restart the unit")
	}
}

func TestStartOffloadLineMissing(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.journal = "llama_server: listening\n"
	st, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeGPUOffloadFailed)
	wantStoppedAfterFailure(t, f, st, CodeGPUOffloadFailed)
}

func TestStartOffloadZero(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.journal = "load_tensors: offloaded 0/33 layers to GPU\n"
	st, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeGPUOffloadFailed)
	wantStoppedAfterFailure(t, f, st, CodeGPUOffloadFailed)
	if !strings.Contains(st.LastError.Message, "0/33") {
		t.Fatalf("message lacks the actual value: %s", st.LastError.Message)
	}
}

func TestStartNGLAllButPartial(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.journal = "load_tensors: offloaded 20/33 layers to GPU\n"
	all := "all"
	st, err := f.startSync(StartRequest{Overrides: autoconf.Overrides{NGL: &all}})
	wantCode(t, err, CodeGPUOffloadFailed)
	wantStoppedAfterFailure(t, f, st, CodeGPUOffloadFailed)
	if !strings.Contains(st.LastError.Message, "20/33") {
		t.Fatalf("message lacks offloaded 20/33: %s", st.LastError.Message)
	}
}

func TestStartNGLIntegerMatches(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.journal = "load_tensors: offloaded 10/33 layers to GPU\n"
	ten := "10"
	st, err := f.startSync(StartRequest{Overrides: autoconf.Overrides{NGL: &ten}})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.State != StateRunning || st.Offload.Offloaded != 10 {
		t.Fatalf("status = %+v", st)
	}
}

func TestStartNoGPUWarningInJournal(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.journal = offloadOK + "warning: no usable GPU found, --gpu-layers option will be ignored\n"
	st, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeGPUOffloadFailed)
	wantStoppedAfterFailure(t, f, st, CodeGPUOffloadFailed)
}

func TestStartPIDNotInProcessList(t *testing.T) {
	snap := readyGPU()
	snap.Devices[0].Processes = []platform.GPUProcess{{PID: 999, UsedMiB: 100}}
	f := newFixture(t, snap, "ready")
	st, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeGPUOffloadFailed)
	wantStoppedAfterFailure(t, f, st, CodeGPUOffloadFailed)
}

func TestStartProcessListUnknownSkipsVRAMCheck(t *testing.T) {
	snap := readyGPU()
	snap.Devices[0].ProcessesKnown = false
	snap.Devices[0].Processes = nil
	f := newFixture(t, snap, "ready")
	st, err := f.startSync(StartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.State != StateRunning || !strings.Contains(st.Offload.Note, "vram check skipped") {
		t.Fatalf("status = %+v", st)
	}
}

func TestStartUnitDiesWhileLoading(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.health.Store(http.StatusServiceUnavailable)
	f.sys.activeSeq = []string{"active", "failed"}
	st, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeServerExited)
	wantStoppedAfterFailure(t, f, st, CodeServerExited)
}

func TestStartHealthTimeout(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.health.Store(http.StatusServiceUnavailable)
	st, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeHealthTimeout)
	wantStoppedAfterFailure(t, f, st, CodeHealthTimeout)
}

func TestStartLANWithoutKeyRejected(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.LANExposed = true
	_, err := f.sup.StartAsync(StartRequest{})
	wantCode(t, err, CodeAPIKeyRequired)
	if f.sys.count("systemctl", "restart") != 0 {
		t.Fatal("LAN without key must not start the unit")
	}
}

func TestStartLANWithKeyBindsAllInterfaces(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.LANExposed = true
	if _, _, err := f.keys.Create("laptop"); err != nil {
		t.Fatal(err)
	}
	st, err := f.startSync(StartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.Host != "0.0.0.0" || !st.LANExposed {
		t.Fatalf("status = %+v", st)
	}
	env, err := os.ReadFile(f.envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `LLAMA_ARG_API_KEY_FILE="`+f.keys.KeyFile+`"`) {
		t.Fatalf("env file lacks key file path: %s", env)
	}
	if strings.Contains(string(env), "llx_") {
		t.Fatal("env file must not contain a plaintext key")
	}
}

func TestStartNoModel(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	_, err := f.sup.StartAsync(StartRequest{ModelID: "missing.gguf"})
	wantCode(t, err, CodeNoModel)

	// 기본 모델이 없고 모델 ID 도 없으면 no_model 이다.
	if err := os.Remove(filepath.Join(f.dir, ".default")); err != nil {
		t.Fatal(err)
	}
	_, err = f.sup.StartAsync(StartRequest{})
	wantCode(t, err, CodeNoModel)
}

func TestStartInvalidOverride(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	bad := "lots"
	_, err := f.sup.StartAsync(StartRequest{Overrides: autoconf.Overrides{NGL: &bad}})
	wantCode(t, err, CodeInvalidOverrides)
}

func TestConcurrentStartBusyAndStopInterrupts(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.health.Store(http.StatusServiceUnavailable)
	f.clk.block = make(chan struct{}) // 시작은 첫 대기에서 멈춘다

	if _, err := f.sup.StartAsync(StartRequest{}); err != nil {
		t.Fatalf("first StartAsync: %v", err)
	}
	_, err := f.sup.StartAsync(StartRequest{})
	wantCode(t, err, CodeBusy)

	if err := f.sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st, werr := f.sup.Wait(context.Background())
	wantCode(t, werr, CodeCanceled)
	if st.State != StateStopped {
		t.Fatalf("state after Stop = %s, want stopped", st.State)
	}
	if f.sys.count("systemctl", "stop") == 0 {
		t.Fatal("Stop must stop the unit")
	}
}

func TestStopRunningServer(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := f.sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st := f.sup.Status()
	if st.State != StateStopped || st.PID != 0 {
		t.Fatalf("status after Stop = %+v", st)
	}
}

func TestStopReportsUnitStopFailure(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.sys.stopExit = 1
	err := f.sup.Stop(context.Background())
	wantCode(t, err, CodeUnitStopFailed)
	if f.sup.Status().State != StateFailed {
		t.Fatal("a failed stop must leave the state failed")
	}
}

func TestWatchDetectsDeadServer(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.sys.activeSeq = []string{"failed"}
	f.sup.watchOnce(context.Background())
	st := f.sup.Status()
	if st.State != StateFailed || st.LastError == nil || st.LastError.Code != CodeServerExited {
		t.Fatalf("status = %+v", st)
	}
	if f.sys.count("systemctl", "restart") != 1 {
		t.Fatal("Watch must not restart the unit")
	}
}

func TestWatchReturnsWhenContextEnds(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() {
		f.sup.Watch(ctx)
		close(done)
	}()
	<-done
}

func TestAutostartWithoutDefaultDoesNothing(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.Autostart = true
	if err := os.Remove(filepath.Join(f.dir, ".default")); err != nil {
		t.Fatal(err)
	}
	f.sup.Autostart(context.Background())
	if f.sys.count("systemctl", "restart") != 0 {
		t.Fatal("autostart without a default model must not start the server")
	}
}

func TestApplyFirewallWritesRulesAndRunsNft(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.LANExposed = true
	if err := f.sup.ApplyFirewall(context.Background()); err != nil {
		t.Fatalf("ApplyFirewall: %v", err)
	}
	data, err := os.ReadFile(f.fwPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), fmt.Sprintf("tcp dport %d accept", f.cfg.Port)) {
		t.Fatalf("rules lack the LAN port: %s", data)
	}
	if f.sys.count("nft", "-f") != 1 {
		t.Fatal("nft -f not run")
	}
}

func TestApplyFirewallNftFailure(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.nftExit = 1
	f.sys.nftStderr = "syntax error"
	wantCode(t, f.sup.ApplyFirewall(context.Background()), CodeFirewallFailed)
}

func TestSetModeDesktopNotInstalled(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	wantCode(t, f.sup.SetMode(context.Background(), "desktop"), CodeDesktopNotInstalled)
	if f.sys.count("systemctl", "set-default") != 0 {
		t.Fatal("desktop must not change the default target when it is not installed")
	}
}

func TestSetModeServerSwitchesTarget(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.defaultTarget = "graphical.target"
	if err := f.sup.SetMode(context.Background(), "server"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if f.sys.count("systemctl", "set-default") != 1 || f.sys.count("systemctl", "isolate") != 1 {
		t.Fatal("expected set-default and isolate")
	}
	mode, err := f.sup.Mode(context.Background())
	if err != nil || mode != "server" {
		t.Fatalf("Mode = %q, %v", mode, err)
	}
}

func TestSetModeInvalid(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	wantCode(t, f.sup.SetMode(context.Background(), "kiosk"), CodeInvalidMode)
}

func TestSetModeDesktopWithDisplayManager(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.greetd = true
	if err := f.sup.SetMode(context.Background(), "desktop"); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if f.sys.count("systemctl", "set-default") != 1 {
		t.Fatal("expected set-default graphical.target")
	}
}

func TestLogsBounds(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.journal = "line\n"
	_, err := f.sup.Logs(context.Background(), 5000)
	wantCode(t, err, CodeInvalidLines)
	out, err := f.sup.Logs(context.Background(), 0)
	if err != nil || out != "line\n" {
		t.Fatalf("Logs default = %q, %v", out, err)
	}
}

func TestPlanWithoutGPUStatus(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if err := os.Remove(f.gpuPath); err != nil {
		t.Fatal(err)
	}
	pr, err := f.sup.Plan(context.Background(), "", autoconf.Overrides{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if !pr.Output.CPUOnly {
		t.Fatal("plan must be CPU only without the GPU status")
	}
	if !strings.Contains(strings.Join(pr.Output.Warnings, "\n"), "GPU check has not run") {
		t.Fatalf("warnings = %q", pr.Output.Warnings)
	}
}

func TestPlanNVMLUnavailableUsesAutoNGL(t *testing.T) {
	f := newFixture(t, platform.GPUSnapshot{Available: false, Reason: "library not found"}, "ready")
	pr, err := f.sup.Plan(context.Background(), "", autoconf.Overrides{})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if pr.Output.NGL != "auto" || pr.Output.CPUOnly {
		t.Fatalf("plan NGL = %s, cpu = %v", pr.Output.NGL, pr.Output.CPUOnly)
	}
	if !strings.Contains(strings.Join(pr.Output.Warnings, "\n"), "NVML is unavailable") {
		t.Fatalf("warnings = %q", pr.Output.Warnings)
	}
}

func TestPlanRequestOverridesWin(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	cfgNGL := "0"
	f.cfg.Overrides.NGL = &cfgNGL
	reqNGL := "all"
	pr, err := f.sup.Plan(context.Background(), "", autoconf.Overrides{NGL: &reqNGL})
	if err != nil {
		t.Fatalf("Plan: %v", err)
	}
	if pr.Output.NGL != "all" {
		t.Fatalf("NGL = %s, want the request value all", pr.Output.NGL)
	}
}

func TestPlanRejectsInvalidOverride(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	bad := 1
	_, err := f.sup.Plan(context.Background(), "", autoconf.Overrides{Ctx: &bad})
	wantCode(t, err, CodeInvalidOverrides)
}

func TestAutostartStartsDefaultModel(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.Autostart = true
	f.sup.Autostart(context.Background())
	if st := f.sup.Status(); st.State != StateRunning {
		t.Fatalf("state = %s, want running", st.State)
	}
}
