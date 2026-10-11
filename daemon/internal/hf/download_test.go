package hf

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// resolvePath is the resolve path for the test file in the test repository.
const resolvePath = "/ggml-org/gemma-3-1b-it-GGUF/resolve/main/gemma.gguf"

// fixture is the content that the fake Hub serves, with its expected digest.
type fixture struct {
	content []byte
	sha     string
}

// newFixture makes n bytes of deterministic content.
func newFixture(n int) fixture {
	content := make([]byte, n)
	for i := range content {
		content[i] = byte(i*7 + i/251)
	}
	sum := sha256.Sum256(content)
	return fixture{content: content, sha: hex.EncodeToString(sum[:])}
}

// recorder notes every request that reaches a fake server.
type recorder struct {
	mu     sync.Mutex
	hits   int
	ranges []string
	auths  []string
}

// note records one request.
func (r *recorder) note(req *http.Request) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.hits++
	r.ranges = append(r.ranges, req.Header.Get("Range"))
	r.auths = append(r.auths, req.Header.Get("Authorization"))
}

// snapshot returns the number of requests and the recorded Range headers.
func (r *recorder) snapshot() (int, []string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.hits, append([]string(nil), r.ranges...)
}

// authSnapshot returns the recorded Authorization headers.
func (r *recorder) authSnapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.auths...)
}

// plentyFree reports a large free space.
func plentyFree(string) (uint64, error) {
	return 1 << 40, nil
}

// request returns a valid DownloadRequest for fx, saved under dir.
func request(dir string, fx fixture) DownloadRequest {
	return DownloadRequest{
		Repo:      "ggml-org/gemma-3-1b-it-GGUF",
		Revision:  "main",
		Path:      "gemma.gguf",
		Dest:      filepath.Join(dir, "model.gguf"),
		Size:      int64(len(fx.content)),
		SHA256:    fx.sha,
		FreeBytes: plentyFree,
	}
}

// serve writes content with Range support, as a CDN would.
func serve(w http.ResponseWriter, r *http.Request, content []byte) {
	http.ServeContent(w, r, "gemma.gguf", time.Time{}, bytes.NewReader(content))
}

// mustNotExist fails the test if path exists.
func mustNotExist(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("%s exists (or stat failed: %v), want absent", filepath.Base(path), err)
	}
}

// readFile returns the content of path.
func readFile(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func TestDownloadRedirectDropsTokenOnCDNHop(t *testing.T) {
	fx := newFixture(3000)
	var cdnRec recorder
	cdn := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cdnRec.note(r)
		serve(w, r, fx.content)
	}))
	defer cdn.Close()

	var hubRec recorder
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hubRec.note(r)
		if r.URL.Path != resolvePath {
			http.NotFound(w, r)
			return
		}
		// Go's client compares host names without ports when it strips Authorization on a redirect.
		// So the CDN is reached by another host name ("localhost") that resolves to the same loopback address.
		cdnOtherHost := strings.Replace(cdn.URL, "127.0.0.1", "localhost", 1)
		http.Redirect(w, r, cdnOtherHost+"/xet/blob/abc", http.StatusFound)
	}))
	dir := t.TempDir()
	req := request(dir, fx)
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(readFile(t, req.Dest), fx.content) {
		t.Fatal("downloaded content differs")
	}
	if auths := hubRec.authSnapshot(); len(auths) != 1 || auths[0] != "Bearer "+testToken {
		t.Fatalf("Hub saw Authorization %q, want the bearer token", auths)
	}
	if auths := cdnRec.authSnapshot(); len(auths) != 1 || auths[0] != "" {
		t.Fatalf("CDN saw Authorization %q, want none", auths)
	}
	if _, err := os.Stat(req.Dest + ".part"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal(".part file remains after success")
	}
}

func TestDownloadReplacesExistingDest(t *testing.T) {
	fx := newFixture(100)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	dir := t.TempDir()
	req := request(dir, fx)
	if err := os.WriteFile(req.Dest, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(readFile(t, req.Dest), fx.content) {
		t.Fatal("existing Dest was not replaced")
	}
}

func TestDownloadZeroSize(t *testing.T) {
	fx := newFixture(0)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download of an empty file: %v", err)
	}
	if got := readFile(t, req.Dest); len(got) != 0 {
		t.Fatalf("file has %d bytes, want 0", len(got))
	}
}

