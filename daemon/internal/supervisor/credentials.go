package supervisor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

const keySnapshotPrefix = "api-keys.start-"

// validateCredentialSnapshot 은 Store 스냅샷이 실제 키만 가진 정규 바이트인지 확인한다.
func validateCredentialSnapshot(data []byte) error {
	if len(data) == 0 {
		return nil
	}
	if data[len(data)-1] != '\n' || bytes.Contains(data, []byte{'\r'}) {
		return errors.New("credential snapshot is not newline-terminated canonical data")
	}
	lines := bytes.Split(data[:len(data)-1], []byte{'\n'})
	seen := make(map[[sha256.Size]byte]struct{}, len(lines))
	for _, line := range lines {
		if len(line) != 47 || !bytes.HasPrefix(line, []byte("dpx_")) {
			return errors.New("credential snapshot contains a malformed key")
		}
		raw, err := base64.RawURLEncoding.DecodeString(string(line[4:]))
		if err != nil || len(raw) != 32 || base64.RawURLEncoding.EncodeToString(raw) != string(line[4:]) {
			return errors.New("credential snapshot contains a malformed key")
		}
		sum := sha256.Sum256(line)
		if _, ok := seen[sum]; ok {
			return errors.New("credential snapshot contains a duplicate key")
		}
		seen[sum] = struct{}{}
	}
	return nil
}

