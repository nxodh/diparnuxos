// Package gateway 는 모델 서버(llama-server) 앞에 서는 역방향 프록시다.
//
// 공개 포트를 데몬이 받아 API 키를 확인하고 요청을 127.0.0.1 의 내부 포트로 넘긴다. llama-server 에 키 파일을 주지
// 않으므로 키를 만들거나 지워도 서버를 다시 시작하지 않아도 되고, 키별 사용량(요청 수, 오류 수, 마지막 사용, 토큰 수)과
// 최근 요청 기록을 남길 수 있다. 응답은 버퍼링하지 않고 그대로 흘려보낸다(스트리밍 SSE 포함).
//
// 키 평문은 저장하지 않는다. 요청의 키를 SHA-256 으로 바꿔 키 목록의 해시와 상수 시간 비교한다. 요청 본문과 응답 본문은
// 기록하지 않고, 응답 끝부분에서 토큰 수(usage)만 읽는다.
package gateway

import (
	"context"
	"crypto/sha256"
	"crypto/subtle"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/apikeys"
)

// KeySource 는 키 목록이다. *apikeys.Store 가 만족한다.
type KeySource interface {
	List() ([]apikeys.Key, error)
}

const (
	keyCacheTTL  = 2 * time.Second
	recentLimit  = 100
	tailBytes    = 8 << 10
	usageFileMax = 1 << 20
)

