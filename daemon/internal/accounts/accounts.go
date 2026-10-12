// Package accounts는 사용자가 정한 이름과 비밀번호로 로그인 계정을 만들고 지운다.
//
// lightlixd 는 ProtectSystem 과 ProtectHome 아래에서 돌아 /etc 와 /home 에 쓸 수 없다. 그래서 실제 작업은
// 하드닝이 없는 헬퍼(lightlix-user)가 하고, 데몬은 systemd-run --pipe 로 그 헬퍼를 호출한다. 비밀번호는 표준 입력으로만
// 넘기며 인자, 환경 변수, 파일, 로그에 남기지 않는다.
package accounts

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

// 기본 경로다.
const (
	DefaultHelper   = "/usr/lib/lightlix/lightlix-user"
	DefaultListPath = "/var/lib/lightlix/users.list"
)

// 오류 코드다. API 계층이 HTTP 상태로 바꾼다.
const (
	CodeInvalidUsername = "invalid_username"
	CodeInvalidPassword = "invalid_password"
	CodeUserExists      = "user_exists"
	CodeUserNotFound    = "user_not_found"
	CodeAccountFailed   = "account_failed"
	CodeSSHConfig       = "ssh_config"
	CodeSSHKeys         = "ssh_keys"
)

// Error는 코드를 가진 오류다.
type Error struct {
	Code    string
	Message string
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

// Account는 Lightlix 를 통해 만든 계정이다.
type Account struct {
	Name    string `json:"name"`
	Admin   bool   `json:"admin"`
	Created string `json:"created"`
}

// Manager는 계정 목록을 읽고 헬퍼를 호출한다.
type Manager struct {
	// Runner는 systemd-run 을 실행한다.
	Runner platform.Runner
	// Helper는 헬퍼 실행 파일 경로다. 비어 있으면 DefaultHelper 다.
	Helper string
	// ListPath는 헬퍼가 쓰는 계정 목록 파일이다. 비어 있으면 DefaultListPath 다.
	ListPath string
	// Timeout은 헬퍼 한 번의 제한 시간이다. 0이면 60초다.
	Timeout time.Duration
}

var nameRE = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)

// reserved는 헬퍼의 예약어 목록과 같다. 헬퍼도 다시 검사한다.
var reserved = map[string]bool{
	"root": true, "user": true, "nobody": true, "daemon": true, "bin": true, "sys": true, "sync": true,
	"games": true, "man": true, "lp": true, "mail": true, "news": true, "uucp": true, "proxy": true,
	"www-data": true, "backup": true, "list": true, "irc": true, "sshd": true, "messagebus": true,
	"polkitd": true, "greetd": true, "tuigreet": true,
}

// ValidName은 사용자 이름이 규칙에 맞는지 본다. 소문자로 시작하고 소문자, 숫자, 밑줄, 하이픈만 쓰며 32자 이하다.
func ValidName(name string) bool {
	if !nameRE.MatchString(name) || reserved[name] {
		return false
	}
	return !strings.HasPrefix(name, "lightlix") && !strings.HasPrefix(name, "systemd")
}

// ValidPassword는 비밀번호가 8~128자의 출력 가능한 문자이고 사용자 이름과 다른지 본다.
func ValidPassword(name, password string) bool {
	n := len(password)
	if n < 8 || n > 128 || password == name {
		return false
	}
	for i := 0; i < n; i++ {
		if c := password[i]; c < 0x20 || c > 0x7e {
			return false
		}
	}
	return true
}

func (m *Manager) helper() string {
	if m.Helper != "" {
		return m.Helper
	}
	return DefaultHelper
}

func (m *Manager) listPath() string {
	if m.ListPath != "" {
		return m.ListPath
	}
	return DefaultListPath
}

// List는 계정 목록을 읽는다. 파일이 없으면 빈 목록이다.
func (m *Manager) List() ([]Account, error) {
	f, err := os.Open(m.listPath())
	if errors.Is(err, fs.ErrNotExist) {
		return []Account{}, nil
	}
	if err != nil {
		return nil, fmt.Errorf("accounts: open %s: %w", m.listPath(), err)
	}
	defer f.Close()
	out := []Account{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 || !ValidName(fields[0]) {
			continue
		}
		a := Account{Name: fields[0], Admin: fields[1] == "1"}
		if len(fields) >= 3 {
			a.Created = fields[2]
		}
		out = append(out, a)
	}
	return out, sc.Err()
}

