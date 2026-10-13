// Package apikeys는 llama-server의 --api-key-file(LLAMA_ARG_API_KEY_FILE)이 읽는 키 파일과
// 키 목록(메타 파일)을 관리한다.
//
// 키 파일은 한 줄에 키 하나이고 "#"으로 시작하는 줄은 주석이다. 평문 키는 키 파일에만 있고,
// 메타 파일에는 ID, 라벨, 생성 시각, 앞 8자, 평문의 sha256만 있다. 평문은 Create가 한 번 돌려준다.
//
// 파일 모드는 키 파일 0640, 메타 파일 0600이다. 키 파일의 그룹(llama-server 실행 사용자)은 호출자가
// 설정해야 한다. 원자적 쓰기는 rename으로 하므로 쓸 때마다 새 파일이 되어 그룹 설정이 사라진다.
// 쓴 뒤에 다시 설정하는 것은 호출자의 책임이다.
//
// Store는 값으로 복사하지 않는다. 같은 파일 쌍을 쓰는 Store는 하나만 만들어 공유해야 한다.
// 뮤텍스는 Store마다 있으므로 서로 다른 Store가 같은 파일을 동시에 쓰면 직렬화되지 않는다.
package apikeys

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

const (
	keyPrefix        = "dpx_" // 키 접두사
	keyRandomBytes   = 32     // 키의 무작위 바이트 수, base64url 인코딩 후 43자
	idRandomBytes    = 16     // ID의 무작위 바이트 수, hex 인코딩 후 32자
	displayPrefixLen = 8      // 메타 파일에 저장하는 평문 앞 글자 수
	labelMaxLen      = 64     // 라벨 최대 길이(바이트, ASCII이므로 글자 수와 같다)
)

const (
	keyFileMode  fs.FileMode = 0o640 // llama-server 실행 사용자의 그룹이 읽는다
	metaFileMode fs.FileMode = 0o600
)

// ErrNotFound는 Delete에 준 ID가 메타 파일에 없을 때의 오류다.
var ErrNotFound = errors.New("no API key with that id")

// ErrInconsistent 는 평문 키 파일과 메타 파일의 키 목록이 다르다는 뜻이다.
// 호출자는 어느 한쪽만 믿지 말고 요청을 거부해야 한다.
var ErrInconsistent = errors.New("API key files are inconsistent")

// Key는 키 목록의 항목이다. 평문은 들어 있지 않다.
type Key struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	Created time.Time `json:"created"`
	Prefix  string    `json:"prefix"`
	SHA256  string    `json:"sha256"`
}

// Store는 키 파일과 메타 파일 한 쌍을 관리한다.
type Store struct {
	KeyFile  string
	MetaFile string
	Now      func() time.Time // nil이면 time.Now를 쓴다
	Rand     io.Reader        // nil이면 crypto/rand.Reader를 쓴다

	mu sync.Mutex
}

// metaDoc은 메타 파일의 JSON 모양이다.
type metaDoc struct {
	Keys []Key `json:"keys"`
}

