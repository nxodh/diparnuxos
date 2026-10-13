package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/netconf"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
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
