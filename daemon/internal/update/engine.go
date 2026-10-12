package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

// 기본 경로다. 테스트는 Config 로 바꾼다.
const (
	DefaultStateDir = "/var/lib/lightlix/update"
	// PolicyPackage 는 드라이버 핀과 정확한 의존성을 가진 패키지다. 드라이버는 이 패키지를 통해서만 올라간다.
	PolicyPackage = "lightlix-nvidia"
	// DriverPackage 는 드라이버 버전을 대표하는 패키지다.
	DriverPackage = "nvidia-kernel-open-dkms"
)

// Config 는 Engine 이 읽고 쓰는 경로와 제한이다. 빈 값은 기본값이다.
type Config struct {
	StateDir       string
	GrubCfg        string
	ProcCmdline    string
	ProcNvidia     string
	LiveDir        string
	PrefsDir       string
	PolicyPrefName string
	BootDir        string
	RootDir        string
	GPUCheck       string
	TmpDir         string
	MinFreeBoot    uint64
	MinFreeRoot    uint64
}

func (c Config) withDefaults() Config {
	def := func(v *string, d string) {
		if *v == "" {
			*v = d
		}
	}
	def(&c.StateDir, DefaultStateDir)
	def(&c.GrubCfg, "/boot/grub/grub.cfg")
	def(&c.ProcCmdline, "/proc/cmdline")
	def(&c.ProcNvidia, "/proc/driver/nvidia/version")
	def(&c.LiveDir, "/run/live")
	def(&c.PrefsDir, "/etc/apt/preferences.d")
	def(&c.PolicyPrefName, "00-lightlix-nvidia.pref")
	def(&c.BootDir, "/boot")
	def(&c.RootDir, "/")
	def(&c.GPUCheck, "/usr/sbin/lightlix-gpu-check")
	def(&c.TmpDir, os.TempDir())
	if c.MinFreeBoot == 0 {
		c.MinFreeBoot = 512 << 20
	}
	if c.MinFreeRoot == 0 {
		c.MinFreeRoot = 3 << 30
	}
	return c
}

// Engine 은 갱신 작업을 실행한다.
type Engine struct {
	Cfg Config
	Run platform.Runner
	// Now 는 현재 시각이다. nil 이면 time.Now 다.
	Now func() time.Time
	// FreeBytes 는 경로의 여유 바이트다. nil 이면 statfs 를 쓴다.
	FreeBytes func(dir string) (uint64, error)
	// Out 은 진행 이벤트를 JSON 줄로 받는다. nil 이면 버린다.
	Out io.Writer

	state *State
}

var driverModules = []string{"nvidia", "nvidia-modeset", "nvidia-uvm", "nvidia-drm"}

// 제한 시간이다.
const (
	shortTimeout   = 2 * time.Minute
	updateTimeout  = 15 * time.Minute
	installTimeout = 2 * time.Hour
	maxOutput      = 4 << 20
)

func (e *Engine) cfg() Config { return e.Cfg.withDefaults() }

func (e *Engine) now() time.Time {
	if e.Now != nil {
		return e.Now()
	}
	return time.Now()
}

func (e *Engine) stamp() string { return e.now().UTC().Format(time.RFC3339) }

// sh 는 명령을 실행하고 stdout 과 stderr, 종료 코드를 돌려준다. 시작하지 못하면 오류다.
func (e *Engine) sh(ctx context.Context, timeout time.Duration, env []string, name string, args ...string) (platform.Result, error) {
	return e.Run.Run(ctx, platform.Command{Name: name, Args: args, Env: env, Timeout: timeout, MaxOutput: maxOutput})
}

var aptEnv = []string{
	"DEBIAN_FRONTEND=noninteractive", "LC_ALL=C", "APT_LISTCHANGES_FRONTEND=none",
	"NEEDRESTART_SUSPEND=1",
}

// 모든 apt-get 호출에 붙는 공통 옵션이다. 설정 파일은 기존 것을 지킨다.
var aptCommon = []string{
	"-y", "-q", "--no-install-recommends",
	"-o", "Dpkg::Options::=--force-confold", "-o", "Dpkg::Options::=--force-confdef",
}

// 홀드 처리 옵션이다. --ignore-hold 는 hold 된 모든 패키지를 올릴 수 있게 하므로 전체 갱신에만 쓴다.
// 커널만 설치하거나 특정 버전으로 되돌릴 때는 --allow-change-held-packages 만 쓴다(지정한 패키지만 바뀐다).
var (
	holdIgnoreAll  = []string{"--ignore-hold", "--allow-change-held-packages"}
	holdAllowNamed = []string{"--allow-change-held-packages"}
)

// ---- 상태 파일 ----

func (e *Engine) statePath() string { return filepath.Join(e.cfg().StateDir, "state.json") }

// LoadState 는 상태 파일을 읽는다. 없으면 idle 이다.
func (e *Engine) LoadState() (*State, error) {
	b, err := os.ReadFile(e.statePath())
	if errors.Is(err, fs.ErrNotExist) {
		return &State{Phase: PhaseIdle}, nil
	}
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("update: parse %s: %w", e.statePath(), err)
	}
	if s.Phase == "" {
		s.Phase = PhaseIdle
	}
	return &s, nil
}

func (e *Engine) save(s *State) error {
	if err := os.MkdirAll(e.cfg().StateDir, 0o755); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := e.statePath() + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, e.statePath())
}

func (e *Engine) rollbackPath() string { return filepath.Join(e.cfg().StateDir, "rollback.json") }

