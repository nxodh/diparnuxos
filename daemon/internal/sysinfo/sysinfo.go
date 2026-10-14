// Package sysinfo 는 /proc/meminfo 와 파일 시스템의 여유 공간을 읽는다.
//
// 경로는 모두 인자로 받아서 테스트가 가짜 파일로 바꿀 수 있다. 전역 상태는 두지 않는다.
package sysinfo

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"strconv"
	"strings"
	"syscall"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// 파일과 값의 상한이다.
const (
	// maxMemInfoBytes 는 meminfo 를 읽는 최대 바이트 수다. 실제 파일은 수 KB 이다.
	maxMemInfoBytes = 1 << 20
	// kibPerMiB 는 meminfo 의 kB(1024 바이트 단위)를 MiB 로 바꾸는 나눗수다.
	kibPerMiB = 1024
)

// ReadMemInfo 는 meminfo 형식 파일 path 에서 MemTotal 과 MemAvailable 을 읽어 MiB 로 돌려준다.
// /proc/meminfo 의 값은 kB 단위이며 1024 바이트 단위로 읽는다. 두 키 중 하나라도 없으면 오류다.
func ReadMemInfo(path string) (platform.MemInfo, error) {
	f, err := os.Open(path)
	if err != nil {
		return platform.MemInfo{}, fmt.Errorf("sysinfo: open meminfo: %w", err)
	}
	defer f.Close()

	var total, avail uint64
	var haveTotal, haveAvail bool
	sc := bufio.NewScanner(io.LimitReader(f, maxMemInfoBytes))
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		var dst *uint64
		var seen *bool
		switch fields[0] {
		case "MemTotal:":
			dst, seen = &total, &haveTotal
		case "MemAvailable:":
			dst, seen = &avail, &haveAvail
		default:
			continue
		}
		if len(fields) == 3 && fields[2] != "kB" {
			return platform.MemInfo{}, fmt.Errorf("sysinfo: %s: unexpected unit %q", fields[0], fields[2])
		}
		v, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return platform.MemInfo{}, fmt.Errorf("sysinfo: %s: %w", fields[0], err)
		}
		*dst, *seen = v, true
	}
	if err := sc.Err(); err != nil {
		return platform.MemInfo{}, fmt.Errorf("sysinfo: read meminfo: %w", err)
	}
	if !haveTotal || !haveAvail {
		return platform.MemInfo{}, errors.New("sysinfo: meminfo has no MemTotal or MemAvailable")
	}
	if total/kibPerMiB > math.MaxInt64 || avail/kibPerMiB > math.MaxInt64 {
		return platform.MemInfo{}, errors.New("sysinfo: meminfo value is too large")
	}
	return platform.MemInfo{
		TotalMiB:     int64(total / kibPerMiB),
		AvailableMiB: int64(avail / kibPerMiB),
	}, nil
}

// DiskFree 는 dir 가 있는 파일 시스템에서 일반 사용자가 쓸 수 있는 여유 바이트를 돌려준다.
// root 만 쓸 수 있는 예약 공간은 빼므로 f_bavail 을 쓴다. 결과는 hf.DownloadRequest.FreeBytes 에 그대로 넣을 수 있다.
func DiskFree(dir string) (uint64, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(dir, &st); err != nil {
		return 0, fmt.Errorf("sysinfo: statfs %s: %w", dir, err)
	}
	if st.Bsize <= 0 {
		return 0, fmt.Errorf("sysinfo: statfs %s: invalid block size %d", dir, st.Bsize)
	}
	bsize := uint64(st.Bsize)
	avail := uint64(st.Bavail)
	if avail > math.MaxUint64/bsize {
		return 0, fmt.Errorf("sysinfo: statfs %s: free space overflows", dir)
	}
	return avail * bsize, nil
}
