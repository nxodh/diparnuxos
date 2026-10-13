package llamastats

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestParseSlots(t *testing.T) {
	s, err := ParseSlots([]byte(`[{"id":0,"n_ctx":4096,"is_processing":true,"id_task":7},{"id":1,"n_ctx":4096,"is_processing":false,"id_task":0}]`))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Available || s.Total != 2 || s.Busy != 1 || s.Items[0].NCtx != 4096 || !s.Items[0].Busy || s.Items[1].Busy {
		t.Fatalf("slots = %+v", s)
	}
	if _, err := ParseSlots([]byte(`{"error":"x"}`)); err == nil {
		t.Fatal("a non-array answer was accepted")
	}
}

func TestParseMetrics(t *testing.T) {
	text := `# HELP llamacpp:prompt_tokens_total Number of prompt tokens processed.
# TYPE llamacpp:prompt_tokens_total counter
llamacpp:prompt_tokens_total 1200
llamacpp:tokens_predicted_total 3400
llamacpp:prompt_tokens_seconds 850.5
llamacpp:predicted_tokens_seconds 42.25
llamacpp:requests_processing 2
llamacpp:requests_deferred 1
llamacpp:kv_cache_usage_ratio 0.25
llamacpp:kv_cache_tokens{slot="0"} 512
unrelated_metric 99
broken line
`
	m := ParseMetrics(text)
	if !m.Available || m.PromptTokensTotal != 1200 || m.GeneratedTotal != 3400 || m.PromptTokensPerSec != 850.5 ||
		m.GenTokensPerSec != 42.25 || m.RequestsProcessing != 2 || m.RequestsDeferred != 1 ||
		m.KVCacheUsageRatio != 0.25 || m.KVCacheTokens != 512 {
		t.Fatalf("metrics = %+v", m)
	}
}

func TestFetchDegradesPerEndpoint(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/slots", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`[{"id":0,"n_ctx":2048,"is_processing":false}]`))
	})
	mux.HandleFunc("/metrics", func(w http.ResponseWriter, r *http.Request) { http.Error(w, "disabled", http.StatusNotImplemented) })
	srv := httptest.NewServer(mux)
	defer srv.Close()

	st := Fetch(context.Background(), srv.Client(), srv.URL)
	if !st.Slots.Available || st.Slots.Total != 1 {
		t.Fatalf("slots = %+v", st.Slots)
	}
	if st.Metrics.Available || st.Metrics.Reason == "" {
		t.Fatalf("metrics = %+v", st.Metrics)
	}

	srv.Close()
	down := Fetch(context.Background(), http.DefaultClient, srv.URL)
	if down.Slots.Available || down.Metrics.Available || down.Slots.Reason == "" {
		t.Fatalf("a closed server must report both as unavailable: %+v", down)
	}
}
