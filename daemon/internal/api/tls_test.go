package api

import (
	"encoding/json"
	"net"
	"net/http"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/certstore"
	"github.com/nxodh/operetaos/daemon/internal/gateway"
	"github.com/nxodh/operetaos/daemon/internal/supervisor"
)

// testTLS 는 실제 certstore 를 쓰는 TLSManager 다. 이름은 고정이다.
type testTLS struct {
	store *certstore.Store
	state gateway.ManagerState
}

func (t *testTLS) names() certstore.Names {
	return certstore.Names{DNS: []string{"box.local", "box"}, IPs: []net.IP{net.ParseIP("192.168.0.23")}}
}
func (t *testTLS) Info() (certstore.Info, error)             { return t.store.Info() }
func (t *testTLS) Ensure(x []string) (certstore.Info, error) { return t.store.Ensure(t.names(), x) }
func (t *testTLS) RenewServer(x []string) (certstore.Info, error) {
	return t.store.RenewServer(t.names(), x)
}
func (t *testTLS) RenewCA(x []string) (certstore.Info, error) { return t.store.RenewCA(t.names(), x) }
func (t *testTLS) CAPEM() ([]byte, error)                     { return t.store.CAPEM() }
func (t *testTLS) Listener() gateway.ManagerState             { return t.state }

func tlsEnv(t *testing.T) (*env, *fakeSup) {
	t.Helper()
	sup := &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}
	tm := &testTLS{store: &certstore.Store{Dir: filepath.Join(t.TempDir(), "tls")}}
	e := newEnvWith(t, sup, func(d *Deps) { d.TLS = tm })
	return e, sup
}

func decodeTLS(t *testing.T, body []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatalf("decode: %v\n%s", err, body)
	}
	return m
}

func TestTLSEnableNeedsLANAndAKey(t *testing.T) {
	e, _ := tlsEnv(t)
	w := e.do("GET", "/v1/tls", "")
	wantStatus(t, w, http.StatusOK)
	if got := decodeTLS(t, w.Body.Bytes()); got["enabled"] != false || got["ca_fingerprint"] != nil {
		t.Fatalf("initial = %v", got)
	}

	w = e.do("PUT", "/v1/tls", `{"enabled":true}`)
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != "lan_required" {
		t.Fatalf("code = %s", errCode(t, w))
	}
	// LAN 공개는 키가 있어야 켠다. 키를 만든 뒤 켠다.
	wantStatus(t, e.do("POST", "/v1/apikeys", `{"label":"laptop"}`), http.StatusCreated)
	wantStatus(t, e.do("PUT", "/v1/config", `{"lan_exposed":true}`), http.StatusOK)
	// 키를 지울 수는 없지만, 키가 없을 때의 거부는 키 저장소가 비어 있는 별도 환경에서 본다.
	e2, _ := tlsEnv(t)
	cfgOnly := `{"lan_exposed":true}`
	if w := e2.do("PUT", "/v1/config", cfgOnly); w.Code == http.StatusOK {
		t.Fatal("LAN exposure without a key was accepted")
	}
}

