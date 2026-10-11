// Package autoconf는 모델 크기와 GPU의 현재 여유 메모리로 llama-server 설정을 계산한다.
//
// Plan은 순수 함수다. 파일을 읽지 않고, 명령을 실행하지 않고, 전역 상태를 두지 않는다.
// 같은 Input에는 항상 같은 Output이 나온다. 계산 근거는 Rationale에, 확인이 필요한
// 상황은 Warnings에 영어 문장으로 남긴다.
//
// KV 캐시 크기는 상한 추정이다. 레이어마다 n_embd 전체 폭을 쓴다고 보고, 부분 오프로드에서도
// KV 전체가 VRAM에 올라간다고 본다. GQA 모델은 실제 KV가 더 작으므로 추정이 실제보다 크다.
package autoconf

import (
	"errors"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
)

// MiB는 1 MiB를 바이트로 나타낸다.
const MiB int64 = 1 << 20

const (
	// gpuReserveMiB는 GPU마다 드라이버와 연산 버퍼용으로 남겨 두는 메모리다.
	gpuReserveMiB int64 = 512

	// ctxCap은 자동 계획이 고르는 컨텍스트 상한(8192 토큰)이다.
	// 학습 컨텍스트가 더 커도 KV 캐시가 VRAM을 넘지 않도록 여기서 자른다.
	ctxCap int64 = 8192
	// ctxDefaultTrain은 모델의 학습 컨텍스트를 모를 때 쓰는 값이다.
	ctxDefaultTrain int64 = 4096
	// ctxFloor는 자동 계획이 고르는 컨텍스트 하한(2048 토큰)이다.
	ctxFloor int64 = 2048

	// batchGPU는 GPU 경로의 배치 크기다. llama-server 기본값 2048과 같다.
	batchGPU int64 = 2048
	// batchCPU는 CPU 경로의 배치 크기다.
	batchCPU int64 = 512

	// cpuRAMReserveMiB는 CPU 경로에서 서버 프로세스와 OS용으로 남기는 RAM 여유다.
	cpuRAMReserveMiB int64 = 512

	// desktopFreeVRAMMiB는 데스크톱 모드에서 서버 모드 전환을 권하는 남은 VRAM 기준이다.
	desktopFreeVRAMMiB int64 = 1024

	// 입력 상한이다. 정수 연산이 int64 안에 머물도록 값을 먼저 자른다.
	maxLayers      int64 = 1 << 20
	maxEmbedding   int64 = 1 << 20
	maxCtx         int64 = 1 << 22
	maxBatch       int64 = 1 << 16
	minCtxOverride int64 = 256
)

// GPU는 쓸 수 있는 GPU 하나의 상태다. Index는 nvidia-smi의 index와 같다.
type GPU struct {
	Index    int
	FreeMiB  int64
	TotalMiB int64
}

// Overrides는 사용자가 직접 지정한 값이다. nil이면 계산한 값을 쓴다.
type Overrides struct {
	NGL       *string
	Ctx       *int
	CacheType *string
	Batch     *int
	LoadMode  *string
}

// Input은 Plan이 받는 입력이다. 모르는 값은 0으로 둔다.
type Input struct {
	ModelBytes  int64 // GGUF 파일 크기
	Layers      int   // block_count, 모르면 0
	CtxTrain    int   // 학습 컨텍스트, 모르면 0
	Embedding   int   // embedding_length, 모르면 0
	GPUs        []GPU // 쓸 수 있는 GPU만
	RAMAvailMiB int64
	RAMTotalMiB int64
	DesktopMode bool
	Overrides   Overrides
}

// Output은 Plan이 돌려주는 계산 결과다. 항상 사용할 수 있는 값을 담는다.
type Output struct {
	NGL            string   // "0", 정수 문자열, "all", "auto"
	Ctx            int      // 컨텍스트 크기(토큰)
	CacheTypeK     string   // K 캐시 타입
	CacheTypeV     string   // V 캐시 타입
	FlashAttn      bool     // 양자화 KV 캐시에는 필수다
	Batch          int      // 배치 크기
	TensorSplit    string   // GPU가 둘 이상일 때만 값이 있다
	VisibleDevices []int    // CUDA에 보일 GPU index 목록, 오름차순
	LoadMode       string   // 모델 로드 방식
	Fit            bool     // llama-server의 --fit 사용 여부
	CPUOnly        bool     // GPU 오프로드를 하지 않는다
	EstVRAMMiB     int64    // 오프로드한 레이어와 KV 추정
	EstRAMMiB      int64    // 오프로드하지 않은 가중치와 CPU 경로의 KV 추정
	Rationale      []string // 계산 근거
	Warnings       []string // 주의가 필요한 상황
	Advice         []string // 사용자에게 권할 조치
}

