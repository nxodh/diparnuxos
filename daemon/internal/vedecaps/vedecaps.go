// Package vedecaps는 Opereta 모델 서버를 코딩 에이전트 Vedecaps 에 연결하는 설정을 만든다.
//
// Vedecaps 는 llama.cpp 서버를 127.0.0.1:8080 에서 키 없이 스스로 찾는다. 그래도 설정 조각을 두는 이유는 셋이다.
// 키가 필요한 원격 접속과 슬롯당 컨텍스트(limit.context)를 정확히 알려 주는 것이다.
// 도구 호출(tool_call)은 Vedecaps 가 기본으로 켜 두므로 적지 않는다. 실제 동작은 `opereta serve toolcheck` 로 확인한다.
// 설정 형식은 Vedecaps 의 config.schema.json 의 provider 절을 따른다(additionalProperties 가 false 라서 모르는 키는 넣지 않는다).
package vedecaps

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// ProviderID 는 Vedecaps 설정에서 Opereta 서버를 가리키는 제공자 이름이다.
const ProviderID = "opereta"

// DefaultKeyEnv 는 원격 접속에서 API 키를 읽을 환경 변수 이름이다. 키 값은 설정 파일에 쓰지 않는다.
const DefaultKeyEnv = "OPERETA_API_KEY"

// DefaultCAFile 은 다른 컴퓨터의 Vedecaps 설정이 가리킬 CA 인증서 경로의 기본값이다. 파일은 사용자가 그 컴퓨터에 둔다
// (`opereta tls ca --out`). 설정 파일에는 경로만 쓴다.
const DefaultCAFile = "~/.config/vedecaps/opereta-ca.pem"

// defaultOutput 은 응답 최대 길이로 알려 줄 값이다. 슬롯 컨텍스트보다 작게 잡는다.
const defaultOutput = 4096

// Input 은 설정 조각을 만드는 데 필요한 값이다.
type Input struct {
	// BaseURL 은 Vedecaps 가 접속할 주소(.../v1)다.
	BaseURL string
	// ModelID 는 서버가 내는 모델 이름이다. 비어 있으면 모델을 적지 않고 Vedecaps 가 서버에서 가져오게 한다.
	ModelID string
	// SlotCtx 는 요청 하나가 쓸 수 있는 컨텍스트(토큰)다. 0 이면 모른다.
	SlotCtx int
	// NeedsKey 는 이 주소로 접속할 때 API 키가 필요한지다.
	NeedsKey bool
	// KeyEnv 는 키를 읽을 환경 변수 이름이다. 비어 있으면 DefaultKeyEnv 다.
	KeyEnv string
	// CAFile 은 https 주소의 서버 인증서를 믿게 할 CA 인증서(PEM) 경로다. 비어 있으면 쓰지 않는다. Vedecaps 가
	// options.ca 를 지원하는 버전(0.1.5 이상)에서 쓰이고, 모르는 버전은 이 키를 무시한다.
	CAFile string
	// Packaged 가 true 이면 Opereta 패키지가 /usr/bin 에 둔 Vedecaps 다. 사용자가 이 파일을 바꿀 수 없으므로
	// 자체 업데이트 확인을 끈다(업데이트는 Opereta 업데이트로 온다).
	Packaged bool
	// MCP 가 true 이면 `opereta mcp` 를 Vedecaps 의 MCP 서버로 등록하는 조각도 넣는다.
	MCP bool
}

