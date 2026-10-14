package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// newSocketServer 는 임시 디렉터리의 유닉스 소켓에 handler 를 띄우고 소켓 경로를 돌려준다.
// 소켓 경로가 짧아야 하므로 os.TempDir 바로 아래에 만든다.
func newSocketServer(t *testing.T, handler http.Handler) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "lxc")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "d.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: handler}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { _ = srv.Close() })
	return sock
}

// writeJSONHandler 는 고정 상태와 본문을 돌려주는 핸들러다.
func writeJSONHandler(status int, body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	}
}

func TestStatusDecodesAndKeepsRawBody(t *testing.T) {
	body := `{"version":"0.2.0","mode":"server","gpu":{"status":{"state":"ready","usable_count":1,"usable_indexes":[0],"gpus":[]},"status_error":"","open_module":"yes","secure_boot":"disabled","driver_version":"595.91.07","nvml":{"available":true,"devices":[{"index":0,"name":"GeForce RTX 4090","memory_total_mib":24564,"memory_used_mib":1200,"temperature_c":45,"power_mw":120000}]}},"memory":{"total_mib":15912,"available_mib":10000},"server":{"state":"running","model_id":"m.gguf","host":"127.0.0.1","port":8080,"lan_exposed":false,"unit_unverifiable":true,"offload":{"requested":"auto","offloaded":33,"total":33,"verified":true},"warnings":[],"plan":{"ngl":"33","ctx":4096,"cache_type_k":"f16","cache_type_v":"f16","flash_attn":false,"batch":2048,"visible_devices":[0],"load_mode":"mmap","fit":false,"cpu_only":false,"est_vram_mib":2000,"est_ram_mib":0,"rationale":["a"],"warnings":[],"advice":[]}}}`
	sock := newSocketServer(t, writeJSONHandler(http.StatusOK, body))
	st, raw, err := New(sock).Status(context.Background())
	if err != nil {
		t.Fatalf("Status: %v", err)
	}
	if st.Version != "0.2.0" || st.Mode != "server" {
		t.Fatalf("version/mode = %q/%q", st.Version, st.Mode)
	}
	if st.GPU.Status == nil || st.GPU.Status.State != "ready" || st.GPU.Status.UsableCount != 1 {
		t.Fatalf("gpu status = %+v", st.GPU.Status)
	}
	if len(st.GPU.NVML.Devices) != 1 || st.GPU.NVML.Devices[0].MemTotalMiB != 24564 || st.GPU.NVML.Devices[0].PowerMilliwatt != 120000 {
		t.Fatalf("devices = %+v", st.GPU.NVML.Devices)
	}
	if st.Memory == nil || st.Memory.TotalMiB != 15912 {
		t.Fatalf("memory = %+v", st.Memory)
	}
	if st.Server.State != "running" || st.Server.Port != 8080 || st.Server.Offload.Offloaded != 33 || !st.Server.Offload.Verified || !st.Server.UnitUnverifiable {
		t.Fatalf("server = %+v", st.Server)
	}
	if st.Server.Plan == nil || st.Server.Plan.NGL != "33" || st.Server.Plan.Ctx != 4096 {
		t.Fatalf("plan = %+v", st.Server.Plan)
	}
	if string(raw) != body {
		t.Fatalf("raw body was not kept as received")
	}
}

func TestCreateAPIKeyDecodesRestartRequired(t *testing.T) {
	const key = "opr_api-key-once"
	body := fmt.Sprintf(`{"id":"k1","label":"laptop","prefix":"opr_api-","created":"2026-10-11T09:00:00Z","key":%q,"note":"shown once","restart_required":true}`, key)
	created, raw, err := New(newSocketServer(t, writeJSONHandler(http.StatusCreated, body))).CreateAPIKey(context.Background(), "laptop")
	if err != nil {
		t.Fatal("CreateAPIKey:", err)
	}
	if !created.RestartRequired || created.Key != key || string(raw) != body {
		t.Fatal("CreateAPIKey did not preserve its restart contract and raw response")
	}
}

// recorder 는 요청의 메서드, 경로와 쿼리, 본문을 기록한다. 핸들러 고루틴과 테스트 고루틴이 같이 쓰므로 잠근다.
type recorder struct {
	mu     sync.Mutex
	method string
	uri    string
	body   string
}

// set 은 요청 하나를 기록한다.
func (r *recorder) set(method, uri, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.method, r.uri, r.body = method, uri, body
}

// get 은 마지막으로 기록한 요청을 돌려준다.
func (r *recorder) get() (method, uri, body string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.method, r.uri, r.body
}