// Create는 새 키를 만든다. 평문 키는 이 반환값으로 한 번만 보여 준다.
// 라벨은 1-64자의 출력 가능한 ASCII여야 한다.
func (s *Store) Create(label string) (Key, string, error) {
	if err := checkLabel(label); err != nil {
		return Key{}, "", fmt.Errorf("apikeys: create: %w", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return Key{}, "", fmt.Errorf("apikeys: create: %w", err)
	}
	if err := state.validate(); err != nil {
		return Key{}, "", fmt.Errorf("apikeys: create: %w", err)
	}
	meta := state.meta
	oldKeys, existed := state.keyFile, state.keyFileExists

	plain, err := s.newPlaintext()
	if err != nil {
		return Key{}, "", fmt.Errorf("apikeys: create: %w", err)
	}
	id, err := s.newID()
	if err != nil {
		return Key{}, "", fmt.Errorf("apikeys: create: %w", err)
	}
	sum := sha256Hex(plain)
	for _, k := range meta.Keys {
		if k.ID == id || k.SHA256 == sum {
			return Key{}, "", errors.New("apikeys: create: generated key collides with an existing key")
		}
	}

	key := Key{
		ID:      id,
		Label:   label,
		Created: s.now().UTC(),
		Prefix:  plain[:displayPrefixLen],
		SHA256:  sum,
	}

	newKeys := append(withTrailingNewline(oldKeys), []byte(plain+"\n")...)
	if err := writeFileAtomic(s.KeyFile, newKeys, keyFileMode); err != nil {
		return Key{}, "", fmt.Errorf("apikeys: create: write key file: %w", err)
	}

	meta.Keys = append(meta.Keys, key)
	if err := s.writeMeta(meta); err != nil {
		// 메타를 쓰지 못하면 키 파일을 이전 상태로 되돌린다. 그래야 목록에 없는 키가 살아 있지 않는다.
		werr := fmt.Errorf("apikeys: create: write meta file: %w", err)
		if rbErr := restoreKeyFile(s.KeyFile, oldKeys, existed); rbErr != nil {
			werr = errors.Join(werr, fmt.Errorf("apikeys: create: rollback of key file failed: %w", rbErr))
		}
		return Key{}, "", werr
	}
	return key, plain, nil
}

// List는 키 목록을 돌려준다. 평문은 포함하지 않는다.
func (s *Store) List() ([]Key, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return nil, fmt.Errorf("apikeys: list: %w", err)
	}
	if err := state.validate(); err != nil {
		return nil, fmt.Errorf("apikeys: list: %w", err)
	}
	out := make([]Key, len(state.meta.Keys))
	copy(out, state.meta.Keys)
	return out, nil
}

// Delete는 ID의 키를 지운다. 키 파일에서 먼저 지워 접근을 끊고, 그다음 메타 파일을 고친다.
// 메타 쓰기 실패 뒤에는 키를 되살리지 않으며, 불일치한 목록 읽기는 오류를 돌려준다.
func (s *Store) Delete(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return fmt.Errorf("apikeys: delete: %w", err)
	}
	idx := -1
	for i, k := range state.meta.Keys {
		if k.ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		return fmt.Errorf("apikeys: delete %q: %w", id, ErrNotFound)
	}
	hash := state.meta.Keys[idx].SHA256

	if err := state.validate(); err != nil {
		// 폐기는 성공했지만 메타 쓰기가 실패하면 오래된 메타 항목이 남는다.
		// 같은 삭제를 다시 요청했을 때 그 항목만 제거해 두 파일이 일치하는 경우에
		// 복구한다. 이 경로에서는 평문 파일을 쓰거나 폐기한 키를 되살리지 않는다.
		for _, plain := range state.keys {
			if sha256Hex(plain) == hash {
				return fmt.Errorf("apikeys: delete: %w", err)
			}
		}
		next := metaDoc{Keys: append([]Key(nil), state.meta.Keys...)}
		next.Keys = append(next.Keys[:idx], next.Keys[idx+1:]...)
		if pairErr := validatePair(next, state.keys); pairErr != nil {
			return fmt.Errorf("apikeys: delete: %w", err)
		}
		if writeErr := s.writeMeta(next); writeErr != nil {
			return fmt.Errorf("apikeys: delete: repair stale metadata: %w", writeErr)
		}
		return nil
	}
	newKeys, removed := dropKeyLines(state.keyFile, hash)
	if removed != 1 {
		return fmt.Errorf("apikeys: delete: %w", ErrInconsistent)
	}
	if err := writeFileAtomic(s.KeyFile, newKeys, keyFileMode); err != nil {
		return fmt.Errorf("apikeys: delete: write key file: %w", err)
	}

	state.meta.Keys = append(state.meta.Keys[:idx], state.meta.Keys[idx+1:]...)
	if err := s.writeMeta(state.meta); err != nil {
		// 폐기는 이미 완료됐다. 평문 키 파일을 되돌리지 않으며, 이후 읽기는
		// 안전한 삭제 재시도가 메타를 복구할 때까지 불일치 오류를 돌려준다.
		return fmt.Errorf("apikeys: delete: write meta file (key already removed from key file): %w", err)
	}
	return nil
}

