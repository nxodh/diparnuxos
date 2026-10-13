package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/netconf"
	"github.com/nxodh/diparnuxos/daemon/internal/rdpctl"
	"github.com/nxodh/diparnuxos/daemon/internal/sshconf"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
	"github.com/nxodh/diparnuxos/daemon/internal/wireguard"
)

type fakeNetConf struct {
	set     netconf.Config
	confirm time.Duration
	pending *netconf.Pending
	err     error
	host    string
	wol     bool
}

func (f *fakeNetConf) Get(ctx context.Context, name string) (netconf.Config, error) {
	return netconf.Config{Interface: name, Mode: netconf.ModeDHCP}, nil
}
func (f *fakeNetConf) Set(ctx context.Context, c netconf.Config, confirm time.Duration) (netconf.Config, *netconf.Pending, error) {
	f.set, f.confirm = c, confirm
	if f.err != nil {
		return c, nil, f.err
	}
	f.pending = &netconf.Pending{Interface: c.Interface}
	return c, f.pending, nil
}
func (f *fakeNetConf) Confirm() error {
	if f.pending == nil {
		return &netconf.Error{Code: netconf.CodeNothingToDo, Message: "nothing"}
	}
	f.pending = nil
	return nil
}
func (f *fakeNetConf) Revert(ctx context.Context) error                { return f.Confirm() }
func (f *fakeNetConf) Pending() *netconf.Pending                       { return f.pending }
func (f *fakeNetConf) Hostname() string                                { return f.host }
func (f *fakeNetConf) SetHostname(ctx context.Context, n string) error { f.host = n; return nil }
func (f *fakeNetConf) WOL() bool                                       { return f.wol }
func (f *fakeNetConf) SetWOL(ctx context.Context, on bool) error       { f.wol = on; return nil }
func (f *fakeNetConf) Diagnose(ctx context.Context) []netconf.Check {
	return []netconf.Check{{Name: "dns", OK: true, Detail: "ok"}}
}

