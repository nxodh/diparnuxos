package firstboot

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func newStore(t *testing.T) Store {
	t.Helper()
	return Store{
		Path: filepath.Join(t.TempDir(), "firstboot.json"),
		Now:  func() time.Time { return time.Date(2026, 10, 12, 1, 2, 3, 0, time.UTC) },
	}
}

func TestLoadMissingFileIsNotDone(t *testing.T) {
	st, err := newStore(t).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if st.Done || st.CompletedAt != "" || st.Skipped == nil || len(st.Skipped) != 0 {
		t.Fatalf("state = %+v, want not done with empty non-nil Skipped", st)
	}
}

func TestCompleteWritesSortedUniqueSkipped(t *testing.T) {
	s := newStore(t)
	got, err := s.Complete([]string{"model", "gpu", "model"})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	want := State{Done: true, CompletedAt: "2026-10-12T01:02:03Z", Skipped: []string{"gpu", "model"}}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Complete = %+v, want %+v", got, want)
	}
	again, err := s.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(again, want) {
		t.Fatalf("Load = %+v, want %+v", again, want)
	}
	info, err := os.Stat(s.Path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o644 {
		t.Fatalf("mode = %v, want 0644", info.Mode().Perm())
	}
}

func TestCompleteRejectsUnknownStep(t *testing.T) {
	s := newStore(t)
	if _, err := s.Complete([]string{"bogus"}); err == nil {
		t.Fatal("Complete accepted an unknown step")
	}
	if _, err := os.Stat(s.Path); err == nil {
		t.Fatal("a rejected Complete must not write the file")
	}
}

func TestResetRemovesFileAndIsIdempotent(t *testing.T) {
	s := newStore(t)
	if _, err := s.Complete(nil); err != nil {
		t.Fatal(err)
	}
	if err := s.Reset(); err != nil {
		t.Fatalf("Reset: %v", err)
	}
	if err := s.Reset(); err != nil {
		t.Fatalf("second Reset: %v", err)
	}
	st, err := s.Load()
	if err != nil || st.Done {
		t.Fatalf("after Reset: %+v, %v", st, err)
	}
}

func TestLoadRejectsCorruptFile(t *testing.T) {
	s := newStore(t)
	if err := os.WriteFile(s.Path, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Load(); err == nil {
		t.Fatal("Load accepted corrupt JSON")
	}
}
