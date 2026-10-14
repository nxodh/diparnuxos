package vedecaps

import (
	"encoding/json"
	"strings"
	"testing"
)

func decode(t *testing.T, b []byte) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("not JSON: %v\n%s", err, b)
	}
	return m
}

func TestFragmentLocalNeedsNoKeyAndNamesContext(t *testing.T) {
	frag, err := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1/", ModelID: "qwen3-14b.gguf", SlotCtx: 32768})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, []byte(Pretty(frag)))
	if m["model"] != "opereta/qwen3-14b.gguf" {
		t.Fatalf("model = %v", m["model"])
	}
	p := m["provider"].(map[string]any)["opereta"].(map[string]any)
	if p["api"] != "http://127.0.0.1:8080/v1" || p["protocol"] != "openai" {
		t.Fatalf("provider = %v", p)
	}
	if _, ok := p["options"]; ok {
		t.Fatal("a local connection must not carry key settings")
	}
	mod := p["models"].(map[string]any)["qwen3-14b.gguf"].(map[string]any)
	limit := mod["limit"].(map[string]any)
	if limit["context"].(float64) != 32768 || limit["output"].(float64) != 4096 {
		t.Fatalf("model = %v", mod)
	}
}

func TestFragmentRemoteReferencesKeyByEnvironmentNeverByValue(t *testing.T) {
	frag, err := Fragment(Input{BaseURL: "http://192.168.0.23:8080/v1", ModelID: "m.gguf", SlotCtx: 8192, NeedsKey: true})
	if err != nil {
		t.Fatal(err)
	}
	out := Pretty(frag)
	if !strings.Contains(out, `"apiKey": "{env:OPERETA_API_KEY}"`) {
		t.Fatalf("output = %s", out)
	}
	if strings.Contains(out, "opr_") {
		t.Fatal("a key value leaked into the fragment")
	}
	// 작은 컨텍스트에서는 응답 길이가 컨텍스트의 1/4 을 넘지 않는다.
	m := decode(t, []byte(out))
	limit := m["provider"].(map[string]any)["opereta"].(map[string]any)["models"].(map[string]any)["m.gguf"].(map[string]any)["limit"].(map[string]any)
	if limit["output"].(float64) != 2048 {
		t.Fatalf("limit = %v", limit)
	}
}

func TestFragmentRejectsBadInput(t *testing.T) {
	cases := []Input{
		{BaseURL: "ftp://x/v1"},
		{BaseURL: "http://x/v1 y"},
		{BaseURL: "http://x/v1", NeedsKey: true, KeyEnv: "BAD NAME"},
		{BaseURL: "http://x/v1", ModelID: "a{env:SECRET}"},
	}
	for _, c := range cases {
		if _, err := Fragment(c); err == nil {
			t.Fatalf("accepted %+v", c)
		}
	}
}

func TestFragmentWithoutModelLetsVedecapsListModels(t *testing.T) {
	frag, err := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := frag["model"]; ok {
		t.Fatal("no model was given but a default was set")
	}
	p := frag["provider"].(map[string]any)["opereta"].(map[string]any)
	if _, ok := p["models"]; ok {
		t.Fatal("no model was given but a model table was written")
	}
}

