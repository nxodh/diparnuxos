package api

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/power"
)

// fakePower 는 PowerManager 의 가짜다.
type fakePower struct {
	status power.Status
	err    error
	set    []bool
}

func (f *fakePower) Status(context.Context) (power.Status, error) { return f.status, f.err }
func (f *fakePower) Set(_ context.Context, on bool) (power.Status, error) {
	f.set = append(f.set, on)
	st := f.status
	st.AlwaysOn, st.Enabled = on, on
	return st, f.err
}

func TestPowerUnavailable(t *testing.T) {
	e := newEnv(t, &fakeSup{})
	w := e.do("GET", "/v1/system/power", "")
	wantStatus(t, w, http.StatusServiceUnavailable)
	if errCode(t, w) != power.CodeUnavailable {
		t.Fatalf("code = %s", errCode(t, w))
	}
}

func TestPowerEndpoints(t *testing.T) {
	fp := &fakePower{status: power.Status{AlwaysOn: true, Enabled: true, Lid: "open", BatteryPercent: -1}}
	e := newEnvWith(t, &fakeSup{}, func(d *Deps) { d.Power = fp })

	w := e.do("GET", "/v1/system/power", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"always_on":true`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	wantStatus(t, e.do("PUT", "/v1/system/power", `{}`), http.StatusBadRequest)
	w = e.do("PUT", "/v1/system/power", `{"always_on":false}`)
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"always_on":false`) {
		t.Fatalf("body = %s", w.Body.String())
	}
	if len(fp.set) != 1 || fp.set[0] {
		t.Fatalf("set = %v", fp.set)
	}
}

func TestPowerErrorStatuses(t *testing.T) {
	fp := &fakePower{err: &power.Error{Code: power.CodeApplyFailed, Message: "x"}}
	e := newEnvWith(t, &fakeSup{}, func(d *Deps) { d.Power = fp })
	w := e.do("PUT", "/v1/system/power", `{"always_on":true}`)
	wantStatus(t, w, http.StatusInternalServerError)
	if errCode(t, w) != power.CodeApplyFailed {
		t.Fatalf("code = %s", errCode(t, w))
	}
}
