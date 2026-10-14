// Package webadmin 는 다른 컴퓨터의 브라우저에서 이 컴퓨터를 관리하는 웹 페이지를 제공한다. 기본은 꺼져 있다.
//
// 데몬 안의 별도 HTTPS 리스너(자체 서명 인증서)로 돌며 새 프로세스를 만들지 않는다. 인증은 이 기능 전용 비밀번호
// (PBKDF2-HMAC-SHA256)와 세션 쿠키이고, 틀린 시도는 주소별로 점점 길게 막는다. 관리 API 는 소켓 API 와 같은 핸들러를
// 쓰되 허용 목록의 요청만 통과시킨다. 계정, SSH 키, 방화벽, VPN, 웹 관리 자신은 이 길로 바꿀 수 없다(비밀번호를 알아낸
// 공격자의 피해를 서버 운영으로 한정한다).
package webadmin

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"embed"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"errors"
	"fmt"
	"io/fs"
	"math/big"
	"net"
	"net/http"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

//go:embed static/*
var staticFS embed.FS

// 기본값과 한도다.
const (
	DefaultPort    = 8443
	SourceLAN      = "lan"
	SourceAny      = "any"
	pbkdf2Iter     = 600000
	minPasswordLen = 10
	maxPasswordLen = 128
	sessionIdle    = 30 * time.Minute
	sessionMax     = 8 * time.Hour
	maxSessions    = 32
	maxBody        = 1 << 20
	cookieName     = "opr_session"
	csrfHeader     = "X-Opereta-Admin"
	lockThreshold  = 5
	maxLock        = 15 * time.Minute
	loginFailDelay = 400 * time.Millisecond
)

// 오류 코드다.
const (
	CodeInvalidInput = "invalid_input"
	CodeNoPassword   = "password_required"
	CodeFailed       = "webadmin_failed"
)

// Error 는 코드를 가진 오류다.
type Error struct{ Code, Message string }

func (e *Error) Error() string { return e.Code + ": " + e.Message }

type password struct {
	Iter int    `json:"iter"`
	Salt string `json:"salt"`
	Hash string `json:"hash"`
}

// state 는 디스크에 저장하는 설정이다. 비밀번호는 해시만 둔다.
type state struct {
	Enabled  bool     `json:"enabled"`
	Port     int      `json:"port"`
	Source   string   `json:"source"`
	Password password `json:"password"`
}

// Status 는 API 가 돌려주는 상태다.
type Status struct {
	Enabled         bool     `json:"enabled"`
	Running         bool     `json:"running"`
	Port            int      `json:"port"`
	Source          string   `json:"source"`
	PasswordSet     bool     `json:"password_set"`
	CertFingerprint string   `json:"cert_fingerprint,omitempty"`
	URLs            []string `json:"urls"`
	Error           string   `json:"error,omitempty"`
}

// Manager 는 설정, 리스너, 세션을 관리한다.
type Manager struct {
	// API 는 소켓 API 와 같은 핸들러다.
	API http.Handler
	// StateDir 는 설정과 인증서를 둔다(/var/lib/opereta).
	StateDir string
	// Reapply 는 방화벽을 다시 적용한다.
	Reapply func(ctx context.Context) error
	// Addresses 는 인증서에 넣고 URL 로 보일 이 컴퓨터의 주소(호스트 이름, IP)다.
	Addresses func() []string
	Logf      func(format string, args ...any)
	Now       func() time.Time
	// Iterations 는 비밀번호 해시의 PBKDF2 반복 횟수다. 0 이면 600000 이다(시험만 줄인다).
	Iterations int
	// FailDelay 는 틀린 비밀번호에 답하기 전에 기다리는 시간이다. 0 이면 400 ms 다.
	FailDelay time.Duration

	mu       sync.Mutex
	st       state
	loaded   bool
	srv      *http.Server
	runErr   string
	sessions map[string]*session
	fails    map[string]*failure
}

type session struct {
	created, last time.Time
}

type failure struct {
	count       int
	lockedUntil time.Time
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) logf(f string, a ...any) {
	if m.Logf != nil {
		m.Logf(f, a...)
	}
}

func (m *Manager) path() string { return filepath.Join(m.StateDir, "webadmin.json") }

