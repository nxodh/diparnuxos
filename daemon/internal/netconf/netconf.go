// Package netconf 는 유선 인터페이스의 IP 설정(DHCP 또는 고정 주소), DNS, 호스트 이름, 깨우기(Wake-on-LAN)를 바꾸고
// 연결 진단을 한다.
//
// 설정은 systemd-networkd 의 조각 파일(/etc/systemd/network/10-opereta-<이름>.network)로 쓴다. 데몬은 /etc 에 쓸 수
// 없으므로 권한 있는 헬퍼(internal/admin)로 쓴다. 잘못된 설정은 접속을 끊을 수 있으므로 변경은 "확인 대기"로 적용한다:
// 적용한 뒤 제한 시간 안에 Confirm 을 부르지 않으면 이전 설정으로 되돌린다. 되돌릴 정보는 디스크에도 남겨 데몬이
// 다시 시작되어도 되돌린다.
package netconf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/network"
	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// 오류 코드다.
const (
	CodeInvalidInput = "invalid_input"
	CodeNoInterface  = "interface_not_found"
	CodeNotWired     = "not_wired"
	CodeNothingToDo  = "nothing_pending"
	CodeApplyFailed  = "apply_failed"
	CodeInternal     = "internal"
)

// Error 는 코드를 가진 오류다.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, a ...any) *Error { return &Error{code, fmt.Sprintf(format, a...)} }

// 모드 값이다.
const (
	ModeDHCP   = "dhcp"
	ModeStatic = "static"
)

// DefaultConfirm 은 적용 뒤 확인을 기다리는 기본 시간이다.
const DefaultConfirm = 60 * time.Second

// Config 는 인터페이스 하나의 IP 설정이다.
type Config struct {
	Interface string   `json:"interface"`
	Mode      string   `json:"mode"`
	Addresses []string `json:"addresses,omitempty"` // CIDR. 고정일 때만
	Gateway   string   `json:"gateway,omitempty"`
	DNS       []string `json:"dns,omitempty"`
	Domains   []string `json:"domains,omitempty"`
	// Managed 가 false 이면 Opereta 조각이 없어 이미지 기본값(DHCP)을 쓰는 중이다.
	Managed bool `json:"managed"`
}

// Pending 은 확인을 기다리는 변경이다.
type Pending struct {
	Interface string    `json:"interface"`
	Deadline  time.Time `json:"deadline"`
	// 아래는 되돌릴 이전 상태다.
	HadFile     bool   `json:"had_file"`
	PreviousRaw string `json:"previous_raw"`
}

// FileWriter 는 권한 있는 파일 쓰기다. *admin.Client 가 만족한다.
type FileWriter interface {
	Write(ctx context.Context, path string, data []byte, secret bool) error
	Remove(ctx context.Context, path string) error
}

// InterfaceLister 는 인터페이스 목록이다. *network.Manager 는 Status 로 만족시킨다(Wrap 참고).
type InterfaceLister interface {
	Interfaces(ctx context.Context) ([]network.Interface, bool, error)
}

// Manager 는 설정을 쓰고 되돌린다.
type Manager struct {
	Files  FileWriter
	Runner platform.Runner
	Lister InterfaceLister
	// Dir 은 networkd 조각 디렉터리다. 비어 있으면 /etc/systemd/network 다.
	Dir string
	// HostsPath 는 /etc/hosts 다.
	HostsPath string
	// PendingPath 는 되돌릴 정보를 남기는 파일이다. 비어 있으면 /var/lib/opereta/netconf-pending.json 이다.
	PendingPath string
	Now         func() time.Time
	// AfterFunc 는 시험이 타이머를 대신할 때 쓴다. nil 이면 time.AfterFunc 다.
	AfterFunc func(d time.Duration, f func()) (stop func() bool)

	mu    sync.Mutex
	stop  func() bool
	state *Pending
}

