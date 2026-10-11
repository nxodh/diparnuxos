// Package client는 lightlixd 의 REST API 를 유닉스 소켓으로 호출하는 클라이언트다.
//
// 메서드마다 경로와 응답 타입이 하나씩 있다. 응답 타입은 internal/api 의 JSON 모양과 같은 태그를 쓴다.
// api 패키지는 뷰 타입을 내보내지 않으므로 여기에 같은 모양을 다시 정의한다.
// 모든 메서드는 원본 응답 본문도 돌려준다. 명령행 도구의 --json 출력이 그 본문을 쓴다.
// 시간 제한은 호출자가 context 로 정한다. 이 패키지는 기본 시간 제한을 두지 않는다.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/url"
	"strconv"
)

// 응답 본문 크기 상한이다. 리포트는 크므로 따로 둔다.
const (
	defaultLimit int64 = 4 << 20
	reportLimit  int64 = 8 << 20
)

// 연결 실패를 구분하는 오류다. errors.Is 로 확인한다.
var (
	// ErrUnavailable 은 소켓이 없거나 데몬이 연결을 받지 않을 때 돌려준다.
	ErrUnavailable = errors.New("lightlixd is not reachable")
	// ErrPermission 은 소켓에 연결할 권한이 없을 때 돌려준다.
	ErrPermission = errors.New("permission denied on the lightlixd socket")
	// ErrTooLarge 는 응답 본문이 상한을 넘을 때 돌려준다.
	ErrTooLarge = errors.New("response from lightlixd is larger than the limit")
)

// APIError 는 데몬이 {"error":{"code","message"}} 형태로 돌려준 오류다.
type APIError struct {
	// Status 는 HTTP 상태 코드다.
	Status int
	// Code 는 "not_found" 같은 짧은 오류 코드다. 형식이 맞지 않는 응답이면 "http_error" 다.
	Code string
	// Message 는 사람이 읽는 설명이다.
	Message string
}

// Error 는 "code: message (HTTP status)" 형태의 문자열을 돌려준다.
func (e *APIError) Error() string {
	return fmt.Sprintf("%s: %s (HTTP %d)", e.Code, e.Message, e.Status)
}

// Client 는 lightlixd 소켓에 연결하는 API 클라이언트다. 여러 고루틴에서 같이 써도 된다.
type Client struct {
	socket string
	hc     *http.Client
}

// New는 socketPath 의 유닉스 소켓으로 연결하는 클라이언트를 만든다. 실제 연결은 호출 때 한다.
func New(socketPath string) *Client {
	tr := &http.Transport{
		DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
			var d net.Dialer
			conn, err := d.DialContext(ctx, "unix", socketPath)
			if err != nil {
				return nil, classifyDialError(err)
			}
			return conn, nil
		},
		// 요청마다 소켓을 새로 연다. CLI 는 몇 번 부르고 끝나므로 유휴 연결을 둘 이유가 없다.
		DisableKeepAlives: true,
	}
	return &Client{socket: socketPath, hc: &http.Client{Transport: tr}}
}

// classifyDialError 는 연결 실패를 ErrPermission 이나 ErrUnavailable 로 감싼다.
// 호출자가 취소하거나 시간 제한을 넘긴 경우는 감싸지 않고 그대로 돌려준다.
func classifyDialError(err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, fs.ErrPermission) {
		return fmt.Errorf("%w: %w", ErrPermission, err)
	}
	return fmt.Errorf("%w: %w", ErrUnavailable, err)
}

// parseAPIError 는 오류 응답 본문을 APIError 로 바꾼다.
func parseAPIError(status int, body []byte) *APIError {
	var env struct {
		Error struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err == nil && env.Error.Code != "" {
		return &APIError{Status: status, Code: env.Error.Code, Message: env.Error.Message}
	}
	return &APIError{Status: status, Code: "http_error", Message: http.StatusText(status)}
}

// do 는 요청 하나를 보내고 2xx 응답 본문을 돌려준다. 3xx 이상은 APIError 다.
// body 가 nil 이 아니면 JSON 으로 보낸다. limit 을 넘는 본문은 ErrTooLarge 다.
func (c *Client) do(ctx context.Context, method, path string, body any, limit int64) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("encode request: %w", err)
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://lightlixd"+path, rd)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, parseAPIError(resp.StatusCode, data)
	}
	return data, nil
}

// call 은 do 를 부르고 응답 JSON 을 T 로 푼다. 원본 본문도 함께 돌려준다.
func call[T any](ctx context.Context, c *Client, method, path string, body any, limit int64) (T, []byte, error) {
	var out T
	raw, err := c.do(ctx, method, path, body, limit)
	if err != nil {
		return out, nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, raw, fmt.Errorf("decode response from %s: %w", path, err)
	}
	return out, raw, nil
}

// withQuery 는 경로에 쿼리를 붙인다. 값은 URL 인코딩한다.
func withQuery(path string, q url.Values) string {
	if len(q) == 0 {
		return path
	}
	return path + "?" + q.Encode()
}

// escapeID 는 경로 조각으로 쓸 ID 를 이스케이프한다.
func escapeID(id string) string {
	return url.PathEscape(id)
}

// itoa 는 정수를 쿼리 값으로 바꾼다.
func itoa(v int) string {
	return strconv.Itoa(v)
}
