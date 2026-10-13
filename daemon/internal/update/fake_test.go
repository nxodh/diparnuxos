package update

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// fakePkg 는 가짜 시스템의 설치된 패키지 하나다.
type fakePkg struct {
	version string
	source  string
}

// fakeSys 는 apt, dpkg, dkms, grub, modinfo 를 흉내 내는 platform.Runner 다.
// 실제 패키지 관리자의 모든 동작을 재현하지 않고 갱신 엔진이 쓰는 명령과 출력 형식만 구현한다.
type fakeSys struct {
	t    *testing.T
	root string

	pkgs map[string]*fakePkg
	held map[string]bool
	// cand 는 설치된 패키지의 후보 버전이다. 같으면 올릴 것이 없다.
	cand map[string]string
	// newKernels 는 설치 가능한 새 커널(릴리스 → 패키지 버전)이다.
	newKernels map[string]string
	// modules 는 커널 릴리스별로 빌드된 드라이버 버전이다. 비어 있으면 빌드되지 않았다.
	modules map[string]string
	running string

	// 실패 주입이다.
	dkmsFail       map[string]bool // 이 커널은 DKMS 빌드가 실패한다.
	upgradeFail    bool            // dist-upgrade 가 실패한다.
	updateFail     bool            // apt-get update 가 실패한다.
	oldUnavailable bool            // 이전 버전 설치가 실패한다.
	removeOnUpg    []string        // dist-upgrade 시뮬레이션이 지우겠다고 하는 패키지.

	savedEntry string
	nextEntry  string
	calls      []string
	rebooted   bool
	gpuExit    int
}

