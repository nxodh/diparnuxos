// Package wireguard 는 WireGuard VPN 인터페이스 하나(wg0)를 systemd-networkd 로 만들고 관리한다.
//
// 상태(개인 키 포함)는 /var/lib/opereta/wireguard.json 에 root 만 읽게 저장하고, networkd 조각 파일
// (50-opereta-wg0.netdev, 50-opereta-wg0.network)은 권한 있는 헬퍼가 쓴다. .netdev 에는 개인 키가 들어가므로
// root:systemd-network 0640 으로 쓴다. 개인 키는 API 응답과 로그에 나오지 않는다. 동료(피어)를 만들 때 서버가 만든
// 피어의 개인 키는 클라이언트 설정에 담아 한 번만 돌려주고 저장하지 않는다.
//
// 새 패키지가 필요 없다: WireGuard 는 커널에 들어 있고 인터페이스는 networkd 가 만든다. 키는 crypto/ecdh 로 만든다.
package wireguard

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// 이름과 기본값이다.
const (
	Interface     = "wg0"
	DefaultPort   = 51820
	DefaultSubnet = "10.8.0.1/24"
	NetdevPath    = "/etc/systemd/network/50-opereta-wg0.netdev"
	NetworkPath   = "/etc/systemd/network/50-opereta-wg0.network"
	maxPeers      = 32
)

// 오류 코드다.
const (
	CodeInvalidInput = "invalid_input"
	CodeNotEnabled   = "vpn_not_enabled"
	CodeNotFound     = "peer_not_found"
	CodeConflict     = "conflict"
	CodeApplyFailed  = "apply_failed"
	CodeInternal     = "internal"
)

// Error 는 코드를 가진 오류다.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, f string, a ...any) *Error { return &Error{code, fmt.Sprintf(f, a...)} }

// Peer 는 원격 기기 하나다.
type Peer struct {
	Name       string   `json:"name"`
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips"`
	Endpoint   string   `json:"endpoint,omitempty"`
	Keepalive  int      `json:"keepalive,omitempty"`
}

// State 는 저장된 설정이다. PrivateKey 는 JSON 응답에 쓰지 않는다(View 를 쓴다).
type State struct {
	Enabled    bool   `json:"enabled"`
	Address    string `json:"address"`
	ListenPort int    `json:"listen_port"`
	PrivateKey string `json:"private_key"`
	Peers      []Peer `json:"peers"`
}

// View 는 API 가 돌려주는 모양이다. 개인 키는 없다.
type View struct {
	Enabled    bool   `json:"enabled"`
	Interface  string `json:"interface"`
	Address    string `json:"address"`
	ListenPort int    `json:"listen_port"`
	PublicKey  string `json:"public_key"`
	Peers      []Peer `json:"peers"`
}

// FileWriter 는 권한 있는 파일 쓰기다.
type FileWriter interface {
	Write(ctx context.Context, path string, data []byte, secret bool) error
	Remove(ctx context.Context, path string) error
}

// Manager 는 VPN 상태를 읽고 바꾼다.
type Manager struct {
	Files  FileWriter
	Runner platform.Runner
	// StatePath 는 상태 파일이다. 비어 있으면 /var/lib/opereta/wireguard.json 이다.
	StatePath string
	// Reapply 는 방화벽 규칙을 다시 적용한다(UDP 포트와 wg0 신뢰). nil 이면 부르지 않는다.
	Reapply func(ctx context.Context) error
	// DefaultEndpoint 는 클라이언트 설정에 넣을 서버 주소(호스트 이름이나 IP)를 돌려준다.
	DefaultEndpoint func() string

	mu sync.Mutex
}

func (m *Manager) path() string {
	if m.StatePath != "" {
		return m.StatePath
	}
	return "/var/lib/opereta/wireguard.json"
}

func (m *Manager) load() (State, error) {
	data, err := os.ReadFile(m.path())
	if errors.Is(err, fs.ErrNotExist) {
		return State{Address: DefaultSubnet, ListenPort: DefaultPort, Peers: []Peer{}}, nil
	}
	if err != nil {
		return State{}, err
	}
	var s State
	if err := json.Unmarshal(data, &s); err != nil {
		return State{}, err
	}
	if s.Peers == nil {
		s.Peers = []Peer{}
	}
	return s, nil
}

