package main

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/nxodh/lightlixos/daemon/internal/client"
)

// user 는 user 명령 묶음이다.
func (c *cli) user(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: lightlix user <add|list|rm>", "user")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "add":
		return c.userAdd(rest)
	case "list":
		return c.userList(rest)
	case "rm":
		return c.userRemove(rest)
	}
	return c.usage(fmt.Sprintf("unknown user command %q", sub), "user")
}

// userAdd 는 계정을 만든다. 비밀번호는 표준 입력이나 터미널에서만 받고 출력에 나오지 않는다.
func (c *cli) userAdd(args []string) int {
	fs := newFlags("user add")
	admin := fs.Bool("admin", false, "add the account to the sudo group")
	fromStdin := fs.Bool("password-stdin", false, "read the password from standard input")
	pos, code, ok := c.parse(fs, "user add", args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.usage("usage: lightlix user add <name> [--admin] [--password-stdin]", "user add")
	}
	name := pos[0]

	var password string
	switch {
	case *fromStdin:
		line, err := readLine(c.env.stdin)
		if err != nil {
			c.errorLine("cannot read the password from standard input")
			return exitFailure
		}
		password = line
	case c.env.stdinTTY || c.env.readPassword != nil:
		first, err := c.promptPassword("Password: ")
		if err != nil {
			c.errorLine("cannot read the password: " + err.Error())
			return exitFailure
		}
		second, err := c.promptPassword("Repeat the password: ")
		if err != nil {
			c.errorLine("cannot read the password: " + err.Error())
			return exitFailure
		}
		if first != second {
			c.errorLine("the passwords do not match")
			return exitFailure
		}
		password = first
	default:
		return c.usage("standard input is not a terminal; pass the password with --password-stdin", "user add")
	}
	if password == "" {
		return c.usage("the password is empty", "user add")
	}

	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	res, raw, err := c.api.CreateUser(ctx, client.UserCreateRequest{Name: name, Password: password, Admin: *admin})
	password = ""
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	kind := "standard"
	if res.Admin {
		kind = "administrator (sudo)"
	}
	fmt.Fprintf(c.env.stdout, "created account %s (%s)\n", res.Name, kind)
	fmt.Fprintln(c.env.stdout, "Log in with it at the console or the login screen. Turn SSH on to log in over the network.")
	return exitOK
}

// promptPassword 는 에코 없이 비밀번호 한 줄을 읽는다.
func (c *cli) promptPassword(prompt string) (string, error) {
	if c.env.readPassword != nil {
		return c.env.readPassword(prompt)
	}
	f, ok := c.env.stdin.(*os.File)
	if !ok {
		return "", errors.New("standard input is not a terminal")
	}
	fmt.Fprint(c.env.stderr, prompt)
	s, err := readSecret(f)
	fmt.Fprintln(c.env.stderr)
	return s, err
}

// userList 는 Lightlix 로 만든 계정을 보여 준다.
func (c *cli) userList(args []string) int {
	if len(args) != 0 {
		return c.usage("user list takes no arguments", "user list")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	list, raw, err := c.api.Users(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if len(list.Users) == 0 {
		fmt.Fprintln(c.env.stdout, "no accounts; create one with: lightlix user add <name>")
		return exitOK
	}
	rows := make([][]string, 0, len(list.Users))
	for _, u := range list.Users {
		rows = append(rows, []string{u.Name, yesNo(u.Admin), orDash(u.Created)})
	}
	writeTable(c.env.stdout, []string{"NAME", "ADMIN", "CREATED"}, rows)
	return exitOK
}

// userRemove 는 계정을 지운다. 홈 디렉터리는 남는다.
func (c *cli) userRemove(args []string) int {
	if len(args) != 1 {
		return c.usage("usage: lightlix user rm <name>", "user rm")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	res, raw, err := c.api.DeleteUser(ctx, args[0])
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "deleted account %s; its home directory was kept\n", res.Name)
	return exitOK
}

// ssh 는 SSH 서버 상태를 보여 주거나 켜고 끈다.
func (c *cli) ssh(args []string) int {
	if len(args) > 1 {
		return c.usage("usage: lightlix ssh [on|off]", "ssh")
	}
	var (
		res client.SSH
		raw []byte
		err error
	)
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	switch {
	case len(args) == 0:
		res, raw, err = c.api.SSH(ctx)
	case args[0] == "on" || args[0] == "off":
		res, raw, err = c.api.SetSSH(ctx, args[0] == "on")
	default:
		return c.usage("usage: lightlix ssh [on|off]", "ssh")
	}
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	kv(c.env.stdout, "ssh", yesNo(res.Enabled))
	kv(c.env.stdout, "installed", yesNo(res.Installed))
	kv(c.env.stdout, "running", yesNo(res.Active))
	kv(c.env.stdout, "password login", orDash(res.PasswordAuth))
	users := "-"
	if len(res.LoginUsers) > 0 {
		users = strings.Join(res.LoginUsers, ", ")
	}
	kv(c.env.stdout, "login accounts", users)
	if res.Warning != "" {
		fmt.Fprintf(c.env.stdout, "note: %s\n", res.Warning)
	}
	return exitOK
}
