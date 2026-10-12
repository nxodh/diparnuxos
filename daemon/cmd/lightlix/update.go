package main

import (
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/client"
	"github.com/nxodh/lightlixos/daemon/internal/update"
	"github.com/nxodh/lightlixos/daemon/internal/updatectl"
)

// updateWaitLimit 은 갱신이 끝나기를 기다리는 최대 시간이다. 커널과 DKMS 빌드가 길 수 있다.
const updateWaitLimit = 3 * time.Hour

// update 는 update 명령이다. 동작은 데몬이 별도 유닛에서 하고, 이 명령은 시작과 진행 표시만 한다.
func (c *cli) update(args []string) int {
	fs := newFlags("update")
	check := fs.Bool("check", false, "show what would change and install nothing")
	rollback := fs.Bool("rollback", false, "return to the kernel and driver from before the last update")
	status := fs.Bool("status", false, "show the state of the last update")
	reboot := fs.Bool("reboot", false, "reboot when the update succeeds")
	pos, code, ok := c.parse(fs, "update", args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return c.usage("update takes no positional arguments", "update")
	}
	n := 0
	for _, b := range []bool{*check, *rollback, *status} {
		if b {
			n++
		}
	}
	if n > 1 {
		return c.usage("choose only one of --check, --rollback and --status", "update")
	}
	if *status {
		return c.updateStatus()
	}
	action := updatectl.ActionApply
	switch {
	case *check:
		action = updatectl.ActionCheck
	case *rollback:
		action = updatectl.ActionRollback
	}
	return c.updateRun(action, *reboot && action != updatectl.ActionCheck)
}

