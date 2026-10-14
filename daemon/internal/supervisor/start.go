package supervisor

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/llamaargs"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// run 은 입력을 먼저 검증하고 기존 유닛을 중지·회수한 뒤 VRAM 계획과 새 실행을 한다.
// 호출자는 수명 주기 세마포어를 잡고 있어야 한다.
func (s *Supervisor) run(ctx context.Context, job startJob) *Error {
	host := "127.0.0.1"
	// 게이트웨이를 쓰면 llama-server 는 내부 포트에서 127.0.0.1 로만 듣고 공개 포트는 데몬이 받는다.
	if job.cfg.LANExposed && !job.cfg.Gateway {
		host = "0.0.0.0"
	}
	settings := llamaargs.ServerSettings{
		ModelPath: job.model.Path,
		// /v1/models 의 id 가 Diparnux 의 모델 이름과 같아지게 한다. 클라이언트(Vedecaps 등)가 같은 이름으로 모델을 가리킨다.
		Alias:      job.model.ID,
		Host:       host,
		Port:       job.cfg.BackendPort(),
		APIKeyFile: job.apiKeyFile,
		// /metrics 는 서버 현황(처리 속도, 대기 요청)에 쓴다. 게이트웨이가 있으면 내부 포트(127.0.0.1)로만 열린다.
		Metrics: true,
	}
	if err := autoconf.ValidateOverrides(mergeOverrides(job.cfg.Overrides, job.ov)); err != nil {
		s.discardKeySnapshot(job.apiKeyFile)
		return s.failWithoutStopping(ctx, errorf(CodeInvalidSettings, "invalid model settings: %v", err))
	}
	if _, err := llamaargs.Env(validationOutput(), settings); err != nil {
		s.discardKeySnapshot(job.apiKeyFile)
		return s.failWithoutStopping(ctx, errorf(CodeInvalidSettings, "%v", err))
	}

	if err := s.stopAndReclaim(ctx); err != nil {
		s.discardKeySnapshotIfInactive(ctx, job.apiKeyFile)
		return s.recordFailedState(ctx, err)
	}
	if err := s.cleanupKeySnapshots(job.apiKeyFile); err != nil {
		s.discardKeySnapshot(job.apiKeyFile)
		return s.recordFailedState(ctx, errorf(CodeEnvFile, "remove inactive API key snapshots: %v", err))
	}
	s.mu.Lock()
	s.priorStatus = nil
	s.st.PID = 0
	s.st.UnitUnverifiable = false
	s.mu.Unlock()

	// 기존 llama-server의 메모리가 회수된 뒤에만 free VRAM을 읽어 계획한다.
	pr := s.buildPlan(ctx, job.model, job.ov, job.cfg)
	s.mu.Lock()
	s.st.Plan = &pr.Output
	s.st.Warnings = append([]string{}, pr.Output.Warnings...)
	s.mu.Unlock()

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
		Args:      []string{"start", s.opts.Unit},
		Timeout:   restartTimeout,
		MaxOutput: maxCommandOutput,
	})
	if err != nil {
		return s.fail(ctx, errorf(CodeUnitStartFailed, "systemctl start: %v", err))
	}
	if res.ExitCode != 0 {
		return s.fail(ctx, errorf(CodeUnitStartFailed, "systemctl start exited %d: %s", res.ExitCode, excerpt(string(res.Stderr), 512)))
	}
	var startedCPUPID uint32
	if pr.Output.CPUOnly {
		pid, e := s.captureCPUStartAttempt(ctx, env)
		if e != nil {
			return s.fail(ctx, e)
		}
		startedCPUPID = pid
	}

	if e := s.waitHealthy(ctx, job.cfg.BackendPort()); e != nil {
		return s.fail(ctx, e)
	}

	pid, err := s.mainPID(ctx)
	if err != nil {
		return s.fail(ctx, errorf(CodeUnitCheck, "read MainPID: %v", err))
	}
	if pid == 0 {
		return s.fail(ctx, errorf(CodeServerExited, "the unit has no main process after the health check"))
	}
	if startedCPUPID != 0 && pid != startedCPUPID {
		return s.fail(ctx, errorf(CodeUnitCheck, "the unit MainPID changed during CPU-only startup"))
	}

	var off Offload
	if pr.Output.CPUOnly {
		off = Offload{Requested: "0", Verified: false, Note: "CPU-only plan selected; health endpoint returned HTTP 200"}
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
	s.st.UnitUnverifiable = false
	s.st.Offload = off
	s.st.LastError = nil
	s.priorStatus = nil
	s.st.Since = s.now()
	if pr.Output.CPUOnly {
		s.st.State = StateRunningCPU
		s.st.Warnings = append(s.st.Warnings, cpuFallbackWarning(pr.GPU))
	} else {
		s.st.State = StateRunning
	}
	return nil
}

// validationOutput 은 llamaargs가 요청 설정을 검사할 때 쓰는 유효한 최소 계획이다.
func validationOutput() autoconf.Output {
	return autoconf.Output{
		NGL:        "0",
		Ctx:        256,
		CacheTypeK: "f16",
		CacheTypeV: "f16",
		Batch:      1,
		LoadMode:   "mmap",
		CPUOnly:    true,
	}
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
	if stopErr := s.stopAndReclaim(ctx); stopErr != nil {
		e = &Error{Code: e.Code, Message: e.Message + "; the unit could not be stopped safely: " + stopErr.Message}
	} else if err := s.cleanupKeySnapshots(""); err != nil {
		e = &Error{Code: e.Code, Message: e.Message + "; inactive API key snapshots could not be removed: " + err.Error()}
	}
	return s.recordFailedState(ctx, e)
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
