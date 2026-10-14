package agentupdate

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// release 는 가짜 GitHub 서버가 내보낼 릴리스 하나다.
type fakeRelease struct {
	tag        string
	prerelease bool
	noSums     bool
	badSum     bool
	files      map[string]string // 묶음 안의 파일(이름 -> 내용). 비어 있으면 deberopa 하나.
	version    string            // 묶음의 deberopa 가 말하는 버전. 비어 있으면 tag 의 버전.
}

func tarball(files map[string]string) []byte {
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		_ = tw.WriteHeader(&tar.Header{Name: name, Mode: 0o755, Size: int64(len(body)), Typeflag: tar.TypeReg})
		_, _ = tw.Write([]byte(body))
	}
	_ = tw.Close()
	_ = zw.Close()
	return buf.Bytes()
}

type env struct {
	u     *Updater
	dir   string
	store string
	link  string
	pkg   string
	hits  map[string]int
}

// newEnv 는 가짜 릴리스 서버와 임시 디렉터리로 갱신기를 만든다. 실행 파일의 내용이 곧 그 버전이다("bin <버전>").
func newEnv(t *testing.T, packaged string, rel fakeRelease) *env {
	t.Helper()
	root := t.TempDir()
	e := &env{dir: filepath.Join(root, "var"), store: filepath.Join(root, "store"), link: filepath.Join(root, "local-bin", "deberopa"), pkg: filepath.Join(root, "usr-bin-deberopa"), hits: map[string]int{}}
	if packaged != "" {
		if err := os.WriteFile(e.pkg, []byte("bin "+packaged), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	ver := rel.version
	if ver == "" {
		ver = strings.TrimPrefix(rel.tag, "v")
	}
	files := rel.files
	if files == nil {
		files = map[string]string{"deberopa": "bin " + ver}
	}
	tgz := tarball(files)
	sum := sha256.Sum256(tgz)
	sumHex := hex.EncodeToString(sum[:])
	if rel.badSum {
		sumHex = strings.Repeat("0", 64)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/repos/nxodh/deberopa/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		e.hits["api"]++
		assets := `[{"name":"deberopa-linux-x64.tar.gz"},{"name":"checksums.txt"}]`
		if rel.noSums {
			assets = `[{"name":"deberopa-linux-x64.tar.gz"}]`
		}
		fmt.Fprintf(w, `{"tag_name":%q,"draft":false,"prerelease":%v,"assets":%s}`, rel.tag, rel.prerelease, assets)
	})
	mux.HandleFunc("/nxodh/deberopa/releases/download/"+rel.tag+"/checksums.txt", func(w http.ResponseWriter, r *http.Request) {
		e.hits["sums"]++
		fmt.Fprintf(w, "%s  deberopa-linux-x64.tar.gz\n", sumHex)
	})
	mux.HandleFunc("/nxodh/deberopa/releases/download/"+rel.tag+"/deberopa-linux-x64.tar.gz", func(w http.ResponseWriter, r *http.Request) {
		e.hits["tar"]++
		_, _ = w.Write(tgz)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	e.u = &Updater{
		Dir: e.dir, Store: e.store, Link: e.link, Packaged: e.pkg, APIBase: srv.URL, DownloadBase: srv.URL,
		Now: func() time.Time { return time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC) },
		Version: func(ctx context.Context, path string) (string, error) {
			b, err := os.ReadFile(path) // 심볼릭 링크도 따라간다
			if err != nil {
				return "", err
			}
			f := strings.Fields(string(b))
			if len(f) != 2 || f[0] != "bin" {
				return "", fmt.Errorf("not a test binary")
			}
			return f[1], nil
		},
	}
	return e
}

func TestCompare(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
		ok   bool
	}{{"0.2.0", "0.2.0", 0, true}, {"0.10.0", "0.9.9", 1, true}, {"1.0.0", "1.0.1", -1, true}, {"0.2", "0.2.0", 0, false}, {"0.2.0-rc1", "0.2.0", 0, false}} {
		got, ok := Compare(c.a, c.b)
		if got != c.want || ok != c.ok {
			t.Errorf("Compare(%q,%q) = %d,%v; want %d,%v", c.a, c.b, got, ok, c.want, c.ok)
		}
	}
}

func TestUpdatesWhenBehind(t *testing.T) {
	e := newEnv(t, "0.2.0", fakeRelease{tag: "v0.3.1"})
	in, err := e.u.Run(context.Background(), Options{Apply: true, Auto: true})
	if err != nil {
		t.Fatal(err)
	}
	if in.Status != StatusUpdated || in.Installed != "0.3.1" || !in.Override || in.Latest != "0.3.1" || in.Packaged != "0.2.0" {
		t.Fatalf("unexpected info: %+v", in)
	}
	target, err := os.Readlink(e.link)
	if err != nil || !strings.HasSuffix(target, filepath.Join("versions", "0.3.1", "deberopa")) {
		t.Fatalf("link = %q, %v", target, err)
	}
	if b, _ := os.ReadFile(e.pkg); string(b) != "bin 0.2.0" {
		t.Fatal("the packaged file was changed")
	}
	if fi, _ := os.Stat(target); fi.Mode().Perm() != 0o755 {
		t.Fatalf("mode = %v", fi.Mode())
	}
	// 같은 버전이면 다시 받지 않는다.
	tarHits := e.hits["tar"]
	in, err = e.u.Run(context.Background(), Options{Apply: true, Auto: true})
	if err != nil || in.Status != StatusUpToDate || e.hits["tar"] != tarHits {
		t.Fatalf("second run: %+v %v (tar hits %d)", in, err, e.hits["tar"])
	}
	// 상태 파일은 데몬이 읽는다.
	if got := e.u.ReadInfo(); got.Installed != "0.3.1" || got.Checked == "" || !got.Auto {
		t.Fatalf("ReadInfo: %+v", got)
	}
}

func TestNeverDowngrades(t *testing.T) {
	e := newEnv(t, "0.4.0", fakeRelease{tag: "v0.3.1"})
	in, err := e.u.Run(context.Background(), Options{Apply: true})
	if err != nil || in.Status != StatusUpToDate || in.Override {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := os.Lstat(e.link); err == nil {
		t.Fatal("a link was created")
	}
	if e.hits["tar"] != 0 {
		t.Fatal("the archive was downloaded")
	}
}

func TestCheckOnlyDoesNotInstall(t *testing.T) {
	e := newEnv(t, "0.2.0", fakeRelease{tag: "v0.3.0"})
	in, err := e.u.Run(context.Background(), Options{})
	if err != nil || in.Status != StatusAvail || in.Latest != "0.3.0" || in.Installed != "0.2.0" {
		t.Fatalf("%+v %v", in, err)
	}
	if e.hits["tar"] != 0 {
		t.Fatal("the archive was downloaded by a check")
	}
}

func TestAutoOffDoesNothing(t *testing.T) {
	e := newEnv(t, "0.2.0", fakeRelease{tag: "v0.3.0"})
	if err := e.u.SetAuto(false); err != nil {
		t.Fatal(err)
	}
	in, err := e.u.Run(context.Background(), Options{Apply: true, Auto: true})
	if err != nil || in.Status != StatusDisabled || in.Auto || e.hits["api"] != 0 {
		t.Fatalf("%+v %v hits=%v", in, err, e.hits)
	}
	// 사용자가 직접 요청한 갱신은 자동 설정과 상관없이 한다.
	in, err = e.u.Run(context.Background(), Options{Apply: true})
	if err != nil || in.Status != StatusUpdated {
		t.Fatalf("manual: %+v %v", in, err)
	}
}

func TestRejectsBadReleases(t *testing.T) {
	cases := map[string]struct {
		rel  fakeRelease
		code string
	}{
		"prerelease":    {fakeRelease{tag: "v0.3.0", prerelease: true}, CodeRelease},
		"odd tag":       {fakeRelease{tag: "v0.3.0-rc1"}, CodeRelease},
		"no checksums":  {fakeRelease{tag: "v0.3.0", noSums: true}, CodeRelease},
		"wrong sum":     {fakeRelease{tag: "v0.3.0", badSum: true}, CodeChecksum},
		"extra file":    {fakeRelease{tag: "v0.3.0", files: map[string]string{"deberopa": "bin 0.3.0", "evil": "x"}}, CodeArchive},
		"path escape":   {fakeRelease{tag: "v0.3.0", files: map[string]string{"../deberopa": "bin 0.3.0"}}, CodeArchive},
		"wrong version": {fakeRelease{tag: "v0.3.0", version: "0.2.9"}, CodeVersion},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, "0.2.0", c.rel)
			in, err := e.u.Run(context.Background(), Options{Apply: true})
			ue, ok := err.(*Error)
			if !ok || ue.Code != c.code {
				t.Fatalf("err = %v, want code %s", err, c.code)
			}
			if in.Status != StatusError || in.Code != c.code {
				t.Fatalf("info: %+v", in)
			}
			if _, err := os.Lstat(e.link); err == nil {
				t.Fatal("the link was changed by a failed update")
			}
			if in.Installed != "0.2.0" {
				t.Fatalf("installed = %q", in.Installed)
			}
		})
	}
}