func TestNetworkConfigEndpoints(t *testing.T) {
	nc := &fakeNetConf{host: "diparnux"}
	e := newEnvWith(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}, func(d *Deps) { d.NetConf = nc })

	w := e.do("PUT", "/v1/network/interfaces/enp3s0", `{"mode":"static","addresses":["192.168.0.50/24"],"gateway":"192.168.0.1","dns":["1.1.1.1"]}`)
	wantStatus(t, w, http.StatusOK)
	if nc.set.Interface != "enp3s0" || nc.set.Mode != "static" || nc.confirm != netconf.DefaultConfirm {
		t.Fatalf("set = %+v confirm = %v", nc.set, nc.confirm)
	}
	var res struct {
		Seconds int `json:"confirm_seconds"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil || res.Seconds != 60 {
		t.Fatalf("body = %s", w.Body.String())
	}

	w = e.do("POST", "/v1/network/confirm", "")
	wantStatus(t, w, http.StatusOK)
	w = e.do("POST", "/v1/network/confirm", "")
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != "nothing_pending" {
		t.Fatalf("code = %s", errCode(t, w))
	}

	w = e.do("PUT", "/v1/network/interfaces/enp3s0", `{"mode":"dhcp","confirm_seconds":0}`)
	wantStatus(t, w, http.StatusOK)
	if nc.confirm != 0 {
		t.Fatalf("confirm = %v", nc.confirm)
	}
	w = e.do("PUT", "/v1/network/interfaces/enp3s0", `{"mode":"dhcp","confirm_seconds":9999}`)
	wantStatus(t, w, http.StatusBadRequest)

	nc.err = &netconf.Error{Code: netconf.CodeInvalidInput, Message: "bad address"}
	w = e.do("PUT", "/v1/network/interfaces/enp3s0", `{"mode":"static"}`)
	wantStatus(t, w, http.StatusBadRequest)
	if !strings.Contains(w.Body.String(), "bad address") {
		t.Fatalf("body = %s", w.Body.String())
	}

	w = e.do("PUT", "/v1/network/hostname", `{"name":"box"}`)
	wantStatus(t, w, http.StatusOK)
	if nc.host != "box" {
		t.Fatalf("host = %q", nc.host)
	}
	w = e.do("PUT", "/v1/network/wol", `{"enabled":true}`)
	wantStatus(t, w, http.StatusOK)
	if !nc.wol {
		t.Fatal("wake-on-lan was not set")
	}
	w = e.do("PUT", "/v1/network/wol", `{}`)
	wantStatus(t, w, http.StatusBadRequest)
	w = e.do("POST", "/v1/network/diagnose", "")
	wantStatus(t, w, http.StatusOK)
}

func TestNetworkConfigWithoutManager(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	w := e.do("GET", "/v1/network/config", "")
	wantStatus(t, w, http.StatusServiceUnavailable)
}

type fakeSSHConf struct {
	set     sshconf.Settings
	applied []sshconf.Settings
	err     error
	keys    []sshconf.Key
}

func (f *fakeSSHConf) Settings() sshconf.Settings { return f.set }
func (f *fakeSSHConf) Apply(ctx context.Context, s sshconf.Settings, active bool) error {
	f.applied = append(f.applied, s)
	if f.err != nil {
		return f.err
	}
	f.set = s
	return nil
}
func (f *fakeSSHConf) CountKeys(ctx context.Context) (int, error) { return len(f.keys), nil }
func (f *fakeSSHConf) ListKeys(ctx context.Context, user string) ([]sshconf.Key, error) {
	return f.keys, nil
}
func (f *fakeSSHConf) AddKey(ctx context.Context, user, line string) (sshconf.Key, bool, error) {
	k := sshconf.Key{Type: "ssh-ed25519", Fingerprint: "SHA256:x", Comment: "c"}
	f.keys = append(f.keys, k)
	return k, true, nil
}
func (f *fakeSSHConf) RemoveKey(ctx context.Context, user, fp string) error { return nil }
func (f *fakeSSHConf) HostKeys(ctx context.Context) []sshconf.Key {
	return []sshconf.Key{{Type: "ssh-ed25519", Fingerprint: "SHA256:host"}}
}
func (f *fakeSSHConf) RecentLogins(ctx context.Context, limit int) ([]sshconf.LoginEvent, error) {
	return []sshconf.LoginEvent{{Result: "accepted", User: "alice"}}, nil
}

func TestSSHAdvancedChangesThePortAndTheFirewallTogether(t *testing.T) {
	sup := &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}
	sc := &fakeSSHConf{set: sshconf.Settings{Port: 22}}
	e := newEnvWith(t, sup, func(d *Deps) { d.SSHConf = sc })

	w := e.do("PUT", "/v1/ssh/advanced", `{"port":2222}`)
	wantStatus(t, w, http.StatusOK)
	if len(sc.applied) != 1 || sc.applied[0].Port != 2222 {
		t.Fatalf("applied = %+v", sc.applied)
	}
	if sup.firewalls != 1 {
		t.Fatalf("the firewall was applied %d times", sup.firewalls)
	}
	cfg, err := config.Load(e.cfgPath)
	if err != nil || cfg.SSHPort != 2222 {
		t.Fatalf("config = %+v, %v", cfg, err)
	}

	// 다시 22 로 돌리면 설정에는 0(기본)을 남긴다.
	w = e.do("PUT", "/v1/ssh/advanced", `{"port":22}`)
	wantStatus(t, w, http.StatusOK)
	cfg, _ = config.Load(e.cfgPath)
	if cfg.SSHPort != 0 {
		t.Fatalf("ssh_port = %d", cfg.SSHPort)
	}

	// sshd 가 거절하면 설정을 되돌린다.
	sc.err = &sshconf.Error{Code: sshconf.CodeSSHConfig, Message: "sshd rejected the configuration"}
	w = e.do("PUT", "/v1/ssh/advanced", `{"port":2200}`)
	wantStatus(t, w, http.StatusConflict)
	cfg, _ = config.Load(e.cfgPath)
	if cfg.SSHPort != 0 {
		t.Fatalf("the rejected port was kept: %d", cfg.SSHPort)
	}
	w = e.do("PUT", "/v1/ssh/advanced", `{"port":70000}`)
	wantStatus(t, w, http.StatusBadRequest)
}

func TestSSHKeysAndLog(t *testing.T) {
	sc := &fakeSSHConf{}
	e := newEnvWith(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}, func(d *Deps) { d.SSHConf = sc })
	w := e.do("POST", "/v1/ssh/keys", `{"user":"alice","key":"ssh-ed25519 AAAA c"}`)
	wantStatus(t, w, http.StatusCreated)
	w = e.do("GET", "/v1/ssh/keys?user=alice", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "SHA256:x") {
		t.Fatalf("keys = %s", w.Body.String())
	}
	w = e.do("DELETE", "/v1/ssh/keys", `{"user":"alice","fingerprint":"SHA256:x"}`)
	wantStatus(t, w, http.StatusOK)
	w = e.do("GET", "/v1/ssh/log?limit=5", "")
	wantStatus(t, w, http.StatusOK)
	w = e.do("GET", "/v1/ssh/log?limit=0", "")
	wantStatus(t, w, http.StatusBadRequest)
	w = e.do("GET", "/v1/ssh/advanced", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"key_count":1`) || !strings.Contains(w.Body.String(), "SHA256:host") {
		t.Fatalf("advanced = %s", w.Body.String())
	}
}

