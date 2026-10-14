// Package certstore 는 게이트웨이 TLS 에 쓰는 로컬 CA 와 서버 인증서를 만들고 보관한다.
//
// 구조: 작은 로컬 CA(ECDSA P-256, 10년, pathlen 0, DNS 이름 제약만)가 서버 인증서(397일)에 서명한다. 클라이언트는 CA 하나만
// 믿으면 되고, 서버의 주소나 이름이 바뀌면 서버 인증서만 다시 발급한다. CA 에는 IP 이름 제약을 넣지 않는다: Bun 은 CA 에 IP
// 제약이 있고 서버 인증서에 IP SAN 이 있으면 검증하지 못한다(UNSUPPORTED_CONSTRAINT_TYPE). 서버 인증서의 SAN 에는 IP 를 넣는다.
//
// 개인 키는 이 패키지의 파일(0600)에만 있다. API, 로그, 보고서에 나오지 않는다.
package certstore

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// 수명과 갱신 시점이다.
const (
	CAValidity        = 10 * 365 * 24 * time.Hour
	ServerValidity    = 397 * 24 * time.Hour
	RenewBefore       = 30 * 24 * time.Hour
	CAWarnBefore      = 365 * 24 * time.Hour
	backdate          = time.Hour
	caFile            = "ca.pem"
	caKeyFile         = "ca-key.pem"
	serverFile        = "server.pem"
	serverKeyFile     = "server-key.pem"
	maxExtraNames     = 32
	maxNameLen        = 253
	permittedLocalTLD = "local"
)

// Names 는 서버 인증서의 SAN 으로 넣고 싶은 이름이다.
type Names struct {
	DNS []string
	IPs []net.IP
}

// Info 는 지금 저장된 인증서의 공개 정보다. 개인 키는 없다.
type Info struct {
	Present           bool      `json:"present"`
	CAFingerprint     string    `json:"ca_fingerprint,omitempty"`
	CANotAfter        time.Time `json:"ca_not_after,omitempty"`
	ServerFingerprint string    `json:"server_fingerprint,omitempty"`
	ServerNotAfter    time.Time `json:"server_not_after,omitempty"`
	// DNS 와 IPs 는 서버 인증서에 실제로 들어 있는 이름이다.
	DNS []string `json:"dns,omitempty"`
	IPs []string `json:"ips,omitempty"`
	// PermittedDNS 는 CA 가 발급을 허용하는 DNS 이름(도메인)이다.
	PermittedDNS []string `json:"permitted_dns,omitempty"`
	// Skipped 는 요청했지만 CA 의 이름 제약 밖이라 서버 인증서에 넣지 못한 DNS 이름이다. 넣으려면 CA 를 다시 만든다.
	Skipped []string `json:"skipped,omitempty"`
	// CAExpiresSoon 은 CA 가 1년 안에 만료한다는 뜻이다.
	CAExpiresSoon bool `json:"ca_expires_soon,omitempty"`
}

// Store 는 한 디렉터리에 CA 와 서버 인증서를 둔다. 동시에 써도 안전하다.
type Store struct {
	// Dir 은 인증서 디렉터리다. 예: /var/lib/diparnux/tls
	Dir string
	// Now 는 시계다. nil 이면 time.Now.
	Now func() time.Time

	mu       sync.Mutex
	cached   *tls.Certificate
	cachedAt string // server.pem 의 크기와 수정 시각
}

func (s *Store) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

// Fingerprint 는 DER 인증서의 SHA-256 지문을 "AA:BB:..." 로 돌려준다.
func Fingerprint(der []byte) string {
	sum := sha256.Sum256(der)
	parts := make([]string, len(sum))
	for i, b := range sum {
		parts[i] = strings.ToUpper(hex.EncodeToString([]byte{b}))
	}
	return strings.Join(parts, ":")
}

func (s *Store) path(name string) string { return filepath.Join(s.Dir, name) }

