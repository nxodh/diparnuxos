package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/config"
	"github.com/nxodh/diparnuxos/daemon/internal/platform"
	"github.com/nxodh/diparnuxos/daemon/internal/report"
)

type secretRunner struct{ output string }

func (r secretRunner) Run(context.Context, platform.Command) (platform.Result, error) {
	return platform.Result{Stdout: []byte(r.output)}, nil
}

func TestReportRedactsActiveRuntimeKeySnapshotAfterStoreDeletion(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o750); err != nil {
		t.Fatal(err)
	}
	keyFile := filepath.Join(dir, "api-keys")
	snapshot := filepath.Join(dir, "api-keys.start-test.key")
	secret := "dpx_" + strings.Repeat("A", 43)
	if err := os.WriteFile(snapshot, []byte(secret+"\n"), 0o640); err != nil {
		t.Fatal(err)
	}
	secrets := secretsFunc(keyFile, dir, func() config.Config { return config.Default() })
	out := report.Build(context.Background(), report.Deps{
		Runner:  secretRunner{output: "diagnostic output contains " + secret},
		Secrets: secrets,
	})
	if strings.Contains(out, secret) {
		t.Fatal("diagnostic report leaked a key held only by the active runtime snapshot")
	}
	if !strings.Contains(out, "<redacted>") {
		t.Fatal("diagnostic report did not mark the active runtime key as redacted")
	}
}
