// Package runnertest 는 테스트에서 platform.Runner 를 대신하는 Fake 를 둔다.
//
// Fake 는 호출을 기록하고, 명령 이름과 인자 접두사가 맞는 첫 규칙의 결과나 오류를 돌려준다.
// 맞는 규칙이 없으면 ErrNoRule 을 돌려준다. 다른 패키지의 테스트도 이 패키지를 쓸 수 있다.
package runnertest

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// ErrNoRule 은 어떤 규칙도 맞지 않는 명령이 실행되었을 때 돌려주는 오류다. errors.Is 로 확인한다.
var ErrNoRule = errors.New("runnertest: no rule matches the command")

// rule 은 명령 이름과 인자 접두사가 맞을 때 돌려줄 결과다.
type rule struct {
	name      string
	argPrefix []string
	result    platform.Result
	err       error
}

// Fake 는 platform.Runner 의 테스트 대역이다. 값으로 만들어 바로 쓸 수 있고, 여러 고루틴에서 써도 안전하다.
type Fake struct {
	mu    sync.Mutex
	rules []rule
	calls []platform.Command
}

// On 은 이름이 name 이고 인자가 argPrefix 로 시작하는 명령에 res 와 err 를 돌려주는 규칙을 추가한다.
// 규칙은 추가된 순서대로 검사하며 처음 맞는 것을 쓴다. argPrefix 가 nil 이면 인자와 상관없이 맞는다.
func (f *Fake) On(name string, argPrefix []string, res platform.Result, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rules = append(f.rules, rule{name: name, argPrefix: slices.Clone(argPrefix), result: res, err: err})
}

// Run 은 호출을 기록하고 맞는 규칙의 결과를 돌려준다. ctx 가 이미 끝났으면 그 오류를 돌려주고 기록하지 않는다.
func (f *Fake) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	if err := ctx.Err(); err != nil {
		return platform.Result{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, platform.Command{
		Name:      cmd.Name,
		Args:      slices.Clone(cmd.Args),
		Env:       slices.Clone(cmd.Env),
		Timeout:   cmd.Timeout,
		MaxOutput: cmd.MaxOutput,
	})
	for _, r := range f.rules {
		if r.name == cmd.Name && hasPrefix(cmd.Args, r.argPrefix) {
			return r.result, r.err
		}
	}
	return platform.Result{}, fmt.Errorf("%w: %s %v", ErrNoRule, cmd.Name, cmd.Args)
}

// Calls 는 지금까지 받은 명령의 복사본을 호출 순서대로 돌려준다. 인자와 환경 변수 슬라이스까지 복사하므로
// 돌려받은 값을 고쳐도 기록은 바뀌지 않는다.
func (f *Fake) Calls() []platform.Command {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]platform.Command, 0, len(f.calls))
	for _, c := range f.calls {
		c.Args = slices.Clone(c.Args)
		c.Env = slices.Clone(c.Env)
		out = append(out, c)
	}
	return out
}

// hasPrefix 는 args 가 prefix 로 시작하는지 본다. prefix 가 비었으면 항상 참이다.
func hasPrefix(args, prefix []string) bool {
	if len(prefix) > len(args) {
		return false
	}
	for i, p := range prefix {
		if args[i] != p {
			return false
		}
	}
	return true
}
