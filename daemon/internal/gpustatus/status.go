// Package gpustatus reads the GPU diagnosis state file that opereta-gpu-check writes.
//
// The file is /run/opereta/gpu-status by default. Its format and exit-code contract
// are defined in docs/drivers.md, section "GPU 진단 계약". The file is a sequence of
// KEY=VALUE lines that a shell can source. A value is either bare or wrapped in double
// quotes. Values are taken literally: no shell expansion is performed.
package gpustatus

import (
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// State is the diagnosis result written to OPERETA_GPU_STATUS.
type State string

// The states that opereta-gpu-check can write.
const (
	StateReady             State = "ready"
	StateNoGPU             State = "no_gpu"
	StateModuleUnloaded    State = "module_unloaded"
	StateSecureBoot        State = "secure_boot"
	StateProprietaryModule State = "proprietary_module"
	StateVersionMismatch   State = "version_mismatch"
	StateDriverMissing     State = "driver_missing"
	StateQueryFailed       State = "query_failed"
	StateUnsupportedGPU    State = "unsupported_gpu"
	StateConfigMissing     State = "config_missing"
	StateInternalError     State = "internal_error"
)

// Limits that bound the work done on untrusted file contents.
const (
	maxFileSize = 64 << 10 // Largest status file accepted, in bytes.
	maxLineSize = 4 << 10  // Longest single line accepted, in bytes.
	maxDevices  = 256      // Upper bound for PCI device counts and GPU indexes.
)

// Status is the parsed content of the GPU status file.
type Status struct {
	// State is the diagnosis result. Only StateReady allows GPU inference.
	State State
	// Present reports whether an NVIDIA display device was found on the PCI bus.
	Present bool
	// PCICount is the number of NVIDIA PCI display devices.
	PCICount int
	// Count is the number of GPUs that nvidia-smi reported.
	Count int
	// UsableCount is the number of usable GPUs. It is greater than zero only when State is StateReady.
	UsableCount int
	// UsableIndexes lists the nvidia-smi indexes of the usable GPUs.
	UsableIndexes []int
	// SecureBoot is "enabled", "disabled", or "unknown".
	SecureBoot string
	// DriverVersion is the loaded module version. It is empty when unknown.
	DriverVersion string
	// Message is the one-line summary shown on the console.
	Message string
	// GPUs describes each GPU that nvidia-smi reported, in ascending index order.
	GPUs []GPU
}

// GPU describes one GPU from the status file.
type GPU struct {
	// Index is the nvidia-smi index.
	Index int
	// Name is the product name.
	Name string
	// ComputeCap is the compute capability, such as "8.6".
	ComputeCap string
	// Supported reports whether the GPU is one of the supported targets.
	Supported bool
	// Reason is "ok", "compute_cap", or "name".
	Reason string
}

// Usable reports whether GPU inference may be attempted: the state is ready and at least one GPU is usable.
func (s Status) Usable() bool {
	return s.State == StateReady && s.UsableCount > 0
}

// Load reads and parses the status file at path.
//
// If the file does not exist, the returned error wraps os.ErrNotExist, so callers can tell
// "diagnosis has not run yet" apart from a broken file.
func Load(path string) (Status, error) {
	f, err := os.Open(path)
	if err != nil {
		return Status{}, fmt.Errorf("gpustatus: open %s: %w", path, err)
	}
	defer f.Close()

	s, err := Parse(f)
	if err != nil {
		return Status{}, fmt.Errorf("gpustatus: parse %s: %w", path, err)
	}
	return s, nil
}

// Parse reads a status file from r and validates it.
//
// Unknown keys are ignored. A malformed line, a missing required key, a duplicate known key,
// a negative or out-of-range number, or contradictory fields are errors.
func Parse(r io.Reader) (Status, error) {
	data, err := io.ReadAll(io.LimitReader(r, maxFileSize+1))
	if err != nil {
		return Status{}, fmt.Errorf("gpustatus: read: %w", err)
	}
	if len(data) > maxFileSize {
		return Status{}, fmt.Errorf("gpustatus: file is larger than %d bytes", maxFileSize)
	}

	p := newParser()
	for i, line := range strings.Split(string(data), "\n") {
		if err := p.line(line); err != nil {
			return Status{}, fmt.Errorf("gpustatus: line %d: %w", i+1, err)
		}
	}
	return p.build()
}

// keyPattern accepts shell variable names only.
var keyPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// gpuKeyPattern matches the per-GPU keys, such as OPERETA_GPU_0_NAME.
var gpuKeyPattern = regexp.MustCompile(`^OPERETA_GPU_([0-9]+)_(NAME|COMPUTE_CAP|SUPPORTED|REASON)$`)

// topKeys lists the top-level keys that the script always writes.
var topKeys = map[string]bool{
	"OPERETA_GPU_STATUS":         true,
	"OPERETA_GPU_PRESENT":        true,
	"OPERETA_GPU_PCI_COUNT":      true,
	"OPERETA_GPU_COUNT":          true,
	"OPERETA_GPU_USABLE_COUNT":   true,
	"OPERETA_GPU_USABLE_INDEXES": true,
	"OPERETA_SECURE_BOOT":        true,
	"OPERETA_DRIVER_VERSION":     true,
	"OPERETA_GPU_MESSAGE":        true,
}

// knownStates is the set of states that the script can write.
var knownStates = map[State]bool{
	StateReady: true, StateNoGPU: true, StateModuleUnloaded: true, StateSecureBoot: true,
	StateProprietaryModule: true, StateVersionMismatch: true, StateDriverMissing: true,
	StateQueryFailed: true, StateUnsupportedGPU: true, StateConfigMissing: true, StateInternalError: true,
}

// gpuFields collects the four keys of one GPU before validation.
type gpuFields struct {
	name, cap, supported, reason             string
	hasName, hasCap, hasSupported, hasReason bool
}

// parser holds the values seen so far while the file is read line by line.
type parser struct {
	top  map[string]string
	gpus map[int]*gpuFields
}

func newParser() *parser {
	return &parser{top: make(map[string]string), gpus: make(map[int]*gpuFields)}
}

// line handles one line of the file. Blank lines and comment lines are skipped.
func (p *parser) line(line string) error {
	if len(line) > maxLineSize {
		return fmt.Errorf("line is longer than %d bytes", maxLineSize)
	}
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	eq := strings.IndexByte(line, '=')
	if eq <= 0 {
		return errors.New("malformed line: expected KEY=VALUE")
	}
	key := line[:eq]
	if !keyPattern.MatchString(key) {
		return errors.New("malformed line: invalid key name")
	}

	if topKeys[key] {
		value, err := unquote(line[eq+1:])
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		if _, dup := p.top[key]; dup {
			return fmt.Errorf("duplicate key %s", key)
		}
		p.top[key] = value
		return nil
	}

	m := gpuKeyPattern.FindStringSubmatch(key)
	if m == nil {
		// Unknown key: ignored.
		return nil
	}
	value, err := unquote(line[eq+1:])
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	index, err := parseIndex(m[1])
	if err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}
	g := p.gpus[index]
	if g == nil {
		g = &gpuFields{}
		p.gpus[index] = g
	}
	switch m[2] {
	case "NAME":
		if g.hasName {
			return fmt.Errorf("duplicate key %s", key)
		}
		g.name, g.hasName = value, true
	case "COMPUTE_CAP":
		if g.hasCap {
			return fmt.Errorf("duplicate key %s", key)
		}
		g.cap, g.hasCap = value, true
	case "SUPPORTED":
		if g.hasSupported {
			return fmt.Errorf("duplicate key %s", key)
		}
		g.supported, g.hasSupported = value, true
	case "REASON":
		if g.hasReason {
			return fmt.Errorf("duplicate key %s", key)
		}
		g.reason, g.hasReason = value, true
	}
	return nil
}

