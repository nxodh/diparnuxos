package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const connRunning = `{"server_state":"running","model_id":"qwen3-14b.gguf","models":["qwen3-14b.gguf"],"public_port":8080,
"lan_exposed":false,"gateway":true,"key_required":true,"key_count":1,"local_no_key":true,"local_key_required":false,
"slot_ctx":32768,"parallel":2,"hostname":"dpx","local_url":"http://127.0.0.1:8080/v1","addresses":[],"warnings":[],"examples":{}}`

func decodeOut(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, s)
	}
	return m
}

func TestConnectVedecapsPrintsLocalSettingsWithoutKey(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	res := runCLI(t, f, "connect", "vedecaps")
	wantCode(t, res, exitOK)
	m := decodeOut(t, res.stdout)
	if m["model"] != "diparnux/qwen3-14b.gguf" {
		t.Fatalf("model = %v", m["model"])
	}
	prov := m["provider"].(map[string]any)["diparnux"].(map[string]any)
	if prov["api"] != "http://127.0.0.1:8080/v1" {
		t.Fatalf("api = %v", prov["api"])
	}
	if _, ok := prov["options"]; ok {
		t.Fatal("a local connection with local_no_key must not mention a key")
	}
	limit := prov["models"].(map[string]any)["qwen3-14b.gguf"].(map[string]any)["limit"].(map[string]any)
	if limit["context"].(float64) != 32768 {
		t.Fatalf("limit = %v", limit)
	}
	if strings.Contains(res.stderr, "needs an API key") {
		t.Fatalf("stderr = %s", res.stderr)
	}
}

func TestConnectVedecapsRemoteUsesLANAddressAndKeyFromEnvironment(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, strings.Replace(strings.Replace(connRunning, `"lan_exposed":false`, `"lan_exposed":true`, 1),
		`"addresses":[]`, `"addresses":[{"interface":"eth0","address":"192.168.0.23","family":"ipv4","url":"http://192.168.0.23:8080/v1"}]`, 1))
	res := runCLI(t, f, "connect", "vedecaps", "--remote")
	wantCode(t, res, exitOK)
	prov := decodeOut(t, res.stdout)["provider"].(map[string]any)["diparnux"].(map[string]any)
	if prov["api"] != "http://192.168.0.23:8080/v1" {
		t.Fatalf("api = %v", prov["api"])
	}
	if prov["options"].(map[string]any)["apiKey"] != "{env:DIPARNUX_API_KEY}" {
		t.Fatalf("options = %v", prov["options"])
	}
	wantContains(t, "hints", res.stderr, "DIPARNUX_API_KEY", "diparnux apikey create")

	// LAN 공개가 꺼져 있으면 줄 주소가 없다.
	f.on("GET /v1/connection", 200, connRunning)
	res = runCLI(t, f, "connect", "vedecaps", "--remote")
	wantCode(t, res, exitFailure)
	wantContains(t, "no address", res.stderr, "LAN exposure")
}

func TestConnectVedecapsUsesPlanWhenServerIsStopped(t *testing.T) {
	f := newFakeDaemon(t)
	stopped := strings.Replace(strings.Replace(connRunning, `"server_state":"running"`, `"server_state":"stopped"`, 1), `"slot_ctx":32768`, `"slot_ctx":0`, 1)
	f.on("GET /v1/connection", 200, stopped)
	f.on("GET /v1/server/plan", 200, `{"model_id":"qwen3-14b.gguf","plan":{"ngl":"all","ctx":16384,"slot_ctx":16384,"parallel":1,"rationale":[],"warnings":[],"advice":[],"visible_devices":[0]},"input":{},"gpu":{}}`)
	res := runCLI(t, f, "connect", "vedecaps")
	wantCode(t, res, exitOK)
	limit := decodeOut(t, res.stdout)["provider"].(map[string]any)["diparnux"].(map[string]any)["models"].(map[string]any)["qwen3-14b.gguf"].(map[string]any)["limit"].(map[string]any)
	if limit["context"].(float64) != 16384 {
		t.Fatalf("limit = %v", limit)
	}
	wantContains(t, "stopped hint", res.stderr, "not running", "diparnux serve start")
}

