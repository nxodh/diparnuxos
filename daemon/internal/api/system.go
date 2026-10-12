package api

import (
	"net/http"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/firstboot"
	"github.com/nxodh/diparnuxos/daemon/internal/recommend"
	"github.com/nxodh/diparnuxos/daemon/internal/updatectl"
)

// SSH 를 켠 상태에서 보여 줄 안내 문구다.
const (
	sshWarnNoAccount = "No Diparnux account exists, so SSH accepts public keys only; create an account to log in with a password"
	sshWarnKeyOnly   = "SSH password login is disabled on this image; add your public key to ~/.ssh/authorized_keys of the login user to connect"
	sshWarnUnknown   = "SSH is open; make sure the login user has a strong password or an authorized public key"
)

// sshView 는 SSH 상태 응답이다.
type sshView struct {
	// Enabled 는 설정상 SSH 를 켠 상태(방화벽에서 포트를 연다)다.
	Enabled bool `json:"enabled"`
	// Installed 는 ssh.service 유닛이 이미지에 있다는 뜻이다.
	Installed bool `json:"installed"`
	// Active 는 유닛이 지금 실행 중이라는 뜻이다.
	Active bool `json:"active"`
	// PasswordAuth 는 sshd 의 비밀번호 로그인 값이다. "yes", "no", "unknown" 이다.
	PasswordAuth string `json:"password_auth"`
	// LoginUsers 는 SSH 비밀번호 로그인을 허용하는 계정이다. Diparnux 로 만든 계정만 들어간다.
	LoginUsers []string `json:"login_users"`
	Port       int      `json:"port"`
	Warning    string   `json:"warning,omitempty"`
}

// sshViewOf 는 설정과 유닛 상태로 응답을 만든다.
func (s *server) sshViewOf(r *http.Request, cfg config.Config) (sshView, error) {
	st, err := s.d.Supervisor.SSHStatus(r.Context())
	if err != nil {
		return sshView{}, err
	}
	pw := st.PasswordAuth
	if pw == "" {
		pw = "unknown"
	}
	users := []string{}
	if s.d.Accounts != nil {
		if names, err := accountNames(s.d.Accounts); err == nil {
			users = names
		}
	}
	v := sshView{Enabled: cfg.SSHEnabled, Installed: st.Installed, Active: st.Active, PasswordAuth: pw, LoginUsers: users, Port: 22}
	if cfg.SSHEnabled {
		switch {
		case len(users) > 0:
			v.Warning = "SSH password login is allowed only for these accounts: " + strings.Join(users, ", ")
		case pw == "no":
			v.Warning = sshWarnNoAccount
		case pw == "yes":
			v.Warning = sshWarnUnknown
		default:
			v.Warning = sshWarnUnknown
		}
	}
	return v, nil
}

