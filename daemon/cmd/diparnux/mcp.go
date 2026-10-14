package main

import (
	"context"
	"fmt"

	"github.com/nxodh/diparnuxos/daemon/internal/client"
	"github.com/nxodh/diparnuxos/daemon/internal/mcpserver"
)

const mcpInstructions = "Diparnux runs a local language model server on this computer. " +
	"These tools read its state: whether the server runs, which model and context it uses, " +
	"how to connect, memory and GPU use, and what the computer exposes to the network. " +
	"Use them to answer questions about the machine you run on or to find out why the model server is slow or stopped."

// mcp 는 Diparnux 의 상태를 도구로 내놓는 MCP 서버를 표준 입출력으로 돌린다.
// Vedecaps 같은 에이전트의 설정에서 {"type":"local","command":["diparnux","mcp"]} 로 부른다.
func (c *cli) mcp(args []string) int {
	fs := newFlags("mcp")
	allowControl := fs.Bool("allow-control", false, "also offer tools that start and stop the model server")
	pos, code, ok := c.parse(fs, "mcp", args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return c.usage("mcp takes no arguments", "mcp")
	}

	srv := &mcpserver.Server{
		Name:         "diparnux",
		Version:      version,
		Instructions: mcpInstructions,
		Tools:        c.mcpTools(*allowControl),
		Logf: func(format string, a ...any) {
			fmt.Fprintf(c.env.stderr, "diparnux mcp: "+format+"\n", a...)
		},
	}
	if err := srv.Serve(c.env.ctx, c.env.stdin, c.env.stdout); err != nil && c.env.ctx.Err() == nil {
		c.errorLine(err.Error())
		return exitFailure
	}
	return exitOK
}

// mcpTools 는 도구 목록을 만든다. 읽기 전용 도구는 항상, 서버를 바꾸는 도구는 허용했을 때만 넣는다.
func (c *cli) mcpTools(allowControl bool) []mcpserver.Tool {
	// 각 도구는 데몬이 돌려준 JSON 을 그대로 글로 넘긴다. 데몬 응답에는 API 키와 토큰이 들어 있지 않다.
	read := func(name, title, desc string, schema map[string]any, call func(ctx context.Context, args map[string]any) ([]byte, error)) mcpserver.Tool {
		return mcpserver.Tool{Name: name, Title: title, Description: desc, Schema: schema, ReadOnly: true,
			Run: func(ctx context.Context, args map[string]any) (string, error) {
				ctx, cancel := context.WithTimeout(ctx, chatTimeout)
				defer cancel()
				raw, err := call(ctx, args)
				if err != nil {
					_, msg := describeError(err)
					return "", fmt.Errorf("%s", msg)
				}
				return string(raw), nil
			}}
	}
	str := func(args map[string]any, key string) string {
		s, _ := args[key].(string)
		return s
	}

	tools := []mcpserver.Tool{
		read("diparnux_status", "Diparnux status", "Version, mode, GPU, memory and the state of the model server.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.Status(ctx)
				return raw, err
			}),
		read("diparnux_server_stats", "Model server load", "Busy slots, tokens per second, queued requests and gateway counters of the running model server.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.ServerStats(ctx)
				return raw, err
			}),
		read("diparnux_connection", "How to connect", "The addresses, whether an API key is needed, the model name and the context of one request.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.Connection(ctx)
				return raw, err
			}),
		read("diparnux_models", "Installed models", "The installed GGUF models and the default one.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.Models(ctx)
				return raw, err
			}),
		read("diparnux_plan", "Run plan", "The plan the server would use for a model: GPU layers, context per request, slots and why.",
			map[string]any{"type": "object", "properties": map[string]any{
				"model":   map[string]any{"type": "string", "description": "Model id. Omit for the default model."},
				"profile": map[string]any{"type": "string", "enum": []string{"chat", "agent"}, "description": "chat (default) or agent (long context, two slots)."},
			}},
			func(ctx context.Context, args map[string]any) ([]byte, error) {
				var ov client.Overrides
				if p := str(args, "profile"); p != "" {
					ov.Profile = &p
				}
				_, raw, err := c.api.Plan(ctx, str(args, "model"), ov)
				return raw, err
			}),
		read("diparnux_resources", "Resource use", "CPU, memory, disks, network, temperature and GPU use of this computer.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.Resources(ctx)
				return raw, err
			}),
		read("diparnux_exposure", "Network exposure", "What this computer offers to the network, with warnings for risky settings.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.Exposure(ctx)
				return raw, err
			}),
		read("diparnux_toolcheck", "Tool-call check", "Send the model one small request and report whether it calls tools correctly.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.ToolCheck(ctx)
				return raw, err
			}),
		read("diparnux_logs", "Server log", "The last lines of the model server log.",
			map[string]any{"type": "object", "properties": map[string]any{
				"lines": map[string]any{"type": "integer", "minimum": 1, "maximum": logLinesLimit, "description": "Number of lines (default 100)."},
			}},
			func(ctx context.Context, args map[string]any) ([]byte, error) {
				n := 100
				if v, ok := args["lines"].(float64); ok && v >= 1 && v <= logLinesLimit {
					n = int(v)
				}
				_, raw, err := c.api.Logs(ctx, n)
				return raw, err
			}),
	}
	if !allowControl {
		return tools
	}
	control := func(name, title, desc string, schema map[string]any, call func(ctx context.Context, args map[string]any) ([]byte, error)) mcpserver.Tool {
		t := read(name, title, desc, schema, call)
		t.ReadOnly = false
		return t
	}
	return append(tools,
		control("diparnux_server_start", "Start the model server", "Start the model server (it loads in the background; check diparnux_status afterwards).",
			map[string]any{"type": "object", "properties": map[string]any{
				"model":   map[string]any{"type": "string", "description": "Model id. Omit for the default model."},
				"profile": map[string]any{"type": "string", "enum": []string{"chat", "agent"}},
			}},
			func(ctx context.Context, args map[string]any) ([]byte, error) {
				var ov client.Overrides
				if p := str(args, "profile"); p != "" {
					ov.Profile = &p
				}
				_, raw, err := c.api.Start(ctx, client.StartRequest{Model: str(args, "model"), Overrides: ov, Wait: false})
				return raw, err
			}),
		control("diparnux_server_stop", "Stop the model server", "Stop the model server. Programs using it lose their connection.", nil,
			func(ctx context.Context, _ map[string]any) ([]byte, error) {
				_, raw, err := c.api.Stop(ctx)
				return raw, err
			}),
	)
}
