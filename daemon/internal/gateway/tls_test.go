package gateway

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"io"
	"net"
	"net/http"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/certstore"
)

func freeAddr(t *testing.T) (string, int) {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := l.Addr().String()
	l.Close()
	_, p, _ := net.SplitHostPort(addr)
	n, _ := strconv.Atoi(p)
	return addr, n
}

type tlsRig struct {
	plainAddr, tlsAddr string
	mgr                *Manager
	store              *certstore.Store
	pool               *x509.CertPool
	setRunning         func(bool)
	setTLS             func(bool)
	prepared           func() int
}

// newTLSRig 는 평문 리스너 하나와 TLS 리스너 하나를 둔 관리자를 띄운다. 모델 서버는 흉내다.
func newTLSRig(t *testing.T, keys *fakeKeys) *tlsRig {
	t.Helper()
	be, _ := newBackend(t)
	beURL, _ := url.Parse(be.URL)
	_, bePortStr, _ := net.SplitHostPort(beURL.Host)
	bePort, _ := strconv.Atoi(bePortStr)
	plainAddr, plainPort := freeAddr(t)
	tlsAddr, _ := freeAddr(t)

	store := &certstore.Store{Dir: filepath.Join(t.TempDir(), "tls")}
	var mu sync.Mutex
	running, tlsOn, prepared := false, true, 0
	g := &Gateway{Keys: keys, Version: "0.5.4-test", LANExposed: func() bool { return false },
		UsagePath: filepath.Join(t.TempDir(), "usage.json")}
	m := &Manager{G: g,
		Wanted: func() (bool, string, int) { return true, plainAddr, plainPort },
		Backend: func() (int, bool) {
			mu.Lock()
			defer mu.Unlock()
			return bePort, running
		},
		WantedTLS: func() (bool, string) {
			mu.Lock()
			defer mu.Unlock()
			return tlsOn, tlsAddr
		},
		Cert: store.GetCertificate,
		PrepareTLS: func() error {
			mu.Lock()
			prepared++
			mu.Unlock()
			_, err := store.Ensure(certstore.Names{DNS: []string{"box.local"}, IPs: []net.IP{net.ParseIP("127.0.0.1")}}, nil)
			return err
		},
		Interval:     20 * time.Millisecond,
		PrepareEvery: time.Hour,
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	rig := &tlsRig{plainAddr: plainAddr, tlsAddr: tlsAddr, mgr: m, store: store,
		setRunning: func(v bool) { mu.Lock(); running = v; mu.Unlock() },
		setTLS:     func(v bool) { mu.Lock(); tlsOn = v; mu.Unlock() },
		prepared:   func() int { mu.Lock(); defer mu.Unlock(); return prepared },
	}
	return rig
}

func (r *tlsRig) loadPool(t *testing.T) {
	t.Helper()
	pemData, err := r.store.CAPEM()
	if err != nil {
		t.Fatal(err)
	}
	r.pool = x509.NewCertPool()
	if !r.pool.AppendCertsFromPEM(pemData) {
		t.Fatal("CA did not load")
	}
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out: %s", what)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func httpsClient(pool *x509.CertPool) *http.Client {
	return &http.Client{Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{RootCAs: pool, ServerName: "box.local"}}}
}

