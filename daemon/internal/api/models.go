package api

import (
	"errors"
	"io/fs"
	"net/http"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/hf"
	"github.com/nxodh/operetaos/daemon/internal/platform"
	"github.com/nxodh/operetaos/daemon/internal/supervisor"
)

// 검색과 목록의 상한이다.
const (
	defaultSearchLimit = 20
	maxSearchLimit     = 100
)

// modelView 는 모델 한 개의 JSON 모양이다.
type modelView struct {
	ID              string `json:"id"`
	SizeBytes       int64  `json:"size_bytes"`
	Repo            string `json:"repo,omitempty"`
	Revision        string `json:"revision,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	License         string `json:"license,omitempty"`
	Architecture    string `json:"architecture,omitempty"`
	Name            string `json:"name,omitempty"`
	FileType        string `json:"file_type,omitempty"`
	BlockCount      uint64 `json:"block_count"`
	ContextLength   uint64 `json:"context_length"`
	EmbeddingLength uint64 `json:"embedding_length"`
	Default         bool   `json:"default"`
	Incomplete      bool   `json:"incomplete"`
}

// listModels 는 로컬 모델 목록을 돌려준다.
func (s *server) listModels(w http.ResponseWriter, r *http.Request) {
	list, err := s.d.Store.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]modelView, 0, len(list))
	def := ""
	for _, m := range list {
		if m.Default {
			def = m.ID
		}
		out = append(out, modelView{
			ID:              m.ID,
			SizeBytes:       m.Size,
			Repo:            m.Repo,
			Revision:        m.Revision,
			SHA256:          m.SHA256,
			License:         m.License,
			Architecture:    m.Architecture,
			Name:            m.Name,
			FileType:        m.FileTypeName,
			BlockCount:      m.BlockCount,
			ContextLength:   m.ContextLength,
			EmbeddingLength: m.EmbeddingLength,
			Default:         m.Default,
			Incomplete:      m.Incomplete,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"default": def, "models": out})
}

// deleteModel 은 모델을 지운다. 실행 중이거나 상태가 불확실한 모델이면 409 다.
func (s *server) deleteModel(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	list, err := s.d.Store.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	found := false
	for _, m := range list {
		if m.ID == id {
			found = true
			break
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "model %q was not found", id)
		return
	}
	st := s.d.Supervisor.Status()
	if st.UnitUnverifiable {
		writeError(w, http.StatusConflict, "server_state_unverifiable", "the server unit could not be verified; stop or verify the server before deleting models")
		return
	}
	if st.PID > 0 && st.ModelID == "" {
		writeError(w, http.StatusConflict, "server_state_unverifiable", "the server has an active process but its model is unknown; stop or verify the server before deleting models")
		return
	}
	if st.ModelID == id && st.PID > 0 {
		writeError(w, http.StatusConflict, "model_in_use", "model %q is used by the active server process; stop the server first", id)
		return
	}
	if st.ModelID == id && (st.State == supervisor.StateStarting || st.State == supervisor.StateRunning || st.State == supervisor.StateRunningCPU) {
		writeError(w, http.StatusConflict, "model_in_use", "model %q is used by the running server; stop the server first", id)
		return
	}
	if err := s.d.Store.Remove(id); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}

// putDefaultModel 은 기본 모델을 정한다. 본문은 {"id": "..."} 다.
func (s *server) putDefaultModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		ID string `json:"id"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if err := s.d.Store.SetDefault(body.ID); err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeError(w, http.StatusNotFound, "not_found", "model %q was not found", body.ID)
			return
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"default": body.ID})
}

// searchModels 는 Hugging Face 에서 GGUF 모델을 찾는다. q 는 필수다.
func (s *server) searchModels(w http.ResponseWriter, r *http.Request) {
	if !rejectUnknownQuery(w, r, "q", "limit") {
		return
	}
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	if q == "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "q is required")
		return
	}
	limit, ok := intQuery(w, r, "limit", defaultSearchLimit, 1, maxSearchLimit)
	if !ok {
		return
	}
	results, err := s.d.HF.Search(r.Context(), q, limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	type item struct {
		ID            string `json:"id"`
		Downloads     uint64 `json:"downloads"`
		Gated         bool   `json:"gated"`
		License       string `json:"license,omitempty"`
		Architecture  string `json:"architecture,omitempty"`
		ContextLength uint64 `json:"context_length"`
	}
	out := make([]item, 0, len(results))
	for _, res := range results {
		out = append(out, item{
			ID:            res.ID,
			Downloads:     res.Downloads,
			Gated:         gated(res.Gated),
			License:       res.License,
			Architecture:  res.Architecture,
			ContextLength: res.ContextLength,
		})
	}
	writeJSON(w, http.StatusOK, map[string]any{"results": out})
}

