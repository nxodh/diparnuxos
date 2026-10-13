package netconf

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/network"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

type memFiles struct {
	dir     string
	writes  []string
	removed []string
	fail    error
}

// 실제 헬퍼 대신 임시 디렉터리에 쓴다. 경로의 /etc/systemd/network 를 dir 로 바꾼다.
func (f *memFiles) real(p string) string {
	if strings.HasPrefix(p, f.dir) {
		return p
	}
	return filepath.Join(f.dir, "hosts") // /etc/hosts
}

func (f *memFiles) Write(ctx context.Context, p string, data []byte, secret bool) error {
	if f.fail != nil {
		return f.fail
	}
	f.writes = append(f.writes, p)
	return os.WriteFile(f.real(p), data, 0o644)
}

func (f *memFiles) Remove(ctx context.Context, p string) error {
	f.removed = append(f.removed, p)
	err := os.Remove(f.real(p))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}

type lister struct{ list []network.Interface }

func (l lister) Interfaces(ctx context.Context) ([]network.Interface, bool, error) {
	return l.list, true, nil
}

type runner struct {
	calls [][]string
	exit  map[string]int
}

func (r *runner) Run(ctx context.Context, c platform.Command) (platform.Result, error) {
	r.calls = append(r.calls, append([]string{c.Name}, c.Args...))
	return platform.Result{ExitCode: r.exit[c.Name]}, nil
}

type fixture struct {
	m     *Manager
	files *memFiles
	run   *runner
	dir   string
	fire  func() // 확인 대기 타이머를 지금 만료시킨다
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	dir := t.TempDir()
	f := &fixture{dir: dir, files: &memFiles{dir: dir}, run: &runner{exit: map[string]int{}}}
	f.m = &Manager{
		Files: f.files, Runner: f.run,
		Lister: lister{[]network.Interface{
			{Name: "enp3s0", Kind: "ethernet", State: "up", Addresses: []string{"192.168.0.23/24"}, Gateway: "192.168.0.1"},
			{Name: "wlan0", Kind: "wifi", State: "up"},
		}},
		Dir: dir, HostsPath: filepath.Join(dir, "hosts"), PendingPath: filepath.Join(dir, "pending.json"),
		Now: func() time.Time { return time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC) },
	}
	f.m.AfterFunc = func(d time.Duration, fn func()) func() bool {
		f.fire = fn
		return func() bool { f.fire = nil; return true }
	}
	return f
}

func staticCfg() Config {
	return Config{Interface: "enp3s0", Mode: ModeStatic, Addresses: []string{"192.168.0.50/24"}, Gateway: "192.168.0.1", DNS: []string{"1.1.1.1", "9.9.9.9"}, Domains: []string{"lan"}}
}

func TestRenderAndParseRoundTrip(t *testing.T) {
	c := staticCfg()
	text := Render(c)
	want := "[Match]\nName=enp3s0\n\n[Network]\nDHCP=no\nAddress=192.168.0.50/24\nGateway=192.168.0.1\nIPv6AcceptRA=yes\nDNS=1.1.1.1\nDNS=9.9.9.9\nDomains=lan\nMulticastDNS=yes\n"
	if !strings.HasSuffix(text, want) {
		t.Fatalf("rendered:\n%s", text)
	}
	got := Parse("enp3s0", text)
	c.Managed = true
	if !reflect.DeepEqual(got, c) {
		t.Fatalf("parsed = %+v\nwant   = %+v", got, c)
	}
	dhcp := Render(Config{Interface: "enp3s0", Mode: ModeDHCP, DNS: []string{"1.1.1.1"}})
	if !strings.Contains(dhcp, "DHCP=yes") || !strings.Contains(dhcp, "[DHCPv4]\nUseDNS=no") {
		t.Fatalf("dhcp with DNS:\n%s", dhcp)
	}
}

