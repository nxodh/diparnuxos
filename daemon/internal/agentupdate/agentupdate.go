// Package agentupdate 는 Deberopa(코딩 에이전트)의 최신 릴리스를 확인하고 뒤처졌으면 설치한다.
//
// Deberopa 는 opereta-deberopa 패키지가 /usr/bin/deberopa 에 고정 버전으로 싣는다. 이 패키지는 OS 이미지와 함께 갱신되므로
// 새 Deberopa 가 나와도 다음 OS 릴리스까지는 뒤처진다. 이 패키지는 그 사이를 메운다. 패키지의 파일은 건드리지 않고
// /usr/local/lib/opereta/deberopa/versions/<버전>/deberopa 에 설치한 뒤 /usr/local/bin/deberopa 심볼릭 링크로 앞서게 한다
// (PATH 에서 /usr/local/bin 이 /usr/bin 보다 앞이다). 패키지의 버전이 같거나 높아지면 링크와 내려받은 파일을 지운다.
//
// 안전 장치:
//   - 정식 릴리스(vX.Y.Z)만 쓰고, 설치된 버전보다 높을 때만 바꾼다(내리지 않는다).
//   - 릴리스의 checksums.txt 와 SHA-256 이 일치해야 한다. 묶음에는 deberopa 파일 하나만 있어야 한다.
//   - 내려받은 실행 파일은 root 가 아니라 nobody 권한으로만 실행해 버전을 확인한다.
//   - 새 파일이 완성되기 전에는 링크를 바꾸지 않는다. 실패하면 이전 Deberopa 가 그대로 쓰인다.
//   - 자동 갱신은 끌 수 있다(SetAuto). 끄면 타이머가 아무것도 하지 않는다.
//
// 주의: 체크섬은 같은 GitHub 릴리스에서 받으므로 릴리스 계정이 탈취되면 막지 못한다. Opereta 가 따로 서명한 것이 아니다.
package agentupdate

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// 기본값이다.
const (
	DefaultRepo     = "nxodh/deberopa"
	DefaultDir      = "/var/lib/opereta/deberopa"
	DefaultStore    = "/usr/local/lib/opereta/deberopa"
	DefaultLink     = "/usr/local/bin/deberopa"
	DefaultPackaged = "/usr/bin/deberopa"
	assetName       = "deberopa-linux-x64.tar.gz"
	checksumsName   = "checksums.txt"
	maxArchive      = 400 << 20
	maxChecksums    = 1 << 20
	maxBinary       = 600 << 20
)

// 상태 값이다.
const (
	StatusUpToDate = "up_to_date"
	StatusUpdated  = "updated"
	StatusAvail    = "update_available"
	StatusDisabled = "disabled"
	StatusError    = "error"
	StatusNone     = "not_installed"
)

// 오류 코드다.
const (
	CodeNetwork   = "network"
	CodeRelease   = "bad_release"
	CodeChecksum  = "checksum_mismatch"
	CodeArchive   = "bad_archive"
	CodeVersion   = "version_mismatch"
	CodeInstall   = "install_failed"
	CodeBusy      = "busy"
	CodeNoPackage = "not_installed"
)

// Error 는 코드를 가진 오류다.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return e.Code + ": " + e.Message }

func errf(code, format string, a ...any) *Error { return &Error{code, fmt.Sprintf(format, a...)} }

// Info 는 state.json 과 설정이 합쳐진 현재 상태다. 데몬과 GUI 가 읽는다.
type Info struct {
	// Auto 는 자동 갱신이 켜져 있다는 뜻이다(기본 켜짐).
	Auto bool `json:"auto"`
	// Installed 는 실제로 실행되는 deberopa 의 버전이다. 없으면 비어 있다.
	Installed string `json:"installed,omitempty"`
	// Packaged 는 opereta-deberopa 패키지의 버전이다.
	Packaged string `json:"packaged,omitempty"`
	// Latest 는 마지막으로 확인한 GitHub 의 최신 정식 릴리스다.
	Latest string `json:"latest,omitempty"`
	// Override 가 참이면 /usr/local/bin 의 링크가 패키지보다 앞서 실행된다.
	Override bool   `json:"override"`
	Status   string `json:"status"`
	Checked  string `json:"checked,omitempty"`
	Code     string `json:"code,omitempty"`
	Message  string `json:"message,omitempty"`
}

