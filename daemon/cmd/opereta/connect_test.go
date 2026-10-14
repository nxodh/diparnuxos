package main

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const connRunning = `{"server_state":"running","model_id":"qwen3-14b.gguf","models":["qwen3-14b.gguf"],"public_port":8080,
"lan_exposed":false,"gateway":true,"key_required":true,"key_count":1,"local_no_key":true,"local_key_required":false,
"slot_ctx":32768,"parallel":2,"hostname":"opr","local_url":"http://127.0.0.1:8080/v1","addresses":[],"warnings":[],"examples":{}}`

func decodeOut(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, s)
	}
	return m
}

func TestConnectDeberopaPrintsLocalSettingsWithoutKey(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	res := runCLI(t, f, "connect", "deberopa")
	wantCode(t, res, exitOK)
	m := decodeOut(t, res.stdout)
	if m["model"] != "opereta/qwen3-14b.gguf" {
		t.Fatalf("model = %v", m["model"])
	}
	prov := m["provider"].(map[string]any)["opereta"].(map[string]any)
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

func TestConnectDeberopaRemoteUsesLANAddressAndKeyFromEnvironment(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, strings.Replace(strings.Replace(connRunning, `"lan_exposed":false`, `"lan_exposed":true`, 1),
		`"addresses":[]`, `"addresses":[{"interface":"eth0","address":"192.168.0.23","family":"ipv4","url":"http://192.168.0.23:8080/v1"}]`, 1))
	res := runCLI(t, f, "connect", "deberopa", "--remote")
	wantCode(t, res, exitOK)
	prov := decodeOut(t, res.stdout)["provider"].(map[string]any)["opereta"].(map[string]any)
	if prov["api"] != "http://192.168.0.23:8080/v1" {
		t.Fatalf("api = %v", prov["api"])
	}
	if prov["options"].(map[string]any)["apiKey"] != "{env:OPERETA_API_KEY}" {
		t.Fatalf("options = %v", prov["options"])
	}
	wantContains(t, "hints", res.stderr, "OPERETA_API_KEY", "opereta apikey create")

	// LAN 공개가 꺼져 있으면 줄 주소가 없다.
	f.on("GET /v1/connection", 200, connRunning)
	res = runCLI(t, f, "connect", "deberopa", "--remote")
	wantCode(t, res, exitFailure)
	wantContains(t, "no address", res.stderr, "LAN exposure")
}

func TestConnectDeberopaUsesPlanWhenServerIsStopped(t *testing.T) {
	f := newFakeDaemon(t)
	stopped := strings.Replace(strings.Replace(connRunning, `"server_state":"running"`, `"server_state":"stopped"`, 1), `"slot_ctx":32768`, `"slot_ctx":0`, 1)
	f.on("GET /v1/connection", 200, stopped)
	f.on("GET /v1/server/plan", 200, `{"model_id":"qwen3-14b.gguf","plan":{"ngl":"all","ctx":16384,"slot_ctx":16384,"parallel":1,"rationale":[],"warnings":[],"advice":[],"visible_devices":[0]},"input":{},"gpu":{}}`)
	res := runCLI(t, f, "connect", "deberopa")
	wantCode(t, res, exitOK)
	limit := decodeOut(t, res.stdout)["provider"].(map[string]any)["opereta"].(map[string]any)["models"].(map[string]any)["qwen3-14b.gguf"].(map[string]any)["limit"].(map[string]any)
	if limit["context"].(float64) != 16384 {
		t.Fatalf("limit = %v", limit)
	}
	wantContains(t, "stopped hint", res.stderr, "not running", "opereta serve start")
}

func TestConnectDeberopaWriteMergesAndKeepsBackup(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "deberopa", "deberopa.json")
	if err := os.MkdirAll(filepath.Dir(cfg), 0o700); err != nil {
		t.Fatal(err)
	}
	orig := `{"theme":"dark","provider":{"ollama":{"api":"http://localhost:11434/v1"}},"model":"ollama/llama3"}`
	if err := os.WriteFile(cfg, []byte(orig), 0o600); err != nil {
		t.Fatal(err)
	}
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "deberopa", "--write")
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
	if _, ok := provs["opereta"]; !ok {
		t.Fatalf("opereta provider missing: %s", data)
	}
	bak, err := os.ReadFile(cfg + ".bak")
	if err != nil || string(bak) != orig {
		t.Fatalf("backup = %q, %v", bak, err)
	}
	if st, _ := os.Stat(cfg); st.Mode().Perm() != 0o600 {
		t.Fatalf("mode = %v", st.Mode().Perm())
	}

	// --make-default 는 기본 모델도 바꾼다. 다시 실행해도 같은 결과다.
	res = runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "deberopa", "--write", "--make-default")
	wantCode(t, res, exitOK)
	data, _ = os.ReadFile(cfg)
	if decodeOut(t, string(data))["model"] != "opereta/qwen3-14b.gguf" {
		t.Fatalf("default model = %s", data)
	}
}

