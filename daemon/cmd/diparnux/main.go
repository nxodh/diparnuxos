// Command diparnux는 Diparnux 데몬(diparnuxd)을 다루는 명령행 도구다.
//
// 이 도구는 데몬의 유닉스 소켓 API 하고만 통신한다. root 권한이 필요한 일은 데몬이 한다.
// 모든 출력은 영어 ASCII 다. 사람용 출력과 --json 출력은 명령마다 같은 종료 코드 규칙을 따른다.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strings"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/client"
	"github.com/nxodh/diparnuxos/daemon/internal/termio"
)

// version 은 빌드 때 -ldflags "-X main.version=..." 으로 넣는다. 코드는 이 값을 바꾸지 않는다.
var version = "dev"

// 기본 소켓 경로와 환경 변수 이름이다.
const (
	defaultSocket  = "/run/diparnux/diparnuxd.sock"
	socketEnvVar   = "DIPARNUX_SOCKET"
	defaultPoll    = time.Second
	maxTokensLimit = 4096
	logLinesLimit  = 2000
	searchLimitMax = 100
)

// 종료 코드다. 0 성공, 1 데몬이 돌려준 오류나 작업 실패, 2 사용법 오류와 미구현, 3 데몬에 연결하지 못함.
const (
	exitOK          = 0
	exitFailure     = 1
	exitUsage       = 2
	exitUnreachable = 3
)

// runEnv 는 명령 실행에 필요한 입출력과 환경이다. 테스트는 이 값을 직접 채워 execute 를 부른다.
type runEnv struct {
	ctx    context.Context
	stdin  io.Reader
	stdout io.Writer
	stderr io.Writer
	// getenv 는 환경 변수를 읽는다. nil 이면 빈 값을 돌려준다.
	getenv func(string) string
	// stdinTTY 와 stdoutTTY 는 표준 입력과 출력이 터미널인지다.
	stdinTTY  bool
	stdoutTTY bool
	// pollInterval 은 작업과 서버 상태를 다시 읽는 간격이다. 0 이면 기다리지 않는다.
	pollInterval time.Duration
	// readPassword 는 터미널에서 에코 없이 한 줄을 읽는다. nil 이면 표준 입력의 termios 를 쓴다. 테스트가 바꾼다.
	readPassword func(prompt string) (string, error)
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv))
}

// run 은 명령을 실행하고 종료 코드를 돌려준다. Ctrl-C 를 받으면 진행 중인 요청과 기다림을 끊는다.
func run(args []string, stdin io.Reader, stdout, stderr io.Writer, getenv func(string) string) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	return execute(runEnv{
		ctx:          ctx,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		getenv:       getenv,
		stdinTTY:     termio.IsTerminal(stdin),
		stdoutTTY:    termio.IsTerminal(stdout),
		pollInterval: defaultPoll,
	}, args)
}

// cli 는 한 번의 실행 동안 쓰는 상태다.
type cli struct {
	env    runEnv
	api    *client.Client
	asJSON bool
}

// execute 는 전역 플래그를 읽고 명령을 고른다. 출력은 모두 ASCII 로 바뀐다.
func execute(e runEnv, args []string) int {
	e.stdout = asciiWriter{w: e.stdout}
	e.stderr = asciiWriter{w: e.stderr}
	if e.getenv == nil {
		e.getenv = func(string) string { return "" }
	}
	if e.ctx == nil {
		e.ctx = context.Background()
	}
	if e.stdin == nil {
		e.stdin = strings.NewReader("")
	}

	g := flag.NewFlagSet("diparnux", flag.ContinueOnError)
	g.SetOutput(io.Discard)
	socket := g.String("socket", "", "diparnuxd socket path")
	asJSON := g.Bool("json", false, "print the API JSON instead of text")
	if err := g.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeTopUsage(e.stdout)
			return exitOK
		}
		fmt.Fprintf(e.stderr, "diparnux: %v\n", err)
		fmt.Fprintln(e.stderr, "Run 'diparnux help' for usage.")
		return exitUsage
	}
	rest := g.Args()
	if len(rest) == 0 {
		writeTopUsage(e.stderr)
		return exitUsage
	}

	sock := *socket
	if sock == "" {
		sock = e.getenv(socketEnvVar)
	}
	if sock == "" {
		sock = defaultSocket
	}
	c := &cli{env: e, api: client.New(sock), asJSON: *asJSON}

	cmd, cargs := rest[0], rest[1:]
	switch cmd {
	case "status":
		return c.status(cargs)
	case "gpu":
		return c.gpu(cargs)
	case "model":
		return c.model(cargs)
	case "serve":
		return c.serve(cargs)
	case "mode":
		return c.mode(cargs)
	case "update":
		return c.update(cargs)
	case "report":
		return c.report(cargs)
	case "apikey":
		return c.apikey(cargs)
	case "user":
		return c.user(cargs)
	case "ssh":
		return c.ssh(cargs)
	case "power":
		return c.power(cargs)
	case "firewall":
		return c.firewall(cargs)
	case "exposure":
		return c.exposure(cargs)
	case "webadmin":
		return c.webadmin(cargs)
	case "vpn":
		return c.vpn(cargs)
	case "remote-desktop":
		return c.remoteDesktop(cargs)
	case "services":
		return c.services(cargs)
	case "resources":
		return c.resources(cargs)
	case "connection":
		return c.connection(cargs)
	case "connect":
		return c.connect(cargs)
	case "gateway":
		return c.gateway(cargs)
	case "network":
		return c.network(cargs)
	case "config":
		return c.config(cargs)
	case "chat":
		return c.chat(cargs)
	case "version":
		return c.version(cargs)
	case "help":
		return c.help(cargs)
	}
	return c.usage(fmt.Sprintf("unknown command %q", cmd), "")
}