// norm은 Input을 int64로 바꾸고 범위를 검사한 값이다.
type norm struct {
	modelBytes int64
	layers     int64
	ctxTrain   int64
	embedding  int64
	ramAvail   int64
	gpus       []GPU
	desktop    bool
	ov         Overrides
}

// choice는 단계별 선택 결과다. 재정의는 이 값을 고친 뒤 파생 값을 계산한다.
type choice struct {
	ngl      string // "auto", "all", "0" 또는 정수 문자열
	ctx      int64
	cache    string // K와 V에 같이 쓰는 타입
	cpuOnly  bool
	sized    bool  // 모델 크기와 레이어 수로 계산했는지
	nGPU     int64 // 오프로드한 레이어 수(출력 레이어 포함), sized일 때만 의미가 있다
	modelMiB int64
	perLayer int64
	offload  int64 // 오프로드 가능한 레이어 수 = Layers + 1
	usable   int64
	batch    int64  // 재정의 값, 0이면 없음
	loadMode string // 재정의 값, 빈 문자열이면 없음
}

// planner는 계산 중 쌓이는 근거와 경고를 모은다.
type planner struct {
	rationale []string
	warnings  []string
}

// Plan은 입력에서 llama-server 설정을 계산한다. 잘못된 재정의는 무시하고 Warnings에 적는다.
func Plan(in Input) Output {
	pl := &planner{}
	n := pl.normalize(in)
	c := pl.choose(n)
	pl.applyOverrides(n, &c)
	return pl.finish(n, c)
}

// ValidateOverrides는 재정의 값의 형식과 범위를 검사한다. 학습 컨텍스트는 모르므로 Ctx는 전역 상한만 본다.
func ValidateOverrides(o Overrides) error {
	var errs []error
	if o.NGL != nil {
		if _, ok := ParseNGL(*o.NGL); !ok {
			errs = append(errs, fmt.Errorf("invalid NGL override %q", *o.NGL))
		}
	}
	if o.Ctx != nil && (int64(*o.Ctx) < minCtxOverride || int64(*o.Ctx) > maxCtx) {
		errs = append(errs, fmt.Errorf("ctx override %d is out of range %d-%d", *o.Ctx, minCtxOverride, maxCtx))
	}
	if o.CacheType != nil && !ValidCacheType(*o.CacheType) {
		errs = append(errs, fmt.Errorf("invalid cache type override %q", *o.CacheType))
	}
	if o.Batch != nil && (int64(*o.Batch) < 1 || int64(*o.Batch) > maxBatch) {
		errs = append(errs, fmt.Errorf("batch override %d is out of range 1-%d", *o.Batch, maxBatch))
	}
	if o.LoadMode != nil && !ValidLoadMode(*o.LoadMode) {
		errs = append(errs, fmt.Errorf("invalid load mode override %q", *o.LoadMode))
	}
	return errors.Join(errs...)
}

// ParseNGL은 GPU 레이어 값을 검사한다. "auto", "all", 0 이상의 정수만 받고 정수는 정규화해 돌려준다.
func ParseNGL(s string) (string, bool) {
	switch s {
	case "auto", "all":
		return s, true
	}
	if s == "" {
		return "", false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return "", false
		}
	}
	v, err := strconv.ParseInt(s, 10, 32)
	if err != nil {
		return "", false
	}
	return strconv.FormatInt(v, 10), true
}

// ValidCacheType은 KV 캐시 타입이 허용 목록(f16, q8_0, q4_0)에 있는지 본다.
// 이 목록은 llama.cpp CUDA 빌드가 기본으로 FlashAttention 커널을 갖는 조합에 맞춘 것이다.
func ValidCacheType(s string) bool {
	switch s {
	case "f16", "q8_0", "q4_0":
		return true
	}
	return false
}

// ValidLoadMode는 로드 방식이 llama-server v0.6.0의 허용 값인지 본다.
func ValidLoadMode(s string) bool {
	switch s {
	case "mmap", "mlock", "mmap+mlock", "none", "auto", "dio":
		return true
	}
	return false
}

