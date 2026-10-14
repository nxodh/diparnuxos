package accounts

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

type fakeRunner struct {
	cmds []platform.Command
	res  platform.Result
	err  error
}

func (f *fakeRunner) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	f.cmds = append(f.cmds, cmd)
	return f.res, f.err
}

func ok() platform.Result {
	return platform.Result{Stdout: []byte("noise\nOPERETA_USER_RESULT=ok\n")}
}

func TestValidName(t *testing.T) {
	good := []string{"alice", "a", "a_b-c9", strings.Repeat("a", 32)}
	bad := []string{"", "Alice", "1abc", "a b", "root", "user", "operetad", "systemd-x", "x;y", "a.b", strings.Repeat("a", 33), "한글"}
	for _, n := range good {
		if !ValidName(n) {
			t.Errorf("ValidName(%q) = false", n)
		}
	}
	for _, n := range bad {
		if ValidName(n) {
			t.Errorf("ValidName(%q) = true", n)
		}
	}
}

func TestValidPassword(t *testing.T) {
	if !ValidPassword("alice", "correct-horse-9") {
		t.Fatal("good password rejected")
	}
	for _, pw := range []string{"", "short", "alicealice", strings.Repeat("a", 129), "tab\there123", "han글password"} {
		name := "alice"
		if pw == "alicealice" {
			name = "alicealice"
		}
		if ValidPassword(name, pw) {
			t.Errorf("ValidPassword(%q) = true", pw)
		}
	}
}

func TestAddPassesPasswordOnlyThroughStdin(t *testing.T) {
	r := &fakeRunner{res: ok()}
	m := &Manager{Runner: r, Helper: "/h/opereta-user"}
	if err := m.Add(context.Background(), "alice", "correct-horse-9", true); err != nil {
		t.Fatalf("Add: %v", err)
	}
	c := r.cmds[0]
	if c.Name != "systemd-run" {
		t.Fatalf("name = %s", c.Name)
	}
	wantTail := []string{"/h/opereta-user", "add", "alice", "--admin"}
	if !reflect.DeepEqual(c.Args[len(c.Args)-4:], wantTail) {
		t.Fatalf("args = %v", c.Args)
	}
	for _, a := range c.Args {
		if strings.Contains(a, "correct-horse") {
			t.Fatalf("password leaked into args: %v", c.Args)
		}
	}
	for _, e := range c.Env {
		if strings.Contains(e, "correct-horse") {
			t.Fatal("password leaked into env")
		}
	}
	if string(c.Stdin) != "correct-horse-9\n" {
		t.Fatalf("stdin = %q", c.Stdin)
	}
	hasPipe := false
	for _, a := range c.Args {
		if a == "--pipe" {
			hasPipe = true
		}
	}
	if !hasPipe {
		t.Fatal("--pipe is required so stdin reaches the helper")
	}
}

func TestAddRejectsBeforeRunning(t *testing.T) {
	r := &fakeRunner{res: ok()}
	m := &Manager{Runner: r}
	var e *Error
	if err := m.Add(context.Background(), "Root", "correct-horse-9", false); !errors.As(err, &e) || e.Code != CodeInvalidUsername {
		t.Fatalf("name: %v", err)
	}
	if err := m.Add(context.Background(), "alice", "short", false); !errors.As(err, &e) || e.Code != CodeInvalidPassword {
		t.Fatalf("password: %v", err)
	}
	if len(r.cmds) != 0 {
		t.Fatal("the helper must not run for invalid input")
	}
}

func TestHelperErrorCodesAreMapped(t *testing.T) {
	cases := map[string]string{
		"OPERETA_USER_RESULT=error:user_exists":    CodeUserExists,
		"OPERETA_USER_RESULT=error:ssh_config":     CodeSSHConfig,
		"OPERETA_USER_RESULT=error:weird_thing":    CodeAccountFailed,
		"no result line at all":                    CodeAccountFailed,
		"OPERETA_USER_RESULT=error:user_not_found": CodeUserNotFound,
	}
	for out, want := range cases {
		r := &fakeRunner{res: platform.Result{Stdout: []byte(out + "\n"), Stderr: []byte("opereta-user: boom\n"), ExitCode: 1}}
		err := (&Manager{Runner: r}).Remove(context.Background(), "alice")
		var e *Error
		if !errors.As(err, &e) || e.Code != want {
			t.Errorf("%q: err = %v, want code %s", out, err, want)
		}
	}
}

func TestRunnerFailureIsAccountFailed(t *testing.T) {
	r := &fakeRunner{err: errors.New("exec: not found")}
	err := (&Manager{Runner: r}).PrepareSSH(context.Background())
	var e *Error
	if !errors.As(err, &e) || e.Code != CodeAccountFailed {
		t.Fatalf("err = %v", err)
	}
}

func TestListReadsHelperFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "users.list")
	data := "alice 1 2026-10-12T01:02:03Z\nbob 0 2026-10-12T02:00:00Z\nbad;name 1 x\n\nroot 1 x\n"
	if err := os.WriteFile(path, []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	m := &Manager{ListPath: path}
	list, err := m.List()
	if err != nil {
		t.Fatal(err)
	}
	want := []Account{{"alice", true, "2026-10-12T01:02:03Z"}, {"bob", false, "2026-10-12T02:00:00Z"}}
	if !reflect.DeepEqual(list, want) {
		t.Fatalf("list = %+v", list)
	}
	empty := &Manager{ListPath: filepath.Join(t.TempDir(), "none")}
	l, err := empty.List()
	if err != nil || l == nil || len(l) != 0 {
		t.Fatalf("missing file: %v %v", l, err)
	}
}
