package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/client"
)

// mode 는 GET 이나 POST /v1/mode 를 부른다.
func (c *cli) mode(args []string) int {
	if len(args) == 0 {
		ctx, cancel := c.timeout(shortTimeout)
		defer cancel()
		res, raw, err := c.api.Mode(ctx)
		if err != nil {
			return c.fail(err)
		}
		if c.asJSON {
			return c.printJSON(raw)
		}
		kv(c.env.stdout, "mode", orDash(res.Mode))
		return exitOK
	}
	if len(args) != 1 {
		return c.usage("usage: diparnux mode [server|desktop]", "mode")
	}
	want := args[0]
	if want != "server" && want != "desktop" {
		return c.usage("mode must be server or desktop", "mode")
	}
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	res, raw, err := c.api.SetMode(ctx, want)
	if err != nil {
		var ae *client.APIError
		if errors.As(err, &ae) && ae.Code == "desktop_not_installed" {
			c.errorLine("desktop mode is not installed on this system")
			return exitFailure
		}
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "mode set to %s\n", res.Mode)
	return exitOK
}

// report 는 진단 리포트를 표준 출력이나 파일로 쓴다. 파일은 모드 0600 이다.
func (c *cli) report(args []string) int {
	fs := newFlags("report")
	out := fs.String("o", "", "write the report to FILE (mode 0600)")
	pos, code, ok := c.parse(fs, "report", args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return c.usage("report takes no positional arguments", "report")
	}
	ctx, cancel := c.timeout(reportTimeout)
	defer cancel()
	text, err := c.api.Report(ctx)
	if err != nil {
		return c.fail(err)
	}
	if *out == "" {
		if _, err := io.WriteString(c.env.stdout, text); err != nil {
			return exitFailure
		}
		return exitOK
	}
	if err := writeSecretFile(*out, text); err != nil {
		c.errorLine(fmt.Sprintf("cannot write the report: %v", err))
		return exitFailure
	}
	fmt.Fprintf(c.env.stdout, "report written to %s (mode 0600)\n", *out)
	return exitOK
}

// writeSecretFile 은 파일을 모드 0600 으로 새로 쓴다. 기존 파일이 있어도 모드를 0600 으로 맞춘다.
func writeSecretFile(path, text string) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := io.WriteString(f, text); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// apikey 는 apikey 명령 묶음이다.
func (c *cli) apikey(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: diparnux apikey <create|list|rm>", "apikey")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "create":
		return c.apikeyCreate(rest)
	case "list":
		return c.apikeyList(rest)
	case "rm":
		return c.apikeyRemove(rest)
	}
	return c.usage(fmt.Sprintf("unknown apikey command %q", sub), "apikey")
}

