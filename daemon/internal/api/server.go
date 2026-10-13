package api

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
	"github.com/nxodh/diparnuxos/daemon/internal/supervisor"
)

// 채팅 프록시의 상한과 시간 제한이다.
const (
	chatTimeout       = 120 * time.Second
	maxChatMessages   = 64
	maxChatContent    = 16 << 10
	maxChatTotalBytes = 64 << 10
	defaultMaxTokens  = 512
	maxMaxTokens      = 4096
	maxUpstreamBytes  = 4 << 20
	defaultLogLines   = 200
	maxLogLinesAPI    = 2000
)

// overridesJSON 은 요청 본문의 재정의다. 모든 값은 선택이고, 없으면 자동 계획을 쓴다.
// ngl 은 문자열("auto", "all", 정수 문자열)이다.
type overridesJSON struct {
	NGL       *string `json:"ngl"`
	Ctx       *int    `json:"ctx"`
	CacheType *string `json:"cache_type"`
	Batch     *int    `json:"batch"`
	LoadMode  *string `json:"load_mode"`
	Parallel  *int    `json:"parallel"`
}

// toAutoconf 는 요청 재정의를 autoconf 타입으로 바꾼다.
func (o overridesJSON) toAutoconf() autoconf.Overrides {
	return autoconf.Overrides{NGL: o.NGL, Ctx: o.Ctx, CacheType: o.CacheType, Batch: o.Batch, LoadMode: o.LoadMode, Parallel: o.Parallel}
}

// getPlan 은 계획을 계산해 돌려준다. 재정의는 쿼리로 받는다: ngl, ctx, cache_type, batch, load_mode, parallel.
func (s *server) getPlan(w http.ResponseWriter, r *http.Request) {
	if !rejectUnknownQuery(w, r, "model", "ngl", "ctx", "cache_type", "batch", "load_mode", "parallel") {
		return
	}
	q := r.URL.Query()
	var ov overridesJSON
	if q.Has("ngl") {
		v := q.Get("ngl")
		ov.NGL = &v
	}
	if q.Has("cache_type") {
		v := q.Get("cache_type")
		ov.CacheType = &v
	}
	if q.Has("load_mode") {
		v := q.Get("load_mode")
		ov.LoadMode = &v
	}
	if q.Has("ctx") {
		v, ok := intQuery(w, r, "ctx", 0, -1<<31, 1<<31-1)
		if !ok {
			return
		}
		ov.Ctx = &v
	}
	if q.Has("batch") {
		v, ok := intQuery(w, r, "batch", 0, -1<<31, 1<<31-1)
		if !ok {
			return
		}
		ov.Batch = &v
	}
	if q.Has("parallel") {
		v, ok := intQuery(w, r, "parallel", 0, -1<<31, 1<<31-1)
		if !ok {
			return
		}
		ov.Parallel = &v
	}
	pr, err := s.d.Supervisor.Plan(r.Context(), q.Get("model"), ov.toAutoconf())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, planResultOf(pr))
}

