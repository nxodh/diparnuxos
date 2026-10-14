package api

import (
	"net/http"
	"strconv"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/llamastats"
	"github.com/nxodh/operetaos/daemon/internal/supervisor"
)

// serverStatsView 는 GET /v1/server/stats 의 응답이다.
type serverStatsView struct {
	Running       bool                 `json:"running"`
	State         string               `json:"state"`
	ModelID       string               `json:"model_id"`
	UptimeSeconds int64                `json:"uptime_seconds"`
	Parallel      int                  `json:"parallel"`
	SlotCtx       int                  `json:"slot_ctx"`
	Slots         llamastats.Slots     `json:"slots"`
	Metrics       llamastats.Metrics   `json:"metrics"`
	Gateway       *gatewayStatsSummary `json:"gateway,omitempty"`
}

type gatewayStatsSummary struct {
	Requests     int64 `json:"requests"`
	Errors       int64 `json:"errors"`
	Unauthorized int64 `json:"unauthorized"`
	InFlight     int64 `json:"in_flight"`
}

// getServerStats 는 서버가 돌고 있을 때 llama-server 의 슬롯과 지표를 읽어 돌려준다.
// 화면이 열려 있는 동안만 불리며 서버 쪽 조회는 한 번에 최대 3초다.
func (s *server) getServerStats(w http.ResponseWriter, r *http.Request) {
	st := s.d.Supervisor.Status()
	out := serverStatsView{
		State:   string(st.State),
		ModelID: st.ModelID,
		Slots:   llamastats.Slots{Items: []llamastats.Slot{}},
	}
	if st.Plan != nil {
		out.Parallel = max(st.Plan.Parallel, 1)
		out.SlotCtx = slotCtxOf(st.Plan)
	}
	running := st.State == supervisor.StateRunning || st.State == supervisor.StateRunningCPU
	out.Running = running
	if running {
		if !st.Since.IsZero() {
			out.UptimeSeconds = int64(s.d.Now().Sub(st.Since) / time.Second)
		}
		port := st.BackendPort
		if port == 0 {
			port = st.Port
		}
		if port != 0 {
			stats := llamastats.Fetch(r.Context(), s.d.HTTP, "http://127.0.0.1:"+strconv.Itoa(port))
			out.Slots, out.Metrics = stats.Slots, stats.Metrics
		}
	}
	if s.d.Gateway != nil {
		snap := s.d.Gateway.Snapshot()
		out.Gateway = &gatewayStatsSummary{Requests: snap.Requests, Errors: snap.Errors, Unauthorized: snap.Unauthorized, InFlight: snap.InFlight}
	}
	writeJSON(w, http.StatusOK, out)
}
