package hf

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
)

const testToken = "hf_test_secret_token_value"

// newTestClient starts an httptest server with handler and returns a client that points at it.
func newTestClient(t *testing.T, handler http.Handler) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Client{BaseURL: srv.URL, Token: testToken, HTTP: srv.Client()}, srv
}

// countingHandler counts requests so that tests can check that no request was sent.
func countingHandler(n *atomic.Int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		next.ServeHTTP(w, r)
	})
}

// gemmaModelJSON is a model info response shaped like the one recorded in the research notes.
const gemmaModelJSON = `{"id":"ggml-org/gemma-3-1b-it-GGUF","private":false,"tags":["gguf","license:gemma"],` +
	`"sha":"f9c28bcd85737ffc5aef028638d3341d49869c27","gated":false,"cardData":{"license":"gemma"},` +
	`"gguf":{"total":999885952,"architecture":"gemma3","context_length":32768}}`

func TestModelInfo(t *testing.T) {
	tests := []struct {
		name string
		body string
		want ModelInfo
	}{
		{
			name: "license from cardData, gated false",
			body: gemmaModelJSON,
			want: ModelInfo{ID: "ggml-org/gemma-3-1b-it-GGUF", SHA: "f9c28bcd85737ffc5aef028638d3341d49869c27",
				Gated: GateNone, License: "gemma",
				GGUF: GGUFSummary{Architecture: "gemma3", ContextLength: 32768, Total: 999885952}},
		},
		{
			name: "gated auto",
			body: `{"id":"a/b","gated":"auto"}`,
			want: ModelInfo{ID: "a/b", Gated: GateAuto},
		},
		{
			name: "gated manual with private true",
			body: `{"id":"a/b","private":true,"gated":"manual"}`,
			want: ModelInfo{ID: "a/b", Private: true, Gated: GateManual},
		},
		{
			name: "license falls back to the license tag",
			body: `{"id":"a/b","tags":["gguf","license:apache-2.0"],"gated":false}`,
			want: ModelInfo{ID: "a/b", Gated: GateNone, License: "apache-2.0"},
		},
		{
			name: "non-string cardData license is treated as absent and the tag is used",
			body: `{"id":"a/b","cardData":{"license":["apache-2.0","mit"]},"tags":["license:mit"]}`,
			want: ModelInfo{ID: "a/b", License: "mit"},
		},
		{
			name: "no license anywhere",
			body: `{"id":"a/b","tags":["gguf"],"gated":false}`,
			want: ModelInfo{ID: "a/b", Gated: GateNone},
		},
		{
			name: "missing gated field gives the empty mode",
			body: `{"id":"a/b"}`,
			want: ModelInfo{ID: "a/b"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.HasPrefix(r.URL.Path, "/api/models/") {
					http.NotFound(w, r)
					return
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprint(w, tt.body)
			}))
			got, err := c.ModelInfo(context.Background(), tt.want.ID)
			if err != nil {
				t.Fatalf("ModelInfo: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("ModelInfo = %+v\nwant %+v", got, tt.want)
			}
		})
	}
}

func TestModelInfoSendsTokenOnlyWhenSet(t *testing.T) {
	var sawAuth []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		sawAuth = append(sawAuth, r.Header.Get("Authorization"))
		fmt.Fprint(w, `{"id":"a/b"}`)
	})
	c, _ := newTestClient(t, handler)
	if _, err := c.ModelInfo(context.Background(), "a/b"); err != nil {
		t.Fatal(err)
	}
	c.Token = ""
	if _, err := c.ModelInfo(context.Background(), "a/b"); err != nil {
		t.Fatal(err)
	}
	want := []string{"Bearer " + testToken, ""}
	if !reflect.DeepEqual(sawAuth, want) {
		t.Fatalf("Authorization headers = %q, want %q", sawAuth, want)
	}
}

func TestUserAgent(t *testing.T) {
	var ua string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ua = r.Header.Get("User-Agent")
		fmt.Fprint(w, `{"id":"a/b"}`)
	}))
	if _, err := c.ModelInfo(context.Background(), "a/b"); err != nil {
		t.Fatal(err)
	}
	if ua != "lightlixd" {
		t.Fatalf("User-Agent = %q, want lightlixd", ua)
	}
	c.UserAgent = "custom/1"
	if _, err := c.ModelInfo(context.Background(), "a/b"); err != nil {
		t.Fatal(err)
	}
	if ua != "custom/1" {
		t.Fatalf("User-Agent = %q, want custom/1", ua)
	}
}

