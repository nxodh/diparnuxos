package api

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/apikeys"
	"github.com/nxodh/lightlixos/daemon/internal/autoconf"
	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/hf"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
	"github.com/nxodh/lightlixos/daemon/internal/supervisor"
)

// fakeSup는 Supervisor 의 가짜다. 호출 결과를 필드로 정한다.
type fakeSup struct {
	status      supervisor.Status
	startErr    error
	waitStatus  supervisor.Status
	waitErr     error
	stopErr     error
	planErr     error
	planRes     supervisor.PlanResult
	logs        string
	logsErr     error
	firewalls   int
	fwErr       error
	mode        string
	modeErr     error
	setModeErr  error
	lastStart   supervisor.StartRequest
	startCalled bool
}

func (f *fakeSup) Plan(ctx context.Context, modelID string, ov autoconf.Overrides) (supervisor.PlanResult, error) {
	return f.planRes, f.planErr
}

func (f *fakeSup) StartAsync(req supervisor.StartRequest) (supervisor.Status, error) {
	f.lastStart = req
	f.startCalled = true
	if f.startErr != nil {
		return supervisor.Status{}, f.startErr
	}
	return supervisor.Status{State: supervisor.StateStarting}, nil
}

func (f *fakeSup) Wait(ctx context.Context) (supervisor.Status, error) {
	return f.waitStatus, f.waitErr
}

func (f *fakeSup) Stop(ctx context.Context) error { return f.stopErr }

func (f *fakeSup) Status() supervisor.Status { return f.status }

func (f *fakeSup) Logs(ctx context.Context, lines int) (string, error) { return f.logs, f.logsErr }

func (f *fakeSup) ApplyFirewall(ctx context.Context) error {
	f.firewalls++
	return f.fwErr
}

func (f *fakeSup) Mode(ctx context.Context) (string, error) { return f.mode, f.modeErr }

func (f *fakeSup) SetMode(ctx context.Context, mode string) error { return f.setModeErr }

// fakeHF는 HFClient 의 가짜다.
type fakeHF struct {
	results []hf.SearchResult
	info    hf.ModelInfo
	files   []hf.File
	err     error
}

func (f fakeHF) Search(ctx context.Context, q string, limit int) ([]hf.SearchResult, error) {
	return f.results, f.err
}

func (f fakeHF) ModelInfo(ctx context.Context, repo string) (hf.ModelInfo, error) {
	return f.info, f.err
}

func (f fakeHF) ListFiles(ctx context.Context, repo, rev string) ([]hf.File, error) {
	return f.files, f.err
}

// fakeJobs는 platform.Jobs 의 가짜다.
type fakeJobs struct {
	jobs map[string]platform.Job
}

func (f *fakeJobs) StartPull(ctx context.Context, req platform.PullRequest) (platform.Job, error) {
	j := platform.Job{ID: "job1", State: platform.JobQueued, Repo: req.Repo, File: req.File}
	f.jobs = map[string]platform.Job{"job1": j}
	return j, nil
}

func (f *fakeJobs) Get(id string) (platform.Job, bool) {
	j, ok := f.jobs[id]
	return j, ok
}

func (f *fakeJobs) List() []platform.Job {
	var out []platform.Job
	for _, j := range f.jobs {
		out = append(out, j)
	}
	return out
}

func (f *fakeJobs) Cancel(id string) error { return nil }

// fakeRunner는 platform.Runner 의 가짜다.
type fakeRunner struct {
	res platform.Result
	err error
}

func (f fakeRunner) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	return f.res, f.err
}

// env는 테스트 한 건의 API 환경이다.
type env struct {
	t       *testing.T
	h       http.Handler
	sup     *fakeSup
	dir     string
	keys    *apikeys.Store
	cfgPath string
	chowns  int
	store   models.Store
}