// Fragment 는 Vedecaps 전역 설정에 합칠 조각을 돌려준다.
func Fragment(in Input) (map[string]any, error) {
	if !strings.HasPrefix(in.BaseURL, "http://") && !strings.HasPrefix(in.BaseURL, "https://") {
		return nil, errors.New("vedecaps: the base URL must start with http:// or https://")
	}
	if strings.ContainsAny(in.BaseURL, " \t\r\n{}") {
		return nil, errors.New("vedecaps: the base URL has characters that are not allowed")
	}
	if strings.ContainsAny(in.ModelID, "{}/\\\r\n") {
		return nil, errors.New("vedecaps: the model name has characters that are not allowed")
	}
	provider := map[string]any{
		"name":     "Opereta",
		"protocol": "openai",
		"api":      strings.TrimRight(in.BaseURL, "/"),
	}
	if in.NeedsKey {
		env := in.KeyEnv
		if env == "" {
			env = DefaultKeyEnv
		}
		if !validEnvName(env) {
			return nil, fmt.Errorf("vedecaps: %q is not a valid environment variable name", env)
		}
		// 키 값은 쓰지 않는다. Vedecaps 가 시작할 때 환경 변수에서 읽는다.
		provider["options"] = map[string]any{"apiKey": "{env:" + env + "}"}
	}
	if in.CAFile != "" {
		if !strings.HasPrefix(in.BaseURL, "https://") {
			return nil, errors.New("vedecaps: a CA file only makes sense for an https:// address")
		}
		if strings.ContainsAny(in.CAFile, "\r\n{}") {
			return nil, errors.New("vedecaps: the CA file path has characters that are not allowed")
		}
		opts, _ := provider["options"].(map[string]any)
		if opts == nil {
			opts = map[string]any{}
			provider["options"] = opts
		}
		opts["ca"] = in.CAFile
	}
	frag := map[string]any{"provider": map[string]any{ProviderID: provider}}
	if in.Packaged {
		frag["autoupdate"] = false
	}
	if in.MCP {
		// 읽기 전용 도구만 내는 기본 모드다. 서버를 바꾸는 도구(--allow-control)는 설정 조각에 넣지 않는다.
		frag["mcp"] = map[string]any{ProviderID: map[string]any{
			"type": "local", "command": []string{"opereta", "mcp"}, "enabled": true,
		}}
	}
	if in.ModelID != "" {
		model := map[string]any{"name": in.ModelID}
		limit := map[string]any{}
		if in.SlotCtx > 0 {
			limit["context"] = in.SlotCtx
			out := defaultOutput
			if in.SlotCtx/4 < out {
				out = in.SlotCtx / 4
			}
			if out > 0 {
				limit["output"] = out
			}
		}
		if len(limit) > 0 {
			model["limit"] = limit
		}
		provider["models"] = map[string]any{in.ModelID: model}
		frag["model"] = ProviderID + "/" + in.ModelID
	}
	return frag, nil
}

func validEnvName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r == '_' || (r >= 'A' && r <= 'Z') || (r >= 'a' && r <= 'z'):
		case r >= '0' && r <= '9' && i > 0:
		default:
			return false
		}
	}
	return true
}

// Pretty 는 조각을 들여쓴 JSON 으로 바꾼다.
func Pretty(v any) string {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return "{}"
	}
	return string(b) + "\n"
}

// ErrNeedsManualMerge 는 기존 설정 파일을 안전하게 고쳐 쓸 수 없을 때(주석이 있거나 JSON 이 아님) 돌려준다.
var ErrNeedsManualMerge = errors.New("vedecaps: the existing config is not plain JSON; merge the fragment by hand")

// MergeOptions 는 합치는 방법이다.
type MergeOptions struct {
	// MakeDefault 가 true 이면 이미 다른 기본 모델이 있어도 Opereta 로 바꾼다. false 이면 기본 모델이 없을 때만 넣는다.
	MakeDefault bool
}

// MergeResult 는 합친 결과와 사용자에게 알릴 내용이다.
type MergeResult struct {
	Data  []byte
	Notes []string
}

