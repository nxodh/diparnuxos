package llamaargs

import (
	"reflect"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
)

// gpuPlan은 24GB GPU 하나에 전체 오프로드하는 계획 결과다.
func gpuPlan() autoconf.Output {
	return autoconf.Output{
		NGL:            "all",
		Ctx:            8192,
		CacheTypeK:     "f16",
		CacheTypeV:     "f16",
		Batch:          2048,
		LoadMode:       "mmap",
		Fit:            false,
		VisibleDevices: []int{0},
	}
}

func lanSettings() ServerSettings {
	return ServerSettings{
		ModelPath:  "/var/lib/diparnux/models/m.gguf",
		Alias:      "local",
		Host:       "127.0.0.1",
		Port:       8080,
		APIKeyFile: "/etc/diparnux/api-keys",
		Metrics:    true,
	}
}

func TestEnvGPUPlan(t *testing.T) {
	env, err := Env(gpuPlan(), lanSettings())
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	want := map[string]string{
		"LLAMA_ARG_MODEL":            "/var/lib/diparnux/models/m.gguf",
		"LLAMA_ARG_HOST":             "127.0.0.1",
		"LLAMA_ARG_PORT":             "8080",
		"LLAMA_ARG_ALIAS":            "local",
		"LLAMA_ARG_API_KEY_FILE":     "/etc/diparnux/api-keys",
		"LLAMA_ARG_ENDPOINT_METRICS": "1",
		"LLAMA_ARG_N_GPU_LAYERS":     "all",
		"LLAMA_ARG_CTX_SIZE":         "8192",
		"LLAMA_ARG_BATCH":            "2048",
		"LLAMA_ARG_CACHE_TYPE_K":     "f16",
		"LLAMA_ARG_CACHE_TYPE_V":     "f16",
		"LLAMA_ARG_FLASH_ATTN":       "off",
		"LLAMA_ARG_LOAD_MODE":        "mmap",
		"LLAMA_ARG_FIT":              "off",
		"CUDA_DEVICE_ORDER":          "PCI_BUS_ID",
		"CUDA_VISIBLE_DEVICES":       "0",
	}
	if !reflect.DeepEqual(env, want) {
		t.Fatalf("env mismatch\n got: %v\nwant: %v", env, want)
	}
}

func TestEnvTensorSplitAndMultiGPU(t *testing.T) {
	p := gpuPlan()
	p.VisibleDevices = []int{0, 2}
	p.TensorSplit = "367,120"
	s := lanSettings()
	s.Metrics = false
	env, err := Env(p, s)
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	if env["LLAMA_ARG_TENSOR_SPLIT"] != "367,120" || env["CUDA_VISIBLE_DEVICES"] != "0,2" {
		t.Fatalf("split=%q visible=%q", env["LLAMA_ARG_TENSOR_SPLIT"], env["CUDA_VISIBLE_DEVICES"])
	}
	if _, ok := env["LLAMA_ARG_ENDPOINT_METRICS"]; ok {
		t.Fatalf("metrics variable set while Metrics is false")
	}
}

func TestEnvCPUOnly(t *testing.T) {
	p := autoconf.Output{NGL: "0", Ctx: 4096, CacheTypeK: "f16", CacheTypeV: "f16", Batch: 512, LoadMode: "mmap", CPUOnly: true}
	s := ServerSettings{ModelPath: "/m.gguf", Host: "127.0.0.1", Port: 8080}
	env, err := Env(p, s)
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	if v, ok := env["CUDA_VISIBLE_DEVICES"]; !ok || v != "" {
		t.Fatalf("CUDA_VISIBLE_DEVICES = %q (present=%v), want empty", v, ok)
	}
	if env["LLAMA_ARG_N_GPU_LAYERS"] != "0" || env["LLAMA_ARG_FIT"] != "off" {
		t.Fatalf("NGL=%q FIT=%q", env["LLAMA_ARG_N_GPU_LAYERS"], env["LLAMA_ARG_FIT"])
	}
	if _, ok := env["LLAMA_ARG_API_KEY_FILE"]; ok {
		t.Fatalf("API key variable set though no key file was given")
	}
}

func TestEnvAutoUsesFit(t *testing.T) {
	p := gpuPlan()
	p.NGL = "auto"
	p.Fit = true
	env, err := Env(p, lanSettings())
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	if env["LLAMA_ARG_N_GPU_LAYERS"] != "auto" || env["LLAMA_ARG_FIT"] != "on" {
		t.Fatalf("NGL=%q FIT=%q", env["LLAMA_ARG_N_GPU_LAYERS"], env["LLAMA_ARG_FIT"])
	}
}

func TestEnvFlashAttnForQuantizedCache(t *testing.T) {
	p := gpuPlan()
	p.CacheTypeK, p.CacheTypeV, p.FlashAttn = "q8_0", "q8_0", true
	env, err := Env(p, lanSettings())
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	if env["LLAMA_ARG_FLASH_ATTN"] != "on" {
		t.Fatalf("FLASH_ATTN=%q, want on", env["LLAMA_ARG_FLASH_ATTN"])
	}
}

