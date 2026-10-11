package report

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
	"github.com/nxodh/lightlixos/daemon/internal/supervisor"
)

const (
	plainKey  = "llx_PLAINKEYabcdefghijklmnopqrstuvwxyz0123456789ABC"
	hfToken   = "hf_HFTOKENabcdefghijklmnopqrstuv"
	bearerKey = "llx_UNLISTEDKEYabcdefghijklmnopqrstuvwxyz"
)

// fakeRunner는 명령마다 다른 출력을 낸다. journalctl 에는 비밀을 섞는다.
type fakeRunner struct{}

func (fakeRunner) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	switch cmd.Name {
	case "nvidia-smi":
		return platform.Result{}, errors.New("executable file not found")
	case "uname":
		return platform.Result{Stdout: []byte("6.12.0-test\n")}, nil
	case "journalctl":
		text := "llama: loaded model 모델 with key " + plainKey + "\nAuthorization: Bearer " + bearerKey + "\n"
		return platform.Result{Stdout: []byte(text)}, nil
	}
	return platform.Result{Stdout: []byte("ok\n")}, nil
}

// fixedNow는 고정 시각이다.
func fixedNow() time.Time { return time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC) }

func TestBuildRedactsSecretsAndIsASCII(t *testing.T) {
	dir := t.TempDir()
	envPath := filepath.Join(dir, "llama-server.env")
	// 환경 파일에는 비밀이 없어야 하지만, 실수로 들어가도 리포트에서 가린다.
	if err := os.WriteFile(envPath, []byte("LLAMA_ARG_API_KEY_FILE=\"/x\"\nLEAK=\""+plainKey+"\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.ModelsDir = dir
	cfg.HFToken = hfToken

	d := Deps{
		Version:       "0.1.0",
		Now:           fixedNow,
		Runner:        fakeRunner{},
		GPU:           nil,
		MemInfo:       func() (platform.MemInfo, error) { return platform.MemInfo{TotalMiB: 32768, AvailableMiB: 20000}, nil },
		Config:        func() (config.Config, error) { return cfg, nil },
		KeyCount:      func() (int, error) { return 1, nil },
		Models:        models.Store{Dir: dir},
		Server:        func() supervisor.Status { return supervisor.Status{State: supervisor.StateStopped} },
		Secrets:       func() []string { return []string{plainKey, hfToken} },
		GPUStatusPath: filepath.Join(dir, "missing-gpu-status"),
		EnvFilePath:   envPath,
		OSReleasePath: filepath.Join(dir, "missing-os-release"),
	}
	out := Build(context.Background(), d)

	for _, secret := range []string{plainKey, hfToken, bearerKey, "HFTOKENabcdef"} {
		if strings.Contains(out, secret) {
			t.Fatalf("report leaks %q", secret)
		}
	}
	if !strings.Contains(out, "<redacted>") {
		t.Fatal("report has no redaction marker")
	}
	for i := 0; i < len(out); i++ {
		c := out[i]
		if c != '\n' && c != '\t' && (c < 0x20 || c > 0x7e) {
			t.Fatalf("non-ASCII or control byte 0x%02x at %d", c, i)
		}
	}
	if !strings.Contains(out, "count: 1 (key values are not included)") {
		t.Fatal("key count line missing")
	}
	if !strings.Contains(out, "nvidia-smi\nnot available: executable file not found") {
		t.Fatalf("missing command failure not recorded:\n%s", out)
	}
	if !strings.Contains(out, "6.12.0-test") {
		t.Fatal("kernel line missing")
	}
	if !strings.Contains(out, "generated: 2026-10-11T09:00:00Z") || !strings.Contains(out, "daemon version: 0.1.0") {
		t.Fatal("header missing")
	}
}

func TestBuildWithoutDependenciesStillReports(t *testing.T) {
	out := Build(context.Background(), Deps{})
	for _, section := range []string{"== kernel ==", "== GPU status file ==", "== commands =="} {
		if !strings.Contains(out, section) {
			t.Fatalf("section %q missing", section)
		}
	}
	if !strings.Contains(out, "not configured") {
		t.Fatal("unconfigured dependencies should be reported as not configured")
	}
}

func TestRedactPatternsWithoutInjectedSecrets(t *testing.T) {
	got := redact("a "+bearerKey+" b "+hfToken, nil)
	if strings.Contains(got, bearerKey) || strings.Contains(got, "HFTOKEN") {
		t.Fatalf("pattern redaction failed: %q", got)
	}
}

func TestSanitizeReplacesNonASCII(t *testing.T) {
	// 한글 두 글자는 UTF-8 에서 여섯 바이트이고 \x01 은 한 바이트이므로 물음표 일곱 개가 된다.
	if got := sanitize("ok 한글\x01\n\ttab"); got != "ok ???????\n\ttab" {
		t.Fatalf("sanitize = %q", got)
	}
}
