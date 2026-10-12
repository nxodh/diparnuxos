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

	"github.com/nxodh/diparnuxos/daemon/internal/client"
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
		"not checked", "GPU check has not run", "run 'diparnux gpu check'",
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

func TestStatusUnreachableExit3(t *testing.T) {
	res := runCLI(t, nil, "--socket", filepath.Join(t.TempDir(), "absent.sock"), "status")
	wantCode(t, res, exitUnreachable)
	if !strings.Contains(res.stderr, "diparnuxd is not running (systemctl status diparnuxd)") {
		t.Fatalf("stderr = %q", res.stderr)
	}
}

func TestSocketFromEnvironment(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNoGPU)
	res := runCLIWith(t, nil, cliOpts{env: map[string]string{"DIPARNUX_SOCKET": f.sock}}, "status")
	wantCode(t, res, exitOK)
}

func TestFlagSocketBeatsEnvironment(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusNoGPU)
	res := runCLIWith(t, nil, cliOpts{env: map[string]string{"DIPARNUX_SOCKET": filepath.Join(t.TempDir(), "x.sock")}},
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
		"diparnux model default stories260K.gguf",
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
	if !strings.HasSuffix(res.stdout, "installed model: stories260K.gguf\n"+"make it the default with: diparnux model default stories260K.gguf\n") {
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
		{"unauthorized", "unauthorized", "repository not found, or it is private (set a token: diparnux config set hf-token)"},
		{"gated", "gated", "accept its terms, then set a token: diparnux config set hf-token"},
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
	wantContains(t, "interrupt", res.stdout, "Interrupted.", "The job keeps running in diparnuxd.", "diparnux model pull")
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
	wantContains(t, "no-wait", res.stdout, "server state: starting", "check it with: diparnux status")
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
	wantContains(t, "lan", res.stderr, "LAN exposure needs an API key. Create one first: diparnux apikey create")
	if got := f.bodiesOf("PUT /v1/config"); len(got) != 1 || got[0] != `{"lan_exposed":true}` {
		t.Fatalf("config body = %v", got)
	}
}

func TestConfigSetPortAndRestartNotice(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("PUT /v1/config", 200, `{"config":{"port":9090,"lan_exposed":false,"autostart":true,"models_dir":"/var/lib/diparnux/models","hf_token":"","overrides":{}},"restart_required":true}`)
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
	wantContains(t, "hf-token", res.stdout, "hf-token saved", "restart the daemon", "sudo systemctl restart diparnuxd")
	if strings.Contains(res.stdout, "diparnux serve stop") {
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
	f.on("GET /v1/config", 200, `{"port":8080,"lan_exposed":true,"autostart":true,"models_dir":"/var/lib/diparnux/models","hf_token":"<redacted>","overrides":{"ngl":"all","ctx":4096}}`)
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
	const key = "llx_test-created-key"
	f := newFakeDaemon(t)
	f.on("POST /v1/apikeys", 201, fmt.Sprintf(`{"id":"k1","label":"laptop","prefix":"llx_test","created":"2026-10-11T09:00:00Z","key":%q,"note":"the key is shown only once; store it now","restart_required":true}`, key))
	res := runCLI(t, f, "apikey", "create", "laptop")
	wantCode(t, res, exitOK)
	wantContains(t, "create", res.stdout, "API key created.", key, "shown only once", "restart the server for the new API key to take effect", "diparnux serve stop, then diparnux serve start")
	if strings.Count(res.stdout, key) != 1 {
		t.Fatal("running-server key creation did not show the key exactly once")
	}
}

func TestAPIKeyCreateJSONKeepsRestartContract(t *testing.T) {
	const key = "llx_json-created-key"
	f := newFakeDaemon(t)
	f.on("POST /v1/apikeys", 201, fmt.Sprintf(`{"id":"k1","label":"laptop","prefix":"llx_json","created":"2026-10-11T09:00:00Z","key":%q,"note":"the key is shown only once; store it now","restart_required":true}`, key))
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
		_, _ = w.Write([]byte("diparnux report\nversion: 0.2.0\n"))
	})
	res := runCLI(t, f, "report")
	wantCode(t, res, exitOK)
	if res.stdout != "diparnux report\nversion: 0.2.0\n" {
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
	if res.stdout != "diparnux dev\n" {
		t.Fatalf("version = %q", res.stdout)
	}
	res = runCLI(t, nil, "help")
	wantCode(t, res, exitOK)
	wantContains(t, "help", res.stdout, "Usage: diparnux", "serve start", "config set")
	res = runCLI(t, nil, "help", "serve", "start")
	wantCode(t, res, exitOK)
	wantContains(t, "help serve start", res.stdout, "Usage: diparnux serve start", "--no-wait")
	res = runCLI(t, nil, "serve", "start", "-h")
	wantCode(t, res, exitOK)
	wantContains(t, "serve start -h", res.stdout, "Usage: diparnux serve start", "Flags:", "-ngl")
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
		{"permission", fmt.Errorf("%w: connect", client.ErrPermission), exitUnreachable, "permission denied: add your user to the 'diparnux' group or run with sudo"},
		{"unavailable", fmt.Errorf("%w: connect", client.ErrUnavailable), exitUnreachable, "diparnuxd is not running (systemctl status diparnuxd)"},
		{"deadline", context.DeadlineExceeded, exitFailure, "timed out waiting for diparnuxd"},
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
