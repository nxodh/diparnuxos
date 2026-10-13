package supervisor

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/gpustatus"
	"github.com/nxodh/diparnuxos/daemon/internal/models"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// PlanResult는 계획 한 번의 결과다. Output 의 Warnings 와 Rationale 에 계산 근거와 주의 사항이 들어 있다.
type PlanResult struct {
	// ModelID 는 계획한 모델이다.
	ModelID string
	// Output 은 autoconf 의 계산 결과다.
	Output autoconf.Output
	// Input 은 계산에 쓴 입력 요약이다.
	Input InputSummary
	// GPU 는 GPU 진단 상태 파일과 NVML 조회의 요약이다.
	GPU GPUSummary
}

// InputSummary는 autoconf 에 넘긴 입력의 요약이다.
type InputSummary struct {
	ModelBytes  int64
	Layers      int
	CtxTrain    int
	Embedding   int
	GPUs        []autoconf.GPU
	RAMAvailMiB int64
	RAMTotalMiB int64
	DesktopMode bool
}

// GPUSummary는 GPU 진단 상태 파일과 NVML 조회 결과의 요약이다.
type GPUSummary struct {
	// StatusChecked 는 상태 파일을 읽었는지 나타낸다.
	StatusChecked bool
	// State 와 Message 는 상태 파일의 값이다.
	State   string
	Message string
	// UsableIndexes 는 상태 파일이 쓸 수 있다고 한 GPU index 다.
	UsableIndexes []int
	// NVMLAvailable 과 NVMLReason 은 NVML 조회 결과다.
	NVMLAvailable bool
	NVMLReason    string
}

// Plan은 모델과 현재 상태로 llama-server 설정을 계산한다. 설정 파일의 재정의에 요청의 재정의를 덮어쓴다.
// 잘못된 재정의는 invalid_overrides 오류다. 나머지 상태 문제는 계획의 경고가 된다.
func (s *Supervisor) Plan(ctx context.Context, modelID string, ov autoconf.Overrides) (PlanResult, error) {
	if err := autoconf.ValidateOverrides(ov); err != nil {
		return PlanResult{}, errorf(CodeInvalidOverrides, "%v", err)
	}
	m, err := s.findModel(modelID)
	if err != nil {
		return PlanResult{}, err
	}
	return s.buildPlan(ctx, m, ov, s.opts.Config()), nil
}

