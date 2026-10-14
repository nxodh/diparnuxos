package rdpctl

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

type runner struct {
	loadState  string
	active     string
	calls      []string
	failEnable bool
}

func (r *runner) Run(ctx context.Context, c platform.Command) (platform.Result, error) {
	r.calls = append(r.calls, c.Name+" "+strings.Join(c.Args, " "))
	if len(c.Args) > 0 {
		switch c.Args[0] {
		case "show":
			return platform.Result{Stdout: []byte(r.loadState + "\n")}, nil
		case "is-active":
			return platform.Result{Stdout: []byte(r.active + "\n")}, nil
		case "enable":
			if r.failEnable {
				return platform.Result{ExitCode: 1}, nil
			}
		}
	}
	return platform.Result{}, nil
}

func newManager(t *testing.T, r *runner) *Manager {
	return &Manager{Runner: r, StatePath: filepath.Join(t.TempDir(), "rdp.json")}
}

func TestDefaultIsOffAndLANOnly(t *testing.T) {
	m := newManager(t, &runner{loadState: "loaded", active: "inactive"})
	st := m.Status(context.Background())
	if st.Enabled || st.Active || !st.Installed || st.Source != SourceLAN || st.Port != 3389 {
		t.Fatalf("status = %+v", st)
	}
	if _, _, ok := m.Opens(); ok {
		t.Fatal("a disabled remote desktop asks for an open port")
	}
}

func TestEnableOpensThePortForTheChosenSource(t *testing.T) {
	r := &runner{loadState: "loaded", active: "active"}
	m := newManager(t, r)
	reapplied := 0
	m.Reapply = func(ctx context.Context) error { reapplied++; return nil }
	st, err := m.Set(context.Background(), true, "192.168.1.0/24")
	if err != nil {
		t.Fatal(err)
	}
	if !st.Enabled || !st.Active || st.Source != "192.168.1.0/24" {
		t.Fatalf("status = %+v", st)
	}
	port, src, ok := m.Opens()
	if !ok || port != 3389 || src != "192.168.1.0/24" || reapplied != 1 {
		t.Fatalf("opens = %d %q %v reapplied = %d", port, src, ok, reapplied)
	}
	found := false
	for _, c := range r.calls {
		if c == "systemctl enable --now xrdp.service xrdp-sesman.service" {
			found = true
		}
	}
	if !found {
		t.Fatalf("calls = %v", r.calls)
	}
	if _, err := m.Set(context.Background(), false, ""); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.Opens(); ok {
		t.Fatal("the port stays open after disabling")
	}
}

func TestRefusesWhenNotInstalledOrBadSource(t *testing.T) {
	m := newManager(t, &runner{loadState: "not-found"})
	if _, err := m.Set(context.Background(), true, ""); err == nil || err.(*Error).Code != CodeNotInstalled {
		t.Fatalf("err = %v", err)
	}
	m2 := newManager(t, &runner{loadState: "loaded"})
	if _, err := m2.Set(context.Background(), true, "everyone"); err == nil {
		t.Fatal("a bad source was accepted")
	}
	if _, err := m2.Set(context.Background(), true, "0.0.0.0/0"); err == nil {
		t.Fatal("a /0 source was accepted (use any)")
	}
}

func TestFailedEnableSavesNothing(t *testing.T) {
	m := newManager(t, &runner{loadState: "loaded", failEnable: true})
	if _, err := m.Set(context.Background(), true, ""); err == nil {
		t.Fatal("a failed enable was reported as success")
	}
	if _, err := os.Stat(m.StatePath); err == nil {
		t.Fatal("the state was saved")
	}
}
