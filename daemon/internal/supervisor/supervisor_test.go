package supervisor

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
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
	"syscall"
	"testing"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/apikeys"
	"github.com/nxodh/lightlixos/daemon/internal/autoconf"
	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/llamaargs"
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
	stateErr      error
	pidErr        error
	pid           string
	journal       string
	listDevices   string
	defaultTarget string
	lightdm       bool
	nftExit       int
	nftStderr     string
	stopCount     int
	gpu           *gpuFake
	newPID        int
	startGate     chan struct{}
	onStart       func(pid int) error
	// ssh.service 용 값이다.
	sshInstalled bool
	sshActive    string
	sshExit      int
	sshdOutput   string
	sshdExit     int
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
		case "start":
			if len(cmd.Args) > 1 && cmd.Args[1] == "ssh.service" {
				if f.sshExit == 0 {
					f.sshActive = "active"
				}
				return exit(f.sshExit, "ssh unit change failed")
			}
			if f.startGate != nil {
				select {
				case <-ctx.Done():
					return platform.Result{}, ctx.Err()
				case <-f.startGate:
				}
			}
			if f.restartExit == 0 {
				f.active = "active"
				pid := testPID
				if f.newPID > 0 {
					pid = f.newPID
				}
				f.pid = strconv.Itoa(pid)
				if f.stopCount > 0 && f.gpu != nil {
					f.gpu.addPID(uint32(pid))
				}
				if f.onStart != nil {
					if err := f.onStart(pid); err != nil {
						return platform.Result{}, err
					}
				}
			}
			return exit(f.restartExit, "start failed")
		case "is-active":
			if len(cmd.Args) > 1 && cmd.Args[1] == "ssh.service" {
				if f.sshActive == "active" {
					return out("active\n")
				}
				return platform.Result{Stdout: []byte("inactive\n"), ExitCode: 3}, nil
			}
			if f.stateErr != nil {
				return platform.Result{}, f.stateErr
			}
			if len(f.activeSeq) > 0 {
				v := f.activeSeq[0]
				if len(f.activeSeq) > 1 {
					f.activeSeq = f.activeSeq[1:]
				}
				return out(v + "\n")
			}
			return out(f.active + "\n")
		case "stop":
			if len(cmd.Args) > 1 && cmd.Args[1] == "ssh.service" {
				if f.sshExit == 0 {
					f.sshActive = "inactive"
				}
				return exit(f.sshExit, "ssh unit change failed")
			}
			if f.stopExit == 0 {
				f.active = "inactive"
				oldPID, _ := strconv.Atoi(f.pid)
				f.pid = "0"
				f.stopCount++
				if f.gpu != nil {
					f.gpu.beginReclaim(uint32(oldPID))
				}
			}
			return exit(f.stopExit, "stop failed")
		case "show":
			if f.pidErr != nil {
				return platform.Result{}, f.pidErr
			}
			return out(f.pid + "\n")
		case "get-default":
			return out(f.defaultTarget + "\n")
		case "set-default":
			f.defaultTarget = cmd.Args[1]
			return exit(0, "")
		case "isolate":
			return exit(0, "")
		case "list-unit-files":
			if f.sshInstalled && len(cmd.Args) > 1 && cmd.Args[1] == "ssh.service" {
				return out("ssh.service enabled enabled\n")
			}
			if f.lightdm && len(cmd.Args) > 1 && cmd.Args[1] == "lightdm.service" {
				return out("lightdm.service enabled enabled\n")
			}
			return out("")
		}
	case "journalctl":
		return out(f.journal)
	case "nft":
		return exit(f.nftExit, f.nftStderr)
	case "sshd":
		if f.sshdExit != 0 {
			return exit(f.sshdExit, "no hostkeys available")
		}
		return out(f.sshdOutput)
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
type gpuFake struct {
	mu          sync.Mutex
	snap        platform.GPUSnapshot
	history     []platform.GPUSnapshot
	holdReclaim bool
}

func (g *gpuFake) Snapshot(ctx context.Context) platform.GPUSnapshot {
	g.mu.Lock()
	defer g.mu.Unlock()
	snap := cloneSnapshot(g.snap)
	g.history = append(g.history, cloneSnapshot(snap))
	return snap
}

func (g *gpuFake) addPID(pid uint32) {
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range g.snap.Devices {
		device := &g.snap.Devices[i]
		for _, process := range device.Processes {
			if process.PID == pid {
				return
			}
		}
		if device.ProcessesKnown {
			device.Processes = append(device.Processes, platform.GPUProcess{PID: pid, UsedMiB: 1200})
			device.MemFreeMiB -= 1200
			device.MemUsedMiB += 1200
		}
	}
}