// normalize는 입력을 int64로 바꾸고 음수와 너무 큰 값을 자른다. 바꾼 내용은 경고로 남긴다.
func (pl *planner) normalize(in Input) norm {
	n := norm{desktop: in.DesktopMode, ov: in.Overrides}

	n.modelBytes = in.ModelBytes
	if n.modelBytes < 0 {
		pl.warn("negative model size treated as unknown")
		n.modelBytes = 0
	}

	n.layers = int64(in.Layers)
	if n.layers < 0 {
		pl.warn("negative block count treated as unknown")
		n.layers = 0
	}
	if n.layers > maxLayers {
		pl.warnf("block count %d is out of range; clamped to %d", n.layers, maxLayers)
		n.layers = maxLayers
	}

	n.embedding = int64(in.Embedding)
	if n.embedding < 0 {
		pl.warn("negative embedding length treated as unknown")
		n.embedding = 0
	}
	if n.embedding > maxEmbedding {
		pl.warnf("embedding length %d is out of range; clamped to %d", n.embedding, maxEmbedding)
		n.embedding = maxEmbedding
	}

	n.ctxTrain = int64(in.CtxTrain)
	if n.ctxTrain < 0 {
		pl.warn("negative training context treated as unknown")
		n.ctxTrain = 0
	}
	if n.ctxTrain > maxCtx {
		pl.warnf("training context %d is out of range; clamped to %d", n.ctxTrain, maxCtx)
		n.ctxTrain = maxCtx
	}

	n.ramAvail = in.RAMAvailMiB
	if n.ramAvail < 0 {
		pl.warn("negative available RAM treated as 0")
		n.ramAvail = 0
	}

	seen := make(map[int]bool, len(in.GPUs))
	for _, g := range in.GPUs {
		if g.Index < 0 {
			pl.warnf("GPU index %d is negative; GPU ignored", g.Index)
			continue
		}
		if seen[g.Index] {
			pl.warnf("GPU index %d is listed twice; duplicate ignored", g.Index)
			continue
		}
		seen[g.Index] = true
		if g.FreeMiB < 0 {
			pl.warnf("GPU %d reports negative free memory; treated as 0", g.Index)
			g.FreeMiB = 0
		}
		n.gpus = append(n.gpus, g)
	}
	sort.Slice(n.gpus, func(i, j int) bool { return n.gpus[i].Index < n.gpus[j].Index })
	return n
}

// choose는 자동 계획의 단계를 순서대로 시도해 처음 들어맞는 설정을 고른다.
func (pl *planner) choose(n norm) choice {
	ctx0 := planCtx(n.ctxTrain)

	if len(n.gpus) == 0 {
		pl.warn("no usable NVIDIA GPU; running on CPU")
		pl.rationalef("no usable GPU is listed, so the model runs on the CPU with a %d token context", ctx0)
		return choice{ngl: "0", ctx: ctx0, cache: "f16", cpuOnly: true}
	}

	usable := usableSum(n.gpus)
	if n.layers == 0 || n.modelBytes == 0 {
		var reason string
		switch {
		case n.layers == 0 && n.modelBytes == 0:
			reason = "block count and model size are unknown"
		case n.layers == 0:
			reason = "block count is unknown"
		default:
			reason = "model size is unknown"
		}
		pl.rationalef("%s, so the offload layer count cannot be computed here; llama-server --fit (auto) will choose it", reason)
		return choice{ngl: "auto", ctx: ctx0, cache: "f16", usable: usable}
	}

	offload := n.layers + 1
	modelMiB := ceilDiv(n.modelBytes, MiB)
	perLayer := ceilDiv(modelMiB, offload)
	base := choice{
		ctx:      ctx0,
		sized:    true,
		modelMiB: modelMiB,
		perLayer: perLayer,
		offload:  offload,
		usable:   usable,
	}
	pl.rationalef("model is %d MiB with %d offloadable layers (%d repeating plus the output layer), about %d MiB per layer", modelMiB, offload, n.layers, perLayer)
	pl.rationalef("usable VRAM is %d MiB across %d GPU(s), after reserving %d MiB on each GPU", usable, len(n.gpus), gpuReserveMiB)

	// 단계 1: 컨텍스트 기본값, f16 KV, 전체 오프로드.
	if pl.attempt(n, "stage 1", modelMiB, ctx0, "f16", usable) {
		c := base
		c.ngl, c.cache, c.nGPU = "all", "f16", offload
		return c
	}
	// 단계 2: 컨텍스트 기본값, q8_0 KV, 전체 오프로드.
	if pl.attempt(n, "stage 2", modelMiB, ctx0, "q8_0", usable) {
		c := base
		c.ngl, c.cache, c.nGPU = "all", "q8_0", offload
		return c
	}
	// 단계 3: 컨텍스트를 절반씩 줄여 2048까지 q8_0, 전체 오프로드.
	for _, ctx := range ctxLadder(ctx0) {
		if pl.attempt(n, "stage 3", modelMiB, ctx, "q8_0", usable) {
			c := base
			c.ctx = ctx
			c.ngl, c.cache, c.nGPU = "all", "q8_0", offload
			return c
		}
	}

	// 단계 4: 부분 오프로드. 컨텍스트 2048, q8_0 KV, 남는 VRAM에 맞춘 레이어 수.
	ctx4 := min(ctxFloor, ctx0)
	kv4 := kvMiB(n.layers, ctx4, n.embedding, "q8_0")
	room := usable - kv4
	var k int64
	if room > 0 {
		k = room / perLayer
	}
	if k > offload {
		k = offload
	}
	pl.rationalef("stage 4: partial offload at ctx %d with q8_0 KV (%d MiB): floor((%d - %d) / %d) = %d layers", ctx4, kv4, usable, kv4, perLayer, k)
	if k == 0 {
		pl.warn("GPU memory is too small for this model; running on CPU")
		pl.rationalef("stage 4 gives 0 layers, so the model runs on the CPU with a %d token context and f16 KV", ctx0)
		c := base
		c.ngl, c.cache, c.cpuOnly = "0", "f16", true
		return c
	}
	c := base
	c.ctx = ctx4
	c.ngl = strconv.FormatInt(k, 10)
	c.cache = "q8_0"
	c.nGPU = k
	return c
}