// Ensure 는 CA 와 서버 인증서가 있고 names 를 덮고 있는지 보장한다. 없거나 부족하면 만든다.
// CA 는 이미 있으면 바꾸지 않는다(클라이언트가 이미 배포받았기 때문이다). 서버 인증서만 다시 만든다.
// names.DNS 중 CA 의 이름 제약 밖인 이름은 넣지 않고 Info.Skipped 로 알린다.
func (s *Store) Ensure(names Names, extra []string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.Dir == "" {
		return Info{}, errors.New("certstore: no directory")
	}
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Info{}, err
	}
	ca, caKey, err := s.loadCA()
	if err != nil || ca == nil || s.now().After(ca.NotAfter) {
		// CA 가 없거나 읽을 수 없거나 만료했다. 새로 만든다. 허용 DNS 는 이 이름들 기준이다.
		ca, caKey, err = s.createCA(names, extra)
		if err != nil {
			return Info{}, err
		}
		// 새 CA 로는 이전 서버 인증서가 검증되지 않는다.
		_ = os.Remove(s.path(serverFile))
		_ = os.Remove(s.path(serverKeyFile))
		s.cached = nil
	}
	want, skipped := filterNames(names, extra, ca)
	if !s.serverOK(ca, want) {
		if err := s.createServer(ca, caKey, want); err != nil {
			return Info{}, err
		}
	}
	info, err := s.infoLocked()
	info.Skipped = skipped
	return info, err
}

// RenewServer 는 서버 인증서만 지금 다시 만든다. CA 는 그대로다.
func (s *Store) RenewServer(names Names, extra []string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	ca, caKey, err := s.loadCA()
	if err != nil || ca == nil {
		return Info{}, errors.New("certstore: there is no CA yet; turn TLS on first")
	}
	want, skipped := filterNames(names, extra, ca)
	if err := s.createServer(ca, caKey, want); err != nil {
		return Info{}, err
	}
	info, err := s.infoLocked()
	info.Skipped = skipped
	return info, err
}

// RenewCA 는 CA 와 서버 인증서를 모두 새로 만든다. 클라이언트에 새 CA 를 다시 배포해야 한다.
func (s *Store) RenewCA(names Names, extra []string) (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := os.MkdirAll(s.Dir, 0o700); err != nil {
		return Info{}, err
	}
	ca, caKey, err := s.createCA(names, extra)
	if err != nil {
		return Info{}, err
	}
	want, skipped := filterNames(names, extra, ca)
	if err := s.createServer(ca, caKey, want); err != nil {
		return Info{}, err
	}
	info, err := s.infoLocked()
	info.Skipped = skipped
	return info, err
}

// Info 는 저장된 인증서의 공개 정보를 읽는다(만들지 않는다).
func (s *Store) Info() (Info, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.infoLocked()
}

func (s *Store) infoLocked() (Info, error) {
	ca, _, err := s.loadCA()
	if err != nil || ca == nil {
		return Info{}, nil
	}
	info := Info{Present: true, CAFingerprint: Fingerprint(ca.Raw), CANotAfter: ca.NotAfter,
		PermittedDNS: append([]string{}, ca.PermittedDNSDomains...)}
	info.CAExpiresSoon = s.now().After(ca.NotAfter.Add(-CAWarnBefore))
	if leaf, err := s.loadServerLeaf(); err == nil {
		info.ServerFingerprint = Fingerprint(leaf.Raw)
		info.ServerNotAfter = leaf.NotAfter
		info.DNS = append([]string{}, leaf.DNSNames...)
		for _, ip := range leaf.IPAddresses {
			info.IPs = append(info.IPs, ip.String())
		}
	}
	return info, nil
}

// CAPEM 은 클라이언트에 배포할 CA 인증서(공개 값)를 돌려준다.
func (s *Store) CAPEM() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	data, err := os.ReadFile(s.path(caFile))
	if err != nil {
		return nil, err
	}
	if block, _ := pem.Decode(data); block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("certstore: ca.pem is not a certificate")
	}
	return data, nil
}

