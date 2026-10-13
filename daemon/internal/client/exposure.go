package client

import (
	"context"
	"net/http"
)

// ExposureFinding 은 노출 점검 항목 하나다.
type ExposureFinding struct {
	Level  string `json:"level"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
}

// Exposure 는 GET /v1/exposure 의 응답이다.
type Exposure struct {
	Level    string            `json:"level"`
	Findings []ExposureFinding `json:"findings"`
}

// Exposure 는 GET /v1/exposure 를 부른다.
func (c *Client) Exposure(ctx context.Context) (Exposure, []byte, error) {
	return call[Exposure](ctx, c, http.MethodGet, "/v1/exposure", nil, defaultLimit)
}
