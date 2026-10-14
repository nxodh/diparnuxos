// Package exposure 는 이 컴퓨터가 네트워크에 무엇을 내놓고 있는지 한 번에 점검해 위험한 구성을 알린다.
//
// 순수 함수다. 입력(설정, 켜진 기능, 열린 포트)을 모아 넘기면 점검 결과 목록이 나온다.
package exposure

import (
	"bufio"
	"fmt"
	"strconv"
	"strings"
)

// 수준이다.
const (
	OK    = "ok"
	Info  = "info"
	Warn  = "warning"
	Error = "error"
)

// Finding 은 점검 항목 하나다.
type Finding struct {
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Open 은 방화벽이 열어 둔 포트다.
type Open struct {
	Name     string
	Protocol string
	Port     int
	Source   string
}

// Listener 는 모든 주소(또는 LAN 주소)에서 연결을 받는 소켓이다.
type Listener struct {
	Protocol string
	Port     int
	Process  string
}

// Input 은 점검에 필요한 값이다.
type Input struct {
	LANExposed bool
	APIPort    int
	KeyCount   int
	GatewayOn  bool
	// TLSOn 은 TLS 포트가 LAN 에 떠 있다는 뜻이고, PlainOpen 은 그와 별개로 평문 포트가 LAN 에 열려 있다는 뜻이다.
	TLSOn          bool
	TLSPort        int
	PlainOpen      bool
	SSHEnabled     bool
	SSHPasswordOn  bool // 비밀번호 로그인이 켜져 있음
	WebAdminOn     bool
	WebAdminSource string
	RDPOn          bool
	RDPSource      string
	VPNOn          bool
	Firewall       []Open
	Listeners      []Listener
}

// Evaluate 는 점검 결과를 심각한 것부터 돌려준다.
func Evaluate(in Input) []Finding {
	var out []Finding
	add := func(level, title, detail string) { out = append(out, Finding{level, title, detail}) }

	switch {
	case in.LANExposed && in.KeyCount == 0:
		add(Error, "Other computers may connect but there is no API key",
			"Connections are refused without a key. Create one: diparnux apikey create <name>.")
	case in.LANExposed && !in.GatewayOn:
		add(Warn, "The gateway is off",
			"API key changes need a server restart and per-key usage is not counted. Turn it on: diparnux config set gateway on.")
	case in.LANExposed && in.TLSOn && in.PlainOpen:
		add(Warn, "Plain HTTP is still open on the local network",
			fmt.Sprintf("Port %d accepts unencrypted connections, so API keys and prompts can be read on the network. TLS is also on (port %d). Close the plain port: diparnux tls plain off.", in.APIPort, in.TLSPort))
	case in.LANExposed && in.TLSOn:
		add(OK, "The model server is open to the local network over TLS", fmt.Sprintf("Port %d is encrypted and needs an API key (%d key(s)). This computer still uses plain HTTP on 127.0.0.1:%d.", in.TLSPort, in.KeyCount, in.APIPort))
	case in.LANExposed:
		add(OK, "The model server is open to the local network",
			fmt.Sprintf("Port %d needs an API key (%d key(s)). Connections from other computers are not encrypted (plain HTTP); encrypt them with: diparnux tls on, or use the VPN: diparnux vpn on.", in.APIPort, in.KeyCount))
	default:
		add(OK, "The model server is reachable only from this computer", "Turn on LAN exposure to let other computers connect.")
	}

	if in.SSHEnabled {
		if in.SSHPasswordOn {
			add(Warn, "SSH accepts password logins", "Keys are safer: add a public key, then run 'diparnux ssh password off'.")
		} else {
			add(OK, "SSH accepts keys only", "")
		}
	}
	if in.WebAdminOn {
		if in.WebAdminSource == "any" {
			add(Warn, "The web admin page accepts connections from any address", "Limit it to your network: diparnux webadmin on --from lan.")
		} else {
			add(Info, "The web admin page is on", "Allowed from "+in.WebAdminSource+".")
		}
	}
	if in.RDPOn {
		if in.RDPSource == "any" {
			add(Warn, "Remote desktop accepts connections from any address", "Limit it to your network: diparnux remote-desktop on --from lan.")
		} else {
			add(Info, "Remote desktop is on", "Allowed from "+in.RDPSource+".")
		}
	}
	if in.VPNOn {
		add(Info, "The VPN is on", "Devices that join through it are trusted like the local network.")
	}

	// 방화벽이 여는 포트 중 "any" 출발지는 눈에 띄게 알린다.
	opened := map[string]bool{}
	for _, o := range in.Firewall {
		opened[fmt.Sprintf("%s/%d", o.Protocol, o.Port)] = true
		if o.Source == "any" && o.Name != "" && !known(o.Name) {
			add(Warn, fmt.Sprintf("A rule opens %s/%d to any address", o.Protocol, o.Port), o.Name)
		}
	}
	// 소켓을 열어 둔 서비스가 있는데 방화벽이 막고 있으면 정보로 알린다(정상). 방화벽이 여는데 소켓이 없으면 알린다.
	for _, l := range in.Listeners {
		key := fmt.Sprintf("%s/%d", l.Protocol, l.Port)
		if !opened[key] {
			add(Info, fmt.Sprintf("%s is listening on %s but the firewall blocks it", orUnknown(l.Process), key), "Nobody on the network can reach it.")
		}
	}
	for _, o := range in.Firewall {
		found := false
		for _, l := range in.Listeners {
			if l.Protocol == o.Protocol && l.Port == o.Port {
				found = true
			}
		}
		if !found && len(in.Listeners) > 0 {
			add(Info, fmt.Sprintf("%s/%d is open in the firewall but nothing is listening", o.Protocol, o.Port), o.Name)
		}
	}
	rank := map[string]int{Error: 0, Warn: 1, Info: 2, OK: 3}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && rank[out[j].Level] < rank[out[j-1].Level]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// known 은 기능이 여는 이름이다(이미 따로 설명한다).
func known(name string) bool {
	switch name {
	case "Model server API", "SSH", "mDNS", "WireGuard VPN", "Remote desktop (RDP)", "Web admin (HTTPS)", "Model API (HTTPS)":
		return true
	}
	return false
}

func orUnknown(s string) string {
	if s == "" {
		return "a program"
	}
	return s
}

// ParseSS 는 `ss -H -ltunp` 출력에서 loopback 이 아닌 주소에서 받는 소켓을 읽는다.
func ParseSS(text string) []Listener {
	var out []Listener
	seen := map[string]bool{}
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		// 형식: Netid State Recv-Q Send-Q Local:Port Peer:Port [Process]
		if len(f) < 5 {
			continue
		}
		proto := f[0]
		if proto != "tcp" && proto != "udp" {
			continue
		}
		local := f[4]
		i := strings.LastIndexByte(local, ':')
		if i < 0 {
			continue
		}
		host, portText := local[:i], local[i+1:]
		port, err := strconv.Atoi(portText)
		if err != nil {
			continue
		}
		host = strings.Trim(host, "[]")
		if host == "127.0.0.1" || host == "::1" || strings.HasPrefix(host, "127.") {
			continue
		}
		// 인터페이스 이름이 붙은 주소(예: 127.0.0.53%lo)는 loopback 이다.
		if strings.Contains(host, "%lo") {
			continue
		}
		proc := ""
		if len(f) >= 7 {
			if a := strings.Index(f[6], "(\""); a >= 0 {
				if b := strings.Index(f[6][a+2:], "\""); b >= 0 {
					proc = f[6][a+2 : a+2+b]
				}
			}
		}
		key := fmt.Sprintf("%s/%d", proto, port)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, Listener{Protocol: proto, Port: port, Process: proc})
	}
	return out
}