// GetCertificate 는 tls.Config.GetCertificate 에 쓴다. 파일이 바뀌면(갱신) 새 연결부터 새 인증서를 준다.
func (s *Store) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := os.Stat(s.path(serverFile))
	if err != nil {
		return nil, fmt.Errorf("certstore: no server certificate: %w", err)
	}
	stamp := fmt.Sprintf("%d-%d", st.Size(), st.ModTime().UnixNano())
	if s.cached != nil && s.cachedAt == stamp {
		return s.cached, nil
	}
	c, err := tls.LoadX509KeyPair(s.path(serverFile), s.path(serverKeyFile))
	if err != nil {
		return nil, err
	}
	s.cached, s.cachedAt = &c, stamp
	return s.cached, nil
}

// ---- 내부 ----

// filterNames 는 서버 인증서에 넣을 이름을 정한다. DNS 이름은 CA 의 이름 제약 안에 있어야 한다.
func filterNames(names Names, extra []string, ca *x509.Certificate) (Names, []string) {
	seen := map[string]bool{}
	var dns, skipped []string
	add := func(n string) {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || len(n) > maxNameLen || seen[n] {
			return
		}
		seen[n] = true
		if net.ParseIP(n) != nil {
			return
		}
		if !permitted(n, ca.PermittedDNSDomains) {
			skipped = append(skipped, n)
			return
		}
		dns = append(dns, n)
	}
	for _, n := range names.DNS {
		add(n)
	}
	for i, n := range extra {
		if i >= maxExtraNames {
			break
		}
		add(n)
	}
	add("localhost")
	var ips []net.IP
	have := map[string]bool{}
	addIP := func(ip net.IP) {
		if ip == nil || have[ip.String()] {
			return
		}
		have[ip.String()] = true
		ips = append(ips, ip)
	}
	for _, ip := range names.IPs {
		addIP(ip)
	}
	addIP(net.ParseIP("127.0.0.1"))
	addIP(net.ParseIP("::1"))
	sort.Strings(dns)
	sort.Slice(ips, func(i, j int) bool { return ips[i].String() < ips[j].String() })
	sort.Strings(skipped)
	return Names{DNS: dns, IPs: ips}, skipped
}

// permitted 는 이름이 허용 도메인(자신 또는 하위) 안에 있는지 본다. x509 의 DNS 이름 제약과 같은 규칙이다.
func permitted(name string, domains []string) bool {
	for _, d := range domains {
		if name == d || strings.HasSuffix(name, "."+d) {
			return true
		}
	}
	return false
}

// permittedDomains 는 CA 에 넣을 허용 도메인이다: local(.local 이름), localhost, 이 컴퓨터의 호스트 이름과 요청한 이름의 도메인.
func permittedDomains(names Names, extra []string) []string {
	set := map[string]bool{permittedLocalTLD: true, "localhost": true}
	add := func(n string) {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == "" || len(n) > maxNameLen || net.ParseIP(n) != nil {
			return
		}
		set[n] = true
	}
	for _, n := range names.DNS {
		add(n)
	}
	for i, n := range extra {
		if i >= maxExtraNames {
			break
		}
		add(n)
	}
	out := make([]string, 0, len(set))
	for d := range set {
		out = append(out, d)
	}
	sort.Strings(out)
	return out
}

func (s *Store) loadCA() (*x509.Certificate, *ecdsa.PrivateKey, error) {
	certPEM, err := os.ReadFile(s.path(caFile))
	if err != nil {
		return nil, nil, nil
	}
	block, _ := pem.Decode(certPEM)
	if block == nil {
		return nil, nil, errors.New("certstore: ca.pem is not PEM")
	}
	ca, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err := os.ReadFile(s.path(caKeyFile))
	if err != nil {
		return nil, nil, err
	}
	kb, _ := pem.Decode(keyPEM)
	if kb == nil {
		return nil, nil, errors.New("certstore: ca-key.pem is not PEM")
	}
	key, err := x509.ParseECPrivateKey(kb.Bytes)
	if err != nil {
		return nil, nil, err
	}
	return ca, key, nil
}