// usage 는 사용법 오류를 쓰고 종료 코드 2 를 돌려준다. name 이 있으면 그 명령의 도움말을 가리킨다.
func (c *cli) usage(msg, name string) int {
	fmt.Fprintf(c.env.stderr, "diparnux: %s\n", msg)
	if name == "" {
		fmt.Fprintln(c.env.stderr, "Run 'diparnux help' for usage.")
	} else {
		fmt.Fprintf(c.env.stderr, "Run 'diparnux help %s' for usage.\n", name)
	}
	return exitUsage
}

// timeout 은 실행 context 를 바탕으로 시간 제한이 있는 context 를 만든다.
func (c *cli) timeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(c.env.ctx, d)
}

// sleep 은 폴링 간격만큼 기다린다. Ctrl-C 로 취소되면 그 오류를 돌려준다.
func (c *cli) sleep() error {
	if c.env.pollInterval <= 0 {
		return c.env.ctx.Err()
	}
	t := time.NewTimer(c.env.pollInterval)
	defer t.Stop()
	select {
	case <-c.env.ctx.Done():
		return c.env.ctx.Err()
	case <-t.C:
		return nil
	}
}

// interrupted 는 사용자가 Ctrl-C 를 눌렀을 때의 안내를 쓴다. 데몬의 작업은 계속되므로 종료 코드는 0 이다.
func (c *cli) interrupted(msg string) int {
	fmt.Fprintf(c.env.stdout, "Interrupted. %s\n", msg)
	return exitOK
}

// parseArgs 는 플래그를 위치 인자 사이에 두어도 읽는다. "--" 뒤의 값은 모두 위치 인자다.
func parseArgs(fs *flag.FlagSet, args []string) ([]string, error) {
	flagPart := args
	var tail []string
	for i, a := range args {
		if a == "--" {
			flagPart = args[:i]
			tail = args[i+1:]
			break
		}
	}
	var pos []string
	rest := flagPart
	for {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		pos = append(pos, rest[0])
		rest = rest[1:]
	}
	return append(pos, tail...), nil
}

// newFlags 는 출력이 버려지는 플래그 묶음을 만든다. 도움말과 오류는 호출자가 쓴다.
func newFlags(name string) *flag.FlagSet {
	fs := flag.NewFlagSet("diparnux "+name, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	return fs
}

// parse 는 명령의 플래그를 읽는다. ok 가 false 면 code 를 그대로 돌려주고 끝낸다.
// -h 는 도움말을 표준 출력에 쓰고 0 이다. 그 밖의 플래그 오류는 사용법 오류(2)다.
func (c *cli) parse(fs *flag.FlagSet, name string, args []string) (pos []string, code int, ok bool) {
	pos, err := parseArgs(fs, args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			writeCommandHelp(c.env.stdout, name, fs)
			return nil, exitOK, false
		}
		return nil, c.usage(err.Error(), name), false
	}
	return pos, exitOK, true
}

// readLine 은 표준 입력에서 한 줄을 읽는다. 줄바꿈 뒤의 내용은 읽지 않는다.
func readLine(r io.Reader) (string, error) {
	line, err := bufio.NewReader(r).ReadString('\n')
	if err != nil && !errors.Is(err, io.EOF) {
		return "", err
	}
	return strings.TrimRight(line, "\r\n"), nil
}
