package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// writeFile은 테스트용 설정 파일을 임시 디렉터리에 쓴다.
func writeFile(t *testing.T, body string) string {
	t.Helper()
	file := filepath.Join(t.TempDir(), "lightlixd.json")
	if err := os.WriteFile(file, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return file
}

func TestDefault(t *testing.T) {
	d := Default()
	if d.Port != 8080 || d.LANExposed || !d.Autostart || d.ModelsDir != "/var/lib/lightlix/models" || d.HFToken != "" {
		t.Fatalf("Default = %+v", d)
	}
	if err := d.Validate(); err != nil {
		t.Fatalf("Default is invalid: %v", err)
	}
}

func TestLoadMissingFileReturnsDefault(t *testing.T) {
	file := filepath.Join(t.TempDir(), "absent.json")
	cfg, err := Load(file)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if !reflect.DeepEqual(cfg, Default()) {
		t.Fatalf("Load = %+v, want Default", cfg)
	}
}

func TestLoadPartialKeepsDefaults(t *testing.T) {
	cfg, err := Load(writeFile(t, `{"port": 9000}`))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 9000 || !cfg.Autostart || cfg.ModelsDir != "/var/lib/lightlix/models" {
		t.Fatalf("partial load = %+v", cfg)
	}
}

func TestLoadFullFile(t *testing.T) {
	body := `{
  "port": 8443,
  "lan_exposed": true,
  "autostart": false,
  "models_dir": "/srv/models",
  "hf_token": "hf_secret",
  "overrides": {"ngl": "all", "ctx": 4096, "cache_type": "q8_0", "batch": 1024, "load_mode": "mmap+mlock"}
}`
	cfg, err := Load(writeFile(t, body))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Port != 8443 || !cfg.LANExposed || cfg.Autostart || cfg.ModelsDir != "/srv/models" || cfg.HFToken != "hf_secret" {
		t.Fatalf("Load = %+v", cfg)
	}
	if cfg.Overrides.NGL == nil || *cfg.Overrides.NGL != "all" ||
		cfg.Overrides.Ctx == nil || *cfg.Overrides.Ctx != 4096 ||
		cfg.Overrides.CacheType == nil || *cfg.Overrides.CacheType != "q8_0" ||
		cfg.Overrides.Batch == nil || *cfg.Overrides.Batch != 1024 ||
		cfg.Overrides.LoadMode == nil || *cfg.Overrides.LoadMode != "mmap+mlock" {
		t.Fatalf("overrides = %+v", cfg.Overrides)
	}
}

func TestLoadRejectsBadFiles(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{"unknown field is an error", `{"prot": 9000}`},
		{"unknown field in overrides", `{"overrides": {"ngll": "all"}}`},
		{"trailing data", `{"port": 8080} {"port": 9}`},
		{"invalid JSON", `{"port": `},
		{"port zero", `{"port": 0}`},
		{"port too large", `{"port": 65536}`},
		{"relative models dir", `{"models_dir": "models"}`},
		{"empty models dir", `{"models_dir": ""}`},
		{"bad NGL override", `{"overrides": {"ngl": "-1"}}`},
		{"ctx override too small", `{"overrides": {"ctx": 10}}`},
		{"bad cache type override", `{"overrides": {"cache_type": "q5_0"}}`},
		{"bad load mode override", `{"overrides": {"load_mode": "--no-mmap"}}`},
		{"batch override zero", `{"overrides": {"batch": 0}}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := Load(writeFile(t, tc.body)); err == nil {
				t.Fatalf("Load accepted %s", tc.body)
			}
		})
	}
}

func TestSaveLoadRoundTripAndMode(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "lightlixd.json")
	ngl := "17"
	ctx := 8192
	in := Default()
	in.HFToken = "hf_token_value"
	in.Overrides = Overrides{NGL: &ngl, Ctx: &ctx}

	if err := Save(file, in); err != nil {
		t.Fatalf("Save: %v", err)
	}
	info, err := os.Stat(file)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if got := info.Mode().Perm(); got != 0o600 {
		t.Fatalf("mode = %o, want 600", got)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	if len(entries) != 1 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Fatalf("temp files left behind: %v", names)
	}

	out, err := Load(file)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if out.HFToken != in.HFToken || out.Port != in.Port || *out.Overrides.NGL != "17" || *out.Overrides.Ctx != 8192 {
		t.Fatalf("round trip changed the config: %+v", out)
	}
}

func TestSaveOverwritesExisting(t *testing.T) {
	file := writeFile(t, `{"port": 9000}`)
	cfg := Default()
	cfg.Port = 9100
	if err := Save(file, cfg); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(file)
	if err != nil || got.Port != 9100 {
		t.Fatalf("after overwrite: port=%d err=%v", got.Port, err)
	}
}

func TestSaveRefusesInvalidConfig(t *testing.T) {
	file := filepath.Join(t.TempDir(), "lightlixd.json")
	bad := Default()
	bad.ModelsDir = "relative"
	if err := Save(file, bad); err == nil {
		t.Fatalf("Save wrote an invalid config")
	}
	if _, err := os.Stat(file); !os.IsNotExist(err) {
		t.Fatalf("invalid save created a file: %v", err)
	}
}

func TestValidateReportsEveryProblem(t *testing.T) {
	bad := Default()
	bad.Port = 0
	bad.ModelsDir = "rel"
	ngl := "x"
	bad.Overrides.NGL = &ngl
	err := bad.Validate()
	if err == nil {
		t.Fatalf("Validate accepted a bad config")
	}
	for _, sub := range []string{"port 0", "models_dir", "NGL"} {
		if !strings.Contains(err.Error(), sub) {
			t.Errorf("error %q does not mention %q", err, sub)
		}
	}
}

func TestRedactedHidesTokenWithoutChangingOriginal(t *testing.T) {
	ngl := "all"
	cfg := Default()
	cfg.HFToken = "hf_secret_token"
	cfg.Overrides.NGL = &ngl

	red := cfg.Redacted()
	if red.HFToken == cfg.HFToken || strings.Contains(red.HFToken, "secret") {
		t.Fatalf("token not redacted: %q", red.HFToken)
	}
	if cfg.HFToken != "hf_secret_token" {
		t.Fatalf("Redacted changed the original token")
	}
	// 재정의 포인터는 깊이 복사해야 한다.
	*red.Overrides.NGL = "0"
	if *cfg.Overrides.NGL != "all" {
		t.Fatalf("Redacted shares the override pointer with the original")
	}
}

func TestRedactedWithoutToken(t *testing.T) {
	cfg := Default()
	if got := cfg.Redacted().HFToken; got != "" {
		t.Fatalf("empty token became %q", got)
	}
}
