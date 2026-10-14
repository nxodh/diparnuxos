package certstore

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, *time.Time) {
	t.Helper()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	return &Store{Dir: filepath.Join(t.TempDir(), "tls"), Now: func() time.Time { return now }}, &now
}

func names(dns []string, ips ...string) Names {
	n := Names{DNS: dns}
	for _, s := range ips {
		n.IPs = append(n.IPs, net.ParseIP(s))
	}
	return n
}

func parsePEM(t *testing.T, path string) *x509.Certificate {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(data)
	if block == nil {
		t.Fatalf("%s is not PEM", path)
	}
	c, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func verifyAs(t *testing.T, s *Store, leaf *x509.Certificate, dnsName string, at time.Time) error {
	t.Helper()
	pemData, err := s.CAPEM()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemData) {
		t.Fatal("CA PEM did not load")
	}
	_, err = leaf.Verify(x509.VerifyOptions{Roots: pool, DNSName: dnsName, CurrentTime: at, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}})
	return err
}

// U1: CA 는 CA 이고 pathlen 0, DNS 이름 제약만 있고 IP 제약은 없으며 수명이 10년이다.
func TestCAProperties(t *testing.T) {
	s, now := testStore(t)
	if _, err := s.Ensure(names([]string{"box.local", "box"}, "192.168.0.23"), nil); err != nil {
		t.Fatal(err)
	}
	ca := parsePEM(t, filepath.Join(s.Dir, "ca.pem"))
	if !ca.IsCA || !ca.BasicConstraintsValid || ca.MaxPathLen != 0 || !ca.MaxPathLenZero {
		t.Fatalf("CA basic constraints: IsCA=%v pathlen=%d zero=%v", ca.IsCA, ca.MaxPathLen, ca.MaxPathLenZero)
	}
	if len(ca.PermittedDNSDomains) == 0 {
		t.Fatal("CA has no DNS name constraints")
	}
	if len(ca.PermittedIPRanges) != 0 || len(ca.ExcludedIPRanges) != 0 {
		t.Fatalf("CA must not carry IP name constraints (Bun cannot verify them): %v", ca.PermittedIPRanges)
	}
	if !ca.PermittedDNSDomainsCritical {
		t.Fatal("the DNS name constraints are not critical")
	}
	if got := ca.NotAfter.Sub(*now); got < 9*365*24*time.Hour || got > 11*365*24*time.Hour {
		t.Fatalf("CA validity = %v", got)
	}
	if ca.KeyUsage&x509.KeyUsageCertSign == 0 {
		t.Fatal("CA cannot sign certificates")
	}
}

// U2: 서버 인증서는 CA 로 검증되고 호스트, .local, IP, localhost 를 덮는다.
func TestServerCertificateVerifiesForEveryName(t *testing.T) {
	s, now := testStore(t)
	info, err := s.Ensure(names([]string{"box.local", "box"}, "192.168.0.23", "10.0.0.5"), nil)
	if err != nil {
		t.Fatal(err)
	}
	leaf := parsePEM(t, filepath.Join(s.Dir, "server.pem"))
	for _, name := range []string{"box.local", "box", "localhost", "192.168.0.23", "10.0.0.5", "127.0.0.1"} {
		if err := verifyAs(t, s, leaf, name, *now); err != nil {
			t.Errorf("verify as %s: %v", name, err)
		}
	}
	if got := leaf.NotAfter.Sub(*now); got < 390*24*time.Hour || got > 400*24*time.Hour {
		t.Fatalf("server certificate validity = %v", got)
	}
	if info.CAFingerprint == "" || info.ServerFingerprint == "" || info.CAFingerprint == info.ServerFingerprint {
		t.Fatalf("fingerprints = %+v", info)
	}
	if !strings.Contains(info.CAFingerprint, ":") || len(info.CAFingerprint) != 95 {
		t.Fatalf("fingerprint format = %q", info.CAFingerprint)
	}
	// 다른 이름은 거부한다.
	if err := verifyAs(t, s, leaf, "other.local", *now); err == nil {
		t.Error("a name that is not in the certificate was accepted")
	}
}

