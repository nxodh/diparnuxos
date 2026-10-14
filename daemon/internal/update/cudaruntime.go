package update

import (
	"bufio"
	"context"
	"os"
	"strings"
)

// DefaultNvidiaConf 는 opereta-nvidia 패키지가 설치하는 고정 버전 목록이다.
const DefaultNvidiaConf = "/usr/share/opereta/nvidia.conf"

// CodeCUDARuntime 은 CUDA 런타임을 NVIDIA 저장소에서 받아 설치하지 못했다는 오류 코드다.
const CodeCUDARuntime = "cuda_runtime_failed"

// cudaRuntimeMinFree 는 CUDA 런타임(cudart, cublas)을 설치하는 데 필요한 루트 파일 시스템의 여유다.
// 내려받기 약 360 MiB 와 설치 약 540 MiB 에 여유를 더한 값이다.
const cudaRuntimeMinFree = 1200 << 20

// CUDARuntime 은 CUDA 런타임(cudart, cublas)의 설치 상태다. GPU 가속 추론에 필요하다.
//
// 이미지에는 이 런타임이 들어 있지 않다. NVIDIA CUDA EULA 는 이 파일들의 재배포를 응용 프로그램 전용으로만 허용하므로,
// 이 프로젝트는 재배포하지 않고 사용자의 컴퓨터가 NVIDIA 저장소에서 직접 받아 설치한다.
type CUDARuntime struct {
	// Installed 는 두 패키지가 고정 버전으로 설치되어 있다는 뜻이다.
	Installed bool `json:"installed"`
	// Packages 는 필요한 패키지와 고정 버전이다.
	Packages []PkgVersion `json:"packages,omitempty"`
	// Reason 은 Installed 가 거짓일 때의 이유다(not_installed, version_mismatch, no_settings).
	Reason string `json:"reason,omitempty"`
}

type cudaPackages struct {
	cudart, cudartVer, cublas, cublasVer string
}

// readCUDAPackages 는 nvidia.conf 의 KEY="값" 줄에서 CUDA 런타임 패키지와 버전을 읽는다.
func readCUDAPackages(path string) (cudaPackages, error) {
	if path == "" {
		path = DefaultNvidiaConf
	}
	f, err := os.Open(path)
	if err != nil {
		return cudaPackages{}, err
	}
	defer f.Close()
	kv := map[string]string{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		k, v, ok := strings.Cut(strings.TrimSpace(sc.Text()), "=")
		if !ok || strings.HasPrefix(k, "#") {
			continue
		}
		kv[k] = strings.Trim(v, `"'`)
	}
	p := cudaPackages{kv["CUDA_CUDART_PACKAGE"], kv["CUDA_CUDART_VERSION"], kv["CUDA_CUBLAS_PACKAGE"], kv["CUDA_CUBLAS_VERSION"]}
	if p.cudart == "" || p.cudartVer == "" || p.cublas == "" || p.cublasVer == "" {
		return cudaPackages{}, os.ErrInvalid
	}
	return p, sc.Err()
}

// CUDARuntimeStatus 는 CUDA 런타임이 고정 버전으로 설치되어 있는지 본다.
func (e *Engine) CUDARuntimeStatus(ctx context.Context) CUDARuntime {
	want, err := readCUDAPackages(e.cfg().NvidiaConf)
	if err != nil {
		return CUDARuntime{Reason: "no_settings"}
	}
	out := CUDARuntime{Packages: []PkgVersion{{Name: want.cudart, Version: want.cudartVer}, {Name: want.cublas, Version: want.cublasVer}}}
	pkgs, perr := e.installed(ctx)
	if perr != nil {
		out.Reason = "not_installed"
		return out
	}
	gotCudart, gotCublas := versionOf(pkgs, want.cudart), versionOf(pkgs, want.cublas)
	switch {
	case gotCudart == "" || gotCublas == "":
		out.Reason = "not_installed"
	case gotCudart != want.cudartVer || gotCublas != want.cublasVer:
		out.Reason = "version_mismatch"
	default:
		out.Installed = true
	}
	return out
}

// InstallCUDARuntime 은 CUDA 런타임(cudart, cublas)을 NVIDIA 저장소에서 받아 고정 버전으로 설치한다.
// 커널이나 드라이버는 바꾸지 않으므로 재부팅이 필요 없다. 이미 설치되어 있으면 아무것도 하지 않는다.
// 라이브 세션에서도 쓸 수 있다(설치는 재부팅하면 사라진다).
func (e *Engine) InstallCUDARuntime(ctx context.Context) (*State, error) {
	unlock, err := e.lock()
	if err != nil {
		return nil, err
	}
	defer unlock()
	prior, lerr := e.LoadState()
	if lerr != nil {
		return nil, errf(CodeInternal, "%v", lerr)
	}
	if prior.Phase == PhasePendingBoot {
		return prior, errf(CodePendingBoot, "the previous update is waiting for a reboot; reboot first so it can be verified")
	}
	st := e.begin("cuda_runtime")
	ctx = context.WithoutCancel(ctx)

	want, cerr := readCUDAPackages(e.cfg().NvidiaConf)
	if cerr != nil {
		return st, e.fail(errf(CodeInternal, "this image has no CUDA runtime settings (%s): %v", e.cfg().NvidiaConf, cerr))
	}
	if cur := e.CUDARuntimeStatus(ctx); cur.Installed {
		e.event("done", "the CUDA runtime is already installed")
		st.Phase = PhaseCommitted
		st.Message = "already installed"
		st.Finished = e.stamp()
		_ = e.save(st)
		return st, nil
	}

	free := e.FreeBytes
	if free == nil {
		free = statfsFree
	}
	if n, ferr := free(e.cfg().RootDir); ferr != nil {
		return st, e.fail(errf(CodeInternal, "cannot read free space: %v", ferr))
	} else if n < cudaRuntimeMinFree {
		return st, e.fail(errf(CodeDiskSpace, "%d MiB free; at least %d MiB is needed for the CUDA runtime", n>>20, cudaRuntimeMinFree>>20))
	}

	e.event("refresh", "refreshing package indexes")
	if err := e.aptUpdate(ctx); err != nil {
		return st, e.fail(err)
	}
	e.event("download", "downloading and installing %s %s and %s %s from NVIDIA", want.cudart, want.cudartVer, want.cublas, want.cublasVer)
	res, rerr := e.aptRun(ctx, "", "install", holdAllowNamed, want.cudart+"="+want.cudartVer, want.cublas+"="+want.cublasVer)
	if rerr != nil || res.ExitCode != 0 {
		code := aptFailureCode(res, rerr, CodeCUDARuntime)
		return st, e.fail(errf(code, "installing the CUDA runtime failed: %s", aptFailure(res, rerr)))
	}
	if after := e.CUDARuntimeStatus(ctx); !after.Installed {
		return st, e.fail(errf(CodeCUDARuntime, "the CUDA runtime is not installed at the pinned versions after apt finished (%s)", after.Reason))
	}
	// 갱신 도구가 드라이버와 함께 의도하지 않게 올리지 않도록 hold 를 건다.
	_, _ = e.sh(ctx, shortTimeout, aptEnv, "apt-mark", "hold", want.cudart, want.cublas)
	e.event("done", "the CUDA runtime is installed; restart the model server to use the GPU")
	st.Phase = PhaseCommitted
	st.Message = "installed"
	st.Finished = e.stamp()
	_ = e.save(st)
	e.saveHistory(st)
	return st, nil
}