func TestDownloadResumesWith206(t *testing.T) {
	fx := newFixture(3000)
	var rec recorder
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.note(r)
		serve(w, r, fx.content)
	}))
	dir := t.TempDir()
	req := request(dir, fx)
	// Only the first 1000 bytes are on disk. Free space must cover the remaining 2000 bytes, not the whole file.
	if err := os.WriteFile(req.Dest+".part", fx.content[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	req.FreeBytes = func(string) (uint64, error) { return 2000, nil }
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(readFile(t, req.Dest), fx.content) {
		t.Fatal("resumed content differs")
	}
	_, ranges := rec.snapshot()
	if len(ranges) != 1 || ranges[0] != "bytes=1000-" {
		t.Fatalf("Range headers = %q, want [bytes=1000-]", ranges)
	}
}

func TestDownloadRestartsWhenRangeIgnored(t *testing.T) {
	fx := newFixture(3000)
	var rec recorder
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.note(r)
		// Ignore Range and send the whole file with 200.
		w.Header().Set("Content-Length", fmt.Sprint(len(fx.content)))
		_, _ = w.Write(fx.content)
	}))
	req := request(t.TempDir(), fx)
	garbage := bytes.Repeat([]byte{0xAA}, 1000)
	if err := os.WriteFile(req.Dest+".part", garbage, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(readFile(t, req.Dest), fx.content) {
		t.Fatal("content after restart differs")
	}
	if _, ranges := rec.snapshot(); len(ranges) != 1 || ranges[0] != "bytes=1000-" {
		t.Fatalf("Range headers = %q", ranges)
	}
}

func TestDownloadCompletePartIsVerifiedWithoutRequest(t *testing.T) {
	fx := newFixture(500)
	var rec recorder
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.note(r)
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	if err := os.WriteFile(req.Dest+".part", fx.content, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if hits, _ := rec.snapshot(); hits != 0 {
		t.Fatalf("requests = %d, want 0", hits)
	}
}

func TestDownloadOversizedPartIsDiscarded(t *testing.T) {
	fx := newFixture(300)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	if err := os.WriteFile(req.Dest+".part", bytes.Repeat([]byte{1}, 400), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if !bytes.Equal(readFile(t, req.Dest), fx.content) {
		t.Fatal("content differs")
	}
}

func TestDownloadRangeNotSatisfiableRemovesPart(t *testing.T) {
	fx := newFixture(300)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Range") != "" {
			w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)
			return
		}
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	if err := os.WriteFile(req.Dest+".part", fx.content[:100], 0o644); err != nil {
		t.Fatal(err)
	}
	err := c.Download(context.Background(), req)
	var se *StatusError
	if !errors.As(err, &se) || se.StatusCode != http.StatusRequestedRangeNotSatisfiable {
		t.Fatalf("error = %v, want StatusError 416", err)
	}
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadChecksumMismatch(t *testing.T) {
	fx := newFixture(3000)
	other := newFixture(3001)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	req.SHA256 = other.sha
	err := c.Download(context.Background(), req)
	if !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrChecksumMismatch", err)
	}
	mustNotExist(t, req.Dest)
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadChecksumAcceptsUppercase(t *testing.T) {
	fx := newFixture(64)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	req.SHA256 = strings.ToUpper(fx.sha)
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download with uppercase sha256: %v", err)
	}
}

func TestDownloadSizeMismatchFromHeader(t *testing.T) {
	fx := newFixture(3000)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	req.Size = 2999
	err := c.Download(context.Background(), req)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("error = %v, want ErrSizeMismatch", err)
	}
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadContentRangeTotalMismatch(t *testing.T) {
	fx := newFixture(3000)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 1000-2999/3005")
		w.Header().Set("Content-Length", "2000")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(fx.content[1000:])
	}))
	req := request(t.TempDir(), fx)
	if err := os.WriteFile(req.Dest+".part", fx.content[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	err := c.Download(context.Background(), req)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("error = %v, want ErrSizeMismatch", err)
	}
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadContentRangeWrongStart(t *testing.T) {
	fx := newFixture(3000)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Range", "bytes 500-2999/3000")
		w.Header().Set("Content-Length", "2500")
		w.WriteHeader(http.StatusPartialContent)
		_, _ = w.Write(fx.content[500:])
	}))
	req := request(t.TempDir(), fx)
	if err := os.WriteFile(req.Dest+".part", fx.content[:1000], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Download(context.Background(), req); err == nil {
		t.Fatal("Download with a wrong Content-Range start: want error")
	}
}

