//go:build linux

package main

import (
	"errors"
	"io"
	"os"
	"syscall"
	"unsafe"
)

// readSecret 은 터미널의 에코와 신호 문자를 끄고 한 줄을 읽는다. 한 바이트씩 읽어 줄 뒤의 입력을 가져가지 않는다.
// Ctrl-C(0x03)는 취소로 본다.
func readSecret(f *os.File) (string, error) {
	fd := f.Fd()
	var old syscall.Termios
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCGETS, uintptr(unsafe.Pointer(&old))); e != 0 {
		return "", e
	}
	raw := old
	raw.Lflag &^= syscall.ECHO | syscall.ISIG
	if _, _, e := syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&raw))); e != 0 {
		return "", e
	}
	defer func() {
		_, _, _ = syscall.Syscall(syscall.SYS_IOCTL, fd, syscall.TCSETS, uintptr(unsafe.Pointer(&old)))
	}()

	var out []byte
	buf := make([]byte, 1)
	for {
		n, err := f.Read(buf)
		if n == 1 {
			switch buf[0] {
			case '\n', '\r':
				return string(out), nil
			case 0x03:
				return "", errors.New("canceled")
			case 0x7f, 0x08:
				if len(out) > 0 {
					out = out[:len(out)-1]
				}
			default:
				out = append(out, buf[0])
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return string(out), nil
			}
			return "", err
		}
	}
}
