package api

import (
	"errors"
	"net/http"
	"os"
	"syscall"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/models"
	"github.com/nxodh/diparnuxos/daemon/internal/sysinfo"
)

// 가져오기 한도와 기한이다.
const (
	// importMinFree 는 가져온 뒤에도 남겨 둘 디스크 여유 바이트다.
	importMinFree = 1 << 30
	// importMaxBytes 는 가져올 수 있는 파일의 최대 크기다(실수로 보낸 거대한 파일을 막는다).
	importMaxBytes = 1 << 40
	// importIdle 는 본문이 이 시간 동안 늘지 않으면 포기하는 기한이다.
	importIdle = 2 * time.Minute
)

// importDiskFree 는 시험에서 바꿀 수 있다.
var importDiskFree = sysinfo.DiskFree

// importModel 은 사용자가 가진 GGUF 파일을 모델 디렉터리로 받는다.
//
//	PUT /v1/models/import?name=<id>.gguf   (본문: 파일 내용, Content-Length 필수)
//
// 파일을 열어 보내는 쪽은 CLI 나 제어 센터이므로 사용자는 자기가 읽을 수 있는 파일만 가져올 수 있다.
// 이미 있는 이름은 덮어쓰지 않는다(409). 한 번에 하나만 받는다.
func (s *server) importModel(w http.ResponseWriter, r *http.Request) {
	name := r.URL.Query().Get("name")
	if err := models.CheckID(name); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_input", "%v", err)
		return
	}
	size := r.ContentLength
	switch {
	case size < 0:
		writeError(w, http.StatusLengthRequired, "length_required", "the request needs a Content-Length")
		return
	case size == 0:
		writeError(w, http.StatusBadRequest, "invalid_input", "the file is empty")
		return
	case size > importMaxBytes:
		writeError(w, http.StatusRequestEntityTooLarge, "body_too_large", "the file is larger than %d bytes", int64(importMaxBytes))
		return
	}
	if !s.importMu.TryLock() {
		writeError(w, http.StatusConflict, "busy", "another model import is in progress")
		return
	}
	defer s.importMu.Unlock()

	if err := os.MkdirAll(s.d.Store.Dir, 0o755); err == nil {
		if free, err := importDiskFree(s.d.Store.Dir); err == nil && free < uint64(size)+importMinFree {
			writeError(w, http.StatusInsufficientStorage, "insufficient_space", "not enough free disk space for %d bytes", size)
			return
		}
	}

	// 서버의 기본 읽기·쓰기 기한(60초, 180초)은 큰 파일에 짧다. 본문이 진행되는 동안 기한을 계속 늘린다.
	rc := http.NewResponseController(w)
	extend := func() {
		deadline := time.Now().Add(importIdle)
		_ = rc.SetReadDeadline(deadline)
		_ = rc.SetWriteDeadline(deadline.Add(importIdle))
	}
	extend()
	res, err := s.d.Store.Import(r.Context(), name, r.Body, size, func(int64) { extend() })
	if err != nil {
		switch {
		case errors.Is(err, models.ErrExists):
			writeError(w, http.StatusConflict, "name_conflict", "a model named %s already exists; choose another name or delete it first", name)
		case errors.Is(err, models.ErrNotGGUF):
			writeError(w, http.StatusBadRequest, "not_gguf", "the file is not a GGUF model")
		case errors.Is(err, models.ErrSizeMismatch):
			writeError(w, http.StatusBadRequest, "size_mismatch", "the upload was shorter or longer than its declared length")
		case errors.Is(err, syscall.ENOSPC):
			writeError(w, http.StatusInsufficientStorage, "insufficient_space", "the disk is full")
		default:
			writeErr(w, err)
		}
		return
	}

	// 기본 모델이 없으면 방금 가져온 모델을 기본으로 한다. 이미 있으면 바꾸지 않는다.
	madeDefault := false
	if cur, err := s.d.Store.Default(); err != nil || cur == "" {
		if err := s.d.Store.SetDefault(res.ID); err == nil {
			madeDefault = true
		}
	}
	writeJSON(w, http.StatusCreated, map[string]any{
		"model_id":   res.ID,
		"size_bytes": res.Size,
		"sha256":     res.SHA256,
		"default":    madeDefault,
	})
}