func (m *Manager) dir() string {
	if m.Dir != "" {
		return m.Dir
	}
	return "/etc/systemd/network"
}

func (m *Manager) hostsPath() string {
	if m.HostsPath != "" {
		return m.HostsPath
	}
	return "/etc/hosts"
}

func (m *Manager) pendingPath() string {
	if m.PendingPath != "" {
		return m.PendingPath
	}
	return "/var/lib/opereta/netconf-pending.json"
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

var ifaceRE = regexp.MustCompile(`^[a-z][a-z0-9]{0,14}$`)

// FilePath 는 인터페이스의 조각 파일 경로다.
func FilePath(dir, iface string) string {
	return filepath.Join(dir, "10-opereta-"+iface+".network")
}

// ValidCIDR 는 호스트 주소가 붙은 CIDR(예: 192.168.0.50/24)인지 본다. 네트워크 주소나 방송 주소도 거부하지 않는다.
func ValidCIDR(s string) bool {
	p, err := netip.ParsePrefix(s)
	return err == nil && p.Addr().IsValid() && !p.Addr().IsUnspecified() && !p.Addr().IsMulticast() && !p.Addr().IsLoopback()
}

// ValidIP 는 IP 주소인지 본다.
func ValidIP(s string) bool {
	a, err := netip.ParseAddr(s)
	return err == nil && !a.IsUnspecified() && !a.IsMulticast()
}

var domainRE = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)*[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// Validate 는 설정을 검사하고 정규화한다.
func Validate(c Config) (Config, error) {
	if !ifaceRE.MatchString(c.Interface) {
		return c, errf(CodeInvalidInput, "the interface name is not valid")
	}
	switch c.Mode {
	case ModeDHCP:
		if len(c.Addresses) > 0 || c.Gateway != "" {
			return c, errf(CodeInvalidInput, "addresses and a gateway are for static mode only")
		}
	case ModeStatic:
		if len(c.Addresses) == 0 {
			return c, errf(CodeInvalidInput, "static mode needs at least one address, such as 192.168.0.50/24")
		}
		for _, a := range c.Addresses {
			if !ValidCIDR(a) {
				return c, errf(CodeInvalidInput, "%q is not an address with a prefix length, such as 192.168.0.50/24", a)
			}
		}
		if c.Gateway != "" {
			if !ValidIP(c.Gateway) {
				return c, errf(CodeInvalidInput, "%q is not a gateway address", c.Gateway)
			}
		}
	default:
		return c, errf(CodeInvalidInput, "mode must be dhcp or static")
	}
	if len(c.Addresses) > 8 || len(c.DNS) > 8 || len(c.Domains) > 8 {
		return c, errf(CodeInvalidInput, "too many addresses, DNS servers or domains")
	}
	for _, d := range c.DNS {
		if !ValidIP(d) {
			return c, errf(CodeInvalidInput, "%q is not a DNS server address", d)
		}
	}
	for i, d := range c.Domains {
		d = strings.ToLower(strings.TrimSpace(d))
		if len(d) > 253 || !domainRE.MatchString(d) {
			return c, errf(CodeInvalidInput, "%q is not a search domain", d)
		}
		c.Domains[i] = d
	}
	return c, nil
}

// Render 는 networkd 조각 파일을 만든다.
func Render(c Config) string {
	var b strings.Builder
	b.WriteString("# Generated by operetad. Change it with 'opereta network set'; manual edits are overwritten.\n")
	b.WriteString("[Match]\nName=" + c.Interface + "\n\n[Network]\n")
	if c.Mode == ModeStatic {
		b.WriteString("DHCP=no\n")
		for _, a := range c.Addresses {
			b.WriteString("Address=" + a + "\n")
		}
		if c.Gateway != "" {
			b.WriteString("Gateway=" + c.Gateway + "\n")
		}
		b.WriteString("IPv6AcceptRA=yes\n")
	} else {
		b.WriteString("DHCP=yes\n")
	}
	for _, d := range c.DNS {
		b.WriteString("DNS=" + d + "\n")
	}
	if len(c.Domains) > 0 {
		b.WriteString("Domains=" + strings.Join(c.Domains, " ") + "\n")
	}
	b.WriteString("MulticastDNS=yes\n")
	if c.Mode == ModeDHCP && len(c.DNS) > 0 {
		b.WriteString("\n[DHCPv4]\nUseDNS=no\n\n[DHCPv6]\nUseDNS=no\n")
	}
	return b.String()
}

// Parse 는 Render 가 만든 조각 파일을 읽는다. 알아보지 못한 줄은 무시한다.
func Parse(iface, text string) Config {
	c := Config{Interface: iface, Mode: ModeDHCP, Managed: true}
	section := ""
	for _, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || section != "[Network]" {
			continue
		}
		switch strings.TrimSpace(key) {
		case "DHCP":
			if strings.TrimSpace(val) == "no" {
				c.Mode = ModeStatic
			}
		case "Address":
			c.Addresses = append(c.Addresses, strings.TrimSpace(val))
		case "Gateway":
			c.Gateway = strings.TrimSpace(val)
		case "DNS":
			c.DNS = append(c.DNS, strings.TrimSpace(val))
		case "Domains":
			c.Domains = append(c.Domains, strings.Fields(val)...)
		}
	}
	return c
}

