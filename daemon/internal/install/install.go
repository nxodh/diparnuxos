// Package install 은 라이브 세션에서 디스크 하나에 Opereta 를 설치하는 opereta-install 의 로직이다.
//
// 설계서(docs/phase-4-design.md 6절)와 같은 순서를 따른다: 디스크 검증, 분할, 포맷, 복사, fstab, 로케일, 계정,
// 라이브 패키지 제거, initramfs, GRUB. Calamares 경로는 같은 대상 마무리 단계(PrepareTarget, BootDefault)를 쓴다.
// 모든 외부 명령은 platform.Runner 로 실행하므로 실제 디스크 없이 단계 순서와 명령 인자를 테스트할 수 있다.
package install

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/accounts"
	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// 오류 코드다.
const (
	CodeInvalidOption  = "invalid_option"
	CodeNotLive        = "not_live_session"
	CodeMissingTool    = "missing_tool"
	CodeDiskNotFound   = "disk_not_found"
	CodeDiskBusy       = "disk_busy"
	CodeDiskTooSmall   = "disk_too_small"
	CodeDiskReadOnly   = "disk_read_only"
	CodeSourceMissing  = "source_missing"
	CodePartition      = "partition_failed"
	CodeFormat         = "format_failed"
	CodeMount          = "mount_failed"
	CodeCopy           = "copy_failed"
	CodeConfigure      = "configure_failed"
	CodeAccount        = "account_failed"
	CodeCleanup        = "cleanup_failed"
	CodeBootloader     = "bootloader_failed"
	CodeNotUEFI        = "not_uefi"
	CodeDamagedSystem  = "damaged_system"
	CodeNotConfirmed   = "not_confirmed"
	defaultTargetRoot  = "/mnt/opereta-target"
	defaultMinDisk     = 16 << 30
	defaultLiveSource  = "/run/live/rootfs/filesystem.squashfs"
	defaultLiveMedium  = "/run/live/medium"
	defaultInstallLog  = "/var/log/opereta-install.log"
	espSize            = "1GiB"
	espPartType        = "U"
	rootPartTypeX86_64 = "4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709"
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

// Options 는 설치 옵션이다.
type Options struct {
	// Disk 는 전체를 지우고 설치할 디스크다(/dev/vda, /dev/nvme0n1 등).
	Disk     string
	Hostname string
	User     string
	// Password 는 표준 입력에서 읽은 값이다. 로그와 출력에 남기지 않는다.
	Password string
	Admin    bool
	// Mode 는 설치 직후 기본 모드(server 또는 desktop)다.
	Mode     string
	Locale   string
	Timezone string
	Keyboard string
	// Console 이 serial 이면 커널과 GRUB 콘솔을 ttyS0 에도 둔다.
	Console string
	// Source 는 복사 원본 디렉터리다. 비어 있으면 라이브 squashfs 마운트를 쓴다.
	Source string
}

var (
	hostnameRE = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
	diskRE     = regexp.MustCompile(`^/dev/(vd[a-z]+|sd[a-z]+|xvd[a-z]+|nvme[0-9]+n[0-9]+|mmcblk[0-9]+)$`)
	localeRE   = regexp.MustCompile(`^[a-z]{2,3}_[A-Z]{2}\.UTF-8$`)
	timezoneRE = regexp.MustCompile(`^[A-Za-z0-9_+-]+(/[A-Za-z0-9_+-]+)*$`)
	keyboardRE = regexp.MustCompile(`^[a-z]{2,8}$`)
)

// Validate 는 옵션 값을 점검한다. 시스템 상태는 보지 않는다.
func (o *Options) Validate() error {
	if !diskRE.MatchString(o.Disk) {
		return errf(CodeInvalidOption, "the disk must be a whole-disk device such as /dev/vda, /dev/sda or /dev/nvme0n1, not %q", o.Disk)
	}
	if !hostnameRE.MatchString(o.Hostname) {
		return errf(CodeInvalidOption, "the host name must be lowercase letters, digits and hyphens (at most 63 characters)")
	}
	if !accounts.ValidName(o.User) {
		return errf(CodeInvalidOption, "the user name must start with a lowercase letter, use only a-z, 0-9, _ and -, and be at most 32 characters (some names are reserved)")
	}
	if !accounts.ValidPassword(o.User, o.Password) {
		return errf(CodeInvalidOption, "the password must be 8 to 128 printable ASCII characters and differ from the user name")
	}
	switch o.Mode {
	case "server", "desktop":
	default:
		return errf(CodeInvalidOption, "the mode must be server or desktop")
	}
	if !localeRE.MatchString(o.Locale) {
		return errf(CodeInvalidOption, "the locale must look like en_US.UTF-8")
	}
	if !timezoneRE.MatchString(o.Timezone) || strings.Contains(o.Timezone, "..") {
		return errf(CodeInvalidOption, "the time zone must look like Asia/Seoul or UTC")
	}
	if !keyboardRE.MatchString(o.Keyboard) {
		return errf(CodeInvalidOption, "the keyboard layout must be a short layout name such as us or kr")
	}
	if o.Console != "" && o.Console != "serial" {
		return errf(CodeInvalidOption, "the console must be empty or serial")
	}
	return nil
}

// Event 는 진행 한 줄이다.
type Event struct {
	Time    string `json:"time"`
	Step    string `json:"step"`
	Message string `json:"message,omitempty"`
}

// Installer 는 설치를 실행한다. 빈 경로는 기본값이다.
type Installer struct {
	Run platform.Runner
	// Out 은 이벤트를 JSON 줄로 받는다. nil 이면 버린다.
	Out io.Writer
	// Now 는 현재 시각이다.
	Now func() time.Time

	// 경로(테스트가 바꾼다).
	SysBlock     string
	ProcMounts   string
	ProcSwaps    string
	TargetRoot   string
	LiveSource   string
	LiveMedium   string
	LogPath      string
	ZoneinfoDir  string
	MinDiskBytes uint64
	// LookPath 는 실행 파일이 있는지 본다. nil 이면 os/exec.LookPath 와 같은 동작이다.
	LookPath func(name string) bool
	// PartitionWait 는 파티션 장치 노드가 나타나길 기다리는 시간이다. 0 이면 15초다.
	PartitionWait time.Duration
	// NodeExists 는 장치 노드가 있는지 본다. nil 이면 파일 시스템을 본다. 테스트가 바꾼다.
	NodeExists func(path string) bool
	// EFIDir 은 UEFI 로 부팅했을 때 있는 펌웨어 디렉터리다. 비어 있으면 /sys/firmware/efi 다. 테스트가 바꾼다.
	EFIDir string

	log     []string
	mounted []string
}

func (in *Installer) sysBlock() string { return orDefault(in.SysBlock, "/sys/block") }
func (in *Installer) procMounts() string {
	return orDefault(in.ProcMounts, "/proc/self/mounts")
}
func (in *Installer) procSwaps() string   { return orDefault(in.ProcSwaps, "/proc/swaps") }
func (in *Installer) target() string      { return orDefault(in.TargetRoot, defaultTargetRoot) }
func (in *Installer) liveSource() string  { return orDefault(in.LiveSource, defaultLiveSource) }
func (in *Installer) liveMedium() string  { return orDefault(in.LiveMedium, defaultLiveMedium) }
func (in *Installer) logPath() string     { return orDefault(in.LogPath, defaultInstallLog) }
func (in *Installer) zoneinfoDir() string { return orDefault(in.ZoneinfoDir, "/usr/share/zoneinfo") }
func (in *Installer) efiDir() string      { return orDefault(in.EFIDir, "/sys/firmware/efi") }

func (in *Installer) minDisk() uint64 {
	if in.MinDiskBytes > 0 {
		return in.MinDiskBytes
	}
	return defaultMinDisk
}

func orDefault(v, d string) string {
	if v != "" {
		return v
	}
	return d
}

func (in *Installer) now() time.Time {
	if in.Now != nil {
		return in.Now()
	}
	return time.Now()
}

// event 는 진행을 JSON 줄로 내보내고 메모리 로그에 쌓는다. 비밀번호는 여기에 들어오지 않는다.
func (in *Installer) event(step, format string, a ...any) {
	ev := Event{Time: in.now().UTC().Format(time.RFC3339), Step: step, Message: fmt.Sprintf(format, a...)}
	in.log = append(in.log, fmt.Sprintf("%s [%s] %s", ev.Time, ev.Step, ev.Message))
	if in.Out != nil {
		b, _ := json.Marshal(ev)
		_, _ = in.Out.Write(append(b, '\n'))
	}
}

func (in *Installer) have(name string) bool {
	if in.LookPath != nil {
		return in.LookPath(name)
	}
	for _, dir := range filepath.SplitList(os.Getenv("PATH")) {
		if fi, err := os.Stat(filepath.Join(dir, name)); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
			return true
		}
	}
	return false
}

