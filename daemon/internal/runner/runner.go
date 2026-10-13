// Package runner 는 외부 명령을 셸을 거치지 않고 실행하는 platform.Runner 구현을 둔다.
//
// 명령은 exec.CommandContext 로 실행한다. 종료 코드가 0 이 아니어도 오류가 아니며 Result.ExitCode 로 돌려준다.
// 실행 파일이 없거나 시작하지 못하면 오류를 돌려준다. 시간 초과가 나면 프로세스 그룹 전체를 끝낸다.
package runner

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"syscall"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// 실행 제한 상수다.
const (
	// DefaultMaxOutput 은 Command.MaxOutput 이 0 일 때 쓰는 stdout, stderr 각각의 상한(1 MiB)이다.
	DefaultMaxOutput = 1 << 20
	// waitDelay 는 프로세스가 끝난 뒤 출력 파이프가 닫히기를 기다리는 최대 시간이다.
	// 손자 프로세스가 파이프를 붙잡고 있어도 Wait 가 끝나도록 한다.
	waitDelay = 2 * time.Second
)

// ErrTimeout 은 Command.Timeout 이 지나 명령을 끝냈을 때 돌려주는 오류다. errors.Is 로 확인한다.
var ErrTimeout = errors.New("command timed out")

// platform.Runner 를 구현하는지 컴파일 시점에 확인한다.
var _ platform.Runner = Exec{}

// Exec 는 os/exec 로 명령을 실행하는 platform.Runner 다. 값이 없으므로 Exec{} 로 쓴다.
type Exec struct{}

// Run 은 cmd 를 끝까지 실행하고 결과를 돌려준다.
//
// 오류가 나는 경우는 다음과 같다. 실행 파일이 없으면 errors.Is(err, exec.ErrNotFound) 가 참이다.
// 시작하지 못한 경우와 ctx 가 취소된 경우도 오류다. cmd.Timeout 이 지나면 ErrTimeout 을 감싼 오류를 돌려준다.
// 종료 코드가 0 이 아니면 오류 없이 Result.ExitCode 에 담는다. 신호로 끝났으면 ExitCode 는 -1 이다.
func (Exec) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	if cmd.Name == "" {
		return platform.Result{}, errors.New("runner: empty command name")
	}
	runCtx, cancel := ctx, context.CancelFunc(func() {})
	if cmd.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, cmd.Timeout)
	}
	defer cancel()

	c := exec.CommandContext(runCtx, cmd.Name, cmd.Args...)
	if len(cmd.Env) > 0 {
		c.Env = append(os.Environ(), cmd.Env...)
	}
	// 새 프로세스 그룹에서 실행해, 취소할 때 자식 프로세스까지 함께 끝낸다.
	c.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	c.Cancel = func() error {
		// 프로세스가 이미 끝났는데 pid 가 재사용되는 경우는 무시할 만큼 드물다. 그룹 전체에 SIGKILL 을 보낸다.
		return syscall.Kill(-c.Process.Pid, syscall.SIGKILL)
	}
	c.WaitDelay = waitDelay
	if len(cmd.Stdin) > 0 {
		c.Stdin = bytes.NewReader(cmd.Stdin)
	}

	limit := cmd.MaxOutput
	if limit <= 0 {
		limit = DefaultMaxOutput
	}
	stdout := &capBuffer{limit: limit}
	stderr := &capBuffer{limit: limit}
	c.Stdout = stdout
	c.Stderr = stderr

	runErr := c.Run()
	res := platform.Result{Stdout: stdout.buf, Stderr: stderr.buf}

	if c.ProcessState == nil {
		// 프로세스가 시작되지 않았다.
		if ctxErr := ctx.Err(); ctxErr != nil {
			return res, fmt.Errorf("runner: %s: %w", cmd.Name, ctxErr)
		}
		if errors.Is(runErr, exec.ErrNotFound) || errors.Is(runErr, fs.ErrNotExist) {
			return res, fmt.Errorf("runner: start %s: %w: %w", cmd.Name, exec.ErrNotFound, runErr)
		}
		return res, fmt.Errorf("runner: start %s: %w", cmd.Name, runErr)
	}
	if ctxErr := ctx.Err(); ctxErr != nil {
		return res, fmt.Errorf("runner: %s: %w", cmd.Name, ctxErr)
	}
	if runCtx.Err() == context.DeadlineExceeded {
		return res, fmt.Errorf("runner: %s: %w", cmd.Name, ErrTimeout)
	}
	res.ExitCode = c.ProcessState.ExitCode()
	return res, nil
}

// capBuffer 는 limit 바이트까지만 저장하고, 그 뒤의 바이트는 버린다. Write 는 항상 전부 받았다고 답하므로
// 프로세스는 끝까지 출력을 쓸 수 있고 파이프가 막히지 않는다.
type capBuffer struct {
	limit int
	buf   []byte
}

// Write 는 p 를 남은 용량만큼 저장하고 len(p) 를 돌려준다.
func (b *capBuffer) Write(p []byte) (int, error) {
	if room := b.limit - len(b.buf); room > 0 {
		b.buf = append(b.buf, p[:min(room, len(p))]...)
	}
	return len(p), nil
}
