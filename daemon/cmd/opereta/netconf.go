package main

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/client"
)

// splitList 는 쉼표로 나눈 목록이다. 빈 값은 nil 이다.
func splitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// networkSet 은 유선 인터페이스의 IP 설정을 바꾼다.
//
//	opereta network set <iface> --dhcp [--dns A,B] [--domains X,Y]
//	opereta network set <iface> --address 192.168.0.50/24 [--gateway G] [--dns A,B] [--domains X,Y]
func (c *cli) networkSet(args []string) int {
	fs := newFlags("network set")
	dhcp := fs.Bool("dhcp", false, "use DHCP")
	address := fs.String("address", "", "static address with prefix length, such as 192.168.0.50/24 (comma separated for several)")
	gateway := fs.String("gateway", "", "default gateway for a static address")
	dns := fs.String("dns", "", "DNS servers, comma separated")
	domains := fs.String("domains", "", "search domains, comma separated")
	confirm := fs.Int("confirm", 60, "seconds to wait for 'opereta network confirm' before the change is reverted (0 = keep without confirmation)")
	pos, code, ok := c.parse(fs, "network set", args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.usage("usage: opereta network set <interface> (--dhcp | --address CIDR [--gateway IP]) [--dns A,B] [--domains X] [--confirm SECONDS]", "network set")
	}
	req := client.WiredRequest{DNS: splitList(*dns), Domains: splitList(*domains), ConfirmSeconds: confirm}
	switch {
	case *dhcp && *address != "":
		return c.usage("choose --dhcp or --address, not both", "network set")
	case *dhcp:
		req.Mode = "dhcp"
	case *address != "":
		req.Mode = "static"
		req.Addresses = splitList(*address)
		req.Gateway = *gateway
	default:
		return c.usage("give --dhcp or --address", "network set")
	}
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	res, raw, err := c.api.SetWired(ctx, pos[0], req)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	fmt.Fprintf(w, "%s: %s\n", res.Config.Interface, describeWired(res.Config))
	if res.Pending != nil {
		fmt.Fprintf(w, "applied. If you can still reach this computer, confirm within %d seconds: opereta network confirm\n", res.ConfirmSeconds)
		fmt.Fprintln(w, "otherwise the previous settings come back by themselves.")
	}
	return exitOK
}

func describeWired(c client.WiredConfig) string {
	if c.Mode != "static" {
		s := "DHCP"
		if len(c.DNS) > 0 {
			s += ", DNS " + strings.Join(c.DNS, ", ")
		}
		return s
	}
	s := "static " + strings.Join(c.Addresses, ", ")
	if c.Gateway != "" {
		s += ", gateway " + c.Gateway
	}
	if len(c.DNS) > 0 {
		s += ", DNS " + strings.Join(c.DNS, ", ")
	}
	return s
}

func (c *cli) networkConfirm(revert bool) int {
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	var raw []byte
	var err error
	if revert {
		raw, err = c.api.RevertNetwork(ctx)
	} else {
		raw, err = c.api.ConfirmNetwork(ctx)
	}
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if revert {
		fmt.Fprintln(c.env.stdout, "reverted to the previous settings")
	} else {
		fmt.Fprintln(c.env.stdout, "confirmed")
	}
	return exitOK
}

// networkHostname 은 호스트 이름을 보이거나 바꾼다.
func (c *cli) networkHostname(args []string) int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	if len(args) == 0 {
		cfg, raw, err := c.api.NetworkConfig(ctx)
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		fmt.Fprintln(c.env.stdout, cfg.Hostname)
		return exitOK
	}
	if len(args) != 1 {
		return c.usage("usage: opereta network hostname [name]", "network hostname")
	}
	raw, err := c.api.SetHostname(ctx, args[0])
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "host name set to %s (reachable as %s.local when LAN exposure is on)\n", strings.ToLower(args[0]), strings.ToLower(args[0]))
	return exitOK
}

// networkWOL 은 깨우기(Wake-on-LAN)를 켜거나 끈다.
func (c *cli) networkWOL(args []string) int {
	if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
		ctx, cancel := c.timeout(metaTimeout)
		defer cancel()
		if len(args) == 0 {
			cfg, raw, err := c.api.NetworkConfig(ctx)
			if err != nil {
				return c.fail(err)
			}
			if c.asJSON {
				return c.printJSON(raw)
			}
			kv(c.env.stdout, "wake-on-lan", yesNo(cfg.WakeOnLAN))
			return exitOK
		}
		return c.usage("usage: opereta network wol [on|off]", "network wol")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	raw, err := c.api.SetWakeOnLAN(ctx, args[0] == "on")
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "wake-on-lan %s (applies from the next boot; the adapter and the firmware must support it)\n", args[0])
	return exitOK
}

