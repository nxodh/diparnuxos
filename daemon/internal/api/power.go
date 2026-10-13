package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/nxodh/diparnuxos/daemon/internal/power"
)

// PowerManager 는 상시 가동(절전과 종료 금지) 상태를 읽고 바꾼다. *power.Manager 가 만족한다.
type PowerManager interface {
	Status(ctx context.Context) (power.Status, error)
	Set(ctx context.Context, on bool) (power.Status, error)
}

func (s *server) power(w http.ResponseWriter) (PowerManager, bool) {
	if s.d.Power == nil {
		writeError(w, http.StatusServiceUnavailable, power.CodeUnavailable, "power management is not configured")
		return nil, false
	}
	return s.d.Power, true
}

func writePowerErr(w http.ResponseWriter, err error) {
	var pe *power.Error
	if errors.As(err, &pe) {
		status := http.StatusInternalServerError
		if pe.Code == power.CodeUnavailable {
			status = http.StatusServiceUnavailable
		}
		writeError(w, status, pe.Code, "%s", pe.Message)
		return
	}
	writeError(w, http.StatusInternalServerError, "internal", "%v", err)
}

// getPower 는 상시 가동 상태를 돌려준다.
func (s *server) getPower(w http.ResponseWriter, r *http.Request) {
	pm, ok := s.power(w)
	if !ok {
		return
	}
	st, err := pm.Status(r.Context())
	if err != nil {
		writePowerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putPower 는 상시 가동을 켜거나 끈다.
func (s *server) putPower(w http.ResponseWriter, r *http.Request) {
	var body struct {
		AlwaysOn *bool `json:"always_on"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.AlwaysOn == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "always_on is required")
		return
	}
	pm, ok := s.power(w)
	if !ok {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	st, err := pm.Set(r.Context(), *body.AlwaysOn)
	if err != nil {
		writePowerErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}