// getSSH 는 SSH 켜짐 여부와 유닛 상태를 돌려준다.
func (s *server) getSSH(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	v, err := s.sshViewOf(r, cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ssh_status_failed", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// putSSH 는 SSH 를 켜거나 끈다. 본문은 {"enabled":true|false} 이다.
// 켤 때는 유닛을 먼저 켜고 설정과 방화벽을 맞춘다. 끌 때는 접근을 먼저 막는다.
func (s *server) putSSH(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled *bool `json:"enabled"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Enabled == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "enabled is required")
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}

	var unitErr error
	if *body.Enabled {
		// 호스트 키와 로그인 허용 계정 목록(sshd 드롭인)을 먼저 맞춘다. 계정이 없으면 드롭인을 지워 키 로그인만 남긴다.
		if s.d.Accounts != nil {
			if err := s.d.Accounts.PrepareSSH(r.Context()); err != nil {
				writeErr(w, err)
				return
			}
		}
		if err := s.d.Supervisor.SetSSHUnit(r.Context(), true); err != nil {
			writeErr(w, err)
			return
		}
	} else {
		// 끌 때는 유닛 정지가 실패해도 방화벽을 닫는다.
		unitErr = s.d.Supervisor.SetSSHUnit(r.Context(), false)
	}

	cfg.SSHEnabled = *body.Enabled
	if err := config.Save(s.d.ConfigPath, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", "%v", err)
		return
	}
	if err := s.d.Supervisor.ApplyFirewall(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "firewall_failed", "the setting was saved, but the firewall rules were not applied: %v", err)
		return
	}
	if unitErr != nil {
		writeErr(w, unitErr)
		return
	}
	v, err := s.sshViewOf(r, cfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "ssh_status_failed", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, v)
}

// firstBootStore 는 첫 부팅 상태 저장소다.
func (s *server) firstBootStore() firstboot.Store {
	return firstboot.Store{Path: s.d.FirstBootPath, Now: s.d.Now}
}

// getFirstBoot 는 첫 부팅 마법사의 완료 상태를 돌려준다.
func (s *server) getFirstBoot(w http.ResponseWriter, r *http.Request) {
	st, err := s.firstBootStore().Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "firstboot_unreadable", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// putFirstBoot 는 마법사를 완료로 기록한다. 본문은 {"done":true,"skipped":["gpu",...]} 이다.
func (s *server) putFirstBoot(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Done    *bool    `json:"done"`
		Skipped []string `json:"skipped"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.Done == nil || !*body.Done {
		writeError(w, http.StatusBadRequest, "invalid_request", "done must be true; use DELETE to run the wizard again")
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()

	st, err := s.firstBootStore().Complete(body.Skipped)
	if err != nil {
		var unknown bool
		for _, name := range body.Skipped {
			if !knownWizardStep(name) {
				unknown = true
			}
		}
		if unknown {
			writeError(w, http.StatusBadRequest, "invalid_request", "%v", err)
			return
		}
		writeError(w, http.StatusInternalServerError, "firstboot_write_failed", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// deleteFirstBoot 는 완료 기록을 지워 마법사를 다시 보이게 한다.
func (s *server) deleteFirstBoot(w http.ResponseWriter, r *http.Request) {
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.firstBootStore().Reset(); err != nil {
		writeError(w, http.StatusInternalServerError, "firstboot_write_failed", "%v", err)
		return
	}
	st, err := s.firstBootStore().Load()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "firstboot_unreadable", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// knownWizardStep 은 마법사 단계 이름인지 본다. firstboot 패키지의 검사와 같은 집합이다.
func knownWizardStep(name string) bool {
	switch name {
	case "gpu", "driver", "secure_boot", "account", "model", "api":
		return true
	}
	return false
}

// recommendationView 는 GET /v1/recommendations 의 응답이다.
type recommendationView struct {
	Hardware hardwareView `json:"hardware"`
	recommend.Result
	// Note 는 계산이 어림 값이라는 안내다.
	Note string `json:"note"`
}

// hardwareView 는 추천에 쓴 하드웨어 값이다.
type hardwareView struct {
	GPUState string `json:"gpu_state"`
	VRAMMiB  int64  `json:"vram_mib"`
	RAMMiB   int64  `json:"ram_mib"`
}

// getRecommendations 는 쓸 수 있는 VRAM 이나 램에 맞춰 고정 목록의 모델을 판단한다.
// GPU 상태가 ready 이고 NVML 을 읽을 수 있을 때만 VRAM 기준을 쓴다. 그 밖에는 램 기준이다.
func (s *server) getRecommendations(w http.ResponseWriter, r *http.Request) {
	gpu := s.gpuView(r.Context())
	hw := hardwareView{GPUState: "unknown"}
	if gpu.Status != nil {
		hw.GPUState = gpu.Status.State
	}
	if gpu.Status != nil && gpu.Status.State == "ready" && gpu.NVML.Available {
		usable := map[int]bool{}
		for _, i := range gpu.Status.UsableIndexes {
			usable[i] = true
		}
		for _, d := range gpu.NVML.Devices {
			if usable[d.Index] {
				hw.VRAMMiB += d.MemTotalMiB
			}
		}
	}
	if mem := s.memoryView(); mem != nil {
		hw.RAMMiB = mem.TotalMiB
	}
	res := recommend.Recommend(recommend.Input{VRAMMiB: hw.VRAMMiB, RAMMiB: hw.RAMMiB})
	writeJSON(w, http.StatusOK, recommendationView{
		Hardware: hw,
		Result:   res,
		Note:     "heuristic estimate from the file size plus a KV cache and runtime margin; the real plan is computed after the download",
	})
}

// updateStatus 는 갱신 시작 거부 코드를 HTTP 상태로 바꾼다.
func updateStatus(code string) int {
	switch code {
	case updatectl.CodeInvalidAction:
		return http.StatusBadRequest
	case updatectl.CodeUnavailable:
		return http.StatusServiceUnavailable
	}
	return http.StatusConflict
}

// getUpdate 는 갱신 상태를 돌려준다. 파일을 읽을 뿐이라 갱신 중에도 바로 답한다.
func (s *server) getUpdate(w http.ResponseWriter, r *http.Request) {
	if s.d.Updates == nil {
		writeError(w, http.StatusServiceUnavailable, updatectl.CodeUnavailable, "updates are not configured")
		return
	}
	info, err := s.d.Updates.Info(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

// postUpdate 는 갱신 동작(check, apply, rollback)을 시작한다. 진행은 GET /v1/update 로 본다.
// 본문이 없으면 apply 다.
func (s *server) postUpdate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
		Reboot bool   `json:"reboot"`
	}
	if r.ContentLength != 0 {
		if !decodeBody(w, r, &body) {
			return
		}
	}
	if body.Action == "" {
		body.Action = updatectl.ActionApply
	}
	if s.d.Updates == nil {
		writeError(w, http.StatusServiceUnavailable, updatectl.CodeUnavailable, "updates are not configured")
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	if err := s.d.Updates.Start(r.Context(), body.Action, body.Reboot); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"started": true, "action": body.Action, "reboot": body.Reboot})
}
