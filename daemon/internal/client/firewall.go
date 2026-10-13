package client

import (
	"context"
	"net/http"
	"net/url"
)

// FirewallRule 은 사용자 방화벽 규칙이다.
type FirewallRule struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	PortEnd  int    `json:"port_end"`
	Source   string `json:"source"`
	Enabled  bool   `json:"enabled"`
}

// FirewallOpen 은 기능이 여는 포트(읽기 전용)다.
type FirewallOpen struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	PortEnd  int    `json:"port_end"`
	Source   string `json:"source"`
	Reason   string `json:"reason"`
}

// Firewall 은 GET /v1/firewall 의 응답이다.
type Firewall struct {
	Policy  string         `json:"policy"`
	Builtin []FirewallOpen `json:"builtin"`
	Rules   []FirewallRule `json:"rules"`
	MaxRule int            `json:"max_rule"`
}

// FirewallRuleRequest 는 규칙을 만들거나 바꾸는 본문이다.
type FirewallRuleRequest struct {
	Name     string `json:"name"`
	Protocol string `json:"protocol"`
	Port     int    `json:"port"`
	PortEnd  int    `json:"port_end,omitempty"`
	Source   string `json:"source,omitempty"`
	Enabled  *bool  `json:"enabled,omitempty"`
}

// Firewall 은 GET /v1/firewall 을 부른다.
func (c *Client) Firewall(ctx context.Context) (Firewall, []byte, error) {
	return call[Firewall](ctx, c, http.MethodGet, "/v1/firewall", nil, defaultLimit)
}

// AddFirewallRule 은 POST /v1/firewall/rules 를 부른다.
func (c *Client) AddFirewallRule(ctx context.Context, req FirewallRuleRequest) (FirewallRule, []byte, error) {
	return call[FirewallRule](ctx, c, http.MethodPost, "/v1/firewall/rules", req, defaultLimit)
}

// UpdateFirewallRule 은 PUT /v1/firewall/rules/{id} 를 부른다.
func (c *Client) UpdateFirewallRule(ctx context.Context, id string, req FirewallRuleRequest) (FirewallRule, []byte, error) {
	return call[FirewallRule](ctx, c, http.MethodPut, "/v1/firewall/rules/"+url.PathEscape(id), req, defaultLimit)
}

// DeleteFirewallRule 은 DELETE /v1/firewall/rules/{id} 를 부른다.
func (c *Client) DeleteFirewallRule(ctx context.Context, id string) ([]byte, error) {
	return c.do(ctx, http.MethodDelete, "/v1/firewall/rules/"+url.PathEscape(id), nil, defaultLimit)
}
