package api

import (
	"net/http"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/certstore"
	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/gateway"
)

// TLSManager 는 게이트웨이 TLS 의 인증서와 리스너 상태다. 데몬이 이름과 주소를 모아 만든다.
type TLSManager interface {
	// Info 는 저장된 인증서의 공개 정보다(만들지 않는다).
	Info() (certstore.Info, error)
	// Ensure 는 CA 와 서버 인증서를 보장한다. 이름은 이 컴퓨터의 지금 이름과 주소다.
	Ensure(extra []string) (certstore.Info, error)
	// RenewServer 는 서버 인증서만, RenewCA 는 CA 까지 다시 만든다.
	RenewServer(extra []string) (certstore.Info, error)
	RenewCA(extra []string) (certstore.Info, error)
	// CAPEM 은 클라이언트에 배포할 CA 인증서다.
	CAPEM() ([]byte, error)
	// Listener 는 TLS 리스너의 상태다.
	Listener() gateway.ManagerState
}

// tlsReply 는 GET/PUT /v1/tls 의 응답이다. 개인 키는 없다.
type tlsReply struct {
	// Enabled 는 설정의 tls 값이고, Active 는 리스너가 떠야 하는 상태(LAN 공개와 게이트웨이도 켜짐)다.
	Enabled  bool     `json:"enabled"`
	Active   bool     `json:"active"`
	Port     int      `json:"port"`
	LANPlain bool     `json:"lan_plain"`
	Names    []string `json:"names"`
	// PlainPort 는 평문 포트이고 PlainLANOpen 은 그 포트가 LAN 에 열려 있는지다.
	PlainPort    int  `json:"plain_port"`
	PlainLANOpen bool `json:"plain_lan_open"`

	Listening     bool   `json:"listening"`
	ListenAddr    string `json:"listen_addr,omitempty"`
	ListenerError string `json:"listener_error,omitempty"`

	CAFingerprint     string   `json:"ca_fingerprint,omitempty"`
	CANotAfter        string   `json:"ca_not_after,omitempty"`
	CAExpiresSoon     bool     `json:"ca_expires_soon,omitempty"`
	ServerFingerprint string   `json:"server_fingerprint,omitempty"`
	ServerNotAfter    string   `json:"server_not_after,omitempty"`
	DNS               []string `json:"dns,omitempty"`
	IPs               []string `json:"ips,omitempty"`
	PermittedDNS      []string `json:"permitted_dns,omitempty"`
	Skipped           []string `json:"skipped,omitempty"`
	Notes             []string `json:"notes,omitempty"`
}

func (s *server) tlsManager(w http.ResponseWriter) (TLSManager, bool) {
	if s.d.TLS == nil {
		writeError(w, http.StatusServiceUnavailable, "tls_unavailable", "TLS is not configured")
		return nil, false
	}
	return s.d.TLS, true
}

func tlsView(cfg config.Config, info certstore.Info, l gateway.ManagerState) tlsReply {
	r := tlsReply{
		Enabled: cfg.TLS, Active: cfg.TLSActive(), Port: cfg.TLSListenPort(), LANPlain: cfg.LANPlain,
		Names: append([]string{}, cfg.TLSNames...), PlainPort: cfg.Port, PlainLANOpen: cfg.PlainLANOpen(),
		Listening: l.Listening, ListenAddr: l.Addr, ListenerError: l.Error,
		CAFingerprint: info.CAFingerprint, CAExpiresSoon: info.CAExpiresSoon, ServerFingerprint: info.ServerFingerprint,
		DNS: info.DNS, IPs: info.IPs, PermittedDNS: info.PermittedDNS, Skipped: info.Skipped,
	}
	if !info.CANotAfter.IsZero() {
		r.CANotAfter = info.CANotAfter.UTC().Format("2006-01-02T15:04:05Z")
	}
	if !info.ServerNotAfter.IsZero() {
		r.ServerNotAfter = info.ServerNotAfter.UTC().Format("2006-01-02T15:04:05Z")
	}
	switch {
	case cfg.TLS && !cfg.LANExposed:
		r.Notes = append(r.Notes, "TLS is on but LAN exposure is off, so nothing is encrypted for other computers yet: diparnux config set lan on")
	case cfg.TLS && !cfg.Gateway:
		r.Notes = append(r.Notes, "TLS needs the gateway: diparnux config set gateway on")
	case cfg.TLSActive() && cfg.LANPlain:
		r.Notes = append(r.Notes, "plain HTTP is also open on the local network (lan_plain); keys and prompts sent there are not encrypted")
	}
	if len(info.Skipped) > 0 {
		r.Notes = append(r.Notes, "these names are outside the CA's name constraint and were left out; renew the CA to include them (clients must then receive the new CA): "+strings.Join(info.Skipped, ", "))
	}
	if info.CAExpiresSoon {
		r.Notes = append(r.Notes, "the CA expires within a year; renew it and give clients the new CA")
	}
	return r
}

// getTLS 는 TLS 설정, 인증서 정보, 리스너 상태를 돌려준다.
func (s *server) getTLS(w http.ResponseWriter, r *http.Request) {
	m, ok := s.tlsManager(w)
	if !ok {
		return
	}
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	info, err := m.Info()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tls_unreadable", "%v", err)
		return
	}
	writeJSON(w, http.StatusOK, tlsView(cfg, info, m.Listener()))
}

