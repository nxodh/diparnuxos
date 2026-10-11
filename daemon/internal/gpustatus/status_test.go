package gpustatus

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// readyFile is a ready state with one usable and one unsupported GPU. GPU 1 comes first in the file on purpose.
const readyFile = `LIGHTLIX_GPU_STATUS=ready
LIGHTLIX_GPU_PRESENT=1
LIGHTLIX_GPU_PCI_COUNT=2
LIGHTLIX_GPU_COUNT=2
LIGHTLIX_GPU_USABLE_COUNT=1
LIGHTLIX_GPU_USABLE_INDEXES="0"
LIGHTLIX_SECURE_BOOT=disabled
LIGHTLIX_DRIVER_VERSION=595.45.04
LIGHTLIX_GPU_MESSAGE="NVIDIA open kernel module is loaded. 1 of 2 GPU(s) usable."
LIGHTLIX_GPU_1_NAME="NVIDIA GeForce GTX 1080"
LIGHTLIX_GPU_1_COMPUTE_CAP=6.1
LIGHTLIX_GPU_1_SUPPORTED=0
LIGHTLIX_GPU_1_REASON=compute_cap
LIGHTLIX_GPU_0_NAME="NVIDIA GeForce RTX 4090"
LIGHTLIX_GPU_0_COMPUTE_CAP=8.9
LIGHTLIX_GPU_0_SUPPORTED=1
LIGHTLIX_GPU_0_REASON=ok
`

// moduleFailedAfterQuery is what the script writes when nvidia-uvm fails after the GPU query:
// USABLE_COUNT is reset to 0 but the GPU entries keep SUPPORTED=1.
const moduleFailedAfterQuery = `LIGHTLIX_GPU_STATUS=module_unloaded
LIGHTLIX_GPU_PRESENT=1
LIGHTLIX_GPU_PCI_COUNT=1
LIGHTLIX_GPU_COUNT=1
LIGHTLIX_GPU_USABLE_COUNT=0
LIGHTLIX_GPU_USABLE_INDEXES=""
LIGHTLIX_SECURE_BOOT=unknown
LIGHTLIX_DRIVER_VERSION=595.45.04
LIGHTLIX_GPU_MESSAGE="nvidia-uvm failed to load. See journalctl -k."
LIGHTLIX_GPU_0_NAME="NVIDIA GeForce RTX 5090"
LIGHTLIX_GPU_0_COMPUTE_CAP=12.0
LIGHTLIX_GPU_0_SUPPORTED=1
LIGHTLIX_GPU_0_REASON=ok
`

// noGPUFile is what the script writes when the PCI bus has no NVIDIA display device.
const noGPUFile = `LIGHTLIX_GPU_STATUS=no_gpu
LIGHTLIX_GPU_PRESENT=0
LIGHTLIX_GPU_PCI_COUNT=0
LIGHTLIX_GPU_COUNT=0
LIGHTLIX_GPU_USABLE_COUNT=0
LIGHTLIX_GPU_USABLE_INDEXES=""
LIGHTLIX_SECURE_BOOT=disabled
LIGHTLIX_DRIVER_VERSION=
LIGHTLIX_GPU_MESSAGE="No NVIDIA GPU was found on the PCI bus."
`

func TestParseReady(t *testing.T) {
	s, err := Parse(strings.NewReader(readyFile))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := Status{
		State:         StateReady,
		Present:       true,
		PCICount:      2,
		Count:         2,
		UsableCount:   1,
		UsableIndexes: []int{0},
		SecureBoot:    "disabled",
		DriverVersion: "595.45.04",
		Message:       "NVIDIA open kernel module is loaded. 1 of 2 GPU(s) usable.",
		GPUs: []GPU{
			{Index: 0, Name: "NVIDIA GeForce RTX 4090", ComputeCap: "8.9", Supported: true, Reason: "ok"},
			{Index: 1, Name: "NVIDIA GeForce GTX 1080", ComputeCap: "6.1", Supported: false, Reason: "compute_cap"},
		},
	}
	if !reflect.DeepEqual(s, want) {
		t.Fatalf("Parse mismatch\n got: %+v\nwant: %+v", s, want)
	}
	if !s.Usable() {
		t.Fatal("Usable() = false for a ready status with a usable GPU")
	}
}

