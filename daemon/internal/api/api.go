// Package api는 lightlixd 의 REST API 를 구현한다. 서비스는 유닉스 소켓에서만 돈다.
//
// 인증은 소켓 권한이 맡는다. 소켓은 0660 이고 그룹은 lightlix 이므로 그 그룹의 사용자만 연결할 수 있다.
// 이 패키지는 TCP 로 서비스하지 않는다. 데몬 자체의 API 키 검사는 없다.
//
// 모든 응답은 JSON이다. 오류는 {"error":{"code":"...","message":"..."}} 형태이고 HTTP 상태와 맞춘다.
// 메서드가 맞지 않으면 405, 경로가 없으면 404 를 같은 형태의 JSON 으로 돌려준다.
// 요청 본문은 1MiB 까지 받고, 모르는 JSON 필드는 거부한다.
//
// API 키 평문과 Hugging Face 토큰은 어떤 응답에도 나오지 않는다. 평문 키는 키 생성 응답에서 한 번만 보인다.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/apikeys"
	"github.com/nxodh/lightlixos/daemon/internal/autoconf"
	"github.com/nxodh/lightlixos/daemon/internal/gpustatus"
	"github.com/nxodh/lightlixos/daemon/internal/hf"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
	"github.com/nxodh/lightlixos/daemon/internal/supervisor"
)

// 요청과 응답의 크기 상한이다.
const (
	maxBodyBytes = 1 << 20
)

// Supervisor 는 API 가 쓰는 supervisor 기능이다. *supervisor.Supervisor 가 만족한다.
type Supervisor interface {
	Plan(ctx context.Context, modelID string, ov autoconf.Overrides) (supervisor.PlanResult, error)
	StartAsync(req supervisor.StartRequest) (supervisor.Status, error)
	Wait(ctx context.Context) (supervisor.Status, error)
	Stop(ctx context.Context) error
	Status() supervisor.Status
	Logs(ctx context.Context, lines int) (string, error)
	ApplyFirewall(ctx context.Context) error
	Mode(ctx context.Context) (string, error)
	SetMode(ctx context.Context, mode string) error
}

// HFClient 는 검색과 원격 파일 목록에 쓰는 Hugging Face 기능이다. *hf.Client 가 만족한다.
type HFClient interface {
	Search(ctx context.Context, query string, limit int) ([]hf.SearchResult, error)
	ModelInfo(ctx context.Context, repo string) (hf.ModelInfo, error)
	ListFiles(ctx context.Context, repo, rev string) ([]hf.File, error)
}

// Deps 는 API 가 쓰는 의존성과 설정이다.
type Deps struct {
	// Supervisor 는 llama-server 수명 주기다.
	Supervisor Supervisor
	// Jobs 는 모델 내려받기 작업이다.
	Jobs platform.Jobs
	// Store 는 모델 디렉터리다.
	Store models.Store
	// Keys 는 API 키 저장소다.
	Keys *apikeys.Store
	// HF 는 Hugging Face 조회다.
	HF HFClient
	// GPU 는 NVML 프로버다. nil 이면 NVML 을 쓸 수 없는 것으로 본다.
	GPU platform.GPUProber
	// Runner 는 외부 명령 실행기다. GPU 진단 실행에 쓴다.
	Runner platform.Runner
	// ConfigPath 는 설정 파일 경로다.
	ConfigPath string
	// GPUStatusPath 는 GPU 진단 상태 파일 경로다.
	GPUStatusPath string
	// GPUCheckPath 는 GPU 진단 스크립트 경로다. 기본 /usr/sbin/lightlix-gpu-check.
	GPUCheckPath string
	// MemInfo 는 메모리 상태를 돌려준다. nil 이면 메모리를 null 로 보고한다.
	MemInfo func() (platform.MemInfo, error)
	// Report 는 진단 리포트 텍스트를 만든다. nil 이면 GET /v1/report 는 오류다.
	Report func(ctx context.Context) string
	// Version 은 데몬 버전이다.
	Version string
	// Now 는 현재 시각이다. nil 이면 time.Now 를 쓴다.
	Now func() time.Time
	// ChownKeyFile 은 키 파일의 그룹을 lightlix-llm 으로 맞춘다. 키 파일을 쓸 때마다 부른다. nil 이면 아무것도 하지 않는다.
	ChownKeyFile func(path string) error
	// HTTP 는 llama-server 채팅 프록시에 쓰는 클라이언트다. nil 이면 기본 클라이언트를 쓴다.
	HTTP *http.Client
}