// writeKeySnapshot 은 API 키 복사본을 llama-server 전용의 0640 파일로 기록한다.
func (s *Supervisor) writeKeySnapshot(data []byte) (string, error) {
	if err := validateCredentialSnapshot(data); err != nil {
		return "", err
	}
	if len(data) == 0 {
		return "", nil
	}
	dir := s.opts.KeySnapshotDir
	if dir == "" {
		return "", errors.New("API key snapshot directory is not configured")
	}
	if err := s.verifySnapshotDirectory(dir); err != nil {
		return "", err
	}
	tmp, err := os.CreateTemp(dir, "."+keySnapshotPrefix+"*.tmp")
	if err != nil {
		return "", fmt.Errorf("create API key snapshot: %w", err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(0o640); err != nil {
		return "", fmt.Errorf("set API key snapshot mode: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		return "", fmt.Errorf("write API key snapshot: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		return "", fmt.Errorf("sync API key snapshot: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return "", fmt.Errorf("close API key snapshot: %w", err)
	}
	base := strings.TrimPrefix(filepath.Base(tmpName), ".")
	base = strings.TrimSuffix(base, ".tmp")
	path := filepath.Join(dir, base+".key")
	if err := os.Rename(tmpName, path); err != nil {
		return "", fmt.Errorf("publish API key snapshot: %w", err)
	}
	if s.opts.ChownKeyFile != nil {
		if err := s.opts.ChownKeyFile(path); err != nil {
			_ = os.Remove(path)
			return "", fmt.Errorf("set API key snapshot group: %w", err)
		}
	}
	verified, err := s.readRuntimeCredentialFile(path)
	if err != nil || !bytes.Equal(verified, data) {
		_ = os.Remove(path)
		if err != nil {
			return "", fmt.Errorf("verify API key snapshot: %w", err)
		}
		return "", errors.New("API key snapshot changed after writing")
	}
	d, err := os.Open(dir)
	if err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("open API key snapshot directory: %w", err)
	}
	if err := d.Sync(); err != nil {
		_ = d.Close()
		_ = os.Remove(path)
		return "", fmt.Errorf("sync API key snapshot directory: %w", err)
	}
	if err := d.Close(); err != nil {
		_ = os.Remove(path)
		return "", fmt.Errorf("close API key snapshot directory: %w", err)
	}
	committed = true
	return path, nil
}

// readCredentialFile 은 서비스 사용자가 읽을 수 있는 키 파일을 검증하고 복사한다.
func readCredentialFile(path string) ([]byte, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o640 {
		return nil, errors.New("API key file must be a regular 0640 file")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, errors.New("API key file is not owned by the daemon user")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if err := validateCredentialSnapshot(data); err != nil {
		return nil, errors.New("API key file is malformed")
	}
	return append([]byte(nil), data...), nil
}

func (s *Supervisor) readRuntimeCredentialFile(path string) ([]byte, error) {
	data, err := readCredentialFile(path)
	if err != nil {
		return nil, err
	}
	if err := s.verifySnapshotDirectory(filepath.Dir(path)); err != nil {
		return nil, err
	}
	if err := s.verifySnapshotGroup(path); err != nil {
		return nil, err
	}
	return data, nil
}

func (s *Supervisor) verifySnapshotDirectory(dir string) error {
	info, err := os.Lstat(dir)
	if err != nil {
		return fmt.Errorf("stat API key snapshot directory: %w", err)
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("API key snapshot path is not a directory")
	}
	if info.Mode().Perm()&0o010 == 0 || info.Mode().Perm()&0o022 != 0 {
		return errors.New("API key snapshot directory is not safely group-accessible")
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("API key snapshot directory is not owned by the daemon user")
	}
	if err := s.verifySnapshotGroup(dir); err != nil {
		return err
	}
	for parent := filepath.Dir(dir); parent != dir; parent = filepath.Dir(parent) {
		parentInfo, err := os.Stat(parent)
		if err != nil {
			return fmt.Errorf("stat API key snapshot parent directory: %w", err)
		}
		if !parentInfo.IsDir() || !directoryTraversable(parentInfo, s.opts.KeySnapshotGID) {
			return errors.New("API key snapshot parent directory is not traversable by the llama-server group")
		}
		if filepath.Dir(parent) == parent {
			break
		}
	}
	return nil
}

func directoryTraversable(info os.FileInfo, gid uint32) bool {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return false
	}
	perm := info.Mode().Perm()
	if gid != 0 && stat.Gid == gid && perm&0o010 != 0 {
		return true
	}
	return perm&0o001 != 0
}

func (s *Supervisor) verifySnapshotGroup(path string) error {
	if s.opts.KeySnapshotGID == 0 {
		return nil
	}
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Gid != s.opts.KeySnapshotGID {
		return errors.New("API key path is not owned by the llama-server group")
	}
	return nil
}

// cleanupKeySnapshots 는 이 프로세스 소유의 스냅샷만 지운다.
// 호출자는 해당 llama 유닛이 완전히 중지됐음을 확인해야 한다.
func (s *Supervisor) cleanupKeySnapshots(keep string) error {
	dir := s.opts.KeySnapshotDir
	if dir == "" {
		return nil
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var errs []error
	for _, entry := range entries {
		name := entry.Name()
		if !strings.HasPrefix(name, keySnapshotPrefix) || !strings.HasSuffix(name, ".key") {
			continue
		}
		path := filepath.Join(dir, name)
		if path == keep {
			continue
		}
		owned, err := ownedRegularSnapshot(path)
		if err != nil {
			errs = append(errs, fmt.Errorf("inspect inactive API key snapshot: %w", err))
			continue
		}
		if !owned {
			continue
		}
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, fmt.Errorf("remove inactive API key snapshot: %w", err))
		}
	}
	return errors.Join(errs...)
}

func (s *Supervisor) discardKeySnapshot(path string) {
	if path == "" || s.opts.KeySnapshotDir == "" || filepath.Dir(path) != s.opts.KeySnapshotDir {
		return
	}
	if owned, err := ownedRegularSnapshot(path); err == nil && owned {
		_ = os.Remove(path)
	}
}

func (s *Supervisor) discardKeySnapshotIfInactive(ctx context.Context, path string) {
	if path == "" {
		return
	}
	state, stateErr := s.unitState(ctx)
	pid, pidErr := s.mainPID(ctx)
	if stateErr == nil && pidErr == nil && pid == 0 && (state == "inactive" || state == "failed") {
		s.discardKeySnapshot(path)
	}
}

func ownedRegularSnapshot(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, nil
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return false, nil
	}
	return true, nil
}