// unquote returns the literal value of a shell-style bare or double-quoted value.
// Characters in the value must be printable ASCII. Double quotes and single quotes may appear
// only as the wrapping pair.
func unquote(raw string) (string, error) {
	value := raw
	if strings.HasPrefix(raw, `"`) {
		if len(raw) < 2 || !strings.HasSuffix(raw, `"`) {
			return "", errors.New("unterminated double quote")
		}
		value = raw[1 : len(raw)-1]
	} else if strings.ContainsAny(raw, ` 	"'`) {
		return "", errors.New("bare value contains a space or quote")
	}
	if strings.ContainsAny(value, `"'`) {
		return "", errors.New("value contains a quote character")
	}
	for i := 0; i < len(value); i++ {
		if value[i] < 0x20 || value[i] > 0x7e {
			return "", errors.New("value contains a non-printable or non-ASCII byte")
		}
	}
	return value, nil
}

// parseCount parses a non-negative decimal count that must not exceed maxDevices.
func parseCount(key, s string) (int, error) {
	n, err := parseDecimal(s, maxDevices)
	if err != nil {
		return 0, fmt.Errorf("invalid %s %q: %w", key, s, err)
	}
	return n, nil
}

// parseIndex parses a GPU index in the range [0, maxDevices).
func parseIndex(s string) (int, error) {
	n, err := parseDecimal(s, maxDevices-1)
	if err != nil {
		return 0, fmt.Errorf("invalid GPU index %q: %w", s, err)
	}
	return n, nil
}

