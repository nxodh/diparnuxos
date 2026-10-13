package sshconf

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// testKey 는 시험용 ed25519 공개 키 줄을 만든다(비밀 값이 아니다. 실행 때마다 새로 만든다).
func testKey(t *testing.T, comment string) string {
	t.Helper()
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	var blob []byte
	put := func(b []byte) {
		n := len(b)
		blob = append(blob, byte(n>>24), byte(n>>16), byte(n>>8), byte(n))
		blob = append(blob, b...)
	}
	put([]byte("ssh-ed25519"))
	put(pub)
	return "ssh-ed25519 " + base64.StdEncoding.EncodeToString(blob) + " " + comment
}

type files struct {
	dir     string
	removed int
	fail    bool
}

func (f *files) Write(ctx context.Context, p string, data []byte, secret bool) error {
	return os.WriteFile(filepath.Join(f.dir, filepath.Base(p)), data, 0o644)
}
func (f *files) Remove(ctx context.Context, p string) error {
	f.removed++
	err := os.Remove(filepath.Join(f.dir, filepath.Base(p)))
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type keyStore struct{ m map[string][]byte }

func (k *keyStore) ReadAuthorizedKeys(ctx context.Context, u string) ([]byte, error) {
	return k.m[u], nil
}
func (k *keyStore) WriteAuthorizedKeys(ctx context.Context, u string, d []byte) error {
	k.m[u] = d
	return nil
}

type runner struct {
	sshdExit int
	sshdErr  string
	calls    []string
	journal  string
}

func (r *runner) Run(ctx context.Context, c platform.Command) (platform.Result, error) {
	r.calls = append(r.calls, c.Name+" "+strings.Join(c.Args, " "))
	switch c.Name {
	case "sshd":
		return platform.Result{ExitCode: r.sshdExit, Stderr: []byte(r.sshdErr)}, nil
	case "journalctl":
		return platform.Result{Stdout: []byte(r.journal)}, nil
	}
	return platform.Result{}, nil
}

func newManager(t *testing.T) (*Manager, *files, *keyStore, *runner) {
	t.Helper()
	dir := t.TempDir()
	f := &files{dir: dir}
	ks := &keyStore{m: map[string][]byte{}}
	r := &runner{}
	m := &Manager{
		Files: f, Keys: ks, Runner: r, Users: func() []string { return []string{"alice"} },
		Dropin: "/etc/ssh/sshd_config.d/40-diparnux-ssh.conf", ConfigPath: filepath.Join(dir, "40-diparnux-ssh.conf"),
		HostKeyDir: dir,
	}
	return m, f, ks, r
}

func TestRenderAndParse(t *testing.T) {
	no := false
	if got := Render(Settings{Port: 22}); got != "" {
		t.Fatalf("defaults must render nothing, got %q", got)
	}
	text := Render(Settings{Port: 2222, PasswordLogin: &no})
	if !strings.Contains(text, "Port 2222\n") || !strings.Contains(text, "PasswordAuthentication no\n") || !strings.Contains(text, "KbdInteractiveAuthentication no") {
		t.Fatalf("text = %q", text)
	}
	got := Parse(text)
	if got.Port != 2222 || got.PasswordLogin == nil || *got.PasswordLogin {
		t.Fatalf("parsed = %+v", got)
	}
	if got := Parse(""); got.Port != 22 || got.PasswordLogin != nil {
		t.Fatalf("empty = %+v", got)
	}
}

func TestParseKeyLine(t *testing.T) {
	line := testKey(t, "alice@laptop")
	k, err := ParseKeyLine(line)
	if err != nil || k.Type != "ssh-ed25519" || !strings.HasPrefix(k.Fingerprint, "SHA256:") || k.Comment != "alice@laptop" {
		t.Fatalf("k = %+v, %v", k, err)
	}
	bad := []string{
		"", "ssh-ed25519", "ssh-ed25519 !!!notbase64", "ssh-dss AAAA", "command=\"x\" " + line,
		"ssh-rsa " + strings.Fields(line)[1], // 선언한 종류와 본문이 다르다
		line + "\nssh-ed25519 AAAA",
	}
	for _, b := range bad {
		if _, err := ParseKeyLine(b); err == nil {
			t.Errorf("%q accepted", b)
		}
	}
}

func TestAddAndRemoveKeepOtherLines(t *testing.T) {
	a, b := testKey(t, "a"), testKey(t, "b")
	data := []byte("# my keys\n" + a + "\n")
	out, k, added, err := AddKey(data, b)
	if err != nil || !added {
		t.Fatalf("added = %v, %v", added, err)
	}
	if again, _, added2, _ := AddKey(out, b); added2 || string(again) != string(out) {
		t.Fatal("the same key was added twice")
	}
	if len(ParseKeys(out)) != 2 {
		t.Fatalf("keys = %v", ParseKeys(out))
	}
	rest, removed := RemoveKey(out, k.Fingerprint)
	if !removed || len(ParseKeys(rest)) != 1 || !strings.Contains(string(rest), "# my keys") || strings.Contains(string(rest), " b") {
		t.Fatalf("rest = %q removed = %v", rest, removed)
	}
	if _, removed := RemoveKey(rest, "SHA256:nope"); removed {
		t.Fatal("an unknown fingerprint removed something")
	}
}

func TestApplyRefusesToLockYouOut(t *testing.T) {
	m, f, ks, _ := newManager(t)
	no := false
	err := m.Apply(context.Background(), Settings{Port: 22, PasswordLogin: &no}, true)
	if err == nil || err.(*Error).Code != CodeNoKeys {
		t.Fatalf("err = %v", err)
	}
	if _, serr := os.Stat(filepath.Join(f.dir, "40-diparnux-ssh.conf")); serr == nil {
		t.Fatal("a refused change wrote the file")
	}
	ks.m["alice"] = []byte(testKey(t, "alice") + "\n")
	if err := m.Apply(context.Background(), Settings{Port: 2222, PasswordLogin: &no}, true); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(filepath.Join(f.dir, "40-diparnux-ssh.conf"))
	if !strings.Contains(string(data), "Port 2222") {
		t.Fatalf("file = %s", data)
	}
}

func TestApplyRevertsWhenSSHDRejects(t *testing.T) {
	m, f, _, r := newManager(t)
	old := "Port 2200\n"
	if err := os.WriteFile(filepath.Join(f.dir, "40-diparnux-ssh.conf"), []byte(old), 0o644); err != nil {
		t.Fatal(err)
	}
	r.sshdExit, r.sshdErr = 255, "/etc/ssh/sshd_config.d/40-diparnux-ssh.conf line 1: Bad port\nmore"
	yes := true
	err := m.Apply(context.Background(), Settings{Port: 2222, PasswordLogin: &yes}, true)
	if err == nil || !strings.Contains(err.Error(), "Bad port") {
		t.Fatalf("err = %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(f.dir, "40-diparnux-ssh.conf"))
	if string(data) != old {
		t.Fatalf("the previous file was not restored: %q", data)
	}
	for _, c := range r.calls {
		if strings.HasPrefix(c, "systemctl reload") {
			t.Fatal("sshd was reloaded after a rejected configuration")
		}
	}
}

func TestKeyManagementIsLimitedToLoginAccounts(t *testing.T) {
	m, _, ks, _ := newManager(t)
	ctx := context.Background()
	line := testKey(t, "alice@laptop")
	k, added, err := m.AddKey(ctx, "alice", line)
	if err != nil || !added {
		t.Fatalf("add = %+v %v %v", k, added, err)
	}
	if _, _, err := m.AddKey(ctx, "mallory", line); err == nil {
		t.Fatal("a key was added for an unknown account")
	}
	if _, _, err := m.AddKey(ctx, "root", line); err == nil {
		t.Fatal("a key was added for root")
	}
	keys, err := m.ListKeys(ctx, "alice")
	if err != nil || len(keys) != 1 {
		t.Fatalf("keys = %v %v", keys, err)
	}
	if err := m.RemoveKey(ctx, "alice", k.Fingerprint); err != nil {
		t.Fatal(err)
	}
	if len(ParseKeys(ks.m["alice"])) != 0 {
		t.Fatal("the key is still registered")
	}
	if err := m.RemoveKey(ctx, "alice", k.Fingerprint); err == nil {
		t.Fatal("removing a missing key succeeded")
	}
}

func TestParseAuthLog(t *testing.T) {
	log := strings.Join([]string{
		"2026-10-13T12:00:00+0900 box sshd[1]: Accepted publickey for alice from 192.168.0.5 port 50000 ssh2: ED25519 SHA256:abc",
		"2026-10-13T12:01:00+0900 box sshd[2]: Failed password for invalid user admin from 10.0.0.9 port 40000 ssh2",
		"2026-10-13T12:02:00+0900 box sshd[3]: Invalid user test from 10.0.0.9 port 40001",
		"2026-10-13T12:03:00+0900 box sshd[4]: Failed password for alice from 192.168.0.5 port 50001 ssh2",
		"2026-10-13T12:04:00+0900 box sshd[5]: Server listening on 0.0.0.0 port 22.",
	}, "\n")
	ev := ParseAuthLog(log, 10)
	if len(ev) != 4 || ev[0].User != "alice" || ev[0].Result != "failed" || ev[3].Result != "accepted" || ev[3].Method != "publickey" || ev[3].From != "192.168.0.5" {
		t.Fatalf("events = %+v", ev)
	}
	if got := ParseAuthLog(log, 2); len(got) != 2 {
		t.Fatalf("limited = %d", len(got))
	}
}
