package network

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// managedJSON 은 실제 busctl --json=short 가 낸 GetManagedObjects 출력의 모양이다(tests/network/fake_iwd.py 로 확인).
const managedJSON = `{"type":"a{oa{sa{sv}}}","data":[{"/net/connman/iwd/0":{"net.connman.iwd.Adapter":{"Name":{"type":"s","data":"phy0"},"Powered":{"type":"b","data":true}}},"/net/connman/iwd/0/3":{"net.connman.iwd.Device":{"Name":{"type":"s","data":"wlan0"},"Address":{"type":"s","data":"aa:bb:cc:dd:ee:ff"},"Powered":{"type":"b","data":true},"Adapter":{"type":"o","data":"/net/connman/iwd/0"},"Mode":{"type":"s","data":"station"}},"net.connman.iwd.Station":{"State":{"type":"s","data":"connected"},"Scanning":{"type":"b","data":false},"ConnectedNetwork":{"type":"o","data":"/net/connman/iwd/0/3/486f6d6557694669_psk"}}},"/net/connman/iwd/0/3/486f6d6557694669_psk":{"net.connman.iwd.Network":{"Name":{"type":"s","data":"HomeWiFi"},"Connected":{"type":"b","data":true},"Device":{"type":"o","data":"/net/connman/iwd/0/3"},"Type":{"type":"s","data":"psk"},"KnownNetwork":{"type":"o","data":"/net/connman/iwd/486f6d6557694669_psk"}}},"/net/connman/iwd/486f6d6557694669_psk":{"net.connman.iwd.KnownNetwork":{"Name":{"type":"s","data":"HomeWiFi"},"Type":{"type":"s","data":"psk"},"Hidden":{"type":"b","data":false},"AutoConnect":{"type":"b","data":true}}},"/net/connman/iwd/0/3/4361666546726565_psk":{"net.connman.iwd.Network":{"Name":{"type":"s","data":"CafeFree"},"Connected":{"type":"b","data":false},"Device":{"type":"o","data":"/net/connman/iwd/0/3"},"Type":{"type":"s","data":"open"}}},"/net/connman/iwd/0/3/4f6666696365203547_psk":{"net.connman.iwd.Network":{"Name":{"type":"s","data":"Office 5G"},"Connected":{"type":"b","data":false},"Device":{"type":"o","data":"/net/connman/iwd/0/3"},"Type":{"type":"s","data":"psk"}}}}]}`

const orderedJSON = `{"type":"a(on)","data":[[["/net/connman/iwd/0/3/486f6d6557694669_psk",-4200],["/net/connman/iwd/0/3/4361666546726565_psk",-6100],["/net/connman/iwd/0/3/4f6666696365203547_psk",-7300]]]}`

const ipAddrJSON = `[
 {"ifname":"lo","operstate":"UNKNOWN","link_type":"loopback","address":"00:00:00:00:00:00","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},
 {"ifname":"enp3s0","operstate":"DOWN","link_type":"ether","address":"11:22:33:44:55:66","addr_info":[]},
 {"ifname":"wlan0","operstate":"UP","link_type":"ether","address":"aa:bb:cc:dd:ee:ff","addr_info":[{"family":"inet","local":"192.168.0.23","prefixlen":24,"scope":"global"},{"family":"inet6","local":"fe80::1","prefixlen":64,"scope":"link"}]}
]`

const ipRouteJSON = `[{"dst":"default","gateway":"192.168.0.1","dev":"wlan0","protocol":"dhcp"}]`

// fakeRunner 는 명령 이름과 인자로 응답을 고른다.
type fakeRunner struct {
	managed   string
	ordered   string
	failBus   string // 이 문자열이 인자에 있으면 busctl 이 실패한다
	failErr   string
	calls     []string
	connected bool
	onCall    func(args string)
}

func (f *fakeRunner) Run(_ context.Context, cmd platform.Command) (platform.Result, error) {
	line := cmd.Name + " " + strings.Join(cmd.Args, " ")
	f.calls = append(f.calls, line)
	switch cmd.Name {
	case "ip":
		if strings.Contains(line, "route") {
			return platform.Result{Stdout: []byte(ipRouteJSON)}, nil
		}
		return platform.Result{Stdout: []byte(ipAddrJSON)}, nil
	case "busctl":
		if f.onCall != nil {
			f.onCall(line)
		}
		if f.failBus != "" && strings.Contains(line, f.failBus) {
			return platform.Result{ExitCode: 1, Stderr: []byte(f.failErr)}, nil
		}
		switch {
		case strings.Contains(line, "GetManagedObjects"):
			if f.managed == "" {
				return platform.Result{ExitCode: 1, Stderr: []byte("Call failed: The name net.connman.iwd was not found.")}, nil
			}
			return platform.Result{Stdout: []byte(f.managed)}, nil
		case strings.Contains(line, "GetOrderedNetworks"):
			return platform.Result{Stdout: []byte(f.ordered)}, nil
		}
		return platform.Result{}, nil
	}
	return platform.Result{}, errors.New("unexpected command " + line)
}

