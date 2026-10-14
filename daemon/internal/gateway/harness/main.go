// Command harness 는 게이트웨이만 따로 띄우는 시험용 프로그램이다(제품에 들어가지 않는다).
// tests/vedecaps 가 실제 Vedecaps 실행 파일을 이 게이트웨이와 가짜 모델 서버에 붙여 호환을 확인한다.
// 환경 변수: HARNESS_KEY(키 하나를 만든다), HARNESS_LOCAL_NO_KEY=1(이 컴퓨터의 요청은 키 없이 통과).
// 127.0.0.1:8080 에서 받고 127.0.0.1:18080 으로 넘긴다.
// HARNESS_TLS_DIR 이 있으면 그 디렉터리에 로컬 CA 와 서버 인증서(box.local, 127.0.0.1)를 만들어 127.0.0.1:8444 에서도 받는다.
// HARNESS_CA_OUT 에 CA 인증서(PEM)를 쓴다. HARNESS_ONLY_CA=1 이면 CA 만 만들고 끝낸다(다른 CA 시험용).
package main

import (
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"

	"github.com/nxodh/operetaos/daemon/internal/apikeys"
	"github.com/nxodh/operetaos/daemon/internal/certstore"
	"github.com/nxodh/operetaos/daemon/internal/gateway"
)

type keys struct{ list []apikeys.Key }

func (k keys) List() ([]apikeys.Key, error) { return k.list, nil }

func main() {
	var ks []apikeys.Key
	if p := os.Getenv("HARNESS_KEY"); p != "" {
		sum := sha256.Sum256([]byte(p))
		ks = append(ks, apikeys.Key{ID: "k1", Label: "harness", Prefix: p[:8], SHA256: hex.EncodeToString(sum[:])})
	}
	g := &gateway.Gateway{
		Keys:       keys{ks},
		LANExposed: func() bool { return false },
		LocalNoKey: func() bool { return os.Getenv("HARNESS_LOCAL_NO_KEY") == "1" },
		Version:    "0.6.0",
		Info: func() map[string]any {
			return map[string]any{"opereta": "0.6.0", "server": "running", "model": "fake.gguf", "ctx_per_slot": 16384, "slots": 2}
		},
	}
	u, _ := url.Parse("http://127.0.0.1:18080")
	h := g.Handler(u)

	if dir := os.Getenv("HARNESS_TLS_DIR"); dir != "" {
		store := &certstore.Store{Dir: dir}
		names := certstore.Names{DNS: []string{"box.local"}, IPs: []net.IP{net.ParseIP("127.0.0.1")}}
		if _, err := store.Ensure(names, nil); err != nil {
			log.Fatalf("harness: certificate: %v", err)
		}
		if out := os.Getenv("HARNESS_CA_OUT"); out != "" {
			pem, err := store.CAPEM()
			if err != nil {
				log.Fatal(err)
			}
			if err := os.WriteFile(out, pem, 0o644); err != nil {
				log.Fatal(err)
			}
		}
		if os.Getenv("HARNESS_ONLY_CA") == "1" {
			return
		}
		ln, err := net.Listen("tcp", "127.0.0.1:8444")
		if err != nil {
			log.Fatal(err)
		}
		go func() {
			srv := &http.Server{Handler: h}
			log.Fatal(srv.Serve(tls.NewListener(ln, &tls.Config{GetCertificate: store.GetCertificate, MinVersion: tls.VersionTLS12})))
		}()
	}
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", h))
}