// server 는 핸들러 묶음이다.
type server struct {
	d   Deps
	mux *http.ServeMux
}

// New는 API 핸들러를 만든다. 반환값은 유닉스 소켓 서버에 그대로 꽂는다.
func New(d Deps) http.Handler {
	if d.Now == nil {
		d.Now = time.Now
	}
	if d.HTTP == nil {
		d.HTTP = &http.Client{}
	}
	if d.GPUCheckPath == "" {
		d.GPUCheckPath = "/usr/sbin/lightlix-gpu-check"
	}
	if d.GPUStatusPath == "" {
		d.GPUStatusPath = "/run/lightlix/gpu-status"
	}
	s := &server{d: d, mux: http.NewServeMux()}
	s.routes()
	return jsonErrors{next: s.mux}
}

// routes 는 경로 표를 등록한다.
func (s *server) routes() {
	s.mux.HandleFunc("GET /v1/status", s.getStatus)
	s.mux.HandleFunc("POST /v1/gpu/check", s.postGPUCheck)

	s.mux.HandleFunc("GET /v1/models", s.listModels)
	s.mux.HandleFunc("GET /v1/models/search", s.searchModels)
	s.mux.HandleFunc("GET /v1/models/remote", s.remoteModel)
	s.mux.HandleFunc("PUT /v1/models/default", s.putDefaultModel)
	s.mux.HandleFunc("DELETE /v1/models/{id}", s.deleteModel)
	s.mux.HandleFunc("POST /v1/models/pull", s.pullModel)

	s.mux.HandleFunc("GET /v1/jobs", s.listJobs)
	s.mux.HandleFunc("GET /v1/jobs/{id}", s.getJob)
	s.mux.HandleFunc("DELETE /v1/jobs/{id}", s.deleteJob)

	s.mux.HandleFunc("GET /v1/server/plan", s.getPlan)
	s.mux.HandleFunc("POST /v1/server/start", s.postStart)
	s.mux.HandleFunc("POST /v1/server/stop", s.postStop)
	s.mux.HandleFunc("GET /v1/server/logs", s.getLogs)
	s.mux.HandleFunc("POST /v1/server/chat", s.postChat)

	s.mux.HandleFunc("GET /v1/config", s.getConfig)
	s.mux.HandleFunc("PUT /v1/config", s.putConfig)

	s.mux.HandleFunc("GET /v1/apikeys", s.listKeys)
	s.mux.HandleFunc("POST /v1/apikeys", s.createKey)
	s.mux.HandleFunc("DELETE /v1/apikeys/{id}", s.deleteKey)

	s.mux.HandleFunc("GET /v1/mode", s.getMode)
	s.mux.HandleFunc("POST /v1/mode", s.postMode)

	s.mux.HandleFunc("POST /v1/update", s.postUpdate)
	s.mux.HandleFunc("GET /v1/report", s.getReport)
}

// jsonErrors 는 ServeMux 가 내는 일반 텍스트 404 와 405 를 JSON 오류로 바꾼다.
// 핸들러가 직접 쓴 오류는 Content-Type 이 JSON 이므로 그대로 둔다.
type jsonErrors struct {
	next http.Handler
}

func (h jsonErrors) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.next.ServeHTTP(&errorWriter{ResponseWriter: w}, r)
}

// errorWriter 는 ServeMux 의 평문 오류 응답을 가로채 JSON 으로 쓴다.
type errorWriter struct {
	http.ResponseWriter
	wrote bool
	skip  bool
}

