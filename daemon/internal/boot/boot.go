// Package boot 는 UEFI 부팅 항목을 읽고 바꾼다(efibootmgr).
//
// 일부 펌웨어(예: MSI Stealth 15M)는 재부팅 때마다 BootOrder 를 자체 우선순위로 다시 쓰고 모르는 항목을 뒤에 붙인다.
// 이 패키지는 (1) 상태를 해석해 문제를 분류하고, (2) 다음 한 번만 부팅할 항목(BootNext)을 지정하고, (3) 순서를 바꾸고,
// (4) 항목과 파일을 다시 만들고, (5) 이전 이름의 흔적을 정리한다. 모든 외부 명령은 platform.Runner 로 실행한다.
package boot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// 기본 경로다.
const (
	DefaultEFIDir   = "/sys/firmware/efi"
	DefaultESPDir   = "/boot/efi"
	DefaultStateDir = "/var/lib/opereta/boot"
	// AutoUnit 은 종료 때 BootNext 를 Opereta 로 지정하는 유닛이다(O2b).
	AutoUnit = "opereta-bootnext.service"
	// BootloaderID 는 NVRAM 항목 이름과 ESP 폴더 이름이다.
	BootloaderID = "opereta"
)

// 문제 코드다. 제어 센터와 CLI 가 코드별 안내를 붙인다.
const (
	ProblemNotUEFI          = "not_uefi"
	ProblemNoEntry          = "no_entry"
	ProblemNotFirst         = "opereta_not_first"
	ProblemFirmwareReorders = "firmware_reorders"
	ProblemLegacyEntries    = "legacy_entries"
	ProblemESPNotMounted    = "esp_not_mounted"
	ProblemNoFallback       = "no_fallback"
)

// 오류 코드다.
const (
	CodeNotUEFI    = "not_uefi"
	CodeNoEntry    = "no_such_entry"
	CodeBadTarget  = "invalid_target"
	CodeCommand    = "command_failed"
	CodeNoTool     = "efibootmgr_missing"
	CodeNoESP      = "esp_not_mounted"
	CodeInternal   = "internal"
	legacyReason   = "legacy"
	kindOpereta    = "opereta"
	kindWindows    = "windows"
	kindRemovable  = "removable"
	kindOther      = "other"
	kindLegacy     = legacyReason
	espOperetaDir  = "EFI/opereta"
	espFallbackEFI = "EFI/BOOT/BOOTX64.EFI"
)

// Error 는 코드를 가진 오류다.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, a ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, a...)}
}

// Entry 는 NVRAM 의 부팅 항목 하나다.
type Entry struct {
	Num    string `json:"num"`
	Label  string `json:"label"`
	Active bool   `json:"active"`
	// Kind 는 opereta, windows, removable(USB 등), legacy(이전 이름), other 다.
	Kind   string `json:"kind"`
	Loader string `json:"loader,omitempty"`
}

// Problem 은 상태에서 찾은 문제 하나다.
type Problem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// ESP 는 EFI 시스템 파티션의 상태다.
type ESP struct {
	Mounted       bool     `json:"mounted"`
	OperetaFiles  bool     `json:"opereta_files"`
	Fallback      bool     `json:"fallback"`
	LegacyFolders []string `json:"legacy_folders,omitempty"`
}

// Info 는 부팅 상태 전체다. GET /v1/update 의 boot 필드다.
type Info struct {
	// Available 이 거짓이면 efibootmgr 가 없거나 실행하지 못했다.
	Available bool `json:"available"`
	UEFI      bool `json:"uefi"`
	// SecureBoot 는 on, off, unknown 이다.
	SecureBoot string   `json:"secure_boot"`
	Current    string   `json:"current,omitempty"`
	Next       string   `json:"next,omitempty"`
	Order      []string `json:"order,omitempty"`
	Entries    []Entry  `json:"entries,omitempty"`
	// OperetaEntry 와 WindowsEntry 는 해당 항목의 번호다. 없으면 비어 있다.
	OperetaEntry string `json:"opereta_entry,omitempty"`
	WindowsEntry string `json:"windows_entry,omitempty"`
	// Auto 는 종료 때 다음 부팅을 Opereta 로 지정하는 기능(O2b)이 켜져 있다는 뜻이다.
	Auto      bool      `json:"auto"`
	ESP       ESP       `json:"esp"`
	BitLocker bool      `json:"bitlocker"`
	Problems  []Problem `json:"problems,omitempty"`
	// LastAction 은 마지막으로 이 도구가 한 동작과 시각이다.
	LastAction string `json:"last_action,omitempty"`
	LastTime   string `json:"last_time,omitempty"`
}