// KeyUsage 는 키 하나의 사용량이다.
type KeyUsage struct {
	ID               string    `json:"id"`
	Label            string    `json:"label"`
	Prefix           string    `json:"prefix"`
	Requests         int64     `json:"requests"`
	Errors           int64     `json:"errors"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	LastUsed         time.Time `json:"last_used"`
}

// Recent 는 최근 요청 하나의 기록이다. 본문은 담지 않는다.
type Recent struct {
	Time       time.Time `json:"time"`
	KeyID      string    `json:"key_id,omitempty"`
	KeyLabel   string    `json:"key_label,omitempty"`
	Method     string    `json:"method"`
	Path       string    `json:"path"`
	Status     int       `json:"status"`
	DurationMS int64     `json:"duration_ms"`
	Remote     string    `json:"remote"`
}

// Snapshot 은 게이트웨이의 현재 통계다.
type Snapshot struct {
	InFlight     int64      `json:"in_flight"`
	Requests     int64      `json:"requests"`
	Errors       int64      `json:"errors"`
	Unauthorized int64      `json:"unauthorized"`
	Since        time.Time  `json:"since"`
	Keys         []KeyUsage `json:"keys"`
	Recent       []Recent   `json:"recent"`
}

type counters struct {
	Requests         int64     `json:"requests"`
	Errors           int64     `json:"errors"`
	PromptTokens     int64     `json:"prompt_tokens"`
	CompletionTokens int64     `json:"completion_tokens"`
	LastUsed         time.Time `json:"last_used"`
}

// Gateway 는 인증과 통계를 맡는다. 제로 값에 Keys 를 채워 쓴다.
type Gateway struct {
	// Keys 는 키 목록이다.
	Keys KeySource
	// LANExposed 는 LAN 공개 여부를 돌려준다. 공개이면 키가 하나도 없을 때 모든 요청을 거절한다.
	LANExposed func() bool
	// UsagePath 가 비어 있지 않으면 사용량을 이 파일에 저장하고 읽는다(0600).
	UsagePath string
	// MaxInFlight 가 0 보다 크면 동시에 처리하는 요청 수를 제한한다. 넘으면 429 를 돌려준다.
	MaxInFlight int
	// Now 는 시계다. nil 이면 time.Now 다.
	Now func() time.Time
	// LocalNoKey 가 참을 돌려주면 이 컴퓨터에서 온 요청(루프백, 이 컴퓨터의 주소)은 키 없이 통과한다. 내부 포트가
	// 이미 로컬 프로세스에 열려 있으므로 로컬 요청에 키를 요구해도 보안 이득이 없고, 같은 컴퓨터의 클라이언트
	// (자동 감지를 하는 코딩 에이전트 등)가 키 없이 서버를 찾지 못하게 막을 뿐이다. nil 이면 거짓이다.
	LocalNoKey func() bool
	// Version 은 모든 응답의 X-Opereta 헤더와 /opereta/info 에 담는다.
	Version string
	// Info 는 /opereta/info 의 본문이다(키 없이 읽힌다. 모델 경로나 설정은 담지 않는다). nil 이면 이 경로를 모델 서버로 넘긴다.
	Info func() map[string]any

	localMu   sync.Mutex
	localIPs  map[string]bool
	localIPAt time.Time

	mu       sync.Mutex
	usage    map[string]*counters
	loaded   bool
	since    time.Time
	recent   []Recent
	keyCache []apikeys.Key
	keyAt    time.Time

	inflight     atomic.Int64
	requests     atomic.Int64
	errCount     atomic.Int64
	unauthorized atomic.Int64
}

func (g *Gateway) now() time.Time {
	if g.Now != nil {
		return g.Now()
	}
	return time.Now()
}

func (g *Gateway) ensureLoaded() {
	if g.loaded {
		return
	}
	g.loaded = true
	g.usage = map[string]*counters{}
	g.since = g.now()
	if g.UsagePath == "" {
		return
	}
	f, err := os.Open(g.UsagePath)
	if err != nil {
		return
	}
	defer f.Close()
	var doc struct {
		Keys map[string]*counters `json:"keys"`
	}
	if err := json.NewDecoder(io.LimitReader(f, usageFileMax)).Decode(&doc); err == nil && doc.Keys != nil {
		g.usage = doc.Keys
	}
}

// keys 는 키 목록을 짧은 시간 캐시해서 돌려준다.
func (g *Gateway) keys() []apikeys.Key {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.keyCache != nil && g.now().Sub(g.keyAt) < keyCacheTTL {
		return g.keyCache
	}
	list, err := g.Keys.List()
	if err != nil {
		// 읽을 수 없으면 가장 최근의 정상 목록을 쓴다. 처음부터 읽지 못했다면 비어 있다(공개 상태면 모두 거절된다).
		return g.keyCache
	}
	g.keyCache = list
	g.keyAt = g.now()
	return g.keyCache
}

// match 는 요청이 낸 키가 목록의 어느 키인지 찾는다.
func match(keys []apikeys.Key, presented string) (apikeys.Key, bool) {
	sum := sha256.Sum256([]byte(presented))
	want := []byte(hex.EncodeToString(sum[:]))
	var found apikeys.Key
	ok := false
	for _, k := range keys {
		// 모든 키를 같은 시간에 비교한다(조기 종료하지 않는다).
		if subtle.ConstantTimeCompare([]byte(strings.ToLower(k.SHA256)), want) == 1 {
			found, ok = k, true
		}
	}
	return found, ok
}

// presentedKey 는 Authorization: Bearer 또는 X-Api-Key 헤더의 키를 돌려준다.
func presentedKey(r *http.Request) string {
	if v := r.Header.Get("Authorization"); len(v) > 7 && strings.EqualFold(v[:7], "bearer ") {
		return strings.TrimSpace(v[7:])
	}
	return strings.TrimSpace(r.Header.Get("X-Api-Key"))
}

type statusRecorder struct {
	http.ResponseWriter
	status int
	bytes  int64
	tail   []byte
}

func (s *statusRecorder) WriteHeader(code int) {
	if s.status == 0 {
		s.status = code
	}
	s.ResponseWriter.WriteHeader(code)
}

func (s *statusRecorder) Write(p []byte) (int, error) {
	if s.status == 0 {
		s.status = http.StatusOK
	}
	n, err := s.ResponseWriter.Write(p)
	s.bytes += int64(n)
	// 끝부분 tailBytes 만 남긴다. 토큰 수(usage)는 응답 끝에 있다.
	s.tail = append(s.tail, p[:n]...)
	if len(s.tail) > 2*tailBytes {
		s.tail = append([]byte(nil), s.tail[len(s.tail)-tailBytes:]...)
	}
	return n, err
}

// Flush 는 스트리밍 응답을 즉시 내보낸다.
func (s *statusRecorder) Flush() {
	if f, ok := s.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

var (
	reCompletion = regexp.MustCompile(`"completion_tokens"\s*:\s*(\d+)`)
	rePrompt     = regexp.MustCompile(`"prompt_tokens"\s*:\s*(\d+)`)
)

func lastInt(re *regexp.Regexp, b []byte) int64 {
	all := re.FindAllSubmatch(b, -1)
	if len(all) == 0 {
		return 0
	}
	v, _ := strconv.ParseInt(string(all[len(all)-1][1]), 10, 64)
	return v
}

// Handler 는 backend 로 넘기는 HTTP 핸들러를 만든다.
func (g *Gateway) Handler(backend *url.URL) http.Handler {
	rp := httputil.NewSingleHostReverseProxy(backend)
	rp.FlushInterval = -1 // 스트리밍 응답을 쌓지 않는다.
	rp.ErrorHandler = func(w http.ResponseWriter, r *http.Request, err error) {
		writeJSONError(w, http.StatusBadGateway, "model_server_unavailable", "the model server did not answer; check that it is running")
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		g.mu.Lock()
		g.ensureLoaded()
		g.mu.Unlock()
		start := g.now()
		if g.Version != "" {
			w.Header().Set("X-Opereta", g.Version)
		}
		if g.Info != nil && r.Method == http.MethodGet && r.URL.Path == "/opereta/info" {
			writeJSON(w, http.StatusOK, g.Info())
			return
		}

		public := r.URL.Path == "/health" || r.URL.Path == "/v1/health"
		var key apikeys.Key
		authed := false
		if !public {
			keys := g.keys()
			lan := g.LANExposed != nil && g.LANExposed()
			switch {
			case g.LocalNoKey != nil && g.LocalNoKey() && g.isLocal(r):
				// 로컬 요청: 키를 내면 그 키로 집계하고, 없거나 틀려도 통과한다.
				if k, ok := match(keys, presentedKey(r)); ok && presentedKey(r) != "" {
					key, authed = k, true
				}
			case len(keys) == 0 && lan:
				g.deny(w, r, start, "api_key_required", "LAN exposure needs at least one API key")
				return
			case len(keys) > 0:
				k, ok := match(keys, presentedKey(r))
				if !ok {
					w.Header().Set("WWW-Authenticate", `Bearer realm="opereta"`)
					g.deny(w, r, start, "invalid_api_key", "a valid API key is required (Authorization: Bearer <key>)")
					return
				}
				key, authed = k, true
			}
		}

		if g.MaxInFlight > 0 && g.inflight.Load() >= int64(g.MaxInFlight) {
			w.Header().Set("Retry-After", "2")
			writeJSONError(w, http.StatusTooManyRequests, "too_many_requests", "the server is busy; retry later")
			return
		}
		g.inflight.Add(1)
		defer g.inflight.Add(-1)

		// 키는 이 프록시에서 끝낸다. 모델 서버로 넘기지 않는다.
		r.Header.Del("Authorization")
		r.Header.Del("X-Api-Key")

		rec := &statusRecorder{ResponseWriter: w}
		rp.ServeHTTP(rec, r)
		if rec.status == 0 {
			rec.status = http.StatusOK
		}
		g.record(r, rec, start, key, authed)
	})
}

func (g *Gateway) deny(w http.ResponseWriter, r *http.Request, start time.Time, code, msg string) {
	g.unauthorized.Add(1)
	g.addRecent(Recent{Time: start, Method: r.Method, Path: r.URL.Path, Status: http.StatusUnauthorized,
		DurationMS: g.now().Sub(start).Milliseconds(), Remote: remoteHost(r)})
	writeJSONError(w, http.StatusUnauthorized, code, msg)
}

func (g *Gateway) record(r *http.Request, rec *statusRecorder, start time.Time, key apikeys.Key, authed bool) {
	g.requests.Add(1)
	failed := rec.status >= 500 || rec.status == http.StatusTooManyRequests
	if failed {
		g.errCount.Add(1)
	}
	entry := Recent{Time: start, Method: r.Method, Path: r.URL.Path, Status: rec.status,
		DurationMS: g.now().Sub(start).Milliseconds(), Remote: remoteHost(r)}
	if authed {
		entry.KeyID, entry.KeyLabel = key.ID, key.Label
	}
	g.addRecent(entry)
	if !authed {
		return
	}
	prompt := lastInt(rePrompt, rec.tail)
	completion := lastInt(reCompletion, rec.tail)
	g.mu.Lock()
	c := g.usage[key.ID]
	if c == nil {
		c = &counters{}
		g.usage[key.ID] = c
	}
	c.Requests++
	if failed {
		c.Errors++
	}
	c.PromptTokens += prompt
	c.CompletionTokens += completion
	c.LastUsed = g.now()
	g.mu.Unlock()
}

func (g *Gateway) addRecent(e Recent) {
	g.mu.Lock()
	g.recent = append(g.recent, e)
	if len(g.recent) > recentLimit {
		g.recent = append([]Recent(nil), g.recent[len(g.recent)-recentLimit:]...)
	}
	g.mu.Unlock()
}

// isLocal 은 요청이 이 컴퓨터에서 왔는지 본다: 루프백이거나 이 컴퓨터의 인터페이스 주소다.
func (g *Gateway) isLocal(r *http.Request) bool {
	ip := net.ParseIP(remoteHost(r))
	if ip == nil {
		return false
	}
	if ip.IsLoopback() {
		return true
	}
	g.localMu.Lock()
	defer g.localMu.Unlock()
	if g.localIPs == nil || g.now().Sub(g.localIPAt) > 10*time.Second {
		set := map[string]bool{}
		if addrs, err := net.InterfaceAddrs(); err == nil {
			for _, a := range addrs {
				if n, ok := a.(*net.IPNet); ok {
					set[n.IP.String()] = true
				}
			}
		}
		g.localIPs, g.localIPAt = set, g.now()
	}
	return g.localIPs[ip.String()]
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func remoteHost(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func writeJSONError(w http.ResponseWriter, status int, code, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": code, "message": msg, "type": code}})
}

// Snapshot 은 통계를 복사해 돌려준다. 지워진 키의 통계는 빼고, 쓰지 않은 키도 목록에 넣는다.
func (g *Gateway) Snapshot() Snapshot {
	g.mu.Lock()
	g.ensureLoaded()
	g.mu.Unlock()
	keys := g.keys()
	g.mu.Lock()
	defer g.mu.Unlock()
	out := Snapshot{
		InFlight:     g.inflight.Load(),
		Requests:     g.requests.Load(),
		Errors:       g.errCount.Load(),
		Unauthorized: g.unauthorized.Load(),
		Since:        g.since,
		Keys:         []KeyUsage{},
		Recent:       append([]Recent{}, g.recent...),
	}
	for _, k := range keys {
		u := KeyUsage{ID: k.ID, Label: k.Label, Prefix: k.Prefix}
		if c := g.usage[k.ID]; c != nil {
			u.Requests, u.Errors, u.PromptTokens, u.CompletionTokens, u.LastUsed = c.Requests, c.Errors, c.PromptTokens, c.CompletionTokens, c.LastUsed
		}
		out.Keys = append(out.Keys, u)
	}
	sort.Slice(out.Keys, func(i, j int) bool { return out.Keys[i].Label < out.Keys[j].Label })
	return out
}

// Flush 는 사용량을 파일에 쓴다. 지워진 키의 항목은 정리한다.
func (g *Gateway) Flush() error {
	if g.UsagePath == "" {
		return nil
	}
	keys := g.keys()
	g.mu.Lock()
	g.ensureLoaded()
	live := map[string]bool{}
	for _, k := range keys {
		live[k.ID] = true
	}
	doc := struct {
		Keys map[string]*counters `json:"keys"`
	}{Keys: map[string]*counters{}}
	for id, c := range g.usage {
		if live[id] || len(keys) == 0 {
			cp := *c
			doc.Keys[id] = &cp
		}
	}
	g.mu.Unlock()
	data, err := json.Marshal(doc)
	if err != nil {
		return err
	}
	dir := filepath.Dir(g.UsagePath)
	tmp, err := os.CreateTemp(dir, ".usage-*")
	if err != nil {
		return fmt.Errorf("gateway: create temp usage file: %w", err)
	}
	defer os.Remove(tmp.Name())
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), g.UsagePath)
}

// ---- 매니저 ----

// ManagerState 는 리스너 상태다.
type ManagerState struct {
	Listening   bool   `json:"listening"`
	Addr        string `json:"addr"`
	BackendPort int    `json:"backend_port"`
	Error       string `json:"error,omitempty"`
}

// Manager 는 모델 서버가 도는 동안만 공개 포트에서 듣는다. 서버 수명 주기 코드와 얽히지 않도록 상태를 주기적으로 본다.
type Manager struct {
	G *Gateway
	// Wanted 는 게이트웨이가 켜져 있어야 하는지와 공개 주소(host:port)를 돌려준다.
	Wanted func() (enabled bool, listenAddr string, publicPort int)
	// Backend 는 모델 서버가 도는 중이면 그 내부 포트를 돌려준다. 돌지 않으면 ok 가 거짓이다.
	Backend func() (port int, ok bool)
	// Interval 은 확인 주기다. 0 이면 1초다.
	Interval time.Duration
	Logf     func(format string, args ...any)

	// WantedTLS 는 TLS 리스너가 떠야 하는지와 주소(host:port)를 돌려준다. nil 이면 TLS 를 쓰지 않는다.
	// TLS 리스너는 평문 리스너와 같은 핸들러를 쓴다(키 규칙, 429, /opereta/info 가 같다). 리다이렉트는 하지 않는다.
	WantedTLS func() (enabled bool, listenAddr string)
	// Cert 는 핸드셰이크에 쓸 서버 인증서를 돌려준다(tls.Config.GetCertificate). 갱신된 인증서는 새 연결부터 쓴다.
	Cert func(*tls.ClientHelloInfo) (*tls.Certificate, error)
	// PrepareTLS 는 리스너를 열기 전과 PrepareEvery 마다 인증서를 만들거나 갱신한다(주소가 바뀐 경우 포함).
	PrepareTLS   func() error
	PrepareEvery time.Duration

	mu            sync.Mutex
	srv           *http.Server
	state         ManagerState
	tlsSrv        *http.Server
	tlsState      ManagerState
	tlsPreparedAt time.Time
}

// Snapshot 은 게이트웨이의 통계를 돌려준다.
func (m *Manager) Snapshot() Snapshot { return m.G.Snapshot() }

// State 는 현재 리스너 상태를 돌려준다.
func (m *Manager) State() ManagerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// TLSState 는 TLS 리스너 상태를 돌려준다.
func (m *Manager) TLSState() ManagerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.tlsState
}

// Run 은 ctx 가 끝날 때까지 상태를 맞춘다. 끝나면 리스너를 닫고 사용량을 저장한다.
func (m *Manager) Run(ctx context.Context) {
	interval := m.Interval
	if interval <= 0 {
		interval = time.Second
	}
	t := time.NewTicker(interval)
	defer t.Stop()
	flush := time.NewTicker(time.Minute)
	defer flush.Stop()
	m.reconcile()
	for {
		select {
		case <-ctx.Done():
			m.stop()
			m.stopTLS()
			_ = m.G.Flush()
			return
		case <-t.C:
			m.reconcile()
		case <-flush.C:
			_ = m.G.Flush()
		}
	}
}

func (m *Manager) logf(format string, args ...any) {
	if m.Logf != nil {
		m.Logf(format, args...)
	}
}

func (m *Manager) stop() {
	m.mu.Lock()
	srv := m.srv
	m.srv = nil
	m.state = ManagerState{Error: m.state.Error}
	m.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
}

func (m *Manager) stopTLS() {
	m.mu.Lock()
	srv := m.tlsSrv
	m.tlsSrv = nil
	m.tlsState = ManagerState{Error: m.tlsState.Error}
	m.tlsPreparedAt = time.Time{}
	m.mu.Unlock()
	if srv != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			_ = srv.Close()
		}
	}
}

func (m *Manager) setTLSError(msg string) {
	m.mu.Lock()
	m.tlsState.Error = msg
	m.mu.Unlock()
}

func (m *Manager) setError(msg string) {
	m.mu.Lock()
	m.state.Error = msg
	m.mu.Unlock()
}

func (m *Manager) reconcile() {
	m.reconcilePlain()
	m.reconcileTLS()
}

// reconcileTLS 는 TLS 리스너를 원하는 상태로 맞춘다. 게이트웨이가 꺼져 있거나 모델 서버가 돌지 않으면 닫는다.
func (m *Manager) reconcileTLS() {
	if m.WantedTLS == nil || m.Cert == nil {
		return
	}
	gwOn, _, public := m.Wanted()
	port, running := m.Backend()
	wanted, addr := m.WantedTLS()
	if !gwOn || !wanted || !running || port == public {
		m.stopTLS()
		m.setTLSError("")
		return
	}
	every := m.PrepareEvery
	if every <= 0 {
		every = 30 * time.Second
	}
	m.mu.Lock()
	due := m.tlsPreparedAt.IsZero() || time.Since(m.tlsPreparedAt) >= every
	m.mu.Unlock()
	if due && m.PrepareTLS != nil {
		if err := m.PrepareTLS(); err != nil {
			m.stopTLS()
			m.setTLSError("certificate: " + err.Error())
			return
		}
		m.mu.Lock()
		m.tlsPreparedAt = time.Now()
		m.mu.Unlock()
	}
	m.mu.Lock()
	same := m.tlsSrv != nil && m.tlsState.Addr == addr && m.tlsState.BackendPort == port
	m.mu.Unlock()
	if same {
		return
	}
	m.stopTLS()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.setTLSError(fmt.Sprintf("cannot listen on %s: %v", addr, err))
		return
	}
	backend, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		_ = ln.Close()
		m.setTLSError(err.Error())
		return
	}
	srv := &http.Server{Handler: m.G.Handler(backend), ReadHeaderTimeout: 15 * time.Second}
	tlsLn := tls.NewListener(ln, &tls.Config{GetCertificate: m.Cert, MinVersion: tls.VersionTLS12})
	m.mu.Lock()
	m.tlsSrv = srv
	m.tlsState = ManagerState{Listening: true, Addr: addr, BackendPort: port}
	m.tlsPreparedAt = time.Now()
	m.mu.Unlock()
	m.logf("gateway TLS listening on %s, backend 127.0.0.1:%d", addr, port)
	go func() {
		if err := srv.Serve(tlsLn); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.logf("gateway TLS: serve: %v", err)
		}
	}()
}

func (m *Manager) reconcilePlain() {
	enabled, addr, public := m.Wanted()
	port, running := m.Backend()
	if !enabled || !running {
		m.stop()
		m.setError("")
		return
	}
	if port == public {
		// 서버가 게이트웨이 없이 시작되었다. 같은 포트를 둘이 쓸 수 없으니 다시 시작해야 한다.
		m.stop()
		m.setError("restart the model server to apply the gateway setting")
		return
	}
	m.mu.Lock()
	same := m.srv != nil && m.state.Addr == addr && m.state.BackendPort == port
	m.mu.Unlock()
	if same {
		return
	}
	m.stop()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		m.setError(fmt.Sprintf("cannot listen on %s: %v", addr, err))
		return
	}
	backend, err := url.Parse("http://127.0.0.1:" + strconv.Itoa(port))
	if err != nil {
		_ = ln.Close()
		m.setError(err.Error())
		return
	}
	srv := &http.Server{Handler: m.G.Handler(backend), ReadHeaderTimeout: 15 * time.Second}
	m.mu.Lock()
	m.srv = srv
	m.state = ManagerState{Listening: true, Addr: addr, BackendPort: port}
	m.mu.Unlock()
	m.logf("gateway listening on %s, backend 127.0.0.1:%d", addr, port)
	go func() {
		if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
			m.logf("gateway: serve: %v", err)
		}
	}()
}
