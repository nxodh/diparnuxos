// Package network 는 인터넷 연결 상태를 읽고 Wi-Fi 에 연결한다.
//
// 유선은 systemd-networkd 가 DHCP 로 맡으므로 상태만 읽는다(ip -j). 무선은 iwd 가 맡는다. 이 패키지는 iwd 의
// D-Bus 인터페이스(net.connman.iwd)를 systemd 의 busctl 로 부른다(--json=short 로 구조화된 출력을 받는다).
// 외부 D-Bus 라이브러리를 새로 들이지 않고, 모든 명령은 platform.Runner 로 실행하므로 실제 iwd 없이 시험할 수 있다.
//
// 비밀번호(passphrase)는 인자나 로그에 남기지 않는다. iwd 는 알려진 네트워크(/var/lib/iwd/<SSID>.psk)의 비밀번호로
// 에이전트 없이 연결하므로, 설정 파일을 모드 0600 으로 쓴 뒤 Connect 를 부른다. 연결에 실패하면 새로 만든 파일을 지운다.
package network

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

// 오류 코드다. API 계층이 HTTP 상태로 바꾼다.
const (
	CodeNoWifi         = "no_wifi_device"
	CodeIwdUnavailable = "iwd_unavailable"
	CodeNotFound       = "network_not_found"
	CodeInvalidSSID    = "invalid_ssid"
	CodeInvalidPass    = "invalid_passphrase"
	CodeConnectFailed  = "connect_failed"
	CodeNotConnected   = "not_connected"
	CodeInternal       = "internal"
)

// Error 는 코드를 가진 오류다.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Interface 는 네트워크 인터페이스 하나다.
type Interface struct {
	Name string `json:"name"`
	// Kind 는 ethernet, wifi, other 중 하나다.
	Kind string `json:"kind"`
	// State 는 up, down, unknown 이다(커널의 operstate).
	State     string   `json:"state"`
	MAC       string   `json:"mac,omitempty"`
	Addresses []string `json:"addresses"`
	Gateway   string   `json:"gateway,omitempty"`
}

// WifiNetwork 는 주변에서 보이는 무선 네트워크다.
type WifiNetwork struct {
	SSID string `json:"ssid"`
	// Security 는 open, psk, 8021x, wep 이다. iwd 의 Network.Type 과 같다.
	Security string `json:"security"`
	// Signal 은 0~100 이다. dBm 은 SignalDBm 에 있다.
	Signal    int  `json:"signal"`
	SignalDBm int  `json:"signal_dbm"`
	Connected bool `json:"connected"`
	// Known 은 비밀번호가 저장되어 있다는 뜻이다.
	Known bool `json:"known"`
}

// WifiState 는 무선 장치의 상태다.
type WifiState struct {
	// Available 이 거짓이면 Reason 에 이유(no_wifi_device, iwd_unavailable)가 있다.
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Device    string `json:"device,omitempty"`
	Powered   bool   `json:"powered"`
	// State 는 iwd Station 의 상태다(connected, disconnected, connecting, disconnecting, roaming).
	State         string `json:"state,omitempty"`
	Scanning      bool   `json:"scanning"`
	ConnectedSSID string `json:"connected_ssid,omitempty"`
}

// Status 는 GET /v1/network 의 응답이다.
type Status struct {
	Interfaces []Interface `json:"interfaces"`
	Wifi       WifiState   `json:"wifi"`
	// Online 은 기본 경로(default route)가 있고 주소가 있는 인터페이스가 있다는 뜻이다. 인터넷 도달을 뜻하지는 않는다.
	Online bool `json:"online"`
}

// Manager 는 busctl, ip 와 iwd 설정 디렉터리를 쓴다.
type Manager struct {
	Runner platform.Runner
	// BusctlArgs 는 busctl 앞에 붙는 인자다(시험이 사설 버스 주소를 줄 때 쓴다).
	BusctlArgs []string
	// IwdDir 은 iwd 의 저장 디렉터리다. 비어 있으면 /var/lib/iwd 다.
	IwdDir string
	// SysClassNet 은 인터페이스가 무선인지 보는 /sys/class/net 이다.
	SysClassNet string
	// ConnectTimeout 은 Connect 호출의 제한 시간이다. 0 이면 45초다.
	ConnectTimeout time.Duration
	// KnownWait 는 새 설정 파일을 iwd 가 읽을 때까지 기다리는 시간이다. 0 이면 8초다.
	KnownWait time.Duration
}

