package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/client"
	"github.com/nxodh/diparnuxos/daemon/internal/vedecaps"
)

// connect 는 다른 프로그램을 이 서버에 연결하는 설정을 만든다. 지금은 Vedecaps 만 있다.
func (c *cli) connect(args []string) int {
	if len(args) == 0 || args[0] != "vedecaps" {
		return c.usage("usage: diparnux connect vedecaps [--remote] [--url URL] [--model ID] [--write] [--mcp]", "connect")
	}
	return c.connectVedecaps(args[1:])
}

func (c *cli) connectVedecaps(args []string) int {
	fs := newFlags("connect vedecaps")
	remote := fs.Bool("remote", false, "use this computer's LAN address (for Vedecaps on another computer)")
	baseURL := fs.String("url", "", "base URL to use instead")
	modelID := fs.String("model", "", "model name (default: the running or default model)")
	keyEnv := fs.String("key-env", vedecaps.DefaultKeyEnv, "environment variable that holds the API key")
	write := fs.Bool("write", false, "merge the settings into the Vedecaps config of the current user")
	makeDefault := fs.Bool("make-default", false, "make Diparnux the default model even when another default is set")
	withMCP := fs.Bool("mcp", false, "also register the read-only Diparnux MCP server (diparnux mcp)")
	pos, code, ok := c.parse(fs, "connect vedecaps", args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return c.usage("connect vedecaps takes no arguments", "connect vedecaps")
	}
	if *remote && *baseURL != "" {
		return c.usage("use either --remote or --url", "connect vedecaps")
	}

	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	conn, _, err := c.api.Connection(ctx)
	if err != nil {
		return c.fail(err)
	}

	// 접속 주소와 키가 필요한지를 정한다. 이 컴퓨터의 주소는 키 면제(local_no_key)를 따르고, 다른 주소는 항상 키가 필요하다.
	base, needsKey := conn.LocalURL, conn.LocalKeyRequired
	switch {
	case *baseURL != "":
		base = strings.TrimRight(*baseURL, "/")
		needsKey = !isLocalURL(base) || conn.LocalKeyRequired
	case *remote:
		if len(conn.Addresses) == 0 {
			c.errorLine("this computer has no LAN address to give out: turn on LAN exposure first (diparnux config set lan on)")
			return exitFailure
		}
		base, needsKey = conn.Addresses[0].URL, true
	}

	model := *modelID
	if model == "" {
		model = conn.ModelID
	}
	if model == "" && len(conn.Models) > 0 {
		model = conn.Models[0]
	}
	slotCtx := conn.SlotCtx
	if slotCtx == 0 {
		// 서버가 멈춰 있으면 시작했을 때의 계획에서 가져온다.
		if pr, _, perr := c.api.Plan(ctx, model, client.Overrides{}); perr == nil {
			slotCtx = pr.Plan.SlotCtx
		}
	}

	frag, err := vedecaps.Fragment(vedecaps.Input{BaseURL: base, ModelID: model, SlotCtx: slotCtx, NeedsKey: needsKey, KeyEnv: *keyEnv, MCP: *withMCP, Packaged: packagedVedecaps()})
	if err != nil {
		c.errorLine(strings.TrimPrefix(err.Error(), "vedecaps: "))
		return exitUsage
	}

	w := c.env.stdout
	if !*write {
		fmt.Fprint(w, vedecaps.Pretty(frag))
		c.vedecapsHints(base, needsKey, *keyEnv, model, conn.ServerState)
		return exitOK
	}

	home := c.env.getenv("HOME")
	if home == "" {
		c.errorLine("HOME is not set, so the Vedecaps config cannot be located; print the settings without --write")
		return exitFailure
	}
	path := vedecaps.ConfigFile(home, c.env.getenv("XDG_CONFIG_HOME"))
	existing, rerr := os.ReadFile(path)
	if rerr != nil && !errors.Is(rerr, os.ErrNotExist) {
		c.errorLine("cannot read " + path + ": " + rerr.Error())
		return exitFailure
	}
	res, merr := vedecaps.Merge(existing, frag, vedecaps.MergeOptions{MakeDefault: *makeDefault})
	if merr != nil {
		if errors.Is(merr, vedecaps.ErrNeedsManualMerge) {
			c.errorLine(path + " is not plain JSON (comments or a trailing comma). Nothing was changed; merge this by hand:")
			fmt.Fprint(w, vedecaps.Pretty(frag))
			return exitFailure
		}
		c.errorLine(strings.TrimPrefix(merr.Error(), "vedecaps: "))
		return exitFailure
	}
	if err := writeConfig(path, existing, res.Data); err != nil {
		c.errorLine(err.Error())
		return exitFailure
	}
	kv(w, "config", path)
	for _, n := range res.Notes {
		kv(w, "change", n)
	}
	if note := linkSkill(home); note != "" {
		kv(w, "skill", note)
	}
	if len(existing) > 0 {
		kv(w, "backup", path+".bak")
	}
	c.vedecapsHints(base, needsKey, *keyEnv, model, conn.ServerState)
	return exitOK
}