// gated 는 게이팅 모드가 접근 제한인지 본다.
func gated(m hf.GateMode) bool {
	return m == hf.GateAuto || m == hf.GateManual
}

// remoteModel 은 저장소의 GGUF 파일 목록과 라이선스, 게이팅 여부를 돌려준다.
func (s *server) remoteModel(w http.ResponseWriter, r *http.Request) {
	if !rejectUnknownQuery(w, r, "repo", "revision") {
		return
	}
	repo := r.URL.Query().Get("repo")
	if repo == "" {
		writeError(w, http.StatusBadRequest, "invalid_query", "repo is required")
		return
	}
	rev := r.URL.Query().Get("revision")
	if rev == "" {
		rev = "main"
	}
	info, err := s.d.HF.ModelInfo(r.Context(), repo)
	if err != nil {
		writeErr(w, err)
		return
	}
	files, err := s.d.HF.ListFiles(r.Context(), repo, rev)
	if err != nil {
		writeErr(w, err)
		return
	}
	type file struct {
		Path   string `json:"path"`
		Size   int64  `json:"size"`
		SHA256 string `json:"sha256,omitempty"`
		LFS    bool   `json:"lfs"`
	}
	out := []file{}
	for _, f := range files {
		if !strings.HasSuffix(strings.ToLower(f.Path), ".gguf") {
			continue
		}
		out = append(out, file{Path: f.Path, Size: f.Size, SHA256: f.SHA256, LFS: f.LFS})
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"repo":      repo,
		"revision":  rev,
		"license":   info.License,
		"gated":     gated(info.Gated),
		"gate_mode": string(info.Gated),
		"files":     out,
	})
}

// pullModel 은 내려받기 작업을 만든다. 본문은 {"repo","file","revision"} 이다. 응답은 202 다.
func (s *server) pullModel(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Repo     string `json:"repo"`
		File     string `json:"file"`
		Revision string `json:"revision"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if s.d.Jobs == nil {
		writeError(w, http.StatusInternalServerError, "internal", "download jobs are not configured")
		return
	}
	job, err := s.d.Jobs.StartPull(r.Context(), platform.PullRequest{Repo: body.Repo, File: body.File, Revision: body.Revision})
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, jobOf(job))
}

// listJobs 는 내려받기 작업 목록이다.
func (s *server) listJobs(w http.ResponseWriter, r *http.Request) {
	if s.d.Jobs == nil {
		writeJSON(w, http.StatusOK, map[string]any{"jobs": []jobView{}})
		return
	}
	list := s.d.Jobs.List()
	out := make([]jobView, 0, len(list))
	for _, j := range list {
		out = append(out, jobOf(j))
	}
	writeJSON(w, http.StatusOK, map[string]any{"jobs": out})
}

// getJob 은 작업 하나다. 없으면 404 다.
func (s *server) getJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.d.Jobs == nil {
		writeError(w, http.StatusNotFound, "not_found", "job %q was not found", id)
		return
	}
	job, ok := s.d.Jobs.Get(id)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "job %q was not found", id)
		return
	}
	writeJSON(w, http.StatusOK, jobOf(job))
}

// deleteJob 은 작업을 취소한다. 없으면 404, 취소할 수 없으면 409 다.
func (s *server) deleteJob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if s.d.Jobs == nil {
		writeError(w, http.StatusNotFound, "not_found", "job %q was not found", id)
		return
	}
	if _, ok := s.d.Jobs.Get(id); !ok {
		writeError(w, http.StatusNotFound, "not_found", "job %q was not found", id)
		return
	}
	if err := s.d.Jobs.Cancel(id); err != nil {
		writeError(w, http.StatusConflict, "job_not_cancelable", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "canceled": true})
}

// jobOf 는 platform.Job 을 JSON 모양으로 바꾼다.
func jobOf(j platform.Job) jobView {
	return jobView{
		ID:         j.ID,
		State:      string(j.State),
		Repo:       j.Repo,
		File:       j.File,
		Revision:   j.Revision,
		BytesDone:  j.BytesDone,
		BytesTotal: j.BytesTotal,
		ModelID:    j.ModelID,
		License:    j.License,
		ErrorCode:  j.ErrorCode,
		Error:      j.Error,
		Started:    j.Started,
		Finished:   j.Finished,
	}
}
