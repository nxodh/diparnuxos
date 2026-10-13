// Package sysstat 는 /proc 과 /sys 에서 CPU, 메모리, 디스크, 네트워크 처리량, 온도를 읽는다.
//
// 상주 수집기를 두지 않는다. Sample 을 부를 때마다 짧은 간격(기본 250 ms)을 두고 두 번 읽어 그 사이의 변화로 사용률과
// 처리량을 계산한다. 경로는 구조체 필드로 바꿀 수 있어 가짜 /proc 로 시험한다.
package sysstat

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// CPU 는 CPU 사용 상태다.
type CPU struct {
	Cores       int     `json:"cores"`
	UsedPercent float64 `json:"used_percent"`
	Load1       float64 `json:"load1"`
	Load5       float64 `json:"load5"`
	Load15      float64 `json:"load15"`
}

// Memory 는 메모리 상태다(MiB).
type Memory struct {
	TotalMiB     int64 `json:"total_mib"`
	AvailableMiB int64 `json:"available_mib"`
	UsedMiB      int64 `json:"used_mib"`
	SwapTotalMiB int64 `json:"swap_total_mib"`
	SwapUsedMiB  int64 `json:"swap_used_mib"`
}

// Disk 는 마운트된 파일 시스템 하나다.
type Disk struct {
	Mount     string `json:"mount"`
	Device    string `json:"device"`
	FSType    string `json:"fs_type"`
	TotalMiB  int64  `json:"total_mib"`
	UsedMiB   int64  `json:"used_mib"`
	FreeMiB   int64  `json:"free_mib"`
	UsedRatio int    `json:"used_percent"`
}

// NetIface 는 인터페이스의 처리량이다(바이트/초).
type NetIface struct {
	Name      string  `json:"name"`
	RxBytesPS float64 `json:"rx_bytes_per_second"`
	TxBytesPS float64 `json:"tx_bytes_per_second"`
	RxTotal   uint64  `json:"rx_bytes_total"`
	TxTotal   uint64  `json:"tx_bytes_total"`
}

// Temp 는 온도 센서 하나다.
type Temp struct {
	Name    string  `json:"name"`
	Celsius float64 `json:"celsius"`
}

// Process 는 메모리를 많이 쓰는 프로세스다.
type Process struct {
	PID    int    `json:"pid"`
	Name   string `json:"name"`
	RSSMiB int64  `json:"rss_mib"`
}

// Sample 은 한 번의 측정이다.
type Sample struct {
	UptimeSeconds int64      `json:"uptime_seconds"`
	CPU           CPU        `json:"cpu"`
	Memory        Memory     `json:"memory"`
	Disks         []Disk     `json:"disks"`
	Network       []NetIface `json:"network"`
	Temperatures  []Temp     `json:"temperatures"`
	Top           []Process  `json:"top_processes"`
}

// Reader 는 읽을 경로와 표본 간격이다. 0 값이면 실제 시스템을 쓴다.
type Reader struct {
	Proc     string
	Sys      string
	Interval time.Duration
	// Statfs 는 시험이 디스크 용량을 대신할 때 쓴다.
	Statfs func(path string) (total, free uint64, err error)
}

func (r *Reader) proc() string {
	if r.Proc != "" {
		return r.Proc
	}
	return "/proc"
}

func (r *Reader) sys() string {
	if r.Sys != "" {
		return r.Sys
	}
	return "/sys"
}

func (r *Reader) interval() time.Duration {
	if r.Interval > 0 {
		return r.Interval
	}
	return 250 * time.Millisecond
}

type cpuTimes struct{ idle, total uint64 }

