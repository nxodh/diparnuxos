package models

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"

	"github.com/nxodh/diparnuxos/daemon/internal/gguf"
)

// ErrExists 는 같은 이름의 모델이나 받는 중인 파일이 이미 있을 때 돌려준다.
var ErrExists = errors.New("a model with this name already exists")

// ErrNotGGUF 는 받은 파일이 GGUF 모델이 아닐 때 돌려준다.
var ErrNotGGUF = errors.New("the file is not a valid GGUF model")

// 직접 가져온 모델의 사이드카에 적는 값이다. 목록에서 출처를 알아볼 수 있게 한다.
const (
	ImportRepo     = "local-import"
	ImportRevision = "local"
)

// ImportResult 는 가져온 모델이다.
type ImportResult struct {
	ID     string
	Size   int64
	SHA256 string
}

// CheckID 는 모델 ID(파일 이름)로 쓸 수 있는지 본다. 쓸 수 없으면 ErrInvalidID 를 감싼 오류다.
func CheckID(id string) error { return validID(id) }

// Import 는 r 의 size 바이트를 모델 id 로 저장한다.
//
// 파일은 "<id>.part" 로 받아 SHA-256 을 계산하고, 길이와 GGUF 머리글을 확인한 뒤 이름을 바꾼다.
// 사이드카에는 출처(local-import)와 SHA-256 을 쓴다. 실패하면 받은 파일을 지운다.
// onProgress 는 쓴 바이트 수가 늘 때마다(큰 단위로) 불린다. 서버가 읽기 기한을 늘리는 데 쓴다.
// 같은 이름의 모델이나 .part 가 있으면 아무것도 건드리지 않고 ErrExists 를 돌려준다.
func (s Store) Import(ctx context.Context, id string, r io.Reader, size int64, onProgress func(done int64)) (res ImportResult, err error) {
	if err := validID(id); err != nil {
		return res, err
	}
	if size <= 0 {
		return res, fmt.Errorf("models: import size must be positive")
	}
	if err := os.MkdirAll(s.Dir, 0o755); err != nil {
		return res, fmt.Errorf("models: create directory: %w", err)
	}
	dest, part := s.path(id), s.path(id)+partSuffix
	for _, p := range []string{dest, part, dest + sidecarSuffix} {
		if _, statErr := os.Lstat(p); statErr == nil {
			return res, ErrExists
		}
	}
	f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return res, ErrExists
		}
		return res, fmt.Errorf("models: create %s: %w", id+partSuffix, err)
	}
	keep := false
	defer func() {
		f.Close()
		if !keep {
			os.Remove(part)
		}
	}()

	h := sha256.New()
	buf := make([]byte, 1<<20)
	var done, lastReport int64
	for done < size {
		if err := ctx.Err(); err != nil {
			return res, err
		}
		want := int64(len(buf))
		if size-done < want {
			want = size - done
		}
		n, readErr := io.ReadFull(r, buf[:want])
		if n > 0 {
			if _, werr := f.Write(buf[:n]); werr != nil {
				return res, fmt.Errorf("models: write %s: %w", id+partSuffix, werr)
			}
			h.Write(buf[:n])
			done += int64(n)
			if onProgress != nil && done-lastReport >= 32<<20 {
				lastReport = done
				onProgress(done)
			}
		}
		if readErr != nil {
			if done == size {
				break
			}
			if errors.Is(readErr, io.EOF) || errors.Is(readErr, io.ErrUnexpectedEOF) {
				return res, fmt.Errorf("%w: received %d of %d bytes", ErrSizeMismatch, done, size)
			}
			return res, fmt.Errorf("models: read upload: %w", readErr)
		}
	}
	// 본문이 선언한 길이보다 길면 거부한다.
	var extra [1]byte
	if n, _ := r.Read(extra[:]); n > 0 {
		return res, fmt.Errorf("%w: more data than the declared %d bytes", ErrSizeMismatch, size)
	}
	if err := f.Sync(); err != nil {
		return res, fmt.Errorf("models: sync %s: %w", id+partSuffix, err)
	}
	// 머리글 확인은 쓴 파일을 다시 읽어서 한다(실제로 저장된 바이트를 본다).
	rf, err := os.Open(part)
	if err != nil {
		return res, fmt.Errorf("models: reopen %s: %w", id+partSuffix, err)
	}
	_, metaErr := gguf.ReadMeta(bufio.NewReader(rf))
	rf.Close()
	if metaErr != nil {
		return res, fmt.Errorf("%w: %v", ErrNotGGUF, metaErr)
	}
	if err := f.Close(); err != nil {
		return res, fmt.Errorf("models: close %s: %w", id+partSuffix, err)
	}
	if err := os.Rename(part, dest); err != nil {
		return res, fmt.Errorf("models: rename %s: %w", id+partSuffix, err)
	}
	keep = true // 이름이 바뀌었으므로 .part 는 없다.
	sum := hex.EncodeToString(h.Sum(nil))
	if err := s.WriteSidecar(id, Sidecar{Repo: ImportRepo, Revision: ImportRevision, SHA256: sum}); err != nil {
		os.Remove(dest)
		return res, err
	}
	if onProgress != nil {
		onProgress(done)
	}
	return ImportResult{ID: id, Size: size, SHA256: sum}, nil
}