func TestDownloadBodyShorterThanSize(t *testing.T) {
	fx := newFixture(3000)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// No Content-Length: flush so the response is chunked and the short body ends cleanly.
		_, _ = w.Write(fx.content[:2000])
		w.(http.Flusher).Flush()
	}))
	req := request(t.TempDir(), fx)
	err := c.Download(context.Background(), req)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("error = %v, want ErrSizeMismatch", err)
	}
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadBodyLongerThanSize(t *testing.T) {
	fx := newFixture(3000)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(fx.content)
		w.(http.Flusher).Flush()
	}))
	req := request(t.TempDir(), fx)
	req.Size = 2999
	err := c.Download(context.Background(), req)
	if !errors.Is(err, ErrSizeMismatch) {
		t.Fatalf("error = %v, want ErrSizeMismatch", err)
	}
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadInsufficientSpaceSendsNoRequest(t *testing.T) {
	fx := newFixture(3000)
	var rec recorder
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.note(r)
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	req.FreeBytes = func(string) (uint64, error) { return 100, nil }
	if err := c.Download(context.Background(), req); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("error = %v, want ErrInsufficientSpace", err)
	}
	if hits, _ := rec.snapshot(); hits != 0 {
		t.Fatalf("requests = %d, want 0", hits)
	}
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadFreeSpaceMargin(t *testing.T) {
	fx := newFixture(1000)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	const margin = 500
	req := request(t.TempDir(), fx)
	req.MinFreeMargin = margin
	req.FreeBytes = func(string) (uint64, error) { return 1000 + margin - 1, nil }
	if err := c.Download(context.Background(), req); !errors.Is(err, ErrInsufficientSpace) {
		t.Fatalf("one byte short of the margin: error = %v", err)
	}
	req.FreeBytes = func(dir string) (uint64, error) {
		if dir != filepath.Dir(req.Dest) {
			return 0, fmt.Errorf("unexpected dir %q", dir)
		}
		return 1000 + margin, nil
	}
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("exactly the margin: %v", err)
	}
}

func TestDownloadFreeBytesError(t *testing.T) {
	fx := newFixture(10)
	var rec recorder
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.note(r)
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	req.FreeBytes = func(string) (uint64, error) { return 0, errors.New("statfs failed") }
	if err := c.Download(context.Background(), req); err == nil {
		t.Fatal("want error when FreeBytes fails")
	}
	if hits, _ := rec.snapshot(); hits != 0 {
		t.Fatalf("requests = %d, want 0", hits)
	}
}

func TestDownloadStatusErrors(t *testing.T) {
	fx := newFixture(10)
	tests := []struct {
		name    string
		status  int
		headers map[string]string
		want    error
	}{
		{"gated", 401, map[string]string{"X-Error-Code": "GatedRepo"}, ErrGated},
		{"gated 403", 403, map[string]string{"X-Error-Code": "GatedRepo"}, ErrGated},
		{"not found", 404, map[string]string{"X-Error-Code": "EntryNotFound"}, ErrNotFound},
		{"rate limited", 429, map[string]string{"RateLimit": `"resolvers";r=0;t=152`}, ErrRateLimited},
		{"server error", 500, nil, nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				fmt.Fprint(w, "body with "+testToken)
			}))
			req := request(t.TempDir(), fx)
			err := c.Download(context.Background(), req)
			if err == nil {
				t.Fatal("want error")
			}
			if tt.want != nil && !errors.Is(err, tt.want) {
				t.Fatalf("error %v does not match %v", err, tt.want)
			}
			if strings.Contains(err.Error(), testToken) {
				t.Fatalf("error contains the token: %v", err)
			}
			mustNotExist(t, req.Dest)
			mustNotExist(t, req.Dest+".part")
		})
	}
}

func TestDownloadRateLimitResetSeconds(t *testing.T) {
	fx := newFixture(10)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("RateLimit", `"resolvers";r=0;t=152`)
		w.WriteHeader(http.StatusTooManyRequests)
	}))
	err := c.Download(context.Background(), request(t.TempDir(), fx))
	var rl *RateLimitError
	if !errors.As(err, &rl) || rl.ResetSeconds != 152 {
		t.Fatalf("error = %v, want RateLimitError with 152", err)
	}
}

func TestDownloadInvalidRequestsSendNoRequest(t *testing.T) {
	fx := newFixture(10)
	var rec recorder
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.note(r)
		serve(w, r, fx.content)
	}))
	dir := t.TempDir()
	mutate := map[string]func(*DownloadRequest){
		"missing sha256":        func(r *DownloadRequest) { r.SHA256 = "" },
		"short sha256":          func(r *DownloadRequest) { r.SHA256 = "abc" },
		"non-hex sha256":        func(r *DownloadRequest) { r.SHA256 = strings.Repeat("g", 64) },
		"nil FreeBytes":         func(r *DownloadRequest) { r.FreeBytes = nil },
		"negative size":         func(r *DownloadRequest) { r.Size = -1 },
		"empty dest":            func(r *DownloadRequest) { r.Dest = "" },
		"dest is a directory":   func(r *DownloadRequest) { r.Dest = dir + "/" },
		"path traversal":        func(r *DownloadRequest) { r.Path = "../secret" },
		"absolute path":         func(r *DownloadRequest) { r.Path = "/etc/passwd" },
		"inner dot-dot":         func(r *DownloadRequest) { r.Path = "sub/../gemma.gguf" },
		"double slash":          func(r *DownloadRequest) { r.Path = "sub//gemma.gguf" },
		"backslash":             func(r *DownloadRequest) { r.Path = `sub\gemma.gguf` },
		"control character":     func(r *DownloadRequest) { r.Path = "gemma\x01.gguf" },
		"empty path":            func(r *DownloadRequest) { r.Path = "" },
		"revision dot-dot":      func(r *DownloadRequest) { r.Revision = ".." },
		"revision absolute":     func(r *DownloadRequest) { r.Revision = "/main" },
		"repo traversal":        func(r *DownloadRequest) { r.Repo = "../x" },
		"repo three segments":   func(r *DownloadRequest) { r.Repo = "a/b/c" },
		"repo with query chars": func(r *DownloadRequest) { r.Repo = "a/b?x" },
	}
	for name, fn := range mutate {
		t.Run(name, func(t *testing.T) {
			req := request(dir, fx)
			fn(&req)
			err := c.Download(context.Background(), req)
			if !errors.Is(err, ErrInvalidInput) {
				t.Fatalf("error = %v, want ErrInvalidInput", err)
			}
		})
	}
	if hits, _ := rec.snapshot(); hits != 0 {
		t.Fatalf("requests = %d, want 0", hits)
	}
}

