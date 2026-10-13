// Package llamastats 는 llama-server 의 /slots 와 /metrics 응답을 읽어 서버 현황으로 바꾼다.
//
// 이 패키지는 상주하지 않는다. 화면이 현황을 요청할 때마다 한 번 조회한다.
// 엔드포인트가 꺼져 있거나 서버가 응답하지 않으면 해당 항목을 쓸 수 없다고만 표시하고 오류로 취급하지 않는다.
package llamastats

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"
)

// 조회 한도다.
const (
	fetchTimeout = 3 * time.Second
	maxBody      = 1 << 20
)

// Slot 은 슬롯 하나의 상태다.
type Slot struct {
	ID    int  `json:"id"`
	Busy  bool `json:"busy"`
	NCtx  int  `json:"n_ctx"`
	Tasks int  `json:"task_id"`
}

// Slots 는 /slots 응답의 요약이다.
type Slots struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason,omitempty"`
	Total     int    `json:"total"`
	Busy      int    `json:"busy"`
	Items     []Slot `json:"items"`
}

// Metrics 는 /metrics(Prometheus 텍스트)의 요약이다. 값을 모르면 0 이다.
type Metrics struct {
	Available          bool    `json:"available"`
	Reason             string  `json:"reason,omitempty"`
	PromptTokensTotal  float64 `json:"prompt_tokens_total"`
	GeneratedTotal     float64 `json:"generated_tokens_total"`
	PromptTokensPerSec float64 `json:"prompt_tokens_per_second"`
	GenTokensPerSec    float64 `json:"generated_tokens_per_second"`
	RequestsProcessing float64 `json:"requests_processing"`
	RequestsDeferred   float64 `json:"requests_deferred"`
	KVCacheUsageRatio  float64 `json:"kv_cache_usage_ratio"`
	KVCacheTokens      float64 `json:"kv_cache_tokens"`
}

// Stats 는 서버 현황이다.
type Stats struct {
	Slots   Slots   `json:"slots"`
	Metrics Metrics `json:"metrics"`
}

// ParseSlots 는 /slots 의 JSON 배열을 읽는다. 슬롯 하나는 id, is_processing, n_ctx 를 가진다.
func ParseSlots(data []byte) (Slots, error) {
	var raw []struct {
		ID           int  `json:"id"`
		IsProcessing bool `json:"is_processing"`
		NCtx         int  `json:"n_ctx"`
		IDTask       int  `json:"id_task"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return Slots{}, fmt.Errorf("llamastats: decode slots: %w", err)
	}
	out := Slots{Available: true, Items: make([]Slot, 0, len(raw))}
	for _, s := range raw {
		out.Items = append(out.Items, Slot{ID: s.ID, Busy: s.IsProcessing, NCtx: s.NCtx, Tasks: s.IDTask})
		out.Total++
		if s.IsProcessing {
			out.Busy++
		}
	}
	return out, nil
}

// ParseMetrics 는 Prometheus 텍스트에서 llama-server 지표를 읽는다. 모르는 줄은 건너뛴다.
func ParseMetrics(text string) Metrics {
	m := Metrics{Available: true}
	sc := bufio.NewScanner(strings.NewReader(text))
	sc.Buffer(make([]byte, 0, 64<<10), maxBody)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		name, rest, ok := strings.Cut(line, " ")
		if !ok {
			continue
		}
		v, err := strconv.ParseFloat(strings.TrimSpace(rest), 64)
		if err != nil {
			continue
		}
		// 레이블이 붙은 이름은 레이블을 떼고 비교한다.
		if i := strings.IndexByte(name, '{'); i >= 0 {
			name = name[:i]
		}
		name = strings.TrimPrefix(strings.TrimPrefix(name, "llamacpp:"), "llamacpp_")
		switch name {
		case "prompt_tokens_total":
			m.PromptTokensTotal = v
		case "tokens_predicted_total":
			m.GeneratedTotal = v
		case "prompt_tokens_seconds":
			m.PromptTokensPerSec = v
		case "predicted_tokens_seconds":
			m.GenTokensPerSec = v
		case "requests_processing":
			m.RequestsProcessing = v
		case "requests_deferred":
			m.RequestsDeferred = v
		case "kv_cache_usage_ratio":
			m.KVCacheUsageRatio = v
		case "kv_cache_tokens":
			m.KVCacheTokens = v
		}
	}
	return m
}

// Fetch 는 base(예: http://127.0.0.1:18080)의 /slots 와 /metrics 를 조회한다. 오류는 돌려주지 않고 각 항목의 Reason 에 적는다.
func Fetch(ctx context.Context, hc *http.Client, base string) Stats {
	var out Stats
	if hc == nil {
		hc = http.DefaultClient
	}
	if body, status, err := get(ctx, hc, base+"/slots"); err != nil {
		out.Slots = Slots{Reason: err.Error(), Items: []Slot{}}
	} else if status != http.StatusOK {
		out.Slots = Slots{Reason: fmt.Sprintf("the slots endpoint answered HTTP %d", status), Items: []Slot{}}
	} else if s, err := ParseSlots(body); err != nil {
		out.Slots = Slots{Reason: err.Error(), Items: []Slot{}}
	} else {
		out.Slots = s
	}
	if body, status, err := get(ctx, hc, base+"/metrics"); err != nil {
		out.Metrics = Metrics{Reason: err.Error()}
	} else if status != http.StatusOK {
		out.Metrics = Metrics{Reason: fmt.Sprintf("the metrics endpoint answered HTTP %d", status)}
	} else {
		out.Metrics = ParseMetrics(string(body))
	}
	return out
}

func get(ctx context.Context, hc *http.Client, url string) ([]byte, int, error) {
	ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("the server did not answer")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return nil, resp.StatusCode, fmt.Errorf("reading the answer failed")
	}
	return data, resp.StatusCode, nil
}
