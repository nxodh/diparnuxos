package main

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// networkConnectTimeout 은 연결 요청의 제한 시간이다. 데몬의 연결 제한(45초)보다 길다.
const networkConnectTimeout = 70 * time.Second

// network 는 network 명령 묶음이다.
func (c *cli) network(args []string) int {
	if len(args) == 0 {
		return c.networkStatus()
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "status":
		return c.networkStatus()
	case "wifi":
		return c.networkWifi(rest)
	}
	return c.usage(fmt.Sprintf("unknown network command %q", sub), "network")
}

func (c *cli) networkWifi(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: diparnux network wifi <list|scan|connect|disconnect|forget|on|off>", "network wifi")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return c.wifiList()
	case "scan":
		return c.wifiScan()
	case "connect":
		return c.wifiConnect(rest)
	case "disconnect":
		return c.wifiSimple("disconnect", func(ctx context.Context) ([]byte, error) { return c.api.WifiDisconnect(ctx) }, "disconnected")
	case "forget":
		if len(rest) != 1 {
			return c.usage("usage: diparnux network wifi forget <ssid>", "network wifi forget")
		}
		return c.wifiSimple("forget", func(ctx context.Context) ([]byte, error) { return c.api.WifiForget(ctx, rest[0]) }, "forgot "+rest[0])
	case "on", "off":
		on := sub == "on"
		return c.wifiSimple("power", func(ctx context.Context) ([]byte, error) { return c.api.WifiPower(ctx, on) }, "wireless "+sub)
	}
	return c.usage(fmt.Sprintf("unknown wifi command %q", sub), "network wifi")
}

func (c *cli) networkStatus() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	st, raw, err := c.api.Network(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	if len(st.Interfaces) == 0 {
		fmt.Fprintln(w, "no network interfaces")
	}
	for _, it := range st.Interfaces {
		addr := "-"
		if len(it.Addresses) > 0 {
			addr = strings.Join(it.Addresses, ", ")
		}
		fmt.Fprintf(w, "%-10s %-9s %-8s %s\n", it.Name, it.Kind, it.State, addr)
		if it.Gateway != "" {
			fmt.Fprintf(w, "%-10s gateway %s\n", "", it.Gateway)
		}
	}
	switch {
	case !st.Wifi.Available:
		fmt.Fprintf(w, "wifi: not available (%s)\n", st.Wifi.Reason)
	case st.Wifi.ConnectedSSID != "":
		fmt.Fprintf(w, "wifi: connected to %s on %s\n", asciiOnly(st.Wifi.ConnectedSSID), st.Wifi.Device)
	default:
		fmt.Fprintf(w, "wifi: %s on %s\n", st.Wifi.State, st.Wifi.Device)
	}
	if st.Online {
		fmt.Fprintln(w, "online: a default route exists")
	} else {
		fmt.Fprintln(w, "online: no default route")
	}
	return exitOK
}

func (c *cli) wifiList() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	reply, raw, err := c.api.WifiNetworks(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if len(reply.Networks) == 0 {
		fmt.Fprintln(c.env.stdout, "no wireless networks found; run 'diparnux network wifi scan' and list again")
		return exitOK
	}
	for _, n := range reply.Networks {
		mark := " "
		switch {
		case n.Connected:
			mark = "*"
		case n.Known:
			mark = "+"
		}
		fmt.Fprintf(c.env.stdout, "%s %-32s %-5s %3d%%  %d dBm\n", mark, asciiOnly(n.SSID), n.Security, n.Signal, n.SignalDBm)
	}
	fmt.Fprintln(c.env.stdout, "(* connected, + saved)")
	return exitOK
}

func (c *cli) wifiScan() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	raw, err := c.api.WifiScan(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintln(c.env.stdout, "scanning; list the networks in a few seconds with 'diparnux network wifi list'")
	return exitOK
}

// wifiConnect 는 SSID 에 연결한다. 비밀번호는 표준 입력이나 터미널에서만 받고 출력에 나오지 않는다.
func (c *cli) wifiConnect(args []string) int {
	fs := newFlags("network wifi connect")
	fromStdin := fs.Bool("password-stdin", false, "read the password from standard input")
	pos, code, ok := c.parse(fs, "network wifi connect", args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.usage("usage: diparnux network wifi connect <ssid> [--password-stdin]", "network wifi connect")
	}
	ssid := pos[0]
	password := ""
	switch {
	case *fromStdin:
		line, err := readLine(c.env.stdin)
		if err != nil {
			c.errorLine("cannot read the password from standard input")
			return exitFailure
		}
		password = line
	case c.env.stdinTTY || c.env.readPassword != nil:
		p, err := c.promptPassword("Password (leave empty for an open or saved network): ")
		if err != nil {
			c.errorLine("cannot read the password: " + err.Error())
			return exitFailure
		}
		password = p
	}
	ctx, cancel := c.timeout(networkConnectTimeout)
	defer cancel()
	raw, err := c.api.WifiConnect(ctx, ssid, password)
	password = ""
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "connected to %s\n", asciiOnly(ssid))
	return exitOK
}

func (c *cli) wifiSimple(what string, do func(context.Context) ([]byte, error), done string) int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	raw, err := do(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	_ = what
	fmt.Fprintln(c.env.stdout, done)
	return exitOK
}

// asciiOnly 는 ASCII 가 아닌 문자를 ?로 바꾼다. 실기 텍스트 콘솔은 한글을 표시하지 못한다(모양 대신 네모가 나온다).
// 정확한 이름이 필요하면 --json 을 쓴다.
func asciiOnly(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 && r < 0x7f {
			b.WriteRune(r)
		} else {
			b.WriteByte('?')
		}
	}
	return b.String()
}
