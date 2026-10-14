package webadmin

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
)

func newManager(t *testing.T) (*Manager, *[]string) {
	t.Helper()
	var seen []string
	api := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = append(seen, r.Method+" "+r.URL.Path)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	})
	m := &Manager{
		API: api, StateDir: t.TempDir(),
		Addresses: func() []string { return []string{"opereta.local", "192.168.0.23"} },
		FailDelay: time.Millisecond, Iterations: 1000,
	}
	return m, &seen
}

func do(t *testing.T, h http.Handler, method, path, body string, cookie *http.Cookie, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	var r *http.Request
	if body == "" {
		r = httptest.NewRequest(method, path, nil)
	} else {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	r.RemoteAddr = "192.168.0.5:50000"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	for k, v := range headers {
		r.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

var admin = map[string]string{"X-Opereta-Admin": "1"}

func login(t *testing.T, h http.Handler, pw string) (*http.Cookie, *httptest.ResponseRecorder) {
	t.Helper()
	w := do(t, h, "POST", "/auth/login", fmt.Sprintf(`{"password":%q}`, pw), nil, admin)
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			return c, w
		}
	}
	return nil, w
}

func TestPasswordRules(t *testing.T) {
	for _, bad := range []string{"", "short", "123456789", "has\x00nul-char", strings.Repeat("a", 200)} {
		if ValidPassword(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, good := range []string{"correct horse battery", "비밀번호는열글자이상입니다"} {
		if err := ValidPassword(good); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
}

func TestLoginSessionAndAllowList(t *testing.T) {
	m, seen := newManager(t)
	if err := m.SetPassword("correct horse battery"); err != nil {
		t.Fatal(err)
	}
	h := m.handler()

	// 로그인 전에는 API 가 막혀 있다.
	if w := do(t, h, "GET", "/v1/status", "", nil, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous status = %d", w.Code)
	}
	if len(*seen) != 0 {
		t.Fatal("an anonymous request reached the API")
	}
	// 틀린 비밀번호.
	if _, w := login(t, h, "wrong password!!"); w.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d", w.Code)
	}
	// 맞는 비밀번호는 보안 속성이 붙은 쿠키를 준다.
	ck, w := login(t, h, "correct horse battery")
	if w.Code != http.StatusOK || ck == nil {
		t.Fatalf("login = %d %s", w.Code, w.Body.String())
	}
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != http.SameSiteStrictMode || len(ck.Value) != 64 {
		t.Fatalf("cookie = %+v", ck)
	}
	// 읽기는 되고, 허용 목록 밖은 막힌다.
	if w := do(t, h, "GET", "/v1/status", "", ck, nil); w.Code != http.StatusOK {
		t.Fatalf("status = %d", w.Code)
	}
	for _, p := range []struct{ m, path string }{
		{"GET", "/v1/users"}, {"POST", "/v1/users"}, {"PUT", "/v1/ssh/advanced"}, {"POST", "/v1/ssh/keys"},
		{"PUT", "/v1/webadmin"}, {"PUT", "/v1/webadmin/password"}, {"POST", "/v1/vpn/peers"}, {"POST", "/v1/firewall/rules"},
		{"POST", "/v1/network/wifi/connect"}, {"PUT", "/v1/models/import"}, {"POST", "/v1/update"}, {"POST", "/v1/mode"},
		{"POST", "/v1/services/ssh.service/disable"}, {"PUT", "/v1/network/hostname"},
	} {
		w := do(t, h, p.m, p.path, `{}`, ck, admin)
		if w.Code != http.StatusForbidden {
			t.Errorf("%s %s = %d, want 403", p.m, p.path, w.Code)
		}
	}
	for _, p := range []struct{ m, path string }{
		{"POST", "/v1/server/start"}, {"POST", "/v1/server/stop"}, {"POST", "/v1/apikeys"}, {"DELETE", "/v1/apikeys/abc"},
		{"POST", "/v1/services/ssh.service/restart"}, {"PUT", "/v1/config"}, {"GET", "/v1/resources"}, {"GET", "/v1/services/ssh.service/logs"},
	} {
		w := do(t, h, p.m, p.path, `{}`, ck, admin)
		if w.Code != http.StatusOK {
			t.Errorf("%s %s = %d, want 200", p.m, p.path, w.Code)
		}
	}
	// 경로를 거슬러 올라가는 요청은 API 에 닿지 않는다(리다이렉트 되거나 막힌다).
	before := len(*seen)
	if w := do(t, h, "POST", "/v1/services/../x/restart", `{}`, ck, admin); w.Code == http.StatusOK {
		t.Fatalf("a path traversal was served: %d", w.Code)
	}
	if len(*seen) != before {
		t.Fatal("a path traversal reached the API")
	}
	// 변경 요청은 CSRF 헤더 없이는 거절한다.
	if w := do(t, h, "POST", "/v1/server/stop", `{}`, ck, nil); w.Code != http.StatusForbidden {
		t.Fatalf("no csrf header = %d", w.Code)
	}
	if w := do(t, h, "POST", "/v1/server/stop", `{}`, ck, map[string]string{"X-Opereta-Admin": "1", "Origin": "https://evil.example"}); w.Code != http.StatusForbidden {
		t.Fatalf("cross origin = %d", w.Code)
	}
	// 로그아웃하면 세션이 끝난다.
	do(t, h, "POST", "/auth/logout", `{}`, ck, admin)
	if w := do(t, h, "GET", "/v1/status", "", ck, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("after logout = %d", w.Code)
	}
}

func TestWrongPasswordsLockTheAddressOut(t *testing.T) {
	m, _ := newManager(t)
	m.SetPassword("correct horse battery")
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	h := m.handler()
	for i := 0; i < lockThreshold; i++ {
		if _, w := login(t, h, "nope nope nope"); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d", i, w.Code)
		}
	}
	// 잠긴 동안은 맞는 비밀번호도 받지 않는다.
	if _, w := login(t, h, "correct horse battery"); w.Code != http.StatusTooManyRequests {
		t.Fatalf("while locked = %d", w.Code)
	}
	now = now.Add(31 * time.Second)
	if ck, w := login(t, h, "correct horse battery"); w.Code != http.StatusOK || ck == nil {
		t.Fatalf("after the lock = %d", w.Code)
	}
}

func TestSessionsExpireWhenIdle(t *testing.T) {
	m, _ := newManager(t)
	m.SetPassword("correct horse battery")
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	m.Now = func() time.Time { return now }
	h := m.handler()
	ck, _ := login(t, h, "correct horse battery")
	now = now.Add(sessionIdle + time.Minute)
	if w := do(t, h, "GET", "/v1/status", "", ck, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("idle session = %d", w.Code)
	}
}

func TestChangingThePasswordEndsEverySession(t *testing.T) {
	m, _ := newManager(t)
	m.SetPassword("correct horse battery")
	h := m.handler()
	ck, _ := login(t, h, "correct horse battery")
	if err := m.SetPassword("another long password"); err != nil {
		t.Fatal(err)
	}
	if w := do(t, h, "GET", "/v1/status", "", ck, nil); w.Code != http.StatusUnauthorized {
		t.Fatalf("old session = %d", w.Code)
	}
	if _, w := login(t, h, "correct horse battery"); w.Code != http.StatusUnauthorized {
		t.Fatalf("old password = %d", w.Code)
	}
}

func TestStateFileHoldsOnlyAHash(t *testing.T) {
	m, _ := newManager(t)
	m.SetPassword("correct horse battery")
	data, err := os.ReadFile(m.path())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "correct horse battery") {
		t.Fatal("the password is stored in clear text")
	}
	st, _ := os.Stat(m.path())
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode())
	}
}

