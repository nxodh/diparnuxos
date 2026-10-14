package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/client"
)

// model 은 model 명령 묶음이다.
func (c *cli) model(args []string) int {
	if len(args) == 0 {
		return c.usage("usage: opereta model <list|pull|import|rm|search|files|default>", "model")
	}
	sub, rest := args[0], args[1:]
	switch sub {
	case "list":
		return c.modelList(rest)
	case "pull":
		return c.modelPull(rest)
	case "rm":
		return c.modelRemove(rest)
	case "search":
		return c.modelSearch(rest)
	case "files":
		return c.modelFiles(rest)
	case "default":
		return c.modelDefault(rest)
	case "import":
		return c.modelImport(rest)
	}
	return c.usage(fmt.Sprintf("unknown model command %q", sub), "model")
}

// modelList 는 GET /v1/models 를 보여 준다.
func (c *cli) modelList(args []string) int {
	if len(args) != 0 {
		return c.usage("model list takes no arguments", "model list")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	list, raw, err := c.api.Models(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if len(list.Models) == 0 {
		fmt.Fprintln(c.env.stdout, "no models installed; download one with: opereta model pull <repo>")
		return exitOK
	}
	rows := make([][]string, 0, len(list.Models))
	for _, m := range list.Models {
		def := ""
		if m.Default || m.ID == list.Default {
			def = "yes"
		}
		status := "ok"
		if m.Incomplete {
			status = "incomplete"
		}
		ctxLen := "-"
		if m.ContextLength > 0 {
			ctxLen = fmt.Sprintf("%d", m.ContextLength)
		}
		rows = append(rows, []string{
			m.ID,
			humanBytes(m.SizeBytes),
			def,
			orDash(m.Architecture),
			orDash(m.FileType),
			ctxLen,
			status,
		})
	}
	writeTable(c.env.stdout, []string{"ID", "SIZE", "DEFAULT", "ARCH", "QUANT", "CONTEXT", "STATUS"}, rows)
	return exitOK
}

// writeRemoteHeader 는 저장소의 저장소 이름, 판본, 라이선스, gated 여부를 쓴다.
func writeRemoteHeader(w io.Writer, r client.Remote) {
	kv(w, "repository", r.Repo)
	kv(w, "revision", orDash(r.Revision))
	kv(w, "license", orDash(r.License))
	gated := "no"
	if r.Gated {
		gated = fmt.Sprintf("yes (%s)", orDash(r.GateMode))
	}
	kv(w, "gated", gated)
}

// modelPull 은 저장소를 확인하고 내려받기 작업을 만든 뒤 끝날 때까지 진행률을 보여 준다.
func (c *cli) modelPull(args []string) int {
	fs := newFlags("model pull")
	file := fs.String("file", "", "GGUF file path inside the repository")
	rev := fs.String("revision", "", "branch, tag, or commit (default main)")
	noWait := fs.Bool("no-wait", false, "return once the download is started")
	pos, code, ok := c.parse(fs, "model pull", args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.usage("model pull needs exactly one repository, such as owner/name", "model pull")
	}
	repo := pos[0]

	ctx, cancel := c.timeout(metaTimeout)
	remote, _, err := c.api.RemoteFiles(ctx, repo, *rev)
	cancel()
	if err != nil {
		return c.pullFailure(err, nil, *file)
	}
	if !c.asJSON {
		writeRemoteHeader(c.env.stdout, remote)
	}

	ctx, cancel = c.timeout(metaTimeout)
	job, raw, err := c.api.Pull(ctx, client.PullRequest{Repo: repo, File: *file, Revision: *rev})
	cancel()
	if err != nil {
		return c.pullFailure(err, &remote, *file)
	}
	if *noWait {
		return c.printJobStart(job, raw, &remote)
	}
	return c.followJob(job, raw, &remote)
}

// printJobStart 는 --no-wait 로 만든 작업의 상태를 한 번 보여 준다.
func (c *cli) printJobStart(job client.Job, raw []byte, remote *client.Remote) int {
	if c.asJSON {
		if rc := c.printJSON(raw); rc != exitOK {
			return rc
		}
	} else {
		fmt.Fprintf(c.env.stdout, "job %s: %s (%s)\n", job.ID, job.State, job.File)
		if job.State != "failed" && job.State != "canceled" && job.State != "done" {
			fmt.Fprintln(c.env.stdout, "download started; run the same 'opereta model pull' command again to follow it")
		}
	}
	switch job.State {
	case "failed":
		return c.jobFailure(job, remote)
	case "canceled":
		c.errorLine("download was canceled")
		return exitFailure
	}
	return exitOK
}

// followJob 은 작업이 끝날 때까지 기다리며 진행률을 보여 준다. Ctrl-C 면 작업은 데몬에서 계속된다.
func (c *cli) followJob(first client.Job, raw []byte, remote *client.Remote) int {
	var prog *progress
	if !c.asJSON {
		prog = &progress{w: c.env.stdout, tty: c.env.stdoutTTY, decile: -1}
	}
	cur, lastRaw := first, raw
	for {
		if prog != nil {
			prog.update(cur)
		}
		if cur.State != "queued" && cur.State != "running" {
			break
		}
		if err := c.sleep(); err != nil {
			prog.finish()
			return c.interrupted(pullInterruptedMsg)
		}
		ctx, cancel := c.timeout(shortTimeout)
		j, r, err := c.api.Job(ctx, cur.ID)
		cancel()
		if err != nil {
			prog.finish()
			if c.env.ctx.Err() != nil {
				return c.interrupted(pullInterruptedMsg)
			}
			return c.fail(err)
		}
		cur, lastRaw = j, r
	}
	prog.finish()
	if c.asJSON {
		if rc := c.printJSON(lastRaw); rc != exitOK {
			return rc
		}
	}
	switch cur.State {
	case "done":
		if !c.asJSON {
			fmt.Fprintf(c.env.stdout, "installed model: %s\n", cur.ModelID)
			fmt.Fprintf(c.env.stdout, "make it the default with: opereta model default %s\n", cur.ModelID)
		}
		return exitOK
	case "canceled":
		c.errorLine("download was canceled")
		return exitFailure
	}
	return c.jobFailure(cur, remote)
}

// pullInterruptedMsg 는 내려받기 중 Ctrl-C 를 눌렀을 때의 안내다.
const pullInterruptedMsg = "The job keeps running in operetad. Run 'opereta model pull' again to follow it."

// progress 는 내려받기 진행률을 쓴다. 터미널이면 한 줄을 캐리지 리턴으로 갱신하고,
// 아니면 10% 단위로 한 줄씩 쓴다.
type progress struct {
	w      io.Writer
	tty    bool
	decile int
	drawn  bool
}

// update 는 작업 상태 하나를 반영한다.
func (p *progress) update(j client.Job) {
	line := progressLine(j)
	if p.tty {
		fmt.Fprintf(p.w, "\r%-79s", line)
		p.drawn = true
		return
	}
	pct := percentOf(j)
	if pct < 0 {
		return
	}
	if d := pct / 10; d > p.decile {
		p.decile = d
		fmt.Fprintln(p.w, line)
	}
}

// finish 는 터미널 진행 줄을 줄바꿈으로 마무리한다.
func (p *progress) finish() {
	if p != nil && p.tty && p.drawn {
		fmt.Fprintln(p.w)
		p.drawn = false
	}
}

// percentOf 는 내려받은 비율(0-100)이다. 전체 크기를 모르면 -1 이다.
func percentOf(j client.Job) int {
	if j.BytesTotal <= 0 {
		return -1
	}
	pct := int(j.BytesDone * 100 / j.BytesTotal)
	if pct > 100 {
		pct = 100
	}
	return pct
}

// progressLine 은 진행 줄 한 개의 내용이다.
func progressLine(j client.Job) string {
	pct := percentOf(j)
	if pct < 0 {
		return fmt.Sprintf("downloading %s: %s", j.File, humanBytes(j.BytesDone))
	}
	return fmt.Sprintf("downloading %s: %s of %s (%d%%)", j.File, humanBytes(j.BytesDone), humanBytes(j.BytesTotal), pct)
}

// modelRemove 는 DELETE /v1/models/{id} 를 부른다.
func (c *cli) modelRemove(args []string) int {
	if len(args) != 1 {
		return c.usage("usage: opereta model rm <id>", "model rm")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	res, raw, err := c.api.DeleteModel(ctx, args[0])
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "removed model %s\n", res.ID)
	return exitOK
}

// modelSearch 는 GET /v1/models/search 를 보여 준다. 검색어의 나머지 인자는 공백으로 이어 붙인다.
func (c *cli) modelSearch(args []string) int {
	fs := newFlags("model search")
	limit := fs.Int("limit", 20, "number of results (1-100)")
	pos, code, ok := c.parse(fs, "model search", args)
	if !ok {
		return code
	}
	if len(pos) == 0 {
		return c.usage("usage: opereta model search <query> [--limit N]", "model search")
	}
	if *limit < 1 || *limit > searchLimitMax {
		return c.usage(fmt.Sprintf("--limit must be 1-%d", searchLimitMax), "model search")
	}
	query := strings.Join(pos, " ")
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	res, raw, err := c.api.Search(ctx, query, *limit)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	if len(res.Results) == 0 {
		fmt.Fprintln(c.env.stdout, "no GGUF models matched")
		return exitOK
	}
	rows := make([][]string, 0, len(res.Results))
	for _, h := range res.Results {
		rows = append(rows, []string{
			h.ID,
			fmt.Sprintf("%d", h.Downloads),
			yesNo(h.Gated),
			orDash(h.License),
			orDash(h.Architecture),
		})
	}
	writeTable(c.env.stdout, []string{"ID", "DOWNLOADS", "GATED", "LICENSE", "ARCH"}, rows)
	return exitOK
}

// modelFiles 는 저장소의 GGUF 파일 목록을 보여 준다.
func (c *cli) modelFiles(args []string) int {
	fs := newFlags("model files")
	rev := fs.String("revision", "", "branch, tag, or commit (default main)")
	pos, code, ok := c.parse(fs, "model files", args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.usage("usage: opereta model files <repo> [--revision R]", "model files")
	}
	ctx, cancel := c.timeout(metaTimeout)
	defer cancel()
	remote, raw, err := c.api.RemoteFiles(ctx, pos[0], *rev)
	if err != nil {
		return c.pullFailure(err, nil, "")
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	writeRemoteHeader(c.env.stdout, remote)
	if len(remote.Files) == 0 {
		fmt.Fprintln(c.env.stdout, "no GGUF files in this repository")
		return exitOK
	}
	rows := make([][]string, 0, len(remote.Files))
	for _, f := range remote.Files {
		rows = append(rows, []string{f.Path, humanBytes(f.Size), orDash(f.SHA256)})
	}
	writeTable(c.env.stdout, []string{"FILE", "SIZE", "SHA256"}, rows)
	return exitOK
}

// modelDefault 는 PUT /v1/models/default 를 부른다.
func (c *cli) modelDefault(args []string) int {
	if len(args) != 1 {
		return c.usage("usage: opereta model default <id>", "model default")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	res, raw, err := c.api.SetDefaultModel(ctx, args[0])
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "default model: %s\n", res.Default)
	return exitOK
}

// countingReader 는 읽힌 바이트 수를 세어 진행률을 알린다.
type countingReader struct {
	r      io.Reader
	done   int64
	report func(done int64)
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	if n > 0 {
		c.done += int64(n)
		c.report(c.done)
	}
	return n, err
}

// modelImport 는 내려받아 둔 GGUF 파일을 모델 디렉터리로 가져온다.
// 파일은 이 명령이 사용자 권한으로 열어 데몬에 보내므로, 읽을 수 있는 파일만 가져올 수 있다.
func (c *cli) modelImport(args []string) int {
	fs := newFlags("model import")
	name := fs.String("name", "", "model name to store it under (default: the file name)")
	pos, code, ok := c.parse(fs, "model import", args)
	if !ok {
		return code
	}
	if len(pos) != 1 {
		return c.usage("usage: opereta model import <file.gguf> [--name NAME]", "model import")
	}
	path := pos[0]
	id := *name
	if id == "" {
		id = filepath.Base(path)
	}
	if !strings.HasSuffix(id, ".gguf") || len(id) <= len(".gguf") || strings.ContainsAny(id, `/\`) {
		return c.usage("the model name must end in .gguf and contain no slash; give one with --name", "model import")
	}
	f, err := os.Open(path)
	if err != nil {
		c.errorLine(err.Error())
		return exitFailure
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		c.errorLine(err.Error())
		return exitFailure
	}
	if !info.Mode().IsRegular() || info.Size() == 0 {
		c.errorLine(path + " is not a non-empty regular file")
		return exitFailure
	}
	size := info.Size()

	last := -1
	rd := &countingReader{r: f, report: func(done int64) {
		if c.asJSON {
			return
		}
		pct := int(done * 100 / size)
		switch {
		case c.env.stdoutTTY:
			fmt.Fprintf(c.env.stdout, "\rimporting %s: %s of %s (%d%%)   ", id, humanBytes(done), humanBytes(size), pct)
		case pct/10 > last:
			last = pct / 10
			fmt.Fprintf(c.env.stdout, "importing %s: %d%%\n", id, pct)
		}
	}}
	res, raw, err := c.api.ImportModel(c.env.ctx, id, rd, size)
	if c.env.stdoutTTY && !c.asJSON && rd.done > 0 {
		fmt.Fprintln(c.env.stdout)
	}
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	fmt.Fprintf(c.env.stdout, "imported %s (%s, sha256 %s)\n", res.ModelID, humanBytes(res.SizeBytes), res.SHA256)
	if res.Default {
		fmt.Fprintln(c.env.stdout, "it is now the default model")
	}
	return exitOK
}
