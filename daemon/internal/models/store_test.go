package models

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// goodSHA is a syntactically valid SHA-256 for sidecar files. Its value is not checked against any content here.
const goodSHA = "8ccc5cd1f1b3602548715ae25a66ed73fd5dc68a210412eea643eb20eb75a135"

// ggufBytes builds a minimal GGUF v3 file with general.architecture and <arch>.block_count.
func ggufBytes(arch string, block uint32) []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString("GGUF")
	binary.Write(&b, le, uint32(3))
	binary.Write(&b, le, uint64(0))
	binary.Write(&b, le, uint64(2))
	writeKey := func(k string) {
		binary.Write(&b, le, uint64(len(k)))
		b.WriteString(k)
	}
	writeKey("general.architecture")
	binary.Write(&b, le, uint32(8))
	binary.Write(&b, le, uint64(len(arch)))
	b.WriteString(arch)
	writeKey(arch + ".block_count")
	binary.Write(&b, le, uint32(4))
	binary.Write(&b, le, block)
	return b.Bytes()
}

// write creates a file under dir with the given content.
func write(t *testing.T, dir, name string, data []byte) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// fixture builds a store with a mix of good, incomplete, and invalid entries.
func fixture(t *testing.T) Store {
	t.Helper()
	dir := t.TempDir()
	write(t, dir, "good.gguf", ggufBytes("gemma3", 26))
	write(t, dir, "good.gguf.json", []byte(`{"repo":"ggml-org/gemma-3-1b-it-GGUF","revision":"f9c28bcd","sha256":"`+goodSHA+`","license":"gemma"}`))
	write(t, dir, "nosidecar.gguf", ggufBytes("llama", 16))
	write(t, dir, "broken.gguf", ggufBytes("llama", 12))
	write(t, dir, "broken.gguf.json", []byte(`{"repo":`))
	write(t, dir, "badsha.gguf", ggufBytes("llama", 12))
	write(t, dir, "badsha.gguf.json", []byte(`{"repo":"a/b","sha256":"not-hex"}`))
	write(t, dir, "garbage.gguf", []byte("this is not a GGUF file"))
	write(t, dir, "partial.gguf", ggufBytes("llama", 8))
	write(t, dir, "partial.gguf.part", []byte("half"))
	write(t, dir, "notes.txt", []byte("not a model"))
	write(t, dir, ".hidden.gguf", ggufBytes("llama", 8))
	if err := os.Mkdir(filepath.Join(dir, "dir.gguf"), 0o755); err != nil {
		t.Fatal(err)
	}
	return Store{Dir: dir}
}

