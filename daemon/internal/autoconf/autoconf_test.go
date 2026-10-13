package autoconf

import (
	"math"
	"strings"
	"testing"
)

// mib는 MiB 단위 값을 바이트로 바꾼다.
func mib(n int64) int64 { return n * MiB }

// strp와 intp는 재정의 필드에 넣을 포인터를 만든다.
func strp(s string) *string { return &s }
func intp(v int) *int       { return &v }

// contains는 목록에 부분 문자열이 있는지 본다.
func contains(list []string, sub string) bool {
	for _, s := range list {
		if strings.Contains(s, sub) {
			return true
		}
	}
	return false
}

// 참조 시나리오: 4.9GB 모델(5018 MiB), 레이어 32, 임베딩 4096, 학습 컨텍스트 32768.
// 레이어 수는 33개(반복 32 + 출력 1)이고 레이어당 153 MiB다.
func refModel() Input {
	return Input{
		ModelBytes:  mib(5018),
		Layers:      32,
		CtxTrain:    32768,
		Embedding:   4096,
		RAMAvailMiB: 16000,
		RAMTotalMiB: 32000,
	}
}

// gpu는 index와 여유 VRAM으로 GPU 하나를 만든다.
func gpu(index int, freeMiB int64) GPU {
	return GPU{Index: index, FreeMiB: freeMiB, TotalMiB: freeMiB}
}

func withGPUs(in Input, gpus ...GPU) Input {
	in.GPUs = gpus
	return in
}