// Count는 키 개수를 돌려준다.
func (s *Store) Count() (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return 0, fmt.Errorf("apikeys: count: %w", err)
	}
	if err := state.validate(); err != nil {
		return 0, fmt.Errorf("apikeys: count: %w", err)
	}
	return len(state.keys), nil
}

// Snapshot 은 실제 평문 키를 줄바꿈으로 구분한 복사본으로 돌려준다.
// Store 뮤텍스를 잡은 채 메타와 키 파일을 함께 읽어 목록이 정확히 일치하는지
// 확인한다. 주석과 빈 줄은 제외하고 반환된 각 키 뒤에는 줄바꿈을 붙인다.
func (s *Store) Snapshot() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	state, err := s.readState()
	if err != nil {
		return nil, fmt.Errorf("apikeys: snapshot: %w", err)
	}
	if err := state.validate(); err != nil {
		return nil, fmt.Errorf("apikeys: snapshot: %w", err)
	}
	out := make([]byte, len(state.snapshot))
	copy(out, state.snapshot)
	return out, nil
}

type storeState struct {
	meta          metaDoc
	keyFile       []byte
	keyFileExists bool
	keys          []string
	snapshot      []byte
	consistency   error
}

func (state storeState) validate() error { return state.consistency }

// readState 는 두 파일을 각각 읽고 형식을 검사한다. 파일 목록 불일치는 상태에
// 담아 반환해 Delete 가 폐기 후 남은 메타 유령 항목만 안전하게 복구할 수 있게 한다.
// 보안 판단에 쓰는 읽기 메서드는 validate 를 호출해 불일치 시 거부한다.
func (s *Store) readState() (storeState, error) {
	meta, err := s.readMeta()
	if err != nil {
		return storeState{}, err
	}
	keyFile, keyFileExists, err := s.readKeyFile()
	if err != nil {
		return storeState{}, err
	}
	keys, snapshot, err := parseKeyFile(keyFile)
	if err != nil {
		return storeState{}, err
	}
	return storeState{
		meta:          meta,
		keyFile:       keyFile,
		keyFileExists: keyFileExists,
		keys:          keys,
		snapshot:      snapshot,
		consistency:   validatePair(meta, keys),
	}, nil
}

func parseKeyFile(data []byte) ([]string, []byte, error) {
	keys := make([]string, 0)
	snapshot := make([]byte, 0, len(data))
	seen := make(map[string]struct{})
	for i, line := range strings.Split(string(data), "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if line != trimmed || !validPlaintext(line) {
			return nil, nil, fmt.Errorf("malformed API key file at line %d", i+1)
		}
		hash := sha256Hex(line)
		if _, exists := seen[hash]; exists {
			return nil, nil, fmt.Errorf("duplicate plaintext key in API key file at line %d", i+1)
		}
		seen[hash] = struct{}{}
		keys = append(keys, line)
		snapshot = append(snapshot, line...)
		snapshot = append(snapshot, '\n')
	}
	return keys, snapshot, nil
}

func validPlaintext(key string) bool {
	if !strings.HasPrefix(key, keyPrefix) || len(key) != len(keyPrefix)+base64.RawURLEncoding.EncodedLen(keyRandomBytes) {
		return false
	}
	raw, err := base64.RawURLEncoding.DecodeString(key[len(keyPrefix):])
	if err != nil || len(raw) != keyRandomBytes {
		return false
	}
	return base64.RawURLEncoding.EncodeToString(raw) == key[len(keyPrefix):]
}

