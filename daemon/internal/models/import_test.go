package models

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestImportStoresTheModelWithASidecar(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	data := ggufBytes("llama", 12)
	var reported int64
	res, err := s.Import(context.Background(), "mine.gguf", bytes.NewReader(data), int64(len(data)), func(done int64) { reported = done })
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	if res.SHA256 != hex.EncodeToString(sum[:]) || res.Size != int64(len(data)) || res.ID != "mine.gguf" {
		t.Fatalf("result = %+v", res)
	}
	if reported != int64(len(data)) {
		t.Fatalf("final progress = %d", reported)
	}
	list, err := s.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v %v", list, err)
	}
	m := list[0]
	if m.Incomplete || m.Repo != ImportRepo || m.Revision != ImportRevision || m.SHA256 != res.SHA256 || m.Architecture != "llama" {
		t.Fatalf("model = %+v", m)
	}
	if _, err := os.Lstat(filepath.Join(s.Dir, "mine.gguf.part")); err == nil {
		t.Fatal("the .part file was left behind")
	}
}

func TestImportRefusesBadInputsAndCleansUp(t *testing.T) {
	data := ggufBytes("llama", 12)
	cases := []struct {
		name string
		id   string
		body []byte
		size int64
		want error
	}{
		{"not gguf", "x.gguf", bytes.Repeat([]byte("hello world "), 20), 240, ErrNotGGUF},
		{"short body", "x.gguf", data[:len(data)-4], int64(len(data)), ErrSizeMismatch},
		{"long body", "x.gguf", append(append([]byte{}, data...), 0), int64(len(data)), ErrSizeMismatch},
		{"bad name", "../x.gguf", data, int64(len(data)), ErrInvalidID},
		{"wrong suffix", "x.bin", data, int64(len(data)), ErrInvalidID},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s := Store{Dir: t.TempDir()}
			_, err := s.Import(context.Background(), tc.id, bytes.NewReader(tc.body), tc.size, nil)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			entries, _ := os.ReadDir(s.Dir)
			if len(entries) != 0 {
				t.Fatalf("files left behind: %v", entries)
			}
		})
	}
}

func TestImportNeverReplacesAnExistingModel(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	write(t, s.Dir, "keep.gguf", ggufBytes("llama", 8))
	data := ggufBytes("llama", 12)
	if _, err := s.Import(context.Background(), "keep.gguf", bytes.NewReader(data), int64(len(data)), nil); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	got, _ := os.ReadFile(filepath.Join(s.Dir, "keep.gguf"))
	if !bytes.Equal(got, ggufBytes("llama", 8)) {
		t.Fatal("the existing model was changed")
	}
	// 받는 중인 파일이 있어도 건드리지 않는다.
	write(t, s.Dir, "busy.gguf.part", []byte("partial"))
	if _, err := s.Import(context.Background(), "busy.gguf", bytes.NewReader(data), int64(len(data)), nil); !errors.Is(err, ErrExists) {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(filepath.Join(s.Dir, "busy.gguf.part")); string(b) != "partial" {
		t.Fatal("the other download's part file was removed")
	}
}

func TestImportStopsWhenCanceled(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	data := ggufBytes("llama", 12)
	if _, err := s.Import(ctx, "x.gguf", bytes.NewReader(data), int64(len(data)), nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if entries, _ := os.ReadDir(s.Dir); len(entries) != 0 {
		t.Fatalf("files left behind: %v", entries)
	}
}
