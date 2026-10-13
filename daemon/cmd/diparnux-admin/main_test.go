package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runHelper(t *testing.T, root, stdin string, args ...string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	code := run(args, strings.NewReader(stdin), &out, &errb, root)
	return out.String(), errb.String(), code
}

func TestWriteAndRemoveAllowedFile(t *testing.T) {
	root := t.TempDir()
	p := "/etc/systemd/network/10-diparnux-enp3s0.network"
	out, _, code := runHelper(t, root, "[Match]\nName=enp3s0\n", "write", p)
	if code != 0 || !strings.Contains(out, "DIPARNUX_ADMIN_RESULT=ok") {
		t.Fatalf("code %d out %q", code, out)
	}
	got, err := os.ReadFile(filepath.Join(root, p))
	if err != nil || string(got) != "[Match]\nName=enp3s0\n" {
		t.Fatalf("file = %q, %v", got, err)
	}
	if st, _ := os.Stat(filepath.Join(root, p)); st.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v", st.Mode())
	}
	// 같은 내용을 다시 써도 되고 임시 파일이 남지 않는다.
	runHelper(t, root, "second\n", "write", p)
	entries, _ := os.ReadDir(filepath.Dir(filepath.Join(root, p)))
	if len(entries) != 1 {
		t.Fatalf("entries = %v", entries)
	}
	if _, _, code := runHelper(t, root, "", "remove", p); code != 0 {
		t.Fatal("remove failed")
	}
	if _, _, code := runHelper(t, root, "", "remove", p); code != 0 {
		t.Fatal("removing a missing file must not fail")
	}
}

func TestWriteSecretUsesTightMode(t *testing.T) {
	root := t.TempDir()
	p := "/etc/systemd/network/50-diparnux-wg0.netdev"
	if _, _, code := runHelper(t, root, "PrivateKey=x\n", "write", p, "--secret"); code != 0 {
		t.Fatal("write failed")
	}
	if st, _ := os.Stat(filepath.Join(root, p)); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode = %v", st.Mode())
	}
}

func TestRefusesOtherPaths(t *testing.T) {
	root := t.TempDir()
	for _, p := range []string{"/etc/passwd", "/etc/systemd/network/../../passwd", "/etc/ssh/sshd_config", "/root/.bashrc"} {
		out, _, code := runHelper(t, root, "x", "write", p)
		if code == 0 || !strings.Contains(out, "error:invalid_path") {
			t.Errorf("%s: code %d out %q", p, code, out)
		}
		if _, err := os.Stat(filepath.Join(root, p)); err == nil {
			t.Errorf("%s was created", p)
		}
	}
}

func TestRefusesLargeContentAndRoot(t *testing.T) {
	root := t.TempDir()
	big := strings.Repeat("a", 300<<10)
	out, _, code := runHelper(t, root, big, "write", "/etc/hosts")
	if code == 0 || !strings.Contains(out, "error:too_large") {
		t.Fatalf("code %d out %q", code, out)
	}
	out, _, code = runHelper(t, root, "", "authkeys-read", "root")
	if code == 0 || !strings.Contains(out, "error:invalid_user") {
		t.Fatalf("root keys: code %d out %q", code, out)
	}
}
