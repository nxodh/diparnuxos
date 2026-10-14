package updatectl

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/platform"
	"github.com/nxodh/operetaos/daemon/internal/update"
)

type fakeRunner struct {
	active bool
	calls  []platform.Command
	runErr error
	exit   int
}

func (f *fakeRunner) Run(_ context.Context, cmd platform.Command) (platform.Result, error) {
	f.calls = append(f.calls, cmd)
	if cmd.Name == "systemctl" {
		if f.active {
			return platform.Result{}, nil
		}
		return platform.Result{ExitCode: 3}, nil
	}
	if f.runErr != nil {
		return platform.Result{}, f.runErr
	}
	return platform.Result{ExitCode: f.exit, Stderr: []byte("denied")}, nil
}

func newController(t *testing.T, r *fakeRunner) *Controller {
	t.Helper()
	dir := t.TempDir()
	return &Controller{
		Runner:    r,
		StateDir:  filepath.Join(dir, "update"),
		LiveDir:   filepath.Join(dir, "live"),
		OSRelease: filepath.Join(dir, "osrelease"),
		// 호스트의 os-release 와 소스 파일을 읽지 않도록 임시 경로를 쓴다.
		OSReleaseFile: filepath.Join(dir, "os-release"),
		SourcesFile:   filepath.Join(dir, "opereta.sources"),
		HelperExists:  func(string) bool { return true },
	}
}