// wired 는 목록에서 이름이 name 인 유선 인터페이스를 찾는다.
func (m *Manager) wired(ctx context.Context, name string) error {
	if !ifaceRE.MatchString(name) {
		return errf(CodeInvalidInput, "the interface name is not valid")
	}
	list, _, err := m.Lister.Interfaces(ctx)
	if err != nil {
		return errf(CodeInternal, "reading the interfaces: %v", err)
	}
	for _, it := range list {
		if it.Name == name {
			if it.Kind != "ethernet" {
				return errf(CodeNotWired, "%s is not a wired interface; Wi-Fi addresses are assigned by DHCP", name)
			}
			return nil
		}
	}
	return errf(CodeNoInterface, "there is no interface named %s", name)
}

// Get 은 인터페이스의 현재 설정이다. 조각 파일이 없으면 DHCP(이미지 기본값)다.
func (m *Manager) Get(ctx context.Context, name string) (Config, error) {
	if err := m.wired(ctx, name); err != nil {
		return Config{}, err
	}
	return m.read(name)
}

func (m *Manager) read(name string) (Config, error) {
	data, err := os.ReadFile(FilePath(m.dir(), name))
	if errors.Is(err, fs.ErrNotExist) {
		return Config{Interface: name, Mode: ModeDHCP}, nil
	}
	if err != nil {
		return Config{}, errf(CodeInternal, "reading the network configuration: %v", err)
	}
	return Parse(name, string(data)), nil
}

