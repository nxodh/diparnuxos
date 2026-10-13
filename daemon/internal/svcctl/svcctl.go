// Package svcctl 은 허용 목록에 있는 서비스의 상태를 보여 주고 시작, 정지, 다시 시작, 부팅 때 시작 설정을 한다.
//
// 임의 유닛을 다루지 않는다. 허용 목록 밖의 이름과 보호된 동작은 거부한다. 로그는 허용 목록의 유닛만 읽고 줄 수를
// 제한한다. 모든 명령은 platform.Runner 로 실행하므로 실제 systemd 없이 시험할 수 있다.
package svcctl

import (
	"context"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// 오류 코드다.
const (
	CodeNotAllowed = "service_not_allowed"
	CodeProtected  = "action_not_allowed"
	CodeInvalid    = "invalid_input"
	CodeFailed     = "service_failed"
)

// Error 는 코드를 가진 오류다.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Service 는 허용 목록의 서비스 하나다.
type Service struct {
	// Unit 은 유닛 이름이다(.service 포함).
	Unit string
	// Title 은 화면에 보일 짧은 설명이다.
	Title string
	// Actions 는 허용하는 동작이다. start, stop, restart, enable, disable.
	Actions []string
}

var all = []string{"start", "stop", "restart", "enable", "disable"}

// Catalog 는 제어 센터와 CLI 에서 다룰 수 있는 서비스다. 목록에 없는 유닛은 보이지도 제어되지도 않는다.
var Catalog = []Service{
	{"diparnuxd.service", "Diparnux daemon", nil}, // 보기만 한다: 멈추면 이 API 가 사라진다
	{"diparnux-llama.service", "Model server (llama-server)", []string{"start", "stop", "restart"}},
	{"diparnux-firewall.service", "Saved firewall rules", nil},
	{"ssh.service", "SSH server", all},
	{"xrdp.service", "Remote desktop (RDP)", all},
	{"xrdp-sesman.service", "Remote desktop sessions", []string{"restart"}},
	{"lightdm.service", "Login screen and desktop", []string{"restart"}},
	{"systemd-networkd.service", "Network configuration", []string{"restart"}},
	{"systemd-resolved.service", "Name resolution (DNS, mDNS)", []string{"restart"}},
	{"iwd.service", "Wi-Fi (iwd)", []string{"restart", "start", "stop"}},
	{"systemd-timesyncd.service", "Clock synchronisation", []string{"restart"}},
}

func find(unit string) (Service, bool) {
	for _, s := range Catalog {
		if s.Unit == unit {
			return s, true
		}
	}
	return Service{}, false
}

// Status 는 서비스 하나의 현재 상태다.
type Status struct {
	Unit        string   `json:"unit"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Installed   bool     `json:"installed"`
	Active      string   `json:"active"`  // active, inactive, failed, activating, ...
	Sub         string   `json:"sub"`     // running, dead, exited, ...
	Enabled     string   `json:"enabled"` // enabled, disabled, static, masked, not-found
	PID         int      `json:"pid"`
	MemoryMiB   int64    `json:"memory_mib"`
	Actions     []string `json:"actions"`
}

// Manager 는 서비스를 읽고 제어한다.
type Manager struct {
	Runner platform.Runner
}

func (m *Manager) systemctl(ctx context.Context, timeout time.Duration, args ...string) (platform.Result, error) {
	return m.Runner.Run(ctx, platform.Command{Name: "systemctl", Args: args, Timeout: timeout, MaxOutput: 128 << 10})
}

// List 는 허용 목록의 모든 서비스 상태다. 한 번의 systemctl show 로 읽는다.
func (m *Manager) List(ctx context.Context) ([]Status, error) {
	args := []string{"show", "--property=Id,Description,LoadState,ActiveState,SubState,UnitFileState,MainPID,MemoryCurrent", "--"}
	for _, s := range Catalog {
		args = append(args, s.Unit)
	}
	res, err := m.systemctl(ctx, 15*time.Second, args...)
	if err != nil {
		return nil, &Error{CodeFailed, fmt.Sprintf("systemctl: %v", err)}
	}
	if res.ExitCode != 0 {
		return nil, &Error{CodeFailed, fmt.Sprintf("systemctl show exited %d", res.ExitCode)}
	}
	byUnit := map[string]Status{}
	for _, block := range strings.Split(strings.TrimSpace(string(res.Stdout)), "\n\n") {
		kv := map[string]string{}
		for _, line := range strings.Split(block, "\n") {
			if k, v, ok := strings.Cut(line, "="); ok {
				kv[k] = v
			}
		}
		if kv["Id"] == "" {
			continue
		}
		st := Status{Unit: kv["Id"], Description: kv["Description"], Active: kv["ActiveState"], Sub: kv["SubState"], Enabled: kv["UnitFileState"]}
		st.Installed = kv["LoadState"] != "not-found"
		if st.Enabled == "" {
			st.Enabled = "not-found"
		}
		fmt.Sscanf(kv["MainPID"], "%d", &st.PID)
		var mem uint64
		if _, err := fmt.Sscanf(kv["MemoryCurrent"], "%d", &mem); err == nil && mem < 1<<60 {
			st.MemoryMiB = int64(mem >> 20)
		}
		byUnit[st.Unit] = st
	}
	out := make([]Status, 0, len(Catalog))
	for _, s := range Catalog {
		st, ok := byUnit[s.Unit]
		if !ok {
			st = Status{Unit: s.Unit, Active: "inactive", Enabled: "not-found"}
		}
		st.Title = s.Title
		st.Actions = append([]string{}, s.Actions...)
		if !st.Installed {
			st.Actions = []string{}
		}
		out = append(out, st)
	}
	return out, nil
}

// Do 는 서비스에 동작을 한다.
func (m *Manager) Do(ctx context.Context, unit, action string) error {
	svc, ok := find(unit)
	if !ok {
		return &Error{CodeNotAllowed, "this service is not managed here"}
	}
	allowed := false
	for _, a := range svc.Actions {
		if a == action {
			allowed = true
		}
	}
	if !allowed {
		return &Error{CodeProtected, fmt.Sprintf("%s cannot be %sed from here", svc.Title, strings.TrimSuffix(action, "e"))}
	}
	var args []string
	switch action {
	case "enable":
		args = []string{"enable", "--now", "--", unit}
	case "disable":
		args = []string{"disable", "--now", "--", unit}
	default:
		args = []string{action, "--", unit}
	}
	res, err := m.systemctl(ctx, 90*time.Second, args...)
	if err != nil {
		return &Error{CodeFailed, fmt.Sprintf("systemctl: %v", err)}
	}
	if res.ExitCode != 0 {
		msg := strings.TrimSpace(string(res.Stderr))
		if len(msg) > 300 {
			msg = msg[:300]
		}
		return &Error{CodeFailed, fmt.Sprintf("systemctl %s exited %d: %s", action, res.ExitCode, msg)}
	}
	return nil
}

var unitRE = regexp.MustCompile(`^[a-zA-Z0-9@._:-]+\.service$`)

// Logs 는 서비스의 마지막 lines 줄이다. 허용 목록의 유닛만 읽는다.
func (m *Manager) Logs(ctx context.Context, unit string, lines int) (string, error) {
	if !unitRE.MatchString(unit) {
		return "", &Error{CodeInvalid, "the service name is not valid"}
	}
	if _, ok := find(unit); !ok {
		return "", &Error{CodeNotAllowed, "this service is not managed here"}
	}
	if lines == 0 {
		lines = 200
	}
	if lines < 1 || lines > 1000 {
		return "", &Error{CodeInvalid, "lines must be 1-1000"}
	}
	res, err := m.Runner.Run(ctx, platform.Command{
		Name: "journalctl", Args: []string{"-u", unit, "-n", fmt.Sprint(lines), "-o", "short-iso", "--no-pager"},
		Timeout: 20 * time.Second, MaxOutput: 2 << 20,
	})
	if err != nil {
		return "", &Error{CodeFailed, fmt.Sprintf("journalctl: %v", err)}
	}
	if res.ExitCode != 0 {
		return "", &Error{CodeFailed, fmt.Sprintf("journalctl exited %d", res.ExitCode)}
	}
	return string(res.Stdout), nil
}