func TestMergeKeepsOtherProvidersAndSettings(t *testing.T) {
	frag, _ := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1", ModelID: "m.gguf", SlotCtx: 16384})
	existing := []byte(`{"theme":"dark","provider":{"ollama":{"api":"http://localhost:11434/v1"}},"model":"ollama/llama3"}`)
	res, err := Merge(existing, frag, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, res.Data)
	if m["theme"] != "dark" || m["model"] != "ollama/llama3" {
		t.Fatalf("user settings changed: %v", m)
	}
	if got := Providers(res.Data); strings.Join(got, ",") != "ollama,opereta" {
		t.Fatalf("providers = %v", got)
	}
	if !strings.Contains(strings.Join(res.Notes, "|"), "kept the default model ollama/llama3") {
		t.Fatalf("notes = %v", res.Notes)
	}
	res, err = Merge(existing, frag, MergeOptions{MakeDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if decode(t, res.Data)["model"] != "opereta/m.gguf" {
		t.Fatal("--make-default did not switch the default model")
	}
}

func TestMergeIsIdempotentAndReplacesOwnProvider(t *testing.T) {
	first, _ := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1", ModelID: "m.gguf", SlotCtx: 8192})
	a, err := Merge(nil, first, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := Merge(a.Data, first, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if string(a.Data) != string(b.Data) {
		t.Fatalf("a second merge changed the file:\n%s\n---\n%s", a.Data, b.Data)
	}
	second, _ := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1", ModelID: "m.gguf", SlotCtx: 32768})
	c, err := Merge(b.Data, second, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(c.Data), "32768") || strings.Contains(string(c.Data), "8192") {
		t.Fatalf("own provider was not replaced:\n%s", c.Data)
	}
}

func TestMergeRefusesFilesItCannotRewriteSafely(t *testing.T) {
	frag, _ := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1"})
	for _, in := range []string{
		"// my settings\n{\"theme\":\"dark\"}",
		"{\"theme\":\"dark\",}",
		"[1,2]",
		"{\"a\":1} {\"b\":2}",
	} {
		if _, err := Merge([]byte(in), frag, MergeOptions{}); err == nil {
			t.Fatalf("merged %q", in)
		}
	}
	if _, err := Merge([]byte(`{"provider":[]}`), frag, MergeOptions{}); err == nil {
		t.Fatal("merged a provider that is not an object")
	}
}

func TestConfigFileFollowsXDG(t *testing.T) {
	if got := ConfigFile("/home/u", ""); got != "/home/u/.config/vedecaps/vedecaps.json" {
		t.Fatal(got)
	}
	if got := ConfigFile("/home/u", "/x/cfg"); got != "/x/cfg/vedecaps/vedecaps.json" {
		t.Fatal(got)
	}
	if got := ConfigFile("/home/u", "relative"); got != "/home/u/.config/vedecaps/vedecaps.json" {
		t.Fatalf("relative XDG_CONFIG_HOME must be ignored, got %s", got)
	}
}

func TestFragmentAndMergeCarryTheMCPServer(t *testing.T) {
	frag, err := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1", MCP: true})
	if err != nil {
		t.Fatal(err)
	}
	existing := []byte(`{"mcp":{"other":{"type":"remote","url":"https://x.example/mcp"}}}`)
	res, err := Merge(existing, frag, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	m := decode(t, res.Data)["mcp"].(map[string]any)
	if _, ok := m["other"]; !ok {
		t.Fatalf("another MCP server was dropped: %v", m)
	}
	d := m["opereta"].(map[string]any)
	cmd := d["command"].([]any)
	if d["type"] != "local" || len(cmd) != 2 || cmd[0] != "opereta" || cmd[1] != "mcp" {
		t.Fatalf("mcp entry = %v", d)
	}
	if strings.Contains(string(res.Data), "allow-control") {
		t.Fatal("the control tools must never be registered by this command")
	}
	if _, err := Merge([]byte(`{"mcp":[]}`), frag, MergeOptions{}); err == nil {
		t.Fatal("merged an mcp that is not an object")
	}
}

func TestPackagedInstallTurnsOffSelfUpdateOnlyWhenUnset(t *testing.T) {
	frag, err := Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1", Packaged: true})
	if err != nil {
		t.Fatal(err)
	}
	res, err := Merge([]byte(`{"theme":"dark"}`), frag, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if decode(t, res.Data)["autoupdate"] != false {
		t.Fatalf("autoupdate = %v", decode(t, res.Data)["autoupdate"])
	}
	res, err = Merge([]byte(`{"autoupdate":"notify"}`), frag, MergeOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if decode(t, res.Data)["autoupdate"] != "notify" {
		t.Fatalf("the user's choice was overwritten: %v", decode(t, res.Data)["autoupdate"])
	}
	// 패키지로 설치하지 않은 Vedecaps 는 스스로 업데이트하므로 건드리지 않는다.
	frag, _ = Fragment(Input{BaseURL: "http://127.0.0.1:8080/v1"})
	if _, ok := frag["autoupdate"]; ok {
		t.Fatal("autoupdate was written for an install that is not ours")
	}
}

// https 주소에는 CA 경로를 options.ca 로 쓴다. 평문 주소에는 쓰지 않는다. 키 값은 여전히 쓰지 않는다.
func TestFragmentCAFile(t *testing.T) {
	frag, err := Fragment(Input{BaseURL: "https://opereta.local:8444/v1", ModelID: "m.gguf", SlotCtx: 16384, NeedsKey: true, CAFile: DefaultCAFile})
	if err != nil {
		t.Fatal(err)
	}
	opts := frag["provider"].(map[string]any)[ProviderID].(map[string]any)["options"].(map[string]any)
	if opts["ca"] != DefaultCAFile || opts["apiKey"] != "{env:OPERETA_API_KEY}" {
		t.Fatalf("options = %v", opts)
	}
	// 키가 필요 없어도 CA 는 쓴다.
	frag, err = Fragment(Input{BaseURL: "https://box.local:8444/v1", CAFile: "ca.pem"})
	if err != nil {
		t.Fatal(err)
	}
	if got := frag["provider"].(map[string]any)[ProviderID].(map[string]any)["options"].(map[string]any)["ca"]; got != "ca.pem" {
		t.Fatalf("ca = %v", got)
	}
	if _, err := Fragment(Input{BaseURL: "http://box.local:8080/v1", CAFile: "ca.pem"}); err == nil {
		t.Fatal("a CA file with a plain http address was accepted")
	}
	if _, err := Fragment(Input{BaseURL: "https://box.local:8444/v1", CAFile: "bad\npath"}); err == nil {
		t.Fatal("a CA path with a newline was accepted")
	}
	// CA 가 없으면 options 에 ca 가 없다.
	frag, _ = Fragment(Input{BaseURL: "https://box.local:8444/v1", NeedsKey: true})
	if _, has := frag["provider"].(map[string]any)[ProviderID].(map[string]any)["options"].(map[string]any)["ca"]; has {
		t.Fatal("options.ca was written without a CA file")
	}
}
