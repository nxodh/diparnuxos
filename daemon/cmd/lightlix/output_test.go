package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

func TestASCIIWriterReplacesNonASCII(t *testing.T) {
	var buf bytes.Buffer
	w := asciiWriter{w: &buf}
	n, err := w.Write([]byte("한글 ok\tx\r\n\x01end"))
	if err != nil {
		t.Fatalf("Write: %v", err)
	}
	if n != len("한글 ok\tx\r\n\x01end") {
		t.Fatalf("Write returned %d, want the input length", n)
	}
	want := "?? ok\tx\r\n?end"
	if buf.String() != want {
		t.Fatalf("output = %q, want %q", buf.String(), want)
	}
	assertASCII(t, "writer", buf.String())
}

func TestEscapeNonASCIIJSONRoundTrips(t *testing.T) {
	in := `{"name":"모델é😀","n":1}`
	out := escapeNonASCII([]byte(in))
	assertASCII(t, "escaped", string(out))
	var got map[string]any
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatalf("escaped output is not JSON: %v\n%s", err, out)
	}
	if got["name"] != "모델é😀" {
		t.Fatalf("round trip changed the value: %q", got["name"])
	}
}

func TestJSONOutputEscapesNonASCIIFromAPI(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/models", 200, `{"default":"","models":[{"id":"모델.gguf","size_bytes":10,"default":false,"incomplete":false,"block_count":0,"context_length":0,"embedding_length":0}]}`)
	res := runCLI(t, f, "--json", "model", "list")
	wantCode(t, res, exitOK)
	if strings.Contains(res.stdout, "모델") {
		t.Fatalf("JSON output must not contain raw non-ASCII text:\n%s", res.stdout)
	}
	var got struct {
		Models []struct {
			ID string `json:"id"`
		} `json:"models"`
	}
	if err := json.Unmarshal([]byte(res.stdout), &got); err != nil {
		t.Fatalf("stdout is not JSON: %v\n%s", err, res.stdout)
	}
	if len(got.Models) != 1 || got.Models[0].ID != "모델.gguf" {
		t.Fatalf("decoded models = %+v", got.Models)
	}
}

func TestHumanOutputIsASCIIWhenAPIDataIsNot(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/models", 200, `{"default":"","models":[{"id":"모델.gguf","size_bytes":10,"default":false,"incomplete":false,"block_count":0,"context_length":0,"embedding_length":0}]}`)
	res := runCLI(t, f, "model", "list")
	wantCode(t, res, exitOK)
	wantContains(t, "list", res.stdout, "??.gguf")
}

func TestErrorMessageFromAPIIsASCII(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("DELETE /v1/models/x", 404, `{"error":{"code":"not_found","message":"모델 \"x\" was not found"}}`)
	res := runCLI(t, f, "model", "rm", "x")
	wantCode(t, res, exitFailure)
	wantContains(t, "error", res.stderr, `?? "x" was not found (not_found)`)
}

func TestLogsAreASCII(t *testing.T) {
	f := newFakeDaemon(t)
	f.onFunc("GET /v1/server/logs", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"lines":2,"log":"ok line\n로그 줄\n"}`))
	})
	res := runCLI(t, f, "serve", "logs", "-n", "2")
	wantCode(t, res, exitOK)
	if res.stdout != "ok line\n?? ?\n" {
		t.Fatalf("logs = %q", res.stdout)
	}
}

func TestNoANSIEscapesInHumanOutput(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 200, statusReadyRunning)
	res := runCLI(t, f, "status")
	wantCode(t, res, exitOK)
	if strings.Contains(res.stdout, "\x1b[") {
		t.Fatalf("human output must not contain color escapes")
	}
}
