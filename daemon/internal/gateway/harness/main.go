// Command harness 는 게이트웨이만 따로 띄우는 시험용 프로그램이다(제품에 들어가지 않는다).
// tests/vedecaps 가 실제 Vedecaps 실행 파일을 이 게이트웨이와 가짜 모델 서버에 붙여 호환을 확인한다.
// 환경 변수: HARNESS_KEY(키 하나를 만든다), HARNESS_LOCAL_NO_KEY=1(이 컴퓨터의 요청은 키 없이 통과).
// 127.0.0.1:8080 에서 받고 127.0.0.1:18080 으로 넘긴다.
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"net/url"
	"os"

	"github.com/nxodh/diparnuxos/daemon/internal/apikeys"
	"github.com/nxodh/diparnuxos/daemon/internal/gateway"
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
		Version:    "0.5.3",
		Info: func() map[string]any {
			return map[string]any{"diparnux": "0.5.3", "server": "running", "model": "fake.gguf", "ctx_per_slot": 16384, "slots": 2}
		},
	}
	u, _ := url.Parse("http://127.0.0.1:18080")
	log.Fatal(http.ListenAndServe("127.0.0.1:8080", g.Handler(u)))
}
