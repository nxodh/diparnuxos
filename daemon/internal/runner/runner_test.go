package runner

import (
	"context"
	"errors"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// TestRunCapturesOutputAndExitZero 는 stdout 과 stderr 를 나눠 모으고 종료 코드 0 을 돌려주는지 확인한다.
func TestRunCapturesOutputAndExitZero(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), platform.Command{
		Name: "sh",
		Args: []string{"-c", "printf hi; printf err >&2"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "hi" || string(res.Stderr) != "err" || res.ExitCode != 0 {
		t.Fatalf("result = %q %q %d", res.Stdout, res.Stderr, res.ExitCode)
	}
}

// TestNonZeroExitIsNotAnError 는 종료 코드가 0 이 아니어도 오류가 아니라 ExitCode 로 돌아오는지 확인한다.
func TestNonZeroExitIsNotAnError(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), platform.Command{Name: "sh", Args: []string{"-c", "echo oops >&2; exit 3"}})
	if err != nil {
		t.Fatalf("Run returned error for exit 3: %v", err)
	}
	if res.ExitCode != 3 || string(res.Stderr) != "oops\n" {
		t.Fatalf("result = %d %q", res.ExitCode, res.Stderr)
	}
}

// TestMissingExecutableWrapsErrNotFound 는 실행 파일이 없을 때 exec.ErrNotFound 로 판별되는지 확인한다.
func TestMissingExecutableWrapsErrNotFound(t *testing.T) {
	for _, name := range []string{"no-such-binary-opereta-test", "/nonexistent/dir/opereta-test"} {
		_, err := Exec{}.Run(context.Background(), platform.Command{Name: name})
		if !errors.Is(err, exec.ErrNotFound) {
			t.Fatalf("Run(%q) error = %v, want exec.ErrNotFound", name, err)
		}
	}
}

// TestEmptyNameIsAnError 는 빈 실행 파일 이름을 거부하는지 확인한다.
func TestEmptyNameIsAnError(t *testing.T) {
	if _, err := (Exec{}).Run(context.Background(), platform.Command{}); err == nil {
		t.Fatal("Run with an empty name: want error")
	}
}

// TestEnvIsAppended 는 Env 가 데몬 환경에 덧붙는지 확인한다. PATH 는 그대로 남아야 한다.
func TestEnvIsAppended(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), platform.Command{
		Name: "sh",
		Args: []string{"-c", `printf "%s|%s" "$OPR_TEST_VALUE" "$PATH"`},
		Env:  []string{"OPR_TEST_VALUE=yes"},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	parts := strings.SplitN(string(res.Stdout), "|", 2)
	if len(parts) != 2 || parts[0] != "yes" || parts[1] == "" {
		t.Fatalf("stdout = %q, want the new variable and a PATH", res.Stdout)
	}
}

// TestMaxOutputTruncatesAndDoesNotBlock 는 출력이 상한에서 잘리고, 프로세스는 끝까지 실행되는지 확인한다.
func TestMaxOutputTruncatesAndDoesNotBlock(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), platform.Command{
		Name:      "sh",
		Args:      []string{"-c", "head -c 200000 /dev/zero; echo done >&2"},
		MaxOutput: 16,
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(res.Stdout) != 16 {
		t.Fatalf("stdout length = %d, want 16", len(res.Stdout))
	}
	if string(res.Stderr) != "done\n" || res.ExitCode != 0 {
		t.Fatalf("process did not finish: stderr=%q exit=%d", res.Stderr, res.ExitCode)
	}
}

// TestTimeoutKillsProcessGroup 는 시간 초과 때 자식 프로세스까지 끝내고 ErrTimeout 을 돌려주는지 확인한다.
// 손자 프로세스(sleep)가 출력 파이프를 잡고 있어도 Run 이 끝나야 한다.
func TestTimeoutKillsProcessGroup(t *testing.T) {
	start := time.Now()
	_, err := Exec{}.Run(context.Background(), platform.Command{
		Name:    "sh",
		Args:    []string{"-c", "sleep 30 & wait"},
		Timeout: 200 * time.Millisecond,
	})
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("error = %v, want ErrTimeout", err)
	}
	if elapsed := time.Since(start); elapsed > 15*time.Second {
		t.Fatalf("Run took %v after a 200ms timeout", elapsed)
	}
}

// TestCanceledContextIsReported 는 호출자의 ctx 가 취소되었을 때 그 오류를 돌려주는지 확인한다.
func TestCanceledContextIsReported(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Exec{}.Run(ctx, platform.Command{Name: "sh", Args: []string{"-c", "exit 0"}})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
}

// TestStdinIsPassedToTheCommand 는 Command.Stdin 이 표준 입력으로 전달되는지 확인한다.
func TestStdinIsPassedToTheCommand(t *testing.T) {
	res, err := Exec{}.Run(context.Background(), platform.Command{
		Name:  "sh",
		Args:  []string{"-c", "read line; printf 'got:%s' \"$line\""},
		Stdin: []byte("secret value\n"),
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if string(res.Stdout) != "got:secret value" || res.ExitCode != 0 {
		t.Fatalf("result = %q %d", res.Stdout, res.ExitCode)
	}
}
