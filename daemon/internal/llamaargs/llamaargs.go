// Package llamaargs는 autoconf 계산 결과를 llama-server v0.6.0의 환경 변수와
// systemd EnvironmentFile 텍스트로 바꾸고, llama-server의 출력 줄을 해석한다.
//
// 환경 변수 이름은 llama.cpp v0.6.0의 common/arg.cpp에 있는 set_env("LLAMA_ARG_...") 호출에서 확인했다.
// 확인 근거는 각 상수 주석에 적었다. 명령줄 옵션은 쓰지 않는다. 키 파일 경로만 환경 변수로 넘기고,
// API 키 평문은 명령줄이나 환경 변수에 들어가지 않는다.
package llamaargs

import (
	"errors"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/nxodh/lightlixos/daemon/internal/autoconf"
)

// llama-server 환경 변수 이름이다. 모두 llama.cpp v0.6.0 common/arg.cpp의 set_env 호출에서 확인했다.
const (
	envModel       = "LLAMA_ARG_MODEL"            // arg.cpp:3045, -m/--model
	envHost        = "LLAMA_ARG_HOST"             // arg.cpp:3338, --host
	envPort        = "LLAMA_ARG_PORT"             // arg.cpp:3345, --port
	envAlias       = "LLAMA_ARG_ALIAS"            // arg.cpp:3024, -a/--alias
	envAPIKeyFile  = "LLAMA_ARG_API_KEY_FILE"     // arg.cpp:3528, --api-key-file
	envMetrics     = "LLAMA_ARG_ENDPOINT_METRICS" // arg.cpp:3607, --metrics
	envNGL         = "LLAMA_ARG_N_GPU_LAYERS"     // arg.cpp:2808, -ngl
	envCtx         = "LLAMA_ARG_CTX_SIZE"         // arg.cpp:1651, -c/--ctx-size
	envBatch       = "LLAMA_ARG_BATCH"            // arg.cpp:1677, -b/--batch-size
	envCacheTypeK  = "LLAMA_ARG_CACHE_TYPE_K"     // arg.cpp:2451, -ctk
	envCacheTypeV  = "LLAMA_ARG_CACHE_TYPE_V"     // arg.cpp:2464, -ctv
	envFlashAttn   = "LLAMA_ARG_FLASH_ATTN"       // arg.cpp:1771, -fa/--flash-attn [on|off|auto]
	envTensorSplit = "LLAMA_ARG_TENSOR_SPLIT"     // arg.cpp:2859, -ts/--tensor-split
	envLoadMode    = "LLAMA_ARG_LOAD_MODE"        // arg.cpp:2711, -lm/--load-mode
	envFit         = "LLAMA_ARG_FIT"              // arg.cpp:2883, -fit/--fit [on|off]

	// CUDA 런타임 변수다. llama.cpp 소스에는 나오지 않는다.
	// NVIDIA CUDA 문서 5.2.1.1, 5.2.1.2의 CUDA_VISIBLE_DEVICES와 CUDA_DEVICE_ORDER 절에서 확인했다.
	envCUDAVisibleDevices = "CUDA_VISIBLE_DEVICES"
	envCUDADeviceOrder    = "CUDA_DEVICE_ORDER"
)

// ServerSettings는 llama-server 실행에 필요한 사용자 설정이다.
type ServerSettings struct {
	ModelPath  string // 절대 경로
	Alias      string // 비어 있으면 넘기지 않는다
	Host       string // 쉼표로 여러 주소를 줄 수 있다
	Port       int
	APIKeyFile string // 절대 경로, 비어 있으면 넘기지 않는다
	Metrics    bool   // true이면 /metrics 엔드포인트를 연다
}

// Device는 --list-devices가 출력한 장치 한 줄이다.
type Device struct {
	Name        string // 예: CUDA0
	Description string
	TotalMiB    int64
	FreeMiB     int64
}

