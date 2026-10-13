package main

import (
	"fmt"
	"sort"
	"time"
)

// connection 은 다른 컴퓨터가 이 컴퓨터의 모델 서버에 접속하는 데 필요한 주소와 예시를 보여 준다.
// 평문 API 키는 보여 주지 않는다(키는 만들 때 한 번만 보인다).
func (c *cli) connection(args []string) int {
	const use = "usage: diparnux connection [examples]"
	if len(args) > 1 || (len(args) == 1 && args[0] != "examples") {
		return c.usage(use, "connection")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	res, raw, err := c.api.Connection(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	if len(args) == 1 {
		names := make([]string, 0, len(res.Examples))
		for name := range res.Examples {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(w, "# %s\n%s\n\n", name, res.Examples[name])
		}
		return exitOK
	}
	kv(w, "server", orDash(res.ServerState))
	kv(w, "model", orDash(res.ModelID))
	kv(w, "this computer", res.LocalURL)
	if len(res.Addresses) == 0 {
		kv(w, "other computers", "- (LAN exposure is off or no address)")
	}
	for i, a := range res.Addresses {
		label := ""
		if i == 0 {
			label = "other computers"
		}
		kv(w, label, fmt.Sprintf("%s  (%s, %s)", a.URL, a.Interface, a.Family))
	}
	if res.MDNSURL != "" {
		kv(w, "by name", res.MDNSURL)
	}
	kv(w, "API keys", fmt.Sprintf("%d (required: %s)", res.KeyCount, yesNo(res.KeyRequired)))
	kv(w, "gateway", yesNo(res.Gateway))
	for _, m := range res.Warnings {
		fmt.Fprintf(w, "warning: %s\n", m)
	}
	fmt.Fprintln(w, "examples: diparnux connection examples")
	return exitOK
}

// gateway 는 게이트웨이의 상태, 키별 사용량, 최근 요청을 보여 준다.
func (c *cli) gateway(args []string) int {
	if len(args) != 0 {
		return c.usage("usage: diparnux gateway", "gateway")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	res, raw, err := c.api.Gateway(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	kv(w, "gateway", yesNo(res.Enabled))
	switch {
	case res.State.Listening:
		kv(w, "listening", fmt.Sprintf("%s -> 127.0.0.1:%d", res.State.Addr, res.State.BackendPort))
	case res.State.Error != "":
		kv(w, "listening", "no: "+res.State.Error)
	default:
		kv(w, "listening", "no (the model server is not running)")
	}
	kv(w, "requests", fmt.Sprintf("%d (errors %d, unauthorized %d, in flight %d)", res.Stats.Requests, res.Stats.Errors, res.Stats.Unauthorized, res.Stats.InFlight))
	if res.Note != "" {
		fmt.Fprintf(w, "note: %s\n", res.Note)
	}
	if len(res.Stats.Keys) > 0 {
		fmt.Fprintln(w)
		rows := make([][]string, 0, len(res.Stats.Keys))
		for _, k := range res.Stats.Keys {
			last := "-"
			if !k.LastUsed.IsZero() {
				last = k.LastUsed.Local().Format(time.DateTime)
			}
			rows = append(rows, []string{k.ID, orDash(k.Label), orDash(k.Prefix),
				fmt.Sprint(k.Requests), fmt.Sprint(k.Errors), fmt.Sprint(k.PromptTokens), fmt.Sprint(k.CompletionTokens), last})
		}
		writeTable(w, []string{"ID", "LABEL", "PREFIX", "REQUESTS", "ERRORS", "PROMPT TOK", "OUTPUT TOK", "LAST USED"}, rows)
	}
	return exitOK
}