func newManager(t *testing.T, f *fakeRunner) (*Manager, string) {
	t.Helper()
	dir := t.TempDir()
	sys := filepath.Join(dir, "sys")
	for _, p := range []string{"wlan0/wireless", "wlan0/device", "enp3s0/device"} {
		if err := os.MkdirAll(filepath.Join(sys, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &Manager{Runner: f, IwdDir: filepath.Join(dir, "iwd"), SysClassNet: sys, KnownWait: 500 * time.Millisecond}, dir
}

func TestStatusReadsInterfacesAndWifi(t *testing.T) {
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON}
	m, _ := newManager(t, f)
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Interfaces) != 2 {
		t.Fatalf("interfaces = %+v", st.Interfaces)
	}
	if st.Interfaces[0].Name != "enp3s0" || st.Interfaces[0].Kind != "ethernet" || st.Interfaces[0].State != "down" {
		t.Errorf("first interface = %+v", st.Interfaces[0])
	}
	w := st.Interfaces[1]
	if w.Kind != "wifi" || w.Gateway != "192.168.0.1" || len(w.Addresses) != 1 || w.Addresses[0] != "192.168.0.23/24" {
		t.Errorf("wifi interface = %+v", w)
	}
	if !st.Online {
		t.Error("online should be true with an address and a default route")
	}
	if !st.Wifi.Available || st.Wifi.Device != "wlan0" || st.Wifi.ConnectedSSID != "HomeWiFi" || st.Wifi.State != "connected" || !st.Wifi.Powered {
		t.Errorf("wifi = %+v", st.Wifi)
	}
}

func TestStatusWhenIwdIsMissing(t *testing.T) {
	f := &fakeRunner{}
	m, _ := newManager(t, f)
	st, err := m.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.Wifi.Available || st.Wifi.Reason != CodeIwdUnavailable {
		t.Errorf("wifi = %+v", st.Wifi)
	}
	if len(st.Interfaces) == 0 {
		t.Error("interfaces must still be listed")
	}
}

func TestStatusWithoutWirelessDevice(t *testing.T) {
	f := &fakeRunner{managed: `{"type":"a{oa{sa{sv}}}","data":[{"/net/connman/iwd":{}}]}`}
	m, _ := newManager(t, f)
	st, _ := m.Status(context.Background())
	if st.Wifi.Available || st.Wifi.Reason != CodeNoWifi {
		t.Errorf("wifi = %+v", st.Wifi)
	}
}

func TestNetworksAreOrderedAndLabelled(t *testing.T) {
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON}
	m, _ := newManager(t, f)
	nets, err := m.Networks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 3 {
		t.Fatalf("nets = %+v", nets)
	}
	if nets[0].SSID != "HomeWiFi" || !nets[0].Connected || !nets[0].Known || nets[0].Security != "psk" || nets[0].SignalDBm != -42 {
		t.Errorf("first = %+v", nets[0])
	}
	if nets[1].SSID != "CafeFree" || nets[1].Security != "open" || nets[1].Known {
		t.Errorf("second = %+v", nets[1])
	}
	if nets[2].SSID != "Office 5G" || nets[0].Signal <= nets[2].Signal {
		t.Errorf("third = %+v, signals %d %d", nets[2], nets[0].Signal, nets[2].Signal)
	}
}

