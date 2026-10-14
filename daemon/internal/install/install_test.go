package install

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

const testPassword = "correct-horse-9"

func goodOptions() Options {
	return Options{
		Disk: "/dev/vda", Hostname: "opereta", User: "tester", Password: testPassword, Admin: true,
		Mode: "server", Locale: "ko_KR.UTF-8", Timezone: "Asia/Seoul", Keyboard: "kr", Console: "serial",
	}
}

func TestValidate(t *testing.T) {
	if err := func() error { o := goodOptions(); return o.Validate() }(); err != nil {
		t.Fatalf("good options rejected: %v", err)
	}
	mutations := map[string]func(*Options){
		"partition not disk": func(o *Options) { o.Disk = "/dev/vda1" },
		"loop device":        func(o *Options) { o.Disk = "/dev/loop0" },
		"relative disk":      func(o *Options) { o.Disk = "vda" },
		"host upper":         func(o *Options) { o.Hostname = "Opereta" },
		"host underscore":    func(o *Options) { o.Hostname = "my_host" },
		"host hyphen start":  func(o *Options) { o.Hostname = "-host" },
		"reserved user":      func(o *Options) { o.User = "root" },
		"live user":          func(o *Options) { o.User = "user" },
		"user upper":         func(o *Options) { o.User = "Tester" },
		"short password":     func(o *Options) { o.Password = "short" },
		"password equals":    func(o *Options) { o.User, o.Password = "abcdefgh", "abcdefgh" },
		"mode":               func(o *Options) { o.Mode = "both" },
		"locale":             func(o *Options) { o.Locale = "korean" },
		"timezone traversal": func(o *Options) { o.Timezone = "../etc/passwd" },
		"keyboard":           func(o *Options) { o.Keyboard = "us; rm -rf /" },
		"console":            func(o *Options) { o.Console = "usb" },
	}
	for name, mut := range mutations {
		o := goodOptions()
		mut(&o)
		var ie *Error
		if err := o.Validate(); !errors.As(err, &ie) || ie.Code != CodeInvalidOption {
			t.Errorf("%s: error = %v", name, err)
		}
	}
	for _, disk := range []string{"/dev/sda", "/dev/nvme0n1", "/dev/mmcblk0", "/dev/xvda"} {
		o := goodOptions()
		o.Disk = disk
		if err := o.Validate(); err != nil {
			t.Errorf("%s rejected: %v", disk, err)
		}
	}
}

func TestPartitionNames(t *testing.T) {
	for disk, want := range map[string][2]string{
		"/dev/vda":     {"/dev/vda1", "/dev/vda2"},
		"/dev/nvme0n1": {"/dev/nvme0n1p1", "/dev/nvme0n1p2"},
		"/dev/mmcblk0": {"/dev/mmcblk0p1", "/dev/mmcblk0p2"},
	} {
		if partition(disk, 1) != want[0] || partition(disk, 2) != want[1] {
			t.Errorf("%s: %s %s", disk, partition(disk, 1), partition(disk, 2))
		}
	}
}