const iwdService = "net.connman.iwd"

func (m *Manager) iwdDir() string {
	if m.IwdDir != "" {
		return m.IwdDir
	}
	return "/var/lib/iwd"
}

func (m *Manager) sysClassNet() string {
	if m.SysClassNet != "" {
		return m.SysClassNet
	}
	return "/sys/class/net"
}

// run 은 명령을 실행하고 종료 코드가 0 이 아니면 오류로 바꾼다.
func (m *Manager) run(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, string, error) {
	res, err := m.Runner.Run(ctx, platform.Command{Name: name, Args: args, Timeout: timeout, MaxOutput: 1 << 20})
	if err != nil {
		return nil, "", err
	}
	if res.ExitCode != 0 {
		return res.Stdout, strings.TrimSpace(string(res.Stderr)), fmt.Errorf("%s exited %d", name, res.ExitCode)
	}
	return res.Stdout, "", nil
}

func (m *Manager) busctl(ctx context.Context, timeout time.Duration, args ...string) ([]byte, string, error) {
	full := append(append([]string{}, m.BusctlArgs...), args...)
	return m.run(ctx, timeout, "busctl", full...)
}

// ---- 인터페이스 상태 ----

type ipAddr struct {
	Ifname    string `json:"ifname"`
	Operstate string `json:"operstate"`
	Address   string `json:"address"`
	LinkType  string `json:"link_type"`
	AddrInfo  []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		Prefixlen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
	} `json:"addr_info"`
}

type ipRoute struct {
	Dst     string `json:"dst"`
	Gateway string `json:"gateway"`
	Dev     string `json:"dev"`
}

// Interfaces 는 루프백을 뺀 인터페이스와 기본 경로를 읽는다.
func (m *Manager) Interfaces(ctx context.Context) ([]Interface, bool, error) {
	out, _, err := m.run(ctx, 10*time.Second, "ip", "-j", "addr", "show")
	if err != nil {
		return nil, false, errf(CodeInternal, "ip addr: %v", err)
	}
	var addrs []ipAddr
	if err := json.Unmarshal(out, &addrs); err != nil {
		return nil, false, errf(CodeInternal, "parse ip addr: %v", err)
	}
	gateways := map[string]string{}
	online := false
	if rout, _, rerr := m.run(ctx, 10*time.Second, "ip", "-j", "route", "show", "default"); rerr == nil {
		var routes []ipRoute
		if json.Unmarshal(rout, &routes) == nil {
			for _, r := range routes {
				if r.Dev != "" {
					if _, ok := gateways[r.Dev]; !ok {
						gateways[r.Dev] = r.Gateway
					}
				}
			}
		}
	}
	var list []Interface
	for _, a := range addrs {
		if a.LinkType == "loopback" || a.Ifname == "lo" {
			continue
		}
		it := Interface{Name: a.Ifname, State: strings.ToLower(a.Operstate), MAC: a.Address, Addresses: []string{}}
		if it.State == "" {
			it.State = "unknown"
		}
		it.Kind = m.kind(a.Ifname, a.LinkType)
		for _, ai := range a.AddrInfo {
			if ai.Scope == "link" && ai.Family == "inet6" {
				continue
			}
			it.Addresses = append(it.Addresses, fmt.Sprintf("%s/%d", ai.Local, ai.Prefixlen))
		}
		it.Gateway = gateways[a.Ifname]
		if it.Gateway != "" && len(it.Addresses) > 0 {
			online = true
		}
		list = append(list, it)
	}
	sort.SliceStable(list, func(i, j int) bool { return kindOrder(list[i].Kind) < kindOrder(list[j].Kind) })
	return list, online, nil
}

func kindOrder(k string) int {
	switch k {
	case "ethernet":
		return 0
	case "wifi":
		return 1
	}
	return 2
}