// Updater 는 Deberopa 갱신 동작이다. 영(0) 값 필드는 기본값을 쓴다.
type Updater struct {
	Repo string
	// Dir 은 상태와 설정을 두는 디렉터리다(root 와 데몬만 읽는다).
	Dir string
	// Store 는 내려받은 실행 파일을 두는 디렉터리다. 모든 사용자가 실행하므로 누구나 지나갈 수 있어야 하고,
	// 그래서 0750 인 /var/lib/opereta 밖에 둔다.
	Store    string
	Link     string
	Packaged string
	// APIBase 와 DownloadBase 는 시험이 바꾼다. 기본은 GitHub 다.
	APIBase      string
	DownloadBase string
	HTTP         *http.Client
	// Version 은 실행 파일의 --version 출력을 돌려준다. nil 이면 nobody 권한으로 실행한다.
	Version func(ctx context.Context, path string) (string, error)
	Now     func() time.Time
}

func (u *Updater) repo() string {
	if u.Repo != "" {
		return u.Repo
	}
	return DefaultRepo
}

func (u *Updater) dir() string {
	if u.Dir != "" {
		return u.Dir
	}
	return DefaultDir
}

func (u *Updater) store() string {
	if u.Store != "" {
		return u.Store
	}
	return DefaultStore
}

func (u *Updater) link() string {
	if u.Link != "" {
		return u.Link
	}
	return DefaultLink
}

func (u *Updater) packaged() string {
	if u.Packaged != "" {
		return u.Packaged
	}
	return DefaultPackaged
}

func (u *Updater) apiBase() string {
	if u.APIBase != "" {
		return strings.TrimRight(u.APIBase, "/")
	}
	return "https://api.github.com"
}

func (u *Updater) downloadBase() string {
	if u.DownloadBase != "" {
		return strings.TrimRight(u.DownloadBase, "/")
	}
	return "https://github.com"
}

func (u *Updater) client() *http.Client {
	if u.HTTP != nil {
		return u.HTTP
	}
	return &http.Client{Timeout: 15 * time.Minute}
}

func (u *Updater) now() time.Time {
	if u.Now != nil {
		return u.Now()
	}
	return time.Now()
}

func (u *Updater) statePath() string    { return filepath.Join(u.dir(), "state.json") }
func (u *Updater) settingsPath() string { return filepath.Join(u.dir(), "settings.json") }
func (u *Updater) versionDir(v string) string {
	return filepath.Join(u.store(), "versions", v)
}

type settings struct {
	Auto *bool `json:"auto,omitempty"`
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// AutoEnabled 는 자동 갱신 설정이다. 설정 파일이 없으면 켜져 있다.
func (u *Updater) AutoEnabled() bool {
	var s settings
	if err := readJSON(u.settingsPath(), &s); err != nil || s.Auto == nil {
		return true
	}
	return *s.Auto
}

// SetAuto 는 자동 갱신을 켜거나 끈다.
func (u *Updater) SetAuto(on bool) error {
	return writeJSON(u.settingsPath(), settings{Auto: &on})
}

// ReadInfo 는 마지막 상태를 파일에서 읽는다(네트워크와 실행 파일은 쓰지 않는다). 데몬이 매번 불러도 된다.
func (u *Updater) ReadInfo() Info {
	var in Info
	_ = readJSON(u.statePath(), &in)
	in.Auto = u.AutoEnabled()
	if in.Status == "" {
		in.Status = StatusUpToDate
		if _, err := os.Stat(u.packaged()); err != nil {
			in.Status = StatusNone
		}
		in.Checked = ""
	}
	return in
}

// lock 은 동시에 두 갱신이 돌지 않게 한다.
func (u *Updater) lock() (func(), error) {
	if err := os.MkdirAll(u.dir(), 0o755); err != nil {
		return nil, errf(CodeInstall, "%v", err)
	}
	f, err := os.OpenFile(filepath.Join(u.dir(), "lock"), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, errf(CodeInstall, "%v", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, errf(CodeBusy, "another Deberopa update is running")
	}
	return func() { _ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN); f.Close() }, nil
}

var tagRe = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)$`)
var verRe = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)$`)

