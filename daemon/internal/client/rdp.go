package client

import (
	"context"
	"net/http"
)

// RemoteDesktop 은 GET /v1/remote-desktop 의 응답이다.
type RemoteDesktop struct {
	Installed bool   `json:"installed"`
	Enabled   bool   `json:"enabled"`
	Active    bool   `json:"active"`
	Port      int    `json:"port"`
	Source    string `json:"source"`
	Note      string `json:"note"`
}

// RemoteDesktop 은 GET /v1/remote-desktop 을 부른다.
func (c *Client) RemoteDesktop(ctx context.Context) (RemoteDesktop, []byte, error) {
	return call[RemoteDesktop](ctx, c, http.MethodGet, "/v1/remote-desktop", nil, defaultLimit)
}

// SetRemoteDesktop 은 PUT /v1/remote-desktop 을 부른다.
func (c *Client) SetRemoteDesktop(ctx context.Context, enabled bool, source string) (RemoteDesktop, []byte, error) {
	body := map[string]any{"enabled": enabled}
	if source != "" {
		body["source"] = source
	}
	return call[RemoteDesktop](ctx, c, http.MethodPut, "/v1/remote-desktop", body, defaultLimit)
}
