package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
	"github.com/nxodh/diparnuxos/daemon/internal/svcctl"
	"github.com/nxodh/diparnuxos/daemon/internal/sysstat"
)

type fakeServices struct {
	did  []string
	fail error
}

func (f *fakeServices) List(ctx context.Context) ([]svcctl.Status, error) {
	return []svcctl.Status{{Unit: "ssh.service", Title: "SSH server", Active: "active", Actions: []string{"restart"}}}, nil
}
func (f *fakeServices) Do(ctx context.Context, unit, action string) error {
	f.did = append(f.did, unit+" "+action)
	return f.fail
}
func (f *fakeServices) Logs(ctx context.Context, unit string, lines int) (string, error) {
	if f.fail != nil {
		return "", f.fail
	}
	return "log line\n", nil
}

type fakeStats struct{}

func (fakeStats) Read(ctx context.Context) (sysstat.Sample, error) {
	return sysstat.Sample{CPU: sysstat.CPU{Cores: 4, UsedPercent: 12.5}, Disks: []sysstat.Disk{}, Network: []sysstat.NetIface{}, Temperatures: []sysstat.Temp{}, Top: []sysstat.Process{}}, nil
}

func TestServiceEndpoints(t *testing.T) {
	fs := &fakeServices{}
	e := newEnvWith(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}, func(d *Deps) { d.Services = fs })
	w := e.do("GET", "/v1/services", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "ssh.service") {
		t.Fatalf("body = %s", w.Body.String())
	}
	w = e.do("POST", "/v1/services/ssh.service/restart", "")
	wantStatus(t, w, http.StatusOK)
	if len(fs.did) != 1 || fs.did[0] != "ssh.service restart" {
		t.Fatalf("did = %v", fs.did)
	}
	fs.fail = &svcctl.Error{Code: svcctl.CodeProtected, Message: "not from here"}
	w = e.do("POST", "/v1/services/diparnuxd.service/stop", "")
	wantStatus(t, w, http.StatusForbidden)
	fs.fail = &svcctl.Error{Code: svcctl.CodeNotAllowed, Message: "not managed"}
	w = e.do("POST", "/v1/services/cron.service/stop", "")
	wantStatus(t, w, http.StatusNotFound)
	fs.fail = nil
	w = e.do("GET", "/v1/services/ssh.service/logs?lines=20", "")
	wantStatus(t, w, http.StatusOK)
	w = e.do("GET", "/v1/services/ssh.service/logs?lines=abc", "")
	wantStatus(t, w, http.StatusBadRequest)
}

func TestResourcesEndpoint(t *testing.T) {
	e := newEnvWith(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}, func(d *Deps) { d.Stats = fakeStats{} })
	w := e.do("GET", "/v1/resources", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"used_percent":12.5`) || !strings.Contains(w.Body.String(), `"gpus":[]`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	e2 := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	wantStatus(t, e2.do("GET", "/v1/resources", ""), http.StatusServiceUnavailable)
}