// Compare 는 a 와 b(X.Y.Z)를 비교해 -1, 0, 1 을 돌려준다. 형식이 틀리면 ok 가 거짓이다.
func Compare(a, b string) (int, bool) {
	pa, pb := verRe.FindStringSubmatch(a), verRe.FindStringSubmatch(b)
	if pa == nil || pb == nil {
		return 0, false
	}
	for i := 1; i <= 3; i++ {
		x, _ := strconv.Atoi(pa[i])
		y, _ := strconv.Atoi(pb[i])
		switch {
		case x < y:
			return -1, true
		case x > y:
			return 1, true
		}
	}
	return 0, true
}

// runVersion 은 실행 파일의 --version 을 nobody 권한으로 실행한다.
func (u *Updater) runVersion(ctx context.Context, path string) (string, error) {
	if u.Version != nil {
		return u.Version(ctx, path)
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	args := []string{"--reuid=65534", "--regid=65534", "--clear-groups", "--no-new-privs", path, "--version"}
	cmd := exec.CommandContext(ctx, "setpriv", args...)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "HOME=/nonexistent", "LANG=C"}
	out, err := cmd.Output()
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(out))
	if len(fields) == 0 {
		return "", errors.New("empty version output")
	}
	return strings.TrimPrefix(fields[len(fields)-1], "v"), nil
}

// installedVersions 는 실제로 실행되는 버전과 패키지 버전을 돌려준다.
func (u *Updater) installedVersions(ctx context.Context) (effective, packaged string, override bool) {
	if _, err := os.Stat(u.packaged()); err == nil {
		packaged, _ = u.runVersion(ctx, u.packaged())
	}
	effective = packaged
	if target, err := os.Readlink(u.link()); err == nil && strings.HasPrefix(target, u.store()+string(os.PathSeparator)) {
		if v, err := u.runVersion(ctx, u.link()); err == nil {
			effective, override = v, true
		}
	}
	return
}

type release struct {
	TagName    string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	Assets     []struct {
		Name string `json:"name"`
	} `json:"assets"`
}

// latest 는 GitHub 의 최신 정식 릴리스의 버전(X.Y.Z)을 돌려준다.
func (u *Updater) latest(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.apiBase()+"/repos/"+u.repo()+"/releases/latest", nil)
	if err != nil {
		return "", errf(CodeNetwork, "%v", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "opereta-update")
	resp, err := u.client().Do(req)
	if err != nil {
		return "", errf(CodeNetwork, "cannot reach the release server: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", errf(CodeNetwork, "release server answered %s", resp.Status)
	}
	var rel release
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&rel); err != nil {
		return "", errf(CodeRelease, "unreadable release information: %v", err)
	}
	m := tagRe.FindStringSubmatch(rel.TagName)
	if m == nil || rel.Draft || rel.Prerelease {
		return "", errf(CodeRelease, "the latest release %q is not a stable vX.Y.Z release", rel.TagName)
	}
	var hasTar, hasSums bool
	for _, a := range rel.Assets {
		hasTar = hasTar || a.Name == assetName
		hasSums = hasSums || a.Name == checksumsName
	}
	if !hasTar || !hasSums {
		return "", errf(CodeRelease, "release %s has no %s and %s", rel.TagName, assetName, checksumsName)
	}
	return strings.TrimPrefix(rel.TagName, "v"), nil
}

func (u *Updater) fetch(ctx context.Context, url string, limit int64, dst io.Writer) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return errf(CodeNetwork, "%v", err)
	}
	req.Header.Set("User-Agent", "opereta-update")
	resp, err := u.client().Do(req)
	if err != nil {
		return errf(CodeNetwork, "download failed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return errf(CodeNetwork, "download of %s answered %s", filepath.Base(url), resp.Status)
	}
	n, err := io.Copy(dst, io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return errf(CodeNetwork, "download interrupted: %v", err)
	}
	if n > limit {
		return errf(CodeArchive, "%s is larger than %d MiB", filepath.Base(url), limit>>20)
	}
	return nil
}

