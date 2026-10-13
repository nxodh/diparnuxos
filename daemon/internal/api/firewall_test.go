package api

import (
	"encoding/json"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/firewall"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
)

func firewallEnv(t *testing.T, sup *fakeSup) *env {
	t.Helper()
	return newEnvWith(t, sup, func(d *Deps) {
		d.FirewallRules = &firewall.Store{Path: filepath.Join(t.TempDir(), "rules.json")}
		d.FirewallOpens = func() []firewall.Open {
			return []firewall.Open{{Name: "WireGuard", Protocol: "udp", Port: 51820, Source: "any", Reason: "VPN is on"}}
		}
	})
}

func TestFirewallRuleLifecycleAppliesEveryChange(t *testing.T) {
	sup := &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}
	e := firewallEnv(t, sup)

	w := e.do("POST", "/v1/firewall/rules", `{"name":"Open WebUI","protocol":"tcp","port":3000,"source":"lan"}`)
	wantStatus(t, w, http.StatusCreated)
	var rule firewall.Rule
	if err := json.Unmarshal(w.Body.Bytes(), &rule); err != nil || rule.ID != "r1" || !rule.Enabled {
		t.Fatalf("rule = %+v, %v", rule, err)
	}
	if sup.firewalls != 1 {
		t.Fatalf("firewall applied %d times", sup.firewalls)
	}

	w = e.do("PUT", "/v1/firewall/rules/r1", `{"name":"Open WebUI","protocol":"tcp","port":3000,"source":"lan","enabled":false}`)
	wantStatus(t, w, http.StatusOK)
	w = e.do("GET", "/v1/firewall", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), `"enabled":false`) || !strings.Contains(w.Body.String(), "WireGuard") {
		t.Fatalf("firewall = %s", w.Body.String())
	}
	w = e.do("DELETE", "/v1/firewall/rules/r1", "")
	wantStatus(t, w, http.StatusOK)
	w = e.do("DELETE", "/v1/firewall/rules/r1", "")
	wantStatus(t, w, http.StatusNotFound)
	if sup.firewalls != 3 {
		t.Fatalf("firewall applied %d times", sup.firewalls)
	}
}

func TestFirewallRuleIsRevertedWhenNftRejectsIt(t *testing.T) {
	sup := &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}, fwErr: &supervisor.Error{Code: supervisor.CodeFirewallFailed, Message: "syntax"}}
	e := firewallEnv(t, sup)
	w := e.do("POST", "/v1/firewall/rules", `{"name":"x","protocol":"tcp","port":3000}`)
	wantStatus(t, w, http.StatusBadRequest)
	if errCode(t, w) != "firewall_failed" {
		t.Fatalf("code = %s", errCode(t, w))
	}
	w = e.do("GET", "/v1/firewall", "")
	if !strings.Contains(w.Body.String(), `"rules":[]`) {
		t.Fatalf("the rejected rule was kept: %s", w.Body.String())
	}
}

func TestFirewallRuleValidation(t *testing.T) {
	e := firewallEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	for _, body := range []string{
		`{"name":"x","protocol":"icmp","port":80}`,
		`{"name":"x","protocol":"tcp","port":0}`,
		`{"name":"x","protocol":"tcp","port":80,"source":"1.2.3.4; accept"}`,
		`{"name":"","protocol":"tcp","port":80}`,
	} {
		w := e.do("POST", "/v1/firewall/rules", body)
		wantStatus(t, w, http.StatusBadRequest)
	}
}