// cmd 는 명령을 실행하고 0 이 아닌 종료 코드를 코드 있는 오류로 바꾼다.
func (in *Installer) cmd(ctx context.Context, code string, timeout time.Duration, stdin []byte, name string, args ...string) (string, error) {
	res, err := in.Run.Run(ctx, platform.Command{Name: name, Args: args, Stdin: stdin, Timeout: timeout, MaxOutput: 2 << 20,
		Env: []string{"LC_ALL=C", "DEBIAN_FRONTEND=noninteractive"}})
	if err != nil {
		return "", errf(code, "%s: %v", name, err)
	}
	if res.ExitCode != 0 {
		return string(res.Stdout), errf(code, "%s exited %d: %s", name, res.ExitCode, tail(string(res.Stderr)+"\n"+string(res.Stdout), 500))
	}
	return string(res.Stdout), nil
}

// chroot 는 대상 루트 안에서 명령을 실행한다.
func (in *Installer) chroot(ctx context.Context, code string, timeout time.Duration, stdin []byte, name string, args ...string) (string, error) {
	return in.cmd(ctx, code, timeout, stdin, "chroot", append([]string{in.target(), name}, args...)...)
}

func tail(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return "..." + s[len(s)-n:]
	}
	return s
}

// partition 은 디스크의 n 번째 파티션 장치 이름이다. nvme 와 mmcblk 는 p 를 붙인다.
func partition(disk string, n int) string {
	base := filepath.Base(disk)
	if strings.HasPrefix(base, "nvme") || strings.HasPrefix(base, "mmcblk") {
		return fmt.Sprintf("%sp%d", disk, n)
	}
	return fmt.Sprintf("%s%d", disk, n)
}