// Names는 계정 이름만 돌려준다.
func (m *Manager) Names() ([]string, error) {
	list, err := m.List()
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(list))
	for _, a := range list {
		names = append(names, a.Name)
	}
	return names, nil
}

// Add는 계정을 만든다. 비밀번호는 표준 입력으로만 넘긴다.
func (m *Manager) Add(ctx context.Context, name, password string, admin bool) error {
	if !ValidName(name) {
		return &Error{CodeInvalidUsername, "the user name must start with a lowercase letter, use only a-z, 0-9, _ and -, and be at most 32 characters"}
	}
	if !ValidPassword(name, password) {
		return &Error{CodeInvalidPassword, "the password must be 8 to 128 printable ASCII characters and differ from the user name"}
	}
	args := []string{"add", name}
	if admin {
		args = append(args, "--admin")
	}
	return m.run(ctx, args, []byte(password+"\n"))
}

// Remove는 Lightlix 가 만든 계정을 지운다. 홈 디렉터리는 남긴다.
func (m *Manager) Remove(ctx context.Context, name string) error {
	if !ValidName(name) {
		return &Error{CodeInvalidUsername, "the user name is not allowed"}
	}
	return m.run(ctx, []string{"remove", name}, nil)
}

// PrepareSSH는 없는 SSH 호스트 키를 만들고 sshd 드롭인을 현재 계정 목록에 맞춘다.
// lightlixd 는 /etc 에 쓸 수 없어 이 일도 헬퍼가 한다. SSH 를 켜기 전에 부른다.
func (m *Manager) PrepareSSH(ctx context.Context) error {
	return m.run(ctx, []string{"ssh-prepare"}, nil)
}

func (m *Manager) run(ctx context.Context, helperArgs []string, stdin []byte) error {
	timeout := m.Timeout
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	// 클라이언트가 연결을 끊어도 계정 변경은 끝까지 간다.
	ctx = context.WithoutCancel(ctx)
	args := []string{"--pipe", "--wait", "--collect", "--quiet", "--service-type=exec", m.helper()}
	args = append(args, helperArgs...)
	res, err := m.Runner.Run(ctx, platform.Command{
		Name:      "systemd-run",
		Args:      args,
		Stdin:     stdin,
		Timeout:   timeout,
		MaxOutput: 16 << 10,
	})
	if err != nil {
		return &Error{CodeAccountFailed, fmt.Sprintf("systemd-run: %v", err)}
	}
	return parseResult(res)
}

// parseResult는 헬퍼가 표준 출력 마지막에 쓴 결과 줄을 읽는다.
// systemd-run 의 종료 코드에 기대지 않는다. 결과 줄이 없으면 실패다.
func parseResult(res platform.Result) error {
	const prefix = "LIGHTLIX_USER_RESULT="
	var last string
	for _, line := range bytes.Split(res.Stdout, []byte("\n")) {
		if s := strings.TrimSpace(string(line)); strings.HasPrefix(s, prefix) {
			last = strings.TrimPrefix(s, prefix)
		}
	}
	switch {
	case last == "ok":
		return nil
	case strings.HasPrefix(last, "error:"):
		code := strings.TrimPrefix(last, "error:")
		switch code {
		case CodeInvalidUsername, CodeInvalidPassword, CodeUserExists, CodeUserNotFound, CodeAccountFailed, CodeSSHConfig, CodeSSHKeys:
		default:
			code = CodeAccountFailed
		}
		return &Error{code, firstLine(string(res.Stderr), "the account helper reported an error")}
	}
	return &Error{CodeAccountFailed, "the account helper did not report a result"}
}

func firstLine(s, fallback string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			if len(t) > 200 {
				t = t[:200]
			}
			return t
		}
	}
	return fallback
}
