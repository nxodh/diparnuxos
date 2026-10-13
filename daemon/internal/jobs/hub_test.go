package jobs

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/hf"
	"github.com/nxodh/diparnuxos/daemon/internal/models"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// hubFile 은 가짜 허브 저장소의 파일 하나다.
type hubFile struct {
	path string
	data []byte
	// digest 가 비어 있으면 data 의 실제 SHA-256 을 목록에 보인다. 다른 값이면 무결성 검사가 실패하도록 만든다.
	digest string
	// noLFS 가 true 면 목록에 LFS 정보를 주지 않는다.
	noLFS bool
}

// hub 는 Hugging Face Hub 의 필요한 부분만 흉내 낸 httptest 서버다.
type hub struct {
	t       *testing.T
	repo    string
	sha     string
	license string
	gated   bool
	files   []hubFile
	srv     *httptest.Server

	// onResolve 는 resolve 요청을 가로챈다. true 를 돌려주면 기본 응답을 쓰지 않는다.
	onResolve func(w http.ResponseWriter, r *http.Request, file string) bool
	// blockAPI 가 있으면 모델 정보 요청은 이 채널이 닫히거나 요청이 끝날 때까지 기다린다.
	blockAPI chan struct{}

	mu       sync.Mutex
	requests []string
	auth     []string
}

// newHub 는 repo 를 가진 서버를 만들고 테스트가 끝나면 닫는다.
func newHub(t *testing.T, repo string, files ...hubFile) *hub {
	t.Helper()
	h := &hub{t: t, repo: repo, sha: strings.Repeat("a", 40), license: "apache-2.0", files: files}
	h.srv = httptest.NewServer(http.HandlerFunc(h.serve))
	t.Cleanup(h.srv.Close)
	return h
}

// sha256Hex 는 data 의 SHA-256 을 16진수 문자열로 돌려준다.
func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

// count 는 기록된 요청 중 prefix 로 시작하는 경로의 수를 돌려준다.
func (h *hub) count(prefix string) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, p := range h.requests {
		if strings.HasPrefix(p, prefix) {
			n++
		}
	}
	return n
}

// authSeen 은 받은 Authorization 헤더 중 하나라도 value 와 같은지 본다.
func (h *hub) authSeen(value string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, a := range h.auth {
		if a == value {
			return true
		}
	}
	return false
}

// record 는 요청 경로와 Authorization 헤더를 기록한다.
func (h *hub) record(r *http.Request) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.requests = append(h.requests, r.URL.Path)
	h.auth = append(h.auth, r.Header.Get("Authorization"))
}

// find 는 경로가 file 인 파일을 돌려준다. 없으면 nil 이다.
func (h *hub) find(file string) *hubFile {
	for i := range h.files {
		if h.files[i].path == file {
			return &h.files[i]
		}
	}
	return nil
}

// serve 는 요청 하나를 처리한다.
func (h *hub) serve(w http.ResponseWriter, r *http.Request) {
	h.record(r)
	api := "/api/models/" + h.repo
	switch {
	case r.URL.Path == api:
		if h.blockAPI != nil {
			select {
			case <-h.blockAPI:
			case <-r.Context().Done():
				return
			}
		}
		gated := "false"
		if h.gated {
			gated = `"manual"`
		}
		fmt.Fprintf(w, `{"id":%q,"sha":%q,"gated":%s,"cardData":{"license":%q}}`, h.repo, h.sha, gated, h.license)
	case r.URL.Path == api+"/tree/"+h.sha || r.URL.Path == api+"/tree/main":
		h.serveTree(w)
	case strings.HasPrefix(r.URL.Path, "/"+h.repo+"/resolve/"):
		rev, file, ok := strings.Cut(strings.TrimPrefix(r.URL.Path, "/"+h.repo+"/resolve/"), "/")
		if !ok || (rev != h.sha && rev != "main") {
			w.Header().Set("X-Error-Code", "RevisionNotFound")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		f := h.find(file)
		if f == nil {
			w.Header().Set("X-Error-Code", "EntryNotFound")
			w.WriteHeader(http.StatusNotFound)
			return
		}
		if h.onResolve != nil && h.onResolve(w, r, file) {
			return
		}
		// http.ServeContent 는 Range 와 Content-Length 를 처리한다.
		http.ServeContent(w, r, file, time.Time{}, bytes.NewReader(f.data))
	default:
		// 없는 저장소는 토큰 없이 401 과 X-Error-Code 없음으로 답한다. 실제 허브가 그렇게 답한다.
		w.WriteHeader(http.StatusUnauthorized)
	}
}

// serveTree 는 파일 목록을 JSON 으로 쓴다.
func (h *hub) serveTree(w http.ResponseWriter) {
	list := make([]map[string]any, 0, len(h.files))
	for _, f := range h.files {
		e := map[string]any{
			"type": "file",
			"path": f.path,
			"size": len(f.data),
			"oid":  strings.Repeat("b", 40),
		}
		if !f.noLFS {
			digest := f.digest
			if digest == "" {
				digest = sha256Hex(f.data)
			}
			e["lfs"] = map[string]any{"oid": digest, "size": len(f.data)}
		}
		list = append(list, e)
	}
	_ = json.NewEncoder(w).Encode(list)
}

// fixedNow 는 테스트가 쓰는 고정 시각이다.
func fixedNow() time.Time {
	return time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)
}

// newTestManager 는 h 를 가리키는 Manager 를 만든다. 모델 디렉터리는 테스트 전용 임시 디렉터리다.
// Close 는 테스트 정리 때 한 번 더 불러도 된다.
func newTestManager(t *testing.T, h *hub, token string, mut ...func(*Options)) *Manager {
	t.Helper()
	o := Options{
		HF:        &hf.Client{BaseURL: h.srv.URL, HTTP: h.srv.Client(), Token: token},
		Store:     models.Store{Dir: t.TempDir()},
		FreeBytes: func(string) (uint64, error) { return 1 << 40, nil },
		Now:       fixedNow,
	}
	for _, f := range mut {
		f(&o)
	}
	m := NewManager(o)
	t.Cleanup(m.Close)
	return m
}

// mustWait 은 작업이 끝날 때까지 기다리고 최종 상태를 돌려준다. 없는 작업이면 테스트를 실패시킨다.
func mustWait(t *testing.T, m *Manager, id string) platform.Job {
	t.Helper()
	j, ok := m.Wait(id)
	if !ok {
		t.Fatalf("job %s not found", id)
	}
	return j
}

// mustPull 은 StartPull 이 성공하기를 기대하고 작업을 돌려준다.
func mustPull(t *testing.T, m *Manager, req platform.PullRequest) platform.Job {
	t.Helper()
	j, err := m.StartPull(t.Context(), req)
	if err != nil {
		t.Fatalf("StartPull(%+v): %v", req, err)
	}
	return j
}

// expectCode 는 err 가 코드 code 의 PullError 인지 확인한다.
func expectCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error with code %s, got nil", code)
	}
	if got := CodeOf(err); got != code {
		t.Fatalf("code = %q (%v), want %q", got, err, code)
	}
}

// jobCount 는 Manager 가 기억하는 작업 수를 돌려준다.
func jobCount(m *Manager) int {
	return len(m.List())
}
