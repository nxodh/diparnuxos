package client

import (
	"context"
	"net/http"
	"net/url"
)

// WiredConfig 는 유선 인터페이스 하나의 IP 설정이다.
type WiredConfig struct {
	Interface string   `json:"interface"`
	Mode      string   `json:"mode"`
	Addresses []string `json:"addresses"`
	Gateway   string   `json:"gateway"`
	DNS       []string `json:"dns"`
	Domains   []string `json:"domains"`
	Managed   bool     `json:"managed"`
}

// PendingChange 는 확인을 기다리는 네트워크 변경이다.
type PendingChange struct {
	Interface string `json:"interface"`
	Deadline  string `json:"deadline"`
}

// NetworkConfig 는 GET /v1/network/config 의 응답이다.
type NetworkConfig struct {
	Hostname   string         `json:"hostname"`
	WakeOnLAN  bool           `json:"wake_on_lan"`
	Pending    *PendingChange `json:"pending"`
	Interfaces []WiredConfig  `json:"interfaces"`
}

// WiredUpdate 는 PUT /v1/network/interfaces/{name} 의 응답이다.
type WiredUpdate struct {
	Config         WiredConfig    `json:"config"`
	Pending        *PendingChange `json:"pending"`
	ConfirmSeconds int            `json:"confirm_seconds"`
}

// WiredRequest 는 PUT /v1/network/interfaces/{name} 의 본문이다.
type WiredRequest struct {
	Mode           string   `json:"mode"`
	Addresses      []string `json:"addresses,omitempty"`
	Gateway        string   `json:"gateway,omitempty"`
	DNS            []string `json:"dns,omitempty"`
	Domains        []string `json:"domains,omitempty"`
	ConfirmSeconds *int     `json:"confirm_seconds,omitempty"`
}

// DiagnoseCheck 는 연결 진단 항목 하나다.
type DiagnoseCheck struct {
	Name    string `json:"name"`
	OK      bool   `json:"ok"`
	Skipped bool   `json:"skipped"`
	Detail  string `json:"detail"`
}

// NetworkConfig 는 GET /v1/network/config 를 부른다.
func (c *Client) NetworkConfig(ctx context.Context) (NetworkConfig, []byte, error) {
	return call[NetworkConfig](ctx, c, http.MethodGet, "/v1/network/config", nil, defaultLimit)
}

// SetWired 는 PUT /v1/network/interfaces/{name} 으로 IP 설정을 바꾼다.
func (c *Client) SetWired(ctx context.Context, name string, req WiredRequest) (WiredUpdate, []byte, error) {
	return call[WiredUpdate](ctx, c, http.MethodPut, "/v1/network/interfaces/"+url.PathEscape(name), req, defaultLimit)
}

// ConfirmNetwork 는 POST /v1/network/confirm 으로 대기 중인 변경을 확정한다.
func (c *Client) ConfirmNetwork(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/network/confirm", nil, defaultLimit)
}

// RevertNetwork 는 POST /v1/network/revert 로 대기 중인 변경을 되돌린다.
func (c *Client) RevertNetwork(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/network/revert", nil, defaultLimit)
}

// SetHostname 은 PUT /v1/network/hostname 을 부른다.
func (c *Client) SetHostname(ctx context.Context, name string) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/v1/network/hostname", map[string]string{"name": name}, defaultLimit)
}

// SetWakeOnLAN 은 PUT /v1/network/wol 을 부른다.
func (c *Client) SetWakeOnLAN(ctx context.Context, on bool) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/v1/network/wol", map[string]bool{"enabled": on}, defaultLimit)
}

// Diagnose 는 POST /v1/network/diagnose 를 부른다.
func (c *Client) Diagnose(ctx context.Context) ([]DiagnoseCheck, []byte, error) {
	type reply struct {
		Checks []DiagnoseCheck `json:"checks"`
	}
	r, raw, err := call[reply](ctx, c, http.MethodPost, "/v1/network/diagnose", nil, defaultLimit)
	return r.Checks, raw, err
}