// checksumFor 는 checksums.txt(sha256sum 형식)에서 name 의 해시를 찾는다.
func checksumFor(text, name string) (string, bool) {
	sc := bufio.NewScanner(strings.NewReader(text))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			h := strings.ToLower(f[0])
			if _, err := hex.DecodeString(h); err == nil {
				return h, true
			}
		}
	}
	return "", false
}

// extract 는 묶음에 deberopa 하나만 있는지 확인하고 dst 로 푼다.
func extract(archive, dst string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	zr, err := gzip.NewReader(f)
	if err != nil {
		return errf(CodeArchive, "not a gzip file: %v", err)
	}
	tr := tar.NewReader(zr)
	var found bool
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return errf(CodeArchive, "unreadable archive: %v", err)
		}
		name := strings.TrimPrefix(h.Name, "./")
		if name != "deberopa" || h.Typeflag != tar.TypeReg || found {
			return errf(CodeArchive, "the archive must contain only the file 'deberopa' (found %q)", h.Name)
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
		if err != nil {
			return errf(CodeInstall, "%v", err)
		}
		n, err := io.Copy(out, io.LimitReader(tr, maxBinary+1))
		cerr := out.Close()
		if err != nil || cerr != nil {
			return errf(CodeInstall, "writing deberopa: %v %v", err, cerr)
		}
		if n > maxBinary {
			return errf(CodeArchive, "deberopa is larger than %d MiB", maxBinary>>20)
		}
		found = true
	}
	if !found {
		return errf(CodeArchive, "the archive has no 'deberopa' file")
	}
	return nil
}

func (u *Updater) save(in Info) Info {
	in.Checked = u.now().UTC().Format(time.RFC3339)
	in.Auto = u.AutoEnabled()
	_ = writeJSON(u.statePath(), in)
	return in
}

// Options 는 Run 의 선택이다.
type Options struct {
	// Apply 가 거짓이면 확인만 한다(설치하지 않는다).
	Apply bool
	// Auto 는 타이머가 부른 것이다. 자동 갱신이 꺼져 있으면 아무것도 하지 않는다.
	Auto bool
}

// Run 은 최신 릴리스를 확인하고, Apply 이면 뒤처진 경우에 설치한다. 결과는 state.json 에도 남는다.
// 오류가 나도 이전에 설치된 Deberopa 는 그대로 쓸 수 있다.
func (u *Updater) Run(ctx context.Context, opt Options) (Info, error) {
	if opt.Auto && !u.AutoEnabled() {
		in := u.ReadInfo()
		in.Status = StatusDisabled
		return in, nil
	}
	unlock, err := u.lock()
	if err != nil {
		return u.ReadInfo(), err
	}
	defer unlock()

	eff, pkg, override := u.installedVersions(ctx)
	prev := u.ReadInfo()
	in := Info{Installed: eff, Packaged: pkg, Override: override, Latest: prev.Latest}

	// 패키지가 따라잡았으면 내려받은 파일은 필요 없다.
	if override {
		if c, ok := Compare(pkg, eff); ok && c >= 0 {
			u.dropOverride()
			in.Installed, in.Override = pkg, false
			eff, override = pkg, false
		}
	}
	if eff == "" {
		in.Status, in.Code, in.Message = StatusNone, CodeNoPackage, "Deberopa is not installed"
		return u.save(in), nil
	}

	latest, err := u.latest(ctx)
	if err != nil {
		var e *Error
		errors.As(err, &e)
		in.Status, in.Code, in.Message = StatusError, e.Code, e.Message
		return u.save(in), err
	}
	in.Latest = latest
	c, ok := Compare(latest, eff)
	if !ok || c <= 0 {
		in.Status = StatusUpToDate
		return u.save(in), nil
	}
	if !opt.Apply {
		in.Status = StatusAvail
		return u.save(in), nil
	}
	if err := u.install(ctx, latest); err != nil {
		var e *Error
		errors.As(err, &e)
		in.Status, in.Code, in.Message = StatusError, e.Code, e.Message
		return u.save(in), err
	}
	in.Installed, in.Override, in.Status = latest, true, StatusUpdated
	u.prune(latest)
	return u.save(in), nil
}

