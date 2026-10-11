// Package report는 진단 리포트 텍스트를 만든다. 리포트는 영어 ASCII 이고 비밀을 담지 않는다.
//
// 비밀 처리는 두 겹이다. 첫째, Deps.Secrets 가 돌려주는 값(API 키 평문, HF 토큰)을 모두 "<redacted>" 로 바꾼다.
// 둘째, 키 형식(llx_ 접두사, hf_ 접두사, Bearer 토큰)을 정규식으로 한 번 더 가린다.
// 설정은 Redacted() 로 토큰을 가린 값만 쓴다. 키 개수만 쓰고 키 값은 쓰지 않는다.
//
// 명령 실행(nvidia-smi, dkms, mokutil, systemctl, journalctl)은 주입된 Runner 로 한다. 명령이 없거나 실패하면
// 그 사실을 한 줄로 적고 계속한다. 리포트 전체와 각 명령 출력에 크기 상한이 있다.
package report

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
	"github.com/nxodh/lightlixos/daemon/internal/supervisor"
)

// 상한과 기본값이다.
const (
	maxReportBytes   = 512 << 10
	maxCommandOutput = 16 << 10
	maxFileBytes     = 8 << 10
	commandTimeout   = 10 * time.Second
	journalLines     = 50
)

// defaultUnits 는 저널을 함께 보는 유닛 목록이다.
var defaultUnits = []string{"lightlix-gpu-check.service", "lightlix-llama.service", "lightlixd.service"}

// 키 형식 정규식이다. 주입된 비밀을 놓쳐도 형식으로 가린다.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`llx_[A-Za-z0-9_-]{16,}`),
	regexp.MustCompile(`hf_[A-Za-z0-9]{16,}`),
	regexp.MustCompile(`Bearer [A-Za-z0-9._~+/=-]{8,}`),
}

// Deps 는 리포트가 쓰는 의존성과 설정이다. 비워 둔 값은 해당 항목을 "not configured" 로 적는다.
type Deps struct {
	// Version 은 데몬 버전이다.
	Version string
	// Now 는 현재 시각이다. nil 이면 time.Now 를 쓴다.
	Now func() time.Time
	// Runner 는 외부 명령 실행기다.
	Runner platform.Runner
	// GPU 는 NVML 프로버다.
	GPU platform.GPUProber
	// MemInfo 는 메모리 상태를 돌려준다.
	MemInfo func() (platform.MemInfo, error)
	// Config 는 설정 파일을 읽는다. 토큰은 Redacted 로 가린다.
	Config func() (config.Config, error)
	// KeyCount 는 API 키 개수를 돌려준다. 값은 돌려주지 않는다.
	KeyCount func() (int, error)
	// Models 는 모델 디렉터리다.
	Models models.Store
	// Server 는 llama-server 상태다.
	Server func() supervisor.Status
	// Secrets 는 리포트에서 가릴 비밀 값 목록(API 키 평문, HF 토큰)이다.
	Secrets func() []string
	// GPUStatusPath, EnvFilePath, OSReleasePath 는 읽을 파일 경로다.
	GPUStatusPath string
	EnvFilePath   string
	OSReleasePath string
	// Units 는 저널을 볼 유닛 목록이다. nil 이면 기본 목록을 쓴다.
	Units []string
}

// builder 는 리포트 텍스트를 쌓는다. 크기 상한을 넘으면 더 쓰지 않는다.
type builder struct {
	b         strings.Builder
	truncated bool
}

func (w *builder) section(title string) {
	w.line("")
	w.line("== " + title + " ==")
}

func (w *builder) line(s string) {
	w.write(s + "\n")
}

func (w *builder) write(s string) {
	if w.truncated {
		return
	}
	if w.b.Len()+len(s) > maxReportBytes {
		w.b.WriteString("(report truncated: size limit reached)\n")
		w.truncated = true
		return
	}
	w.b.WriteString(s)
}

