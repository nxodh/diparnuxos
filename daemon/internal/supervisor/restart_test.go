package supervisor

import (
	"context"
	"testing"
	"time"
)

// killServer 는 돌던 서버가 예상 밖으로 죽은 것처럼 만들고 감시를 한 번 돌린다.
func killServer(t *testing.T, f *fixture) {
	t.Helper()
	f.sys.mu.Lock()
	f.sys.activeSeq = []string{"failed"}
	f.sys.pid = "0"
	f.sys.mu.Unlock()
	f.sup.watchOnce(context.Background())
	st := f.sup.Status()
	if st.State != StateFailed || st.LastError == nil || st.LastError.Code != CodeServerExited {
		t.Fatalf("the server death was not detected: %+v", st)
	}
	f.sys.mu.Lock()
	f.sys.activeSeq = nil
	f.sys.active = "inactive"
	f.sys.mu.Unlock()
}

func advance(f *fixture, d time.Duration) {
	f.clk.mu.Lock()
	f.clk.t = f.clk.t.Add(d)
	f.clk.mu.Unlock()
}

func TestAutoRestartStartsTheServerAgainAfterTheBackoff(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatal(err)
	}
	starts := f.sys.count("systemctl", "start")
	killServer(t, f)

	// 첫 확인에서는 대기만 정한다.
	f.sup.restartTick(context.Background())
	st := f.sup.Status()
	if st.Restart == nil || st.Restart.Attempt != 0 || st.Restart.NextAt.IsZero() || st.Restart.Max != len(restartBackoff) {
		t.Fatalf("restart info = %+v", st.Restart)
	}
	if f.sys.count("systemctl", "start") != starts {
		t.Fatal("the server was restarted before the backoff elapsed")
	}
	advance(f, 5*time.Second)
	f.sup.restartTick(context.Background())
	if f.sys.count("systemctl", "start") != starts {
		t.Fatal("the server was restarted too early")
	}

	advance(f, 6*time.Second)
	f.sup.restartTick(context.Background())
	st = f.sup.Status()
	if st.State != StateRunning || f.sys.count("systemctl", "start") != starts+1 {
		t.Fatalf("the server did not come back: %+v", st)
	}
	if st.Restart == nil || st.Restart.Attempt != 1 {
		t.Fatalf("restart info = %+v", st.Restart)
	}

	// 오래 안정적으로 돌면 실패 기록을 잊는다.
	advance(f, stableRun+time.Second)
	f.sup.restartTick(context.Background())
	if r := f.sup.Status().Restart; r != nil {
		t.Fatalf("restart info was kept after a stable run: %+v", r)
	}
}

func TestAutoRestartRespectsTheSettingAndUserStops(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	f.cfg.AutoRestart = false
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatal(err)
	}
	killServer(t, f)
	advance(f, time.Hour)
	f.sup.restartTick(context.Background())
	f.sup.restartTick(context.Background())
	if st := f.sup.Status(); st.State != StateFailed || st.Restart != nil {
		t.Fatalf("a disabled auto restart acted: %+v", st)
	}

	// 사용자가 멈춘 서버는 다시 시작하지 않는다.
	f.cfg.AutoRestart = true
	if err := f.sup.Stop(context.Background()); err != nil {
		t.Fatal(err)
	}
	advance(f, time.Hour)
	f.sup.restartTick(context.Background())
	if st := f.sup.Status(); st.State != StateStopped {
		t.Fatalf("a stopped server was restarted: %+v", st)
	}
}

func TestAutoRestartGivesUpAfterTheLastAttempt(t *testing.T) {
	f := newFixture(t, readyGPU(), "ready")
	if _, err := f.startSync(StartRequest{}); err != nil {
		t.Fatal(err)
	}
	killServer(t, f)
	// 시작이 계속 실패하게 한다: 서버를 시작하는 명령은 성공하지만 건강 검사가 통과하지 않는다.
	f.health.Store(503)
	for i := 0; i < len(restartBackoff)+2; i++ {
		f.sup.restartTick(context.Background())
		advance(f, 20*time.Minute)
		f.sup.restartTick(context.Background())
	}
	st := f.sup.Status()
	if st.Restart == nil || !st.Restart.GaveUp || st.Restart.Attempt != len(restartBackoff) {
		t.Fatalf("restart info = %+v (state %s)", st.Restart, st.State)
	}
	if st.State != StateFailed {
		t.Fatalf("state = %s", st.State)
	}
	before := f.sys.count("systemctl", "start")
	advance(f, 2*time.Hour)
	f.sup.restartTick(context.Background())
	if f.sys.count("systemctl", "start") != before {
		t.Fatal("the supervisor kept restarting after giving up")
	}
}