func TestConnectDeberopaWriteCreatesConfigAndHonorsXDG(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	xdg := t.TempDir()
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": t.TempDir(), "XDG_CONFIG_HOME": xdg}}, "connect", "deberopa", "--write")
	wantCode(t, res, exitOK)
	cfg := filepath.Join(xdg, "deberopa", "deberopa.json")
	data, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatalf("config not created: %v", err)
	}
	if decodeOut(t, string(data))["model"] != "opereta/qwen3-14b.gguf" {
		t.Fatalf("config = %s", data)
	}
	if _, err := os.Stat(cfg + ".bak"); err == nil {
		t.Fatal("a backup was made for a file that did not exist")
	}
}

func TestConnectDeberopaWriteRefusesConfigWithComments(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	cfg := filepath.Join(home, ".config", "deberopa", "deberopa.json")
	_ = os.MkdirAll(filepath.Dir(cfg), 0o700)
	orig := "// mine\n{\"theme\":\"dark\"}\n"
	_ = os.WriteFile(cfg, []byte(orig), 0o600)
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "deberopa", "--write")
	wantCode(t, res, exitFailure)
	wantContains(t, "refusal", res.stderr, "not plain JSON", "Nothing was changed")
	if data, _ := os.ReadFile(cfg); string(data) != orig {
		t.Fatalf("file was changed: %q", data)
	}
	if !strings.Contains(res.stdout, `"opereta"`) {
		t.Fatalf("the fragment to merge by hand was not printed: %s", res.stdout)
	}
}

func TestConnectUsage(t *testing.T) {
	for _, args := range [][]string{{"connect"}, {"connect", "emacs"}, {"connect", "deberopa", "extra"}, {"connect", "deberopa", "--remote", "--url", "http://x/v1"}} {
		res := runCLI(t, nil, args...)
		if res.code != exitUsage {
			t.Fatalf("%v: exit %d stderr %s", args, res.code, res.stderr)
		}
	}
}

func TestConnectDeberopaWriteWithMCPAndSkillLink(t *testing.T) {
	src := t.TempDir()
	if err := os.WriteFile(filepath.Join(src, "SKILL.md"), []byte("---\nname: opereta\ndescription: x\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	old := skillSource
	skillSource = src
	defer func() { skillSource = old }()

	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "deberopa", "--write", "--mcp")
	wantCode(t, res, exitOK)
	wantContains(t, "write", res.stdout, "added the MCP server", "linked")
	data, err := os.ReadFile(filepath.Join(home, ".config", "deberopa", "deberopa.json"))
	if err != nil {
		t.Fatal(err)
	}
	mcp := decodeOut(t, string(data))["mcp"].(map[string]any)["opereta"].(map[string]any)
	if mcp["type"] != "local" {
		t.Fatalf("mcp = %v", mcp)
	}
	target, err := os.Readlink(filepath.Join(home, ".agents", "skills", "opereta"))
	if err != nil || target != src {
		t.Fatalf("skill link = %q, %v", target, err)
	}

	// 다시 실행하면 이미 있는 링크를 그대로 둔다.
	res = runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "deberopa", "--write", "--mcp")
	wantCode(t, res, exitOK)
	wantContains(t, "second write", res.stdout, "already exists")
}

func TestConnectDeberopaWithoutWriteNeverTouchesTheFilesystem(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "deberopa", "--mcp")
	wantCode(t, res, exitOK)
	if _, ok := decodeOut(t, res.stdout)["mcp"]; !ok {
		t.Fatalf("the MCP entry is missing from the printed settings: %s", res.stdout)
	}
	entries, _ := os.ReadDir(home)
	if len(entries) != 0 {
		t.Fatalf("printing created files: %v", entries)
	}
}