func TestDownloadProgressIsRare(t *testing.T) {
	fx := newFixture(3 << 20)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	var calls []int64
	req := request(t.TempDir(), fx)
	req.Progress = func(done, total int64) {
		if total != int64(len(fx.content)) {
			t.Errorf("total = %d", total)
		}
		calls = append(calls, done)
	}
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	if len(calls) < 2 || len(calls) > 6 {
		t.Fatalf("progress calls = %v, want a few", calls)
	}
	for i := 1; i < len(calls); i++ {
		if calls[i] < calls[i-1] {
			t.Fatalf("progress went backwards: %v", calls)
		}
	}
	if calls[len(calls)-1] != int64(len(fx.content)) {
		t.Fatalf("last progress = %d, want %d", calls[len(calls)-1], len(fx.content))
	}
}

func TestDownloadCancelKeepsPartAndResumes(t *testing.T) {
	fx := newFixture(3 << 20)
	var rec recorder
	var stall atomic.Bool
	stall.Store(true)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.note(r)
		if !stall.Load() {
			serve(w, r, fx.content)
			return
		}
		// Send 2 MiB, then hold the connection until the client goes away.
		_, _ = w.Write(fx.content[:2<<20])
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	}))
	dir := t.TempDir()
	req := request(dir, fx)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var cancelled atomic.Bool
	req.Progress = func(done, total int64) {
		if done >= 1<<20 && cancelled.CompareAndSwap(false, true) {
			cancel()
		}
	}
	err := c.Download(ctx, req)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	mustNotExist(t, req.Dest)
	info, statErr := os.Stat(req.Dest + ".part")
	if statErr != nil {
		t.Fatalf("partial file missing after cancel: %v", statErr)
	}
	if info.Size() < 1<<20 || info.Size() > 2<<20 {
		t.Fatalf("partial file has %d bytes, want between 1 MiB and 2 MiB", info.Size())
	}
	kept := info.Size()

	stall.Store(false)
	req.Progress = nil
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("resume after cancel: %v", err)
	}
	if !bytes.Equal(readFile(t, req.Dest), fx.content) {
		t.Fatal("resumed content differs")
	}
	_, ranges := rec.snapshot()
	last := ranges[len(ranges)-1]
	if last != fmt.Sprintf("bytes=%d-", kept) {
		t.Fatalf("resume Range = %q, want bytes=%d-", last, kept)
	}
}

func TestDownloadChecksumMismatchAfterResumeRemovesPart(t *testing.T) {
	// A checksum mismatch after a resume must remove the partial file, so the next attempt starts from zero.
	fx := newFixture(2000)
	bad := newFixture(2001)
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	req.SHA256 = bad.sha
	if err := os.WriteFile(req.Dest+".part", fx.content[:500], 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Download(context.Background(), req); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatalf("error = %v, want ErrChecksumMismatch", err)
	}
	mustNotExist(t, req.Dest+".part")
}

func TestDownloadPathsAreEscaped(t *testing.T) {
	fx := newFixture(10)
	var gotPath string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.EscapedPath()
		serve(w, r, fx.content)
	}))
	req := request(t.TempDir(), fx)
	req.Path = "sub dir/model #1.gguf"
	req.Revision = "refs/pr/1"
	if err := c.Download(context.Background(), req); err != nil {
		t.Fatalf("Download: %v", err)
	}
	want := "/ggml-org/gemma-3-1b-it-GGUF/resolve/refs/pr/1/sub%20dir/model%20%231.gguf"
	if gotPath != want {
		t.Fatalf("path = %q, want %q", gotPath, want)
	}
}