func TestEnableNeedsAPasswordAndServesTLS(t *testing.T) {
	m, _ := newManager(t)
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()

	yes := true
	if _, err := m.Configure(context.Background(), &yes, &port, nil); err == nil || err.(*Error).Code != CodeNoPassword {
		t.Fatalf("err = %v", err)
	}
	m.SetPassword("correct horse battery")
	reapplied := 0
	m.Reapply = func(ctx context.Context) error { reapplied++; return nil }
	st, err := m.Configure(context.Background(), &yes, &port, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if !st.Enabled || !st.Running || st.CertFingerprint == "" || reapplied != 1 || len(st.URLs) != 2 {
		t.Fatalf("status = %+v reapplied = %d", st, reapplied)
	}
	if p, src, ok := m.Opens(); !ok || p != port || src != SourceLAN {
		t.Fatalf("opens = %d %q %v", p, src, ok)
	}
	// 실제로 TLS 로 접속한다. 자체 서명이므로 검증은 건너뛰되 지문이 같은지 본다.
	client := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}, Timeout: 5 * time.Second}
	var resp *http.Response
	for i := 0; i < 20; i++ {
		resp, err = client.Get(fmt.Sprintf("https://127.0.0.1:%d/auth/state", port))
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if !bytes.Contains(body, []byte(`"authenticated":false`)) {
		t.Fatalf("body = %s", body)
	}
	if got := fingerprint(resp.TLS.PeerCertificates[0].Raw); got != st.CertFingerprint {
		t.Fatalf("fingerprint = %s, status = %s", got, st.CertFingerprint)
	}
	if h := resp.Header.Get("Content-Security-Policy"); !strings.Contains(h, "default-src 'self'") {
		t.Fatalf("CSP = %q", h)
	}
	// 끄면 포트가 닫히고 방화벽이 다시 적용된다.
	no := false
	if _, err := m.Configure(context.Background(), &no, nil, nil); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := m.Opens(); ok {
		t.Fatal("a disabled web admin still asks for an open port")
	}
	time.Sleep(100 * time.Millisecond)
	if _, err := client.Get(fmt.Sprintf("https://127.0.0.1:%d/", port)); err == nil {
		t.Fatal("the listener is still open")
	}
	var f map[string]any
	data, _ := os.ReadFile(m.path())
	json.Unmarshal(data, &f)
	if f["enabled"] != false {
		t.Fatalf("state = %s", data)
	}
}

func TestConfigureValidation(t *testing.T) {
	m, _ := newManager(t)
	low := 80
	if _, err := m.Configure(context.Background(), nil, &low, nil); err == nil {
		t.Fatal("a privileged port was accepted")
	}
	bad := "everyone"
	if _, err := m.Configure(context.Background(), nil, nil, &bad); err == nil {
		t.Fatal("a bad source was accepted")
	}
}
