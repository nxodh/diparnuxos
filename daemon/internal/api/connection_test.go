package api

import (
	"strings"
	"testing"

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
