package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/lightlixos/daemon/internal/accounts"

	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
	"github.com/nxodh/lightlixos/daemon/internal/supervisor"
)

func TestSSHStatusReflectsConfigAndUnit(t *testing.T) {
	sup := &fakeSup{ssh: supervisor.SSHStatus{Installed: true, Active: false}}
	e := newEnv(t, sup)
	w := e.do("GET", "/v1/system/ssh", "")
	wantStatus(t, w, http.StatusOK)
	var v sshView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Enabled || !v.Installed || v.Active || v.Port != 22 || v.Warning != "" {
		t.Fatalf("view = %+v", v)
	}
}

func TestSSHEnableStartsUnitSavesConfigAndOpensFirewall(t *testing.T) {
	sup := &fakeSup{ssh: supervisor.SSHStatus{Installed: true, Active: true}}
	e := newEnv(t, sup)
	w := e.do("PUT", "/v1/system/ssh", `{"enabled":true}`)
	wantStatus(t, w, http.StatusOK)
	var v sshView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || v.Warning == "" {
		t.Fatalf("view = %+v, want enabled with a warning", v)
	}
	if len(sup.sshUnitSet) != 1 || !sup.sshUnitSet[0] || sup.firewalls != 1 {
		t.Fatalf("unit calls = %v, firewall applies = %d", sup.sshUnitSet, sup.firewalls)
	}
	cfg, err := config.Load(e.cfgPath)
	if err != nil || !cfg.SSHEnabled {
		t.Fatalf("saved config = %+v, %v", cfg, err)
	}
}

func TestSSHEnableWithoutUnitChangesNothing(t *testing.T) {
	sup := &fakeSup{sshUnitErr: serr(supervisor.CodeSSHNotInstalled, "ssh.service is not installed")}
	e := newEnv(t, sup)
	w := e.do("PUT", "/v1/system/ssh", `{"enabled":true}`)
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != supervisor.CodeSSHNotInstalled {
		t.Fatalf("code = %s", errCode(t, w))
	}
	cfg, err := config.Load(e.cfgPath)
	if err != nil || cfg.SSHEnabled {
		t.Fatalf("config must stay disabled: %+v, %v", cfg, err)
	}
	if sup.firewalls != 0 {
		t.Fatalf("firewall must not be applied, got %d", sup.firewalls)
	}
}

func TestSSHDisableClosesFirewallEvenIfUnitStopFails(t *testing.T) {
	sup := &fakeSup{ssh: supervisor.SSHStatus{Installed: true}, sshUnitErr: serr(supervisor.CodeSSHChangeFailed, "stop failed")}
	e := newEnv(t, sup)
	cfg, _ := config.Load(e.cfgPath)
	cfg.SSHEnabled = true
	if err := config.Save(e.cfgPath, cfg); err != nil {
		t.Fatal(err)
	}
	w := e.do("PUT", "/v1/system/ssh", `{"enabled":false}`)
	wantStatus(t, w, http.StatusInternalServerError)
	if errCode(t, w) != supervisor.CodeSSHChangeFailed {
		t.Fatalf("code = %s", errCode(t, w))
	}
	got, err := config.Load(e.cfgPath)
	if err != nil || got.SSHEnabled {
		t.Fatalf("config must be disabled: %+v, %v", got, err)
	}
	if sup.firewalls != 1 {
		t.Fatalf("firewall must be re-applied once, got %d", sup.firewalls)
	}
}

