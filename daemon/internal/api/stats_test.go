package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"testing"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/autoconf"
	"github.com/nxodh/operetaos/daemon/internal/supervisor"
)

func TestServerStatsReadsSlotsAndMetricsFromTheBackendPort(t *testing.T) {
	backend := http.NewServeMux()
	backend.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":0,"n_ctx":2048,"is_processing":true},{"id":1,"n_ctx":2048,"is_processing":false}]`))
	})
	backend.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("llamacpp:predicted_tokens_seconds 31.5\nllamacpp:requests_processing 1\n"))
	})
	srv := httptest.NewServer(backend)
	defer srv.Close()
	u, _ := url.Parse(srv.URL)
	port, _ := strconv.Atoi(u.Port())

	since := time.Date(2026, 10, 10, 0, 0, 0, 0, time.UTC) // 시험의 현재 시각은 2026-10-11 이다
	sup := &fakeSup{status: supervisor.Status{
		State: supervisor.StateRunning, ModelID: "m.gguf", Port: 8080, BackendPort: port, Since: since,
		Plan: &autoconf.Output{Ctx: 4096, Parallel: 2, SlotCtx: 2048},
	}}
	e := newEnvWith(t, sup, func(d *Deps) { d.HTTP = srv.Client(); d.Gateway = fakeGateway{} })
	w := e.do("GET", "/v1/server/stats", "")
	wantStatus(t, w, http.StatusOK)
	var got serverStatsView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Running || got.Parallel != 2 || got.SlotCtx != 2048 || got.Slots.Total != 2 || got.Slots.Busy != 1 {
		t.Fatalf("stats = %+v", got)
	}
	if !got.Metrics.Available || got.Metrics.GenTokensPerSec != 31.5 || got.Metrics.RequestsProcessing != 1 {
		t.Fatalf("metrics = %+v", got.Metrics)
	}
	if got.UptimeSeconds != 86400 || got.Gateway == nil {
		t.Fatalf("uptime = %d gateway = %v", got.UptimeSeconds, got.Gateway)
	}
}

func TestServerStatsWhenStopped(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	w := e.do("GET", "/v1/server/stats", "")
	wantStatus(t, w, http.StatusOK)
	var got serverStatsView
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	if got.Running || got.State != "stopped" || got.Slots.Items == nil || len(got.Slots.Items) != 0 {
		t.Fatalf("stats = %+v", got)
	}
}
