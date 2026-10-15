package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/client"
)

// 가짜 데몬이 돌려줄 응답 본문이다. 실제 데몬의 JSON 모양을 따른다.
const (
	statusReadyRunning = `{"version":"0.2.0","mode":"server",
"gpu":{"status":{"state":"ready","message":"1 usable GPU","present":true,"pci_count":1,"count":1,"usable_count":1,"usable_indexes":[0],"secure_boot":"disabled","driver_version":"595.91.07","gpus":[{"index":0,"name":"NVIDIA GeForce RTX 4090","compute_cap":"8.9","supported":true,"reason":"ok"}]},"status_error":"","open_module":"yes","secure_boot":"disabled","driver_version":"595.91.07",
"nvml":{"available":true,"nvml_version":"12.595","cuda_driver_version":"13.2","devices":[{"index":0,"name":"NVIDIA GeForce RTX 4090","arch":"ada","compute_cap":"8.9","memory_total_mib":24564,"memory_free_mib":23000,"memory_used_mib":1564,"temperature_c":45,"power_mw":120500,"util_gpu_percent":3,"util_mem_percent":1}]}},
"memory":{"total_mib":15912,"available_mib":10240},
"server":{"state":"running","model_id":"stories260K.gguf","host":"127.0.0.1","port":8080,"lan_exposed":false,"pid":4242,"since":"2026-10-11T09:00:00Z","offload":{"requested":"all","offloaded":33,"total":33,"verified":true},"warnings":[],"last_error":null,"plan":{"ngl":"all","ctx":4096,"cache_type_k":"f16","cache_type_v":"f16","flash_attn":false,"batch":2048,"visible_devices":[0],"load_mode":"mmap","fit":false,"cpu_only":false,"est_vram_mib":900,"est_ram_mib":0,"rationale":[],"warnings":[],"advice":[]}}}`

	statusNotChecked = `{"version":"0.2.0","mode":"server",
"gpu":{"status":null,"status_error":"GPU check has not run","open_module":"unknown","secure_boot":"unknown","driver_version":"","nvml":{"available":false,"reason":"no GPU prober is configured","devices":[]}},
"memory":null,
"server":{"state":"stopped","model_id":"","host":"","port":0,"lan_exposed":false,"pid":0,"since":"0001-01-01T00:00:00Z","offload":{"requested":"","offloaded":0,"total":0,"verified":false},"warnings":[],"plan":null}}`

	statusNoGPU = `{"version":"0.2.0","mode":"server",
"gpu":{"status":{"state":"no_gpu","message":"no NVIDIA GPU found","present":false,"pci_count":0,"count":0,"usable_count":0,"usable_indexes":[],"secure_boot":"disabled","driver_version":"","gpus":[]},"status_error":"","open_module":"unknown","secure_boot":"disabled","driver_version":"","nvml":{"available":false,"reason":"NVML library not found","devices":[]}},
"memory":{"total_mib":8000,"available_mib":4000},
"server":{"state":"stopped","model_id":"","host":"","port":0,"lan_exposed":false,"pid":0,"since":"0001-01-01T00:00:00Z","offload":{"requested":"","offloaded":0,"total":0,"verified":false},"warnings":[],"plan":null}}`

	gpuCheckTpl = `{"exit_code":%d,"status":{"state":%q,"message":"msg","present":true,"pci_count":1,"count":1,"usable_count":%d,"usable_indexes":[],"secure_boot":"disabled","driver_version":"","gpus":[]}}`

	modelsList = `{"default":"stories260K.gguf","models":[{"id":"stories260K.gguf","size_bytes":1200000,"repo":"ggml-org/tiny-llamas","revision":"main","sha256":"aa","license":"mit","architecture":"llama","name":"stories","file_type":"F16","block_count":5,"context_length":512,"embedding_length":64,"default":true,"incomplete":false}]}`

	remoteOne = `{"repo":"ggml-org/tiny-llamas","revision":"main","license":"mit","gated":false,"gate_mode":"false","files":[{"path":"stories260K.gguf","size":1200000,"sha256":"aa","lfs":true}]}`

	remoteTwo = `{"repo":"o/multi","revision":"main","license":"apache-2.0","gated":false,"gate_mode":"false","files":[{"path":"a-Q4.gguf","size":4000000,"sha256":"aa","lfs":true},{"path":"b-Q8.gguf","size":8000000,"sha256":"bb","lfs":true}]}`
)

// jobJSON 은 작업 상태 JSON 을 만든다.
func jobJSON(id, state string, done, total int64, file, errCode, errMsg, modelID string) string {
	b, _ := json.Marshal(map[string]any{
		"id": id, "state": state, "repo": "ggml-org/tiny-llamas", "file": file, "revision": "main",
		"bytes_done": done, "bytes_total": total, "model_id": modelID, "license": "mit",
		"error_code": errCode, "error": errMsg,
	})
	return string(b)
}

// planJSON 은 계획 조회와 시작 응답에 쓰는 계획 JSON 을 만든다.
func planJSON(rationale, warnings, advice []string) string {
	b, _ := json.Marshal(map[string]any{
		"ngl": "auto", "ctx": 4096, "cache_type_k": "f16", "cache_type_v": "f16", "flash_attn": false,
		"batch": 2048, "visible_devices": []int{0}, "load_mode": "mmap", "fit": false, "cpu_only": false,
		"est_vram_mib": 900, "est_ram_mib": 0,
		"rationale": nonNil(rationale), "warnings": nonNil(warnings), "advice": nonNil(advice),
	})
	return string(b)
}

func nonNil(v []string) []string {
	if v == nil {
		return []string{}
	}
	return v
}

// startJSON 은 POST /v1/server/start 응답이다. 데몬은 서버 상태 객체를 평평하게 돌려준다.
func startJSON(state string) string {
	return fmt.Sprintf(`{"state":%q,"model_id":"stories260K.gguf","host":"127.0.0.1","port":8080,"lan_exposed":false,"pid":0,"since":"2026-10-11T09:00:00Z","offload":{"requested":"auto","offloaded":0,"total":0,"verified":false},"warnings":[],"plan":%s}`, state, planJSON(nil, nil, nil))
}

// serverStatusJSON 은 GET /v1/status 응답 중 서버 상태만 바꾼 것이다.
func serverStatusJSON(state string, lastErrCode, lastErrMsg string, warnings []string) string {
	var lastErr any
	if lastErrCode != "" {
		lastErr = map[string]string{"code": lastErrCode, "message": lastErrMsg}
	}
	b, _ := json.Marshal(map[string]any{
		"version": "0.2.0", "mode": "server",
		"gpu":    map[string]any{"status": nil, "status_error": "", "open_module": "yes", "secure_boot": "disabled", "driver_version": "", "nvml": map[string]any{"available": true, "devices": []any{}}},
		"memory": map[string]any{"total_mib": 16000, "available_mib": 9000},
		"server": map[string]any{
			"state": state, "model_id": "stories260K.gguf", "host": "127.0.0.1", "port": 8080,
			"lan_exposed": false, "pid": 99, "since": "2026-10-11T09:00:00Z",
			"offload":  map[string]any{"requested": "auto", "offloaded": 0, "total": 0, "verified": false},
			"warnings": nonNil(warnings), "last_error": lastErr, "plan": nil,
		},
	})
	return string(b)
}

// ---- status ----

func TestStatusWithGPUAndRunningServer(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusReadyRunning)
	res := runCLI(t, f, "status")
	wantCode(t, res, exitOK)
	wantContains(t, "status", res.stdout,
		"version:", "0.2.0", "mode:", "server",
		"state:", "ready",
		"NVIDIA GeForce RTX 4090", "ada", "8.9",
		"1.5 GiB / 24.0 GiB", "45 C", "120.5 W",
		"driver:", "595.91.07",
		"open module:", "yes",
		"NVML 12.595", "CUDA driver 13.2",
		"total:", "15.5 GiB", "available:", "10.0 GiB",
		"model:", "stories260K.gguf",
		"127.0.0.1:8080", "LAN exposed:", "no",
		"requested all, offloaded 33 of 33 layers, verified yes",
	)
}

func TestStatusWithoutGPUCheck(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNotChecked)
	res := runCLI(t, f, "status")
	wantCode(t, res, exitOK)
	wantContains(t, "status not checked", res.stdout,
		"not checked", "GPU check has not run", "run 'opereta gpu check'",
		"unavailable: no GPU prober is configured",
		"total:", "unknown",
		"stopped", "not measured",
	)
}

func TestStatusWithoutGPU(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNoGPU)
	res := runCLI(t, f, "status")
	wantCode(t, res, exitOK)
	wantContains(t, "status no gpu", res.stdout, "no_gpu", "no NVIDIA GPU found", "usable GPUs:", "0", "NVML library not found")
	if strings.Contains(res.stdout, "VRAM USED") {
		t.Fatalf("device table printed although no device is reported:\n%s", res.stdout)
	}
}

func TestStatusJSONIsIndentedAndKeepsAPIBody(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNoGPU)
	res := runCLI(t, f, "--json", "status")
	wantCode(t, res, exitOK)
	var v map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, res.stdout)
	}
	if v["version"] != "0.2.0" {
		t.Fatalf("version = %v", v["version"])
	}
	if !strings.Contains(res.stdout, "\n  \"version\": \"0.2.0\"") {
		t.Fatalf("output is not indented with two spaces:\n%s", res.stdout)
	}
}

// --json 은 원래 명령 앞에 두지만 뒤에 붙여도 같은 결과여야 한다(`opereta boot status --json`).
func TestTrailingJSONFlagIsAccepted(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNoGPU)
	res := runCLI(t, f, "status", "--json")
	wantCode(t, res, exitOK)
	var v map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &v); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, res.stdout)
	}
}

func TestStatusUnreachableExit3(t *testing.T) {
	res := runCLI(t, nil, "--socket", filepath.Join(t.TempDir(), "absent.sock"), "status")
	wantCode(t, res, exitUnreachable)
	if !strings.Contains(res.stderr, "operetad is not running (systemctl status operetad)") {
		t.Fatalf("stderr = %q", res.stderr)
	}
}

func TestSocketFromEnvironment(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNoGPU)
	res := runCLIWith(t, nil, cliOpts{env: map[string]string{"OPERETA_SOCKET": f.sock}}, "status")
	wantCode(t, res, exitOK)
}

func TestFlagSocketBeatsEnvironment(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNoGPU)
	res := runCLIWith(t, nil, cliOpts{env: map[string]string{"OPERETA_SOCKET": filepath.Join(t.TempDir(), "x.sock")}},
		"--socket", f.sock, "status")
	wantCode(t, res, exitOK)
}

// ---- gpu check ----

