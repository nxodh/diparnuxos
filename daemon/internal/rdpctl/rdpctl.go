// Package rdpctl 는 원격 데스크톱(xrdp, RDP)을 켜고 끈다. 기본은 꺼져 있다.
//
// 켜면 xrdp 서비스를 시작하고 부팅 때 시작하게 하며, 방화벽에서 TCP 3389 를 정한 범위(기본 LAN)에만 연다.
// 설정(켜짐, 허용 범위)은 /var/lib/diparnux/remote-desktop.json 에 저장하고 방화벽은 이 값에서 규칙을 만든다.
package rdpctl

import (
	"context"
	"encoding/json"
	"fmt"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// 상수다.
const (
	Port         = 3389
	unit         = "xrdp.service"
	sesmanUnit   = "xrdp-sesman.service"
	SourceLAN    = "lan"
	SourceAny    = "any"
	DefaultState = "/var/lib/diparnux/remote-desktop.json"
)

// 오류 코드다.
const (
	CodeNotInstalled = "xrdp_not_installed"
	CodeInvalidInput = "invalid_input"
	CodeFailed       = "remote_desktop_failed"
)

// Error 는 코드를 가진 오류다.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// State 는 저장된 설정이다.
type State struct {
	Enabled bool   `json:"enabled"`
	Source  string `json:"source"` // lan, any, CIDR
}

// Status 는 API 가 보여 주는 상태다.
type Status struct {
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	Port      int    `json:"port"`
	Source    string `json:"source"`
	Note      string `json:"note,omitempty"`
}

// Manager 는 설정을 읽고 서비스를 제어한다.
type Manager struct {
	Runner    platform.Runner
	StatePath string
	// Reapply 는 방화벽을 다시 적용한다.
	Reapply func(ctx context.Context) error

	mu sync.Mutex
}

func (m *Manager) path() string {
	if m.StatePath != "" {
		return m.StatePath
	}
	return DefaultState
}

func (m *Manager) load() State {
	s := State{Source: SourceLAN}
	data, err := os.ReadFile(m.path())
	if err != nil {
		return s
	}
	var f State
	if json.Unmarshal(data, &f) == nil && validSource(f.Source) {
		return f
	}
	return s
}

func (m *Manager) save(s State) error {
	data, _ := json.MarshalIndent(s, "", "  ")
	if err := os.MkdirAll(filepath.Dir(m.path()), 0o750); err != nil {
		return err
	}
	tmp := m.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path())
}

func validSource(s string) bool {
	if s == SourceLAN || s == SourceAny {
		return true
	}
	p, err := netip.ParsePrefix(s)
	return err == nil && p.Bits() > 0
}

func (m *Manager) systemctl(ctx context.Context, args ...string) (platform.Result, error) {
	return m.Runner.Run(ctx, platform.Command{Name: "systemctl", Args: args, Timeout: 60 * time.Second, MaxOutput: 16 << 10})
}

func (m *Manager) installed(ctx context.Context) bool {
	res, err := m.systemctl(ctx, "show", "--property=LoadState", "--value", unit)
	return err == nil && res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) == "loaded"
}

// Status 는 지금 상태다.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.load()
	st := Status{Port: Port, Source: s.Source, Enabled: s.Enabled}
	st.Installed = m.installed(ctx)
	if !st.Installed {
		st.Note = "xrdp is not installed in this image"
		return st
	}
	res, err := m.systemctl(ctx, "is-active", unit)
	st.Active = err == nil && strings.TrimSpace(string(res.Stdout)) == "active"
	return st
}

// Opens 는 켜져 있을 때 방화벽이 열 포트다.
func (m *Manager) Opens() (port int, source string, ok bool) {
	s := m.load()
	if !s.Enabled {
		return 0, "", false
	}
	return Port, s.Source, true
}

// Set 은 켜거나 끄고 허용 범위를 바꾼다. source 가 비어 있으면 그대로 둔다.
func (m *Manager) Set(ctx context.Context, enabled bool, source string) (Status, error) {
	m.mu.Lock()
	s := m.load()
	prev := s
	if source != "" {
		if !validSource(source) {
			m.mu.Unlock()
			return Status{}, &Error{CodeInvalidInput, "the source must be lan, any, or an address range such as 192.168.1.0/24"}
		}
		s.Source = source
	}
	s.Enabled = enabled
	if enabled && !m.installed(ctx) {
		m.mu.Unlock()
		return Status{}, &Error{CodeNotInstalled, "xrdp is not installed in this image"}
	}
	// 서비스를 먼저 바꾼다. 실패하면 설정도 방화벽도 바꾸지 않는다.
	var args []string
	if enabled {
		args = []string{"enable", "--now", unit, sesmanUnit}
	} else {
		args = []string{"disable", "--now", unit, sesmanUnit}
	}
	res, err := m.systemctl(ctx, args...)
	if err != nil || (res.ExitCode != 0 && enabled) {
		m.mu.Unlock()
		return Status{}, &Error{CodeFailed, "systemctl could not change the xrdp service"}
	}
	if err := m.save(s); err != nil {
		m.mu.Unlock()
		return Status{}, &Error{CodeFailed, fmt.Sprintf("saving the setting: %v", err)}
	}
	m.mu.Unlock()
	if m.Reapply != nil {
		if err := m.Reapply(ctx); err != nil {
			m.mu.Lock()
			_ = m.save(prev)
			m.mu.Unlock()
			return Status{}, &Error{CodeFailed, fmt.Sprintf("the firewall was not updated: %v", err)}
		}
	}
	return m.Status(ctx), nil
}