// apikeyCreate 는 키를 만든다. 평문 키는 이 출력에서 한 번만 보인다.
func (c *cli) apikeyCreate(args []string) int {
	if len(args) > 1 {
		return c.usage("usage: diparnux apikey create [label]", "apikey create")
	}
	label := "cli"
	if len(args) == 1 {
		label = args[0]
	}
	if !validLabel(label) {
		return c.usage("label must be 1-64 printable ASCII characters", "apikey create")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	res, raw, err := c.api.CreateAPIKey(ctx, label)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintln(c.env.stdout, "API key created.")
	kv(c.env.stdout, "  id", res.ID)
	kv(c.env.stdout, "  label", res.Label)
	kv(c.env.stdout, "  prefix", res.Prefix)
	kv(c.env.stdout, "  key", res.Key)
	fmt.Fprintln(c.env.stdout, "The key is shown only once. Store it now.")
	if res.RestartRequired {
		fmt.Fprintln(c.env.stdout, "restart the server for the new API key to take effect: diparnux serve stop, then diparnux serve start")
	}
	return exitOK
}

// validLabel 은 키 라벨이 1-64 자의 출력 가능한 ASCII 인지 본다. 데몬의 규칙과 같다.
func validLabel(label string) bool {
	if len(label) == 0 || len(label) > 64 {
		return false
	}
	for i := 0; i < len(label); i++ {
		if ch := label[i]; ch < 0x20 || ch > 0x7e {
			return false
		}
	}
	return true
}

// apikeyList 는 키 목록을 보여 준다. 평문 키는 없다.
func (c *cli) apikeyList(args []string) int {
	if len(args) != 0 {
		return c.usage("apikey list takes no arguments", "apikey list")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	list, raw, err := c.api.APIKeys(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if len(list.Keys) == 0 {
		fmt.Fprintln(c.env.stdout, "no API keys; create one with: diparnux apikey create")
		return exitOK
	}
	rows := make([][]string, 0, len(list.Keys))
	for _, k := range list.Keys {
		rows = append(rows, []string{k.ID, orDash(k.Label), orDash(k.Prefix), orDash(k.Created)})
	}
	writeTable(c.env.stdout, []string{"ID", "LABEL", "PREFIX", "CREATED"}, rows)
	return exitOK
}

// apikeyRemove 는 키를 지운다.
func (c *cli) apikeyRemove(args []string) int {
	if len(args) != 1 {
		return c.usage("usage: diparnux apikey rm <id>", "apikey rm")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	res, raw, err := c.api.DeleteAPIKey(ctx, args[0])
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "deleted API key %s\n", res.ID)
	if res.RestartRequired {
		fmt.Fprintln(c.env.stdout, "restart the server for the change to take effect: diparnux serve stop, then diparnux serve start")
	}
	return exitOK
}

// config 는 config 명령 묶음이다.
func (c *cli) config(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: diparnux config <show|set>", "config")
	}
	switch args[0] {
	case "show":
		return c.configShow(args[1:])
	case "set":
		return c.configSet(args[1:])
	}
	return c.usage(fmt.Sprintf("unknown config command %q", args[0]), "config")
}

// configShow 는 설정을 보여 준다. hf-token 은 값을 빼고 설정 여부만 보인다.
func (c *cli) configShow(args []string) int {
	if len(args) != 0 {
		return c.usage("config show takes no arguments", "config show")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	cfg, raw, err := c.api.Config(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	kv(c.env.stdout, "port", strconv.Itoa(cfg.Port))
	kv(c.env.stdout, "lan", yesNo(cfg.LANExposed))
	kv(c.env.stdout, "autostart", yesNo(cfg.Autostart))
	kv(c.env.stdout, "models dir", cfg.ModelsDir)
	token := "not set"
	if cfg.HFToken != "" {
		token = "set (hidden)"
	}
	kv(c.env.stdout, "hf-token", token)
	kv(c.env.stdout, "auto-restart", yesNo(cfg.AutoRestart))
	kv(c.env.stdout, "gateway", yesNo(cfg.Gateway))
	kv(c.env.stdout, "overrides", overridesText(cfg.Overrides))
	return exitOK
}

// overridesText 는 설정 재정의를 한 줄로 쓴다. 없으면 자동 계획이다.
func overridesText(o client.Overrides) string {
	var parts []string
	if o.NGL != nil {
		parts = append(parts, "ngl="+*o.NGL)
	}
	if o.Ctx != nil {
		parts = append(parts, fmt.Sprintf("ctx=%d", *o.Ctx))
	}
	if o.CacheType != nil {
		parts = append(parts, "cache-type="+*o.CacheType)
	}
	if o.Batch != nil {
		parts = append(parts, fmt.Sprintf("batch=%d", *o.Batch))
	}
	if o.LoadMode != nil {
		parts = append(parts, "load-mode="+*o.LoadMode)
	}
	if o.Parallel != nil {
		parts = append(parts, fmt.Sprintf("parallel=%d", *o.Parallel))
	}
	if len(parts) == 0 {
		return "none (automatic plan)"
	}
	return strings.Join(parts, " ")
}

// configSet 는 설정 한 항목을 바꾼다. hf-token 은 인자가 아니라 표준 입력에서 읽는다.
func (c *cli) configSet(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: diparnux config set <port|lan|autostart|auto-restart|gateway|parallel|hf-token> [value]", "config set")
	}
	key, rest := args[0], args[1:]
	switch key {
	case "port":
		if len(rest) != 1 {
			return c.usage("usage: diparnux config set port <N>", "config set")
		}
		n, err := strconv.Atoi(rest[0])
		if err != nil || n < 1 || n > 65535 {
			return c.usage("port must be a number from 1 to 65535", "config set")
		}
		return c.applyConfig(key, client.ConfigPatch{Port: &n}, fmt.Sprintf("port set to %d", n))
	case "lan", "autostart", "auto-restart", "gateway":
		if len(rest) != 1 || (rest[0] != "on" && rest[0] != "off") {
			return c.usage(fmt.Sprintf("usage: diparnux config set %s on|off", key), "config set")
		}
		v := rest[0] == "on"
		patch := client.ConfigPatch{LANExposed: &v}
		switch key {
		case "autostart":
			patch = client.ConfigPatch{Autostart: &v}
		case "gateway":
			patch = client.ConfigPatch{Gateway: &v}
		case "auto-restart":
			patch = client.ConfigPatch{AutoRestart: &v}
		}
		return c.applyConfig(key, patch, fmt.Sprintf("%s %s", key, rest[0]))
	case "parallel":
		return c.setParallel(rest)
	case "hf-token":
		if len(rest) != 0 {
			return c.usage("hf-token takes no argument; the token is read from standard input", "config set")
		}
		return c.setHFToken()
	}
	return c.usage(fmt.Sprintf("unknown setting %q", key), "config set")
}

// setParallel 은 동시에 처리할 요청 수(슬롯)를 저장한다. auto 는 지정을 지운다. 다른 재정의는 그대로 둔다.
func (c *cli) setParallel(args []string) int {
	const use = "usage: diparnux config set parallel <1-16|auto>"
	if len(args) != 1 {
		return c.usage(use, "config set")
	}
	var want *int
	if args[0] != "auto" {
		n, err := strconv.Atoi(args[0])
		if err != nil || n < 1 || n > 16 {
			return c.usage(use, "config set")
		}
		want = &n
	}
	ctx, cancel := c.timeout(shortTimeout)
	cur, _, err := c.api.Config(ctx)
	cancel()
	if err != nil {
		return c.fail(err)
	}
	ov := cur.Overrides
	ov.Parallel = want
	text := "parallel set to " + args[0]
	if want == nil {
		text = "parallel set to automatic (one slot)"
	}
	return c.applyConfig("parallel", client.ConfigPatch{Overrides: &ov}, text)
}

// setHFToken 은 표준 입력에서 토큰을 한 줄 읽어 저장한다. 토큰은 어떤 출력에도 나오지 않는다.
func (c *cli) setHFToken() int {
	if c.env.stdinTTY {
		fmt.Fprintln(c.env.stderr, "Paste the Hugging Face token and press Enter. The token is shown on screen.")
	}
	token, err := readLine(c.env.stdin)
	if err != nil {
		c.errorLine("cannot read the token from standard input")
		return exitFailure
	}
	token = strings.TrimSpace(token)
	if token == "" {
		return c.usage("no token on standard input", "config set")
	}
	if !validToken(token) {
		return c.usage("the token must be printable ASCII without spaces, at most 256 bytes", "config set")
	}
	return c.applyConfig("hf-token", client.ConfigPatch{HFToken: &token}, "hf-token saved")
}

// validToken 은 토큰이 출력 가능한 ASCII 이고 공백이 없으며 256바이트 이하인지 본다.
func validToken(t string) bool {
	if len(t) == 0 || len(t) > 256 {
		return false
	}
	for i := 0; i < len(t); i++ {
		if ch := t[i]; ch <= 0x20 || ch >= 0x7f {
			return false
		}
	}
	return true
}

// applyConfig 는 PUT /v1/config 를 부르고 결과를 보여 준다.
func (c *cli) applyConfig(key string, patch client.ConfigPatch, done string) int {
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	res, raw, err := c.api.SetConfig(ctx, patch)
	if err != nil {
		return c.configFailure(key, err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintln(c.env.stdout, done)
	if res.RestartRequired {
		if key == "hf-token" {
			fmt.Fprintln(c.env.stdout, "restart the daemon to use the new Hugging Face token: sudo systemctl restart diparnuxd")
		} else {
			fmt.Fprintln(c.env.stdout, "restart the server for the change to take effect: diparnux serve stop, then diparnux serve start")
		}
	}
	return exitOK
}

// configFailure 는 설정 변경 실패를 안내한다. LAN 공개에 키가 없으면 키를 만들라고 안내한다.
func (c *cli) configFailure(key string, err error) int {
	var ae *client.APIError
	if errors.As(err, &ae) && ae.Code == "api_key_required" {
		c.errorLine("LAN exposure needs an API key. Create one first: diparnux apikey create")
		return exitFailure
	}
	return c.fail(err)
}

// chat 은 프롬프트 한 개를 보내고 응답과 속도를 보여 준다.
func (c *cli) chat(args []string) int {
	fs := newFlags("chat")
	maxTokens := fs.Int("max-tokens", 512, fmt.Sprintf("maximum tokens to generate (1-%d)", maxTokensLimit))
	pos, code, ok := c.parse(fs, "chat", args)
	if !ok {
		return code
	}
	if len(pos) == 0 {
		return c.usage("usage: diparnux chat [--max-tokens N] <prompt...>", "chat")
	}
	if *maxTokens < 1 || *maxTokens > maxTokensLimit {
		return c.usage(fmt.Sprintf("--max-tokens must be 1-%d", maxTokensLimit), "chat")
	}
	prompt := strings.Join(pos, " ")
	ctx, cancel := c.timeout(chatTimeout)
	defer cancel()
	reply, raw, err := c.api.Chat(ctx, client.ChatRequest{
		Messages:  []client.ChatMessage{{Role: "user", Content: prompt}},
		MaxTokens: *maxTokens,
	})
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintln(c.env.stdout, reply.Content)
	fmt.Fprintln(c.env.stdout)
	fmt.Fprintf(c.env.stdout, "tokens: %d\n", reply.TokensPredicted)
	fmt.Fprintf(c.env.stdout, "speed: %.2f tokens/s\n", reply.TokensPerSecond)
	return exitOK
}

// version 은 CLI 버전을 보여 준다. 데몬을 부르지 않는다.
func (c *cli) version(args []string) int {
	if len(args) != 0 {
		return c.usage("version takes no arguments", "version")
	}
	if c.asJSON {
		b, err := json.Marshal(map[string]string{"version": version})
		if err != nil {
			return c.fail(err)
		}
		fmt.Fprintf(c.env.stdout, "%s\n", b)
		return exitOK
	}
	fmt.Fprintf(c.env.stdout, "diparnux %s\n", version)
	return exitOK
}

// help 는 전체 도움말이나 명령별 도움말을 보여 준다.
func (c *cli) help(args []string) int {
	if len(args) == 0 {
		writeTopUsage(c.env.stdout)
		return exitOK
	}
	name := strings.Join(args, " ")
	text, ok := commandUsage[name]
	if !ok {
		return c.usage(fmt.Sprintf("no help for %q", name), "")
	}
	fmt.Fprint(c.env.stdout, text)
	return exitOK
}

// commandUsage 는 명령별 도움말이다. 키는 명령 이름을 공백으로 이은 것이다.
var commandUsage = map[string]string{
	"status":               "Usage: diparnux status\n\nShow the version, mode, GPU, memory, and server state.\n",
	"gpu":                  "Usage: diparnux gpu check\n\nRun the GPU check again and say what to do for the result.\n",
	"gpu check":            "Usage: diparnux gpu check\n\nRun the GPU check again. Exit 0 if a GPU is usable or none exists, 1 if a GPU exists but cannot be used.\n",
	"model":                "Usage: diparnux model <list|pull|import|rm|search|files|default> [arguments]\n",
	"model list":           "Usage: diparnux model list\n\nList installed models.\n",
	"model pull":           "Usage: diparnux model pull <repo> [--file F] [--revision R] [--no-wait]\n\nShow the repository license and gated state, start a download, and follow it.\nThe same command followed again picks up the running download.\n",
	"model import":         "Usage: diparnux model import <file.gguf> [--name NAME]\n\nCopy a GGUF file you already have into the model directory. The file is read with your permissions and sent to the daemon.\nThe SHA-256 is computed while copying and the GGUF header is checked. An existing model is never replaced.\nThe first model you add becomes the default.\n",
	"model rm":             "Usage: diparnux model rm <id>\n\nDelete an installed model.\n",
	"model search":         "Usage: diparnux model search <query> [--limit N]\n\nSearch Hugging Face for GGUF models.\n",
	"model files":          "Usage: diparnux model files <repo> [--revision R]\n\nList the GGUF files in a repository.\n",
	"model default":        "Usage: diparnux model default <id>\n\nSet the default model.\n",
	"power":                "Usage: diparnux power [on|off]\n\nShow whether the computer is kept always on (no suspend when the lid is closed or after a long idle time), or turn that on or off.\nShutting down and rebooting yourself (power button, poweroff, the menu) is never blocked.\nAlways-on is the default. The status asks the running systemd-logind, so another setting that overrides it is reported.\n",
	"connection":           "Usage: diparnux connection [examples]\n\nShow the URLs other computers use to reach this computer's model server (OpenAI-compatible API), whether an API key is required, and warnings.\n'examples' prints ready-made settings for curl, the OpenAI Python client and environment variables.\nThe API key itself is never shown here; it is shown only once when it is created.\n",
	"gateway":              "Usage: diparnux gateway\n\nShow the gateway that checks API keys, with requests, errors and tokens per key.\n",
	"serve":                "Usage: diparnux serve <start|stop|plan|logs|stats> [arguments]\n",
	"network":              "Usage: diparnux network [status|config|set|confirm|revert|hostname|wol|diagnose|wifi ...]\n\n  status                       interfaces, addresses and Wi-Fi\n  config                       the wired settings, host name and wake-on-lan\n  set <iface> --dhcp | --address CIDR [--gateway IP] [--dns A,B] [--domains X] [--confirm SECONDS]\n                               change a wired interface; unless you confirm in time it reverts\n  confirm | revert             keep or undo the change that is waiting\n  hostname [name]              show or change the host name\n  wol [on|off]                 show or change wake-on-lan (applies from the next boot)\n  diagnose                     check the address, route, gateway, DNS and internet\n",
	"firewall":             "Usage: diparnux firewall [list|add|rm|enable|disable]\n\n  list                                 the ports features open and your rules (the default is to block)\n  add <name> <tcp|udp> <port|a-b> [--from any|lan|CIDR]\n  rm <id> | enable <id> | disable <id>\n",
	"ssh keys":             "Usage: diparnux ssh keys list <user> | add <user> [--file F] | rm <user> <fingerprint>\n\nManage the public keys that may log in as a user. 'add' reads one public key line from standard input or --file.\n",
	"serve stats":          "Usage: diparnux serve stats\n\nShow the running server: busy slots, generation and prompt speed, requests being processed, and the gateway counters.\n",
	"serve start":          "Usage: diparnux serve start [model] [--ngl V] [--ctx N] [--cache-type T] [--batch N] [--load-mode M] [--parallel N] [--no-wait]\n\nShow the plan with its rationale, then start llama-server and wait for it to be ready.\n",
	"serve stop":           "Usage: diparnux serve stop\n\nStop llama-server.\n",
	"serve plan":           "Usage: diparnux serve plan [model] [--ngl V] [--ctx N] [--cache-type T] [--batch N] [--load-mode M] [--parallel N]\n\nShow the plan and its rationale without starting the server.\n",
	"serve logs":           "Usage: diparnux serve logs [-n N]\n\nShow the last N llama-server log lines (1-2000, default 200).\n",
	"mode":                 "Usage: diparnux mode [server|desktop]\n\nShow the current mode, or set it.\n",
	"update":               "Usage: diparnux update [--check] [--rollback] [--status] [--reboot]\n\nInstall the kernel, NVIDIA driver and system updates together. The new kernel boots once and becomes the default only after it passes the boot check.\n\n  --check     show what would change and install nothing\n  --rollback  return to the kernel and driver from before the last update\n  --status    show the state of the last update\n  --reboot    reboot when the update succeeds\n",
	"report":               "Usage: diparnux report [-o FILE]\n\nWrite the diagnostic report to standard output, or to FILE (mode 0600).\n",
	"apikey":               "Usage: diparnux apikey <create|list|rm> [arguments]\n",
	"apikey create":        "Usage: diparnux apikey create [label]\n\nCreate an API key. The key is shown only once.\n",
	"apikey list":          "Usage: diparnux apikey list\n\nList API keys. Keys are not shown.\n",
	"apikey rm":            "Usage: diparnux apikey rm <id>\n\nDelete an API key.\n",
	"ssh":                  "Usage: diparnux ssh [on|off|advanced|port N|password on|off|keys ...|log]\n\nShow the SSH server state, or turn it on or off.\nTurning it on opens the SSH port. Password login is allowed only for accounts created with 'diparnux user add'.\n  advanced       the port, password-login setting, key count and the host key fingerprints\n  port N         change the SSH port (the firewall follows)\n  password on|off   allow or refuse password login (off needs at least one public key)\n  keys ...       manage public keys\n  log            recent logins and failed attempts\n",
	"network wifi":         "Usage: diparnux network wifi <list|scan|connect <ssid>|disconnect|forget <ssid>|on|off>\n\nconnect asks for the password on the terminal, or reads it with --password-stdin.\n",
	"network wifi connect": "Usage: diparnux network wifi connect <ssid> [--password-stdin]\n\nConnect to a wireless network. Open and saved networks need no password.\n",
	"network wifi forget":  "Usage: diparnux network wifi forget <ssid>\n\nDelete the saved password of a network.\n",
	"user":                 "Usage: diparnux user <add|list|rm> [arguments]\n",
	"user add":             "Usage: diparnux user add <name> [--admin] [--password-stdin]\n\nCreate a login account with your own user name and password.\nThe name starts with a lowercase letter and uses a-z, 0-9, _ and -. The password has 8 to 128 characters.\nWithout --password-stdin the password is asked on the terminal twice, without echo.\n--admin adds the account to the sudo group.\nThe account can log in at the console and, when SSH is on, over SSH.\n",
	"user list":            "Usage: diparnux user list\n\nList accounts created with Diparnux.\n",
	"user rm":              "Usage: diparnux user rm <name>\n\nDelete an account created with Diparnux. Its home directory is kept.\n",
	"config":               "Usage: diparnux config <show|set> [arguments]\n",
	"config show":          "Usage: diparnux config show\n\nShow the daemon configuration. The Hugging Face token is hidden.\n",
	"config set":           "Usage: diparnux config set port <N> | lan on|off | autostart on|off | auto-restart on|off | gateway on|off | parallel <1-16|auto> | hf-token\n\nChange one setting. The Hugging Face token is read from standard input, never from an argument.\n  auto-restart  start the server again (10 s, 30 s, 2 min, 5 min, 15 min) when it stops unexpectedly (default on)\n  gateway   the daemon receives the public port and checks API keys; key changes need no restart (default on)\n  parallel  how many requests the server handles at the same time; each one gets its own context, so the KV cache grows with the count\n",
	"chat":                 "Usage: diparnux chat [--max-tokens N] <prompt...>\n\nSend one prompt to the running server and show the reply and speed.\nUse -- before a prompt that starts with a dash.\n",
	"version":              "Usage: diparnux version\n\nShow the CLI version.\n",
	"help":                 "Usage: diparnux help [command]\n\nShow help for all commands, or for one command.\n",
}

// writeCommandHelp 는 명령의 도움말과 플래그 설명을 표준 출력에 쓴다.
func writeCommandHelp(w io.Writer, name string, fs *flag.FlagSet) {
	fmt.Fprint(w, commandUsage[name])
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Flags:")
	fs.SetOutput(w)
	fs.PrintDefaults()
}