func (w *errorWriter) WriteHeader(code int) {
	if w.wrote {
		return
	}
	w.wrote = true
	ct := w.Header().Get("Content-Type")
	if (code == http.StatusNotFound || code == http.StatusMethodNotAllowed) && !strings.HasPrefix(ct, "application/json") {
		w.skip = true
		w.Header().Del("X-Content-Type-Options")
		if code == http.StatusNotFound {
			writeError(w.ResponseWriter, code, "not_found", "no such endpoint")
		} else {
			writeError(w.ResponseWriter, code, "method_not_allowed", "method not allowed for this endpoint")
		}
		return
	}
	w.ResponseWriter.WriteHeader(code)
}

func (w *errorWriter) Write(b []byte) (int, error) {
	if w.skip {
		return len(b), nil
	}
	if !w.wrote {
		w.WriteHeader(http.StatusOK)
	}
	return w.ResponseWriter.Write(b)
}

// errBody 는 오류 응답의 JSON 모양이다.
type errBody struct {
	Error errDetail `json:"error"`
}

type errDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// writeJSON 은 JSON 응답을 쓴다.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

// writeError 는 JSON 오류 응답을 쓴다.
func writeError(w http.ResponseWriter, status int, code, format string, args ...any) {
	writeJSON(w, status, errBody{Error: errDetail{Code: code, Message: fmt.Sprintf(format, args...)}})
}

// supervisorStatus 는 supervisor 오류 코드를 HTTP 상태로 바꾼다.
func supervisorStatus(code string) int {
	switch code {
	case supervisor.CodeNoModel:
		return http.StatusNotFound
	case supervisor.CodeAPIKeyRequired, supervisor.CodeBusy, supervisor.CodeDesktopNotInstalled:
		return http.StatusConflict
	case supervisor.CodeInvalidOverrides, supervisor.CodeInvalidLines, supervisor.CodeInvalidMode:
		return http.StatusBadRequest
	case supervisor.CodeHealthTimeout:
		return http.StatusGatewayTimeout
	case supervisor.CodeWaitCancelled:
		return http.StatusRequestTimeout
	}
	return http.StatusInternalServerError
}

// writeErr 는 오류를 HTTP 상태와 JSON 으로 옮긴다. 알 수 없는 오류는 내부 오류로 숨기고 로그에 남긴다.
func writeErr(w http.ResponseWriter, err error) {
	var se *supervisor.Error
	if errors.As(err, &se) {
		writeError(w, supervisorStatus(se.Code), se.Code, "%s", se.Message)
		return
	}
	switch {
	case errors.Is(err, hf.ErrGated):
		writeError(w, http.StatusForbidden, "gated", "the repository is gated and the token has no access")
	case errors.Is(err, hf.ErrNotFound), errors.Is(err, fs.ErrNotExist):
		writeError(w, http.StatusNotFound, "not_found", "not found")
	case errors.Is(err, hf.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "rate_limited", "%v", err)
	case errors.Is(err, hf.ErrInsufficientSpace):
		writeError(w, http.StatusInsufficientStorage, "insufficient_space", "not enough free disk space")
	case errors.Is(err, hf.ErrInvalidInput), errors.Is(err, models.ErrInvalidID):
		writeError(w, http.StatusBadRequest, "invalid_input", "%v", err)
	case errors.Is(err, models.ErrOutsideDir), errors.Is(err, models.ErrNotRegular):
		writeError(w, http.StatusBadRequest, "invalid_input", "%v", err)
	default:
		log.Printf("api: internal error: %v", err)
		writeError(w, http.StatusInternalServerError, "internal", "internal error; see the daemon log")
	}
}