func (m *Manager) kind(name, linkType string) string {
	if _, err := os.Stat(filepath.Join(m.sysClassNet(), name, "wireless")); err == nil {
		return "wifi"
	}
	if linkType == "ether" {
		// 가상 인터페이스(브리지, veth 등)는 device 링크가 없다.
		if _, err := os.Stat(filepath.Join(m.sysClassNet(), name, "device")); err == nil {
			return "ethernet"
		}
		return "other"
	}
	return "other"
}

// ---- iwd ----

type variant struct {
	Type string          `json:"type"`
	Data json.RawMessage `json:"data"`
}

func (v variant) str() string {
	var s string
	_ = json.Unmarshal(v.Data, &s)
	return s
}

func (v variant) boolean() bool {
	var b bool
	_ = json.Unmarshal(v.Data, &b)
	return b
}

type object map[string]map[string]variant

type managedReply struct {
	Data []map[string]object `json:"data"`
}

type iwdTree struct {
	device    string // 무선 Device 객체 경로(Station 이 없어도 Device 가 있으면 잡는다. 꺼진 장치는 Station 이 없다)
	deviceObj object
	station   bool
	networks  map[string]object // 객체 경로 -> Network
	known     map[string]object // 객체 경로 -> KnownNetwork
}

func (m *Manager) tree(ctx context.Context) (*iwdTree, error) {
	out, stderr, err := m.busctl(ctx, 15*time.Second, "--json=short", "call", iwdService, "/", "org.freedesktop.DBus.ObjectManager", "GetManagedObjects")
	if err != nil {
		if strings.Contains(stderr, "not found") || strings.Contains(stderr, "not provided") || strings.Contains(stderr, "ServiceUnknown") || strings.Contains(stderr, "Connection refused") || strings.Contains(stderr, "No such file") {
			return nil, errf(CodeIwdUnavailable, "the wireless service (iwd) is not running")
		}
		return nil, errf(CodeIwdUnavailable, "cannot talk to iwd: %s", firstNonEmpty(stderr, err.Error()))
	}
	var reply managedReply
	if err := json.Unmarshal(out, &reply); err != nil || len(reply.Data) == 0 {
		return nil, errf(CodeInternal, "cannot parse the iwd object list")
	}
	t := &iwdTree{networks: map[string]object{}, known: map[string]object{}}
	var paths []string
	for p := range reply.Data[0] {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		obj := reply.Data[0][p]
		if dev, ok := obj["net.connman.iwd.Device"]; ok && t.device == "" {
			if mode := dev["Mode"].str(); mode == "" || mode == "station" {
				t.device, t.deviceObj = p, obj
				_, t.station = obj["net.connman.iwd.Station"]
			}
		}
		if _, ok := obj["net.connman.iwd.Network"]; ok {
			t.networks[p] = obj
		}
		if _, ok := obj["net.connman.iwd.KnownNetwork"]; ok {
			t.known[p] = obj
		}
	}
	return t, nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

// WifiState 는 무선 장치 상태를 읽는다. 장치가 없으면 Available 이 거짓이다(오류가 아니다).
func (m *Manager) wifiState(ctx context.Context) (WifiState, *iwdTree) {
	t, err := m.tree(ctx)
	if err != nil {
		var e *Error
		reason := CodeIwdUnavailable
		if errors.As(err, &e) {
			reason = e.Code
		}
		return WifiState{Available: false, Reason: reason}, nil
	}
	if t.device == "" {
		return WifiState{Available: false, Reason: CodeNoWifi}, t
	}
	dev := t.deviceObj["net.connman.iwd.Device"]
	st := t.deviceObj["net.connman.iwd.Station"]
	w := WifiState{
		Available: true,
		Device:    dev["Name"].str(),
		Powered:   dev["Powered"].boolean(),
		State:     st["State"].str(),
		Scanning:  st["Scanning"].boolean(),
	}
	if !t.station {
		w.State = "off"
	}
	if cn := st["ConnectedNetwork"].str(); cn != "" {
		if n, ok := t.networks[cn]; ok {
			w.ConnectedSSID = n["net.connman.iwd.Network"]["Name"].str()
		}
	}
	return w, t
}

// Status 는 인터페이스와 무선 상태를 모아 돌려준다.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	list, online, err := m.Interfaces(ctx)
	if err != nil {
		return Status{}, err
	}
	w, _ := m.wifiState(ctx)
	if list == nil {
		list = []Interface{}
	}
	return Status{Interfaces: list, Wifi: w, Online: online}, nil
}

