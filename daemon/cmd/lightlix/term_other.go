//go:build !linux

package main

import (
	"errors"
	"os"
)

// readSecret 은 리눅스가 아닌 곳에서는 지원하지 않는다. --password-stdin 을 쓴다.
func readSecret(_ *os.File) (string, error) {
	return "", errors.New("hidden password prompts are supported on Linux only; use --password-stdin")
}