func TestPlanScenarios(t *testing.T) {
	tests := []struct {
		name  string
		in    Input
		check func(t *testing.T, o Output)
	}{
		{
			name: "no GPU runs on CPU",
			in:   refModel(),
			check: func(t *testing.T, o Output) {
				if !o.CPUOnly || o.NGL != "0" || o.Fit {
					t.Fatalf("want CPU only with NGL 0 and fit off, got CPUOnly=%v NGL=%q Fit=%v", o.CPUOnly, o.NGL, o.Fit)
				}
				if o.VisibleDevices != nil {
					t.Fatalf("VisibleDevices = %v, want nil", o.VisibleDevices)
				}
				if o.Batch != 512 || o.Ctx != 8192 || o.CacheTypeK != "f16" || o.CacheTypeV != "f16" || o.FlashAttn {
					t.Fatalf("unexpected CPU settings: batch=%d ctx=%d k=%s v=%s flash=%v", o.Batch, o.Ctx, o.CacheTypeK, o.CacheTypeV, o.FlashAttn)
				}
				if !contains(o.Warnings, "no usable NVIDIA GPU; running on CPU") {
					t.Fatalf("missing CPU warning: %v", o.Warnings)
				}
				// RAM 추정: 모델 5018 + f16 KV(ctx 8192, 4096 MiB) + 512.
				if o.EstRAMMiB != 5018+4096+512 || o.EstVRAMMiB != 0 {
					t.Fatalf("estimates RAM=%d VRAM=%d, want RAM=9626 VRAM=0", o.EstRAMMiB, o.EstVRAMMiB)
				}
			},
		},
		{
			name: "6GB VRAM with 4.9GB model falls to partial offload",
			in:   withGPUs(refModel(), gpu(0, 5600)),
			check: func(t *testing.T, o Output) {
				// usable = 5600 - 512 = 5088. 단계 1~3은 모두 맞지 않는다.
				// 단계 4: ctx 2048, q8_0 KV 512 MiB, floor((5088 - 512) / 153) = 29.
				if o.NGL != "29" || o.Ctx != 2048 || o.CacheTypeK != "q8_0" || o.CacheTypeV != "q8_0" {
					t.Fatalf("got NGL=%s ctx=%d k=%s v=%s, want 29 2048 q8_0 q8_0", o.NGL, o.Ctx, o.CacheTypeK, o.CacheTypeV)
				}
				if !o.FlashAttn {
					t.Fatalf("quantized KV needs flash attention")
				}
				if o.Fit || o.CPUOnly || o.Batch != 2048 || o.LoadMode != "mmap" {
					t.Fatalf("fit=%v cpu=%v batch=%d load=%s", o.Fit, o.CPUOnly, o.Batch, o.LoadMode)
				}
				if len(o.VisibleDevices) != 1 || o.VisibleDevices[0] != 0 || o.TensorSplit != "" {
					t.Fatalf("devices=%v split=%q", o.VisibleDevices, o.TensorSplit)
				}
				if o.EstVRAMMiB != 29*153+512 || o.EstRAMMiB != 5018-29*153 {
					t.Fatalf("estimates VRAM=%d RAM=%d, want VRAM=4949 RAM=581", o.EstVRAMMiB, o.EstRAMMiB)
				}
				if !contains(o.Rationale, "stage 4") {
					t.Fatalf("rationale does not name stage 4: %v", o.Rationale)
				}
			},
		},
		{
			name: "6GB desktop mode advises server mode",
			in: func() Input {
				in := withGPUs(refModel(), gpu(0, 5600))
				in.DesktopMode = true
				return in
			}(),
			check: func(t *testing.T, o Output) {
				if !contains(o.Advice, "switch to server mode to free memory") {
					t.Fatalf("missing advice: %v", o.Advice)
				}
			},
		},
		{
			name: "24GB with 4.9GB model offloads everything at default context",
			in:   withGPUs(refModel(), gpu(0, 24000)),
			check: func(t *testing.T, o Output) {
				if o.NGL != "all" || o.Ctx != 8192 || o.CacheTypeK != "f16" || o.FlashAttn {
					t.Fatalf("got NGL=%s ctx=%d k=%s flash=%v", o.NGL, o.Ctx, o.CacheTypeK, o.FlashAttn)
				}
				if o.EstVRAMMiB != 5018+4096 || o.EstRAMMiB != 0 {
					t.Fatalf("estimates VRAM=%d RAM=%d, want VRAM=9114 RAM=0", o.EstVRAMMiB, o.EstRAMMiB)
				}
				if o.Fit {
					t.Fatalf("explicit plan must turn fit off")
				}
				if len(o.Advice) != 0 {
					t.Fatalf("unexpected advice: %v", o.Advice)
				}
			},
		},
		{
			name: "24GB desktop mode with room left gives no advice",
			in: func() Input {
				in := withGPUs(refModel(), gpu(0, 24000))
				in.DesktopMode = true
				return in
			}(),
			check: func(t *testing.T, o Output) {
				if len(o.Advice) != 0 {
					t.Fatalf("unexpected advice: %v", o.Advice)
				}
			},
		},
		{
			name: "12GB with 20GB model offloads a partial set",
			in: func() Input {
				in := withGPUs(Input{ModelBytes: mib(20480), Layers: 60, CtxTrain: 32768, Embedding: 5120, RAMAvailMiB: 16000}, gpu(0, 12000))
				return in
			}(),
			check: func(t *testing.T, o Output) {
				// usable = 11488. perLayer = ceil(20480 / 61) = 336. KV(ctx 2048, q8_0) = 1200 MiB.
				// floor((11488 - 1200) / 336) = 30.
				if o.NGL != "30" || o.Ctx != 2048 || o.CacheTypeK != "q8_0" {
					t.Fatalf("got NGL=%s ctx=%d k=%s, want 30 2048 q8_0", o.NGL, o.Ctx, o.CacheTypeK)
				}
				if o.EstVRAMMiB != 30*336+1200 || o.EstRAMMiB != 20480-30*336 {
					t.Fatalf("estimates VRAM=%d RAM=%d", o.EstVRAMMiB, o.EstRAMMiB)
				}
				if contains(o.Warnings, "estimated RAM") {
					t.Fatalf("RAM warning not expected with 16000 MiB available: %v", o.Warnings)
				}
			},
		},
		{
			name: "12GB with 20GB model and little RAM warns about RAM",
			in: func() Input {
				in := withGPUs(Input{ModelBytes: mib(20480), Layers: 60, CtxTrain: 32768, Embedding: 5120, RAMAvailMiB: 8000}, gpu(0, 12000))
				return in
			}(),
			check: func(t *testing.T, o Output) {
				if !contains(o.Warnings, "estimated RAM 10400 MiB exceeds available 8000 MiB") {
					t.Fatalf("missing RAM warning: %v", o.Warnings)
				}
			},
		},
		{
			name: "two GPUs 24GB and 8GB split by usable memory",
			in:   withGPUs(refModel(), gpu(1, 8192), gpu(0, 24000)),
			check: func(t *testing.T, o Output) {
				// 입력 순서가 뒤바뀌어도 index 순서로 정렬한다. usable 23488 : 7680 = 367 : 120.
				if o.NGL != "all" || o.Ctx != 8192 {
					t.Fatalf("got NGL=%s ctx=%d, want all 8192", o.NGL, o.Ctx)
				}
				if o.TensorSplit != "367,120" {
					t.Fatalf("TensorSplit=%q, want 367,120", o.TensorSplit)
				}
				if len(o.VisibleDevices) != 2 || o.VisibleDevices[0] != 0 || o.VisibleDevices[1] != 1 {
					t.Fatalf("VisibleDevices=%v, want [0 1]", o.VisibleDevices)
				}
			},
		},
		{
			name: "unknown layer count defers to llama-server fit",
			in: func() Input {
				in := refModel()
				in.Layers = 0
				return withGPUs(in, gpu(0, 24000))
			}(),
			check: func(t *testing.T, o Output) {
				if o.NGL != "auto" || !o.Fit || o.CPUOnly {
					t.Fatalf("got NGL=%s fit=%v cpu=%v, want auto fit on", o.NGL, o.Fit, o.CPUOnly)
				}
				if !contains(o.Rationale, "auto") {
					t.Fatalf("rationale does not explain auto: %v", o.Rationale)
				}
				if o.Ctx != 8192 || len(o.VisibleDevices) != 1 {
					t.Fatalf("ctx=%d devices=%v", o.Ctx, o.VisibleDevices)
				}
			},
		},
		{
			name: "unknown model size defers to llama-server fit",
			in: func() Input {
				in := refModel()
				in.ModelBytes = 0
				return withGPUs(in, gpu(0, 24000))
			}(),
			check: func(t *testing.T, o Output) {
				if o.NGL != "auto" || !o.Fit {
					t.Fatalf("got NGL=%s fit=%v, want auto fit on", o.NGL, o.Fit)
				}
			},
		},
		{
			name: "GPU too small falls back to CPU with a warning",
			in:   withGPUs(refModel(), gpu(0, 400)),
			check: func(t *testing.T, o Output) {
				if !o.CPUOnly || o.NGL != "0" || o.VisibleDevices != nil {
					t.Fatalf("want CPU fallback, got cpu=%v NGL=%s devices=%v", o.CPUOnly, o.NGL, o.VisibleDevices)
				}
				if !contains(o.Warnings, "GPU memory is too small for this model") {
					t.Fatalf("missing too-small warning: %v", o.Warnings)
				}
				if o.Ctx != 8192 || o.CacheTypeK != "f16" {
					t.Fatalf("CPU fallback ctx=%d k=%s, want 8192 f16", o.Ctx, o.CacheTypeK)
				}
			},
		},
		{
			name: "unknown embedding length gives no KV and a warning",
			in: func() Input {
				in := refModel()
				in.Embedding = 0
				return withGPUs(in, gpu(0, 24000))
			}(),
			check: func(t *testing.T, o Output) {
				if !contains(o.Warnings, "embedding length unknown") {
					t.Fatalf("missing embedding warning: %v", o.Warnings)
				}
				if o.NGL != "all" || o.EstVRAMMiB != 5018 {
					t.Fatalf("NGL=%s VRAM=%d, want all 5018 (KV is 0)", o.NGL, o.EstVRAMMiB)
				}
			},
		},
		{
			name: "zero value input runs on CPU without panic",
			in:   Input{},
			check: func(t *testing.T, o Output) {
				if !o.CPUOnly || o.Ctx != 4096 || o.Batch != 512 {
					t.Fatalf("zero input: cpu=%v ctx=%d batch=%d, want cpu 4096 512", o.CPUOnly, o.Ctx, o.Batch)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, Plan(tc.in))
		})
	}
}