// signalPercent 는 100*dBm 값을 0~100 으로 바꾼다. -30 dBm 이상은 100, -90 dBm 이하는 0 이다.
func signalPercent(strength int) int {
	dbm := float64(strength) / 100.0
	p := (dbm + 90.0) / 60.0 * 100.0
	switch {
	case p < 0:
		return 0
	case p > 100:
		return 100
	}
	return int(p + 0.5)
}

// Scan 은 무선 검색을 시작한다. 결과는 잠시 뒤 Networks 로 읽는다.
func (m *Manager) Scan(ctx context.Context) error {
	w, t := m.wifiState(ctx)
	if !w.Available {
		return errf(w.Reason, "wireless is not available (%s)", w.Reason)
	}
	if !w.Powered || !t.station {
		return errf(CodeNoWifi, "the wireless device is turned off")
	}
	_, stderr, err := m.busctl(ctx, 15*time.Second, "call", iwdService, t.device, "net.connman.iwd.Station", "Scan")
	if err != nil {
		// 이미 검색 중이면 iwd 가 Busy 를 돌려준다. 검색 중이므로 성공으로 본다.
		if strings.Contains(stderr, "Busy") || strings.Contains(stderr, "InProgress") {
			return nil
		}
		return errf(CodeInternal, "scan failed: %s", firstNonEmpty(stderr, err.Error()))
	}
	return nil
}

// Networks 는 지금 보이는 무선 네트워크를 신호가 센 순서로 돌려준다.
func (m *Manager) Networks(ctx context.Context) ([]WifiNetwork, error) {
	w, t := m.wifiState(ctx)
	if !w.Available {
		return nil, errf(w.Reason, "wireless is not available (%s)", w.Reason)
	}
	if !t.station {
		return []WifiNetwork{}, nil
	}
	out, stderr, err := m.busctl(ctx, 15*time.Second, "--json=short", "call", iwdService, t.device, "net.connman.iwd.Station", "GetOrderedNetworks")
	if err != nil {
		return nil, errf(CodeInternal, "list failed: %s", firstNonEmpty(stderr, err.Error()))
	}
	var reply struct {
		Data [][][]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out, &reply); err != nil || len(reply.Data) == 0 {
		return nil, errf(CodeInternal, "cannot parse the network list")
	}
	nets := []WifiNetwork{}
	seen := map[string]bool{}
	for _, item := range reply.Data[0] {
		if len(item) != 2 {
			continue
		}
		var path string
		var strength int
		if json.Unmarshal(item[0], &path) != nil || json.Unmarshal(item[1], &strength) != nil {
			continue
		}
		obj, ok := t.networks[path]
		if !ok {
			continue
		}
		n := obj["net.connman.iwd.Network"]
		ssid := n["Name"].str()
		if ssid == "" || seen[ssid] {
			continue
		}
		seen[ssid] = true
		nets = append(nets, WifiNetwork{
			SSID: ssid, Security: n["Type"].str(), Signal: signalPercent(strength), SignalDBm: strength / 100,
			Connected: n["Connected"].boolean(), Known: n["KnownNetwork"].str() != "",
		})
	}
	return nets, nil
}

// ---- 연결 ----

// ValidSSID 는 SSID 가 1~32바이트이고 제어 문자가 없는지 본다.
func ValidSSID(ssid string) bool {
	if len(ssid) == 0 || len(ssid) > 32 {
		return false
	}
	for i := 0; i < len(ssid); i++ {
		if ssid[i] < 0x20 || ssid[i] == 0x7f {
			return false
		}
	}
	return true
}