func (m *Manager) ensureLoaded() {
	if m.loaded {
		return
	}
	m.loaded = true
	m.st = state{Port: DefaultPort, Source: SourceLAN}
	if data, err := os.ReadFile(m.path()); err == nil {
		var s state
		if json.Unmarshal(data, &s) == nil {
			if s.Port < 1024 || s.Port > 65535 {
				s.Port = DefaultPort
			}
			if !validSource(s.Source) {
				s.Source = SourceLAN
			}
			m.st = s
		}
	}
	if m.sessions == nil {
		m.sessions = map[string]*session{}
		m.fails = map[string]*failure{}
	}
}

func (m *Manager) saveLocked() error {
	data, _ := json.MarshalIndent(m.st, "", "  ")
	if err := os.MkdirAll(m.StateDir, 0o750); err != nil {
		return err
	}
	tmp := m.path() + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, m.path())
}

func validSource(s string) bool {
	if s == SourceLAN || s == SourceAny {
		return true
	}
	p, err := netip.ParsePrefix(s)
	return err == nil && p.Bits() > 0
}

// ---- 비밀번호 ----

// ValidPassword 는 비밀번호 규칙을 본다: 10-128 자, 제어 문자 없음.
func ValidPassword(p string) error {
	n := len([]rune(p))
	if n < minPasswordLen {
		return fmt.Errorf("the password must be at least %d characters", minPasswordLen)
	}
	if len(p) > maxPasswordLen*4 || n > maxPasswordLen {
		return fmt.Errorf("the password must be at most %d characters", maxPasswordLen)
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f {
			return errors.New("the password must not contain control characters")
		}
	}
	return nil
}

func hashPassword(p string, salt []byte, iter int) (string, error) {
	k, err := pbkdf2.Key(sha256.New, p, salt, iter, 32)
	if err != nil {
		return "", err
	}
	return base64.StdEncoding.EncodeToString(k), nil
}

// SetPassword 는 웹 관리 비밀번호를 정한다.
func (m *Manager) SetPassword(p string) error {
	if err := ValidPassword(p); err != nil {
		return &Error{CodeInvalidInput, err.Error()}
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return &Error{CodeFailed, "no random numbers"}
	}
	iter := m.Iterations
	if iter <= 0 {
		iter = pbkdf2Iter
	}
	h, err := hashPassword(p, salt, iter)
	if err != nil {
		return &Error{CodeFailed, err.Error()}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	m.st.Password = password{Iter: iter, Salt: base64.StdEncoding.EncodeToString(salt), Hash: h}
	m.sessions = map[string]*session{} // 비밀번호를 바꾸면 모든 세션을 끝낸다.
	if err := m.saveLocked(); err != nil {
		return &Error{CodeFailed, fmt.Sprintf("saving the setting: %v", err)}
	}
	return nil
}

func (m *Manager) checkPassword(p string) bool {
	m.mu.Lock()
	pw := m.st.Password
	m.mu.Unlock()
	if pw.Hash == "" || len(p) > maxPasswordLen*4 {
		return false
	}
	salt, err := base64.StdEncoding.DecodeString(pw.Salt)
	if err != nil || pw.Iter < 1 {
		return false
	}
	got, err := hashPassword(p, salt, pw.Iter)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(got), []byte(pw.Hash)) == 1
}

// ---- 인증서 ----

func (m *Manager) certPaths() (string, string) {
	return filepath.Join(m.StateDir, "webadmin-cert.pem"), filepath.Join(m.StateDir, "webadmin-key.pem")
}

