//go:build !linux

package termio

import (
	"errors"
	"os"
)

// ReadSecret 은 리눅스가 아닌 곳에서는 지원하지 않는다. --password-stdin 을 쓴다.
func ReadSecret(_ *os.File) (string, error) {
	return "", errors.New("hidden password prompts are supported on Linux only; use --password-stdin")
}