// attempt는 한 단계의 적합 여부를 계산하고 근거를 남긴다.
func (pl *planner) attempt(n norm, label string, modelMiB, ctx int64, typ string, usable int64) bool {
	kv := kvMiB(n.layers, ctx, n.embedding, typ)
	total := satAdd(modelMiB, kv)
	ok := total != math.MaxInt64 && total <= usable
	verdict := "rejected"
	if ok {
		verdict = "accepted"
	}
	pl.rationalef("%s %s: ctx %d, %s KV %d MiB, full offload needs %d MiB of %d MiB usable", label, verdict, ctx, typ, kv, total, usable)
	return ok
}

// applyOverrides는 사용자 재정의를 선택 결과에 적용한다. 잘못된 값은 무시하고 경고를 남긴다.
func (pl *planner) applyOverrides(n norm, c *choice) {
	o := n.ov

	if o.NGL != nil {
		v, ok := ParseNGL(*o.NGL)
		switch {
		case !ok:
			pl.warnf("invalid NGL override %q ignored", *o.NGL)
		case len(n.gpus) == 0 && v != "0":
			pl.warnf("NGL override %q ignored: no usable GPU", v)
		default:
			pl.rationalef("manual override: NGL = %s (planned %s)", v, c.ngl)
			c.ngl = v
			c.cpuOnly = v == "0"
			c.sized = c.sized && v != "auto"
			switch {
			case v == "all":
				c.nGPU = c.offload
			case v == "0" || v == "auto":
				c.nGPU = 0
			default:
				k, _ := strconv.ParseInt(v, 10, 64)
				if c.sized && k > c.offload {
					k = c.offload
				}
				c.nGPU = k
			}
		}
	}

	if o.Ctx != nil {
		limit := maxCtx
		if n.ctxTrain > 0 && n.ctxTrain < limit {
			limit = n.ctxTrain
		}
		v := int64(*o.Ctx)
		if v < minCtxOverride || v > limit {
			pl.warnf("ctx override %d ignored: allowed range is %d-%d", v, minCtxOverride, limit)
		} else {
			pl.rationalef("manual override: ctx = %d (planned %d)", v, c.ctx)
			c.ctx = v
		}
	}

	if o.CacheType != nil {
		if ValidCacheType(*o.CacheType) {
			pl.rationalef("manual override: K and V cache type = %s (planned %s)", *o.CacheType, c.cache)
			c.cache = *o.CacheType
		} else {
			pl.warnf("invalid cache type override %q ignored", *o.CacheType)
		}
	}

	if o.Batch != nil {
		v := int64(*o.Batch)
		if v < 1 || v > maxBatch {
			pl.warnf("batch override %d ignored: allowed range is 1-%d", v, maxBatch)
		} else {
			pl.rationalef("manual override: batch = %d", v)
			c.batch = v
		}
	}

	if o.LoadMode != nil {
		if ValidLoadMode(*o.LoadMode) {
			pl.rationalef("manual override: load mode = %s", *o.LoadMode)
			c.loadMode = *o.LoadMode
		} else {
			pl.warnf("invalid load mode override %q ignored", *o.LoadMode)
		}
	}
}