func validatePair(meta metaDoc, keys []string) error {
	actual := make(map[string]struct{}, len(keys))
	for _, plain := range keys {
		hash := sha256Hex(plain)
		actual[hash] = struct{}{}
	}
	if len(meta.Keys) != len(actual) {
		return ErrInconsistent
	}
	for _, record := range meta.Keys {
		if record.Prefix == "" {
			return ErrInconsistent
		}
		if _, ok := actual[record.SHA256]; !ok {
			return ErrInconsistent
		}
		delete(actual, record.SHA256)
		for _, plain := range keys {
			if sha256Hex(plain) == record.SHA256 && record.Prefix != plain[:displayPrefixLen] {
				return ErrInconsistent
			}
		}
	}
	if len(actual) != 0 {
		return ErrInconsistent
	}
	return nil
}

// now는 시각을 얻는다. 주입된 함수가 없으면 time.Now를 쓴다.
func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// rng는 난수원을 돌려준다. 주입된 것이 없으면 crypto/rand를 쓴다.
func (s *Store) rng() io.Reader {
	if s.Rand != nil {
		return s.Rand
	}
	return rand.Reader
}

// newPlaintext는 "dpx_" 접두사와 base64url(패딩 없음) 32바이트로 키를 만든다. 전체 47자다.
func (s *Store) newPlaintext() (string, error) {
	raw := make([]byte, keyRandomBytes)
	if _, err := io.ReadFull(s.rng(), raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return keyPrefix + base64.RawURLEncoding.EncodeToString(raw), nil
}

// newID는 무작위 16바이트를 hex로 바꾼 ID다.
func (s *Store) newID() (string, error) {
	raw := make([]byte, idRandomBytes)
	if _, err := io.ReadFull(s.rng(), raw); err != nil {
		return "", fmt.Errorf("read random bytes: %w", err)
	}
	return hex.EncodeToString(raw), nil
}

// readMeta는 메타 파일을 읽는다. 파일이 없으면 빈 목록이다. 깨진 파일은 오류다.
func (s *Store) readMeta() (metaDoc, error) {
	data, err := os.ReadFile(s.MetaFile)
	if errors.Is(err, fs.ErrNotExist) {
		return metaDoc{}, nil
	}
	if err != nil {
		return metaDoc{}, fmt.Errorf("read meta file %s: %w", s.MetaFile, err)
	}
	var m metaDoc
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&m); err != nil {
		return metaDoc{}, fmt.Errorf("broken meta file %s: %w", s.MetaFile, err)
	}
	if m.Keys == nil {
		return metaDoc{}, fmt.Errorf("broken meta file %s: keys must be an array", s.MetaFile)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return metaDoc{}, fmt.Errorf("broken meta file %s: data after the JSON object", s.MetaFile)
	}
	ids := make(map[string]struct{}, len(m.Keys))
	hashes := make(map[string]struct{}, len(m.Keys))
	for i, k := range m.Keys {
		if err := checkKeyRecord(k); err != nil {
			return metaDoc{}, fmt.Errorf("broken meta file %s: key %d: %w", s.MetaFile, i, err)
		}
		if _, exists := ids[k.ID]; exists {
			return metaDoc{}, fmt.Errorf("broken meta file %s: duplicate key id", s.MetaFile)
		}
		if _, exists := hashes[k.SHA256]; exists {
			return metaDoc{}, fmt.Errorf("broken meta file %s: duplicate key hash", s.MetaFile)
		}
		ids[k.ID] = struct{}{}
		hashes[k.SHA256] = struct{}{}
	}
	return m, nil
}

// writeMeta는 메타 파일을 0600 모드로 원자적으로 쓴다.
func (s *Store) writeMeta(m metaDoc) error {
	if m.Keys == nil {
		m.Keys = []Key{}
	}
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return fmt.Errorf("encode meta: %w", err)
	}
	data = append(data, '\n')
	return writeFileAtomic(s.MetaFile, data, metaFileMode)
}

