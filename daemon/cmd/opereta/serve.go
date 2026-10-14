package main

import (
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/client"
)

// serve 는 serve 명령 묶음이다.
func (c *cli) serve(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: opereta serve <start|stop|plan|logs|stats|toolcheck>", "serve")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "start":
		return c.serveStart(rest)
	case "stop":
		return c.serveStop(rest)
	case "plan":
		return c.servePlan(rest)
	case "logs":
		return c.serveLogs(rest)
	case "stats":
		return c.serveStats(rest)
	case "toolcheck":
		return c.serveToolCheck(rest)
	}
	return c.usage(fmt.Sprintf("unknown serve command %q", sub), "serve")
}

// overrideFlags 는 계획 재정의 플래그 묶음이다. 주지 않은 플래그는 자동 계획을 쓴다.
type overrideFlags struct {
	ngl       *string
	ctx       *int
	cacheType *string
	batch     *int
	loadMode  *string
	parallel  *int
	profile   *string
}

// addOverrideFlags 는 재정의 플래그를 등록한다.
func addOverrideFlags(fs *flag.FlagSet) *overrideFlags {
	return &overrideFlags{
		ngl:       fs.String("ngl", "", "GPU layers: auto, all, or a number"),
		ctx:       fs.Int("ctx", 0, "context size in tokens"),
		cacheType: fs.String("cache-type", "", "KV cache type, such as f16 or q8_0"),
		batch:     fs.Int("batch", 0, "batch size"),
		loadMode:  fs.String("load-mode", "", "model load mode"),
		parallel:  fs.Int("parallel", 0, "requests handled at the same time (slots); --ctx is the context of one slot"),
		profile:   fs.String("profile", "", "chat (default) or agent: 16K+ tokens per request and 2 slots for coding agents"),
	}
}

// overrides 는 사용자가 실제로 준 플래그만 재정의로 바꾼다.
func (o *overrideFlags) overrides(fs *flag.FlagSet) client.Overrides {
	set := map[string]bool{}
	fs.Visit(func(f *flag.Flag) { set[f.Name] = true })
	var ov client.Overrides
	if set["ngl"] {
		v := *o.ngl
		ov.NGL = &v
	}
	if set["ctx"] {
		v := *o.ctx
		ov.Ctx = &v
	}
	if set["cache-type"] {
		v := *o.cacheType
		ov.CacheType = &v
	}
	if set["batch"] {
		v := *o.batch
		ov.Batch = &v
	}
	if set["load-mode"] {
		v := *o.loadMode
		ov.LoadMode = &v
	}
	if set["parallel"] {
		v := *o.parallel
		ov.Parallel = &v
	}
	if set["profile"] {
		v := *o.profile
		ov.Profile = &v
	}
	return ov
}

// serveStart 는 계획을 보여 주고 서버를 시작한 뒤 상태가 정해질 때까지 기다린다.
func (c *cli) serveStart(args []string) int {
	fs := newFlags("serve start")
	ov := addOverrideFlags(fs)
	noWait := fs.Bool("no-wait", false, "return once the server is starting")
	pos, code, ok := c.parse(fs, "serve start", args)
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return c.usage("serve start takes at most one model", "serve start")
	}
	model := ""
	if len(pos) == 1 {
		model = pos[0]
	}

	ctx, cancel := c.timeout(startTimeout)
	info, raw, err := c.api.Start(ctx, client.StartRequest{Model: model, Overrides: ov.overrides(fs), Wait: false})
	cancel()
	if err != nil {
		return c.fail(err)
	}
	if *noWait {
		if c.asJSON {
			return c.printJSON(raw)
		}
		writePlanBlock(c.env.stdout, info.ModelID, info.Plan)
		fmt.Fprintf(c.env.stdout, "server state: %s\n", orDash(info.State))
		fmt.Fprintln(c.env.stdout, "server is starting; check it with: opereta status")
		return exitOK
	}
	if !c.asJSON {
		writePlanBlock(c.env.stdout, info.ModelID, info.Plan)
		fmt.Fprintf(c.env.stdout, "server state: %s\n", orDash(info.State))
	}
	return c.waitServer(info.State)
}

