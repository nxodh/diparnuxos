package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/nxodh/lightlixos/daemon/internal/hf"
	"github.com/nxodh/lightlixos/daemon/internal/jobs"
)

// TestPullErrors 는 작업 요청의 오류 코드가 원인 오류와 래핑 여부에 관계없이 보존되는지 확인한다.
func TestPullErrors(t *testing.T) {
	cases := []struct {
		code   string
		status int
	}{
		{jobs.CodeGated, http.StatusForbidden},
		{jobs.CodeUnauthorized, http.StatusUnauthorized},
		{jobs.CodeNotFound, http.StatusNotFound},
		{jobs.CodeRateLimited, http.StatusTooManyRequests},
		{jobs.CodeInsufficientSpace, http.StatusInsufficientStorage},
		{jobs.CodeInvalidInput, http.StatusBadRequest},
		{jobs.CodeAmbiguousFile, http.StatusBadRequest},
		{jobs.CodeNameConflict, http.StatusConflict},
		{jobs.CodeNetwork, http.StatusBadGateway},
		{jobs.CodeChecksumMismatch, http.StatusBadGateway},
		{jobs.CodeSizeMismatch, http.StatusBadGateway},
	}
	for _, tc := range cases {
		t.Run(tc.code, func(t *testing.T) {
			// 센티넬이 없는 모호한 파일·gated 오류와 fs 원인을 가진 오류도 같은 코드를 유지한다.
			for _, cause := range []error{nil, errors.New("underlying failure")} {
				w := httptest.NewRecorder()
				err := fmt.Errorf("wrapped: %w", &jobs.PullError{Code: tc.code, Message: "safe detail", Err: cause})
				writeErr(w, err)
				wantStatus(t, w, tc.status)
				if got := errCode(t, w); got != tc.code {
					t.Fatalf("code = %q, want %q", got, tc.code)
				}
				if !strings.Contains(w.Body.String(), "safe detail") {
					t.Fatalf("safe message was lost: %s", w.Body.String())
				}
			}
		})
	}
}

func TestUnauthorizedAndInternalErrors(t *testing.T) {
	w := httptest.NewRecorder()
	writeErr(w, fmt.Errorf("wrapped: %w", hf.ErrUnauthorized))
	wantStatus(t, w, http.StatusUnauthorized)
	if got := errCode(t, w); got != "unauthorized" {
		t.Fatalf("code = %q", got)
	}
	for _, code := range []string{jobs.CodeInternal, "unknown"} {
		w := httptest.NewRecorder()
		writeErr(w, &jobs.PullError{Code: code, Message: "private path detail"})
		wantStatus(t, w, http.StatusInternalServerError)
		if errCode(t, w) != "internal" || strings.Contains(w.Body.String(), "private path detail") {
			t.Fatalf("internal error leaked: %s", w.Body.String())
		}
	}
}
