// Package firstboot는 첫 부팅 마법사의 완료 상태를 파일 하나에 기록한다.
//
// 이 파일에는 비밀이 없다. 마법사가 고른 설정 자체는 설정 파일과 각 API가 따로 저장한다.
// 여기에는 마법사를 다시 보여줄지 판단하는 값만 둔다.
package firstboot

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// 마법사 단계 이름이다. 건너뛴 단계를 기록할 때 이 값만 허용한다.
var knownSteps = map[string]bool{
	"gpu":         true,
	"driver":      true,
	"secure_boot": true,
	"account":     true,
	"model":       true,
	"api":         true,
}

// State는 첫 부팅 마법사 상태다.
type State struct {
	// Done이 true이면 마법사를 다시 보여주지 않는다.
	Done bool `json:"done"`
	// CompletedAt은 완료한 시각(UTC, RFC 3339)이다. 완료 전에는 비어 있다.
	CompletedAt string `json:"completed_at"`
	// Skipped는 사용자가 건너뛴 단계 이름이다. 정렬되어 있고 중복이 없다.
	Skipped []string `json:"skipped"`
}

// Store는 상태 파일을 읽고 쓴다.
type Store struct {
	// Path는 상태 파일 경로다.
	Path string
	// Now는 현재 시각이다. nil이면 time.Now를 쓴다.
	Now func() time.Time
}

// Load는 상태를 읽는다. 파일이 없으면 완료되지 않은 상태를 돌려준다.
func (s Store) Load() (State, error) {
	data, err := os.ReadFile(s.Path)
	if errors.Is(err, fs.ErrNotExist) {
		return State{Skipped: []string{}}, nil
	}
	if err != nil {
		return State{}, fmt.Errorf("firstboot: read %s: %w", s.Path, err)
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return State{}, fmt.Errorf("firstboot: decode %s: %w", s.Path, err)
	}
	if st.Skipped == nil {
		st.Skipped = []string{}
	}
	return st, nil
}

// Complete는 마법사를 완료로 기록한다. skipped의 알 수 없는 단계 이름은 오류다.
func (s Store) Complete(skipped []string) (State, error) {
	seen := map[string]bool{}
	list := make([]string, 0, len(skipped))
	for _, name := range skipped {
		if !knownSteps[name] {
			return State{}, fmt.Errorf("firstboot: unknown step %q", name)
		}
		if !seen[name] {
			seen[name] = true
			list = append(list, name)
		}
	}
	sort.Strings(list)
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	st := State{Done: true, CompletedAt: now().UTC().Format("2006-01-02T15:04:05Z"), Skipped: list}
	return st, s.save(st)
}

// Reset은 완료 기록을 지워 마법사를 다시 보이게 한다.
func (s Store) Reset() error {
	err := os.Remove(s.Path)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("firstboot: remove %s: %w", s.Path, err)
	}
	return nil
}

// save는 같은 디렉터리의 임시 파일에 쓴 뒤 rename으로 바꾼다.
func (s Store) save(st State) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("firstboot: encode: %w", err)
	}
	data = append(data, '\n')
	dir := filepath.Dir(s.Path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(s.Path)+".tmp-*")
	if err != nil {
		return fmt.Errorf("firstboot: create temp file in %s: %w", dir, err)
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			_ = tmp.Close()
			_ = os.Remove(name)
		}
	}()
	if err := tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("firstboot: chmod %s: %w", name, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("firstboot: write %s: %w", name, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("firstboot: sync %s: %w", name, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("firstboot: close %s: %w", name, err)
	}
	if err := os.Rename(name, s.Path); err != nil {
		return fmt.Errorf("firstboot: rename %s to %s: %w", name, s.Path, err)
	}
	ok = true
	return nil
}
