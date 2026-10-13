package api

import (
	"context"
	"net/http"
	"strconv"

	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/sshconf"
)

// SSHConfManager 는 SSH 고급 설정이다. *sshconf.Manager 가 만족한다.
type SSHConfManager interface {
	Settings() sshconf.Settings
	Apply(ctx context.Context, s sshconf.Settings, sshActive bool) error
	CountKeys(ctx context.Context) (int, error)
	ListKeys(ctx context.Context, user string) ([]sshconf.Key, error)
	AddKey(ctx context.Context, user, line string) (sshconf.Key, bool, error)
	RemoveKey(ctx context.Context, user, fingerprint string) error
	HostKeys(ctx context.Context) []sshconf.Key
	RecentLogins(ctx context.Context, limit int) ([]sshconf.LoginEvent, error)
}

func sshconfStatus(code string) int {
	switch code {
	case sshconf.CodeInvalidInput:
		return http.StatusBadRequest
	case sshconf.CodeNotFound:
		return http.StatusNotFound
	case sshconf.CodeNoKeys, sshconf.CodeSSHConfig:
		return http.StatusConflict
	}
	return http.StatusInternalServerError
}

func (s *server) sshconf(w http.ResponseWriter) (SSHConfManager, bool) {
	if s.d.SSHConf == nil {
		writeError(w, http.StatusServiceUnavailable, "ssh_unavailable", "SSH configuration is not available")
		return nil, false
	}
	return s.d.SSHConf, true
}

func (s *server) sshAdvancedView(r *http.Request, m SSHConfManager, cfg config.Config) map[string]any {
	set := m.Settings()
	keys, _ := m.CountKeys(r.Context())
	var configured any
	if set.PasswordLogin != nil {
		configured = *set.PasswordLogin
	}
	port := set.Port
	if port == 0 {
		port = sshconf.DefaultPort
	}
	return map[string]any{
		"enabled":                   cfg.SSHEnabled,
		"port":                      port,
		"password_login_configured": configured,
		"key_count":                 keys,
		"host_keys":                 m.HostKeys(r.Context()),
	}
}

// getSSHAdvanced 는 포트, 비밀번호 로그인 설정, 등록된 키 수, 호스트 키 지문이다.
func (s *server) getSSHAdvanced(w http.ResponseWriter, r *http.Request) {
	m, ok := s.sshconf(w)
	if !ok {
		return
	}
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, s.sshAdvancedView(r, m, cfg))
}

// putSSHAdvanced 는 포트와 비밀번호 로그인을 바꾼다. 본문의 없는 항목은 그대로 둔다.
// 포트를 바꿀 때는 방화벽에 새 포트를 먼저 열고 sshd 를 다시 읽게 한다. 실패하면 모두 되돌린다.
func (s *server) putSSHAdvanced(w http.ResponseWriter, r *http.Request) {
	m, ok := s.sshconf(w)
	if !ok {
		return
	}
	var body struct {
		Port          *int  `json:"port"`
		PasswordLogin *bool `json:"password_login"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	old, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	set := m.Settings()
	if body.Port != nil {
		if *body.Port < 1 || *body.Port > 65535 {
			writeError(w, http.StatusBadRequest, "invalid_input", "the port must be 1-65535")
			return
		}
		set.Port = *body.Port
	}
	if body.PasswordLogin != nil {
		set.PasswordLogin = body.PasswordLogin
	}
	next := old
	if set.Port == sshconf.DefaultPort {
		next.SSHPort = 0
	} else {
		next.SSHPort = set.Port
	}
	if next.SSHPort != old.SSHPort {
		if err := config.Save(s.d.ConfigPath, next); err != nil {
			writeError(w, http.StatusInternalServerError, "config_write_failed", "%v", err)
			return
		}
		if err := s.d.Supervisor.ApplyFirewall(r.Context()); err != nil {
			_ = config.Save(s.d.ConfigPath, old)
			_ = s.d.Supervisor.ApplyFirewall(r.Context())
			writeError(w, http.StatusInternalServerError, "firewall_failed", "the port was not changed: %v", err)
			return
		}
	}
	st, _ := s.d.Supervisor.SSHStatus(r.Context())
	if err := m.Apply(r.Context(), set, st.Active); err != nil {
		if next.SSHPort != old.SSHPort {
			_ = config.Save(s.d.ConfigPath, old)
			_ = s.d.Supervisor.ApplyFirewall(r.Context())
		}
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, s.sshAdvancedView(r, m, next))
}

type sshKeyBody struct {
	User        string `json:"user"`
	Key         string `json:"key"`
	Fingerprint string `json:"fingerprint"`
}

// listSSHKeys 는 사용자의 공개 키 목록이다. ?user=<이름>.
func (s *server) listSSHKeys(w http.ResponseWriter, r *http.Request) {
	m, ok := s.sshconf(w)
	if !ok {
		return
	}
	keys, err := m.ListKeys(r.Context(), r.URL.Query().Get("user"))
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"user": r.URL.Query().Get("user"), "keys": keys})
}

func (s *server) postSSHKey(w http.ResponseWriter, r *http.Request) {
	m, ok := s.sshconf(w)
	if !ok {
		return
	}
	var body sshKeyBody
	if !decodeBody(w, r, &body) {
		return
	}
	k, added, err := m.AddKey(r.Context(), body.User, body.Key)
	if err != nil {
		writeErr(w, err)
		return
	}
	status := http.StatusCreated
	if !added {
		status = http.StatusOK
	}
	writeJSON(w, status, map[string]any{"key": k, "added": added})
}

func (s *server) deleteSSHKey(w http.ResponseWriter, r *http.Request) {
	m, ok := s.sshconf(w)
	if !ok {
		return
	}
	var body sshKeyBody
	if !decodeBody(w, r, &body) {
		return
	}
	if err := m.RemoveKey(r.Context(), body.User, body.Fingerprint); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"deleted": true})
}

// getSSHLog 는 최근 접속 결과다. ?limit=N (1-200, 기본 50).
func (s *server) getSSHLog(w http.ResponseWriter, r *http.Request) {
	m, ok := s.sshconf(w)
	if !ok {
		return
	}
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeError(w, http.StatusBadRequest, "invalid_input", "limit must be 1-200")
			return
		}
		limit = n
	}
	ev, err := m.RecentLogins(r.Context(), limit)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"events": ev})
}
