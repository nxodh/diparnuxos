package main

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"syscall"
)

// sdListenFDsStart는 systemd 가 소켓을 넘기는 첫 파일 디스크립터 번호다. sd_listen_fds(3)의 SD_LISTEN_FDS_START 는 3 이다.
const sdListenFDsStart = 3

// socketListener는 리스너를 만든다. systemd 소켓 활성화가 있으면 그 소켓을 쓰고, 없으면 경로에 직접 만든다.
// created 는 데몬이 소켓 파일을 만들었는지 나타낸다.
func socketListener(path, group string) (ln net.Listener, created bool, err error) {
	activated, ok, err := systemdListener()
	if err != nil {
		return nil, false, err
	}
	if ok {
		return activated, false, nil
	}

	fi, err := os.Lstat(path)
	switch {
	case err == nil:
		if fi.Mode()&fs.ModeSocket == 0 {
			return nil, false, fmt.Errorf("%s exists and is not a socket; refusing to remove it", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, false, fmt.Errorf("remove stale socket %s: %w", path, err)
		}
	case !errors.Is(err, fs.ErrNotExist):
		return nil, false, fmt.Errorf("stat %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, false, fmt.Errorf("create %s: %w", filepath.Dir(path), err)
	}

	// umask 를 0117 로 바꿔 소켓을 처음부터 0660 으로 만든다. 시작 중이라 다른 고루틴은 없다.
	old := syscall.Umask(0o117)
	l, err := net.Listen("unix", path)
	syscall.Umask(old)
	if err != nil {
		return nil, false, fmt.Errorf("listen on %s: %w", path, err)
	}
	gid, err := groupID(group)
	if err != nil {
		_ = l.Close()
		return nil, false, err
	}
	if err := os.Chown(path, -1, gid); err != nil {
		_ = l.Close()
		return nil, false, fmt.Errorf("set group of %s to %s: %w", path, group, err)
	}
	if err := os.Chmod(path, 0o660); err != nil {
		_ = l.Close()
		return nil, false, fmt.Errorf("chmod %s: %w", path, err)
	}
	return l, true, nil
}

// systemdListener는 sd_listen_fds(3) 규약으로 systemd 가 넘긴 소켓을 찾는다.
// LISTEN_PID 가 이 프로세스의 PID 이고 LISTEN_FDS 가 1 이면 fd 3 을 쓴다. 두 변수는 읽은 뒤 지운다.
// 이 프로세스를 위한 것이 아니면(PID 불일치, LISTEN_FDS 가 0이나 없음) 소켓이 없다고 보고 ok=false 를 돌려준다.
func systemdListener() (net.Listener, bool, error) {
	pidVar := os.Getenv("LISTEN_PID")
	fdsVar := os.Getenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_PID")
	_ = os.Unsetenv("LISTEN_FDS")
	_ = os.Unsetenv("LISTEN_FDNAMES")

	if pidVar == "" {
		return nil, false, nil
	}
	pid, err := strconv.Atoi(pidVar)
	if err != nil || pid != os.Getpid() {
		return nil, false, nil
	}
	fds, err := strconv.Atoi(fdsVar)
	if err != nil || fds == 0 {
		return nil, false, nil
	}
	if fds != 1 {
		return nil, false, fmt.Errorf("systemd passed %d sockets; lightlixd expects exactly one", fds)
	}
	f := os.NewFile(uintptr(sdListenFDsStart), "systemd-socket")
	l, err := net.FileListener(f)
	_ = f.Close()
	if err != nil {
		return nil, false, fmt.Errorf("use the systemd socket: %w", err)
	}
	return l, true, nil
}

// groupID는 그룹 이름의 GID 를 찾는다.
func groupID(name string) (int, error) {
	g, err := user.LookupGroup(name)
	if err != nil {
		return 0, fmt.Errorf("look up group %s: %w", name, err)
	}
	gid, err := strconv.Atoi(g.Gid)
	if err != nil {
		return 0, fmt.Errorf("group %s has a non-numeric GID %q", name, g.Gid)
	}
	return gid, nil
}
