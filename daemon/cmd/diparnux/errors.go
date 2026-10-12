package main

import (
	"context"
	"errors"
	"fmt"

	"github.com/nxodh/diparnuxos/daemon/internal/client"
)

// fail 은 API 나 연결 오류를 사람용 한 줄로 쓰고 종료 코드를 돌려준다.
func (c *cli) fail(err error) int {
	code, msg := describeError(err)
	c.errorLine(msg)
	return code
}

// errorLine 은 오류 한 줄을 표준 오류에 쓴다.
func (c *cli) errorLine(msg string) {
	fmt.Fprintf(c.env.stderr, "diparnux: %s\n", msg)
}

// describeError 는 오류를 종료 코드와 한 줄 설명으로 바꾼다.
// 연결 실패는 3, 데몬이 돌려준 오류와 그 밖의 실패는 1 이다.
func describeError(err error) (int, string) {
	switch {
	case errors.Is(err, client.ErrPermission):
		return exitUnreachable, "permission denied: add your user to the 'diparnux' group or run with sudo"
	case errors.Is(err, client.ErrUnavailable):
		return exitUnreachable, "diparnuxd is not running (systemctl status diparnuxd)"
	case errors.Is(err, client.ErrTooLarge):
		return exitFailure, "the response from diparnuxd is larger than the limit"
	case errors.Is(err, context.DeadlineExceeded):
		return exitFailure, "timed out waiting for diparnuxd"
	case errors.Is(err, context.Canceled):
		return exitFailure, "interrupted"
	}
	var ae *client.APIError
	if errors.As(err, &ae) {
		return exitFailure, fmt.Sprintf("%s (%s)", ae.Message, ae.Code)
	}
	return exitFailure, err.Error()
}

// pullHint 는 내려받기 오류 코드에 대한 안내 한 줄이다. 알 수 없는 코드면 빈 문자열이다.
// need 가 0 보다 크면 insufficient_space 안내에 필요한 공간을 넣는다.
func pullHint(code string, need int64) string {
	switch code {
	case "unauthorized":
		return "repository not found, or it is private (set a token: diparnux config set hf-token)"
	case "gated":
		return "repository is gated: open it on huggingface.co, accept its terms, then set a token: diparnux config set hf-token"
	case "insufficient_space":
		if need > 0 {
			return fmt.Sprintf("not enough free disk space: the model needs about %s", humanBytes(need))
		}
		return "not enough free disk space for the model"
	case "checksum_mismatch":
		return "the downloaded file does not match the SHA-256 in the repository; try again"
	case "rate_limited":
		return "rate limited by Hugging Face; try again later"
	}
	return ""
}

// neededBytes 는 file 의 크기를 저장소 목록에서 찾는다. file 이 비었고 GGUF 가 하나뿐이면 그 크기다.
// 알 수 없으면 0 이다.
func neededBytes(remote *client.Remote, file string) int64 {
	if remote == nil {
		return 0
	}
	for _, f := range remote.Files {
		if file != "" && f.Path == file {
			return f.Size
		}
	}
	if file == "" && len(remote.Files) == 1 {
		return remote.Files[0].Size
	}
	return 0
}

// pullFailure 는 내려받기 요청(저장소 조회나 작업 만들기)이 실패했을 때 안내를 쓴다.
func (c *cli) pullFailure(err error, remote *client.Remote, file string) int {
	var ae *client.APIError
	if !errors.As(err, &ae) {
		return c.fail(err)
	}
	if ae.Code == "ambiguous_file" {
		c.errorLine("repository has more than one GGUF file; choose one with --file:")
		c.writeCandidates(remote)
		return exitFailure
	}
	if hint := pullHint(ae.Code, neededBytes(remote, file)); hint != "" {
		c.errorLine(fmt.Sprintf("%s (code: %s)", hint, ae.Code))
		return exitFailure
	}
	return c.fail(err)
}

// jobFailure 는 끝난 내려받기 작업이 실패했을 때 오류와 안내를 쓴다.
func (c *cli) jobFailure(j client.Job, remote *client.Remote) int {
	msg := j.Error
	if msg == "" {
		msg = "download failed"
	}
	c.errorLine(fmt.Sprintf("download failed: %s (code: %s)", msg, orDash(j.ErrorCode)))
	if hint := pullHint(j.ErrorCode, neededBytes(remote, j.File)); hint != "" {
		c.errorLine(hint)
	}
	return exitFailure
}

// writeCandidates 는 GGUF 파일 후보를 표준 오류에 표로 쓴다.
func (c *cli) writeCandidates(remote *client.Remote) {
	if remote == nil {
		return
	}
	rows := make([][]string, 0, len(remote.Files))
	for _, f := range remote.Files {
		rows = append(rows, []string{f.Path, humanBytes(f.Size)})
	}
	writeTable(c.env.stderr, []string{"FILE", "SIZE"}, rows)
}