func (g *gpuFake) dropPID(pid uint32) {
	if pid == 0 {
		return
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	for i := range g.snap.Devices {
		device := &g.snap.Devices[i]
		kept := device.Processes[:0]
		for _, process := range device.Processes {
			if process.PID == pid {
				device.MemFreeMiB += process.UsedMiB
				device.MemUsedMiB -= process.UsedMiB
				continue
			}
			kept = append(kept, process)
		}
		device.Processes = kept
	}
}

func (g *gpuFake) beginReclaim(pid uint32) {
	g.mu.Lock()
	hold := g.holdReclaim
	g.mu.Unlock()
	if !hold {
		g.dropPID(pid)
	}
}

func cloneSnapshot(in platform.GPUSnapshot) platform.GPUSnapshot {
	out := in
	out.Devices = append([]platform.GPUDevice(nil), in.Devices...)
	for i := range out.Devices {
		out.Devices[i].Processes = append([]platform.GPUProcess(nil), in.Devices[i].Processes...)
	}
	return out
}

// clock은 가짜 시각이다. Sleep 은 실제로 기다리지 않고 시각만 앞으로 옮긴다.
// block 이 있으면 ctx 가 끝나거나 block 이 닫힐 때까지 기다린다.
type clock struct {
	mu       sync.Mutex
	t        time.Time
	block    chan struct{}
	sleeping chan struct{}
}

func (c *clock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *clock) sleep(ctx context.Context, d time.Duration) error {
	c.mu.Lock()
	block, sleeping := c.block, c.sleeping
	if sleeping != nil {
		c.sleeping = nil
	}
	c.mu.Unlock()
	if block != nil {
		if sleeping != nil {
			close(sleeping)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-block:
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
	sup        *Supervisor
	sys        *sysFake
	clk        *clock
	health     *atomic.Int32
	cfg        config.Config
	keys       *apikeys.Store
	dir        string
	gpuPath    string
	envPath    string
	fwPath     string
	procRoot   string
	chownCalls []string
}

// newFixture는 GPU 상태가 ready 인 환경을 만든다. gpuState 가 빈 문자열이 아니면 그 상태를 쓴다.
func newFixture(t *testing.T, snap platform.GPUSnapshot, gpuState string) *fixture {
	t.Helper()
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	// t.TempDir 가 만든 테스트 전용 상위 디렉터리만 서비스 그룹이 통과할 수 있게 준비한다.
	if err := os.Chmod(filepath.Dir(dir), 0o755); err != nil {
		t.Fatal(err)
	}
	procRoot := filepath.Join(dir, "proc")
	if err := os.Mkdir(procRoot, 0o755); err != nil {
		t.Fatal(err)
	}
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

	gpuProbe := &gpuFake{snap: cloneSnapshot(snap)}
	f := &fixture{
		sys:      &sysFake{active: "inactive", pid: "0", journal: offloadOK, listDevices: deviceOK, defaultTarget: "multi-user.target", gpu: gpuProbe},
		clk:      &clock{t: time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)},
		health:   health,
		dir:      dir,
		gpuPath:  gpuPath,
		envPath:  filepath.Join(dir, "llama-server.env"),
		fwPath:   filepath.Join(dir, "firewall.nft"),
		procRoot: procRoot,
		keys: &apikeys.Store{
			KeyFile:  filepath.Join(dir, "api-keys"),
			MetaFile: filepath.Join(dir, "api-keys.json"),
		},
	}
	f.cfg = config.Default()
	f.cfg.Port = port
	f.cfg.ModelsDir = dir
	f.sup = New(Options{
		Runner:         f.sys,
		GPU:            gpuProbe,
		Store:          models.Store{Dir: dir},
		Keys:           f.keys,
		Config:         func() config.Config { return f.cfg },
		MemInfo:        func() (platform.MemInfo, error) { return platform.MemInfo{TotalMiB: 65536, AvailableMiB: 32768}, nil },
		GPUStatusPath:  gpuPath,
		EnvFilePath:    f.envPath,
		ServerBinary:   testServerBinary,
		Unit:           testUnit,
		FirewallPath:   f.fwPath,
		KeySnapshotDir: dir,
		ProcRoot:       procRoot,
		ChownKeyFile: func(path string) error {
			f.chownCalls = append(f.chownCalls, path)
			return nil
		},
		HTTP:          srv.Client(),
		Now:           f.clk.now,
		HealthTimeout: 30 * time.Second,
		PollInterval:  10 * time.Second,
		Sleep:         f.clk.sleep,
		Logger:        log.New(io.Discard, "", 0),
	})
	f.sys.onStart = func(pid int) error {
		return writeStartedProcessEvidence(f.procRoot, f.sup.opts.ServerBinary, f.envPath, pid)
	}
	return f
}

func writeStartedProcessEvidence(procRoot, binary, envPath string, pid int) error {
	contents, err := os.ReadFile(envPath)
	if err != nil {
		return err
	}
	var processEnv []byte
	for _, line := range strings.Split(string(contents), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		decoded, err := strconv.Unquote(value)
		if err != nil {
			return fmt.Errorf("decode runtime environment: %w", err)
		}
		processEnv = append(processEnv, []byte(key+"="+decoded)...)
		processEnv = append(processEnv, 0)
	}
	procDir := filepath.Join(procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		return err
	}
	exePath := filepath.Join(procDir, "exe")
	if err := os.Remove(exePath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.Symlink(binary, exePath); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(procDir, "environ"), processEnv, 0o600)
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

func writeActiveRuntime(t *testing.T, f *fixture, pid int, modelID, host string, output autoconf.Output, apiKeyFile string) {
	t.Helper()
	modelPath := filepath.Join(f.dir, modelID)
	if _, err := os.Stat(modelPath); errors.Is(err, os.ErrNotExist) {
		if err := os.WriteFile(modelPath, ggufFile("llama", 32, 8192, 4096), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	settings := llamaargs.ServerSettings{
		ModelPath:  modelPath,
		Host:       host,
		Port:       f.cfg.Port,
		APIKeyFile: apiKeyFile,
	}
	env, err := llamaargs.Env(output, settings)
	if err != nil {
		t.Fatalf("make active runtime environment: %v", err)
	}
	if err := os.WriteFile(f.envPath, []byte(llamaargs.EnvFile(env)), 0o644); err != nil {
		t.Fatal(err)
	}
	procDir := filepath.Join(f.procRoot, strconv.Itoa(pid))
	if err := os.MkdirAll(procDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.sup.opts.ServerBinary, filepath.Join(procDir, "exe")); err != nil {
		t.Fatal(err)
	}
	var rawEnv []byte
	for key, value := range env {
		rawEnv = append(rawEnv, []byte(key+"="+value)...)
		rawEnv = append(rawEnv, 0)
	}
	if err := os.WriteFile(filepath.Join(procDir, "environ"), rawEnv, 0o600); err != nil {
		t.Fatal(err)
	}
}

// wantCode는 오류 코드를 확인한다.
func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if codeOf(err) != code {
		t.Fatalf("error code = %q, want %q (err: %v)", codeOf(err), code, err)
	}
}

func TestStatusJSONUnitUnverifiableOmitsFalseAndExposesTrue(t *testing.T) {
	data, err := json.Marshal(Status{State: StateFailed, UnitUnverifiable: true})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"unit_unverifiable":true`) {
		t.Fatalf("status JSON lacks the active uncertainty flag: %s", data)
	}
	data, err = json.Marshal(Status{State: StateStopped})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "unit_unverifiable") {
		t.Fatalf("false uncertainty flag should be omitted: %s", data)
	}
}

// wantStopped 는 실패 뒤 상태가 failed 이고 실행 유닛이 남지 않았는지 확인한다.
func wantStoppedAfterFailure(t *testing.T, f *fixture, st Status, code string) {
	t.Helper()
	if st.State != StateFailed {
		t.Fatalf("state = %s, want failed", st.State)
	}
	if st.LastError == nil || st.LastError.Code != code {
		t.Fatalf("last error = %+v, want code %s", st.LastError, code)
	}
	f.sys.mu.Lock()
	active, pid := f.sys.active, f.sys.pid
	f.sys.mu.Unlock()
	if active == "active" || pid != "0" {
		t.Fatalf("failed start left unit state=%s PID=%s", active, pid)
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
	if st.State != StateRunningCPU || st.UnitUnverifiable || st.Offload.Verified {
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

func TestCPUOnlyRestartAndStopDoNotRequireNVML(t *testing.T) {
	for _, nvmlUnavailable := range []bool{false, true} {
		t.Run(fmt.Sprintf("nvml_unavailable_%v", nvmlUnavailable), func(t *testing.T) {
			f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
			if nvmlUnavailable {
				f.sup.opts.GPU = nil
			}
			for run := 0; run < 2; run++ {
				st, err := f.startSync(StartRequest{})
				if err != nil {
					t.Fatalf("Start run %d: %v", run+1, err)
				}
				if st.State != StateRunningCPU || st.Offload.Verified || st.UnitUnverifiable {
					t.Fatalf("CPU run %d status = %+v", run+1, st)
				}
			}
			if got := len(f.sup.reclaimPIDs()); got != 0 {
				t.Fatalf("verified CPU-only PID was queued for GPU reclamation: %v", f.sup.reclaimPIDs())
			}
			if err := f.sup.Stop(context.Background()); err != nil {
				t.Fatalf("Stop CPU-only unit: %v", err)
			}
			st := f.sup.Status()
			if st.State != StateStopped || st.PID != 0 || st.UnitUnverifiable {
				t.Fatalf("CPU-only Stop status = %+v", st)
			}
			if f.sys.count("systemctl", "start") != 2 || f.sys.count("systemctl", "stop") != 2 {
				t.Fatalf("CPU-only start/stop calls = %d/%d", f.sys.count("systemctl", "start"), f.sys.count("systemctl", "stop"))
			}
		})
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
	if f.sys.count("systemctl", "start") != 0 {
		t.Fatal("pre-check failure must not start the unit")
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

func TestCPUOnlyHealthFailureCanRetryWithoutNVML(t *testing.T) {
	for _, unavailable := range []string{"nil_gpu", "unavailable_snapshot"} {
		t.Run(unavailable, func(t *testing.T) {
			f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
			if unavailable == "nil_gpu" {
				f.sup.opts.GPU = nil
			}
			f.health.Store(http.StatusServiceUnavailable)

			failed, err := f.startSync(StartRequest{})
			wantCode(t, err, CodeHealthTimeout)
			if failed.State != StateFailed || failed.LastError == nil || failed.LastError.Code != CodeHealthTimeout {
				t.Fatalf("first CPU start failure = %+v, error = %v", failed, err)
			}
			if failed.UnitUnverifiable || failed.PID != 0 {
				t.Fatalf("confirmed stopped unit was reported uncertain: %+v", failed)
			}
			f.sys.mu.Lock()
			active, pid := f.sys.active, f.sys.pid
			f.sys.mu.Unlock()
			if active != "inactive" || pid != "0" {
				t.Fatalf("first CPU start did not stop safely: state=%s PID=%s", active, pid)
			}
			if got := f.sup.reclaimPIDs(); len(got) != 0 {
				t.Fatalf("verified CPU-only PID was queued for GPU reclamation: %v", got)
			}

			f.health.Store(http.StatusOK)
			started, err := f.startSync(StartRequest{})
			if err != nil {
				t.Fatalf("healthy CPU retry: %v", err)
			}
			if started.State != StateRunningCPU || f.sys.count("systemctl", "start") != 2 {
				t.Fatalf("healthy retry status/call count = %+v / %d starts", started, f.sys.count("systemctl", "start"))
			}
			if err := f.sup.Stop(context.Background()); err != nil {
				t.Fatalf("Stop after healthy retry: %v", err)
			}
			stopped := f.sup.Status()
			if stopped.State != StateStopped || stopped.PID != 0 || stopped.UnitUnverifiable {
				t.Fatalf("final Stop status = %+v", stopped)
			}
		})
	}
}

func TestCPUOnlyHealthFailureRetainsEvidenceAfterFailedStop(t *testing.T) {
	f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
	f.sup.opts.GPU = nil
	f.health.Store(http.StatusServiceUnavailable)
	f.sys.stopExit = 1

	failed, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeHealthTimeout)
	if failed.State != StateFailed || !failed.UnitUnverifiable || failed.PID != testPID {
		t.Fatalf("failed Stop did not retain active unit identity: %+v; error=%v", failed, err)
	}
	if got := f.sup.reclaimPIDs(); len(got) != 0 {
		t.Fatalf("verified CPU-only PID was queued as GPU during failed Stop: %v", got)
	}

	f.sys.stopExit = 0
	if err := f.sup.Stop(context.Background()); err != nil {
		t.Fatalf("retry Stop with retained CPU evidence: %v", err)
	}
	stopped := f.sup.Status()
	if stopped.State != StateStopped || stopped.PID != 0 || stopped.UnitUnverifiable {
		t.Fatalf("successful Stop did not clear uncertainty: %+v", stopped)
	}
}

func TestHealthyCPUFailedStopCanRetryWithoutNVML(t *testing.T) {
	for _, unavailable := range []string{"nil_gpu", "unavailable_snapshot"} {
		t.Run(unavailable, func(t *testing.T) {
			f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
			if unavailable == "nil_gpu" {
				f.sup.opts.GPU = nil
			}
			started, err := f.startSync(StartRequest{})
			if err != nil || started.State != StateRunningCPU {
				t.Fatalf("healthy CPU start = %+v, error=%v", started, err)
			}

			f.sys.stopExit = 1
			wantCode(t, f.sup.Stop(context.Background()), CodeUnitStopFailed)
			failed := f.sup.Status()
			if failed.State != StateFailed || !failed.UnitUnverifiable || failed.PID != testPID {
				t.Fatalf("failed Stop did not retain active CPU process identity: %+v", failed)
			}
			if got := f.sup.reclaimPIDs(); len(got) != 0 {
				t.Fatalf("verified CPU-only PID was queued during failed Stop: %v", got)
			}

			f.sys.stopExit = 0
			if err := f.sup.Stop(context.Background()); err != nil {
				t.Fatalf("retry Stop with retained CPU evidence: %v", err)
			}
			stopped := f.sup.Status()
			if stopped.State != StateStopped || stopped.PID != 0 || stopped.UnitUnverifiable {
				t.Fatalf("successful retry Stop status = %+v", stopped)
			}
		})
	}
}

func TestAdoptedCPUFailedStopCanRetryWithoutNVML(t *testing.T) {
	for _, unavailable := range []string{"nil_gpu", "unavailable_snapshot"} {
		t.Run(unavailable, func(t *testing.T) {
			f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
			if unavailable == "nil_gpu" {
				f.sup.opts.GPU = nil
			}
			out := autoconf.Output{
				NGL:        "0",
				Ctx:        4096,
				CacheTypeK: "f16",
				CacheTypeV: "f16",
				Batch:      512,
				LoadMode:   "mmap",
				CPUOnly:    true,
			}
			writeActiveRuntime(t, f, testPID, "llama.gguf", "127.0.0.1", out, "")
			f.sys.mu.Lock()
			f.sys.active = "active"
			f.sys.pid = strconv.Itoa(testPID)
			f.sys.mu.Unlock()
			if err := f.sup.Initialize(context.Background()); err != nil {
				t.Fatalf("Initialize CPU runtime: %v", err)
			}
			adopted := f.sup.Status()
			if adopted.State != StateRunningCPU || adopted.PID != testPID || adopted.UnitUnverifiable {
				t.Fatalf("healthy CPU runtime was not adopted: %+v", adopted)
			}

			f.sys.stopExit = 1
			wantCode(t, f.sup.Stop(context.Background()), CodeUnitStopFailed)
			failed := f.sup.Status()
			if failed.State != StateFailed || !failed.UnitUnverifiable || failed.PID != testPID {
				t.Fatalf("failed adopted-runtime Stop did not retain unit identity: %+v", failed)
			}
			if got := f.sup.reclaimPIDs(); len(got) != 0 {
				t.Fatalf("adopted CPU-only PID was queued during failed Stop: %v", got)
			}

			f.sys.stopExit = 0
			if err := f.sup.Stop(context.Background()); err != nil {
				t.Fatalf("retry Stop with adopted CPU evidence: %v", err)
			}
			stopped := f.sup.Status()
			if stopped.State != StateStopped || stopped.PID != 0 || stopped.UnitUnverifiable {
				t.Fatalf("successful adopted-runtime Stop status = %+v", stopped)
			}
		})
	}
}

func TestCPUOnlyExemptionRequiresMatchingPIDAndRuntime(t *testing.T) {
	for _, mismatch := range []string{"pid", "environment"} {
		t.Run(mismatch, func(t *testing.T) {
			f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
			f.sup.opts.GPU = nil
			if _, err := f.startSync(StartRequest{}); err != nil {
				t.Fatalf("Start CPU-only server: %v", err)
			}
			expectedPID := uint32(testPID)
			if mismatch == "pid" {
				expectedPID = uint32(testPID + 1)
				f.sys.mu.Lock()
				f.sys.pid = strconv.Itoa(testPID + 1)
				f.sys.mu.Unlock()
			} else {
				envPath := filepath.Join(f.procRoot, strconv.Itoa(testPID), "environ")
				raw, err := os.ReadFile(envPath)
				if err != nil {
					t.Fatal(err)
				}
				changed := bytes.Replace(raw, []byte("LLAMA_ARG_N_GPU_LAYERS=0\x00"), []byte("LLAMA_ARG_N_GPU_LAYERS=8\x00"), 1)
				if bytes.Equal(changed, raw) {
					t.Fatal("test did not find the CPU-only runtime setting")
				}
				if err := os.WriteFile(envPath, changed, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			f.sys.stopExit = 1
			wantCode(t, f.sup.Stop(context.Background()), CodeUnitStopFailed)
			if got := f.sup.reclaimPIDs(); !containsPID(got, expectedPID) {
				t.Fatalf("unmatched runtime PID was not conservatively queued: %v", got)
			}
		})
	}
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
	groupGID := uint32(os.Getgid())
	if groupGID == 0 {
		if os.Geteuid() != 0 {
			t.Skip("an expected non-root llama-server group is required for group ownership verification")
		}
		groupGID = 1
	}
	if err := os.Chown(f.dir, os.Geteuid(), int(groupGID)); err != nil {
		t.Skipf("cannot set a test group on runtime directory: %v", err)
	}
	f.sup.opts.KeySnapshotGID = groupGID
	f.sup.opts.ChownKeyFile = func(path string) error {
		f.chownCalls = append(f.chownCalls, path)
		return os.Chown(path, os.Geteuid(), int(groupGID))
	}
	key, plaintext, err := f.keys.Create("laptop")
	if err != nil {
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
	snapshots, err := filepath.Glob(filepath.Join(f.dir, "api-keys.start-*.key"))
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("runtime snapshot files = %v, %v", snapshots, err)
	}
	if !strings.Contains(string(env), `LLAMA_ARG_API_KEY_FILE="`+snapshots[0]+`"`) || strings.Contains(string(env), `LLAMA_ARG_API_KEY_FILE="`+f.keys.KeyFile+`"`) {
		t.Fatalf("env file lacks key file path: %s", env)
	}
	if strings.Contains(string(env), "llx_") {
		t.Fatal("env file must not contain a plaintext key")
	}
	snapshot, err := os.ReadFile(snapshots[0])
	if err != nil || string(snapshot) != plaintext+"\n" {
		t.Fatalf("runtime snapshot did not preserve one canonical key")
	}
	info, err := os.Stat(snapshots[0])
	if err != nil || info.Mode().Perm() != 0o640 {
		t.Fatalf("snapshot mode = %v, %v", info, err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) || stat.Gid != groupGID {
		t.Fatalf("snapshot uid/gid = %+v, want daemon uid %d and group %d", info.Sys(), os.Geteuid(), groupGID)
	}
	if len(f.chownCalls) != 1 || f.chownCalls[0] != snapshots[0] {
		t.Fatalf("key group callback paths = %v", f.chownCalls)
	}
	if err := f.keys.Delete(key.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(snapshots[0]); err != nil || string(data) != plaintext+"\n" {
		t.Fatal("deleting the mutable Store key changed the running server snapshot")
	}
}

func TestStartUsesImmutableCredentialSnapshotAcrossStoreDeletion(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.LANExposed = true
	key, plaintext, err := f.keys.Create("laptop")
	if err != nil {
		t.Fatal(err)
	}
	f.sys.startGate = make(chan struct{})
	if _, err := f.sup.StartAsync(StartRequest{}); err != nil {
		t.Fatalf("StartAsync: %v", err)
	}
	snapshots, err := filepath.Glob(filepath.Join(f.dir, "api-keys.start-*.key"))
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("runtime snapshot files = %v, %v", snapshots, err)
	}
	if err := f.keys.Delete(key.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := f.keys.Snapshot()
	if err != nil || len(remaining) != 0 {
		t.Fatalf("mutable Store after deletion has %d bytes, %v", len(remaining), err)
	}
	close(f.sys.startGate)
	st, err := f.sup.Wait(context.Background())
	if err != nil {
		t.Fatalf("Wait: %v", err)
	}
	if st.State != StateRunning {
		t.Fatalf("state = %s, want running", st.State)
	}
	env, err := os.ReadFile(f.envPath)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(env), `LLAMA_ARG_API_KEY_FILE="`+snapshots[0]+`"`) {
		t.Fatalf("llama environment does not use the immutable snapshot: %s", env)
	}
	data, err := os.ReadFile(snapshots[0])
	if err != nil || string(data) != plaintext+"\n" {
		t.Fatal("Store deletion changed the key bytes selected for llama startup")
	}
	if strings.Contains(string(env), plaintext) {
		t.Fatal("environment file contains a plaintext key")
	}
}

func TestStartRejectsLANWhenKeyStoreSnapshotIsInconsistent(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.LANExposed = true
	_, plaintext, err := f.keys.Create("laptop")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.keys.KeyFile, nil, 0o640); err != nil {
		t.Fatal(err)
	}
	_, err = f.sup.StartAsync(StartRequest{})
	wantCode(t, err, CodeInternal)
	if strings.Contains(err.Error(), plaintext) {
		t.Fatal("key snapshot error leaked a plaintext credential")
	}
	if f.sys.count("systemctl", "start") != 0 || f.sys.count("systemctl", "stop") != 0 {
		t.Fatal("inconsistent key store changed the running unit")
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

func TestStartClearsPriorUnitUnverifiableAfterVerifiedLaunch(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sup.st = Status{State: StateFailed, ModelID: "previous-model", UnitUnverifiable: true}
	st, err := f.startSync(StartRequest{})
	if err != nil {
		t.Fatalf("Start: %v", err)
	}
	if st.State != StateRunning || st.PID != testPID || st.UnitUnverifiable {
		t.Fatalf("verified launch did not clear prior unit uncertainty: %+v", st)
	}
	if err := f.sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	if st := f.sup.Status(); st.State != StateStopped || st.PID != 0 || st.UnitUnverifiable {
		t.Fatalf("verified stop did not clear unit uncertainty: %+v", st)
	}
}

func TestStartInvalidOverride(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	bad := "lots"
	_, err := f.sup.StartAsync(StartRequest{Overrides: autoconf.Overrides{NGL: &bad}})
	wantCode(t, err, CodeInvalidOverrides)
}

func TestInvalidStartRequestDoesNotStopHealthyActiveServer(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.mu.Lock()
	f.sys.active = "active"
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.mu.Unlock()
	_, err := f.sup.StartAsync(StartRequest{ModelID: "missing.gguf"})
	wantCode(t, err, CodeNoModel)
	f.cfg.Port = 0
	_, err = f.sup.StartAsync(StartRequest{})
	wantCode(t, err, CodeInvalidSettings)
	if f.sys.count("systemctl", "stop") != 0 || f.sys.count("systemctl", "start") != 0 {
		t.Fatal("invalid model or settings stopped/replaced the healthy active unit")
	}
	f.sys.mu.Lock()
	active, pid := f.sys.active, f.sys.pid
	f.sys.mu.Unlock()
	if active != "active" || pid != strconv.Itoa(testPID) {
		t.Fatalf("active unit changed after invalid request: %s/%s", active, pid)
	}
}

func TestRestartWaitsForOldGPUProcessAndKeepsExternalUsage(t *testing.T) {
	snap := readyGPU()
	snap.Devices[0].MemFreeMiB = 1564
	snap.Devices[0].MemUsedMiB = 23000
	snap.Devices[0].Processes = []platform.GPUProcess{
		{PID: testPID, UsedMiB: 17000},
		{PID: 9000, UsedMiB: 6000},
	}
	f := newFixture(t, snap, "ready")
	f.sys.mu.Lock()
	f.sys.active = "active"
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.newPID = 4343
	f.sys.journal = "load_tensors: offloaded 33/33 layers to GPU\n"
	f.sys.mu.Unlock()
	st, err := f.startSync(StartRequest{})
	if err != nil {
		t.Fatalf("restart: %v", err)
	}
	if st.State != StateRunning || st.Plan == nil || st.Plan.CPUOnly || st.Plan.NGL == "0" {
		t.Fatalf("restart plan ignored released server memory: %+v", st)
	}
	probe := f.sup.opts.GPU.(*gpuFake)
	probe.mu.Lock()
	history := append([]platform.GPUSnapshot(nil), probe.history...)
	probe.mu.Unlock()
	if len(history) < 2 {
		t.Fatalf("GPU snapshots = %d, want reclaim and planning observations", len(history))
	}
	first := history[0].Devices[0]
	if first.MemFreeMiB != 18564 {
		t.Fatalf("post-reclaim free VRAM = %d MiB, want 18564 MiB with external usage retained", first.MemFreeMiB)
	}
	seenExternal, seenOld := false, false
	for _, process := range first.Processes {
		seenExternal = seenExternal || process.PID == 9000
		seenOld = seenOld || process.PID == testPID
	}
	if !seenExternal || seenOld {
		t.Fatalf("post-reclaim GPU processes = %+v; external PID must remain and old unit PID must be gone", first.Processes)
	}
	probe.mu.Lock()
	final := append([]platform.GPUProcess(nil), probe.snap.Devices[0].Processes...)
	probe.mu.Unlock()
	seenNew, seenExternal, seenOld := false, false, false
	for _, process := range final {
		seenNew = seenNew || process.PID == 4343
		seenExternal = seenExternal || process.PID == 9000
		seenOld = seenOld || process.PID == testPID
	}
	if !seenNew || !seenExternal || seenOld {
		t.Fatalf("post-start GPU processes = %+v", final)
	}
}

func TestRestartGPUReclaimWaitIsBoundedAndCancellable(t *testing.T) {
	t.Run("bounded", func(t *testing.T) {
		f := newFixture(t, readyGPU(), "ready")
		f.sys.mu.Lock()
		f.sys.active = "active"
		f.sys.pid = strconv.Itoa(testPID)
		f.sys.mu.Unlock()
		probe := f.sup.opts.GPU.(*gpuFake)
		probe.mu.Lock()
		probe.holdReclaim = true
		probe.mu.Unlock()
		f.sup.opts.ReclaimTimeout = 2 * time.Second
		f.sup.opts.ReclaimPollInterval = 500 * time.Millisecond
		_, err := f.startSync(StartRequest{})
		wantCode(t, err, CodeUnitStopFailed)
		if f.sys.count("systemctl", "start") != 0 || f.sup.Status().PID != 0 {
			t.Fatal("new server started before the old GPU process was reclaimed")
		}
		if elapsed := f.clk.now().Sub(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)); elapsed < 2*time.Second {
			t.Fatalf("reclaim polling stopped after %s, want bounded 2s deadline", elapsed)
		}
	})
	t.Run("cancellable", func(t *testing.T) {
		f := newFixture(t, readyGPU(), "ready")
		f.sys.mu.Lock()
		f.sys.active = "active"
		f.sys.pid = strconv.Itoa(testPID)
		f.sys.mu.Unlock()
		f.sup.opts.ReclaimTimeout = 2 * time.Second
		f.sup.opts.ReclaimPollInterval = 500 * time.Millisecond
		probe := f.sup.opts.GPU.(*gpuFake)
		probe.mu.Lock()
		probe.holdReclaim = true
		probe.mu.Unlock()
		f.clk.block = make(chan struct{})
		f.clk.sleeping = make(chan struct{})
		sleeping := f.clk.sleeping
		block := f.clk.block
		if _, err := f.sup.StartAsync(StartRequest{}); err != nil {
			t.Fatalf("StartAsync: %v", err)
		}
		select {
		case <-sleeping:
		case <-time.After(2 * time.Second):
			t.Fatal("start did not enter cancellable GPU reclaim polling")
		}
		f.sup.mu.Lock()
		cancel := f.sup.cancel
		f.sup.mu.Unlock()
		cancel()
		if _, err := f.sup.Wait(context.Background()); err == nil {
			t.Fatal("canceled reclaim unexpectedly succeeded")
		}
		close(block)
		f.clk.block = nil
		if _, err := f.sup.StartAsync(StartRequest{}); err != nil {
			t.Fatalf("retry StartAsync: %v", err)
		}
		_, err := f.sup.Wait(context.Background())
		wantCode(t, err, CodeUnitStopFailed)
		if f.sys.count("systemctl", "start") != 0 {
			t.Fatal("retry started before the canceled old GPU PID was reclaimed")
		}
		probe.mu.Lock()
		probe.holdReclaim = false
		probe.mu.Unlock()
		probe.dropPID(testPID)
		if _, err := f.sup.StartAsync(StartRequest{}); err != nil {
			t.Fatalf("final retry StartAsync: %v", err)
		}
		if _, err := f.sup.Wait(context.Background()); err != nil {
			t.Fatalf("final retry after process release: %v", err)
		}
		if err := f.sup.Stop(context.Background()); err != nil {
			t.Fatalf("Stop after retry: %v", err)
		}
	})
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
	f.sup.mu.Lock()
	f.sup.st.UnitUnverifiable = true
	f.sup.mu.Unlock()
	if err := f.sup.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	st := f.sup.Status()
	if st.State != StateStopped || st.PID != 0 || st.UnitUnverifiable {
		t.Fatalf("status after Stop = %+v", st)
	}
}

func TestStopReportsUnitStopFailure(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.LANExposed = true
	_, plaintext, err := f.keys.Create("lan")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	snapshots, err := filepath.Glob(filepath.Join(f.dir, keySnapshotPrefix+"*.key"))
	if err != nil || len(snapshots) != 1 {
		t.Fatalf("active snapshots = %v, %v", snapshots, err)
	}
	f.sys.stopExit = 1
	err = f.sup.Stop(context.Background())
	wantCode(t, err, CodeUnitStopFailed)
	st := f.sup.Status()
	if st.State != StateFailed || !st.UnitUnverifiable || st.PID != testPID {
		t.Fatalf("a failed stop must retain an unverifiable active MainPID: %+v", st)
	}
	data, readErr := os.ReadFile(snapshots[0])
	if readErr != nil || string(data) != plaintext+"\n" {
		t.Fatalf("failed stop removed or changed the active credential snapshot: %v", readErr)
	}
}

func TestRecordFailedStateTracksUnitVerifiability(t *testing.T) {
	t.Run("state unreadable", func(t *testing.T) {
		f := newFixture(t, readyGPU(), "ready")
		f.sup.st = Status{State: StateRunning, ModelID: "known-model", PID: 9876}
		f.sys.stateErr = errors.New("systemctl unavailable")
		f.sys.pid = strconv.Itoa(testPID)
		f.sup.recordFailedState(context.Background(), errorf(CodeUnitCheck, "test failure"))
		st := f.sup.Status()
		if !st.UnitUnverifiable || st.PID != testPID || st.ModelID != "known-model" {
			t.Fatalf("unreadable unit state lost evidence: %+v", st)
		}
	})
	t.Run("MainPID unreadable", func(t *testing.T) {
		f := newFixture(t, readyGPU(), "ready")
		f.sup.st = Status{State: StateRunning, ModelID: "known-model", PID: 9876}
		f.sys.active = "active"
		f.sys.pidErr = errors.New("systemctl show failed")
		f.sup.recordFailedState(context.Background(), errorf(CodeUnitCheck, "test failure"))
		st := f.sup.Status()
		if !st.UnitUnverifiable || st.PID != 9876 || st.ModelID != "known-model" {
			t.Fatalf("unreadable MainPID lost prior evidence: %+v", st)
		}
	})
	t.Run("verified stopped", func(t *testing.T) {
		f := newFixture(t, readyGPU(), "ready")
		f.sup.st = Status{State: StateRunning, ModelID: "known-model", PID: testPID, UnitUnverifiable: true}
		f.sup.recordFailedState(context.Background(), errorf(CodeUnitStopFailed, "test failure"))
		st := f.sup.Status()
		if st.UnitUnverifiable || st.PID != 0 {
			t.Fatalf("verified inactive unit remained unverifiable: %+v", st)
		}
	})
}

func TestFailedStartPreparationRetainsActivePIDAndPriorModel(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.mu.Lock()
	f.sys.active = "active"
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.stopExit = 1
	f.sys.mu.Unlock()
	f.sup.st = Status{State: StateRunning, ModelID: "known-manual-model", Host: "127.0.0.1", Port: f.cfg.Port, PID: testPID}
	_, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeUnitStopFailed)
	st := f.sup.Status()
	if st.State != StateFailed || !st.UnitUnverifiable || st.PID != testPID || st.ModelID != "known-manual-model" {
		t.Fatalf("failed replacement did not retain old server evidence: %+v", st)
	}
}

func TestFailedStartCleanupRetainsActivePID(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.mu.Lock()
	f.sys.stopExit = 1
	f.sys.journal = "server started without a verifiable offload line\n"
	f.sys.mu.Unlock()
	_, err := f.startSync(StartRequest{})
	wantCode(t, err, CodeGPUOffloadFailed)
	st := f.sup.Status()
	if st.State != StateFailed || !st.UnitUnverifiable || st.PID != testPID || st.ModelID != "llama.gguf" {
		t.Fatalf("failed start cleanup lost its active MainPID: %+v", st)
	}
}

func TestWatchDetectsDeadServer(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.sys.activeSeq = []string{"failed"}
	f.sys.pid = "0"
	f.sup.watchOnce(context.Background())
	st := f.sup.Status()
	if st.State != StateFailed || st.LastError == nil || st.LastError.Code != CodeServerExited {
		t.Fatalf("status = %+v", st)
	}
	if f.sys.count("systemctl", "start") != 1 {
		t.Fatal("Watch must not start another server")
	}
}

func TestWatchRetainsPIDForTransitionalUnit(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	f.sys.mu.Lock()
	f.sys.activeSeq = []string{"deactivating"}
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.mu.Unlock()
	f.sup.watchOnce(context.Background())
	st := f.sup.Status()
	if st.State != StateFailed || !st.UnitUnverifiable || st.PID != testPID || st.ModelID != "llama.gguf" {
		t.Fatalf("transitional unit evidence was discarded: %+v", st)
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

func TestApplyFirewallSerializesConfigReadThroughNft(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	cfgMu := sync.Mutex{}
	cfg := config.Default()
	cfg.Port = f.cfg.Port
	cfg.LANExposed = true
	reads := 0
	firstRead := make(chan struct{})
	secondRead := make(chan struct{})
	releaseFirst := make(chan struct{})
	f.sup.opts.Config = func() config.Config {
		cfgMu.Lock()
		reads++
		readNumber := reads
		value := cfg
		cfgMu.Unlock()
		if readNumber == 1 {
			close(firstRead)
			<-releaseFirst
		}
		if readNumber == 2 {
			close(secondRead)
		}
		return value
	}
	results := make(chan error, 2)
	go func() { results <- f.sup.ApplyFirewall(context.Background()) }()
	<-firstRead
	cfgMu.Lock()
	cfg.LANExposed = false
	cfgMu.Unlock()
	go func() { results <- f.sup.ApplyFirewall(context.Background()) }()
	select {
	case <-secondRead:
		close(releaseFirst)
		t.Fatal("the second firewall update read config before the first nft application finished")
	case <-time.After(100 * time.Millisecond):
	}
	close(releaseFirst)
	for range 2 {
		if err := <-results; err != nil {
			t.Fatalf("ApplyFirewall: %v", err)
		}
	}
	if reads != 2 {
		t.Fatalf("config reads = %d, want 2", reads)
	}
	rules, err := os.ReadFile(f.fwPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(rules), fmt.Sprintf("tcp dport %d accept", f.cfg.Port)) {
		t.Fatalf("stale LAN-on rules were last applied after LAN-off: %s", rules)
	}
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
	f.sys.lightdm = true
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

func TestInitializeAdoptsActiveManualModelForEitherAutostartValue(t *testing.T) {
	for _, autostart := range []bool{false, true} {
		t.Run(fmt.Sprintf("autostart_%v", autostart), func(t *testing.T) {
			f := newFixture(t, readyGPU(), "ready")
			f.cfg.Autostart = autostart
			manualPath := filepath.Join(f.dir, "manual.gguf")
			if err := os.WriteFile(manualPath, ggufFile("llama", 32, 8192, 4096), 0o644); err != nil {
				t.Fatal(err)
			}
			out := autoconf.Output{
				NGL:            "12",
				Ctx:            4096,
				CacheTypeK:     "f16",
				CacheTypeV:     "f16",
				Batch:          768,
				LoadMode:       "mmap",
				VisibleDevices: []int{0},
			}
			f.sys.journal = "load_tensors: offloaded 12/33 layers to GPU\n"
			writeActiveRuntime(t, f, testPID, "manual.gguf", "127.0.0.1", out, "")
			f.sys.mu.Lock()
			f.sys.active = "active"
			f.sys.pid = strconv.Itoa(testPID)
			f.sys.mu.Unlock()

			if err := f.sup.Initialize(context.Background()); err != nil {
				t.Fatalf("Initialize: %v", err)
			}
			st := f.sup.Status()
			if st.State != StateRunning || st.ModelID != "manual.gguf" || st.PID != testPID || st.Port != f.cfg.Port || st.UnitUnverifiable {
				t.Fatalf("active manual server was not restored: %+v", st)
			}
			if st.Plan == nil || st.Plan.NGL != "12" || st.Plan.Ctx != 4096 || st.Plan.Batch != 768 {
				t.Fatalf("runtime overrides were not restored: %+v", st.Plan)
			}
			if !st.Offload.Verified || st.Offload.Offloaded != 12 {
				t.Fatalf("GPU offload state was not verified: %+v", st.Offload)
			}
			if f.sys.count("systemctl", "start") != 0 || f.sys.count("systemctl", "stop") != 0 {
				t.Fatal("Initialize replaced or stopped the healthy active server")
			}
		})
	}
}

func TestInitializeAdoptsVerifiedCPUFallback(t *testing.T) {
	f := newFixture(t, platform.GPUSnapshot{}, "no_gpu")
	out := autoconf.Output{
		NGL:        "0",
		Ctx:        4096,
		CacheTypeK: "f16",
		CacheTypeV: "f16",
		Batch:      512,
		LoadMode:   "mmap",
		CPUOnly:    true,
	}
	writeActiveRuntime(t, f, testPID, "llama.gguf", "127.0.0.1", out, "")
	f.sys.mu.Lock()
	f.sys.active = "active"
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.mu.Unlock()
	if err := f.sup.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	st := f.sup.Status()
	if st.State != StateRunningCPU || st.PID != testPID || st.Plan == nil || !st.Plan.CPUOnly || st.Offload.Verified || st.UnitUnverifiable {
		t.Fatalf("CPU fallback state was not restored: %+v", st)
	}
	if !strings.Contains(st.Offload.Note, "CPU-only runtime environment and health endpoint verified") {
		t.Fatalf("CPU runtime state was not verified: %+v", st.Offload)
	}
	for _, warnings := range [][]string{st.Warnings, st.Plan.Warnings} {
		if !strings.Contains(strings.Join(warnings, "\n"), "adopted CPU-only runtime (N_GPU_LAYERS=0)") || !strings.Contains(strings.Join(warnings, "\n"), "health endpoint returned HTTP 200") {
			t.Fatalf("CPU adoption warnings do not explain the restored runtime: %q", warnings)
		}
	}
	if f.sys.count("systemctl", "start") != 0 {
		t.Fatal("CPU fallback adoption was replaced by autostart")
	}
}

func TestInitializePreservesActiveCredentialSnapshot(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.Autostart = false
	f.cfg.LANExposed = true
	key, plaintext, err := f.keys.Create("lan")
	if err != nil {
		t.Fatal(err)
	}
	runtimeKeyFile, err := f.sup.writeKeySnapshot([]byte(plaintext + "\n"))
	if err != nil {
		t.Fatalf("write runtime snapshot: %v", err)
	}
	out := autoconf.Output{
		NGL:            "all",
		Ctx:            8192,
		CacheTypeK:     "f16",
		CacheTypeV:     "f16",
		Batch:          2048,
		LoadMode:       "mmap",
		VisibleDevices: []int{0},
	}
	writeActiveRuntime(t, f, testPID, "llama.gguf", "0.0.0.0", out, runtimeKeyFile)
	f.sys.mu.Lock()
	f.sys.active = "active"
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.mu.Unlock()
	if err := f.sup.Initialize(context.Background()); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	if st := f.sup.Status(); st.State != StateRunning || !st.LANExposed || st.PID != testPID {
		t.Fatalf("active LAN server was not adopted: %+v", st)
	}
	if data, err := os.ReadFile(runtimeKeyFile); err != nil || string(data) != plaintext+"\n" {
		t.Fatal("daemon reconciliation changed the active runtime credential snapshot")
	}
	if err := f.keys.Delete(key.ID); err != nil {
		t.Fatal(err)
	}
	if data, err := os.ReadFile(runtimeKeyFile); err != nil || string(data) != plaintext+"\n" {
		t.Fatal("mutable Store deletion changed the adopted server credential snapshot")
	}
}

func TestInitializeRejectsLegacyStoreKeyFileForLAN(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.LANExposed = true
	if _, _, err := f.keys.Create("legacy-key"); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.keys.KeyFile, 0o640); err != nil {
		t.Fatal(err)
	}
	out := autoconf.Output{NGL: "0", Ctx: 4096, CacheTypeK: "f16", CacheTypeV: "f16", Batch: 512, LoadMode: "mmap", CPUOnly: true}
	writeActiveRuntime(t, f, testPID, "llama.gguf", "0.0.0.0", out, f.keys.KeyFile)
	f.sys.mu.Lock()
	f.sys.active = "active"
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.mu.Unlock()
	if err := f.sup.Initialize(context.Background()); err == nil {
		t.Fatal("Initialize adopted a LAN server using the mutable legacy key store path")
	}
	st := f.sup.Status()
	f.sys.mu.Lock()
	active, pid := f.sys.active, f.sys.pid
	f.sys.mu.Unlock()
	if active != "inactive" || pid != "0" || f.sys.count("systemctl", "stop") != 1 || st.State != StateFailed || st.PID != 0 || st.UnitUnverifiable {
		t.Fatalf("legacy mutable LAN key file was adopted or reported inaccurately: unit=%s/%s status=%+v", active, pid, st)
	}
}

func TestRuntimeCredentialRequiresDaemonOwnershipGroupAndSafeDirectory(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	groupGID := uint32(os.Getgid())
	if groupGID == 0 {
		if os.Geteuid() != 0 {
			t.Skip("an expected non-root llama-server group is required for group ownership verification")
		}
		groupGID = 1
	}
	if err := os.Chown(f.dir, os.Geteuid(), int(groupGID)); err != nil {
		t.Skipf("cannot set a test group on runtime directory: %v", err)
	}
	f.sup.opts.KeySnapshotGID = groupGID
	_, plaintext, err := f.keys.Create("runtime-key")
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.dir, keySnapshotPrefix+"verification.key")
	writeValid := func(uid, gid int) {
		t.Helper()
		if err := os.WriteFile(path, []byte(plaintext+"\n"), 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(path, 0o640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, uid, gid); err != nil {
			t.Skipf("cannot set credential test ownership: %v", err)
		}
	}
	writeValid(os.Geteuid(), int(groupGID))
	if got, err := f.sup.readRuntimeCredentialFile(path); err != nil || string(got) != plaintext+"\n" {
		t.Fatalf("valid owned 0640 runtime file rejected: err=%v", err)
	}

	if err := os.Chmod(path, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sup.readRuntimeCredentialFile(path); err == nil {
		t.Fatal("runtime key file with mode 0600 passed validation")
	}
	writeValid(os.Geteuid(), int(groupGID))

	if os.Geteuid() == 0 {
		wrongGID := 2
		if uint32(wrongGID) == groupGID {
			wrongGID = 3
		}
		if err := os.Chown(path, os.Geteuid(), wrongGID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.sup.readRuntimeCredentialFile(path); err == nil {
			t.Fatal("runtime key file with the wrong llama-server group passed validation")
		}
		writeValid(1, int(groupGID))
		if _, err := f.sup.readRuntimeCredentialFile(path); err == nil {
			t.Fatal("runtime key file owned by a different user passed validation")
		}
		writeValid(os.Geteuid(), int(groupGID))
		if err := os.Chown(f.dir, os.Geteuid(), wrongGID); err != nil {
			t.Fatal(err)
		}
		if _, err := f.sup.readRuntimeCredentialFile(path); err == nil {
			t.Fatal("runtime key directory with the wrong llama-server group passed validation")
		}
		if err := os.Chown(f.dir, os.Geteuid(), int(groupGID)); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Log("wrong-user and wrong-group ownership cases require privileged chown; production ownership checks were exercised by the valid uid/gid case")
	}

	writeValid(os.Geteuid(), int(groupGID))
	if err := os.Chmod(f.dir, 0o770); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sup.readRuntimeCredentialFile(path); err == nil {
		t.Fatal("group-writable key directory passed validation")
	}
	if err := os.Chmod(f.dir, 0o750); err != nil {
		t.Fatal(err)
	}
	linkPath := path + ".link"
	if err := os.Symlink(path, linkPath); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sup.readRuntimeCredentialFile(linkPath); err == nil {
		t.Fatal("symlink key file passed runtime credential validation")
	}
	if err := os.Remove(linkPath); err != nil {
		t.Fatal(err)
	}
	// 부모는 t.TempDir 전용이며 owner(root) 실행 권한만으로는 서비스 그룹 통과를 증명하지 못한다.
	parent := filepath.Dir(f.dir)
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := f.sup.readRuntimeCredentialFile(path); err == nil {
		t.Fatal("a root-only parent directory was accepted for the llama-server group")
	}
	if err := os.Chmod(parent, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestInitializeStopsUnsafeActiveUnitOrReportsItsPID(t *testing.T) {
	for _, stopFails := range []bool{false, true} {
		t.Run(fmt.Sprintf("stop_fails_%v", stopFails), func(t *testing.T) {
			f := newFixture(t, readyGPU(), "ready")
			f.cfg.Autostart = true
			procDir := filepath.Join(f.procRoot, strconv.Itoa(testPID))
			if err := os.MkdirAll(procDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink("/bin/sleep", filepath.Join(procDir, "exe")); err != nil {
				t.Fatal(err)
			}
			f.sys.mu.Lock()
			f.sys.active = "active"
			f.sys.pid = strconv.Itoa(testPID)
			if stopFails {
				f.sys.stopExit = 1
			}
			f.sys.mu.Unlock()

			err := f.sup.Initialize(context.Background())
			if err == nil {
				t.Fatal("unsafe active process unexpectedly passed startup reconciliation")
			}
			st := f.sup.Status()
			if st.State != StateFailed || f.sys.count("systemctl", "start") != 0 {
				t.Fatalf("unsafe active server state = %+v, starts=%d", st, f.sys.count("systemctl", "start"))
			}
			f.sys.mu.Lock()
			active, pid := f.sys.active, f.sys.pid
			f.sys.mu.Unlock()
			if stopFails {
				if active != "active" || pid != strconv.Itoa(testPID) || st.PID != testPID {
					t.Fatalf("failed stop did not report the active PID: unit=%s/%s status=%+v", active, pid, st)
				}
			} else if active != "inactive" || pid != "0" || st.PID != 0 {
				t.Fatalf("unsafe server was not stopped accurately: unit=%s/%s status=%+v", active, pid, st)
			}
		})
	}
}

func TestInitializeRejectsEnvironmentFileFromFailedRestart(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.Autostart = true
	manual := autoconf.Output{
		NGL:            "12",
		Ctx:            4096,
		CacheTypeK:     "f16",
		CacheTypeV:     "f16",
		Batch:          768,
		LoadMode:       "mmap",
		VisibleDevices: []int{0},
	}
	writeActiveRuntime(t, f, testPID, "llama.gguf", "127.0.0.1", manual, "")
	defaultEnv, err := llamaargs.Env(autoconf.Output{
		NGL:            "all",
		Ctx:            8192,
		CacheTypeK:     "f16",
		CacheTypeV:     "f16",
		Batch:          2048,
		LoadMode:       "mmap",
		VisibleDevices: []int{0},
	}, llamaargs.ServerSettings{
		ModelPath: filepath.Join(f.dir, "llama.gguf"),
		Host:      "127.0.0.1",
		Port:      f.cfg.Port,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.envPath, []byte(llamaargs.EnvFile(defaultEnv)), 0o644); err != nil {
		t.Fatal(err)
	}
	f.sys.journal = "load_tensors: offloaded 12/33 layers to GPU\n"
	f.sys.mu.Lock()
	f.sys.active = "active"
	f.sys.pid = strconv.Itoa(testPID)
	f.sys.mu.Unlock()

	err = f.sup.Initialize(context.Background())
	if err == nil {
		t.Fatal("stale environment file was accepted for the active MainPID")
	}
	if st := f.sup.Status(); st.State != StateFailed || st.PID != 0 {
		t.Fatalf("stale environment was not stopped and reported: %+v", st)
	}
	if f.sys.count("systemctl", "stop") != 1 || f.sys.count("systemctl", "start") != 0 {
		t.Fatal("unsafe active process was not stopped without default autostart")
	}
}

func TestSSHStatusMissingUnit(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	st, err := f.sup.SSHStatus(context.Background())
	if err != nil {
		t.Fatalf("SSHStatus: %v", err)
	}
	if st.Installed || st.Active {
		t.Fatalf("status = %+v, want not installed", st)
	}
}

func TestSSHStatusInstalledActiveAndInactive(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.sshInstalled = true
	st, err := f.sup.SSHStatus(context.Background())
	if err != nil || !st.Installed || st.Active {
		t.Fatalf("inactive: %+v, %v", st, err)
	}
	f.sys.sshActive = "active"
	st, err = f.sup.SSHStatus(context.Background())
	if err != nil || !st.Installed || !st.Active {
		t.Fatalf("active: %+v, %v", st, err)
	}
}

func TestSetSSHUnitEnableRequiresInstalledUnit(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	wantCode(t, f.sup.SetSSHUnit(context.Background(), true), CodeSSHNotInstalled)
	if f.sys.count("systemctl", "start") != 0 {
		t.Fatal("start must not run when the unit is missing")
	}
}

func TestSetSSHUnitEnableAndDisable(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.sshInstalled = true
	if err := f.sup.SetSSHUnit(context.Background(), true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if f.sys.count("systemctl", "start") != 1 || f.sys.sshActive != "active" {
		t.Fatalf("start calls = %d, active = %q", f.sys.count("systemctl", "start"), f.sys.sshActive)
	}
	// 데몬은 /etc 에 쓸 수 없으므로 enable 과 disable 을 부르면 안 된다.
	if f.sys.count("systemctl", "enable") != 0 {
		t.Fatal("enable must never be called from the daemon")
	}
	if err := f.sup.SetSSHUnit(context.Background(), false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if f.sys.count("systemctl", "stop") != 1 || f.sys.sshActive != "inactive" {
		t.Fatalf("stop calls = %d, active = %q", f.sys.count("systemctl", "stop"), f.sys.sshActive)
	}
	if f.sys.count("systemctl", "disable") != 0 {
		t.Fatal("disable must never be called from the daemon")
	}
}

func TestSetSSHUnitDisableRunsEvenWhenNotInstalled(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.sshExit = 1
	wantCode(t, f.sup.SetSSHUnit(context.Background(), false), CodeSSHChangeFailed)
	if f.sys.count("systemctl", "stop") != 1 {
		t.Fatal("stop must be attempted")
	}
}

func TestApplyFirewallOpensSSHOnlyWhenEnabled(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if err := f.sup.ApplyFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(f.fwPath)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "tcp dport 22 accept") {
		t.Fatalf("SSH must be closed by default: %s", data)
	}
	f.cfg.SSHEnabled = true
	if err := f.sup.ApplyFirewall(context.Background()); err != nil {
		t.Fatal(err)
	}
	data, _ = os.ReadFile(f.fwPath)
	if !strings.Contains(string(data), "tcp dport 22 accept") {
		t.Fatalf("SSH port is not open: %s", data)
	}
}

func TestSSHStatusReadsPasswordAuthentication(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.sys.sshInstalled = true
	f.sys.sshdOutput = "port 22\npasswordauthentication no\npermitrootlogin prohibit-password\n"
	st, err := f.sup.SSHStatus(context.Background())
	if err != nil || st.PasswordAuth != "no" {
		t.Fatalf("status = %+v, %v", st, err)
	}
	f.sys.sshdOutput = "passwordauthentication yes\n"
	st, _ = f.sup.SSHStatus(context.Background())
	if st.PasswordAuth != "yes" {
		t.Fatalf("status = %+v", st)
	}
	f.sys.sshdExit = 255
	st, _ = f.sup.SSHStatus(context.Background())
	if st.PasswordAuth != "unknown" {
		t.Fatalf("sshd -T failure must give unknown, got %+v", st)
	}
}
