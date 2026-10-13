package client

import (
	"context"
	"net/http"
)

// WebAdmin 은 GET /v1/webadmin 의 응답이다.
type WebAdmin struct {
	Enabled         bool     `json:"enabled"`
	Running         bool     `json:"running"`
	Port            int      `json:"port"`
	Source          string   `json:"source"`
	PasswordSet     bool     `json:"password_set"`
	CertFingerprint string   `json:"cert_fingerprint"`
	URLs            []string `json:"urls"`
	Error           string   `json:"error"`
}

// WebAdmin 은 GET /v1/webadmin 을 부른다.
func (c *Client) WebAdmin(ctx context.Context) (WebAdmin, []byte, error) {
	return call[WebAdmin](ctx, c, http.MethodGet, "/v1/webadmin", nil, defaultLimit)
}

// SetWebAdmin 은 PUT /v1/webadmin 을 부른다. nil 인 항목은 그대로 둔다.
func (c *Client) SetWebAdmin(ctx context.Context, enabled *bool, port *int, source *string) (WebAdmin, []byte, error) {
	body := map[string]any{}
	if enabled != nil {
		body["enabled"] = *enabled
	}
	if port != nil {
		body["port"] = *port
	}
	if source != nil {
		body["source"] = *source
	}
	return call[WebAdmin](ctx, c, http.MethodPut, "/v1/webadmin", body, defaultLimit)
}

// SetWebAdminPassword 는 PUT /v1/webadmin/password 를 부른다.
func (c *Client) SetWebAdminPassword(ctx context.Context, password string) ([]byte, error) {
	return c.do(ctx, http.MethodPut, "/v1/webadmin/password", map[string]string{"password": password}, defaultLimit)
}