func TestEveryMethodUsesItsRoute(t *testing.T) {
	ctx := context.Background()
	type call struct {
		name   string
		method string
		uri    string
		run    func(c *Client) error
	}
	n := 2
	ngl := "auto"
	cases := []call{
		{"Status", "GET", "/v1/status", func(c *Client) error { _, _, err := c.Status(ctx); return err }},
		{"GPUCheck", "POST", "/v1/gpu/check", func(c *Client) error { _, _, err := c.GPUCheck(ctx); return err }},
		{"Models", "GET", "/v1/models", func(c *Client) error { _, _, err := c.Models(ctx); return err }},
		{"DeleteModel", "DELETE", "/v1/models/a.gguf", func(c *Client) error { _, _, err := c.DeleteModel(ctx, "a.gguf"); return err }},
		{"SetDefaultModel", "PUT", "/v1/models/default", func(c *Client) error { _, _, err := c.SetDefaultModel(ctx, "a.gguf"); return err }},
		{"Search", "GET", "/v1/models/search?limit=5&q=llama+3", func(c *Client) error { _, _, err := c.Search(ctx, "llama 3", 5); return err }},
		{"RemoteFiles", "GET", "/v1/models/remote?repo=o%2Fn&revision=dev", func(c *Client) error { _, _, err := c.RemoteFiles(ctx, "o/n", "dev"); return err }},
		{"RemoteFilesDefaultRevision", "GET", "/v1/models/remote?repo=o%2Fn", func(c *Client) error { _, _, err := c.RemoteFiles(ctx, "o/n", ""); return err }},
		{"Pull", "POST", "/v1/models/pull", func(c *Client) error { _, _, err := c.Pull(ctx, PullRequest{Repo: "o/n"}); return err }},
		{"Job", "GET", "/v1/jobs/abc", func(c *Client) error { _, _, err := c.Job(ctx, "abc"); return err }},
		{"CancelJob", "DELETE", "/v1/jobs/abc", func(c *Client) error { _, _, err := c.CancelJob(ctx, "abc"); return err }},
		{"Plan", "GET", "/v1/server/plan?batch=512&ctx=8192&model=a.gguf&ngl=auto", func(c *Client) error {
			ctxv := 8192
			batch := 512
			_, _, err := c.Plan(ctx, "a.gguf", Overrides{NGL: &ngl, Ctx: &ctxv, Batch: &batch})
			return err
		}},
		{"PlanNoModel", "GET", "/v1/server/plan", func(c *Client) error { _, _, err := c.Plan(ctx, "", Overrides{}); return err }},
		{"Start", "POST", "/v1/server/start", func(c *Client) error { _, _, err := c.Start(ctx, StartRequest{}); return err }},
		{"Stop", "POST", "/v1/server/stop", func(c *Client) error { _, _, err := c.Stop(ctx); return err }},
		{"Logs", "GET", "/v1/server/logs?lines=20", func(c *Client) error { _, _, err := c.Logs(ctx, 20); return err }},
		{"Chat", "POST", "/v1/server/chat", func(c *Client) error { _, _, err := c.Chat(ctx, ChatRequest{MaxTokens: 1}); return err }},
		{"Config", "GET", "/v1/config", func(c *Client) error { _, _, err := c.Config(ctx); return err }},
		{"SetConfig", "PUT", "/v1/config", func(c *Client) error { _, _, err := c.SetConfig(ctx, ConfigPatch{Port: &n}); return err }},
		{"APIKeys", "GET", "/v1/apikeys", func(c *Client) error { _, _, err := c.APIKeys(ctx); return err }},
		{"CreateAPIKey", "POST", "/v1/apikeys", func(c *Client) error { _, _, err := c.CreateAPIKey(ctx, "cli"); return err }},
		{"DeleteAPIKey", "DELETE", "/v1/apikeys/k1", func(c *Client) error { _, _, err := c.DeleteAPIKey(ctx, "k1"); return err }},
		{"SSH", "GET", "/v1/system/ssh", func(c *Client) error { _, _, err := c.SSH(ctx); return err }},
		{"SetSSH", "PUT", "/v1/system/ssh", func(c *Client) error { _, _, err := c.SetSSH(ctx, true); return err }},
		{"Power", "GET", "/v1/system/power", func(c *Client) error { _, _, err := c.Power(ctx); return err }},
		{"SetPower", "PUT", "/v1/system/power", func(c *Client) error { _, _, err := c.SetPower(ctx, true); return err }},
		{"Users", "GET", "/v1/users", func(c *Client) error { _, _, err := c.Users(ctx); return err }},
		{"CreateUser", "POST", "/v1/users", func(c *Client) error {
			_, _, err := c.CreateUser(ctx, UserCreateRequest{Name: "alice", Password: "correct-horse-9"})
			return err
		}},
		{"DeleteUser", "DELETE", "/v1/users/alice", func(c *Client) error { _, _, err := c.DeleteUser(ctx, "alice"); return err }},
		{"Mode", "GET", "/v1/mode", func(c *Client) error { _, _, err := c.Mode(ctx); return err }},
		{"SetMode", "POST", "/v1/mode", func(c *Client) error { _, _, err := c.SetMode(ctx, "desktop"); return err }},
		{"UpdateInfo", "GET", "/v1/update", func(c *Client) error { _, _, err := c.UpdateInfo(ctx); return err }},
		{"UpdateStart", "POST", "/v1/update", func(c *Client) error {
			_, _, err := c.UpdateStart(ctx, UpdateRequest{Action: "check"})
			return err
		}},
		{"Report", "GET", "/v1/report", func(c *Client) error { _, err := c.Report(ctx); return err }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var got recorder
			h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				got.set(r.Method, r.URL.RequestURI(), string(b))
				w.Header().Set("Content-Type", "application/json")
				_, _ = io.WriteString(w, `{}`)
			})
			c := New(newSocketServer(t, h))
			_ = tc.run(c) // 응답 본문이 {} 라서 일부 메서드의 디코드 결과는 상관없다.
			method, uri, _ := got.get()
			if method != tc.method || uri != tc.uri {
				t.Fatalf("request = %s %s, want %s %s", method, uri, tc.method, tc.uri)
			}
		})
	}
}

