package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"

	"github.com/nxodh/diparnuxos/daemon/internal/network"
	"github.com/nxodh/diparnuxos/daemon/internal/power"
	"github.com/nxodh/diparnuxos/daemon/internal/updatectl"
)

// Status 는 GET /v1/status 를 부른다. 응답과 원본 본문을 돌려준다.
func (c *Client) Status(ctx context.Context) (Status, []byte, error) {
	return call[Status](ctx, c, http.MethodGet, "/v1/status", nil, defaultLimit)
}

// GPUCheck 는 POST /v1/gpu/check 를 불러 GPU 진단을 다시 돌린다. 진단 스크립트 실행 시간만큼 걸릴 수 있다.
func (c *Client) GPUCheck(ctx context.Context) (GPUCheckResult, []byte, error) {
	return call[GPUCheckResult](ctx, c, http.MethodPost, "/v1/gpu/check", nil, defaultLimit)
}

// Models 는 GET /v1/models 를 부른다.
func (c *Client) Models(ctx context.Context) (ModelList, []byte, error) {
	return call[ModelList](ctx, c, http.MethodGet, "/v1/models", nil, defaultLimit)
}

// DeleteModel 은 DELETE /v1/models/{id} 를 부른다.
func (c *Client) DeleteModel(ctx context.Context, id string) (ModelDeleted, []byte, error) {
	return call[ModelDeleted](ctx, c, http.MethodDelete, "/v1/models/"+escapeID(id), nil, defaultLimit)
}

// SetDefaultModel 은 PUT /v1/models/default 를 부른다.
func (c *Client) SetDefaultModel(ctx context.Context, id string) (DefaultModel, []byte, error) {
	return call[DefaultModel](ctx, c, http.MethodPut, "/v1/models/default", map[string]string{"id": id}, defaultLimit)
}

// Search 는 GET /v1/models/search 를 부른다. limit 은 1-100 이다.
func (c *Client) Search(ctx context.Context, query string, limit int) (SearchResults, []byte, error) {
	q := url.Values{}
	q.Set("q", query)
	q.Set("limit", strconv.Itoa(limit))
	return call[SearchResults](ctx, c, http.MethodGet, withQuery("/v1/models/search", q), nil, defaultLimit)
}

// RemoteFiles 는 GET /v1/models/remote 를 부른다. rev 가 비어 있으면 데몬이 main 을 쓴다.
func (c *Client) RemoteFiles(ctx context.Context, repo, rev string) (Remote, []byte, error) {
	q := url.Values{}
	q.Set("repo", repo)
	if rev != "" {
		q.Set("revision", rev)
	}
	return call[Remote](ctx, c, http.MethodGet, withQuery("/v1/models/remote", q), nil, defaultLimit)
}

// Pull 은 POST /v1/models/pull 로 내려받기 작업을 만든다. 응답은 작업 상태다.
func (c *Client) Pull(ctx context.Context, req PullRequest) (Job, []byte, error) {
	return call[Job](ctx, c, http.MethodPost, "/v1/models/pull", req, defaultLimit)
}

// Job 은 GET /v1/jobs/{id} 를 부른다.
func (c *Client) Job(ctx context.Context, id string) (Job, []byte, error) {
	return call[Job](ctx, c, http.MethodGet, "/v1/jobs/"+escapeID(id), nil, defaultLimit)
}

// CancelJob 은 DELETE /v1/jobs/{id} 로 작업을 취소한다.
func (c *Client) CancelJob(ctx context.Context, id string) (JobCanceled, []byte, error) {
	return call[JobCanceled](ctx, c, http.MethodDelete, "/v1/jobs/"+escapeID(id), nil, defaultLimit)
}

// Plan 은 GET /v1/server/plan 으로 계획만 계산한다. model 이 비어 있으면 데몬이 기본 모델을 쓴다.
func (c *Client) Plan(ctx context.Context, model string, ov Overrides) (PlanResult, []byte, error) {
	q := url.Values{}
	if model != "" {
		q.Set("model", model)
	}
	if ov.NGL != nil {
		q.Set("ngl", *ov.NGL)
	}
	if ov.Ctx != nil {
		q.Set("ctx", strconv.Itoa(*ov.Ctx))
	}
	if ov.CacheType != nil {
		q.Set("cache_type", *ov.CacheType)
	}
	if ov.Batch != nil {
		q.Set("batch", strconv.Itoa(*ov.Batch))
	}
	if ov.LoadMode != nil {
		q.Set("load_mode", *ov.LoadMode)
	}
	return call[PlanResult](ctx, c, http.MethodGet, withQuery("/v1/server/plan", q), nil, defaultLimit)
}

