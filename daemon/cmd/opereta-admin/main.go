// opereta-admin 은 operetad 가 systemd-run --pipe 로 부르는 권한 있는 파일 쓰기 헬퍼다.
//
// 쓸 수 있는 경로는 internal/admin 의 허용 목록으로만 제한한다. 사용법:
//
//	opereta-admin write PATH [--secret]   (내용은 표준 입력)
//	opereta-admin remove PATH
//	opereta-admin authkeys-read USER
//	opereta-admin authkeys-write USER     (내용은 표준 입력)
//
// 결과는 표준 출력의 마지막 줄 OPERETA_ADMIN_RESULT=ok 또는 OPERETA_ADMIN_RESULT=error:<코드> 로 알린다.
// 환경 변수 OPERETA_ADMIN_ROOT 는 시험용이다. 값이 있으면 모든 경로 앞에 붙이고 소유자 변경을 하지 않는다.
package main

import (
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/admin"
)

func main() {
	os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr, os.Getenv("OPERETA_ADMIN_ROOT")))
}

func fail(stdout, stderr io.Writer, code, format string, a ...any) int {
	fmt.Fprintf(stderr, "opereta-admin: "+format+"\n", a...)
	fmt.Fprintf(stdout, "%s%s%s\n", "\n"+admin.ResultPrefix, "error:", code)
	return 1
}

func ok(stdout io.Writer) int {
	fmt.Fprintf(stdout, "%sok\n", "\n"+admin.ResultPrefix)
	return 0
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer, root string) int {
	if len(args) < 2 {
		return fail(stdout, stderr, admin.CodeFailed, "usage: opereta-admin write|remove|authkeys-read|authkeys-write ...")
	}
	switch args[0] {
	case "write":
		secret := false
		for _, a := range args[2:] {
			if a == "--secret" {
				secret = true
			} else {
				return fail(stdout, stderr, admin.CodeFailed, "unknown option %q", a)
			}
		}
		return doWrite(args[1], secret, stdin, stdout, stderr, root)
	case "remove":
		return doRemove(args[1], stdout, stderr, root)
	case "authkeys-read":
		return doAuthKeysRead(args[1], stdout, stderr, root)
	case "authkeys-write":
		return doAuthKeysWrite(args[1], stdin, stdout, stderr, root)
	}
	return fail(stdout, stderr, admin.CodeFailed, "unknown command %q", args[0])
}

func readLimited(r io.Reader) ([]byte, error) {
	data, err := io.ReadAll(io.LimitReader(r, admin.MaxContent+1))
	if err != nil {
		return nil, err
	}
	if len(data) > admin.MaxContent {
		return nil, errors.New("too large")
	}
	return data, nil
}

func doWrite(p string, secret bool, stdin io.Reader, stdout, stderr io.Writer, root string) int {
	if !admin.AllowedPath(p) {
		return fail(stdout, stderr, admin.CodeInvalidPath, "writing %s is not allowed", p)
	}
	data, err := readLimited(stdin)
	if err != nil {
		code := admin.CodeFailed
		if err.Error() == "too large" {
			code = admin.CodeTooLarge
		}
		return fail(stdout, stderr, code, "reading the content: %v", err)
	}
	target := filepath.Join(root, p)
	mode := fs.FileMode(0o644)
	group := ""
	if secret {
		mode, group = 0o640, "systemd-network"
	}
	if err := atomicWrite(target, data, mode, root == "", -1, groupID(group, root)); err != nil {
		return fail(stdout, stderr, admin.CodeFailed, "writing %s: %v", p, err)
	}
	return ok(stdout)
}

func doRemove(p string, stdout, stderr io.Writer, root string) int {
	if !admin.AllowedPath(p) {
		return fail(stdout, stderr, admin.CodeInvalidPath, "removing %s is not allowed", p)
	}
	if err := os.Remove(filepath.Join(root, p)); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail(stdout, stderr, admin.CodeFailed, "removing %s: %v", p, err)
	}
	return ok(stdout)
}

func groupID(name, root string) int {
	if name == "" || root != "" {
		return -1
	}
	g, err := user.LookupGroup(name)
	if err != nil {
		return -1
	}
	id, err := strconv.Atoi(g.Gid)
	if err != nil {
		return -1
	}
	return id
}

