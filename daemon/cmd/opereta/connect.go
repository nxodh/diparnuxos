package main

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/client"
	"github.com/nxodh/operetaos/daemon/internal/vedecaps"
)

// connect 는 다른 프로그램을 이 서버에 연결하는 설정을 만든다. 지금은 Vedecaps 만 있다.
func (c *cli) connect(args []string) int {
	if len(args) == 0 || args[0] != "vedecaps" {
		return c.usage("usage: opereta connect vedecaps [--remote] [--url URL] [--model ID] [--write] [--mcp]", "connect")
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
	makeDefault := fs.Bool("make-default", false, "make Opereta the default model even when another default is set")
	withMCP := fs.Bool("mcp", false, "also register the read-only Opereta MCP server (opereta mcp)")
	caPath := fs.String("ca", "", "path of the CA certificate on the computer where Vedecaps runs (https addresses; default "+vedecaps.DefaultCAFile+")")
	caOut := fs.String("ca-out", "", "write this computer's CA certificate to this file (copy it to the other computer)")
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
			c.errorLine("this computer has no LAN address to give out: turn on LAN exposure first (opereta config set lan on)")
			return exitFailure
		}
		base, needsKey = conn.Addresses[0].URL, true
	}

	model := *modelID
	if model == "" {
		model = conn.ModelID
	}
	if model == "" {
		// 서버가 멈춰 있으면 기본 모델을, 그것도 없으면 첫 모델을 쓴다.
		if list, _, lerr := c.api.Models(ctx); lerr == nil {
			model = list.Default
		}
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

	caFile := ""
	if strings.HasPrefix(base, "https://") {
		caFile = *caPath
		if caFile == "" {
			caFile = vedecaps.DefaultCAFile
		}
	} else if *caPath != "" {
		return c.usage("--ca only applies to an https:// address (use --remote while TLS is on, or --url https://...)", "connect vedecaps")
	}
	frag, err := vedecaps.Fragment(vedecaps.Input{BaseURL: base, ModelID: model, SlotCtx: slotCtx, NeedsKey: needsKey, KeyEnv: *keyEnv, MCP: *withMCP, Packaged: packagedVedecaps(), CAFile: caFile})
	if err != nil {
		c.errorLine(strings.TrimPrefix(err.Error(), "vedecaps: "))
		return exitUsage
	}

	w := c.env.stdout
	if *caOut != "" {
		pem, perr := c.api.TLSCA(ctx)
		if perr != nil {
			return c.fail(perr)
		}
		if werr := os.WriteFile(*caOut, pem, 0o644); werr != nil {
			c.errorLine(werr.Error())
			return exitFailure
		}
		fmt.Fprintf(c.env.stderr, "wrote the CA certificate to %s", *caOut)
		if conn.CAFingerprint != "" {
			fmt.Fprintf(c.env.stderr, " (fingerprint %s)", conn.CAFingerprint)
		}
		fmt.Fprintln(c.env.stderr)
	}
	if !*write {
		fmt.Fprint(w, vedecaps.Pretty(frag))
		c.vedecapsHints(base, needsKey, *keyEnv, model, conn.ServerState)
		if caFile != "" {
			c.vedecapsCAHints(caFile, conn.CAFingerprint)
		}
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
		fmt.Fprintln(e, "      Create one with: opereta apikey create vedecaps   (the key is shown once)")
	}
	if model == "" {
		fmt.Fprintln(e, "note: no model is installed yet, so no model was written. Vedecaps lists the server's models by itself.")
	}
	if serverState != "running" && serverState != "running_cpu_fallback" {
		fmt.Fprintln(e, "note: the model server is not running. Start it first: opereta serve start")
	}
	fmt.Fprintln(e, "next: check that the model can call tools with: opereta serve toolcheck")
}

// vedecapsCAHints 는 https 주소를 쓸 때 CA 를 어떻게 배포하는지 알린다.
func (c *cli) vedecapsCAHints(caFile, fingerprint string) {
	e := c.env.stderr
	fmt.Fprintf(e, "note: this address uses HTTPS with this computer's own CA. Vedecaps needs the CA file at %s\n", caFile)
	fmt.Fprintln(e, "      Make it here:  opereta tls ca --out opereta-ca.pem   (or add --ca-out FILE to this command)")
	if fingerprint != "" {
		fmt.Fprintf(e, "      After copying it, check that its SHA-256 fingerprint is %s\n", fingerprint)
	}
	fmt.Fprintln(e, "      Trusting the CA needs Vedecaps 0.1.5 or newer (options.ca). Do not turn certificate checking off.")
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
var skillSource = "/usr/share/opereta/skills/opereta"

// linkSkill 은 ~/.agents/skills/opereta 에 스킬 폴더 링크를 만든다. 이미 무언가 있으면 건드리지 않는다.
// 스킬 원본이 이 컴퓨터에 없으면(개발 환경 등) 아무것도 하지 않는다. 결과를 한 줄로 돌려준다.
func linkSkill(home string) string {
	if _, err := os.Stat(filepath.Join(skillSource, "SKILL.md")); err != nil {
		return ""
	}
	dir := filepath.Join(home, ".agents", "skills")
	link := filepath.Join(dir, "opereta")
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

// vedecapsPath 는 Opereta 패키지가 설치하는 Vedecaps 실행 파일 경로다. 시험이 바꾼다.
var vedecapsPath = "/usr/bin/vedecaps"

// packagedVedecaps 는 이 컴퓨터의 Vedecaps 가 Opereta 패키지로 설치된 것인지다.
func packagedVedecaps() bool {
	st, err := os.Stat(vedecapsPath)
	return err == nil && st.Mode().IsRegular()
}