// Build 는 리포트 전체를 만든다. 실패한 항목은 그 사실을 적고 계속한다.
func Build(ctx context.Context, d Deps) string {
	now := d.Now
	if now == nil {
		now = time.Now
	}
	secrets := []string{}
	if d.Secrets != nil {
		secrets = d.Secrets()
	}
	w := &builder{}
	w.line("Lightlix diagnostic report")
	w.line("generated: " + now().UTC().Format(time.RFC3339))
	w.line("daemon version: " + nonEmpty(d.Version, "dev"))

	w.section("os-release")
	w.write(osRelease(d.OSReleasePath))

	w.section("kernel")
	w.write(runSummary(ctx, d.Runner, "uname", "-r"))

	w.section("configuration (token redacted)")
	if d.Config == nil {
		w.line("not configured")
	} else if cfg, err := d.Config(); err != nil {
		w.line("cannot read the config: " + err.Error())
	} else {
		data, _ := json.MarshalIndent(cfg.Redacted(), "", "  ")
		w.line(string(data))
	}

	w.section("API keys")
	if d.KeyCount == nil {
		w.line("not configured")
	} else if n, err := d.KeyCount(); err != nil {
		w.line("cannot count the keys: " + err.Error())
	} else {
		w.line(fmt.Sprintf("count: %d (key values are not included)", n))
	}

	w.section("GPU status file")
	w.write(fileText(d.GPUStatusPath, "GPU status file"))

	w.section("NVML snapshot")
	w.write(nvmlSummary(ctx, d.GPU))

	w.section("server status and plan")
	w.write(serverSummary(d.Server))

	w.section("models")
	w.write(modelsSummary(d.Models))

	w.section("memory")
	if d.MemInfo == nil {
		w.line("not configured")
	} else if m, err := d.MemInfo(); err != nil {
		w.line("cannot read memory: " + err.Error())
	} else {
		w.line(fmt.Sprintf("total: %d MiB, available: %d MiB", m.TotalMiB, m.AvailableMiB))
	}

	w.section("llama-server environment file")
	w.write(fileText(d.EnvFilePath, "environment file"))

	w.section("commands")
	w.write(commandSection(ctx, d.Runner, d.Units))

	return sanitize(redact(w.b.String(), secrets))
}

// osRelease 는 os-release 의 주요 항목을 요약한다.
func osRelease(path string) string {
	if path == "" {
		path = "/etc/os-release"
	}
	f, err := os.Open(path)
	if err != nil {
		return "cannot read " + path + ": " + err.Error() + "\n"
	}
	defer f.Close()
	keep := map[string]bool{"ID": true, "VERSION_ID": true, "VERSION_CODENAME": true, "PRETTY_NAME": true}
	var b strings.Builder
	sc := bufio.NewScanner(io.LimitReader(f, maxFileBytes))
	for sc.Scan() {
		k, v, ok := strings.Cut(sc.Text(), "=")
		if ok && keep[k] {
			b.WriteString(k + "=" + strings.Trim(v, "\"") + "\n")
		}
	}
	return b.String()
}

// fileText 는 작은 텍스트 파일의 원문을 돌려준다. 없으면 그 사실을 적는다.
func fileText(path, what string) string {
	if path == "" {
		return what + ": not configured\n"
	}
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return what + ": not present (" + path + ")\n"
		}
		return what + ": cannot be read: " + err.Error() + "\n"
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxFileBytes+1))
	if err != nil {
		return what + ": cannot be read: " + err.Error() + "\n"
	}
	text := string(data)
	if len(data) > maxFileBytes {
		text = text[:maxFileBytes] + "\n(file truncated)\n"
	}
	if !strings.HasSuffix(text, "\n") {
		text += "\n"
	}
	return text
}

// runSummary 는 명령을 실행하고 출력 한 덩어리를 돌려준다. 실패하면 그 사실을 적는다.
func runSummary(ctx context.Context, r platform.Runner, name string, args ...string) string {
	if r == nil {
		return "not configured\n"
	}
	res, err := r.Run(ctx, platform.Command{Name: name, Args: args, Timeout: commandTimeout, MaxOutput: maxCommandOutput})
	if err != nil {
		return "not available: " + err.Error() + "\n"
	}
	out := strings.TrimSpace(string(res.Stdout))
	if res.ExitCode != 0 {
		return fmt.Sprintf("exit %d: %s\n", res.ExitCode, strings.TrimSpace(string(res.Stderr)))
	}
	if out == "" {
		return "(no output)\n"
	}
	return out + "\n"
}