var (
	entryRE  = regexp.MustCompile(`^Boot([0-9A-Fa-f]{4})(\*?)\s+(.*)$`)
	hexNumRE = regexp.MustCompile(`^[0-9A-Fa-f]{4}$`)
	// 이전 이름 둘. 저장소의 이름 흔적 검사에 걸리지 않게 조각으로 나눠 만든다.
	legacyRE  = regexp.MustCompile(`(?i)` + "dipar" + "nux" + `|` + "light" + "lix")
	loaderRE  = regexp.MustCompile(`File\(([^)]*)\)`)
	pathStart = regexp.MustCompile(`(HD\(|PciRoot\(|Pci\(|File\(|BBS\(|VenHw\(|USB\()`)
)

// Parse 는 `efibootmgr -v` 의 출력을 해석한다. UEFI 여부와 ESP 상태는 채우지 않는다.
func Parse(out string) Info {
	var in Info
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		switch {
		case strings.HasPrefix(line, "BootCurrent:"):
			in.Current = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(line, "BootCurrent:")))
		case strings.HasPrefix(line, "BootNext:"):
			in.Next = strings.ToUpper(strings.TrimSpace(strings.TrimPrefix(line, "BootNext:")))
		case strings.HasPrefix(line, "BootOrder:"):
			v := strings.TrimSpace(strings.TrimPrefix(line, "BootOrder:"))
			for _, f := range strings.FieldsFunc(v, func(r rune) bool { return r == ',' || r == ' ' }) {
				if hexNumRE.MatchString(f) {
					in.Order = append(in.Order, strings.ToUpper(f))
				}
			}
		default:
			m := entryRE.FindStringSubmatch(line)
			if m == nil {
				continue
			}
			label, path := splitLabel(m[3])
			e := Entry{Num: strings.ToUpper(m[1]), Label: label, Active: m[2] == "*"}
			if lm := loaderRE.FindStringSubmatch(path); lm != nil {
				e.Loader = lm[1]
			}
			e.Kind = classify(e.Label, path)
			in.Entries = append(in.Entries, e)
		}
	}
	for _, e := range in.Entries {
		switch e.Kind {
		case kindOpereta:
			if in.OperetaEntry == "" {
				in.OperetaEntry = e.Num
			}
		case kindWindows:
			if in.WindowsEntry == "" {
				in.WindowsEntry = e.Num
			}
		}
	}
	return in
}

// splitLabel 은 "라벨<탭>경로" 또는 탭이 없는 줄을 라벨과 장치 경로로 나눈다.
func splitLabel(s string) (label, path string) {
	if i := strings.Index(s, "\t"); i >= 0 {
		return strings.TrimSpace(s[:i]), s[i+1:]
	}
	if loc := pathStart.FindStringIndex(s); loc != nil {
		return strings.TrimSpace(s[:loc[0]]), s[loc[0]:]
	}
	return strings.TrimSpace(s), ""
}