// Set 은 설정을 쓰고 networkd 에 반영한다. confirm 이 0 보다 크면 그 시간 안에 Confirm 을 부르지 않을 때 되돌린다.
func (m *Manager) Set(ctx context.Context, c Config, confirm time.Duration) (Config, *Pending, error) {
	c, err := Validate(c)
	if err != nil {
		return c, nil, err
	}
	if err := m.wired(ctx, c.Interface); err != nil {
		return c, nil, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state != nil {
		return c, nil, errf(CodeApplyFailed, "a change to %s is waiting for confirmation; confirm or wait for it to revert first", m.state.Interface)
	}
	path := FilePath(m.dir(), c.Interface)
	prev, rerr := os.ReadFile(path)
	pending := &Pending{Interface: c.Interface, HadFile: rerr == nil, PreviousRaw: string(prev)}

	// DHCP 이고 DNS 재정의도 없으면 조각 파일을 두지 않는다(이미지 기본값을 쓴다).
	var werr error
	if c.Mode == ModeDHCP && len(c.DNS) == 0 && len(c.Domains) == 0 {
		werr = m.Files.Remove(ctx, path)
	} else {
		werr = m.Files.Write(ctx, path, []byte(Render(c)), false)
	}
	if werr != nil {
		return c, nil, errf(CodeApplyFailed, "writing the configuration: %v", werr)
	}
	if err := m.reload(ctx, c.Interface); err != nil {
		m.restoreLocked(ctx, pending)
		return c, nil, errf(CodeApplyFailed, "networkd did not accept the configuration (it was reverted): %v", err)
	}
	c.Managed = c.Mode == ModeStatic || len(c.DNS) > 0 || len(c.Domains) > 0
	if confirm > 0 {
		pending.Deadline = m.now().Add(confirm)
		m.state = pending
		m.persistLocked()
		after := m.AfterFunc
		if after == nil {
			after = func(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }
		}
		m.stop = after(confirm, func() { m.expire(pending.Interface) })
		return c, pending, nil
	}
	return c, nil, nil
}

// reload 는 networkd 가 조각 파일을 다시 읽고 인터페이스에 적용하게 한다.
func (m *Manager) reload(ctx context.Context, iface string) error {
	for _, args := range [][]string{{"reload"}, {"reconfigure", iface}} {
		res, err := m.Runner.Run(ctx, platform.Command{Name: "networkctl", Args: args, Timeout: 20 * time.Second, MaxOutput: 16 << 10})
		if err != nil {
			return err
		}
		if res.ExitCode != 0 {
			return fmt.Errorf("networkctl %s exited %d: %s", args[0], res.ExitCode, strings.TrimSpace(string(res.Stderr)))
		}
	}
	return nil
}

// restoreLocked 는 이전 상태를 되돌린다. 오류는 로그에 남기지 않고 돌려주지 않는다(최선을 다한다).
func (m *Manager) restoreLocked(ctx context.Context, p *Pending) {
	path := FilePath(m.dir(), p.Interface)
	ctx = context.WithoutCancel(ctx)
	if p.HadFile {
		_ = m.Files.Write(ctx, path, []byte(p.PreviousRaw), false)
	} else {
		_ = m.Files.Remove(ctx, path)
	}
	_ = m.reload(ctx, p.Interface)
}

// Confirm 은 대기 중인 변경을 확정한다.
func (m *Manager) Confirm() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return errf(CodeNothingToDo, "no network change is waiting for confirmation")
	}
	if m.stop != nil {
		m.stop()
	}
	m.state, m.stop = nil, nil
	m.clearPersistedLocked()
	return nil
}

// Revert 는 대기 중인 변경을 지금 되돌린다.
func (m *Manager) Revert(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return errf(CodeNothingToDo, "no network change is waiting for confirmation")
	}
	if m.stop != nil {
		m.stop()
	}
	m.restoreLocked(ctx, m.state)
	m.state, m.stop = nil, nil
	m.clearPersistedLocked()
	return nil
}

// expire 는 타이머가 끝났을 때 되돌린다.
func (m *Manager) expire(iface string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil || m.state.Interface != iface {
		return
	}
	m.restoreLocked(context.Background(), m.state)
	m.state, m.stop = nil, nil
	m.clearPersistedLocked()
}

// Pending 은 확인을 기다리는 변경이다. 없으면 nil 이다.
func (m *Manager) Pending() *Pending {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.state == nil {
		return nil
	}
	cp := *m.state
	return &cp
}