// nvmlSummary 는 NVML 스냅샷을 요약한다.
func nvmlSummary(ctx context.Context, g platform.GPUProber) string {
	if g == nil {
		return "not configured\n"
	}
	snap := g.Snapshot(ctx)
	var b strings.Builder
	if !snap.Available {
		fmt.Fprintf(&b, "NVML unavailable: %s\n", snap.Reason)
		return b.String()
	}
	fmt.Fprintf(&b, "NVML version: %s, driver: %s, CUDA driver: %s\n", snap.NVMLVersion, snap.DriverVersion, snap.CUDADriverVersion)
	for _, d := range snap.Devices {
		fmt.Fprintf(&b, "GPU %d: %s, arch %s, compute %s, memory total %d MiB, free %d MiB, used %d MiB, temp %d C, power %d mW, util gpu %d%% mem %d%%, processes %d (known: %v)\n",
			d.Index, d.Name, d.Arch, d.ComputeCap, d.MemTotalMiB, d.MemFreeMiB, d.MemUsedMiB,
			d.TemperatureC, d.PowerMilliwatt, d.UtilGPUPercent, d.UtilMemPercent, len(d.Processes), d.ProcessesKnown)
	}
	return b.String()
}

// serverSummary 는 서버 상태 JSON 과 계획의 주요 값이다.
func serverSummary(f func() supervisor.Status) string {
	if f == nil {
		return "not configured\n"
	}
	st := f()
	data, _ := json.MarshalIndent(st, "", "  ")
	var b strings.Builder
	b.Write(data)
	b.WriteString("\n")
	if st.Plan != nil {
		p := st.Plan
		fmt.Fprintf(&b, "plan: ngl=%s ctx=%d cache=%s/%s flash_attn=%v batch=%d cpu_only=%v load_mode=%s\n",
			p.NGL, p.Ctx, p.CacheTypeK, p.CacheTypeV, p.FlashAttn, p.Batch, p.CPUOnly, p.LoadMode)
		for _, r := range p.Rationale {
			fmt.Fprintf(&b, "rationale: %s\n", r)
		}
	}
	return b.String()
}

// modelsSummary 는 모델 목록(이름, 크기, 라이선스)이다.
func modelsSummary(s models.Store) string {
	if s.Dir == "" {
		return "not configured\n"
	}
	list, err := s.List()
	if err != nil {
		return "cannot list models: " + err.Error() + "\n"
	}
	if len(list) == 0 {
		return "(no models)\n"
	}
	var b strings.Builder
	for _, m := range list {
		def := ""
		if m.Default {
			def = " [default]"
		}
		inc := ""
		if m.Incomplete {
			inc = " [incomplete]"
		}
		fmt.Fprintf(&b, "%s: %d MiB, license %s, repo %s%s%s\n", m.ID, m.Size/(1<<20), nonEmpty(m.License, "unknown"), nonEmpty(m.Repo, "unknown"), def, inc)
	}
	return b.String()
}

// commandSection 은 진단 명령의 출력을 모은다.
func commandSection(ctx context.Context, r platform.Runner, units []string) string {
	var b strings.Builder
	cmds := [][]string{
		{"nvidia-smi"},
		{"dkms", "status"},
		{"mokutil", "--sb-state"},
		{"systemctl", "--failed", "--legend=no"},
	}
	for _, c := range cmds {
		b.WriteString("$ " + strings.Join(c, " ") + "\n")
		b.WriteString(runSummary(ctx, r, c[0], c[1:]...))
	}
	if len(units) == 0 {
		units = defaultUnits
	}
	for _, u := range units {
		args := []string{"-u", u, "-n", fmt.Sprint(journalLines), "-o", "cat", "--no-pager"}
		b.WriteString("$ journalctl " + strings.Join(args, " ") + "\n")
		b.WriteString(runSummary(ctx, r, "journalctl", args...))
	}
	return b.String()
}

// redact 는 주입된 비밀 값과 형식이 맞는 값을 "<redacted>" 로 바꾼다.
func redact(text string, secrets []string) string {
	for _, s := range secrets {
		if s != "" {
			text = strings.ReplaceAll(text, s, "<redacted>")
		}
	}
	for _, re := range secretPatterns {
		text = re.ReplaceAllString(text, "<redacted>")
	}
	return text
}

// sanitize 는 ASCII 가 아닌 바이트와 제어 문자를 '?' 로 바꾼다. 줄바꿈과 탭은 남긴다.
func sanitize(text string) string {
	var b strings.Builder
	b.Grow(len(text))
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case c == '\n' || c == '\t':
			b.WriteByte(c)
		case c < 0x20 || c > 0x7e:
			b.WriteByte('?')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

func nonEmpty(v, def string) string {
	if v == "" {
		return def
	}
	return v
}