func TestGPUCheckGuidanceAndExitCodes(t *testing.T) {
	cases := []struct {
		state  string
		usable int
		exit   int
		want   string
	}{
		{"ready", 1, exitOK, "GPU is ready"},
		{"no_gpu", 0, exitOK, "run the model on the CPU"},
		{"secure_boot", 0, exitFailure, "Turn off Secure Boot in the UEFI settings, then reboot."},
		{"module_unloaded", 0, exitFailure, "journalctl -k"},
		{"unsupported_gpu", 0, exitFailure, "GeForce RTX 30, 40, or 50 series"},
		{"version_mismatch", 0, exitFailure, "Restore the same version, then reboot."},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			f := newFakeDaemon(t)
			code := 0
			if tc.exit != exitOK {
				code = 2
			}
			f.on("POST /v1/gpu/check", 200, fmt.Sprintf(gpuCheckTpl, code, tc.state, tc.usable))
			res := runCLI(t, f, "gpu", "check")
			wantCode(t, res, tc.exit)
			wantContains(t, tc.state, res.stdout, "GPU check: "+tc.state, tc.want)
		})
	}
}

func TestGPUCheckJSON(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/gpu/check", 200, fmt.Sprintf(gpuCheckTpl, 1, "no_gpu", 0))
	res := runCLI(t, f, "--json", "gpu", "check")
	wantCode(t, res, exitOK)
	if !strings.Contains(res.stdout, `"exit_code": 1`) {
		t.Fatalf("stdout = %s", res.stdout)
	}
}

// ---- model list, rm, default, search, files ----

func TestModelListShowsDefault(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/models", 200, modelsList)
	res := runCLI(t, f, "model", "list")
	wantCode(t, res, exitOK)
	wantContains(t, "model list", res.stdout, "stories260K.gguf", "1.1 MiB", "yes", "F16", "512", "ok")
}

func TestModelListEmpty(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/models", 200, `{"default":"","models":[]}`)
	res := runCLI(t, f, "model", "list")
	wantCode(t, res, exitOK)
	wantContains(t, "empty", res.stdout, "no models installed")
}

func TestModelRemoveAndDefault(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("DELETE /v1/models/a.gguf", 200, `{"id":"a.gguf","deleted":true}`)
	f.on("PUT /v1/models/default", 200, `{"default":"a.gguf"}`)
	res := runCLI(t, f, "model", "rm", "a.gguf")
	wantCode(t, res, exitOK)
	wantContains(t, "rm", res.stdout, "removed model a.gguf")
	res = runCLI(t, f, "model", "default", "a.gguf")
	wantCode(t, res, exitOK)
	wantContains(t, "default", res.stdout, "default model: a.gguf")
	if got := f.bodiesOf("PUT /v1/models/default"); len(got) != 1 || got[0] != `{"id":"a.gguf"}` {
		t.Fatalf("default body = %v", got)
	}
}

func TestModelRemoveInUseIsFailure(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("DELETE /v1/models/a.gguf", 409, `{"error":{"code":"model_in_use","message":"model \"a.gguf\" is used by the running server; stop the server first"}}`)
	res := runCLI(t, f, "model", "rm", "a.gguf")
	wantCode(t, res, exitFailure)
	wantContains(t, "rm in use", res.stderr, "model_in_use", "stop the server first")
}