// Recover 는 데몬이 시작될 때 남은 변경이 있으면 되돌린다. 확인을 기다리던 중 데몬이 다시 시작되었다면 사용자가
// 확인하지 못한 것으로 본다.
func (m *Manager) Recover(ctx context.Context) error {
	data, err := os.ReadFile(m.pendingPath())
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var p Pending
	if err := json.Unmarshal(data, &p); err != nil || !ifaceRE.MatchString(p.Interface) {
		_ = os.Remove(m.pendingPath())
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.restoreLocked(ctx, &p)
	m.clearPersistedLocked()
	return nil
}

func (m *Manager) persistLocked() {
	if m.state == nil {
		return
	}
	data, err := json.Marshal(m.state)
	if err != nil {
		return
	}
	_ = os.MkdirAll(filepath.Dir(m.pendingPath()), 0o750)
	_ = os.WriteFile(m.pendingPath(), data, 0o600)
}

func (m *Manager) clearPersistedLocked() { _ = os.Remove(m.pendingPath()) }

// ---- 깨우기(Wake-on-LAN) ----

// WOLPath 는 깨우기 설정 파일이다. udev 가 부팅 때 읽어 유선 어댑터에 적용한다(systemd.link 의 WakeOnLan=).
func (m *Manager) wolPath() string { return filepath.Join(m.dir(), "10-opereta-wol.link") }

// WOL 은 깨우기가 켜져 있는지(설정 파일이 있는지) 본다. 어댑터와 BIOS 가 지원해야 실제로 동작한다.
func (m *Manager) WOL() bool {
	_, err := os.Stat(m.wolPath())
	return err == nil
}

// SetWOL 은 깨우기 설정 파일을 쓰거나 지운다. 다음 부팅부터 적용된다.
func (m *Manager) SetWOL(ctx context.Context, on bool) error {
	if !on {
		if err := m.Files.Remove(ctx, m.wolPath()); err != nil {
			return errf(CodeApplyFailed, "removing the Wake-on-LAN setting: %v", err)
		}
		return nil
	}
	text := "# Generated by operetad. Wake the computer with a magic packet (applies from the next boot).\n" +
		"[Match]\nType=ether\nOriginalName=en* eth*\n\n[Link]\nWakeOnLan=magic\n"
	if err := m.Files.Write(ctx, m.wolPath(), []byte(text), false); err != nil {
		return errf(CodeApplyFailed, "writing the Wake-on-LAN setting: %v", err)
	}
	return nil
}

// ---- 호스트 이름 ----

var hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)

// ValidHostname 은 호스트 이름이 소문자, 숫자, 하이픈으로 된 63자 이하의 한 단어인지 본다.
func ValidHostname(name string) bool { return hostnameRE.MatchString(name) }

// Hostname 은 현재 호스트 이름이다.
func (m *Manager) Hostname() string {
	h, err := os.Hostname()
	if err != nil {
		return ""
	}
	return h
}

// SetHostname 은 호스트 이름을 바꾸고 /etc/hosts 의 127.0.1.1 줄을 맞춘다.
func (m *Manager) SetHostname(ctx context.Context, name string) error {
	name = strings.ToLower(strings.TrimSpace(name))
	if !ValidHostname(name) {
		return errf(CodeInvalidInput, "the host name must use only a-z, 0-9 and -, start and end with a letter or digit, and be at most 63 characters")
	}
	res, err := m.Runner.Run(ctx, platform.Command{Name: "hostnamectl", Args: []string{"set-hostname", name}, Timeout: 20 * time.Second, MaxOutput: 16 << 10})
	if err != nil {
		return errf(CodeApplyFailed, "hostnamectl: %v", err)
	}
	if res.ExitCode != 0 {
		return errf(CodeApplyFailed, "hostnamectl exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	hosts, rerr := os.ReadFile(m.hostsPath())
	if rerr != nil && !errors.Is(rerr, fs.ErrNotExist) {
		return errf(CodeApplyFailed, "reading /etc/hosts: %v", rerr)
	}
	if err := m.Files.Write(ctx, "/etc/hosts", []byte(UpdateHosts(string(hosts), name)), false); err != nil {
		return errf(CodeApplyFailed, "the host name was changed, but /etc/hosts could not be updated: %v", err)
	}
	return nil
}

// UpdateHosts 는 /etc/hosts 에서 127.0.1.1 줄을 name 으로 맞춘다. 줄이 없으면 localhost 줄 뒤에 더한다.
func UpdateHosts(hosts, name string) string {
	lines := strings.Split(strings.TrimRight(hosts, "\n"), "\n")
	if len(lines) == 1 && lines[0] == "" {
		lines = nil
	}
	entry := "127.0.1.1\t" + name
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 1 && f[0] == "127.0.1.1" {
			lines[i] = entry
			return strings.Join(lines, "\n") + "\n"
		}
	}
	for i, l := range lines {
		f := strings.Fields(l)
		if len(f) >= 1 && f[0] == "127.0.0.1" {
			out := append(append(append([]string{}, lines[:i+1]...), entry), lines[i+1:]...)
			return strings.Join(out, "\n") + "\n"
		}
	}
	return strings.Join(append(lines, entry), "\n") + "\n"
}

// ---- 진단 ----

// Check 는 진단 항목 하나다.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Skip   bool   `json:"skipped,omitempty"`
	Detail string `json:"detail"`
}

