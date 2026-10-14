package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// vpn 은 WireGuard VPN 을 다룬다.
//
//	opereta vpn                     상태
//	opereta vpn on|off [--address 10.8.0.1/24] [--port 51820]
//	opereta vpn peer add <name> [--public-key K] [--endpoint HOST] [--out FILE]
//	opereta vpn peer rm <name|public-key>
func (c *cli) vpn(args []string) int {
	if len(args) == 0 {
		return c.vpnStatus()
	}
	switch args[0] {
	case "status":
		return c.vpnStatus()
	case "on", "off":
		return c.vpnSet(args[0] == "on", args[1:])
	case "peer":
		return c.vpnPeer(args[1:])
	}
	return c.usage(fmt.Sprintf("unknown vpn command %q", args[0]), "vpn")
}

func (c *cli) vpnStatus() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	v, raw, err := c.api.VPN(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	kv(w, "vpn", yesNo(v.Enabled))
	if v.Enabled {
		kv(w, "interface", v.Interface)
		kv(w, "address", v.Address)
		kv(w, "listen port", strconv.Itoa(v.ListenPort)+"/udp")
		kv(w, "public key", v.PublicKey)
	}
	if len(v.Peers) == 0 {
		fmt.Fprintln(w, "no peers; add one with: opereta vpn peer add <name>")
		return exitOK
	}
	rows := make([][]string, 0, len(v.Peers))
	for _, p := range v.Peers {
		rows = append(rows, []string{p.Name, strings.Join(p.AllowedIPs, ","), p.PublicKey})
	}
	writeTable(w, []string{"PEER", "ADDRESS", "PUBLIC KEY"}, rows)
	return exitOK
}

func (c *cli) vpnSet(on bool, args []string) int {
	fs := newFlags("vpn")
	address := fs.String("address", "", "VPN address of this computer with prefix, such as 10.8.0.1/24")
	port := fs.Int("port", 0, "UDP listen port (default 51820)")
	pos, code, ok := c.parse(fs, "vpn", args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return c.usage("usage: opereta vpn on|off [--address CIDR] [--port N]", "vpn")
	}
	var addr *string
	var p *int
	if *address != "" {
		addr = address
	}
	if *port != 0 {
		p = port
	}
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	v, raw, err := c.api.SetVPN(ctx, &on, addr, p)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if on {
		fmt.Fprintf(c.env.stdout, "vpn on: %s on %s, listening on %d/udp (opened in the firewall)\n", v.Interface, v.Address, v.ListenPort)
	} else {
		fmt.Fprintln(c.env.stdout, "vpn off")
	}
	return exitOK
}

func (c *cli) vpnPeer(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: opereta vpn peer <add|rm> ...", "vpn")
	}
	switch args[0] {
	case "add":
		fs := newFlags("vpn peer add")
		pub := fs.String("public-key", "", "the peer's own public key (otherwise a key pair is made here)")
		endpoint := fs.String("endpoint", "", "address the peer should connect to (default: this computer's .local name)")
		out := fs.String("out", "", "write the client configuration to this file (mode 0600) instead of showing it")
		pos, code, ok := c.parse(fs, "vpn peer add", args[1:])
		if !ok {
			return code
		}
		if len(pos) != 1 {
			return c.usage("usage: opereta vpn peer add <name> [--public-key K] [--endpoint HOST] [--out FILE]", "vpn peer add")
		}
		ctx, cancel := c.timeout(startTimeout)
		defer cancel()
		res, raw, err := c.api.AddVPNPeer(ctx, pos[0], *pub, *endpoint)
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		w := c.env.stdout
		fmt.Fprintf(w, "peer %s added with address %s\n", res.Peer.Name, strings.Join(res.Peer.AllowedIPs, ","))
		if res.ClientConfig == "" {
			return exitOK
		}
		if *out != "" {
			if err := os.WriteFile(*out, []byte(res.ClientConfig), 0o600); err != nil {
				c.errorLine(err.Error())
				return exitFailure
			}
			fmt.Fprintf(w, "client configuration written to %s (it contains the peer's private key; keep it private)\n", *out)
			return exitOK
		}
		fmt.Fprintln(w, "\nClient configuration (shown only once; it contains the peer's private key):")
		fmt.Fprintln(w, res.ClientConfig)
		return exitOK
	case "rm":
		if len(args) != 2 {
			return c.usage("usage: opereta vpn peer rm <name|public-key>", "vpn peer rm")
		}
		ctx, cancel := c.timeout(metaTimeout)
		defer cancel()
		raw, err := c.api.RemoveVPNPeer(ctx, args[1])
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		fmt.Fprintf(c.env.stdout, "peer %s removed\n", args[1])
		return exitOK
	}
	return c.usage(fmt.Sprintf("unknown vpn peer command %q", args[0]), "vpn")
}
