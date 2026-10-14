package api

import (
	"context"
	"net/http"
	"net/url"

	"github.com/nxodh/operetaos/daemon/internal/wireguard"
)

// VPNManager 는 WireGuard VPN 이다. *wireguard.Manager 가 만족한다.
type VPNManager interface {
	Status() (wireguard.View, error)
	Update(ctx context.Context, enabled *bool, address *string, port *int) (wireguard.View, error)
	AddPeer(ctx context.Context, name, publicKey, endpointHost string) (wireguard.Peer, string, error)
	RemovePeer(ctx context.Context, nameOrKey string) error
}

func vpnStatus(code string) int {
	switch code {
	case wireguard.CodeInvalidInput:
		return http.StatusBadRequest
	case wireguard.CodeNotFound:
		return http.StatusNotFound
	case wireguard.CodeNotEnabled, wireguard.CodeConflict, wireguard.CodeApplyFailed:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *server) vpn(w http.ResponseWriter) (VPNManager, bool) {
	if s.d.VPN == nil {
		writeError(w, http.StatusServiceUnavailable, "vpn_unavailable", "the VPN is not configured")
		return nil, false
	}
	return s.d.VPN, true
}

func (s *server) getVPN(w http.ResponseWriter, r *http.Request) {
	m, ok := s.vpn(w)
	if !ok {
		return
	}
	v, err := m.Status()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

func (s *server) putVPN(w http.ResponseWriter, r *http.Request) {
	m, ok := s.vpn(w)
	if !ok {
		return
	}
	var body struct {
		Enabled    *bool   `json:"enabled"`
		Address    *string `json:"address"`
		ListenPort *int    `json:"listen_port"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	v, err := m.Update(r.Context(), body.Enabled, body.Address, body.ListenPort)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// postVPNPeer 는 피어를 더한다. public_key 가 없으면 서버가 키 쌍을 만들고 클라이언트 설정(개인 키 포함)을 한 번만 돌려준다.
func (s *server) postVPNPeer(w http.ResponseWriter, r *http.Request) {
	m, ok := s.vpn(w)
	if !ok {
		return
	}
	var body struct {
		Name      string `json:"name"`
		PublicKey string `json:"public_key"`
		Endpoint  string `json:"endpoint"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	peer, cfg, err := m.AddPeer(r.Context(), body.Name, body.PublicKey, body.Endpoint)
	if err != nil {
		writeErr(w, err)
		return
	}
	out := map[string]any{"peer": peer}
	if cfg != "" {
		out["client_config"] = cfg
		out["note"] = "the client configuration contains the peer's private key; it is shown only once"
	}
	writeJSON(w, http.StatusCreated, out)
}

func (s *server) deleteVPNPeer(w http.ResponseWriter, r *http.Request) {
	m, ok := s.vpn(w)
	if !ok {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	id, _ := url.PathUnescape(r.PathValue("id"))
	if err := m.RemovePeer(r.Context(), id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}