func TestTLSEnableRenewAndDisable(t *testing.T) {
	e, sup := tlsEnv(t)
	wantStatus(t, e.do("POST", "/v1/apikeys", `{"label":"laptop"}`), http.StatusCreated)
	wantStatus(t, e.do("PUT", "/v1/config", `{"lan_exposed":true}`), http.StatusOK)
	applied := sup.firewalls

	w := e.do("PUT", "/v1/tls", `{"enabled":true}`)
	wantStatus(t, w, http.StatusOK)
	on := decodeTLS(t, w.Body.Bytes())
	if on["enabled"] != true || on["active"] != true || on["port"] != float64(8444) {
		t.Fatalf("on = %v", on)
	}
	if on["plain_lan_open"] != false {
		t.Fatalf("the plain port stays open on the LAN after turning TLS on: %v", on)
	}
	if sup.firewalls != applied+1 {
		t.Fatalf("firewall applied %d times after enabling", sup.firewalls-applied)
	}
	caFP, _ := on["ca_fingerprint"].(string)
	srvFP, _ := on["server_fingerprint"].(string)
	if caFP == "" || srvFP == "" {
		t.Fatalf("no fingerprints: %v", on)
	}
	if strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatal("the reply contains key material")
	}

	// CA 는 PEM 이고 개인 키가 아니다.
	w = e.do("GET", "/v1/tls/ca", "")
	wantStatus(t, w, http.StatusOK)
	if !strings.Contains(w.Body.String(), "BEGIN CERTIFICATE") || strings.Contains(w.Body.String(), "PRIVATE") {
		t.Fatalf("ca = %q", w.Body.String())
	}

	// 서버 인증서만 갱신하면 CA 지문은 같다.
	w = e.do("POST", "/v1/tls/renew", `{}`)
	wantStatus(t, w, http.StatusOK)
	renewed := decodeTLS(t, w.Body.Bytes())
	if renewed["ca_fingerprint"] != caFP || renewed["server_fingerprint"] == srvFP {
		t.Fatalf("server renew: %v", renewed)
	}
	// CA 까지 갱신하면 CA 지문이 바뀌고 안내가 붙는다.
	w = e.do("POST", "/v1/tls/renew", `{"ca":true}`)
	wantStatus(t, w, http.StatusOK)
	reCA := decodeTLS(t, w.Body.Bytes())
	if reCA["ca_fingerprint"] == caFP {
		t.Fatalf("CA renew did not change the CA: %v", reCA)
	}
	if !strings.Contains(w.Body.String(), "new CA") {
		t.Fatalf("no notice about distributing the new CA: %s", w.Body.String())
	}

	// 끄면 평문이 LAN 에 다시 열린다. 인증서는 남는다.
	w = e.do("PUT", "/v1/tls", `{"enabled":false}`)
	wantStatus(t, w, http.StatusOK)
	off := decodeTLS(t, w.Body.Bytes())
	if off["enabled"] != false || off["active"] != false || off["plain_lan_open"] != true || off["ca_fingerprint"] == nil {
		t.Fatalf("off = %v", off)
	}
	// lan_plain 은 TLS 가 켜져 있어도 평문을 연다.
	wantStatus(t, e.do("PUT", "/v1/tls", `{"enabled":true,"lan_plain":true}`), http.StatusOK)
	w = e.do("GET", "/v1/tls", "")
	both := decodeTLS(t, w.Body.Bytes())
	if both["active"] != true || both["plain_lan_open"] != true || !strings.Contains(w.Body.String(), "not encrypted") {
		t.Fatalf("both = %s", w.Body.String())
	}
}

func TestTLSRejectsBadSettings(t *testing.T) {
	e, _ := tlsEnv(t)
	wantStatus(t, e.do("POST", "/v1/apikeys", `{"label":"laptop"}`), http.StatusCreated)
	wantStatus(t, e.do("PUT", "/v1/config", `{"lan_exposed":true}`), http.StatusOK)
	for name, body := range map[string]string{
		"port = api port":   `{"enabled":true,"port":8080}`,
		"port = ssh":        `{"enabled":true,"port":22}`,
		"bad name":          `{"enabled":true,"names":["-bad"]}`,
		"port out of range": `{"enabled":true,"port":70000}`,
	} {
		w := e.do("PUT", "/v1/tls", body)
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s: status %d body %s", name, w.Code, w.Body.String())
		}
	}
	// 거부된 요청은 설정을 바꾸지 않는다.
	w := e.do("GET", "/v1/tls", "")
	if decodeTLS(t, w.Body.Bytes())["enabled"] != false {
		t.Fatal("a rejected request changed the setting")
	}
	// 갱신은 인증서가 없을 때 거부된다.
	if w := e.do("POST", "/v1/tls/renew", `{}`); w.Code != http.StatusConflict {
		t.Errorf("renew without a certificate = %d", w.Code)
	}
	// CA 가 없으면 404.
	if w := e.do("GET", "/v1/tls/ca", ""); w.Code != http.StatusNotFound {
		t.Errorf("CA before setup = %d", w.Code)
	}
}

func TestTLSUnavailableWithoutAManager(t *testing.T) {
	e := newEnvWith(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}}, nil)
	w := e.do("GET", "/v1/tls", "")
	wantStatus(t, w, http.StatusServiceUnavailable)
	if errCode(t, w) != "tls_unavailable" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}
