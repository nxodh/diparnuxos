package api

import (
	"errors"
	"net/http"
	"unicode/utf8"

	"github.com/nxodh/operetaos/daemon/internal/apikeys"
	"github.com/nxodh/operetaos/daemon/internal/config"
	"github.com/nxodh/operetaos/daemon/internal/supervisor"
)

// redactedToken 은 토큰 자리에 돌아온 가린 값이다. PUT 에서 이 값이 오면 기존 토큰을 그대로 둔다.
const redactedToken = "<redacted>"

// configPatch 는 PUT /v1/config 의 부분 갱신 본문이다. 없는 필드는 그대로 둔다.
// overrides 가 있으면 재정의 전체를 그 값으로 바꾼다(없는 재정의 항목은 자동 계획을 쓴다).
type configPatch struct {
	Port       *int           `json:"port"`
	LANExposed *bool          `json:"lan_exposed"`
	Autostart  *bool          `json:"autostart"`
	ModelsDir  *string        `json:"models_dir"`
	HFToken    *string        `json:"hf_token"`
	Overrides  *overridesJSON `json:"overrides"`
	// Gateway 는 데몬이 공개 포트를 받아 키를 확인하게 할지다. 바꾸면 서버를 다시 시작해야 반영된다.
	Gateway *bool `json:"gateway"`
	// LocalNoKey 는 이 컴퓨터에서 온 요청이 키 없이 통과하는지다. 바로 반영된다.
	LocalNoKey *bool `json:"local_no_key"`
	// AutoRestart 는 서버가 예상 밖으로 멈췄을 때 데몬이 다시 시작할지다. 바로 반영된다.
	AutoRestart *bool `json:"auto_restart"`
}

// getConfig 는 설정 파일을 읽어 토큰을 가리고 돌려준다.
func (s *server) getConfig(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, cfg.Redacted())
}

// putConfig 는 설정을 부분 갱신한다. 검사 후 저장하고 방화벽을 다시 적용한다.
// restart_required 는 llama-server 나 HF 클라이언트에 영향을 주는 값이 바뀌었다는 뜻이다.
func (s *server) putConfig(w http.ResponseWriter, r *http.Request) {
	var patch configPatch
	if !decodeBody(w, r, &patch) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	old, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	next := old
	if patch.Port != nil {
		next.Port = *patch.Port
	}
	if patch.LANExposed != nil {
		next.LANExposed = *patch.LANExposed
	}
	if patch.Autostart != nil {
		next.Autostart = *patch.Autostart
	}
	if patch.ModelsDir != nil {
		next.ModelsDir = *patch.ModelsDir
	}
	if patch.HFToken != nil && *patch.HFToken != redactedToken {
		if !validToken(*patch.HFToken) {
			writeError(w, http.StatusBadRequest, "invalid_config", "hf_token must be printable ASCII without spaces and at most 256 bytes")
			return
		}
		next.HFToken = *patch.HFToken
	}
	if patch.Gateway != nil {
		next.Gateway = *patch.Gateway
	}
	if patch.AutoRestart != nil {
		next.AutoRestart = *patch.AutoRestart
	}
	if patch.LocalNoKey != nil {
		next.LocalNoKey = *patch.LocalNoKey
	}
	if patch.Overrides != nil {
		next.Overrides = config.Overrides{
			NGL:       patch.Overrides.NGL,
			Ctx:       patch.Overrides.Ctx,
			CacheType: patch.Overrides.CacheType,
			Batch:     patch.Overrides.Batch,
			LoadMode:  patch.Overrides.LoadMode,
			Parallel:  patch.Overrides.Parallel,
			Profile:   patch.Overrides.Profile,
		}
	}
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", "%v", err)
		return
	}
	if next.LANExposed {
		n, err := s.keyCount()
		if err != nil {
			writeErr(w, err)
			return
		}
		if n == 0 {
			writeError(w, http.StatusConflict, "api_key_required", "LAN exposure requires at least one API key; create a key first")
			return
		}
	}
	if err := config.Save(s.d.ConfigPath, next); err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", "%v", err)
		return
	}
	if err := s.d.Supervisor.ApplyFirewall(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "firewall_failed", "the config was saved, but the firewall rules were not applied: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"config":           next.Redacted(),
		"restart_required": restartRequired(old, next),
	})
}

