// Command diparnux-install 는 라이브 세션에서 디스크 하나에 Diparnux 를 설치한다.
//
//	diparnux-install run [--disk DEV] [--hostname NAME] [--user NAME] [--admin] [--password-stdin]
//	                     [--mode server|desktop] [--locale L] [--timezone TZ] [--keyboard K]
//	                     [--console serial] [--yes] [--reboot] [--json]
//	diparnux-install target-prepare --root DIR [--mode M] [--register-user NAME] [--admin]
//	diparnux-install boot-default --root DIR
//	diparnux-install target-bootloader --root DIR [--console serial]
//
// run 은 디스크 전체를 지운다. 터미널에서 값을 빼고 실행하면 물어본다. 비대화 환경에서는 --yes 가 있어야 한다.
// 비밀번호는 표준 입력의 첫 줄이나 터미널 입력으로만 받고 인자, 환경 변수, 파일, 로그에 남기지 않는다.
// target-prepare 와 boot-default 는 Calamares 의 shellprocess 가 대상 시스템 안에서 부르는 마무리 단계다.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/nxodh/diparnuxos/daemon/internal/install"
	"github.com/nxodh/diparnuxos/daemon/internal/runner"
	"github.com/nxodh/diparnuxos/daemon/internal/termio"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
}

func usage(w io.Writer) {
	fmt.Fprintln(w, "usage: diparnux-install run [options] | target-prepare --root DIR [options] | target-bootloader --root DIR | boot-default --root DIR")
}

func run(args []string, stdin *os.File, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		usage(stderr)
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()
	switch args[0] {
	case "run":
		return cmdRun(ctx, args[1:], stdin, stdout, stderr)
	case "target-prepare":
		return cmdTargetPrepare(ctx, args[1:], stdout, stderr)
	case "boot-default":
		return cmdBootDefault(ctx, args[1:], stdout, stderr)
	case "target-bootloader":
		return cmdTargetBootloader(ctx, args[1:], stdout, stderr)
	}
	usage(stderr)
	return 2
}

// jsonOrText 는 이벤트 줄을 JSON 그대로, 또는 사람이 읽는 줄로 내보낸다.
type textWriter struct{ w io.Writer }

func (t *textWriter) Write(p []byte) (int, error) {
	var ev install.Event
	if json.Unmarshal(p, &ev) != nil {
		return t.w.Write(p)
	}
	_, err := fmt.Fprintf(t.w, "[%s] %s\n", ev.Step, ev.Message)
	return len(p), err
}

func newInstaller(out io.Writer, asJSON bool) *install.Installer {
	in := &install.Installer{Run: runner.Exec{}}
	if asJSON {
		in.Out = out
	} else {
		in.Out = &textWriter{w: out}
	}
	return in
}

func fail(w io.Writer, asJSON bool, err error) int {
	var ie *install.Error
	if !errors.As(err, &ie) {
		ie = &install.Error{Code: "internal", Message: err.Error()}
	}
	if asJSON {
		b, _ := json.Marshal(map[string]any{"ok": false, "error": ie})
		fmt.Fprintf(w, "%s\n", b)
	} else {
		fmt.Fprintf(w, "install failed: %s: %s\n", ie.Code, ie.Message)
	}
	return 1
}