func TestModelSearchTable(t *testing.T) {
	f := newFakeDaemon(t)
	f.onFunc("GET /v1/models/search", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") != "llama 3" || r.URL.Query().Get("limit") != "5" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_query","message":"bad"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"results":[{"id":"o/llama-3-gguf","downloads":1200,"gated":true,"license":"llama3","architecture":"llama","context_length":8192}]}`))
	})
	res := runCLI(t, f, "model", "search", "llama", "3", "--limit", "5")
	wantCode(t, res, exitOK)
	wantContains(t, "search", res.stdout, "o/llama-3-gguf", "1200", "yes", "llama3")
}

func TestModelSearchLimitOutOfRangeIsUsage(t *testing.T) {
	res := runCLI(t, nil, "model", "search", "x", "--limit", "500")
	wantCode(t, res, exitUsage)
}

func TestModelFilesListsGGUF(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/models/remote", 200, remoteTwo)
	res := runCLI(t, f, "model", "files", "o/multi")
	wantCode(t, res, exitOK)
	wantContains(t, "files", res.stdout, "repository:", "o/multi", "apache-2.0", "a-Q4.gguf", "b-Q8.gguf", "SHA256")
}

// ---- model pull ----

// pullSetup 은 저장소 조회와 작업 생성, 작업 상태 순서를 등록한다.
func pullSetup(f *fakeDaemon, remote string, jobs ...reply) {
	f.on("GET /v1/models/remote", 200, remote)
	f.on("POST /v1/models/pull", 202, jobs[0].body)
	if len(jobs) > 1 {
		f.seq("GET /v1/jobs/j1", jobs[1:]...)
	}
}

func TestModelPullNonTTYProgressAndDone(t *testing.T) {
	f := newFakeDaemon(t)
	const total = 1200000
	pullSetup(f, remoteOne,
		reply{202, jobJSON("j1", "queued", 0, total, "stories260K.gguf", "", "", "")},
		reply{200, jobJSON("j1", "running", 300000, total, "stories260K.gguf", "", "", "")},
		reply{200, jobJSON("j1", "running", 650000, total, "stories260K.gguf", "", "", "")},
		reply{200, jobJSON("j1", "done", total, total, "stories260K.gguf", "", "", "stories260K.gguf")},
	)
	res := runCLI(t, f, "model", "pull", "ggml-org/tiny-llamas", "--file", "stories260K.gguf")
	wantCode(t, res, exitOK)
	wantContains(t, "pull", res.stdout,
		"repository:", "ggml-org/tiny-llamas", "license:", "mit", "gated:",
		"downloading stories260K.gguf: 0 B of 1.1 MiB (0%)",
		"(25%)", "(54%)", "(100%)",
		"installed model: stories260K.gguf",
		"opereta model default stories260K.gguf",
	)
	if strings.Contains(res.stdout, "\r") {
		t.Fatalf("non-terminal output must not use carriage returns")
	}
	body := f.bodiesOf("POST /v1/models/pull")
	if len(body) != 1 || !strings.Contains(body[0], `"repo":"ggml-org/tiny-llamas"`) || !strings.Contains(body[0], `"file":"stories260K.gguf"`) {
		t.Fatalf("pull body = %v", body)
	}
}

func TestModelPullNonTTYPrintsOneLinePerTenPercent(t *testing.T) {
	f := newFakeDaemon(t)
	const total = 1000
	var replies []reply
	replies = append(replies, reply{202, jobJSON("j1", "queued", 0, total, "m.gguf", "", "", "")})
	for done := 100; done <= 1000; done += 100 {
		replies = append(replies, reply{200, jobJSON("j1", "running", int64(done), total, "m.gguf", "", "", "")})
	}
	replies = append(replies, reply{200, jobJSON("j1", "done", total, total, "m.gguf", "", "", "m.gguf")})
	pullSetup(f, remoteOne, replies...)
	res := runCLI(t, f, "model", "pull", "o/n")
	wantCode(t, res, exitOK)
	lines := 0
	for _, line := range strings.Split(res.stdout, "\n") {
		if strings.HasPrefix(line, "downloading ") {
			lines++
		}
	}
	// 0%, 10%, ..., 100% 까지 11 줄이 나와야 한다.
	if lines != 11 {
		t.Fatalf("progress lines = %d, want 11\n%s", lines, res.stdout)
	}
}

func TestModelPullTTYUsesCarriageReturn(t *testing.T) {
	f := newFakeDaemon(t)
	pullSetup(f, remoteOne,
		reply{202, jobJSON("j1", "queued", 0, 100, "stories260K.gguf", "", "", "")},
		reply{200, jobJSON("j1", "done", 100, 100, "stories260K.gguf", "", "", "stories260K.gguf")},
	)
	res := runCLIWith(t, f, cliOpts{outTTY: true}, "model", "pull", "o/n")
	wantCode(t, res, exitOK)
	if !strings.Contains(res.stdout, "\rdownloading stories260K.gguf") {
		t.Fatalf("terminal progress must start with a carriage return:\n%q", res.stdout)
	}
	if !strings.HasSuffix(res.stdout, "installed model: stories260K.gguf\n"+"make it the default with: opereta model default stories260K.gguf\n") {
		t.Fatalf("unexpected tail:\n%q", res.stdout)
	}
}

func TestModelPullNoWaitReturnsAtOnce(t *testing.T) {
	f := newFakeDaemon(t)
	pullSetup(f, remoteOne, reply{202, jobJSON("j1", "queued", 0, 100, "stories260K.gguf", "", "", "")})
	res := runCLI(t, f, "model", "pull", "o/n", "--no-wait")
	wantCode(t, res, exitOK)
	wantContains(t, "no-wait", res.stdout, "job j1: queued", "download started")
	if f.hitsContain("GET /v1/jobs/j1") {
		t.Fatalf("--no-wait must not poll the job")
	}
}

func TestModelPullFailureCodesHaveGuidance(t *testing.T) {
	cases := []struct {
		name string
		code string
		want string
	}{
		{"unauthorized", "unauthorized", "repository not found, or it is private (set a token: opereta config set hf-token)"},
		{"gated", "gated", "accept its terms, then set a token: opereta config set hf-token"},
		{"checksum", "checksum_mismatch", "does not match the SHA-256"},
	}
	for _, tc := range cases {
		t.Run(tc.name+"/api", func(t *testing.T) {
			f := newFakeDaemon(t)
			f.on("GET /v1/models/remote", 200, remoteOne)
			f.on("POST /v1/models/pull", 403, fmt.Sprintf(`{"error":{"code":%q,"message":"x"}}`, tc.code))
			res := runCLI(t, f, "model", "pull", "o/n")
			wantCode(t, res, exitFailure)
			wantContains(t, tc.name, res.stderr, tc.want, "(code: "+tc.code+")")
		})
		t.Run(tc.name+"/job", func(t *testing.T) {
			f := newFakeDaemon(t)
			pullSetup(f, remoteOne,
				reply{202, jobJSON("j1", "queued", 0, 100, "stories260K.gguf", "", "", "")},
				reply{200, jobJSON("j1", "failed", 0, 100, "stories260K.gguf", tc.code, "failure detail", "")},
			)
			res := runCLI(t, f, "model", "pull", "o/n")
			wantCode(t, res, exitFailure)
			wantContains(t, tc.name, res.stderr, "download failed: failure detail (code: "+tc.code+")", tc.want)
		})
	}
}

func TestModelPullInsufficientSpaceShowsNeed(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/models/remote", 200, remoteOne)
	f.on("POST /v1/models/pull", 507, `{"error":{"code":"insufficient_space","message":"not enough free disk space"}}`)
	res := runCLI(t, f, "model", "pull", "o/n")
	wantCode(t, res, exitFailure)
	wantContains(t, "space", res.stderr, "not enough free disk space: the model needs about 1.1 MiB", "(code: insufficient_space)")
}

func TestModelPullAmbiguousFileListsCandidates(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/models/remote", 200, remoteTwo)
	f.on("POST /v1/models/pull", 400, `{"error":{"code":"ambiguous_file","message":"repository has more than one GGUF file; choose one with file: a-Q4.gguf, b-Q8.gguf"}}`)
	res := runCLI(t, f, "model", "pull", "o/multi")
	wantCode(t, res, exitFailure)
	wantContains(t, "ambiguous", res.stderr, "more than one GGUF file", "--file", "a-Q4.gguf", "b-Q8.gguf", "FILE", "SIZE")
}

func TestModelPullBadArgumentsIsUsage(t *testing.T) {
	res := runCLI(t, nil, "model", "pull")
	wantCode(t, res, exitUsage)
}

func TestModelPullInterruptedKeepsJob(t *testing.T) {
	f := newFakeDaemon(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pullSetup(f, remoteOne, reply{202, jobJSON("j1", "queued", 0, 100, "stories260K.gguf", "", "", "")})
	f.onFunc("GET /v1/jobs/j1", func(w http.ResponseWriter, _ *http.Request) {
		cancel() // 사용자가 Ctrl-C 를 누른 것과 같다.
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(jobJSON("j1", "running", 10, 100, "stories260K.gguf", "", "", "")))
	})
	res := runCLIWith(t, f, cliOpts{cancelCtx: ctx}, "model", "pull", "o/n")
	wantCode(t, res, exitOK)
	wantContains(t, "interrupt", res.stdout, "Interrupted.", "The job keeps running in operetad.", "opereta model pull")
}

// ---- serve ----

func TestServePlanShowsRationaleWarningsAndAdvice(t *testing.T) {
	f := newFakeDaemon(t)
	plan := fmt.Sprintf(`{"model_id":"stories260K.gguf","plan":%s,"input":{"model_bytes":1200000,"layers":5,"ctx_train":512,"embedding":64,"gpus":[{"index":0,"free_mib":23000,"total_mib":24564}],"ram_avail_mib":10240,"ram_total_mib":15912,"desktop_mode":false},"gpu":{"status_checked":true,"state":"ready","message":"1 usable GPU","usable_indexes":[0],"nvml_available":true}}`,
		planJSON([]string{"all layers fit in VRAM"}, []string{"context was reduced to 4096"}, []string{"close other GPU apps"}))
	f.onFunc("GET /v1/server/plan", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("ngl") != "20" || r.URL.Query().Get("model") != "stories260K.gguf" {
			w.WriteHeader(400)
			_, _ = w.Write([]byte(`{"error":{"code":"invalid_query","message":"bad"}}`))
			return
		}
		_, _ = w.Write([]byte(plan))
	})
	res := runCLI(t, f, "serve", "plan", "stories260K.gguf", "--ngl", "20")
	wantCode(t, res, exitOK)
	wantContains(t, "plan", res.stdout,
		"model:", "stories260K.gguf",
		"GPU layers:", "auto",
		"KV cache:", "K f16, V f16",
		"rationale:", "- all layers fit in VRAM",
		"warnings:", "- WARNING: context was reduced to 4096",
		"advice:", "- close other GPU apps",
		"GPU check:", "ready",
	)
}

func TestServePlanWithoutModelFails(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/server/plan", 404, `{"error":{"code":"no_model","message":"no model was given and no default model is set"}}`)
	res := runCLI(t, f, "serve", "plan")
	wantCode(t, res, exitFailure)
	wantContains(t, "no model", res.stderr, "no default model is set", "(no_model)")
}

func TestServeStartRunning(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/start", 202, startJSON("starting"))
	f.seq("GET /v1/status",
		reply{200, serverStatusJSON("starting", "", "", nil)},
		reply{200, serverStatusJSON("starting", "", "", nil)},
		reply{200, serverStatusJSON("running", "", "", nil)},
	)
	res := runCLI(t, f, "serve", "start")
	wantCode(t, res, exitOK)
	wantContains(t, "start", res.stdout, "GPU layers:", "rationale:", "server state: starting", "server state: running", "server is running with model stories260K.gguf at 127.0.0.1:8080")
	body := f.bodiesOf("POST /v1/server/start")
	if len(body) != 1 || !strings.Contains(body[0], `"wait":false`) {
		t.Fatalf("start must not wait on the daemon: %v", body)
	}
}

func TestServeStartPassesOverrides(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/start", 202, startJSON("starting"))
	f.seq("GET /v1/status", reply{200, serverStatusJSON("running", "", "", nil)})
	res := runCLI(t, f, "serve", "start", "--ngl", "20", "--ctx", "8192", "stories260K.gguf", "--cache-type", "q8_0")
	wantCode(t, res, exitOK)
	body := f.bodiesOf("POST /v1/server/start")
	want := `{"model":"stories260K.gguf","overrides":{"ngl":"20","ctx":8192,"cache_type":"q8_0"},"wait":false}`
	if len(body) != 1 || body[0] != want {
		t.Fatalf("start body = %v, want %s", body, want)
	}
}

func TestServeStartCPUFallbackShowsWarning(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/start", 202, startJSON("starting"))
	f.seq("GET /v1/status", reply{200, serverStatusJSON("running_cpu_fallback", "", "", []string{"no usable GPU; layers stay on the CPU"})})
	res := runCLI(t, f, "serve", "start")
	wantCode(t, res, exitOK)
	wantContains(t, "cpu", res.stdout,
		"WARNING: the server is running on the CPU. The GPU is not used for inference.",
		"WARNING: no usable GPU; layers stay on the CPU")
}

func TestServeStartGPUOffloadFailed(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/start", 202, startJSON("starting"))
	f.seq("GET /v1/status", reply{200, serverStatusJSON("failed", "gpu_offload_failed", "offloaded 0/33 layers to GPU", nil)})
	res := runCLI(t, f, "serve", "start")
	wantCode(t, res, exitFailure)
	wantContains(t, "offload failed", res.stderr,
		"server failed: offloaded 0/33 layers to GPU (code: gpu_offload_failed)",
		"a GPU was detected, but inference would not run on the GPU, so the server was stopped")
}

func TestServeStartNoWaitPrintsPlanAndReturns(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/start", 202, startJSON("starting"))
	res := runCLI(t, f, "serve", "start", "--no-wait")
	wantCode(t, res, exitOK)
	wantContains(t, "no-wait", res.stdout, "server state: starting", "check it with: opereta status")
	if f.hitsContain("GET /v1/status") {
		t.Fatalf("--no-wait must not poll")
	}
}

func TestServeStopAndLogs(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/stop", 200, `{"state":"stopped"}`)
	f.onFunc("GET /v1/server/logs", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("lines") != "20" {
			w.WriteHeader(400)
			return
		}
		_, _ = w.Write([]byte(`{"lines":20,"log":"line one\nline two\n"}`))
	})
	res := runCLI(t, f, "serve", "stop")
	wantCode(t, res, exitOK)
	wantContains(t, "stop", res.stdout, "server state: stopped")
	res = runCLI(t, f, "serve", "logs", "-n", "20")
	wantCode(t, res, exitOK)
	if res.stdout != "line one\nline two\n" {
		t.Fatalf("logs = %q", res.stdout)
	}
}

func TestServeLogsRangeIsUsage(t *testing.T) {
	res := runCLI(t, nil, "serve", "logs", "-n", "0")
	wantCode(t, res, exitUsage)
}

// ---- config ----

func TestConfigSetLANWithoutKeyExplains(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("PUT /v1/config", 409, `{"error":{"code":"api_key_required","message":"LAN exposure requires at least one API key; create a key first"}}`)
	res := runCLI(t, f, "config", "set", "lan", "on")
	wantCode(t, res, exitFailure)
	wantContains(t, "lan", res.stderr, "LAN exposure needs an API key. Create one first: opereta apikey create")
	if got := f.bodiesOf("PUT /v1/config"); len(got) != 1 || got[0] != `{"lan_exposed":true}` {
		t.Fatalf("config body = %v", got)
	}
}

func TestConfigSetPortAndRestartNotice(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("PUT /v1/config", 200, `{"config":{"port":9090,"lan_exposed":false,"autostart":true,"models_dir":"/var/lib/opereta/models","hf_token":"","overrides":{}},"restart_required":true}`)
	res := runCLI(t, f, "config", "set", "port", "9090")
	wantCode(t, res, exitOK)
	wantContains(t, "port", res.stdout, "port set to 9090", "restart the server")
	if got := f.bodiesOf("PUT /v1/config"); len(got) != 1 || got[0] != `{"port":9090}` {
		t.Fatalf("config body = %v", got)
	}
}

func TestConfigSetPortRangeIsUsage(t *testing.T) {
	res := runCLI(t, nil, "config", "set", "port", "70000")
	wantCode(t, res, exitUsage)
}

func TestConfigSetAutostart(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("PUT /v1/config", 200, `{"config":{"port":8080,"lan_exposed":false,"autostart":false,"models_dir":"/x","hf_token":"","overrides":{}},"restart_required":false}`)
	res := runCLI(t, f, "config", "set", "autostart", "off")
	wantCode(t, res, exitOK)
	if got := f.bodiesOf("PUT /v1/config"); len(got) != 1 || got[0] != `{"autostart":false}` {
		t.Fatalf("config body = %v", got)
	}
}

func TestConfigSetHFTokenReadsStdinOnly(t *testing.T) {
	const token = "hf_SECRET_token_1234"
	f := newFakeDaemon(t)
	f.on("PUT /v1/config", 200, `{"config":{"port":8080,"lan_exposed":false,"autostart":true,"models_dir":"/x","hf_token":"<redacted>","overrides":{}},"restart_required":true}`)
	res := runCLIWith(t, f, cliOpts{stdin: token + "\n"}, "config", "set", "hf-token")
	wantCode(t, res, exitOK)
	if strings.Contains(res.stdout, token) || strings.Contains(res.stderr, token) {
		t.Fatalf("token was printed:\n%s\n%s", res.stdout, res.stderr)
	}
	wantContains(t, "hf-token", res.stdout, "hf-token saved", "restart the daemon", "sudo systemctl restart operetad")
	if strings.Contains(res.stdout, "opereta serve stop") {
		t.Fatal("HF token change must restart the daemon, not only llama-server")
	}
	got := f.bodiesOf("PUT /v1/config")
	if len(got) != 1 || got[0] != `{"hf_token":"`+token+`"}` {
		t.Fatalf("config body = %v", got)
	}
}

func TestConfigSetHFTokenArgumentRejected(t *testing.T) {
	const token = "hf_ARGUMENT_token"
	f := newFakeDaemon(t)
	res := runCLI(t, f, "config", "set", "hf-token", token)
	wantCode(t, res, exitUsage)
	if strings.Contains(res.stdout, token) || strings.Contains(res.stderr, token) {
		t.Fatalf("token from argument was echoed")
	}
	if len(f.bodiesOf("PUT /v1/config")) != 0 {
		t.Fatalf("token argument must not reach the daemon")
	}
}

func TestConfigSetHFTokenEmptyStdinIsUsage(t *testing.T) {
	res := runCLIWith(t, nil, cliOpts{stdin: "\n"}, "config", "set", "hf-token")
	wantCode(t, res, exitUsage)
}

func TestConfigShowHidesToken(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/config", 200, `{"port":8080,"lan_exposed":true,"autostart":true,"models_dir":"/var/lib/opereta/models","hf_token":"<redacted>","overrides":{"ngl":"all","ctx":4096}}`)
	res := runCLI(t, f, "config", "show")
	wantCode(t, res, exitOK)
	wantContains(t, "config", res.stdout, "port:", "8080", "lan:", "yes", "hf-token:", "set (hidden)", "ngl=all ctx=4096")
	if strings.Contains(res.stdout, "redacted") {
		t.Fatalf("redaction marker should not be shown as a value:\n%s", res.stdout)
	}
}

// ---- api keys ----

func TestAPIKeyCreatePrintsKeyOnce(t *testing.T) {
	const key = "llk_0123456789abcdef"
	f := newFakeDaemon(t)
	f.on("POST /v1/apikeys", 201, fmt.Sprintf(`{"id":"k1","label":"laptop","prefix":"llk_0123","created":"2026-10-11T09:00:00Z","key":%q,"note":"the key is shown only once; store it now","restart_required":false}`, key))
	res := runCLI(t, f, "apikey", "create", "laptop")
	wantCode(t, res, exitOK)
	wantContains(t, "create", res.stdout, key, "shown only once", "label:", "laptop")
	if strings.Count(res.stdout, key) != 1 || strings.Contains(res.stdout, "restart the server") {
		t.Fatal("stopped-server key creation did not show the key once without a restart notice")
	}
	if got := f.bodiesOf("POST /v1/apikeys"); len(got) != 1 || got[0] != `{"label":"laptop"}` {
		t.Fatalf("apikey body = %v", got)
	}
}

func TestAPIKeyCreateWhileRunningShowsRestartInstruction(t *testing.T) {
	const key = "opr_test-created-key"
	f := newFakeDaemon(t)
	f.on("POST /v1/apikeys", 201, fmt.Sprintf(`{"id":"k1","label":"laptop","prefix":"opr_test","created":"2026-10-11T09:00:00Z","key":%q,"note":"the key is shown only once; store it now","restart_required":true}`, key))
	res := runCLI(t, f, "apikey", "create", "laptop")
	wantCode(t, res, exitOK)
	wantContains(t, "create", res.stdout, "API key created.", key, "shown only once", "restart the server for the new API key to take effect", "opereta serve stop, then opereta serve start")
	if strings.Count(res.stdout, key) != 1 {
		t.Fatal("running-server key creation did not show the key exactly once")
	}
}

func TestAPIKeyCreateJSONKeepsRestartContract(t *testing.T) {
	const key = "opr_json-created-key"
	f := newFakeDaemon(t)
	f.on("POST /v1/apikeys", 201, fmt.Sprintf(`{"id":"k1","label":"laptop","prefix":"opr_json","created":"2026-10-11T09:00:00Z","key":%q,"note":"the key is shown only once; store it now","restart_required":true}`, key))
	res := runCLI(t, f, "--json", "apikey", "create", "laptop")
	wantCode(t, res, exitOK)
	var response map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &response); err != nil {
		t.Fatal("JSON key response did not decode:", err)
	}
	if response["restart_required"] != true || strings.Count(res.stdout, key) != 1 {
		t.Fatal("JSON key response did not preserve restart_required and one-time plaintext")
	}
}

func TestAPIKeyCreateFailureReported(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/apikeys", 500, `{"error":{"code":"key_file_failed","message":"chown failed"}}`)
	res := runCLI(t, f, "apikey", "create")
	wantCode(t, res, exitFailure)
	wantContains(t, "create failed", res.stderr, "chown failed", "(key_file_failed)")
}

func TestAPIKeyListHasNoPlaintext(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/apikeys", 200, `{"keys":[{"id":"k1","label":"laptop","created":"2026-10-11T09:00:00Z","prefix":"llk_0123"}]}`)
	res := runCLI(t, f, "apikey", "list")
	wantCode(t, res, exitOK)
	wantContains(t, "list", res.stdout, "k1", "laptop", "llk_0123")
}

func TestAPIKeyRemove(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("DELETE /v1/apikeys/k1", 200, `{"id":"k1","deleted":true,"restart_required":true}`)
	res := runCLI(t, f, "apikey", "rm", "k1")
	wantCode(t, res, exitOK)
	wantContains(t, "rm", res.stdout, "deleted API key k1", "restart the server")
}

// ---- user accounts ----

func TestUserAddWithPasswordStdin(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/users", 201, `{"name":"alice","admin":true,"created":"2026-10-12T00:00:00Z"}`)
	res := runCLIWith(t, f, cliOpts{stdin: "correct-horse-9\n"}, "user", "add", "alice", "--admin", "--password-stdin")
	wantCode(t, res, exitOK)
	wantContains(t, "user add", res.stdout, "created account alice", "administrator (sudo)")
	if strings.Contains(res.stdout+res.stderr, "correct-horse") {
		t.Fatal("the password must never be printed")
	}
	bodies := f.bodiesOf("POST /v1/users")
	if len(bodies) != 1 || bodies[0] != `{"name":"alice","password":"correct-horse-9","admin":true}` {
		t.Fatalf("body = %v", bodies)
	}
}

func TestUserAddPromptsTwiceAndChecksMatch(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/users", 201, `{"name":"bob","admin":false,"created":"2026-10-12T00:00:00Z"}`)
	var prompts []string
	answers := []string{"correct-horse-9", "correct-horse-9"}
	res := runCLIWith(t, f, cliOpts{stdinTTY: true, readPassword: func(p string) (string, error) {
		prompts = append(prompts, p)
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}}, "user", "add", "bob")
	wantCode(t, res, exitOK)
	if len(prompts) != 2 {
		t.Fatalf("prompts = %v", prompts)
	}
	wantContains(t, "user add", res.stdout, "created account bob", "(standard)")
}

func TestUserAddRejectsMismatchedPasswords(t *testing.T) {
	f := newFakeDaemon(t)
	answers := []string{"correct-horse-9", "different-pass-1"}
	res := runCLIWith(t, f, cliOpts{stdinTTY: true, readPassword: func(string) (string, error) {
		a := answers[0]
		answers = answers[1:]
		return a, nil
	}}, "user", "add", "bob")
	wantCode(t, res, exitFailure)
	wantContains(t, "mismatch", res.stderr, "do not match")
	if f.hitsContain("POST /v1/users") {
		t.Fatal("no request may be sent when the passwords differ")
	}
}

func TestUserAddWithoutTerminalNeedsPasswordStdin(t *testing.T) {
	f := newFakeDaemon(t)
	res := runCLIWith(t, f, cliOpts{stdin: "pw\n"}, "user", "add", "bob")
	wantCode(t, res, exitUsage)
	wantContains(t, "usage", res.stderr, "--password-stdin")
}

func TestUserAddReportsDaemonError(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/users", 409, `{"error":{"code":"user_exists","message":"the account already exists"}}`)
	res := runCLIWith(t, f, cliOpts{stdin: "correct-horse-9\n"}, "user", "add", "alice", "--password-stdin")
	wantCode(t, res, exitFailure)
	wantContains(t, "exists", res.stderr, "already exists", "(user_exists)")
}

func TestUserListAndRemove(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/users", 200, `{"users":[{"name":"alice","admin":true,"created":"2026-10-12T00:00:00Z"}]}`)
	res := runCLI(t, f, "user", "list")
	wantCode(t, res, exitOK)
	wantContains(t, "list", res.stdout, "alice", "yes", "NAME")
	f.on("DELETE /v1/users/alice", 200, `{"name":"alice","deleted":true,"home_kept":true}`)
	res = runCLI(t, f, "user", "rm", "alice")
	wantCode(t, res, exitOK)
	wantContains(t, "rm", res.stdout, "deleted account alice", "home directory was kept")
}

func TestSSHShowAndChange(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/system/ssh", 200, `{"enabled":false,"installed":true,"active":false,"password_auth":"no","login_users":[],"port":22}`)
	res := runCLI(t, f, "ssh")
	wantCode(t, res, exitOK)
	wantContains(t, "ssh", res.stdout, "ssh:", "no", "installed:", "yes", "password login:", "no")
	f.on("PUT /v1/system/ssh", 200, `{"enabled":true,"installed":true,"active":true,"password_auth":"yes","login_users":["alice"],"port":22,"warning":"SSH password login is allowed only for these accounts: alice"}`)
	res = runCLI(t, f, "ssh", "on")
	wantCode(t, res, exitOK)
	wantContains(t, "ssh on", res.stdout, "login accounts:", "alice", "note:")
	if got := f.bodiesOf("PUT /v1/system/ssh"); len(got) != 1 || got[0] != `{"enabled":true}` {
		t.Fatalf("body = %v", got)
	}
	res = runCLI(t, f, "ssh", "maybe")
	wantCode(t, res, exitUsage)
}

// ---- mode, update, report, chat, version, help ----

func TestModeShowAndSet(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/mode", 200, `{"mode":"server"}`)
	f.on("POST /v1/mode", 200, `{"mode":"desktop"}`)
	res := runCLI(t, f, "mode")
	wantCode(t, res, exitOK)
	wantContains(t, "mode", res.stdout, "mode:", "server")
	res = runCLI(t, f, "mode", "desktop")
	wantCode(t, res, exitOK)
	wantContains(t, "mode set", res.stdout, "mode set to desktop")
	if got := f.bodiesOf("POST /v1/mode"); len(got) != 1 || got[0] != `{"mode":"desktop"}` {
		t.Fatalf("mode body = %v", got)
	}
}

func TestModeDesktopNotInstalled(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/mode", 409, `{"error":{"code":"desktop_not_installed","message":"desktop is not installed"}}`)
	res := runCLI(t, f, "mode", "desktop")
	wantCode(t, res, exitFailure)
	wantContains(t, "desktop", res.stderr, "desktop mode is not installed on this system")
}

func TestModeInvalidArgumentIsUsage(t *testing.T) {
	res := runCLI(t, nil, "mode", "turbo")
	wantCode(t, res, exitUsage)
}

// updateInfoJSON 은 GET /v1/update 의 가짜 응답이다. 시각은 호출 시점이라 CLI 의 "이번 요청 이후" 판정을 통과한다.
func updateInfoJSON(running bool, phase, extra string) string {
	now := time.Now().UTC().Format(time.RFC3339)
	return fmt.Sprintf(`{"available":true,"live":false,"running":%v,"running_kernel":"6.12.111+deb13-amd64","state":{"phase":%q,"started":%q,"reboot_required":false%s}}`,
		running, phase, now, extra)
}

func TestUpdateCheckShowsPlan(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/update", 202, `{"started":true,"action":"check","reboot":false}`)
	now := time.Now().UTC().Format(time.RFC3339)
	f.on("GET /v1/update", 200, fmt.Sprintf(`{"available":true,"running":false,"state":{"phase":"idle"},"last_check":{"time":%q,"plan":{"up_to_date":false,"changes":[{"name":"linux-image-amd64","from":"6.12.111-1","to":"6.12.112-1"}],"kernels":["6.12.112+deb13-amd64"],"driver_from":"595.91.07-1","driver_to":"595.100.01-1"}}}`, now))
	res := runCLI(t, f, "update", "--check")
	wantCode(t, res, exitOK)
	wantContains(t, "check", res.stdout, "1 package changes")
	wantContains(t, "check", res.stdout, "new kernel: 6.12.112+deb13-amd64")
	wantContains(t, "check", res.stdout, "driver: 595.91.07-1 -> 595.100.01-1")
	if got := f.bodies["POST /v1/update"]; len(got) != 1 || !strings.Contains(got[0], `"action":"check"`) {
		t.Fatalf("request bodies = %v", got)
	}
}

func TestUpdateApplyFollowsEventsAndAsksForReboot(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/update", 202, `{"started":true,"action":"apply","reboot":false}`)
	calls := 0
	f.onFunc("GET /v1/update", func(w http.ResponseWriter, _ *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		now := time.Now().UTC().Format(time.RFC3339)
		events := fmt.Sprintf(`,"events":[{"time":%q,"step":"plan","message":"7 package changes"}]`, now)
		if calls == 1 {
			_, _ = fmt.Fprint(w, updateInfoJSON(true, "running", events))
			return
		}
		done := fmt.Sprintf(`,"new_kernel":"6.12.112+deb13-amd64","driver_from":"595.91.07-1","driver_to":"595.100.01-1","events":[{"time":%q,"step":"plan","message":"7 package changes"},{"time":%q,"step":"done","message":"installed; reboot to finish"}]`, now, now)
		_, _ = fmt.Fprint(w, strings.Replace(updateInfoJSON(false, "pending_boot", done), `"reboot_required":false`, `"reboot_required":true`, 1))
	})
	res := runCLI(t, f, "update")
	wantCode(t, res, exitOK)
	wantContains(t, "apply", res.stdout, "[plan] 7 package changes")
	wantContains(t, "apply", res.stdout, "reboot to try the new kernel 6.12.112+deb13-amd64")
}

func TestUpdateFailureIsReported(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/update", 202, `{"started":true,"action":"apply"}`)
	f.onFunc("GET /v1/update", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = fmt.Fprint(w, updateInfoJSON(false, "failed", `,"code":"dkms_failed","message":"kernel 6.12.112: nvidia: module not found"`))
	})
	res := runCLI(t, f, "update")
	wantCode(t, res, exitFailure)
	wantContains(t, "failure", res.stderr, "dkms_failed")
}

func TestUpdateStartRejectionsAreExplained(t *testing.T) {
	cases := []struct {
		status     int
		code, want string
	}{
		{409, "busy", "already running"},
		{409, "pending_reboot", "waiting for a reboot"},
		{409, "live_session", "live session"},
		{503, "update_unavailable", "not available"},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			f := newFakeDaemon(t)
			f.on("POST /v1/update", tc.status, fmt.Sprintf(`{"error":{"code":%q,"message":"x"}}`, tc.code))
			res := runCLI(t, f, "update")
			wantCode(t, res, exitFailure)
			wantContains(t, tc.code, res.stderr, tc.want)
		})
	}
}

func TestUpdateStatusAndFlagChecks(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/update", 200, `{"available":true,"running":false,"running_kernel":"6.12.111+deb13-amd64","state":{"phase":"committed","reboot_required":false}}`)
	res := runCLI(t, f, "update", "--status")
	wantCode(t, res, exitOK)
	wantContains(t, "status", res.stdout, "state: committed")
	wantContains(t, "status", res.stdout, "running kernel: 6.12.111+deb13-amd64")

	wantCode(t, runCLI(t, nil, "update", "--check", "--rollback"), exitUsage)
	wantCode(t, runCLI(t, nil, "update", "extra"), exitUsage)
}

func TestReportToStdout(t *testing.T) {
	f := newFakeDaemon(t)
	f.onFunc("GET /v1/report", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("opereta report\nversion: 0.2.0\n"))
	})
	res := runCLI(t, f, "report")
	wantCode(t, res, exitOK)
	if res.stdout != "opereta report\nversion: 0.2.0\n" {
		t.Fatalf("report stdout = %q", res.stdout)
	}
}

func TestReportToFileMode0600(t *testing.T) {
	f := newFakeDaemon(t)
	f.onFunc("GET /v1/report", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("report body\n"))
	})
	path := filepath.Join(t.TempDir(), "report.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatalf("seed: %v", err)
	}
	res := runCLI(t, f, "report", "-o", path)
	wantCode(t, res, exitOK)
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %o, want 600", info.Mode().Perm())
	}
	data, _ := os.ReadFile(path)
	if string(data) != "report body\n" {
		t.Fatalf("report file = %q", data)
	}
}

func TestChatPrintsReplyAndSpeed(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/chat", 200, `{"content":"Hello there.","tokens_predicted":5,"tokens_per_second":12.5}`)
	res := runCLI(t, f, "chat", "say", "hi")
	wantCode(t, res, exitOK)
	wantContains(t, "chat", res.stdout, "Hello there.", "tokens: 5", "speed: 12.50 tokens/s")
	body := f.bodiesOf("POST /v1/server/chat")
	want := `{"messages":[{"role":"user","content":"say hi"}],"max_tokens":512}`
	if len(body) != 1 || body[0] != want {
		t.Fatalf("chat body = %v, want %s", body, want)
	}
}

func TestChatServerNotRunning(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/chat", 409, `{"error":{"code":"server_not_running","message":"the server is not running (state stopped)"}}`)
	res := runCLI(t, f, "chat", "hi")
	wantCode(t, res, exitFailure)
	wantContains(t, "chat stopped", res.stderr, "the server is not running (state stopped)")
}

func TestChatPromptAfterDoubleDashKeepsDash(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/chat", 200, `{"content":"ok","tokens_predicted":1,"tokens_per_second":1}`)
	res := runCLI(t, f, "chat", "--max-tokens", "7", "--", "-h")
	wantCode(t, res, exitOK)
	body := f.bodiesOf("POST /v1/server/chat")
	want := `{"messages":[{"role":"user","content":"-h"}],"max_tokens":7}`
	if len(body) != 1 || body[0] != want {
		t.Fatalf("chat body = %v", body)
	}
}

func TestVersionAndHelp(t *testing.T) {
	res := runCLI(t, nil, "version")
	wantCode(t, res, exitOK)
	if res.stdout != "opereta dev\n" {
		t.Fatalf("version = %q", res.stdout)
	}
	res = runCLI(t, nil, "help")
	wantCode(t, res, exitOK)
	wantContains(t, "help", res.stdout, "Usage: opereta", "serve start", "config set")
	res = runCLI(t, nil, "help", "serve", "start")
	wantCode(t, res, exitOK)
	wantContains(t, "help serve start", res.stdout, "Usage: opereta serve start", "--no-wait")
	res = runCLI(t, nil, "serve", "start", "-h")
	wantCode(t, res, exitOK)
	wantContains(t, "serve start -h", res.stdout, "Usage: opereta serve start", "Flags:", "-ngl")
	res = runCLI(t, nil, "-h")
	wantCode(t, res, exitOK)
	wantContains(t, "-h", res.stdout, "Commands:")
}

// ---- usage errors and error mapping ----

func TestUsageErrors(t *testing.T) {
	cases := [][]string{
		{},
		{"nosuchcommand"},
		{"status", "extra"},
		{"gpu"},
		{"gpu", "frobnicate"},
		{"model"},
		{"model", "pull"},
		{"model", "rm"},
		{"serve"},
		{"serve", "start", "a", "b"},
		{"apikey", "create", "a", "b"},
		{"config", "set"},
		{"config", "set", "port"},
		{"config", "set", "unknown", "x"},
		{"chat"},
		{"serve", "start", "--nosuchflag"},
		{"--nosuchglobal", "status"},
	}
	for _, args := range cases {
		res := runCLI(t, nil, args...)
		if res.code != exitUsage {
			t.Fatalf("args %v: exit %d, want %d\nstderr: %s", args, res.code, exitUsage, res.stderr)
		}
	}
}

func TestDescribeErrorMapping(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code int
		want string
	}{
		{"permission", fmt.Errorf("%w: connect", client.ErrPermission), exitUnreachable, "permission denied: add your user to the 'opereta' group or run with sudo"},
		{"unavailable", fmt.Errorf("%w: connect", client.ErrUnavailable), exitUnreachable, "operetad is not running (systemctl status operetad)"},
		{"deadline", context.DeadlineExceeded, exitFailure, "timed out waiting for operetad"},
	}
	for _, tc := range cases {
		code, msg := describeError(tc.err)
		if code != tc.code || msg != tc.want {
			t.Fatalf("%s: got (%d, %q)", tc.name, code, msg)
		}
	}
}

func TestNetworkStatusAndWifiList(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/network", 200, `{"interfaces":[{"name":"enp3s0","kind":"ethernet","state":"down","addresses":[]},{"name":"wlan0","kind":"wifi","state":"up","addresses":["192.168.0.23/24"],"gateway":"192.168.0.1"}],"wifi":{"available":true,"device":"wlan0","powered":true,"state":"connected","scanning":false,"connected_ssid":"HomeWiFi"},"online":true}`)
	res := runCLI(t, f, "network")
	wantCode(t, res, exitOK)
	wantContains(t, "network", res.stdout, "wlan0")
	wantContains(t, "network", res.stdout, "192.168.0.23/24")
	wantContains(t, "network", res.stdout, "wifi: connected to HomeWiFi on wlan0")

	f.on("GET /v1/network/wifi/networks", 200, `{"networks":[{"ssid":"HomeWiFi","security":"psk","signal":80,"signal_dbm":-42,"connected":true,"known":true},{"ssid":"CafeFree","security":"open","signal":50,"signal_dbm":-61,"connected":false,"known":false}]}`)
	res = runCLI(t, f, "network", "wifi", "list")
	wantCode(t, res, exitOK)
	wantContains(t, "list", res.stdout, "* HomeWiFi")
	wantContains(t, "list", res.stdout, "CafeFree")
}

func TestWifiConnectSendsThePasswordInTheBodyOnly(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/network/wifi/connect", 200, `{"connected":true,"ssid":"Home"}`)
	res := runCLIWith(t, f, cliOpts{stdin: "s3cretpassw0rd\n"}, "network", "wifi", "connect", "Home", "--password-stdin")
	wantCode(t, res, exitOK)
	if strings.Contains(res.stdout+res.stderr, "s3cretpassw0rd") {
		t.Fatal("the password appeared in the output")
	}
	bodies := f.bodies["POST /v1/network/wifi/connect"]
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"passphrase":"s3cretpassw0rd"`) || !strings.Contains(bodies[0], `"ssid":"Home"`) {
		t.Fatalf("bodies = %v", bodies)
	}
}

