package apikeys

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// newStore는 임시 디렉터리에 키 파일과 메타 파일을 두는 Store를 만든다.
// 시각은 고정 값을 주입한다.
func newStore(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	fixed := time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)
	return &Store{
		KeyFile:  filepath.Join(dir, "api-keys"),
		MetaFile: filepath.Join(dir, "api-keys.json"),
		Now:      func() time.Time { return fixed },
	}
}

// readLines는 키 파일을 줄 목록으로 읽는다.
func readLines(t *testing.T, file string) []string {
	t.Helper()
	data, err := os.ReadFile(file)
	if err != nil {
		t.Fatalf("read %s: %v", file, err)
	}
	s := strings.TrimSuffix(string(data), "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func TestCreateListDeleteRoundTrip(t *testing.T) {
	s := newStore(t)

	k1, p1, err := s.Create("laptop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	k2, p2, err := s.Create("phone")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}

	keys, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(keys) != 2 || keys[0].ID != k1.ID || keys[1].ID != k2.ID {
		t.Fatalf("List = %+v", keys)
	}
	if keys[0].Label != "laptop" || keys[1].Label != "phone" {
		t.Fatalf("labels = %q %q", keys[0].Label, keys[1].Label)
	}
	if !keys[0].Created.Equal(time.Date(2026, 10, 11, 12, 0, 0, 0, time.UTC)) {
		t.Fatalf("created = %v", keys[0].Created)
	}
	if keys[0].Prefix != p1[:8] || keys[1].Prefix != p2[:8] {
		t.Fatalf("prefix mismatch: %q %q", keys[0].Prefix, keys[1].Prefix)
	}
	if keys[0].SHA256 != sha256Hex(p1) {
		t.Fatalf("stored hash does not match plaintext")
	}

	if n, err := s.Count(); err != nil || n != 2 {
		t.Fatalf("Count = %d, %v; want 2", n, err)
	}
	lines := readLines(t, s.KeyFile)
	if len(lines) != 2 || lines[0] != p1 || lines[1] != p2 {
		t.Fatalf("key file lines = %v", lines)
	}

	if err := s.Delete(k1.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	lines = readLines(t, s.KeyFile)
	if len(lines) != 1 || lines[0] != p2 {
		t.Fatalf("key file after delete = %v, want only %s", lines, p2)
	}
	if n, _ := s.Count(); n != 1 {
		t.Fatalf("Count after delete = %d, want 1", n)
	}
}

func TestDeleteUnknownID(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.Create("a"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	err := s.Delete("ffffffffffffffffffffffffffffffff")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("Delete unknown id: err = %v, want ErrNotFound", err)
	}
	if n, _ := s.Count(); n != 1 {
		t.Fatalf("unknown delete changed the store, count = %d", n)
	}
}

func TestDeleteKeepsComments(t *testing.T) {
	s := newStore(t)
	_, p1, err := s.Create("keep")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	k2, p2, err := s.Create("drop")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	// 사람이 넣은 주석과 빈 줄은 지우기 작업에서 살아남아야 한다.
	data := "# managed by hand\n" + p1 + "\n\n" + p2 + "\n"
	if err := os.WriteFile(s.KeyFile, []byte(data), 0o640); err != nil {
		t.Fatalf("write key file: %v", err)
	}
	if err := s.Delete(k2.ID); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	got, err := os.ReadFile(s.KeyFile)
	if err != nil {
		t.Fatalf("read key file: %v", err)
	}
	want := "# managed by hand\n" + p1 + "\n\n"
	if string(got) != want {
		t.Fatalf("key file = %q, want %q", got, want)
	}
}

func TestKeyFormat(t *testing.T) {
	s := newStore(t)
	k, plain, err := s.Create("format")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !strings.HasPrefix(plain, "opr_") {
		t.Fatalf("key %q lacks opr_ prefix", plain)
	}
	if len(plain) != 47 {
		t.Fatalf("key length = %d, want 47", len(plain))
	}
	raw, err := base64.RawURLEncoding.DecodeString(plain[4:])
	if err != nil {
		t.Fatalf("key body is not unpadded base64url: %v", err)
	}
	if len(raw) != 32 {
		t.Fatalf("decoded key body = %d bytes, want 32", len(raw))
	}
	if len(k.ID) != 32 {
		t.Fatalf("ID %q has length %d, want 32", k.ID, len(k.ID))
	}
	if _, err := hex.DecodeString(k.ID); err != nil {
		t.Fatalf("ID is not hex: %v", err)
	}
	if len(k.SHA256) != 64 {
		t.Fatalf("sha256 length = %d", len(k.SHA256))
	}
}

func TestDeterministicEncoding(t *testing.T) {
	// 난수원을 고정하면 키 본문은 base64url(0x00 * 32)이고 ID는 hex(0x00 * 16)이다.
	s := newStore(t)
	s.Rand = bytes.NewReader(make([]byte, 48))
	k, plain, err := s.Create("zero")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if want := "opr_" + strings.Repeat("A", 43); plain != want {
		t.Fatalf("plain = %q, want %q", plain, want)
	}
	if k.ID != strings.Repeat("0", 32) {
		t.Fatalf("ID = %q", k.ID)
	}
}

func TestCreateFailsWhenRandomSourceIsShort(t *testing.T) {
	s := newStore(t)
	s.Rand = bytes.NewReader(make([]byte, 10))
	if _, _, err := s.Create("short"); err == nil {
		t.Fatalf("Create succeeded with a short random source")
	}
	if _, err := os.Stat(s.KeyFile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("key file was written despite random failure: %v", err)
	}
}

func TestFileModes(t *testing.T) {
	s := newStore(t)
	if _, _, err := s.Create("modes"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	ki, err := os.Stat(s.KeyFile)
	if err != nil {
		t.Fatalf("stat key file: %v", err)
	}
	if got := ki.Mode().Perm(); got != 0o640 {
		t.Fatalf("key file mode = %o, want 640", got)
	}
	mi, err := os.Stat(s.MetaFile)
	if err != nil {
		t.Fatalf("stat meta file: %v", err)
	}
	if got := mi.Mode().Perm(); got != 0o600 {
		t.Fatalf("meta file mode = %o, want 600", got)
	}
}

func TestLabelValidation(t *testing.T) {
	tests := []struct {
		name    string
		label   string
		wantErr bool
	}{
		{"empty", "", true},
		{"65 characters", strings.Repeat("a", 65), true},
		{"64 characters", strings.Repeat("a", 64), false},
		{"space is printable", "my laptop", false},
		{"korean is rejected", "노트북", true},
		{"newline is rejected", "a\nb", true},
		{"tab is rejected", "a\tb", true},
		{"DEL is rejected", "a\x7fb", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			_, _, err := s.Create(tc.label)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Create(%q) err = %v, wantErr %v", tc.label, err, tc.wantErr)
			}
			if tc.wantErr {
				if _, statErr := os.Stat(s.KeyFile); !errors.Is(statErr, fs.ErrNotExist) {
					t.Fatalf("rejected label still wrote the key file")
				}
			}
		})
	}
}

func TestNoDuplicateKeys(t *testing.T) {
	s := newStore(t)
	seen := make(map[string]bool)
	for i := 0; i < 200; i++ {
		_, plain, err := s.Create("bulk")
		if err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
		if seen[plain] {
			t.Fatalf("duplicate key at %d", i)
		}
		seen[plain] = true
	}
	if n, _ := s.Count(); n != 200 {
		t.Fatalf("Count = %d, want 200", n)
	}
}

func TestConcurrentCreate(t *testing.T) {
	s := newStore(t)
	const workers = 40
	var wg sync.WaitGroup
	plains := make(chan string, workers)
	errs := make(chan error, workers)
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, p, err := s.Create("concurrent")
			if err != nil {
				errs <- err
				return
			}
			plains <- p
		}()
	}
	wg.Wait()
	close(plains)
	close(errs)
	for err := range errs {
		t.Fatalf("concurrent Create: %v", err)
	}

	uniq := make(map[string]bool)
	for p := range plains {
		uniq[p] = true
	}
	if len(uniq) != workers {
		t.Fatalf("got %d unique keys, want %d", len(uniq), workers)
	}
	if n, err := s.Count(); err != nil || n != workers {
		t.Fatalf("Count = %d, %v; want %d", n, err, workers)
	}
	lines := readLines(t, s.KeyFile)
	if len(lines) != workers {
		t.Fatalf("key file has %d lines, want %d", len(lines), workers)
	}
	for _, l := range lines {
		if !uniq[l] {
			t.Fatalf("key file has a key that Create did not return")
		}
	}
}