// install 은 version 을 받아 검증하고 링크를 바꾼다.
func (u *Updater) install(ctx context.Context, version string) error {
	if err := os.MkdirAll(u.store(), 0o755); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	tmp, err := os.MkdirTemp(u.store(), "download-")
	if err != nil {
		return errf(CodeInstall, "%v", err)
	}
	defer os.RemoveAll(tmp)
	if err := os.Chmod(tmp, 0o755); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	base := u.downloadBase() + "/" + u.repo() + "/releases/download/v" + version + "/"

	var sums strings.Builder
	if err := u.fetch(ctx, base+checksumsName, maxChecksums, &sums); err != nil {
		return err
	}
	want, ok := checksumFor(sums.String(), assetName)
	if !ok {
		return errf(CodeChecksum, "%s has no entry for %s", checksumsName, assetName)
	}
	archive := filepath.Join(tmp, assetName)
	af, err := os.OpenFile(archive, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return errf(CodeInstall, "%v", err)
	}
	h := sha256.New()
	ferr := u.fetch(ctx, base+assetName, maxArchive, io.MultiWriter(af, h))
	cerr := af.Close()
	if ferr != nil {
		return ferr
	}
	if cerr != nil {
		return errf(CodeInstall, "%v", cerr)
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return errf(CodeChecksum, "SHA-256 of %s is %s, the release says %s", assetName, got, want)
	}
	bin := filepath.Join(tmp, "deberopa")
	if err := extract(archive, bin); err != nil {
		return err
	}
	got, err := u.runVersion(ctx, bin)
	if err != nil {
		return errf(CodeVersion, "the new deberopa did not run: %v", err)
	}
	if got != version {
		return errf(CodeVersion, "the new deberopa reports %q, expected %q", got, version)
	}

	dst := u.versionDir(version)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	_ = os.RemoveAll(dst)
	staged := dst + ".new"
	_ = os.RemoveAll(staged)
	if err := os.MkdirAll(staged, 0o755); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	if err := os.Rename(bin, filepath.Join(staged, "deberopa")); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	if err := os.Rename(staged, dst); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	return u.point(filepath.Join(dst, "deberopa"))
}

// point 는 /usr/local/bin/deberopa 링크를 target 으로 바꾼다. 링크가 아닌 파일이 있으면 건드리지 않는다.
func (u *Updater) point(target string) error {
	if fi, err := os.Lstat(u.link()); err == nil && fi.Mode()&os.ModeSymlink == 0 {
		return errf(CodeInstall, "%s exists and is not a link managed by Opereta; leaving it alone", u.link())
	}
	if err := os.MkdirAll(filepath.Dir(u.link()), 0o755); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	tmp := u.link() + ".opereta-new"
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return errf(CodeInstall, "%v", err)
	}
	if err := os.Rename(tmp, u.link()); err != nil {
		_ = os.Remove(tmp)
		return errf(CodeInstall, "%v", err)
	}
	return nil
}

// dropOverride 는 링크와 내려받은 파일을 지운다. 링크가 Opereta 가 만든 것일 때만 지운다.
func (u *Updater) dropOverride() {
	if target, err := os.Readlink(u.link()); err == nil && strings.HasPrefix(target, u.store()+string(os.PathSeparator)) {
		_ = os.Remove(u.link())
	}
	_ = os.RemoveAll(filepath.Join(u.store(), "versions"))
}

// prune 은 keep 한 버전만 남기고 내려받은 버전 디렉터리를 지운다.
func (u *Updater) prune(keep ...string) {
	entries, err := os.ReadDir(filepath.Join(u.store(), "versions"))
	if err != nil {
		return
	}
	set := map[string]bool{}
	for _, k := range keep {
		set[k] = true
	}
	var names []string
	for _, e := range entries {
		names = append(names, e.Name())
	}
	sort.Strings(names)
	for _, n := range names {
		if !set[n] {
			_ = os.RemoveAll(filepath.Join(u.store(), "versions", n))
		}
	}
}