// finish는 선택 결과에서 Output의 파생 값(배치, GPU 목록, 텐서 분할, 추정치, 조언)을 계산한다.
func (pl *planner) finish(n norm, c choice) Output {
	out := Output{
		NGL:        c.ngl,
		Ctx:        int(c.ctx),
		CacheTypeK: c.cache,
		CacheTypeV: c.cache,
		FlashAttn:  c.cache != "f16",
		LoadMode:   "mmap",
		Fit:        c.ngl == "auto",
		CPUOnly:    c.cpuOnly,
	}

	out.Batch = int(batchGPU)
	if c.cpuOnly {
		out.Batch = int(batchCPU)
	}
	if c.batch != 0 {
		out.Batch = int(c.batch)
	}
	if c.loadMode != "" {
		out.LoadMode = c.loadMode
	}

	if !c.cpuOnly {
		for _, g := range n.gpus {
			out.VisibleDevices = append(out.VisibleDevices, g.Index)
		}
		if len(n.gpus) > 1 {
			out.TensorSplit = tensorSplit(n.gpus)
		}
	}

	if n.embedding == 0 {
		pl.warn("embedding length unknown; KV cache size is not estimated")
	}
	if n.layers == 0 {
		pl.warn("block count unknown; KV cache size is not estimated")
	}
	kv := kvMiB(n.layers, c.ctx, n.embedding, c.cache)

	var estVRAM, estRAM int64
	modelMiB := ceilDiv(n.modelBytes, MiB)
	vramKnown := false
	switch {
	case c.cpuOnly:
		if n.modelBytes > 0 {
			estRAM = satAdd(satAdd(modelMiB, kv), cpuRAMReserveMiB)
		}
	case c.sized:
		onGPU := modelMiB
		if c.nGPU < c.offload {
			onGPU = min(modelMiB, c.nGPU*c.perLayer)
		}
		estVRAM = satAdd(onGPU, kv)
		estRAM = modelMiB - onGPU
		vramKnown = true
	}
	out.EstVRAMMiB = estVRAM
	out.EstRAMMiB = estRAM
	pl.rationalef("estimated VRAM %d MiB (offloaded weights plus KV upper bound), estimated RAM %d MiB", estVRAM, estRAM)

	if vramKnown && estVRAM > c.usable {
		pl.warnf("estimated VRAM %d MiB exceeds usable %d MiB; the server may fail to load or run out of memory", estVRAM, c.usable)
	}
	ramWarn := estRAM > n.ramAvail
	if ramWarn {
		pl.warnf("estimated RAM %d MiB exceeds available %d MiB", estRAM, n.ramAvail)
	}

	// 남는 VRAM은 GPU가 있고 사용량을 알 때만 본다. 자동 맞춤 경로에서는 사용량을 모른다.
	if n.desktop {
		lowVRAM := len(n.gpus) > 0 && (vramKnown || c.cpuOnly) && c.usable-estVRAM < desktopFreeVRAMMiB
		if lowVRAM || ramWarn {
			out.Advice = append(out.Advice, "switch to server mode to free memory")
		}
	}

	out.Rationale = pl.rationale
	out.Warnings = pl.warnings
	return out
}

// planCtx는 자동 계획의 기본 컨텍스트를 고른다. 학습 컨텍스트를 넘지 않고 8192를 넘지 않는다.
// 학습 컨텍스트가 2048보다 짧으면 그 값을 그대로 쓴다. 최소 2048 규칙이 학습 범위를 넘게 만들기 때문이다.
func planCtx(train int64) int64 {
	switch {
	case train == 0:
		return ctxDefaultTrain
	case train < ctxFloor:
		return train
	case train > ctxCap:
		return ctxCap
	default:
		return train
	}
}