// parseDecimal accepts only ASCII digits, so signs and spaces are rejected.
func parseDecimal(s string, limit int) (int, error) {
	if s == "" || len(s) > 10 {
		return 0, errors.New("expected a decimal number")
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return 0, errors.New("expected a non-negative decimal number")
		}
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0, err
	}
	if n > limit {
		return 0, fmt.Errorf("value exceeds %d", limit)
	}
	return n, nil
}

// build checks the required keys and the cross-field contract, then returns the Status.
func (p *parser) build() (Status, error) {
	for key := range topKeys {
		if _, ok := p.top[key]; !ok {
			return Status{}, fmt.Errorf("missing key %s", key)
		}
	}

	var s Status
	s.State = State(p.top["OPERETA_GPU_STATUS"])
	if !knownStates[s.State] {
		return Status{}, fmt.Errorf("unknown state %q", p.top["OPERETA_GPU_STATUS"])
	}

	switch p.top["OPERETA_GPU_PRESENT"] {
	case "0":
		s.Present = false
	case "1":
		s.Present = true
	default:
		return Status{}, fmt.Errorf("invalid OPERETA_GPU_PRESENT %q", p.top["OPERETA_GPU_PRESENT"])
	}

	var err error
	if s.PCICount, err = parseCount("OPERETA_GPU_PCI_COUNT", p.top["OPERETA_GPU_PCI_COUNT"]); err != nil {
		return Status{}, err
	}
	if s.Count, err = parseCount("OPERETA_GPU_COUNT", p.top["OPERETA_GPU_COUNT"]); err != nil {
		return Status{}, err
	}
	if s.UsableCount, err = parseCount("OPERETA_GPU_USABLE_COUNT", p.top["OPERETA_GPU_USABLE_COUNT"]); err != nil {
		return Status{}, err
	}

	if raw := p.top["OPERETA_GPU_USABLE_INDEXES"]; raw != "" {
		seen := make(map[int]bool)
		for _, part := range strings.Split(raw, ",") {
			idx, err := parseIndex(part)
			if err != nil {
				return Status{}, fmt.Errorf("OPERETA_GPU_USABLE_INDEXES: %w", err)
			}
			if seen[idx] {
				return Status{}, fmt.Errorf("OPERETA_GPU_USABLE_INDEXES repeats index %d", idx)
			}
			seen[idx] = true
			s.UsableIndexes = append(s.UsableIndexes, idx)
		}
	}

	switch sb := p.top["OPERETA_SECURE_BOOT"]; sb {
	case "enabled", "disabled", "unknown":
		s.SecureBoot = sb
	default:
		return Status{}, fmt.Errorf("invalid OPERETA_SECURE_BOOT %q", sb)
	}
	s.DriverVersion = p.top["OPERETA_DRIVER_VERSION"]
	s.Message = p.top["OPERETA_GPU_MESSAGE"]

	for idx, g := range p.gpus {
		gpu, err := buildGPU(idx, g)
		if err != nil {
			return Status{}, err
		}
		s.GPUs = append(s.GPUs, gpu)
	}
	sort.Slice(s.GPUs, func(i, j int) bool { return s.GPUs[i].Index < s.GPUs[j].Index })

	if err := checkConsistency(s); err != nil {
		return Status{}, err
	}
	return s, nil
}

