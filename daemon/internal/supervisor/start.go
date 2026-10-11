package supervisor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/nxodh/lightlixos/daemon/internal/autoconf"
	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/llamaargs"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

// startJob은 Start 한 번의 입력이다. 설정은 시작 시점의 값을 고정해 쓴다.
type startJob struct {
	model models.Model
	ov    autoconf.Overrides
	cfg   config.Config
}

// run은 시작 순서를 수행한다. 실패하면 유닛을 멈추고 실패 오류를 돌려준다. 성공하면 nil 이다.
// 호출자는 수명 주기 세마포어를 잡고 있어야 한다.
func (s *Supervisor) run(ctx context.Context, job startJob) *Error {
	pr := s.buildPlan(ctx, job.model, job.ov, job.cfg)
	s.mu.Lock()
	s.st.Plan = &pr.Output
	s.st.Warnings = append([]string{}, pr.Output.Warnings...)
	s.mu.Unlock()

	keys, err := s.keyCount()
	if err != nil {
		return s.fail(ctx, errorf(CodeInternal, "read API keys: %v", err))
	}
	host := "127.0.0.1"
	if job.cfg.LANExposed {
		host = "0.0.0.0"
	}
	settings := llamaargs.ServerSettings{
		ModelPath: job.model.Path,
		Host:      host,
		Port:      job.cfg.Port,
	}
	if keys > 0 {
		settings.APIKeyFile = s.opts.Keys.KeyFile
	}
	env, err := llamaargs.Env(pr.Output, settings)
	if err != nil {
		return s.fail(ctx, errorf(CodeInvalidSettings, "%v", err))
	}

	if !pr.Output.CPUOnly {
		if e := s.checkCUDA(ctx, env); e != nil {
			return s.fail(ctx, e)
		}
	}

	if err := writeFileAtomic(s.opts.EnvFilePath, []byte(llamaargs.EnvFile(env)), 0o644); err != nil {
		return s.fail(ctx, errorf(CodeEnvFile, "write %s: %v", s.opts.EnvFilePath, err))
	}

	// 저널 검사는 이 시각 이후의 줄만 본다. 초 단위라 같은 초의 이전 줄이 섞일 수 있다.
	sinceUnix := s.now().Unix()
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{"restart", s.opts.Unit},
		Timeout:   restartTimeout,
		MaxOutput: maxCommandOutput,
	})
	if err != nil {
		return s.fail(ctx, errorf(CodeUnitStartFailed, "systemctl restart: %v", err))
	}
	if res.ExitCode != 0 {
		return s.fail(ctx, errorf(CodeUnitStartFailed, "systemctl restart exited %d: %s", res.ExitCode, excerpt(string(res.Stderr), 512)))
	}

	if e := s.waitHealthy(ctx, job.cfg.Port); e != nil {
		return s.fail(ctx, e)
	}

	pid, err := s.mainPID(ctx)
	if err != nil {
		return s.fail(ctx, errorf(CodeUnitCheck, "read MainPID: %v", err))
	}
	if pid == 0 {
		return s.fail(ctx, errorf(CodeServerExited, "the unit has no main process after the health check"))
	}

	var off Offload
	if pr.Output.CPUOnly {
		off = Offload{Requested: "0", Note: "CPU-only plan; GPU offload is not attempted"}
	} else {
		o, e := s.verifyOffload(ctx, pr.Output, sinceUnix, pid)
		if e != nil {
			return s.fail(ctx, e)
		}
		off = o
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.st.PID = int(pid)
	s.st.Offload = off
	s.st.LastError = nil
	s.st.Since = s.now()
	if pr.Output.CPUOnly {
		s.st.State = StateRunningCPU
		s.st.Warnings = append(s.st.Warnings, cpuFallbackWarning(pr.GPU))
	} else {
		s.st.State = StateRunning
	}
	return nil
}