// saveRollback 은 마지막으로 성공한 갱신의 되돌리기 정보를 따로 저장한다. 이후 실패한 시도가 이 정보를 지우지 않게 한다.
func (e *Engine) saveRollback(s *State) {
	r := State{Phase: s.Phase, Operation: "apply", Started: s.Started, Before: s.Before, PreviousKernel: s.PreviousKernel,
		PreviousDefault: s.PreviousDefault, DriverFrom: s.DriverFrom, DriverTo: s.DriverTo, NewKernel: s.NewKernel}
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return
	}
	_ = os.WriteFile(e.rollbackPath(), append(b, '\n'), 0o644)
}

func (e *Engine) loadRollback() (*State, error) {
	b, err := os.ReadFile(e.rollbackPath())
	if err != nil {
		return nil, err
	}
	var s State
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// saveHistory 는 끝난 작업의 상태를 history 디렉터리에 복사한다.
func (e *Engine) saveHistory(s *State) {
	dir := filepath.Join(e.cfg().StateDir, "history")
	if os.MkdirAll(dir, 0o755) != nil {
		return
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return
	}
	name := strings.NewReplacer(":", "", "-", "").Replace(s.Started) + "-" + s.Operation + ".json"
	_ = os.WriteFile(filepath.Join(dir, name), append(b, '\n'), 0o644)
}

// event 는 진행 기록을 남기고 상태 파일을 갱신한다.
func (e *Engine) event(step, format string, a ...any) {
	msg := fmt.Sprintf(format, a...)
	ev := Event{Time: e.stamp(), Step: step, Message: msg}
	if e.state != nil {
		e.state.Step = step
		e.state.Events = append(e.state.Events, ev)
		if n := len(e.state.Events); n > 300 {
			e.state.Events = e.state.Events[n-300:]
		}
		_ = e.save(e.state)
	}
	if e.Out != nil {
		b, _ := json.Marshal(ev)
		_, _ = e.Out.Write(append(b, '\n'))
	}
}

// ---- 잠금 ----

// lock 은 한 번에 하나의 갱신만 돌게 한다.
func (e *Engine) lock() (func(), error) {
	if err := os.MkdirAll(e.cfg().StateDir, 0o755); err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	f, err := os.OpenFile(filepath.Join(e.cfg().StateDir, "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errf(CodeBusy, "another update is already running")
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

// ---- 시스템 조회 ----

type pkgInfo struct{ Name, Source, Version string }

// installed 는 설치 상태인 패키지 목록이다.
func (e *Engine) installed(ctx context.Context) ([]pkgInfo, error) {
	return e.listPackages(ctx, false)
}

// present 는 설정이 끝나지 않은 패키지(unpacked, half-configured 등)도 포함한다.
// 갱신이 실패한 뒤 되돌릴 대상을 찾을 때 쓴다.
func (e *Engine) present(ctx context.Context) ([]pkgInfo, error) {
	return e.listPackages(ctx, true)
}

func (e *Engine) listPackages(ctx context.Context, partial bool) ([]pkgInfo, error) {
	res, err := e.sh(ctx, shortTimeout, aptEnv, "dpkg-query", "-W", "-f", `${binary:Package}\t${source:Package}\t${Version}\t${db:Status-Status}\n`)
	if err != nil {
		return nil, err
	}
	if res.ExitCode != 0 {
		return nil, fmt.Errorf("dpkg-query exited %d: %s", res.ExitCode, tailString(string(res.Stderr), 300))
	}
	var out []pkgInfo
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		f := strings.Split(strings.TrimRight(line, "\r"), "\t")
		if len(f) < 4 {
			continue
		}
		if f[3] != "installed" {
			if !partial || f[3] == "not-installed" || f[3] == "config-files" || f[3] == "" {
				continue
			}
		}
		out = append(out, pkgInfo{Name: f[0], Source: f[1], Version: f[2]})
	}
	return out, nil
}

func (e *Engine) heldSet(ctx context.Context) (map[string]bool, error) {
	res, err := e.sh(ctx, shortTimeout, aptEnv, "apt-mark", "showhold")
	if err != nil {
		return nil, err
	}
	held := map[string]bool{}
	for _, l := range strings.Fields(string(res.Stdout)) {
		held[l] = true
	}
	return held, nil
}

// managedSnapshot 은 관리 대상 패키지와 버전이다. 롤백이 읽는다.
func managedSnapshot(pkgs []pkgInfo, held map[string]bool) []PkgVersion {
	var out []PkgVersion
	for _, p := range pkgs {
		if managedPackage(p.Name, p.Source, held) || p.Name == PolicyPackage {
			out = append(out, PkgVersion{Name: p.Name, Version: p.Version})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func versionOf(pkgs []pkgInfo, name string) string {
	for _, p := range pkgs {
		if p.Name == name {
			return p.Version
		}
	}
	return ""
}

var bootImageRE = regexp.MustCompile(`BOOT_IMAGE=\S*vmlinuz-(\S+)`)

// runningKernel 은 지금 부팅된 커널 릴리스다. GRUB 이 넘긴 BOOT_IMAGE 를 먼저 보고, 없으면 uname -r 이다.
func (e *Engine) runningKernel(ctx context.Context) string {
	if b, err := os.ReadFile(e.cfg().ProcCmdline); err == nil {
		if m := bootImageRE.FindStringSubmatch(string(b)); m != nil {
			return m[1]
		}
	}
	if res, err := e.sh(ctx, shortTimeout, nil, "uname", "-r"); err == nil && res.ExitCode == 0 {
		return strings.TrimSpace(string(res.Stdout))
	}
	return ""
}

// installedKernels 는 설치된 커널 이미지 패키지와 릴리스다. 버전 오름차순이다.
func installedKernels(pkgs []pkgInfo) []PkgVersion {
	var out []PkgVersion
	for _, p := range pkgs {
		if rel := kernelRelease(p.Name); rel != "" {
			out = append(out, PkgVersion{Name: rel, Version: p.Version})
		}
	}
	sortByVersion(out)
	return out
}

// ---- GRUB ----

// grubEntry 는 커널 릴리스에 해당하는 GRUB 메뉴 항목 ID 다.
func (e *Engine) grubEntry(rel string) (string, error) {
	id, err := GrubEntryID(e.cfg().GrubCfg, rel)
	if err != nil {
		return "", errf(CodeGrub, "%v", err)
	}
	return id, nil
}

// GrubEntryID 는 grub.cfg 에서 커널 릴리스의 메뉴 항목 ID(gnulinux-<릴리스>-advanced-<UUID>)를 찾는다.
func GrubEntryID(cfgPath, rel string) (string, error) {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", cfgPath, err)
	}
	re := regexp.MustCompile(`\$menuentry_id_option '(gnulinux-` + regexp.QuoteMeta(rel) + `-advanced-[^']+)'`)
	if m := re.FindStringSubmatch(string(b)); m != nil {
		return m[1], nil
	}
	return "", fmt.Errorf("no GRUB menu entry for kernel %s in %s", rel, cfgPath)
}

var grubReleaseRE = regexp.MustCompile(`\$menuentry_id_option 'gnulinux-(.+)-advanced-[^']+'`)

// GrubKernelReleases 는 grub.cfg 의 커널 메뉴 항목에 있는 릴리스 목록이다. 버전 오름차순이다.
func GrubKernelReleases(cfgPath string) ([]string, error) {
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, m := range grubReleaseRE.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			out = append(out, m[1])
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return compareVersions(out[i], out[j]) < 0 })
	return out, nil
}

func (e *Engine) savedEntry(ctx context.Context) string {
	res, err := e.sh(ctx, shortTimeout, nil, "grub-editenv", "list")
	if err != nil || res.ExitCode != 0 {
		return ""
	}
	for _, l := range strings.Split(string(res.Stdout), "\n") {
		if v, ok := strings.CutPrefix(l, "saved_entry="); ok {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func (e *Engine) grubCmd(ctx context.Context, name string, args ...string) error {
	res, err := e.sh(ctx, shortTimeout, nil, name, args...)
	if err != nil {
		return errf(CodeGrub, "%s: %v", name, err)
	}
	if res.ExitCode != 0 {
		return errf(CodeGrub, "%s exited %d: %s", name, res.ExitCode, tailString(string(res.Stderr), 300))
	}
	return nil
}

// ---- 사전 검사 ----

func (e *Engine) preflight(ctx context.Context) error {
	c := e.cfg()
	if _, err := os.Stat(c.LiveDir); err == nil {
		return errf(CodeLiveSession, "this is a live session; updates apply to an installed system")
	}
	free := e.FreeBytes
	if free == nil {
		free = statfsFree
	}
	for _, chk := range []struct {
		dir string
		min uint64
	}{{c.BootDir, c.MinFreeBoot}, {c.RootDir, c.MinFreeRoot}} {
		n, err := free(chk.dir)
		if err != nil {
			return errf(CodeInternal, "cannot read free space of %s: %v", chk.dir, err)
		}
		if n < chk.min {
			return errf(CodeDiskSpace, "%s has %d MiB free; at least %d MiB is needed", chk.dir, n>>20, chk.min>>20)
		}
	}
	return nil
}

func statfsFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, err
	}
	return uint64(st.Bavail) * uint64(st.Bsize), nil
}

func (e *Engine) aptUpdate(ctx context.Context) error {
	res, err := e.sh(ctx, updateTimeout, aptEnv, "apt-get", "update", "-q")
	if err != nil {
		return errf(CodeNetwork, "apt-get update: %v", err)
	}
	stderr := string(res.Stderr)
	if strings.Contains(stderr, "Could not get lock") {
		return errf(CodeDpkgLocked, "another package manager is running")
	}
	if res.ExitCode != 0 || strings.Contains(stderr, "Failed to fetch") || strings.Contains(stderr, "Temporary failure resolving") {
		return errf(CodeNetwork, "the package indexes could not be refreshed: %s", tailString(stderr, 400))
	}
	return nil
}

// prefsOverride 는 드라이버 핀 파일을 뺀 apt preferences 디렉터리를 임시로 만든다.
// 드라이버를 올리려면 새 lightlix-nvidia 가 가져올 정확한 버전을 apt 가 고를 수 있어야 한다. 브랜치 핀은 남긴다.
func (e *Engine) prefsOverride() (dir string, cleanup func(), err error) {
	c := e.cfg()
	dir, err = os.MkdirTemp(c.TmpDir, "lightlix-prefs-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(dir) }
	entries, err := os.ReadDir(c.PrefsDir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return dir, cleanup, nil
		}
		cleanup()
		return "", nil, err
	}
	for _, ent := range entries {
		if ent.IsDir() || ent.Name() == c.PolicyPrefName {
			continue
		}
		b, rerr := os.ReadFile(filepath.Join(c.PrefsDir, ent.Name()))
		if rerr != nil {
			continue
		}
		if werr := os.WriteFile(filepath.Join(dir, ent.Name()), b, 0o644); werr != nil {
			cleanup()
			return "", nil, werr
		}
	}
	return dir, cleanup, nil
}

// simulate 는 apt-get -s 로 계획을 만든다. prefsDir 가 비어 있지 않으면 그 디렉터리를 preferences 로 쓴다.
func (e *Engine) simulate(ctx context.Context, prefsDir string, verb string, pkgs ...string) (changes []Change, removals []string, err error) {
	args := []string{"-s"}
	args = append(args, aptCommon...)
	args = append(args, holdIgnoreAll...)
	if prefsDir != "" {
		args = append(args, "-o", "Dir::Etc::PreferencesParts="+prefsDir)
	}
	args = append(args, verb)
	args = append(args, pkgs...)
	res, rerr := e.sh(ctx, updateTimeout, aptEnv, "apt-get", args...)
	if rerr != nil {
		return nil, nil, errf(CodeInternal, "apt-get -s: %v", rerr)
	}
	if res.ExitCode != 0 {
		return nil, nil, errf(CodeUpgrade, "apt-get could not compute a plan: %s", tailString(string(res.Stderr)+string(res.Stdout), 600))
	}
	changes, removals = parseSimulation(string(res.Stdout))
	return changes, removals, nil
}

// buildPlan 은 설치 상태와 apt 후보를 비교해 계획을 만든다.
func (e *Engine) buildPlan(ctx context.Context, pkgs []pkgInfo, prefsDir string) (*Plan, error) {
	changes, removals, err := e.simulate(ctx, prefsDir, "dist-upgrade")
	if err != nil {
		return nil, err
	}
	p := &Plan{Changes: changes, Removals: removals, DriverFrom: versionOf(pkgs, DriverPackage), PolicyFrom: versionOf(pkgs, PolicyPackage)}
	for _, c := range changes {
		if rel := kernelRelease(c.Name); rel != "" && c.New {
			p.Kernels = append(p.Kernels, rel)
		}
		if isKernelVersioned(c.Name) && c.New {
			p.KernelPackages = append(p.KernelPackages, c.Name)
		}
		switch c.Name {
		case DriverPackage:
			p.DriverTo = c.To
		case PolicyPackage:
			p.PolicyTo = c.To
		}
	}
	if p.DriverTo == p.DriverFrom {
		p.DriverTo = ""
	}
	p.UpToDate = len(changes) == 0
	return p, nil
}

func checkRemovals(p *Plan) error {
	var bad []string
	for _, r := range p.Removals {
		if protectedPackage(r) {
			bad = append(bad, r)
		}
	}
	if len(bad) > 0 {
		return errf(CodeUnexpectedRemoval, "the update would remove protected packages: %s", strings.Join(bad, ", "))
	}
	return nil
}

// Check 는 인덱스를 새로 받고 갱신 계획만 돌려준다. 설치하지 않는다. 결과는 check.json 에도 남긴다.
func (e *Engine) Check(ctx context.Context) (*Plan, error) {
	if err := e.preflightCheck(); err != nil {
		return nil, err
	}
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	plan, cerr := e.check(ctx)
	e.saveCheck(plan, cerr)
	return plan, cerr
}

func (e *Engine) check(ctx context.Context) (*Plan, error) {
	e.event("refresh", "refreshing package indexes")
	if err := e.aptUpdate(ctx); err != nil {
		return nil, err
	}
	pkgs, err := e.installed(ctx)
	if err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	dir, cleanup, err := e.prefsOverride()
	if err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	defer cleanup()
	p, err := e.buildPlan(ctx, pkgs, dir)
	if err != nil {
		return nil, err
	}
	return p, checkRemovals(p)
}

func (e *Engine) checkPath() string { return filepath.Join(e.cfg().StateDir, "check.json") }

// saveCheck 는 check 결과를 파일로 남긴다. 쓰지 못해도 check 자체는 실패로 보지 않는다.
func (e *Engine) saveCheck(plan *Plan, err error) {
	r := CheckResult{Time: e.stamp(), Plan: plan}
	if err != nil {
		var ue *Error
		if !errors.As(err, &ue) {
			ue = errf(CodeInternal, "%v", err)
		}
		r.Error = ue
	}
	b, jerr := json.MarshalIndent(r, "", "  ")
	if jerr != nil || os.MkdirAll(e.cfg().StateDir, 0o755) != nil {
		return
	}
	tmp := e.checkPath() + ".tmp"
	if os.WriteFile(tmp, append(b, '\n'), 0o644) == nil {
		_ = os.Rename(tmp, e.checkPath())
	}
}

// preflightCheck 는 점검 전용 사전 검사다. 디스크 여유는 보지 않는다.
func (e *Engine) preflightCheck() error {
	if _, err := os.Stat(e.cfg().LiveDir); err == nil {
		return errf(CodeLiveSession, "this is a live session; updates apply to an installed system")
	}
	return nil
}

// ---- 모듈 검증 ----

// verifyKernelModules 는 커널 rel 에 드라이버 오픈 모듈이 want 버전으로 빌드되어 있는지 확인한다.
func (e *Engine) verifyKernelModules(ctx context.Context, rel, want string) error {
	if want == "" {
		return nil
	}
	var problems []string
	prefix := filepath.Join("/lib/modules", rel, "updates", "dkms") + "/"
	for _, m := range driverModules {
		res, err := e.sh(ctx, shortTimeout, nil, "modinfo", "-k", rel, "-F", "filename", m)
		path := strings.TrimSpace(string(res.Stdout))
		if err != nil || res.ExitCode != 0 || path == "" {
			problems = append(problems, m+": module not found")
			continue
		}
		if !strings.HasPrefix(path, prefix) {
			problems = append(problems, fmt.Sprintf("%s: unexpected path %s", m, path))
			continue
		}
		for _, f := range []struct{ field, want string }{{"version", want}, {"license", "Dual MIT/GPL"}} {
			r, err := e.sh(ctx, shortTimeout, nil, "modinfo", "-k", rel, "-F", f.field, m)
			if got := strings.TrimSpace(string(r.Stdout)); err != nil || r.ExitCode != 0 || got != f.want {
				problems = append(problems, fmt.Sprintf("%s: %s is %q, want %q", m, f.field, got, f.want))
			}
		}
	}
	initrd := filepath.Join(e.cfg().BootDir, "initrd.img-"+rel)
	if fi, err := os.Stat(initrd); err != nil || fi.Size() == 0 {
		problems = append(problems, "initramfs missing: "+initrd)
	}
	if len(problems) > 0 {
		return errf(CodeDKMS, "kernel %s: %s", rel, strings.Join(problems, "; "))
	}
	return nil
}

// ---- 적용 ----

// ApplyOptions 는 Apply 의 선택 사항이다.
type ApplyOptions struct {
	// Reboot 가 참이고 갱신이 성공하면 systemctl reboot 를 부른다.
	Reboot bool
}

func (e *Engine) begin(op string) *State {
	s := &State{Phase: PhaseRunning, Operation: op, Started: e.stamp()}
	e.state = s
	_ = e.save(s)
	return s
}

// fail 은 상태를 failed 로 바꾸고 기록한 뒤 그 오류를 돌려준다.
func (e *Engine) fail(err error) error {
	var ue *Error
	if !errors.As(err, &ue) {
		ue = errf(CodeInternal, "%v", err)
	}
	if e.state != nil {
		e.state.Phase = PhaseFailed
		e.state.Code = ue.Code
		e.state.Message = ue.Message
		e.state.Finished = e.stamp()
		e.event("failed", "%s: %s", ue.Code, ue.Message)
		e.saveHistory(e.state)
	}
	return ue
}

// rehold 는 설치된 관리 대상 패키지에 hold 를 다시 건다. 오래된 hold 는 apt 가 패키지와 함께 지운다.
func (e *Engine) rehold(ctx context.Context, held map[string]bool) {
	pkgs, err := e.installed(ctx)
	if err != nil {
		return
	}
	var names []string
	for _, p := range pkgs {
		if managedPackage(p.Name, p.Source, held) {
			names = append(names, p.Name)
		}
	}
	sort.Strings(names)
	if len(names) > 0 {
		_, _ = e.sh(ctx, shortTimeout, aptEnv, "apt-mark", append([]string{"hold"}, names...)...)
	}
}

// aptInstall 은 apt-get 으로 설치나 업그레이드를 실행한다. prefsDir 가 있으면 그 preferences 를 쓴다.
func (e *Engine) aptRun(ctx context.Context, prefsDir, verb string, extra []string, pkgs ...string) (platform.Result, error) {
	args := append([]string{}, aptCommon...)
	if prefsDir != "" {
		args = append(args, "-o", "Dir::Etc::PreferencesParts="+prefsDir)
	}
	args = append(args, extra...)
	args = append(args, verb)
	args = append(args, pkgs...)
	return e.sh(ctx, installTimeout, aptEnv, "apt-get", args...)
}

func aptFailure(res platform.Result, err error) string {
	if err != nil {
		return err.Error()
	}
	return tailString(string(res.Stderr)+"\n"+string(res.Stdout), 700)
}

// aptFailureCode 는 apt-get 의 전체 출력을 보고 실패 원인에 맞는 코드를 고른다. 메시지의 끝부분만으로는 DKMS 로그가 잘릴 수 있다.
func aptFailureCode(res platform.Result, err error, def string) string {
	if err != nil {
		return def
	}
	full := strings.ToLower(string(res.Stderr) + "\n" + string(res.Stdout))
	switch {
	case strings.Contains(full, "could not get lock"):
		return CodeDpkgLocked
	case strings.Contains(full, "dkms"):
		return CodeDKMS
	}
	return def
}

// Apply 는 갱신을 설치한다. 성공하면 pending_boot 상태(또는 바뀐 것이 없으면 committed)가 된다.
func (e *Engine) Apply(ctx context.Context, opt ApplyOptions) (*State, error) {
	if err := e.preflightCheck(); err != nil {
		return nil, err
	}
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()

	prior, lerr := e.LoadState()
	if lerr != nil {
		return nil, errf(CodeInternal, "%v", lerr)
	}
	if prior.Phase == PhasePendingBoot {
		return prior, errf(CodePendingBoot, "the previous update is waiting for a reboot; reboot first so it can be verified")
	}
	st := e.begin("apply")
	// 클라이언트가 끊겨도 dpkg 는 끝까지 간다.
	ctx = context.WithoutCancel(ctx)

	if err := e.preflight(ctx); err != nil {
		return st, e.fail(err)
	}
	e.event("refresh", "refreshing package indexes")
	if err := e.aptUpdate(ctx); err != nil {
		return st, e.fail(err)
	}
	pkgs, err := e.installed(ctx)
	if err != nil {
		return st, e.fail(errf(CodeInternal, "%v", err))
	}
	held, err := e.heldSet(ctx)
	if err != nil {
		return st, e.fail(errf(CodeInternal, "%v", err))
	}
	prefs, cleanup, err := e.prefsOverride()
	if err != nil {
		return st, e.fail(errf(CodeInternal, "%v", err))
	}
	defer cleanup()

	e.event("plan", "computing the update plan")
	plan, err := e.buildPlan(ctx, pkgs, prefs)
	if err != nil {
		return st, e.fail(err)
	}
	if err := checkRemovals(plan); err != nil {
		return st, e.fail(err)
	}
	st.Plan = plan
	st.Before = managedSnapshot(pkgs, held)
	st.DriverFrom = plan.DriverFrom
	st.DriverTo = plan.DriverTo
	prevKernel := e.runningKernel(ctx)
	st.PreviousKernel = prevKernel
	if res, gerr := e.sh(ctx, shortTimeout, nil, e.cfg().GPUCheck); gerr == nil {
		code := res.ExitCode
		st.GPUBefore = &code
	}
	if plan.UpToDate {
		// 바뀐 것이 없으므로 이전 상태와 되돌리기 정보를 그대로 둔다.
		e.event("done", "the system is up to date")
		_ = e.save(prior)
		e.state = prior
		return &State{Phase: prior.Phase, Operation: "apply", Plan: plan, Message: "up to date", Finished: e.stamp(), Events: st.Events}, nil
	}
	e.event("plan", "%d package changes, kernels %v, driver %q -> %q", len(plan.Changes), plan.Kernels, plan.DriverFrom, plan.DriverTo)

	// 새 커널이 부팅 항목을 가로채지 않도록, 지금 쓰는 커널을 저장된 기본값으로 먼저 고정한다.
	if len(plan.Kernels) > 0 && prevKernel != "" {
		id, gerr := e.grubEntry(prevKernel)
		if gerr != nil {
			return st, e.fail(gerr)
		}
		st.PreviousDefault = id
		if gerr := e.grubCmd(ctx, "grub-set-default", id); gerr != nil {
			return st, e.fail(gerr)
		}
		e.event("grub", "the running kernel %s is pinned as the saved default", prevKernel)
	}
	defer e.rehold(ctx, held)

	// 1단계: 새 커널과 헤더를 먼저 설치해 DKMS 빌드를 확인한다.
	driverNow := upstreamVersion(versionOf(pkgs, DriverPackage))
	if len(plan.KernelPackages) > 0 {
		e.event("kernel", "installing %s", strings.Join(plan.KernelPackages, " "))
		res, rerr := e.aptRun(ctx, prefs, "install", nil, plan.KernelPackages...)
		if rerr != nil || res.ExitCode != 0 {
			msg := aptFailure(res, rerr)
			code := aptFailureCode(res, rerr, CodeKernelInstall)
			e.cleanupKernels(ctx, plan.Kernels, prefs)
			return st, e.fail(errf(code, "installing the new kernel failed, the previous kernel is still the default: %s", msg))
		}
		for _, rel := range plan.Kernels {
			if verr := e.verifyKernelModules(ctx, rel, driverNow); verr != nil {
				e.cleanupKernels(ctx, plan.Kernels, prefs)
				return st, e.fail(errf(CodeDKMS, "%v; the new kernel was removed and the previous kernel is still the default", verr))
			}
			e.event("kernel", "kernel %s: open modules %s verified", rel, driverNow)
		}
	}

	// 2단계: 나머지 갱신(드라이버, 메타 패키지, 보안 갱신)을 설치한다.
	e.event("upgrade", "installing the remaining updates")
	res, rerr := e.aptRun(ctx, prefs, "dist-upgrade", holdIgnoreAll)
	if rerr != nil || res.ExitCode != 0 {
		msg := aptFailure(res, rerr)
		code := aptFailureCode(res, rerr, CodeUpgrade)
		recovered := e.recoverDriver(ctx, st, prefs)
		note := ""
		if recovered {
			note = " The previous driver versions were restored."
		}
		return st, e.fail(errf(code, "the upgrade failed: %s.%s", msg, note))
	}

	// 3단계: 최종 드라이버 버전이 모든 설치된 커널에 빌드되었는지 확인한다.
	after, err := e.installed(ctx)
	if err != nil {
		return st, e.fail(errf(CodeInternal, "%v", err))
	}
	driverNew := upstreamVersion(versionOf(after, DriverPackage))
	for _, k := range installedKernels(after) {
		if verr := e.verifyKernelModules(ctx, k.Name, driverNew); verr != nil {
			recovered := e.recoverDriver(ctx, st, prefs)
			note := ""
			if recovered {
				note = " The previous driver versions were restored."
			}
			return st, e.fail(errf(CodeDKMS, "%v.%s", verr, note))
		}
	}
	e.event("verify", "driver %s is built for %d installed kernel(s)", driverNew, len(installedKernels(after)))

	st.DriverTo = ""
	if driverNew != upstreamVersion(plan.DriverFrom) {
		st.DriverTo = versionOf(after, DriverPackage)
	}
	st.RebootRequired = st.DriverTo != "" || len(plan.Kernels) > 0
	if len(plan.Kernels) > 0 {
		// 가장 높은 버전의 새 커널을 고른다.
		best := plan.Kernels[0]
		for _, k := range plan.Kernels[1:] {
			if compareVersions(versionOfKernel(after, k), versionOfKernel(after, best)) > 0 {
				best = k
			}
		}
		st.NewKernel = best
		id, gerr := e.grubEntry(best)
		if gerr != nil {
			return st, e.fail(gerr)
		}
		if gerr := e.grubCmd(ctx, "grub-reboot", id); gerr != nil {
			return st, e.fail(gerr)
		}
		e.event("grub", "booting %s once; the default changes only after the verification passes", best)
	}
	if st.RebootRequired {
		st.Phase = PhasePendingBoot
		e.event("done", "installed; reboot to finish")
	} else {
		st.Phase = PhaseCommitted
		e.event("done", "installed; no reboot is needed")
	}
	st.Finished = e.stamp()
	_ = e.save(st)
	e.saveRollback(st)
	e.saveHistory(st)
	if opt.Reboot && st.RebootRequired {
		_, _ = e.sh(ctx, shortTimeout, nil, "systemctl", "reboot")
	}
	return st, nil
}

func versionOfKernel(pkgs []pkgInfo, rel string) string {
	return versionOf(pkgs, "linux-image-"+rel)
}

// cleanupKernels 는 설치에 실패한 새 커널의 패키지, DKMS 잔여물, GRUB 항목을 지운다. 이전 커널은 건드리지 않는다.
func (e *Engine) cleanupKernels(ctx context.Context, releases []string, prefs string) {
	for _, rel := range releases {
		e.event("cleanup", "removing the failed kernel %s", rel)
		res, err := e.sh(ctx, shortTimeout, aptEnv, "dkms", "status", "-k", rel)
		if err == nil {
			for _, l := range strings.Split(string(res.Stdout), "\n") {
				// 예: nvidia-open/595.91.07, 6.12.112+deb13-amd64, x86_64: built
				if m := regexp.MustCompile(`^([^/\s]+)/([^,\s]+),`).FindStringSubmatch(strings.TrimSpace(l)); m != nil {
					_, _ = e.sh(ctx, shortTimeout, aptEnv, "dkms", "remove", m[1]+"/"+m[2], "-k", rel)
				}
			}
		}
		pkgs := []string{"linux-image-" + rel, "linux-headers-" + rel}
		_, _ = e.aptRun(ctx, prefs, "purge", holdAllowNamed, pkgs...)
		_, _ = e.sh(ctx, shortTimeout, aptEnv, "dpkg", "--purge", "--force-remove-reinstreq", "linux-image-"+rel, "linux-headers-"+rel)
	}
	_, _ = e.sh(ctx, updateTimeout, aptEnv, "update-grub")
}

// recoverDriver 는 드라이버 계열 패키지를 갱신 전 버전으로 되돌린다. 되돌릴 것이 없으면 false 다.
func (e *Engine) recoverDriver(ctx context.Context, st *State, prefs string) bool {
	args := e.downgradeArgs(ctx, st)
	if len(args) == 0 {
		return false
	}
	e.event("recover", "restoring %d package(s) to the previous versions", len(args))
	res, err := e.aptRun(ctx, prefs, "install", append([]string{"--allow-downgrades"}, holdAllowNamed...), args...)
	return err == nil && res.ExitCode == 0
}

// downgradeArgs 는 갱신 전 버전과 다른 드라이버 계열 패키지를 name=version 으로 돌려준다.
func (e *Engine) downgradeArgs(ctx context.Context, st *State) []string {
	pkgs, err := e.present(ctx)
	if err != nil {
		return nil
	}
	var args []string
	for _, b := range st.Before {
		if strings.HasPrefix(b.Name, "linux-") {
			continue
		}
		cur := versionOf(pkgs, b.Name)
		if cur != "" && cur != b.Version {
			args = append(args, b.Name+"="+b.Version)
		}
	}
	sort.Strings(args)
	return args
}

// ---- 부팅 뒤 검증 ----

// Verify 는 pending_boot 상태에서 부팅 결과를 확인하고, 통과하면 기본 커널을 확정한다.
func (e *Engine) Verify(ctx context.Context) (*State, error) {
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	st, err := e.LoadState()
	if err != nil {
		return nil, errf(CodeInternal, "%v", err)
	}
	if st.Phase != PhasePendingBoot {
		return st, errf(CodeNoPending, "no update is waiting for verification (state: %s)", st.Phase)
	}
	e.state = st
	ctx = context.WithoutCancel(ctx)
	booted := e.runningKernel(ctx)
	e.event("verify", "booted kernel %s", booted)

	if st.NewKernel != "" && booted != st.NewKernel {
		return st, e.failVerify(errf(CodeVerify, "the new kernel %s did not boot; the system is running %s and the saved default is unchanged", st.NewKernel, booted))
	}
	pkgs, err := e.installed(ctx)
	if err != nil {
		return st, e.failVerify(errf(CodeInternal, "%v", err))
	}
	driver := upstreamVersion(versionOf(pkgs, DriverPackage))
	if driver != "" {
		if verr := e.verifyKernelModules(ctx, booted, driver); verr != nil {
			return st, e.failVerify(verr)
		}
		if b, rerr := os.ReadFile(e.cfg().ProcNvidia); rerr == nil {
			text := string(b)
			if !strings.Contains(text, "Open Kernel Module") || !strings.Contains(text, driver) {
				return st, e.failVerify(errf(CodeVerify, "the loaded NVIDIA module is not the open module %s", driver))
			}
		}
		res, gerr := e.sh(ctx, shortTimeout, nil, e.cfg().GPUCheck)
		if gerr == nil && (res.ExitCode == 2 || res.ExitCode == 3) {
			// 갱신 전에도 못 쓰던 GPU 라면 이번 갱신 탓이 아니다. 갱신 전에 쓸 수 있었던 GPU 가 못 쓰게 되었을 때만 실패로 본다.
			if st.GPUBefore != nil && *st.GPUBefore == 0 {
				return st, e.failVerify(errf(CodeVerify, "the GPU worked before the update but the diagnostics now report a problem (exit %d)", res.ExitCode))
			}
			e.event("verify", "the GPU diagnostics report a problem (exit %d), as before the update", res.ExitCode)
		}
	}
	if st.NewKernel != "" {
		id, gerr := e.grubEntry(booted)
		if gerr != nil {
			return st, e.failVerify(gerr)
		}
		if gerr := e.grubCmd(ctx, "grub-set-default", id); gerr != nil {
			return st, e.failVerify(gerr)
		}
		e.event("grub", "the saved default is now %s", booted)
	}
	e.pruneKernels(ctx, st, booted, pkgs)
	st.Phase = PhaseCommitted
	st.RebootRequired = false
	st.Code, st.Message = "", ""
	st.Finished = e.stamp()
	e.event("done", "the update is verified and committed")
	e.saveHistory(st)
	return st, nil
}

func (e *Engine) failVerify(err error) error {
	var ue *Error
	if !errors.As(err, &ue) {
		ue = errf(CodeInternal, "%v", err)
	}
	e.state.Phase = PhaseFailed
	e.state.Code, e.state.Message = ue.Code, ue.Message
	e.state.RebootRequired = false
	e.state.Finished = e.stamp()
	e.event("failed", "%s: %s", ue.Code, ue.Message)
	e.saveHistory(e.state)
	return ue
}

// pruneKernels 는 부팅한 커널과 갱신 전 커널만 남기고 오래된 커널을 지운다.
func (e *Engine) pruneKernels(ctx context.Context, st *State, booted string, pkgs []pkgInfo) {
	keep := map[string]bool{booted: true}
	if st.PreviousKernel != "" {
		keep[st.PreviousKernel] = true
	}
	var remove []string
	for _, k := range installedKernels(pkgs) {
		if !keep[k.Name] {
			remove = append(remove, "linux-image-"+k.Name)
			if versionOf(pkgs, "linux-headers-"+k.Name) != "" {
				remove = append(remove, "linux-headers-"+k.Name)
			}
		}
	}
	if len(remove) == 0 {
		return
	}
	e.event("prune", "removing old kernels: %s", strings.Join(remove, " "))
	_, _ = e.aptRun(ctx, "", "purge", holdAllowNamed, remove...)
	_, _ = e.sh(ctx, updateTimeout, aptEnv, "update-grub")
}

// ---- 롤백 ----

// Rollback 은 마지막 갱신 전의 드라이버 버전과 커널로 되돌린다. 재부팅이 필요하다.
func (e *Engine) Rollback(ctx context.Context, opt ApplyOptions) (*State, error) {
	if err := e.preflightCheck(); err != nil {
		return nil, err
	}
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	prev, err := e.loadRollback()
	if err != nil || (prev.PreviousKernel == "" && len(prev.Before) == 0) {
		cur, _ := e.LoadState()
		return cur, errf(CodeRollbackNA, "there is no previous update to roll back")
	}
	st := &State{Phase: PhaseRunning, Operation: "rollback", Started: e.stamp(), Before: prev.Before, PreviousKernel: prev.PreviousKernel, DriverFrom: prev.DriverTo, DriverTo: prev.DriverFrom}
	e.state = st
	_ = e.save(st)
	ctx = context.WithoutCancel(ctx)

	pkgs, err := e.installed(ctx)
	if err != nil {
		return st, e.fail(errf(CodeInternal, "%v", err))
	}
	// 갱신 전 커널이 남아 있어야 한다.
	var target string
	if prev.PreviousKernel != "" {
		for _, k := range installedKernels(pkgs) {
			if k.Name == prev.PreviousKernel {
				target = k.Name
			}
		}
		if target == "" {
			return st, e.fail(errf(CodeRollbackNA, "the previous kernel %s is no longer installed", prev.PreviousKernel))
		}
	}
	held, _ := e.heldSet(ctx)
	prefs, cleanup, err := e.prefsOverride()
	if err != nil {
		return st, e.fail(errf(CodeInternal, "%v", err))
	}
	defer cleanup()
	defer e.rehold(ctx, held)

	args := e.downgradeArgs(ctx, &State{Before: prev.Before})
	if len(args) > 0 {
		e.event("driver", "restoring %s", strings.Join(args, " "))
		res, rerr := e.aptRun(ctx, prefs, "install", append([]string{"--allow-downgrades"}, holdAllowNamed...), args...)
		if rerr != nil || res.ExitCode != 0 {
			return st, e.fail(errf(CodeRollbackNA, "the previous package versions are not available: %s", aptFailure(res, rerr)))
		}
		after, ierr := e.installed(ctx)
		if ierr != nil {
			return st, e.fail(errf(CodeInternal, "%v", ierr))
		}
		driver := upstreamVersion(versionOf(after, DriverPackage))
		for _, k := range installedKernels(after) {
			if verr := e.verifyKernelModules(ctx, k.Name, driver); verr != nil {
				return st, e.fail(verr)
			}
		}
	}
	if target != "" {
		id, gerr := e.grubEntry(target)
		if gerr != nil {
			return st, e.fail(gerr)
		}
		if gerr := e.grubCmd(ctx, "grub-set-default", id); gerr != nil {
			return st, e.fail(gerr)
		}
		e.event("grub", "the saved default is now %s", target)
	}
	// 아직 쓰지 않은 한 번 부팅 요청이 있으면 지운다.
	_, _ = e.sh(ctx, shortTimeout, nil, "grub-editenv", "-", "unset", "next_entry")
	st.Phase = PhaseRolledBack
	st.RebootRequired = true
	st.Finished = e.stamp()
	e.event("done", "rolled back; reboot to use the previous configuration")
	e.saveHistory(st)
	_ = os.Remove(e.rollbackPath())
	if opt.Reboot {
		_, _ = e.sh(ctx, shortTimeout, nil, "systemctl", "reboot")
	}
	return st, nil
}

// ---- 상태 조회 ----

// Status 는 상태 파일과 현재 시스템 정보를 합쳐 돌려준다.
func (e *Engine) Status(ctx context.Context) (*Status, error) {
	st, err := e.LoadState()
	if err != nil {
		return nil, err
	}
	out := &Status{State: *st}
	if _, err := os.Stat(e.cfg().LiveDir); err == nil {
		out.Live = true
	}
	out.RunningKernel = e.runningKernel(ctx)
	out.GrubDefault = e.savedEntry(ctx)
	if pkgs, err := e.installed(ctx); err == nil {
		for _, k := range installedKernels(pkgs) {
			out.Kernels = append(out.Kernels, k.Name)
		}
	}
	return out, nil
}

// protectedPackage 는 갱신 중 지워지면 안 되는 패키지다.
func protectedPackage(name string) bool {
	for _, p := range []string{"lightlix-", "nvidia-", "libnvidia-", "cuda-", "libcublas", "linux-image", "linux-headers", "linux-kbuild", "dkms", "grub-", "shim-", "initramfs-tools", "systemd", "base-files", "libc6"} {
		if strings.HasPrefix(name, p) {
			return true
		}
	}
	return false
}

func tailString(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}