// restartRequired 는 llama-server 재시작이나 데몬 재시작이 필요한 설정 변경인지 본다.
func restartRequired(old, next config.Config) bool {
	if old.Gateway != next.Gateway || old.Port != next.Port || old.LANExposed != next.LANExposed || old.ModelsDir != next.ModelsDir || old.HFToken != next.HFToken {
		return true
	}
	return !sameOverrides(old.Overrides, next.Overrides)
}

// sameOverrides 는 재정의 두 벌이 같은지 본다.
func sameOverrides(a, b config.Overrides) bool {
	return sameStr(a.NGL, b.NGL) && sameInt(a.Ctx, b.Ctx) && sameStr(a.CacheType, b.CacheType) &&
		sameInt(a.Batch, b.Batch) && sameStr(a.LoadMode, b.LoadMode) && sameInt(a.Parallel, b.Parallel) && sameStr(a.Profile, b.Profile)
}

func sameStr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func sameInt(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// validToken 은 토큰이 출력 가능한 ASCII 이고 공백이 없으며 256바이트 이하인지 본다.
func validToken(t string) bool {
	if t == "" {
		return true
	}
	if len(t) > 256 || !utf8.ValidString(t) {
		return false
	}
	for i := 0; i < len(t); i++ {
		if c := t[i]; c <= 0x20 || c >= 0x7f {
			return false
		}
	}
	return true
}

// keyCount 는 API 키 개수다. 저장소가 없으면 0 이다.
func (s *server) keyCount() (int, error) {
	if s.d.Keys == nil {
		return 0, nil
	}
	return s.d.Keys.Count()
}

// keyView 는 키 목록의 항목이다. 평문과 해시는 없다.
type keyView struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Created string `json:"created"`
	Prefix  string `json:"prefix"`
}

// listKeys 는 키 목록을 돌려준다.
func (s *server) listKeys(w http.ResponseWriter, r *http.Request) {
	if s.d.Keys == nil {
		writeJSON(w, http.StatusOK, map[string]any{"keys": []keyView{}})
		return
	}
	list, err := s.d.Keys.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	out := make([]keyView, 0, len(list))
	for _, k := range list {
		out = append(out, keyView{ID: k.ID, Label: k.Label, Created: k.Created.UTC().Format("2006-01-02T15:04:05Z"), Prefix: k.Prefix})
	}
	writeJSON(w, http.StatusOK, map[string]any{"keys": out})
}

