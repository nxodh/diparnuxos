package api

import (
	"context"
	"net/http"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/netconf"
)

// NetConfManager 는 유선 IP 설정, 호스트 이름, 깨우기, 진단이다. *netconf.Manager 가 만족한다.
type NetConfManager interface {
	Get(ctx context.Context, name string) (netconf.Config, error)
	Set(ctx context.Context, c netconf.Config, confirm time.Duration) (netconf.Config, *netconf.Pending, error)
	Confirm() error
	Revert(ctx context.Context) error
	Pending() *netconf.Pending
	Hostname() string
	SetHostname(ctx context.Context, name string) error
	WOL() bool
	SetWOL(ctx context.Context, on bool) error
	Diagnose(ctx context.Context) []netconf.Check
}

func netconfStatus(code string) int {
	switch code {
	case netconf.CodeInvalidInput:
		return http.StatusBadRequest
	case netconf.CodeNoInterface:
		return http.StatusNotFound
	case netconf.CodeNotWired, netconf.CodeNothingToDo, netconf.CodeApplyFailed:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *server) netconf(w http.ResponseWriter) (NetConfManager, bool) {
	if s.d.NetConf == nil {
		writeError(w, http.StatusServiceUnavailable, "network_unavailable", "network configuration is not available")
		return nil, false
	}
	return s.d.NetConf, true
}

// getNetworkConfig 는 유선 인터페이스별 설정, 호스트 이름, 깨우기, 확인 대기 중인 변경이다.
func (s *server) getNetworkConfig(w http.ResponseWriter, r *http.Request) {
	m, ok := s.netconf(w)
	if !ok {
		return
	}
	out := map[string]any{"hostname": m.Hostname(), "wake_on_lan": m.WOL(), "pending": m.Pending(), "interfaces": []netconf.Config{}}
	if s.d.Network != nil {
		if st, err := s.d.Network.Status(r.Context()); err == nil {
			var cfgs []netconf.Config
			for _, it := range st.Interfaces {
				if it.Kind != "ethernet" {
					continue
				}
				if c, err := m.Get(r.Context(), it.Name); err == nil {
					cfgs = append(cfgs, c)
				}
			}
			if cfgs != nil {
				out["interfaces"] = cfgs
			}
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// putInterfaceConfig 는 인터페이스의 IP 설정을 바꾼다. confirm_seconds 가 0 보다 크면(기본 60) 그 시간 안에
// POST /v1/network/confirm 을 부르지 않을 때 이전 설정으로 되돌린다.
func (s *server) putInterfaceConfig(w http.ResponseWriter, r *http.Request) {
	m, ok := s.netconf(w)
	if !ok {
		return
	}
	var body struct {
		Mode           string   `json:"mode"`
		Addresses      []string `json:"addresses"`
		Gateway        string   `json:"gateway"`
		DNS            []string `json:"dns"`
		Domains        []string `json:"domains"`
		ConfirmSeconds *int     `json:"confirm_seconds"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	confirm := netconf.DefaultConfirm
	if body.ConfirmSeconds != nil {
		if *body.ConfirmSeconds < 0 || *body.ConfirmSeconds > 600 {
			writeError(w, http.StatusBadRequest, "invalid_input", "confirm_seconds must be 0 to 600")
			return
		}
		confirm = time.Duration(*body.ConfirmSeconds) * time.Second
	}
	cfg := netconf.Config{Interface: r.PathValue("name"), Mode: body.Mode, Addresses: body.Addresses, Gateway: body.Gateway, DNS: body.DNS, Domains: body.Domains}
	got, pending, err := m.Set(r.Context(), cfg, confirm)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"config": got, "pending": pending, "confirm_seconds": int(confirm / time.Second)})
}

func (s *server) postNetworkConfirm(w http.ResponseWriter, r *http.Request) {
	m, ok := s.netconf(w)
	if !ok {
		return
	}
	if err := m.Confirm(); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"confirmed": true})
}

func (s *server) postNetworkRevert(w http.ResponseWriter, r *http.Request) {
	m, ok := s.netconf(w)
	if !ok {
		return
	}
	if err := m.Revert(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"reverted": true})
}

func (s *server) putHostname(w http.ResponseWriter, r *http.Request) {
	m, ok := s.netconf(w)
	if !ok {
		return
	}
	var body struct {
		Name string `json:"name"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := m.SetHostname(r.Context(), body.Name); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"hostname": m.Hostname()})
}

func (s *server) putWakeOnLAN(w http.ResponseWriter, r *http.Request) {
	m, ok := s.netconf(w)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "enabled is required")
		return
	}
	if err := m.SetWOL(r.Context(), *body.Enabled); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"wake_on_lan": m.WOL(), "note": "applies from the next boot; the adapter and the firmware must support it"})
}

func (s *server) postNetworkDiagnose(w http.ResponseWriter, r *http.Request) {
	m, ok := s.netconf(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"checks": m.Diagnose(r.Context())})
}
