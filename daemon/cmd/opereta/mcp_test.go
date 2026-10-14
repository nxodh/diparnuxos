package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func mcpTalk(t *testing.T, f *fakeDaemon, args []string, lines ...string) []map[string]any {
	t.Helper()
	res := runCLIWith(t, f, cliOpts{stdin: strings.Join(lines, "\n") + "\n"}, append([]string{"mcp"}, args...)...)
	wantCode(t, res, exitOK)
	var out []map[string]any
	for _, l := range strings.Split(strings.TrimSpace(res.stdout), "\n") {
		if l == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(l), &m); err != nil {
			t.Fatalf("stdout line is not JSON: %q", l)
		}
		out = append(out, m)
	}
	return out
}

func toolNames(t *testing.T, msg map[string]any) []string {
	t.Helper()
	var names []string
	for _, x := range msg["result"].(map[string]any)["tools"].([]any) {
		names = append(names, x.(map[string]any)["name"].(string))
	}
	return names
}

func TestMCPOffersOnlyReadOnlyToolsByDefault(t *testing.T) {
	f := newFakeDaemon(t)
	msgs := mcpTalk(t, f, nil,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`)
	if len(msgs) != 2 {
		t.Fatalf("messages = %v", msgs)
	}
	names := strings.Join(toolNames(t, msgs[1]), ",")
	for _, want := range []string{"opereta_status", "opereta_connection", "opereta_plan", "opereta_exposure", "opereta_toolcheck"} {
		if !strings.Contains(names, want) {
			t.Fatalf("tools = %s, missing %s", names, want)
		}
	}
	if strings.Contains(names, "start") || strings.Contains(names, "stop") {
		t.Fatalf("control tools offered without --allow-control: %s", names)
	}

	msgs = mcpTalk(t, f, []string{"--allow-control"}, `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`)
	names = strings.Join(toolNames(t, msgs[0]), ",")
	if !strings.Contains(names, "opereta_server_start") || !strings.Contains(names, "opereta_server_stop") {
		t.Fatalf("tools = %s", names)
	}
	for _, x := range msgs[0]["result"].(map[string]any)["tools"].([]any) {
		tool := x.(map[string]any)
		ro := tool["annotations"].(map[string]any)["readOnlyHint"].(bool)
		control := tool["name"] == "opereta_server_start" || tool["name"] == "opereta_server_stop"
		if ro == control {
			t.Fatalf("%v readOnlyHint = %v", tool["name"], ro)
		}
	}
}

func TestMCPToolCallsForwardToTheDaemon(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/server/stats", 200, `{"running":true,"state":"running","slots":{"available":true,"total":2,"busy":1}}`)
	f.on("POST /v1/server/toolcheck", 200, `{"ok":true,"code":"ok","message":"the model calls tools correctly","finish_reason":"tool_calls","latency_ms":10}`)
	f.on("POST /v1/server/stop", 200, `{"state":"stopped"}`)
	text := func(m map[string]any) (string, bool) {
		r := m["result"].(map[string]any)
		return r["content"].([]any)[0].(map[string]any)["text"].(string), r["isError"].(bool)
	}

	msgs := mcpTalk(t, f, nil,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"opereta_server_stats"}}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"opereta_toolcheck"}}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"opereta_server_stop"}}`)
	if s, e := text(msgs[0]); e || !strings.Contains(s, `"busy":1`) {
		t.Fatalf("stats = %q %v", s, e)
	}
	if s, e := text(msgs[1]); e || !strings.Contains(s, `"ok":true`) {
		t.Fatalf("toolcheck = %q %v", s, e)
	}
	if msgs[2]["error"] == nil {
		t.Fatalf("the stop tool ran without --allow-control: %v", msgs[2])
	}
	if got := len(f.bodiesOf("POST /v1/server/stop")); got != 0 {
		t.Fatalf("stop reached the daemon %d times", got)
	}

	msgs = mcpTalk(t, f, []string{"--allow-control"},
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"opereta_server_stop"}}`)
	if s, e := text(msgs[0]); e || !strings.Contains(s, "stopped") {
		t.Fatalf("stop = %q %v", s, e)
	}
}

func TestMCPDaemonErrorsBecomeToolErrors(t *testing.T) {
	f := newFakeDaemon(t)
	f.on("GET /v1/status", 500, `{"error":{"code":"internal","message":"boom"}}`)
	msgs := mcpTalk(t, f, nil, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"opereta_status"}}`)
	r := msgs[0]["result"].(map[string]any)
	if r["isError"] != true || !strings.Contains(r["content"].([]any)[0].(map[string]any)["text"].(string), "boom") {
		t.Fatalf("result = %v", r)
	}
}

func TestMCPEndsCleanlyWhenInputCloses(t *testing.T) {
	f := newFakeDaemon(t)
	res := runCLIWith(t, f, cliOpts{stdin: ""}, "mcp")
	wantCode(t, res, exitOK)
	if res.stdout != "" {
		t.Fatalf("stdout = %q", res.stdout)
	}
}
