package gateway

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/apikeys"
)

type fakeKeys struct {
	mu   sync.Mutex
	keys []apikeys.Key
}

func (f *fakeKeys) List() ([]apikeys.Key, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]apikeys.Key(nil), f.keys...), nil
}

func (f *fakeKeys) set(keys ...apikeys.Key) {
	f.mu.Lock()
	f.keys = keys
	f.mu.Unlock()
}

func keyFor(id, label, plain string) apikeys.Key {
	sum := sha256.Sum256([]byte(plain))
	return apikeys.Key{ID: id, Label: label, Prefix: plain[:8], SHA256: hex.EncodeToString(sum[:])}
}

// backend 는 받은 요청의 헤더를 돌려주는 모델 서버 흉내다.
func newBackend(t *testing.T) (*httptest.Server, *http.Header) {
	t.Helper()
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
		switch r.URL.Path {
		case "/health":
			io.WriteString(w, `{"status":"ok"}`)
		case "/v1/chat/completions":
			w.Header().Set("Content-Type", "application/json")
			io.WriteString(w, `{"choices":[{"message":{"content":"hi"}}],"usage":{"prompt_tokens":11,"completion_tokens":7,"total_tokens":18}}`)
		case "/boom":
			http.Error(w, "boom", http.StatusInternalServerError)
		default:
			io.WriteString(w, "ok")
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &got
}

func newGateway(t *testing.T, keys *fakeKeys, lan bool) (*Gateway, http.Handler, *http.Header) {
	t.Helper()
	be, got := newBackend(t)
	u, _ := url.Parse(be.URL)
	g := &Gateway{Keys: keys, LANExposed: func() bool { return lan }, UsagePath: filepath.Join(t.TempDir(), "usage.json")}
	return g, g.Handler(u), got
}

func do(h http.Handler, method, path, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	r.RemoteAddr = "192.168.0.7:5555"
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestNoKeysAndLocalOnlyIsOpen(t *testing.T) {
	_, h, _ := newGateway(t, &fakeKeys{}, false)
	if w := do(h, "GET", "/v1/models", ""); w.Code != 200 {
		t.Fatalf("code = %d", w.Code)
	}
}

func TestLANExposedWithoutKeysDeniesEverything(t *testing.T) {
	_, h, _ := newGateway(t, &fakeKeys{}, true)
	if w := do(h, "GET", "/v1/models", ""); w.Code != 401 || !strings.Contains(w.Body.String(), "api_key_required") {
		t.Fatalf("code = %d body = %s", w.Code, w.Body.String())
	}
	// 상태 확인은 키 없이 된다.
	if w := do(h, "GET", "/health", ""); w.Code != 200 {
		t.Fatalf("health = %d", w.Code)
	}
}

func TestKeyRequiredAndAcceptedAndStripped(t *testing.T) {
	keys := &fakeKeys{}
	keys.set(keyFor("k1", "laptop", "dpx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	g, h, got := newGateway(t, keys, true)
	if w := do(h, "GET", "/v1/models", ""); w.Code != 401 {
		t.Fatalf("no key: %d", w.Code)
	}
	if w := do(h, "GET", "/v1/models", "Bearer dpx_wrong"); w.Code != 401 {
		t.Fatalf("wrong key: %d", w.Code)
	}
	w := do(h, "GET", "/v1/models", "Bearer dpx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	if w.Code != 200 || w.Body.String() != "ok" {
		t.Fatalf("valid key: %d %q", w.Code, w.Body.String())
	}
	if got.Get("Authorization") != "" || got.Get("X-Api-Key") != "" {
		t.Fatalf("the key reached the model server: %v", *got)
	}
	// X-Api-Key 헤더로도 된다.
	r := httptest.NewRequest("GET", "/v1/models", nil)
	r.Header.Set("X-Api-Key", "dpx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, r)
	if rec.Code != 200 {
		t.Fatalf("x-api-key: %d", rec.Code)
	}
	if s := g.Snapshot(); s.Unauthorized != 2 {
		t.Fatalf("unauthorized = %d", s.Unauthorized)
	}
}

func TestKeysChangeWithoutRestart(t *testing.T) {
	keys := &fakeKeys{}
	keys.set(keyFor("k1", "a", "dpx_1111111111111111111111111111111111111"))
	g, h, _ := newGateway(t, keys, true)
	now := time.Now()
	g.Now = func() time.Time { return now }
	if w := do(h, "GET", "/x", "Bearer dpx_2222222222222222222222222222222222222"); w.Code != 401 {
		t.Fatalf("second key before creation: %d", w.Code)
	}
	keys.set(keyFor("k1", "a", "dpx_1111111111111111111111111111111111111"), keyFor("k2", "b", "dpx_2222222222222222222222222222222222222"))
	now = now.Add(3 * time.Second) // 캐시 만료
	if w := do(h, "GET", "/x", "Bearer dpx_2222222222222222222222222222222222222"); w.Code != 200 {
		t.Fatalf("second key after creation: %d", w.Code)
	}
	keys.set(keyFor("k2", "b", "dpx_2222222222222222222222222222222222222"))
	now = now.Add(3 * time.Second)
	if w := do(h, "GET", "/x", "Bearer dpx_1111111111111111111111111111111111111"); w.Code != 401 {
		t.Fatalf("deleted key must stop working: %d", w.Code)
	}
}

func TestUsageIsCountedAndPersisted(t *testing.T) {
	keys := &fakeKeys{}
	keys.set(keyFor("k1", "orca", "dpx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"), keyFor("k2", "idle", "dpx_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"))
	g, h, _ := newGateway(t, keys, true)
	auth := "Bearer dpx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	do(h, "POST", "/v1/chat/completions", auth)
	do(h, "POST", "/v1/chat/completions", auth)
	do(h, "GET", "/boom", auth)
	s := g.Snapshot()
	if s.Requests != 3 || s.Errors != 1 {
		t.Fatalf("totals: %+v", s)
	}
	var orca, idle KeyUsage
	for _, k := range s.Keys {
		switch k.Label {
		case "orca":
			orca = k
		case "idle":
			idle = k
		}
	}
	if orca.Requests != 3 || orca.Errors != 1 || orca.PromptTokens != 22 || orca.CompletionTokens != 14 || orca.LastUsed.IsZero() {
		t.Fatalf("orca usage: %+v", orca)
	}
	if idle.Requests != 0 || !idle.LastUsed.IsZero() {
		t.Fatalf("idle key must be listed with zero usage: %+v", idle)
	}
	if len(s.Recent) != 3 || s.Recent[0].KeyLabel != "orca" || s.Recent[0].Remote != "192.168.0.7" {
		t.Fatalf("recent: %+v", s.Recent)
	}
	if err := g.Flush(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(g.UsagePath)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("usage file: %v %v", st, err)
	}
	// 새 게이트웨이가 저장한 사용량을 이어받는다.
	g2 := &Gateway{Keys: keys, UsagePath: g.UsagePath}
	for _, k := range g2.Snapshot().Keys {
		if k.Label == "orca" && k.Requests != 3 {
			t.Fatalf("loaded usage: %+v", k)
		}
	}
	// 키 평문은 파일에 없다.
	data, _ := os.ReadFile(g.UsagePath)
	if strings.Contains(string(data), "dpx_aaaa") {
		t.Fatal("the usage file must not contain a key")
	}
}

func TestMaxInFlight(t *testing.T) {
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(300 * time.Millisecond)
		io.WriteString(w, "ok")
	}))
	defer be.Close()
	u, _ := url.Parse(be.URL)
	g := &Gateway{Keys: &fakeKeys{}, MaxInFlight: 1}
	h := g.Handler(u)
	var wg sync.WaitGroup
	codes := make(chan int, 2)
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			codes <- do(h, "GET", "/slow", "").Code
			time.Sleep(0)
		}()
		time.Sleep(50 * time.Millisecond)
	}
	wg.Wait()
	close(codes)
	var ok, busy int
	for c := range codes {
		switch c {
		case 200:
			ok++
		case 429:
			busy++
		}
	}
	if ok != 1 || busy != 1 {
		t.Fatalf("ok=%d busy=%d", ok, busy)
	}
}