// requiredTools 는 호스트(라이브 시스템)에서 직접 실행하는 도구다.
var requiredTools = []string{"sfdisk", "mkfs.vfat", "mkfs.ext4", "rsync", "chroot", "blkid", "mount", "umount", "udevadm", "wipefs"}

// Preflight 는 옵션과 시스템 상태를 확인한다. 디스크를 바꾸지 않는다.
func (in *Installer) Preflight(o *Options) error {
	if err := o.Validate(); err != nil {
		return err
	}
	if _, err := os.Stat(in.zoneinfoDir() + "/" + o.Timezone); err != nil {
		return errf(CodeInvalidOption, "the time zone %q is not known on this system", o.Timezone)
	}
	for _, t := range requiredTools {
		if !in.have(t) {
			return errf(CodeMissingTool, "%s is needed but was not found", t)
		}
	}
	if err := in.checkDisk(o.Disk); err != nil {
		return err
	}
	if _, err := os.Stat(in.sourceDir(o)); err != nil {
		if o.Source == "" {
			if _, ferr := os.Stat(in.squashfsFile()); ferr == nil {
				return nil // Install 이 이 파일을 직접 마운트한다.
			}
		}
		return errf(CodeSourceMissing, "the copy source %s was not found; run this from a live session", in.sourceDir(o))
	}
	return nil
}

