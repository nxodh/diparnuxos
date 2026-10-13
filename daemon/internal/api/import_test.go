package api

import (
	"bytes"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
)

// importGGUF 는 아주 작은 GGUF v3 파일이다.
func importGGUF() []byte {
	var b bytes.Buffer
	le := binary.LittleEndian
	b.WriteString("GGUF")
	binary.Write(&b, le, uint32(3))
	binary.Write(&b, le, uint64(0))
	binary.Write(&b, le, uint64(1))
	k := "general.architecture"
	binary.Write(&b, le, uint64(len(k)))
	b.WriteString(k)
	binary.Write(&b, le, uint32(8))
	binary.Write(&b, le, uint64(5))
	b.WriteString("llama")
	return b.Bytes()
}

func (e *env) put(path string, body []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	r := httptest.NewRequest(http.MethodPut, path, bytes.NewReader(body))
	r.ContentLength = int64(len(body))
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	return w
}

func TestImportModelStoresItAndMakesItTheDefault(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	data := importGGUF()
	w := e.put("/v1/models/import?name=mine.gguf", data)
	wantStatus(t, w, http.StatusCreated)
	var res struct {
		ModelID   string `json:"model_id"`
		SizeBytes int64  `json:"size_bytes"`
		SHA256    string `json:"sha256"`
		Default   bool   `json:"default"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.ModelID != "mine.gguf" || res.SizeBytes != int64(len(data)) || len(res.SHA256) != 64 || !res.Default {
		t.Fatalf("response = %+v", res)
	}
	list := e.do("GET", "/v1/models", "")
	wantStatus(t, list, http.StatusOK)
	if !strings.Contains(list.Body.String(), `"repo":"local-import"`) || !strings.Contains(list.Body.String(), `"incomplete":false`) {
		t.Fatalf("model list = %s", list.Body.String())
	}
	if got, _ := os.ReadFile(filepath.Join(e.dir, "mine.gguf")); !bytes.Equal(got, data) {
		t.Fatal("stored bytes differ")
	}

	// 같은 이름은 덮어쓰지 않는다. 두 번째 모델은 기본을 바꾸지 않는다.
	w = e.put("/v1/models/import?name=mine.gguf", data)
	wantStatus(t, w, http.StatusConflict)
	if errCode(t, w) != "name_conflict" {
		t.Fatalf("code = %s", errCode(t, w))
	}
	w = e.put("/v1/models/import?name=second.gguf", data)
	wantStatus(t, w, http.StatusCreated)
	if !strings.Contains(w.Body.String(), `"default":false`) {
		t.Fatalf("second import changed the default: %s", w.Body.String())
	}
}

func TestImportModelRejectsBadRequests(t *testing.T) {
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	cases := []struct {
		name string
		path string
		body []byte
		want int
		code string
	}{
		{"missing name", "/v1/models/import", importGGUF(), 400, "invalid_input"},
		{"path in name", "/v1/models/import?name=../x.gguf", importGGUF(), 400, "invalid_input"},
		{"wrong extension", "/v1/models/import?name=x.bin", importGGUF(), 400, "invalid_input"},
		{"empty body", "/v1/models/import?name=x.gguf", nil, 400, "invalid_input"},
		{"not gguf", "/v1/models/import?name=x.gguf", []byte("this is not a model file at all"), 400, "not_gguf"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := e.put(tc.path, tc.body)
			wantStatus(t, w, tc.want)
			if errCode(t, w) != tc.code {
				t.Fatalf("code = %s, want %s", errCode(t, w), tc.code)
			}
		})
	}
	if entries, _ := os.ReadDir(e.dir); len(entries) != 1 { // diparnuxd.json 만 있다.
		t.Fatalf("files were left behind: %v", entries)
	}
}

func TestImportModelChecksFreeSpace(t *testing.T) {
	old := importDiskFree
	importDiskFree = func(string) (uint64, error) { return 1 << 20, nil }
	defer func() { importDiskFree = old }()
	e := newEnv(t, &fakeSup{status: supervisor.Status{State: supervisor.StateStopped}})
	w := e.put("/v1/models/import?name=big.gguf", importGGUF())
	wantStatus(t, w, http.StatusInsufficientStorage)
	if errCode(t, w) != "insufficient_space" {
		t.Fatalf("code = %s", errCode(t, w))
	}
}
