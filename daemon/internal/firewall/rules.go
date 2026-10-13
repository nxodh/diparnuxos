package firewall

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
)

// 규칙 한도다.
const (
	MaxRules = 64
	// SourceAny 와 SourceLAN 은 출발지의 이름 값이다. 그 밖의 값은 CIDR 이다.
	SourceAny = "any"
	SourceLAN = "lan"
)

// lanV4 와 lanV6 는 "LAN" 으로 보는 사설 주소 대역이다.
var (
	lanV4 = []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16", "169.254.0.0/16"}
	lanV6 = []string{"fc00::/7", "fe80::/10"}
)

// Open 은 기능이 여는 포트 하나다. 규칙 화면에 읽기 전용으로 보인다.
type Open struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	PortEnd  int    `json:"port_end,omitempty"`
	Source   string `json:"source"`
	// Reason 은 왜 열려 있는지(어느 기능이 연다)다.
	Reason string `json:"reason,omitempty"`
}

// Validate 는 열림 하나가 규칙 파일에 쓸 수 있는 값인지 본다.
func (o Open) Validate() error {
	return validateTarget(o.Name, o.Protocol, o.Port, o.PortEnd, o.Source)
}

// Rule 은 사용자가 만든 허용 규칙이다. 기본 정책은 차단이므로 허용 규칙만 있다.
type Rule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"` // tcp, udp
	Port     int    `json:"port"`
	PortEnd  int    `json:"port_end,omitempty"` // 0 이면 한 포트, 아니면 Port-PortEnd 범위
	Source   string `json:"source"`             // any, lan, CIDR
	Enabled  bool   `json:"enabled"`
}

// Validate 는 규칙을 검사한다.
func (r Rule) Validate() error {
	return validateTarget(r.Name, r.Protocol, r.Port, r.PortEnd, r.Source)
}

var nameRE = regexp.MustCompile(`^[\x20-\x7e]{1,64}$`)

func validateTarget(name, proto string, port, portEnd int, source string) error {
	if !nameRE.MatchString(name) || strings.ContainsAny(name, "\"\\#") {
		return errors.New("the name must be 1-64 printable ASCII characters without quotes, backslashes or #")
	}
	if proto != "tcp" && proto != "udp" {
		return errors.New("the protocol must be tcp or udp")
	}
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is out of range 1-65535", port)
	}
	if portEnd != 0 && (portEnd < port || portEnd > 65535) {
		return fmt.Errorf("the end of the port range %d must be %d-65535", portEnd, port)
	}
	switch source {
	case SourceAny, SourceLAN:
		return nil
	}
	p, err := netip.ParsePrefix(source)
	if err != nil {
		return fmt.Errorf("the source %q must be any, lan or an address range such as 192.168.1.0/24", source)
	}
	if p.Bits() == 0 {
		return errors.New("a /0 source is the same as any; use any")
	}
	return nil
}

func sanitizeComment(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 0x20 || r > 0x7e || r == '#' {
			return '_'
		}
		return r
	}, s)
}

func portExpr(port, portEnd int) string {
	if portEnd != 0 && portEnd != port {
		return fmt.Sprintf("%d-%d", port, portEnd)
	}
	return fmt.Sprintf("%d", port)
}

// nftLines 는 허용 규칙을 nft 규칙 줄로 바꾼다. LAN 과 CIDR 출발지는 IPv4 와 IPv6 줄을 따로 만든다.
func nftLines(proto string, port, portEnd int, source string) []string {
	dport := fmt.Sprintf("%s dport %s accept", proto, portExpr(port, portEnd))
	switch source {
	case SourceAny, "":
		return []string{dport}
	case SourceLAN:
		return []string{
			"ip saddr { " + strings.Join(lanV4, ", ") + " } " + dport,
			"ip6 saddr { " + strings.Join(lanV6, ", ") + " } " + dport,
		}
	}
	p := netip.MustParsePrefix(source)
	if p.Addr().Is4() {
		return []string{"ip saddr " + p.Masked().String() + " " + dport}
	}
	return []string{"ip6 saddr " + p.Masked().String() + " " + dport}
}

// Builtins 는 옵션 때문에 열리는 포트를 사용자가 읽을 수 있게 나열한다(규칙 화면의 읽기 전용 줄).
func Builtins(o Options) []Open {
	var out []Open
	if o.APIOpen {
		out = append(out, Open{Name: "Model server API", Protocol: "tcp", Port: o.APIPort, Source: SourceAny, Reason: "LAN exposure is on (an API key is required)"})
	}
	if o.SSHOpen {
		port := o.SSHPort
		if port == 0 {
			port = defaultSSHPort
		}
		out = append(out, Open{Name: "SSH", Protocol: "tcp", Port: port, Source: SourceAny, Reason: "the SSH server is on"})
	}
	if o.MDNSOpen {
		out = append(out, Open{Name: "mDNS", Protocol: "udp", Port: 5353, Source: SourceAny, Reason: "the <host name>.local name"})
	}
	out = append(out, o.Open...)
	return out
}

// Store 는 사용자 규칙을 JSON 파일로 보관한다. 파일은 데몬(root)만 읽고 쓴다.
type Store struct {
	Path string
	mu   sync.Mutex
}

type storeFile struct {
	Rules []Rule `json:"rules"`
}

// List 는 규칙을 ID 순서대로 돌려준다. 파일이 없으면 빈 목록이다.
func (s *Store) List() ([]Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.load()
}

func (s *Store) load() ([]Rule, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return []Rule{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("firewall: read rules: %w", err)
	}
	var f storeFile
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("firewall: decode rules: %w", err)
	}
	if f.Rules == nil {
		f.Rules = []Rule{}
	}
	return f.Rules, nil
}

func (s *Store) save(rules []Rule) error {
	data, err := json.MarshalIndent(storeFile{Rules: rules}, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o750); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// ErrNotFound 는 없는 규칙 ID 다.
var ErrNotFound = errors.New("firewall: rule not found")

// ErrTooMany 는 규칙 한도를 넘었다는 뜻이다.
var ErrTooMany = fmt.Errorf("firewall: at most %d rules are allowed", MaxRules)

// Add 는 규칙을 더하고 ID 를 정해 돌려준다.
func (s *Store) Add(r Rule) (Rule, error) {
	if err := r.Validate(); err != nil {
		return r, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rules, err := s.load()
	if err != nil {
		return r, err
	}
	if len(rules) >= MaxRules {
		return r, ErrTooMany
	}
	used := map[string]bool{}
	for _, x := range rules {
		used[x.ID] = true
	}
	for i := 1; ; i++ {
		id := fmt.Sprintf("r%d", i)
		if !used[id] {
			r.ID = id
			break
		}
	}
	rules = append(rules, r)
	return r, s.save(rules)
}

// Update 는 ID 가 같은 규칙을 바꾼다.
func (s *Store) Update(r Rule) (Rule, error) {
	if err := r.Validate(); err != nil {
		return r, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	rules, err := s.load()
	if err != nil {
		return r, err
	}
	for i := range rules {
		if rules[i].ID == r.ID {
			rules[i] = r
			return r, s.save(rules)
		}
	}
	return r, ErrNotFound
}

// Remove 는 규칙을 지운다.
func (s *Store) Remove(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	rules, err := s.load()
	if err != nil {
		return err
	}
	kept := rules[:0]
	found := false
	for _, r := range rules {
		if r.ID == id {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return ErrNotFound
	}
	sort.SliceStable(kept, func(i, j int) bool { return kept[i].ID < kept[j].ID })
	return s.save(kept)
}