// atomicWrite 는 같은 디렉터리의 임시 파일에 쓰고 이름을 바꾼다. 소유자를 바꾸지 못하면(실제 시스템에서) 실패한다.
func atomicWrite(target string, data []byte, mode fs.FileMode, chown bool, uid, gid int) error {
	dir := filepath.Dir(target)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if li, err := os.Lstat(target); err == nil && li.Mode()&fs.ModeSymlink != 0 {
		return errors.New("the target is a symbolic link")
	}
	tmp, err := os.CreateTemp(dir, ".opereta-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer func() {
		if err != nil {
			tmp.Close()
			os.Remove(name)
		}
	}()
	if _, err = tmp.Write(data); err != nil {
		return err
	}
	if err = tmp.Chmod(mode); err != nil {
		return err
	}
	if chown && (uid >= 0 || gid >= 0) {
		if err = tmp.Chown(uid, gid); err != nil {
			return err
		}
	}
	if err = tmp.Sync(); err != nil {
		return err
	}
	if err = tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, target)
}

// lookupUser 는 로그인 사용자(uid 1000 이상, root 아님)의 uid, gid, 홈을 돌려준다.
func lookupUser(name string) (uid, gid int, home string, err error) {
	if !admin.ValidUser(name) {
		return 0, 0, "", errors.New("invalid user name")
	}
	u, err := user.Lookup(name)
	if err != nil {
		return 0, 0, "", err
	}
	uid, err = strconv.Atoi(u.Uid)
	if err != nil || uid < 1000 {
		return 0, 0, "", errors.New("only login accounts can have keys managed here")
	}
	gid, _ = strconv.Atoi(u.Gid)
	if !filepath.IsAbs(u.HomeDir) || strings.Contains(u.HomeDir, "..") {
		return 0, 0, "", errors.New("the home directory is not usable")
	}
	return uid, gid, u.HomeDir, nil
}

func authKeysPaths(name, root string) (dir, file string, uid, gid int, err error) {
	uid, gid, home, err := lookupUser(name)
	if err != nil {
		return "", "", 0, 0, err
	}
	dir = filepath.Join(root, home, ".ssh")
	return dir, filepath.Join(dir, "authorized_keys"), uid, gid, nil
}

func doAuthKeysRead(name string, stdout, stderr io.Writer, root string) int {
	_, file, _, _, err := authKeysPaths(name, root)
	if err != nil {
		return fail(stdout, stderr, admin.CodeInvalidUser, "%v", err)
	}
	data, err := os.ReadFile(file)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fail(stdout, stderr, admin.CodeFailed, "reading authorized_keys: %v", err)
	}
	if len(data) > admin.MaxContent {
		return fail(stdout, stderr, admin.CodeTooLarge, "authorized_keys is too large")
	}
	fmt.Fprintf(stdout, "%s%s\n", admin.DataPrefix, base64.StdEncoding.EncodeToString(data))
	return ok(stdout)
}

func doAuthKeysWrite(name string, stdin io.Reader, stdout, stderr io.Writer, root string) int {
	dir, file, uid, gid, err := authKeysPaths(name, root)
	if err != nil {
		return fail(stdout, stderr, admin.CodeInvalidUser, "%v", err)
	}
	data, err := readLimited(stdin)
	if err != nil {
		return fail(stdout, stderr, admin.CodeTooLarge, "reading the key list: %v", err)
	}
	if li, lerr := os.Lstat(dir); lerr == nil && li.Mode()&fs.ModeSymlink != 0 {
		return fail(stdout, stderr, admin.CodeFailed, "the .ssh directory is a symbolic link")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fail(stdout, stderr, admin.CodeFailed, "creating .ssh: %v", err)
	}
	if root == "" {
		if err := os.Chown(dir, uid, gid); err != nil {
			return fail(stdout, stderr, admin.CodeFailed, "owning .ssh: %v", err)
		}
	}
	if err := atomicWrite(file, data, 0o600, root == "", uid, gid); err != nil {
		return fail(stdout, stderr, admin.CodeFailed, "writing authorized_keys: %v", err)
	}
	return ok(stdout)
}