// newEnv는 설정 파일, 키 저장소, 모델 디렉터리를 가진 API 를 만든다.
func newEnv(t *testing.T, sup *fakeSup) *env {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "lightlixd.json")
	cfg := config.Default()
	cfg.Port = 8080
	cfg.ModelsDir = dir
	cfg.HFToken = "hf_SECRETTOKEN0123456789abcdef"
	if err := config.Save(cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	e := &env{
		t:       t,
		sup:     sup,
		dir:     dir,
		cfgPath: cfgPath,
		keys:    &apikeys.Store{KeyFile: filepath.Join(dir, "api-keys"), MetaFile: filepath.Join(dir, "api-keys.json")},
		store:   models.Store{Dir: dir},
	}
	e.h = New(Deps{
		Supervisor:    sup,
		Jobs:          &fakeJobs{},
		Store:         e.store,
		Keys:          e.keys,
		HF:            fakeHF{},
		Runner:        fakeRunner{},
		ConfigPath:    cfgPath,
		GPUStatusPath: filepath.Join(dir, "gpu-status"),
		GPUCheckPath:  "/fake/check",
		Version:       "9.9.9",
		Now:           func() time.Time { return time.Date(2026, 10, 11, 0, 0, 0, 0, time.UTC) },
		ChownKeyFile: func(path string) error {
			e.chowns++
			return nil
		},
		Report: func(ctx context.Context) string { return "report text\n" },
	})
	return e
}

// do는 요청을 보내고 응답을 돌려준다.
func (e *env) do(method, path, body string) *httptest.ResponseRecorder {
	e.t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

// errCode는 JSON 오류 응답의 코드를 돌려준다.
func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var b struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
		t.Fatalf("error body is not JSON: %q (%v)", w.Body.String(), err)
	}
	return b.Error.Code
}

// wantStatus는 응답 코드를 확인한다. 실패하면 본문을 함께 보인다.
func wantStatus(t *testing.T, w *httptest.ResponseRecorder, code int) {
	t.Helper()
	if w.Code != code {
		t.Fatalf("status = %d, want %d; body: %s", w.Code, code, w.Body.String())
	}
}

// writeKeyCount 는 키 하나를 만들고 평문을 돌려준다.
func (e *env) createKey(label string) string {
	e.t.Helper()
	_, plain, err := e.keys.Create(label)
	if err != nil {
		e.t.Fatal(err)
	}
	return plain
}

func TestUnknownPathAndWrongMethodAreJSON(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("GET", "/v1/nope", "")
	wantStatus(t, w, http.StatusNotFound)
	if errCode(t, w) != "not_found" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("404 body/header = %s / %s", w.Body.String(), w.Header().Get("Content-Type"))
	}
	w = e.do("POST", "/v1/status", "")
	wantStatus(t, w, http.StatusMethodNotAllowed)
	if errCode(t, w) != "method_not_allowed" || w.Header().Get("Allow") == "" {
		t.Fatalf("405 body = %s, Allow = %q", w.Body.String(), w.Header().Get("Allow"))
	}
}

func TestStatusHasNoSecrets(t *testing.T) {
	e := newEnv(t, &fakeSup{mode: "server", status: supervisor.Status{State: supervisor.StateStopped}})
	plain := e.createKey("laptop")
	w := e.do("GET", "/v1/status", "")
	wantStatus(t, w, http.StatusOK)
	body := w.Body.String()
	if strings.Contains(body, plain) || strings.Contains(body, "hf_SECRETTOKEN") {
		t.Fatalf("status leaks a secret: %s", body)
	}
	if !strings.Contains(body, `"version":"9.9.9"`) || !strings.Contains(body, `"mode":"server"`) {
		t.Fatalf("status body = %s", body)
	}
}