// cpuFallbackWarning은 CPU 전용 계획일 때 GPU 상태를 경고 한 줄로 만든다.
func cpuFallbackWarning(g GPUSummary) string {
	if !g.StatusChecked {
		return "running on CPU: the GPU check has not run (no GPU status file)"
	}
	return fmt.Sprintf("running on CPU: GPU status state=%s, message=%s", g.State, g.Message)
}

// fail은 실패를 기록하고 유닛을 멈춘다. 멈추지 못하면 그 사실을 오류 메시지에 덧붙인다.
func (s *Supervisor) fail(ctx context.Context, e *Error) *Error {
	if stopErr := s.stopUnit(context.WithoutCancel(ctx)); stopErr != nil {
		e = &Error{Code: e.Code, Message: e.Message + "; the unit could not be stopped: " + stopErr.Message}
	}
	s.mu.Lock()
	s.st.State = StateFailed
	s.st.PID = 0
	s.st.LastError = e
	s.mu.Unlock()
	s.logf("start failed: %s", e)
	return e
}

// checkCUDA는 서버와 같은 CUDA 환경 변수로 --list-devices 를 실행해 장치가 하나 이상 있는지 본다.
// 장치가 없으면 CUDA 백엔드가 로드되지 않은 것이고, llama-server 는 그때 CPU로 조용히 돈다.
func (s *Supervisor) checkCUDA(ctx context.Context, env map[string]string) *Error {
	var cudaEnv []string
	for _, k := range []string{"CUDA_VISIBLE_DEVICES", "CUDA_DEVICE_ORDER"} {
		if v, ok := env[k]; ok {
			cudaEnv = append(cudaEnv, k+"="+v)
		}
	}
	res, err := s.exec(ctx, platform.Command{
		Name:      s.opts.ServerBinary,
		Args:      []string{"--list-devices"},
		Env:       cudaEnv,
		Timeout:   listDevicesTimeout,
		MaxOutput: maxCommandOutput,
	})
	if err != nil {
		return errorf(CodeCUDABackend, "cannot run llama-server --list-devices: %v", err)
	}
	out := string(res.Stdout) + "\n" + string(res.Stderr)
	if res.ExitCode != 0 {
		return errorf(CodeCUDABackend, "llama-server --list-devices exited %d: %s", res.ExitCode, excerpt(out, 1024))
	}
	if len(llamaargs.ParseListDevices(out)) == 0 {
		return errorf(CodeCUDABackend, "llama-server lists no device, so the CUDA backend is not loaded and the server would silently run on CPU: %s", excerpt(out, 1024))
	}
	return nil
}

// waitHealthy는 /health 가 200 이 될 때까지 기다린다. 유닛이 로딩 중에 죽으면 바로 실패한다.
func (s *Supervisor) waitHealthy(ctx context.Context, port int) *Error {
	deadline := s.now().Add(s.opts.HealthTimeout)
	for {
		if err := ctx.Err(); err != nil {
			return errorf(CodeCanceled, "start was canceled: %v", err)
		}
		state, err := s.unitState(ctx)
		if err != nil {
			return errorf(CodeUnitCheck, "cannot read unit state: %v", err)
		}
		if state == "failed" || state == "inactive" {
			return errorf(CodeServerExited, "the unit became %s while loading: %s", state, s.journalTail(ctx))
		}
		if s.healthOK(ctx, port) {
			return nil
		}
		if !s.now().Before(deadline) {
			return errorf(CodeHealthTimeout, "/health did not return 200 within %s: %s", s.opts.HealthTimeout, s.journalTail(ctx))
		}
		if err := s.opts.Sleep(ctx, s.opts.PollInterval); err != nil {
			return errorf(CodeCanceled, "start was canceled: %v", err)
		}
	}
}