// buildPlan은 모델, 설정, GPU 상태, NVML, 메모리, 모드를 모아 autoconf.Plan 을 부른다.
// 재정의는 이미 검사되었다고 가정한다. 상태를 읽지 못한 경우는 오류가 아니라 경고와 CPU 계획이다.
func (s *Supervisor) buildPlan(ctx context.Context, m models.Model, ov autoconf.Overrides, cfg config.Config) PlanResult {
	var warnings []string
	in := autoconf.Input{
		ModelBytes: m.Size,
		Layers:     clampInt(m.BlockCount),
		CtxTrain:   clampInt(m.ContextLength),
		Embedding:  clampInt(m.EmbeddingLength),
	}
	var gs GPUSummary

	var usable []int
	st, err := gpustatus.Load(s.opts.GPUStatusPath)
	switch {
	case errors.Is(err, os.ErrNotExist):
		warnings = append(warnings, "GPU check has not run; planning without a GPU. Run the GPU check to enable offload.")
	case err != nil:
		warnings = append(warnings, fmt.Sprintf("GPU status file cannot be read, planning without a GPU: %v", err))
	default:
		gs.StatusChecked = true
		gs.State = string(st.State)
		gs.Message = st.Message
		if st.Usable() {
			usable = st.UsableIndexes
			gs.UsableIndexes = append([]int{}, st.UsableIndexes...)
		} else {
			warnings = append(warnings, fmt.Sprintf("GPU status is %s: %s. Planning on CPU.", st.State, st.Message))
		}
	}

	if len(usable) > 0 {
		snap := s.snapshot(ctx)
		gs.NVMLAvailable = snap.Available
		gs.NVMLReason = snap.Reason
		if snap.Available {
			byIndex := make(map[int]platform.GPUDevice, len(snap.Devices))
			for _, d := range snap.Devices {
				byIndex[d.Index] = d
			}
			for _, idx := range usable {
				d, ok := byIndex[idx]
				if !ok {
					warnings = append(warnings, fmt.Sprintf("usable GPU %d is not reported by NVML; it is not used", idx))
					continue
				}
				in.GPUs = append(in.GPUs, autoconf.GPU{Index: idx, FreeMiB: d.MemFreeMiB, TotalMiB: d.MemTotalMiB})
			}
		} else {
			// VRAM 을 모르므로 자체 맞춤(NGL auto)으로 계획한다. 블록 수를 0으로 두면 autoconf 가 그 경로를 쓴다.
			warnings = append(warnings, fmt.Sprintf("NVML is unavailable (%s); VRAM is unknown, so llama-server chooses the layers itself (NGL auto)", snap.Reason))
			for _, idx := range usable {
				in.GPUs = append(in.GPUs, autoconf.GPU{Index: idx})
			}
			in.Layers = 0
		}
	}

	summary := InputSummary{
		ModelBytes: in.ModelBytes,
		Layers:     in.Layers,
		CtxTrain:   in.CtxTrain,
		Embedding:  in.Embedding,
		GPUs:       append([]autoconf.GPU{}, in.GPUs...),
	}

	if s.opts.MemInfo == nil {
		warnings = append(warnings, "memory information is not configured; RAM check skipped")
		in.RAMAvailMiB = unknownRAMMiB
	} else if mem, merr := s.opts.MemInfo(); merr != nil {
		warnings = append(warnings, fmt.Sprintf("memory information is unavailable (%v); RAM check skipped", merr))
		in.RAMAvailMiB = unknownRAMMiB
	} else {
		in.RAMAvailMiB = mem.AvailableMiB
		summary.RAMTotalMiB = mem.TotalMiB
	}
	summary.RAMAvailMiB = in.RAMAvailMiB

	if mode, merr := s.Mode(ctx); merr != nil {
		warnings = append(warnings, fmt.Sprintf("cannot read the boot mode; desktop advice skipped: %v", merr))
	} else {
		in.DesktopMode = mode == "desktop"
	}
	summary.DesktopMode = in.DesktopMode

	in.Overrides = mergeOverrides(cfg.Overrides, ov)
	out := autoconf.Plan(in)
	out.Warnings = append(warnings, out.Warnings...)
	return PlanResult{ModelID: m.ID, Output: out, Input: summary, GPU: gs}
}

// snapshot은 NVML 스냅샷을 읽는다. GPU 프로버가 없으면 쓸 수 없는 것으로 본다.
func (s *Supervisor) snapshot(ctx context.Context) platform.GPUSnapshot {
	if s.opts.GPU == nil {
		return platform.GPUSnapshot{Available: false, Reason: "no GPU prober is configured"}
	}
	return s.opts.GPU.Snapshot(ctx)
}

// mergeOverrides는 설정 파일의 재정의에 요청의 재정의를 덮어쓴다. 포인터는 복사해서 공유하지 않는다.
func mergeOverrides(cfg config.Overrides, req autoconf.Overrides) autoconf.Overrides {
	out := autoconf.Overrides{
		NGL:       copyStrPtr(cfg.NGL),
		Ctx:       copyIntPtr(cfg.Ctx),
		CacheType: copyStrPtr(cfg.CacheType),
		Batch:     copyIntPtr(cfg.Batch),
		LoadMode:  copyStrPtr(cfg.LoadMode),
		Parallel:  copyIntPtr(cfg.Parallel),
	}
	if req.NGL != nil {
		out.NGL = copyStrPtr(req.NGL)
	}
	if req.Ctx != nil {
		out.Ctx = copyIntPtr(req.Ctx)
	}
	if req.CacheType != nil {
		out.CacheType = copyStrPtr(req.CacheType)
	}
	if req.Batch != nil {
		out.Batch = copyIntPtr(req.Batch)
	}
	if req.LoadMode != nil {
		out.LoadMode = copyStrPtr(req.LoadMode)
	}
	if req.Parallel != nil {
		out.Parallel = copyIntPtr(req.Parallel)
	}
	return out
}

func copyStrPtr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyIntPtr(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

// clampInt는 uint64 를 int 로 바꾼다. 범위를 넘으면 int 의 최댓값으로 자른다(autoconf 가 다시 자른다).
func clampInt(v uint64) int {
	if v > math.MaxInt32 {
		return math.MaxInt32
	}
	return int(v)
}