// readKeyFile은 키 파일 내용을 읽는다. 없으면 existed가 false다.
func (s *Store) readKeyFile() (data []byte, existed bool, err error) {
	data, err = os.ReadFile(s.KeyFile)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("read key file %s: %w", s.KeyFile, err)
	}
	return data, true, nil
}

// checkKeyRecord는 메타 파일 항목의 형식을 검사한다.
func checkKeyRecord(k Key) error {
	if len(k.ID) != idRandomBytes*2 || !isHex(k.ID) {
		return fmt.Errorf("id %q is not %d hex characters", k.ID, idRandomBytes*2)
	}
	if strings.ToLower(k.ID) != k.ID {
		return errors.New("id must use lowercase hex")
	}
	if len(k.SHA256) != sha256.Size*2 || !isHex(k.SHA256) {
		return errors.New("sha256 is not 64 hex characters")
	}
	if strings.ToLower(k.SHA256) != k.SHA256 {
		return errors.New("sha256 must use lowercase hex")
	}
	if !strings.HasPrefix(k.Prefix, keyPrefix) || len(k.Prefix) != displayPrefixLen {
		return fmt.Errorf("prefix %q is malformed", k.Prefix)
	}
	if k.Created.IsZero() {
		return errors.New("created time is missing")
	}
	return checkLabel(k.Label)
}

// checkLabel은 라벨이 1-64자의 출력 가능한 ASCII인지 본다.
func checkLabel(label string) error {
	if len(label) == 0 || len(label) > labelMaxLen {
		return fmt.Errorf("label must be 1-%d characters", labelMaxLen)
	}
	for i := 0; i < len(label); i++ {
		if c := label[i]; c < 0x20 || c > 0x7e {
			return fmt.Errorf("label contains a non-printable or non-ASCII byte at %d", i)
		}
	}
	return nil
}

// dropKeyLines는 해시가 같은 키 줄을 지운다. 주석과 빈 줄은 그대로 둔다.
func dropKeyLines(data []byte, hash string) ([]byte, int) {
	if len(data) == 0 {
		return nil, 0
	}
	parts := strings.Split(string(data), "\n")
	kept := make([]string, 0, len(parts))
	removed := 0
	for _, line := range parts {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			kept = append(kept, line)
			continue
		}
		if sha256Hex(trimmed) == hash {
			removed++
			continue
		}
		kept = append(kept, line)
	}
	if removed == 0 {
		return data, 0
	}
	return []byte(strings.Join(kept, "\n")), removed
}

// withTrailingNewline은 데이터 끝에 개행이 없으면 하나 붙인 복사본을 돌려준다.
func withTrailingNewline(b []byte) []byte {
	out := append([]byte{}, b...)
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(out, '\n')
	}
	return out
}

// restoreKeyFile은 키 파일을 이전 상태로 되돌린다. 이전에 없었으면 파일을 지운다.
func restoreKeyFile(file string, old []byte, existed bool) error {
	if !existed {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		return nil
	}
	return writeFileAtomic(file, old, keyFileMode)
}

// writeFileAtomic은 같은 디렉터리의 임시 파일에 쓰고 fsync 후 rename한다.
func writeFileAtomic(file string, data []byte, mode fs.FileMode) error {
	dir := filepath.Dir(file)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(file)+".tmp-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(mode); err != nil {
		return fmt.Errorf("chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, file); err != nil {
		return fmt.Errorf("rename %s to %s: %w", tmpName, file, err)
	}
	committed = true

	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("open dir %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("sync dir %s: %w", dir, err)
	}
	return nil
}

// sha256Hex는 문자열의 sha256을 소문자 hex로 돌려준다.
func sha256Hex(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// isHex는 문자열이 소문자 또는 대문자 hex 문자로만 이루어졌는지 본다.
func isHex(s string) bool {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}