// decodeBody 는 JSON 본문을 읽는다. 크기 상한을 넘거나, 모르는 필드가 있거나, 형식이 틀리면 false 를 돌려주고 응답을 쓴다.
func decodeBody(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		var mbe *http.MaxBytesError
		if errors.As(err, &mbe) {
			writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "request body is larger than %d bytes", maxBodyBytes)
			return false
		}
		if errors.Is(err, io.EOF) {
			writeError(w, http.StatusBadRequest, "invalid_json", "a JSON request body is required")
			return false
		}
		writeError(w, http.StatusBadRequest, "invalid_json", "invalid JSON body: %v", err)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_json", "unexpected data after the JSON object")
		return false
	}
	return true
}

// rejectUnknownQuery 는 허용하지 않은 쿼리 키가 있으면 400 을 쓰고 false 를 돌려준다.
func rejectUnknownQuery(w http.ResponseWriter, r *http.Request, allowed ...string) bool {
	ok := make(map[string]bool, len(allowed))
	for _, a := range allowed {
		ok[a] = true
	}
	for k := range r.URL.Query() {
		if !ok[k] {
			writeError(w, http.StatusBadRequest, "invalid_query", "unknown query parameter %q", k)
			return false
		}
	}
	return true
}

// intQuery 는 정수 쿼리 값을 읽는다. 없으면 def 다. 범위를 벗어나면 400 을 쓰고 false 를 돌려준다.
func intQuery(w http.ResponseWriter, r *http.Request, key string, def, lo, hi int) (int, bool) {
	raw := r.URL.Query().Get(key)
	if raw == "" {
		return def, true
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_query", "%s must be an integer", key)
		return 0, false
	}
	if v < lo || v > hi {
		writeError(w, http.StatusBadRequest, "invalid_query", "%s must be %d-%d", key, lo, hi)
		return 0, false
	}
	return v, true
}

// getStatus 는 버전, 모드, GPU, 메모리, 서버 상태를 돌려준다.
func (s *server) getStatus(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	mode, err := s.d.Supervisor.Mode(ctx)
	if err != nil {
		mode = "unknown"
	}
	writeJSON(w, http.StatusOK, statusView{
		Version: s.d.Version,
		Mode:    mode,
		GPU:     s.gpuView(ctx),
		Memory:  s.memoryView(),
		Server:  s.serverView(),
	})
}

// gpuView 는 상태 파일과 NVML 스냅샷을 합친다.
func (s *server) gpuView(ctx context.Context) gpuInfo {
	var out gpuInfo
	var fileState gpustatus.State
	st, err := gpustatus.Load(s.d.GPUStatusPath)
	if err != nil {
		out.StatusError = describeStatusError(err)
	} else {
		v := statusFileView(st)
		out.Status = &v
		fileState = st.State
		out.SecureBoot = st.SecureBoot
		out.DriverVersion = st.DriverVersion
	}
	out.OpenModule = openModule(fileState)

	snap := platform.GPUSnapshot{Available: false, Reason: "no GPU prober is configured"}
	if s.d.GPU != nil {
		snap = s.d.GPU.Snapshot(ctx)
	}
	out.NVML = nvmlView{
		Available:         snap.Available,
		Reason:            snap.Reason,
		NVMLVersion:       snap.NVMLVersion,
		CUDADriverVersion: snap.CUDADriverVersion,
		Devices:           []deviceView{},
	}
	if snap.Available {
		if snap.DriverVersion != "" {
			out.DriverVersion = snap.DriverVersion
		}
		for _, d := range snap.Devices {
			out.NVML.Devices = append(out.NVML.Devices, deviceView{
				Index:          d.Index,
				Name:           d.Name,
				Arch:           d.Arch,
				ComputeCap:     d.ComputeCap,
				MemTotalMiB:    d.MemTotalMiB,
				MemFreeMiB:     d.MemFreeMiB,
				MemUsedMiB:     d.MemUsedMiB,
				TemperatureC:   d.TemperatureC,
				PowerMilliwatt: d.PowerMilliwatt,
				UtilGPUPercent: d.UtilGPUPercent,
				UtilMemPercent: d.UtilMemPercent,
			})
		}
	}
	return out
}