// squashfsFile 은 라이브 매체의 squashfs 이미지 파일이다. live-boot 가 마운트 지점을 만들지 않은 경우의 대체 원본이다.
func (in *Installer) squashfsFile() string {
	return filepath.Join(in.liveMedium(), "live", "filesystem.squashfs")
}

func (in *Installer) sourceDir(o *Options) string {
	if o.Source != "" {
		return o.Source
	}
	return in.liveSource()
}

// checkDisk 는 디스크가 있고 쓸 수 있고 충분히 크며 쓰이지 않는지 본다.
func (in *Installer) checkDisk(disk string) error {
	name := filepath.Base(disk)
	sizeRaw, err := os.ReadFile(filepath.Join(in.sysBlock(), name, "size"))
	if err != nil {
		return errf(CodeDiskNotFound, "%s was not found on this system", disk)
	}
	var sectors uint64
	if _, err := fmt.Sscanf(strings.TrimSpace(string(sizeRaw)), "%d", &sectors); err != nil {
		return errf(CodeDiskNotFound, "cannot read the size of %s", disk)
	}
	if ro, err := os.ReadFile(filepath.Join(in.sysBlock(), name, "ro")); err == nil && strings.TrimSpace(string(ro)) == "1" {
		return errf(CodeDiskReadOnly, "%s is read-only", disk)
	}
	if size := sectors * 512; size < in.minDisk() {
		return errf(CodeDiskTooSmall, "%s is %d GiB; at least %d GiB is needed", disk, size>>30, in.minDisk()>>30)
	}
	if busy := in.mountedFrom(disk); busy != "" {
		return errf(CodeDiskBusy, "%s is in use (%s); the disk holding the live medium or a mounted file system cannot be erased", disk, busy)
	}
	return nil
}

// mountedFrom 은 디스크나 그 파티션이 마운트되어 있으면 마운트 지점(또는 swap)을 돌려준다.
func (in *Installer) mountedFrom(disk string) string {
	matches := func(dev string) bool {
		if dev == disk {
			return true
		}
		if !strings.HasPrefix(dev, disk) {
			return false
		}
		rest := strings.TrimPrefix(dev, disk)
		rest = strings.TrimPrefix(rest, "p")
		if rest == "" {
			return false
		}
		for _, c := range rest {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	if b, err := os.ReadFile(in.procMounts()); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 2 && strings.HasPrefix(f[0], "/dev/") {
				dev := f[0]
				if real, err := filepath.EvalSymlinks(dev); err == nil {
					dev = real
				}
				if matches(dev) {
					return f[1]
				}
			}
		}
	}
	if b, err := os.ReadFile(in.procSwaps()); err == nil {
		for _, line := range strings.Split(string(b), "\n") {
			f := strings.Fields(line)
			if len(f) >= 1 && matches(f[0]) {
				return "swap"
			}
		}
	}
	return ""
}