func (r *Reader) cpuTimes() (cpuTimes, int, error) {
	f, err := os.Open(filepath.Join(r.proc(), "stat"))
	if err != nil {
		return cpuTimes{}, 0, err
	}
	defer f.Close()
	var t cpuTimes
	cores := 0
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) == 0 {
			continue
		}
		switch {
		case fields[0] == "cpu":
			var total, idle uint64
			for i, v := range fields[1:] {
				n, _ := strconv.ParseUint(v, 10, 64)
				// user nice system idle iowait irq softirq steal guest guest_nice. guest 는 user 에 이미 들어 있다.
				if i >= 8 {
					break
				}
				total += n
				if i == 3 || i == 4 {
					idle += n
				}
			}
			t = cpuTimes{idle: idle, total: total}
		case strings.HasPrefix(fields[0], "cpu"):
			cores++
		}
	}
	return t, cores, sc.Err()
}

type netCounters map[string][2]uint64

func (r *Reader) netCounters() netCounters {
	out := netCounters{}
	f, err := os.Open(filepath.Join(r.proc(), "net", "dev"))
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		name, rest, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		name = strings.TrimSpace(name)
		fields := strings.Fields(rest)
		if len(fields) < 9 || name == "lo" {
			continue
		}
		rx, _ := strconv.ParseUint(fields[0], 10, 64)
		tx, _ := strconv.ParseUint(fields[8], 10, 64)
		out[name] = [2]uint64{rx, tx}
	}
	return out
}

// Read 는 한 번 측정한다. ctx 가 끝나면 두 번째 읽기 없이 첫 값으로 채운다.
func (r *Reader) Read(ctx context.Context) (Sample, error) {
	var s Sample
	c1, cores, err := r.cpuTimes()
	if err != nil {
		return s, fmt.Errorf("sysstat: read /proc/stat: %w", err)
	}
	n1 := r.netCounters()
	start := time.Now()
	select {
	case <-ctx.Done():
	case <-time.After(r.interval()):
	}
	c2, _, _ := r.cpuTimes()
	n2 := r.netCounters()
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = r.interval().Seconds()
	}

	s.CPU.Cores = cores
	if dt := c2.total - c1.total; c2.total > c1.total && dt > 0 {
		s.CPU.UsedPercent = round1(100 * float64(dt-(c2.idle-c1.idle)) / float64(dt))
	}
	if data, err := os.ReadFile(filepath.Join(r.proc(), "loadavg")); err == nil {
		f := strings.Fields(string(data))
		if len(f) >= 3 {
			s.CPU.Load1, _ = strconv.ParseFloat(f[0], 64)
			s.CPU.Load5, _ = strconv.ParseFloat(f[1], 64)
			s.CPU.Load15, _ = strconv.ParseFloat(f[2], 64)
		}
	}
	if data, err := os.ReadFile(filepath.Join(r.proc(), "uptime")); err == nil {
		if f := strings.Fields(string(data)); len(f) > 0 {
			up, _ := strconv.ParseFloat(f[0], 64)
			s.UptimeSeconds = int64(up)
		}
	}
	s.Memory = r.memory()
	s.Disks = r.disks()
	for name, b := range n2 {
		it := NetIface{Name: name, RxTotal: b[0], TxTotal: b[1]}
		if a, ok := n1[name]; ok && b[0] >= a[0] && b[1] >= a[1] {
			it.RxBytesPS = round1(float64(b[0]-a[0]) / elapsed)
			it.TxBytesPS = round1(float64(b[1]-a[1]) / elapsed)
		}
		s.Network = append(s.Network, it)
	}
	sort.Slice(s.Network, func(i, j int) bool { return s.Network[i].Name < s.Network[j].Name })
	s.Temperatures = r.temperatures()
	s.Top = r.topProcesses(6)
	if s.Disks == nil {
		s.Disks = []Disk{}
	}
	if s.Network == nil {
		s.Network = []NetIface{}
	}
	if s.Temperatures == nil {
		s.Temperatures = []Temp{}
	}
	if s.Top == nil {
		s.Top = []Process{}
	}
	return s, nil
}

func round1(v float64) float64 { return float64(int64(v*10+0.5)) / 10 }

