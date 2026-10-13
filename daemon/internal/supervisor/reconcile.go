package supervisor

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/llamaargs"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

var runtimeEnvKeys = []string{
	"LLAMA_ARG_MODEL",
	"LLAMA_ARG_HOST",
	"LLAMA_ARG_PORT",
	"LLAMA_ARG_N_GPU_LAYERS",
	"LLAMA_ARG_CTX_SIZE",
	"LLAMA_ARG_BATCH",
	"LLAMA_ARG_CACHE_TYPE_K",
	"LLAMA_ARG_CACHE_TYPE_V",
	"LLAMA_ARG_FLASH_ATTN",
	"LLAMA_ARG_LOAD_MODE",
	"LLAMA_ARG_FIT",
	"CUDA_DEVICE_ORDER",
	"CUDA_VISIBLE_DEVICES",
}

// reconcileActive 는 기존 유닛을 검사하고 안전하게 확인된 서버만 관리 상태에 채택한다.
// 반환 bool 은 유닛이 이미 실행 중이거나 상태를 확인하지 못해 자동 시작을 건너뛰어야 함을 뜻한다.
func (s *Supervisor) reconcileActive(ctx context.Context) (bool, error) {
	state, stateErr := s.unitState(ctx)
	if stateErr != nil {
		err := errorf(CodeUnitCheck, "cannot reconcile the llama unit state: %v", stateErr)
		return true, s.recordFailedState(ctx, err)
	}
	pid, pidErr := s.mainPID(ctx)
	if pidErr != nil {
		err := errorf(CodeUnitCheck, "cannot reconcile the llama unit MainPID: %v", pidErr)
		return true, s.recordFailedState(ctx, err)
	}
	if (state == "inactive" || state == "failed") && pid == 0 {
		s.mu.Lock()
		s.st = Status{State: StateStopped, Warnings: []string{}}
		s.mu.Unlock()
		return false, nil
	}
	if state == "active" && pid > 0 {
		st, err := s.adoptActive(ctx, pid)
		if err == nil {
			s.mu.Lock()
			s.st = st
			s.priorStatus = nil
			s.mu.Unlock()
			s.logf("adopted active llama-server PID %d", pid)
			return true, nil
		}
		adoptErr := errorf(CodeUnitCheck, "active llama-server could not be safely adopted: %v", err)
		stopErr := s.stopAndReclaim(ctx)
		if stopErr == nil {
			_ = s.cleanupKeySnapshots("")
			adoptErr.Message += "; the unverifiable server was stopped"
		} else {
			adoptErr.Message += "; stop or process release could not be verified: " + stopErr.Message
		}
		return true, s.recordFailedState(ctx, adoptErr)
	}

	// Transitional states and a nonzero PID outside active are not safe to overwrite.
	err := errorf(CodeUnitCheck, "llama unit has an unreconciled state %q with MainPID %d", state, pid)
	stopErr := s.stopAndReclaim(ctx)
	if stopErr == nil {
		_ = s.cleanupKeySnapshots("")
		err.Message += "; the unverifiable unit was stopped"
	} else {
		err.Message += "; stop or process release could not be verified: " + stopErr.Message
	}
	return true, s.recordFailedState(ctx, err)
}