// buildGPU converts the collected keys of one GPU into a GPU value.
func buildGPU(idx int, g *gpuFields) (GPU, error) {
	if !(g.hasName && g.hasCap && g.hasSupported && g.hasReason) {
		return GPU{}, fmt.Errorf("GPU %d has an incomplete set of keys", idx)
	}
	var supported bool
	switch g.supported {
	case "0":
		supported = false
	case "1":
		supported = true
	default:
		return GPU{}, fmt.Errorf("invalid OPERETA_GPU_%d_SUPPORTED %q", idx, g.supported)
	}
	switch g.reason {
	case "ok", "compute_cap", "name":
	default:
		return GPU{}, fmt.Errorf("invalid OPERETA_GPU_%d_REASON %q", idx, g.reason)
	}
	if supported != (g.reason == "ok") {
		return GPU{}, fmt.Errorf("GPU %d: SUPPORTED and REASON disagree", idx)
	}
	if g.cap == "" {
		return GPU{}, fmt.Errorf("GPU %d has an empty compute capability", idx)
	}
	return GPU{Index: idx, Name: g.name, ComputeCap: g.cap, Supported: supported, Reason: g.reason}, nil
}

// checkConsistency enforces the cross-field rules of the contract.
func checkConsistency(s Status) error {
	if s.Present != (s.PCICount > 0) {
		return errors.New("OPERETA_GPU_PRESENT disagrees with OPERETA_GPU_PCI_COUNT")
	}
	if s.Count != len(s.GPUs) {
		return fmt.Errorf("OPERETA_GPU_COUNT is %d but %d GPU entries were written", s.Count, len(s.GPUs))
	}
	if s.UsableCount > 0 && s.State != StateReady {
		return fmt.Errorf("usable GPU count is %d but state is %s", s.UsableCount, s.State)
	}
	if s.State == StateReady && s.UsableCount == 0 {
		return errors.New("state is ready but no GPU is usable")
	}

	// The script can record supported GPUs and then reset the usable count when a later
	// module load fails. So the usable count may be lower than the supported count.
	supported := make(map[int]bool)
	for _, g := range s.GPUs {
		if g.Supported {
			supported[g.Index] = true
		}
	}
	if s.UsableCount > len(supported) {
		return fmt.Errorf("usable GPU count %d exceeds the %d supported GPU entries", s.UsableCount, len(supported))
	}
	if len(s.UsableIndexes) != s.UsableCount {
		return fmt.Errorf("OPERETA_GPU_USABLE_INDEXES lists %d indexes but the usable count is %d", len(s.UsableIndexes), s.UsableCount)
	}
	for _, idx := range s.UsableIndexes {
		if !supported[idx] {
			return fmt.Errorf("usable index %d is not a supported GPU entry", idx)
		}
	}
	return nil
}