func (s *Store) loadServerLeaf() (*x509.Certificate, error) {
	data, err := os.ReadFile(s.path(serverFile))
	if err != nil {
		return nil, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("certstore: server.pem is not PEM")
	}
	return x509.ParseCertificate(block.Bytes)
}

// serverOK 는 지금 서버 인증서가 CA 로 검증되고, 이름을 덮고, 갱신 시점 전인지 본다.
func (s *Store) serverOK(ca *x509.Certificate, want Names) bool {
	leaf, err := s.loadServerLeaf()
	if err != nil {
		return false
	}
	if _, err := os.Stat(s.path(serverKeyFile)); err != nil {
		return false
	}
	if !s.now().Before(leaf.NotAfter.Add(-RenewBefore)) {
		return false
	}
	pool := x509.NewCertPool()
	pool.AddCert(ca)
	if _, err := leaf.Verify(x509.VerifyOptions{Roots: pool, CurrentTime: s.now(), KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
		return false
	}
	have := map[string]bool{}
	for _, d := range leaf.DNSNames {
		have[d] = true
	}
	for _, ip := range leaf.IPAddresses {
		have[ip.String()] = true
	}
	for _, d := range want.DNS {
		if !have[d] {
			return false
		}
	}
	for _, ip := range want.IPs {
		if !have[ip.String()] {
			return false
		}
	}
	return true
}

func (s *Store) createCA(names Names, extra []string) (*x509.Certificate, *ecdsa.PrivateKey, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, err
	}
	serial, err := newSerial()
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	tmpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Diparnux local CA", Organization: []string{"Diparnux"}},
		NotBefore:             now.Add(-backdate),
		NotAfter:              now.Add(CAValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		MaxPathLen:            0,
		MaxPathLenZero:        true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign | x509.KeyUsageDigitalSignature,
		// DNS 이름 제약만 둔다. IP 제약은 두지 않는다(패키지 설명 참고).
		PermittedDNSDomainsCritical: true,
		PermittedDNSDomains:         permittedDomains(names, extra),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		return nil, nil, err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return nil, nil, err
	}
	if err := writeFile(s.path(caKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return nil, nil, err
	}
	if err := writeFile(s.path(caFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return nil, nil, err
	}
	ca, err := x509.ParseCertificate(der)
	return ca, key, err
}

func (s *Store) createServer(ca *x509.Certificate, caKey *ecdsa.PrivateKey, want Names) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, err := newSerial()
	if err != nil {
		return err
	}
	cn := "diparnux"
	if len(want.DNS) > 0 {
		cn = want.DNS[0]
	}
	now := s.now()
	notAfter := now.Add(ServerValidity)
	if notAfter.After(ca.NotAfter) {
		notAfter = ca.NotAfter
	}
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: cn, Organization: []string{"Diparnux"}},
		NotBefore:    now.Add(-backdate),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     want.DNS,
		IPAddresses:  want.IPs,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		return err
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		return err
	}
	// 키를 먼저, 인증서를 나중에 쓴다. 중간에 멈춰도 인증서 없이 키만 남는다(다음 Ensure 가 다시 만든다).
	if err := writeFile(s.path(serverKeyFile), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}), 0o600); err != nil {
		return err
	}
	if err := writeFile(s.path(serverFile), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644); err != nil {
		return err
	}
	s.cached = nil
	return nil
}

func newSerial() (*big.Int, error) {
	return rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 100))
}

// writeFile 은 임시 파일에 쓰고 이름을 바꿔 넣는다. 권한은 처음부터 mode 다.
func writeFile(path string, data []byte, mode os.FileMode) error {
	tmp := path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, mode)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, path)
}
