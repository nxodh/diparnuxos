package main

import (
	"fmt"
	"strings"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/boot"
	"github.com/nxodh/operetaos/daemon/internal/client"
	"github.com/nxodh/operetaos/daemon/internal/updatectl"
)

const bootUsage = "usage: opereta boot [status|next <opereta|windows|usb|NNNN>|order <target>|clear-next|repair|cleanup|auto on|off]"

// bootWaitLimit 은 부팅 동작이 끝나기를 기다리는 최대 시간이다(GRUB 재설치가 가장 오래 걸린다).
const bootWaitLimit = 15 * time.Minute

// boot 는 `opereta boot ...` 다. UEFI 부팅 항목을 보고, 다음 한 번만 부팅할 항목을 정하고, 항목을 복구한다.
func (c *cli) boot(args []string) int {
	sub := "status"
	rest := args
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	switch sub {
	case "status":
		if len(rest) != 0 {
			return c.usage(bootUsage, "boot")
		}
		return c.bootStatus()
	case "next", "order":
		if len(rest) != 1 || !boot.ValidTarget(rest[0]) {
			return c.usage(bootUsage, "boot")
		}
		action := updatectl.ActionBootNext
		if sub == "order" {
			action = updatectl.ActionBootOrder
		}
		return c.bootRun(action + ":" + strings.ToLower(rest[0]))
	case "clear-next":
		return c.bootNoArg(rest, updatectl.ActionBootClearNext)
	case "repair":
		return c.bootNoArg(rest, updatectl.ActionBootRepair)
	case "cleanup":
		return c.bootNoArg(rest, updatectl.ActionBootCleanup)
	case "auto":
		if len(rest) != 1 || (rest[0] != "on" && rest[0] != "off") {
			return c.usage(bootUsage, "boot")
		}
		if rest[0] == "on" {
			return c.bootRun(updatectl.ActionBootAutoOn)
		}
		return c.bootRun(updatectl.ActionBootAutoOff)
	}
	return c.usage(bootUsage, "boot")
}

func (c *cli) bootNoArg(rest []string, action string) int {
	if len(rest) != 0 {
		return c.usage(bootUsage, "boot")
	}
	return c.bootRun(action)
}

func (c *cli) bootStatus() int {
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	info, raw, err := c.api.UpdateInfo(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	c.printBoot(info.Boot)
	return exitOK
}

func (c *cli) printBoot(b boot.Info) {
	w := c.env.stdout
	if !b.UEFI {
		fmt.Fprintln(w, "boot mode: legacy BIOS. Boot entries are not available; Opereta needs UEFI.")
		return
	}
	if !b.Available {
		fmt.Fprintln(w, "boot mode: UEFI (efibootmgr is not available, so entries cannot be read)")
		return
	}
	fmt.Fprintf(w, "boot mode: UEFI, Secure Boot %s\n", b.SecureBoot)
	fmt.Fprintf(w, "started from: %s\n", orDash(entryName(b, b.Current)))
	if b.Next != "" {
		fmt.Fprintf(w, "next boot only: %s\n", entryName(b, b.Next))
	}
	fmt.Fprintln(w, "boot order:")
	for i, n := range b.Order {
		fmt.Fprintf(w, "  %d. %s\n", i+1, entryName(b, n))
	}
	fmt.Fprintf(w, "start Opereta next time after every shutdown: %s\n", map[bool]string{true: "on", false: "off"}[b.Auto])
	if b.BitLocker {
		fmt.Fprintln(w, "note: this disk has BitLocker volumes. Keep the Windows recovery key at hand when you change boot settings.")
	}
	for _, p := range b.Problems {
		fmt.Fprintf(w, "problem: %s\n", bootProblemText(p))
	}
}

func entryName(b boot.Info, num string) string {
	for _, e := range b.Entries {
		if e.Num == num {
			return fmt.Sprintf("%s (%s)", e.Label, num)
		}
	}
	return num
}

// bootProblemText 는 문제 코드를 해결 방법과 함께 한 줄로 쓴다.
func bootProblemText(p boot.Problem) string {
	switch p.Code {
	case boot.ProblemNotUEFI:
		return "this system was not started in UEFI mode. Restart in UEFI mode (turn off CSM/legacy boot in the firmware settings)."
	case boot.ProblemNoEntry:
		return "there is no 'opereta' boot entry. Create it with: opereta boot repair"
	case boot.ProblemNotFirst:
		return "the firmware starts another entry before Opereta. Choose Opereta once with: opereta boot next opereta"
	case boot.ProblemFirmwareReorders:
		return "this firmware rewrites the boot order after you set it. Use 'opereta boot next opereta' for one boot, or 'opereta boot auto on' to start Opereta after every shutdown from Opereta."
	case boot.ProblemLegacyEntries:
		return "folders or boot entries from an earlier name of this system remain. Remove them with: opereta boot cleanup"
	case boot.ProblemESPNotMounted:
		return "the EFI system partition is not mounted at /boot/efi."
	case boot.ProblemNoFallback:
		return "the fallback boot file is missing. Restore it with: opereta boot repair"
	}
	return p.Message
}

// bootRun 은 동작을 시작하고 마지막 동작 기록이 갱신될 때까지 기다린다.
func (c *cli) bootRun(action string) int {
	since := time.Now().UTC().Add(-2 * time.Second)
	ctx, cancel := c.timeout(shortTimeout)
	_, _, err := c.api.UpdateStart(ctx, client.UpdateRequest{Action: action})
	cancel()
	if err != nil {
		return c.updateStartFailure(err)
	}
	deadline := time.Now().Add(bootWaitLimit)
	idle := 0
	for {
		if err := c.sleep(); err != nil {
			c.errorLine("interrupted; the work keeps running. Check it with 'opereta boot status'")
			return exitFailure
		}
		ctx, cancel := c.timeout(shortTimeout)
		info, raw, err := c.api.UpdateInfo(ctx)
		cancel()
		if err != nil {
			return c.fail(err)
		}
		if info.Running {
			idle = 0
			if time.Now().After(deadline) {
				c.errorLine("timed out; check it with 'opereta boot status'")
				return exitFailure
			}
			continue
		}
		t, perr := time.Parse(time.RFC3339, info.Boot.LastTime)
		if perr == nil && !t.Before(since) {
			if c.asJSON {
				return c.printJSON(raw)
			}
			c.printBoot(info.Boot)
			return exitOK
		}
		// 유닛이 끝났는데 기록이 없다: 동작이 실패했다.
		idle++
		if idle >= 2 || time.Now().After(deadline) {
			c.errorLine("the boot action did not complete; see 'journalctl -u opereta-update-run' for the reason")
			return exitFailure
		}
	}
}