func (c *cli) updateStatus() int {
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	info, raw, err := c.api.UpdateInfo(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	if info.Live {
		fmt.Fprintln(w, "live session: updates apply to an installed system")
		return exitOK
	}
	if !info.Available {
		fmt.Fprintf(w, "updates are not available (%s)\n", info.Reason)
		return exitOK
	}
	st := info.State
	fmt.Fprintf(w, "state: %s\n", st.Phase)
	if info.RunningKernel != "" {
		fmt.Fprintf(w, "running kernel: %s\n", info.RunningKernel)
	}
	if st.Step != "" && st.Phase == update.PhaseRunning {
		fmt.Fprintf(w, "step: %s\n", st.Step)
	}
	if st.NewKernel != "" {
		fmt.Fprintf(w, "new kernel: %s\n", st.NewKernel)
	}
	if st.DriverTo != "" {
		fmt.Fprintf(w, "driver: %s -> %s\n", st.DriverFrom, st.DriverTo)
	}
	if st.RebootRequired {
		fmt.Fprintln(w, "reboot required")
	}
	if st.Code != "" {
		fmt.Fprintf(w, "last error: %s: %s\n", st.Code, st.Message)
	}
	if info.LastCheck != nil && info.LastCheck.Plan != nil {
		p := info.LastCheck.Plan
		if p.UpToDate {
			fmt.Fprintf(w, "last check (%s): up to date\n", info.LastCheck.Time)
		} else {
			fmt.Fprintf(w, "last check (%s): %d package changes\n", info.LastCheck.Time, len(p.Changes))
		}
	}
	return exitOK
}

// updateRun 은 동작을 시작하고 끝날 때까지 이벤트를 보여 준다.
func (c *cli) updateRun(action string, reboot bool) int {
	started := time.Now().UTC().Add(-2 * time.Second)
	ctx, cancel := c.timeout(shortTimeout)
	_, _, err := c.api.UpdateStart(ctx, client.UpdateRequest{Action: action, Reboot: reboot})
	cancel()
	if err != nil {
		return c.updateStartFailure(err)
	}
	seen := 0
	deadline := time.Now().Add(updateWaitLimit)
	var info updatectl.Info
	var raw []byte
	for {
		if err := c.sleep(); err != nil {
			c.errorLine("interrupted; the update keeps running. Follow it with 'lightlix update --status'")
			return exitFailure
		}
		ctx, cancel := c.timeout(shortTimeout)
		cur, r, err := c.api.UpdateInfo(ctx)
		cancel()
		if err != nil {
			return c.fail(err)
		}
		info, raw = cur, r
		if !c.asJSON {
			seen = c.printUpdateEvents(info.State.Events, seen, started)
		}
		if !info.Running && c.updateFinished(info, action, started) {
			break
		}
		if time.Now().After(deadline) {
			c.errorLine("timed out waiting for the update; follow it with 'lightlix update --status'")
			return exitFailure
		}
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	return c.updateSummary(info, action)
}

// updateFinished 는 이번 요청의 결과가 상태 파일에 반영되었는지 본다.
func (c *cli) updateFinished(info updatectl.Info, action string, since time.Time) bool {
	switch action {
	case updatectl.ActionCheck:
		if info.LastCheck == nil {
			return false
		}
		t, err := time.Parse(time.RFC3339, info.LastCheck.Time)
		return err == nil && !t.Before(since)
	default:
		t, err := time.Parse(time.RFC3339, info.State.Started)
		if err != nil || t.Before(since) {
			// 업데이트할 것이 없어 상태가 바뀌지 않은 경우도 있다. 이벤트가 이번 요청 시각 이후면 끝난 것이다.
			for _, ev := range info.State.Events {
				if et, perr := time.Parse(time.RFC3339, ev.Time); perr == nil && !et.Before(since) && ev.Step == "done" {
					return true
				}
			}
			return false
		}
		return info.State.Phase != update.PhaseRunning
	}
}

func (c *cli) printUpdateEvents(events []update.Event, seen int, since time.Time) int {
	for i := seen; i < len(events); i++ {
		ev := events[i]
		if t, err := time.Parse(time.RFC3339, ev.Time); err == nil && t.Before(since) {
			continue
		}
		fmt.Fprintf(c.env.stdout, "[%s] %s\n", ev.Step, ev.Message)
	}
	return len(events)
}

func (c *cli) updateSummary(info updatectl.Info, action string) int {
	w := c.env.stdout
	if action == updatectl.ActionCheck {
		ck := info.LastCheck
		if ck.Error != nil {
			c.errorLine(fmt.Sprintf("%s (%s)", ck.Error.Message, ck.Error.Code))
			return exitFailure
		}
		p := ck.Plan
		if p == nil || p.UpToDate {
			fmt.Fprintln(w, "the system is up to date")
			return exitOK
		}
		fmt.Fprintf(w, "%d package changes\n", len(p.Changes))
		if len(p.Kernels) > 0 {
			fmt.Fprintf(w, "new kernel: %s\n", strings.Join(p.Kernels, ", "))
		}
		if p.DriverTo != "" {
			fmt.Fprintf(w, "driver: %s -> %s\n", p.DriverFrom, p.DriverTo)
		}
		fmt.Fprintln(w, "run 'lightlix update' to install")
		return exitOK
	}
	st := info.State
	if st.Phase == update.PhaseFailed {
		c.errorLine(fmt.Sprintf("%s (%s)", st.Message, st.Code))
		return exitFailure
	}
	switch st.Phase {
	case update.PhasePendingBoot:
		if st.NewKernel != "" {
			fmt.Fprintf(w, "installed. reboot to try the new kernel %s; it becomes the default only after the boot check passes\n", st.NewKernel)
		} else {
			fmt.Fprintln(w, "installed. reboot to load the new driver")
		}
	case update.PhaseRolledBack:
		fmt.Fprintln(w, "rolled back. reboot to use the previous kernel and driver")
	default:
		fmt.Fprintln(w, "done; no reboot is needed")
	}
	return exitOK
}

// updateStartFailure 는 시작 요청의 거부를 사용자 말로 바꾼다.
func (c *cli) updateStartFailure(err error) int {
	var ae *client.APIError
	if errors.As(err, &ae) {
		switch ae.Code {
		case update.CodeBusy:
			c.errorLine("an update operation is already running; follow it with 'lightlix update --status'")
			return exitFailure
		case update.CodePendingBoot:
			c.errorLine("the previous update is waiting for a reboot; reboot first so it can be verified")
			return exitFailure
		case update.CodeLiveSession:
			c.errorLine("this is a live session; install Lightlix first, updates apply to an installed system")
			return exitFailure
		case updatectl.CodeUnavailable:
			c.errorLine("the update helper is not available on this system")
			return exitFailure
		}
		if ae.Status == http.StatusNotImplemented {
			c.errorLine("this lightlixd does not support updates")
			return exitFailure
		}
	}
	return c.fail(err)
}