// adoptActive 는 MainPID와 실제 실행 환경을 맞춰 보고 기존 서버를 Status로 복원한다.
func (s *Supervisor) adoptActive(ctx context.Context, pid uint32) (Status, error) {
	exe, rawEnv, err := s.processEvidence(pid)
	if err != nil {
		return Status{}, fmt.Errorf("cannot inspect unit MainPID: %w", err)
	}
	if strings.TrimSuffix(filepath.Clean(exe), " (deleted)") != filepath.Clean(s.opts.ServerBinary) {
		return Status{}, errors.New("unit MainPID is not the configured llama-server executable")
	}
	env, err := parseRuntimeEnvironment(rawEnv)
	if err != nil {
		return Status{}, err
	}
	for _, key := range runtimeEnvKeys {
		if _, ok := env[key]; !ok {
			return Status{}, fmt.Errorf("runtime environment is missing %s", key)
		}
	}
	envFile, err := os.ReadFile(s.opts.EnvFilePath)
	if err != nil {
		return Status{}, fmt.Errorf("cannot read current runtime environment file: %w", err)
	}
	if !bytes.Equal(envFile, []byte(llamaargs.EnvFile(env))) {
		return Status{}, errors.New("runtime environment does not match the current environment file")
	}

	modelPath := env["LLAMA_ARG_MODEL"]
	modelID := ""
	models, err := s.opts.Store.List()
	if err != nil {
		return Status{}, fmt.Errorf("cannot list installed models: %w", err)
	}
	for _, model := range models {
		if filepath.Clean(model.Path) == filepath.Clean(modelPath) {
			modelID = model.ID
			break
		}
	}
	if modelID == "" {
		return Status{}, errors.New("runtime model path is not an installed model")
	}

	port, err := strconv.Atoi(env["LLAMA_ARG_PORT"])
	if err != nil || port < 1 || port > 65535 {
		return Status{}, errors.New("runtime API port is invalid")
	}
	output, settings, err := runtimePlan(env)
	if err != nil {
		return Status{}, err
	}
	canonicalEnv, err := llamaargs.Env(output, settings)
	if err != nil {
		return Status{}, fmt.Errorf("runtime environment is invalid: %w", err)
	}
	if !sameEnvironment(canonicalEnv, env) {
		return Status{}, errors.New("runtime environment contains unsupported or inconsistent settings")
	}
	if settings.ModelPath != modelPath {
		return Status{}, errors.New("runtime model path changed during reconciliation")
	}
	lan := !loopbackHost(settings.Host)
	if settings.APIKeyFile == "" {
		if lan {
			return Status{}, errors.New("LAN server has no API key file")
		}
	} else {
		if !s.allowedCredentialPath(settings.APIKeyFile) {
			return Status{}, errors.New("runtime API key file is outside the managed key paths")
		}
		keys, err := s.readRuntimeCredentialFile(settings.APIKeyFile)
		if err != nil {
			return Status{}, fmt.Errorf("runtime API key file cannot be verified: %w", err)
		}
		if lan && len(keys) == 0 {
			return Status{}, errors.New("LAN server has an empty API key file")
		}
	}
	if !s.healthOK(ctx, port) {
		return Status{}, errors.New("runtime health endpoint did not return HTTP 200")
	}

	var off Offload
	warnings := append([]string{}, output.Warnings...)
	if output.CPUOnly {
		cpuReason := "adopted CPU-only runtime (N_GPU_LAYERS=0); health endpoint returned HTTP 200"
		output.Warnings = append(warnings, cpuReason)
		warnings = append(warnings, cpuReason)
		off = Offload{Requested: "0", Verified: false, Note: "CPU-only runtime environment and health endpoint verified during adoption"}
	} else {
		off, err = s.verifyAdoptedOffload(ctx, output, pid)
		if err != nil {
			return Status{}, err
		}
	}
	if state, err := s.unitState(ctx); err != nil || state != "active" {
		return Status{}, errors.New("unit state changed during runtime adoption")
	}
	if currentPID, err := s.mainPID(ctx); err != nil || currentPID != pid {
		return Status{}, errors.New("unit MainPID changed during runtime adoption")
	}
	if !s.processMatchesRuntime(pid, env) {
		return Status{}, errors.New("unit MainPID runtime changed during adoption")
	}
	status := Status{
		State:       map[bool]State{true: StateRunningCPU, false: StateRunning}[output.CPUOnly],
		ModelID:     modelID,
		Host:        settings.Host,
		Port:        port,
		BackendPort: port,
		LANExposed:  lan,
		PID:         int(pid),
		Since:       s.now(),
		Plan:        &output,
		Offload:     off,
		Warnings:    warnings,
	}
	// 게이트웨이 모드로 시작된 서버는 내부 포트에서 loopback 으로만 듣는다. 사용자에게 보이는 포트와 공개 여부는 설정을 따른다.
	if cfg := s.opts.Config(); cfg.Gateway && port == cfg.InternalPort && loopbackHost(settings.Host) {
		status.Port = cfg.Port
		status.LANExposed = cfg.LANExposed
		if cfg.LANExposed {
			status.Host = "0.0.0.0"
		}
	}
	if output.CPUOnly {
		s.mu.Lock()
		s.cpuRuntime = &cpuRuntimeEvidence{pid: pid, env: cloneRuntimeEnvironment(env)}
		s.mu.Unlock()
	}
	return status, nil
}

