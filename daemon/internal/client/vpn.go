package client

import (
	"context"
	"net/http"
	"net/url"
)

// VPNPeer 는 VPN 피어 하나다.
type VPNPeer struct {
	Name       string   `json:"name"`
	PublicKey  string   `json:"public_key"`
	AllowedIPs []string `json:"allowed_ips"`
	Endpoint   string   `json:"endpoint"`
	Keepalive  int      `json:"keepalive"`
}

// VPN 은 GET /v1/vpn 의 응답이다. 개인 키는 없다.
type VPN struct {
	Enabled    bool      `json:"enabled"`
	Interface  string    `json:"interface"`
	Address    string    `json:"address"`
	ListenPort int       `json:"listen_port"`
	PublicKey  string    `json:"public_key"`
	Peers      []VPNPeer `json:"peers"`
}

// VPNPeerAdded 는 POST /v1/vpn/peers 의 응답이다. ClientConfig 는 서버가 키를 만들었을 때만 있다.
type VPNPeerAdded struct {
	Peer         VPNPeer `json:"peer"`
	ClientConfig string  `json:"client_config"`
	Note         string  `json:"note"`
}

// VPN 은 GET /v1/vpn 을 부른다.
func (c *Client) VPN(ctx context.Context) (VPN, []byte, error) {
	return call[VPN](ctx, c, http.MethodGet, "/v1/vpn", nil, defaultLimit)
}

// SetVPN 은 PUT /v1/vpn 을 부른다. nil 인 항목은 그대로 둔다.
func (c *Client) SetVPN(ctx context.Context, enabled *bool, address *string, port *int) (VPN, []byte, error) {
	body := map[string]any{}
	if enabled != nil {
		body["enabled"] = *enabled
	}
	if address != nil {
		body["address"] = *address
	}
	if port != nil {
		body["listen_port"] = *port
	}
	return call[VPN](ctx, c, http.MethodPut, "/v1/vpn", body, defaultLimit)
}

// AddVPNPeer 는 POST /v1/vpn/peers 를 부른다.
func (c *Client) AddVPNPeer(ctx context.Context, name, publicKey, endpoint string) (VPNPeerAdded, []byte, error) {
	body := map[string]string{"name": name}
	if publicKey != "" {
		body["public_key"] = publicKey
	}
	if endpoint != "" {
		body["endpoint"] = endpoint
	}
	return call[VPNPeerAdded](ctx, c, http.MethodPost, "/v1/vpn/peers", body, defaultLimit)
}

// RemoveVPNPeer 는 DELETE /v1/vpn/peers/{id} 를 부른다.
func (c *Client) RemoveVPNPeer(ctx context.Context, id string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/v1/vpn/peers/"+url.PathEscape(id), nil, defaultLimit)
}