// Diagnose 는 연결을 단계별로 점검한다: 주소, 기본 경로, 게이트웨이 응답, 이름 확인, 인터넷 도달.
func (m *Manager) Diagnose(ctx context.Context) []Check {
	var out []Check
	list, online, err := m.Lister.Interfaces(ctx)
	if err != nil {
		return []Check{{Name: "interfaces", Detail: err.Error()}}
	}
	var withAddr []string
	gateway := ""
	for _, it := range list {
		if it.State != "down" && len(it.Addresses) > 0 {
			withAddr = append(withAddr, it.Name+" "+strings.Join(it.Addresses, ", "))
		}
		if gateway == "" && it.Gateway != "" {
			gateway = it.Gateway
		}
	}
	sort.Strings(withAddr)
	out = append(out, Check{Name: "address", OK: len(withAddr) > 0, Detail: orNone(strings.Join(withAddr, "; "), "no interface has an address")})
	out = append(out, Check{Name: "route", OK: online && gateway != "", Detail: orNone(gateway, "there is no default route")})

	switch {
	case gateway == "":
		out = append(out, Check{Name: "gateway", Skip: true, Detail: "no gateway to test"})
	default:
		res, err := m.Runner.Run(ctx, platform.Command{Name: "ping", Args: []string{"-c", "1", "-W", "2", gateway}, Timeout: 6 * time.Second, MaxOutput: 8 << 10})
		switch {
		case err != nil:
			out = append(out, Check{Name: "gateway", Skip: true, Detail: "ping is not available"})
		case res.ExitCode == 0:
			out = append(out, Check{Name: "gateway", OK: true, Detail: gateway + " answered"})
		default:
			out = append(out, Check{Name: "gateway", Detail: gateway + " did not answer"})
		}
	}

	dctx, cancel := context.WithTimeout(ctx, 6*time.Second)
	addrs, derr := net.DefaultResolver.LookupHost(dctx, "huggingface.co")
	cancel()
	if derr != nil {
		out = append(out, Check{Name: "dns", Detail: "huggingface.co could not be resolved"})
	} else {
		out = append(out, Check{Name: "dns", OK: true, Detail: "huggingface.co is " + addrs[0]})
	}

	var d net.Dialer
	cctx, cancel2 := context.WithTimeout(ctx, 6*time.Second)
	conn, cerr := d.DialContext(cctx, "tcp", "huggingface.co:443")
	cancel2()
	if cerr != nil {
		out = append(out, Check{Name: "internet", Detail: "cannot connect to huggingface.co:443"})
	} else {
		conn.Close()
		out = append(out, Check{Name: "internet", OK: true, Detail: "huggingface.co:443 is reachable"})
	}
	return out
}

func orNone(s, none string) string {
	if s == "" {
		return none
	}
	return s
}