// ctxLadder는 단계 3에서 시도할 컨텍스트 목록이다. ctx0의 절반부터 2048까지 내려간다.
func ctxLadder(ctx0 int64) []int64 {
	var out []int64
	for v := ctx0 / 2; v > ctxFloor; v /= 2 {
		out = append(out, v)
	}
	if ctx0 > ctxFloor {
		out = append(out, ctxFloor)
	}
	return out
}

// kvMiB는 KV 캐시 크기의 상한(MiB)이다. 계산식은 2 * layers * ctx * embedding * 원소당 바이트 / MiB이다.
// 원소당 바이트는 halfBytesPerElem의 값을 쓰고, K와 V를 합친 2는 식에 이미 들어 있다.
// embedding이나 layers가 0이면 크기를 모르므로 0을 돌려준다.
func kvMiB(layers, ctx, embedding int64, typ string) int64 {
	h := halfBytesPerElem(typ)
	if h == 0 || layers <= 0 || ctx <= 0 || embedding <= 0 {
		return 0
	}
	bytes := satMul(satMul(layers, ctx), satMul(embedding, h))
	return ceilDiv(bytes, MiB)
}

// halfBytesPerElem은 KV 원소당 바이트를 K와 V를 합친 2배 값으로 돌려준다.
// 그래서 f16은 2바이트라 4, q8_0은 1바이트라 2, q4_0은 0.5바이트라 1이다. 정수로 계산하려고 2배를 쓴다.
// 이 값은 K와 V를 합친 2배 계수와 함께 쓰여 kvMiB에서 원래 식과 같아진다.
func halfBytesPerElem(typ string) int64 {
	switch typ {
	case "f16":
		return 4
	case "q8_0":
		return 2
	case "q4_0":
		return 1
	}
	return 0
}

// usableSum은 GPU마다 예약량을 뺀 사용 가능 VRAM의 합이다. 합은 포화 연산으로 계산한다.
func usableSum(gpus []GPU) int64 {
	var sum int64
	for _, g := range gpus {
		sum = satAdd(sum, usableOf(g.FreeMiB))
	}
	return sum
}

// usableOf는 GPU 하나의 사용 가능 VRAM이다.
func usableOf(freeMiB int64) int64 {
	if freeMiB <= gpuReserveMiB {
		return 0
	}
	return freeMiB - gpuReserveMiB
}

// tensorSplit은 GPU별 사용 가능 VRAM 비율을 정수로 약분한 --tensor-split 값을 만든다.
func tensorSplit(gpus []GPU) string {
	vals := make([]int64, len(gpus))
	var g int64
	for i, gp := range gpus {
		vals[i] = usableOf(gp.FreeMiB)
		g = gcd(g, vals[i])
	}
	if g == 0 {
		return ""
	}
	parts := make([]string, len(vals))
	for i, v := range vals {
		parts[i] = strconv.FormatInt(v/g, 10)
	}
	return strings.Join(parts, ",")
}

// gcd는 최대공약수다. gcd(0, x)는 x이다.
func gcd(a, b int64) int64 {
	for b != 0 {
		a, b = b, a%b
	}
	return a
}

// satMul은 음이 아닌 두 수의 곱을 포화 연산으로 계산한다. 넘치면 MaxInt64를 돌려준다.
func satMul(a, b int64) int64 {
	if a == 0 || b == 0 {
		return 0
	}
	if a > math.MaxInt64/b {
		return math.MaxInt64
	}
	return a * b
}

// satAdd는 음이 아닌 두 수의 합을 포화 연산으로 계산한다.
func satAdd(a, b int64) int64 {
	if a > math.MaxInt64-b {
		return math.MaxInt64
	}
	return a + b
}

// ceilDiv는 음이 아닌 a를 양의 b로 나눈 올림값이다. a + b - 1 식은 넘칠 수 있어 쓰지 않는다.
func ceilDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 {
		q++
	}
	return q
}

// planner 메서드: 근거와 경고를 쌓는다.

func (pl *planner) rationalef(format string, args ...any) {
	pl.rationale = append(pl.rationale, fmt.Sprintf(format, args...))
}

func (pl *planner) warn(msg string) {
	pl.warnings = append(pl.warnings, msg)
}

func (pl *planner) warnf(format string, args ...any) {
	pl.warnings = append(pl.warnings, fmt.Sprintf(format, args...))
}