// idsOf returns the IDs of models in order.
func idsOf(models []Model) []string {
	ids := make([]string, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	return ids
}

// byID returns the model with the given ID.
func byID(t *testing.T, models []Model, id string) Model {
	t.Helper()
	for _, m := range models {
		if m.ID == id {
			return m
		}
	}
	t.Fatalf("model %s not listed", id)
	return Model{}
}

func TestListSkipsNonModelsAndKeepsIncomplete(t *testing.T) {
	s := fixture(t)
	models, err := s.List()
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	want := []string{"badsha.gguf", "broken.gguf", "garbage.gguf", "good.gguf", "nosidecar.gguf", "partial.gguf"}
	if got := idsOf(models); !reflect.DeepEqual(got, want) {
		t.Fatalf("List IDs = %q\nwant %q", got, want)
	}
}

func TestListGoodModelFields(t *testing.T) {
	s := fixture(t)
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	good := byID(t, models, "good.gguf")
	if good.Incomplete {
		t.Fatal("good model marked incomplete")
	}
	if good.Path != filepath.Join(s.Dir, "good.gguf") || good.Size != int64(len(ggufBytes("gemma3", 26))) {
		t.Fatalf("path or size wrong: %+v", good)
	}
	if good.Repo != "ggml-org/gemma-3-1b-it-GGUF" || good.Revision != "f9c28bcd" || good.SHA256 != goodSHA || good.License != "gemma" {
		t.Fatalf("sidecar fields wrong: %+v", good)
	}
	if good.Architecture != "gemma3" || good.BlockCount != 26 || good.ContextLength != 0 || good.FileTypeName != "" {
		t.Fatalf("GGUF fields wrong: %+v", good)
	}
	if good.Default {
		t.Fatal("no default set, but Default is true")
	}
}

func TestListIncompleteFlags(t *testing.T) {
	s := fixture(t)
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{
		"nosidecar.gguf": true,  // sidecar missing
		"broken.gguf":    true,  // sidecar is not JSON
		"badsha.gguf":    true,  // sidecar digest is not hex
		"garbage.gguf":   true,  // GGUF metadata unreadable
		"partial.gguf":   true,  // a .part file exists for this name
		"good.gguf":      false, // complete
	}
	for id, want := range cases {
		if got := byID(t, models, id).Incomplete; got != want {
			t.Errorf("%s: Incomplete = %v, want %v", id, got, want)
		}
	}
	// A broken sidecar must leave every sidecar field empty.
	broken := byID(t, models, "broken.gguf")
	if broken.Repo != "" || broken.SHA256 != "" || broken.License != "" {
		t.Fatalf("broken sidecar fields not empty: %+v", broken)
	}
	// The GGUF fields of an unreadable file are zero.
	garbage := byID(t, models, "garbage.gguf")
	if garbage.Architecture != "" || garbage.BlockCount != 0 {
		t.Fatalf("garbage model has GGUF fields: %+v", garbage)
	}
}

func TestListMissingDirectory(t *testing.T) {
	s := Store{Dir: filepath.Join(t.TempDir(), "missing")}
	models, err := s.List()
	if err != nil || len(models) != 0 {
		t.Fatalf("List of missing directory = %v, %v; want empty and nil", models, err)
	}
}

func TestListSymlinks(t *testing.T) {
	s := fixture(t)
	outside := t.TempDir()
	write(t, outside, "secret.gguf", ggufBytes("evil", 99))
	if err := os.Symlink(filepath.Join(outside, "secret.gguf"), filepath.Join(s.Dir, "outlink.gguf")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(s.Dir, "good.gguf"), filepath.Join(s.Dir, "inlink.gguf")); err != nil {
		t.Fatal(err)
	}
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	ids := idsOf(models)
	for _, id := range ids {
		if id == "outlink.gguf" {
			t.Fatal("symbolic link to a file outside the directory was listed")
		}
	}
	if !containsString(ids, "inlink.gguf") {
		t.Fatalf("symbolic link inside the directory was not listed: %q", ids)
	}
	if err := s.WriteSidecar("outlink.gguf", Sidecar{}); !errors.Is(err, ErrOutsideDir) {
		t.Fatalf("WriteSidecar through outside link: error %v, want ErrOutsideDir", err)
	}
	if err := s.SetDefault("outlink.gguf"); !errors.Is(err, ErrOutsideDir) {
		t.Fatalf("SetDefault through outside link: error %v, want ErrOutsideDir", err)
	}
}

// containsString reports whether list has s.
func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func TestSetDefaultAndDefault(t *testing.T) {
	s := fixture(t)
	if id, err := s.Default(); err != nil || id != "" {
		t.Fatalf("Default before set = %q, %v", id, err)
	}
	if err := s.SetDefault("good.gguf"); err != nil {
		t.Fatalf("SetDefault: %v", err)
	}
	if id, err := s.Default(); err != nil || id != "good.gguf" {
		t.Fatalf("Default = %q, %v", id, err)
	}
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range models {
		if m.Default != (m.ID == "good.gguf") {
			t.Fatalf("%s: Default = %v", m.ID, m.Default)
		}
	}
	if err := s.SetDefault("good.gguf"); err != nil {
		t.Fatalf("second SetDefault: %v", err)
	}
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".tmp-") {
			t.Fatalf("temporary file %s left behind", e.Name())
		}
	}
}

func TestSetDefaultRequiresExistingModel(t *testing.T) {
	s := fixture(t)
	err := s.SetDefault("missing.gguf")
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("SetDefault of missing model: error %v, want ErrNotExist", err)
	}
	if _, statErr := os.Stat(filepath.Join(s.Dir, defaultFile)); !errors.Is(statErr, fs.ErrNotExist) {
		t.Fatal("default file written for a missing model")
	}
}

func TestDefaultBrokenFileDoesNotHideModels(t *testing.T) {
	s := fixture(t)
	write(t, s.Dir, defaultFile, []byte("../escape.gguf\n"))
	if _, err := s.Default(); !errors.Is(err, ErrInvalidID) {
		t.Fatalf("Default with invalid content: error %v, want ErrInvalidID", err)
	}
	models, err := s.List()
	if err != nil {
		t.Fatalf("List with broken default file: %v", err)
	}
	if len(models) == 0 {
		t.Fatal("broken default file hid every model")
	}
	for _, m := range models {
		if m.Default {
			t.Fatalf("%s marked default from a broken file", m.ID)
		}
	}
}