func cmdRun(ctx context.Context, args []string, stdin *os.File, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o install.Options
	fs.StringVar(&o.Disk, "disk", "", "disk to erase and install to (for example /dev/vda)")
	fs.StringVar(&o.Hostname, "hostname", "", "host name (default diparnux)")
	fs.StringVar(&o.User, "user", "", "login user name")
	fs.BoolVar(&o.Admin, "admin", true, "add the user to the sudo group")
	fromStdin := fs.Bool("password-stdin", false, "read the password from the first line of standard input")
	fs.StringVar(&o.Mode, "mode", "server", "default mode: server or desktop")
	fs.StringVar(&o.Locale, "locale", "en_US.UTF-8", "system locale")
	fs.StringVar(&o.Timezone, "timezone", "UTC", "time zone")
	fs.StringVar(&o.Keyboard, "keyboard", "us", "keyboard layout")
	fs.StringVar(&o.Console, "console", "", "set to serial to also use ttyS0")
	fs.StringVar(&o.Source, "source", "", "copy source directory (default: the live system image)")
	yes := fs.Bool("yes", false, "do not ask for confirmation")
	reboot := fs.Bool("reboot", false, "reboot when the installation finishes")
	asJSON := fs.Bool("json", false, "print JSON lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	interactive := termio.IsTerminal(stdin) && !*asJSON
	in := newInstaller(stdout, *asJSON)
	reader := bufio.NewReader(stdin)

	switch {
	case *fromStdin:
		line, err := reader.ReadString('\n')
		if err != nil && line == "" {
			return fail(stdout, *asJSON, &install.Error{Code: install.CodeInvalidOption, Message: "cannot read the password from standard input"})
		}
		o.Password = strings.TrimRight(line, "\r\n")
	case interactive:
		// 아래 prompts 에서 채운다.
	default:
		return fail(stdout, *asJSON, &install.Error{Code: install.CodeInvalidOption, Message: "pass the password with --password-stdin when standard input is not a terminal"})
	}

	if interactive {
		if err := prompts(stdin, reader, stdout, &o, *fromStdin); err != nil {
			return fail(stdout, false, err)
		}
	} else if o.Hostname == "" {
		o.Hostname = "diparnux"
	}
	if o.Disk == "" || o.User == "" {
		return fail(stdout, *asJSON, &install.Error{Code: install.CodeInvalidOption, Message: "--disk and --user are required"})
	}
	if !*yes {
		if !interactive {
			return fail(stdout, *asJSON, &install.Error{Code: install.CodeNotConfirmed, Message: "this erases the whole disk; pass --yes to confirm"})
		}
		fmt.Fprintf(stdout, "\nThis ERASES everything on %s. Type the disk path again to continue: ", o.Disk)
		line, _ := reader.ReadString('\n')
		if strings.TrimSpace(line) != o.Disk {
			return fail(stdout, false, &install.Error{Code: install.CodeNotConfirmed, Message: "the disk path did not match; nothing was changed"})
		}
	}

	if err := in.Install(ctx, &o); err != nil {
		o.Password = ""
		return fail(stdout, *asJSON, err)
	}
	o.Password = ""
	if *asJSON {
		b, _ := json.Marshal(map[string]any{"ok": true, "disk": o.Disk, "mode": o.Mode, "user": o.User})
		fmt.Fprintf(stdout, "%s\n", b)
	} else {
		fmt.Fprintf(stdout, "Diparnux is installed on %s. Remove the installation medium and reboot.\n", o.Disk)
	}
	if *reboot {
		_ = syscall.Exec("/bin/systemctl", []string{"systemctl", "reboot"}, os.Environ())
	}
	return 0
}

// prompts 는 비어 있는 값을 터미널에서 묻는다.
func prompts(stdin *os.File, r *bufio.Reader, w io.Writer, o *install.Options, passwordGiven bool) error {
	ask := func(label, def string) string {
		if def != "" {
			fmt.Fprintf(w, "%s [%s]: ", label, def)
		} else {
			fmt.Fprintf(w, "%s: ", label)
		}
		line, _ := r.ReadString('\n')
		line = strings.TrimSpace(line)
		if line == "" {
			return def
		}
		return line
	}
	if o.Disk == "" {
		listDisks(w)
		o.Disk = ask("Install to which disk (the whole disk is erased)", "")
	}
	if o.Hostname == "" {
		o.Hostname = ask("Host name", "diparnux")
	}
	if o.User == "" {
		o.User = ask("User name", "")
	}
	if !passwordGiven {
		fmt.Fprint(w, "Password: ")
		first, err := termio.ReadSecret(stdin)
		fmt.Fprintln(w)
		if err != nil {
			return &install.Error{Code: install.CodeInvalidOption, Message: "cannot read the password: " + err.Error()}
		}
		fmt.Fprint(w, "Repeat the password: ")
		second, err := termio.ReadSecret(stdin)
		fmt.Fprintln(w)
		if err != nil || first != second {
			return &install.Error{Code: install.CodeInvalidOption, Message: "the passwords do not match"}
		}
		o.Password = first
	}
	o.Locale = ask("Locale", o.Locale)
	o.Timezone = ask("Time zone", o.Timezone)
	o.Keyboard = ask("Keyboard layout", o.Keyboard)
	o.Mode = ask("Default mode (server or desktop)", o.Mode)
	return nil
}

var diskNameRE = regexp.MustCompile(`^(vd[a-z]+|sd[a-z]+|xvd[a-z]+|nvme[0-9]+n[0-9]+|mmcblk[0-9]+)$`)

// listDisks 는 설치 가능한 디스크를 크기와 모델과 함께 보여 준다.
func listDisks(w io.Writer) {
	entries, err := os.ReadDir("/sys/block")
	if err != nil {
		return
	}
	var names []string
	for _, e := range entries {
		if diskNameRE.MatchString(e.Name()) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	fmt.Fprintln(w, "Disks:")
	for _, n := range names {
		sectors := readInt(filepath.Join("/sys/block", n, "size"))
		model := strings.TrimSpace(readText(filepath.Join("/sys/block", n, "device", "model")))
		fmt.Fprintf(w, "  /dev/%-10s %6.1f GiB  %s\n", n, float64(sectors)*512/(1<<30), model)
	}
}

func readText(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func readInt(path string) int64 {
	var n int64
	_, _ = fmt.Sscanf(strings.TrimSpace(readText(path)), "%d", &n)
	return n
}

func cmdTargetPrepare(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("target-prepare", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "/", "target root (use / inside the target chroot)")
	var po install.PrepareOptions
	fs.StringVar(&po.Mode, "mode", "", "default mode: server or desktop")
	fs.StringVar(&po.RegisterUser, "register-user", "", "record an existing account in the Diparnux account list")
	fs.BoolVar(&po.Admin, "admin", false, "the registered account is an administrator")
	fs.BoolVar(&po.KeepInstaller, "keep-installer", false, "do not remove the installer packages")
	asJSON := fs.Bool("json", false, "print JSON lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if po.Mode != "" && po.Mode != "server" && po.Mode != "desktop" {
		fmt.Fprintln(stderr, "diparnux-install: --mode must be server or desktop")
		return 2
	}
	if po.RegisterUser != "" && !regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`).MatchString(po.RegisterUser) {
		fmt.Fprintln(stderr, "diparnux-install: invalid --register-user")
		return 2
	}
	in := newInstaller(stdout, *asJSON)
	if err := in.PrepareTarget(ctx, *root, po); err != nil {
		return fail(stdout, *asJSON, err)
	}
	return 0
}

func cmdBootDefault(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("boot-default", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "/", "target root (use / inside the target chroot)")
	asJSON := fs.Bool("json", false, "print JSON lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	in := newInstaller(stdout, *asJSON)
	if err := in.BootDefault(ctx, *root); err != nil {
		return fail(stdout, *asJSON, err)
	}
	return 0
}

func cmdTargetBootloader(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("target-bootloader", flag.ContinueOnError)
	fs.SetOutput(stderr)
	root := fs.String("root", "/", "target root (use / inside the target chroot)")
	console := fs.String("console", "", "set to serial to also use ttyS0")
	asJSON := fs.Bool("json", false, "print JSON lines")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *console != "" && *console != "serial" {
		fmt.Fprintln(stderr, "diparnux-install: --console must be empty or serial")
		return 2
	}
	in := newInstaller(stdout, *asJSON)
	if err := in.InstallBootloader(ctx, *root, *console); err != nil {
		return fail(stdout, *asJSON, err)
	}
	return 0
}