// vedecapsHints 는 설정 뒤에 해야 할 일을 알려 준다. 표준 오류로 써서 JSON 출력을 해치지 않는다.
func (c *cli) vedecapsHints(base string, needsKey bool, keyEnv, model, serverState string) {
	e := c.env.stderr
	if needsKey {
		fmt.Fprintf(e, "note: this address needs an API key. Set %s in the environment where Vedecaps runs.\n", keyEnv)
		fmt.Fprintln(e, "      Create one with: diparnux apikey create vedecaps   (the key is shown once)")
	}
	if model == "" {
		fmt.Fprintln(e, "note: no model is installed yet, so no model was written. Vedecaps lists the server's models by itself.")
	}
	if serverState != "running" && serverState != "running_cpu_fallback" {
		fmt.Fprintln(e, "note: the model server is not running. Start it first: diparnux serve start")
	}
	fmt.Fprintln(e, "next: check that the model can call tools with: diparnux serve toolcheck")
}

// isLocalURL 은 주소가 이 컴퓨터(루프백)를 가리키는지다.
func isLocalURL(raw string) bool {
	u, err := url.Parse(raw)
	if err != nil {
		return false
	}
	h := u.Hostname()
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

// writeConfig 는 설정 파일을 원자적으로 쓴다. 이미 파일이 있으면 .bak 로 한 부를 남긴다.
// 설정 폴더가 없으면 사용자만 쓰는 권한으로 만든다. 파일은 사용자만 읽는다(키 이름이 들어갈 수 있다).
func writeConfig(path string, old, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("cannot create %s: %v", dir, err)
	}
	if len(old) > 0 {
		if err := os.WriteFile(path+".bak", old, 0o600); err != nil {
			return fmt.Errorf("cannot write the backup %s.bak: %v", path, err)
		}
	}
	tmp, err := os.CreateTemp(dir, ".vedecaps-*.tmp")
	if err != nil {
		return fmt.Errorf("cannot write in %s: %v", dir, err)
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot write %s: %v", path, err)
	}
	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return fmt.Errorf("cannot set permissions on %s: %v", path, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("cannot write %s: %v", path, err)
	}
	if err := os.Rename(name, path); err != nil {
		return fmt.Errorf("cannot replace %s: %v", path, err)
	}
	return nil
}

// skillSource 는 패키지가 설치하는 스킬 폴더다. 시험이 바꾼다.
var skillSource = "/usr/share/diparnux/skills/diparnux"

// linkSkill 은 ~/.agents/skills/diparnux 에 스킬 폴더 링크를 만든다. 이미 무언가 있으면 건드리지 않는다.
// 스킬 원본이 이 컴퓨터에 없으면(개발 환경 등) 아무것도 하지 않는다. 결과를 한 줄로 돌려준다.
func linkSkill(home string) string {
	if _, err := os.Stat(filepath.Join(skillSource, "SKILL.md")); err != nil {
		return ""
	}
	dir := filepath.Join(home, ".agents", "skills")
	link := filepath.Join(dir, "diparnux")
	if _, err := os.Lstat(link); err == nil {
		return link + " already exists (left as it is)"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "could not create " + dir + ": " + err.Error()
	}
	if err := os.Symlink(skillSource, link); err != nil {
		return "could not link " + link + ": " + err.Error()
	}
	return "linked " + link
}

// vedecapsPath 는 Diparnux 패키지가 설치하는 Vedecaps 실행 파일 경로다. 시험이 바꾼다.
var vedecapsPath = "/usr/bin/vedecaps"

// packagedVedecaps 는 이 컴퓨터의 Vedecaps 가 Diparnux 패키지로 설치된 것인지다.
func packagedVedecaps() bool {
	st, err := os.Stat(vedecapsPath)
	return err == nil && st.Mode().IsRegular()
}