// waitServer 는 서버 상태가 starting 이 아니게 될 때까지 상태를 다시 읽는다.
// 모델 로드는 몇 분 걸릴 수 있으므로 데몬의 wait 를 쓰지 않고 상태를 폴링한다.
func (c *cli) waitServer(prev string) int {
	for {
		ctx, cancel := c.timeout(shortTimeout)
		st, raw, err := c.api.Status(ctx)
		cancel()
		if err != nil {
			if c.env.ctx.Err() != nil {
				return c.interrupted("The server keeps starting in operetad. Check it with: opereta status")
			}
			return c.fail(err)
		}
		cur := st.Server
		if !c.asJSON && cur.State != prev {
			fmt.Fprintf(c.env.stdout, "server state: %s\n", cur.State)
		}
		prev = cur.State
		if cur.State != "starting" {
			if c.asJSON {
				if rc := c.printJSON(raw); rc != exitOK {
					return rc
				}
			}
			return c.finishServer(cur)
		}
		if err := c.sleep(); err != nil {
			return c.interrupted("The server keeps starting in operetad. Check it with: opereta status")
		}
	}
}

// finishServer 는 끝난 서버 상태를 보여 주고 종료 코드를 정한다.
func (c *cli) finishServer(s client.Server) int {
	switch s.State {
	case "running":
		if !c.asJSON {
			fmt.Fprintf(c.env.stdout, "server is running with model %s at %s:%d\n", orDash(s.ModelID), orDash(s.Host), s.Port)
		}
		return exitOK
	case "running_cpu_fallback":
		if !c.asJSON {
			fmt.Fprintln(c.env.stdout, "WARNING: the server is running on the CPU. The GPU is not used for inference.")
			for _, w := range s.Warnings {
				fmt.Fprintf(c.env.stdout, "WARNING: %s\n", w)
			}
		}
		return exitOK
	case "failed":
		if s.LastError == nil {
			c.errorLine("server failed to start")
			return exitFailure
		}
		c.errorLine(fmt.Sprintf("server failed: %s (code: %s)", s.LastError.Message, s.LastError.Code))
		if s.LastError.Code == "gpu_offload_failed" || s.LastError.Code == "cuda_backend_unavailable" {
			c.errorLine("a GPU was detected, but inference would not run on the GPU, so the server was stopped")
		}
		return exitFailure
	}
	c.errorLine(fmt.Sprintf("server stopped before it became ready (state: %s)", orDash(s.State)))
	if s.LastError != nil {
		c.errorLine(fmt.Sprintf("last error: %s (code: %s)", s.LastError.Message, s.LastError.Code))
	}
	return exitFailure
}

// serveStop 는 POST /v1/server/stop 을 부른다.
func (c *cli) serveStop(args []string) int {
	if len(args) != 0 {
		return c.usage("serve stop takes no arguments", "serve stop")
	}
	ctx, cancel := c.timeout(startTimeout)
	defer cancel()
	res, raw, err := c.api.Stop(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "server state: %s\n", orDash(res.State))
	return exitOK
}