// ensureCert 는 인증서가 없거나 주소가 달라졌으면 새로 만든다. 지문을 돌려준다.
func (m *Manager) ensureCert() (tls.Certificate, string, error) {
	certPath, keyPath := m.certPaths()
	names := m.hostNames()
	if c, err := tls.LoadX509KeyPair(certPath, keyPath); err == nil && len(c.Certificate) > 0 {
		if leaf, perr := x509.ParseCertificate(c.Certificate[0]); perr == nil && covers(leaf, names) && m.now().Before(leaf.NotAfter.Add(-30*24*time.Hour)) {
			return c, fingerprint(c.Certificate[0]), nil
		}
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
	cn := "opereta"
	if len(names.dns) > 0 {
		cn = names.dns[0]
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial, Subject: pkix.Name{CommonName: cn, Organization: []string{"Opereta"}},
		NotBefore: m.now().Add(-time.Hour), NotAfter: m.now().Add(10 * 365 * 24 * time.Hour),
		KeyUsage: x509.KeyUsageDigitalSignature, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames: names.dns, IPAddresses: names.ips, BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.MkdirAll(m.StateDir, 0o750); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(keyPath, pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err := os.WriteFile(certPath, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return tls.Certificate{}, "", err
	}
	c, err := tls.LoadX509KeyPair(certPath, keyPath)
	return c, fingerprint(der), err
}

type nameSet struct {
	dns []string
	ips []net.IP
}

func (m *Manager) hostNames() nameSet {
	set := nameSet{dns: []string{"localhost"}, ips: []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}}
	seen := map[string]bool{"localhost": true}
	if m.Addresses != nil {
		for _, a := range m.Addresses() {
			if ip := net.ParseIP(a); ip != nil {
				set.ips = append(set.ips, ip)
			} else if a != "" && !seen[a] {
				seen[a] = true
				set.dns = append(set.dns, a)
			}
		}
	}
	return set
}

func covers(c *x509.Certificate, n nameSet) bool {
	have := map[string]bool{}
	for _, d := range c.DNSNames {
		have[d] = true
	}
	for _, ip := range c.IPAddresses {
		have[ip.String()] = true
	}
	for _, d := range n.dns {
		if !have[d] {
			return false
		}
	}
	for _, ip := range n.ips {
		if !have[ip.String()] {
			return false
		}
	}
	return true
}

func fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(parts, ":")
}

// ---- 수명 주기 ----

// Opens 는 켜져 있을 때 방화벽이 열 포트와 허용 범위다.
func (m *Manager) Opens() (port int, source string, ok bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	if !m.st.Enabled {
		return 0, "", false
	}
	return m.st.Port, m.st.Source, true
}

// Status 는 지금 상태다.
func (m *Manager) Status() Status {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ensureLoaded()
	st := Status{Enabled: m.st.Enabled, Running: m.srv != nil, Port: m.st.Port, Source: m.st.Source,
		PasswordSet: m.st.Password.Hash != "", URLs: []string{}, Error: m.runErr}
	if m.st.Enabled {
		if c, _, err := m.certFingerprintOnly(); err == nil {
			st.CertFingerprint = c
		}
		if m.Addresses != nil {
			for _, a := range m.Addresses() {
				h := a
				if strings.Contains(a, ":") {
					h = "[" + a + "]"
				}
				st.URLs = append(st.URLs, "https://"+h+":"+strconv.Itoa(m.st.Port)+"/")
			}
		}
	}
	return st
}

func (m *Manager) certFingerprintOnly() (string, bool, error) {
	certPath, _ := m.certPaths()
	data, err := os.ReadFile(certPath)
	if err != nil {
		return "", false, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return "", false, errors.New("no certificate")
	}
	return fingerprint(block.Bytes), true, nil
}

// Configure 는 켜고 끄고 포트와 허용 범위를 바꾼다. 켜려면 비밀번호가 있어야 한다.
func (m *Manager) Configure(ctx context.Context, enabled *bool, port *int, source *string) (Status, error) {
	m.mu.Lock()
	m.ensureLoaded()
	prev := m.st
	next := m.st
	if port != nil {
		if *port < 1024 || *port > 65535 {
			m.mu.Unlock()
			return Status{}, &Error{CodeInvalidInput, "the port must be 1024-65535"}
		}
		next.Port = *port
	}
	if source != nil {
		if !validSource(*source) {
			m.mu.Unlock()
			return Status{}, &Error{CodeInvalidInput, "the source must be lan, any, or an address range such as 192.168.1.0/24"}
		}
		next.Source = *source
	}
	if enabled != nil {
		next.Enabled = *enabled
	}
	if next.Enabled && next.Password.Hash == "" {
		m.mu.Unlock()
		return Status{}, &Error{CodeNoPassword, "set the web admin password first"}
	}
	m.st = next
	if err := m.saveLocked(); err != nil {
		m.st = prev
		m.mu.Unlock()
		return Status{}, &Error{CodeFailed, fmt.Sprintf("saving the setting: %v", err)}
	}
	m.mu.Unlock()

	if err := m.restart(); err != nil {
		m.mu.Lock()
		m.st = prev
		_ = m.saveLocked()
		m.mu.Unlock()
		_ = m.restart()
		return Status{}, &Error{CodeFailed, err.Error()}
	}
	if m.Reapply != nil {
		if err := m.Reapply(ctx); err != nil {
			return m.Status(), &Error{CodeFailed, fmt.Sprintf("the firewall was not updated: %v", err)}
		}
	}
	return m.Status(), nil
}

// Start 는 데몬이 시작될 때 설정이 켜져 있으면 리스너를 연다.
func (m *Manager) Start() {
	m.mu.Lock()
	m.ensureLoaded()
	enabled := m.st.Enabled
	m.mu.Unlock()
	if enabled {
		if err := m.restart(); err != nil {
			m.logf("web admin: %v", err)
		}
	}
}

// Close 는 리스너를 닫는다.
func (m *Manager) Close() {
	m.mu.Lock()
	srv := m.srv
	m.srv = nil
	m.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
	}
}

