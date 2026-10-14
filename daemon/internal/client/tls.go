package client

import (
	"context"
	"net/http"
)

// TLS 는 GET/PUT /v1/tls 의 응답이다. 개인 키는 없다.
type TLS struct {
	Enabled      bool     `json:"enabled"`
	Active       bool     `json:"active"`
	Port         int      `json:"port"`
	LANPlain     bool     `json:"lan_plain"`
	Names        []string `json:"names"`
	PlainPort    int      `json:"plain_port"`
	PlainLANOpen bool     `json:"plain_lan_open"`

	Listening     bool   `json:"listening"`
	ListenAddr    string `json:"listen_addr"`
	ListenerError string `json:"listener_error"`

	CAFingerprint     string   `json:"ca_fingerprint"`
	CANotAfter        string   `json:"ca_not_after"`
	CAExpiresSoon     bool     `json:"ca_expires_soon"`
	ServerFingerprint string   `json:"server_fingerprint"`
	ServerNotAfter    string   `json:"server_not_after"`
	DNS               []string `json:"dns"`
	IPs               []string `json:"ips"`
	PermittedDNS      []string `json:"permitted_dns"`
	Skipped           []string `json:"skipped"`
	Notes             []string `json:"notes"`
}

// TLSPatch 는 PUT /v1/tls 의 부분 갱신이다. nil 인 항목은 그대로 둔다.
type TLSPatch struct {
	Enabled  *bool     `json:"enabled,omitempty"`
	Port     *int      `json:"port,omitempty"`
	LANPlain *bool     `json:"lan_plain,omitempty"`
	Names    *[]string `json:"names,omitempty"`
}

// TLS 는 GET /v1/tls 를 부른다.
func (c *Client) TLS(ctx context.Context) (TLS, []byte, error) {
	return call[TLS](ctx, c, http.MethodGet, "/v1/tls", nil, defaultLimit)
}

// SetTLS 는 PUT /v1/tls 를 부른다.
func (c *Client) SetTLS(ctx context.Context, patch TLSPatch) (TLS, []byte, error) {
	return call[TLS](ctx, c, http.MethodPut, "/v1/tls", patch, defaultLimit)
}

// RenewTLS 는 POST /v1/tls/renew 를 부른다. ca 가 true 이면 CA 까지 다시 만든다.
func (c *Client) RenewTLS(ctx context.Context, ca bool) (TLS, []byte, error) {
	return call[TLS](ctx, c, http.MethodPost, "/v1/tls/renew", map[string]bool{"ca": ca}, defaultLimit)
}

// TLSCA 는 GET /v1/tls/ca 로 클라이언트에 배포할 CA 인증서(PEM)를 읽는다.
func (c *Client) TLSCA(ctx context.Context) ([]byte, error) {
	return c.do(ctx, http.MethodGet, "/v1/tls/ca", nil, defaultLimit)
}