// createKey 는 새 키를 만든다. 평문 키는 이 응답에서만 나온다. 응답은 201 이다.
func (s *server) createKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Label string `json:"label"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if s.d.Keys == nil {
		writeError(w, http.StatusInternalServerError, "internal", "API key storage is not configured")
		return
	}
	if !validLabel(body.Label) {
		writeError(w, http.StatusBadRequest, "invalid_label", "label must be 1-64 printable ASCII characters")
		return
	}
	key, plain, err := s.d.Keys.Create(body.Label)
	if err != nil {
		writeErr(w, err)
		return
	}
	if err := s.chownKeys(); err != nil {
		// 그룹을 맞추지 못하면 llama-server 가 키 파일을 읽지 못한다. 방금 만든 키를 되돌린다.
		if delErr := s.d.Keys.Delete(key.ID); delErr != nil {
			writeError(w, http.StatusInternalServerError, "key_file_failed", "%v; rollback also failed: %v", err, delErr)
			return
		}
		writeError(w, http.StatusInternalServerError, "key_file_failed", "%v", err)
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"id":               key.ID,
		"label":            key.Label,
		"prefix":           key.Prefix,
		"created":          key.Created.UTC().Format("2006-01-02T15:04:05Z"),
		"key":              plain,
		"note":             "the key is shown only once; store it now",
		"restart_required": s.apiKeyRestartRequired(),
	})
}

// deleteKey 는 키를 지운다. LAN 공개 중에는 마지막 키를 지울 수 없다(409).
func (s *server) deleteKey(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	if s.d.Keys == nil {
		writeError(w, http.StatusNotFound, "not_found", "key %q was not found", id)
		return
	}

	list, err := s.d.Keys.List()
	if err != nil {
		// 키 폐기 뒤 메타 저장에 실패했을 때 같은 삭제를 다시 요청하면 오래된
		// 메타 항목을 복구할 수 있다. 저장 설정이나 현재 서버가 LAN에 공개된
		// 상태에서는 복구를 허용하지 않는다.
		cfg, cfgErr := config.Load(s.d.ConfigPath)
		if cfgErr != nil {
			writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", cfgErr)
			return
		}
		st := s.d.Supervisor.Status()
		if cfg.LANExposed || st.LANExposed || st.UnitUnverifiable {
			writeError(w, http.StatusInternalServerError, "api_key_store_inconsistent", "API key storage is inconsistent; disable LAN exposure and stop or verify the server before retrying the key deletion")
			return
		}
		if err := s.d.Keys.Delete(id); err != nil {
			if errors.Is(err, apikeys.ErrNotFound) {
				writeError(w, http.StatusNotFound, "not_found", "key %q was not found", id)
				return
			}
			writeErr(w, err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"id":               id,
			"deleted":          true,
			"restart_required": s.apiKeyRestartRequired(),
		})
		return
	}
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	found := false
	for _, k := range list {
		if k.ID == id {
			found = true
		}
	}
	if !found {
		writeError(w, http.StatusNotFound, "not_found", "key %q was not found", id)
		return
	}
	if cfg.LANExposed && len(list) == 1 {
		writeError(w, http.StatusConflict, "last_key_in_use", "the last API key cannot be deleted while LAN exposure is on")
		return
	}
	if err := s.d.Keys.Delete(id); err != nil {
		if errors.Is(err, apikeys.ErrNotFound) {
			writeError(w, http.StatusNotFound, "not_found", "key %q was not found", id)
			return
		}
		writeErr(w, err)
		return
	}
	if err := s.chownKeys(); err != nil {
		writeError(w, http.StatusInternalServerError, "key_file_failed", "the key was deleted, but the key file group could not be set: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"id":               id,
		"deleted":          true,
		"restart_required": s.apiKeyRestartRequired(),
	})
}

func (s *server) apiKeyRestartRequired() bool {
	if s.d.Supervisor == nil {
		return false
	}
	st := s.d.Supervisor.Status()
	// 게이트웨이가 키를 읽는 서버는 키 변경을 바로 반영한다(내부 포트가 공개 포트와 다르다). 다시 시작할 필요가 없다.
	if st.BackendPort != 0 && st.BackendPort != st.Port && s.d.Gateway != nil {
		return false
	}
	if st.UnitUnverifiable || (st.State == supervisor.StateFailed && st.PID > 0) {
		return true
	}
	switch st.State {
	case supervisor.StateStarting, supervisor.StateRunning, supervisor.StateRunningCPU:
		return true
	default:
		return false
	}
}

// chownKeys 는 키 파일을 쓴 뒤 그룹을 맞춘다. 원자적 쓰기는 파일을 새로 만들므로 매번 필요하다.
func (s *server) chownKeys() error {
	if s.d.ChownKeyFile == nil || s.d.Keys == nil {
		return nil
	}
	return s.d.ChownKeyFile(s.d.Keys.KeyFile)
}

// validLabel 은 라벨이 1-64 자의 출력 가능한 ASCII 인지 본다.
func validLabel(label string) bool {
	if len(label) == 0 || len(label) > 64 {
		return false
	}
	for i := 0; i < len(label); i++ {
		if c := label[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}
