package api

import (
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/gpustatus"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
)

// statusView 는 GET /v1/status 의 응답이다.
type statusView struct {
	Version string       `json:"version"`
	Mode    string       `json:"mode"`
	GPU     gpuInfo      `json:"gpu"`
	Memory  *memInfoView `json:"memory"`
	Server  serverInfo   `json:"server"`
}

// gpuInfo 는 GPU 상태 파일과 NVML 스냅샷의 합친 값이다.
type gpuInfo struct {
	// Status 는 상태 파일을 읽었을 때만 있다.
	Status *gpuFileView `json:"status"`
	// StatusError 는 상태 파일을 읽지 못한 이유다.
	StatusError string `json:"status_error,omitempty"`
	// OpenModule 은 "yes", "no", "unknown" 이다. 상태 파일의 상태로 추정한다.
	OpenModule string `json:"open_module"`
	// SecureBoot 은 "enabled", "disabled", "unknown" 이다.
	SecureBoot string `json:"secure_boot"`
	// DriverVersion 은 NVML 값이 있으면 그 값, 없으면 상태 파일의 값이다.
	DriverVersion string   `json:"driver_version"`
	NVML          nvmlView `json:"nvml"`
}

// gpuFileView 는 상태 파일의 요약이다.
type gpuFileView struct {
	State         string    `json:"state"`
	Message       string    `json:"message"`
	Present       bool      `json:"present"`
	PCICount      int       `json:"pci_count"`
	Count         int       `json:"count"`
	UsableCount   int       `json:"usable_count"`
	UsableIndexes []int     `json:"usable_indexes"`
	SecureBoot    string    `json:"secure_boot"`
	DriverVersion string    `json:"driver_version"`
	GPUs          []gpuItem `json:"gpus"`
}

// gpuItem 은 상태 파일의 GPU 한 장이다.
type gpuItem struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	ComputeCap string `json:"compute_cap"`
	Supported  bool   `json:"supported"`
	Reason     string `json:"reason"`
}

// nvmlView 는 NVML 스냅샷의 요약이다.
type nvmlView struct {
	Available         bool         `json:"available"`
	Reason            string       `json:"reason,omitempty"`
	NVMLVersion       string       `json:"nvml_version,omitempty"`
	CUDADriverVersion string       `json:"cuda_driver_version,omitempty"`
	Devices           []deviceView `json:"devices"`
}

// deviceView 는 NVML 장치 한 장이다. 읽지 못한 값은 -1 이다.
type deviceView struct {
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Arch           string `json:"arch"`
	ComputeCap     string `json:"compute_cap"`
	MemTotalMiB    int64  `json:"memory_total_mib"`
	MemFreeMiB     int64  `json:"memory_free_mib"`
	MemUsedMiB     int64  `json:"memory_used_mib"`
	TemperatureC   int    `json:"temperature_c"`
	PowerMilliwatt int    `json:"power_mw"`
	UtilGPUPercent int    `json:"util_gpu_percent"`
	UtilMemPercent int    `json:"util_mem_percent"`
}

// memInfoView 는 메모리 상태다.
type memInfoView struct {
	TotalMiB     int64 `json:"total_mib"`
	AvailableMiB int64 `json:"available_mib"`
}

// serverInfo 는 서버 상태와 사용한 계획이다.
type serverInfo struct {
	supervisor.Status
	Plan *planView `json:"plan"`
}

// gpuCheckView 는 POST /v1/gpu/check 의 응답이다.
type gpuCheckView struct {
	ExitCode int         `json:"exit_code"`
	Status   gpuFileView `json:"status"`
}

// planView 는 autoconf.Output 의 JSON 모양이다.
type planView struct {
	NGL            string   `json:"ngl"`
	Ctx            int      `json:"ctx"`
	CacheTypeK     string   `json:"cache_type_k"`
	CacheTypeV     string   `json:"cache_type_v"`
	FlashAttn      bool     `json:"flash_attn"`
	Batch          int      `json:"batch"`
	Parallel       int      `json:"parallel"`
	SlotCtx        int      `json:"slot_ctx"`
	CacheReuse     int      `json:"cache_reuse"`
	CacheRAMMiB    int      `json:"cache_ram_mib"`
	TensorSplit    string   `json:"tensor_split,omitempty"`
	VisibleDevices []int    `json:"visible_devices"`
	LoadMode       string   `json:"load_mode"`
	Fit            bool     `json:"fit"`
	CPUOnly        bool     `json:"cpu_only"`
	EstVRAMMiB     int64    `json:"est_vram_mib"`
	EstRAMMiB      int64    `json:"est_ram_mib"`
	Rationale      []string `json:"rationale"`
	Warnings       []string `json:"warnings"`
	Advice         []string `json:"advice"`
}

// slotCtxOf 는 슬롯 하나의 컨텍스트다. 값이 없으면 전체 컨텍스트를 슬롯 수로 나눈다.
func slotCtxOf(o *autoconf.Output) int {
	if o.SlotCtx > 0 {
		return o.SlotCtx
	}
	return o.Ctx / max(o.Parallel, 1)
}