func TestErrorStatuses(t *testing.T) {
	tests := []struct {
		name    string
		status  int
		headers map[string]string
		want    error
	}{
		{"gated 401", 401, map[string]string{"X-Error-Code": "GatedRepo"}, ErrGated},
		{"gated 403", 403, map[string]string{"X-Error-Code": "GatedRepo"}, ErrGated},
		{"unauthorized without gated code is a status error", 401, nil, nil},
		{"not found", 404, map[string]string{"X-Error-Code": "RepoNotFound"}, ErrNotFound},
		{"rate limited", 429, map[string]string{"RateLimit": `"api";r=0;t=152`}, ErrRateLimited},
		{"server error", 500, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, "error body that mentions "+testToken)
			}))
			_, err := c.ModelInfo(context.Background(), "a/b")
			if err == nil {
				t.Fatal("ModelInfo: want error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("error %v does not match %v", err, tt.want)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatalf("error text contains the token: %v", err)
			}
			if tt.want == nil {
				var se *StatusError
				if !errors.As(err, &se) || se.StatusCode != tt.status {
					t.Fatalf("error %v is not a StatusError with %d", err, tt.status)
				}
			}
		})
	}
}

func TestRateLimitErrorCarriesReset(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("RateLimit", `"resolvers";r=0;t=152, "api";r=0;t=141`)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	_, err := c.ModelInfo(context.Background(), "a/b")
	if !errors.Is(err, ErrRateLimited) {
		t.Fatalf("error %v is not ErrRateLimited", err)
	}
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.ResetSeconds != 152 {
		t.Fatalf("RateLimitError = %+v, want ResetSeconds 152", rl)
	}
}

func TestListFilesFollowsPages(t *testing.T) {
	var srv *httptest.Server
	var queries []string
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models/ggml-org/gemma-3-1b-it-GGUF/tree/main" {
			http.NotFound(w, r)
			return
		}
		queries = append(queries, r.URL.RawQuery)
		if r.URL.Query().Get("cursor") == "" {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?recursive=true&cursor=p2>; rel="next"`, srv.URL, r.URL.Path))
			fmt.Fprint(w, `[{"type":"file","oid":"x","size":806058240,"path":"gemma-3-1b-it-Q4_K_M.gguf",`+
				`"lfs":{"oid":"8ccc5cd1f1b3602548715ae25a66ed73fd5dc68a210412eea643eb20eb75a135","size":806058240}},`+
				`{"type":"directory","oid":"d","size":0,"path":"subdir"}]`)
			return
		}
		w.Header().Set("Link", `<https://evil.example/api/models/x?cursor=ignored>; rel="prev"`)
		fmt.Fprint(w, `[{"type":"file","oid":"y","size":42,"path":"README.md"}]`)
	})
	c, s := newTestClient(t, handler)
	srv = s

	files, err := c.ListFiles(context.Background(), "ggml-org/gemma-3-1b-it-GGUF", "main")
	if err != nil {
		t.Fatalf("ListFiles: %v", err)
	}
	want := []File{
		{Path: "gemma-3-1b-it-Q4_K_M.gguf", Size: 806058240, SHA256: "8ccc5cd1f1b3602548715ae25a66ed73fd5dc68a210412eea643eb20eb75a135", LFS: true},
		{Path: "README.md", Size: 42},
	}
	if !reflect.DeepEqual(files, want) {
		t.Fatalf("ListFiles = %+v\nwant %+v", files, want)
	}
	if len(queries) != 2 || queries[0] != "recursive=true" || queries[1] != "recursive=true&cursor=p2" {
		t.Fatalf("queries = %q", queries)
	}
}

func TestListFilesLowercasesDigestAndRejectsBadDigest(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"type":"file","size":1,"path":"a.gguf","lfs":{"oid":"ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789ABCDEF0123456789"}}]`)
	}))
	files, err := c.ListFiles(context.Background(), "a/b", "main")
	if err != nil {
		t.Fatal(err)
	}
	if files[0].SHA256 != "abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789" {
		t.Fatalf("SHA256 = %q", files[0].SHA256)
	}

	bad, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"type":"file","size":1,"path":"a.gguf","lfs":{"oid":"not-a-digest"}}]`)
	}))
	if _, err := bad.ListFiles(context.Background(), "a/b", "main"); err == nil {
		t.Fatal("ListFiles with a malformed lfs.oid: want error")
	}
}