func sameEnvironment(expected, actual map[string]string) bool {
	if len(expected) != len(actual) {
		return false
	}
	for key, value := range expected {
		if actual[key] != value {
			return false
		}
	}
	return true
}

func (s *Supervisor) processEvidence(pid uint32) (string, []byte, error) {
	base := filepath.Join(s.opts.ProcRoot, strconv.FormatUint(uint64(pid), 10))
	exe, err := os.Readlink(filepath.Join(base, "exe"))
	if err != nil {
		return "", nil, err
	}
	env, err := os.ReadFile(filepath.Join(base, "environ"))
	if err != nil {
		return "", nil, err
	}
	return exe, env, nil
}

func parseRuntimeEnvironment(raw []byte) (map[string]string, error) {
	env := make(map[string]string)
	for _, entry := range bytes.Split(raw, []byte{0}) {
		if len(entry) == 0 {
			continue
		}
		name, value, ok := bytes.Cut(entry, []byte{'='})
		if !ok {
			continue
		}
		key := string(name)
		if !strings.HasPrefix(key, "LLAMA_ARG_") && key != "CUDA_VISIBLE_DEVICES" && key != "CUDA_DEVICE_ORDER" {
			continue
		}
		if _, exists := env[key]; exists {
			return nil, errors.New("runtime environment contains duplicate llama settings")
		}
		env[key] = string(value)
	}
	return env, nil
}

func runtimePlan(env map[string]string) (autoconf.Output, llamaargs.ServerSettings, error) {
	parseInt := func(key string) (int, error) {
		v, err := strconv.Atoi(env[key])
		if err != nil || v <= 0 {
			return 0, fmt.Errorf("runtime setting %s is invalid", key)
		}
		return v, nil
	}
	ctx, err := parseInt("LLAMA_ARG_CTX_SIZE")
	if err != nil {
		return autoconf.Output{}, llamaargs.ServerSettings{}, err
	}
	batch, err := parseInt("LLAMA_ARG_BATCH")
	if err != nil {
		return autoconf.Output{}, llamaargs.ServerSettings{}, err
	}
	ngl, ok := autoconf.ParseNGL(env["LLAMA_ARG_N_GPU_LAYERS"])
	if !ok {
		return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime GPU layer setting is invalid")
	}
	flash, err := parseOnOff(env["LLAMA_ARG_FLASH_ATTN"])
	if err != nil {
		return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime flash-attention setting is invalid")
	}
	fit, err := parseOnOff(env["LLAMA_ARG_FIT"])
	if err != nil {
		return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime fit setting is invalid")
	}
	visible := []int{}
	visibleText := env["CUDA_VISIBLE_DEVICES"]
	if visibleText != "" {
		for _, part := range strings.Split(visibleText, ",") {
			idx, err := strconv.Atoi(part)
			if err != nil || idx < 0 {
				return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime visible GPU list is invalid")
			}
			visible = append(visible, idx)
		}
	}
	if env["CUDA_DEVICE_ORDER"] != "PCI_BUS_ID" {
		return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime CUDA device order is invalid")
	}
	metrics := false
	if value, ok := env["LLAMA_ARG_ENDPOINT_METRICS"]; ok {
		if value != "1" {
			return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime metrics setting is invalid")
		}
		metrics = true
	}
	parallel := 1
	if value, ok := env["LLAMA_ARG_N_PARALLEL"]; ok {
		p, err := strconv.Atoi(value)
		if err != nil || p < 2 || p > 16 || ctx%p != 0 {
			return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime parallel slot setting is invalid")
		}
		parallel = p
	}
	out := autoconf.Output{
		Parallel:       parallel,
		SlotCtx:        ctx / parallel,
		NGL:            ngl,
		Ctx:            ctx,
		CacheTypeK:     env["LLAMA_ARG_CACHE_TYPE_K"],
		CacheTypeV:     env["LLAMA_ARG_CACHE_TYPE_V"],
		FlashAttn:      flash,
		Batch:          batch,
		TensorSplit:    env["LLAMA_ARG_TENSOR_SPLIT"],
		VisibleDevices: visible,
		LoadMode:       env["LLAMA_ARG_LOAD_MODE"],
		Fit:            fit,
		CPUOnly:        ngl == "0",
	}
	settings := llamaargs.ServerSettings{
		ModelPath:  env["LLAMA_ARG_MODEL"],
		Alias:      env["LLAMA_ARG_ALIAS"],
		Host:       env["LLAMA_ARG_HOST"],
		APIKeyFile: env["LLAMA_ARG_API_KEY_FILE"],
		Metrics:    metrics,
	}
	port, err := strconv.Atoi(env["LLAMA_ARG_PORT"])
	if err != nil {
		return autoconf.Output{}, llamaargs.ServerSettings{}, errors.New("runtime API port is invalid")
	}
	settings.Port = port
	return out, settings, nil
}

