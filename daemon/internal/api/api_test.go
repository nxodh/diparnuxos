package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/apikeys"
	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/gateway"
	"github.com/nxodh/diparnuxos/daemon/internal/hf"
	"github.com/nxodh/diparnuxos/daemon/internal/models"
	"github.com/nxodh/diparnuxos/daemon/internal/network"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
	"github.com/nxodh/diparnuxos/daemon/internal/update"
	"github.com/nxodh/diparnuxos/daemon/internal/updatectl"
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
	ssh         supervisor.SSHStatus
	sshErr      error
	sshUnitErr  error
	sshUnitSet  []bool
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

func (f *fakeSup) SSHStatus(ctx context.Context) (supervisor.SSHStatus, error) {
	return f.ssh, f.sshErr
}

func (f *fakeSup) SetSSHUnit(ctx context.Context, enable bool) error {
	f.sshUnitSet = append(f.sshUnitSet, enable)
	return f.sshUnitErr
}

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
	return newEnvWith(t, sup, nil)
}

// newEnvWith는 newEnv 와 같되 mutate 로 의존성을 바꿀 수 있다.
func newEnvWith(t *testing.T, sup *fakeSup, mutate func(*Deps)) *env {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "diparnuxd.json")
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
	deps := Deps{
		FirstBootPath: filepath.Join(dir, "firstboot.json"),
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
	}
	if mutate != nil {
		mutate(&deps)
	}
	e.h = New(deps)
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

func TestConcurrentConfigPatchesKeepBothChanges(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	codes := make(chan int, 2)
	requests := []string{`{"port":9091}`, `{"autostart":false}`}
	for _, body := range requests {
		body := body
		go func() {
			ready <- struct{}{}
			<-start
			codes <- e.do("PUT", "/v1/config", body).Code
		}()
	}
	<-ready
	<-ready
	close(start)
	first, second := <-codes, <-codes
	if first != http.StatusOK || second != http.StatusOK {
		t.Fatalf("concurrent config statuses = %d, %d", first, second)
	}
	saved, err := config.Load(e.cfgPath)
	if err != nil {
		t.Fatal("load saved config:", err)
	}
	if saved.Port != 9091 || saved.Autostart {
		t.Fatalf("independent concurrent config updates were lost: port=%d autostart=%v", saved.Port, saved.Autostart)
	}
}

