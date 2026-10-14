package main

import (
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
)

// sshAdvanced 는 SSH 포트, 비밀번호 로그인, 공개 키, 접속 기록을 다룬다.
func (c *cli) sshAdvanced(sub string, args []string) int {
	switch sub {
	case "advanced":
		return c.sshAdvancedShow()
	case "port":
		if len(args) != 1 {
			return c.usage("usage: opereta ssh port <1-65535>", "ssh port")
		}
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > 65535 {
			return c.usage("the port must be a number from 1 to 65535", "ssh port")
		}
		return c.sshSet(&n, nil, fmt.Sprintf("SSH port set to %d (the firewall follows)", n))
	case "password":
		if len(args) != 1 || (args[0] != "on" && args[0] != "off") {
			return c.usage("usage: opereta ssh password on|off", "ssh password")
		}
		v := args[0] == "on"
		return c.sshSet(nil, &v, "password login "+args[0])
	case "keys":
		return c.sshKeys(args)
	case "log":
		return c.sshLog()
	}
	return c.usage("unknown ssh command", "ssh")
}

func (c *cli) sshSet(port *int, password *bool, done string) int {
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	_, raw, err := c.api.SetSSHAdvanced(ctx, port, password)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintln(c.env.stdout, done)
	return exitOK
}

func (c *cli) sshAdvancedShow() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	a, raw, err := c.api.SSHAdvanced(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	kv(w, "ssh", yesNo(a.Enabled))
	kv(w, "port", strconv.Itoa(a.Port))
	pw := "image default"
	if a.PasswordLoginConfigured != nil {
		pw = "on"
		if !*a.PasswordLoginConfigured {
			pw = "off"
		}
	}
	kv(w, "password login", pw)
	kv(w, "public keys", strconv.Itoa(a.KeyCount))
	for _, k := range a.HostKeys {
		kv(w, "host key", k.Type+" "+k.Fingerprint)
	}
	return exitOK
}

// sshKeys 는 공개 키를 보이고 더하고 지운다.
//
//	opereta ssh keys list <user>
//	opereta ssh keys add <user> [--file F]   (공개 키 한 줄을 파일이나 표준 입력에서 읽는다)
//	opereta ssh keys rm <user> <fingerprint>
func (c *cli) sshKeys(args []string) int {
	if len(args) < 2 {
		return c.usage("usage: opereta ssh keys <list|add|rm> <user> ...", "ssh keys")
	}
	sub, user, rest := args[0], args[1], args[2:]
	switch sub {
	case "list":
		ctx, cancel := c.timeout(metaTimeout)
		defer cancel()
		keys, raw, err := c.api.SSHKeys(ctx, user)
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		if len(keys) == 0 {
			fmt.Fprintf(c.env.stdout, "%s has no public keys; add one with: opereta ssh keys add %s < key.pub\n", user, user)
			return exitOK
		}
		rows := make([][]string, 0, len(keys))
		for _, k := range keys {
			rows = append(rows, []string{k.Type, k.Fingerprint, orDash(k.Comment)})
		}
		writeTable(c.env.stdout, []string{"TYPE", "FINGERPRINT", "COMMENT"}, rows)
		return exitOK
	case "add":
		fs := newFlags("ssh keys add")
		file := fs.String("file", "", "read the public key from this file instead of standard input")
		pos, code, ok := c.parse(fs, "ssh keys add", rest)
		if !ok {
			return code
		}
		if len(pos) != 0 {
			return c.usage("usage: opereta ssh keys add <user> [--file F]", "ssh keys add")
		}
		var r io.Reader = c.env.stdin
		if *file != "" {
			f, err := os.Open(*file)
			if err != nil {
				c.errorLine(err.Error())
				return exitFailure
			}
			defer f.Close()
			r = f
		}
		line, err := readLine(r)
		if err != nil || strings.TrimSpace(line) == "" {
			return c.usage("no public key on standard input; use: opereta ssh keys add <user> < key.pub", "ssh keys add")
		}
		ctx, cancel := c.timeout(metaTimeout)
		defer cancel()
		raw, err := c.api.AddSSHKey(ctx, user, line)
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		fmt.Fprintf(c.env.stdout, "key added for %s\n", user)
		return exitOK
	case "rm":
		if len(rest) != 1 {
			return c.usage("usage: opereta ssh keys rm <user> <fingerprint>", "ssh keys rm")
		}
		ctx, cancel := c.timeout(metaTimeout)
		defer cancel()
		raw, err := c.api.RemoveSSHKey(ctx, user, rest[0])
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		fmt.Fprintf(c.env.stdout, "key removed from %s\n", user)
		return exitOK
	}
	return c.usage(fmt.Sprintf("unknown ssh keys command %q", sub), "ssh keys")
}

func (c *cli) sshLog() int {
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	ev, raw, err := c.api.SSHLog(ctx, 50)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if len(ev) == 0 {
		fmt.Fprintln(c.env.stdout, "no SSH logins in the recent log")
		return exitOK
	}
	rows := make([][]string, 0, len(ev))
	for _, e := range ev {
		rows = append(rows, []string{e.Time, e.Result, e.User, e.Method, e.From})
	}
	writeTable(c.env.stdout, []string{"TIME", "RESULT", "USER", "METHOD", "FROM"}, rows)
	return exitOK
}