// Merge 는 기존 설정(비어 있을 수 있다)에 조각을 합친다. 합치는 곳은 provider.opereta 와 model 뿐이다.
// 다른 제공자와 나머지 설정은 건드리지 않고, 기존 provider.opereta 는 통째로 바꾼다.
func Merge(existing []byte, frag map[string]any, opt MergeOptions) (MergeResult, error) {
	root := map[string]any{}
	if len(bytes.TrimSpace(existing)) > 0 {
		dec := json.NewDecoder(bytes.NewReader(existing))
		dec.UseNumber()
		if err := dec.Decode(&root); err != nil || root == nil {
			return MergeResult{}, ErrNeedsManualMerge
		}
		if dec.More() {
			return MergeResult{}, ErrNeedsManualMerge
		}
	}
	var res MergeResult
	providers, _ := root["provider"].(map[string]any)
	if root["provider"] != nil && providers == nil {
		return MergeResult{}, errors.New("vedecaps: the existing \"provider\" is not an object")
	}
	if providers == nil {
		providers = map[string]any{}
	}
	fragProviders, _ := frag["provider"].(map[string]any)
	if _, had := providers[ProviderID]; had {
		res.Notes = append(res.Notes, "replaced the existing provider \""+ProviderID+"\"")
	} else {
		res.Notes = append(res.Notes, "added the provider \""+ProviderID+"\"")
	}
	providers[ProviderID] = fragProviders[ProviderID]
	root["provider"] = providers

	if au, ok := frag["autoupdate"]; ok {
		// 사용자가 이미 정한 값은 바꾸지 않는다.
		if _, set := root["autoupdate"]; !set {
			root["autoupdate"] = au
			res.Notes = append(res.Notes, "turned off Vedecaps self-update (updates come with Opereta updates)")
		}
	}
	if fragMCP, ok := frag["mcp"].(map[string]any); ok {
		mcp, _ := root["mcp"].(map[string]any)
		if root["mcp"] != nil && mcp == nil {
			return MergeResult{}, errors.New("vedecaps: the existing \"mcp\" is not an object")
		}
		if mcp == nil {
			mcp = map[string]any{}
		}
		if _, had := mcp[ProviderID]; had {
			res.Notes = append(res.Notes, "replaced the existing MCP server \""+ProviderID+"\"")
		} else {
			res.Notes = append(res.Notes, "added the MCP server \""+ProviderID+"\" (read-only tools)")
		}
		mcp[ProviderID] = fragMCP[ProviderID]
		root["mcp"] = mcp
	}

	if want, ok := frag["model"].(string); ok {
		cur, _ := root["model"].(string)
		switch {
		case cur == "" || opt.MakeDefault:
			root["model"] = want
			res.Notes = append(res.Notes, "default model set to "+want)
		case cur == want:
		default:
			res.Notes = append(res.Notes, "kept the default model "+cur+" (use --make-default to switch to "+want+")")
		}
	}
	out, err := marshalSorted(root)
	if err != nil {
		return MergeResult{}, err
	}
	res.Data = out
	return res, nil
}

// marshalSorted 는 키를 정렬해 들여쓴 JSON 으로 쓴다(encoding/json 은 map 키를 정렬한다).
func marshalSorted(v map[string]any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// ConfigDir 는 Vedecaps 전역 설정 폴더다. XDG_CONFIG_HOME 이 있으면 그 아래, 없으면 ~/.config 아래의 vedecaps 다.
func ConfigDir(home, xdg string) string {
	if xdg != "" && filepath.IsAbs(xdg) {
		return filepath.Join(xdg, "vedecaps")
	}
	return filepath.Join(home, ".config", "vedecaps")
}

// ConfigFile 은 합친 설정을 쓸 파일이다. Vedecaps 의 globalConfigFile() 과 같다.
func ConfigFile(home, xdg string) string {
	return filepath.Join(ConfigDir(home, xdg), "vedecaps.json")
}

// Providers 는 시험에서 쓴다: 파일 안의 제공자 이름을 정렬해 돌려준다.
func Providers(data []byte) []string {
	var root struct {
		Provider map[string]json.RawMessage `json:"provider"`
	}
	if json.Unmarshal(data, &root) != nil {
		return nil
	}
	names := make([]string, 0, len(root.Provider))
	for k := range root.Provider {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}