// U3: CA 의 이름 제약 밖의 DNS 이름은 인증서에 넣지 않고, 억지로 서명해도 검증되지 않는다.
func TestNamesOutsideTheConstraintAreSkippedAndNotTrusted(t *testing.T) {
	s, now := testStore(t)
	info, err := s.Ensure(names([]string{"box.local", "box"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	// 나중에 요청한 이름이 CA 의 제약 밖이면 건너뛰고 알린다.
	info, err = s.Ensure(names([]string{"box.local", "box", "evil.example.com"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(info.Skipped) != 1 || info.Skipped[0] != "evil.example.com" {
		t.Fatalf("skipped = %v", info.Skipped)
	}
	for _, d := range info.DNS {
		if d == "evil.example.com" {
			t.Fatal("a name outside the constraint is in the certificate")
		}
	}
	// 같은 CA 키로 제약 밖 이름에 서명해 보아도 체인 검증에서 거부된다.
	ca, caKey, err := s.loadCA()
	if err != nil || ca == nil {
		t.Fatalf("load CA: %v", err)
	}
	forged := Names{DNS: []string{"example.com"}}
	if err := s.createServer(ca, caKey, forged); err != nil {
		t.Fatal(err)
	}
	leaf := parsePEM(t, filepath.Join(s.Dir, "server.pem"))
	if err := verifyAs(t, s, leaf, "example.com", *now); err == nil {
		t.Fatal("the CA name constraint did not stop a certificate for example.com")
	}
}

// U4: 주소가 바뀌면 서버 인증서만 다시 만들고 CA 는 그대로다.
func TestAddressChangeRenewsOnlyTheServerCertificate(t *testing.T) {
	s, _ := testStore(t)
	first, err := s.Ensure(names([]string{"box.local"}, "192.168.0.23"), nil)
	if err != nil {
		t.Fatal(err)
	}
	same, err := s.Ensure(names([]string{"box.local"}, "192.168.0.23"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if same.ServerFingerprint != first.ServerFingerprint {
		t.Fatal("an unchanged address set renewed the certificate")
	}
	next, err := s.Ensure(names([]string{"box.local"}, "192.168.0.99"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.CAFingerprint != first.CAFingerprint {
		t.Fatal("the CA changed when only the address changed")
	}
	if next.ServerFingerprint == first.ServerFingerprint {
		t.Fatal("the server certificate was not reissued for a new address")
	}
	found := false
	for _, ip := range next.IPs {
		if ip == "192.168.0.99" {
			found = true
		}
	}
	if !found {
		t.Fatalf("new address missing: %v", next.IPs)
	}
}

// U5: 만료 30일 전에 서버 인증서를 갱신한다. CA 는 그대로다.
func TestServerCertificateRenewsBeforeExpiry(t *testing.T) {
	s, now := testStore(t)
	first, err := s.Ensure(names([]string{"box.local"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	*now = now.Add(300 * 24 * time.Hour) // 아직 갱신 시점 전
	mid, _ := s.Ensure(names([]string{"box.local"}), nil)
	if mid.ServerFingerprint != first.ServerFingerprint {
		t.Fatal("renewed too early")
	}
	*now = first.ServerNotAfter.Add(-10 * 24 * time.Hour) // 만료 10일 전
	late, err := s.Ensure(names([]string{"box.local"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	if late.ServerFingerprint == first.ServerFingerprint {
		t.Fatal("not renewed inside the renewal window")
	}
	if late.CAFingerprint != first.CAFingerprint {
		t.Fatal("renewal changed the CA")
	}
	if !late.ServerNotAfter.After(first.ServerNotAfter) {
		t.Fatalf("new certificate ends %v, not after the old one %v", late.ServerNotAfter, first.ServerNotAfter)
	}
}

// U6: 개인 키는 0600, 디렉터리는 0700 이다. 임시 파일이 남지 않는다.
func TestFilePermissions(t *testing.T) {
	s, _ := testStore(t)
	if _, err := s.Ensure(names([]string{"box.local"}), nil); err != nil {
		t.Fatal(err)
	}
	check := func(name string, want os.FileMode) {
		t.Helper()
		st, err := os.Stat(filepath.Join(s.Dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := st.Mode().Perm(); got != want {
			t.Errorf("%s mode = %o, want %o", name, got, want)
		}
	}
	check("ca-key.pem", 0o600)
	check("server-key.pem", 0o600)
	check("ca.pem", 0o644)
	check("server.pem", 0o644)
	if st, err := os.Stat(s.Dir); err != nil || st.Mode().Perm() != 0o700 {
		t.Errorf("dir mode: %v %v", st, err)
	}
	entries, _ := os.ReadDir(s.Dir)
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".tmp") {
			t.Errorf("leftover temp file %s", e.Name())
		}
	}
}

// CA 를 다시 만들면 지문이 바뀌고, 이전 서버 인증서는 새 CA 로 검증되지 않는다.
func TestRenewCAChangesTheFingerprint(t *testing.T) {
	s, now := testStore(t)
	first, err := s.Ensure(names([]string{"box.local"}, "192.168.0.23"), nil)
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.RenewCA(names([]string{"box.local"}, "192.168.0.23"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if next.CAFingerprint == first.CAFingerprint {
		t.Fatal("the CA fingerprint did not change")
	}
	leaf := parsePEM(t, filepath.Join(s.Dir, "server.pem"))
	if err := verifyAs(t, s, leaf, "box.local", *now); err != nil {
		t.Fatalf("the new server certificate does not verify with the new CA: %v", err)
	}
}

// 추가 이름은 CA 를 만들 때 포함되면 서버 인증서에 들어간다.
func TestExtraNamesAreIncludedWhenTheCAIsCreated(t *testing.T) {
	s, now := testStore(t)
	info, err := s.Ensure(names([]string{"box.local"}), []string{"ai.lan"})
	if err != nil {
		t.Fatal(err)
	}
	leaf := parsePEM(t, filepath.Join(s.Dir, "server.pem"))
	if err := verifyAs(t, s, leaf, "ai.lan", *now); err != nil {
		t.Fatalf("extra name: %v (info %+v)", err, info)
	}
}

// 실제 TLS 연결: CA 만 믿는 클라이언트가 이름, IP 로 접속하고, CA 없이는 실패한다. 갱신은 새 연결부터 반영된다.
func TestHandshakeWithTheCAOnly(t *testing.T) {
	// 클라이언트는 실제 시계로 검증하므로 이 시험은 가짜 시계를 쓰지 않는다.
	s := &Store{Dir: filepath.Join(t.TempDir(), "tls")}
	if _, err := s.Ensure(names([]string{"box.local"}, "127.0.0.1"), nil); err != nil {
		t.Fatal(err)
	}
	// 서버 이름 표시(SNI)가 없는 IP 접속에서도 GetCertificate 가 쓰이도록 Certificates 를 비운 설정으로 직접 듣는다.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })}
	go func() {
		_ = srv.Serve(tls.NewListener(ln, &tls.Config{GetCertificate: s.GetCertificate, MinVersion: tls.VersionTLS12}))
	}()
	defer srv.Close()
	url := "https://" + ln.Addr().String()

	pemData, err := s.CAPEM()
	if err != nil {
		t.Fatal(err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(pemData)

	get := func(rootCAs *x509.CertPool) error {
		c := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: rootCAs}}}
		defer c.CloseIdleConnections()
		resp, err := c.Get(url)
		if err != nil {
			return err
		}
		_ = resp.Body.Close()
		return nil
	}
	if err := get(pool); err != nil {
		t.Fatalf("with the CA: %v", err)
	}
	if err := get(x509.NewCertPool()); err == nil {
		t.Fatal("a client without the CA connected")
	}
	// 갱신 후 새 연결은 새 인증서를 쓴다.
	before, _ := s.Info()
	if _, err := s.RenewServer(names([]string{"box.local"}, "127.0.0.1"), nil); err != nil {
		t.Fatal(err)
	}
	after, _ := s.Info()
	if after.ServerFingerprint == before.ServerFingerprint {
		t.Fatal("renew did not change the server certificate")
	}
	if err := get(pool); err != nil {
		t.Fatalf("after renewal: %v", err)
	}
}

// 개인 키가 공개 정보와 CA PEM 에 없다.
func TestPublicOutputsCarryNoPrivateKey(t *testing.T) {
	s, _ := testStore(t)
	info, err := s.Ensure(names([]string{"box.local"}), nil)
	if err != nil {
		t.Fatal(err)
	}
	pemData, _ := s.CAPEM()
	if strings.Contains(string(pemData), "PRIVATE KEY") {
		t.Fatal("CAPEM contains a private key")
	}
	if strings.Contains(strings.Join(append(append([]string{}, info.DNS...), info.IPs...), " "), "PRIVATE") {
		t.Fatal("info contains key material")
	}
}
