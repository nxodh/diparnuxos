package jobs

import (
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/hf"
)

// TestClassifyMapsHubErrors 는 hf 의 오류가 작업 오류 코드로 옮겨지는지 확인한다.
func TestClassifyMapsHubErrors(t *testing.T) {
	netErr := &url.Error{Op: "Get", URL: "https://huggingface.co/x", Err: errors.New("dial failed")}
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"gated", hf.ErrGated, CodeGated},
		{"not found", fmt.Errorf("hf: x: %w", hf.ErrNotFound), CodeNotFound},
		{"unauthorized", hf.ErrUnauthorized, CodeUnauthorized},
		{"rate limited", &hf.RateLimitError{ResetSeconds: 120}, CodeRateLimited},
		{"insufficient space", hf.ErrInsufficientSpace, CodeInsufficientSpace},
		{"checksum", hf.ErrChecksumMismatch, CodeChecksumMismatch},
		{"size", hf.ErrSizeMismatch, CodeSizeMismatch},
		{"invalid input", hf.ErrInvalidInput, CodeInvalidInput},
		{"server error", &hf.StatusError{StatusCode: 503}, CodeNetwork},
		{"client status", &hf.StatusError{StatusCode: 400}, CodeInternal},
		{"transport error", fmt.Errorf("hf: download x: request failed: %w", netErr), CodeNetwork},
		{"truncated body", io.ErrUnexpectedEOF, CodeNetwork},
		{"local error", errors.New("disk on fire"), CodeInternal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, msg := classify(tt.err, "")
			if code != tt.want {
				t.Fatalf("code = %q, want %q (message %q)", code, tt.want, msg)
			}
			if msg == "" || strings.Contains(msg, "\n") {
				t.Fatalf("message is not one line: %q", msg)
			}
		})
	}
}

// TestClassifyRateLimitMentionsReset 는 속도 제한 설명에 다시 시도할 시간이 들어가는지 확인한다.
func TestClassifyRateLimitMentionsReset(t *testing.T) {
	_, msg := classify(fmt.Errorf("wrapped: %w", &hf.RateLimitError{ResetSeconds: 120}), "")
	if !strings.Contains(msg, "120 seconds") {
		t.Fatalf("message = %q, want the reset time", msg)
	}
}

// TestClassifyNeverShowsToken 는 오류 원문에 토큰이 있어도 설명에서 가려지는지 확인한다.
func TestClassifyNeverShowsToken(t *testing.T) {
	err := fmt.Errorf("hf: request with %s failed\nsecond line", testToken)
	_, msg := classify(err, testToken)
	if strings.Contains(msg, testToken) {
		t.Fatalf("message shows the token: %q", msg)
	}
	if !strings.Contains(msg, "<redacted>") || strings.Contains(msg, "\n") {
		t.Fatalf("message = %q, want a redacted single line", msg)
	}
}

// TestSanitizeLimitsLength 는 긴 원문을 글자 수 기준으로 자르고, 빈 토큰은 아무것도 가리지 않는지 확인한다.
func TestSanitizeLimitsLength(t *testing.T) {
	long := strings.Repeat("가", 500)
	got := sanitize(long, "")
	if r := []rune(got); len(r) != maxDetailLen+3 || !strings.HasSuffix(got, "...") {
		t.Fatalf("sanitize length = %d runes, want %d with an ellipsis", len(r), maxDetailLen+3)
	}
	if got := sanitize("keep  this\tas   is", ""); got != "keep this as is" {
		t.Fatalf("sanitize = %q", got)
	}
}

// TestCheckLocalName 은 모델 파일 이름 규칙을 확인한다.
func TestCheckLocalName(t *testing.T) {
	ok := []string{"model.gguf", "Q4_K_M-model.gguf"}
	bad := []string{"", ".gguf", ".hidden.gguf", "model.bin", "model.GGUF", "a\\b.gguf", "a\x01.gguf", strings.Repeat("a", 260) + ".gguf"}
	for _, name := range ok {
		if err := checkLocalName(name); err != nil {
			t.Fatalf("checkLocalName(%q): %v", name, err)
		}
	}
	for _, name := range bad {
		if err := checkLocalName(name); CodeOf(err) != CodeInvalidInput {
			t.Fatalf("checkLocalName(%q) = %v, want invalid_input", name, err)
		}
	}
}

// TestCodeOfOnlyReadsPullErrors 는 CodeOf 가 PullError 가 아닌 오류에는 빈 문자열을 돌려주는지 확인한다.
func TestCodeOfOnlyReadsPullErrors(t *testing.T) {
	if got := CodeOf(ErrClosed); got != "" {
		t.Fatalf("CodeOf(ErrClosed) = %q", got)
	}
	if got := CodeOf(fmt.Errorf("wrap: %w", newPullError(CodeGated, "x", nil))); got != CodeGated {
		t.Fatalf("CodeOf(wrapped PullError) = %q", got)
	}
}