// restart 는 설정에 맞게 리스너를 닫고 다시 연다.
func (m *Manager) restart() error {
	m.Close()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.runErr = ""
	m.sessions = map[string]*session{}
	if !m.st.Enabled {
		return nil
	}
	cert, _, err := m.ensureCert()
	if err != nil {
		m.runErr = "certificate: " + err.Error()
		return fmt.Errorf("the certificate could not be made: %w", err)
	}
	ln, err := net.Listen("tcp", ":"+strconv.Itoa(m.st.Port))
	if err != nil {
		m.runErr = "listen: " + err.Error()
		return fmt.Errorf("cannot listen on port %d: %w", m.st.Port, err)
	}
	srv := &http.Server{
		Handler:           m.handler(),
		TLSConfig:         &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12},
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      3 * time.Minute,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    32 << 10,
	}
	m.srv = srv
	go func() {
		if err := srv.ServeTLS(ln, "", ""); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.logf("web admin: serve: %v", err)
		}
	}()
	return nil
}

// ---- HTTP ----

type rule struct {
	method string
	re     *regexp.Regexp
}

// allowed 는 웹으로 통과시킬 소켓 API 요청이다. 목록에 없는 것은 막는다.
var allowed = []rule{
	{"GET", regexp.MustCompile(`^/v1/(status|models|models/search|models/remote|jobs|server/plan|server/logs|server/stats|config|apikeys|mode|system/ssh|system/power|gateway|connection|recommendations|network|network/config|remote-desktop|vpn|services|resources|ssh/advanced|ssh/log|firewall|update|report)$`)},
	{"GET", regexp.MustCompile(`^/v1/(jobs/[A-Za-z0-9_-]+|services/[A-Za-z0-9@._-]+\.service/logs)$`)},
	{"POST", regexp.MustCompile(`^/v1/(server/start|server/stop|server/chat|apikeys|models/pull|network/diagnose)$`)},
	{"POST", regexp.MustCompile(`^/v1/services/[A-Za-z0-9@._-]+\.service/(start|stop|restart)$`)},
	{"PUT", regexp.MustCompile(`^/v1/(models/default|config|system/power)$`)},
	{"DELETE", regexp.MustCompile(`^/v1/(apikeys|models|jobs)/[A-Za-z0-9._-]+$`)},
}

// Allowed 는 요청이 웹 관리에서 허용되는지 본다.
func Allowed(method, path string) bool {
	for _, r := range allowed {
		if r.method == method && r.re.MatchString(path) {
			return true
		}
	}
	return false
}

func (m *Manager) handler() http.Handler {
	mux := http.NewServeMux()
	static, _ := fs.Sub(staticFS, "static")
	files := http.FileServer(http.FS(static))
	mux.Handle("/", files)
	mux.HandleFunc("POST /auth/login", m.login)
	mux.HandleFunc("POST /auth/logout", m.logout)
	mux.HandleFunc("GET /auth/state", m.authState)
	mux.HandleFunc("/v1/", m.proxy)
	return securityHeaders(mux)
}

func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("Content-Security-Policy", "default-src 'self'; style-src 'self'; script-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		h.Set("X-Frame-Options", "DENY")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Strict-Transport-Security", "max-age=31536000")
		h.Set("Cache-Control", "no-store")
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": msg}})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// sameOrigin 은 Origin 헤더가 있으면 요청 호스트와 같은지 본다.
func sameOrigin(r *http.Request) bool {
	o := r.Header.Get("Origin")
	if o == "" {
		return true
	}
	return strings.HasSuffix(o, "://"+r.Host)
}