// openModule 은 상태 파일의 상태로 오픈 모듈 여부를 추정한다. ready 는 독점 모듈이 감지되지 않았다는 뜻으로 본다.
func openModule(st gpustatus.State) string {
	switch st {
	case gpustatus.StateReady:
		return "yes"
	case gpustatus.StateProprietaryModule:
		return "no"
	}
	return "unknown"
}

// describeStatusError 는 상태 파일 오류를 짧게 설명한다. 파일이 없으면 진단이 아직 돌지 않은 것이다.
func describeStatusError(err error) string {
	if errors.Is(err, os.ErrNotExist) {
		return "GPU check has not run"
	}
	return "GPU status file cannot be read"
}

// memoryView 는 메모리 상태다. 읽지 못하면 null 이다.
func (s *server) memoryView() *memInfoView {
	if s.d.MemInfo == nil {
		return nil
	}
	mem, err := s.d.MemInfo()
	if err != nil {
		return nil
	}
	return &memInfoView{TotalMiB: mem.TotalMiB, AvailableMiB: mem.AvailableMiB}
}

// serverView 는 서버 상태와 그 계획이다.
func (s *server) serverView() serverInfo {
	st := s.d.Supervisor.Status()
	return serverInfo{Status: st, Plan: planViewOf(st.Plan)}
}

// postGPUCheck 는 GPU 진단 스크립트를 실행하고 상태 파일을 다시 읽어 돌려준다.
// 종료 코드 0, 1, 2 는 정상 결과다. 3 이상이나 실행 실패는 오류다.
func (s *server) postGPUCheck(w http.ResponseWriter, r *http.Request) {
	if s.d.Runner == nil {
		writeError(w, http.StatusInternalServerError, "gpu_check_failed", "no command runner is configured")
		return
	}
	res, err := s.d.Runner.Run(r.Context(), platform.Command{
		Name:      s.d.GPUCheckPath,
		Timeout:   150 * time.Second,
		MaxOutput: 64 << 10,
	})
	if err != nil {
		writeError(w, http.StatusInternalServerError, "gpu_check_failed", "cannot run the GPU check: %v", err)
		return
	}
	if res.ExitCode > 2 || res.ExitCode < 0 {
		writeError(w, http.StatusInternalServerError, "gpu_check_failed", "the GPU check exited %d: %s", res.ExitCode, tail(string(res.Stderr)+string(res.Stdout), 512))
		return
	}
	st, err := gpustatus.Load(s.d.GPUStatusPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "gpu_status_unreadable", "the GPU check ran but its status file cannot be read: %s", describeStatusError(err))
		return
	}
	v := statusFileView(st)
	writeJSON(w, http.StatusOK, gpuCheckView{ExitCode: res.ExitCode, Status: v})
}

// getMode 는 현재 모드를 돌려준다.
func (s *server) getMode(w http.ResponseWriter, r *http.Request) {
	mode, err := s.d.Supervisor.Mode(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": mode})
}

// postMode 는 모드를 바꾼다. 본문은 {"mode":"server"|"desktop"} 이다.
func (s *server) postMode(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Mode string `json:"mode"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.d.Supervisor.SetMode(r.Context(), body.Mode); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"mode": body.Mode})
}

// postUpdate 는 업데이트 기능이 아직 없다.
func (s *server) postUpdate(w http.ResponseWriter, r *http.Request) {
	writeError(w, http.StatusNotImplemented, "not_implemented", "update arrives in a later release")
}

// getReport 는 진단 리포트를 평문으로 돌려준다.
func (s *server) getReport(w http.ResponseWriter, r *http.Request) {
	if s.d.Report == nil {
		writeError(w, http.StatusInternalServerError, "report_unavailable", "the report generator is not configured")
		return
	}
	text := s.d.Report(r.Context())
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, text)
}

// tail 은 문자열의 끝 n 바이트를 돌려준다. 오류 메시지에 쓴다.
func tail(v string, n int) string {
	v = strings.TrimSpace(v)
	if len(v) > n {
		return "..." + v[len(v)-n:]
	}
	return v
}