// ValidPassphrase 는 WPA 비밀번호(8~63자의 출력 가능한 ASCII) 또는 64자리 16진수 PSK 인지 본다.
func ValidPassphrase(p string) bool {
	if len(p) == 64 {
		for i := 0; i < len(p); i++ {
			c := p[i]
			if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
				return false
			}
		}
		return true
	}
	if len(p) < 8 || len(p) > 63 {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x20 || p[i] > 0x7e {
			return false
		}
	}
	return true
}

// ConfigFileName 은 iwd 가 SSID 로 만드는 설정 파일 이름이다. 영숫자, 공백, '-', '_' 만 있으면 그대로 쓰고,
// 아니면 '=' 와 16진수로 쓴다(iwd 의 storage_network_ssid_to_file_name 과 같은 규칙).
func ConfigFileName(ssid, security string) string {
	plain := true
	for i := 0; i < len(ssid); i++ {
		c := ssid[i]
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == ' ' || c == '-' || c == '_') {
			plain = false
			break
		}
	}
	name := ssid
	if !plain {
		name = "=" + hex.EncodeToString([]byte(ssid))
	}
	return name + "." + security
}

func (m *Manager) findNetwork(t *iwdTree, ssid string) (string, object) {
	var paths []string
	for p, obj := range t.networks {
		if obj["net.connman.iwd.Network"]["Name"].str() == ssid {
			paths = append(paths, p)
		}
	}
	sort.Strings(paths)
	// 같은 SSID 가 여러 보안 방식으로 보이면 연결된 것, 알려진 것, 첫 번째 순서로 고른다.
	best := ""
	score := -1
	for _, p := range paths {
		n := t.networks[p]["net.connman.iwd.Network"]
		s := 0
		if n["Connected"].boolean() {
			s += 2
		}
		if n["KnownNetwork"].str() != "" {
			s++
		}
		if s > score {
			best, score = p, s
		}
	}
	if best == "" {
		return "", nil
	}
	return best, t.networks[best]
}

// Connect 는 SSID 에 연결한다. 보안 네트워크이고 아직 알려지지 않았으면 passphrase 를 저장한 뒤 연결한다.
func (m *Manager) Connect(ctx context.Context, ssid, passphrase string) error {
	if !ValidSSID(ssid) {
		return errf(CodeInvalidSSID, "the network name must be 1 to 32 characters without control characters")
	}
	w, t := m.wifiState(ctx)
	if !w.Available {
		return errf(w.Reason, "wireless is not available (%s)", w.Reason)
	}
	path, obj := m.findNetwork(t, ssid)
	if path == "" {
		return errf(CodeNotFound, "the network %q is not in range; scan again", ssid)
	}
	n := obj["net.connman.iwd.Network"]
	security := n["Type"].str()
	known := n["KnownNetwork"].str() != ""
	wrote := ""
	needsPassphrase := security == "psk" && !known
	switch {
	case security == "8021x":
		return errf(CodeConnectFailed, "enterprise (802.1X) networks are not supported here; use iwctl")
	case needsPassphrase || (security == "psk" && passphrase != ""):
		if !ValidPassphrase(passphrase) {
			return errf(CodeInvalidPass, "the password must be 8 to 63 characters, or 64 hexadecimal digits")
		}
		file, err := m.writePassphrase(ssid, passphrase)
		if err != nil {
			return err
		}
		if !known {
			wrote = file
		}
		// iwd 가 새 파일을 읽어 KnownNetwork 를 만들 때까지 기다린다.
		if !m.waitKnown(ctx, ssid) {
			_ = os.Remove(file)
			return errf(CodeConnectFailed, "the wireless service did not pick up the saved password")
		}
		// 객체 경로는 그대로이지만 KnownNetwork 속성이 생겼다. 다시 읽는다.
		if _, t2 := m.wifiState(ctx); t2 != nil {
			if p2, _ := m.findNetwork(t2, ssid); p2 != "" {
				path = p2
			}
		}
	case security == "wep":
		return errf(CodeConnectFailed, "WEP networks are not supported")
	}

	timeout := m.ConnectTimeout
	if timeout <= 0 {
		timeout = 45 * time.Second
	}
	_, stderr, err := m.busctl(context.WithoutCancel(ctx), timeout, "call", iwdService, path, "net.connman.iwd.Network", "Connect")
	if err != nil {
		if wrote != "" {
			_ = os.Remove(wrote)
		}
		return errf(CodeConnectFailed, "could not connect to %q: %s", ssid, firstNonEmpty(stderr, err.Error()))
	}
	return nil
}

