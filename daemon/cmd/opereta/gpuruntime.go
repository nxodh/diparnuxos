package main

import (
	"bufio"
	"fmt"
	"strings"

	"github.com/nxodh/operetaos/daemon/internal/updatectl"
)

// NVIDIA 약관의 주소다. 설치 전에 사용자에게 보여 준다.
const (
	cudaEULAURL   = "https://docs.nvidia.com/cuda/eula/index.html"
	cudaRuntimeNB = "The CUDA runtime (cudart, cuBLAS) is NVIDIA software. It is not part of the Opereta image: this computer downloads it directly from NVIDIA's repository, under NVIDIA's license."
)

// gpuRuntime 은 `opereta gpu runtime [status|install [--yes]]` 이다.
// GPU 가속 추론에 필요한 CUDA 런타임을 NVIDIA 저장소에서 직접 받아 설치한다.
func (c *cli) gpuRuntime(args []string) int {
	sub := "status"
	rest := args
	if len(args) > 0 {
		sub, rest = args[0], args[1:]
	}
	switch sub {
	case "status":
		if len(rest) != 0 {
			return c.usage("gpu runtime status takes no arguments", "gpu runtime")
		}
		return c.gpuRuntimeStatus()
	case "install":
		fs := newFlags("gpu runtime install")
		yes := fs.Bool("yes", false, "accept NVIDIA's license without asking")
		pos, code, ok := c.parse(fs, "gpu runtime install", rest)
		if !ok {
			return code
		}
		if len(pos) != 0 {
			return c.usage("gpu runtime install takes no positional arguments", "gpu runtime")
		}
		return c.gpuRuntimeInstall(*yes)
	}
	return c.usage("usage: opereta gpu runtime [status|install [--yes]]", "gpu runtime")
}

func (c *cli) gpuRuntimeStatus() int {
	ctx, cancel := c.timeout(shortTimeout)
	defer cancel()
	info, raw, err := c.api.UpdateInfo(ctx)
	if err != nil {
		return c.fail(err)
	}
	if c.asJSON {
		return c.printJSON(raw)
	}
	w := c.env.stdout
	rt := info.CUDARuntime
	if rt.Installed {
		fmt.Fprintln(w, "CUDA runtime: installed")
	} else {
		fmt.Fprintf(w, "CUDA runtime: not installed (%s)\n", orDash(rt.Reason))
		fmt.Fprintln(w, "GPU acceleration needs it. Install it with: opereta gpu runtime install")
	}
	for _, p := range rt.Packages {
		fmt.Fprintf(w, "  %s %s\n", p.Name, p.Version)
	}
	return exitOK
}

func (c *cli) gpuRuntimeInstall(yes bool) int {
	w := c.env.stdout
	if !yes {
		fmt.Fprintln(w, cudaRuntimeNB)
		fmt.Fprintf(w, "License: %s\n", cudaEULAURL)
		fmt.Fprintln(w, "The download is about 360 MiB and needs about 1.2 GiB of free space.")
		if !c.env.stdinTTY {
			c.errorLine("this needs your confirmation; run it in a terminal or add --yes to accept NVIDIA's license")
			return exitUsage
		}
		fmt.Fprint(w, "Download and install it now? [y/N] ")
		line, _ := bufio.NewReader(c.env.stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(line)); a != "y" && a != "yes" {
			fmt.Fprintln(w, "cancelled")
			return exitFailure
		}
	}
	return c.updateRun(updatectl.ActionCUDARuntime, false)
}