// postStart 는 llama-server 를 시작한다. 본문은 {"model","overrides","wait"} 다.
// wait 가 false 면 202 와 starting 상태를 바로 돌려준다. true 면 끝까지 기다린 뒤 200 이나 오류를 돌려준다.
func (s *server) postStart(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Model     string        `json:"model"`
		Overrides overridesJSON `json:"overrides"`
		Wait      bool          `json:"wait"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	st, err := s.d.Supervisor.StartAsync(supervisor.StartRequest{ModelID: body.Model, Overrides: body.Overrides.toAutoconf()})
	if err != nil {
		writeErr(w, err)
		return
	}
	if !body.Wait {
		writeJSON(w, http.StatusAccepted, serverInfo{Status: st, Plan: planViewOf(st.Plan)})
		return
	}
	final, err := s.d.Supervisor.Wait(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, serverInfo{Status: final, Plan: planViewOf(final.Plan)})
}

// postStop 은 llama-server 를 멈춘다. 진행 중인 시작이 있으면 끊는다.
func (s *server) postStop(w http.ResponseWriter, r *http.Request) {
	if err := s.d.Supervisor.Stop(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"state": string(supervisor.StateStopped)})
}

// getLogs 는 llama-server 저널의 마지막 줄들을 돌려준다. lines 는 1-2000, 기본 200 이다.
func (s *server) getLogs(w http.ResponseWriter, r *http.Request) {
	if !rejectUnknownQuery(w, r, "lines") {
		return
	}
	lines, ok := intQuery(w, r, "lines", defaultLogLines, 1, maxLogLinesAPI)
	if !ok {
		return
	}
	text, err := s.d.Supervisor.Logs(r.Context(), lines)
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"lines": lines, "log": text})
}

// chatRequest 는 POST /v1/server/chat 의 본문이다.
type chatRequest struct {
	Messages  []chatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

// chatMessage 는 대화 한 줄이다.
type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// upstreamChat 은 llama-server 의 chat completions 응답 중 쓰는 부분이다.
// content 는 choices[0].message.content, 토큰 수와 속도는 최상위 timings 의 predicted_n 과 predicted_per_second 다.
// v0.6.0 tools/server 의 to_json_oaicompat_chat 이 timings 를 최상위에 넣는다.
type upstreamChat struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Timings *struct {
		PredictedN         int     `json:"predicted_n"`
		PredictedPerSecond float64 `json:"predicted_per_second"`
	} `json:"timings"`
}

// postChat 은 데몬이 llama-server 의 chat completions 로 대신 요청한다. 스트리밍은 없다.
// 키 파일에 키가 있으면 첫 키를 Authorization 헤더로 붙인다. 키는 응답에 나가지 않는다.
func (s *server) postChat(w http.ResponseWriter, r *http.Request) {
	var body chatRequest
	if !decodeBody(w, r, &body) {
		return
	}
	if len(body.Messages) == 0 || len(body.Messages) > maxChatMessages {
		writeError(w, http.StatusBadRequest, "invalid_messages", "messages must hold 1-%d entries", maxChatMessages)
		return
	}
	total := 0
	for _, m := range body.Messages {
		switch m.Role {
		case "system", "user", "assistant":
		default:
			writeError(w, http.StatusBadRequest, "invalid_messages", "role must be system, user, or assistant")
			return
		}
		if len(m.Content) > maxChatContent {
			writeError(w, http.StatusBadRequest, "invalid_messages", "each message content is limited to %d bytes", maxChatContent)
			return
		}
		total += len(m.Content)
	}
	if total > maxChatTotalBytes {
		writeError(w, http.StatusBadRequest, "invalid_messages", "messages are limited to %d bytes in total", maxChatTotalBytes)
		return
	}
	if body.MaxTokens == 0 {
		body.MaxTokens = defaultMaxTokens
	}
	if body.MaxTokens < 1 || body.MaxTokens > maxMaxTokens {
		writeError(w, http.StatusBadRequest, "invalid_messages", "max_tokens must be 1-%d", maxMaxTokens)
		return
	}

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

	payload, err := json.Marshal(map[string]any{
		"messages":   body.Messages,
		"max_tokens": body.MaxTokens,
		"stream":     false,
	})
	if err != nil {
		writeErr(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), chatTimeout)
	defer cancel()
	backendPort := st.BackendPort
	if backendPort == 0 {
		backendPort = st.Port
	}
	up, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:"+strconv.Itoa(backendPort)+"/v1/chat/completions", strings.NewReader(string(payload)))
	if err != nil {
		writeErr(w, err)
		return
	}
	up.Header.Set("Content-Type", "application/json")
	if key != "" {
		up.Header.Set("Authorization", "Bearer "+key)
	}
	resp, err := s.d.HTTP.Do(up)
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_unreachable", "llama-server did not answer: %v", errorKind(err))
		return
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxUpstreamBytes+1))
	if err != nil {
		writeError(w, http.StatusBadGateway, "upstream_unreachable", "reading the llama-server answer failed: %v", errorKind(err))
		return
	}
	if len(data) > maxUpstreamBytes {
		writeError(w, http.StatusBadGateway, "upstream_too_large", "llama-server answer is larger than %d bytes", maxUpstreamBytes)
		return
	}
	if resp.StatusCode != http.StatusOK {
		writeError(w, http.StatusBadGateway, "upstream_error", "llama-server returned HTTP %d", resp.StatusCode)
		return
	}
	var parsed upstreamChat
	if err := json.Unmarshal(data, &parsed); err != nil || len(parsed.Choices) == 0 {
		writeError(w, http.StatusBadGateway, "upstream_bad_response", "llama-server answer has no choices")
		return
	}
	out := map[string]any{"content": parsed.Choices[0].Message.Content, "tokens_predicted": 0, "tokens_per_second": 0.0}
	if parsed.Timings != nil {
		out["tokens_predicted"] = parsed.Timings.PredictedN
		out["tokens_per_second"] = parsed.Timings.PredictedPerSecond
	}
	writeJSON(w, http.StatusOK, out)
}

// errorKind 는 전송 오류를 짧은 설명으로 바꾼다. 주소와 헤더는 넣지 않는다.
func errorKind(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "timed out"
	}
	return "transport error"
}

// firstKey 는 키 파일의 첫 키를 돌려준다. 키 파일이 없으면 빈 문자열이다.
func (s *server) firstKey() (string, error) {
	if s.d.Keys == nil {
		return "", nil
	}
	data, err := os.ReadFile(s.d.Keys.KeyFile)
	if errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", errors.New("key file cannot be read")
	}
	for _, line := range strings.Split(string(data), "\n") {
		t := strings.TrimSpace(line)
		if t != "" && !strings.HasPrefix(t, "#") {
			return t, nil
		}
	}
	return "", nil
}