type fakeVPN struct {
	view   wireguard.View
	added  string
	cfg    string
	remove string
	err    error
}

func (f *fakeVPN) Status() (wireguard.View, error) { return f.view, f.err }
func (f *fakeVPN) Update(ctx context.Context, enabled *bool, address *string, port *int) (wireguard.View, error) {
	if f.err != nil {
		return wireguard.View{}, f.err
	}
	if enabled != nil {
		f.view.Enabled = *enabled
	}
	return f.view, nil
}
func (f *fakeVPN) AddPeer(ctx context.Context, name, key, endpoint string) (wireguard.Peer, string, error) {
	f.added = name
	return wireguard.Peer{Name: name, PublicKey: "pub", AllowedIPs: []string{"10.8.0.2/32"}}, f.cfg, f.err
}
func (f *fakeVPN) RemovePeer(ctx context.Context, id string) error { f.remove = id; return f.err }

func TestVPNEndpoints(t *testing.T) {
	v := &fakeVPN{view: wireguard.View{Interface: "wg0", Address: "10.8.0.1/24", ListenPort: 51820, Peers: []wireguard.Peer{}}, cfg: "[Interface]\nPrivateKey = SECRET\n"}
	e := newEnvWith(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}, func(d *Deps) { d.VPN = v })
	w := e.do("PUT", "/v1/vpn", `{"enabled":true}`)
	wantStatus(t, w, http.StatusOK)
	if !v.view.Enabled {
		t.Fatal("the VPN was not enabled")
	}
	w = e.do("POST", "/v1/vpn/peers", `{"name":"phone"}`)
	wantStatus(t, w, http.StatusCreated)
	if !strings.Contains(w.Body.String(), "client_config") || v.added != "phone" {
		t.Fatalf("body = %s", w.Body.String())
	}
	w = e.do("GET", "/v1/vpn", "")
	wantStatus(t, w, http.StatusOK)
	if strings.Contains(w.Body.String(), "PrivateKey") || strings.Contains(w.Body.String(), "private") {
		t.Fatalf("the status shows secrets: %s", w.Body.String())
	}
	w = e.do("DELETE", "/v1/vpn/peers/phone", "")
	wantStatus(t, w, http.StatusOK)
	if v.remove != "phone" {
		t.Fatalf("removed = %q", v.remove)
	}
	v.err = &wireguard.Error{Code: wireguard.CodeNotEnabled, Message: "turn the VPN on first"}
	w = e.do("POST", "/v1/vpn/peers", `{"name":"x"}`)
	wantStatus(t, w, http.StatusConflict)
}

type fakeRDP struct {
	st  rdpctl.Status
	err error
}

func (f *fakeRDP) Status(ctx context.Context) rdpctl.Status { return f.st }
func (f *fakeRDP) Set(ctx context.Context, enabled bool, source string) (rdpctl.Status, error) {
	if f.err != nil {
		return rdpctl.Status{}, f.err
	}
	f.st.Enabled = enabled
	if source != "" {
		f.st.Source = source
	}
	return f.st, nil
}

func TestRemoteDesktopEndpoints(t *testing.T) {
	rd := &fakeRDP{st: rdpctl.Status{Installed: true, Port: 3389, Source: "lan"}}
	e := newEnvWith(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}, func(d *Deps) { d.RDP = rd })
	w := e.do("PUT", "/v1/remote-desktop", `{"enabled":true,"source":"any"}`)
	wantStatus(t, w, http.StatusOK)
	if !rd.st.Enabled || rd.st.Source != "any" {
		t.Fatalf("st = %+v", rd.st)
	}
	wantStatus(t, e.do("PUT", "/v1/remote-desktop", `{}`), http.StatusBadRequest)
	rd.err = &rdpctl.Error{Code: rdpctl.CodeNotInstalled, Message: "xrdp is not installed"}
	wantStatus(t, e.do("PUT", "/v1/remote-desktop", `{"enabled":true}`), http.StatusConflict)
	wantStatus(t, e.do("GET", "/v1/remote-desktop", ""), http.StatusOK)
}
