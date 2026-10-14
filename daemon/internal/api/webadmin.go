package api

import (
	"context"
	"net/http"

	"github.com/nxodh/operetaos/daemon/internal/webadmin"
)

// WebAdminManager 는 원격 관리 웹 페이지다. *webadmin.Manager 가 만족한다.
type WebAdminManager interface {
	Status() webadmin.Status
	Configure(ctx context.Context, enabled *bool, port *int, source *string) (webadmin.Status, error)
	SetPassword(p string) error
}

func (s *server) webadmin(w http.ResponseWriter) (WebAdminManager, bool) {
	if s.d.WebAdmin == nil {
		writeError(w, http.StatusServiceUnavailable, "webadmin_unavailable", "the web admin page is not configured")
		return nil, false
	}
	return s.d.WebAdmin, true
}

func webadminErr(w http.ResponseWriter, err error) {
	if e, ok := err.(*webadmin.Error); ok {
		status := http.StatusInternalServerError
		switch e.Code {
		case webadmin.CodeInvalidInput:
			status = http.StatusBadRequest
		case webadmin.CodeNoPassword:
			status = http.StatusConflict
		}
		writeError(w, status, e.Code, "%s", e.Message)
		return
	}
	writeErr(w, err)
}

func (s *server) getWebAdmin(w http.ResponseWriter, r *http.Request) {
	m, ok := s.webadmin(w)
	if !ok {
		return
	}
	writeJSON(w, http.StatusOK, m.Status())
}

func (s *server) putWebAdmin(w http.ResponseWriter, r *http.Request) {
	m, ok := s.webadmin(w)
	if !ok {
		return
	}
	var body struct {
		Enabled *bool   `json:"enabled"`
		Port    *int    `json:"port"`
		Source  *string `json:"source"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	st, err := m.Configure(r.Context(), body.Enabled, body.Port, body.Source)
	if err != nil {
		webadminErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putWebAdminPassword 는 웹 관리 비밀번호를 정한다. 비밀번호는 응답과 로그에 남기지 않는다.
func (s *server) putWebAdminPassword(w http.ResponseWriter, r *http.Request) {
	m, ok := s.webadmin(w)
	if !ok {
		return
	}
	var body struct {
		Password string `json:"password"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := m.SetPassword(body.Password); err != nil {
		webadminErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"password_set": true})
}
