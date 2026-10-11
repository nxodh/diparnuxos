package supervisor

import "fmt"

// Error는 코드를 가진 오류다. API 계층이 코드를 HTTP 상태와 JSON 오류로 옮긴다.
type Error struct {
	// Code 는 "no_model" 같은 짧은 식별자다.
	Code string `json:"code"`
	// Message 는 사람이 읽을 영어 설명이다. 비밀은 담지 않는다.
	Message string `json:"message"`
}

// Error 는 "code: message" 형태의 문자열을 돌려준다.
func (e *Error) Error() string {
	return e.Code + ": " + e.Message
}

// 오류 코드 목록이다. API 계층이 이 값으로 HTTP 상태를 고른다.
const (
	CodeNoModel             = "no_model"
	CodeAPIKeyRequired      = "api_key_required"
	CodeInvalidOverrides    = "invalid_overrides"
	CodeInvalidSettings     = "invalid_settings"
	CodeCUDABackend         = "cuda_backend_unavailable"
	CodeEnvFile             = "env_file_failed"
	CodeUnitStartFailed     = "unit_start_failed"
	CodeUnitCheck           = "unit_check_failed"
	CodeUnitStopFailed      = "unit_stop_failed"
	CodeServerExited        = "server_exited"
	CodeHealthTimeout       = "health_timeout"
	CodeGPUOffloadFailed    = "gpu_offload_failed"
	CodeCanceled            = "canceled"
	CodeWaitCancelled       = "wait_cancelled"
	CodeBusy                = "busy"
	CodeInvalidLines        = "invalid_lines"
	CodeLogsFailed          = "logs_failed"
	CodeInvalidMode         = "invalid_mode"
	CodeDesktopNotInstalled = "desktop_not_installed"
	CodeModeChangeFailed    = "mode_change_failed"
	CodeFirewallFailed      = "firewall_failed"
	CodeSSHNotInstalled     = "ssh_not_installed"
	CodeSSHChangeFailed     = "ssh_change_failed"
	CodeInternal            = "internal"
)

// errorf는 코드와 형식 문자열로 *Error를 만든다.
func errorf(code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}
