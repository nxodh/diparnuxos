package main

import (
	"fmt"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/agentupdate"
	"github.com/nxodh/operetaos/daemon/internal/client"
	"github.com/nxodh/operetaos/daemon/internal/updatectl"
)

// deberopaWaitLimit 은 Deberopa 갱신이 끝나기를 기다리는 최대 시간이다(내려받기 약 100 MB).
const deberopaWaitLimit = 20 * time.Minute

// deberopa 는 `opereta deberopa [status|check|update|auto on|off]` 이다.
// 코딩 에이전트 Deberopa 의 최신 릴리스를 확인하고 뒤처졌으면 갱신한다. 하루에 한 번 자동으로도 한다.
func (c *cli) deberopa(args []string) int {
	sub := "status"
	rest := args
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	const usage = "usage: opereta deberopa [status|check|update|auto on|off]"
	switch sub {
	case "status":
		if len(rest) != 0 {
			return c.usage(usage, "deberopa")
		}
		return c.deberopaStatus()
	case "check":
		if len(rest) != 0 {
			return c.usage(usage, "deberopa")
		}
		return c.deberopaRun(updatectl.ActionDeberopaCheck)
	case "update":
		if len(rest) != 0 {
			return c.usage(usage, "deberopa")
		}
		return c.deberopaRun(updatectl.ActionDeberopaUpdate)
	case "auto":
		if len(rest) != 1 || (rest[0] != "on" && rest[0] != "off") {
			return c.usage(usage, "deberopa")
		}
		action := updatectl.ActionDeberopaAutoOn
		if rest[0] == "off" {
			action = updatectl.ActionDeberopaAutoOff
		}
		return c.deberopaRun(action)
	}
	return c.usage(usage, "deberopa")
}

func (c *cli) deberopaStatus() int {
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	info, raw, err := c.api.UpdateInfo(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	c.printDeberopa(info.Deberopa)
	return exitOK
}

func (c *cli) printDeberopa(d agentupdate.Info) {
	w := c.env.stdout
	switch {
	case d.Installed != "":
		fmt.Fprintf(w, "deberopa: %s\n", d.Installed)
	case d.Status == agentupdate.StatusUnchecked:
		fmt.Fprintln(w, "deberopa: installed (the version shows after the first check)")
	default:
		fmt.Fprintln(w, "deberopa: not installed")
	}
	if d.Override && d.Packaged != "" {
		fmt.Fprintf(w, "image version: %s (a newer release was downloaded)\n", d.Packaged)
	}
	if d.Latest != "" {
		fmt.Fprintf(w, "latest release: %s\n", d.Latest)
	}
	fmt.Fprintf(w, "automatic updates: %s\n", map[bool]string{true: "on", false: "off"}[d.Auto])
	switch d.Status {
	case agentupdate.StatusAvail:
		fmt.Fprintln(w, "an update is available: opereta deberopa update")
	case agentupdate.StatusError:
		fmt.Fprintf(w, "last check failed: %s (%s)\n", d.Message, d.Code)
	case agentupdate.StatusUnchecked:
		fmt.Fprintln(w, "not checked for updates yet: opereta deberopa check")
	}
	if d.Checked != "" {
		fmt.Fprintf(w, "last check: %s\n", d.Checked)
	}
}

// deberopaRun 은 동작을 시작하고 상태 파일이 바뀔 때까지 기다린다.
func (c *cli) deberopaRun(action string) int {
	since := time.Now().UTC().Add(-2 * time.Second)
	ctx, cancel := c.timeout(shortTimeout)
	_, _, err := c.api.UpdateStart(ctx, client.UpdateRequest{Action: action})
	cancel()
	if err != nil {
		return c.updateStartFailure(err)
	}
	deadline := time.Now().Add(deberopaWaitLimit)
	for {
		if err := c.sleep(); err != nil {
			c.errorLine("interrupted; the work keeps running. Follow it with 'opereta deberopa status'")
			return exitFailure
		}
		ctx, cancel := c.timeout(shortTimeout)
		info, raw, err := c.api.UpdateInfo(ctx)
		cancel()
		if err != nil {
			return c.fail(err)
		}
		if info.Running {
			if time.Now().After(deadline) {
				c.errorLine("timed out; follow it with 'opereta deberopa status'")
				return exitFailure
			}
			continue
		}
		d := info.Deberopa
		done := false
		switch action {
		case updatectl.ActionDeberopaAutoOn:
			done = d.Auto
		case updatectl.ActionDeberopaAutoOff:
			done = !d.Auto
		default:
			t, perr := time.Parse(time.RFC3339, d.Checked)
			done = perr == nil && !t.Before(since)
		}
		if !done {
			if time.Now().After(deadline) {
				c.errorLine("timed out; follow it with 'opereta deberopa status'")
				return exitFailure
			}
			continue
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		c.printDeberopa(d)
		if d.Status == agentupdate.StatusError {
			return exitFailure
		}
		return exitOK
	}
}