func TestSignalPercent(t *testing.T) {
	for _, c := range []struct{ in, want int }{{-2500, 100}, {-3000, 100}, {-6000, 50}, {-9000, 0}, {-9900, 0}} {
		if got := signalPercent(c.in); got != c.want {
			t.Errorf("signalPercent(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestConfigFileName(t *testing.T) {
	cases := map[string]string{
		"HomeWiFi":  "HomeWiFi.psk",
		"Office 5G": "Office 5G.psk",
		"my_net-1":  "my_net-1.psk",
		"카페":        "=ecb9b4ed8e98.psk",
		"a.b":       "=612e62.psk",
	}
	for in, want := range cases {
		if got := ConfigFileName(in, "psk"); got != want {
			t.Errorf("ConfigFileName(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestValidation(t *testing.T) {
	if ValidSSID("") || ValidSSID(strings.Repeat("a", 33)) || ValidSSID("a\nb") || !ValidSSID("카페") {
		t.Error("ValidSSID")
	}
	for _, ok := range []string{"12345678", "correct horse battery", strings.Repeat("a", 63), strings.Repeat("f", 64)} {
		if !ValidPassphrase(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "short", strings.Repeat("a", 65), "한글비밀번호입니다", "pass\nword12"} {
		if ValidPassphrase(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestConnectOpenNetworkNeedsNoPassword(t *testing.T) {
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON}
	m, dir := newManager(t, f)
	if err := m.Connect(context.Background(), "CafeFree", ""); err != nil {
		t.Fatal(err)
	}
	found := false
	for _, c := range f.calls {
		if strings.Contains(c, "4361666546726565_psk net.connman.iwd.Network Connect") {
			found = true
		}
	}
	if !found {
		t.Errorf("Connect was not called: %v", f.calls)
	}
	if _, err := os.Stat(filepath.Join(dir, "iwd")); err == nil {
		t.Error("an open network must not write a configuration file")
	}
}

func TestConnectKnownNetworkDoesNotRewritePassword(t *testing.T) {
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON}
	m, dir := newManager(t, f)
	if err := m.Connect(context.Background(), "HomeWiFi", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "iwd", "HomeWiFi.psk")); err == nil {
		t.Error("a known network must not be rewritten without a password")
	}
}

func TestConnectNewSecuredNetworkWritesAndConnects(t *testing.T) {
	// 파일이 쓰이면 iwd 가 KnownNetwork 를 만든 것처럼 다음 조회부터 known 으로 보이게 한다.
	known := strings.Replace(managedJSON,
		`"/net/connman/iwd/0/3/4f6666696365203547_psk":{"net.connman.iwd.Network":{"Name":{"type":"s","data":"Office 5G"},"Connected":{"type":"b","data":false},"Device":{"type":"o","data":"/net/connman/iwd/0/3"},"Type":{"type":"s","data":"psk"}}}`,
		`"/net/connman/iwd/0/3/4f6666696365203547_psk":{"net.connman.iwd.Network":{"Name":{"type":"s","data":"Office 5G"},"Connected":{"type":"b","data":false},"Device":{"type":"o","data":"/net/connman/iwd/0/3"},"Type":{"type":"s","data":"psk"},"KnownNetwork":{"type":"o","data":"/net/connman/iwd/4f6666696365203547_psk"}}}`, 1)
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON}
	m, dir := newManager(t, f)
	f.onCall = func(string) {
		if _, err := os.Stat(filepath.Join(dir, "iwd", "Office 5G.psk")); err == nil {
			f.managed = known
		}
	}
	if err := m.Connect(context.Background(), "Office 5G", "s3cretpassw0rd"); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "iwd", "Office 5G.psk"))
	if err != nil || string(b) != "[Security]\nPassphrase=s3cretpassw0rd\n" {
		t.Fatalf("config = %q, %v", b, err)
	}
	fi, _ := os.Stat(filepath.Join(dir, "iwd", "Office 5G.psk"))
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("mode = %v", fi.Mode().Perm())
	}
	for _, c := range f.calls {
		if strings.Contains(c, "s3cretpassw0rd") {
			t.Errorf("the password reached a command line: %s", c)
		}
	}
}

func TestConnectFailureRemovesTheNewConfiguration(t *testing.T) {
	known := strings.Replace(managedJSON, `"Type":{"type":"s","data":"psk"}}}}]}`, `"Type":{"type":"s","data":"psk"},"KnownNetwork":{"type":"o","data":"/net/connman/iwd/4f6666696365203547_psk"}}}}]}`, 1)
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON, failBus: "Network Connect", failErr: "Call failed: Operation failed"}
	m, dir := newManager(t, f)
	f.onCall = func(line string) {
		if _, err := os.Stat(filepath.Join(dir, "iwd", "Office 5G.psk")); err == nil && !strings.Contains(line, "Connect") {
			f.managed = known
		}
	}
	err := m.Connect(context.Background(), "Office 5G", "wrongpassword1")
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeConnectFailed {
		t.Fatalf("error = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(dir, "iwd", "Office 5G.psk")); serr == nil {
		t.Error("the configuration with the wrong password must be removed")
	}
}

func TestConnectRejections(t *testing.T) {
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON}
	m, _ := newManager(t, f)
	code := func(err error) string {
		var e *Error
		if errors.As(err, &e) {
			return e.Code
		}
		return "none"
	}
	if got := code(m.Connect(context.Background(), "", "")); got != CodeInvalidSSID {
		t.Errorf("empty ssid: %s", got)
	}
	if got := code(m.Connect(context.Background(), "Nowhere", "")); got != CodeNotFound {
		t.Errorf("unknown ssid: %s", got)
	}
	if got := code(m.Connect(context.Background(), "Office 5G", "short")); got != CodeInvalidPass {
		t.Errorf("short password: %s", got)
	}
	if got := code(m.Connect(context.Background(), "Office 5G", "")); got != CodeInvalidPass {
		t.Errorf("missing password for a new secured network: %s", got)
	}
}

func TestDisconnectForgetAndPower(t *testing.T) {
	f := &fakeRunner{managed: managedJSON, ordered: orderedJSON}
	m, _ := newManager(t, f)
	ctx := context.Background()
	if err := m.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := m.Forget(ctx, "HomeWiFi"); err != nil {
		t.Fatal(err)
	}
	if err := m.Forget(ctx, "CafeFree"); err == nil {
		t.Error("forgetting an unknown network must fail")
	}
	if err := m.SetPowered(ctx, false); err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(f.calls, "\n")
	for _, want := range []string{"Station Disconnect", "KnownNetwork Forget", "set-property net.connman.iwd /net/connman/iwd/0/3 net.connman.iwd.Device Powered b false"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing call %q in\n%s", want, joined)
		}
	}
}