func TestConfigGetRedactsTokenAndPutKeepsRedactedValue(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("GET", "/v1/config", "")
	wantStatus(t, w, http.StatusOK)
	var shown config.Config
	if err := json.Unmarshal(w.Body.Bytes(), &shown); err != nil {
		t.Fatal(err)
	}
	if shown.HFToken != "<redacted>" || strings.Contains(w.Body.String(), "hf_SECRETTOKEN") {
		t.Fatalf("config body = %s", w.Body.String())
	}
	// 가린 값을 그대로 보내면 토큰은 바뀌지 않는다.
	w = e.do("PUT", "/v1/config", `{"hf_token":"<redacted>","autostart":false}`)
	wantStatus(t, w, http.StatusOK)
	saved, err := config.Load(e.cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if saved.HFToken != "hf_SECRETTOKEN0123456789abcdef" || saved.Autostart {
		t.Fatalf("saved config = %+v", saved)
	}
}

func TestConfigPutRejectsUnknownField(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("PUT", "/v1/config", `{"prot":1}`)
	wantStatus(t, w, http.StatusBadRequest)
	if errCode(t, w) != "invalid_json" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestConfigPutLANNeedsKeyThenAppliesFirewall(t *testing.T) {
	sup := &fakeSup{}
	e := newEnv(t, sup)
	w := e.do("PUT", "/v1/config", `{"lan_exposed":true}`)
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != "api_key_required" || sup.firewalls != 0 {
		t.Fatalf("code = %s, firewalls = %d", errCode(t, w), sup.firewalls)
	}

	e.createKey("laptop")
	w = e.do("PUT", "/v1/config", `{"lan_exposed":true,"port":9000}`)
	wantStatus(t, w, http.StatusOK)
	if sup.firewalls != 1 {
		t.Fatalf("firewall applications = %d, want 1", sup.firewalls)
	}
	if !strings.Contains(w.Body.String(), `"restart_required":true`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestConfigPutInvalidPortIsRejected(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("PUT", "/v1/config", `{"port":70000}`)
	wantStatus(t, w, http.StatusBadRequest)
	if errCode(t, w) != "invalid_config" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestKeyCreateShowsPlaintextOnceAndListHidesIt(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("POST", "/v1/apikeys", `{"label":"phone"}`)
	wantStatus(t, w, http.StatusCreated)
	var created struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(created.Key, "llx_") || e.chowns != 1 {
		t.Fatalf("key = %q, chowns = %d", created.Key, e.chowns)
	}
	list := e.do("GET", "/v1/apikeys", "")
	wantStatus(t, list, http.StatusOK)
	if strings.Contains(list.Body.String(), created.Key) || !strings.Contains(list.Body.String(), created.ID) {
		t.Fatalf("list = %s", list.Body.String())
	}
}

func TestKeyCreateRejectsBadLabel(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("POST", "/v1/apikeys", `{"label":"tab\tkey"}`)
	wantStatus(t, w, http.StatusBadRequest)
	if errCode(t, w) != "invalid_label" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestDeleteLastKeyWhileLANExposedIsConflict(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	cfg, _ := config.Load(e.cfgPath)
	cfg.LANExposed = true
	if err := config.Save(e.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	_, _, err := e.keys.Create("only")
	if err != nil {
		t.Fatal(err)
	}
	list, _ := e.keys.List()
	w := e.do("DELETE", "/v1/apikeys/"+list[0].ID, "")
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != "last_key_in_use" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestDeleteUnknownKeyIsNotFound(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("DELETE", "/v1/apikeys/"+strings.Repeat("a", 32), "")
	wantStatus(t, w, http.StatusNotFound)
}

func TestStartModes(t *testing.T) {
	sup := &fakeSup{startErr: serr(supervisor.CodeNoModel, "no model")}
	e := newEnv(t, sup)
	w := e.do("POST", "/v1/server/start", `{"model":"x.gguf"}`)
	wantStatus(t, w, http.StatusNotFound)
	if errCode(t, w) != supervisor.CodeNoModel {
		t.Fatalf("code = %s", errCode(t, w))
	}

	sup.startErr = nil
	w = e.do("POST", "/v1/server/start", `{"wait":false,"overrides":{"ngl":"auto"}}`)
	wantStatus(t, w, http.StatusAccepted)
	if sup.lastStart.Overrides.NGL == nil || *sup.lastStart.Overrides.NGL != "auto" {
		t.Fatalf("overrides not passed: %+v", sup.lastStart)
	}

	sup.waitErr = serr(supervisor.CodeGPUOffloadFailed, "offloaded 0/33")
	w = e.do("POST", "/v1/server/start", `{"wait":true}`)
	wantStatus(t, w, http.StatusInternalServerError)
	if errCode(t, w) != supervisor.CodeGPUOffloadFailed {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestStartBodyTooLargeIs413(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	big := `{"model":"` + strings.Repeat("a", 2<<20) + `"}`
	w := e.do("POST", "/v1/server/start", big)
	wantStatus(t, w, http.StatusRequestEntityTooLarge)
}

func TestLogsQueryBounds(t *testing.T) {
	e := newEnv(t, &fakeSup{logs: "hello\n"})
	w := e.do("GET", "/v1/server/logs?lines=5000", "")
	wantStatus(t, w, http.StatusBadRequest)
	w = e.do("GET", "/v1/server/logs?lines=abc", "")
	wantStatus(t, w, http.StatusBadRequest)
	w = e.do("GET", "/v1/server/logs?lines=5", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "hello") {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestPlanQueryPassesOverrides(t *testing.T) {
	sup := &fakeSup{planRes: supervisor.PlanResult{ModelID: "m.gguf", Output: autoconf.Output{NGL: "all", Ctx: 8192}}}
	e := newEnv(t, sup)
	w := e.do("GET", "/v1/server/plan?model=m.gguf&ngl=all&ctx=4096", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"ngl":"all"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	w = e.do("GET", "/v1/server/plan?bogus=1", "")
	wantStatus(t, w, http.StatusBadRequest)
}

func TestStopAndStatusFields(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("POST", "/v1/server/stop", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"state":"stopped"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestChatProxiesWithKeyAndHidesIt(t *testing.T) {
	var gotAuth atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		if !strings.Contains(string(body), `"stream":false`) {
			http.Error(w, "stream must be false", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"choices":[{"message":{"role":"assistant","content":"hi there"}}],"timings":{"predicted_n":3,"predicted_per_second":42.5}}`)
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	sup := &fakeSup{status: supervisor.Status{State: supervisor.StateRunning, Port: port}}
	e := newEnv(t, sup)
	plain := e.createKey("laptop")
	w := e.do("POST", "/v1/server/chat", `{"messages":[{"role":"user","content":"hello"}],"max_tokens":16}`)
	wantStatus(t, w, http.StatusOK)
	if auth, _ := gotAuth.Load().(string); auth != "Bearer "+plain {
		t.Fatalf("upstream Authorization = %q", auth)
	}
	body := w.Body.String()
	if strings.Contains(body, plain) || !strings.Contains(body, "hi there") || !strings.Contains(body, `"tokens_per_second":42.5`) || !strings.Contains(body, `"tokens_predicted":3`) {
		t.Fatalf("chat body = %s", body)
	}
}

func TestChatNeedsRunningServer(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	w := e.do("POST", "/v1/server/chat", `{"messages":[{"role":"user","content":"hi"}]}`)
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != "server_not_running" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestChatValidatesRole(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateRunning, Port: 1}})
	w := e.do("POST", "/v1/server/chat", `{"messages":[{"role":"tool","content":"x"}]}`)
	wantStatus(t, w, http.StatusBadRequest)
}

func TestModelDeleteInUseAndMissing(t *testing.T) {
	sup := &fakeSup{status: supervisor.Status{State: supervisor.StateRunning, ModelID: "m.gguf"}}
	e := newEnv(t, sup)
	if err := os.WriteFile(filepath.Join(e.dir, "m.gguf"), []byte("GGUF"), 0o644); err != nil {
		t.Fatal(err)
	}
	w := e.do("DELETE", "/v1/models/m.gguf", "")
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != "model_in_use" {
		t.Fatalf("code = %s", errCode(t, w))
	}
	w = e.do("DELETE", "/v1/models/nothere.gguf", "")
	wantStatus(t, w, http.StatusNotFound)
}

func TestSearchNeedsQuery(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("GET", "/v1/models/search?q=", "")
	wantStatus(t, w, http.StatusBadRequest)
	w = e.do("GET", "/v1/models/search?q=gemma&limit=500", "")
	wantStatus(t, w, http.StatusBadRequest)
}

func TestRemoteListsOnlyGGUFAndGatedFlag(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	e.h = New(Deps{
		Supervisor: e.sup,
		Jobs:       &fakeJobs{},
		Store:      e.store,
		Keys:       e.keys,
		HF: fakeHF{
			info:  hf.ModelInfo{License: "apache-2.0", Gated: hf.GateManual},
			files: []hf.File{{Path: "a.gguf", Size: 10, SHA256: "abc"}, {Path: "README.md", Size: 1}},
		},
		ConfigPath: e.cfgPath,
		Now:        time.Now,
	})
	w := e.do("GET", "/v1/models/remote?repo=ggml-org/x", "")
	wantStatus(t, w, http.StatusOK)
	body := w.Body.String()
	if strings.Contains(body, "README") || !strings.Contains(body, `"gated":true`) || !strings.Contains(body, "a.gguf") {
		t.Fatalf("remote body = %s", body)
	}
}

func TestPullReturns202AndJobEndpoints(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("POST", "/v1/models/pull", `{"repo":"ggml-org/x","file":"a.gguf"}`)
	wantStatus(t, w, http.StatusAccepted)
	w = e.do("GET", "/v1/jobs/missing", "")
	wantStatus(t, w, http.StatusNotFound)
}

func TestUpdateIsNotImplemented(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("POST", "/v1/update", "")
	wantStatus(t, w, http.StatusNotImplemented)
	if errCode(t, w) != "not_implemented" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestReportIsPlainText(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("GET", "/v1/report", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.HasPrefix(w.Header().Get("Content-Type"), "text/plain") || w.Body.String() != "report text\n" {
		t.Fatalf("report = %q (%s)", w.Body.String(), w.Header().Get("Content-Type"))
	}
}

func TestGPUCheckExitCodes(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	e.h = New(Deps{
		Supervisor:    e.sup,
		Jobs:          &fakeJobs{},
		Store:         e.store,
		Keys:          e.keys,
		HF:            fakeHF{},
		Runner:        fakeRunner{res: platform.Result{ExitCode: 3, Stderr: []byte("boom")}},
		ConfigPath:    e.cfgPath,
		GPUStatusPath: filepath.Join(e.dir, "gpu-status"),
		Now:           time.Now,
	})
	w := e.do("POST", "/v1/gpu/check", "")
	wantStatus(t, w, http.StatusInternalServerError)
	if errCode(t, w) != "gpu_check_failed" {
		t.Fatalf("code = %s", errCode(t, w))
	}

	writeStatusFile(t, filepath.Join(e.dir, "gpu-status"))
	e.h = New(Deps{
		Supervisor:    e.sup,
		Jobs:          &fakeJobs{},
		Store:         e.store,
		Keys:          e.keys,
		HF:            fakeHF{},
		Runner:        fakeRunner{res: platform.Result{ExitCode: 1}},
		ConfigPath:    e.cfgPath,
		GPUStatusPath: filepath.Join(e.dir, "gpu-status"),
		Now:           time.Now,
	})
	w = e.do("POST", "/v1/gpu/check", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"state":"no_gpu"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestModeValidation(t *testing.T) {
	sup := &fakeSup{mode: "server", setModeErr: serr(supervisor.CodeInvalidMode, "bad")}
	e := newEnv(t, sup)
	w := e.do("GET", "/v1/mode", "")
	wantStatus(t, w, http.StatusOK)
	w = e.do("POST", "/v1/mode", `{"mode":"kiosk"}`)
	wantStatus(t, w, http.StatusBadRequest)
	if errCode(t, w) != supervisor.CodeInvalidMode {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

// serr은 supervisor 오류 값을 만든다.
func serr(code, msg string) error {
	return &supervisor.Error{Code: code, Message: msg}
}

// writeStatusFile은 no_gpu 상태 파일을 쓴다.
func writeStatusFile(t *testing.T, path string) {
	t.Helper()
	text := "LIGHTLIX_GPU_STATUS=no_gpu\nLIGHTLIX_GPU_PRESENT=0\nLIGHTLIX_GPU_PCI_COUNT=0\nLIGHTLIX_GPU_COUNT=0\n" +
		"LIGHTLIX_GPU_USABLE_COUNT=0\nLIGHTLIX_GPU_USABLE_INDEXES=\nLIGHTLIX_SECURE_BOOT=unknown\n" +
		"LIGHTLIX_DRIVER_VERSION=\nLIGHTLIX_GPU_MESSAGE=\"no NVIDIA GPU\"\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}