// healthOK는 127.0.0.1 의 /health 가 200 을 돌려주는지 본다. /health 는 키 없이 열려 있다.
func (s *Supervisor) healthOK(ctx context.Context, port int) bool {
	rctx, cancel := context.WithTimeout(ctx, healthRequestTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, "http://127.0.0.1:"+strconv.Itoa(port)+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := s.opts.HTTP.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	return resp.StatusCode == http.StatusOK
}

// verifyOffload는 시작 이후의 저널에서 오프로드 줄을 찾고, NGL 계획과 맞는지 본다.
// 그다음 NVML 프로세스 목록에 메인 PID 가 있는지 본다. 프로세스 목록을 알 수 없으면 건너뛴다.
func (s *Supervisor) verifyOffload(ctx context.Context, out autoconf.Output, sinceUnix int64, pid uint32) (Offload, *Error) {
	res, err := s.exec(ctx, platform.Command{
		Name:      "journalctl",
		Args:      []string{"-u", s.opts.Unit, "-o", "cat", "--since", "@" + strconv.FormatInt(sinceUnix, 10), "--no-pager"},
		Timeout:   journalTimeout,
		MaxOutput: maxJournalBytes,
	})
	if err != nil {
		return Offload{}, errorf(CodeGPUOffloadFailed, "cannot read the journal to verify offload: %v", err)
	}
	if res.ExitCode != 0 {
		return Offload{}, errorf(CodeGPUOffloadFailed, "journalctl exited %d while verifying offload: %s", res.ExitCode, excerpt(string(res.Stderr), 512))
	}
	logText := string(res.Stdout)
	off := Offload{Requested: out.NGL}

	offloaded, total, ok := llamaargs.ParseOffload(logText)
	if !ok {
		return off, errorf(CodeGPUOffloadFailed, "no \"offloaded N/M layers to GPU\" line in the journal since start (requested NGL %s); journal: %s", out.NGL, excerpt(logText, excerptBytes))
	}
	off.Offloaded, off.Total = offloaded, total
	if llamaargs.HasNoGPUWarning(logText) {
		return off, errorf(CodeGPUOffloadFailed, "llama-server reported no usable GPU (requested NGL %s, offloaded %d/%d); journal: %s", out.NGL, offloaded, total, excerpt(logText, excerptBytes))
	}
	if offloaded == 0 {
		return off, errorf(CodeGPUOffloadFailed, "llama-server offloaded 0/%d layers (requested NGL %s); journal: %s", total, out.NGL, excerpt(logText, excerptBytes))
	}

	var want int
	var wantText string
	switch out.NGL {
	case "all":
		want, wantText = total, "all layers"
	case "auto":
		if offloaded <= 0 {
			return off, errorf(CodeGPUOffloadFailed, "NGL auto offloaded no layers (0/%d)", total)
		}
		want, wantText = offloaded, "more than zero layers"
	default:
		k, convErr := strconv.Atoi(out.NGL)
		if convErr != nil {
			return off, errorf(CodeGPUOffloadFailed, "unexpected NGL value %q", out.NGL)
		}
		want = min(k, total)
		wantText = fmt.Sprintf("min(%d, %d) = %d layers", k, total, want)
	}
	if out.NGL != "auto" && offloaded != want {
		return off, errorf(CodeGPUOffloadFailed, "requested NGL %s but offloaded %d/%d (expected %s); journal: %s", out.NGL, offloaded, total, wantText, excerpt(logText, excerptBytes))
	}
	off.Verified = true

	snap := s.snapshot(ctx)
	if !snap.Available {
		off.Note = "vram check skipped: NVML is unavailable (" + snap.Reason + ")"
		return off, nil
	}
	visible := make(map[int]bool, len(out.VisibleDevices))
	for _, idx := range out.VisibleDevices {
		visible[idx] = true
	}
	known := false
	found := false
	for _, d := range snap.Devices {
		if !visible[d.Index] || !d.ProcessesKnown {
			continue
		}
		known = true
		for _, p := range d.Processes {
			if p.PID == pid {
				found = true
			}
		}
	}
	switch {
	case !known:
		off.Note = "vram check skipped: the GPU process list is not available"
	case !found:
		return off, errorf(CodeGPUOffloadFailed, "llama-server PID %d is not a compute process on its GPUs, so the model is not in VRAM (offloaded %d/%d)", pid, offloaded, total)
	default:
		off.Note = fmt.Sprintf("VRAM check: PID %d holds GPU memory", pid)
	}
	return off, nil
}