func TestConcurrentLastKeyDeletesKeepLANProtected(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	cfg, err := config.Load(e.cfgPath)
	if err != nil {
		t.Fatal("load config:", err)
	}
	cfg.LANExposed = true
	if err := config.Save(e.cfgPath, cfg); err != nil {
		t.Fatal("enable LAN in fixture:", err)
	}
	first, _, err := e.keys.Create("first")
	if err != nil {
		t.Fatal("create first key:", err)
	}
	second, _, err := e.keys.Create("second")
	if err != nil {
		t.Fatal("create second key:", err)
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	codes := make(chan int, 2)
	for _, id := range []string{first.ID, second.ID} {
		id := id
		go func() {
			ready <- struct{}{}
			<-start
			codes <- e.do("DELETE", "/v1/apikeys/"+id, "").Code
		}()
	}
	<-ready
	<-ready
	close(start)
	codeA, codeB := <-codes, <-codes
	wins := map[int]int{codeA: 1}
	wins[codeB]++
	if wins[http.StatusOK] != 1 || wins[http.StatusConflict] != 1 {
		t.Fatalf("concurrent last-key delete statuses = %d, %d", codeA, codeB)
	}
	count, err := e.keys.Count()
	if err != nil || count != 1 {
		t.Fatalf("keys after concurrent deletes = %d, %v; want one key", count, err)
	}
}

func TestConcurrentLANEnableAndLastKeyDeletePreserveInvariant(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	key, _, err := e.keys.Create("only")
	if err != nil {
		t.Fatal("create key:", err)
	}
	start := make(chan struct{})
	ready := make(chan struct{}, 2)
	putCode := make(chan int, 1)
	deleteCode := make(chan int, 1)
	go func() {
		ready <- struct{}{}
		<-start
		putCode <- e.do("PUT", "/v1/config", `{"lan_exposed":true}`).Code
	}()
	go func() {
		ready <- struct{}{}
		<-start
		deleteCode <- e.do("DELETE", "/v1/apikeys/"+key.ID, "").Code
	}()
	<-ready
	<-ready
	close(start)
	putStatus, deleteStatus := <-putCode, <-deleteCode
	cfg, err := config.Load(e.cfgPath)
	if err != nil {
		t.Fatal("load resulting config:", err)
	}
	count, err := e.keys.Count()
	if err != nil {
		t.Fatal("count resulting keys:", err)
	}
	if cfg.LANExposed && count == 0 {
		t.Fatalf("concurrent requests violated the LAN key invariant: PUT=%d DELETE=%d", putStatus, deleteStatus)
	}
	if (putStatus != http.StatusOK && putStatus != http.StatusConflict) || (deleteStatus != http.StatusOK && deleteStatus != http.StatusConflict) {
		t.Fatalf("concurrent LAN enable/delete statuses = %d, %d", putStatus, deleteStatus)
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
	if !strings.HasPrefix(created.Key, "dpx_") || e.chowns != 1 {
		t.Fatalf("key = %q, chowns = %d", created.Key, e.chowns)
	}
	list := e.do("GET", "/v1/apikeys", "")
	wantStatus(t, list, http.StatusOK)
	if strings.Contains(list.Body.String(), created.Key) || !strings.Contains(list.Body.String(), created.ID) {
		t.Fatalf("list = %s", list.Body.String())
	}
}

func TestKeyCreateRestartRequiredMatchesServerState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		state   supervisor.State
		pid     int
		unit    bool
		gateway bool
		want    bool
	}{
		{name: "verified stopped", state: supervisor.StateStopped, want: false},
		{name: "starting", state: supervisor.StateStarting, want: true},
		{name: "running", state: supervisor.StateRunning, want: true},
		{name: "CPU fallback", state: supervisor.StateRunningCPU, want: true},
		{name: "failed without process", state: supervisor.StateFailed, want: false},
		{name: "failed with active process", state: supervisor.StateFailed, pid: 4242, want: true},
		{name: "unit unverifiable", state: supervisor.StateFailed, unit: true, want: true},
		{name: "gateway reads keys live", state: supervisor.StateRunning, gateway: true, want: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st := supervisor.Status{State: tc.state, PID: tc.pid, UnitUnverifiable: tc.unit}
			if tc.gateway {
				st.Port, st.BackendPort = 8080, 18080
			}
			e := newEnvWith(t, &fakeSup{status: st}, func(d *Deps) {
				if tc.gateway {
					d.Gateway = fakeGateway{}
				}
			})
			w := e.do("POST", "/v1/apikeys", `{"label":"laptop"}`)
			wantStatus(t, w, http.StatusCreated)
			var response struct {
				Key             string `json:"key"`
				RestartRequired bool   `json:"restart_required"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
				t.Fatal("decode create response:", err)
			}
			if response.RestartRequired != tc.want {
				t.Fatalf("restart_required = %v, want %v", response.RestartRequired, tc.want)
			}
			if response.Key == "" || strings.Count(w.Body.String(), response.Key) != 1 {
				t.Fatal("plaintext key was missing or appeared more than once")
			}
		})
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

func TestDeleteRetryRepairsRevocationMetadataWithoutOpeningLAN(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run as an unprivileged user to exercise directory permissions")
	}
	sup := &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}
	e := newEnv(t, sup)
	metaDir := filepath.Join(e.dir, "metadata")
	if err := os.Mkdir(metaDir, 0o700); err != nil {
		t.Fatal("make metadata directory:", err)
	}
	e.keys.MetaFile = filepath.Join(metaDir, "keys.json")
	revoked, _, err := e.keys.Create("revoked")
	if err != nil {
		t.Fatal("create revoked key:", err)
	}
	remaining, _, err := e.keys.Create("remaining")
	if err != nil {
		t.Fatal("create remaining key:", err)
	}
	if err := os.Chmod(metaDir, 0o500); err != nil {
		t.Fatal("remove metadata write permission:", err)
	}
	defer os.Chmod(metaDir, 0o700)
	w := e.do("DELETE", "/v1/apikeys/"+revoked.ID, "")
	wantStatus(t, w, http.StatusInternalServerError)
	if err := os.Chmod(metaDir, 0o700); err != nil {
		t.Fatal("restore metadata write permission:", err)
	}
	if _, err := e.keys.Snapshot(); err == nil {
		t.Fatal("snapshot accepted stale metadata after key revocation")
	}

	// 시스템 서비스 상태를 검증할 수 없을 때는 메타 복구를 보류한다.
	sup.status.UnitUnverifiable = true
	w = e.do("DELETE", "/v1/apikeys/"+revoked.ID, "")
	wantStatus(t, w, http.StatusInternalServerError)
	if errCode(t, w) != "api_key_store_inconsistent" {
		t.Fatal("inconsistent key store was repairable while the server unit was unverifiable")
	}
	sup.status.UnitUnverifiable = false

	// LAN이 켜진 동안에는 불일치 복구를 거부한다.
	cfg, err := config.Load(e.cfgPath)
	if err != nil {
		t.Fatal("load config:", err)
	}
	cfg.LANExposed = true
	if err := config.Save(e.cfgPath, cfg); err != nil {
		t.Fatal("enable LAN in fixture:", err)
	}
	sup.status.LANExposed = true
	w = e.do("DELETE", "/v1/apikeys/"+revoked.ID, "")
	wantStatus(t, w, http.StatusInternalServerError)
	if errCode(t, w) != "api_key_store_inconsistent" {
		t.Fatal("inconsistent key store was not rejected while LAN was enabled")
	}

	// 먼저 LAN을 끄고 같은 ID 삭제를 재시도하면 유령 메타만 정리한다.
	sup.status.LANExposed = false
	w = e.do("PUT", "/v1/config", `{"lan_exposed":false}`)
	wantStatus(t, w, http.StatusOK)
	w = e.do("DELETE", "/v1/apikeys/"+revoked.ID, "")
	wantStatus(t, w, http.StatusOK)
	count, err := e.keys.Count()
	if err != nil || count != 1 {
		t.Fatalf("key count after metadata repair = %d, %v", count, err)
	}
	list, err := e.keys.List()
	if err != nil || len(list) != 1 || list[0].ID != remaining.ID {
		t.Fatal("metadata retry did not leave the remaining key manageable")
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

func TestModelDeleteBlocksActiveOrUnidentifiedFailedUnits(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status supervisor.Status
		code   string
	}{
		{
			name:   "failed unit still owns requested model",
			status: supervisor.Status{State: supervisor.StateFailed, ModelID: "m.gguf", PID: 4242},
			code:   "model_in_use",
		},
		{
			name:   "active process has unknown model",
			status: supervisor.Status{State: supervisor.StateFailed, PID: 4242},
			code:   "server_state_unverifiable",
		},
		{
			name:   "unit identity is unverifiable",
			status: supervisor.Status{State: supervisor.StateStopped, UnitUnverifiable: true},
			code:   "server_state_unverifiable",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t, &fakeSup{status: tc.status})
			path := filepath.Join(e.dir, "m.gguf")
			content := []byte("GGUF active model fixture")
			if err := os.WriteFile(path, content, 0o644); err != nil {
				t.Fatal("write model fixture:", err)
			}
			w := e.do("DELETE", "/v1/models/m.gguf", "")
			wantStatus(t, w, http.StatusConflict)
			if errCode(t, w) != tc.code {
				t.Fatalf("code = %s, want %s", errCode(t, w), tc.code)
			}
			stillPresent, err := os.ReadFile(path)
			if err != nil || !bytes.Equal(stillPresent, content) {
				t.Fatal("API removed model bytes while the unit state was active or unknown")
			}
		})
	}
}

func TestModelDeleteAllowsVerifiedStoppedUnit(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	path := filepath.Join(e.dir, "m.gguf")
	if err := os.WriteFile(path, []byte("GGUF stopped model fixture"), 0o644); err != nil {
		t.Fatal("write model fixture:", err)
	}
	w := e.do("DELETE", "/v1/models/m.gguf", "")
	wantStatus(t, w, http.StatusOK)
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("verified stopped model still exists: %v", err)
	}
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

// fakeUpdates 는 UpdateController 의 가짜다.
type fakeUpdates struct {
	info    updatectl.Info
	startFn func(action string, reboot bool) error
	actions []string
	reboots []bool
}

func (f *fakeUpdates) Info(context.Context) (updatectl.Info, error) { return f.info, nil }

func (f *fakeUpdates) Start(_ context.Context, action string, reboot bool) error {
	f.actions = append(f.actions, action)
	f.reboots = append(f.reboots, reboot)
	if f.startFn != nil {
		return f.startFn(action, reboot)
	}
	return nil
}

func TestUpdateWithoutControllerIsUnavailable(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	for _, req := range [][2]string{{"GET", ""}, {"POST", ""}} {
		w := e.do(req[0], "/v1/update", req[1])
		wantStatus(t, w, http.StatusServiceUnavailable)
		if errCode(t, w) != "update_unavailable" {
			t.Fatalf("%s code = %s", req[0], errCode(t, w))
		}
	}
}

func TestUpdateStatusAndStart(t *testing.T) {
	fu := &fakeUpdates{info: updatectl.Info{Available: true, RunningKernel: "6.12.111+deb13-amd64", State: update.State{Phase: update.PhaseIdle}}}
	e := newEnvWith(t, &fakeSup{}, func(d *Deps) { d.Updates = fu })

	w := e.do("GET", "/v1/update", "")
	wantStatus(t, w, http.StatusOK)
	var got updatectl.Info
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Available || got.RunningKernel != "6.12.111+deb13-amd64" || got.State.Phase != update.PhaseIdle {
		t.Fatalf("info = %+v", got)
	}

	// 본문이 없으면 apply 다.
	w = e.do("POST", "/v1/update", "")
	wantStatus(t, w, http.StatusAccepted)
	w = e.do("POST", "/v1/update", `{"action":"check"}`)
	wantStatus(t, w, http.StatusAccepted)
	w = e.do("POST", "/v1/update", `{"action":"rollback","reboot":true}`)
	wantStatus(t, w, http.StatusAccepted)
	if strings.Join(fu.actions, ",") != "apply,check,rollback" || fu.reboots[2] != true {
		t.Fatalf("actions = %v reboots = %v", fu.actions, fu.reboots)
	}
}

func TestUpdateStartRejections(t *testing.T) {
	cases := []struct {
		name   string
		err    error
		status int
		code   string
	}{
		{"busy", &updatectl.Error{Code: update.CodeBusy, Message: "running"}, http.StatusConflict, "busy"},
		{"live", &updatectl.Error{Code: update.CodeLiveSession, Message: "live"}, http.StatusConflict, "live_session"},
		{"pending", &updatectl.Error{Code: update.CodePendingBoot, Message: "reboot"}, http.StatusConflict, "pending_reboot"},
		{"invalid", &updatectl.Error{Code: updatectl.CodeInvalidAction, Message: "bad"}, http.StatusBadRequest, "invalid_action"},
		{"missing helper", &updatectl.Error{Code: updatectl.CodeUnavailable, Message: "no helper"}, http.StatusServiceUnavailable, "update_unavailable"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fu := &fakeUpdates{startFn: func(string, bool) error { return tc.err }}
			e := newEnvWith(t, &fakeSup{}, func(d *Deps) { d.Updates = fu })
			w := e.do("POST", "/v1/update", `{"action":"apply"}`)
			wantStatus(t, w, tc.status)
			if errCode(t, w) != tc.code {
				t.Fatalf("code = %s", errCode(t, w))
			}
		})
	}
	t.Run("unknown field", func(t *testing.T) {
		e := newEnvWith(t, &fakeSup{}, func(d *Deps) { d.Updates = &fakeUpdates{} })
		w := e.do("POST", "/v1/update", `{"action":"apply","force":true}`)
		wantStatus(t, w, http.StatusBadRequest)
	})
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
	text := "DIPARNUX_GPU_STATUS=no_gpu\nDIPARNUX_GPU_PRESENT=0\nDIPARNUX_GPU_PCI_COUNT=0\nDIPARNUX_GPU_COUNT=0\n" +
		"DIPARNUX_GPU_USABLE_COUNT=0\nDIPARNUX_GPU_USABLE_INDEXES=\nDIPARNUX_SECURE_BOOT=unknown\n" +
		"DIPARNUX_DRIVER_VERSION=\nDIPARNUX_GPU_MESSAGE=\"no NVIDIA GPU\"\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

// fakeNetwork 는 NetworkManager 의 가짜다.
type fakeNetwork struct {
	status    network.Status
	nets      []network.WifiNetwork
	err       error
	connected []string
	forgot    []string
	power     []bool
}

func (f *fakeNetwork) Status(context.Context) (network.Status, error) { return f.status, f.err }
func (f *fakeNetwork) Scan(context.Context) error                     { return f.err }
func (f *fakeNetwork) Networks(context.Context) ([]network.WifiNetwork, error) {
	return f.nets, f.err
}
func (f *fakeNetwork) Connect(_ context.Context, ssid, pass string) error {
	f.connected = append(f.connected, ssid+":"+pass)
	return f.err
}
func (f *fakeNetwork) Disconnect(context.Context) error { return f.err }
func (f *fakeNetwork) Forget(_ context.Context, ssid string) error {
	f.forgot = append(f.forgot, ssid)
	return f.err
}
func (f *fakeNetwork) SetPowered(_ context.Context, on bool) error {
	f.power = append(f.power, on)
	return f.err
}

func TestNetworkWithoutManagerIsUnavailable(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("GET", "/v1/network", "")
	wantStatus(t, w, http.StatusServiceUnavailable)
	if errCode(t, w) != "network_unavailable" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestNetworkEndpoints(t *testing.T) {
	fn := &fakeNetwork{
		status: network.Status{Interfaces: []network.Interface{{Name: "wlan0", Kind: "wifi", State: "up"}}, Wifi: network.WifiState{Available: true, Device: "wlan0"}, Online: true},
		nets:   []network.WifiNetwork{{SSID: "Home", Security: "psk", Signal: 80}},
	}
	e := newEnvWith(t, &fakeSup{}, func(d *Deps) { d.Network = fn })

	w := e.do("GET", "/v1/network", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"wlan0"`) || !strings.Contains(w.Body.String(), `"online":true`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	wantStatus(t, e.do("POST", "/v1/network/wifi/scan", ""), http.StatusAccepted)
	w = e.do("GET", "/v1/network/wifi/networks", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"Home"`) {
		t.Fatalf("networks = %s", w.Body.String())
	}
	w = e.do("POST", "/v1/network/wifi/connect", `{"ssid":"Home","passphrase":"s3cretpassw0rd"}`)
	wantStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), "s3cretpassw0rd") {
		t.Fatal("the password must not be echoed")
	}
	if len(fn.connected) != 1 || fn.connected[0] != "Home:s3cretpassw0rd" {
		t.Fatalf("connected = %v", fn.connected)
	}
	wantStatus(t, e.do("POST", "/v1/network/wifi/disconnect", ""), http.StatusOK)
	wantStatus(t, e.do("POST", "/v1/network/wifi/forget", `{"ssid":"Home"}`), http.StatusOK)
	wantStatus(t, e.do("PUT", "/v1/network/wifi/power", `{"on":false}`), http.StatusOK)
	wantStatus(t, e.do("PUT", "/v1/network/wifi/power", `{}`), http.StatusBadRequest)
	if len(fn.forgot) != 1 || len(fn.power) != 1 || fn.power[0] {
		t.Fatalf("forgot=%v power=%v", fn.forgot, fn.power)
	}
}

func TestNetworkErrorStatuses(t *testing.T) {
	cases := []struct {
		code   string
		status int
	}{
		{network.CodeInvalidSSID, http.StatusBadRequest},
		{network.CodeInvalidPass, http.StatusBadRequest},
		{network.CodeNotFound, http.StatusNotFound},
		{network.CodeNoWifi, http.StatusConflict},
		{network.CodeIwdUnavailable, http.StatusServiceUnavailable},
		{network.CodeConnectFailed, http.StatusBadGateway},
	}
	for _, tc := range cases {
		fn := &fakeNetwork{err: &network.Error{Code: tc.code, Message: "x"}}
		e := newEnvWith(t, &fakeSup{}, func(d *Deps) { d.Network = fn })
		w := e.do("POST", "/v1/network/wifi/connect", `{"ssid":"a","passphrase":"12345678"}`)
		wantStatus(t, w, tc.status)
		if errCode(t, w) != tc.code {
			t.Errorf("%s: code = %s", tc.code, errCode(t, w))
		}
	}
}

// fakeGateway 는 게이트웨이가 있다는 사실만 필요한 시험에 쓴다.
type fakeGateway struct{}

func (fakeGateway) State() gateway.ManagerState { return gateway.ManagerState{} }
func (fakeGateway) Snapshot() gateway.Snapshot  { return gateway.Snapshot{} }

func TestToolCheckReportsWorkingAndBrokenToolCalls(t *testing.T) {
	var bodies []string
	reply := `{"choices":[{"finish_reason":"tool_calls","message":{"role":"assistant","content":null,"tool_calls":[{"id":"c1","type":"function","function":{"name":"get_time","arguments":"{\"city\":\"Seoul\"}"}}]}}]}`
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		bodies = append(bodies, string(b))
		_, _ = io.WriteString(w, reply)
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateRunning, Port: port}})

	w := e.do("POST", "/v1/server/toolcheck", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"ok":true`) || !strings.Contains(w.Body.String(), `"code":"ok"`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	if len(bodies) != 1 || !strings.Contains(bodies[0], `"tools"`) || !strings.Contains(bodies[0], `"get_time"`) {
		t.Fatalf("the probe did not offer the tool: %v", bodies)
	}

	// 글로만 답하면 도구 호출이 안 되는 모델이다.
	reply = `{"choices":[{"finish_reason":"stop","message":{"role":"assistant","content":"It is noon."}}]}`
	w = e.do("POST", "/v1/server/toolcheck", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), `"code":"no_tool_call"`) {
		t.Fatalf("text answer body = %s", w.Body.String())
	}

	// 인수가 JSON 이 아니면 쓸 수 없다.
	reply = `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"function":{"name":"get_time","arguments":"not json"}}]}}]}`
	w = e.do("POST", "/v1/server/toolcheck", "")
	if !strings.Contains(w.Body.String(), `"code":"bad_arguments"`) {
		t.Fatalf("bad arguments body = %s", w.Body.String())
	}

	// 객체로 온 인수도 받아 준다(일부 서버).
	reply = `{"choices":[{"finish_reason":"tool_calls","message":{"tool_calls":[{"function":{"name":"get_time","arguments":{"city":"Seoul"}}}]}}]}`
	w = e.do("POST", "/v1/server/toolcheck", "")
	if !strings.Contains(w.Body.String(), `"ok":true`) {
		t.Fatalf("object arguments body = %s", w.Body.String())
	}
}

func TestToolCheckTemplateErrorIsAResultNotAFailure(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"error":{"message":"tools unsupported"}}`, http.StatusInternalServerError)
	}))
	defer upstream.Close()
	port := upstream.Listener.Addr().(*net.TCPAddr).Port
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateRunning, Port: port}})
	w := e.do("POST", "/v1/server/toolcheck", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"ok":false`) || !strings.Contains(w.Body.String(), "HTTP 500") {
		t.Fatalf("body = %s", w.Body.String())
	}
}

func TestToolCheckNeedsRunningServer(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	w := e.do("POST", "/v1/server/toolcheck", "")
	wantStatus(t, w, http.StatusConflict)
}
