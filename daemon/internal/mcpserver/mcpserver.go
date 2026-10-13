// Package mcpserver는 표준 입출력으로 말하는 최소한의 MCP(Model Context Protocol) 서버다.
//
// 프로토콜 형식: JSON-RPC 2.0 메시지를 한 줄에 하나씩 쓴다(MCP stdio 전송).
// 지원하는 것은 initialize, ping, tools/list, tools/call 이다. 도구만 제공하고 자원과 프롬프트는 없다.
// 표준 출력에는 프로토콜 메시지만 쓴다. 로그는 호출한 쪽이 정한 별도 Writer 로 간다.
package mcpserver

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
)

// 지원하는 프로토콜 판이다. 새 것부터 적는다. 클라이언트가 요청한 판이 여기 있으면 그것을, 아니면 첫 번째를 돌려준다.
var supportedVersions = []string{"2025-06-18", "2025-03-26", "2024-11-05"}

const (
	// maxLineBytes 는 한 메시지의 최대 크기다. 넘으면 그 줄을 버리고 오류를 돌려준다.
	maxLineBytes = 1 << 20
	// maxResultBytes 는 도구 결과 텍스트의 최대 크기다. 넘으면 잘라 알린다.
	maxResultBytes = 64 << 10
)

// JSON-RPC 오류 코드다.
const (
	codeParse          = -32700
	codeInvalidRequest = -32600
	codeMethodNotFound = -32601
	codeInvalidParams  = -32602
	codeInternal       = -32603
)

// Tool 은 클라이언트에 알리고 실행하는 도구 하나다.
type Tool struct {
	Name        string
	Title       string
	Description string
	// Schema 는 입력의 JSON Schema(object)다. nil 이면 인수가 없는 도구다.
	Schema map[string]any
	// ReadOnly 가 false 이면 시스템 상태를 바꾸는 도구다. 클라이언트가 확인을 받도록 힌트로 알린다.
	ReadOnly bool
	// Run 은 도구를 실행해 결과 텍스트를 돌려준다. 오류는 isError 결과로 전달한다.
	Run func(ctx context.Context, args map[string]any) (string, error)
}

// Server 는 도구 모음과 서버 정보다.
type Server struct {
	Name         string
	Version      string
	Instructions string
	Tools        []Tool
	// Logf 가 있으면 진단 한 줄씩을 보낸다(표준 오류 용).
	Logf func(format string, args ...any)
}

type request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