// sysfs 는 가짜 /sys/block 과 /proc 파일을 만든다.
func fakeSystem(t *testing.T, name string, sizeBytes uint64, ro bool) *Installer {
	t.Helper()
	dir := t.TempDir()
	blk := filepath.Join(dir, "sys-block", name)
	if err := os.MkdirAll(blk, 0o755); err != nil {
		t.Fatal(err)
	}
	write := func(path, body string) {
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(blk, "size"), itoa(sizeBytes/512)+"\n")
	if ro {
		write(filepath.Join(blk, "ro"), "1\n")
	} else {
		write(filepath.Join(blk, "ro"), "0\n")
	}
	write(filepath.Join(dir, "mounts"), "proc /proc proc rw 0 0\n/dev/sr0 /run/live/medium iso9660 ro 0 0\n")
	write(filepath.Join(dir, "swaps"), "Filename\tType\tSize\tUsed\tPriority\n")
	if err := os.MkdirAll(filepath.Join(dir, "live-src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "zoneinfo", "Asia"), 0o755); err != nil {
		t.Fatal(err)
	}
	write(filepath.Join(dir, "zoneinfo", "Asia", "Seoul"), "tz")
	return &Installer{
		SysBlock:    filepath.Join(dir, "sys-block"),
		ProcMounts:  filepath.Join(dir, "mounts"),
		ProcSwaps:   filepath.Join(dir, "swaps"),
		TargetRoot:  filepath.Join(dir, "target"),
		LiveSource:  filepath.Join(dir, "live-src"),
		LogPath:     filepath.Join(dir, "install.log"),
		ZoneinfoDir: filepath.Join(dir, "zoneinfo"),
		LookPath:    func(string) bool { return true },
		NodeExists:  func(string) bool { return true },
		Now:         func() time.Time { return time.Date(2026, 10, 12, 8, 0, 0, 0, time.UTC) },
	}
}

func itoa(n uint64) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

func TestPreflightDiskChecks(t *testing.T) {
	code := func(err error) string {
		var ie *Error
		if errors.As(err, &ie) {
			return ie.Code
		}
		return "none"
	}
	good := goodOptions()

	in := fakeSystem(t, "vda", 32<<30, false)
	if err := in.Preflight(&good); err != nil {
		t.Fatalf("good system rejected: %v", err)
	}

	in = fakeSystem(t, "vda", 8<<30, false)
	if got := code(in.Preflight(&good)); got != CodeDiskTooSmall {
		t.Errorf("small disk: %s", got)
	}

	in = fakeSystem(t, "vda", 32<<30, true)
	if got := code(in.Preflight(&good)); got != CodeDiskReadOnly {
		t.Errorf("read-only: %s", got)
	}

	in = fakeSystem(t, "vdb", 32<<30, false)
	if got := code(in.Preflight(&good)); got != CodeDiskNotFound {
		t.Errorf("missing disk: %s", got)
	}

	// 마운트된 파티션이 있는 디스크는 거절한다(라이브 USB 가 이 디스크에 있는 경우).
	in = fakeSystem(t, "sda", 32<<30, false)
	if err := os.WriteFile(in.ProcMounts, []byte("/dev/sda1 /run/live/medium vfat ro 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	usb := goodOptions()
	usb.Disk = "/dev/sda"
	if got := code(in.Preflight(&usb)); got != CodeDiskBusy {
		t.Errorf("busy disk: %s", got)
	}
	// 다른 디스크의 마운트는 상관없다.
	other := goodOptions()
	in = fakeSystem(t, "sdb", 32<<30, false)
	other.Disk = "/dev/sdb"
	if err := os.WriteFile(in.ProcMounts, []byte("/dev/sda1 /run/live/medium vfat ro 0 0\n/dev/sdb2x /x ext4 rw 0 0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := in.Preflight(&other); err != nil {
		t.Errorf("unrelated mount blocked the install: %v", err)
	}
	// swap 으로 쓰이는 디스크도 거절한다.
	in = fakeSystem(t, "nvme0n1", 32<<30, false)
	if err := os.WriteFile(in.ProcSwaps, []byte("Filename Type Size Used Priority\n/dev/nvme0n1p3 partition 1 0 -2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	nv := goodOptions()
	nv.Disk = "/dev/nvme0n1"
	if got := code(in.Preflight(&nv)); got != CodeDiskBusy {
		t.Errorf("swap disk: %s", got)
	}

	in = fakeSystem(t, "vda", 32<<30, false)
	in.LookPath = func(name string) bool { return name != "rsync" }
	if got := code(in.Preflight(&good)); got != CodeMissingTool {
		t.Errorf("missing tool: %s", got)
	}

	in = fakeSystem(t, "vda", 32<<30, false)
	in.LiveSource = filepath.Join(t.TempDir(), "missing")
	if got := code(in.Preflight(&good)); got != CodeSourceMissing {
		t.Errorf("missing source: %s", got)
	}
}

// recorder 는 실행한 명령을 기록하고 명령별 응답을 돌려준다.
type recorder struct {
	calls   []platform.Command
	failOn  string
	chrootR func(args []string) platform.Result
}

func (r *recorder) Run(_ context.Context, cmd platform.Command) (platform.Result, error) {
	r.calls = append(r.calls, cmd)
	line := cmd.Name + " " + strings.Join(cmd.Args, " ")
	if r.failOn != "" && strings.Contains(line, r.failOn) {
		return platform.Result{ExitCode: 1, Stderr: []byte("boom")}, nil
	}
	switch cmd.Name {
	case "blkid":
		dev := cmd.Args[len(cmd.Args)-1]
		return platform.Result{Stdout: []byte("uuid-of-" + filepath.Base(dev) + "\n")}, nil
	case "chroot":
		if r.chrootR != nil {
			if res := r.chrootR(cmd.Args); res.Stdout != nil || res.ExitCode != 0 {
				return res, nil
			}
		}
		rest := cmd.Args[1:]
		switch rest[0] {
		case "/usr/lib/opereta/opereta-user":
			return platform.Result{Stdout: []byte("OPERETA_USER_RESULT=ok\n")}, nil
		case "dpkg-query":
			joined := strings.Join(rest, " ")
			switch {
			case strings.Contains(joined, "live-boot*"):
				return platform.Result{Stdout: []byte("live-boot\nlive-config\nlive-config-systemd\n"), ExitCode: 1}, nil
			case strings.Contains(joined, "${Status}"):
				return platform.Result{Stdout: []byte("install ok installed")}, nil
			case strings.Contains(joined, "${db:Status-Status}"):
				return platform.Result{Stdout: []byte("installed")}, nil
			}
		}
	}
	return platform.Result{}, nil
}

func (r *recorder) lines() []string {
	var out []string
	for _, c := range r.calls {
		out = append(out, c.Name+" "+strings.Join(c.Args, " "))
	}
	return out
}

func indexOf(lines []string, sub string) int {
	for i, l := range lines {
		if strings.Contains(l, sub) {
			return i
		}
	}
	return -1
}

func TestInstallRunsStepsInOrderAndWritesConfiguration(t *testing.T) {
	in := fakeSystem(t, "vda", 32<<30, false)
	rec := &recorder{}
	// update-grub 가 grub.cfg 를 만든 것처럼 흉내 낸다.
	rec.chrootR = func(args []string) platform.Result {
		if len(args) >= 2 && args[1] == "update-grub" {
			dir := filepath.Join(args[0], "boot/grub")
			_ = os.MkdirAll(dir, 0o755)
			cfg := "menuentry 'x' $menuentry_id_option 'gnulinux-6.12.111+deb13-amd64-advanced-fakeuuid' {\n}\n"
			_ = os.WriteFile(filepath.Join(dir, "grub.cfg"), []byte(cfg), 0o644)
		}
		return platform.Result{}
	}
	in.Run = rec
	var out bytes.Buffer
	in.Out = &out
	o := goodOptions()

	if err := in.Install(context.Background(), &o); err != nil {
		t.Fatalf("Install: %v", err)
	}
	lines := rec.lines()
	order := []string{
		"wipefs --all --force /dev/vda",
		"sfdisk --wipe always --wipe-partitions always /dev/vda",
		"mkfs.vfat -F 32 -n LXESP /dev/vda1",
		"mkfs.ext4 -F -q -L opereta-root /dev/vda2",
		"mount /dev/vda2 " + in.TargetRoot,
		"mount /dev/vda1 " + in.TargetRoot + "/boot/efi",
		"rsync -aHAXx --numeric-ids " + in.LiveSource + "/ " + in.TargetRoot + "/",
		"systemd-machine-id-setup --root=" + in.TargetRoot,
		"locale-gen",
		"/usr/lib/opereta/opereta-user add tester --admin",
		"apt-get -y -q purge live-boot live-config live-config-systemd",
		"systemctl --root=" + in.TargetRoot + " set-default multi-user.target",
		"update-initramfs -u -k all",
		"grub-install --target=x86_64-efi --efi-directory=/boot/efi --bootloader-id=opereta --uefi-secure-boot",
		"grub-install --target=x86_64-efi --efi-directory=/boot/efi --removable --uefi-secure-boot",
		"update-grub",
		"grub-set-default gnulinux-6.12.111+deb13-amd64-advanced-fakeuuid",
	}
	last := -1
	for _, want := range order {
		i := indexOf(lines, want)
		if i < 0 {
			t.Fatalf("command %q was not run.\nran:\n%s", want, strings.Join(lines, "\n"))
		}
		if i < last {
			t.Errorf("command %q ran out of order (index %d before %d)", want, i, last)
		}
		last = i
	}

	// 파티션 표는 GPT 이고 EFI 와 루트 타입이 맞다.
	for _, c := range rec.calls {
		if c.Name == "sfdisk" {
			script := string(c.Stdin)
			for _, want := range []string{"label: gpt", "size=1GiB, type=U", "type=4F68BCE3-E8CD-4DB1-96E7-FBCAF984B709"} {
				if !strings.Contains(script, want) {
					t.Errorf("sfdisk script misses %q: %s", want, script)
				}
			}
		}
	}

	read := func(rel string) string {
		b, err := os.ReadFile(filepath.Join(in.TargetRoot, rel))
		if err != nil {
			t.Fatalf("read %s: %v", rel, err)
		}
		return string(b)
	}
	fstab := read("etc/fstab")
	if !strings.Contains(fstab, "UUID=uuid-of-vda2 / ext4") || !strings.Contains(fstab, "UUID=uuid-of-vda1 /boot/efi vfat") {
		t.Errorf("fstab = %q", fstab)
	}
	if read("etc/hostname") != "opereta\n" || !strings.Contains(read("etc/hosts"), "127.0.1.1\topereta") {
		t.Error("host name files are wrong")
	}
	if read("etc/default/locale") != "LANG=ko_KR.UTF-8\n" || !strings.Contains(read("etc/locale.gen"), "ko_KR.UTF-8 UTF-8") {
		t.Error("locale files are wrong")
	}
	if read("etc/timezone") != "Asia/Seoul\n" {
		t.Error("timezone file is wrong")
	}
	if !strings.Contains(read("etc/default/keyboard"), `XKBLAYOUT="kr"`) {
		t.Error("keyboard file is wrong")
	}
	if !strings.Contains(read("etc/default/grub.d/30-opereta-console.cfg"), "console=ttyS0,115200") {
		t.Error("serial console drop-in is missing")
	}
	if link, err := os.Readlink(filepath.Join(in.TargetRoot, "etc/localtime")); err != nil || link != "/usr/share/zoneinfo/Asia/Seoul" {
		t.Errorf("localtime = %q, %v", link, err)
	}

	// 비밀번호는 표준 입력으로만 가고 어디에도 남지 않는다.
	for _, c := range rec.calls {
		if strings.Contains(strings.Join(c.Args, " "), testPassword) || strings.Contains(strings.Join(c.Env, " "), testPassword) {
			t.Errorf("password leaked into %s", c.Name)
		}
	}
	sawStdin := false
	for _, c := range rec.calls {
		if strings.Contains(string(c.Stdin), testPassword) {
			sawStdin = true
			if c.Name != "chroot" || !strings.Contains(strings.Join(c.Args, " "), "opereta-user add") {
				t.Errorf("password sent to %s", c.Name)
			}
		}
	}
	if !sawStdin {
		t.Error("the password never reached the account helper")
	}
	if strings.Contains(out.String(), testPassword) {
		t.Error("password in event output")
	}
	if b, err := os.ReadFile(in.LogPath); err != nil || strings.Contains(string(b), testPassword) {
		t.Errorf("install log: %v", err)
	}

	// 마운트는 역순으로 모두 풀린다.
	var umounts []string
	for _, l := range lines {
		if strings.HasPrefix(l, "umount") {
			umounts = append(umounts, l)
		}
	}
	if len(umounts) == 0 || !strings.HasSuffix(umounts[len(umounts)-1], in.TargetRoot) {
		t.Errorf("umount sequence = %v", umounts)
	}
}

func TestInstallFailureStopsAndUnmounts(t *testing.T) {
	in := fakeSystem(t, "vda", 32<<30, false)
	rec := &recorder{failOn: "rsync"}
	in.Run = rec
	o := goodOptions()
	err := in.Install(context.Background(), &o)
	var ie *Error
	if !errors.As(err, &ie) || ie.Code != CodeCopy {
		t.Fatalf("error = %v", err)
	}
	lines := rec.lines()
	if indexOf(lines, "update-initramfs") >= 0 || indexOf(lines, "grub-install") >= 0 {
		t.Error("later steps ran after the copy failed")
	}
	if indexOf(lines, "umount -R "+in.TargetRoot) < 0 {
		t.Errorf("the target was not unmounted: %v", lines)
	}
}

func TestInstallRefusesBeforeTouchingTheDisk(t *testing.T) {
	in := fakeSystem(t, "vda", 4<<30, false)
	rec := &recorder{}
	in.Run = rec
	o := goodOptions()
	if err := in.Install(context.Background(), &o); err == nil {
		t.Fatal("a too small disk was accepted")
	}
	if len(rec.calls) != 0 {
		t.Errorf("commands ran before the checks passed: %v", rec.lines())
	}
}

func TestPrepareTargetPurgesInstallerAndVerifiesProtectedPackages(t *testing.T) {
	in := fakeSystem(t, "vda", 32<<30, false)
	rec := &recorder{}
	in.Run = rec
	root := t.TempDir()
	err := in.PrepareTarget(context.Background(), root, PrepareOptions{Mode: "desktop", RegisterUser: "alice", Admin: true})
	if err != nil {
		t.Fatal(err)
	}
	lines := rec.lines()
	if indexOf(lines, "apt-get -y -q purge --autoremove calamares opereta-installer") < 0 {
		t.Errorf("installer was not purged: %v", lines)
	}
	if indexOf(lines, "systemctl --root="+root+" set-default graphical.target") < 0 {
		t.Errorf("mode was not set: %v", lines)
	}
	b, err := os.ReadFile(filepath.Join(root, "var/lib/opereta/users.list"))
	if err != nil || !strings.HasPrefix(string(b), "alice 1 2026-10-12T08:00:00Z") {
		t.Errorf("users.list = %q, %v", b, err)
	}

	// 보호 패키지가 없으면 실패한다.
	rec2 := &recorder{chrootR: func(args []string) platform.Result {
		if strings.Contains(strings.Join(args, " "), "${db:Status-Status}") && strings.HasSuffix(strings.Join(args, " "), "opereta-daemon") {
			return platform.Result{Stdout: []byte("not-installed")}
		}
		return platform.Result{}
	}}
	in.Run = rec2
	err = in.PrepareTarget(context.Background(), t.TempDir(), PrepareOptions{})
	var ie *Error
	if !errors.As(err, &ie) || ie.Code != CodeDamagedSystem {
		t.Errorf("damaged system error = %v", err)
	}
}

func TestPrepareTargetKeepInstaller(t *testing.T) {
	in := fakeSystem(t, "vda", 32<<30, false)
	rec := &recorder{}
	in.Run = rec
	if err := in.PrepareTarget(context.Background(), t.TempDir(), PrepareOptions{KeepInstaller: true}); err != nil {
		t.Fatal(err)
	}
	if indexOf(rec.lines(), "--autoremove") >= 0 {
		t.Error("the installer was purged although KeepInstaller was set")
	}
}

func TestBootDefaultPicksNewestKernel(t *testing.T) {
	in := fakeSystem(t, "vda", 32<<30, false)
	rec := &recorder{}
	in.Run = rec
	root := t.TempDir()
	cfg := filepath.Join(root, "boot/grub")
	if err := os.MkdirAll(cfg, 0o755); err != nil {
		t.Fatal(err)
	}
	grub := "menuentry 'a' $menuentry_id_option 'gnulinux-6.12.9+deb13-amd64-advanced-uuid' {\n}\n" +
		"menuentry 'b' $menuentry_id_option 'gnulinux-6.12.10+deb13-amd64-advanced-uuid' {\n}\n"
	if err := os.WriteFile(filepath.Join(cfg, "grub.cfg"), []byte(grub), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := in.BootDefault(context.Background(), root); err != nil {
		t.Fatal(err)
	}
	if indexOf(rec.lines(), "grub-set-default gnulinux-6.12.10+deb13-amd64-advanced-uuid") < 0 {
		t.Errorf("calls = %v", rec.lines())
	}
	if err := in.BootDefault(context.Background(), t.TempDir()); err == nil {
		t.Error("a missing grub.cfg was accepted")
	}
}

func TestInstallFallsBackToMountingTheSquashfsFile(t *testing.T) {
	in := fakeSystem(t, "vda", 32<<30, false)
	rec := &recorder{}
	rec.chrootR = func(args []string) platform.Result {
		if len(args) >= 2 && args[1] == "update-grub" {
			dir := filepath.Join(args[0], "boot/grub")
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "grub.cfg"), []byte("menuentry 'x' $menuentry_id_option 'gnulinux-6.12.1-amd64-advanced-u' {\n}\n"), 0o644)
		}
		return platform.Result{}
	}
	in.Run = rec
	// live-boot 의 마운트 지점은 없고 매체의 squashfs 파일만 있다.
	in.LiveSource = filepath.Join(t.TempDir(), "missing")
	medium := t.TempDir()
	in.LiveMedium = medium
	if err := os.MkdirAll(filepath.Join(medium, "live"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(medium, "live", "filesystem.squashfs"), []byte("sq"), 0o644); err != nil {
		t.Fatal(err)
	}
	o := goodOptions()
	if err := in.Install(context.Background(), &o); err != nil {
		t.Fatalf("Install: %v", err)
	}
	lines := rec.lines()
	mountIdx := indexOf(lines, "mount -t squashfs -o loop,ro "+filepath.Join(medium, "live", "filesystem.squashfs"))
	rsyncIdx := indexOf(lines, "rsync -aHAXx")
	if mountIdx < 0 || rsyncIdx < mountIdx {
		t.Errorf("squashfs was not mounted before rsync: %v", lines)
	}
}

func TestGrubInstallFallsBackToNoNVRAM(t *testing.T) {
	in := fakeSystem(t, "vda", 32<<30, false)
	rec := &recorder{}
	calls := 0
	rec.chrootR = func(args []string) platform.Result {
		if len(args) >= 2 && args[1] == "update-grub" {
			dir := filepath.Join(args[0], "boot/grub")
			_ = os.MkdirAll(dir, 0o755)
			_ = os.WriteFile(filepath.Join(dir, "grub.cfg"), []byte("menuentry 'x' $menuentry_id_option 'gnulinux-6.12.1-amd64-advanced-u' {\n}\n"), 0o644)
		}
		if len(args) >= 2 && args[1] == "grub-install" && strings.Contains(strings.Join(args, " "), "--bootloader-id=opereta") && !strings.Contains(strings.Join(args, " "), "--no-nvram") {
			calls++
			return platform.Result{ExitCode: 1, Stderr: []byte("efibootmgr failed")}
		}
		return platform.Result{}
	}
	in.Run = rec
	o := goodOptions()
	if err := in.Install(context.Background(), &o); err != nil {
		t.Fatalf("Install: %v", err)
	}
	if calls != 1 || indexOf(rec.lines(), "--bootloader-id=opereta --uefi-secure-boot --no-nvram") < 0 {
		t.Errorf("no-nvram retry missing (calls=%d): %v", calls, rec.lines())
	}
}
