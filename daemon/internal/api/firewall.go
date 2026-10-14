package api

import (
	"errors"
	"net/http"

	"github.com/nxodh/operetaos/daemon/internal/config"
	"github.com/nxodh/operetaos/daemon/internal/firewall"
)

// firewallBuiltins 는 설정과 켜진 기능 때문에 열리는 포트다. 규칙 화면에 읽기 전용으로 보인다.
func (s *server) firewallBuiltins(cfg config.Config) []firewall.Open {
	o := firewall.Options{APIPort: cfg.Port, APIOpen: cfg.PlainLANOpen(), SSHOpen: cfg.SSHEnabled, SSHPort: cfg.SSHPort, MDNSOpen: cfg.LANExposed}
	if s.d.FirewallOpens != nil {
		o.Open = s.d.FirewallOpens()
	}
	if cfg.TLSActive() {
		o.Open = append(o.Open, firewall.Open{Name: "Model API (HTTPS)", Protocol: "tcp", Port: cfg.TLSListenPort(), Source: firewall.SourceLAN, Reason: "TLS is on"})
	}
	return firewall.Builtins(o)
}

func (s *server) firewallStore(w http.ResponseWriter) (*firewall.Store, bool) {
	if s.d.FirewallRules == nil {
		writeError(w, http.StatusServiceUnavailable, "firewall_unavailable", "firewall rule management is not configured")
		return nil, false
	}
	return s.d.FirewallRules, true
}

// getFirewall 은 열려 있는 포트(기능이 여는 것)와 사용자 규칙이다.
func (s *server) getFirewall(w http.ResponseWriter, r *http.Request) {
	st, ok := s.firewallStore(w)
	if !ok {
		return
	}
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	rules, err := st.List()
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"policy":   "drop",
		"builtin":  s.firewallBuiltins(cfg),
		"rules":    rules,
		"max_rule": firewall.MaxRules,
	})
}

type ruleBody struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	PortEnd  int    `json:"port_end"`
	Source   string `json:"source"`
	Enabled  *bool  `json:"enabled"`
}

func (b ruleBody) rule(id string) firewall.Rule {
	r := firewall.Rule{ID: id, Name: b.Name, Protocol: b.Protocol, Port: b.Port, PortEnd: b.PortEnd, Source: b.Source, Enabled: true}
	if b.Source == "" {
		r.Source = firewall.SourceAny
	}
	if b.Enabled != nil {
		r.Enabled = *b.Enabled
	}
	return r
}

// applyOrRollback 은 규칙 변경을 nft 로 적용한다. 실패하면 undo 로 저장소를 되돌린다.
func (s *server) applyOrRollback(w http.ResponseWriter, r *http.Request, undo func() error) bool {
	if err := s.d.Supervisor.ApplyFirewall(r.Context()); err != nil {
		if undo != nil {
			_ = undo()
		}
		writeError(w, http.StatusBadRequest, "firewall_failed", "the rule was not applied and was reverted: %v", err)
		return false
	}
	return true
}

func writeRuleErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, firewall.ErrNotFound):
		writeError(w, http.StatusNotFound, "not_found", "the rule was not found")
	case errors.Is(err, firewall.ErrTooMany):
		writeError(w, http.StatusConflict, "too_many_rules", "%v", err)
	default:
		writeError(w, http.StatusBadRequest, "invalid_input", "%v", err)
	}
}

// postFirewallRule 은 규칙을 만든다.
func (s *server) postFirewallRule(w http.ResponseWriter, r *http.Request) {
	st, ok := s.firewallStore(w)
	if !ok {
		return
	}
	var body ruleBody
	if !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	rule, err := st.Add(body.rule(""))
	if err != nil {
		writeRuleErr(w, err)
		return
	}
	if !s.applyOrRollback(w, r, func() error { return st.Remove(rule.ID) }) {
		return
	}
	writeJSON(w, http.StatusCreated, rule)
}

// putFirewallRule 은 규칙을 바꾼다. 켜고 끄는 것도 이 호출이다.
func (s *server) putFirewallRule(w http.ResponseWriter, r *http.Request) {
	st, ok := s.firewallStore(w)
	if !ok {
		return
	}
	var body ruleBody
	if !decodeBody(w, r, &body) {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	id := r.PathValue("id")
	rules, err := st.List()
	if err != nil {
		writeRuleErr(w, err)
		return
	}
	var old *firewall.Rule
	for i := range rules {
		if rules[i].ID == id {
			old = &rules[i]
		}
	}
	if old == nil {
		writeRuleErr(w, firewall.ErrNotFound)
		return
	}
	prev := *old
	rule, err := st.Update(body.rule(id))
	if err != nil {
		writeRuleErr(w, err)
		return
	}
	if !s.applyOrRollback(w, r, func() error { _, e := st.Update(prev); return e }) {
		return
	}
	writeJSON(w, http.StatusOK, rule)
}

// deleteFirewallRule 은 규칙을 지운다.
func (s *server) deleteFirewallRule(w http.ResponseWriter, r *http.Request) {
	st, ok := s.firewallStore(w)
	if !ok {
		return
	}
	s.mutationMu.Lock()
	defer s.mutationMu.Unlock()
	id := r.PathValue("id")
	rules, err := st.List()
	if err != nil {
		writeRuleErr(w, err)
		return
	}
	var prev *firewall.Rule
	for i := range rules {
		if rules[i].ID == id {
			p := rules[i]
			prev = &p
		}
	}
	if prev == nil {
		writeRuleErr(w, firewall.ErrNotFound)
		return
	}
	if err := st.Remove(id); err != nil {
		writeRuleErr(w, err)
		return
	}
	if !s.applyOrRollback(w, r, func() error { _, e := st.Add(*prev); return e }) {
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"id": id, "deleted": true})
}
