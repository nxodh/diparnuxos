package wireguard

import (
	"context"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

type files struct {
	written map[string]string
	secret  map[string]bool
	removed []string
	fail    bool
}

func (f *files) Write(ctx context.Context, p string, data []byte, secret bool) error {
	if f.fail {
		return os.ErrPermission
	}
	if f.written == nil {
		f.written, f.secret = map[string]string{}, map[string]bool{}
	}
	f.written[p], f.secret[p] = string(data), secret
	return nil
}
func (f *files) Remove(ctx context.Context, p string) error {
	f.removed = append(f.removed, p)
	delete(f.written, p)
	return nil
}

type runner struct{ calls []string }

func (r *runner) Run(ctx context.Context, c platform.Command) (platform.Result, error) {
	r.calls = append(r.calls, c.Name+" "+strings.Join(c.Args, " "))
	return platform.Result{}, nil
}

func newManager(t *testing.T) (*Manager, *files, *runner) {
	t.Helper()
	f, r := &files{}, &runner{}
	return &Manager{Files: f, Runner: r, StatePath: filepath.Join(t.TempDir(), "wireguard.json")}, f, r
}

func TestGenerateKeyIsAValidClampedPair(t *testing.T) {
	priv, pub, err := GenerateKey()
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := base64.StdEncoding.DecodeString(priv)
	if len(raw) != 32 || raw[0]&7 != 0 || raw[31]&0x80 != 0 || raw[31]&0x40 == 0 {
		t.Fatalf("private key is not clamped: %x", raw)
	}
	again, err := PublicFromPrivate(priv)
	if err != nil || again != pub || !ValidPublicKey(pub) {
		t.Fatalf("pub = %q again = %q err = %v", pub, again, err)
	}
	// RFC 7748 의 X25519 시험 벡터(Alice): 개인 키에서 알려진 공개 키가 나와야 한다.
	pubA, err := PublicFromPrivate("dwdtCnMYpX08FsFyUbJmRd9ML4frwJkqsXf7pR25LCo=")
	if err != nil || pubA != "hSDwCYkwp1R0i33ctD73Wg2/Og0mOBr066SpjqqbTmo=" {
		t.Fatalf("known vector: %q %v", pubA, err)
	}
}

func TestEnableCreatesKeysAndFiles(t *testing.T) {
	m, f, r := newManager(t)
	reapplied := 0
	m.Reapply = func(ctx context.Context) error { reapplied++; return nil }
	yes := true
	v, err := m.Update(context.Background(), &yes, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !v.Enabled || v.PublicKey == "" || v.Address != DefaultSubnet || v.ListenPort != DefaultPort {
		t.Fatalf("view = %+v", v)
	}
	netdev := f.written[NetdevPath]
	if !strings.Contains(netdev, "Kind=wireguard") || !strings.Contains(netdev, "PrivateKey=") || !f.secret[NetdevPath] {
		t.Fatalf("netdev = %q secret = %v", netdev, f.secret[NetdevPath])
	}
	if f.secret[NetworkPath] || !strings.Contains(f.written[NetworkPath], "Address=10.8.0.1/24") {
		t.Fatalf("network = %q", f.written[NetworkPath])
	}
	if reapplied != 1 {
		t.Fatalf("firewall reapplied %d times", reapplied)
	}
	if len(r.calls) < 2 || r.calls[0] != "networkctl reload" {
		t.Fatalf("calls = %v", r.calls)
	}
	// 상태 파일은 root 만 읽는다. 개인 키가 들어 있다.
	st, _ := os.Stat(m.StatePath)
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("state mode = %v", st.Mode())
	}
	// API 가 돌려주는 값에는 개인 키가 없다.
	if strings.Contains(strings.Join([]string{v.PublicKey, v.Address}, ""), "PrivateKey") {
		t.Fatal("private key leaked")
	}
	if port, iface, ok := m.Opens(); !ok || port != DefaultPort || iface != Interface {
		t.Fatalf("opens = %d %q %v", port, iface, ok)
	}
}

func TestAddPeerGeneratesAClientConfigOnce(t *testing.T) {
	m, f, _ := newManager(t)
	m.DefaultEndpoint = func() string { return "diparnux.local" }
	if _, _, err := m.AddPeer(context.Background(), "phone", "", ""); err == nil {
		t.Fatal("a peer was added while the VPN is off")
	}
	yes := true
	view, _ := m.Update(context.Background(), &yes, nil, nil)
	peer, cfg, err := m.AddPeer(context.Background(), "phone", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if peer.AllowedIPs[0] != "10.8.0.2/32" || !ValidPublicKey(peer.PublicKey) {
		t.Fatalf("peer = %+v", peer)
	}
	for _, want := range []string{"[Interface]", "PrivateKey = ", "Address = 10.8.0.2/32", "PublicKey = " + view.PublicKey, "Endpoint = diparnux.local:51820", "AllowedIPs = 10.8.0.0/24"} {
		if !strings.Contains(cfg, want) {
			t.Fatalf("client config lacks %q:\n%s", want, cfg)
		}
	}
	// 상태와 networkd 조각에는 피어의 개인 키가 없다.
	clientPriv := strings.TrimSpace(strings.SplitN(strings.SplitN(cfg, "PrivateKey = ", 2)[1], "\n", 2)[0])
	state, _ := os.ReadFile(m.StatePath)
	if strings.Contains(string(state), clientPriv) || strings.Contains(f.written[NetdevPath], clientPriv) {
		t.Fatal("the peer's private key was stored")
	}
	if !strings.Contains(f.written[NetdevPath], "[WireGuardPeer]\nPublicKey="+peer.PublicKey) {
		t.Fatalf("netdev = %s", f.written[NetdevPath])
	}

	// 직접 만든 공개 키를 넣으면 설정을 만들어 주지 않는다. 주소는 다음 빈 주소다.
	_, ownPub, _ := GenerateKey()
	p2, cfg2, err := m.AddPeer(context.Background(), "laptop", ownPub, "")
	if err != nil || cfg2 != "" || p2.AllowedIPs[0] != "10.8.0.3/32" {
		t.Fatalf("p2 = %+v cfg = %q err = %v", p2, cfg2, err)
	}
	if _, _, err := m.AddPeer(context.Background(), "phone", "", ""); err == nil {
		t.Fatal("a duplicate name was accepted")
	}
	if _, _, err := m.AddPeer(context.Background(), "x", ownPub, ""); err == nil {
		t.Fatal("a duplicate key was accepted")
	}
	if _, _, err := m.AddPeer(context.Background(), "bad", "not-a-key", ""); err == nil {
		t.Fatal("an invalid key was accepted")
	}
	if _, _, err := m.AddPeer(context.Background(), "../x", "", ""); err == nil {
		t.Fatal("a path-like name was accepted")
	}
}

func TestRemovePeerAndDisable(t *testing.T) {
	m, f, r := newManager(t)
	yes, no := true, false
	m.Update(context.Background(), &yes, nil, nil)
	p, _, _ := m.AddPeer(context.Background(), "phone", "", "")
	if err := m.RemovePeer(context.Background(), "PHONE"); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(f.written[NetdevPath], p.PublicKey) {
		t.Fatal("the removed peer is still configured")
	}
	if err := m.RemovePeer(context.Background(), "phone"); err == nil {
		t.Fatal("removing a missing peer succeeded")
	}
	if _, err := m.Update(context.Background(), &no, nil, nil); err != nil {
		t.Fatal(err)
	}
	if len(f.removed) < 2 {
		t.Fatalf("removed = %v", f.removed)
	}
	found := false
	for _, c := range r.calls {
		if c == "networkctl delete wg0" {
			found = true
		}
	}
	if !found {
		t.Fatalf("the interface was not deleted: %v", r.calls)
	}
	if _, _, ok := m.Opens(); ok {
		t.Fatal("a disabled VPN still asks for open ports")
	}
}

func TestUpdateValidation(t *testing.T) {
	m, _, _ := newManager(t)
	bad := "192.168.0.1"
	if _, err := m.Update(context.Background(), nil, &bad, nil); err == nil {
		t.Fatal("an address without a prefix was accepted")
	}
	v6 := "fd00::1/64"
	if _, err := m.Update(context.Background(), nil, &v6, nil); err == nil {
		t.Fatal("an IPv6 address was accepted")
	}
	low := 80
	if _, err := m.Update(context.Background(), nil, nil, &low); err == nil {
		t.Fatal("a low port was accepted")
	}
	yes := true
	m.Update(context.Background(), &yes, nil, nil)
	m.AddPeer(context.Background(), "phone", "", "")
	other := "10.9.0.1/24"
	if _, err := m.Update(context.Background(), nil, &other, nil); err == nil {
		t.Fatal("the address changed while peers exist")
	}
}

func TestFailedWriteDoesNotSaveState(t *testing.T) {
	m, f, _ := newManager(t)
	f.fail = true
	yes := true
	if _, err := m.Update(context.Background(), &yes, nil, nil); err == nil {
		t.Fatal("a failed write was reported as success")
	}
	if _, err := os.Stat(m.StatePath); err == nil {
		t.Fatal("the state was saved after a failed write")
	}
}