func newFakeSys(t *testing.T) *fakeSys {
	t.Helper()
	f := &fakeSys{
		t:          t,
		root:       t.TempDir(),
		pkgs:       map[string]*fakePkg{},
		held:       map[string]bool{},
		cand:       map[string]string{},
		newKernels: map[string]string{},
		modules:    map[string]string{},
		dkmsFail:   map[string]bool{},
		running:    "6.12.111+deb13-amd64",
		gpuExit:    1,
	}
	add := func(name, ver, src string, hold bool) {
		f.pkgs[name] = &fakePkg{version: ver, source: src}
		if hold {
			f.held[name] = true
		}
	}
	add("linux-image-amd64", "6.12.111-1", "linux-signed-amd64", true)
	add("linux-headers-amd64", "6.12.111-1", "linux", true)
	add("linux-image-6.12.111+deb13-amd64", "6.12.111-1", "linux-signed-amd64", true)
	add("linux-headers-6.12.111+deb13-amd64", "6.12.111-1", "linux", true)
	add("nvidia-kernel-open-dkms", "595.91.07-1", "nvidia-graphics-drivers", true)
	add("nvidia-driver", "595.91.07-1", "nvidia-graphics-drivers", true)
	add("diparnux-nvidia", "0.3.0", "diparnux-nvidia", false)
	add("bash", "5.2.37-2", "bash", false)
	f.modules["6.12.111+deb13-amd64"] = "595.91.07"

	for _, d := range []string{"boot/grub", "etc/apt/preferences.d", "state", "run", "proc"} {
		if err := os.MkdirAll(filepath.Join(f.root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	f.writeInitrd("6.12.111+deb13-amd64")
	f.writeGrub()
	f.savedEntry = "gnulinux-6.12.111+deb13-amd64-advanced-uuid"
	f.writeCmdline()
	return f
}

func (f *fakeSys) path(p string) string { return filepath.Join(f.root, p) }

func (f *fakeSys) engine() *Engine {
	return &Engine{
		Cfg: Config{
			StateDir:    f.path("state"),
			GrubCfg:     f.path("boot/grub/grub.cfg"),
			ProcCmdline: f.path("proc/cmdline"),
			ProcNvidia:  f.path("proc/nvidia-version"),
			LiveDir:     f.path("run/live"),
			PrefsDir:    f.path("etc/apt/preferences.d"),
			BootDir:     f.path("boot"),
			RootDir:     f.root,
			TmpDir:      f.root,
		},
		Run:       f,
		FreeBytes: func(string) (uint64, error) { return 100 << 30, nil },
	}
}

func (f *fakeSys) writeInitrd(rel string) {
	if err := os.WriteFile(f.path("boot/initrd.img-"+rel), []byte("initrd"), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeSys) writeCmdline() {
	line := fmt.Sprintf("BOOT_IMAGE=/boot/vmlinuz-%s root=/dev/vda2 ro quiet\n", f.running)
	if err := os.WriteFile(f.path("proc/cmdline"), []byte(line), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

// kernels 는 설치된 커널 릴리스를 정렬해 돌려준다.
func (f *fakeSys) kernels() []string {
	var out []string
	for n := range f.pkgs {
		if rel := kernelRelease(n); rel != "" {
			out = append(out, rel)
		}
	}
	sort.Strings(out)
	return out
}

func (f *fakeSys) writeGrub() {
	var b strings.Builder
	for _, rel := range f.kernels() {
		fmt.Fprintf(&b, "menuentry 'Diparnux, with Linux %s' --class debian $menuentry_id_option 'gnulinux-%s-advanced-uuid' {\n}\n", rel, rel)
	}
	if err := os.WriteFile(f.path("boot/grub/grub.cfg"), []byte(b.String()), 0o644); err != nil {
		f.t.Fatal(err)
	}
}

func (f *fakeSys) heldNames() []string {
	var out []string
	for n := range f.held {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

func (f *fakeSys) has(call string) bool {
	for _, c := range f.calls {
		if strings.Contains(c, call) {
			return true
		}
	}
	return false
}

func (f *fakeSys) driverVersion() string {
	return upstreamVersion(f.pkgs["nvidia-kernel-open-dkms"].version)
}

func out(s string) platform.Result { return platform.Result{Stdout: []byte(s)} }

func fail(code int, stderr string) platform.Result {
	return platform.Result{ExitCode: code, Stderr: []byte(stderr)}
}

// Run 은 platform.Runner 를 구현한다.
func (f *fakeSys) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	line := cmd.Name + " " + strings.Join(cmd.Args, " ")
	f.calls = append(f.calls, line)
	args := cmd.Args
	switch cmd.Name {
	case "dpkg-query":
		var names []string
		for n := range f.pkgs {
			names = append(names, n)
		}
		sort.Strings(names)
		var b strings.Builder
		for _, n := range names {
			p := f.pkgs[n]
			fmt.Fprintf(&b, "%s\t%s\t%s\tinstalled\n", n, p.source, p.version)
		}
		return out(b.String()), nil
	case "apt-mark":
		switch args[0] {
		case "showhold":
			return out(strings.Join(f.heldNames(), "\n") + "\n"), nil
		case "hold":
			for _, n := range args[1:] {
				f.held[n] = true
			}
		}
		return out(""), nil
	case "uname":
		return out(f.running + "\n"), nil
	case "apt-get":
		return f.apt(args), nil
	case "dkms":
		return f.dkms(args), nil
	case "dpkg":
		for _, a := range args {
			if strings.HasPrefix(a, "linux-") {
				f.removePkg(a)
			}
		}
		return out(""), nil
	case "modinfo":
		return f.modinfo(args), nil
	case "grub-editenv":
		if len(args) > 0 && args[0] == "list" {
			s := ""
			if f.savedEntry != "" {
				s += "saved_entry=" + f.savedEntry + "\n"
			}
			if f.nextEntry != "" {
				s += "next_entry=" + f.nextEntry + "\n"
			}
			return out(s), nil
		}
		f.nextEntry = ""
		return out(""), nil
	case "grub-set-default":
		f.savedEntry = args[0]
		return out(""), nil
	case "grub-reboot":
		f.nextEntry = args[0]
		return out(""), nil
	case "update-grub":
		f.writeGrub()
		return out(""), nil
	case "systemctl":
		if len(args) > 0 && args[0] == "reboot" {
			f.rebooted = true
		}
		return out(""), nil
	}
	if strings.HasSuffix(cmd.Name, "diparnux-gpu-check") {
		return platform.Result{ExitCode: f.gpuExit}, nil
	}
	f.t.Fatalf("unexpected command: %s", line)
	return platform.Result{}, nil
}

func (f *fakeSys) removePkg(name string) {
	delete(f.pkgs, name)
	delete(f.held, name)
	if rel := kernelRelease(name); rel != "" {
		delete(f.modules, rel)
		_ = os.Remove(f.path("boot/initrd.img-" + rel))
	}
}

func (f *fakeSys) dkms(args []string) platform.Result {
	if len(args) > 0 && args[0] == "status" {
		return out("")
	}
	return out("")
}

func (f *fakeSys) modinfo(args []string) platform.Result {
	// modinfo -k REL -F FIELD MODULE
	rel, field, mod := args[1], args[3], args[4]
	ver, ok := f.modules[rel]
	if !ok || ver == "" {
		return fail(1, "modinfo: ERROR: Module "+mod+" not found.")
	}
	switch field {
	case "filename":
		return out("/lib/modules/" + rel + "/updates/dkms/" + mod + ".ko.xz\n")
	case "version":
		return out(ver + "\n")
	case "license":
		return out("Dual MIT/GPL\n")
	}
	return out("")
}

// apt 는 apt-get 의 하위 명령을 처리한다.
func (f *fakeSys) apt(args []string) platform.Result {
	simulate := false
	var verb string
	var rest []string
	skip := false
	for i, a := range args {
		if skip {
			skip = false
			continue
		}
		switch {
		case a == "-s":
			simulate = true
		case a == "-o":
			skip = true
		case strings.HasPrefix(a, "-"):
		case verb == "":
			verb = a
		default:
			rest = append(rest, a)
		}
		_ = i
	}
	switch verb {
	case "update":
		if f.updateFail {
			return fail(100, "W: Failed to fetch http://example.invalid/Release  Temporary failure resolving 'example.invalid'\nE: Some index files failed to download.")
		}
		return out("")
	case "dist-upgrade":
		return f.distUpgrade(simulate)
	case "install":
		return f.install(rest)
	case "purge":
		for _, n := range rest {
			f.removePkg(n)
		}
		return out("")
	}
	f.t.Fatalf("unexpected apt-get verb %q (%v)", verb, args)
	return platform.Result{}
}

// pendingKernels 는 아직 설치되지 않은 새 커널 패키지 이름이다.
func (f *fakeSys) pendingKernelPkgs() []string {
	var names []string
	for rel := range f.newKernels {
		if _, ok := f.pkgs["linux-image-"+rel]; !ok {
			names = append(names, "linux-image-"+rel, "linux-headers-"+rel)
		}
	}
	sort.Strings(names)
	return names
}

func (f *fakeSys) distUpgrade(simulate bool) platform.Result {
	var names []string
	for n := range f.cand {
		if f.pkgs[n] != nil && f.pkgs[n].version != f.cand[n] {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if simulate {
		var b strings.Builder
		for _, n := range f.pendingKernelPkgs() {
			rel := strings.TrimPrefix(strings.TrimPrefix(n, "linux-image-"), "linux-headers-")
			fmt.Fprintf(&b, "Inst %s (%s Debian-Security:13/stable-security [amd64])\n", n, f.newKernels[rel])
		}
		for _, n := range names {
			fmt.Fprintf(&b, "Inst %s [%s] (%s Debian:13/stable [amd64])\n", n, f.pkgs[n].version, f.cand[n])
		}
		for _, r := range f.removeOnUpg {
			fmt.Fprintf(&b, "Remv %s [1.0]\n", r)
		}
		return out(b.String())
	}
	if f.upgradeFail {
		// 일부는 풀렸지만 DKMS 가 실패한 상태를 흉내 낸다.
		for _, n := range names {
			if strings.Contains(n, "nvidia") {
				f.pkgs[n].version = f.cand[n]
			}
		}
		return fail(100, "dpkg: error processing package nvidia-kernel-open-dkms (--configure):\n dkms autoinstall failed")
	}
	oldDriver := f.driverVersion()
	for _, n := range pendingNames(f) {
		f.installKernelPkg(n)
	}
	for _, n := range names {
		f.pkgs[n].version = f.cand[n]
	}
	if f.driverVersion() != oldDriver {
		for rel := range f.modules {
			f.modules[rel] = f.driverVersion()
		}
	}
	f.writeGrub()
	return out("")
}

func pendingNames(f *fakeSys) []string { return f.pendingKernelPkgs() }

func (f *fakeSys) installKernelPkg(name string) {
	rel := strings.TrimPrefix(strings.TrimPrefix(name, "linux-image-"), "linux-headers-")
	f.pkgs[name] = &fakePkg{version: f.newKernels[rel], source: "linux"}
	if strings.HasPrefix(name, "linux-image-") {
		f.modules[rel] = f.driverVersion()
		f.writeInitrd(rel)
	}
}

func (f *fakeSys) install(pkgs []string) platform.Result {
	// 새 커널 설치
	var kernelPkgs []string
	for _, p := range pkgs {
		if strings.Contains(p, "=") {
			continue
		}
		if isKernelVersioned(p) {
			kernelPkgs = append(kernelPkgs, p)
		}
	}
	if len(kernelPkgs) > 0 {
		for _, p := range kernelPkgs {
			rel := strings.TrimPrefix(strings.TrimPrefix(p, "linux-image-"), "linux-headers-")
			if f.dkmsFail[rel] {
				// 패키지는 풀렸지만 설정이 실패해 반쯤 설치된 상태를 흉내 낸다.
				f.pkgs[p] = &fakePkg{version: f.newKernels[rel], source: "linux"}
				return fail(100, "dpkg: error processing package "+p+" (--configure):\n dkms: autoinstall for kernel "+rel+" failed")
			}
		}
		for _, p := range kernelPkgs {
			f.installKernelPkg(p)
		}
		f.writeGrub()
		return out("")
	}
	// name=version 되돌리기
	for _, p := range pkgs {
		name, ver, ok := strings.Cut(p, "=")
		if !ok {
			continue
		}
		if f.oldUnavailable {
			return fail(100, "E: Version '"+ver+"' for '"+name+"' was not found")
		}
		if f.pkgs[name] != nil {
			f.pkgs[name].version = ver
		}
	}
	if f.driverVersion() != f.modules[f.running] {
		for rel := range f.modules {
			f.modules[rel] = f.driverVersion()
		}
	}
	return out("")
}