func TestConnectDeberopaTurnsOffSelfUpdateForThePackagedInstall(t *testing.T) {
	bin := filepath.Join(t.TempDir(), "deberopa")
	if err := os.WriteFile(bin, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	old := deberopaPath
	deberopaPath = bin
	defer func() { deberopaPath = old }()
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	res := runCLI(t, f, "connect", "deberopa")
	wantCode(t, res, exitOK)
	if decodeOut(t, res.stdout)["autoupdate"] != false {
		t.Fatalf("output = %s", res.stdout)
	}
	deberopaPath = filepath.Join(t.TempDir(), "missing")
	res = runCLI(t, f, "connect", "deberopa")
	if _, ok := decodeOut(t, res.stdout)["autoupdate"]; ok {
		t.Fatalf("autoupdate written without the packaged binary: %s", res.stdout)
	}
}

func TestConnectDeberopaUsesTheDefaultModelWhenTheServerIsStopped(t *testing.T) {
	f := newFakeDaemon(t)
	stopped := strings.Replace(strings.Replace(strings.Replace(connRunning, `"server_state":"running"`, `"server_state":"stopped"`, 1),
		`"model_id":"qwen3-14b.gguf"`, `"model_id":""`, 1), `"models":["qwen3-14b.gguf"]`, `"models":["a.gguf","b.gguf"]`, 1)
	f.on("GET /v1/connection", 200, stopped)
	f.on("GET /v1/models", 200, `{"default":"b.gguf","models":[{"id":"a.gguf"},{"id":"b.gguf"}]}`)
	f.on("GET /v1/server/plan", 200, `{"model_id":"b.gguf","plan":{"ngl":"0","ctx":8192,"slot_ctx":8192,"parallel":1,"rationale":[],"warnings":[],"advice":[],"visible_devices":[]},"input":{},"gpu":{}}`)
	res := runCLI(t, f, "connect", "deberopa")
	wantCode(t, res, exitOK)
	if decodeOut(t, res.stdout)["model"] != "opereta/b.gguf" {
		t.Fatalf("output = %s", res.stdout)
	}
}

func TestConnectDeberopaRemoteWithTLSWritesHTTPSAndCA(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, `{"server_state":"running","model_id":"m.gguf","models":["m.gguf"],"local_url":"http://127.0.0.1:8080/v1",`+
		`"slot_ctx":16384,"key_required":true,"local_key_required":false,"tls_active":true,"tls_port":8444,"ca_fingerprint":"AA:BB:CC",`+
		`"addresses":[{"interface":"enp3s0","address":"192.168.0.23","family":"ipv4","url":"https://192.168.0.23:8444/v1"}]}`)
	f.onFunc("GET /v1/tls/ca", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("-----BEGIN CERTIFICATE-----\nMIIB\n-----END CERTIFICATE-----\n"))
	})
	out := filepath.Join(t.TempDir(), "opereta-ca.pem")
	res := runCLI(t, f, "connect", "deberopa", "--remote", "--ca-out", out)
	wantCode(t, res, exitOK)
	wantContains(t, "fragment", res.stdout, `"api": "https://192.168.0.23:8444/v1"`, `"ca": "~/.config/deberopa/opereta-ca.pem"`, `{env:OPERETA_API_KEY}`)
	wantContains(t, "hints", res.stderr, "own CA", "AA:BB:CC", "0.1.5 or newer", "wrote the CA certificate")
	if data, err := os.ReadFile(out); err != nil || !strings.Contains(string(data), "BEGIN CERTIFICATE") {
		t.Fatalf("CA file = %q, %v", data, err)
	}
	if strings.Contains(res.stdout, "PRIVATE") {
		t.Fatal("key material in the output")
	}

	res = runCLI(t, f, "connect", "deberopa", "--remote", "--ca", "/home/u/ca.pem")
	wantCode(t, res, exitOK)
	wantContains(t, "custom path", res.stdout, `"ca": "/home/u/ca.pem"`)

	// 평문 주소에 --ca 는 쓸 수 없다.
	f.on("GET /v1/connection", 200, `{"server_state":"running","model_id":"m.gguf","models":["m.gguf"],"local_url":"http://127.0.0.1:8080/v1","slot_ctx":16384,"addresses":[]}`)
	res = runCLI(t, f, "connect", "deberopa", "--ca", "ca.pem")
	wantCode(t, res, exitUsage)
}

func TestConnectDeberopaWritesIntoTheOldFolderUntilDeberopaMigratesIt(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	home := t.TempDir()
	old := filepath.Join(home, ".config", "vedecaps")
	if err := os.MkdirAll(old, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(old, "vedecaps.json"), []byte(`{"theme":"dark"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	res := runCLIWith(t, f, cliOpts{env: map[string]string{"HOME": home}}, "connect", "deberopa", "--write")
	wantCode(t, res, exitOK)
	wantContains(t, "old folder", res.stdout, filepath.Join(old, "vedecaps.json"), "first run")
	if _, err := os.Stat(filepath.Join(home, ".config", "deberopa")); err == nil {
		t.Fatal("the new folder was created, which would stop Deberopa from migrating the old one")
	}
	data, err := os.ReadFile(filepath.Join(old, "vedecaps.json"))
	if err != nil || !strings.Contains(string(data), `"theme": "dark"`) && !strings.Contains(string(data), `"theme":"dark"`) {
		t.Fatalf("the user's setting was lost: %s %v", data, err)
	}
}

func TestConnectVedecapsOldNameStillWorksWithANote(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/connection", 200, connRunning)
	res := runCLI(t, f, "connect", "vedecaps")
	wantCode(t, res, exitOK)
	wantContains(t, "alias", res.stderr, "old name")
}
