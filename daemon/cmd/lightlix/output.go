package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"unicode/utf16"
	"unicode/utf8"
)

// asciiWriter 는 ASCII 가 아닌 글자를 '?' 로 바꿔 쓴다. 리눅스 텍스트 콘솔은 한글을 표시하지 못한다.
// 줄바꿈, 탭, 캐리지 리턴은 그대로 둔다. 진행 표시가 캐리지 리턴을 쓰기 때문이다.
type asciiWriter struct {
	w io.Writer
}

// Write 는 p 를 ASCII 로 바꿔 내부 writer 에 쓴다. 돌려주는 길이는 원래 p 의 길이다.
func (a asciiWriter) Write(p []byte) (int, error) {
	out := make([]byte, 0, len(p))
	for i := 0; i < len(p); {
		r, size := utf8.DecodeRune(p[i:])
		i += size
		switch {
		case r == '\n' || r == '\r' || r == '\t':
			out = append(out, byte(r))
		case r >= 0x20 && r < 0x7f:
			out = append(out, byte(r))
		default:
			out = append(out, '?')
		}
	}
	if _, err := a.w.Write(out); err != nil {
		return 0, err
	}
	return len(p), nil
}

// escapeNonASCII 는 JSON 본문의 ASCII 가 아닌 글자를 \uXXXX 이스케이프로 바꾼다.
// JSON 에서 ASCII 가 아닌 글자는 문자열 안에만 나오므로 이 바꿈은 JSON 의 뜻을 바꾸지 않는다.
func escapeNonASCII(in []byte) []byte {
	out := make([]byte, 0, len(in))
	for i := 0; i < len(in); {
		if in[i] < 0x80 {
			out = append(out, in[i])
			i++
			continue
		}
		r, size := utf8.DecodeRune(in[i:])
		i += size
		if r > 0xFFFF {
			r1, r2 := utf16.EncodeRune(r)
			out = append(out, fmt.Sprintf(`\u%04x\u%04x`, r1, r2)...)
			continue
		}
		out = append(out, fmt.Sprintf(`\u%04x`, r)...)
	}
	return out
}

// printJSON 은 API 의 원본 JSON 을 들여쓰기해서 표준 출력에 쓴다. 비 ASCII 글자는 이스케이프한다.
func (c *cli) printJSON(raw []byte) int {
	var buf bytes.Buffer
	if err := json.Indent(&buf, bytes.TrimSpace(raw), "", "  "); err != nil {
		return c.fail(fmt.Errorf("response is not JSON: %w", err))
	}
	out := append(escapeNonASCII(buf.Bytes()), '\n')
	if _, err := c.env.stdout.Write(out); err != nil {
		return exitFailure
	}
	return exitOK
}

// writeTable 은 머리글과 행을 열 단위로 맞춰 쓴다. 셀 안의 탭과 줄바꿈은 공백으로 바꾼다.
func writeTable(w io.Writer, header []string, rows [][]string) {
	clean := strings.NewReplacer("\t", " ", "\n", " ", "\r", " ")
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, joinCells(header, clean))
	for _, r := range rows {
		fmt.Fprintln(tw, joinCells(r, clean))
	}
	_ = tw.Flush()
}

// joinCells 는 셀을 탭으로 이어 붙인다.
func joinCells(cells []string, clean *strings.Replacer) string {
	parts := make([]string, len(cells))
	for i, c := range cells {
		parts[i] = clean.Replace(c)
	}
	return strings.Join(parts, "\t")
}

// kv 는 "label: value" 한 줄을 쓴다. label 에 앞쪽 들여쓰기를 넣을 수 있다.
func kv(w io.Writer, label, value string) {
	fmt.Fprintf(w, "%-18s %s\n", label+":", value)
}

// humanBytes 는 바이트 수를 사람이 읽는 단위로 바꾼다. 음수는 unknown 이다.
func humanBytes(n int64) string {
	switch {
	case n < 0:
		return "unknown"
	case n < 1<<10:
		return fmt.Sprintf("%d B", n)
	case n < 1<<20:
		return fmt.Sprintf("%.1f KiB", float64(n)/(1<<10))
	case n < 1<<30:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1<<20))
	}
	return fmt.Sprintf("%.1f GiB", float64(n)/(1<<30))
}

// humanMiB 는 MiB 수를 사람이 읽는 단위로 바꾼다. 음수는 unknown 이다.
func humanMiB(mib int64) string {
	if mib < 0 {
		return "unknown"
	}
	return humanBytes(mib << 20)
}

// yesNo 는 불리언을 yes 나 no 로 바꾼다.
func yesNo(v bool) string {
	if v {
		return "yes"
	}
	return "no"
}

// orDash 는 빈 값을 "-" 로 바꾼다.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// orUnknown 는 빈 값을 "unknown" 으로 바꾼다.
func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// topUsage 는 전체 도움말 본문이다.
const topUsage = `Usage: lightlix [--socket PATH] [--json] <command> [arguments]

Commands:
  status                      show version, mode, GPU, memory, and server state
  gpu check                   run the GPU check again and say what to do
  model list                  list installed models
  model pull <repo>           download a GGUF model from Hugging Face
  model rm <id>               delete an installed model
  model search <query>        search Hugging Face for GGUF models
  model files <repo>          list the GGUF files in a repository
  model default <id>          set the default model
  serve start [model]         start llama-server (shows the plan first)
  serve stop                  stop llama-server
  serve plan [model]          show the plan without starting
  serve logs                  show recent llama-server log lines
  mode [server|desktop]       show or set the mode
  update                      update Lightlix (not implemented yet)
  report                      write a diagnostic report
  apikey create [label]       create an API key (shown once)
  apikey list                 list API keys
  apikey rm <id>              delete an API key
  ssh [on|off]                show or change the SSH server
  user add <name>             create a login account (prompts for a password)
  user list                   list accounts created with Lightlix
  user rm <name>              delete an account (the home directory is kept)
  config show                 show the daemon configuration
  config set <name> <value>   change port, lan, autostart, or hf-token
  chat <prompt...>            send one prompt to the running server
  version                     show the CLI version
  help [command]              show help for a command

Global flags:
  --socket PATH   lightlixd socket (default /run/lightlix/lightlixd.sock,
                  or the LIGHTLIX_SOCKET environment variable)
  --json          print the API JSON as is instead of text

Global flags go before the command. Command flags may follow the arguments.
`

// writeTopUsage 는 전체 도움말을 쓴다.
func writeTopUsage(w io.Writer) {
	fmt.Fprint(w, topUsage)
}