func TestDefaultOversizedFile(t *testing.T) {
	s := fixture(t)
	write(t, s.Dir, defaultFile, bytes.Repeat([]byte("a"), maxDefaultBytes+1))
	if _, err := s.Default(); err == nil {
		t.Fatal("Default with oversized file: want error")
	}
}

func TestWriteSidecarRoundTrip(t *testing.T) {
	s := fixture(t)
	want := Sidecar{Repo: "a/b", Revision: "main", SHA256: goodSHA, License: "mit"}
	if err := s.WriteSidecar("nosidecar.gguf", want); err != nil {
		t.Fatalf("WriteSidecar: %v", err)
	}
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	m := byID(t, models, "nosidecar.gguf")
	if m.Incomplete {
		t.Fatal("model still incomplete after its sidecar was written")
	}
	got := Sidecar{Repo: m.Repo, Revision: m.Revision, SHA256: m.SHA256, License: m.License}
	if got != want {
		t.Fatalf("sidecar read back = %+v, want %+v", got, want)
	}
}

func TestWriteSidecarRequiresModel(t *testing.T) {
	s := fixture(t)
	if err := s.WriteSidecar("missing.gguf", Sidecar{}); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("error = %v, want ErrNotExist", err)
	}
}

func TestRemove(t *testing.T) {
	s := fixture(t)
	if err := s.Remove("partial.gguf"); err != nil {
		t.Fatalf("Remove: %v", err)
	}
	for _, name := range []string{"partial.gguf", "partial.gguf.part", "partial.gguf.json"} {
		if _, err := os.Stat(filepath.Join(s.Dir, name)); !errors.Is(err, fs.ErrNotExist) {
			t.Fatalf("%s still exists after Remove", name)
		}
	}
	if err := s.Remove("partial.gguf"); err != nil {
		t.Fatalf("Remove of an already removed model: %v", err)
	}
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if containsString(idsOf(models), "partial.gguf") {
		t.Fatal("removed model still listed")
	}
}

func TestInvalidIDsAreRejected(t *testing.T) {
	s := fixture(t)
	long := strings.Repeat("a", maxIDLen) + ".gguf"
	ids := []string{
		"", ".gguf", "../good.gguf", "sub/good.gguf", `sub\good.gguf`, "/abs.gguf", "..",
		".hidden.gguf", "good.bin", "good.gguf.json", "good\n.gguf", "good\x00.gguf",
		"x\x7f.gguf", long, "bad\xff.gguf",
	}
	for _, id := range ids {
		if err := s.Remove(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("Remove(%q): error %v, want ErrInvalidID", id, err)
		}
		if err := s.SetDefault(id); !errors.Is(err, ErrInvalidID) {
			t.Errorf("SetDefault(%q): error %v, want ErrInvalidID", id, err)
		}
		if err := s.WriteSidecar(id, Sidecar{}); !errors.Is(err, ErrInvalidID) {
			t.Errorf("WriteSidecar(%q): error %v, want ErrInvalidID", id, err)
		}
	}
	// The directory must be untouched after the rejected calls.
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(models) != 6 {
		t.Fatalf("models after rejected calls = %d, want 6", len(models))
	}
}

func TestValidIDAcceptsNormalNames(t *testing.T) {
	for _, id := range []string{"a.gguf", "gemma-3-1b-it-Q4_K_M.gguf", "with space.gguf", "x.y.gguf", strings.Repeat("a", maxIDLen-5) + ".gguf"} {
		if err := validID(id); err != nil {
			t.Errorf("validID(%q): %v", id, err)
		}
	}
}

func TestListSortsByName(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, name := range []string{"c.gguf", "a.gguf", "b.gguf"} {
		write(t, s.Dir, name, ggufBytes("llama", 1))
	}
	models, err := s.List()
	if err != nil {
		t.Fatal(err)
	}
	if got := idsOf(models); !reflect.DeepEqual(got, []string{"a.gguf", "b.gguf", "c.gguf"}) {
		t.Fatalf("order = %q", got)
	}
}
