package sysstat

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func write(t *testing.T, root, rel, content string) {
	t.Helper()
	p := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func fakeProc(t *testing.T) (proc, sys string) {
	root := t.TempDir()
	proc, sys = filepath.Join(root, "proc"), filepath.Join(root, "sys")
	write(t, proc, "stat", "cpu  100 0 100 800 0 0 0 0 0 0\ncpu0 50 0 50 400 0 0 0 0 0 0\ncpu1 50 0 50 400 0 0 0 0 0 0\nintr 1\n")
	write(t, proc, "loadavg", "0.52 0.40 0.30 1/200 1234\n")
	write(t, proc, "uptime", "90061.50 100.00\n")
	write(t, proc, "meminfo", "MemTotal:       16384000 kB\nMemFree:         1000000 kB\nMemAvailable:    8192000 kB\nSwapTotal:       2048000 kB\nSwapFree:        2048000 kB\n")
	write(t, proc, "mounts", "/dev/sda2 / ext4 rw 0 0\ntmpfs /run tmpfs rw 0 0\n/dev/sda1 /boot/efi vfat rw 0 0\nproc /proc proc rw 0 0\n/dev/sda2 / ext4 rw 0 0\n")
	write(t, proc, "net/dev", "Inter-|   Receive |  Transmit\n face |bytes packets errs drop fifo frame compressed multicast|bytes packets\n    lo: 1000 10 0 0 0 0 0 0 1000 10 0 0 0 0 0 0\n enp3s0: 5000 10 0 0 0 0 0 0 2000 10 0 0 0 0 0 0\n")
	write(t, proc, "4242/statm", "100000 25600 100 10 0 5000 0\n")
	write(t, proc, "4242/comm", "llama-server\n")
	write(t, proc, "17/statm", "1000 512 100 10 0 500 0\n")
	write(t, proc, "17/comm", "bash\n")
	write(t, sys, "class/thermal/thermal_zone0/temp", "52500\n")
	write(t, sys, "class/thermal/thermal_zone0/type", "x86_pkg_temp\n")
	write(t, sys, "class/thermal/thermal_zone1/temp", "-274000\n")
	return
}

func TestReadParsesProcFiles(t *testing.T) {
	proc, sys := fakeProc(t)
	r := &Reader{
		Proc: proc, Sys: sys, Interval: time.Millisecond,
		Statfs: func(path string) (uint64, uint64, error) { return 100 << 30, 40 << 30, nil },
	}
	s, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU.Cores != 2 || s.CPU.Load1 != 0.52 || s.UptimeSeconds != 90061 {
		t.Fatalf("cpu = %+v uptime = %d", s.CPU, s.UptimeSeconds)
	}
	if s.Memory.TotalMiB != 16000 || s.Memory.AvailableMiB != 8000 || s.Memory.UsedMiB != 8000 || s.Memory.SwapTotalMiB != 2000 || s.Memory.SwapUsedMiB != 0 {
		t.Fatalf("memory = %+v", s.Memory)
	}
	if len(s.Disks) != 2 || s.Disks[0].Mount != "/" || s.Disks[0].UsedRatio != 60 || s.Disks[1].FSType != "vfat" {
		t.Fatalf("disks = %+v", s.Disks)
	}
	if len(s.Network) != 1 || s.Network[0].Name != "enp3s0" || s.Network[0].RxTotal != 5000 {
		t.Fatalf("network = %+v", s.Network)
	}
	if len(s.Temperatures) != 1 || s.Temperatures[0].Name != "x86_pkg_temp" || s.Temperatures[0].Celsius != 52.5 {
		t.Fatalf("temps = %+v", s.Temperatures)
	}
	if len(s.Top) != 2 || s.Top[0].Name != "llama-server" || s.Top[0].PID != 4242 {
		t.Fatalf("top = %+v", s.Top)
	}
}

func TestCPUUsageIsTheChangeBetweenTwoReads(t *testing.T) {
	proc, sys := fakeProc(t)
	r := &Reader{Proc: proc, Sys: sys, Interval: 60 * time.Millisecond}
	go func() {
		time.Sleep(20 * time.Millisecond)
		// 두 번째 읽기 전에 사용 시간 100 틱(그중 idle 100)이 늘어난다: 사용률 0 %.
		write(t, proc, "stat", "cpu  100 0 100 900 0 0 0 0 0 0\ncpu0 50 0 50 450 0 0 0 0 0 0\ncpu1 50 0 50 450 0 0 0 0 0 0\n")
	}()
	s, err := r.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if s.CPU.UsedPercent != 0 {
		t.Fatalf("used = %v", s.CPU.UsedPercent)
	}
	go func() {
		time.Sleep(20 * time.Millisecond)
		write(t, proc, "stat", "cpu  200 0 200 1000 0 0 0 0 0 0\ncpu0 100 0 100 500 0 0 0 0 0 0\ncpu1 100 0 100 500 0 0 0 0 0 0\n")
	}()
	s, _ = r.Read(context.Background())
	// 이전 읽기 이후 전체 300 틱 중 idle 100 틱이므로 사용률은 66.7 %다.
	if s.CPU.UsedPercent != 66.7 {
		t.Fatalf("used = %v", s.CPU.UsedPercent)
	}
}

func TestReadWithoutProcFails(t *testing.T) {
	r := &Reader{Proc: t.TempDir() + "/missing"}
	if _, err := r.Read(context.Background()); err == nil {
		t.Fatal("a missing /proc was accepted")
	}
}