func TestParseTable(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantState  State
		wantUsable bool
		wantErr    bool
	}{
		{name: "ready", input: readyFile, wantState: StateReady, wantUsable: true},
		{name: "module failed after query keeps supported entries", input: moduleFailedAfterQuery, wantState: StateModuleUnloaded},
		{name: "no gpu", input: noGPUFile, wantState: StateNoGPU},
		{name: "unknown keys are ignored", input: readyFile + "LIGHTLIX_FUTURE=1\nLIGHTLIX_GPU_0_EXTRA=x\n", wantState: StateReady, wantUsable: true},
		{name: "comments and blank lines are skipped", input: "# note\n\n" + noGPUFile, wantState: StateNoGPU},
		{name: "missing trailing newline", input: strings.TrimSuffix(noGPUFile, "\n"), wantState: StateNoGPU},
		{name: "broken line without equals", input: noGPUFile + "GARBAGE\n", wantErr: true},
		{name: "key with a space", input: noGPUFile + "LIGHTLIX_X Y=1\n", wantErr: true},
		{name: "unterminated quote on known key", input: strings.Replace(noGPUFile, `LIGHTLIX_GPU_MESSAGE="No NVIDIA GPU was found on the PCI bus."`, `LIGHTLIX_GPU_MESSAGE="No NVIDIA`, 1), wantErr: true},
		{name: "bare value with a space", input: strings.Replace(noGPUFile, "LIGHTLIX_DRIVER_VERSION=", "LIGHTLIX_DRIVER_VERSION=1 2", 1), wantErr: true},
		{name: "negative count", input: strings.Replace(noGPUFile, "LIGHTLIX_GPU_PCI_COUNT=0", "LIGHTLIX_GPU_PCI_COUNT=-1", 1), wantErr: true},
		{name: "count with a plus sign", input: strings.Replace(noGPUFile, "LIGHTLIX_GPU_PCI_COUNT=0", "LIGHTLIX_GPU_PCI_COUNT=+1", 1), wantErr: true},
		{name: "GPU index out of range", input: readyFile + "LIGHTLIX_GPU_300_NAME=\"x\"\n", wantErr: true},
		{name: "duplicate top-level key", input: noGPUFile + "LIGHTLIX_GPU_COUNT=0\n", wantErr: true},
		{name: "duplicate GPU key", input: readyFile + "LIGHTLIX_GPU_0_REASON=ok\n", wantErr: true},
		{name: "missing required key", input: strings.Replace(noGPUFile, "LIGHTLIX_GPU_MESSAGE=\"No NVIDIA GPU was found on the PCI bus.\"\n", "", 1), wantErr: true},
		{name: "unknown state", input: strings.Replace(noGPUFile, "LIGHTLIX_GPU_STATUS=no_gpu", "LIGHTLIX_GPU_STATUS=bogus", 1), wantErr: true},
		{name: "present disagrees with pci count", input: strings.Replace(noGPUFile, "LIGHTLIX_GPU_PRESENT=0", "LIGHTLIX_GPU_PRESENT=1", 1), wantErr: true},
		{name: "count disagrees with GPU entries", input: strings.Replace(readyFile, "LIGHTLIX_GPU_COUNT=2", "LIGHTLIX_GPU_COUNT=3", 1), wantErr: true},
		{name: "usable count above zero but not ready", input: strings.NewReplacer(
			"LIGHTLIX_GPU_USABLE_COUNT=0", "LIGHTLIX_GPU_USABLE_COUNT=1",
			`LIGHTLIX_GPU_USABLE_INDEXES=""`, `LIGHTLIX_GPU_USABLE_INDEXES="0"`,
		).Replace(moduleFailedAfterQuery), wantErr: true},
		{name: "ready without usable GPU", input: strings.NewReplacer(
			"LIGHTLIX_GPU_USABLE_COUNT=1", "LIGHTLIX_GPU_USABLE_COUNT=0",
			`LIGHTLIX_GPU_USABLE_INDEXES="0"`, `LIGHTLIX_GPU_USABLE_INDEXES=""`,
		).Replace(readyFile), wantErr: true},
		{name: "usable index points at unsupported GPU", input: strings.Replace(readyFile, `LIGHTLIX_GPU_USABLE_INDEXES="0"`, `LIGHTLIX_GPU_USABLE_INDEXES="1"`, 1), wantErr: true},
		{name: "usable indexes count mismatch", input: strings.Replace(readyFile, `LIGHTLIX_GPU_USABLE_INDEXES="0"`, `LIGHTLIX_GPU_USABLE_INDEXES="0,1"`, 1), wantErr: true},
		{name: "supported and reason disagree", input: strings.Replace(readyFile, "LIGHTLIX_GPU_0_REASON=ok", "LIGHTLIX_GPU_0_REASON=name", 1), wantErr: true},
		{name: "incomplete GPU entry", input: strings.Replace(readyFile, "LIGHTLIX_GPU_1_REASON=compute_cap\n", "", 1), wantErr: true},
		{name: "invalid secure boot value", input: strings.Replace(noGPUFile, "LIGHTLIX_SECURE_BOOT=disabled", "LIGHTLIX_SECURE_BOOT=yes", 1), wantErr: true},
		{name: "non-ASCII value", input: strings.Replace(noGPUFile, "No NVIDIA", "No NVIDIA \xec\x97\x94", 1), wantErr: true},
		{name: "carriage return at line end", input: strings.Replace(noGPUFile, "LIGHTLIX_GPU_PCI_COUNT=0\n", "LIGHTLIX_GPU_PCI_COUNT=0\r\n", 1), wantErr: true},
		{name: "line longer than limit", input: noGPUFile + "LIGHTLIX_X=" + strings.Repeat("a", maxLineSize) + "\n", wantErr: true},
		{name: "file larger than limit", input: noGPUFile + strings.Repeat("# pad\n", maxFileSize/6+1), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			s, err := Parse(strings.NewReader(tt.input))
			if tt.wantErr {
				if err == nil {
					t.Fatalf("Parse: want error, got %+v", s)
				}
				return
			}
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if s.State != tt.wantState {
				t.Fatalf("State = %q, want %q", s.State, tt.wantState)
			}
			if s.Usable() != tt.wantUsable {
				t.Fatalf("Usable() = %v, want %v", s.Usable(), tt.wantUsable)
			}
		})
	}
}

