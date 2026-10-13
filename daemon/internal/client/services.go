package client

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Service 는 서비스 하나의 상태다.
type Service struct {
	Unit        string   `json:"unit"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Installed   bool     `json:"installed"`
	Active      string   `json:"active"`
	Sub         string   `json:"sub"`
	Enabled     string   `json:"enabled"`
	PID         int      `json:"pid"`
	MemoryMiB   int64    `json:"memory_mib"`
	Actions     []string `json:"actions"`
}

// Services 는 GET /v1/services 를 부른다.
func (c *Client) Services(ctx context.Context) ([]Service, []byte, error) {
	type reply struct {
		Services []Service `json:"services"`
	}
	r, raw, err := call[reply](ctx, c, http.MethodGet, "/v1/services", nil, defaultLimit)
	return r.Services, raw, err
}

// ServiceAction 은 POST /v1/services/{unit}/{action} 을 부른다.
func (c *Client) ServiceAction(ctx context.Context, unit, action string) ([]byte, error) {
	return c.do(ctx, http.MethodPost, "/v1/services/"+url.PathEscape(unit)+"/"+url.PathEscape(action), nil, defaultLimit)
}

// ServiceLogs 는 GET /v1/services/{unit}/logs 를 부른다.
func (c *Client) ServiceLogs(ctx context.Context, unit string, lines int) (string, []byte, error) {
	type reply struct {
		Log string `json:"log"`
	}
	r, raw, err := call[reply](ctx, c, http.MethodGet, "/v1/services/"+url.PathEscape(unit)+"/logs?lines="+strconv.Itoa(lines), nil, defaultLimit)
	return r.Log, raw, err
}

// Resources 는 GET /v1/resources 의 응답이다.
type Resources struct {
	System struct {
		UptimeSeconds int64 `json:"uptime_seconds"`
		CPU           struct {
			Cores       int     `json:"cores"`
			UsedPercent float64 `json:"used_percent"`
			Load1       float64 `json:"load1"`
			Load5       float64 `json:"load5"`
			Load15      float64 `json:"load15"`
		} `json:"cpu"`
		Memory struct {
			TotalMiB     int64 `json:"total_mib"`
			AvailableMiB int64 `json:"available_mib"`
			UsedMiB      int64 `json:"used_mib"`
			SwapTotalMiB int64 `json:"swap_total_mib"`
			SwapUsedMiB  int64 `json:"swap_used_mib"`
		} `json:"memory"`
		Disks []struct {
			Mount    string `json:"mount"`
			FSType   string `json:"fs_type"`
			TotalMiB int64  `json:"total_mib"`
			UsedMiB  int64  `json:"used_mib"`
			Used     int    `json:"used_percent"`
		} `json:"disks"`
		Network []struct {
			Name     string  `json:"name"`
			RxPerSec float64 `json:"rx_bytes_per_second"`
			TxPerSec float64 `json:"tx_bytes_per_second"`
			RxTotal  uint64  `json:"rx_bytes_total"`
			TxTotal  uint64  `json:"tx_bytes_total"`
		} `json:"network"`
		Temperatures []struct {
			Name    string  `json:"name"`
			Celsius float64 `json:"celsius"`
		} `json:"temperatures"`
		Top []struct {
			PID    int    `json:"pid"`
			Name   string `json:"name"`
			RSSMiB int64  `json:"rss_mib"`
		} `json:"top_processes"`
	} `json:"system"`
	GPUs         []Device `json:"gpus"`
	GPUAvailable bool     `json:"gpu_available"`
	GPUReason    string   `json:"gpu_reason"`
}

// Resources 는 GET /v1/resources 를 부른다.
func (c *Client) Resources(ctx context.Context) (Resources, []byte, error) {
	return call[Resources](ctx, c, http.MethodGet, "/v1/resources", nil, defaultLimit)
}