// Env는 계획과 서버 설정을 llama-server 환경 변수 맵으로 바꾼다.
// 값이 잘못되었거나 LAN 공개인데 API 키 파일이 없으면 오류를 돌려준다.
func Env(p autoconf.Output, s ServerSettings) (map[string]string, error) {
	if err := checkSettings(s); err != nil {
		return nil, fmt.Errorf("llamaargs: env: %w", err)
	}
	if err := checkPlan(p); err != nil {
		return nil, fmt.Errorf("llamaargs: env: %w", err)
	}

	visible := ""
	if !p.CPUOnly {
		parts := make([]string, len(p.VisibleDevices))
		for i, idx := range p.VisibleDevices {
			parts[i] = strconv.Itoa(idx)
		}
		visible = strings.Join(parts, ",")
	}

	env := map[string]string{
		envModel:              s.ModelPath,
		envHost:               s.Host,
		envPort:               strconv.Itoa(s.Port),
		envNGL:                p.NGL,
		envCtx:                strconv.Itoa(p.Ctx),
		envBatch:              strconv.Itoa(p.Batch),
		envCacheTypeK:         p.CacheTypeK,
		envCacheTypeV:         p.CacheTypeV,
		envFlashAttn:          onOff(p.FlashAttn),
		envLoadMode:           p.LoadMode,
		envFit:                onOff(p.Fit),
		envCUDADeviceOrder:    "PCI_BUS_ID",
		envCUDAVisibleDevices: visible,
	}
	if s.Alias != "" {
		env[envAlias] = s.Alias
	}
	if s.APIKeyFile != "" {
		env[envAPIKeyFile] = s.APIKeyFile
	}
	if s.Metrics {
		// 플래그형 옵션은 truthy 값("1", "on", "true", "enabled")이어야 켜진다(arg.cpp:790).
		env[envMetrics] = "1"
	}
	if p.TensorSplit != "" {
		env[envTensorSplit] = p.TensorSplit
	}
	return env, nil
}

