package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
	"github.com/nxodh/diparnuxos/daemon/internal/svcctl"
	"github.com/nxodh/diparnuxos/daemon/internal/sysstat"
)

// ServiceManager 는 서비스 목록과 제어다. *svcctl.Manager 가 만족한다.
type ServiceManager interface {
	List(ctx context.Context) ([]svcctl.Status, error)
	Do(ctx context.Context, unit, action string) error
	Logs(ctx context.Context, unit string, lines int) (string, error)
}

// StatReader 는 자원 사용량 측정이다. *sysstat.Reader 가 만족한다.
type StatReader interface {
	Read(ctx context.Context) (sysstat.Sample, error)
}

func svcStatus(code string) int {
	switch code {
	case svcctl.CodeNotAllowed:
		return http.StatusNotFound
	case svcctl.CodeProtected:
		return http.StatusForbidden
	case svcctl.CodeInvalid:
		return http.StatusBadRequest
	}
	return http.StatusBadGateway
}

func (s *server) services(w http.ResponseWriter) (ServiceManager, bool) {
	if s.d.Services == nil {
		writeError(w, http.StatusServiceUnavailable, "services_unavailable", "service management is not configured")
		return nil, false
	}
	return s.d.Services, true
}

func (s *server) listServices(w http.ResponseWriter, r *http.Request) {
	m, ok := s.services(w)
	if !ok {
		return
	}
	list, err := m.List(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"services": list})
}

func (s *server) postServiceAction(w http.ResponseWriter, r *http.Request) {
	m, ok := s.services(w)
	if !ok {
		return
	}
	unit, action := r.PathValue("unit"), r.PathValue("action")
	if err := m.Do(r.Context(), unit, action); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": unit, "action": action, "done": true})
}

func (s *server) getServiceLogs(w http.ResponseWriter, r *http.Request) {
	m, ok := s.services(w)
	if !ok {
		return
	}
	lines := 0
	if v := r.URL.Query().Get("lines"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_input", "lines must be a number")
			return
		}
		lines = n
	}
	text, err := m.Logs(r.Context(), r.PathValue("unit"), lines)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"unit": r.PathValue("unit"), "log": text})
}

// getResources 는 CPU, 메모리, 디스크, 네트워크 처리량, 온도, 메모리를 많이 쓰는 프로세스와 GPU 다.
func (s *server) getResources(w http.ResponseWriter, r *http.Request) {
	if s.d.Stats == nil {
		writeError(w, http.StatusServiceUnavailable, "resources_unavailable", "resource monitoring is not configured")
		return
	}
	sample, err := s.d.Stats.Read(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	snap := platform.GPUSnapshot{Available: false, Reason: "no GPU prober is configured"}
	if s.d.GPU != nil {
		snap = s.d.GPU.Snapshot(r.Context())
	}
	gpus := []deviceView{}
	if snap.Available {
		for _, d := range snap.Devices {
			gpus = append(gpus, deviceViewOf(d))
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"system": sample, "gpus": gpus, "gpu_available": snap.Available, "gpu_reason": snap.Reason})
}
