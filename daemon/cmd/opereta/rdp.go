package main

import "fmt"

// remoteDesktop 은 원격 데스크톱(RDP)을 보이거나 켜고 끈다.
func (c *cli) remoteDesktop(args []string) int {
	fs := newFlags("remote-desktop")
	from := fs.String("from", "", "who may connect: lan (default), any, or an address range such as 192.168.1.0/24")
	pos, code, ok := c.parse(fs, "remote-desktop", args)
	if !ok {
		return code
	}
	if len(pos) > 1 || (len(pos) == 1 && pos[0] != "on" && pos[0] != "off") {
		return c.usage("usage: opereta remote-desktop [on|off] [--from lan|any|CIDR]", "remote-desktop")
	}
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	var (
		st = struct {
			Installed, Enabled, Active bool
			Port                       int
			Source, Note               string
		}{}
		raw []byte
	)
	if len(pos) == 0 {
		r, b, err := c.api.RemoteDesktop(ctx)
		if err != nil {
			return c.fail(err)
		}
		st.Installed, st.Enabled, st.Active, st.Port, st.Source, st.Note = r.Installed, r.Enabled, r.Active, r.Port, r.Source, r.Note
		raw = b
	} else {
		r, b, err := c.api.SetRemoteDesktop(ctx, pos[0] == "on", *from)
		if err != nil {
			return c.fail(err)
		}
		st.Installed, st.Enabled, st.Active, st.Port, st.Source, st.Note = r.Installed, r.Enabled, r.Active, r.Port, r.Source, r.Note
		raw = b
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	kv(w, "remote desktop", yesNo(st.Enabled))
	kv(w, "installed", yesNo(st.Installed))
	kv(w, "running", yesNo(st.Active))
	kv(w, "port", fmt.Sprintf("%d/tcp", st.Port))
	kv(w, "allowed from", st.Source)
	if st.Note != "" {
		fmt.Fprintf(w, "note: %s\n", st.Note)
	}
	return exitOK
}
