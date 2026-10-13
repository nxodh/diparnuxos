package api

import (
	"net/http"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/exposure"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// getExposure 는 이 컴퓨터가 네트워크에 내놓는 것을 점검한다. 열린 포트는 ss 로 읽고 나머지는 설정에서 모은다.
func (s *server) getExposure(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	in := exposure.Input{LANExposed: cfg.LANExposed, APIPort: cfg.Port, GatewayOn: cfg.Gateway, SSHEnabled: cfg.SSHEnabled}
	if s.d.Keys != nil {
		if n, err := s.d.Keys.Count(); err == nil {
			in.KeyCount = n
		}
	}
	if s.d.SSHConf != nil {
		set := s.d.SSHConf.Settings()
		in.SSHPasswordOn = set.PasswordLogin == nil || *set.PasswordLogin
	}
	if s.d.WebAdmin != nil {
		st := s.d.WebAdmin.Status()
		in.WebAdminOn, in.WebAdminSource = st.Enabled, st.Source
	}
	if s.d.RDP != nil {
		st := s.d.RDP.Status(r.Context())
		in.RDPOn, in.RDPSource = st.Enabled, st.Source
	}
	if s.d.VPN != nil {
		if v, err := s.d.VPN.Status(); err == nil {
			in.VPNOn = v.Enabled
		}
	}
	for _, o := range s.firewallBuiltins(cfg) {
		in.Firewall = append(in.Firewall, exposure.Open{Name: o.Name, Protocol: o.Protocol, Port: o.Port, Source: o.Source})
	}
	if s.d.FirewallRules != nil {
		if rules, err := s.d.FirewallRules.List(); err == nil {
			for _, ru := range rules {
				if ru.Enabled {
					in.Firewall = append(in.Firewall, exposure.Open{Name: ru.Name, Protocol: ru.Protocol, Port: ru.Port, Source: ru.Source})
				}
			}
		}
	}
	if s.d.Runner != nil {
		res, err := s.d.Runner.Run(r.Context(), platform.Command{Name: "ss", Args: []string{"-H", "-ltunp"}, Timeout: 5 * time.Second, MaxOutput: 128 << 10})
		if err == nil && res.ExitCode == 0 {
			in.Listeners = exposure.ParseSS(string(res.Stdout))
		}
	}
	findings := exposure.Evaluate(in)
	worst := exposure.OK
	for _, f := range findings {
		switch {
		case f.Level == exposure.Error:
			worst = exposure.Error
		case f.Level == exposure.Warn && worst != exposure.Error:
			worst = exposure.Warn
		}
	}
	writeJSON(w, http.StatusOK, map[string]any{"level": worst, "findings": findings, "listening": in.Listeners})
}