func TestValidateRejectsBadInput(t *testing.T) {
	bad := []Config{
		{Interface: "../x", Mode: ModeDHCP},
		{Interface: "enp3s0", Mode: "manual"},
		{Interface: "enp3s0", Mode: ModeStatic},
		{Interface: "enp3s0", Mode: ModeStatic, Addresses: []string{"192.168.0.50"}},
		{Interface: "enp3s0", Mode: ModeStatic, Addresses: []string{"192.168.0.50/24\nGateway=1.2.3.4"}},
		{Interface: "enp3s0", Mode: ModeStatic, Addresses: []string{"192.168.0.50/24"}, Gateway: "gw"},
		{Interface: "enp3s0", Mode: ModeDHCP, DNS: []string{"dns.example"}},
		{Interface: "enp3s0", Mode: ModeDHCP, Domains: []string{"bad domain"}},
		{Interface: "enp3s0", Mode: ModeDHCP, Addresses: []string{"10.0.0.2/24"}},
	}
	for i, c := range bad {
		if _, err := Validate(c); err == nil {
			t.Errorf("case %d accepted: %+v", i, c)
		}
	}
	if _, err := Validate(staticCfg()); err != nil {
		t.Fatal(err)
	}
}

func TestSetStaticWritesReloadsAndWaitsForConfirmation(t *testing.T) {
	f := newFixture(t)
	c, pending, err := f.m.Set(context.Background(), staticCfg(), DefaultConfirm)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Managed || pending == nil || !pending.Deadline.Equal(time.Date(2026, 10, 13, 12, 1, 0, 0, time.UTC)) {
		t.Fatalf("c = %+v pending = %+v", c, pending)
	}
	data, _ := os.ReadFile(filepath.Join(f.dir, "10-diparnux-enp3s0.network"))
	if !strings.Contains(string(data), "Address=192.168.0.50/24") {
		t.Fatalf("file = %s", data)
	}
	if !reflect.DeepEqual(f.run.calls, [][]string{{"networkctl", "reload"}, {"networkctl", "reconfigure", "enp3s0"}}) {
		t.Fatalf("calls = %v", f.run.calls)
	}
	if _, err := os.Stat(f.m.pendingPath()); err != nil {
		t.Fatal("the pending change was not persisted")
	}
	// 대기 중에는 다른 변경을 받지 않는다.
	if _, _, err := f.m.Set(context.Background(), staticCfg(), DefaultConfirm); err == nil {
		t.Fatal("a second change was accepted while one is pending")
	}
	if err := f.m.Confirm(); err != nil {
		t.Fatal(err)
	}
	if f.m.Pending() != nil || f.fire != nil {
		t.Fatal("the change is still pending after confirmation")
	}
	if _, err := os.Stat(f.m.pendingPath()); err == nil {
		t.Fatal("the persisted record was kept")
	}
	got, err := f.m.Get(context.Background(), "enp3s0")
	if err != nil || got.Mode != ModeStatic || got.Gateway != "192.168.0.1" {
		t.Fatalf("Get = %+v, %v", got, err)
	}
}

func TestUnconfirmedChangeRevertsToThePreviousFile(t *testing.T) {
	f := newFixture(t)
	old := "[Match]\nName=enp3s0\n\n[Network]\nDHCP=yes\nDNS=8.8.8.8\n"
	if err := os.WriteFile(filepath.Join(f.dir, "10-diparnux-enp3s0.network"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := f.m.Set(context.Background(), staticCfg(), DefaultConfirm); err != nil {
		t.Fatal(err)
	}
	f.fire() // 확인하지 않고 시간이 지났다.
	data, _ := os.ReadFile(filepath.Join(f.dir, "10-diparnux-enp3s0.network"))
	if string(data) != old {
		t.Fatalf("the previous file was not restored:\n%s", data)
	}
	if f.m.Pending() != nil {
		t.Fatal("still pending after the revert")
	}
}

func TestRevertWithoutAPreviousFileRemovesTheNewOne(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.m.Set(context.Background(), staticCfg(), DefaultConfirm); err != nil {
		t.Fatal(err)
	}
	if err := f.m.Revert(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "10-diparnux-enp3s0.network")); err == nil {
		t.Fatal("the file was kept after the revert")
	}
	if err := f.m.Confirm(); err == nil {
		t.Fatal("nothing should be pending")
	}
}

func TestRecoverRevertsAChangeLeftByARestart(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.m.Set(context.Background(), staticCfg(), DefaultConfirm); err != nil {
		t.Fatal(err)
	}
	// 데몬이 다시 시작된 것처럼 새 관리자로 복구한다.
	m2 := &Manager{Files: f.files, Runner: f.run, Lister: f.m.Lister, Dir: f.dir, PendingPath: f.m.PendingPath}
	if err := m2.Recover(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "10-diparnux-enp3s0.network")); err == nil {
		t.Fatal("the unconfirmed file survived the restart")
	}
	if _, err := os.Stat(f.m.pendingPath()); err == nil {
		t.Fatal("the record was kept")
	}
}