func (m *Manager) login(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	if !sameOrigin(r) || r.Header.Get(csrfHeader) == "" {
		writeErr(w, http.StatusForbidden, "forbidden", "the request was not made by the admin page")
		return
	}
	m.mu.Lock()
	m.ensureLoaded()
	f := m.fails[ip]
	if f != nil && m.now().Before(f.lockedUntil) {
		wait := int(f.lockedUntil.Sub(m.now()).Seconds()) + 1
		m.mu.Unlock()
		w.Header().Set("Retry-After", strconv.Itoa(wait))
		writeErr(w, http.StatusTooManyRequests, "locked", fmt.Sprintf("too many wrong passwords; try again in %d seconds", wait))
		return
	}
	m.mu.Unlock()

	var body struct {
		Password string `json:"password"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 4096)
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid_json", "a JSON body with a password is required")
		return
	}
	if !m.checkPassword(body.Password) {
		m.mu.Lock()
		f := m.fails[ip]
		if f == nil {
			f = &failure{}
			m.fails[ip] = f
		}
		f.count++
		if f.count >= lockThreshold {
			d := 30 * time.Second << uint(f.count-lockThreshold)
			if d > maxLock || d <= 0 {
				d = maxLock
			}
			f.lockedUntil = m.now().Add(d)
		}
		m.mu.Unlock()
		delay := m.FailDelay
		if delay == 0 {
			delay = loginFailDelay
		}
		time.Sleep(delay)
		m.logf("web admin: wrong password from %s", ip)
		writeErr(w, http.StatusUnauthorized, "invalid_password", "the password is not correct")
		return
	}
	tokenBytes := make([]byte, 32)
	if _, err := rand.Read(tokenBytes); err != nil {
		writeErr(w, http.StatusInternalServerError, "internal", "no random numbers")
		return
	}
	token := hex.EncodeToString(tokenBytes)
	m.mu.Lock()
	delete(m.fails, ip)
	now := m.now()
	for k, s := range m.sessions {
		if now.Sub(s.last) > sessionIdle || now.Sub(s.created) > sessionMax {
			delete(m.sessions, k)
		}
	}
	if len(m.sessions) >= maxSessions {
		// 가장 오래된 세션을 지운다.
		var oldest string
		var ot time.Time
		for k, s := range m.sessions {
			if oldest == "" || s.last.Before(ot) {
				oldest, ot = k, s.last
			}
		}
		delete(m.sessions, oldest)
	}
	m.sessions[token] = &session{created: now, last: now}
	m.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: token, Path: "/", HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode, MaxAge: int(sessionMax.Seconds())})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": true})
}

func (m *Manager) validSession(r *http.Request) bool {
	c, err := r.Cookie(cookieName)
	if err != nil || len(c.Value) != 64 {
		return false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[c.Value]
	if s == nil {
		return false
	}
	now := m.now()
	if now.Sub(s.last) > sessionIdle || now.Sub(s.created) > sessionMax {
		delete(m.sessions, c.Value)
		return false
	}
	s.last = now
	return true
}

func (m *Manager) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		m.mu.Lock()
		delete(m.sessions, c.Value)
		m.mu.Unlock()
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode})
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": false})
}

func (m *Manager) authState(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"authenticated": m.validSession(r)})
}

// proxy 는 로그인한 세션의 허용된 요청만 소켓 API 핸들러로 넘긴다.
func (m *Manager) proxy(w http.ResponseWriter, r *http.Request) {
	if !m.validSession(r) {
		writeErr(w, http.StatusUnauthorized, "login_required", "log in first")
		return
	}
	if r.Method != http.MethodGet && (r.Header.Get(csrfHeader) == "" || !sameOrigin(r)) {
		writeErr(w, http.StatusForbidden, "forbidden", "the request was not made by the admin page")
		return
	}
	if !Allowed(r.Method, r.URL.Path) {
		writeErr(w, http.StatusForbidden, "not_allowed", "this action is not available from the web page; use the Control Center or the opereta command on the computer")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBody)
	m.API.ServeHTTP(w, r)
}
