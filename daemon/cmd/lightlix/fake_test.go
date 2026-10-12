package main

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// fakeDaemon 은 유닉스 소켓에서 응답하는 가짜 lightlixd 다. 키는 "메서드 경로" 형식이다.
type fakeDaemon struct {
	sock string

	mu     sync.Mutex
	routes map[string]http.HandlerFunc
	hits   []string
	bodies map[string][]string
}

// reply 는 고정 응답 하나다.
type reply struct {
	status int
	body   string
}

// newFakeDaemon 은 임시 소켓에 가짜 데몬을 띄운다. 테스트가 끝나면 닫는다.
func newFakeDaemon(t *testing.T) *fakeDaemon {
	t.Helper()
	dir, err := os.MkdirTemp("", "lxcli")
	if err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f := &fakeDaemon{
		sock:   filepath.Join(dir, "d.sock"),
		routes: map[string]http.HandlerFunc{},
		bodies: map[string][]string{},
	}
	ln, err := net.Listen("unix", f.sock)
	if err != nil {
		os.RemoveAll(dir)
		t.Fatalf("listen: %v", err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(f.serve)}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() {
		_ = srv.Close()
		os.RemoveAll(dir)
	})
	return f
}

// serve 는 요청을 기록하고 등록된 응답을 쓴다. 없는 경로는 404 JSON 이다.
func (f *fakeDaemon) serve(w http.ResponseWriter, r *http.Request) {
	body, _ := io.ReadAll(r.Body)
	key := r.Method + " " + r.URL.Path
	f.mu.Lock()
	f.hits = append(f.hits, r.Method+" "+r.URL.RequestURI())
	f.bodies[key] = append(f.bodies[key], string(body))
	h, ok := f.routes[key]
	f.mu.Unlock()
	if !ok {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"error":{"code":"not_found","message":"no such endpoint"}}`)
		return
	}
	h(w, r)
}

// on 은 키에 고정 응답을 등록한다.
func (f *fakeDaemon) on(key string, status int, body string) {
	f.onFunc(key, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = io.WriteString(w, body)
	})
}

// onFunc 은 키에 직접 만든 핸들러를 등록한다.
func (f *fakeDaemon) onFunc(key string, h http.HandlerFunc) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.routes[key] = h
}

// seq 는 키에 응답 순서를 등록한다. 마지막 응답은 계속 반복된다.
func (f *fakeDaemon) seq(key string, replies ...reply) {
	var mu sync.Mutex
	i := 0
	f.onFunc(key, func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		r := replies[i]
		if i < len(replies)-1 {
			i++
		}
		mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(r.status)
		_, _ = io.WriteString(w, r.body)
	})
}

// bodiesOf 는 키로 받은 요청 본문들을 돌려준다.
func (f *fakeDaemon) bodiesOf(key string) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.bodies[key]...)
}

// hitsContain 은 받은 요청 중 "메서드 경로?쿼리" 가 있는지 본다.
func (f *fakeDaemon) hitsContain(want string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, h := range f.hits {
		if h == want {
			return true
		}
	}
	return false
}

// outcome 은 한 번의 명령 실행 결과다.
type outcome struct {
	code   int
	stdout string
	stderr string
}

// cliOpts 는 실행 환경 설정이다.
type cliOpts struct {
	stdin     string
	stdinTTY  bool
	outTTY    bool
	env       map[string]string
	cancelCtx context.Context
	// readPassword 는 터미널 비밀번호 입력을 대신한다.
	readPassword func(prompt string) (string, error)
}

// runCLI 는 --socket 을 붙여 명령을 실행한다.
func runCLI(t *testing.T, f *fakeDaemon, args ...string) outcome {
	t.Helper()
	return runCLIWith(t, f, cliOpts{}, args...)
}

// runCLIWith 는 옵션을 주고 명령을 실행한다. 출력이 ASCII 인지 항상 검사한다.
func runCLIWith(t *testing.T, f *fakeDaemon, opt cliOpts, args ...string) outcome {
	t.Helper()
	full := args
	if f != nil {
		full = append([]string{"--socket", f.sock}, args...)
	}
	ctx := opt.cancelCtx
	if ctx == nil {
		ctx = context.Background()
	}
	var out, errb bytes.Buffer
	env := runEnv{
		ctx:          ctx,
		stdin:        strings.NewReader(opt.stdin),
		stdout:       &out,
		stderr:       &errb,
		getenv:       func(k string) string { return opt.env[k] },
		stdinTTY:     opt.stdinTTY,
		stdoutTTY:    opt.outTTY,
		pollInterval: 0,
		readPassword: opt.readPassword,
	}
	code := execute(env, full)
	res := outcome{code: code, stdout: out.String(), stderr: errb.String()}
	assertASCII(t, "stdout", res.stdout)
	assertASCII(t, "stderr", res.stderr)
	return res
}

// assertASCII 는 s 가 출력 가능한 ASCII 와 줄바꿈, 탭, 캐리지 리턴만 담는지 검사한다.
func assertASCII(t *testing.T, name, s string) {
	t.Helper()
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c == '\n' || c == '\t' || c == '\r' {
			continue
		}
		if c < 0x20 || c >= 0x7f {
			t.Fatalf("%s has non-ASCII byte 0x%02x at %d: %q", name, c, i, s)
		}
	}
}

// wantContains 는 out 에 want 가 있는지 검사한다.
func wantContains(t *testing.T, label, out string, wants ...string) {
	t.Helper()
	for _, w := range wants {
		if !strings.Contains(out, w) {
			t.Fatalf("%s: missing %q in:\n%s", label, w, out)
		}
	}
}

// wantCode 는 종료 코드를 검사한다. 실패하면 두 출력을 함께 보여 준다.
func wantCode(t *testing.T, res outcome, want int) {
	t.Helper()
	if res.code != want {
		t.Fatalf("exit code = %d, want %d\nstdout:\n%s\nstderr:\n%s", res.code, want, res.stdout, res.stderr)
	}
}