func (m *Manager) save(s State) error {
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(m.path()), 0o750); err != nil {
		return err
	}
	tmp := m.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path())
}

// GenerateKey 는 새 X25519 키 쌍을 base64 로 돌려준다. 개인 키는 WireGuard 가 요구하는 대로 클램프한다.
func GenerateKey() (private, public string, err error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", "", err
	}
	raw[0] &= 248
	raw[31] = (raw[31] & 127) | 64
	pub, err := PublicFromPrivate(base64.StdEncoding.EncodeToString(raw[:]))
	if err != nil {
		return "", "", err
	}
	return base64.StdEncoding.EncodeToString(raw[:]), pub, nil
}

// PublicFromPrivate 는 개인 키에서 공개 키를 계산한다.
func PublicFromPrivate(private string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(private)
	if err != nil || len(raw) != 32 {
		return "", errors.New("invalid private key")
	}
	k, err := ecdh.X25519().NewPrivateKey(raw)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

var keyRE = regexp.MustCompile(`^[A-Za-z0-9+/]{43}=$`)

// ValidPublicKey 는 WireGuard 공개 키(32 바이트 base64)인지 본다.
func ValidPublicKey(k string) bool {
	if !keyRE.MatchString(k) {
		return false
	}
	raw, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(raw) == 32
}

var nameRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,31}$`)

// ValidName 은 피어 이름이 1-32 자의 글자, 숫자, 공백, 점, 밑줄, 하이픈인지 본다.
func ValidName(n string) bool { return nameRE.MatchString(n) }

// Render 는 .netdev 와 .network 내용을 만든다.
func Render(s State) (netdev, network string, err error) {
	if _, err := netip.ParsePrefix(s.Address); err != nil {
		return "", "", fmt.Errorf("invalid address %q", s.Address)
	}
	var b strings.Builder
	b.WriteString("# Generated by operetad. Change it with 'opereta vpn'; manual edits are overwritten.\n")
	b.WriteString("[NetDev]\nName=" + Interface + "\nKind=wireguard\nDescription=Opereta VPN\n\n")
	fmt.Fprintf(&b, "[WireGuard]\nPrivateKey=%s\nListenPort=%d\n", s.PrivateKey, s.ListenPort)
	for _, p := range s.Peers {
		b.WriteString("\n[WireGuardPeer]\nPublicKey=" + p.PublicKey + "\n")
		b.WriteString("AllowedIPs=" + strings.Join(p.AllowedIPs, ",") + "\n")
		if p.Endpoint != "" {
			b.WriteString("Endpoint=" + p.Endpoint + "\n")
		}
		if p.Keepalive > 0 {
			fmt.Fprintf(&b, "PersistentKeepalive=%d\n", p.Keepalive)
		}
	}
	n := "# Generated by operetad.\n[Match]\nName=" + Interface + "\n\n[Network]\nAddress=" + s.Address + "\n"
	return b.String(), n, nil
}

func (m *Manager) viewOf(s State) (View, error) {
	v := View{Enabled: s.Enabled, Interface: Interface, Address: s.Address, ListenPort: s.ListenPort, Peers: s.Peers}
	if s.PrivateKey != "" {
		pub, err := PublicFromPrivate(s.PrivateKey)
		if err != nil {
			return v, err
		}
		v.PublicKey = pub
	}
	if v.Peers == nil {
		v.Peers = []Peer{}
	}
	return v, nil
}

// Status 는 현재 설정이다. 개인 키는 없다.
func (m *Manager) Status() (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return View{}, errf(CodeInternal, "reading the VPN state: %v", err)
	}
	return m.viewOf(s)
}

// Opens 는 켜져 있을 때 방화벽이 열 UDP 포트와 신뢰할 인터페이스다.
func (m *Manager) Opens() (port int, iface string, ok bool) {
	s, err := m.load()
	if err != nil || !s.Enabled {
		return 0, "", false
	}
	return s.ListenPort, Interface, true
}

// Update 는 켜고 끄고 주소와 포트를 바꾼다. nil 인 값은 그대로 둔다. 처음 켤 때 키를 만든다.
func (m *Manager) Update(ctx context.Context, enabled *bool, address *string, port *int) (View, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return View{}, errf(CodeInternal, "reading the VPN state: %v", err)
	}
	prev := s
	if address != nil {
		p, perr := netip.ParsePrefix(*address)
		if perr != nil || !p.Addr().Is4() || p.Bits() < 16 || p.Bits() > 30 {
			return View{}, errf(CodeInvalidInput, "the VPN address must be an IPv4 address with a prefix of /16 to /30, such as 10.8.0.1/24")
		}
		// 주소가 바뀌면 피어의 AllowedIPs 가 새 대역 밖일 수 있다. 피어가 있으면 바꾸지 못하게 한다.
		if s.Address != "" && p.String() != s.Address && len(s.Peers) > 0 {
			return View{}, errf(CodeConflict, "remove the peers before changing the VPN address")
		}
		s.Address = p.String()
	}
	if port != nil {
		if *port < 1024 || *port > 65535 {
			return View{}, errf(CodeInvalidInput, "the listen port must be 1024-65535")
		}
		s.ListenPort = *port
	}
	if enabled != nil {
		s.Enabled = *enabled
	}
	if s.Enabled && s.PrivateKey == "" {
		priv, _, kerr := GenerateKey()
		if kerr != nil {
			return View{}, errf(CodeInternal, "generating a key: %v", kerr)
		}
		s.PrivateKey = priv
	}
	if err := m.apply(ctx, s); err != nil {
		return View{}, err
	}
	if err := m.save(s); err != nil {
		_ = m.apply(ctx, prev)
		return View{}, errf(CodeInternal, "saving the VPN state: %v", err)
	}
	if m.Reapply != nil {
		if err := m.Reapply(ctx); err != nil {
			return View{}, errf(CodeApplyFailed, "the VPN is configured but the firewall was not updated: %v", err)
		}
	}
	return m.viewOf(s)
}

// apply 는 상태에 맞게 networkd 조각을 쓰거나 지운다.
func (m *Manager) apply(ctx context.Context, s State) error {
	if !s.Enabled {
		_ = m.Files.Remove(ctx, NetdevPath)
		_ = m.Files.Remove(ctx, NetworkPath)
		m.run(ctx, "networkctl", "reload")
		m.run(ctx, "networkctl", "delete", Interface)
		return nil
	}
	netdev, network, err := Render(s)
	if err != nil {
		return errf(CodeInvalidInput, "%v", err)
	}
	if err := m.Files.Write(ctx, NetdevPath, []byte(netdev), true); err != nil {
		return errf(CodeApplyFailed, "writing the WireGuard configuration: %v", err)
	}
	if err := m.Files.Write(ctx, NetworkPath, []byte(network), false); err != nil {
		return errf(CodeApplyFailed, "writing the VPN network: %v", err)
	}
	if res, rerr := m.run(ctx, "networkctl", "reload"); rerr != nil || res.ExitCode != 0 {
		return errf(CodeApplyFailed, "networkd could not load the VPN configuration")
	}
	// 이미 있는 인터페이스는 .netdev 의 피어 변경이 자동으로 반영되지 않을 수 있다. 다시 적용한다.
	m.run(ctx, "networkctl", "reconfigure", Interface)
	return nil
}

func (m *Manager) run(ctx context.Context, name string, args ...string) (platform.Result, error) {
	return m.Runner.Run(ctx, platform.Command{Name: name, Args: args, Timeout: 20 * time.Second, MaxOutput: 16 << 10})
}

// AddPeer 는 피어를 더한다. publicKey 가 비어 있으면 키 쌍을 만들어 클라이언트 설정에 개인 키를 담아 돌려준다(한 번만).
func (m *Manager) AddPeer(ctx context.Context, name, publicKey, endpointHost string) (Peer, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if !ValidName(name) {
		return Peer{}, "", errf(CodeInvalidInput, "the peer name must be 1-32 letters, digits, spaces, dots, underscores or hyphens")
	}
	s, err := m.load()
	if err != nil {
		return Peer{}, "", errf(CodeInternal, "reading the VPN state: %v", err)
	}
	if !s.Enabled {
		return Peer{}, "", errf(CodeNotEnabled, "turn the VPN on first")
	}
	if len(s.Peers) >= maxPeers {
		return Peer{}, "", errf(CodeConflict, "at most %d peers are allowed", maxPeers)
	}
	for _, p := range s.Peers {
		if strings.EqualFold(p.Name, name) {
			return Peer{}, "", errf(CodeConflict, "a peer named %q already exists", name)
		}
	}
	clientPrivate := ""
	if publicKey == "" {
		var gerr error
		clientPrivate, publicKey, gerr = GenerateKey()
		if gerr != nil {
			return Peer{}, "", errf(CodeInternal, "generating a key: %v", gerr)
		}
	} else if !ValidPublicKey(publicKey) {
		return Peer{}, "", errf(CodeInvalidInput, "the public key must be a WireGuard key (44 characters, base64)")
	}
	for _, p := range s.Peers {
		if p.PublicKey == publicKey {
			return Peer{}, "", errf(CodeConflict, "that public key is already a peer (%s)", p.Name)
		}
	}
	ip, err := nextAddress(s)
	if err != nil {
		return Peer{}, "", err
	}
	peer := Peer{Name: name, PublicKey: publicKey, AllowedIPs: []string{ip + "/32"}}
	prev := s
	s.Peers = append(append([]Peer{}, s.Peers...), peer)
	if err := m.apply(ctx, s); err != nil {
		_ = m.apply(ctx, prev)
		return Peer{}, "", err
	}
	if err := m.save(s); err != nil {
		_ = m.apply(ctx, prev)
		return Peer{}, "", errf(CodeInternal, "saving the VPN state: %v", err)
	}
	config := ""
	if clientPrivate != "" {
		serverPub, _ := PublicFromPrivate(s.PrivateKey)
		host := endpointHost
		if host == "" && m.DefaultEndpoint != nil {
			host = m.DefaultEndpoint()
		}
		if host == "" {
			host = "<this computer's address>"
		}
		config = ClientConfig(clientPrivate, ip, serverPub, fmt.Sprintf("%s:%d", host, s.ListenPort), s.Address)
	}
	return peer, config, nil
}

// ClientConfig 는 피어(원격 기기)에 넣을 wg-quick 형식 설정이다.
func ClientConfig(clientPrivate, clientIP, serverPublic, endpoint, serverAddress string) string {
	subnet := "10.8.0.0/24"
	if p, err := netip.ParsePrefix(serverAddress); err == nil {
		subnet = p.Masked().String()
	}
	return "[Interface]\nPrivateKey = " + clientPrivate + "\nAddress = " + clientIP + "/32\n\n" +
		"[Peer]\nPublicKey = " + serverPublic + "\nEndpoint = " + endpoint + "\nAllowedIPs = " + subnet + "\nPersistentKeepalive = 25\n"
}

// nextAddress 는 서버 주소 대역에서 쓰지 않은 첫 호스트 주소를 돌려준다.
func nextAddress(s State) (string, error) {
	p, err := netip.ParsePrefix(s.Address)
	if err != nil {
		return "", errf(CodeInternal, "the VPN address is not valid")
	}
	used := map[netip.Addr]bool{p.Addr(): true}
	for _, peer := range s.Peers {
		for _, a := range peer.AllowedIPs {
			if pp, err := netip.ParsePrefix(a); err == nil {
				used[pp.Addr()] = true
			}
		}
	}
	net := p.Masked()
	for a := net.Addr().Next(); net.Contains(a); a = a.Next() {
		if used[a] {
			continue
		}
		// 방송 주소는 건너뛴다(마지막 주소).
		if !net.Contains(a.Next()) {
			break
		}
		return a.String(), nil
	}
	return "", errf(CodeConflict, "the VPN address range is full")
}

// RemovePeer 는 이름이나 공개 키로 피어를 지운다.
func (m *Manager) RemovePeer(ctx context.Context, nameOrKey string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, err := m.load()
	if err != nil {
		return errf(CodeInternal, "reading the VPN state: %v", err)
	}
	idx := -1
	for i, p := range s.Peers {
		if p.PublicKey == nameOrKey || strings.EqualFold(p.Name, nameOrKey) {
			idx = i
		}
	}
	if idx < 0 {
		return errf(CodeNotFound, "there is no such peer")
	}
	prev := s
	s.Peers = append(append([]Peer{}, s.Peers[:idx]...), s.Peers[idx+1:]...)
	if err := m.apply(ctx, s); err != nil {
		_ = m.apply(ctx, prev)
		return err
	}
	if err := m.save(s); err != nil {
		_ = m.apply(ctx, prev)
		return errf(CodeInternal, "saving the VPN state: %v", err)
	}
	return nil
}