func parseOnOff(value string) (bool, error) {
	switch value {
	case "on":
		return true, nil
	case "off":
		return false, nil
	default:
		return false, errors.New("value must be on or off")
	}
}

func loopbackHost(host string) bool {
	for _, value := range strings.Split(host, ",") {
		switch strings.TrimSpace(value) {
		case "127.0.0.1", "::1", "localhost":
		default:
			return false
		}
	}
	return true
}

func (s *Supervisor) allowedCredentialPath(path string) bool {
	if s.opts.KeySnapshotDir == "" || filepath.Dir(filepath.Clean(path)) != filepath.Clean(s.opts.KeySnapshotDir) {
		return false
	}
	base := filepath.Base(path)
	return strings.HasPrefix(base, keySnapshotPrefix) && strings.HasSuffix(base, ".key")
}

func (s *Supervisor) verifyAdoptedOffload(ctx context.Context, out autoconf.Output, pid uint32) (Offload, error) {
	res, err := s.exec(ctx, platform.Command{
		Name:      "journalctl",
		Args:      []string{"-u", s.opts.Unit, "_PID=" + strconv.FormatUint(uint64(pid), 10), "-o", "cat", "--no-pager"},
		Timeout:   journalTimeout,
		MaxOutput: maxJournalBytes,
	})
	if err != nil || res.ExitCode != 0 {
		return Offload{}, errors.New("cannot verify GPU offload journal for the unit MainPID")
	}
	off := Offload{Requested: out.NGL}
	offloaded, total, ok := llamaargs.ParseOffload(string(res.Stdout))
	if !ok || offloaded <= 0 || llamaargs.HasNoGPUWarning(string(res.Stdout)) {
		return off, errors.New("GPU offload is not verifiable for the active MainPID")
	}
	want := total
	switch out.NGL {
	case "all":
	case "auto":
		want = offloaded
	default:
		layers, err := strconv.Atoi(out.NGL)
		if err != nil {
			return Offload{}, errors.New("runtime GPU layer setting is invalid")
		}
		want = min(layers, total)
	}
	if out.NGL != "auto" && offloaded != want {
		return off, errors.New("active MainPID GPU offload does not match its runtime settings")
	}
	off.Offloaded, off.Total, off.Verified = offloaded, total, true
	snap := s.snapshot(ctx)
	if !snap.Available {
		off.Note = "vram check skipped: NVML is unavailable (" + snap.Reason + ")"
		return off, nil
	}
	visible := make(map[int]bool, len(out.VisibleDevices))
	for _, idx := range out.VisibleDevices {
		visible[idx] = true
	}
	known, found := false, false
	for _, device := range snap.Devices {
		if !visible[device.Index] || !device.ProcessesKnown {
			continue
		}
		known = true
		for _, process := range device.Processes {
			if process.PID == pid {
				found = true
			}
		}
	}
	if !known {
		off.Note = "vram check skipped: the GPU process list is not available"
		return off, nil
	}
	if !found {
		return off, errors.New("active MainPID is not listed as a GPU process")
	}
	off.Note = fmt.Sprintf("VRAM check: PID %d holds GPU memory", pid)
	return off, nil
}

