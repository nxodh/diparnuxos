package api

import (
	"net/http"

	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/gateway"
)

// GatewayView 는 게이트웨이의 상태와 통계다. *gateway.Manager 가 만족한다.
type GatewayView interface {
	State() gateway.ManagerState
	Snapshot() gateway.Snapshot
}

type gatewayReply struct {
	Enabled    bool                 `json:"enabled"`
	PublicPort int                  `json:"public_port"`
	LANExposed bool                 `json:"lan_exposed"`
	State      gateway.ManagerState `json:"state"`
	Stats      gateway.Snapshot     `json:"stats"`
	Note       string               `json:"note,omitempty"`
}

// getGateway 는 게이트웨이의 상태, 키별 사용량, 최근 요청을 돌려준다.
func (s *server) getGateway(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	reply := gatewayReply{Enabled: cfg.Gateway, PublicPort: cfg.Port, LANExposed: cfg.LANExposed}
	if s.d.Gateway == nil {
		writeError(w, http.StatusServiceUnavailable, "gateway_unavailable", "the gateway is not configured")
		return
	}
	reply.State = s.d.Gateway.State()
	reply.Stats = s.d.Gateway.Snapshot()
	if !cfg.Gateway {
		reply.Note = "the gateway is turned off: llama-server receives the public port directly and the key file is read at start"
	}
	writeJSON(w, http.StatusOK, reply)
}
