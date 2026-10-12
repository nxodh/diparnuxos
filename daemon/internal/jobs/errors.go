package jobs

import (
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"unicode/utf8"

	"github.com/nxodh/diparnuxos/daemon/internal/hf"
)

// 작업 오류 코드다. Job.ErrorCode 와 PullError.Code 에 들어간다. 값은 API 와 CLI 가 그대로 보여 준다.
const (
	CodeGated             = "gated"
	CodeNotFound          = "not_found"
	CodeUnauthorized      = "unauthorized"
	CodeRateLimited       = "rate_limited"
	CodeInsufficientSpace = "insufficient_space"
	CodeChecksumMismatch  = "checksum_mismatch"
	CodeSizeMismatch      = "size_mismatch"
	CodeCorruptModel      = "corrupt_model"
	CodeInvalidInput      = "invalid_input"
	CodeNetwork           = "network"
	CodeAmbiguousFile     = "ambiguous_file"
	CodeNameConflict      = "name_conflict"
	// CodeInternal 은 로컬 파일 시스템이나 메타데이터 쓰기 같은 데몬 쪽 오류다.
	CodeInternal = "internal"
)

// 메서드가 돌려주는 센티넬 오류다. errors.Is 로 확인한다.
var (
	// ErrNotFound 는 없는 작업 ID 를 받았을 때 돌려준다.
	ErrNotFound = errors.New("job not found")
	// ErrJobFinished 는 이미 끝난 작업을 취소하려 할 때 돌려준다.
	ErrJobFinished = errors.New("job already finished")
	// ErrClosed 는 Close 된 Manager 를 쓰려 할 때 돌려준다.
	ErrClosed = errors.New("job manager is closed")
)

// maxDetailLen 은 오류 설명에 붙이는 원문의 최대 글자 수다.
const maxDetailLen = 200

// PullError 는 StartPull 이 작업을 만들기 전에 요청을 거부했을 때 돌려주는 오류다.
// Code 는 위의 Code 상수 중 하나이고, Message 는 사람이 읽는 영어 한 줄이다. 토큰은 들어 있지 않다.
type PullError struct {
	// Code 는 오류 코드다.
	Code string
	// Message 는 한 줄짜리 설명이다.
	Message string
	// Candidates 는 ambiguous_file 일 때 고를 수 있는 파일 경로 목록이다.
	Candidates []string
	// Err 는 원인이 된 오류다. errors.Is 와 errors.As 가 따라간다.
	Err error
}

// Error 는 Message 를 돌려준다.
func (e *PullError) Error() string { return e.Message }

// Unwrap 은 원인이 된 오류를 돌려준다.
func (e *PullError) Unwrap() error { return e.Err }

// CodeOf 는 err 가 PullError 면 그 Code 를, 아니면 빈 문자열을 돌려준다.
func CodeOf(err error) string {
	var pe *PullError
	if errors.As(err, &pe) {
		return pe.Code
	}
	return ""
}

// newPullError 는 코드와 설명으로 PullError 를 만든다.
func newPullError(code, msg string, err error) *PullError {
	return &PullError{Code: code, Message: msg, Err: err}
}

// classify 는 hf 가 돌려준 오류를 작업 오류 코드와 한 줄 설명으로 바꾼다.
// 설명에는 토큰이 들어가지 않는다. 원문 설명은 sanitize 를 거친다.
func classify(err error, token string) (code, msg string) {
	var rl *hf.RateLimitError
	var se *hf.StatusError
	var ne net.Error
	switch {
	case errors.Is(err, hf.ErrGated):
		return CodeGated, "repository is gated; a token with access is required"
	case errors.Is(err, hf.ErrNotFound):
		return CodeNotFound, "repository, revision, or file was not found"
	case errors.Is(err, hf.ErrUnauthorized):
		return CodeUnauthorized, "a Hugging Face token is required or the token has no access"
	case errors.Is(err, hf.ErrRateLimited):
		msg := "rate limited by Hugging Face; try again later"
		if errors.As(err, &rl) && rl.ResetSeconds > 0 {
			msg = fmt.Sprintf("rate limited by Hugging Face; retry after %d seconds", rl.ResetSeconds)
		}
		return CodeRateLimited, msg
	case errors.Is(err, hf.ErrInsufficientSpace):
		return CodeInsufficientSpace, "not enough free disk space for the model"
	case errors.Is(err, hf.ErrChecksumMismatch):
		return CodeChecksumMismatch, "downloaded file does not match the SHA-256 in the repository"
	case errors.Is(err, hf.ErrSizeMismatch):
		return CodeSizeMismatch, "downloaded size does not match the repository"
	case errors.Is(err, hf.ErrInvalidInput):
		return CodeInvalidInput, "request was rejected: " + detail(err, token)
	case errors.As(err, &se) && se.StatusCode >= 500:
		return CodeNetwork, "Hugging Face server error: " + detail(err, token)
	case errors.As(err, &ne), errors.Is(err, io.ErrUnexpectedEOF):
		return CodeNetwork, "network error: " + detail(err, token)
	}
	return CodeInternal, "internal error: " + detail(err, token)
}

// detail 은 오류 문자열을 sanitize 한 설명이다.
func detail(err error, token string) string {
	return sanitize(err.Error(), token)
}

// sanitize 는 토큰을 가리고, 줄바꿈과 연속 공백을 한 칸으로 줄이고, 글자 수를 제한한다.
// 토큰이 빈 문자열이면 가리지 않는다.
func sanitize(s, token string) string {
	if token != "" {
		s = strings.ReplaceAll(s, token, "<redacted>")
	}
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > maxDetailLen {
		s = string(r[:maxDetailLen]) + "..."
	}
	return s
}

// checkLocalName 은 내려받은 파일을 모델 디렉터리에 둘 이름이 models 패키지 규칙에 맞는지 본다.
// 이름은 .gguf 로 끝나야 하고, 숨김 파일이 아니어야 하며, 경로 구분자와 제어 문자를 포함하지 않아야 한다.
func checkLocalName(name string) error {
	switch {
	case name == "" || len(name) > maxLocalNameLen:
		return newPullError(CodeInvalidInput, "file name is empty or too long", nil)
	case strings.HasPrefix(name, "."):
		return newPullError(CodeInvalidInput, "file name must not start with a dot: "+name, nil)
	case !strings.HasSuffix(name, ggufSuffix) || len(name) == len(ggufSuffix):
		return newPullError(CodeInvalidInput, "file name must end in .gguf: "+name, nil)
	case !utf8.ValidString(name) || hasControl(name) || strings.ContainsAny(name, `/\`):
		return newPullError(CodeInvalidInput, "file name has a separator or a control character", nil)
	}
	return nil
}

// hasControl 은 ASCII 제어 문자나 DEL 이 있는지 본다.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}