func TestListFilesRejectsNegativeSize(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"type":"file","size":-5,"path":"a.gguf"}]`)
	}))
	if _, err := c.ListFiles(context.Background(), "a/b", "main"); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("error = %v, want ErrInvalidInput", err)
	}
}

func TestListFilesPaginationLoopAndLimits(t *testing.T) {
	t.Run("self link is a loop", func(t *testing.T) {
		var srv *httptest.Server
		c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?recursive=true>; rel="next"`, srv.URL, r.URL.Path))
			fmt.Fprint(w, `[]`)
		}))
		srv = s
		if _, err := c.ListFiles(context.Background(), "a/b", "main"); err == nil {
			t.Fatal("ListFiles: want loop error")
		}
	})
	t.Run("endless new cursors hit the page limit", func(t *testing.T) {
		var srv *httptest.Server
		var n atomic.Int64
		c, s := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			i := n.Add(1)
			w.Header().Set("Link", fmt.Sprintf(`<%s%s?cursor=%d>; rel="next"`, srv.URL, r.URL.Path, i))
			fmt.Fprint(w, `[]`)
		}))
		srv = s
		_, err := c.ListFiles(context.Background(), "a/b", "main")
		if err == nil || !strings.Contains(err.Error(), "more than") {
			t.Fatalf("ListFiles error = %v, want page limit", err)
		}
		if n.Load() != maxPages {
			t.Fatalf("requests = %d, want %d", n.Load(), maxPages)
		}
	})
	t.Run("link to another host is refused", func(t *testing.T) {
		c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Link", `<https://evil.example/api/models/a/b/tree/main?cursor=1>; rel="next"`)
			fmt.Fprint(w, `[]`)
		}))
		if _, err := c.ListFiles(context.Background(), "a/b", "main"); err == nil || !strings.Contains(err.Error(), "another host") {
			t.Fatalf("ListFiles error = %v, want another-host error", err)
		}
	})
}

func TestSearch(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/models" {
			http.NotFound(w, r)
			return
		}
		q := r.URL.Query()
		checks := map[string]string{"search": "gemma", "filter": "gguf", "sort": "downloads", "direction": "-1", "limit": "2"}
		for k, v := range checks {
			if q.Get(k) != v {
				http.Error(w, "bad "+k, http.StatusBadRequest)
				return
			}
		}
		if !reflect.DeepEqual(q["expand[]"], []string{"gguf", "gated", "cardData", "tags", "downloads"}) {
			http.Error(w, "bad expand", http.StatusBadRequest)
			return
		}
		fmt.Fprint(w, `[
			{"id":"ggml-org/gemma-3-1b-it-GGUF","downloads":206237,"gated":false,"cardData":{"license":"gemma"},"gguf":{"architecture":"gemma3","context_length":32768}},
			{"id":"google/x-GGUF","downloads":5,"gated":"manual","cardData":{"license":"gemma"},"gguf":{"architecture":"gemma3","context_length":8192}},
			{"id":"third/one","downloads":1}
		]`)
	})
	c, _ := newTestClient(t, handler)
	got, err := c.Search(context.Background(), "gemma", 2)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	want := []SearchResult{
		{ID: "ggml-org/gemma-3-1b-it-GGUF", Downloads: 206237, Gated: GateNone, License: "gemma", Architecture: "gemma3", ContextLength: 32768},
		{ID: "google/x-GGUF", Downloads: 5, Gated: GateManual, License: "gemma", Architecture: "gemma3", ContextLength: 8192},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Search = %+v\nwant %+v", got, want)
	}
}