func (r *Reader) memory() Memory {
	var m Memory
	data, err := os.ReadFile(filepath.Join(r.proc(), "meminfo"))
	if err != nil {
		return m
	}
	vals := map[string]int64{}
	for _, line := range strings.Split(string(data), "\n") {
		k, v, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		f := strings.Fields(v)
		if len(f) == 0 {
			continue
		}
		n, _ := strconv.ParseInt(f[0], 10, 64)
		vals[k] = n / 1024 // kB → MiB
	}
	m.TotalMiB = vals["MemTotal"]
	m.AvailableMiB = vals["MemAvailable"]
	m.UsedMiB = m.TotalMiB - m.AvailableMiB
	m.SwapTotalMiB = vals["SwapTotal"]
	m.SwapUsedMiB = vals["SwapTotal"] - vals["SwapFree"]
	return m
}

// realFS 는 용량을 보여 줄 파일 시스템 종류다.
var realFS = map[string]bool{"ext4": true, "ext3": true, "xfs": true, "btrfs": true, "vfat": true, "f2fs": true, "exfat": true, "ntfs": true, "ntfs3": true}

func (r *Reader) disks() []Disk {
	f, err := os.Open(filepath.Join(r.proc(), "mounts"))
	if err != nil {
		return nil
	}
	defer f.Close()
	statfs := r.Statfs
	if statfs == nil {
		statfs = func(path string) (uint64, uint64, error) {
			var st syscall.Statfs_t
			if err := syscall.Statfs(path, &st); err != nil {
				return 0, 0, err
			}
			return st.Blocks * uint64(st.Bsize), st.Bavail * uint64(st.Bsize), nil
		}
	}
	var out []Disk
	seen := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 || !realFS[f[2]] || seen[f[0]+f[1]] {
			continue
		}
		seen[f[0]+f[1]] = true
		total, free, err := statfs(f[1])
		if err != nil || total == 0 {
			continue
		}
		d := Disk{Mount: f[1], Device: f[0], FSType: f[2], TotalMiB: int64(total >> 20), FreeMiB: int64(free >> 20)}
		d.UsedMiB = d.TotalMiB - d.FreeMiB
		d.UsedRatio = int(100 * d.UsedMiB / max(d.TotalMiB, 1))
		out = append(out, d)
	}
	return out
}

func (r *Reader) temperatures() []Temp {
	var out []Temp
	zones, _ := filepath.Glob(filepath.Join(r.sys(), "class", "thermal", "thermal_zone*"))
	sort.Strings(zones)
	for _, z := range zones {
		raw, err := os.ReadFile(filepath.Join(z, "temp"))
		if err != nil {
			continue
		}
		milli, err := strconv.ParseInt(strings.TrimSpace(string(raw)), 10, 64)
		if err != nil || milli <= -50000 || milli > 150000 {
			continue
		}
		name := filepath.Base(z)
		if t, err := os.ReadFile(filepath.Join(z, "type")); err == nil {
			name = strings.TrimSpace(string(t))
		}
		out = append(out, Temp{Name: name, Celsius: float64(milli) / 1000})
	}
	return out
}

func (r *Reader) topProcesses(n int) []Process {
	dirs, err := filepath.Glob(filepath.Join(r.proc(), "[0-9]*", "statm"))
	if err != nil {
		return nil
	}
	pageKiB := int64(os.Getpagesize() / 1024)
	var all []Process
	for _, p := range dirs {
		pidDir := filepath.Dir(p)
		pid, err := strconv.Atoi(filepath.Base(pidDir))
		if err != nil {
			continue
		}
		raw, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		f := strings.Fields(string(raw))
		if len(f) < 2 {
			continue
		}
		pages, _ := strconv.ParseInt(f[1], 10, 64)
		name := ""
		if c, err := os.ReadFile(filepath.Join(pidDir, "comm")); err == nil {
			name = strings.TrimSpace(string(c))
		}
		if pages > 0 {
			all = append(all, Process{PID: pid, Name: name, RSSMiB: pages * pageKiB / 1024})
		}
	}
	sort.Slice(all, func(i, j int) bool { return all[i].RSSMiB > all[j].RSSMiB })
	if len(all) > n {
		all = all[:n]
	}
	return all
}
