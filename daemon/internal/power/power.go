// Package power 는 "상시 가동" 상태를 확인하고 켜고 끈다.
//
// 상시 가동은 노트북을 서버로 쓸 때 덮개를 닫거나 오래 두어도 절전이나 종료로 서버가 멈추지 않게 하는 설정이다.
// 이 설정은 systemd 의 두 드롭인이 만든다. 패키지가 /usr/lib/systemd 에 두며(기본으로 켜져 있다), 끄려면 /etc 에서
// 같은 이름의 파일을 /dev/null 로 연결해 덮는다(systemd 의 표준 방법).
//
//   - logind.conf.d/50-diparnux-always-on.conf: 덮개, 절전 키, 최대 절전 키, 유휴 동작을 무시한다.
//     전원 버튼과 사용자가 직접 내리는 종료·재부팅 명령은 막지 않는다(사용자가 끄는 길은 항상 열려 있다).
//   - sleep.conf.d/50-diparnux-always-on.conf:  절전, 최대 절전, 하이브리드, 절전 후 최대 절전을 허용하지 않는다.
//
// 파일이 있다는 것만으로는 부족하다. 다른 설정이 값을 덮을 수 있으므로 Status 는 실행 중인 logind 에 속성과
// 가능 여부(CanSuspend 등)를 물어 실제 효과를 확인한다. 모든 명령은 platform.Runner 로 실행한다.
package power

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// 오류 코드다. API 계층이 HTTP 상태로 바꾼다.
const (
	CodeUnavailable = "power_unavailable"
	CodeApplyFailed = "power_apply_failed"
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

// dropinName 은 두 드롭인의 파일 이름이다. /usr/lib 의 기본 파일과 /etc 의 덮개 파일이 같은 이름이어야 한다.
const dropinName = "50-diparnux-always-on.conf"

const (
	logindDest  = "org.freedesktop.login1"
	logindPath  = "/org/freedesktop/login1"
	logindIface = "org.freedesktop.login1.Manager"
)

// logindWant 는 실행 중인 logind 의 속성이 가져야 할 값이다.
var logindWant = []string{
	"HandleLidSwitch",
	"HandleLidSwitchExternalPower",
	"HandleLidSwitchDocked",
	"HandleSuspendKey",
	"HandleHibernateKey",
	"IdleAction",
}

// canMethods 는 logind 에 가능 여부를 묻는 메서드다. 결과가 "no" 나 "na" 여야 한다.
var canMethods = []string{"CanSuspend", "CanHibernate", "CanHybridSleep", "CanSuspendThenHibernate"}

// Check 는 검사 하나의 결과다.
type Check struct {
	Name string `json:"name"`
	// Want 는 기대하는 값이다. "ignore" 또는 "no".
	Want string `json:"want"`
	// Got 은 logind 가 돌려준 값이다. 읽지 못하면 비어 있다.
	Got string `json:"got"`
	OK  bool   `json:"ok"`
}

// Status 는 상시 가동 상태다.
type Status struct {
	// AlwaysOn 은 모든 검사가 통과했는지다. 사용자에게 보이는 최종 판정이다.
	AlwaysOn bool `json:"always_on"`
	// Enabled 는 /etc 에서 드롭인을 끄지 않았는지다(설정 의도). AlwaysOn 과 다르면 다른 설정이 값을 덮고 있는 것이다.
	Enabled bool    `json:"enabled"`
	Checks  []Check `json:"checks"`
	// Lid 는 덮개 상태다. open, closed, unknown.
	Lid string `json:"lid"`
	// HasBattery 는 배터리가 있는지다. OnBattery 는 전원이 배터리인지다(AC 가 없을 때).
	HasBattery     bool `json:"has_battery"`
	OnBattery      bool `json:"on_battery"`
	BatteryPercent int  `json:"battery_percent"` // 모르면 -1
	// UptimeSeconds 는 부팅한 뒤 지난 시간이다.
	UptimeSeconds int64 `json:"uptime_seconds"`
	// Notes 는 사람이 읽는 안내다. 검사에 실패하면 이유를, 배터리로 돌 때는 한계를 적는다.
	Notes []string `json:"notes"`
}

// Manager 는 상시 가동을 확인하고 바꾼다.
type Manager struct {
	Runner platform.Runner
	// BusctlArgs 는 busctl 앞에 붙는 인자다(시험이 사설 버스 주소를 줄 때 쓴다).
	BusctlArgs []string
	// SystemdEtc 는 /etc/systemd 다. 시험이 바꿔 쓴다.
	SystemdEtc string
	// PowerSupply 는 /sys/class/power_supply 다.
	PowerSupply string
	// LidGlob 은 덮개 상태 파일의 글롭이다. 비어 있으면 /proc/acpi/button/lid/*/state 다.
	LidGlob string
	// ProcUptime 은 /proc/uptime 이다.
	ProcUptime string
}

func (m *Manager) etc() string {
	if m.SystemdEtc != "" {
		return m.SystemdEtc
	}
	return "/etc/systemd"
}

func (m *Manager) overridePaths() []string {
	return []string{
		filepath.Join(m.etc(), "logind.conf.d", dropinName),
		filepath.Join(m.etc(), "sleep.conf.d", dropinName),
	}
}

func (m *Manager) run(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	res, err := m.Runner.Run(ctx, platform.Command{Name: name, Args: args, Timeout: timeout, MaxOutput: 1 << 16})
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return res.Stdout, fmt.Errorf("%s exited %d: %s", name, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return res.Stdout, nil
}

// busctlString 은 busctl --json=short 의 출력에서 문자열 값 하나를 꺼낸다.
// 속성은 {"type":"s","data":"ignore"} 이고 메서드 호출은 {"type":"s","data":["no"]} 다.
func busctlString(out []byte) (string, bool) {
	var v struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(out, &v); err != nil || len(v.Data) == 0 {
		return "", false
	}
	var s string
	if err := json.Unmarshal(v.Data, &s); err == nil {
		return s, true
	}
	var list []string
	if err := json.Unmarshal(v.Data, &list); err == nil && len(list) > 0 {
		return list[0], true
	}
	return "", false
}

func (m *Manager) busctl(ctx context.Context, args ...string) (string, bool) {
	full := append(append([]string{}, m.BusctlArgs...), "--json=short")
	full = append(full, args...)
	out, err := m.run(ctx, 5*time.Second, "busctl", full...)
	if err != nil {
		return "", false
	}
	return busctlString(out)
}

// enabled 는 두 덮개 파일이 어느 것도 드롭인을 무효화하지 않는지 본다.
func (m *Manager) enabled() bool {
	for _, p := range m.overridePaths() {
		if _, err := os.Lstat(p); err == nil {
			return false
		}
	}
	return true
}

// Status 는 실행 중인 logind 에 물어 상시 가동의 실제 효과를 확인한다.
func (m *Manager) Status(ctx context.Context) (Status, error) {
	if m.Runner == nil {
		return Status{}, errf(CodeUnavailable, "no command runner is configured")
	}
	st := Status{Enabled: m.enabled(), BatteryPercent: -1, Lid: "unknown"}
	allOK := true
	known := 0

	for _, name := range logindWant {
		got, ok := m.busctl(ctx, "get-property", logindDest, logindPath, logindIface, name)
		c := Check{Name: name, Want: "ignore", Got: got, OK: ok && got == "ignore"}
		if ok {
			known++
		}
		allOK = allOK && c.OK
		st.Checks = append(st.Checks, c)
	}
	for _, name := range canMethods {
		got, ok := m.busctl(ctx, "call", logindDest, logindPath, logindIface, name)
		c := Check{Name: name, Want: "no", Got: got, OK: ok && (got == "no" || got == "na")}
		if ok {
			known++
		}
		allOK = allOK && c.OK
		st.Checks = append(st.Checks, c)
	}
	if known == 0 {
		return st, errf(CodeUnavailable, "systemd-logind could not be queried")
	}
	st.AlwaysOn = allOK

	m.readPowerSupply(&st)
	st.Lid = m.lidState()
	st.UptimeSeconds = m.uptime()

	if !st.AlwaysOn {
		if st.Enabled {
			st.Notes = append(st.Notes, "The always-on configuration is installed but another setting overrides it: see the failed checks.")
		} else {
			st.Notes = append(st.Notes, "Always-on is turned off: closing the lid or an idle timeout can suspend this computer.")
		}
	}
	if st.HasBattery && st.OnBattery {
		st.Notes = append(st.Notes, "Running on battery: software cannot keep the computer on when the battery runs out. Connect the charger.")
	}
	return st, nil
}

// Set 은 상시 가동을 켜거나 끈다. 켜면 /etc 의 덮개 파일을 지우고, 끄면 /dev/null 로 연결한다.
// logind 는 설정을 SIGHUP 으로 다시 읽는다. sleep.conf 는 쓸 때마다 읽으므로 따로 알릴 필요가 없다.
func (m *Manager) Set(ctx context.Context, on bool) (Status, error) {
	if m.Runner == nil {
		return Status{}, errf(CodeUnavailable, "no command runner is configured")
	}
	for _, p := range m.overridePaths() {
		if on {
			if err := os.Remove(p); err != nil && !os.IsNotExist(err) {
				return Status{}, errf(CodeApplyFailed, "remove %s: %v", p, err)
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			return Status{}, errf(CodeApplyFailed, "create %s: %v", filepath.Dir(p), err)
		}
		_ = os.Remove(p)
		if err := os.Symlink("/dev/null", p); err != nil {
			return Status{}, errf(CodeApplyFailed, "mask %s: %v", p, err)
		}
	}
	if _, err := m.run(ctx, 10*time.Second, "systemctl", "kill", "--kill-whom=main", "--signal=HUP", "systemd-logind.service"); err != nil {
		return Status{}, errf(CodeApplyFailed, "reload systemd-logind: %v", err)
	}
	// logind 가 파일을 다시 읽을 시간을 준다.
	select {
	case <-time.After(500 * time.Millisecond):
	case <-ctx.Done():
		return Status{}, ctx.Err()
	}
	return m.Status(ctx)
}

// readPowerSupply 는 배터리 유무, AC 연결, 잔량을 읽는다.
func (m *Manager) readPowerSupply(st *Status) {
	dir := m.PowerSupply
	if dir == "" {
		dir = "/sys/class/power_supply"
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	acKnown, acOnline := false, false
	for _, e := range entries {
		base := filepath.Join(dir, e.Name())
		kind := strings.TrimSpace(readFile(filepath.Join(base, "type")))
		switch kind {
		case "Battery":
			st.HasBattery = true
			if v, err := strconv.Atoi(strings.TrimSpace(readFile(filepath.Join(base, "capacity")))); err == nil {
				st.BatteryPercent = v
			}
		case "Mains", "USB":
			acKnown = true
			if strings.TrimSpace(readFile(filepath.Join(base, "online"))) == "1" {
				acOnline = true
			}
		}
	}
	st.OnBattery = st.HasBattery && acKnown && !acOnline
}

func (m *Manager) lidState() string {
	glob := m.LidGlob
	if glob == "" {
		glob = "/proc/acpi/button/lid/*/state"
	}
	matches, _ := filepath.Glob(glob)
	for _, p := range matches {
		s := strings.ToLower(readFile(p))
		switch {
		case strings.Contains(s, "closed"):
			return "closed"
		case strings.Contains(s, "open"):
			return "open"
		}
	}
	return "unknown"
}

func (m *Manager) uptime() int64 {
	p := m.ProcUptime
	if p == "" {
		p = "/proc/uptime"
	}
	f := strings.Fields(readFile(p))
	if len(f) == 0 {
		return 0
	}
	v, err := strconv.ParseFloat(f[0], 64)
	if err != nil {
		return 0
	}
	return int64(v)
}

func readFile(p string) string {
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(b)
}