func TestConnectVedecapsWriteMergesAndKeepsBackup(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "vedecaps", "vedecaps.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	orig := `{"theme":"dark","provider":{"ollama":{"api":"http://localhost:11434/v1"}},"model":"ollama/llama3"}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "vedecaps", "--write")
	wantCode(t, res, exitOK)
	wantContains(t, "write", res.stdout, cfg, "added the provider", "kept the default model ollama/llama3")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	m := decodeOut(t, string(data))
	if m["theme"] != "dark" || m["model"] != "ollama/llama3" {
		t.Fatalf("user settings changed: %s", data)
	}
	provs := m["provider"].(map[string]any)
	if _, ok := provs["ollama"]; !ok {
		t.Fatalf("another provider was dropped: %s", data)
	}
	if _, ok := provs["diparnux"]; !ok {
		t.Fatalf("diparnux provider missing: %s", data)
	}
	bak, err := os.ReadFile(cfg + ".bak")
	if err != nil || string(bak) != orig {
		t.Fatalf("backup = %q, %v", bak, err)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}

	// --make-default 는 기본 모델도 바꾼다. 다시 실행해도 같은 결과다.
	res = runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "vedecaps", "--write", "--make-default")
	wantCode(t, res, exitOK)
	data, _ = os.ReadFile(cfg)
	if decodeOut(t, string(data))["model"] != "diparnux/qwen3-14b.gguf" {
		t.Fatalf("default model = %s", data)
	}
}

func TestConnectVedecapsWriteCreatesConfigAndHonorsXDG(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	xdg := t.TempDir()
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": xdg}}, "connect", "vedecaps", "--write")
	wantCode(t, res, exitOK)
	cfg := filepath.Join(xdg, "vedecaps", "vedecaps.json")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	if decodeOut(t, string(data))["model"] != "diparnux/qwen3-14b.gguf" {
		t.Fatalf("config = %s", data)
	}
	if _, err := os.Stat(cfg + ".bak"); err == nil {
		t.Fatal("a backup was made for a file that did not exist")
	}
}

func TestConnectVedecapsWriteRefusesConfigWithComments(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "vedecaps", "vedecaps.json")
	_ = os.MkdirAll(filepath.Dir(cfg), 0o700)
	orig := "// mine\n{\"theme\":\"dark\"}\n"
	_ = os.WriteFile(cfg, []byte(orig), 0o600)
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "vedecaps", "--write")
	wantCode(t, res, exitFailure)
	wantContains(t, "refusal", res.stderr, "not plain JSON", "Nothing was changed")
	if data, _ := os.ReadFile(cfg); string(data) != orig {
		t.Fatalf("file was changed: %q", data)
	}
	if !strings.Contains(res.stdout, `"diparnux"`) {
		t.Fatalf("the fragment to merge by hand was not printed: %s", res.stdout)
	}
}

func TestConnectUsage(t *testing.T) {
	for _, args := range [][]string{{"connect"}, {"connect", "emacs"}, {"connect", "vedecaps", "extra"}, {"connect", "vedecaps", "--remote", "--url", "http://x/v1"}} {
		res := runCLI(t, nil, args...)
		if res.code != exitUsage {
			t.Fatalf("%v: exit %d stderr %s", args, res.code, res.stderr)
		}
	}
}

func TestConnectVedecapsWriteWithMCPAndSkillLink(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: diparnux\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := skillSource
	skillSource = src
	defer func() { skillSource = old }()

	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "vedecaps", "--write", "--mcp")
	wantCode(t, res, exitOK)
	wantContains(t, "write", res.stdout, "added the MCP server", "linked")
	data, err := os.ReadFile(filepath.Join(home, ".config", "vedecaps", "vedecaps.json"))
	if err != nil {
		t.Fatal(err)
	}
	mcp := decodeOut(t, string(data))["mcp"].(map[string]any)["diparnux"].(map[string]any)
	if mcp["type"] != "local" {
		t.Fatalf("mcp = %v", mcp)
	}
	target, err := os.Readlink(filepath.Join(home, ".agents", "skills", "diparnux"))
	if err != nil || target != src {
		t.Fatalf("skill link = %q, %v", target, err)
	}

	// 다시 실행하면 이미 있는 링크를 그대로 둔다.
	res = runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "vedecaps", "--write", "--mcp")
	wantCode(t, res, exitOK)
	wantContains(t, "second write", res.stdout, "already exists")
}

func TestConnectVedecapsWithoutWriteNeverTouchesTheFilesystem(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "vedecaps", "--mcp")
	wantCode(t, res, exitOK)
	if _, ok := decodeOut(t, res.stdout)["mcp"]; !ok {
		t.Fatalf("the MCP entry is missing from the printed settings: %s", res.stdout)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatalf("printing created files: %v", entries)
	}
}

func TestConnectVedecapsTurnsOffSelfUpdateForThePackagedInstall(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "vedecaps")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := vedecapsPath
	vedecapsPath = bin
	defer func() { vedecapsPath = old }()
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	res := runCLI(t, f, "connect", "vedecaps")
	wantCode(t, res, exitOK)
	if decodeOut(t, res.stdout)["autoupdate"] != false {
		t.Fatalf("output = %s", res.stdout)
	}
	vedecapsPath = filepath.Join(t.TempDir(), "missing")
	res = runCLI(t, f, "connect", "vedecaps")
	if _, ok := decodeOut(t, res.stdout)["autoupdate"]; ok {
		t.Fatalf("autoupdate written without the packaged binary: %s", res.stdout)
	}
}