func TestSearchFallsBackToModelID(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[{"modelId":"only/modelId"}]`)
	}))
	got, err := c.Search(context.Background(), "x", 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0].ID != "only/modelId" {
		t.Fatalf("Search = %+v", got)
	}
}

func TestSearchLimitAndQueryValidation(t *testing.T) {
	var n atomic.Int64
	c, _ := newTestClient(t, countingHandler(&n, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `[]`)
	})))
	for _, limit := range []int{0, -1, maxSearchLimit + 1} {
		if _, err := c.Search(context.Background(), "x", limit); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("limit %d: error %v, want ErrInvalidInput", limit, err)
		}
	}
	if _, err := c.Search(context.Background(), "bad\nquery", 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("control character query: error %v", err)
	}
	if _, err := c.Search(context.Background(), strings.Repeat("q", maxQueryLen+1), 1); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("long query: error %v", err)
	}
	if n.Load() != 0 {
		t.Fatalf("requests sent for invalid input: %d", n.Load())
	}
}

func TestInvalidInputSendsNoRequest(t *testing.T) {
	var n atomic.Int64
	c, _ := newTestClient(t, countingHandler(&n, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{}`)
	})))
	ctx := context.Background()
	repos := []string{"", "noslash", "a/b/c", "../x", "a/..", "./b", "a/b c", "a/b\n", "a/.", "a b/c", "a/b?x=1"}
	for _, repo := range repos {
		if _, err := c.ModelInfo(ctx, repo); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ModelInfo(%q): error %v, want ErrInvalidInput", repo, err)
		}
		if _, err := c.ListFiles(ctx, repo, "main"); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ListFiles(%q): error %v, want ErrInvalidInput", repo, err)
		}
	}
	revs := []string{"", "..", "a/../b", "/abs", "a//b", "a\\b", "x\x00y", "a/./b", "a/"}
	for _, rev := range revs {
		if _, err := c.ListFiles(ctx, "a/b", rev); !errors.Is(err, ErrInvalidInput) {
			t.Fatalf("ListFiles rev %q: error %v, want ErrInvalidInput", rev, err)
		}
	}
	if n.Load() != 0 {
		t.Fatalf("requests sent for invalid input: %d", n.Load())
	}
}

func TestBaseURLRules(t *testing.T) {
	ctx := context.Background()
	bad := []string{"http://example.com", "ftp://example.com", "example.com", "https://"}
	for _, base := range bad {
		c := &Client{BaseURL: base}
		if _, err := c.ModelInfo(ctx, "a/b"); err == nil {
			t.Fatalf("BaseURL %q: want error", base)
		}
	}
	// A loopback http origin is allowed for tests and local mirrors.
	for _, base := range []string{"http://127.0.0.1:8080", "http://localhost:9", "http://[::1]:9"} {
		if _, err := (&Client{BaseURL: base}).url("api/models/a/b", ""); err != nil {
			t.Fatalf("BaseURL %q: %v", base, err)
		}
	}
}

func TestEscapePath(t *testing.T) {
	got := escapePath("refs/pr/1 x")
	if got != "refs/pr/1%20x" {
		t.Fatalf("escapePath = %q", got)
	}
}

// TestMissingRepoAndFileStatuses checks how the status codes observed on the Hub map to sentinel errors.
// A missing repository answers 401 with no X-Error-Code, so it is ErrUnauthorized and not ErrNotFound.
func TestMissingRepoAndFileStatuses(t *testing.T) {
	tests := []struct {
		name         string
		status       int
		code         string
		wantNotFound bool
		wantUnauth   bool
		wantGated    bool
	}{
		{"401 without code is unauthorized", 401, "", false, true, false},
		{"403 without code is unauthorized", 403, "", false, true, false},
		{"401 EntryNotFound is not found", 401, "EntryNotFound", true, false, false},
		{"401 RevisionNotFound is not found", 401, "RevisionNotFound", true, false, false},
		{"401 RepoNotFound is not found", 401, "RepoNotFound", true, false, false},
		{"404 EntryNotFound is not found", 404, "EntryNotFound", true, false, false},
		{"401 GatedRepo is gated, not unauthorized", 401, "GatedRepo", false, false, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if tt.code != "" {
					w.Header().Set("X-Error-Code", tt.code)
				}
				w.WriteHeader(tt.status)
			}))
			_, err := c.ListFiles(context.Background(), "a/b", "main")
			if err == nil {
				t.Fatal("ListFiles: want error")
			}
			if got := errors.Is(err, ErrNotFound); got != tt.wantNotFound {
				t.Fatalf("errors.Is(ErrNotFound) = %v, want %v (%v)", got, tt.wantNotFound, err)
			}
			if got := errors.Is(err, ErrUnauthorized); got != tt.wantUnauth {
				t.Fatalf("errors.Is(ErrUnauthorized) = %v, want %v (%v)", got, tt.wantUnauth, err)
			}
			if got := errors.Is(err, ErrGated); got != tt.wantGated {
				t.Fatalf("errors.Is(ErrGated) = %v, want %v (%v)", got, tt.wantGated, err)
			}
		})
	}
}

// TestUnauthorizedKeepsStatusError checks that an unauthorized response is still a *StatusError,
// so that callers that test the status keep working.
func TestUnauthorizedKeepsStatusError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	_, err := c.ModelInfo(context.Background(), "a/b")
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusUnauthorized {
		t.Fatalf("error %v is not a StatusError with 401", err)
	}
	if !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("error %v does not match ErrUnauthorized", err)
	}
}