// servePlan 은 시작하지 않고 계획만 계산해 보여 준다.
func (c *cli) servePlan(args []string) int {
	fs := newFlags("serve plan")
	ov := addOverrideFlags(fs)
	pos, code, ok := c.parse(fs, "serve plan", args)
	if !ok {
		return code
	}
	if len(pos) > 1 {
		return c.usage("serve plan takes at most one model", "serve plan")
	}
	model := ""
	if len(pos) == 1 {
		model = pos[0]
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	res, raw, err := c.api.Plan(ctx, model, ov.overrides(fs))
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	writePlanResult(c.env.stdout, res)
	return exitOK
}

// writePlanResult 는 계획 조회 결과를 쓴다. 입력 요약, GPU 상태, 계획, 계산 근거, 경고, 조언 순서다.
func writePlanResult(w io.Writer, r client.PlanResult) {
	kv(w, "model", orDash(r.ModelID))
	kv(w, "model size", humanBytes(r.Input.ModelBytes))
	kv(w, "layers", fmt.Sprintf("%d", r.Input.Layers))
	kv(w, "train context", fmt.Sprintf("%d", r.Input.CtxTrain))
	kv(w, "GPU check", fmt.Sprintf("%s (%s)", orDash(r.GPU.State), orDash(r.GPU.Message)))
	kv(w, "RAM", fmt.Sprintf("%s available of %s", humanMiB(r.Input.RAMAvailMiB), humanMiB(r.Input.RAMTotalMiB)))
	if len(r.Input.GPUs) > 0 {
		rows := make([][]string, 0, len(r.Input.GPUs))
		for _, g := range r.Input.GPUs {
			rows = append(rows, []string{
				fmt.Sprintf("%d", g.Index),
				humanMiB(g.FreeMiB),
				humanMiB(g.TotalMiB),
			})
		}
		writeTable(w, []string{"GPU", "FREE", "TOTAL"}, rows)
	}
	fmt.Fprintln(w)
	writePlanBlock(w, r.ModelID, &r.Plan)
}

// writePlanBlock 는 계획 본문과 근거, 경고, 조언을 쓴다. 근거와 경고와 조언은 줄마다 하나씩이다.
// 계획이 없으면(nil) 아무것도 쓰지 않는다.
func writePlanBlock(w io.Writer, model string, p *client.Plan) {
	if p == nil {
		return
	}
	fmt.Fprintf(w, "plan for %s\n", orDash(model))
	kv(w, "  GPU layers", orDash(p.NGL))
	if p.Parallel > 1 {
		kv(w, "  slots", fmt.Sprintf("%d (context per slot %d)", p.Parallel, p.SlotCtx))
	}
	kv(w, "  context", fmt.Sprintf("%d", p.Ctx))
	kv(w, "  KV cache", fmt.Sprintf("K %s, V %s", orDash(p.CacheTypeK), orDash(p.CacheTypeV)))
	kv(w, "  flash attention", yesNo(p.FlashAttn))
	kv(w, "  batch", fmt.Sprintf("%d", p.Batch))
	kv(w, "  tensor split", orDash(p.TensorSplit))
	kv(w, "  visible GPUs", joinInts(p.VisibleDevices))
	kv(w, "  load mode", orDash(p.LoadMode))
	kv(w, "  fit", yesNo(p.Fit))
	kv(w, "  CPU only", yesNo(p.CPUOnly))
	kv(w, "  est. VRAM", humanMiB(p.EstVRAMMiB))
	kv(w, "  est. RAM", humanMiB(p.EstRAMMiB))
	writeList(w, "rationale", "", p.Rationale)
	writeList(w, "warnings", "WARNING: ", p.Warnings)
	writeList(w, "advice", "", p.Advice)
}

// writeList 는 제목과 항목 목록을 쓴다. 항목이 없으면 제목만 "none" 으로 쓴다.
func writeList(w io.Writer, title, prefix string, items []string) {
	if len(items) == 0 {
		kv(w, title, "none")
		return
	}
	fmt.Fprintf(w, "%s:\n", title)
	for _, it := range items {
		fmt.Fprintf(w, "  - %s%s\n", prefix, it)
	}
}

// joinInts 는 정수 목록을 쉼표로 잇는다. 빈 목록은 "none" 이다.
func joinInts(v []int) string {
	if len(v) == 0 {
		return "none"
	}
	parts := make([]string, len(v))
	for i, n := range v {
		parts[i] = fmt.Sprintf("%d", n)
	}
	return strings.Join(parts, ",")
}

// serveLogs 는 llama-server 로그의 마지막 줄을 보여 준다.
func (c *cli) serveLogs(args []string) int {
	fs := newFlags("serve logs")
	n := fs.Int("n", 200, "number of lines (1-2000)")
	pos, code, ok := c.parse(fs, "serve logs", args)
	if !ok {
		return code
	}
	if len(pos) != 0 {
		return c.usage("serve logs takes no positional arguments", "serve logs")
	}
	if *n < 1 || *n > logLinesLimit {
		return c.usage(fmt.Sprintf("-n must be 1-%d", logLinesLimit), "serve logs")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	logs, raw, err := c.api.Logs(ctx, *n)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if strings.TrimSpace(logs.Log) == "" {
		fmt.Fprintln(c.env.stdout, "no log lines")
		return exitOK
	}
	fmt.Fprintln(c.env.stdout, strings.TrimRight(logs.Log, "\n"))
	return exitOK
}

// serveStats 는 서버 현황을 보여 준다. 서버가 돌고 있을 때만 슬롯과 처리 속도가 있다.
func (c *cli) serveStats(args []string) int {
	if len(args) != 0 {
		return c.usage("serve stats takes no arguments", "serve stats")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	st, raw, err := c.api.ServerStats(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	kv(w, "state", orDash(st.State))
	if !st.Running {
		fmt.Fprintln(w, "the server is not running; start it with: opereta serve start")
		return exitOK
	}
	kv(w, "model", orDash(st.ModelID))
	kv(w, "uptime", formatDuration(st.UptimeSeconds))
	if st.Slots.Available {
		kv(w, "slots", fmt.Sprintf("%d busy of %d (context per slot %d)", st.Slots.Busy, st.Slots.Total, st.SlotCtx))
	} else {
		kv(w, "slots", "unavailable: "+orDash(st.Slots.Reason))
	}
	if st.Metrics.Available {
		kv(w, "generation", fmt.Sprintf("%.1f tokens/s", st.Metrics.GenTokensPerSec))
		kv(w, "prompt", fmt.Sprintf("%.1f tokens/s", st.Metrics.PromptTokensPerSec))
		kv(w, "requests", fmt.Sprintf("%.0f processing, %.0f deferred", st.Metrics.RequestsProcessing, st.Metrics.RequestsDeferred))
		kv(w, "tokens so far", fmt.Sprintf("%.0f prompt, %.0f generated", st.Metrics.PromptTokensTotal, st.Metrics.GeneratedTotal))
	} else {
		kv(w, "metrics", "unavailable: "+orDash(st.Metrics.Reason))
	}
	if st.Gateway != nil {
		kv(w, "gateway", fmt.Sprintf("%d requests, %d errors, %d refused, %d in flight",
			st.Gateway.Requests, st.Gateway.Errors, st.Gateway.Unauthorized, st.Gateway.InFlight))
	}
	return exitOK
}

// serveToolCheck 는 서버가 도구 호출을 제대로 돌려주는지 작은 요청으로 확인한다.
// 코딩 에이전트(Vedecaps 같은 도구)는 이 기능이 있어야 파일을 읽고 고칠 수 있다.
func (c *cli) serveToolCheck(args []string) int {
	if len(args) != 0 {
		return c.usage("serve toolcheck takes no arguments", "serve toolcheck")
	}
	ctx, cancel := c.timeout(chatTimeout)
	defer cancel()
	res, raw, err := c.api.ToolCheck(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		if c.printJSON(raw) != exitOK {
			return exitFailure
		}
	} else {
		w := c.env.stdout
		kv(w, "tool calls", map[bool]string{true: "working", false: "not working"}[res.OK])
		kv(w, "result", res.Message)
		kv(w, "finish reason", orDash(res.FinishReason))
		kv(w, "time", fmt.Sprintf("%d ms", res.LatencyMS))
	}
	if !res.OK {
		return exitFailure
	}
	return exitOK
}