func TestSSHRequiresEnabledField(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("PUT", "/v1/system/ssh", `{}`)
	wantStatus(t, w, http.StatusBadRequest)
	if errCode(t, w) != "invalid_request" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestFirstBootLifecycle(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("GET", "/v1/firstboot", "")
	wantStatus(t, w, http.StatusOK)
	var st struct {
		Done    bool     `json:"done"`
		Skipped []string `json:"skipped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Done || st.Skipped == nil {
		t.Fatalf("initial = %s (%v)", w.Body.String(), err)
	}

	w = e.do("PUT", "/v1/firstboot", `{"done":true,"skipped":["model","gpu"]}`)
	wantStatus(t, w, http.StatusOK)
	var done struct {
		Done        bool     `json:"done"`
		CompletedAt string   `json:"completed_at"`
		Skipped     []string `json:"skipped"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &done); err != nil {
		t.Fatal(err)
	}
	if !done.Done || done.CompletedAt != "2026-10-11T00:00:00Z" || len(done.Skipped) != 2 || done.Skipped[0] != "gpu" {
		t.Fatalf("done = %+v", done)
	}

	w = e.do("DELETE", "/v1/firstboot", "")
	wantStatus(t, w, http.StatusOK)
	w = e.do("GET", "/v1/firstboot", "")
	if err := json.Unmarshal(w.Body.Bytes(), &st); err != nil || st.Done {
		t.Fatalf("after reset = %s (%v)", w.Body.String(), err)
	}
}

func TestFirstBootRejectsBadInput(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	for _, body := range []string{`{}`, `{"done":false}`, `{"done":true,"skipped":["bogus"]}`} {
		w := e.do("PUT", "/v1/firstboot", body)
		wantStatus(t, w, http.StatusBadRequest)
		if errCode(t, w) != "invalid_request" {
			t.Fatalf("%s: code = %s", body, errCode(t, w))
		}
	}
}

// fakeGPU 는 platform.GPUProber 의 가짜다.
type fakeGPU struct{ snap platform.GPUSnapshot }

func (f fakeGPU) Snapshot(ctx context.Context) platform.GPUSnapshot { return f.snap }

func TestRecommendationsUseVRAMWhenGPUReady(t *testing.T) {
	gpu := fakeGPU{snap: platform.GPUSnapshot{
		Available: true,
		Devices:   []platform.GPUDevice{{Index: 0, Name: "RTX 3060 Laptop", MemTotalMiB: 6144}},
	}}
	e := newEnvWith(t, &fakeSup{}, func(d *Deps) {
		d.GPU = gpu
		d.MemInfo = func() (platform.MemInfo, error) { return platform.MemInfo{TotalMiB: 16000, AvailableMiB: 12000}, nil }
	})
	writeReadyStatusFile(t, filepath.Join(e.dir, "gpu-status"))
	w := e.do("GET", "/v1/recommendations", "")
	wantStatus(t, w, http.StatusOK)
	var v struct {
		Hardware struct {
			VRAMMiB int64 `json:"vram_mib"`
			RAMMiB  int64 `json:"ram_mib"`
		} `json:"hardware"`
		Basis       string `json:"basis"`
		Recommended int    `json:"recommended"`
		Models      []struct {
			Params    string `json:"params"`
			Placement string `json:"placement"`
		} `json:"models"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Basis != "gpu" || v.Hardware.VRAMMiB != 6144 || v.Hardware.RAMMiB != 16000 {
		t.Fatalf("hardware/basis = %+v / %s", v.Hardware, v.Basis)
	}
	// 6144 MiB: 4B(2374)는 올라가고 8B(4693)는 올라가지 않는다.
	if v.Recommended < 0 || v.Models[v.Recommended].Params != "4B" || v.Models[v.Recommended].Placement != "gpu" {
		t.Fatalf("recommended = %d in %+v", v.Recommended, v.Models)
	}
}

func TestRecommendationsFallBackToRAMWithoutGPU(t *testing.T) {
	e := newEnvWith(t, &fakeSup{}, func(d *Deps) {
		d.MemInfo = func() (platform.MemInfo, error) { return platform.MemInfo{TotalMiB: 3915, AvailableMiB: 3400}, nil }
	})
	writeStatusFile(t, filepath.Join(e.dir, "gpu-status"))
	w := e.do("GET", "/v1/recommendations", "")
	wantStatus(t, w, http.StatusOK)
	var v struct {
		Basis       string `json:"basis"`
		Recommended int    `json:"recommended"`
		Hardware    struct {
			GPUState string `json:"gpu_state"`
			VRAMMiB  int64  `json:"vram_mib"`
		} `json:"hardware"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if v.Basis != "cpu" || v.Hardware.VRAMMiB != 0 || v.Hardware.GPUState != "no_gpu" {
		t.Fatalf("view = %+v", v)
	}
}

// writeReadyStatusFile 은 GPU 한 장이 ready 인 상태 파일을 쓴다.
func writeReadyStatusFile(t *testing.T, path string) {
	t.Helper()
	text := "LIGHTLIX_GPU_STATUS=ready\nLIGHTLIX_GPU_PRESENT=1\nLIGHTLIX_GPU_PCI_COUNT=1\nLIGHTLIX_GPU_COUNT=1\n" +
		"LIGHTLIX_GPU_USABLE_COUNT=1\nLIGHTLIX_GPU_USABLE_INDEXES=\"0\"\nLIGHTLIX_SECURE_BOOT=disabled\n" +
		"LIGHTLIX_DRIVER_VERSION=595.91.07\nLIGHTLIX_GPU_MESSAGE=\"ready\"\n" +
		"LIGHTLIX_GPU_0_NAME=\"RTX 3060 Laptop\"\nLIGHTLIX_GPU_0_COMPUTE_CAP=8.6\nLIGHTLIX_GPU_0_SUPPORTED=1\nLIGHTLIX_GPU_0_REASON=ok\n"
	if err := os.WriteFile(path, []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSSHWarningDependsOnPasswordAuthentication(t *testing.T) {
	cases := map[string]string{"yes": sshWarnUnknown, "no": sshWarnNoAccount, "unknown": sshWarnUnknown, "": sshWarnUnknown}
	for pw, want := range cases {
		sup := &fakeSup{ssh: supervisor.SSHStatus{Installed: true, Active: true, PasswordAuth: pw}}
		e := newEnv(t, sup)
		cfg, _ := config.Load(e.cfgPath)
		cfg.SSHEnabled = true
		if err := config.Save(e.cfgPath, cfg); err != nil {
			t.Fatal(err)
		}
		w := e.do("GET", "/v1/system/ssh", "")
		wantStatus(t, w, http.StatusOK)
		var v sshView
		if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
			t.Fatal(err)
		}
		if v.Warning != want {
			t.Fatalf("password_auth=%q: warning = %q, want %q", pw, v.Warning, want)
		}
		wantPW := pw
		if wantPW == "" {
			wantPW = "unknown"
		}
		if v.PasswordAuth != wantPW {
			t.Fatalf("password_auth=%q: view = %q", pw, v.PasswordAuth)
		}
	}
}

// fakeAccounts 는 AccountManager 의 가짜다.
type fakeAccounts struct {
	list      []accounts.Account
	addErr    error
	removeErr error
	syncErr   error
	synced    int
	added     []string
	passwords []string
}

func (f *fakeAccounts) List() ([]accounts.Account, error) { return f.list, nil }

func (f *fakeAccounts) Add(ctx context.Context, name, password string, admin bool) error {
	if f.addErr != nil {
		return f.addErr
	}
	f.added = append(f.added, name)
	f.passwords = append(f.passwords, password)
	f.list = append(f.list, accounts.Account{Name: name, Admin: admin, Created: "2026-10-12T00:00:00Z"})
	return nil
}

func (f *fakeAccounts) Remove(ctx context.Context, name string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	out := f.list[:0]
	for _, a := range f.list {
		if a.Name != name {
			out = append(out, a)
		}
	}
	f.list = out
	return nil
}

func (f *fakeAccounts) PrepareSSH(ctx context.Context) error {
	f.synced++
	return f.syncErr
}

func withAccounts(acc *fakeAccounts) func(*Deps) {
	return func(d *Deps) { d.Accounts = acc }
}

func TestUsersCreateListDelete(t *testing.T) {
	acc := &fakeAccounts{}
	e := newEnvWith(t, &fakeSup{}, withAccounts(acc))
	w := e.do("POST", "/v1/users", `{"name":"alice","password":"correct-horse-9","admin":true}`)
	wantStatus(t, w, http.StatusCreated)
	if strings.Contains(w.Body.String(), "correct-horse") {
		t.Fatalf("the response must not contain the password: %s", w.Body.String())
	}
	var created accounts.Account
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "alice" || !created.Admin || created.Created == "" {
		t.Fatalf("created = %+v", created)
	}
	if len(acc.passwords) != 1 || acc.passwords[0] != "correct-horse-9" {
		t.Fatalf("the password did not reach the manager: %v", acc.passwords)
	}

	w = e.do("GET", "/v1/users", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"alice"`) || strings.Contains(w.Body.String(), "correct-horse") {
		t.Fatalf("list = %s", w.Body.String())
	}

	w = e.do("DELETE", "/v1/users/alice", "")
	wantStatus(t, w, http.StatusOK)
	if len(acc.list) != 0 {
		t.Fatalf("list after delete = %+v", acc.list)
	}
}

func TestUsersErrorStatuses(t *testing.T) {
	cases := []struct {
		err    error
		status int
		code   string
	}{
		{&accounts.Error{Code: accounts.CodeInvalidUsername, Message: "x"}, http.StatusBadRequest, "invalid_username"},
		{&accounts.Error{Code: accounts.CodeInvalidPassword, Message: "x"}, http.StatusBadRequest, "invalid_password"},
		{&accounts.Error{Code: accounts.CodeUserExists, Message: "x"}, http.StatusConflict, "user_exists"},
		{&accounts.Error{Code: accounts.CodeAccountFailed, Message: "x"}, http.StatusInternalServerError, "account_failed"},
	}
	for _, c := range cases {
		e := newEnvWith(t, &fakeSup{}, withAccounts(&fakeAccounts{addErr: c.err}))
		w := e.do("POST", "/v1/users", `{"name":"alice","password":"correct-horse-9"}`)
		wantStatus(t, w, c.status)
		if errCode(t, w) != c.code {
			t.Fatalf("code = %s, want %s", errCode(t, w), c.code)
		}
	}
	e := newEnvWith(t, &fakeSup{}, withAccounts(&fakeAccounts{removeErr: &accounts.Error{Code: accounts.CodeUserNotFound, Message: "x"}}))
	w := e.do("DELETE", "/v1/users/ghost", "")
	wantStatus(t, w, http.StatusNotFound)
	if errCode(t, w) != "user_not_found" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestSSHEnableSyncsPolicyAndListsLoginUsers(t *testing.T) {
	acc := &fakeAccounts{list: []accounts.Account{{Name: "alice"}, {Name: "bob"}}}
	sup := &fakeSup{ssh: supervisor.SSHStatus{Installed: true, Active: true, PasswordAuth: "yes"}}
	e := newEnvWith(t, sup, withAccounts(acc))
	w := e.do("PUT", "/v1/system/ssh", `{"enabled":true}`)
	wantStatus(t, w, http.StatusOK)
	var v sshView
	if err := json.Unmarshal(w.Body.Bytes(), &v); err != nil {
		t.Fatal(err)
	}
	if acc.synced != 1 {
		t.Fatalf("policy sync calls = %d", acc.synced)
	}
	if len(v.LoginUsers) != 2 || v.LoginUsers[0] != "alice" || !strings.Contains(v.Warning, "alice, bob") {
		t.Fatalf("view = %+v", v)
	}
}

func TestSSHEnableStopsWhenPolicySyncFails(t *testing.T) {
	acc := &fakeAccounts{syncErr: &accounts.Error{Code: accounts.CodeSSHConfig, Message: "sshd rejected the configuration"}}
	sup := &fakeSup{ssh: supervisor.SSHStatus{Installed: true}}
	e := newEnvWith(t, sup, withAccounts(acc))
	w := e.do("PUT", "/v1/system/ssh", `{"enabled":true}`)
	wantStatus(t, w, http.StatusInternalServerError)
	if len(sup.sshUnitSet) != 0 {
		t.Fatal("the unit must not be enabled when the policy could not be applied")
	}
	cfg, _ := config.Load(e.cfgPath)
	if cfg.SSHEnabled {
		t.Fatal("config must stay disabled")
	}
}

func TestWizardAcceptsAccountStep(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("PUT", "/v1/firstboot", `{"done":true,"skipped":["account"]}`)
	wantStatus(t, w, http.StatusOK)
}
