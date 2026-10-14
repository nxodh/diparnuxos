package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/client"
)

// 명령별 시간 제한이다. GPU 진단은 데몬이 스크립트를 최대 약 110초 기다리므로 넉넉히 잡는다.
const (
	shortTimeout  = 15 * time.Second
	metaTimeout   = 60 * time.Second
	startTimeout  = 30 * time.Second
	gpuTimeout    = 3 * time.Minute
	chatTimeout   = 150 * time.Second
	reportTimeout = 60 * time.Second
)

// status 는 GET /v1/status 를 보여 준다.
func (c *cli) status(args []string) int {
	if len(args) != 0 {
		return c.usage("status takes no arguments", "status")
	}
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	st, raw, err := c.api.Status(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	writeStatus(c.env.stdout, st)
	return exitOK
}

// writeStatus 는 상태 화면을 쓴다.
func writeStatus(w io.Writer, st client.Status) {
	kv(w, "version", orDash(st.Version))
	kv(w, "mode", orDash(st.Mode))
	fmt.Fprintln(w)
	fmt.Fprintln(w, "GPU")
	writeGPU(w, st.GPU)
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Memory")
	if st.Memory == nil {
		kv(w, "  total", "unknown")
		kv(w, "  available", "unknown")
	} else {
		kv(w, "  total", humanMiB(st.Memory.TotalMiB))
		kv(w, "  available", humanMiB(st.Memory.AvailableMiB))
	}
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Server")
	writeServer(w, st.Server)
}

// writeGPU 는 GPU 절의 내용을 쓴다. 진단 상태 파일과 NVML 의 값을 함께 보여 준다.
func writeGPU(w io.Writer, g client.GPU) {
	if g.Status == nil {
		kv(w, "  state", "not checked")
		kv(w, "  note", orDash(g.StatusError))
		fmt.Fprintln(w, "  run 'opereta gpu check' to check the GPU")
	} else {
		kv(w, "  state", orDash(g.Status.State))
		kv(w, "  message", orDash(g.Status.Message))
		kv(w, "  usable GPUs", fmt.Sprintf("%d", g.Status.UsableCount))
	}
	kv(w, "  secure boot", orUnknown(g.SecureBoot))
	kv(w, "  driver", orUnknown(g.DriverVersion))
	kv(w, "  open module", orUnknown(g.OpenModule))
	if !g.NVML.Available {
		kv(w, "  NVML", "unavailable: "+orDash(g.NVML.Reason))
	} else {
		detail := "available"
		var parts []string
		if g.NVML.NVMLVersion != "" {
			parts = append(parts, "NVML "+g.NVML.NVMLVersion)
		}
		if g.NVML.CUDADriverVersion != "" {
			parts = append(parts, "CUDA driver "+g.NVML.CUDADriverVersion)
		}
		if len(parts) > 0 {
			detail += " (" + strings.Join(parts, ", ") + ")"
		}
		kv(w, "  NVML", detail)
	}
	if len(g.NVML.Devices) == 0 {
		kv(w, "  devices", "none reported")
		return
	}
	fmt.Fprintln(w, "  devices:")
	rows := make([][]string, 0, len(g.NVML.Devices))
	for _, d := range g.NVML.Devices {
		rows = append(rows, []string{
			fmt.Sprintf("%d", d.Index),
			orDash(d.Name),
			orDash(d.Arch),
			orDash(d.ComputeCap),
			fmt.Sprintf("%s / %s", humanMiB(d.MemUsedMiB), humanMiB(d.MemTotalMiB)),
			tempText(d.TemperatureC),
			powerText(d.PowerMilliwatt),
		})
	}
	writeTable(w, []string{"INDEX", "NAME", "ARCH", "CC", "VRAM USED / TOTAL", "TEMP", "POWER"}, rows)
}

// tempText 는 온도를 쓴다. 읽지 못한 값(-1)은 "-" 다.
func tempText(c int) string {
	if c < 0 {
		return "-"
	}
	return fmt.Sprintf("%d C", c)
}

// powerText 는 전력을 와트로 쓴다. 읽지 못한 값(-1)은 "-" 다.
func powerText(mw int) string {
	if mw < 0 {
		return "-"
	}
	return fmt.Sprintf("%.1f W", float64(mw)/1000)
}

// writeServer 는 서버 절의 내용을 쓴다.
func writeServer(w io.Writer, s client.Server) {
	kv(w, "  state", orDash(s.State))
	kv(w, "  model", orDash(s.ModelID))
	addr := "-"
	if s.Port > 0 {
		addr = fmt.Sprintf("%s:%d", orDash(s.Host), s.Port)
	}
	kv(w, "  address", addr)
	kv(w, "  LAN exposed", yesNo(s.LANExposed))
	if s.Offload.Total > 0 {
		kv(w, "  offload", fmt.Sprintf("requested %s, offloaded %d of %d layers, verified %s",
			orDash(s.Offload.Requested), s.Offload.Offloaded, s.Offload.Total, yesNo(s.Offload.Verified)))
	} else {
		kv(w, "  offload", fmt.Sprintf("requested %s, not measured", orDash(s.Offload.Requested)))
	}
	if s.Offload.Note != "" {
		kv(w, "  offload note", s.Offload.Note)
	}
	if len(s.Warnings) == 0 {
		kv(w, "  warnings", "none")
	} else {
		fmt.Fprintln(w, "  warnings:")
		for _, warn := range s.Warnings {
			fmt.Fprintf(w, "    - %s\n", warn)
		}
	}
	if s.LastError != nil {
		kv(w, "  last error", fmt.Sprintf("%s: %s", s.LastError.Code, s.LastError.Message))
	}
}

// gpuGuidance 는 진단 상태별로 할 일 한 줄이다. 없는 상태는 기본 안내를 쓴다.
var gpuGuidance = map[string]string{
	"secure_boot":      "Secure Boot blocks the NVIDIA module. Turn off Secure Boot in the UEFI settings, then reboot.",
	"module_unloaded":  "The NVIDIA kernel module did not load. Check the kernel log: journalctl -k",
	"unsupported_gpu":  "No supported GPU was found. The target is GeForce RTX 30, 40, or 50 series.",
	"version_mismatch": "The driver and library versions do not match. Restore the same version, then reboot.",
}

// gpuVerdict 는 진단 결과의 종료 코드와 안내 한 줄을 정한다.
// 쓸 수 있는 GPU가 있으면 0, GPU가 없으면 0 과 CPU 모드 안내, GPU는 있는데 못 쓰면 1 이다.
func gpuVerdict(st client.GPUFile) (int, string) {
	if st.State == "ready" || st.UsableCount > 0 {
		return exitOK, fmt.Sprintf("GPU is ready: %d usable GPU(s).", st.UsableCount)
	}
	if st.State == "no_gpu" {
		return exitOK, "No NVIDIA GPU was found. operetad will run the model on the CPU."
	}
	if g, ok := gpuGuidance[st.State]; ok {
		return exitFailure, g
	}
	return exitFailure, "Run 'opereta report' and check the kernel log (journalctl -k) for details."
}

// gpu 는 gpu 명령 묶음이다. 지금은 check 만 있다.
func (c *cli) gpu(args []string) int {
	if len(args) == 0 || args[0] != "check" {
		return c.usage("usage: opereta gpu check", "gpu check")
	}
	if len(args) != 1 {
		return c.usage("gpu check takes no arguments", "gpu check")
	}
	ctx, cancel := c.timeout(gpuTimeout)
	defer cancel()
	res, raw, err := c.api.GPUCheck(ctx)
	if err != nil {
		return c.fail(err)
	}
	code, guide := gpuVerdict(res.Status)
	if c.asJSON {
		if rc := c.printJSON(raw); rc != exitOK {
			return rc
		}
		return code
	}
	writeGPUCheck(c.env.stdout, res, guide)
	return code
}

// writeGPUCheck 는 진단 결과를 쓴다. 마지막 줄은 할 일 안내다.
func writeGPUCheck(w io.Writer, res client.GPUCheckResult, guide string) {
	st := res.Status
	fmt.Fprintf(w, "GPU check: %s\n", orDash(st.State))
	kv(w, "  message", orDash(st.Message))
	kv(w, "  PCI GPUs", fmt.Sprintf("%d", st.PCICount))
	kv(w, "  usable GPUs", fmt.Sprintf("%d", st.UsableCount))
	kv(w, "  secure boot", orUnknown(st.SecureBoot))
	kv(w, "  driver", orUnknown(st.DriverVersion))
	if len(st.GPUs) > 0 {
		rows := make([][]string, 0, len(st.GPUs))
		for _, g := range st.GPUs {
			rows = append(rows, []string{
				fmt.Sprintf("%d", g.Index),
				orDash(g.Name),
				orDash(g.ComputeCap),
				yesNo(g.Supported),
				orDash(g.Reason),
			})
		}
		writeTable(w, []string{"INDEX", "NAME", "CC", "SUPPORTED", "REASON"}, rows)
	}
	fmt.Fprintln(w, guide)
}
