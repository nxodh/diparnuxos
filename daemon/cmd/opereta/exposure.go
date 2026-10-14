package main

import "fmt"

// exposure 는 이 컴퓨터가 네트워크에 내놓는 것을 점검한다. 경고나 오류가 있으면 종료 코드 1 이다.
func (c *cli) exposure(args []string) int {
	if len(args) != 0 {
		return c.usage("exposure takes no arguments", "exposure")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	r, raw, err := c.api.Exposure(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	marks := map[string]string{"ok": "ok  ", "info": "info", "warning": "WARN", "error": "FAIL"}
	for _, f := range r.Findings {
		fmt.Fprintf(c.env.stdout, "%s  %s\n", marks[f.Level], f.Title)
		if f.Detail != "" {
			fmt.Fprintf(c.env.stdout, "      %s\n", f.Detail)
		}
	}
	if r.Level == "warning" || r.Level == "error" {
		return exitFailure
	}
	return exitOK
}
