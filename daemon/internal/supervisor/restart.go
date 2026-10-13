package supervisor

import (
	"context"
	"time"
)

// 자동 재시작 정책이다. 모델 서버가 예상 밖으로 멈추면 이 대기 시간을 두고 다시 시작한다.
// 모든 시도가 실패하면 포기하고 failed 상태와 마지막 오류를 남긴다(사용자가 원인을 보고 직접 시작한다).
var restartBackoff = []time.Duration{10 * time.Second, 30 * time.Second, 2 * time.Minute, 5 * time.Minute, 15 * time.Minute}

// stableRun 만큼 계속 돌았으면 이전 실패는 잊는다.
const stableRun = 10 * time.Minute

// RestartInfo 는 자동 재시작의 진행 상황이다.
type RestartInfo struct {
	// Attempt 는 지금까지 시도한 횟수다.
	Attempt int `json:"attempt"`
	// Max 는 최대 시도 횟수다.
	Max int `json:"max"`
	// NextAt 은 다음 시도 시각이다. 대기 중이 아니면 비어 있다.
	NextAt time.Time `json:"next_at,omitempty"`
	// GaveUp 이 true 이면 더 시도하지 않는다.
	GaveUp bool `json:"gave_up"`
}

type restartState struct {
	attempts int
	nextAt   time.Time
	gaveUp   bool
}

// info 는 시도한 적이 없으면 nil 이다. mu 를 쥔 채 부른다.
func (r restartState) info() *RestartInfo {
	if r.attempts == 0 && r.nextAt.IsZero() && !r.gaveUp {
		return nil
	}
	return &RestartInfo{Attempt: r.attempts, Max: len(restartBackoff), NextAt: r.nextAt, GaveUp: r.gaveUp}
}

// restartTick 은 Watch 가 30초마다 부른다. 서버가 예상 밖으로 멈췄으면 다시 시작할 때가 됐는지 보고 시작한다.
//
// 다시 시작하는 경우는 둘이다. 돌던 서버가 죽었을 때(server_exited), 그리고 자동 재시작 도중 시작이 실패했을 때다.
// 사용자가 멈추면(stopped) 하지 않고, 사용자가 설정에서 끄면 하지 않는다.
func (s *Supervisor) restartTick(ctx context.Context) {
	cfg := s.opts.Config()
	st := s.Status()
	now := s.now()

	s.mu.Lock()
	switch st.State {
	case StateRunning, StateRunningCPU:
		if !st.Since.IsZero() && now.Sub(st.Since) >= stableRun && s.restart != (restartState{}) {
			s.restart = restartState{}
		}
		s.mu.Unlock()
		return
	case StateFailed:
	default:
		s.mu.Unlock()
		return
	}
	inSequence := s.restart.attempts > 0
	exited := st.LastError != nil && st.LastError.Code == CodeServerExited
	if !cfg.AutoRestart || st.ModelID == "" || s.restart.gaveUp || (!exited && !inSequence) {
		s.mu.Unlock()
		return
	}
	if s.restart.attempts >= len(restartBackoff) {
		s.restart.gaveUp = true
		s.restart.nextAt = time.Time{}
		s.mu.Unlock()
		s.logf("auto restart: giving up after %d attempts; start the server from the Control Center or with 'diparnux serve start'", len(restartBackoff))
		return
	}
	if s.restart.nextAt.IsZero() {
		next := s.restart.attempts + 1
		wait := restartBackoff[s.restart.attempts]
		s.restart.nextAt = now.Add(wait)
		s.mu.Unlock()
		s.logf("auto restart: attempt %d of %d in %s", next, len(restartBackoff), wait)
		return
	}
	if now.Before(s.restart.nextAt) {
		s.mu.Unlock()
		return
	}
	s.restart.attempts++
	s.restart.nextAt = time.Time{}
	attempt := s.restart.attempts
	s.mu.Unlock()

	s.logf("auto restart: starting %s (attempt %d of %d)", st.ModelID, attempt, len(restartBackoff))
	if _, err := s.Start(ctx, StartRequest{ModelID: st.ModelID}); err != nil {
		s.logf("auto restart: attempt %d failed: %v", attempt, err)
		return
	}
	s.logf("auto restart: the server is running again")
}
