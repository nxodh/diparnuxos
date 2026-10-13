package runnertest

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// TestFakeMatchesFirstRuleByPrefix 는 이름과 인자 접두사로 규칙을 고르고, 처음 맞는 규칙을 쓰는지 확인한다.
func TestFakeMatchesFirstRuleByPrefix(t *testing.T) {
	var f Fake
	f.On("systemctl", []string{"is-active"}, platform.Result{Stdout: []byte("active\n")}, nil)
	f.On("systemctl", nil, platform.Result{ExitCode: 1}, nil)

	res, err := f.Run(context.Background(), platform.Command{Name: "systemctl", Args: []string{"is-active", "x.service"}})
	if err != nil || string(res.Stdout) != "active\n" {
		t.Fatalf("first rule: %q, %v", res.Stdout, err)
	}
	res, err = f.Run(context.Background(), platform.Command{Name: "systemctl", Args: []string{"start", "x.service"}})
	if err != nil || res.ExitCode != 1 {
		t.Fatalf("fallback rule: %+v, %v", res, err)
	}
}

// TestFakeReturnsRuleError 는 규칙에 적은 오류를 그대로 돌려주는지 확인한다.
func TestFakeReturnsRuleError(t *testing.T) {
	var f Fake
	boom := errors.New("boom")
	f.On("nft", nil, platform.Result{}, boom)
	if _, err := f.Run(context.Background(), platform.Command{Name: "nft"}); !errors.Is(err, boom) {
		t.Fatalf("error = %v, want boom", err)
	}
}

// TestFakeNoRuleIsAnError 는 맞는 규칙이 없으면 ErrNoRule 을 돌려주는지 확인한다.
func TestFakeNoRuleIsAnError(t *testing.T) {
	var f Fake
	f.On("nft", []string{"-c"}, platform.Result{}, nil)
	_, err := f.Run(context.Background(), platform.Command{Name: "nft", Args: []string{"-f", "x"}})
	if !errors.Is(err, ErrNoRule) {
		t.Fatalf("error = %v, want ErrNoRule", err)
	}
}

// TestFakeRecordsCallsAsCopies 는 호출 기록이 순서대로 남고, 돌려준 복사본을 고쳐도 기록이 바뀌지 않는지 확인한다.
func TestFakeRecordsCallsAsCopies(t *testing.T) {
	var f Fake
	f.On("a", nil, platform.Result{}, nil)
	args := []string{"x", "y"}
	if _, err := f.Run(context.Background(), platform.Command{Name: "a", Args: args}); err != nil {
		t.Fatal(err)
	}
	args[0] = "changed"
	calls := f.Calls()
	if len(calls) != 1 || !reflect.DeepEqual(calls[0].Args, []string{"x", "y"}) {
		t.Fatalf("calls = %+v", calls)
	}
	calls[0].Args[0] = "mutated"
	if got := f.Calls()[0].Args[0]; got != "x" {
		t.Fatalf("recorded args changed through a returned copy: %q", got)
	}
}

// TestFakeHonorsCanceledContext 는 ctx 가 끝났으면 기록하지 않고 그 오류를 돌려주는지 확인한다.
func TestFakeHonorsCanceledContext(t *testing.T) {
	var f Fake
	f.On("a", nil, platform.Result{}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.Run(ctx, platform.Command{Name: "a"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("error = %v, want context.Canceled", err)
	}
	if n := len(f.Calls()); n != 0 {
		t.Fatalf("calls = %d after a canceled context, want 0", n)
	}
}
