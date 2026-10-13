package main

import (
	"fmt"
	"strconv"
)

// services 는 서비스 목록을 보이고 제어한다.
//
//	diparnux services                       목록
//	diparnux services start|stop|restart|enable|disable <unit>
//	diparnux services logs <unit> [-n N]
func (c *cli) services(args []string) int {
	if len(args) == 0 {
		return c.servicesList()
	}
	switch args[0] {
	case "list":
		return c.servicesList()
	case "start", "stop", "restart", "enable", "disable":
		if len(args) != 2 {
			return c.usage("usage: diparnux services "+args[0]+" <unit>", "services")
		}
		ctx, cancel := c.timeout(startTimeout)
		defer cancel()
		raw, err := c.api.ServiceAction(ctx, args[1], args[0])
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		fmt.Fprintf(c.env.stdout, "%s: %s done\n", args[1], args[0])
		return exitOK
	case "logs":
		fs := newFlags("services logs")
		n := fs.Int("n", 100, "number of lines (1-1000)")
		pos, code, ok := c.parse(fs, "services logs", args[1:])
		if !ok {
			return code
		}
		if len(pos) != 1 {
			return c.usage("usage: diparnux services logs <unit> [-n N]", "services logs")
		}
		ctx, cancel := c.timeout(metaTimeout)
		defer cancel()
		text, raw, err := c.api.ServiceLogs(ctx, pos[0], *n)
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		fmt.Fprint(c.env.stdout, text)
		return exitOK
	}
	return c.usage(fmt.Sprintf("unknown services command %q", args[0]), "services")
}

func (c *cli) servicesList() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	list, raw, err := c.api.Services(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	rows := make([][]string, 0, len(list))
	for _, s := range list {
		mem := "-"
		if s.MemoryMiB > 0 {
			mem = humanMiB(s.MemoryMiB)
		}
		rows = append(rows, []string{s.Unit, s.Active + "/" + orDash(s.Sub), s.Enabled, mem, s.Title})
	}
	writeTable(c.env.stdout, []string{"UNIT", "STATE", "AT BOOT", "MEMORY", "WHAT"}, rows)
	return exitOK
}

// resources 는 CPU, 메모리, 디스크, 네트워크, 온도, GPU 를 한 번 측정해 보인다.
func (c *cli) resources(args []string) int {
	if len(args) != 0 {
		return c.usage("resources takes no arguments", "resources")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	r, raw, err := c.api.Resources(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w, s := c.env.stdout, r.System
	kv(w, "uptime", formatDuration(s.UptimeSeconds))
	kv(w, "cpu", fmt.Sprintf("%.1f%% of %d cores, load %.2f %.2f %.2f", s.CPU.UsedPercent, s.CPU.Cores, s.CPU.Load1, s.CPU.Load5, s.CPU.Load15))
	kv(w, "memory", fmt.Sprintf("%s used of %s", humanMiB(s.Memory.UsedMiB), humanMiB(s.Memory.TotalMiB)))
	if s.Memory.SwapTotalMiB > 0 {
		kv(w, "swap", fmt.Sprintf("%s used of %s", humanMiB(s.Memory.SwapUsedMiB), humanMiB(s.Memory.SwapTotalMiB)))
	}
	for _, d := range s.Disks {
		kv(w, "disk "+d.Mount, fmt.Sprintf("%s used of %s (%d%%)", humanMiB(d.UsedMiB), humanMiB(d.TotalMiB), d.Used))
	}
	for _, n := range s.Network {
		kv(w, "net "+n.Name, fmt.Sprintf("in %s/s, out %s/s", humanBytes(int64(n.RxPerSec)), humanBytes(int64(n.TxPerSec))))
	}
	for _, t := range s.Temperatures {
		kv(w, "temp "+t.Name, strconv.FormatFloat(t.Celsius, 'f', 1, 64)+" C")
	}
	for _, g := range r.GPUs {
		kv(w, fmt.Sprintf("gpu %d", g.Index), fmt.Sprintf("%s, %d/%d MiB, %d%% busy, %d C, %.0f W",
			g.Name, g.MemUsedMiB, g.MemTotalMiB, g.UtilGPUPercent, g.TemperatureC, float64(g.PowerMilliwatt)/1000))
	}
	if len(r.GPUs) == 0 && r.GPUReason != "" {
		kv(w, "gpu", "unavailable: "+r.GPUReason)
	}
	for _, p := range s.Top {
		kv(w, "process", fmt.Sprintf("%s (pid %d) %s", p.Name, p.PID, humanMiB(p.RSSMiB)))
	}
	return exitOK
}