func TestBrokenMetaFile(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"not JSON", "{not json"},
		{"empty file", ""},
		{"unknown field", `{"keys":[],"extra":1}`},
		{"bad id", `{"keys":[{"id":"zz","label":"a","created":"2026-10-11T00:00:00Z","prefix":"opr_abcd","sha256":"` + strings.Repeat("a", 64) + `"}]}`},
		{"trailing data", `{"keys":[]} {}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			if err := os.WriteFile(s.MetaFile, []byte(tc.body), 0o600); err != nil {
				t.Fatalf("write meta: %v", err)
			}
			if _, err := s.List(); err == nil {
				t.Errorf("List accepted a broken meta file")
			}
			if _, err := s.Count(); err == nil {
				t.Errorf("Count accepted a broken meta file")
			}
			if _, _, err := s.Create("x"); err == nil {
				t.Errorf("Create accepted a broken meta file")
			}
			if _, err := os.Stat(s.KeyFile); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Create touched the key file although the meta file is broken")
			}
		})
	}
}

func TestEmptyStore(t *testing.T) {
	s := newStore(t)
	keys, err := s.List()
	if err != nil || len(keys) != 0 {
		t.Fatalf("List on empty store = %v, %v", keys, err)
	}
	if n, err := s.Count(); err != nil || n != 0 {
		t.Fatalf("Count on empty store = %d, %v", n, err)
	}
}

func TestDefaultClockAndRandom(t *testing.T) {
	// Now과 Rand를 주입하지 않아도 동작해야 한다.
	dir := t.TempDir()
	s := &Store{KeyFile: filepath.Join(dir, "k"), MetaFile: filepath.Join(dir, "m.json")}
	before := time.Now().Add(-time.Second)
	k, plain, err := s.Create("default")
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if k.Created.Before(before) {
		t.Fatalf("created %v is before %v", k.Created, before)
	}
	if len(plain) != 47 {
		t.Fatalf("plain length %d", len(plain))
	}
}

func TestSnapshotEmptyAndCanonicalCopy(t *testing.T) {
	s := newStore(t)
	snapshot, err := s.Snapshot()
	if err != nil || snapshot == nil || len(snapshot) != 0 {
		t.Fatalf("empty snapshot length/nil/error = %d/%v/%v", len(snapshot), snapshot == nil, err)
	}

	if err := os.WriteFile(s.KeyFile, []byte("# local comment\n\n"), 0o640); err != nil {
		t.Fatal("write comments:", err)
	}
	snapshot, err = s.Snapshot()
	if err != nil || snapshot == nil || len(snapshot) != 0 {
		t.Fatalf("comment-only snapshot length/nil/error = %d/%v/%v", len(snapshot), snapshot == nil, err)
	}

	first, firstPlain, err := s.Create("first")
	if err != nil {
		t.Fatal("create first key:", err)
	}
	second, secondPlain, err := s.Create("second")
	if err != nil {
		t.Fatal("create second key:", err)
	}
	want := []byte(firstPlain + "\n" + secondPlain + "\n")
	snapshot, err = s.Snapshot()
	if err != nil || !bytes.Equal(snapshot, want) {
		t.Fatal("snapshot did not contain the canonical keys")
	}
	snapshot[0] ^= 0xff
	again, err := s.Snapshot()
	if err != nil || !bytes.Equal(again, want) {
		t.Fatal("snapshot was not an independent copy")
	}
	if first.ID == second.ID {
		t.Fatal("test key ids unexpectedly collided")
	}
}

func TestSnapshotRejectsMismatchMalformedDuplicatesAndReadErrors(t *testing.T) {
	t.Run("metadata without actual key", func(t *testing.T) {
		s := newStore(t)
		if _, _, err := s.Create("valid"); err != nil {
			t.Fatal("create key:", err)
		}
		if err := os.WriteFile(s.KeyFile, nil, 0o640); err != nil {
			t.Fatal("clear key file:", err)
		}
		assertStoreRejectsInconsistency(t, s)
	})
	t.Run("malformed key", func(t *testing.T) {
		s := newStore(t)
		if err := os.WriteFile(s.KeyFile, []byte("opr_not-a-valid-key\n"), 0o640); err != nil {
			t.Fatal("write malformed key:", err)
		}
		assertStoreRejectsInconsistency(t, s)
	})
	t.Run("duplicate plaintext", func(t *testing.T) {
		s := newStore(t)
		_, plain, err := s.Create("duplicate")
		if err != nil {
			t.Fatal("create key:", err)
		}
		if err := os.WriteFile(s.KeyFile, []byte(plain+"\n"+plain+"\n"), 0o640); err != nil {
			t.Fatal("duplicate key line:", err)
		}
		assertStoreRejectsInconsistency(t, s)
	})
	t.Run("key file read error", func(t *testing.T) {
		s := newStore(t)
		if err := os.Mkdir(s.KeyFile, 0o700); err != nil {
			t.Fatal("make key path a directory:", err)
		}
		if _, err := s.Snapshot(); err == nil {
			t.Fatal("snapshot accepted an unreadable key file")
		}
	})
	t.Run("metadata read error", func(t *testing.T) {
		s := newStore(t)
		if err := os.Mkdir(s.MetaFile, 0o700); err != nil {
			t.Fatal("make metadata path a directory:", err)
		}
		if _, err := s.Snapshot(); err == nil {
			t.Fatal("snapshot accepted unreadable metadata")
		}
	})
}

func assertStoreRejectsInconsistency(t *testing.T, s *Store) {
	t.Helper()
	if _, err := s.Snapshot(); err == nil {
		t.Error("Snapshot accepted an inconsistent or malformed store")
	}
	if _, err := s.List(); err == nil {
		t.Error("List accepted an inconsistent or malformed store")
	}
	if _, err := s.Count(); err == nil {
		t.Error("Count accepted an inconsistent or malformed store")
	}
	if _, _, err := s.Create("must-fail"); err == nil {
		t.Error("Create accepted an inconsistent or malformed store")
	}
}

func TestDeleteMetadataWriteFailureNeverRestoresRevokedKey(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("run as an unprivileged user to exercise directory permissions")
	}
	dir := t.TempDir()
	metaDir := filepath.Join(dir, "metadata")
	if err := os.Mkdir(metaDir, 0o700); err != nil {
		t.Fatal("make metadata directory:", err)
	}
	s := &Store{KeyFile: filepath.Join(dir, "api-keys"), MetaFile: filepath.Join(metaDir, "keys.json")}
	revoked, revokedPlain, err := s.Create("revoked")
	if err != nil {
		t.Fatal("create revoked key:", err)
	}
	remaining, remainingPlain, err := s.Create("remaining")
	if err != nil {
		t.Fatal("create remaining key:", err)
	}
	if err := os.Chmod(metaDir, 0o500); err != nil {
		t.Fatal("remove metadata write permission:", err)
	}
	if err := s.Delete(revoked.ID); err == nil {
		t.Fatal("Delete succeeded when metadata could not be written")
	}
	if err := os.Chmod(metaDir, 0o700); err != nil {
		t.Fatal("restore metadata write permission:", err)
	}
	keyFile, err := os.ReadFile(s.KeyFile)
	if err != nil {
		t.Fatal("read actual key file:", err)
	}
	if bytes.Contains(keyFile, []byte(revokedPlain)) || !bytes.Contains(keyFile, []byte(remainingPlain)) {
		t.Fatal("failed metadata write changed the revocation result")
	}
	if _, err := s.Snapshot(); err == nil {
		t.Fatal("Snapshot accepted stale metadata after revocation")
	}
	if _, err := s.List(); err == nil {
		t.Fatal("List exposed stale metadata as actual keys")
	}
	if count, err := s.Count(); err == nil || count != 0 {
		t.Fatal("Count treated stale metadata as actual keys")
	}

	// 같은 삭제를 재시도하면 오래된 메타 항목만 없어지고 폐기 키는 되살아나지 않는다.
	if err := s.Delete(revoked.ID); err != nil {
		t.Fatal("repair stale metadata on delete retry:", err)
	}
	snapshot, err := s.Snapshot()
	if err != nil || !bytes.Equal(snapshot, []byte(remainingPlain+"\n")) {
		t.Fatal("retry did not leave only the still-valid key")
	}
	keys, err := s.List()
	if err != nil || len(keys) != 1 || keys[0].ID != remaining.ID {
		t.Fatal("retry did not restore a usable key list")
	}
}
