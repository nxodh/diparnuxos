package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
)

// toolCheckResult 는 POST /v1/server/toolcheck 의 응답이다.
// 코딩 에이전트는 모델이 도구 호출을 돌려줘야 일할 수 있다. 이 시험은 작은 요청 한 번으로 그걸 확인한다.
type toolCheckResult struct {
	OK bool `json:"ok"`
	// Code 는 ok, no_tool_call, bad_arguments, wrong_tool 중 하나다.
	Code    string `json:"code"`
	Message string `json:"message"`
	// FinishReason 은 서버가 돌려준 종료 이유다. 도구를 불렀다면 보통 tool_calls 다.
	FinishReason string `json:"finish_reason"`
	LatencyMS    int64  `json:"latency_ms"`
}

type upstreamToolChat struct {
	Choices []struct {
		FinishReason string `json:"finish_reason"`
		Message      struct {
			Content   string `json:"content"`
			ToolCalls []struct {
				Function struct {
					Name      string          `json:"name"`
					Arguments json.RawMessage `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
}

// toolCheckPayload 는 시험 요청이다. 답이 정해진 도구 하나와 그 도구를 쓰라는 질문만 담는다.
const toolCheckPayload = `{
 "messages":[{"role":"user","content":"What time is it in Seoul right now? Use the get_time tool."}],
 "tools":[{"type":"function","function":{"name":"get_time","description":"Get the current time in a city.",
   "parameters":{"type":"object","properties":{"city":{"type":"string","description":"City name"}},"required":["city"]}}}],
 "tool_choice":"auto","max_tokens":128,"temperature":0,"stream":false}`

// evaluateToolCheck 는 서버 응답이 도구 호출인지 판정한다.
func evaluateToolCheck(data []byte) toolCheckResult {
	var parsed upstreamToolChat
	if err := json.Unmarshal(data, &parsed); err != nil || len(parsed.Choices) == 0 {
		return toolCheckResult{Code: "no_tool_call", Message: "the server answer has no choices"}
	}
	ch := parsed.Choices[0]
	res := toolCheckResult{FinishReason: ch.FinishReason}
	if len(ch.Message.ToolCalls) == 0 {
		res.Code = "no_tool_call"
		res.Message = "the model answered in text instead of calling the tool. Coding agents need a model whose chat template supports tool calls; pick a model marked for agents"
		return res
	}
	call := ch.Message.ToolCalls[0].Function
	if call.Name != "get_time" {
		res.Code = "wrong_tool"
		res.Message = "the model called a tool that was not offered: " + call.Name
		return res
	}
	// arguments 는 JSON 문자열(OpenAI 방식)이거나 객체일 수 있다. 둘 다 객체로 풀려야 한다.
	raw := call.Arguments
	var asString string
	if json.Unmarshal(raw, &asString) == nil {
		raw = json.RawMessage(asString)
	}
	var args map[string]any
	if err := json.Unmarshal(raw, &args); err != nil || args == nil {
		res.Code = "bad_arguments"
		res.Message = "the tool call arguments are not a valid JSON object"
		return res
	}
	res.OK = true
	res.Code = "ok"
	res.Message = "the model calls tools correctly"
	return res
}

// postToolCheck 는 실행 중인 서버에 도구 호출 시험 요청을 한 번 보낸다.
func (s *server) postToolCheck(w http.ResponseWriter, r *http.Request) {
	st := s.d.Supervisor.Status()
	if st.State != supervisor.StateRunning && st.State != supervisor.StateRunningCPU {
		writeError(w, http.StatusConflict, "server_not_running", "the server is not running (state %s)", st.State)
		return
	}
	key, err := s.firstKey()
	if err != nil {
		writeErr(w, err)
		return
	}
	backendPort := st.BackendPort
	if backendPort == 0 {
		backendPort = st.Port
	}
	ctx, cancel := context.WithTimeout(r.Context(), chatTimeout)
	defer cancel()
	up, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(backendPort)+"/v1/chat/completions", strings.NewReader(toolCheckPayload))
	if err != nil {
		writeErr(w, err)
		return
	}
	up.Header.Set("Content-Type", "application/json")
	if key != "" {
		up.Header.Set("Authorization", "Bearer "+key)
	}
	started := time.Now()
	resp, err := s.d.HTTP.Do(up)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_unreachable", "llama-server did not answer: %v", errorKind(err))
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBytes+1))
	if err != nil || len(data) > maxUpstreamBytes {
		writeError(w, http.StatusBadGateway, "upstream_bad_response", "the llama-server answer could not be read")
		return
	}
	if resp.StatusCode != http.StatusOK {
		// 도구를 받지 못하는 채팅 틀은 보통 500 으로 끝난다. 그 자체가 시험 결과다.
		writeJSON(w, http.StatusOK, toolCheckResult{
			Code:      "no_tool_call",
			Message:   "the server refused a request that offers tools (HTTP " + strconv.Itoa(resp.StatusCode) + "). Its chat template probably has no tool support",
			LatencyMS: time.Since(started).Milliseconds(),
		})
		return
	}
	res := evaluateToolCheck(data)
	res.LatencyMS = time.Since(started).Milliseconds()
	writeJSON(w, http.StatusOK, res)
}
