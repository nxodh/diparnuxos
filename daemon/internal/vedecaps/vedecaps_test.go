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
	if m["model"] != "diparnux/qwen3-14b.gguf" {
		t.Fatalf("model = %v", m["model"])
	}
	p := m["provider"].(map[string]any)["diparnux"].(map[string]any)
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
	if !strings.Contains(out, `"apiKey": "{env:DIPARNUX_API_KEY}"`) {
		t.Fatalf("output = %s", out)
	}
	if strings.Contains(out, "dpx_") {
		t.Fatal("a key value leaked into the fragment")
	}
	// 작은 컨텍스트에서는 응답 길이가 컨텍스트의 1/4 을 넘지 않는다.
	m := decode(t, []byte(out))
	limit := m["provider"].(map[string]any)["diparnux"].(map[string]any)["models"].(map[string]any)["m.gguf"].(map[string]any)["limit"].(map[string]any)
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
	p := frag["provider"].(map[string]any)["diparnux"].(map[string]any)
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
	if got := Providers(res.Data); strings.Join(got, ",") != "diparnux,ollama" {
		t.Fatalf("providers = %v", got)
	}
	if !strings.Contains(strings.Join(res.Notes, "|"), "kept the default model ollama/llama3") {
		t.Fatalf("notes = %v", res.Notes)
	}
	res, err = Merge(existing, frag, MergeOptions{MakeDefault: true})
	if err != nil {
		t.Fatal(err)
	}
	if decode(t, res.Data)["model"] != "diparnux/m.gguf" {
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