func TestEnvRejectsBadSettings(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(p *autoconf.Output, s *ServerSettings)
	}{
		{"LAN host without key", func(p *autoconf.Output, s *ServerSettings) { s.Host = "0.0.0.0"; s.APIKeyFile = "" }},
		{"second address is LAN", func(p *autoconf.Output, s *ServerSettings) { s.Host = "127.0.0.1,192.168.1.5"; s.APIKeyFile = "" }},
		{"empty host", func(p *autoconf.Output, s *ServerSettings) { s.Host = " , " }},
		{"IPv6 any without key", func(p *autoconf.Output, s *ServerSettings) { s.Host = "::"; s.APIKeyFile = "" }},
		{"unix socket without key", func(p *autoconf.Output, s *ServerSettings) { s.Host = "/run/llama.sock"; s.APIKeyFile = "" }},
		{"port zero", func(p *autoconf.Output, s *ServerSettings) { s.Port = 0 }},
		{"port too large", func(p *autoconf.Output, s *ServerSettings) { s.Port = 65536 }},
		{"relative model path", func(p *autoconf.Output, s *ServerSettings) { s.ModelPath = "models/m.gguf" }},
		{"newline in alias", func(p *autoconf.Output, s *ServerSettings) { s.Alias = "a\nb" }},
		{"relative key file", func(p *autoconf.Output, s *ServerSettings) { s.APIKeyFile = "keys" }},
		{"quantized KV without flash attention", func(p *autoconf.Output, s *ServerSettings) {
			p.CacheTypeK, p.CacheTypeV, p.FlashAttn = "q8_0", "q8_0", false
		}},
		{"GPU plan without devices", func(p *autoconf.Output, s *ServerSettings) { p.VisibleDevices = nil }},
		{"CPU plan with devices", func(p *autoconf.Output, s *ServerSettings) {
			p.CPUOnly, p.NGL = true, "0"
		}},
		{"CPU plan with NGL all", func(p *autoconf.Output, s *ServerSettings) {
			p.CPUOnly, p.VisibleDevices = true, nil
		}},
		{"NGL with leading zero", func(p *autoconf.Output, s *ServerSettings) { p.NGL = "007" }},
		{"zero context", func(p *autoconf.Output, s *ServerSettings) { p.Ctx = 0 }},
		{"bad cache type", func(p *autoconf.Output, s *ServerSettings) { p.CacheTypeK = "q5_0" }},
		{"bad load mode", func(p *autoconf.Output, s *ServerSettings) { p.LoadMode = "--mlock" }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := gpuPlan()
			s := lanSettings()
			tc.mutate(&p, &s)
			if _, err := Env(p, s); err == nil {
				t.Fatalf("Env accepted a bad setting")
			}
		})
	}
}

func TestEnvAcceptsLoopbackVariants(t *testing.T) {
	for _, host := range []string{"127.0.0.1", "::1", "localhost", " localhost , 127.0.0.1 "} {
		s := lanSettings()
		s.Host = host
		s.APIKeyFile = ""
		if _, err := Env(gpuPlan(), s); err != nil {
			t.Errorf("host %q without key: %v", host, err)
		}
	}
}

// parseSystemdValue는 systemd.exec(5) EnvironmentFile 규칙으로 KEY="..." 한 줄의 값을 되돌린다.
// 테스트에서 quote의 왕복을 확인하려고 쓴다. 큰따옴표 안에서 "\`$ 앞의 백슬래시만 벗기고,
// 다른 백슬래시는 그대로 둔다.
func parseSystemdValue(t *testing.T, line string) string {
	t.Helper()
	eq := strings.IndexByte(line, '=')
	if eq < 0 || len(line) < eq+2 || line[eq+1] != '"' || line[len(line)-1] != '"' {
		t.Fatalf("line is not KEY=\"...\": %q", line)
	}
	body := line[eq+2 : len(line)-1]
	var out strings.Builder
	for i := 0; i < len(body); i++ {
		c := body[i]
		if c == '\\' && i+1 < len(body) && strings.IndexByte("\"\\`$", body[i+1]) >= 0 {
			out.WriteByte(body[i+1])
			i++
			continue
		}
		out.WriteByte(c)
	}
	return out.String()
}

func TestEnvFileQuotingRoundTrip(t *testing.T) {
	values := []string{
		"/plain/path.gguf",
		`with "quotes" and \backslash`,
		"dollar $HOME and ${X}",
		"backtick ` here",
		"a\\\"b$c`d",
		"",
		"spaces  inside",
		"한글 경로",
	}
	for _, v := range values {
		env := map[string]string{"LLAMA_ARG_ALIAS": v}
		text := EnvFile(env)
		line := strings.TrimSuffix(text, "\n")
		if strings.Count(text, "\n") != 1 {
			t.Fatalf("EnvFile for %q wrote %d lines", v, strings.Count(text, "\n"))
		}
		if got := parseSystemdValue(t, line); got != v {
			t.Errorf("round trip of %q gave %q (line %q)", v, got, line)
		}
	}
}