// Serve 는 r 에서 메시지를 읽고 w 에 응답을 쓴다. r 이 끝나거나 ctx 가 취소되면 돌아온다.
// 요청은 순서대로 처리한다. 도구 실행이 길어도 다음 줄은 기다린다(에이전트는 도구를 하나씩 기다린다).
func (s *Server) Serve(ctx context.Context, r io.Reader, w io.Writer) error {
	var mu sync.Mutex
	send := func(resp response) {
		resp.JSONRPC = "2.0"
		b, err := json.Marshal(resp)
		if err != nil {
			s.logf("cannot encode a response: %v", err)
			return
		}
		mu.Lock()
		defer mu.Unlock()
		_, _ = w.Write(append(b, '\n'))
	}

	br := bufio.NewReaderSize(r, 64<<10)
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		line, tooLong, err := readLine(br)
		if len(bytes.TrimSpace(line)) > 0 || tooLong {
			switch {
			case tooLong:
				send(response{ID: json.RawMessage("null"), Error: &rpcError{Code: codeInvalidRequest, Message: "the message is too large"}})
			default:
				s.handle(ctx, bytes.TrimSpace(line), send)
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
	}
}

// readLine 은 한 줄을 읽는다. maxLineBytes 를 넘는 줄은 끝까지 읽어 버리고 tooLong 을 돌려준다.
func readLine(br *bufio.Reader) (line []byte, tooLong bool, err error) {
	var buf []byte
	for {
		chunk, isPrefix, rerr := br.ReadLine()
		if rerr != nil {
			return buf, tooLong, rerr
		}
		if !tooLong {
			if len(buf)+len(chunk) > maxLineBytes {
				tooLong = true
				buf = nil
			} else {
				buf = append(buf, chunk...)
			}
		}
		if !isPrefix {
			return buf, tooLong, nil
		}
	}
}

func (s *Server) handle(ctx context.Context, raw []byte, send func(response)) {
	if len(raw) > 0 && raw[0] == '[' {
		send(response{ID: json.RawMessage("null"), Error: &rpcError{Code: codeInvalidRequest, Message: "batches are not supported"}})
		return
	}
	var req request
	if err := json.Unmarshal(raw, &req); err != nil {
		send(response{ID: json.RawMessage("null"), Error: &rpcError{Code: codeParse, Message: "the message is not valid JSON"}})
		return
	}
	isNotification := len(req.ID) == 0
	if req.Method == "" {
		// 응답이나 알 수 없는 메시지다. 응답은 무시한다(이 서버는 클라이언트에 요청하지 않는다).
		if !isNotification {
			send(response{ID: req.ID, Error: &rpcError{Code: codeInvalidRequest, Message: "method is missing"}})
		}
		return
	}
	reply := func(result any) {
		if !isNotification {
			send(response{ID: req.ID, Result: result})
		}
	}
	fail := func(code int, msg string) {
		if !isNotification {
			send(response{ID: req.ID, Error: &rpcError{Code: code, Message: msg}})
		}
	}

	switch req.Method {
	case "initialize":
		var p struct {
			ProtocolVersion string `json:"protocolVersion"`
		}
		_ = json.Unmarshal(req.Params, &p)
		version := supportedVersions[0]
		for _, v := range supportedVersions {
			if v == p.ProtocolVersion {
				version = v
			}
		}
		res := map[string]any{
			"protocolVersion": version,
			"capabilities":    map[string]any{"tools": map[string]any{"listChanged": false}},
			"serverInfo":      map[string]any{"name": s.Name, "version": s.Version},
		}
		if s.Instructions != "" {
			res["instructions"] = s.Instructions
		}
		reply(res)
	case "ping":
		reply(map[string]any{})
	case "tools/list":
		tools := make([]map[string]any, 0, len(s.Tools))
		sorted := append([]Tool(nil), s.Tools...)
		sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].Name < sorted[j].Name })
		for _, t := range sorted {
			schema := t.Schema
			if schema == nil {
				schema = map[string]any{"type": "object", "properties": map[string]any{}}
			}
			title := t.Title
			if title == "" {
				title = t.Name
			}
			tools = append(tools, map[string]any{
				"name":        t.Name,
				"title":       title,
				"description": t.Description,
				"inputSchema": schema,
				"annotations": map[string]any{"title": title, "readOnlyHint": t.ReadOnly, "openWorldHint": false},
			})
		}
		reply(map[string]any{"tools": tools})
	case "tools/call":
		var p struct {
			Name      string         `json:"name"`
			Arguments map[string]any `json:"arguments"`
		}
		if err := json.Unmarshal(req.Params, &p); err != nil || p.Name == "" {
			fail(codeInvalidParams, "tools/call needs a tool name")
			return
		}
		var tool *Tool
		for i := range s.Tools {
			if s.Tools[i].Name == p.Name {
				tool = &s.Tools[i]
			}
		}
		if tool == nil {
			fail(codeInvalidParams, fmt.Sprintf("unknown tool %q", p.Name))
			return
		}
		text, err := s.run(ctx, tool, p.Arguments)
		isErr := false
		if err != nil {
			text, isErr = err.Error(), true
		}
		reply(map[string]any{"content": []map[string]any{{"type": "text", "text": clip(text)}}, "isError": isErr})
	default:
		if strings.HasPrefix(req.Method, "notifications/") {
			return
		}
		fail(codeMethodNotFound, "method not found: "+req.Method)
	}
}

// run 은 도구를 실행한다. 도구가 패닉해도 서버는 계속 돈다.
func (s *Server) run(ctx context.Context, t *Tool, args map[string]any) (text string, err error) {
	defer func() {
		if r := recover(); r != nil {
			s.logf("tool %s panicked: %v", t.Name, r)
			text, err = "", errors.New("the tool failed unexpectedly")
		}
	}()
	if args == nil {
		args = map[string]any{}
	}
	return t.Run(ctx, args)
}

func clip(s string) string {
	if len(s) <= maxResultBytes {
		return s
	}
	cut := maxResultBytes
	for cut > 0 && s[cut]&0xC0 == 0x80 { // UTF-8 중간에서 자르지 않는다
		cut--
	}
	return s[:cut] + "\n... (truncated)"
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}