func TestPlanOverrides(t *testing.T) {
	base24 := func() Input { return withGPUs(refModel(), gpu(0, 24000)) }
	tests := []struct {
		name  string
		in    Input
		check func(t *testing.T, o Output)
	}{
		{
			name: "manual NGL is applied and marked",
			in:   func() Input { in := base24(); in.Overrides.NGL = strp("17"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.NGL != "17" || o.Fit {
					t.Fatalf("NGL=%s fit=%v, want 17 false", o.NGL, o.Fit)
				}
				if !contains(o.Rationale, "manual override: NGL = 17") {
					t.Fatalf("rationale missing override: %v", o.Rationale)
				}
				if o.EstVRAMMiB != 17*153+4096 {
					t.Fatalf("VRAM=%d, want %d", o.EstVRAMMiB, 17*153+4096)
				}
			},
		},
		{
			name: "invalid NGL is ignored with a warning",
			in:   func() Input { in := base24(); in.Overrides.NGL = strp("bogus"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.NGL != "all" || !contains(o.Warnings, "invalid NGL override") {
					t.Fatalf("NGL=%s warnings=%v", o.NGL, o.Warnings)
				}
			},
		},
		{
			name: "negative NGL is invalid",
			in:   func() Input { in := base24(); in.Overrides.NGL = strp("-1"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.NGL != "all" || !contains(o.Warnings, "invalid NGL override") {
					t.Fatalf("NGL=%s warnings=%v", o.NGL, o.Warnings)
				}
			},
		},
		{
			name: "NGL 0 forces CPU",
			in:   func() Input { in := base24(); in.Overrides.NGL = strp("0"); return in }(),
			check: func(t *testing.T, o Output) {
				if !o.CPUOnly || o.NGL != "0" || o.VisibleDevices != nil || o.Batch != 512 {
					t.Fatalf("cpu=%v NGL=%s devices=%v batch=%d", o.CPUOnly, o.NGL, o.VisibleDevices, o.Batch)
				}
			},
		},
		{
			name: "NGL auto turns fit on",
			in:   func() Input { in := base24(); in.Overrides.NGL = strp("auto"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.NGL != "auto" || !o.Fit {
					t.Fatalf("NGL=%s fit=%v, want auto true", o.NGL, o.Fit)
				}
			},
		},
		{
			name: "NGL override without a GPU is ignored",
			in:   func() Input { in := refModel(); in.Overrides.NGL = strp("17"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.NGL != "0" || !o.CPUOnly || !contains(o.Warnings, "ignored: no usable GPU") {
					t.Fatalf("NGL=%s cpu=%v warnings=%v", o.NGL, o.CPUOnly, o.Warnings)
				}
			},
		},
		{
			name: "manual ctx within training limit",
			in:   func() Input { in := base24(); in.Overrides.Ctx = intp(4096); return in }(),
			check: func(t *testing.T, o Output) {
				if o.Ctx != 4096 {
					t.Fatalf("Ctx=%d, want 4096", o.Ctx)
				}
			},
		},
		{
			name: "ctx below 256 is ignored",
			in:   func() Input { in := base24(); in.Overrides.Ctx = intp(100); return in }(),
			check: func(t *testing.T, o Output) {
				if o.Ctx != 8192 || !contains(o.Warnings, "ctx override 100 ignored") {
					t.Fatalf("Ctx=%d warnings=%v", o.Ctx, o.Warnings)
				}
			},
		},
		{
			name: "ctx above training context is ignored",
			in:   func() Input { in := base24(); in.Overrides.Ctx = intp(40000); return in }(),
			check: func(t *testing.T, o Output) {
				if o.Ctx != 8192 || !contains(o.Warnings, "ctx override 40000 ignored") {
					t.Fatalf("Ctx=%d warnings=%v", o.Ctx, o.Warnings)
				}
			},
		},
		{
			name: "ctx above global cap is ignored when training context is unknown",
			in: func() Input {
				in := base24()
				in.CtxTrain = 0
				in.Overrides.Ctx = intp(1 << 23)
				return in
			}(),
			check: func(t *testing.T, o Output) {
				if o.Ctx != 4096 || !contains(o.Warnings, "ctx override") {
					t.Fatalf("Ctx=%d warnings=%v", o.Ctx, o.Warnings)
				}
			},
		},
		{
			name: "ctx accepted up to global cap when training context is unknown",
			in: func() Input {
				in := base24()
				in.CtxTrain = 0
				in.Overrides.Ctx = intp(100000)
				return in
			}(),
			check: func(t *testing.T, o Output) {
				if o.Ctx != 100000 {
					t.Fatalf("Ctx=%d, want 100000", o.Ctx)
				}
			},
		},
		{
			name: "manual q4_0 cache sets flash attention",
			in:   func() Input { in := base24(); in.Overrides.CacheType = strp("q4_0"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.CacheTypeK != "q4_0" || o.CacheTypeV != "q4_0" || !o.FlashAttn {
					t.Fatalf("k=%s v=%s flash=%v", o.CacheTypeK, o.CacheTypeV, o.FlashAttn)
				}
			},
		},
		{
			name: "invalid cache type is ignored",
			in:   func() Input { in := base24(); in.Overrides.CacheType = strp("q5_0"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.CacheTypeK != "f16" || !contains(o.Warnings, "invalid cache type override") {
					t.Fatalf("k=%s warnings=%v", o.CacheTypeK, o.Warnings)
				}
			},
		},
		{
			name: "manual batch",
			in:   func() Input { in := base24(); in.Overrides.Batch = intp(4096); return in }(),
			check: func(t *testing.T, o Output) {
				if o.Batch != 4096 {
					t.Fatalf("Batch=%d, want 4096", o.Batch)
				}
			},
		},
		{
			name: "zero batch is ignored",
			in:   func() Input { in := base24(); in.Overrides.Batch = intp(0); return in }(),
			check: func(t *testing.T, o Output) {
				if o.Batch != 2048 || !contains(o.Warnings, "batch override 0 ignored") {
					t.Fatalf("Batch=%d warnings=%v", o.Batch, o.Warnings)
				}
			},
		},
		{
			name: "negative batch is ignored",
			in:   func() Input { in := base24(); in.Overrides.Batch = intp(-5); return in }(),
			check: func(t *testing.T, o Output) {
				if o.Batch != 2048 || !contains(o.Warnings, "batch override -5 ignored") {
					t.Fatalf("Batch=%d warnings=%v", o.Batch, o.Warnings)
				}
			},
		},
		{
			name: "huge batch is ignored without overflow",
			in:   func() Input { in := base24(); in.Overrides.Batch = intp(int(int64(1) << 40)); return in }(),
			check: func(t *testing.T, o Output) {
				if o.Batch != 2048 || !contains(o.Warnings, "batch override") {
					t.Fatalf("Batch=%d warnings=%v", o.Batch, o.Warnings)
				}
			},
		},
		{
			name: "manual load mode mlock",
			in:   func() Input { in := base24(); in.Overrides.LoadMode = strp("mlock"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.LoadMode != "mlock" {
					t.Fatalf("LoadMode=%s, want mlock", o.LoadMode)
				}
			},
		},
		{
			name: "removed --no-mmap style load mode is ignored",
			in:   func() Input { in := base24(); in.Overrides.LoadMode = strp("--no-mmap"); return in }(),
			check: func(t *testing.T, o Output) {
				if o.LoadMode != "mmap" || !contains(o.Warnings, "invalid load mode override") {
					t.Fatalf("LoadMode=%s warnings=%v", o.LoadMode, o.Warnings)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, Plan(tc.in))
		})
	}
}

func TestPlanAbnormalInputs(t *testing.T) {
	t.Run("negative inputs are normalized with warnings", func(t *testing.T) {
		in := Input{
			ModelBytes:  -1,
			Layers:      -5,
			CtxTrain:    -7,
			Embedding:   -3,
			RAMAvailMiB: -10,
			GPUs: []GPU{
				{Index: -1, FreeMiB: 1000},
				{Index: 0, FreeMiB: -50},
				{Index: 0, FreeMiB: 9999},
			},
		}
		o := Plan(in)
		if !o.CPUOnly && o.NGL != "auto" {
			t.Fatalf("unexpected plan NGL=%s cpu=%v", o.NGL, o.CPUOnly)
		}
		for _, sub := range []string{"negative model size", "negative block count", "negative training context", "negative embedding", "negative available RAM", "GPU index -1", "reports negative free memory", "listed twice"} {
			if !contains(o.Warnings, sub) {
				t.Fatalf("missing warning %q in %v", sub, o.Warnings)
			}
		}
	})

	t.Run("huge inputs do not overflow", func(t *testing.T) {
		in := Input{
			ModelBytes:  math.MaxInt64,
			Layers:      math.MaxInt32,
			CtxTrain:    math.MaxInt32,
			Embedding:   math.MaxInt32,
			RAMAvailMiB: math.MaxInt64,
			GPUs:        []GPU{gpu(0, math.MaxInt64), gpu(1, math.MaxInt64)},
		}
		o := Plan(in)
		if o.Ctx <= 0 || o.Batch <= 0 {
			t.Fatalf("bad output ctx=%d batch=%d", o.Ctx, o.Batch)
		}
		if o.EstVRAMMiB < 0 || o.EstRAMMiB < 0 {
			t.Fatalf("negative estimate VRAM=%d RAM=%d", o.EstVRAMMiB, o.EstRAMMiB)
		}
	})

	t.Run("huge model on a small GPU falls back to CPU", func(t *testing.T) {
		in := Input{
			ModelBytes:  math.MaxInt64,
			Layers:      math.MaxInt32,
			CtxTrain:    math.MaxInt32,
			Embedding:   math.MaxInt32,
			RAMAvailMiB: 1,
			GPUs:        []GPU{gpu(0, 16384)},
		}
		o := Plan(in)
		if !o.CPUOnly || !contains(o.Warnings, "GPU memory is too small") {
			t.Fatalf("cpu=%v warnings=%v", o.CPUOnly, o.Warnings)
		}
		if o.EstRAMMiB <= 0 {
			t.Fatalf("EstRAMMiB=%d, want a positive estimate", o.EstRAMMiB)
		}
	})

	t.Run("huge KV estimate saturates instead of wrapping", func(t *testing.T) {
		// 실제 곱은 2^66 바이트라 int64를 넘는다. 포화 연산이면 MiB 단위로 2^40보다 훨씬 커야 한다.
		if got := kvMiB(1<<20, 1<<22, 1<<20, "f16"); got < 1<<40 {
			t.Fatalf("kvMiB = %d, want a saturated large value", got)
		}
	})

	t.Run("huge overrides are rejected and defaults stay", func(t *testing.T) {
		in := withGPUs(refModel(), gpu(0, 24000))
		in.Overrides = Overrides{NGL: strp("99999999999999"), Ctx: intp(math.MaxInt32), Batch: intp(math.MaxInt32)}
		o := Plan(in)
		if o.Ctx != 8192 || o.Batch != 2048 || o.NGL != "all" {
			t.Fatalf("NGL=%s ctx=%d batch=%d, want all 8192 2048", o.NGL, o.Ctx, o.Batch)
		}
		if len(o.Warnings) < 3 {
			t.Fatalf("want a warning for each rejected override, got %v", o.Warnings)
		}
	})

	t.Run("zero-length GPU list with zero index works", func(t *testing.T) {
		o := Plan(withGPUs(refModel(), gpu(0, 0)))
		if !o.CPUOnly {
			t.Fatalf("zero free VRAM must not offload, got NGL=%s", o.NGL)
		}
	})
}

func TestValidateOverrides(t *testing.T) {
	tests := []struct {
		name    string
		o       Overrides
		wantErr bool
	}{
		{"empty overrides are valid", Overrides{}, false},
		{"valid set", Overrides{NGL: strp("all"), Ctx: intp(4096), CacheType: strp("q8_0"), Batch: intp(512), LoadMode: strp("mmap+mlock")}, false},
		{"bad NGL", Overrides{NGL: strp("x")}, true},
		{"ctx too small", Overrides{Ctx: intp(255)}, true},
		{"ctx too large", Overrides{Ctx: intp(1 << 23)}, true},
		{"bad cache type", Overrides{CacheType: strp("q5_1")}, true},
		{"batch zero", Overrides{Batch: intp(0)}, true},
		{"bad load mode", Overrides{LoadMode: strp("mlock ")}, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateOverrides(tc.o)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v, wantErr=%v", err, tc.wantErr)
			}
		})
	}
}

func TestParseNGL(t *testing.T) {
	tests := []struct {
		in   string
		want string
		ok   bool
	}{
		{"0", "0", true},
		{"007", "7", true},
		{"auto", "auto", true},
		{"all", "all", true},
		{"", "", false},
		{"-1", "", false},
		{"+3", "", false},
		{"1.5", "", false},
		{"99999999999", "", false},
		{"auto ", "", false},
	}
	for _, tc := range tests {
		got, ok := ParseNGL(tc.in)
		if got != tc.want || ok != tc.ok {
			t.Errorf("ParseNGL(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

func TestPlanCtx(t *testing.T) {
	tests := map[int64]int64{
		0:     4096,
		1000:  1000,
		2048:  2048,
		4096:  4096,
		8192:  8192,
		32768: 8192,
	}
	for train, want := range tests {
		if got := planCtx(train); got != want {
			t.Errorf("planCtx(%d) = %d, want %d", train, got, want)
		}
	}
}

func TestCtxLadder(t *testing.T) {
	tests := []struct {
		ctx0 int64
		want []int64
	}{
		{8192, []int64{4096, 2048}},
		{4096, []int64{2048}},
		{3000, []int64{2048}},
		{2049, []int64{2048}},
		{2048, nil},
		{1000, nil},
	}
	for _, tc := range tests {
		got := ctxLadder(tc.ctx0)
		if len(got) != len(tc.want) {
			t.Errorf("ctxLadder(%d) = %v, want %v", tc.ctx0, got, tc.want)
			continue
		}
		for i := range got {
			if got[i] != tc.want[i] {
				t.Errorf("ctxLadder(%d) = %v, want %v", tc.ctx0, got, tc.want)
				break
			}
		}
	}
}

func TestKVEstimate(t *testing.T) {
	// 레이어 32, ctx 8192, 임베딩 4096이면 f16은 4096 MiB, q8_0은 2048, q4_0은 1024다.
	tests := []struct {
		typ  string
		want int64
	}{
		{"f16", 4096},
		{"q8_0", 2048},
		{"q4_0", 1024},
	}
	for _, tc := range tests {
		if got := kvMiB(32, 8192, 4096, tc.typ); got != tc.want {
			t.Errorf("kvMiB(%s) = %d, want %d", tc.typ, got, tc.want)
		}
	}
	if got := kvMiB(32, 8192, 0, "f16"); got != 0 {
		t.Errorf("kvMiB with unknown embedding = %d, want 0", got)
	}
	if got := kvMiB(32, 8192, 4096, "bf16"); got != 0 {
		t.Errorf("kvMiB with unsupported type = %d, want 0", got)
	}
}

func TestParallelSlotsMultiplyTheContextAndTheKVEstimate(t *testing.T) {
	// 참조 모델(5018 MiB)에 VRAM 이 충분한 GPU 하나.
	one := Plan(withGPUs(refModel(), gpu(0, 24000)))
	in := withGPUs(refModel(), gpu(0, 24000))
	in.Overrides.Parallel = intp(4)
	four := Plan(in)

	if four.Parallel != 4 || four.SlotCtx != one.Ctx {
		t.Fatalf("parallel = %d, slot ctx = %d, single ctx = %d", four.Parallel, four.SlotCtx, one.Ctx)
	}
	if four.Ctx != four.SlotCtx*4 {
		t.Fatalf("server ctx %d must be 4 x slot ctx %d", four.Ctx, four.SlotCtx)
	}
	if four.EstVRAMMiB <= one.EstVRAMMiB {
		t.Fatalf("four slots must need more VRAM: %d vs %d", four.EstVRAMMiB, one.EstVRAMMiB)
	}
	if !contains(four.Rationale, "4 parallel slots") {
		t.Fatalf("rationale does not explain the slots: %v", four.Rationale)
	}
}

func TestParallelSlotsShrinkTheContextWhenVRAMIsTight(t *testing.T) {
	tight := withGPUs(refModel(), gpu(0, 7000))
	single := Plan(tight)
	tight.Overrides.Parallel = intp(4)
	multi := Plan(tight)
	if multi.SlotCtx > single.SlotCtx {
		t.Fatalf("slot ctx grew with more slots: %d > %d", multi.SlotCtx, single.SlotCtx)
	}
	if multi.Ctx != multi.SlotCtx*4 {
		t.Fatalf("server ctx = %d, slot ctx = %d", multi.Ctx, multi.SlotCtx)
	}
}

func TestParallelOverrideIsValidated(t *testing.T) {
	for _, bad := range []int{0, -1, 17} {
		if err := ValidateOverrides(Overrides{Parallel: intp(bad)}); err == nil {
			t.Fatalf("parallel %d accepted", bad)
		}
	}
	if err := ValidateOverrides(Overrides{Parallel: intp(8)}); err != nil {
		t.Fatal(err)
	}
	in := withGPUs(refModel(), gpu(0, 24000))
	in.Overrides.Parallel = intp(99)
	out := Plan(in)
	if out.Parallel != 1 || !contains(out.Warnings, "parallel override 99 ignored") {
		t.Fatalf("parallel = %d, warnings = %v", out.Parallel, out.Warnings)
	}
}

func TestAgentProfilePrefersLongContextAndTwoSlots(t *testing.T) {
	roomy := withGPUs(refModel(), gpu(0, 48000))
	chat := Plan(roomy)
	roomy.Overrides.Profile = strp(ProfileAgent)
	agent := Plan(roomy)
	if chat.SlotCtx > 8192 {
		t.Fatalf("the chat profile must stay conservative: %d", chat.SlotCtx)
	}
	if agent.Parallel != 2 || agent.SlotCtx != 32768 || agent.Ctx != 65536 {
		t.Fatalf("agent plan = parallel %d slot ctx %d ctx %d", agent.Parallel, agent.SlotCtx, agent.Ctx)
	}
	if !contains(agent.Rationale, "agent profile") || contains(agent.Warnings, "compact its history") {
		t.Fatalf("rationale = %v warnings = %v", agent.Rationale, agent.Warnings)
	}
	// 사용자가 직접 준 슬롯 수와 컨텍스트가 프로필보다 우선한다.
	roomy.Overrides.Parallel = intp(1)
	roomy.Overrides.Ctx = intp(20000)
	manual := Plan(roomy)
	if manual.Parallel != 1 || manual.SlotCtx != 20000 {
		t.Fatalf("manual = parallel %d slot ctx %d", manual.Parallel, manual.SlotCtx)
	}
}

func TestAgentProfileWarnsWhenMemoryIsTight(t *testing.T) {
	tight := withGPUs(refModel(), gpu(0, 6500))
	tight.Overrides.Profile = strp(ProfileAgent)
	out := Plan(tight)
	if out.SlotCtx >= 16384 && !out.CPUOnly {
		t.Skipf("model fits with a roomy context: %d", out.SlotCtx)
	}
	if !contains(out.Warnings, "compact its history") {
		t.Fatalf("warnings = %v (slot ctx %d)", out.Warnings, out.SlotCtx)
	}
}

func TestAgentProfileOnCPUKeepsOneSlot(t *testing.T) {
	in := refModel()
	in.Overrides.Profile = strp(ProfileAgent)
	out := Plan(in)
	if out.Parallel != 1 || !out.CPUOnly {
		t.Fatalf("cpu agent plan = %+v", out)
	}
}

func TestProfileIsValidated(t *testing.T) {
	if err := ValidateOverrides(Overrides{Profile: strp("turbo")}); err == nil {
		t.Fatal("an unknown profile was accepted")
	}
	for _, ok := range []string{ProfileChat, ProfileAgent} {
		if err := ValidateOverrides(Overrides{Profile: strp(ok)}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAgentProfileReusesPromptCacheAndCacheRAMIsBounded(t *testing.T) {
	in := withGPUs(refModel(), gpu(0, 24000))
	in.RAMTotalMiB = 16000
	chat := Plan(in)
	if chat.CacheReuse != 0 {
		t.Fatalf("chat profile cache reuse = %d", chat.CacheReuse)
	}
	if chat.CacheRAMMiB != 2000 {
		t.Fatalf("cache RAM for 16000 MiB = %d, want 2000", chat.CacheRAMMiB)
	}
	in.Overrides.Profile = strp(ProfileAgent)
	agent := Plan(in)
	if agent.CacheReuse != 256 || !contains(agent.Rationale, "cache reuse") {
		t.Fatalf("agent cache reuse = %d rationale = %v", agent.CacheReuse, agent.Rationale)
	}
	in.RAMTotalMiB = 2000
	if got := Plan(in).CacheRAMMiB; got != 512 {
		t.Fatalf("small machine cache RAM = %d, want the 512 floor", got)
	}
	in.RAMTotalMiB = 262144
	if got := Plan(in).CacheRAMMiB; got != 8192 {
		t.Fatalf("large machine cache RAM = %d, want the 8192 ceiling", got)
	}
	in.RAMTotalMiB = 0
	if got := Plan(in).CacheRAMMiB; got != 0 {
		t.Fatalf("unknown RAM must leave the server default, got %d", got)
	}
}