// Start 는 POST /v1/server/start 를 부른다. req.Wait 가 false 면 응답은 starting 상태다.
func (c *Client) Start(ctx context.Context, req StartRequest) (Server, []byte, error) {
	return call[Server](ctx, c, http.MethodPost, "/v1/server/start", req, defaultLimit)
}

// Stop 은 POST /v1/server/stop 을 부른다.
func (c *Client) Stop(ctx context.Context) (StateResult, []byte, error) {
	return call[StateResult](ctx, c, http.MethodPost, "/v1/server/stop", nil, defaultLimit)
}

// Logs 는 GET /v1/server/logs 로 마지막 n 줄을 받는다. n 은 1-2000 이다.
func (c *Client) Logs(ctx context.Context, n int) (ServerLogs, []byte, error) {
	q := url.Values{}
	q.Set("lines", itoa(n))
	return call[ServerLogs](ctx, c, http.MethodGet, withQuery("/v1/server/logs", q), nil, defaultLimit)
}

// Chat 은 POST /v1/server/chat 으로 대화 한 번을 보낸다. 스트리밍은 없다.
func (c *Client) Chat(ctx context.Context, req ChatRequest) (ChatReply, []byte, error) {
	return call[ChatReply](ctx, c, http.MethodPost, "/v1/server/chat", req, defaultLimit)
}

// Config 는 GET /v1/config 를 부른다. hf_token 은 가려져 있다.
func (c *Client) Config(ctx context.Context) (Config, []byte, error) {
	return call[Config](ctx, c, http.MethodGet, "/v1/config", nil, defaultLimit)
}

// SetConfig 는 PUT /v1/config 로 설정을 부분 갱신한다.
func (c *Client) SetConfig(ctx context.Context, patch ConfigPatch) (ConfigUpdate, []byte, error) {
	return call[ConfigUpdate](ctx, c, http.MethodPut, "/v1/config", patch, defaultLimit)
}

// APIKeys 는 GET /v1/apikeys 를 부른다.
func (c *Client) APIKeys(ctx context.Context) (APIKeyList, []byte, error) {
	return call[APIKeyList](ctx, c, http.MethodGet, "/v1/apikeys", nil, defaultLimit)
}

// CreateAPIKey 는 POST /v1/apikeys 로 키를 만든다. 평문 키는 응답에서만 나온다.
func (c *Client) CreateAPIKey(ctx context.Context, label string) (APIKeyCreated, []byte, error) {
	return call[APIKeyCreated](ctx, c, http.MethodPost, "/v1/apikeys", map[string]string{"label": label}, defaultLimit)
}

// DeleteAPIKey 는 DELETE /v1/apikeys/{id} 로 키를 지운다.
func (c *Client) DeleteAPIKey(ctx context.Context, id string) (APIKeyDeleted, []byte, error) {
	return call[APIKeyDeleted](ctx, c, http.MethodDelete, "/v1/apikeys/"+escapeID(id), nil, defaultLimit)
}

// Mode 는 GET /v1/mode 를 부른다.
func (c *Client) Mode(ctx context.Context) (ModeResult, []byte, error) {
	return call[ModeResult](ctx, c, http.MethodGet, "/v1/mode", nil, defaultLimit)
}

// SetMode 는 POST /v1/mode 로 모드를 바꾼다. mode 는 "server" 나 "desktop" 이다.
func (c *Client) SetMode(ctx context.Context, mode string) (ModeResult, []byte, error) {
	return call[ModeResult](ctx, c, http.MethodPost, "/v1/mode", map[string]string{"mode": mode}, defaultLimit)
}

// Network 는 GET /v1/network 로 인터페이스와 무선 상태를 읽는다.
func (c *Client) Network(ctx context.Context) (network.Status, []byte, error) {
	return call[network.Status](ctx, c, http.MethodGet, "/v1/network", nil, defaultLimit)
}

// WifiNetworksReply 는 GET /v1/network/wifi/networks 의 응답이다.
type WifiNetworksReply struct {
	Networks []network.WifiNetwork `json:"networks"`
}

// WifiNetworks 는 보이는 무선 네트워크를 읽는다.
func (c *Client) WifiNetworks(ctx context.Context) (WifiNetworksReply, []byte, error) {
	return call[WifiNetworksReply](ctx, c, http.MethodGet, "/v1/network/wifi/networks", nil, defaultLimit)
}

// WifiScan 은 무선 검색을 시작한다.
func (c *Client) WifiScan(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/network/wifi/scan", nil, defaultLimit)
}

