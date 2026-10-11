// Package models manages the local directory of GGUF model files.
//
// Each model is a "<name>.gguf" file. Its download details (repository, revision, SHA-256, license) are kept in a sidecar
// file "<name>.gguf.json". The ID of the default model is kept in the hidden file ".default". Every file that this
// package writes is replaced atomically with a rename.
package models

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/nxodh/lightlixos/daemon/internal/gguf"
)

// File names and limits.
const (
	modelSuffix     = ".gguf"
	sidecarSuffix   = ".json"
	partSuffix      = ".part"
	defaultFile     = ".default"
	maxIDLen        = 255      // Longest file name accepted, in bytes.
	maxSidecarBytes = 64 << 10 // Largest sidecar file read.
	maxDefaultBytes = 512      // Largest default file read.
)

// ErrInvalidID is wrapped by errors that report an invalid model ID.
var ErrInvalidID = errors.New("invalid model ID")

// ErrOutsideDir is returned when a symbolic link points outside the model directory. Such a link is not followed.
var ErrOutsideDir = errors.New("symbolic link points outside the model directory")

// ErrNotRegular is returned when a path is not a regular file.
var ErrNotRegular = errors.New("not a regular file")

// Store is a model directory. Dir is the directory path, such as /var/lib/lightlix/models.
type Store struct {
	Dir string
}

// Sidecar holds the download details of one model. It is stored as JSON in "<model>.json".
type Sidecar struct {
	// Repo is the Hugging Face repository.
	Repo string `json:"repo"`
	// Revision is the revision that was downloaded, such as a commit SHA.
	Revision string `json:"revision"`
	// SHA256 is the verified SHA-256 of the model file, in hex.
	SHA256 string `json:"sha256"`
	// License is the license of the model.
	License string `json:"license"`
}

// Model describes one model file in the store.
//
// Fields from Repo to License come from the sidecar and are empty when the sidecar is missing or broken.
// The GGUF fields are zero when the file's metadata could not be read.
type Model struct {
	// ID is the file name, such as "gemma-3-1b-it-Q4_K_M.gguf".
	ID string
	// Path is the full path of the file.
	Path string
	// Size is the file size in bytes.
	Size int64
	// SHA256 is the digest recorded in the sidecar.
	SHA256 string
	// Repo is the repository recorded in the sidecar.
	Repo string
	// Revision is the revision recorded in the sidecar.
	Revision string
	// License is the license recorded in the sidecar.
	License string
	// Architecture is general.architecture from the GGUF header.
	Architecture string
	// Name is general.name from the GGUF header.
	Name string
	// FileTypeName is the quantization name from general.file_type.
	FileTypeName string
	// BlockCount is the layer count from the GGUF header.
	BlockCount uint64
	// ContextLength is the context length from the GGUF header.
	ContextLength uint64
	// EmbeddingLength is the embedding length from the GGUF header.
	EmbeddingLength uint64
	// Default reports whether this model is the default.
	Default bool
	// Incomplete reports that information about the model is missing. This happens when the GGUF metadata could not be read,
	// the sidecar is missing or broken, or a partial download with the same name exists.
	Incomplete bool
}

