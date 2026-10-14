package api

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/network"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
	"github.com/nxodh/diparnuxos/daemon/internal/vedecaps"
)

// ConnectionAddress 는 다른 컴퓨터가 이 컴퓨터에 접속할 때 쓸 수 있는 주소 하나다.
type ConnectionAddress struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Family    string `json:"family"` // ipv4, ipv6
	URL       string `json:"url"`    // 기본 URL(.../v1)
}

// Connection 은 외부 클라이언트(Orca, Open WebUI, 에디터 플러그인 등)가 이 컴퓨터의 모델 서버에 접속하는 데 필요한 정보다.
// API 키 평문은 담지 않는다(평문은 만들 때 한 번만 보인다).
type Connection struct {
	ServerState string   `json:"server_state"`
	ModelID     string   `json:"model_id"`
	Models      []string `json:"models"`
	PublicPort  int      `json:"public_port"`
	LANExposed  bool     `json:"lan_exposed"`
	Gateway     bool     `json:"gateway"`
	KeyRequired bool     `json:"key_required"`
	KeyCount    int      `json:"key_count"`
	// LocalNoKey 는 이 컴퓨터에서 온 요청이 키 없이 통과하는지다. LocalKeyRequired 는 이 컴퓨터에서도 키가 필요한지다.
	LocalNoKey       bool `json:"local_no_key"`
	LocalKeyRequired bool `json:"local_key_required"`
	// SlotCtx 와 Parallel 은 실행 중인 서버의 계획 값이다. 서버가 멈춰 있으면 0 이다.
	SlotCtx   int                 `json:"slot_ctx"`
	Parallel  int                 `json:"parallel"`
	Hostname  string              `json:"hostname"`
	LocalURL  string              `json:"local_url"`
	Addresses []ConnectionAddress `json:"addresses"`
	MDNSURL   string              `json:"mdns_url,omitempty"`
	// TLSActive 가 true 이면 Addresses 와 MDNSURL 은 https 주소(TLS 포트)이고 클라이언트는 CA 가 필요하다.
	// LocalURL 은 항상 평문이다(이 컴퓨터 안의 연결). CAFingerprint 는 배포한 CA 와 비교할 지문이다.
	TLSActive     bool              `json:"tls_active"`
	TLSPort       int               `json:"tls_port,omitempty"`
	CAFingerprint string            `json:"ca_fingerprint,omitempty"`
	Warnings      []string          `json:"warnings"`
	Examples      map[string]string `json:"examples"`
}

// connectionInput 은 Connection 을 만드는 데 필요한 값이다. 순수 함수로 만들어 시험하기 쉽게 한다.
type connectionInput struct {
	Cfg        config.Config
	Server     supervisor.Status
	Interfaces []network.Interface
	KeyCount   int
	ModelIDs   []string
	Hostname   string
	// CAFingerprint 는 TLS 가 켜져 있을 때 CA 의 지문이다(없으면 비어 있다).
	CAFingerprint string
}