func TestSetFailureRestoresAndReports(t *testing.T) {
	f := newFixture(t)
	f.run.exit["networkctl"] = 1
	_, _, err := f.m.Set(context.Background(), staticCfg(), DefaultConfirm)
	if err == nil || !strings.Contains(err.Error(), "reverted") {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(f.dir, "10-diparnux-enp3s0.network")); serr == nil {
		t.Fatal("a failed change left its file")
	}
	if f.m.Pending() != nil {
		t.Fatal("a failed change is pending")
	}
}

func TestOnlyWiredInterfacesCanBeConfigured(t *testing.T) {
	f := newFixture(t)
	c := staticCfg()
	c.Interface = "wlan0"
	if _, _, err := f.m.Set(context.Background(), c, 0); err == nil || err.(*Error).Code != CodeNotWired {
		t.Fatalf("err = %v", err)
	}
	c.Interface = "enp9s0"
	if _, _, err := f.m.Set(context.Background(), c, 0); err == nil || err.(*Error).Code != CodeNoInterface {
		t.Fatalf("err = %v", err)
	}
}

func TestDHCPWithoutOverridesRemovesTheFile(t *testing.T) {
	f := newFixture(t)
	if _, _, err := f.m.Set(context.Background(), staticCfg(), 0); err != nil {
		t.Fatal(err)
	}
	c, _, err := f.m.Set(context.Background(), Config{Interface: "enp3s0", Mode: ModeDHCP}, 0)
	if err != nil || c.Managed {
		t.Fatalf("c = %+v err = %v", c, err)
	}
	if _, err := os.Stat(filepath.Join(f.dir, "10-diparnux-enp3s0.network")); err == nil {
		t.Fatal("the file was kept for plain DHCP")
	}
}

func TestUpdateHosts(t *testing.T) {
	in := "127.0.0.1\tlocalhost\n127.0.1.1\told\n::1\tlocalhost ip6-localhost\n"
	if got := UpdateHosts(in, "diparnux-01"); got != "127.0.0.1\tlocalhost\n127.0.1.1\tdiparnux-01\n::1\tlocalhost ip6-localhost\n" {
		t.Fatalf("replace: %q", got)
	}
	if got := UpdateHosts("127.0.0.1\tlocalhost\n::1\tlocalhost\n", "box"); got != "127.0.0.1\tlocalhost\n127.0.1.1\tbox\n::1\tlocalhost\n" {
		t.Fatalf("insert: %q", got)
	}
	if got := UpdateHosts("", "box"); got != "127.0.1.1\tbox\n" {
		t.Fatalf("empty: %q", got)
	}
}

func TestSetHostname(t *testing.T) {
	f := newFixture(t)
	if err := os.WriteFile(f.m.hostsPath(), []byte("127.0.0.1\tlocalhost\n127.0.1.1\told\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := f.m.SetHostname(context.Background(), "Diparnux-Server"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(f.run.calls, [][]string{{"hostnamectl", "set-hostname", "diparnux-server"}}) {
		t.Fatalf("calls = %v", f.run.calls)
	}
	hosts, _ := os.ReadFile(filepath.Join(f.dir, "hosts"))
	if !strings.Contains(string(hosts), "127.0.1.1\tdiparnux-server") {
		t.Fatalf("hosts = %q", hosts)
	}
	for _, bad := range []string{"", "-a", "a-", "a b", "a_b", strings.Repeat("a", 64), "a.b"} {
		if err := f.m.SetHostname(context.Background(), bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestDiagnoseReportsLinkAndRoute(t *testing.T) {
	f := newFixture(t)
	checks := f.m.Diagnose(context.Background())
	if len(checks) < 5 || checks[0].Name != "address" || !checks[0].OK || checks[1].Name != "route" || !checks[1].OK {
		t.Fatalf("checks = %+v", checks)
	}
	if checks[2].Name != "gateway" || !checks[2].OK {
		t.Fatalf("gateway check = %+v", checks[2])
	}
}
