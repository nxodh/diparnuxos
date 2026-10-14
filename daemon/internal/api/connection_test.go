package api

import (
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/network"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
)

func baseConnectionInput() connectionInput {
	cfg := config.Default()
	cfg.LANExposed = true
	return connectionInput{
		Cfg:    cfg,
		Server: supervisor.Status{State: supervisor.StateRunning, ModelID: "gemma.gguf"},
		Interfaces: []network.Interface{
			{Name: "lo", State: "up", Addresses: []string{"127.0.0.1/8"}},
			{Name: "enp3s0", State: "up", Addresses: []string{"192.168.0.23/24", "fe80::1/64", "2001:db8::23/64"}},
			{Name: "wlan0", State: "down", Addresses: []string{"10.0.0.5/24"}},
		},
		KeyCount: 1,
		ModelIDs: []string{"gemma.gguf"},
		Hostname: "diparnux",
	}
}

func TestConnectionListsReachableAddresses(t *testing.T) {
	c := buildConnection(baseConnectionInput())
	if len(c.Addresses) != 2 {
		t.Fatalf("addresses = %+v", c.Addresses)
	}
	if c.Addresses[0].URL != "http://192.168.0.23:8080/v1" || c.Addresses[0].Family != "ipv4" {
		t.Fatalf("first address = %+v", c.Addresses[0])
	}
	if c.Addresses[1].URL != "http://[2001:db8::23]:8080/v1" {
		t.Fatalf("ipv6 url = %q", c.Addresses[1].URL)
	}
	if c.MDNSURL != "http://diparnux.local:8080/v1" {
		t.Fatalf("mdns = %q", c.MDNSURL)
	}
	if len(c.Warnings) != 0 || !c.KeyRequired {
		t.Fatalf("warnings = %v key_required = %v", c.Warnings, c.KeyRequired)
	}
	if !strings.Contains(c.Examples["curl"], "http://192.168.0.23:8080/v1/chat/completions") || !strings.Contains(c.Examples["settings"], "gemma.gguf") {
		t.Fatalf("examples = %v", c.Examples)
	}
	for _, ex := range c.Examples {
		if strings.Contains(ex, "dpx_") {
			t.Fatal("an example must never contain a real key")
		}
	}
}

func TestConnectionWarnings(t *testing.T) {
	in := baseConnectionInput()
	in.Cfg.LANExposed = false
	in.Server.State = supervisor.StateStopped
	c := buildConnection(in)
	joined := strings.Join(c.Warnings, "\n")
	if !strings.Contains(joined, "not running") || !strings.Contains(joined, "LAN exposure is off") {
		t.Fatalf("warnings = %v", c.Warnings)
	}
	if len(c.Addresses) != 0 || c.LocalURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("a LAN-off server must only offer the local URL: %+v", c)
	}

	in = baseConnectionInput()
	in.KeyCount = 0
	if w := strings.Join(buildConnection(in).Warnings, "\n"); !strings.Contains(w, "No API key") {
		t.Fatalf("warnings = %q", w)
	}
}

func TestConnectionVedecapsExampleAndLocalKeyState(t *testing.T) {
	in := baseConnectionInput()
	in.Server.Plan = &autoconf.Output{SlotCtx: 32768, Parallel: 2}
	c := buildConnection(in)
	ex := c.Examples["vedecaps"]
	for _, want := range []string{`"api": "http://192.168.0.23:8080/v1"`, `"model": "diparnux/gemma.gguf"`, `"context": 32768`, `{env:DIPARNUX_API_KEY}`} {
		if !strings.Contains(ex, want) {
			t.Fatalf("example lacks %s:\n%s", want, ex)
		}
	}
	if c.SlotCtx != 32768 || c.Parallel != 2 {
		t.Fatalf("slot ctx = %d parallel = %d", c.SlotCtx, c.Parallel)
	}
	// 기본값(local_no_key)에서는 이 컴퓨터에서 키가 필요 없다. 끄면 키가 있을 때 필요해진다.
	if !c.LocalNoKey || c.LocalKeyRequired {
		t.Fatalf("local no key = %v required = %v", c.LocalNoKey, c.LocalKeyRequired)
	}
	in.Cfg.LocalNoKey = false
	if c := buildConnection(in); c.LocalNoKey || !c.LocalKeyRequired {
		t.Fatalf("with local_no_key off: %v %v", c.LocalNoKey, c.LocalKeyRequired)
	}
	// LAN 공개가 꺼져 있고 키 면제가 켜져 있으면 예시에 키가 없다.
	in = baseConnectionInput()
	in.Cfg.LANExposed = false
	c = buildConnection(in)
	if strings.Contains(c.Examples["vedecaps"], "apiKey") || !strings.Contains(c.Examples["vedecaps"], "127.0.0.1:8080/v1") {
		t.Fatalf("local example:\n%s", c.Examples["vedecaps"])
	}
}

// TLS 가 떠 있으면 LAN 주소는 https 와 TLS 포트이고, 이 컴퓨터의 주소는 평문 그대로다.
func TestConnectionWithTLSUsesHTTPSAddresses(t *testing.T) {
	in := baseConnectionInput()
	in.Cfg.TLS = true
	in.CAFingerprint = "AA:BB:CC"
	c := buildConnection(in)
	if !c.TLSActive || c.TLSPort != 8444 || c.CAFingerprint != "AA:BB:CC" {
		t.Fatalf("tls fields: %+v", c)
	}
	if c.LocalURL != "http://127.0.0.1:8080/v1" {
		t.Fatalf("the local URL must stay plain: %q", c.LocalURL)
	}
	if c.Addresses[0].URL != "https://192.168.0.23:8444/v1" || c.Addresses[1].URL != "https://[2001:db8::23]:8444/v1" {
		t.Fatalf("addresses = %+v", c.Addresses)
	}
	if c.MDNSURL != "https://diparnux.local:8444/v1" {
		t.Fatalf("mdns = %q", c.MDNSURL)
	}
	if !strings.Contains(c.Examples["curl"], "--cacert diparnux-ca.pem https://192.168.0.23:8444/v1/chat/completions") {
		t.Fatalf("curl = %s", c.Examples["curl"])
	}
	if !strings.Contains(c.Examples["openai_python"], "verify=\"diparnux-ca.pem\"") {
		t.Fatalf("python = %s", c.Examples["openai_python"])
	}
	if !strings.Contains(c.Examples["vedecaps"], `"ca": "~/.config/vedecaps/diparnux-ca.pem"`) {
		t.Fatalf("vedecaps = %s", c.Examples["vedecaps"])
	}
	found := false
	for _, w := range c.Warnings {
		if strings.Contains(w, "own CA") {
			found = true
		}
		if strings.Contains(w, "Plain HTTP is also open") {
			t.Fatalf("lan_plain warning without lan_plain: %v", c.Warnings)
		}
	}
	if !found {
		t.Fatalf("no CA warning: %v", c.Warnings)
	}
	in.Cfg.LANPlain = true
	c = buildConnection(in)
	plain := false
	for _, w := range c.Warnings {
		if strings.Contains(w, "Plain HTTP is also open") {
			plain = true
		}
	}
	if !plain {
		t.Fatalf("lan_plain was not warned: %v", c.Warnings)
	}
	// TLS 가 켜져 있어도 LAN 공개가 꺼져 있으면 https 주소는 없다.
	in = baseConnectionInput()
	in.Cfg.TLS, in.Cfg.LANExposed = true, false
	c = buildConnection(in)
	if c.TLSActive || len(c.Addresses) != 0 {
		t.Fatalf("TLS without LAN exposure: %+v", c)
	}
}