func plainClient() *http.Client {
	return &http.Client{Timeout: 2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// U7/U8/U9: 평문과 TLS 두 리스너가 같은 핸들러를 쓴다. 리다이렉트는 없고, 모델 서버가 멈추면 둘 다 닫힌다.
func TestTLSListenerSharesTheHandlerAndNeverRedirects(t *testing.T) {
	rig := newTLSRig(t, &fakeKeys{})
	if rig.mgr.TLSState().Listening {
		t.Fatal("the TLS listener is up while the model server is stopped")
	}
	rig.setRunning(true)
	waitFor(t, "TLS listener", func() bool { return rig.mgr.TLSState().Listening })
	waitFor(t, "plain listener", func() bool { return rig.mgr.State().Listening })
	rig.loadPool(t)

	tlsResp, err := httpsClient(rig.pool).Get("https://" + rig.tlsAddr + "/v1/models")
	if err != nil {
		t.Fatalf("HTTPS with the CA: %v", err)
	}
	body, _ := io.ReadAll(tlsResp.Body)
	tlsResp.Body.Close()
	plainResp, err := plainClient().Get("http://" + rig.plainAddr + "/v1/models")
	if err != nil {
		t.Fatalf("plain HTTP: %v", err)
	}
	plainResp.Body.Close()
	if tlsResp.StatusCode != 200 || plainResp.StatusCode != 200 {
		t.Fatalf("status tls=%d plain=%d body=%s", tlsResp.StatusCode, plainResp.StatusCode, body)
	}
	if tlsResp.Header.Get("X-Opereta") != "0.5.4-test" || plainResp.Header.Get("X-Opereta") != "0.5.4-test" {
		t.Fatalf("X-Opereta: tls=%q plain=%q", tlsResp.Header.Get("X-Opereta"), plainResp.Header.Get("X-Opereta"))
	}
	// 리다이렉트가 없다: 3xx 도 Location 도 아니다.
	for name, r := range map[string]*http.Response{"tls": tlsResp, "plain": plainResp} {
		if r.StatusCode/100 == 3 || r.Header.Get("Location") != "" {
			t.Errorf("%s answered with a redirect: %d %q", name, r.StatusCode, r.Header.Get("Location"))
		}
	}
	// 평문 요청을 TLS 포트로 보내면 연결 오류이지 리다이렉트가 아니다.
	if r, err := plainClient().Get("http://" + rig.tlsAddr + "/v1/models"); err == nil {
		defer r.Body.Close()
		if r.StatusCode/100 == 3 {
			t.Errorf("plain request to the TLS port was redirected: %d", r.StatusCode)
		}
	}

	rig.setRunning(false)
	waitFor(t, "both listeners to close", func() bool {
		return !rig.mgr.TLSState().Listening && !rig.mgr.State().Listening
	})
}

// U9: 키 규칙이 두 리스너에서 같다.
func TestTLSListenerAppliesTheSameKeyRules(t *testing.T) {
	keys := &fakeKeys{}
	secret := "opr_bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	keys.set(keyFor("k1", "laptop", secret))
	rig := newTLSRig(t, keys)
	rig.setRunning(true)
	waitFor(t, "TLS listener", func() bool { return rig.mgr.TLSState().Listening })
	rig.loadPool(t)

	status := func(c *http.Client, base, auth string) int {
		req, _ := http.NewRequest("GET", base+"/v1/models", nil)
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := c.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}
	tlsBase, plainBase := "https://"+rig.tlsAddr, "http://"+rig.plainAddr
	// 127.0.0.1 출발이므로 local_no_key 가 꺼진 이 시험에서는 키가 필요하다(LocalNoKey 함수가 nil).
	if got := status(httpsClient(rig.pool), tlsBase, ""); got != 401 {
		t.Errorf("TLS without a key = %d", got)
	}
	if got := status(plainClient(), plainBase, ""); got != 401 {
		t.Errorf("plain without a key = %d", got)
	}
	if got := status(httpsClient(rig.pool), tlsBase, "Bearer "+secret); got != 200 {
		t.Errorf("TLS with the key = %d", got)
	}
	if got := status(plainClient(), plainBase, "Bearer "+secret); got != 200 {
		t.Errorf("plain with the key = %d", got)
	}
	// 공개 상태 경로는 키 없이 된다.
	req, _ := http.NewRequest("GET", tlsBase+"/health", nil)
	resp, err := httpsClient(rig.pool).Do(req)
	if err != nil || resp.StatusCode != 200 {
		t.Errorf("TLS /health: %v %v", resp, err)
	}
	if resp != nil {
		resp.Body.Close()
	}
}

// CA 없이는 연결되지 않고, TLS 를 끄면 TLS 리스너만 닫히고 평문은 남는다.
func TestTLSRequiresTheCAAndCanBeTurnedOff(t *testing.T) {
	rig := newTLSRig(t, &fakeKeys{})
	rig.setRunning(true)
	waitFor(t, "TLS listener", func() bool { return rig.mgr.TLSState().Listening })
	rig.loadPool(t)

	noCA := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: x509.NewCertPool(), ServerName: "box.local"}}}
	if r, err := noCA.Get("https://" + rig.tlsAddr + "/v1/models"); err == nil {
		r.Body.Close()
		t.Fatal("a client without the CA connected")
	}
	wrongName := &http.Client{Timeout: 2 * time.Second, Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rig.pool, ServerName: "other.example.org"}}}
	if r, err := wrongName.Get("https://" + rig.tlsAddr + "/v1/models"); err == nil {
		r.Body.Close()
		t.Fatal("a name that is not in the certificate was accepted")
	}

	rig.setTLS(false)
	waitFor(t, "TLS listener to close", func() bool { return !rig.mgr.TLSState().Listening })
	if !rig.mgr.State().Listening {
		t.Fatal("turning TLS off closed the plain listener")
	}
	if r, err := plainClient().Get("http://" + rig.plainAddr + "/v1/models"); err != nil || r.StatusCode != 200 {
		t.Fatalf("plain after TLS off: %v %v", r, err)
	}
	// 다시 켜면 다시 듣는다. 인증서를 다시 준비한다.
	before := rig.prepared()
	rig.setTLS(true)
	waitFor(t, "TLS listener again", func() bool { return rig.mgr.TLSState().Listening })
	if rig.prepared() <= before {
		t.Fatal("the certificate was not prepared again when TLS was turned back on")
	}
}

// 인증서를 만들지 못하면 리스너를 열지 않고 이유를 상태에 둔다. 평문은 영향이 없다.
func TestTLSPrepareFailureLeavesPlainRunning(t *testing.T) {
	rig := newTLSRig(t, &fakeKeys{})
	rig.mgr.PrepareTLS = func() error { return io.ErrClosedPipe }
	rig.setRunning(true)
	waitFor(t, "plain listener", func() bool { return rig.mgr.State().Listening })
	waitFor(t, "TLS error", func() bool { return strings.Contains(rig.mgr.TLSState().Error, "certificate") })
	if rig.mgr.TLSState().Listening {
		t.Fatal("TLS is listening without a certificate")
	}
}