type tlsPatch struct {
	Enabled  *bool     `json:"enabled"`
	Port     *int      `json:"port"`
	LANPlain *bool     `json:"lan_plain"`
	Names    *[]string `json:"names"`
}

// putTLS 는 TLS 를 켜고 끄고 포트, 평문 허용, 추가 이름을 바꾼다.
// 켜려면 LAN 공개와 게이트웨이와 API 키가 있어야 한다. 켜면 평문 포트는 LAN 에서 닫힌다(lan_plain 이 아니면).
func (s *server) putTLS(w http.ResponseWriter, r *http.Request) {
	m, ok := s.tlsManager(w)
	if !ok {
		return
	}
	var patch tlsPatch
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
	if patch.Enabled != nil {
		next.TLS = *patch.Enabled
	}
	if patch.Port != nil {
		next.TLSPort = *patch.Port
		if next.TLSPort == config.DefaultTLSPort {
			next.TLSPort = 0
		}
	}
	if patch.LANPlain != nil {
		next.LANPlain = *patch.LANPlain
	}
	if patch.Names != nil {
		next.TLSNames = append([]string{}, (*patch.Names)...)
	}
	if err := next.Validate(); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_config", "%v", err)
		return
	}
	if next.TLS && !old.TLS {
		switch {
		case !next.Gateway:
			writeError(w, http.StatusConflict, "gateway_required", "TLS needs the gateway: diparnux config set gateway on")
			return
		case !next.LANExposed:
			writeError(w, http.StatusConflict, "lan_required", "TLS encrypts connections from other computers, so LAN exposure must be on first: diparnux config set lan on")
			return
		}
		n, err := s.keyCount()
		if err != nil {
			writeErr(w, err)
			return
		}
		if n == 0 {
			writeError(w, http.StatusConflict, "api_key_required", "create an API key first: diparnux apikey create <name>")
			return
		}
	}
	if next.TLS && s.d.WebAdmin != nil {
		if st := s.d.WebAdmin.Status(); st.Enabled && st.Port == next.TLSListenPort() {
			writeError(w, http.StatusConflict, "port_in_use", "port %d is used by the web admin page; choose another TLS port", next.TLSListenPort())
			return
		}
	}
	// 인증서를 먼저 만든다. 실패하면 설정을 바꾸지 않는다.
	var info certstore.Info
	if next.TLS {
		info, err = m.Ensure(next.TLSNames)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "certificate_failed", "the certificate could not be made: %v", err)
			return
		}
	} else if info, err = m.Info(); err != nil {
		writeError(w, http.StatusInternalServerError, "tls_unreadable", "%v", err)
		return
	}
	if err := config.Save(s.d.ConfigPath, next); err != nil {
		writeError(w, http.StatusInternalServerError, "config_write_failed", "%v", err)
		return
	}
	if err := s.d.Supervisor.ApplyFirewall(r.Context()); err != nil {
		writeError(w, http.StatusInternalServerError, "firewall_failed", "the setting was saved, but the firewall rules were not applied: %v", err)
		return
	}
	writeJSON(w, http.StatusOK, tlsView(next, info, m.Listener()))
}

type tlsRenewBody struct {
	// CA 가 true 이면 CA 까지 다시 만든다. 클라이언트는 새 CA 를 다시 받아야 한다.
	CA bool `json:"ca"`
}

// postTLSRenew 는 서버 인증서(또는 CA 까지)를 지금 다시 만든다.
func (s *server) postTLSRenew(w http.ResponseWriter, r *http.Request) {
	m, ok := s.tlsManager(w)
	if !ok {
		return
	}
	var body tlsRenewBody
	if r.ContentLength != 0 && !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	cur, err := m.Info()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "tls_unreadable", "%v", err)
		return
	}
	if !cur.Present && !body.CA {
		writeError(w, http.StatusConflict, "tls_not_set_up", "there is no certificate yet: diparnux tls on")
		return
	}
	var info certstore.Info
	if body.CA {
		info, err = m.RenewCA(cfg.TLSNames)
	} else {
		info, err = m.RenewServer(cfg.TLSNames)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, "certificate_failed", "the certificate could not be made: %v", err)
		return
	}
	view := tlsView(cfg, info, m.Listener())
	if body.CA {
		view.Notes = append(view.Notes, "the CA changed: give every client the new CA (diparnux tls ca) and compare the fingerprint")
	}
	writeJSON(w, http.StatusOK, view)
}

// getTLSCA 는 클라이언트에 배포할 CA 인증서(공개 값)를 PEM 으로 돌려준다.
func (s *server) getTLSCA(w http.ResponseWriter, r *http.Request) {
	m, ok := s.tlsManager(w)
	if !ok {
		return
	}
	data, err := m.CAPEM()
	if err != nil {
		writeError(w, http.StatusNotFound, "tls_not_set_up", "there is no CA yet: diparnux tls on")
		return
	}
	w.Header().Set("Content-Type", "application/x-pem-file")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}
