package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
)

func newTestServer() *Server {
	return &Server{
		Name: "opereta", Version: "0.5.3", Instructions: "read-only",
		Tools: []Tool{
			{Name: "zeta", Description: "z", ReadOnly: true, Run: func(ctx context.Context, a map[string]any) (string, error) { return "z-result", nil }},
			{Name: "alpha", Description: "a", ReadOnly: true, Schema: map[string]any{"type": "object", "properties": map[string]any{"n": map[string]any{"type": "number"}}},
				Run: func(ctx context.Context, a map[string]any) (string, error) {
					return fmt.Sprint(a["n"]), nil
				}},
			{Name: "boom", Run: func(ctx context.Context, a map[string]any) (string, error) { return "", errors.New("it failed") }},
			{Name: "panic", Run: func(ctx context.Context, a map[string]any) (string, error) { panic("secret internals") }},
			{Name: "huge", ReadOnly: true, Run: func(ctx context.Context, a map[string]any) (string, error) {
				return strings.Repeat("x", maxResultBytes+10), nil
			}},
		},
	}
}

func talk(t *testing.T, s *Server, in string) []map[string]any {
	t.Helper()
	var out bytes.Buffer
	if err := s.Serve(context.Background(), strings.NewReader(in), &out); err != nil {
		t.Fatal(err)
	}
	var msgs []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("stdout carries a line that is not JSON: %q", line)
		}
		msgs = append(msgs, m)
	}
	return msgs
}

func TestInitializeNegotiatesVersionAndAdvertisesTools(t *testing.T) {
	msgs := talk(t, newTestServer(), `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-03-26","capabilities":{},"clientInfo":{"name":"vedecaps","version":"0.1.3"}}}
{"jsonrpc":"2.0","method":"notifications/initialized"}
{"jsonrpc":"2.0","id":2,"method":"initialize","params":{"protocolVersion":"2030-01-01"}}
`)
	if len(msgs) != 2 {
		t.Fatalf("a notification must not be answered: %v", msgs)
	}
	r := msgs[0]["result"].(map[string]any)
	if r["protocolVersion"] != "2025-03-26" || r["serverInfo"].(map[string]any)["name"] != "opereta" {
		t.Fatalf("result = %v", r)
	}
	if _, ok := r["capabilities"].(map[string]any)["tools"]; !ok {
		t.Fatalf("capabilities = %v", r["capabilities"])
	}
	if msgs[1]["result"].(map[string]any)["protocolVersion"] != "2025-06-18" {
		t.Fatalf("unknown version must fall back to the newest: %v", msgs[1])
	}
}

func TestToolsListIsSortedAndMarksReadOnly(t *testing.T) {
	msgs := talk(t, newTestServer(), `{"jsonrpc":"2.0","id":"a","method":"tools/list"}`+"\n")
	tools := msgs[0]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, x := range tools {
		tool := x.(map[string]any)
		names = append(names, tool["name"].(string))
		if tool["inputSchema"].(map[string]any)["type"] != "object" {
			t.Fatalf("schema = %v", tool["inputSchema"])
		}
		ro := tool["annotations"].(map[string]any)["readOnlyHint"].(bool)
		wantRO := tool["name"] == "alpha" || tool["name"] == "huge" || tool["name"] == "zeta"
		if ro != wantRO {
			t.Fatalf("%v readOnlyHint = %v", tool["name"], ro)
		}
	}
	if strings.Join(names, ",") != "alpha,boom,huge,panic,zeta" {
		t.Fatalf("names = %v", names)
	}
	if msgs[0]["id"] != "a" {
		t.Fatalf("string ids must be echoed: %v", msgs[0]["id"])
	}
}

func TestToolsCall(t *testing.T) {
	msgs := talk(t, newTestServer(), strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"alpha","arguments":{"n":7}}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"boom"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"panic"}}`,
		`{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nope"}}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":{"name":"huge"}}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{}}`,
	}, "\n")+"\n")
	text := func(m map[string]any) (string, bool) {
		r := m["result"].(map[string]any)
		c := r["content"].([]any)[0].(map[string]any)
		return c["text"].(string), r["isError"].(bool)
	}
	if s, e := text(msgs[0]); s != "7" || e {
		t.Fatalf("alpha = %q %v", s, e)
	}
	if s, e := text(msgs[1]); s != "it failed" || !e {
		t.Fatalf("boom = %q %v", s, e)
	}
	if s, e := text(msgs[2]); !e || strings.Contains(s, "secret internals") {
		t.Fatalf("panic leaked: %q %v", s, e)
	}
	if msgs[3]["error"].(map[string]any)["code"].(float64) != codeInvalidParams {
		t.Fatalf("unknown tool = %v", msgs[3])
	}
	if s, _ := text(msgs[4]); !strings.HasSuffix(s, "(truncated)") || len(s) > maxResultBytes+40 {
		t.Fatalf("huge result len = %d", len(s))
	}
	if msgs[5]["error"].(map[string]any)["code"].(float64) != codeInvalidParams {
		t.Fatalf("missing name = %v", msgs[5])
	}
}

func TestProtocolErrorsKeepTheServerAlive(t *testing.T) {
	big := `{"jsonrpc":"2.0","id":9,"method":"ping","params":{"x":"` + strings.Repeat("a", maxLineBytes) + `"}}`
	msgs := talk(t, newTestServer(), strings.Join([]string{
		`not json`,
		`[{"jsonrpc":"2.0","id":1,"method":"ping"}]`,
		`{"jsonrpc":"2.0","id":2,"method":"resources/list"}`,
		big,
		`{"jsonrpc":"2.0","id":3,"method":"ping"}`,
	}, "\n")+"\n")
	if len(msgs) != 5 {
		t.Fatalf("got %d messages: %v", len(msgs), msgs)
	}
	codes := []float64{codeParse, codeInvalidRequest, codeMethodNotFound, codeInvalidRequest}
	for i, c := range codes {
		if msgs[i]["error"].(map[string]any)["code"].(float64) != c {
			t.Fatalf("message %d = %v, want code %v", i, msgs[i], c)
		}
	}
	if _, ok := msgs[4]["result"]; !ok || msgs[4]["id"].(float64) != 3 {
		t.Fatalf("the server did not recover: %v", msgs[4])
	}
}

func TestServeStopsWhenContextIsCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := newTestServer().Serve(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &bytes.Buffer{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
}
