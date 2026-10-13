package admin

import (
	"context"
	"encoding/base64"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

type fakeRunner struct {
	calls []platform.Command
	res   platform.Result
}

func (f *fakeRunner) Run(ctx context.Context, c platform.Command) (platform.Result, error) {
	f.calls = append(f.calls, c)
	return f.res, nil
}

func TestAllowedPath(t *testing.T) {
	good := []string{
		"/etc/systemd/network/10-diparnux-enp3s0.network",
		"/etc/systemd/network/50-diparnux-wg0.netdev",
		"/etc/ssh/sshd_config.d/60-diparnux-admin.conf",
		"/etc/hosts",
	}
	bad := []string{
		"/etc/passwd", "/etc/shadow", "/etc/systemd/network/10-other.network", "/etc/systemd/network/../../passwd",
		"/etc/ssh/sshd_config", "/etc/ssh/sshd_config.d/50-diparnux.conf", "relative/path", "/etc//hosts",
		"/etc/systemd/system/evil.service", "/etc/sudoers.d/diparnux",
	}
	for _, p := range good {
		if !AllowedPath(p) {
			t.Errorf("%s was refused", p)
		}
	}
	for _, p := range bad {
		if AllowedPath(p) {
			t.Errorf("%s was allowed", p)
		}
	}
}

func TestClientWriteSendsTheContentOnStdinOnly(t *testing.T) {
	r := &fakeRunner{res: platform.Result{Stdout: []byte("\n" + ResultPrefix + "ok\n")}}
	c := &Client{Runner: r}
	if err := c.Write(context.Background(), "/etc/systemd/network/10-diparnux-enp3s0.network", []byte("[Match]\nName=enp3s0\n"), false); err != nil {
		t.Fatal(err)
	}
	call := r.calls[0]
	if call.Name != "systemd-run" || !strings.Contains(strings.Join(call.Args, " "), DefaultHelper+" write /etc/systemd/network/10-diparnux-enp3s0.network") {
		t.Fatalf("call = %+v", call)
	}
	if string(call.Stdin) != "[Match]\nName=enp3s0\n" {
		t.Fatalf("stdin = %q", call.Stdin)
	}
	if strings.Contains(strings.Join(call.Args, " "), "Match") {
		t.Fatal("the content appeared in the arguments")
	}
}

func TestClientRefusesWhatTheHelperMustNotDo(t *testing.T) {
	r := &fakeRunner{}
	c := &Client{Runner: r}
	if err := c.Write(context.Background(), "/etc/passwd", []byte("x"), false); err == nil {
		t.Fatal("a disallowed path was written")
	}
	if err := c.Remove(context.Background(), "/etc/ssh/sshd_config"); err == nil {
		t.Fatal("a disallowed path was removed")
	}
	if _, err := c.ReadAuthorizedKeys(context.Background(), "root"); err == nil {
		t.Fatal("root keys were read")
	}
	if err := c.WriteAuthorizedKeys(context.Background(), "../etc", []byte("x")); err == nil {
		t.Fatal("a path-like user was accepted")
	}
	if len(r.calls) != 0 {
		t.Fatalf("the helper was called %d times", len(r.calls))
	}
}

func TestParseResult(t *testing.T) {
	data := base64.StdEncoding.EncodeToString([]byte("ssh-ed25519 AAAA key\n"))
	got, err := ParseResult(platform.Result{Stdout: []byte(DataPrefix + data + "\n" + ResultPrefix + "ok\n")})
	if err != nil || string(got) != "ssh-ed25519 AAAA key\n" {
		t.Fatalf("got %q, %v", got, err)
	}
	_, err = ParseResult(platform.Result{Stdout: []byte(ResultPrefix + "error:invalid_path\n"), Stderr: []byte("\nwriting is not allowed\n")})
	if e, ok := err.(*Error); !ok || e.Code != CodeInvalidPath || e.Message != "writing is not allowed" {
		t.Fatalf("err = %v", err)
	}
	if _, err = ParseResult(platform.Result{Stdout: []byte("nothing\n")}); err == nil {
		t.Fatal("a missing result line was accepted")
	}
	if _, err = ParseResult(platform.Result{Stdout: []byte(ResultPrefix + "error:weird\n")}); err == nil || err.(*Error).Code != CodeFailed {
		t.Fatalf("unknown code = %v", err)
	}
}