func TestBackendDownIs502(t *testing.T) {
	u, _ := url.Parse("http://127.0.0.1:1")
	g := &Gateway{Keys: &fakeKeys{}}
	w := do(g.Handler(u), "GET", "/v1/models", "")
	if w.Code != http.StatusBadGateway || !strings.Contains(w.Body.String(), "model_server_unavailable") {
		t.Fatalf("code=%d body=%s", w.Code, w.Body.String())
	}
}

func TestManagerListensOnlyWhileTheServerRuns(t *testing.T) {
	be, _ := newBackend(t)
	beURL, _ := url.Parse(be.URL)
	_, bePortStr, _ := net.SplitHostPort(beURL.Host)
	var bePort int
	for _, c := range bePortStr {
		bePort = bePort*10 + int(c-'0')
	}
	// 공개 포트로 쓸 빈 포트를 찾는다.
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := l.Addr().String()
	_, pubStr, _ := net.SplitHostPort(addr)
	l.Close()
	var pub int
	for _, c := range pubStr {
		pub = pub*10 + int(c-'0')
	}

	var mu sync.Mutex
	running := false
	g := &Gateway{Keys: &fakeKeys{}, LANExposed: func() bool { return false }}
	m := &Manager{G: g,
		Wanted: func() (bool, string, int) { return true, addr, pub },
		Backend: func() (int, bool) {
			mu.Lock()
			defer mu.Unlock()
			return bePort, running
		},
		Interval: 20 * time.Millisecond}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	get := func() (int, error) {
		c := &http.Client{Timeout: time.Second}
		r, err := c.Get("http://" + addr + "/v1/models")
		if err != nil {
			return 0, err
		}
		defer r.Body.Close()
		return r.StatusCode, nil
	}
	if _, err := get(); err == nil {
		t.Fatal("nothing should listen while the server is stopped")
	}
	mu.Lock()
	running = true
	mu.Unlock()
	deadline := time.Now().Add(3 * time.Second)
	for {
		if code, err := get(); err == nil && code == 200 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("the gateway did not start listening: %+v", m.State())
		}
		time.Sleep(30 * time.Millisecond)
	}
	if !m.State().Listening || m.State().BackendPort != bePort {
		t.Fatalf("state: %+v", m.State())
	}
	mu.Lock()
	running = false
	mu.Unlock()
	deadline = time.Now().Add(3 * time.Second)
	for {
		if _, err := get(); err != nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("the gateway kept listening after the server stopped")
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func TestManagerRefusesSharedPort(t *testing.T) {
	g := &Gateway{Keys: &fakeKeys{}}
	m := &Manager{G: g,
		Wanted:  func() (bool, string, int) { return true, "127.0.0.1:0", 8080 },
		Backend: func() (int, bool) { return 8080, true }}
	m.reconcile()
	if st := m.State(); st.Listening || !strings.Contains(st.Error, "restart") {
		t.Fatalf("state: %+v", st)
	}
}

// doFrom 은 출발지 주소를 지정해 요청한다.
func doFrom(h http.Handler, remote, method, path, auth string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
	r.RemoteAddr = remote
	if auth != "" {
		r.Header.Set("Authorization", auth)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestLocalRequestsNeedNoKeyWhenAllowed(t *testing.T) {
	keys := &fakeKeys{}
	plain := "dpx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	keys.set(keyFor("k1", "laptop", plain))
	g, h, _ := newGateway(t, keys, true)
	g.LocalNoKey = func() bool { return true }

	// 루프백은 키 없이 통과한다. 다른 컴퓨터는 여전히 키가 필요하다.
	if w := doFrom(h, "127.0.0.1:5000", "GET", "/props", ""); w.Code != 200 {
		t.Fatalf("loopback without a key = %d", w.Code)
	}
	if w := doFrom(h, "[::1]:5000", "GET", "/v1/models", ""); w.Code != 200 {
		t.Fatalf("ipv6 loopback without a key = %d", w.Code)
	}
	if w := doFrom(h, "192.0.2.77:5000", "GET", "/v1/models", ""); w.Code != 401 {
		t.Fatalf("a remote computer without a key = %d", w.Code)
	}
	// 로컬이어도 키를 내면 그 키로 집계한다.
	if w := doFrom(h, "127.0.0.1:5000", "POST", "/v1/chat/completions", "Bearer "+plain); w.Code != 200 {
		t.Fatalf("loopback with a key = %d", w.Code)
	}
	usage := g.Snapshot().Keys
	if len(usage) != 1 || usage[0].Requests != 1 {
		t.Fatalf("usage = %+v", usage)
	}
	// 끄면 로컬에서도 키가 필요하다.
	g.LocalNoKey = func() bool { return false }
	if w := doFrom(h, "127.0.0.1:5000", "GET", "/v1/models", ""); w.Code != 401 {
		t.Fatalf("loopback with local_no_key off = %d", w.Code)
	}
}

func TestInfoEndpointAndVersionHeader(t *testing.T) {
	keys := &fakeKeys{}
	keys.set(keyFor("k1", "laptop", "dpx_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"))
	g, h, _ := newGateway(t, keys, true)
	g.Version = "0.5.3"
	g.Info = func() map[string]any {
		return map[string]any{"diparnux": "0.5.3", "model": "m.gguf", "ctx_per_slot": 16384}
	}

	// /diparnux/info 는 키 없이 읽힌다. 헤더는 모든 응답에 붙는다.
	w := doFrom(h, "192.0.2.77:5000", "GET", "/diparnux/info", "")
	if w.Code != 200 || w.Header().Get("X-Diparnux") != "0.5.3" || !strings.Contains(w.Body.String(), `"ctx_per_slot":16384`) {
		t.Fatalf("info = %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	if strings.Contains(w.Body.String(), "path") {
		t.Fatal("the info endpoint must not expose paths")
	}
	if w := doFrom(h, "192.0.2.77:5000", "GET", "/v1/models", ""); w.Code != 401 || w.Header().Get("X-Diparnux") != "0.5.3" {
		t.Fatalf("models = %d %v", w.Code, w.Header())
	}
	// POST 는 정보 경로가 아니다(모델 서버로 넘어가므로 키가 필요하다).
	if w := doFrom(h, "192.0.2.77:5000", "POST", "/diparnux/info", ""); w.Code != 401 {
		t.Fatalf("post = %d", w.Code)
	}
}

func TestBusyAnswerAsksToRetry(t *testing.T) {
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer be.Close()
	u, _ := url.Parse(be.URL)
	g := &Gateway{Keys: &fakeKeys{}, MaxInFlight: 1}
	h := g.Handler(u)
	go do(h, "GET", "/slow", "")
	time.Sleep(50 * time.Millisecond)
	w := do(h, "GET", "/slow", "")
	if w.Code != 429 || w.Header().Get("Retry-After") != "2" {
		t.Fatalf("busy = %d %v", w.Code, w.Header())
	}
}

// 코딩 에이전트는 SSE 로 토큰을 받아 화면에 바로 쓴다. 게이트웨이가 청크를 모아 두었다 내보내면 안 된다.
// 두 번째 청크는 클라이언트가 첫 청크를 읽은 뒤에야 모델 서버가 내보낸다. 줄 단위로 읽어 도착 순서를 확인한다.
func TestServerSentEventsAreForwardedAsTheyArrive(t *testing.T) {
	release := make(chan struct{})
	be := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fl := w.(http.Flusher)
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
		fl.Flush()
		select {
		case <-release:
		case <-r.Context().Done():
			return
		}
		io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"b\"}}],\"usage\":{\"completion_tokens\":2}}\n\n")
		fl.Flush()
		io.WriteString(w, "data: [DONE]\n\n")
		fl.Flush()
	}))
	defer be.Close()
	u, _ := url.Parse(be.URL)
	g := &Gateway{Keys: &fakeKeys{}, LANExposed: func() bool { return false }, Version: "0.5.3"}
	front := httptest.NewServer(g.Handler(u))
	defer front.Close()

	client := &http.Client{Transport: &http.Transport{}}
	defer client.CloseIdleConnections()
	req, _ := http.NewRequest("POST", front.URL+"/v1/chat/completions", strings.NewReader(`{"stream":true}`))
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if !strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") || resp.Header.Get("X-Diparnux") != "0.5.3" {
		t.Fatalf("headers = %v", resp.Header)
	}

	// 줄을 읽는 일은 별도 고루틴에서 하고, 읽은 줄을 채널로 받는다.
	lines := make(chan string, 16)
	readErr := make(chan error, 1)
	go func() {
		sc := bufio.NewScanner(resp.Body)
		for sc.Scan() {
			lines <- sc.Text()
		}
		readErr <- sc.Err()
		close(lines)
	}()
	next := func(what string) string {
		t.Helper()
		select {
		case l, ok := <-lines:
			if !ok {
				t.Fatalf("the stream ended before %s", what)
			}
			return l
		case <-time.After(5 * time.Second):
			t.Fatalf("timed out waiting for %s: the gateway may buffer the stream", what)
		}
		return ""
	}
	first := next("the first event")
	if !strings.Contains(first, `"content":"a"`) {
		t.Fatalf("first event = %q", first)
	}
	// 첫 이벤트가 도착했으니 이제 두 번째를 내보내게 한다.
	close(release)
	var rest []string
	for len(rest) < 8 {
		l := next("the end of the stream")
		if l == "" {
			continue
		}
		rest = append(rest, l)
		if l == "data: [DONE]" {
			break
		}
	}
	joined := strings.Join(rest, "\n")
	if !strings.Contains(joined, "completion_tokens") || !strings.HasSuffix(joined, "[DONE]") {
		t.Fatalf("rest = %q", joined)
	}
}
