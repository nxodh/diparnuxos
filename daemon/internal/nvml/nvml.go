// Package nvml 은 NVIDIA 관리 라이브러리(NVML)를 읽어 platform.GPUSnapshot 으로 바꾼다.
//
// go-nvml 은 libnvidia-ml.so.1 을 dlopen 으로 연다. 라이브러리가 없거나 드라이버가 아직 올라오지 않으면 Init 이
// 실패하고, 그 뒤에 NVML 함수를 부르면 프로세스가 로더 오류(undefined symbol)로 즉시 종료된다.
// 그래서 이 패키지는 Init 이 SUCCESS 를 돌려준 뒤에만 다른 NVML 함수를 부른다.
// 조사 기록은 docs/verification-phase-2-research.md 의 C2 절과 "설계에 영향을 주는 발견" 8번에 있다.
package nvml

import (
	"context"
	"fmt"
	"math"
	"sync"
	"time"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// 기본 재시도 간격과 단위 상수다.
const (
	// retryInterval 은 NVML 초기화가 실패한 뒤 다시 시도하기까지의 하한이다.
	retryInterval = 5 * time.Second
	// bytesPerMiB 는 바이트를 MiB 로 바꾸는 나눗수다.
	bytesPerMiB = 1 << 20
	// unknown 은 읽지 못한 정수 값이다. docs/drivers.md 와 platform.GPUDevice 주석의 -1 규칙을 따른다.
	unknown = -1
)

// platform.GPUProber 를 구현하는지 컴파일 시점에 확인한다.
var _ platform.GPUProber = (*Prober)(nil)

// Prober 는 NVML 상태를 읽는다. platform.GPUProber 를 구현한다. 모든 메서드는 뮤텍스로 직렬화된다.
type Prober struct {
	mu sync.Mutex

	lib           gonvml.Interface
	now           func() time.Time
	retryInterval time.Duration

	// ready 는 Init 이 SUCCESS 를 돌려준 뒤 true 이고, 그때만 다른 NVML 함수를 부른다.
	ready bool
	// attempted 는 Init 을 한 번이라도 불렀는지다.
	attempted bool
	// nextTry 는 다음 Init 을 허용하는 시각이다.
	nextTry time.Time
	// lastReason 은 마지막 초기화 실패의 이유다. ready 가 아닐 때 Snapshot 이 돌려준다.
	lastReason string
	// closed 는 Close 가 불린 뒤 true 다. 그 뒤의 Snapshot 은 초기화하지 않는다.
	closed bool
}

// New 는 실제 libnvidia-ml.so.1 을 쓰는 Prober 를 만든다. 라이브러리가 없어도 오류 없이 만들어지며,
// 첫 Snapshot 에서 초기화를 시도한다. 사용이 끝나면 Close 를 부른다.
func New() *Prober {
	return newProber(gonvml.New(), time.Now, retryInterval)
}

// NewWithInterface 는 주어진 NVML 구현을 쓰는 Prober 를 만든다. 테스트에서 pkg/nvml/mock 을 넣을 때 쓴다.
func NewWithInterface(lib gonvml.Interface) *Prober {
	return newProber(lib, time.Now, retryInterval)
}

// newProber 는 시각과 재시도 간격을 주입해 Prober 를 만든다. 테스트가 시간을 제어하려고 쓴다.
func newProber(lib gonvml.Interface, now func() time.Time, interval time.Duration) *Prober {
	return &Prober{lib: lib, now: now, retryInterval: interval}
}

// Snapshot 은 GPU 상태를 한 번 읽는다. 오류를 돌려주지 않는다. NVML 을 쓸 수 없으면 Available 이 false 다.
//
// 초기화가 실패했으면 재시도 간격이 지나기 전에는 Init 을 다시 부르지 않고 이전 이유를 돌려준다.
// 초기화가 성공한 뒤에는 장치별 값을 읽는다. 개별 조회가 실패해도 그 필드만 비우거나 -1 로 두고 계속 읽는다.
func (p *Prober) Snapshot(ctx context.Context) platform.GPUSnapshot {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.closed {
		return unavailable("nvml prober is closed")
	}
	if err := ctx.Err(); err != nil {
		return unavailable("snapshot canceled before NVML was read")
	}
	if !p.ready {
		if p.attempted && p.now().Before(p.nextTry) {
			return unavailable(p.lastReason)
		}
		p.attempted = true
		// Init 이 SUCCESS 가 아니면 이 아래에서 NVML 함수를 부르지 않는다. Shutdown 도 부르지 않는다.
		// 라이브러리가 없을 때 Shutdown 은 ERROR_UNINITIALIZED 를 돌려주지만, 드라이버 미적재 경로에서는
		// 로드된 라이브러리의 함수가 불리므로 아예 부르지 않는다.
		if rc := p.lib.Init(); rc != gonvml.SUCCESS {
			p.lastReason = initReason(rc)
			p.nextTry = p.now().Add(p.retryInterval)
			return unavailable(p.lastReason)
		}
		p.ready = true
	}
	return p.read()
}

// Close 는 초기화에 성공했을 때만 NVML 을 끝낸다. 실패했거나 이미 닫혔으면 아무것도 하지 않는다.
func (p *Prober) Close() {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.closed = true
	if p.ready {
		_ = p.lib.Shutdown()
		p.ready = false
	}
}

// unavailable 은 NVML 을 쓸 수 없다는 스냅샷을 만든다.
func unavailable(reason string) platform.GPUSnapshot {
	return platform.GPUSnapshot{Available: false, Reason: reason}
}

// initReason 은 Init 이 실패한 반환 값을 화면용 영어 한 줄로 바꾼다.
func initReason(rc gonvml.Return) string {
	switch rc {
	case gonvml.ERROR_LIBRARY_NOT_FOUND:
		return "nvml library not found"
	case gonvml.ERROR_DRIVER_NOT_LOADED:
		return "nvml driver not loaded"
	}
	return fmt.Sprintf("nvml init failed: %s", rc.Error())
}

// read 는 초기화된 NVML 에서 시스템 값과 장치 목록을 읽는다. 호출자가 ready 를 확인한 뒤에만 부른다.
func (p *Prober) read() platform.GPUSnapshot {
	snap := platform.GPUSnapshot{Available: true}
	if v, rc := p.lib.SystemGetDriverVersion(); rc == gonvml.SUCCESS {
		snap.DriverVersion = v
	}
	if v, rc := p.lib.SystemGetNVMLVersion(); rc == gonvml.SUCCESS {
		snap.NVMLVersion = v
	}
	if v, rc := p.lib.SystemGetCudaDriverVersion(); rc == gonvml.SUCCESS {
		snap.CUDADriverVersion = cudaVersion(v)
	}

	count, rc := p.lib.DeviceGetCount()
	if rc != gonvml.SUCCESS {
		return unavailable(fmt.Sprintf("nvml device count failed: %s", rc.Error()))
	}
	for i := 0; i < count; i++ {
		snap.Devices = append(snap.Devices, p.readDevice(i))
	}
	return snap
}

// readDevice 는 index 번째 장치를 읽는다. 읽지 못한 값은 빈 문자열, -1, ProcessesKnown=false 로 남긴다.
func (p *Prober) readDevice(index int) platform.GPUDevice {
	d := platform.GPUDevice{
		Index:          index,
		MemTotalMiB:    unknown,
		MemFreeMiB:     unknown,
		MemUsedMiB:     unknown,
		TemperatureC:   unknown,
		PowerMilliwatt: unknown,
		UtilGPUPercent: unknown,
		UtilMemPercent: unknown,
	}
	dev, rc := p.lib.DeviceGetHandleByIndex(index)
	if rc != gonvml.SUCCESS {
		return d
	}
	if v, rc := dev.GetName(); rc == gonvml.SUCCESS {
		d.Name = v
	}
	if v, rc := dev.GetUUID(); rc == gonvml.SUCCESS {
		d.UUID = v
	}
	if arch, rc := dev.GetArchitecture(); rc == gonvml.SUCCESS {
		d.Arch = archName(arch)
	}
	if major, minor, rc := dev.GetCudaComputeCapability(); rc == gonvml.SUCCESS {
		d.ComputeCap = fmt.Sprintf("%d.%d", major, minor)
	}
	if mem, rc := dev.GetMemoryInfo(); rc == gonvml.SUCCESS {
		d.MemTotalMiB = int64(mem.Total / bytesPerMiB)
		d.MemFreeMiB = int64(mem.Free / bytesPerMiB)
		d.MemUsedMiB = int64(mem.Used / bytesPerMiB)
	}
	if t, rc := dev.GetTemperature(gonvml.TEMPERATURE_GPU); rc == gonvml.SUCCESS {
		d.TemperatureC = int(t)
	}
	// nvml.h 는 nvmlDeviceGetPowerUsage 의 값을 밀리와트(milliwatts)로 적는다.
	if pw, rc := dev.GetPowerUsage(); rc == gonvml.SUCCESS {
		d.PowerMilliwatt = int(pw)
	}
	if u, rc := dev.GetUtilizationRates(); rc == gonvml.SUCCESS {
		d.UtilGPUPercent = int(u.Gpu)
		d.UtilMemPercent = int(u.Memory)
	}
	if procs, rc := dev.GetComputeRunningProcesses(); rc == gonvml.SUCCESS {
		d.ProcessesKnown = true
		for _, pr := range procs {
			d.Processes = append(d.Processes, platform.GPUProcess{PID: pr.Pid, UsedMiB: processMiB(pr.UsedGpuMemory)})
		}
	}
	return d
}

// processMiB 는 프로세스의 GPU 메모리 바이트 수를 MiB 로 바꾼다. 값을 알 수 없으면 -1 이다.
// nvml.h 는 nvmlProcessInfo_t.usedGpuMemory 를 바이트로 적고, 알 수 없을 때 NVML_VALUE_NOT_AVAILABLE(-1)을
// 쓴다고 적는다. 부호 없는 정수로 읽으면 그 값은 math.MaxUint64 다.
func processMiB(bytes uint64) int64 {
	if bytes == math.MaxUint64 {
		return unknown
	}
	return int64(bytes / bytesPerMiB)
}

// archName 은 NVML 장치 아키텍처 값을 문서 이름으로 바꾼다. 목록에 없는 값은 unknown(<n>)이다.
func archName(arch gonvml.DeviceArchitecture) string {
	switch arch {
	case gonvml.DEVICE_ARCH_AMPERE:
		return "ampere"
	case gonvml.DEVICE_ARCH_ADA:
		return "ada"
	case gonvml.DEVICE_ARCH_BLACKWELL:
		return "blackwell"
	}
	return fmt.Sprintf("unknown(%d)", arch)
}

// cudaVersion 은 nvmlSystemGetCudaDriverVersion 의 정수를 "13.2" 형태로 바꾼다.
// nvml.h 의 매크로는 major 를 v/1000, minor 를 (v%1000)/10 으로 적는다. 13.2 는 13020 이다.
func cudaVersion(v int) string {
	return fmt.Sprintf("%d.%d", v/1000, (v%1000)/10)
}
