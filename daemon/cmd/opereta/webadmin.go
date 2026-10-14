package main

import (
	"fmt"
	"strings"
)

// webadmin 은 원격 관리 웹 페이지를 보이고 켜고 끄고 비밀번호를 정한다.
//
//	opereta webadmin                         상태
//	opereta webadmin password                표준 입력에서 새 비밀번호를 읽는다(터미널이면 두 번 묻는다)
//	opereta webadmin on|off [--port N] [--from lan|any|CIDR]
func (c *cli) webadmin(args []string) int {
	if len(args) > 0 && args[0] == "password" {
		return c.webadminPassword(args[1:])
	}
	fs := newFlags("webadmin")
	port := fs.Int("port", 0, "HTTPS port (default 8443)")
	from := fs.String("from", "", "who may connect: lan (default), any, or an address range such as 192.168.1.0/24")
	pos, code, ok := c.parse(fs, "webadmin", args)
	if !ok {
		return code
	}
	if len(pos) > 1 || (len(pos) == 1 && pos[0] != "on" && pos[0] != "off" && pos[0] != "status") {
		return c.usage("usage: opereta webadmin [status | on|off [--port N] [--from lan|any|CIDR] | password]", "webadmin")
	}
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	var (
		st struct {
			Enabled, Running, PasswordSet bool
			Port                          int
			Source, Fingerprint, Error    string
			URLs                          []string
		}
		raw []byte
	)
	if len(pos) == 0 || pos[0] == "status" {
		r, b, err := c.api.WebAdmin(ctx)
		if err != nil {
			return c.fail(err)
		}
		st.Enabled, st.Running, st.PasswordSet, st.Port, st.Source, st.Fingerprint, st.Error, st.URLs = r.Enabled, r.Running, r.PasswordSet, r.Port, r.Source, r.CertFingerprint, r.Error, r.URLs
		raw = b
	} else {
		on := pos[0] == "on"
		var p *int
		if *port != 0 {
			p = port
		}
		var src *string
		if *from != "" {
			src = from
		}
		r, b, err := c.api.SetWebAdmin(ctx, &on, p, src)
		if err != nil {
			return c.fail(err)
		}
		st.Enabled, st.Running, st.PasswordSet, st.Port, st.Source, st.Fingerprint, st.Error, st.URLs = r.Enabled, r.Running, r.PasswordSet, r.Port, r.Source, r.CertFingerprint, r.Error, r.URLs
		raw = b
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	kv(w, "web admin", yesNo(st.Enabled))
	kv(w, "running", yesNo(st.Running))
	kv(w, "password set", yesNo(st.PasswordSet))
	kv(w, "port", fmt.Sprintf("%d/tcp (HTTPS)", st.Port))
	kv(w, "allowed from", st.Source)
	if st.Fingerprint != "" {
		kv(w, "certificate", st.Fingerprint)
	}
	for _, u := range st.URLs {
		kv(w, "address", u)
	}
	if st.Error != "" {
		fmt.Fprintf(w, "error: %s\n", st.Error)
	}
	if st.Enabled {
		fmt.Fprintln(w, "The page uses a self-signed certificate; compare the certificate fingerprint when the browser warns.")
	}
	return exitOK
}

func (c *cli) webadminPassword(args []string) int {
	if len(args) != 0 {
		return c.usage("usage: opereta webadmin password", "webadmin")
	}
	var pw string
	if c.env.stdinTTY {
		fmt.Fprint(c.env.stderr, "New web admin password (at least 10 characters): ")
		line, err := readLine(c.env.stdin)
		if err != nil {
			c.errorLine("cannot read the password")
			return exitFailure
		}
		pw = line
		fmt.Fprint(c.env.stderr, "Again: ")
		again, err := readLine(c.env.stdin)
		if err != nil || again != pw {
			c.errorLine("the two passwords differ")
			return exitFailure
		}
	} else {
		line, err := readLine(c.env.stdin)
		if err != nil {
			c.errorLine("cannot read the password from standard input")
			return exitFailure
		}
		pw = line
	}
	pw = strings.TrimRight(pw, "\r\n")
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	raw, err := c.api.SetWebAdminPassword(ctx, pw)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintln(c.env.stdout, "web admin password set; all web sessions were ended")
	return exitOK
}