func TestWifiConnectFailureIsReported(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/network/wifi/connect", 502, `{"error":{"code":"connect_failed","message":"could not connect to \"Home\""}}`)
	res := runCLIWith(t, f, cliOpts{stdin: "wrongpassword1\n"}, "network", "wifi", "connect", "Home", "--password-stdin")
	wantCode(t, res, exitFailure)
	wantContains(t, "connect", res.stderr, "connect_failed")
}

func TestWifiListEscapesNonASCIINames(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/network/wifi/networks", 200, `{"networks":[{"ssid":"카페 WiFi","security":"open","signal":70,"signal_dbm":-50,"connected":false,"known":false}]}`)
	res := runCLI(t, f, "network", "wifi", "list") // 출력이 ASCII 가 아니면 runCLI 가 실패시킨다
	wantCode(t, res, exitOK)
	wantContains(t, "list", res.stdout, "WiFi")
}

func TestConnectionShowsAddressesWarningsAndExamples(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, `{"server_state":"running","model_id":"gemma.gguf","models":["gemma.gguf"],"public_port":8080,"lan_exposed":true,"gateway":true,"key_required":true,"key_count":0,"hostname":"opereta","local_url":"http://127.0.0.1:8080/v1","addresses":[{"interface":"enp3s0","address":"192.168.0.23","family":"ipv4","url":"http://192.168.0.23:8080/v1"}],"mdns_url":"http://opereta.local:8080/v1","warnings":["No API key exists. Create one (opereta apikey create <label>); LAN connections are refused without a key."],"examples":{"curl":"curl http://192.168.0.23:8080/v1/chat/completions -H 'Authorization: Bearer <your API key>'","settings":"Base URL: http://192.168.0.23:8080/v1"}}`)
	res := runCLI(t, f, "connection")
	wantCode(t, res, exitOK)
	wantContains(t, "connection", res.stdout, "http://192.168.0.23:8080/v1", "opereta.local", "warning: No API key exists", "gemma.gguf")

	res = runCLI(t, f, "connection", "examples")
	wantCode(t, res, exitOK)
	wantContains(t, "examples", res.stdout, "# curl", "<your API key>")

	res = runCLI(t, f, "connection", "bogus")
	wantCode(t, res, exitUsage)
}

func TestGatewayShowsUsagePerKey(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/gateway", 200, `{"enabled":true,"public_port":8080,"lan_exposed":true,"state":{"listening":true,"addr":"0.0.0.0:8080","backend_port":18080},"stats":{"in_flight":1,"requests":12,"errors":2,"unauthorized":3,"since":"2026-10-13T00:00:00Z","keys":[{"id":"k1","label":"orca","prefix":"opr_ab12","requests":10,"errors":1,"prompt_tokens":420,"completion_tokens":900,"last_used":"2026-10-13T01:02:03Z"}],"recent":[]}}`)
	res := runCLI(t, f, "gateway")
	wantCode(t, res, exitOK)
	wantContains(t, "gateway", res.stdout, "0.0.0.0:8080 -> 127.0.0.1:18080", "requests:", "12 (errors 2, unauthorized 3, in flight 1)", "orca", "opr_ab12", "420", "900")
}

func TestModelImportSendsTheFileAndReportsTheResult(t *testing.T) {
	f := newFakeDaemon(t)
	dir := t.TempDir()
	file := filepath.Join(dir, "my-model.gguf")
	content := "GGUF-not-really-but-the-fake-daemon-does-not-care"
	if err := os.WriteFile(file, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	f.on("PUT /v1/models/import", 201, `{"model_id":"my-model.gguf","size_bytes":49,"sha256":"`+strings.Repeat("ab", 32)+`","default":true}`)
	res := runCLI(t, f, "model", "import", file)
	wantCode(t, res, exitOK)
	wantContains(t, "import", res.stdout, "imported my-model.gguf", "it is now the default model")
	if got := f.bodiesOf("PUT /v1/models/import"); len(got) != 1 || got[0] != content {
		t.Fatalf("body = %v", got)
	}
	if !f.hitsContain("PUT /v1/models/import?name=my-model.gguf") {
		t.Fatalf("hits = %v", f.hits)
	}

	// --name 으로 이름을 바꿔 보낸다. 이름이 .gguf 로 끝나지 않으면 보내지 않는다.
	res = runCLI(t, f, "model", "import", file, "--name", "renamed.gguf")
	wantCode(t, res, exitOK)
	if !f.hitsContain("PUT /v1/models/import?name=renamed.gguf") {
		t.Fatalf("renamed hits = %v", f.hits)
	}
	res = runCLI(t, f, "model", "import", file, "--name", "bad.bin")
	wantCode(t, res, exitUsage)

	res = runCLI(t, f, "model", "import", filepath.Join(dir, "missing.gguf"))
	wantCode(t, res, exitFailure)
}

func TestModelImportShowsTheDaemonsRefusal(t *testing.T) {
	f := newFakeDaemon(t)
	file := filepath.Join(t.TempDir(), "x.gguf")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	f.on("PUT /v1/models/import", 409, `{"error":{"code":"name_conflict","message":"a model named x.gguf already exists"}}`)
	res := runCLI(t, f, "model", "import", file)
	wantCode(t, res, exitFailure)
	wantContains(t, "conflict", res.stderr, "already exists", "name_conflict")
}

func TestServeStatsShowsSlotsAndSpeed(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/server/stats", 200, `{"running":true,"state":"running","model_id":"m.gguf","uptime_seconds":3700,"parallel":2,"slot_ctx":2048,"slots":{"available":true,"total":2,"busy":1,"items":[]},"metrics":{"available":true,"generated_tokens_per_second":31.5,"prompt_tokens_per_second":700,"requests_processing":1,"requests_deferred":0,"prompt_tokens_total":1200,"generated_tokens_total":3400},"gateway":{"requests":12,"errors":1,"unauthorized":2,"in_flight":1}}`)
	res := runCLI(t, f, "serve", "stats")
	wantCode(t, res, exitOK)
	wantContains(t, "stats", res.stdout, "1 busy of 2 (context per slot 2048)", "31.5 tokens/s", "12 requests, 1 errors, 2 refused, 1 in flight", "1h 1m")

	f.on("GET /v1/server/stats", 200, `{"running":false,"state":"stopped","slots":{"available":false,"items":[]},"metrics":{"available":false}}`)
	res = runCLI(t, f, "serve", "stats")
	wantCode(t, res, exitOK)
	wantContains(t, "stopped", res.stdout, "not running")
}

func TestNetworkSetStaticAndConfirm(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("PUT /v1/network/interfaces/enp3s0", 200, `{"config":{"interface":"enp3s0","mode":"static","addresses":["192.168.0.50/24"],"gateway":"192.168.0.1","dns":["1.1.1.1"],"managed":true},"pending":{"interface":"enp3s0","deadline":"2026-10-13T12:01:00Z"},"confirm_seconds":60}`)
	res := runCLI(t, f, "network", "set", "enp3s0", "--address", "192.168.0.50/24", "--gateway", "192.168.0.1", "--dns", "1.1.1.1")
	wantCode(t, res, exitOK)
	wantContains(t, "set", res.stdout, "static 192.168.0.50/24, gateway 192.168.0.1", "opereta network confirm", "60 seconds")
	got := f.bodiesOf("PUT /v1/network/interfaces/enp3s0")
	if len(got) != 1 || !strings.Contains(got[0], `"mode":"static"`) || !strings.Contains(got[0], `"addresses":["192.168.0.50/24"]`) || !strings.Contains(got[0], `"confirm_seconds":60`) {
		t.Fatalf("body = %v", got)
	}

	f.on("POST /v1/network/confirm", 200, `{"confirmed":true}`)
	res = runCLI(t, f, "network", "confirm")
	wantCode(t, res, exitOK)
	wantContains(t, "confirm", res.stdout, "confirmed")

	res = runCLI(t, f, "network", "set", "enp3s0")
	wantCode(t, res, exitUsage)
	res = runCLI(t, f, "network", "set", "enp3s0", "--dhcp", "--address", "10.0.0.2/24")
	wantCode(t, res, exitUsage)
}

func TestNetworkDiagnoseFailsWhenACheckFails(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/network/diagnose", 200, `{"checks":[{"name":"address","ok":true,"detail":"enp3s0 192.168.0.23/24"},{"name":"dns","ok":false,"detail":"huggingface.co could not be resolved"},{"name":"gateway","ok":false,"skipped":true,"detail":"ping is not available"}]}`)
	res := runCLI(t, f, "network", "diagnose")
	wantCode(t, res, exitFailure)
	wantContains(t, "diagnose", res.stdout, "ok    address", "FAIL  dns", "skip  gateway")
}

func TestFirewallListAddAndToggle(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/firewall", 200, `{"policy":"drop","builtin":[{"name":"SSH","protocol":"tcp","port":22,"source":"any","reason":"the SSH server is on"}],"rules":[{"id":"r1","name":"WebUI","protocol":"tcp","port":3000,"source":"lan","enabled":true}],"max_rule":64}`)
	res := runCLI(t, f, "firewall")
	wantCode(t, res, exitOK)
	wantContains(t, "list", res.stdout, "SSH", "the SSH server is on", "r1", "WebUI", "lan")

	f.on("POST /v1/firewall/rules", 201, `{"id":"r2","name":"Range","protocol":"udp","port":6000,"port_end":6010,"source":"10.0.0.0/8","enabled":true}`)
	res = runCLI(t, f, "firewall", "add", "Range", "udp", "6000-6010", "--from", "10.0.0.0/8")
	wantCode(t, res, exitOK)
	wantContains(t, "add", res.stdout, "rule r2 added", "udp/6000-6010")
	body := f.bodiesOf("POST /v1/firewall/rules")
	if len(body) != 1 || !strings.Contains(body[0], `"port":6000`) || !strings.Contains(body[0], `"port_end":6010`) {
		t.Fatalf("body = %v", body)
	}

	f.on("PUT /v1/firewall/rules/r1", 200, `{"id":"r1","name":"WebUI","protocol":"tcp","port":3000,"source":"lan","enabled":false}`)
	res = runCLI(t, f, "firewall", "disable", "r1")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("PUT /v1/firewall/rules/r1"); len(b) != 1 || !strings.Contains(b[0], `"enabled":false`) {
		t.Fatalf("body = %v", b)
	}
	res = runCLI(t, f, "firewall", "disable", "r9")
	wantCode(t, res, exitFailure)
	res = runCLI(t, f, "firewall", "add", "x", "tcp", "abc")
	wantCode(t, res, exitUsage)
}

func TestSSHAdvancedCommands(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/ssh/advanced", 200, `{"enabled":true,"port":2222,"password_login_configured":false,"key_count":2,"host_keys":[{"type":"ssh-ed25519","fingerprint":"SHA256:abc","comment":"ssh_host_ed25519_key.pub"}]}`)
	res := runCLI(t, f, "ssh", "advanced")
	wantCode(t, res, exitOK)
	wantContains(t, "advanced", res.stdout, "2222", "password login:", "off", "public keys:", "SHA256:abc")

	f.on("PUT /v1/ssh/advanced", 200, `{"enabled":true,"port":2200,"key_count":2,"host_keys":[]}`)
	res = runCLI(t, f, "ssh", "port", "2200")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("PUT /v1/ssh/advanced"); len(b) != 1 || b[0] != `{"port":2200}` {
		t.Fatalf("body = %v", b)
	}
	res = runCLI(t, f, "ssh", "password", "off")
	wantCode(t, res, exitOK)
	res = runCLI(t, f, "ssh", "port", "99999")
	wantCode(t, res, exitUsage)

	f.on("PUT /v1/ssh/advanced", 409, `{"error":{"code":"no_keys","message":"add a public key to an account first"}}`)
	res = runCLI(t, f, "ssh", "password", "off")
	wantCode(t, res, exitFailure)
	wantContains(t, "no keys", res.stderr, "add a public key", "no_keys")

	f.on("POST /v1/ssh/keys", 201, `{"key":{"type":"ssh-ed25519","fingerprint":"SHA256:x"},"added":true}`)
	res = runCLIWith(t, f, cliOpts{stdin: "ssh-ed25519 AAAAC3 alice@laptop\n"}, "ssh", "keys", "add", "alice")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("POST /v1/ssh/keys"); len(b) != 1 || !strings.Contains(b[0], `"user":"alice"`) || !strings.Contains(b[0], "AAAAC3") {
		t.Fatalf("body = %v", b)
	}
	f.on("GET /v1/ssh/log", 200, `{"events":[{"time":"2026-10-13T12:00:00+0900","result":"accepted","user":"alice","method":"publickey","from":"192.168.0.5"}]}`)
	res = runCLI(t, f, "ssh", "log")
	wantCode(t, res, exitOK)
	wantContains(t, "log", res.stdout, "alice", "publickey", "192.168.0.5")
}

func TestServicesAndResources(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/services", 200, `{"services":[{"unit":"ssh.service","title":"SSH server","installed":true,"active":"active","sub":"running","enabled":"enabled","pid":10,"memory_mib":4,"actions":["restart"]},{"unit":"xrdp.service","title":"Remote desktop (RDP)","installed":false,"active":"inactive","sub":"dead","enabled":"not-found","actions":[]}]}`)
	res := runCLI(t, f, "services")
	wantCode(t, res, exitOK)
	wantContains(t, "list", res.stdout, "ssh.service", "active/running", "enabled", "xrdp.service", "not-found")

	f.on("POST /v1/services/ssh.service/restart", 200, `{"unit":"ssh.service","action":"restart","done":true}`)
	res = runCLI(t, f, "services", "restart", "ssh.service")
	wantCode(t, res, exitOK)
	f.on("POST /v1/services/operetad.service/stop", 403, `{"error":{"code":"action_not_allowed","message":"Opereta daemon cannot be stopped from here"}}`)
	res = runCLI(t, f, "services", "stop", "operetad.service")
	wantCode(t, res, exitFailure)
	wantContains(t, "protected", res.stderr, "action_not_allowed")

	f.on("GET /v1/services/ssh.service/logs", 200, `{"unit":"ssh.service","log":"2026-10-13 sshd started\n"}`)
	res = runCLI(t, f, "services", "logs", "ssh.service", "-n", "20")
	wantCode(t, res, exitOK)
	wantContains(t, "logs", res.stdout, "sshd started")

	f.on("GET /v1/resources", 200, `{"system":{"uptime_seconds":90061,"cpu":{"cores":8,"used_percent":12.5,"load1":0.5,"load5":0.4,"load15":0.3},"memory":{"total_mib":16000,"available_mib":8000,"used_mib":8000,"swap_total_mib":0,"swap_used_mib":0},"disks":[{"mount":"/","fs_type":"ext4","total_mib":100000,"used_mib":40000,"used_percent":40}],"network":[{"name":"enp3s0","rx_bytes_per_second":2048,"tx_bytes_per_second":0}],"temperatures":[{"name":"x86_pkg_temp","celsius":52.5}],"top_processes":[{"pid":4242,"name":"llama-server","rss_mib":3000}]},"gpus":[{"index":0,"name":"RTX 3060","memory_total_mib":6144,"memory_used_mib":3000,"util_gpu_percent":5,"temperature_c":50,"power_mw":20000}],"gpu_available":true}`)
	res = runCLI(t, f, "resources")
	wantCode(t, res, exitOK)
	wantContains(t, "resources", res.stdout, "12.5% of 8 cores", "disk /", "net enp3s0", "52.5 C", "RTX 3060", "llama-server")
}

func TestVPNPeerAddShowsTheClientConfigOnce(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/vpn/peers", 201, `{"peer":{"name":"phone","public_key":"pub","allowed_ips":["10.8.0.2/32"]},"client_config":"[Interface]\nPrivateKey = CLIENTSECRET\nAddress = 10.8.0.2/32\n","note":"shown only once"}`)
	res := runCLI(t, f, "vpn", "peer", "add", "phone")
	wantCode(t, res, exitOK)
	wantContains(t, "peer add", res.stdout, "peer phone added", "CLIENTSECRET", "shown only once")

	out := filepath.Join(t.TempDir(), "phone.conf")
	res = runCLI(t, f, "vpn", "peer", "add", "phone", "--out", out)
	wantCode(t, res, exitOK)
	if strings.Contains(res.stdout, "CLIENTSECRET") {
		t.Fatal("the private key was printed although --out was given")
	}
	st, err := os.Stat(out)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("out file: %v %v", st, err)
	}

	f.on("GET /v1/vpn", 200, `{"enabled":true,"interface":"wg0","address":"10.8.0.1/24","listen_port":51820,"public_key":"serverpub","peers":[{"name":"phone","public_key":"pub","allowed_ips":["10.8.0.2/32"]}]}`)
	res = runCLI(t, f, "vpn")
	wantCode(t, res, exitOK)
	wantContains(t, "status", res.stdout, "wg0", "51820/udp", "serverpub", "phone")
}

func TestRemoteDesktopOnAndStatus(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("PUT /v1/remote-desktop", 200, `{"installed":true,"enabled":true,"active":true,"port":3389,"source":"192.168.1.0/24"}`)
	res := runCLI(t, f, "remote-desktop", "on", "--from", "192.168.1.0/24")
	wantCode(t, res, exitOK)
	wantContains(t, "on", res.stdout, "remote desktop:", "yes", "3389/tcp", "192.168.1.0/24")
	if b := f.bodiesOf("PUT /v1/remote-desktop"); len(b) != 1 || !strings.Contains(b[0], `"source":"192.168.1.0/24"`) {
		t.Fatalf("body = %v", b)
	}
	f.on("GET /v1/remote-desktop", 200, `{"installed":false,"enabled":false,"active":false,"port":3389,"source":"lan","note":"xrdp is not installed in this image"}`)
	res = runCLI(t, f, "remote-desktop")
	wantCode(t, res, exitOK)
	wantContains(t, "off", res.stdout, "not installed")
	res = runCLI(t, f, "remote-desktop", "maybe")
	wantCode(t, res, exitUsage)
}

func TestWebAdminStatusOnAndPassword(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("PUT /v1/webadmin", 200, `{"enabled":true,"running":true,"port":8443,"source":"lan","password_set":true,"cert_fingerprint":"AA:BB","urls":["https://192.168.0.23:8443/"]}`)
	res := runCLI(t, f, "webadmin", "on", "--port", "9443", "--from", "lan")
	wantCode(t, res, exitOK)
	wantContains(t, "on", res.stdout, "web admin:", "yes", "AA:BB", "https://192.168.0.23:8443/", "self-signed")
	if b := f.bodiesOf("PUT /v1/webadmin"); len(b) != 1 || !strings.Contains(b[0], `"port":9443`) || !strings.Contains(b[0], `"enabled":true`) {
		t.Fatalf("body = %v", b)
	}

	f.on("PUT /v1/webadmin", 409, `{"error":{"code":"password_required","message":"set the web admin password first"}}`)
	res = runCLI(t, f, "webadmin", "on")
	wantCode(t, res, exitFailure)
	wantContains(t, "password", res.stderr, "set the web admin password first")

	f.on("PUT /v1/webadmin/password", 200, `{"password_set":true}`)
	res = runCLIWith(t, f, cliOpts{stdin: "correct horse battery\n"}, "webadmin", "password")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("PUT /v1/webadmin/password"); len(b) != 1 || b[0] != `{"password":"correct horse battery"}` {
		t.Fatalf("body = %v", b)
	}
	if strings.Contains(res.stdout+res.stderr, "correct horse battery") {
		t.Fatal("the password was echoed")
	}
}

func TestExposureExitCodeFollowsTheFindings(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/exposure", 200, `{"level":"warning","findings":[{"level":"warning","title":"SSH accepts password logins","detail":"Keys are safer."},{"level":"ok","title":"The model server is reachable only from this computer"}]}`)
	res := runCLI(t, f, "exposure")
	wantCode(t, res, exitFailure)
	wantContains(t, "warn", res.stdout, "WARN  SSH accepts password logins", "Keys are safer.", "ok    The model server")
	f.on("GET /v1/exposure", 200, `{"level":"ok","findings":[{"level":"ok","title":"fine"}]}`)
	res = runCLI(t, f, "exposure")
	wantCode(t, res, exitOK)
}

func TestServeToolCheckReportsResult(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/server/toolcheck", 200, `{"ok":true,"code":"ok","message":"the model calls tools correctly","finish_reason":"tool_calls","latency_ms":820}`)
	res := runCLI(t, f, "serve", "toolcheck")
	wantCode(t, res, exitOK)
	wantContains(t, "toolcheck", res.stdout, "working", "tool_calls", "820 ms")

	f.on("POST /v1/server/toolcheck", 200, `{"ok":false,"code":"no_tool_call","message":"the model answered in text","finish_reason":"stop","latency_ms":300}`)
	res = runCLI(t, f, "serve", "toolcheck")
	wantCode(t, res, exitFailure)
	wantContains(t, "toolcheck broken", res.stdout, "not working", "answered in text")
}

const tlsBody = `{"enabled":true,"active":true,"port":8444,"lan_plain":false,"names":[],"plain_port":8080,"plain_lan_open":false,` +
	`"listening":true,"listen_addr":"0.0.0.0:8444","ca_fingerprint":"AA:BB:CC","ca_not_after":"2036-10-14T00:00:00Z",` +
	`"server_fingerprint":"11:22:33","server_not_after":"2027-11-15T00:00:00Z","dns":["box.local"],"ips":["192.168.0.23"],` +
	`"notes":["plain HTTP is also open on the local network (lan_plain); keys and prompts sent there are not encrypted"]}`

func TestTLSStatusOnOffAndConfirmations(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/tls", 200, tlsBody)
	res := runCLI(t, f, "tls")
	wantCode(t, res, exitOK)
	wantContains(t, "status", res.stdout, "tls:", "yes", "8444/tcp (HTTPS)", "AA:BB:CC", "box.local", "192.168.0.23", "not encrypted")

	f.on("PUT /v1/tls", 200, tlsBody)
	res = runCLI(t, f, "tls", "on", "--port", "9443")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("PUT /v1/tls"); len(b) != 1 || !strings.Contains(b[0], `"enabled":true`) || !strings.Contains(b[0], `"port":9443`) {
		t.Fatalf("body = %v", b)
	}

	// 끄기, 평문 열기, CA 갱신은 --yes 가 없으면 보내지 않는다.
	for _, args := range [][]string{{"tls", "off"}, {"tls", "plain", "on"}, {"tls", "renew", "--ca"}} {
		before := len(f.bodiesOf("PUT /v1/tls")) + len(f.bodiesOf("POST /v1/tls/renew"))
		res = runCLI(t, f, args...)
		wantCode(t, res, exitUsage)
		if after := len(f.bodiesOf("PUT /v1/tls")) + len(f.bodiesOf("POST /v1/tls/renew")); after != before {
			t.Fatalf("%v sent a request without --yes", args)
		}
	}
	res = runCLI(t, f, "tls", "off", "--yes")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("PUT /v1/tls"); !strings.Contains(b[len(b)-1], `"enabled":false`) {
		t.Fatalf("off body = %v", b)
	}
	res = runCLI(t, f, "tls", "plain", "off")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("PUT /v1/tls"); !strings.Contains(b[len(b)-1], `"lan_plain":false`) {
		t.Fatalf("plain off body = %v", b)
	}

	f.on("PUT /v1/tls", 409, `{"error":{"code":"lan_required","message":"LAN exposure must be on first: opereta config set lan on"}}`)
	res = runCLI(t, f, "tls", "on")
	wantCode(t, res, exitFailure)
	wantContains(t, "lan", res.stderr, "LAN exposure must be on first")
}

func TestTLSCAFingerprintAndRenew(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/tls", 200, tlsBody)
	f.onFunc("GET /v1/tls/ca", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/x-pem-file")
		_, _ = w.Write([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"))
	})
	res := runCLI(t, f, "tls", "ca")
	wantCode(t, res, exitOK)
	wantContains(t, "ca", res.stdout, "BEGIN CERTIFICATE")

	out := filepath.Join(t.TempDir(), "ca.pem")
	res = runCLI(t, f, "tls", "ca", "--out", out)
	wantCode(t, res, exitOK)
	wantContains(t, "ca out", res.stdout, "AA:BB:CC", "compare its fingerprint")
	if data, err := os.ReadFile(out); err != nil || !strings.Contains(string(data), "BEGIN CERTIFICATE") {
		t.Fatalf("file = %q, %v", data, err)
	}

	res = runCLI(t, f, "tls", "fingerprint")
	wantCode(t, res, exitOK)
	if strings.TrimSpace(res.stdout) != "AA:BB:CC" {
		t.Fatalf("fingerprint = %q", res.stdout)
	}

	f.on("POST /v1/tls/renew", 200, tlsBody)
	res = runCLI(t, f, "tls", "renew")
	wantCode(t, res, exitOK)
	res = runCLI(t, f, "tls", "renew", "--ca", "--yes")
	wantCode(t, res, exitOK)
	if b := f.bodiesOf("POST /v1/tls/renew"); len(b) != 2 || !strings.Contains(b[1], `"ca":true`) || !strings.Contains(b[0], `"ca":false`) {
		t.Fatalf("renew bodies = %v", b)
	}
	res = runCLI(t, f, "tls", "bogus")
	wantCode(t, res, exitUsage)
}

func TestGPURuntimeStatus(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/update", 200, `{"available":true,"running":false,"state":{"phase":"idle"},"cuda_runtime":{"installed":false,"reason":"not_installed","packages":[{"name":"cuda-cudart-13-2","version":"13.2.86-1"}]}}`)
	res := runCLI(t, f, "gpu", "runtime")
	wantCode(t, res, exitOK)
	wantContains(t, "status", res.stdout, "not installed")
	wantContains(t, "status", res.stdout, "opereta gpu runtime install")
	wantContains(t, "status", res.stdout, "cuda-cudart-13-2 13.2.86-1")
}

func TestGPURuntimeInstallNeedsConfirmationWithoutATerminal(t *testing.T) {
	f := newFakeDaemon(t)
	res := runCLI(t, f, "gpu", "runtime", "install")
	wantCode(t, res, exitUsage)
	wantContains(t, "no terminal", res.stderr, "--yes")
	wantContains(t, "license shown", res.stdout, "docs.nvidia.com/cuda/eula")
	wantContains(t, "datacenter note", res.stdout, "datacenter")
	if len(f.bodies["POST /v1/update"]) != 0 {
		t.Fatal("the download started without the user's confirmation")
	}
}

func TestGPURuntimeInstallWithYesStartsTheAction(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/update", 202, `{"started":true,"action":"cuda_runtime","reboot":false}`)
	f.onFunc("GET /v1/update", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		now := time.Now().UTC().Format(time.RFC3339)
		extra := fmt.Sprintf(`,"operation":"cuda_runtime","events":[{"time":%q,"step":"done","message":"the CUDA runtime is installed"}]`, now)
		_, _ = fmt.Fprint(w, updateInfoJSON(false, "committed", extra))
	})
	res := runCLI(t, f, "gpu", "runtime", "install", "--yes")
	wantCode(t, res, exitOK)
	wantContains(t, "install", res.stdout, "CUDA runtime is installed")
	if got := f.bodies["POST /v1/update"]; len(got) != 1 || !strings.Contains(got[0], `"action":"cuda_runtime"`) {
		t.Fatalf("request bodies = %v", got)
	}
}

func TestDeberopaStatus(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/update", 200, `{"available":true,"running":false,"state":{"phase":"idle"},"deberopa":{"auto":true,"installed":"0.3.1","packaged":"0.2.0","latest":"0.3.1","override":true,"status":"updated","checked":"2026-10-14T12:00:00Z"}}`)
	res := runCLI(t, f, "deberopa")
	wantCode(t, res, exitOK)
	wantContains(t, "installed", res.stdout, "deberopa: 0.3.1")
	wantContains(t, "image", res.stdout, "image version: 0.2.0")
	wantContains(t, "auto", res.stdout, "automatic updates: on")
}

func TestDeberopaUpdateStartsTheActionAndWaitsForTheCheck(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/update", 202, `{"started":true,"action":"deberopa_update","reboot":false}`)
	f.onFunc("GET /v1/update", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = fmt.Fprintf(w, `{"available":true,"running":false,"state":{"phase":"idle"},"deberopa":{"auto":true,"installed":"0.3.1","latest":"0.3.1","override":true,"status":"updated","checked":%q}}`, now)
	})
	res := runCLI(t, f, "deberopa", "update")
	wantCode(t, res, exitOK)
	wantContains(t, "installed", res.stdout, "deberopa: 0.3.1")
	if got := f.bodies["POST /v1/update"]; len(got) != 1 || !strings.Contains(got[0], `"action":"deberopa_update"`) {
		t.Fatalf("request bodies = %v", got)
	}
}

func TestDeberopaUpdateFailureExitsNonZero(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/update", 202, `{"started":true,"action":"deberopa_update","reboot":false}`)
	f.onFunc("GET /v1/update", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = fmt.Fprintf(w, `{"available":true,"running":false,"state":{"phase":"idle"},"deberopa":{"auto":true,"installed":"0.2.0","status":"error","code":"checksum_mismatch","message":"SHA-256 differs","checked":%q}}`, now)
	})
	res := runCLI(t, f, "deberopa", "update")
	wantCode(t, res, exitFailure)
	wantContains(t, "error", res.stdout, "checksum_mismatch")
}

func TestDeberopaAutoNeedsOnOrOff(t *testing.T) {
	f := newFakeDaemon(t)
	res := runCLI(t, f, "deberopa", "auto")
	wantCode(t, res, exitUsage)
	res = runCLI(t, f, "deberopa", "auto", "maybe")
	wantCode(t, res, exitUsage)
	res = runCLI(t, f, "deberopa", "bogus")
	wantCode(t, res, exitUsage)
}

const bootInfoJSON = `{"available":true,"running":false,"state":{"phase":"idle"},"boot":{"available":true,"uefi":true,"secure_boot":"off","current":"0000","order":["0002","0001","0000"],"entries":[{"num":"0000","label":"opereta","active":true,"kind":"opereta"},{"num":"0001","label":"Windows Boot Manager","active":true,"kind":"windows"},{"num":"0002","label":"UEFI: SanDisk","active":true,"kind":"removable"}],"opereta_entry":"0000","windows_entry":"0001","auto":false,"bitlocker":true,"problems":[{"code":"firmware_reorders","message":"x"}]}}`

func TestBootStatusExplainsTheProblem(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/update", 200, bootInfoJSON)
	res := runCLI(t, f, "boot")
	wantCode(t, res, exitOK)
	wantContains(t, "uefi", res.stdout, "boot mode: UEFI, Secure Boot off")
	wantContains(t, "order", res.stdout, "1. UEFI: SanDisk (0002)")
	wantContains(t, "windows", res.stdout, "Windows Boot Manager (0001)")
	wantContains(t, "problem", res.stdout, "opereta boot next opereta")
	wantContains(t, "bitlocker", res.stdout, "BitLocker")
}

func TestBootNextStartsTheActionAndWaitsForTheRecord(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("POST /v1/update", 202, `{"started":true,"action":"boot_next:opereta","reboot":false}`)
	f.onFunc("GET /v1/update", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		now := time.Now().UTC().Format(time.RFC3339)
		_, _ = fmt.Fprintf(w, `{"available":true,"running":false,"state":{"phase":"idle"},"boot":{"available":true,"uefi":true,"secure_boot":"off","current":"0001","next":"0000","order":["0001","0000"],"entries":[{"num":"0000","label":"opereta","kind":"opereta"},{"num":"0001","label":"Windows Boot Manager","kind":"windows"}],"last_action":"next opereta","last_time":%q}}`, now)
	})
	res := runCLI(t, f, "boot", "next", "opereta")
	wantCode(t, res, exitOK)
	wantContains(t, "next", res.stdout, "next boot only: opereta (0000)")
	if got := f.bodies["POST /v1/update"]; len(got) != 1 || !strings.Contains(got[0], `"action":"boot_next:opereta"`) {
		t.Fatalf("request bodies = %v", got)
	}
}

func TestBootRejectsBadArguments(t *testing.T) {
	f := newFakeDaemon(t)
	for _, args := range [][]string{{"boot", "next"}, {"boot", "next", "ubuntu"}, {"boot", "auto", "maybe"}, {"boot", "bogus"}, {"boot", "status", "x"}} {
		res := runCLI(t, f, args...)
		wantCode(t, res, exitUsage)
	}
	if len(f.bodies["POST /v1/update"]) != 0 {
		t.Fatal("an action was started for an invalid request")
	}
}