func buildConnection(in connectionInput) Connection {
	c := Connection{
		ServerState: string(in.Server.State),
		ModelID:     in.Server.ModelID,
		Models:      append([]string{}, in.ModelIDs...),
		PublicPort:  in.Cfg.Port,
		LANExposed:  in.Cfg.LANExposed,
		Gateway:     in.Cfg.Gateway,
		KeyCount:    in.KeyCount,
		Hostname:    in.Hostname,
		Addresses:   []ConnectionAddress{},
		Warnings:    []string{},
		Examples:    map[string]string{},
	}
	c.KeyRequired = in.Cfg.LANExposed || in.KeyCount > 0
	c.LocalNoKey = in.Cfg.LocalNoKey
	c.LocalKeyRequired = in.KeyCount > 0 && !in.Cfg.LocalNoKey
	if in.Server.Plan != nil {
		c.SlotCtx = in.Server.Plan.SlotCtx
		c.Parallel = in.Server.Plan.Parallel
	}
	port := strconv.Itoa(in.Cfg.Port)
	c.LocalURL = "http://127.0.0.1:" + port + "/v1"
	// TLS 가 떠 있으면 다른 컴퓨터에 알려 줄 주소는 https 와 TLS 포트다(평문은 LAN 에서 닫혀 있다. lan_plain 이면 열려 있다).
	scheme, lanPort := "http", port
	if in.Cfg.TLSActive() {
		scheme, lanPort = "https", strconv.Itoa(in.Cfg.TLSListenPort())
		c.TLSActive, c.TLSPort, c.CAFingerprint = true, in.Cfg.TLSListenPort(), in.CAFingerprint
	}

	if in.Cfg.LANExposed {
		for _, ifc := range in.Interfaces {
			if ifc.State == "down" {
				continue
			}
			for _, a := range ifc.Addresses {
				ip, _, err := net.ParseCIDR(a)
				if err != nil {
					ip = net.ParseIP(a)
				}
				if ip == nil || ip.IsLoopback() || ip.IsLinkLocalUnicast() {
					continue
				}
				fam, host := "ipv4", ip.String()
				if ip.To4() == nil {
					fam, host = "ipv6", "["+ip.String()+"]"
				}
				c.Addresses = append(c.Addresses, ConnectionAddress{
					Interface: ifc.Name, Address: ip.String(), Family: fam, URL: scheme + "://" + host + ":" + lanPort + "/v1",
				})
			}
		}
		sort.SliceStable(c.Addresses, func(i, j int) bool { return c.Addresses[i].Family < c.Addresses[j].Family })
		if in.Hostname != "" {
			c.MDNSURL = scheme + "://" + in.Hostname + ".local:" + lanPort + "/v1"
		}
	}

	switch {
	case in.Server.State != supervisor.StateRunning && in.Server.State != supervisor.StateRunningCPU:
		c.Warnings = append(c.Warnings, "The model server is not running. Start it first (diparnux serve start).")
	}
	if !in.Cfg.LANExposed {
		c.Warnings = append(c.Warnings, "LAN exposure is off: only this computer can connect. Turn it on to let other computers connect (an API key is required).")
	}
	if in.Cfg.LANExposed && in.KeyCount == 0 {
		c.Warnings = append(c.Warnings, "No API key exists. Create one (diparnux apikey create <label>); LAN connections are refused without a key.")
	}
	if in.Cfg.TLSActive() {
		c.Warnings = append(c.Warnings, "These addresses use HTTPS with this computer's own CA. Give each client the CA once (diparnux tls ca --out diparnux-ca.pem) and compare the fingerprint; do not turn certificate checking off.")
		if in.Cfg.LANPlain {
			c.Warnings = append(c.Warnings, "Plain HTTP is also open on the local network (lan_plain): keys and prompts sent there are not encrypted.")
		}
	}
	if in.Cfg.LANExposed && len(c.Addresses) == 0 {
		c.Warnings = append(c.Warnings, "No network address was found. Check the network connection.")
	}

	base := c.LocalURL
	if len(c.Addresses) > 0 {
		base = c.Addresses[0].URL
	}
	model := in.Server.ModelID
	if model == "" && len(in.ModelIDs) > 0 {
		model = in.ModelIDs[0]
	}
	if model == "" {
		model = "<model>"
	}
	c.Examples["settings"] = fmt.Sprintf("Base URL: %s\nAPI key: <your API key>\nModel: %s", base, model)
	caFlag, pyClient, caFile := "", "", ""
	if c.TLSActive && strings.HasPrefix(base, "https://") {
		caFlag = "--cacert diparnux-ca.pem "
		pyClient = ", http_client=httpx.Client(verify=\"diparnux-ca.pem\")"
		caFile = vedecaps.DefaultCAFile
	}
	c.Examples["curl"] = fmt.Sprintf("curl %s%s/chat/completions \\\n  -H 'Authorization: Bearer <your API key>' \\\n  -H 'Content-Type: application/json' \\\n  -d '{\"model\": \"%s\", \"messages\": [{\"role\": \"user\", \"content\": \"Hello\"}]}'", caFlag, base, model)
	imports := "from openai import OpenAI"
	if pyClient != "" {
		imports = "import httpx\nfrom openai import OpenAI"
	}
	c.Examples["openai_python"] = fmt.Sprintf("%s\nclient = OpenAI(base_url=\"%s\", api_key=\"<your API key>\"%s)\nreply = client.chat.completions.create(\n    model=\"%s\",\n    messages=[{\"role\": \"user\", \"content\": \"Hello\"}],\n)\nprint(reply.choices[0].message.content)", imports, base, pyClient, model)
	if frag, err := vedecaps.Fragment(vedecaps.Input{
		BaseURL: base, ModelID: model, SlotCtx: c.SlotCtx, CAFile: caFile,
		NeedsKey: in.Cfg.LANExposed && len(c.Addresses) > 0 || c.LocalKeyRequired,
	}); err == nil && model != "<model>" {
		c.Examples["vedecaps"] = vedecaps.Pretty(frag)
	}
	c.Examples["environment"] = fmt.Sprintf("export OPENAI_BASE_URL=%s\nexport OPENAI_API_KEY=<your API key>", base)
	return c
}

// getConnection 은 외부에서 이 컴퓨터의 모델 서버에 접속하는 데 필요한 정보를 돌려준다.
func (s *server) getConnection(w http.ResponseWriter, r *http.Request) {
	cfg, err := config.Load(s.d.ConfigPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "config_unreadable", "%v", err)
		return
	}
	in := connectionInput{Cfg: cfg, Server: s.d.Supervisor.Status()}
	if s.d.Keys != nil {
		if n, err := s.d.Keys.Count(); err == nil {
			in.KeyCount = n
		}
	}
	if list, err := s.d.Store.List(); err == nil {
		for _, m := range list {
			if !m.Incomplete {
				in.ModelIDs = append(in.ModelIDs, m.ID)
			}
		}
	}
	if s.d.Network != nil {
		if st, err := s.d.Network.Status(r.Context()); err == nil {
			in.Interfaces = st.Interfaces
		}
	}
	if h, err := os.Hostname(); err == nil {
		in.Hostname = strings.TrimSpace(h)
	}
	if cfg.TLSActive() && s.d.TLS != nil {
		if info, err := s.d.TLS.Info(); err == nil {
			in.CAFingerprint = info.CAFingerprint
		}
	}
	writeJSON(w, http.StatusOK, buildConnection(in))
}