func TestRequestBodiesAreJSON(t *testing.T) {
	ctx := context.Background()
	var mu sync.Mutex
	var bodies []string
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, r.Header.Get("Content-Type")+"|"+string(b))
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{}`)
	})
	c := New(newSocketServer(t, h))
	lan := true
	hf := "hf_x"
	ngl := "all"
	if _, _, err := c.Pull(ctx, PullRequest{Repo: "o/n", File: "a.gguf", Revision: "main"}); err != nil {
		t.Fatalf("Pull: %v", err)
	}
	if _, _, err := c.SetConfig(ctx, ConfigPatch{LANExposed: &lan, HFToken: &hf, Overrides: &Overrides{NGL: &ngl}}); err != nil {
		t.Fatalf("SetConfig: %v", err)
	}
	if _, _, err := c.Start(ctx, StartRequest{Model: "a.gguf", Overrides: Overrides{NGL: &ngl}, Wait: false}); err != nil {
		t.Fatalf("Start: %v", err)
	}
	want := []string{
		`application/json|{"repo":"o/n","file":"a.gguf","revision":"main"}`,
		`application/json|{"lan_exposed":true,"hf_token":"hf_x","overrides":{"ngl":"all"}}`,
		`application/json|{"model":"a.gguf","overrides":{"ngl":"all"},"wait":false}`,
	}
	mu.Lock()
	defer mu.Unlock()
	if strings.Join(bodies, "\n") != strings.Join(want, "\n") {
		t.Fatalf("bodies =\n%s\nwant\n%s", strings.Join(bodies, "\n"), strings.Join(want, "\n"))
	}
}

func TestAPIErrorFromJSONBody(t *testing.T) {
	sock := newSocketServer(t, writeJSONHandler(http.StatusConflict, `{"error":{"code":"api_key_required","message":"LAN exposure requires at least one API key"}}`))
	_, _, err := New(sock).Status(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) {
		t.Fatalf("error %v is not *APIError", err)
	}
	if ae.Status != http.StatusConflict || ae.Code != "api_key_required" || ae.Message != "LAN exposure requires at least one API key" {
		t.Fatalf("APIError = %+v", ae)
	}
	if errors.Is(err, ErrUnavailable) || errors.Is(err, ErrPermission) {
		t.Fatalf("API error must not match connection errors: %v", err)
	}
}

func TestAPIErrorFromPlainBody(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = io.WriteString(w, "<html>proxy</html>")
	})
	_, _, err := New(newSocketServer(t, h)).Status(context.Background())
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusBadGateway || ae.Code != "http_error" {
		t.Fatalf("error = %#v", err)
	}
}

func TestUpdateStartReportsConflict(t *testing.T) {
	sock := newSocketServer(t, writeJSONHandler(http.StatusConflict, `{"error":{"code":"busy","message":"an update operation is already running"}}`))
	_, _, err := New(sock).UpdateStart(context.Background(), UpdateRequest{Action: "apply"})
	var ae *APIError
	if !errors.As(err, &ae) || ae.Status != http.StatusConflict || ae.Code != "busy" {
		t.Fatalf("UpdateStart error = %#v", err)
	}
}

func TestReportReturnsTextAndAllowsLargerBody(t *testing.T) {
	big := strings.Repeat("a", 5<<20)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = io.WriteString(w, big)
	})
	text, err := New(newSocketServer(t, h)).Report(context.Background())
	if err != nil {
		t.Fatalf("Report: %v", err)
	}
	if len(text) != len(big) {
		t.Fatalf("report length = %d, want %d", len(text), len(big))
	}
}

func TestStatusRejectsBodyOverFourMiB(t *testing.T) {
	// 4MiB 를 넘는 본문은 ErrTooLarge 다. 리포트 상한(8MiB)은 적용되지 않는다.
	huge := `{"version":"` + strings.Repeat("a", (4<<20)+10) + `"}`
	sock := newSocketServer(t, writeJSONHandler(http.StatusOK, huge))
	_, _, err := New(sock).Status(context.Background())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Status error = %v, want ErrTooLarge", err)
	}
}

func TestReportRejectsBodyOverEightMiB(t *testing.T) {
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, strings.Repeat("b", (8<<20)+1))
	})
	_, err := New(newSocketServer(t, h)).Report(context.Background())
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("Report error = %v, want ErrTooLarge", err)
	}
}

func TestSocketMissingIsUnavailable(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "no.sock")
	_, _, err := New(missing).Status(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
	if errors.Is(err, ErrPermission) {
		t.Fatalf("missing socket must not be a permission error: %v", err)
	}
}

func TestStaleSocketIsUnavailable(t *testing.T) {
	dir, err := os.MkdirTemp("", "lxs")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	// 닫은 뒤에도 소켓 파일을 남겨 두면 연결은 거부된다.
	ln.(*net.UnixListener).SetUnlinkOnClose(false)
	_ = ln.Close()
	_, _, err = New(sock).Status(context.Background())
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want ErrUnavailable", err)
	}
}

func TestClassifyDialErrorPermission(t *testing.T) {
	cause := &net.OpError{Op: "dial", Net: "unix", Err: &os.SyscallError{Syscall: "connect", Err: syscall.EACCES}}
	err := classifyDialError(cause)
	if !errors.Is(err, ErrPermission) {
		t.Fatalf("EACCES classified as %v, want ErrPermission", err)
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("EACCES must not be ErrUnavailable: %v", err)
	}
	if !errors.Is(classifyDialError(fs.ErrPermission), ErrPermission) {
		t.Fatalf("fs.ErrPermission not classified as ErrPermission")
	}
}

func TestClassifyDialErrorKeepsContextErrors(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := classifyDialError(cause)
		if errors.Is(err, ErrUnavailable) || !errors.Is(err, cause) {
			t.Fatalf("context error %v classified as %v", cause, err)
		}
	}
}

func TestContextDeadlineIsNotUnavailable(t *testing.T) {
	release := make(chan struct{})
	t.Cleanup(func() { close(release) })
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	})
	sock := newSocketServer(t, h)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	_, _, err := New(sock).Status(ctx)
	if err == nil {
		t.Fatalf("Status returned no error after the deadline")
	}
	if errors.Is(err, ErrUnavailable) {
		t.Fatalf("deadline reported as unavailable: %v", err)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("error = %v, want DeadlineExceeded", err)
	}
}

func TestInvalidSuccessBodyIsDecodeError(t *testing.T) {
	sock := newSocketServer(t, writeJSONHandler(http.StatusOK, `not json`))
	_, raw, err := New(sock).Status(context.Background())
	if err == nil {
		t.Fatalf("expected a decode error")
	}
	var ae *APIError
	if errors.As(err, &ae) {
		t.Fatalf("decode error must not be an APIError: %v", err)
	}
	if string(raw) != "not json" {
		t.Fatalf("raw body = %q", raw)
	}
}

func TestErrorStringContainsCodeAndStatus(t *testing.T) {
	e := &APIError{Status: 404, Code: "not_found", Message: "model \"x\" was not found"}
	got := e.Error()
	want := fmt.Sprintf("%s: %s (HTTP %d)", "not_found", "model \"x\" was not found", 404)
	if got != want {
		t.Fatalf("Error() = %q, want %q", got, want)
	}
}