// List returns the models in the directory, sorted by file name. A missing directory gives an empty list.
// Files that are not valid model names, are not regular files, or link outside the directory are skipped.
// Partial downloads ("*.part") are not listed. A model whose metadata or sidecar cannot be read is still listed, with Incomplete set.
func (s Store) List() ([]Model, error) {
	entries, err := os.ReadDir(s.Dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("models: read directory: %w", err)
	}
	defaultID, err := s.Default()
	if err != nil {
		// A broken default file must not hide the models. No model is marked as the default.
		defaultID = ""
	}

	var out []Model
	for _, e := range entries {
		id := e.Name()
		if validID(id) != nil {
			continue
		}
		f, err := s.open(s.path(id))
		if err != nil {
			continue
		}
		info, err := f.Stat()
		if err != nil {
			f.Close()
			continue
		}
		meta, metaErr := gguf.ReadMeta(bufio.NewReader(f))
		f.Close()

		m := Model{ID: id, Path: s.path(id), Size: info.Size(), Default: id == defaultID}
		sc, sidecarOK := s.readSidecar(id)
		m.SHA256, m.Repo, m.Revision, m.License = sc.SHA256, sc.Repo, sc.Revision, sc.License
		m.Incomplete = !sidecarOK || metaErr != nil || partExists(s.path(id)+partSuffix)
		if metaErr == nil {
			m.Architecture = meta.Architecture
			m.Name = meta.Name
			m.FileTypeName = meta.FileTypeName()
			m.BlockCount = meta.BlockCount
			m.ContextLength = meta.ContextLength
			m.EmbeddingLength = meta.EmbeddingLength
		}
		out = append(out, m)
	}
	return out, nil
}

// WriteSidecar writes the sidecar of model id. The model file must exist.
func (s Store) WriteSidecar(id string, sc Sidecar) error {
	if err := validID(id); err != nil {
		return err
	}
	f, err := s.open(s.path(id))
	if err != nil {
		return fmt.Errorf("models: sidecar for %s: %w", id, err)
	}
	f.Close()

	data, err := json.Marshal(sc)
	if err != nil {
		return fmt.Errorf("models: encode sidecar: %w", err)
	}
	if len(data) > maxSidecarBytes {
		return fmt.Errorf("models: sidecar is larger than %d bytes", maxSidecarBytes)
	}
	if err := writeAtomic(s.Dir, s.path(id)+sidecarSuffix, data); err != nil {
		return fmt.Errorf("models: write sidecar for %s: %w", id, err)
	}
	return nil
}

// Remove 는 모델 id 와 그 사이드카, 내려받기 중인 .part 파일을 지운다. 없는 파일은 오류가 아니다.
// id 가 기본 모델이었으면 기본 설정도 지운다. 다른 모델을 기본으로 올리지는 않는다.
// 기본 설정은 모델 파일 삭제가 모두 성공했을 때만 지운다.
func (s Store) Remove(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	// 파일을 지우기 전에 기본 모델을 읽는다. 깨진 기본 파일은 id 를 가리키지 않으므로 그대로 둔다.
	defaultID, defErr := s.Default()
	wasDefault := defErr == nil && defaultID == id

	var first error
	for _, p := range []string{s.path(id), s.path(id) + sidecarSuffix, s.path(id) + partSuffix} {
		if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) && first == nil {
			first = fmt.Errorf("models: remove %s: %w", filepath.Base(p), err)
		}
	}
	if first != nil {
		return first
	}
	if wasDefault {
		if err := os.Remove(s.path(defaultFile)); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return fmt.Errorf("models: clear default: %w", err)
		}
	}
	return nil
}

// SetDefault records id as the default model. The model file must exist.
func (s Store) SetDefault(id string) error {
	if err := validID(id); err != nil {
		return err
	}
	f, err := s.open(s.path(id))
	if err != nil {
		return fmt.Errorf("models: default %s: %w", id, err)
	}
	f.Close()
	if err := writeAtomic(s.Dir, filepath.Join(s.Dir, defaultFile), []byte(id+"\n")); err != nil {
		return fmt.Errorf("models: write default: %w", err)
	}
	return nil
}

// Default returns the ID of the default model. It returns the empty string when no default is set.
// It returns an error when the default file exists but does not hold a valid ID.
func (s Store) Default() (string, error) {
	f, err := s.open(filepath.Join(s.Dir, defaultFile))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return "", nil
		}
		return "", fmt.Errorf("models: default file: %w", err)
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxDefaultBytes+1))
	if err != nil {
		return "", fmt.Errorf("models: read default file: %w", err)
	}
	if len(data) > maxDefaultBytes {
		return "", fmt.Errorf("models: default file is larger than %d bytes", maxDefaultBytes)
	}
	id := strings.TrimSuffix(string(data), "\n")
	if err := validID(id); err != nil {
		return "", fmt.Errorf("models: default file: %w", err)
	}
	return id, nil
}

