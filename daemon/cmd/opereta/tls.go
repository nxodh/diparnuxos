package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/client"
)

const tlsUsage = "usage: opereta tls [status | on [--port N] | off --yes | ca [--out FILE] | fingerprint | renew [--ca --yes] | plain on|off --yes | names [NAME...]]"

// tls 는 게이트웨이의 HTTPS(TLS)를 보이고 켜고 끈다.
//
//	opereta tls                       상태
//	opereta tls on [--port N]         CA 와 서버 인증서를 만들고 TLS 포트에서 듣는다(LAN 평문은 닫힌다)
//	opereta tls off --yes             끈다(LAN 평문이 다시 열린다)
//	opereta tls ca [--out FILE]       다른 컴퓨터에 배포할 CA 인증서(공개 값)
//	opereta tls fingerprint           CA 의 SHA-256 지문
//	opereta tls renew [--ca --yes]    서버 인증서(또는 CA 까지)를 다시 만든다
//	opereta tls plain on|off --yes    TLS 를 켠 뒤에도 평문을 LAN 에 열어 둘지
//	opereta tls names [NAME...]       인증서에 더 넣을 이름(CA 를 만들 때 포함된 이름만 들어간다)
func (c *cli) tls(args []string) int {
	fs := newFlags("tls")
	port := fs.Int("port", 0, "TLS port (default 8444)")
	out := fs.String("out", "", "write the CA certificate to this file instead of standard output")
	yes := fs.Bool("yes", false, "confirm a change that weakens or invalidates something")
	ca := fs.Bool("ca", false, "renew the CA as well (clients must receive the new CA)")
	pos, code, ok := c.parse(fs, "tls", args)
	if !ok {
		return code
	}
	sub := "status"
	if len(pos) > 0 {
		sub = pos[0]
	}
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()

	show := func(st client.TLS, raw []byte, err error) int {
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		c.printTLS(st)
		return exitOK
	}

	switch sub {
	case "status":
		if len(pos) > 1 {
			return c.usage(tlsUsage, "tls")
		}
		st, raw, err := c.api.TLS(ctx)
		return show(st, raw, err)
	case "on":
		if len(pos) != 1 {
			return c.usage(tlsUsage, "tls")
		}
		on := true
		patch := client.TLSPatch{Enabled: &on}
		if *port != 0 {
			patch.Port = port
		}
		st, raw, err := c.api.SetTLS(ctx, patch)
		return show(st, raw, err)
	case "off":
		if len(pos) != 1 {
			return c.usage(tlsUsage, "tls")
		}
		if !*yes {
			c.errorLine("turning TLS off opens plain HTTP on the local network again, so keys and prompts are no longer encrypted; run 'opereta tls off --yes' to confirm")
			return exitUsage
		}
		off := false
		st, raw, err := c.api.SetTLS(ctx, client.TLSPatch{Enabled: &off})
		return show(st, raw, err)
	case "plain":
		if len(pos) != 2 || (pos[1] != "on" && pos[1] != "off") {
			return c.usage(tlsUsage, "tls")
		}
		on := pos[1] == "on"
		if on && !*yes {
			c.errorLine("plain HTTP on the local network is not encrypted; run 'opereta tls plain on --yes' to keep it open")
			return exitUsage
		}
		st, raw, err := c.api.SetTLS(ctx, client.TLSPatch{LANPlain: &on})
		return show(st, raw, err)
	case "names":
		names := append([]string{}, pos[1:]...)
		if len(pos) == 1 {
			st, raw, err := c.api.TLS(ctx)
			if err != nil {
				return c.fail(err)
			}
			if c.asJSON {
				return c.printJSON(raw)
			}
			fmt.Fprintf(c.env.stdout, "extra names: %s\n", orDash(strings.Join(st.Names, ", ")))
			return exitOK
		}
		if len(names) == 1 && names[0] == "none" {
			names = []string{}
		}
		st, raw, err := c.api.SetTLS(ctx, client.TLSPatch{Names: &names})
		return show(st, raw, err)
	case "ca":
		if len(pos) != 1 {
			return c.usage(tlsUsage, "tls")
		}
		pem, err := c.api.TLSCA(ctx)
		if err != nil {
			return c.fail(err)
		}
		if *out == "" {
			_, _ = c.env.stdout.Write(pem)
			return exitOK
		}
		if err := os.WriteFile(*out, pem, 0o644); err != nil {
			c.errorLine(err.Error())
			return exitFailure
		}
		st, _, err := c.api.TLS(ctx)
		fmt.Fprintf(c.env.stdout, "wrote the CA certificate to %s\n", *out)
		if err == nil && st.CAFingerprint != "" {
			kv(c.env.stdout, "fingerprint", st.CAFingerprint)
			fmt.Fprintln(c.env.stdout, "Copy the file to the other computer and compare its fingerprint with this one before trusting it.")
		}
		return exitOK
	case "fingerprint":
		if len(pos) != 1 {
			return c.usage(tlsUsage, "tls")
		}
		st, raw, err := c.api.TLS(ctx)
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		if st.CAFingerprint == "" {
			c.errorLine("there is no CA yet: opereta tls on")
			return exitFailure
		}
		fmt.Fprintln(c.env.stdout, st.CAFingerprint)
		return exitOK
	case "renew":
		if len(pos) != 1 {
			return c.usage(tlsUsage, "tls")
		}
		if *ca && !*yes {
			c.errorLine("renewing the CA makes every client's copy useless; run 'opereta tls renew --ca --yes' to confirm, then give clients the new CA")
			return exitUsage
		}
		st, raw, err := c.api.RenewTLS(ctx, *ca)
		return show(st, raw, err)
	}
	return c.usage(tlsUsage, "tls")
}

func (c *cli) printTLS(st client.TLS) {
	w := c.env.stdout
	kv(w, "tls", yesNo(st.Enabled))
	kv(w, "active", yesNo(st.Active))
	kv(w, "port", fmt.Sprintf("%d/tcp (HTTPS)", st.Port))
	kv(w, "plain port", fmt.Sprintf("%d/tcp (HTTP, this computer%s)", st.PlainPort, map[bool]string{true: " and the local network", false: " only"}[st.PlainLANOpen]))
	kv(w, "listening", yesNo(st.Listening))
	if st.ListenerError != "" {
		kv(w, "listener error", st.ListenerError)
	}
	if st.CAFingerprint != "" {
		kv(w, "CA fingerprint", st.CAFingerprint)
		kv(w, "CA valid until", st.CANotAfter)
		kv(w, "server certificate", st.ServerFingerprint)
		kv(w, "valid until", st.ServerNotAfter)
		kv(w, "names", strings.Join(append(append([]string{}, st.DNS...), st.IPs...), ", "))
	}
	if len(st.Names) > 0 {
		kv(w, "extra names", strings.Join(st.Names, ", "))
	}
	for _, n := range st.Notes {
		fmt.Fprintf(w, "note: %s\n", n)
	}
}