func classify(label, path string) string {
	l := strings.ToLower(label)
	p := strings.ToUpper(path)
	switch {
	case legacyRE.MatchString(label) || legacyRE.MatchString(path):
		return kindLegacy
	case strings.Contains(l, BootloaderID) || strings.Contains(p, `\EFI\OPERETA\`):
		return kindOpereta
	case strings.Contains(l, "windows boot manager") || strings.Contains(p, `\EFI\MICROSOFT\BOOT\BOOTMGFW.EFI`):
		return kindWindows
	case strings.HasPrefix(l, "uefi:") || strings.Contains(p, "USB(") || strings.Contains(p, "BBS("):
		return kindRemovable
	}
	return kindOther
}

// Controller 는 부팅 상태를 읽고 바꾼다.
type Controller struct {
	Runner platform.Runner
	// EFIDir 은 펌웨어 디렉터리다(UEFI 로 부팅했으면 있다). 비어 있으면 DefaultEFIDir.
	EFIDir string
	// ESPDir 은 EFI 시스템 파티션 마운트 위치다. 비어 있으면 DefaultESPDir.
	ESPDir string
	// StateDir 은 마지막 동작 기록 디렉터리다. 비어 있으면 DefaultStateDir.
	StateDir string
	// Now 는 시험이 바꾼다.
	Now func() time.Time
}

func (c *Controller) efiDir() string {
	if c.EFIDir != "" {
		return c.EFIDir
	}
	return DefaultEFIDir
}

func (c *Controller) espDir() string {
	if c.ESPDir != "" {
		return c.ESPDir
	}
	return DefaultESPDir
}

func (c *Controller) stateDir() string {
	if c.StateDir != "" {
		return c.StateDir
	}
	return DefaultStateDir
}

func (c *Controller) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}
	return time.Now()
}

type state struct {
	// ExpectedOrder 는 이 도구가 마지막으로 정한 BootOrder 다. 다음에 읽은 값이 다르면 펌웨어가 되돌린 것이다.
	ExpectedOrder []string `json:"expected_order,omitempty"`
	LastAction    string   `json:"last_action,omitempty"`
	LastTime      string   `json:"last_time,omitempty"`
}

func (c *Controller) loadState() state {
	var s state
	if b, err := os.ReadFile(filepath.Join(c.stateDir(), "state.json")); err == nil {
		_ = json.Unmarshal(b, &s)
	}
	return s
}

func (c *Controller) saveState(s state) {
	if err := os.MkdirAll(c.stateDir(), 0o755); err != nil {
		return
	}
	b, _ := json.MarshalIndent(s, "", "  ")
	tmp := filepath.Join(c.stateDir(), "state.json.tmp")
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		_ = os.Rename(tmp, filepath.Join(c.stateDir(), "state.json"))
	}
}

func (c *Controller) run(ctx context.Context, timeout time.Duration, name string, args ...string) (platform.Result, error) {
	return c.Runner.Run(ctx, platform.Command{Name: name, Args: args, Timeout: timeout, MaxOutput: 256 << 10})
}

func (c *Controller) uefi() bool {
	fi, err := os.Stat(c.efiDir())
	return err == nil && fi.IsDir()
}

// secureBoot 는 SecureBoot EFI 변수의 마지막 바이트를 읽는다.
func (c *Controller) secureBoot() string {
	b, err := os.ReadFile(filepath.Join(c.efiDir(), "efivars", "SecureBoot-8be4df61-93ca-11d2-aa0d-00e098032b8c"))
	if err != nil || len(b) < 5 {
		return "unknown"
	}
	if b[len(b)-1] == 1 {
		return "on"
	}
	return "off"
}

func (c *Controller) espState() ESP {
	var e ESP
	root := c.espDir()
	if _, err := os.Stat(filepath.Join(root, "EFI")); err != nil {
		return e
	}
	e.Mounted = true
	if fi, err := os.Stat(filepath.Join(root, espOperetaDir)); err == nil && fi.IsDir() {
		e.OperetaFiles = true
	}
	if fi, err := os.Stat(filepath.Join(root, espFallbackEFI)); err == nil && fi.Mode().IsRegular() {
		e.Fallback = true
	}
	if ents, err := os.ReadDir(filepath.Join(root, "EFI")); err == nil {
		for _, d := range ents {
			if d.IsDir() && legacyRE.MatchString(d.Name()) {
				e.LegacyFolders = append(e.LegacyFolders, d.Name())
			}
		}
		sort.Strings(e.LegacyFolders)
	}
	return e
}

// autoEnabled 는 O2b 유닛이 켜져 있는지 본다.
func (c *Controller) autoEnabled(ctx context.Context) bool {
	res, err := c.run(ctx, 10*time.Second, "systemctl", "is-enabled", "--quiet", AutoUnit)
	return err == nil && res.ExitCode == 0
}

// bitlocker 는 BitLocker 로 암호화된 파티션이 있는지 본다(부팅 구성을 바꾸기 전에 복구 키를 안내하기 위해).
func (c *Controller) bitlocker(ctx context.Context) bool {
	res, err := c.run(ctx, 10*time.Second, "lsblk", "-rno", "FSTYPE")
	if err != nil || res.ExitCode != 0 {
		return false
	}
	return strings.Contains(strings.ToLower(string(res.Stdout)), "bitlocker")
}

// Status 는 부팅 상태를 읽고 문제를 분류한다.
func (c *Controller) Status(ctx context.Context) Info {
	in := Info{SecureBoot: "unknown"}
	in.UEFI = c.uefi()
	if !in.UEFI {
		in.Problems = append(in.Problems, Problem{ProblemNotUEFI, "This system was not started in UEFI mode, so boot entries cannot be read or changed."})
		return in
	}
	in.SecureBoot = c.secureBoot()
	res, err := c.run(ctx, 15*time.Second, "efibootmgr", "-v")
	if err != nil || res.ExitCode != 0 {
		return in
	}
	p := Parse(string(res.Stdout))
	p.Available, p.UEFI, p.SecureBoot = true, true, in.SecureBoot
	p.ESP = c.espState()
	p.Auto = c.autoEnabled(ctx)
	p.BitLocker = c.bitlocker(ctx)
	st := c.loadState()
	p.LastAction, p.LastTime = st.LastAction, st.LastTime
	p.Problems = diagnose(p, st)
	return p
}

// diagnose 는 해석한 상태에서 문제를 찾는다.
func diagnose(in Info, st state) []Problem {
	var out []Problem
	if in.OperetaEntry == "" {
		out = append(out, Problem{ProblemNoEntry, "There is no boot entry named opereta in the firmware."})
	} else if len(in.Order) > 0 && in.Order[0] != in.OperetaEntry && in.Current != in.OperetaEntry {
		out = append(out, Problem{ProblemNotFirst, "The firmware starts another entry before Opereta."})
	}
	if in.OperetaEntry != "" && len(st.ExpectedOrder) > 0 && !sameOrder(st.ExpectedOrder, in.Order) {
		out = append(out, Problem{ProblemFirmwareReorders, "The firmware changed the boot order after it was set. Use the one-time boot choice instead."})
	}
	if !in.ESP.Mounted {
		out = append(out, Problem{ProblemESPNotMounted, "The EFI system partition is not mounted."})
	} else if !in.ESP.Fallback {
		out = append(out, Problem{ProblemNoFallback, "The fallback boot file EFI/BOOT/BOOTX64.EFI is missing."})
	}
	legacy := len(in.ESP.LegacyFolders) > 0
	for _, e := range in.Entries {
		legacy = legacy || e.Kind == kindLegacy
	}
	if legacy {
		out = append(out, Problem{ProblemLegacyEntries, "Folders or entries from an earlier name of this system are still present."})
	}
	return out
}

func sameOrder(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// resolve 는 opereta, windows, usb 또는 4자리 16진수 번호를 항목 번호로 바꾼다.
func resolve(in Info, target string) (string, error) {
	t := strings.ToLower(strings.TrimSpace(target))
	switch t {
	case "opereta":
		if in.OperetaEntry != "" {
			return in.OperetaEntry, nil
		}
		return "", errf(CodeNoEntry, "there is no opereta boot entry; run 'opereta boot repair'")
	case "windows":
		if in.WindowsEntry != "" {
			return in.WindowsEntry, nil
		}
		return "", errf(CodeNoEntry, "there is no Windows Boot Manager entry")
	case "usb":
		for _, e := range in.Entries {
			if e.Kind == kindRemovable {
				return e.Num, nil
			}
		}
		return "", errf(CodeNoEntry, "there is no removable-media boot entry")
	}
	if hexNumRE.MatchString(t) {
		n := strings.ToUpper(t)
		for _, e := range in.Entries {
			if e.Num == n {
				return n, nil
			}
		}
		return "", errf(CodeNoEntry, "there is no boot entry %s", n)
	}
	return "", errf(CodeBadTarget, "the target must be opereta, windows, usb or a four-digit entry number, not %q", target)
}

// ValidTarget 는 대상 문자열이 허용된 형식인지 본다(API 와 CLI 가 부른다).
func ValidTarget(t string) bool {
	switch strings.ToLower(t) {
	case "opereta", "windows", "usb":
		return true
	}
	return hexNumRE.MatchString(t)
}

func (c *Controller) efibootmgr(ctx context.Context, args ...string) error {
	res, err := c.run(ctx, 30*time.Second, "efibootmgr", args...)
	if err != nil {
		var pe *os.PathError
		if errors.As(err, &pe) {
			return errf(CodeNoTool, "efibootmgr is not installed")
		}
		return errf(CodeCommand, "efibootmgr: %v", err)
	}
	if res.ExitCode != 0 {
		return errf(CodeCommand, "efibootmgr exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return nil
}

func (c *Controller) read(ctx context.Context) (Info, error) {
	if !c.uefi() {
		return Info{}, errf(CodeNotUEFI, "this system was not started in UEFI mode")
	}
	res, err := c.run(ctx, 15*time.Second, "efibootmgr", "-v")
	if err != nil {
		return Info{}, errf(CodeNoTool, "efibootmgr is not available: %v", err)
	}
	if res.ExitCode != 0 {
		return Info{}, errf(CodeCommand, "efibootmgr -v exited %d: %s", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	return Parse(string(res.Stdout)), nil
}

func (c *Controller) record(action string, order []string) {
	s := c.loadState()
	s.LastAction = action
	s.LastTime = c.now().UTC().Format(time.RFC3339)
	if order != nil {
		s.ExpectedOrder = order
	}
	c.saveState(s)
}

// Next 는 다음 한 번만 부팅할 항목(BootNext)을 지정한다. 영구 순서는 바꾸지 않는다.
func (c *Controller) Next(ctx context.Context, target string) error {
	in, err := c.read(ctx)
	if err != nil {
		return err
	}
	num, err := resolve(in, target)
	if err != nil {
		return err
	}
	if err := c.efibootmgr(ctx, "-n", num); err != nil {
		return err
	}
	c.record("next "+strings.ToLower(target), nil)
	return nil
}

// ClearNext 는 BootNext 를 지운다.
func (c *Controller) ClearNext(ctx context.Context) error {
	if !c.uefi() {
		return errf(CodeNotUEFI, "this system was not started in UEFI mode")
	}
	if err := c.efibootmgr(ctx, "-N"); err != nil {
		return err
	}
	c.record("clear-next", nil)
	return nil
}

// Order 는 target 을 BootOrder 의 맨 앞에 둔다. 나머지 순서는 유지한다. 펌웨어가 되돌릴 수 있다.
func (c *Controller) Order(ctx context.Context, target string) error {
	in, err := c.read(ctx)
	if err != nil {
		return err
	}
	num, err := resolve(in, target)
	if err != nil {
		return err
	}
	order := []string{num}
	for _, n := range in.Order {
		if n != num {
			order = append(order, n)
		}
	}
	if err := c.efibootmgr(ctx, "-o", strings.Join(order, ",")); err != nil {
		return err
	}
	c.record("order "+strings.ToLower(target), order)
	return nil
}

// SetAuto 는 종료 때 다음 부팅을 Opereta 로 지정하는 유닛(O2b)을 켜거나 끈다.
func (c *Controller) SetAuto(ctx context.Context, on bool) error {
	verb := "disable"
	if on {
		verb = "enable"
	}
	res, err := c.run(ctx, 30*time.Second, "systemctl", verb, "--now", AutoUnit)
	if err != nil {
		return errf(CodeCommand, "systemctl: %v", err)
	}
	if res.ExitCode != 0 {
		return errf(CodeCommand, "systemctl %s exited %d: %s", verb, res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	if !on {
		// 꺼질 때 이미 지정된 BootNext 가 남지 않게 한다. 지울 것이 없어도 실패로 보지 않는다.
		_, _ = c.run(ctx, 10*time.Second, "efibootmgr", "-N")
	}
	c.record("auto "+verb, nil)
	return nil
}

// Repair 는 GRUB 을 다시 설치해 NVRAM 항목과 ESP 파일을 만든다. 대체 경로는 다른 시스템의 파일이면 건드리지 않는다.
func (c *Controller) Repair(ctx context.Context) error {
	if !c.uefi() {
		return errf(CodeNotUEFI, "this system was not started in UEFI mode")
	}
	if !c.espState().Mounted {
		return errf(CodeNoESP, "the EFI system partition is not mounted at %s", c.espDir())
	}
	grub := []string{"--target=x86_64-efi", "--efi-directory=" + c.espDir(), "--bootloader-id=" + BootloaderID}
	if c.secureBoot() != "unknown" {
		grub = append(grub, "--uefi-secure-boot")
	}
	res, err := c.run(ctx, 5*time.Minute, "grub-install", grub...)
	if err != nil || res.ExitCode != 0 {
		// 항목 등록만 실패했을 수 있다. 파일만 설치하고 아래에서 항목을 직접 만든다.
		if res2, err2 := c.run(ctx, 5*time.Minute, "grub-install", append(grub, "--no-nvram")...); err2 != nil || res2.ExitCode != 0 {
			msg := ""
			if err2 != nil {
				msg = err2.Error()
			} else {
				msg = strings.TrimSpace(string(res2.Stderr))
			}
			return errf(CodeCommand, "grub-install failed: %s", msg)
		}
	}
	if _, err := c.run(ctx, 5*time.Minute, "update-grub"); err != nil {
		return errf(CodeCommand, "update-grub: %v", err)
	}
	in, err := c.read(ctx)
	if err != nil {
		return err
	}
	if in.OperetaEntry == "" {
		if err := c.createEntry(ctx); err != nil {
			return err
		}
	}
	c.record("repair", nil)
	return nil
}

// createEntry 는 ESP 가 있는 디스크와 파티션 번호를 찾아 opereta 항목을 직접 만든다.
func (c *Controller) createEntry(ctx context.Context) error {
	res, err := c.run(ctx, 10*time.Second, "findmnt", "-no", "SOURCE", c.espDir())
	if err != nil || res.ExitCode != 0 {
		return errf(CodeNoESP, "cannot find the device mounted at %s", c.espDir())
	}
	dev := strings.TrimSpace(string(res.Stdout))
	disk, part, ok := SplitPartition(dev)
	if !ok {
		return errf(CodeNoESP, "cannot split %q into a disk and a partition number", dev)
	}
	loader := `\EFI\opereta\grubx64.efi`
	if c.secureBoot() != "unknown" {
		if _, err := os.Stat(filepath.Join(c.espDir(), "EFI/opereta/shimx64.efi")); err == nil {
			loader = `\EFI\opereta\shimx64.efi`
		}
	}
	return c.efibootmgr(ctx, "--create", "--disk", disk, "--part", part, "--loader", loader, "--label", BootloaderID)
}

var partRE = regexp.MustCompile(`^(/dev/(?:nvme[0-9]+n[0-9]+|mmcblk[0-9]+))p([0-9]+)$|^(/dev/[a-z]+)([0-9]+)$`)

// SplitPartition 은 /dev/nvme0n1p1 같은 파티션 장치를 디스크와 파티션 번호로 나눈다.
func SplitPartition(dev string) (disk, part string, ok bool) {
	m := partRE.FindStringSubmatch(dev)
	if m == nil {
		return "", "", false
	}
	if m[1] != "" {
		return m[1], m[2], true
	}
	return m[3], m[4], true
}

// Cleanup 은 이전 이름의 ESP 폴더와 NVRAM 항목을 지운다. 이름이 정해진 패턴과 일치하는 것만 지운다.
// 호출하는 쪽(제어 센터, CLI)이 사용자 확인을 받은 뒤에만 부른다.
func (c *Controller) Cleanup(ctx context.Context) error {
	esp := c.espState()
	if esp.Mounted {
		for _, name := range esp.LegacyFolders {
			if strings.ContainsAny(name, `/\`) || name == "." || name == ".." || !legacyRE.MatchString(name) {
				continue
			}
			if err := os.RemoveAll(filepath.Join(c.espDir(), "EFI", name)); err != nil {
				return errf(CodeCommand, "remove %s: %v", name, err)
			}
		}
	}
	if c.uefi() {
		if in, err := c.read(ctx); err == nil {
			for _, e := range in.Entries {
				if e.Kind == kindLegacy {
					if err := c.efibootmgr(ctx, "-B", "-b", e.Num); err != nil {
						return err
					}
				}
			}
		}
	}
	c.record("cleanup", nil)
	return nil
}