// path returns the full path of a file in the store.
func (s Store) path(name string) string {
	return filepath.Join(s.Dir, name)
}

// open opens a regular file in the store. A symbolic link that points outside the store is refused.
func (s Store) open(p string) (*os.File, error) {
	li, err := os.Lstat(p)
	if err != nil {
		return nil, err
	}
	if li.Mode()&fs.ModeSymlink != 0 && !s.contains(p) {
		return nil, ErrOutsideDir
	}
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		f.Close()
		return nil, ErrNotRegular
	}
	return f, nil
}

// contains reports whether the real location of p is inside the store directory.
func (s Store) contains(p string) bool {
	dir, err := filepath.EvalSymlinks(s.Dir)
	if err != nil {
		return false
	}
	target, err := filepath.EvalSymlinks(p)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(dir, target)
	if err != nil || filepath.IsAbs(rel) {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// readSidecar reads and checks the sidecar of model id. A missing, oversized, or malformed sidecar gives ok false.
func (s Store) readSidecar(id string) (Sidecar, bool) {
	f, err := s.open(s.path(id) + sidecarSuffix)
	if err != nil {
		return Sidecar{}, false
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxSidecarBytes+1))
	if err != nil || len(data) > maxSidecarBytes {
		return Sidecar{}, false
	}
	var sc Sidecar
	if err := json.Unmarshal(data, &sc); err != nil {
		return Sidecar{}, false
	}
	if sc.SHA256 != "" && !isSHA256(sc.SHA256) {
		return Sidecar{}, false
	}
	return sc, true
}

// partExists reports whether a partial download exists at p.
func partExists(p string) bool {
	_, err := os.Lstat(p)
	return err == nil
}

// validID checks that id is a single file name that ends in .gguf, is not hidden, and has no separators or control characters.
func validID(id string) error {
	switch {
	case id == "":
		return fmt.Errorf("%w: empty", ErrInvalidID)
	case len(id) > maxIDLen:
		return fmt.Errorf("%w: longer than %d bytes", ErrInvalidID, maxIDLen)
	case !utf8.ValidString(id):
		return fmt.Errorf("%w: not valid UTF-8", ErrInvalidID)
	case strings.HasPrefix(id, "."):
		return fmt.Errorf("%w: hidden file names are not allowed", ErrInvalidID)
	case strings.ContainsAny(id, `/\`):
		return fmt.Errorf("%w: contains a path separator", ErrInvalidID)
	case hasControl(id):
		return fmt.Errorf("%w: contains a control character", ErrInvalidID)
	case len(id) <= len(modelSuffix) || !strings.HasSuffix(id, modelSuffix):
		return fmt.Errorf("%w: must end in %s", ErrInvalidID, modelSuffix)
	}
	return nil
}

// hasControl reports whether s contains an ASCII control character or DEL.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// isSHA256 reports whether s is 64 hexadecimal digits.
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F') {
			return false
		}
	}
	return true
}

// writeAtomic writes data to a temporary file in dir, syncs it, and renames it to target.
// Readers see either the old content or the new content, never a partial write.
func writeAtomic(dir, target string, data []byte) (err error) {
	tmp, err := os.CreateTemp(dir, ".tmp-*")
	if err != nil {
		return fmt.Errorf("create temporary file: %w", err)
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return fmt.Errorf("write temporary file: %w", err)
	}
	if err = tmp.Chmod(0o644); err != nil {
		return fmt.Errorf("chmod temporary file: %w", err)
	}
	if err = tmp.Sync(); err != nil {
		return fmt.Errorf("sync temporary file: %w", err)
	}
	if err = tmp.Close(); err != nil {
		return fmt.Errorf("close temporary file: %w", err)
	}
	if err = os.Rename(name, target); err != nil {
		return fmt.Errorf("rename temporary file: %w", err)
	}
	return nil
}
