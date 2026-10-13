package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// SSHKey 는 등록된 공개 키다.
type SSHKey struct {
	Type        string `json:"type"`
	Fingerprint string `json:"fingerprint"`
	Comment     string `json:"comment"`
}

// SSHAdvanced 는 GET /v1/ssh/advanced 의 응답이다.
type SSHAdvanced struct {
	Enabled                 bool     `json:"enabled"`
	Port                    int      `json:"port"`
	PasswordLoginConfigured *bool    `json:"password_login_configured"`
	KeyCount                int      `json:"key_count"`
	HostKeys                []SSHKey `json:"host_keys"`
}

// SSHLoginEvent 는 접속 결과 한 줄이다.
type SSHLoginEvent struct {
	Time   string `json:"time"`
	Result string `json:"result"`
	User   string `json:"user"`
	Method string `json:"method"`
	From   string `json:"from"`
}

// SSHAdvanced 는 GET /v1/ssh/advanced 를 부른다.
func (c *Client) SSHAdvanced(ctx context.Context) (SSHAdvanced, []byte, error) {
	return call[SSHAdvanced](ctx, c, http.MethodGet, "/v1/ssh/advanced", nil, defaultLimit)
}

// SetSSHAdvanced 는 PUT /v1/ssh/advanced 로 포트나 비밀번호 로그인을 바꾼다. nil 인 항목은 그대로 둔다.
func (c *Client) SetSSHAdvanced(ctx context.Context, port *int, passwordLogin *bool) (SSHAdvanced, []byte, error) {
	body := map[string]any{}
	if port != nil {
		body["port"] = *port
	}
	if passwordLogin != nil {
		body["password_login"] = *passwordLogin
	}
	return call[SSHAdvanced](ctx, c, http.MethodPut, "/v1/ssh/advanced", body, defaultLimit)
}

// SSHKeys 는 GET /v1/ssh/keys 를 부른다.
func (c *Client) SSHKeys(ctx context.Context, user string) ([]SSHKey, []byte, error) {
	type reply struct {
		Keys []SSHKey `json:"keys"`
	}
	r, raw, err := call[reply](ctx, c, http.MethodGet, "/v1/ssh/keys?user="+url.QueryEscape(user), nil, defaultLimit)
	return r.Keys, raw, err
}

// AddSSHKey 는 POST /v1/ssh/keys 를 부른다.
func (c *Client) AddSSHKey(ctx context.Context, user, key string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/ssh/keys", map[string]string{"user": user, "key": key}, defaultLimit)
}

// RemoveSSHKey 는 DELETE /v1/ssh/keys 를 부른다.
func (c *Client) RemoveSSHKey(ctx context.Context, user, fingerprint string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/v1/ssh/keys", map[string]string{"user": user, "fingerprint": fingerprint}, defaultLimit)
}

// SSHLog 는 GET /v1/ssh/log 를 부른다.
func (c *Client) SSHLog(ctx context.Context, limit int) ([]SSHLoginEvent, []byte, error) {
	type reply struct {
		Events []SSHLoginEvent `json:"events"`
	}
	r, raw, err := call[reply](ctx, c, http.MethodGet, "/v1/ssh/log?limit="+strconv.Itoa(limit), nil, defaultLimit)
	return r.Events, raw, err
}