func TestNetworkFailureIsReportedAndKeepsWorking(t *testing.T) {
	e := newEnv(t, "0.2.0", fakeRelease{tag: "v0.3.0"})
	e.u.APIBase = "http://127.0.0.1:1"
	in, err := e.u.Run(context.Background(), Options{Apply: true, Auto: true})
	if ue, ok := err.(*Error); !ok || ue.Code != CodeNetwork {
		t.Fatalf("err = %v", err)
	}
	if in.Status != StatusError || in.Installed != "0.2.0" {
		t.Fatalf("%+v", in)
	}
}

func TestPackageCatchesUpDropsTheOverride(t *testing.T) {
	e := newEnv(t, "0.2.0", fakeRelease{tag: "v0.3.0"})
	if _, err := e.u.Run(context.Background(), Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	// OS 업데이트가 패키지를 0.3.0 으로 올렸다.
	if err := os.WriteFile(e.pkg, []byte("bin 0.3.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	in, err := e.u.Run(context.Background(), Options{Apply: true})
	if err != nil || in.Override || in.Installed != "0.3.0" || in.Status != StatusUpToDate {
		t.Fatalf("%+v %v", in, err)
	}
	if _, err := os.Lstat(e.link); err == nil {
		t.Fatal("the link was not removed")
	}
	if _, err := os.Stat(filepath.Join(e.store, "versions")); err == nil {
		t.Fatal("downloaded versions were not removed")
	}
}

func TestDoesNotTouchForeignLink(t *testing.T) {
	e := newEnv(t, "0.2.0", fakeRelease{tag: "v0.3.0"})
	if err := os.MkdirAll(filepath.Dir(e.link), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(e.link, []byte("bin 0.1.0"), 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := e.u.Run(context.Background(), Options{Apply: true})
	if ue, ok := err.(*Error); !ok || ue.Code != CodeInstall {
		t.Fatalf("err = %v", err)
	}
	if b, _ := os.ReadFile(e.link); string(b) != "bin 0.1.0" {
		t.Fatal("a user's own file was replaced")
	}
}

func TestNotInstalled(t *testing.T) {
	e := newEnv(t, "", fakeRelease{tag: "v0.3.0"})
	in, err := e.u.Run(context.Background(), Options{Apply: true})
	if err != nil || in.Status != StatusNone {
		t.Fatalf("%+v %v", in, err)
	}
	if e.hits["api"] != 0 {
		t.Fatal("checked a release although Deberopa is not installed")
	}
}

func TestOldVersionsArePruned(t *testing.T) {
	e := newEnv(t, "0.2.0", fakeRelease{tag: "v0.3.0"})
	stale := filepath.Join(e.store, "versions", "0.2.5")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := e.u.Run(context.Background(), Options{Apply: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("stale version kept")
	}
}

func TestChecksumFor(t *testing.T) {
	h := strings.Repeat("ab", 32)
	text := h + "  other.tar.gz\n" + strings.ToUpper(h) + " *deberopa-linux-x64.tar.gz\n"
	got, ok := checksumFor(text, "deberopa-linux-x64.tar.gz")
	if !ok || got != h {
		t.Fatalf("%q %v", got, ok)
	}
	if _, ok := checksumFor("nothing here", "x"); ok {
		t.Fatal("matched nothing")
	}
}
