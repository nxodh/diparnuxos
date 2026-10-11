package sysinfo

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFile 은 dir 에 name 파일을 만들고 경로를 돌려준다.
func writeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestReadMemInfo 는 MemTotal 과 MemAvailable 을 kB 에서 MiB 로 바꾸는지 확인한다.
func TestReadMemInfo(t *testing.T) {
	p := writeFile(t, t.TempDir(), "meminfo", `MemTotal:       16777216 kB
MemFree:          102400 kB
MemAvailable:    8388608 kB
Buffers:          204800 kB
`)
	info, err := ReadMemInfo(p)
	if err != nil {
		t.Fatalf("ReadMemInfo: %v", err)
	}
	if info.TotalMiB != 16384 || info.AvailableMiB != 8192 {
		t.Fatalf("info = %+v, want 16384 and 8192 MiB", info)
	}
}

// TestReadMemInfoRoundsDown 는 MiB 로 바꿀 때 나머지를 버리는지 확인한다.
func TestReadMemInfoRoundsDown(t *testing.T) {
	p := writeFile(t, t.TempDir(), "meminfo", "MemTotal: 2047 kB\nMemAvailable: 1025 kB\n")
	info, err := ReadMemInfo(p)
	if err != nil {
		t.Fatal(err)
	}
	if info.TotalMiB != 1 || info.AvailableMiB != 1 {
		t.Fatalf("info = %+v, want 1 and 1", info)
	}
}

// TestReadMemInfoErrors 는 키가 없거나 단위가 다르거나 파일이 없을 때 오류를 돌려주는지 확인한다.
func TestReadMemInfoErrors(t *testing.T) {
	dir := t.TempDir()
	cases := map[string]string{
		"missing key":     "MemTotal: 100 kB\n",
		"bad unit":        "MemTotal: 100 MB\nMemAvailable: 50 kB\n",
		"not a number":    "MemTotal: abc kB\nMemAvailable: 50 kB\n",
		"empty":           "",
		"only other keys": "MemFree: 1 kB\nSwapTotal: 2 kB\n",
	}
	for name, content := range cases {
		t.Run(name, func(t *testing.T) {
			p := writeFile(t, dir, strings.ReplaceAll(name, " ", "_"), content)
			if _, err := ReadMemInfo(p); err == nil {
				t.Fatal("ReadMemInfo: want error")
			}
		})
	}
	if _, err := ReadMemInfo(filepath.Join(dir, "does-not-exist")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("missing file error = %v, want ErrNotExist", err)
	}
}

// TestDiskFree 는 임시 디렉터리의 여유 공간이 0 보다 크고, 없는 경로는 오류인지 확인한다.
func TestDiskFree(t *testing.T) {
	free, err := DiskFree(t.TempDir())
	if err != nil {
		t.Fatalf("DiskFree: %v", err)
	}
	if free == 0 {
		t.Fatal("DiskFree = 0 for a temporary directory")
	}
	if _, err := DiskFree(filepath.Join(t.TempDir(), "missing")); err == nil {
		t.Fatal("DiskFree of a missing directory: want error")
	}
}
