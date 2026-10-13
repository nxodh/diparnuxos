package svcctl

import (
	"context"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

type runner struct {
	calls  [][]string
	out    string
	exit   int
	stderr string
}

func (r *runner) Run(ctx context.Context, c platform.Command) (platform.Result, error) {
	r.calls = append(r.calls, append([]string{c.Name}, c.Args...))
	return platform.Result{Stdout: []byte(r.out), ExitCode: r.exit, Stderr: []byte(r.stderr)}, nil
}

const showOut = `Id=diparnuxd.service
Description=Diparnux daemon (diparnuxd)
LoadState=loaded
ActiveState=active
SubState=running
UnitFileState=enabled
MainPID=812
MemoryCurrent=14680064

Id=ssh.service
Description=OpenBSD Secure Shell server
LoadState=loaded
ActiveState=inactive
SubState=dead
UnitFileState=disabled
MainPID=0
MemoryCurrent=[not set]

Id=xrdp.service
Description=
LoadState=not-found
ActiveState=inactive
SubState=dead
UnitFileState=
MainPID=0
MemoryCurrent=[not set]
`

func TestListMarksMissingUnitsAndLimitsActions(t *testing.T) {
	r := &runner{out: showOut}
	m := &Manager{Runner: r}
	list, err := m.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != len(Catalog) {
		t.Fatalf("len = %d", len(list))
	}
	by := map[string]Status{}
	for _, s := range list {
		by[s.Unit] = s
	}
	d := by["diparnuxd.service"]
	if d.Active != "active" || d.PID != 812 || d.MemoryMiB != 14 || len(d.Actions) != 0 {
		t.Fatalf("daemon = %+v", d)
	}
	ssh := by["ssh.service"]
	if ssh.Enabled != "disabled" || len(ssh.Actions) != 5 {
		t.Fatalf("ssh = %+v", ssh)
	}
	rdp := by["xrdp.service"]
	if rdp.Installed || rdp.Enabled != "not-found" || len(rdp.Actions) != 0 {
		t.Fatalf("xrdp = %+v", rdp)
	}
	// 목록에 없는 유닛은 systemctl 에 넘기지 않는다.
	for _, c := range r.calls {
		joined := strings.Join(c, " ")
		if strings.Contains(joined, "sshd.service") || strings.Contains(joined, "cron") {
			t.Fatalf("unexpected unit in %v", c)
		}
	}
}

func TestDoOnlyAllowedActionsOnAllowedUnits(t *testing.T) {
	r := &runner{}
	m := &Manager{Runner: r}
	ctx := context.Background()
	if err := m.Do(ctx, "ssh.service", "restart"); err != nil {
		t.Fatal(err)
	}
	if err := m.Do(ctx, "ssh.service", "enable"); err != nil {
		t.Fatal(err)
	}
	last := strings.Join(r.calls[len(r.calls)-1], " ")
	if last != "systemctl enable --now -- ssh.service" {
		t.Fatalf("call = %q", last)
	}
	for _, tc := range []struct{ unit, action string }{
		{"diparnuxd.service", "stop"}, // 데몬은 자기 자신을 멈추지 못한다
		{"diparnuxd.service", "restart"},
		{"cron.service", "stop"},             // 허용 목록 밖
		{"ssh.service", "mask"},              // 허용 동작 밖
		{"diparnux-llama.service", "enable"}, // 부팅 때 시작은 autostart 설정이 맡는다
		{"ssh.service; reboot", "start"},
	} {
		before := len(r.calls)
		if err := m.Do(ctx, tc.unit, tc.action); err == nil {
			t.Errorf("%s %s was accepted", tc.action, tc.unit)
		}
		if len(r.calls) != before {
			t.Errorf("%s %s reached systemctl", tc.action, tc.unit)
		}
	}
}

func TestDoReportsSystemctlFailure(t *testing.T) {
	r := &runner{exit: 1, stderr: "Failed to start ssh.service: Unit not found."}
	m := &Manager{Runner: r}
	err := m.Do(context.Background(), "ssh.service", "start")
	if err == nil || !strings.Contains(err.Error(), "Unit not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestLogsAreLimitedToTheCatalog(t *testing.T) {
	r := &runner{out: "line\n"}
	m := &Manager{Runner: r}
	out, err := m.Logs(context.Background(), "ssh.service", 50)
	if err != nil || out != "line\n" {
		t.Fatalf("out = %q err = %v", out, err)
	}
	for _, bad := range []string{"cron.service", "../x.service", "ssh.service --vacuum-time=1s"} {
		if _, err := m.Logs(context.Background(), bad, 10); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	if _, err := m.Logs(context.Background(), "ssh.service", 5000); err == nil {
		t.Fatal("5000 lines accepted")
	}
}