// writePassphrase 는 iwd 의 설정 파일을 모드 0600 으로 쓴다. 쓴 파일 경로를 돌려준다.
func (m *Manager) writePassphrase(ssid, passphrase string) (string, error) {
	dir := m.iwdDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", errf(CodeInternal, "cannot prepare %s: %v", dir, err)
	}
	key := "Passphrase"
	if len(passphrase) == 64 {
		key = "PreSharedKey"
		passphrase = strings.ToLower(passphrase)
	}
	path := filepath.Join(dir, ConfigFileName(ssid, "psk"))
	body := "[Security]\n" + key + "=" + passphrase + "\n"
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o600); err != nil {
		return "", errf(CodeInternal, "cannot save the password: %v", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return "", errf(CodeInternal, "cannot save the password: %v", err)
	}
	return path, nil
}

func (m *Manager) waitKnown(ctx context.Context, ssid string) bool {
	wait := m.KnownWait
	if wait <= 0 {
		wait = 8 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if _, t := m.wifiState(ctx); t != nil {
			if _, obj := m.findNetwork(t, ssid); obj != nil && obj["net.connman.iwd.Network"]["KnownNetwork"].str() != "" {
				return true
			}
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// Disconnect 는 현재 연결을 끊는다.
func (m *Manager) Disconnect(ctx context.Context) error {
	w, t := m.wifiState(ctx)
	if !w.Available {
		return errf(w.Reason, "wireless is not available (%s)", w.Reason)
	}
	if w.ConnectedSSID == "" {
		return errf(CodeNotConnected, "the wireless device is not connected")
	}
	_, stderr, err := m.busctl(ctx, 15*time.Second, "call", iwdService, t.device, "net.connman.iwd.Station", "Disconnect")
	if err != nil {
		return errf(CodeInternal, "disconnect failed: %s", firstNonEmpty(stderr, err.Error()))
	}
	return nil
}

// Forget 은 저장된 네트워크(비밀번호)를 지운다.
func (m *Manager) Forget(ctx context.Context, ssid string) error {
	if !ValidSSID(ssid) {
		return errf(CodeInvalidSSID, "the network name is not valid")
	}
	w, t := m.wifiState(ctx)
	if !w.Available {
		return errf(w.Reason, "wireless is not available (%s)", w.Reason)
	}
	var paths []string
	for p, obj := range t.known {
		if obj["net.connman.iwd.KnownNetwork"]["Name"].str() == ssid {
			paths = append(paths, p)
		}
	}
	if len(paths) == 0 {
		return errf(CodeNotFound, "the network %q is not saved", ssid)
	}
	sort.Strings(paths)
	for _, p := range paths {
		if _, stderr, err := m.busctl(ctx, 15*time.Second, "call", iwdService, p, "net.connman.iwd.KnownNetwork", "Forget"); err != nil {
			return errf(CodeInternal, "forget failed: %s", firstNonEmpty(stderr, err.Error()))
		}
	}
	return nil
}

// SetPowered 는 무선 장치를 켜거나 끈다.
func (m *Manager) SetPowered(ctx context.Context, on bool) error {
	w, t := m.wifiState(ctx)
	if t == nil {
		return errf(w.Reason, "wireless is not available (%s)", w.Reason)
	}
	if t.device == "" {
		return errf(CodeNoWifi, "no wireless device was found")
	}
	val := "false"
	if on {
		val = "true"
	}
	_, stderr, err := m.busctl(ctx, 15*time.Second, "set-property", iwdService, t.device, "net.connman.iwd.Device", "Powered", "b", val)
	if err != nil {
		return errf(CodeInternal, "cannot change the wireless power: %s", firstNonEmpty(stderr, err.Error()))
	}
	return nil
}