func TestParseModuleFailedAfterQuery(t *testing.T) {
	s, err := Parse(strings.NewReader(moduleFailedAfterQuery))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if s.UsableCount != 0 || len(s.UsableIndexes) != 0 {
		t.Fatalf("UsableCount=%d UsableIndexes=%v, want 0 and empty", s.UsableCount, s.UsableIndexes)
	}
	if len(s.GPUs) != 1 || !s.GPUs[0].Supported {
		t.Fatalf("GPUs = %+v, want one supported GPU kept for display", s.GPUs)
	}
}

func TestParseLiteralValues(t *testing.T) {
	// The parser must not expand shell syntax. The value keeps every character literally.
	input := strings.Replace(noGPUFile, `"No NVIDIA GPU was found on the PCI bus."`, `"$HOME \x41 ${PATH} `+"`id`"+`"`, 1)
	s, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := "$HOME \\x41 ${PATH} `id`"
	if s.Message != want {
		t.Fatalf("Message = %q, want %q", s.Message, want)
	}
}

func TestParseEmptyInput(t *testing.T) {
	if _, err := Parse(strings.NewReader("")); err == nil {
		t.Fatal("Parse of empty input: want error for missing keys")
	}
}

func TestLoadMissingFileWrapsErrNotExist(t *testing.T) {
	_, err := Load(filepath.Join(t.TempDir(), "gpu-status"))
	if err == nil {
		t.Fatal("Load of missing file: want error")
	}
	if !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("Load error %v does not wrap os.ErrNotExist", err)
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gpu-status")
	if err := os.WriteFile(path, []byte(moduleFailedAfterQuery), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if s.State != StateModuleUnloaded {
		t.Fatalf("State = %q, want %q", s.State, StateModuleUnloaded)
	}
}

func TestLoadBrokenFileIsNotErrNotExist(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gpu-status")
	if err := os.WriteFile(path, []byte("LIGHTLIX_GPU_STATUS=ready\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("Load of incomplete file: want error")
	}
	if errors.Is(err, os.ErrNotExist) {
		t.Fatalf("broken file error %v must not wrap os.ErrNotExist", err)
	}
}
