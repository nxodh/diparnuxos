package api

import (
	"context"
	"net/http"

	"github.com/nxodh/diparnuxos/daemon/internal/rdpctl"
)

// RDPManager 는 원격 데스크톱이다. *rdpctl.Manager 가 만족한다.
type RDPManager interface {
	Status(ctx context.Context) rdpctl.Status
	Set(ctx context.Context, enabled bool, source string) (rdpctl.Status, error)
}

func (s *server) getRemoteDesktop(w http.ResponseWriter, r *http.Request) {
	if s.d.RDP == nil {
		writeError(w, http.StatusServiceUnavailable, "remote_desktop_unavailable", "remote desktop is not configured")
		return
	}
	writeJSON(w, http.StatusOK, s.d.RDP.Status(r.Context()))
}

func (s *server) putRemoteDesktop(w http.ResponseWriter, r *http.Request) {
	if s.d.RDP == nil {
		writeError(w, http.StatusServiceUnavailable, "remote_desktop_unavailable", "remote desktop is not configured")
		return
	}
	var body struct {
		Enabled *bool  `json:"enabled"`
		Source  string `json:"source"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "enabled is required")
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	st, err := s.d.RDP.Set(r.Context(), *body.Enabled, body.Source)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