func TestInfoReadsStateAndCheck(t *testing.T) {
	c := newController(t, &fakeRunner{})
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	must := func(name, body string) {
		if err := os.WriteFile(filepath.Join(c.StateDir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	must("state.json", `{"phase":"pending_boot","new_kernel":"6.12.112+deb13-amd64","reboot_required":true}`)
	must("check.json", `{"time":"2026-10-12T07:00:00Z","plan":{"up_to_date":true,"changes":[]}}`)
	if err := os.WriteFile(c.OSRelease, []byte("6.12.111+deb13-amd64\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Available || info.State.Phase != update.PhasePendingBoot || info.State.NewKernel != "6.12.112+deb13-amd64" || !info.State.RebootRequired {
		t.Fatalf("info = %+v", info)
	}
	if info.RunningKernel != "6.12.111+deb13-amd64" || info.LastCheck == nil || !info.LastCheck.Plan.UpToDate {
		t.Fatalf("info = %+v", info)
	}
}

func TestInfoWithoutStateIsIdle(t *testing.T) {
	c := newController(t, &fakeRunner{})
	info, err := c.Info(context.Background())
	if err != nil || info.State.Phase != update.PhaseIdle || info.LastCheck != nil {
		t.Fatalf("info = %+v err = %v", info, err)
	}
}

func TestInfoMarksInterruptedRunAsFailed(t *testing.T) {
	c := newController(t, &fakeRunner{active: false})
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.StateDir, "state.json"), []byte(`{"phase":"running","step":"kernel"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	info, _ := c.Info(context.Background())
	if info.State.Phase != update.PhaseFailed || info.State.Code != update.CodeInternal {
		t.Fatalf("state = %+v", info.State)
	}
	// 유닛이 실제로 돌고 있으면 그대로 running 이다.
	c.Runner = &fakeRunner{active: true}
	info, _ = c.Info(context.Background())
	if info.State.Phase != update.PhaseRunning || !info.Running {
		t.Fatalf("state = %+v running = %v", info.State, info.Running)
	}
}

func TestInfoLiveAndMissingHelper(t *testing.T) {
	c := newController(t, &fakeRunner{})
	if err := os.MkdirAll(c.LiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	info, _ := c.Info(context.Background())
	if info.Available || !info.Live || info.Reason != update.CodeLiveSession {
		t.Fatalf("live info = %+v", info)
	}
	c = newController(t, &fakeRunner{})
	c.HelperExists = func(string) bool { return false }
	info, _ = c.Info(context.Background())
	if info.Available || info.Reason != CodeUnavailable {
		t.Fatalf("no-helper info = %+v", info)
	}
}

func TestStartRunsTransientUnit(t *testing.T) {
	r := &fakeRunner{}
	c := newController(t, r)
	if err := c.Start(context.Background(), ActionApply, true); err != nil {
		t.Fatal(err)
	}
	var run platform.Command
	for _, cmd := range r.calls {
		if cmd.Name == "systemd-run" {
			run = cmd
		}
	}
	args := strings.Join(run.Args, " ")
	for _, want := range []string{"--unit=opereta-update-run", "--collect", "--service-type=exec", DefaultHelper + " apply --json --reboot"} {
		if !strings.Contains(args, want) {
			t.Errorf("systemd-run args %q miss %q", args, want)
		}
	}
	// check 에는 --reboot 를 붙이지 않는다.
	r.calls = nil
	if err := c.Start(context.Background(), ActionCheck, true); err != nil {
		t.Fatal(err)
	}
	for _, cmd := range r.calls {
		if cmd.Name == "systemd-run" && strings.Contains(strings.Join(cmd.Args, " "), "--reboot") {
			t.Error("check must not reboot")
		}
	}
}

func TestStartRefusals(t *testing.T) {
	code := func(err error) string {
		var e *Error
		if errors.As(err, &e) {
			return e.Code
		}
		return "none"
	}
	c := newController(t, &fakeRunner{})
	if got := code(c.Start(context.Background(), "format", false)); got != CodeInvalidAction {
		t.Errorf("invalid action: %s", got)
	}

	c = newController(t, &fakeRunner{active: true})
	if got := code(c.Start(context.Background(), ActionApply, false)); got != update.CodeBusy {
		t.Errorf("busy: %s", got)
	}

	c = newController(t, &fakeRunner{})
	if err := os.MkdirAll(c.LiveDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if got := code(c.Start(context.Background(), ActionApply, false)); got != update.CodeLiveSession {
		t.Errorf("live: %s", got)
	}

	c = newController(t, &fakeRunner{})
	c.HelperExists = func(string) bool { return false }
	if got := code(c.Start(context.Background(), ActionApply, false)); got != CodeUnavailable {
		t.Errorf("helper: %s", got)
	}

	c = newController(t, &fakeRunner{})
	if err := os.MkdirAll(c.StateDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.StateDir, "state.json"), []byte(`{"phase":"pending_boot"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := code(c.Start(context.Background(), ActionApply, false)); got != update.CodePendingBoot {
		t.Errorf("pending: %s", got)
	}
	// 롤백은 pending_boot 에서도 시작할 수 있다.
	if got := code(c.Start(context.Background(), ActionRollback, false)); got != "none" {
		t.Errorf("rollback while pending: %s", got)
	}

	c = newController(t, &fakeRunner{exit: 1})
	if got := code(c.Start(context.Background(), ActionApply, false)); got != CodeUnavailable {
		t.Errorf("systemd-run failure: %s", got)
	}
}

func TestInfoReportsOSVersionAndChannel(t *testing.T) {
	c := newController(t, &fakeRunner{})
	if err := os.WriteFile(c.OSReleaseFile, []byte("NAME=\"Opereta\"\nID=opereta\nVERSION_ID=\"0.6.0\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 소스 파일이 없으면 채널은 꺼져 있다.
	info, err := c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if info.OSVersion != "0.6.0" || info.Channel.Enabled || info.Channel.Reason != update.ChannelMissing {
		t.Fatalf("info = %+v", info)
	}
	if err := os.WriteFile(c.SourcesFile, []byte("Types: deb\nURIs: https://example.invalid/latest/download\nSuites: ./\nEnabled: yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	info, err = c.Info(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !info.Channel.Enabled || info.Channel.URL != "https://example.invalid/latest/download" {
		t.Fatalf("channel = %+v", info.Channel)
	}
}
