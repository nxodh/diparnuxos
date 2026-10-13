package main

import (
	"fmt"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/power"
)

// power 는 상시 가동(덮개를 닫거나 오래 두어도 절전과 종료로 서버가 멈추지 않게 하는 설정)을 보여 주거나 켜고 끈다.
func (c *cli) power(args []string) int {
	const use = "usage: diparnux power [on|off]"
	if len(args) > 1 {
		return c.usage(use, "power")
	}
	var (
		res power.Status
		raw []byte
		err error
	)
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	switch {
	case len(args) == 0:
		res, raw, err = c.api.Power(ctx)
	case args[0] == "on" || args[0] == "off":
		res, raw, err = c.api.SetPower(ctx, args[0] == "on")
	default:
		return c.usage(use, "power")
	}
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	kv(c.env.stdout, "always on", yesNo(res.AlwaysOn))
	kv(c.env.stdout, "configured", yesNo(res.Enabled))
	kv(c.env.stdout, "lid", orDash(res.Lid))
	if res.HasBattery {
		power := "AC"
		if res.OnBattery {
			power = "battery"
		}
		pct := "-"
		if res.BatteryPercent >= 0 {
			pct = fmt.Sprintf("%d%%", res.BatteryPercent)
		}
		kv(c.env.stdout, "power source", power)
		kv(c.env.stdout, "battery", pct)
	}
	kv(c.env.stdout, "uptime", formatDuration(res.UptimeSeconds))
	var failed []string
	for _, ch := range res.Checks {
		if !ch.OK {
			got := ch.Got
			if got == "" {
				got = "unknown"
			}
			failed = append(failed, fmt.Sprintf("%s=%s (want %s)", ch.Name, got, ch.Want))
		}
	}
	if len(failed) > 0 {
		kv(c.env.stdout, "failed checks", strings.Join(failed, ", "))
	}
	for _, n := range res.Notes {
		fmt.Fprintf(c.env.stdout, "note: %s\n", n)
	}
	return exitOK
}

// formatDuration 은 초를 "3d 4h 12m" 형태로 바꾼다.
func formatDuration(sec int64) string {
	if sec <= 0 {
		return "-"
	}
	d, h, m := sec/86400, (sec%86400)/3600, (sec%3600)/60
	switch {
	case d > 0:
		return fmt.Sprintf("%dd %dh %dm", d, h, m)
	case h > 0:
		return fmt.Sprintf("%dh %dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}