func (s *Supervisor) captureCPUStartAttempt(ctx context.Context, expectedEnv map[string]string) (uint32, *Error) {
	state, err := s.unitState(ctx)
	if err != nil {
		return 0, errorf(CodeUnitCheck, "read unit state after CPU-only start: %v", err)
	}
	if state != "active" {
		return 0, errorf(CodeServerExited, "the unit became %s before CPU-only health verification", state)
	}
	pid, err := s.mainPID(ctx)
	if err != nil {
		return 0, errorf(CodeUnitCheck, "read CPU-only startup MainPID: %v", err)
	}
	if pid == 0 {
		return 0, errorf(CodeServerExited, "the unit has no MainPID before CPU-only health verification")
	}
	if !s.processMatchesRuntime(pid, expectedEnv) {
		return 0, errorf(CodeUnitCheck, "the unit MainPID runtime does not match the CPU-only startup environment")
	}
	s.mu.Lock()
	s.cpuRuntime = &cpuRuntimeEvidence{pid: pid, env: cloneRuntimeEnvironment(expectedEnv)}
	s.mu.Unlock()
	return pid, nil
}

func cloneRuntimeEnvironment(env map[string]string) map[string]string {
	clone := make(map[string]string, len(env))
	for key, value := range env {
		clone[key] = value
	}
	return clone
}

func (s *Supervisor) processMatchesRuntime(pid uint32, expectedEnv map[string]string) bool {
	exe, rawEnv, err := s.processEvidence(pid)
	if err != nil || strings.TrimSuffix(filepath.Clean(exe), " (deleted)") != filepath.Clean(s.opts.ServerBinary) {
		return false
	}
	actualEnv, err := parseRuntimeEnvironment(rawEnv)
	return err == nil && sameEnvironment(expectedEnv, actualEnv)
}

func (s *Supervisor) clearCPURuntime(pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cpuRuntime != nil && (pid == 0 || s.cpuRuntime.pid == pid) {
		s.cpuRuntime = nil
	}
}

func (s *Supervisor) stopAndReclaim(ctx context.Context) *Error {
	state, err := s.unitState(ctx)
	if err != nil {
		return errorf(CodeUnitCheck, "read unit state before restart: %v", err)
	}
	pid, err := s.mainPID(ctx)
	if err != nil {
		return errorf(CodeUnitCheck, "read unit MainPID before restart: %v", err)
	}
	if pid > 0 {
		if s.verifiedCPUOnly(pid) {
			s.clearReclaimPIDs([]uint32{pid})
		} else {
			s.rememberReclaimPID(pid)
		}
	}
	if pid == 0 && (state == "inactive" || state == "failed") {
		s.clearCPURuntime(0)
	}
	if state != "inactive" || pid != 0 {
		if e := s.stopUnit(context.WithoutCancel(ctx)); e != nil {
			return e
		}
	}
	waitCtx, cancel := context.WithTimeout(ctx, s.opts.ReclaimTimeout)
	defer cancel()
	deadline := s.now().Add(s.opts.ReclaimTimeout)
	if state != "inactive" || pid != 0 {
		for {
			state, err := s.unitState(waitCtx)
			if err != nil {
				return errorf(CodeUnitCheck, "wait for stopped unit: %v", err)
			}
			currentPID, err := s.mainPID(waitCtx)
			if err != nil {
				return errorf(CodeUnitCheck, "wait for stopped unit MainPID: %v", err)
			}
			if currentPID == 0 && (state == "inactive" || state == "failed") {
				s.clearCPURuntime(pid)
				break
			}
			if !s.now().Before(deadline) {
				return errorf(CodeUnitStopFailed, "unit did not release its MainPID within %s", s.opts.ReclaimTimeout)
			}
			if err := s.opts.Sleep(waitCtx, s.opts.ReclaimPollInterval); err != nil {
				return errorf(CodeCanceled, "waiting for the unit to stop was canceled: %v", err)
			}
		}
	}
	pending := s.reclaimPIDs()
	if len(pending) == 0 {
		return nil
	}
	if s.opts.GPU == nil {
		return errorf(CodeUnitStopFailed, "cannot verify release of previous GPU process PID %d because NVML is unavailable", pending[0])
	}
	for {
		snap := s.snapshot(waitCtx)
		if !snap.Available {
			return errorf(CodeUnitStopFailed, "cannot verify release of previous GPU process because NVML is unavailable: %s", snap.Reason)
		}
		allKnown, stillPresent := len(snap.Devices) > 0, make(map[uint32]bool, len(pending))
		for _, device := range snap.Devices {
			if !device.ProcessesKnown {
				allKnown = false
				continue
			}
			for _, process := range device.Processes {
				if containsPID(pending, process.PID) {
					stillPresent[process.PID] = true
				}
			}
		}
		if allKnown && len(stillPresent) == 0 {
			s.clearReclaimPIDs(pending)
			return nil
		}
		if !s.now().Before(deadline) {
			return errorf(CodeUnitStopFailed, "GPU process %d did not release its memory within %s", pending[0], s.opts.ReclaimTimeout)
		}
		if err := s.opts.Sleep(waitCtx, s.opts.ReclaimPollInterval); err != nil {
			return errorf(CodeCanceled, "waiting for GPU process %d to release memory was canceled: %v", pending[0], err)
		}
	}
}