// planViewOf 는 계획을 JSON 모양으로 바꾼다. 계획이 없으면 nil 이다.
func planViewOf(o *autoconf.Output) *planView {
	if o == nil {
		return nil
	}
	return &planView{
		NGL:            o.NGL,
		Ctx:            o.Ctx,
		CacheTypeK:     o.CacheTypeK,
		CacheTypeV:     o.CacheTypeV,
		FlashAttn:      o.FlashAttn,
		Batch:          o.Batch,
		Parallel:       max(o.Parallel, 1),
		SlotCtx:        slotCtxOf(o),
		CacheReuse:     o.CacheReuse,
		CacheRAMMiB:    o.CacheRAMMiB,
		TensorSplit:    o.TensorSplit,
		VisibleDevices: nonNilInts(o.VisibleDevices),
		LoadMode:       o.LoadMode,
		Fit:            o.Fit,
		CPUOnly:        o.CPUOnly,
		EstVRAMMiB:     o.EstVRAMMiB,
		EstRAMMiB:      o.EstRAMMiB,
		Rationale:      nonNilStrings(o.Rationale),
		Warnings:       nonNilStrings(o.Warnings),
		Advice:         nonNilStrings(o.Advice),
	}
}

// planResultView 는 GET /v1/server/plan 의 응답이다.
type planResultView struct {
	ModelID string       `json:"model_id"`
	Plan    planView     `json:"plan"`
	Input   inputView    `json:"input"`
	GPU     gpuCheckInfo `json:"gpu"`
}

// inputView 는 계획 입력의 요약이다.
type inputView struct {
	ModelBytes  int64     `json:"model_bytes"`
	Layers      int       `json:"layers"`
	CtxTrain    int       `json:"ctx_train"`
	Embedding   int       `json:"embedding"`
	GPUs        []gpuFree `json:"gpus"`
	RAMAvailMiB int64     `json:"ram_avail_mib"`
	RAMTotalMiB int64     `json:"ram_total_mib"`
	DesktopMode bool      `json:"desktop_mode"`
}

// gpuFree 는 계획에 쓴 GPU 한 장의 여유 메모리다.
type gpuFree struct {
	Index    int   `json:"index"`
	FreeMiB  int64 `json:"free_mib"`
	TotalMiB int64 `json:"total_mib"`
}

// gpuCheckInfo 는 계획에 쓴 GPU 상태 요약이다.
type gpuCheckInfo struct {
	StatusChecked bool   `json:"status_checked"`
	State         string `json:"state"`
	Message       string `json:"message"`
	UsableIndexes []int  `json:"usable_indexes"`
	NVMLAvailable bool   `json:"nvml_available"`
	NVMLReason    string `json:"nvml_reason,omitempty"`
}

// planResultOf 는 supervisor 의 계획 결과를 JSON 모양으로 바꾼다.
func planResultOf(pr supervisor.PlanResult) planResultView {
	gpus := make([]gpuFree, 0, len(pr.Input.GPUs))
	for _, g := range pr.Input.GPUs {
		gpus = append(gpus, gpuFree{Index: g.Index, FreeMiB: g.FreeMiB, TotalMiB: g.TotalMiB})
	}
	return planResultView{
		ModelID: pr.ModelID,
		Plan:    *planViewOf(&pr.Output),
		Input: inputView{
			ModelBytes:  pr.Input.ModelBytes,
			Layers:      pr.Input.Layers,
			CtxTrain:    pr.Input.CtxTrain,
			Embedding:   pr.Input.Embedding,
			GPUs:        gpus,
			RAMAvailMiB: pr.Input.RAMAvailMiB,
			RAMTotalMiB: pr.Input.RAMTotalMiB,
			DesktopMode: pr.Input.DesktopMode,
		},
		GPU: gpuCheckInfo{
			StatusChecked: pr.GPU.StatusChecked,
			State:         pr.GPU.State,
			Message:       pr.GPU.Message,
			UsableIndexes: nonNilInts(pr.GPU.UsableIndexes),
			NVMLAvailable: pr.GPU.NVMLAvailable,
			NVMLReason:    pr.GPU.NVMLReason,
		},
	}
}

// statusFileView 는 상태 파일을 JSON 모양으로 바꾼다.
func statusFileView(st gpustatus.Status) gpuFileView {
	gpus := make([]gpuItem, 0, len(st.GPUs))
	for _, g := range st.GPUs {
		gpus = append(gpus, gpuItem{Index: g.Index, Name: g.Name, ComputeCap: g.ComputeCap, Supported: g.Supported, Reason: g.Reason})
	}
	return gpuFileView{
		State:         string(st.State),
		Message:       st.Message,
		Present:       st.Present,
		PCICount:      st.PCICount,
		Count:         st.Count,
		UsableCount:   st.UsableCount,
		UsableIndexes: nonNilInts(st.UsableIndexes),
		SecureBoot:    st.SecureBoot,
		DriverVersion: st.DriverVersion,
		GPUs:          gpus,
	}
}

// nonNilInts 와 nonNilStrings 는 nil 을 빈 배열로 바꿔 JSON 에서 null 이 나오지 않게 한다.
func nonNilInts(v []int) []int {
	if v == nil {
		return []int{}
	}
	return v
}

func nonNilStrings(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// jobView 는 내려받기 작업의 JSON 모양이다.
type jobView struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	Repo       string    `json:"repo"`
	File       string    `json:"file"`
	Revision   string    `json:"revision"`
	BytesDone  int64     `json:"bytes_done"`
	BytesTotal int64     `json:"bytes_total"`
	ModelID    string    `json:"model_id,omitempty"`
	License    string    `json:"license,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	Started    time.Time `json:"started"`
	Finished   time.Time `json:"finished"`
}
