// Command lightlix-update 는 커널과 NVIDIA 드라이버를 함께 갱신하고 되돌리는 root 용 헬퍼다.
//
// 데몬은 하드닝 때문에 /boot 와 /etc 에 쓸 수 없어 systemd-run 으로 이 프로그램을 띄운다. 부팅 뒤 검증은
// lightlix-update-verify.service 가 같은 프로그램의 verify 를 부른다. 데몬이 없어도 직접 쓸 수 있다.
//
//	lightlix-update check
//	lightlix-update apply [--reboot]
//	lightlix-update verify
//	lightlix-update rollback [--reboot]
//	lightlix-update status
//
// 진행 상황은 표준 출력에 한 줄씩 나오고, --json 이면 JSON 줄이다. 마지막 줄은 결과다.
// 종료 코드는 성공 0, 실패 1, 사용법 오류 2 다. 검증할 갱신이 없는 verify 는 실패가 아니다.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/nxodh/lightlixos/daemon/internal/runner"
	"github.com/nxodh/lightlixos/daemon/internal/update"
)

// result 는 --json 의 마지막 줄이다.
type result struct {
	OK     bool           `json:"ok"`
	Plan   *update.Plan   `json:"plan,omitempty"`
	State  *update.State  `json:"state,omitempty"`
	Status *update.Status `json:"status,omitempty"`
	Error  *update.Error  `json:"error,omitempty"`
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		fmt.Fprintln(stderr, "usage: lightlix-update check|apply|verify|rollback|status [--json] [--reboot] [--state-dir DIR]")
		return 2
	}
	cmd := args[0]
	fs := flag.NewFlagSet(cmd, flag.ContinueOnError)
	fs.SetOutput(stderr)
	asJSON := fs.Bool("json", false, "print JSON lines")
	reboot := fs.Bool("reboot", false, "reboot when the update succeeds")
	stateDir := fs.String("state-dir", "", "state directory (default /var/lib/lightlix/update)")
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	e := &update.Engine{Cfg: update.Config{StateDir: *stateDir}, Run: runner.Exec{}}
	if *asJSON {
		e.Out = stdout
	} else {
		e.Out = &textWriter{w: stdout}
	}

	var res result
	switch cmd {
	case "check":
		plan, err := e.Check(ctx)
		res.Plan = plan
		res.set(err)
	case "apply":
		st, err := e.Apply(ctx, update.ApplyOptions{Reboot: *reboot})
		res.State = st
		res.set(err)
	case "verify":
		st, err := e.Verify(ctx)
		res.State = st
		res.set(err)
		if res.Error != nil && res.Error.Code == update.CodeNoPending {
			res.OK = true
		}
	case "rollback":
		st, err := e.Rollback(ctx, update.ApplyOptions{Reboot: *reboot})
		res.State = st
		res.set(err)
	case "status":
		st, err := e.Status(ctx)
		res.Status = st
		res.set(err)
	default:
		fmt.Fprintf(stderr, "lightlix-update: unknown command %q\n", cmd)
		return 2
	}
	print(stdout, *asJSON, cmd, &res)
	if res.OK {
		return 0
	}
	return 1
}

func (r *result) set(err error) {
	if err == nil {
		r.OK = true
		return
	}
	var ue *update.Error
	if errors.As(err, &ue) {
		r.Error = ue
		return
	}
	r.Error = &update.Error{Code: update.CodeInternal, Message: err.Error()}
}

// textWriter 는 JSON 이벤트 줄을 사람이 읽는 한 줄로 바꾼다.
type textWriter struct{ w io.Writer }

func (t *textWriter) Write(p []byte) (int, error) {
	var ev update.Event
	if json.Unmarshal(p, &ev) != nil {
		return t.w.Write(p)
	}
	_, err := fmt.Fprintf(t.w, "[%s] %s\n", ev.Step, ev.Message)
	return len(p), err
}

func print(w io.Writer, asJSON bool, cmd string, r *result) {
	if asJSON {
		b, _ := json.Marshal(r)
		fmt.Fprintf(w, "%s\n", b)
		return
	}
	if r.Error != nil && !r.OK {
		fmt.Fprintf(w, "%s failed: %s: %s\n", cmd, r.Error.Code, r.Error.Message)
		return
	}
	switch {
	case r.Plan != nil:
		p := r.Plan
		if p.UpToDate {
			fmt.Fprintln(w, "up to date")
			return
		}
		fmt.Fprintf(w, "%d package changes\n", len(p.Changes))
		if len(p.Kernels) > 0 {
			fmt.Fprintf(w, "new kernel: %v\n", p.Kernels)
		}
		if p.DriverTo != "" {
			fmt.Fprintf(w, "driver: %s -> %s\n", p.DriverFrom, p.DriverTo)
		}
		for _, c := range p.Changes {
			fmt.Fprintf(w, "  %s %s -> %s\n", c.Name, c.From, c.To)
		}
	case r.Status != nil:
		s := r.Status
		fmt.Fprintf(w, "state: %s\nrunning kernel: %s\nsaved default: %s\nkernels: %v\nreboot required: %v\n", s.Phase, s.RunningKernel, s.GrubDefault, s.Kernels, s.RebootRequired)
		if s.Code != "" {
			fmt.Fprintf(w, "last error: %s: %s\n", s.Code, s.Message)
		}
	case r.State != nil:
		s := r.State
		fmt.Fprintf(w, "state: %s\n", s.Phase)
		if s.RebootRequired {
			fmt.Fprintln(w, "reboot required")
		}
		if s.Message != "" {
			fmt.Fprintln(w, s.Message)
		}
	}
}