func TestEnvFileGolden(t *testing.T) {
	env := map[string]string{
		"LLAMA_ARG_MODEL":      "/m.gguf",
		"CUDA_VISIBLE_DEVICES": "",
		"LLAMA_ARG_ALIAS":      `a$b"c`,
	}
	want := "CUDA_VISIBLE_DEVICES=\"\"\n" +
		"LLAMA_ARG_ALIAS=\"a\\$b\\\"c\"\n" +
		"LLAMA_ARG_MODEL=\"/m.gguf\"\n"
	if got := EnvFile(env); got != want {
		t.Fatalf("EnvFile\n got: %q\nwant: %q", got, want)
	}
}

func TestEnvFileFromEnvIsSorted(t *testing.T) {
	env, err := Env(gpuPlan(), lanSettings())
	if err != nil {
		t.Fatalf("Env: %v", err)
	}
	lines := strings.Split(strings.TrimSuffix(EnvFile(env), "\n"), "\n")
	if len(lines) != len(env) {
		t.Fatalf("got %d lines for %d variables", len(lines), len(env))
	}
	for i := 1; i < len(lines); i++ {
		if lines[i-1] >= lines[i] {
			t.Fatalf("lines not sorted: %q before %q", lines[i-1], lines[i])
		}
	}
}

func TestParseOffloadLastMatchWins(t *testing.T) {
	log := "load_tensors: offloading output layer to GPU\n" +
		"load_tensors: offloaded 17/33 layers to GPU\n" +
		"some other line\n" +
		"load_tensors: offloaded 33/33 layers to GPU\n"
	o, tot, ok := ParseOffload(log)
	if !ok || o != 33 || tot != 33 {
		t.Fatalf("ParseOffload = (%d, %d, %v), want (33, 33, true)", o, tot, ok)
	}
}

func TestParseOffloadMissing(t *testing.T) {
	if _, _, ok := ParseOffload("load_tensors: model buffer size = 100.00 MiB\n"); ok {
		t.Fatalf("ParseOffload found a value in a log without the line")
	}
	if _, _, ok := ParseOffload(""); ok {
		t.Fatalf("ParseOffload found a value in an empty log")
	}
}

func TestParseOffloadOverflowIsRejected(t *testing.T) {
	if _, _, ok := ParseOffload("offloaded 99999999999999999999999/3 layers to GPU"); ok {
		t.Fatalf("ParseOffload accepted an overflowing count")
	}
}

func TestParseListDevices(t *testing.T) {
	out := "Available devices:\n" +
		"  CUDA0: NVIDIA GeForce RTX 3060 Laptop GPU (6144 MiB, 5600 MiB free)\n" +
		"  CUDA1: NVIDIA GeForce RTX 4090 (Laptop) (24564 MiB, 23000 MiB free)\n"
	got := ParseListDevices(out)
	want := []Device{
		{Name: "CUDA0", Description: "NVIDIA GeForce RTX 3060 Laptop GPU", TotalMiB: 6144, FreeMiB: 5600},
		{Name: "CUDA1", Description: "NVIDIA GeForce RTX 4090 (Laptop)", TotalMiB: 24564, FreeMiB: 23000},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ParseListDevices\n got: %+v\nwant: %+v", got, want)
	}
}

func TestParseListDevicesNone(t *testing.T) {
	if got := ParseListDevices("Available devices:\n  (none)\n"); len(got) != 0 {
		t.Fatalf("got %+v for (none)", got)
	}
	if got := ParseListDevices(""); len(got) != 0 {
		t.Fatalf("got %+v for empty output", got)
	}
}

func TestHasNoGPUWarning(t *testing.T) {
	stderr := "warning: no usable GPU found, --gpu-layers option will be ignored\n" +
		"warning: one possible reason is that llama.cpp was compiled without GPU support\n"
	if !HasNoGPUWarning(stderr) {
		t.Fatalf("warning not detected")
	}
	if HasNoGPUWarning("srv  llama_server: listening on http://127.0.0.1:8080") {
		t.Fatalf("false positive")
	}
}

func TestEnvParallelSlots(t *testing.T) {
	p := gpuPlan()
	env, err := Env(p, lanSettings())
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := env["LLAMA_ARG_N_PARALLEL"]; ok {
		t.Fatal("a single slot must not set LLAMA_ARG_N_PARALLEL")
	}
	p.Parallel = 4
	env, err = Env(p, lanSettings())
	if err != nil {
		t.Fatal(err)
	}
	if env["LLAMA_ARG_N_PARALLEL"] != "4" {
		t.Fatalf("env = %v", env)
	}
}