// networkDiagnose 는 연결을 단계별로 점검한다. 실패한 항목이 있으면 종료 코드 1 이다.
func (c *cli) networkDiagnose() int {
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	checks, raw, err := c.api.Diagnose(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	failed := false
	for _, ch := range checks {
		mark := "ok  "
		switch {
		case ch.Skipped:
			mark = "skip"
		case !ch.OK:
			mark = "FAIL"
			failed = true
		}
		fmt.Fprintf(c.env.stdout, "%s  %-9s %s\n", mark, ch.Name, ch.Detail)
	}
	if failed {
		return exitFailure
	}
	return exitOK
}

// networkConfigShow 는 유선 인터페이스별 설정을 보인다.
func (c *cli) networkConfigShow() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	cfg, raw, err := c.api.NetworkConfig(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	kv(w, "host name", orDash(cfg.Hostname))
	kv(w, "wake-on-lan", yesNo(cfg.WakeOnLAN))
	for _, it := range cfg.Interfaces {
		kv(w, it.Interface, describeWired(it))
	}
	if cfg.Pending != nil {
		fmt.Fprintf(w, "a change to %s is waiting for 'opereta network confirm' (reverts at %s)\n", cfg.Pending.Interface, cfg.Pending.Deadline)
	}
	return exitOK
}

// firewall 은 firewall 명령 묶음이다.
func (c *cli) firewall(args []string) int {
	if len(args) == 0 {
		return c.firewallList()
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return c.firewallList()
	case "add":
		return c.firewallAdd(rest)
	case "rm":
		if len(rest) != 1 {
			return c.usage("usage: opereta firewall rm <id>", "firewall rm")
		}
		return c.firewallDelete(rest[0])
	case "enable", "disable":
		if len(rest) != 1 {
			return c.usage("usage: opereta firewall "+sub+" <id>", "firewall "+sub)
		}
		return c.firewallToggle(rest[0], sub == "enable")
	}
	return c.usage(fmt.Sprintf("unknown firewall command %q", sub), "firewall")
}

func portText(port, end int) string {
	if end != 0 && end != port {
		return strconv.Itoa(port) + "-" + strconv.Itoa(end)
	}
	return strconv.Itoa(port)
}

func (c *cli) firewallList() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	fw, raw, err := c.api.Firewall(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	fmt.Fprintf(w, "incoming policy: %s (everything not listed is blocked)\n", fw.Policy)
	if len(fw.Builtin) > 0 {
		fmt.Fprintln(w, "\nopened by features:")
		rows := make([][]string, 0, len(fw.Builtin))
		for _, o := range fw.Builtin {
			rows = append(rows, []string{o.Name, o.Protocol, portText(o.Port, o.PortEnd), o.Source, o.Reason})
		}
		writeTable(w, []string{"NAME", "PROTO", "PORT", "FROM", "WHY"}, rows)
	}
	fmt.Fprintln(w, "\nyour rules:")
	if len(fw.Rules) == 0 {
		fmt.Fprintln(w, "none; add one with: opereta firewall add <name> tcp|udp <port> [--from any|lan|CIDR]")
		return exitOK
	}
	rows := make([][]string, 0, len(fw.Rules))
	for _, r := range fw.Rules {
		rows = append(rows, []string{r.ID, r.Name, r.Protocol, portText(r.Port, r.PortEnd), r.Source, yesNo(r.Enabled)})
	}
	writeTable(w, []string{"ID", "NAME", "PROTO", "PORT", "FROM", "ON"}, rows)
	return exitOK
}

func (c *cli) firewallAdd(args []string) int {
	fs := newFlags("firewall add")
	from := fs.String("from", "any", "who may connect: any, lan, or an address range such as 192.168.1.0/24")
	pos, code, ok := c.parse(fs, "firewall add", args)
	if !ok {
		return code
	}
	if len(pos) != 3 {
		return c.usage("usage: opereta firewall add <name> <tcp|udp> <port|first-last> [--from any|lan|CIDR]", "firewall add")
	}
	port, end, err := parsePortRange(pos[2])
	if err != nil {
		return c.usage(err.Error(), "firewall add")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	rule, raw, err := c.api.AddFirewallRule(ctx, client.FirewallRuleRequest{Name: pos[0], Protocol: pos[1], Port: port, PortEnd: end, Source: *from})
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "rule %s added: %s %s/%s from %s\n", rule.ID, rule.Name, rule.Protocol, portText(rule.Port, rule.PortEnd), rule.Source)
	return exitOK
}

func parsePortRange(s string) (int, int, error) {
	first, last, isRange := strings.Cut(s, "-")
	port, err := strconv.Atoi(first)
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a port number", s)
	}
	if !isRange {
		return port, 0, nil
	}
	end, err := strconv.Atoi(last)
	if err != nil {
		return 0, 0, fmt.Errorf("%q is not a port range", s)
	}
	return port, end, nil
}

func (c *cli) firewallDelete(id string) int {
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	raw, err := c.api.DeleteFirewallRule(ctx, id)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "rule %s removed\n", id)
	return exitOK
}

func (c *cli) firewallToggle(id string, on bool) int {
	ctx, cancel := c.timeout(shortTimeout)
	fw, _, err := c.api.Firewall(ctx)
	cancel()
	if err != nil {
		return c.fail(err)
	}
	for _, r := range fw.Rules {
		if r.ID != id {
			continue
		}
		ctx, cancel := c.timeout(shortTimeout)
		defer cancel()
		_, raw, err := c.api.UpdateFirewallRule(ctx, id, client.FirewallRuleRequest{
			Name: r.Name, Protocol: r.Protocol, Port: r.Port, PortEnd: r.PortEnd, Source: r.Source, Enabled: &on,
		})
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		state := "disabled"
		if on {
			state = "enabled"
		}
		fmt.Fprintf(c.env.stdout, "rule %s %s\n", id, state)
		return exitOK
	}
	c.errorLine("there is no rule " + id)
	return exitFailure
}
