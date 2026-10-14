package exposure

import (
	"strings"
	"testing"
)

func find(fs []Finding, sub string) *Finding {
	for i := range fs {
		if strings.Contains(fs[i].Title, sub) {
			return &fs[i]
		}
	}
	return nil
}

func TestEvaluateFlagsRiskyConfigurations(t *testing.T) {
	got := Evaluate(Input{LANExposed: true, APIPort: 8080, KeyCount: 0, SSHEnabled: true, SSHPasswordOn: true,
		WebAdminOn: true, WebAdminSource: "any", RDPOn: true, RDPSource: "lan"})
	if got[0].Level != Error || !strings.Contains(got[0].Title, "no API key") {
		t.Fatalf("the most severe finding must come first: %+v", got)
	}
	if f := find(got, "SSH accepts password"); f == nil || f.Level != Warn {
		t.Fatalf("ssh = %+v", f)
	}
	if f := find(got, "web admin page accepts connections from any"); f == nil || f.Level != Warn {
		t.Fatalf("web = %+v", f)
	}
	if f := find(got, "Remote desktop is on"); f == nil || f.Level != Info {
		t.Fatalf("rdp = %+v", f)
	}
	// 수준 순서: error, warning, info, ok.
	rank := map[string]int{Error: 0, Warn: 1, Info: 2, OK: 3}
	for i := 1; i < len(got); i++ {
		if rank[got[i].Level] < rank[got[i-1].Level] {
			t.Fatalf("findings are not sorted: %+v", got)
		}
	}
}

func TestEvaluateQuietConfiguration(t *testing.T) {
	got := Evaluate(Input{APIPort: 8080})
	if len(got) != 1 || got[0].Level != OK || !strings.Contains(got[0].Title, "only from this computer") {
		t.Fatalf("got = %+v", got)
	}
	open := Evaluate(Input{LANExposed: true, APIPort: 8080, KeyCount: 2, GatewayOn: true})
	if open[0].Level != OK || !strings.Contains(open[0].Detail, "2 key") {
		t.Fatalf("open = %+v", open)
	}
	nogw := Evaluate(Input{LANExposed: true, APIPort: 8080, KeyCount: 1})
	if nogw[0].Level != Warn || !strings.Contains(nogw[0].Title, "gateway is off") {
		t.Fatalf("nogw = %+v", nogw)
	}
}

func TestUserRuleOpeningAnyAddressIsWarned(t *testing.T) {
	got := Evaluate(Input{APIPort: 8080, Firewall: []Open{{Name: "Test", Protocol: "tcp", Port: 9000, Source: "any"}, {Name: "SSH", Protocol: "tcp", Port: 22, Source: "any"}}})
	if f := find(got, "tcp/9000 to any address"); f == nil || f.Level != Warn {
		t.Fatalf("got = %+v", got)
	}
	if f := find(got, "tcp/22 to any"); f != nil {
		t.Fatalf("a feature port was reported as a user rule: %+v", f)
	}
}

func TestParseSS(t *testing.T) {
	text := `tcp LISTEN 0 4096 127.0.0.53%lo:53 0.0.0.0:* users:(("systemd-resolve",pid=300,fd=14))
tcp LISTEN 0 4096 127.0.0.1:18080 0.0.0.0:* users:(("llama-server",pid=500,fd=3))
tcp LISTEN 0 4096 0.0.0.0:8080 0.0.0.0:* users:(("diparnuxd",pid=400,fd=9))
tcp LISTEN 0 128 [::]:22 [::]:* users:(("sshd",pid=600,fd=3))
udp UNCONN 0 0 0.0.0.0:5353 0.0.0.0:* users:(("systemd-resolve",pid=300,fd=12))
udp UNCONN 0 0 192.168.0.23%enp3s0:68 0.0.0.0:*
`
	got := ParseSS(text)
	want := map[string]string{"tcp/8080": "diparnuxd", "tcp/22": "sshd", "udp/5353": "systemd-resolve", "udp/68": ""}
	if len(got) != len(want) {
		t.Fatalf("got = %+v", got)
	}
	for _, l := range got {
		key := l.Protocol + "/" + itoa(l.Port)
		if p, ok := want[key]; !ok || p != l.Process {
			t.Fatalf("unexpected %+v", l)
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// TLS: 켜져 있고 평문이 닫혀 있으면 정상, 평문이 열려 있으면 경고다.
func TestEvaluateTLS(t *testing.T) {
	closed := Evaluate(Input{LANExposed: true, APIPort: 8080, KeyCount: 1, GatewayOn: true, TLSOn: true, TLSPort: 8444})
	if closed[0].Level != OK || !strings.Contains(closed[0].Title, "over TLS") || !strings.Contains(closed[0].Detail, "8444") {
		t.Fatalf("closed = %+v", closed)
	}
	both := Evaluate(Input{LANExposed: true, APIPort: 8080, KeyCount: 1, GatewayOn: true, TLSOn: true, TLSPort: 8444, PlainOpen: true})
	if both[0].Level != Warn || !strings.Contains(both[0].Title, "Plain HTTP is still open") {
		t.Fatalf("both = %+v", both)
	}
	plain := Evaluate(Input{LANExposed: true, APIPort: 8080, KeyCount: 1, GatewayOn: true})
	if plain[0].Level != OK || !strings.Contains(plain[0].Detail, "not encrypted") {
		t.Fatalf("plain = %+v", plain)
	}
	// 프로그램이 여는 TLS 포트는 "any 출발지" 경고 대상이 아닌 알려진 이름이다.
	known := Evaluate(Input{LANExposed: true, APIPort: 8080, KeyCount: 1, GatewayOn: true, TLSOn: true, TLSPort: 8444,
		Firewall: []Open{{Name: "Model API (HTTPS)", Protocol: "tcp", Port: 8444, Source: "any"}}})
	if find(known, "opens tcp/8444 to any") != nil {
		t.Fatalf("the TLS port was reported as an unknown rule: %+v", known)
	}
}