// Install 은 디스크를 지우고 시스템을 설치한다. 실패하면 마운트를 풀고 코드 있는 오류를 돌려준다.
func (in *Installer) Install(ctx context.Context, o *Options) (err error) {
	if err := in.Preflight(o); err != nil {
		return err
	}
	ctx = context.WithoutCancel(ctx)
	defer func() {
		in.unmountAll(ctx)
		in.writeLog()
	}()

	disk := o.Disk
	esp, root := partition(disk, 1), partition(disk, 2)
	in.event("disk", "erasing %s and creating a GPT with an EFI system partition and a root partition", disk)
	layout := fmt.Sprintf("label: gpt\nsize=%s, type=%s, name=\"EFI System\"\ntype=%s, name=\"opereta-root\"\n", espSize, espPartType, rootPartTypeX86_64)
	if _, err := in.cmd(ctx, CodePartition, 2*time.Minute, nil, "wipefs", "--all", "--force", disk); err != nil {
		return err
	}
	if _, err := in.cmd(ctx, CodePartition, 2*time.Minute, []byte(layout), "sfdisk", "--wipe", "always", "--wipe-partitions", "always", disk); err != nil {
		return err
	}
	_, _ = in.cmd(ctx, CodePartition, time.Minute, nil, "udevadm", "settle", "--timeout=30")
	if err := in.waitForNode(esp); err != nil {
		return err
	}
	if err := in.waitForNode(root); err != nil {
		return err
	}

	in.event("format", "creating the file systems")
	if _, err := in.cmd(ctx, CodeFormat, 2*time.Minute, nil, "mkfs.vfat", "-F", "32", "-n", "LXESP", esp); err != nil {
		return err
	}
	if _, err := in.cmd(ctx, CodeFormat, 10*time.Minute, nil, "mkfs.ext4", "-F", "-q", "-L", "opereta-root", root); err != nil {
		return err
	}

	target := in.target()
	if err := os.MkdirAll(target, 0o755); err != nil {
		return errf(CodeMount, "cannot create %s: %v", target, err)
	}
	in.event("mount", "mounting the target at %s", target)
	if _, err := in.cmd(ctx, CodeMount, time.Minute, nil, "mount", root, target); err != nil {
		return err
	}
	in.mounted = append(in.mounted, target)
	if err := os.MkdirAll(filepath.Join(target, "boot", "efi"), 0o755); err != nil {
		return errf(CodeMount, "%v", err)
	}
	if _, err := in.cmd(ctx, CodeMount, time.Minute, nil, "mount", esp, filepath.Join(target, "boot", "efi")); err != nil {
		return err
	}
	in.mounted = append(in.mounted, filepath.Join(target, "boot", "efi"))

	srcDir := in.sourceDir(o)
	if _, err := os.Stat(srcDir); err != nil && o.Source == "" {
		srcDir = filepath.Join(filepath.Dir(in.target()), "opereta-source")
		if err := os.MkdirAll(srcDir, 0o755); err != nil {
			return errf(CodeSourceMissing, "%v", err)
		}
		in.event("copy", "mounting %s", in.squashfsFile())
		if _, err := in.cmd(ctx, CodeSourceMissing, time.Minute, nil, "mount", "-t", "squashfs", "-o", "loop,ro", in.squashfsFile(), srcDir); err != nil {
			return err
		}
		in.mounted = append(in.mounted, srcDir)
	}
	src := strings.TrimRight(srcDir, "/") + "/"
	in.event("copy", "copying the system from %s", src)
	if _, err := in.cmd(ctx, CodeCopy, 60*time.Minute, nil, "rsync", "-aHAXx", "--numeric-ids", src, target+"/"); err != nil {
		return err
	}

	if err := in.configure(ctx, o, esp, root); err != nil {
		return err
	}
	return nil
}

func (in *Installer) unmountAll(ctx context.Context) {
	for i := len(in.mounted) - 1; i >= 0; i-- {
		_, _ = in.cmd(ctx, CodeMount, time.Minute, nil, "umount", "-R", in.mounted[i])
	}
	in.mounted = nil
}

func (in *Installer) waitForNode(path string) error {
	wait := in.PartitionWait
	if wait <= 0 {
		wait = 15 * time.Second
	}
	deadline := time.Now().Add(wait)
	for {
		if in.NodeExists != nil {
			if in.NodeExists(path) {
				return nil
			}
		} else if _, err := os.Stat(path); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return errf(CodePartition, "the partition %s did not appear after partitioning", path)
		}
		time.Sleep(200 * time.Millisecond)
	}
}

// writeLog 는 진행 기록을 호스트 로그 파일에 남긴다.
func (in *Installer) writeLog() {
	if len(in.log) == 0 {
		return
	}
	text := strings.Join(in.log, "\n") + "\n"
	_ = os.MkdirAll(filepath.Dir(in.logPath()), 0o755)
	_ = os.WriteFile(in.logPath(), []byte(text), 0o600)
}
