package power

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/platform"
	"github.com/nxodh/operetaos/daemon/internal/runner/runnertest"
)

// logindFake 는 logind 가 모두 "ignore"/"no" 로 답하는 가짜 러너를 만든다.
func logindFake(ignore, can string) *runnertest.Fake {
	f := &runnertest.Fake{}
	f.On("busctl", []string{"--json=short", "get-property"}, platform.Result{Stdout: []byte(`{"type":"s","data":"` + ignore + `"}` + "\n")}, nil)
	f.On("busctl", []string{"--json=short", "call"}, platform.Result{Stdout: []byte(`{"type":"s","data":["` + can + `"]}` + "\n")}, nil)
	f.On("systemctl", []string{"kill"}, platform.Result{}, nil)
	return f
}

func newManager(t *testing.T, f *runnertest.Fake) *Manager {
	t.Helper()
	dir := t.TempDir()
	return &Manager{
		Runner:      f,
		SystemdEtc:  filepath.Join(dir, "etc-systemd"),
		PowerSupply: filepath.Join(dir, "ps"),
		LidGlob:     filepath.Join(dir, "lid", "*", "state"),
		ProcUptime:  filepath.Join(dir, "uptime"),
	}
}

func TestStatusAlwaysOn(t *testing.T) {
	m := newManager(t, logindFake("ignore", "no"))
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.AlwaysOn || !st.Enabled {
		t.Fatalf("AlwaysOn=%v Enabled=%v checks=%+v", st.AlwaysOn, st.Enabled, st.Checks)
	}
	if len(st.Checks) != len(logindWant)+len(canMethods) {
		t.Fatalf("checks = %d", len(st.Checks))
	}
}

func TestStatusNaCountsAsNo(t *testing.T) {
	m := newManager(t, logindFake("ignore", "na"))
	st, err := m.Status(context.Background())
	if err != nil || !st.AlwaysOn {
		t.Fatalf("AlwaysOn=%v err=%v", st.AlwaysOn, err)
	}
}

func TestStatusDetectsOverride(t *testing.T) {
	// 다른 설정이 값을 덮어 logind 가 suspend 라고 답하면 AlwaysOn 은 거짓이고 이유가 남는다.
	m := newManager(t, logindFake("suspend", "yes"))
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.AlwaysOn {
		t.Fatalf("AlwaysOn must be false: %+v", st.Checks)
	}
	if !strings.Contains(strings.Join(st.Notes, " "), "overrides") {
		t.Fatalf("notes = %v", st.Notes)
	}
}

func TestStatusUnavailableWithoutLogind(t *testing.T) {
	f := &runnertest.Fake{}
	f.On("busctl", nil, platform.Result{ExitCode: 1, Stderr: []byte("no bus")}, nil)
	m := newManager(t, f)
	if _, err := m.Status(context.Background()); err == nil {
		t.Fatal("expected an error when logind cannot be queried")
	}
}

func TestSetOffMasksAndOnRestores(t *testing.T) {
	f := logindFake("ignore", "no")
	m := newManager(t, f)
	ctx := context.Background()

	if _, err := m.Set(ctx, false); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.overridePaths() {
		target, err := os.Readlink(p)
		if err != nil || target != "/dev/null" {
			t.Fatalf("%s -> %q, %v", p, target, err)
		}
	}
	if m.enabled() {
		t.Fatal("enabled() must be false after Set(false)")
	}

	if _, err := m.Set(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, p := range m.overridePaths() {
		if _, err := os.Lstat(p); !os.IsNotExist(err) {
			t.Fatalf("%s must be removed: %v", p, err)
		}
	}
	reloads := 0
	for _, c := range f.Calls() {
		if c.Name == "systemctl" && strings.Join(c.Args, " ") == "kill --kill-whom=main --signal=HUP systemd-logind.service" {
			reloads++
		}
	}
	if reloads != 2 {
		t.Fatalf("logind reloads = %d, want 2", reloads)
	}
}

func TestPowerSupplyAndLid(t *testing.T) {
	m := newManager(t, logindFake("ignore", "no"))
	write := func(p, s string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(m.PowerSupply, "BAT0", "type"), "Battery\n")
	write(filepath.Join(m.PowerSupply, "BAT0", "capacity"), "37\n")
	write(filepath.Join(m.PowerSupply, "AC", "type"), "Mains\n")
	write(filepath.Join(m.PowerSupply, "AC", "online"), "0\n")
	write(filepath.Join(filepath.Dir(m.LidGlob), "..", "LID0", "state"), "state:      closed\n")
	write(m.ProcUptime, "7384.52 11111.00\n")

	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !st.HasBattery || !st.OnBattery || st.BatteryPercent != 37 {
		t.Fatalf("battery: %+v", st)
	}
	if st.Lid != "closed" {
		t.Fatalf("lid = %q", st.Lid)
	}
	if st.UptimeSeconds != 7384 {
		t.Fatalf("uptime = %d", st.UptimeSeconds)
	}
	if !strings.Contains(strings.Join(st.Notes, " "), "battery") {
		t.Fatalf("expected a battery note: %v", st.Notes)
	}
}

func TestBusctlString(t *testing.T) {
	for _, c := range []struct{ in, want string }{
		{`{"type":"s","data":"ignore"}`, "ignore"},
		{`{"type":"s","data":["no"]}`, "no"},
	} {
		if got, ok := busctlString([]byte(c.in)); !ok || got != c.want {
			t.Errorf("%s -> %q, %v", c.in, got, ok)
		}
	}
	if _, ok := busctlString([]byte(`not json`)); ok {
		t.Error("garbage must not parse")
	}
}