func (s *Supervisor) verifiedCPUOnly(pid uint32) bool {
	s.mu.Lock()
	var evidence *cpuRuntimeEvidence
	if s.cpuRuntime != nil {
		evidence = &cpuRuntimeEvidence{pid: s.cpuRuntime.pid, env: cloneRuntimeEnvironment(s.cpuRuntime.env)}
	}
	s.mu.Unlock()
	return evidence != nil && evidence.pid == pid && s.processMatchesRuntime(pid, evidence.env)
}

func containsPID(pids []uint32, pid uint32) bool {
	for _, candidate := range pids {
		if candidate == pid {
			return true
		}
	}
	return false
}

func (s *Supervisor) rememberReclaimPID(pid uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !containsPID(s.pendingReclaimPIDs, pid) {
		s.pendingReclaimPIDs = append(s.pendingReclaimPIDs, pid)
	}
}

func (s *Supervisor) reclaimPIDs() []uint32 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]uint32(nil), s.pendingReclaimPIDs...)
}

func (s *Supervisor) clearReclaimPIDs(pids []uint32) {
	s.mu.Lock()
	defer s.mu.Unlock()
	remaining := s.pendingReclaimPIDs[:0]
	for _, candidate := range s.pendingReclaimPIDs {
		if !containsPID(pids, candidate) {
			remaining = append(remaining, candidate)
		}
	}
	s.pendingReclaimPIDs = remaining
}

func (s *Supervisor) failWithoutStopping(ctx context.Context, err *Error) *Error {
	return s.recordFailedState(ctx, err)
}

func (s *Supervisor) recordFailedState(ctx context.Context, err *Error) *Error {
	stateCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shortTimeout)
	defer cancel()
	state, stateErr := s.unitState(stateCtx)
	pid, pidErr := s.mainPID(stateCtx)
	if stateErr != nil {
		err.Message += "; unit state could not be read"
	} else {
		err.Message += fmt.Sprintf("; unit state is %q", state)
	}
	if pidErr != nil {
		err.Message += "; unit MainPID could not be read"
	}

	verifiedStopped := stateErr == nil && pidErr == nil && pid == 0 && (state == "inactive" || state == "failed")
	s.mu.Lock()
	s.st.State = StateFailed
	s.st.LastError = err
	if verifiedStopped {
		s.st.PID = 0
		s.st.UnitUnverifiable = false
		s.priorStatus = nil
		s.cpuRuntime = nil
	} else {
		s.st.UnitUnverifiable = true
		if pidErr == nil && pid > 0 {
			s.st.PID = int(pid)
		}
		if s.priorStatus != nil {
			prior := s.priorStatus
			if pidErr != nil || pid == 0 || prior.PID == int(pid) {
				s.st.ModelID = prior.ModelID
				s.st.Host = prior.Host
				s.st.Port = prior.Port
				s.st.BackendPort = prior.BackendPort
				s.st.LANExposed = prior.LANExposed
				s.st.Plan = prior.Plan
				s.st.Offload = prior.Offload
				s.st.Since = prior.Since
			} else if pidErr == nil && pid > 0 {
				// 새 MainPID 는 이전 서버 정보와 다르므로 새 시작 요청을 유지한다.
				s.priorStatus = nil
			}
		}
		if s.st.PID > 0 {
			s.st.Warnings = []string{"the llama unit could not be safely verified; MainPID is reported above"}
		} else {
			s.st.Warnings = []string{"the unit state or MainPID is unverifiable; it was not reported as stopped"}
		}
	}
	s.mu.Unlock()
	s.logf("server reconciliation failed: %s", err)
	return err
}