// WifiConnect 는 SSID 에 연결한다. 비밀번호는 요청 본문에만 실린다.
func (c *Client) WifiConnect(ctx context.Context, ssid, passphrase string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/network/wifi/connect", map[string]string{"ssid": ssid, "passphrase": passphrase}, defaultLimit)
}

// WifiDisconnect 는 무선 연결을 끊는다.
func (c *Client) WifiDisconnect(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/network/wifi/disconnect", nil, defaultLimit)
}

// WifiForget 은 저장된 네트워크를 지운다.
func (c *Client) WifiForget(ctx context.Context, ssid string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/network/wifi/forget", map[string]string{"ssid": ssid}, defaultLimit)
}

// WifiPower 는 무선 장치를 켜거나 끈다.
func (c *Client) WifiPower(ctx context.Context, on bool) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/v1/network/wifi/power", map[string]bool{"on": on}, defaultLimit)
}

// UpdateInfo 는 GET /v1/update 로 갱신 상태를 읽는다.
func (c *Client) UpdateInfo(ctx context.Context) (updatectl.Info, []byte, error) {
	return call[updatectl.Info](ctx, c, http.MethodGet, "/v1/update", nil, defaultLimit)
}

// UpdateRequest 는 POST /v1/update 의 본문이다.
type UpdateRequest struct {
	// Action 은 check, apply, rollback 이다.
	Action string `json:"action"`
	Reboot bool   `json:"reboot,omitempty"`
}

// UpdateStarted 는 POST /v1/update 의 응답이다.
type UpdateStarted struct {
	Started bool   `json:"started"`
	Action  string `json:"action"`
	Reboot  bool   `json:"reboot"`
}

// UpdateStart 는 POST /v1/update 로 갱신 동작을 시작한다. 진행은 UpdateInfo 로 본다.
func (c *Client) UpdateStart(ctx context.Context, req UpdateRequest) (UpdateStarted, []byte, error) {
	return call[UpdateStarted](ctx, c, http.MethodPost, "/v1/update", req, defaultLimit)
}

// Report 는 GET /v1/report 로 진단 리포트 텍스트를 받는다. 상한은 8MiB 다.
func (c *Client) Report(ctx context.Context) (string, error) {
	raw, err := c.do(ctx, http.MethodGet, "/v1/report", nil, reportLimit)
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// Users 는 GET /v1/users 로 Diparnux 계정 목록을 받는다.
func (c *Client) Users(ctx context.Context) (UserList, []byte, error) {
	return call[UserList](ctx, c, http.MethodGet, "/v1/users", nil, defaultLimit)
}

// CreateUser 는 POST /v1/users 로 계정을 만든다. 비밀번호는 요청 본문에만 실린다.
func (c *Client) CreateUser(ctx context.Context, req UserCreateRequest) (User, []byte, error) {
	return call[User](ctx, c, http.MethodPost, "/v1/users", req, defaultLimit)
}

// DeleteUser 는 DELETE /v1/users/{name} 로 계정을 지운다. 홈 디렉터리는 남는다.
func (c *Client) DeleteUser(ctx context.Context, name string) (UserDeleted, []byte, error) {
	return call[UserDeleted](ctx, c, http.MethodDelete, "/v1/users/"+escapeID(name), nil, defaultLimit)
}

// SSH 는 GET /v1/system/ssh 로 SSH 서버 상태를 받는다.
func (c *Client) SSH(ctx context.Context) (SSH, []byte, error) {
	return call[SSH](ctx, c, http.MethodGet, "/v1/system/ssh", nil, defaultLimit)
}

// SetSSH 는 PUT /v1/system/ssh 로 SSH 서버를 켜거나 끈다.
func (c *Client) SetSSH(ctx context.Context, enabled bool) (SSH, []byte, error) {
	return call[SSH](ctx, c, http.MethodPut, "/v1/system/ssh", map[string]bool{"enabled": enabled}, defaultLimit)
}

// Power 는 GET /v1/system/power 로 상시 가동 상태를 읽는다.
func (c *Client) Power(ctx context.Context) (power.Status, []byte, error) {
	return call[power.Status](ctx, c, http.MethodGet, "/v1/system/power", nil, defaultLimit)
}

// SetPower 는 PUT /v1/system/power 로 상시 가동을 켜거나 끈다.
func (c *Client) SetPower(ctx context.Context, alwaysOn bool) (power.Status, []byte, error) {
	return call[power.Status](ctx, c, http.MethodPut, "/v1/system/power", map[string]bool{"always_on": alwaysOn}, defaultLimit)
}