// EnvFile은 환경 변수 맵을 systemd EnvironmentFile= 형식 텍스트로 바꾼다. 키는 정렬한다.
// 값은 큰따옴표로 감싸고 \ " ` $ 앞에 백슬래시를 붙인다. 규칙은 systemd.exec(5) EnvironmentFile 절을 따른다.
// 개행과 제어 문자는 표현할 수 없으므로 Env가 이미 거부한 값만 넣어야 한다.
func EnvFile(env map[string]string) string {
	keys := make([]string, 0, len(env))
	for k := range env {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var b strings.Builder
	for _, k := range keys {
		b.WriteString(k)
		b.WriteByte('=')
		b.WriteString(quote(env[k]))
		b.WriteByte('\n')
	}
	return b.String()
}

// quote는 한 값을 EnvironmentFile의 큰따옴표 형식으로 감싼다.
// systemd.exec(5)는 큰따옴표 안에서 \ 다음의 "\`$ 문자를 그대로 보존한다고 적는다.
// 다른 문자 앞의 백슬래시는 무시되므로 이스케이프 대상만 백슬래시를 붙인다.
func quote(v string) string {
	var b strings.Builder
	b.Grow(len(v) + 2)
	b.WriteByte('"')
	for i := 0; i < len(v); i++ {
		c := v[i]
		switch c {
		case '\\', '"', '`', '$':
			b.WriteByte('\\')
		}
		b.WriteByte(c)
	}
	b.WriteByte('"')
	return b.String()
}

// checkSettings는 서버 설정을 검사한다. LAN 공개(loopback이 아닌 주소)에는 API 키 파일이 필수다.
func checkSettings(s ServerSettings) error {
	if !path.IsAbs(s.ModelPath) {
		return fmt.Errorf("model path %q must be absolute", s.ModelPath)
	}
	if err := checkEnvValue("model path", s.ModelPath); err != nil {
		return err
	}
	if s.Port < 1 || s.Port > 65535 {
		return fmt.Errorf("port %d is out of range 1-65535", s.Port)
	}
	if err := checkEnvValue("alias", s.Alias); err != nil {
		return err
	}
	if s.APIKeyFile != "" && !path.IsAbs(s.APIKeyFile) {
		return fmt.Errorf("API key file %q must be absolute", s.APIKeyFile)
	}
	if err := checkEnvValue("API key file", s.APIKeyFile); err != nil {
		return err
	}
	return checkHost(s.Host, s.APIKeyFile)
}

// checkHost는 --host 값을 검사한다. llama-server처럼 쉼표로 나누고 앞뒤 공백을 자르며 빈 항목은 무시한다.
// loopback이 아닌 주소가 하나라도 있으면 API 키 파일을 요구한다.
func checkHost(host, apiKeyFile string) error {
	if err := checkEnvValue("host", host); err != nil {
		return err
	}
	count := 0
	for _, h := range strings.Split(host, ",") {
		h = strings.TrimSpace(h)
		if h == "" {
			continue
		}
		count++
		if isLoopback(h) {
			continue
		}
		if apiKeyFile == "" {
			return fmt.Errorf("host %q is not loopback; LAN exposure requires an API key file", h)
		}
	}
	if count == 0 {
		return errors.New("host is empty")
	}
	return nil
}

// isLoopback은 127.0.0.1, ::1, localhost만 loopback으로 본다. 다른 주소와 UNIX 소켓은 LAN 공개로 취급한다.
func isLoopback(h string) bool {
	switch h {
	case "127.0.0.1", "::1", "localhost":
		return true
	}
	return false
}

// checkPlan은 계획 결과가 환경 변수로 쓸 수 있는 값인지 검사한다.
func checkPlan(p autoconf.Output) error {
	ngl, ok := autoconf.ParseNGL(p.NGL)
	if !ok || ngl != p.NGL {
		return fmt.Errorf("invalid NGL %q", p.NGL)
	}
	if p.CPUOnly && p.NGL != "0" {
		return fmt.Errorf("CPU-only plan must use NGL 0, got %q", p.NGL)
	}
	if p.Ctx <= 0 {
		return fmt.Errorf("context size %d must be positive", p.Ctx)
	}
	if p.Batch <= 0 {
		return fmt.Errorf("batch size %d must be positive", p.Batch)
	}
	if !autoconf.ValidCacheType(p.CacheTypeK) || !autoconf.ValidCacheType(p.CacheTypeV) {
		return fmt.Errorf("invalid KV cache types %q/%q", p.CacheTypeK, p.CacheTypeV)
	}
	if p.CacheTypeK != "f16" || p.CacheTypeV != "f16" {
		if !p.FlashAttn {
			return errors.New("quantized KV cache requires flash attention")
		}
	}
	if !autoconf.ValidLoadMode(p.LoadMode) {
		return fmt.Errorf("invalid load mode %q", p.LoadMode)
	}
	if p.CPUOnly && len(p.VisibleDevices) > 0 {
		return errors.New("CPU-only plan must not list visible devices")
	}
	if !p.CPUOnly && len(p.VisibleDevices) == 0 {
		return errors.New("GPU plan lists no visible devices")
	}
	return checkEnvValue("tensor split", p.TensorSplit)
}

// checkEnvValue는 EnvironmentFile에 쓸 수 있는 문자열인지 본다.
// 개행과 제어 문자, U+FEFF, 잘못된 UTF-8은 거부한다.
func checkEnvValue(what, v string) error {
	if !utf8.ValidString(v) {
		return fmt.Errorf("%s is not valid UTF-8", what)
	}
	for _, r := range v {
		if r < 0x20 || r == 0x7f || r == 0xfeff {
			return fmt.Errorf("%s contains a control character or U+FEFF", what)
		}
	}
	return nil
}

// onOff는 bool을 llama-server의 on/off 값으로 바꾼다.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

var offloadRe = regexp.MustCompile(`offloaded (\d+)/(\d+) layers to GPU`)

// ParseOffload는 llama-server 로그에서 "offloaded N/M layers to GPU" 줄을 찾는다.
// src/llama-model.cpp v0.6.0의 load_tensors 로그 형식이다. 여러 번 나오면 마지막 값을 쓴다.
func ParseOffload(log string) (offloaded, total int, ok bool) {
	m := offloadRe.FindAllStringSubmatch(log, -1)
	if len(m) == 0 {
		return 0, 0, false
	}
	last := m[len(m)-1]
	o, err1 := strconv.Atoi(last[1])
	t, err2 := strconv.Atoi(last[2])
	if err1 != nil || err2 != nil {
		return 0, 0, false
	}
	return o, t, true
}

var deviceRe = regexp.MustCompile(`^\s+(\S+): (.+) \((\d+) MiB, (\d+) MiB free\)\s*$`)

// ParseListDevices는 llama-server --list-devices 출력을 해석한다.
// 출력 형식은 common/arg.cpp의 common_print_available_devices다:
// "Available devices:" 다음에 "  <name>: <description> (<total> MiB, <free> MiB free)" 줄들이 오고,
// 장치가 없으면 "  (none)" 한 줄이 온다. (none)이나 형식이 다른 줄은 건너뛴다.
func ParseListDevices(out string) []Device {
	var devs []Device
	inList := false
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r")
		if !inList {
			if strings.TrimSpace(line) == "Available devices:" {
				inList = true
			}
			continue
		}
		m := deviceRe.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		total, err1 := strconv.ParseInt(m[3], 10, 64)
		free, err2 := strconv.ParseInt(m[4], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		devs = append(devs, Device{Name: m[1], Description: m[2], TotalMiB: total, FreeMiB: free})
	}
	return devs
}

// HasNoGPUWarning은 stderr에 GPU 백엔드가 없다는 경고가 있는지 본다.
// common/arg.cpp v0.6.0이 "warning: no usable GPU found, --gpu-layers option will be ignored"를 낸다.
// 이 경고가 나오면 GPU를 쓰지 못한 것이므로 데몬은 실패로 취급해야 한다.
func HasNoGPUWarning(stderr string) bool {
	return strings.Contains(stderr, "warning: no usable GPU found")
}
