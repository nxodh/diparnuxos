// Package admin 은 diparnuxd 가 직접 쓸 수 없는 곳(/etc 의 네트워크와 SSH 설정, 사용자 홈의 authorized_keys)에
// 파일을 쓰는 권한 있는 헬퍼(diparnux-admin)를 부른다.
//
// diparnuxd 는 ProtectSystem=full 과 ProtectHome=yes 아래에서 돌아 그런 곳에 쓸 수 없다. 헬퍼는 하드닝이 없는 임시
// 유닛에서 root 로 돌고(systemd-run --pipe), 쓸 수 있는 경로를 이 패키지의 허용 목록으로만 제한한다. 규칙은 헬퍼와
// 데몬 양쪽에서 검사한다. 파일 내용은 표준 입력으로만 넘기고 인자나 로그에 남기지 않는다.
package admin

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"path"
	"regexp"
	"strings"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// DefaultHelper 는 헬퍼 설치 경로다.
const DefaultHelper = "/usr/lib/diparnux/diparnux-admin"

// 결과 줄 접두사다. 헬퍼가 표준 출력에 쓴다.
const (
	ResultPrefix = "DIPARNUX_ADMIN_RESULT="
	DataPrefix   = "DIPARNUX_ADMIN_DATA="
)

// 오류 코드다.
const (
	CodeInvalidPath = "invalid_path"
	CodeInvalidUser = "invalid_user"
	CodeTooLarge    = "too_large"
	CodeFailed      = "admin_failed"
)

// MaxContent 는 한 번에 쓸 수 있는 내용의 최대 바이트 수다.
const MaxContent = 256 << 10

// Error 는 코드를 가진 오류다.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// 허용 경로다. 데몬과 헬퍼가 같은 목록을 쓴다.
var allowed = []*regexp.Regexp{
	regexp.MustCompile(`^/etc/systemd/network/[0-9]{2}-diparnux-[a-z0-9][a-z0-9._-]{0,40}\.(network|netdev|link)$`),
	regexp.MustCompile(`^/etc/ssh/sshd_config\.d/60-diparnux-[a-z0-9][a-z0-9-]{0,30}\.conf$`),
	regexp.MustCompile(`^/etc/hosts$`),
}

// AllowedPath 는 헬퍼가 쓰거나 지울 수 있는 경로인지 본다.
func AllowedPath(p string) bool {
	if !path.IsAbs(p) || path.Clean(p) != p {
		return false
	}
	for _, re := range allowed {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}

// Client 는 헬퍼를 부른다.
type Client struct {
	Runner  platform.Runner
	Helper  string
	Timeout time.Duration
}

func (c *Client) helper() string {
	if c.Helper != "" {
		return c.Helper
	}
	return DefaultHelper
}

// Write 는 path 에 data 를 원자적으로 쓴다. secret 이면 파일을 root:systemd-network 0640 으로 만든다(개인 키가 든 파일).
func (c *Client) Write(ctx context.Context, p string, data []byte, secret bool) error {
	if !AllowedPath(p) {
		return &Error{CodeInvalidPath, "the helper may not write " + p}
	}
	if len(data) > MaxContent {
		return &Error{CodeTooLarge, "the file is too large"}
	}
	args := []string{"write", p}
	if secret {
		args = append(args, "--secret")
	}
	_, err := c.run(ctx, args, data)
	return err
}

// Remove 는 허용된 경로의 파일을 지운다. 없는 파일은 오류가 아니다.
func (c *Client) Remove(ctx context.Context, p string) error {
	if !AllowedPath(p) {
		return &Error{CodeInvalidPath, "the helper may not remove " + p}
	}
	_, err := c.run(ctx, []string{"remove", p}, nil)
	return err
}

// ReadAuthorizedKeys 는 사용자의 authorized_keys 내용을 읽는다. 파일이 없으면 빈 내용이다.
func (c *Client) ReadAuthorizedKeys(ctx context.Context, user string) ([]byte, error) {
	if !ValidUser(user) {
		return nil, &Error{CodeInvalidUser, "the user name is not allowed"}
	}
	return c.run(ctx, []string{"authkeys-read", user}, nil)
}

// WriteAuthorizedKeys 는 사용자의 authorized_keys 를 data 로 바꾼다.
func (c *Client) WriteAuthorizedKeys(ctx context.Context, user string, data []byte) error {
	if !ValidUser(user) {
		return &Error{CodeInvalidUser, "the user name is not allowed"}
	}
	if len(data) > MaxContent {
		return &Error{CodeTooLarge, "the key list is too large"}
	}
	_, err := c.run(ctx, []string{"authkeys-write", user}, data)
	return err
}

var userRE = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// ValidUser 는 사용자 이름 형식을 본다. 헬퍼가 uid 와 홈 디렉터리도 다시 확인한다.
func ValidUser(name string) bool { return name != "root" && userRE.MatchString(name) }

func (c *Client) run(ctx context.Context, helperArgs []string, stdin []byte) ([]byte, error) {
	timeout := c.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	// 클라이언트가 연결을 끊어도 파일 쓰기는 끝까지 간다.
	ctx = context.WithoutCancel(ctx)
	args := []string{"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec", c.helper()}
	args = append(args, helperArgs...)
	res, err := c.Runner.Run(ctx, platform.Command{Name: "systemd-run", Args: args, Stdin: stdin, Timeout: timeout, MaxOutput: MaxContent * 2})
	if err != nil {
		return nil, &Error{CodeFailed, fmt.Sprintf("systemd-run: %v", err)}
	}
	return ParseResult(res)
}

// ParseResult 는 헬퍼의 표준 출력에서 결과 줄과 데이터 줄을 읽는다. 결과 줄이 없으면 실패다.
func ParseResult(res platform.Result) ([]byte, error) {
	var result string
	var data []byte
	for _, line := range bytes.Split(res.Stdout, []byte("\n")) {
		s := strings.TrimSpace(string(line))
		switch {
		case strings.HasPrefix(s, ResultPrefix):
			result = strings.TrimPrefix(s, ResultPrefix)
		case strings.HasPrefix(s, DataPrefix):
			b, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(s, DataPrefix))
			if err != nil {
				return nil, &Error{CodeFailed, "the helper returned data that cannot be decoded"}
			}
			data = b
		}
	}
	switch {
	case result == "ok":
		return data, nil
	case strings.HasPrefix(result, "error:"):
		code := strings.TrimPrefix(result, "error:")
		switch code {
		case CodeInvalidPath, CodeInvalidUser, CodeTooLarge:
		default:
			code = CodeFailed
		}
		msg := "the helper reported an error"
		for _, l := range strings.Split(string(res.Stderr), "\n") {
			if t := strings.TrimSpace(l); t != "" {
				msg = t
				break
			}
		}
		return nil, &Error{code, msg}
	}
	return nil, &Error{CodeFailed, "the helper did not report a result"}
}
