package nvml

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	gonvml "github.com/NVIDIA/go-nvml/pkg/nvml"
	"github.com/NVIDIA/go-nvml/pkg/nvml/mock"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// fakeClock 은 테스트가 직접 진행시키는 시계다.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time { return c.t }

// newClock 은 고정된 시작 시각을 가진 시계를 만든다.
func newClock() *fakeClock {
	return &fakeClock{t: time.Date(2026, 10, 11, 9, 0, 0, 0, time.UTC)}
}

// nvmlFuncs 는 mock.Interface 의 모든 함수 필드를 채운다. 호출되면 실패 값을 돌려주므로 테스트가 패닉하지 않는다.
// 호출 횟수는 mock 의 *Calls 메서드로 확인한다.
func nvmlFuncs(lib *mock.Interface) {
	lib.InitFunc = func() gonvml.Return { return gonvml.ERROR_UNINITIALIZED }
	lib.ShutdownFunc = func() gonvml.Return { return gonvml.ERROR_UNINITIALIZED }
	lib.DeviceGetCountFunc = func() (int, gonvml.Return) { return 0, gonvml.ERROR_UNINITIALIZED }
	lib.DeviceGetHandleByIndexFunc = func(int) (gonvml.Device, gonvml.Return) {
		return nil, gonvml.ERROR_UNINITIALIZED
	}
	lib.SystemGetDriverVersionFunc = func() (string, gonvml.Return) { return "", gonvml.ERROR_UNINITIALIZED }
	lib.SystemGetNVMLVersionFunc = func() (string, gonvml.Return) { return "", gonvml.ERROR_UNINITIALIZED }
	lib.SystemGetCudaDriverVersionFunc = func() (int, gonvml.Return) { return 0, gonvml.ERROR_UNINITIALIZED }
}

// totalCalls 는 mock 에 기록된 NVML 호출의 총 수다.
func totalCalls(lib *mock.Interface) int {
	return len(lib.InitCalls()) + len(lib.ShutdownCalls()) + len(lib.DeviceGetCountCalls()) +
		len(lib.DeviceGetHandleByIndexCalls()) + len(lib.SystemGetDriverVersionCalls()) +
		len(lib.SystemGetNVMLVersionCalls()) + len(lib.SystemGetCudaDriverVersionCalls())
}

// TestInitFailureMakesNoOtherCalls 는 Init 이 SUCCESS 가 아니면 다른 NVML 함수가 한 번도 불리지 않는지 확인한다.
// 라이브러리가 없을 때 그런 호출은 프로세스를 죽인다.
func TestInitFailureMakesNoOtherCalls(t *testing.T) {
	tests := []struct {
		name   string
		rc     gonvml.Return
		reason string
	}{
		{"library not found", gonvml.ERROR_LIBRARY_NOT_FOUND, "nvml library not found"},
		{"driver not loaded", gonvml.ERROR_DRIVER_NOT_LOADED, "nvml driver not loaded"},
		{"other init error", gonvml.ERROR_NO_PERMISSION, "nvml init failed: "},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			lib := &mock.Interface{}
			nvmlFuncs(lib)
			lib.InitFunc = func() gonvml.Return { return tt.rc }
			p := newProber(lib, newClock().now, retryInterval)

			snap := p.Snapshot(context.Background())
			if snap.Available {
				t.Fatal("Available = true after a failed Init")
			}
			if !strings.HasPrefix(snap.Reason, tt.reason) || snap.Reason == "" {
				t.Fatalf("Reason = %q, want prefix %q", snap.Reason, tt.reason)
			}
			p.Close()

			if n := len(lib.InitCalls()); n != 1 {
				t.Fatalf("Init calls = %d, want 1", n)
			}
			if n := totalCalls(lib) - len(lib.InitCalls()); n != 0 {
				t.Fatalf("%d NVML calls other than Init were made after a failed Init", n)
			}
		})
	}
}

// TestRetryHonorsInterval 는 실패 뒤의 재시도가 간격의 하한을 지키고, 성공하면 장치를 읽는지 확인한다.
func TestRetryHonorsInterval(t *testing.T) {
	clock := newClock()
	lib := &mock.Interface{}
	nvmlFuncs(lib)
	attempts := 0
	lib.InitFunc = func() gonvml.Return {
		attempts++
		if attempts == 1 {
			return gonvml.ERROR_LIBRARY_NOT_FOUND
		}
		return gonvml.SUCCESS
	}
	lib.DeviceGetCountFunc = func() (int, gonvml.Return) { return 0, gonvml.SUCCESS }
	lib.SystemGetDriverVersionFunc = func() (string, gonvml.Return) { return "595.91.07", gonvml.SUCCESS }
	p := newProber(lib, clock.now, 5*time.Second)
	ctx := context.Background()

	if snap := p.Snapshot(ctx); snap.Available {
		t.Fatal("first Snapshot is available, want unavailable")
	}
	clock.t = clock.t.Add(4 * time.Second)
	if snap := p.Snapshot(ctx); snap.Available || attempts != 1 {
		t.Fatalf("Snapshot before the interval: available=%v attempts=%d, want false and 1", snap.Available, attempts)
	}
	clock.t = clock.t.Add(time.Second)
	snap := p.Snapshot(ctx)
	if attempts != 2 {
		t.Fatalf("attempts = %d after the interval, want 2", attempts)
	}
	if !snap.Available || snap.DriverVersion != "595.91.07" {
		t.Fatalf("Snapshot after recovery = %+v", snap)
	}
	p.Close()
	if n := len(lib.ShutdownCalls()); n != 1 {
		t.Fatalf("Shutdown calls = %d, want 1 after a successful init", n)
	}
}

// TestReadsDevices 는 모든 필드가 읽히는 장치의 값을 확인한다.
func TestReadsDevices(t *testing.T) {
	lib := &mock.Interface{}
	nvmlFuncs(lib)
	lib.InitFunc = func() gonvml.Return { return gonvml.SUCCESS }
	lib.DeviceGetCountFunc = func() (int, gonvml.Return) { return 1, gonvml.SUCCESS }
	lib.SystemGetDriverVersionFunc = func() (string, gonvml.Return) { return "595.91.07", gonvml.SUCCESS }
	lib.SystemGetNVMLVersionFunc = func() (string, gonvml.Return) { return "13.595.91", gonvml.SUCCESS }
	lib.SystemGetCudaDriverVersionFunc = func() (int, gonvml.Return) { return 13020, gonvml.SUCCESS }
	lib.DeviceGetHandleByIndexFunc = func(i int) (gonvml.Device, gonvml.Return) {
		if i != 0 {
			return nil, gonvml.ERROR_INVALID_ARGUMENT
		}
		return fullDevice(), gonvml.SUCCESS
	}
	p := newProber(lib, newClock().now, retryInterval)
	defer p.Close()

	snap := p.Snapshot(context.Background())
	if !snap.Available || snap.Reason != "" {
		t.Fatalf("Snapshot = %+v", snap)
	}
	if snap.DriverVersion != "595.91.07" || snap.NVMLVersion != "13.595.91" || snap.CUDADriverVersion != "13.2" {
		t.Fatalf("system versions = %q %q %q", snap.DriverVersion, snap.NVMLVersion, snap.CUDADriverVersion)
	}
	if len(snap.Devices) != 1 {
		t.Fatalf("devices = %d, want 1", len(snap.Devices))
	}
	d := snap.Devices[0]
	want := platform.GPUDevice{
		Index: 0, Name: "NVIDIA GeForce RTX 3060", UUID: "GPU-1234", Arch: "ampere", ComputeCap: "8.6",
		MemTotalMiB: 12288, MemFreeMiB: 10240, MemUsedMiB: 2048,
		TemperatureC: 45, PowerMilliwatt: 35000, UtilGPUPercent: 12, UtilMemPercent: 7,
		Processes:      []platform.GPUProcess{{PID: 4242, UsedMiB: 1024}, {PID: 77, UsedMiB: -1}},
		ProcessesKnown: true,
	}
	if !equalDevice(d, want) {
		t.Fatalf("device = %+v\nwant   %+v", d, want)
	}
}

// TestPartialFailuresKeepOtherFields 는 개별 조회가 실패해도 나머지 필드를 계속 읽는지 확인한다.
func TestPartialFailuresKeepOtherFields(t *testing.T) {
	lib := &mock.Interface{}
	nvmlFuncs(lib)
	lib.InitFunc = func() gonvml.Return { return gonvml.SUCCESS }
	lib.DeviceGetCountFunc = func() (int, gonvml.Return) { return 1, gonvml.SUCCESS }
	lib.DeviceGetHandleByIndexFunc = func(int) (gonvml.Device, gonvml.Return) {
		dev := fullDevice()
		// 노트북 GPU 처럼 일부 조회를 지원하지 않는 장치를 흉내 낸다.
		dev.GetTemperatureFunc = func(gonvml.TemperatureSensors) (uint32, gonvml.Return) { return 0, gonvml.ERROR_NOT_SUPPORTED }
		dev.GetPowerUsageFunc = func() (uint32, gonvml.Return) { return 0, gonvml.ERROR_NOT_SUPPORTED }
		dev.GetComputeRunningProcessesFunc = func() ([]gonvml.ProcessInfo, gonvml.Return) {
			return nil, gonvml.ERROR_NOT_SUPPORTED
		}
		dev.GetArchitectureFunc = func() (gonvml.DeviceArchitecture, gonvml.Return) {
			return 0, gonvml.ERROR_NOT_SUPPORTED
		}
		return dev, gonvml.SUCCESS
	}
	p := newProber(lib, newClock().now, retryInterval)
	defer p.Close()

	d := p.Snapshot(context.Background()).Devices[0]
	if d.TemperatureC != -1 || d.PowerMilliwatt != -1 {
		t.Fatalf("unsupported temperature/power = %d/%d, want -1/-1", d.TemperatureC, d.PowerMilliwatt)
	}
	if d.ProcessesKnown || d.Processes != nil {
		t.Fatalf("processes known=%v list=%v, want unknown and nil", d.ProcessesKnown, d.Processes)
	}
	if d.Arch != "" {
		t.Fatalf("Arch = %q, want empty after a failed architecture query", d.Arch)
	}
	if d.Name != "NVIDIA GeForce RTX 3060" || d.MemTotalMiB != 12288 || d.UtilGPUPercent != 12 || d.ComputeCap != "8.6" {
		t.Fatalf("fields after the failures are wrong: %+v", d)
	}
}

// TestDeviceHandleFailureKeepsIndex 는 장치 핸들을 얻지 못해도 그 index 의 빈 장치를 남기는지 확인한다.
func TestDeviceHandleFailureKeepsIndex(t *testing.T) {
	lib := &mock.Interface{}
	nvmlFuncs(lib)
	lib.InitFunc = func() gonvml.Return { return gonvml.SUCCESS }
	lib.DeviceGetCountFunc = func() (int, gonvml.Return) { return 2, gonvml.SUCCESS }
	lib.DeviceGetHandleByIndexFunc = func(i int) (gonvml.Device, gonvml.Return) {
		return nil, gonvml.ERROR_GPU_IS_LOST
	}
	p := newProber(lib, newClock().now, retryInterval)
	defer p.Close()

	snap := p.Snapshot(context.Background())
	if !snap.Available || len(snap.Devices) != 2 {
		t.Fatalf("snapshot = %+v, want 2 devices", snap)
	}
	for i, d := range snap.Devices {
		if d.Index != i || d.MemTotalMiB != -1 || d.TemperatureC != -1 || d.ProcessesKnown {
			t.Fatalf("device %d = %+v, want unknown fields with its index", i, d)
		}
	}
}

// TestDeviceCountFailureIsUnavailable 는 장치 수를 읽지 못하면 Available 이 false 인지 확인한다.
func TestDeviceCountFailureIsUnavailable(t *testing.T) {
	lib := &mock.Interface{}
	nvmlFuncs(lib)
	lib.InitFunc = func() gonvml.Return { return gonvml.SUCCESS }
	lib.DeviceGetCountFunc = func() (int, gonvml.Return) { return 0, gonvml.ERROR_UNKNOWN }
	p := newProber(lib, newClock().now, retryInterval)
	defer p.Close()

	snap := p.Snapshot(context.Background())
	if snap.Available || !strings.HasPrefix(snap.Reason, "nvml device count failed") {
		t.Fatalf("snapshot = %+v", snap)
	}
}

// TestCloseWithoutInitMakesNoCalls 는 초기화에 성공하지 않은 Prober 의 Close 가 NVML 을 건드리지 않는지 확인한다.
func TestCloseWithoutInitMakesNoCalls(t *testing.T) {
	lib := &mock.Interface{}
	nvmlFuncs(lib)
	p := newProber(lib, newClock().now, retryInterval)
	p.Close()
	if n := totalCalls(lib); n != 0 {
		t.Fatalf("%d NVML calls on a prober that never initialized", n)
	}
	if snap := p.Snapshot(context.Background()); snap.Available {
		t.Fatal("Snapshot after Close is available")
	}
	if n := totalCalls(lib); n != 0 {
		t.Fatalf("Snapshot after Close made %d NVML calls", n)
	}
}

// TestSnapshotAfterCloseDoesNotReinit 는 Close 뒤의 Snapshot 이 Init 을 다시 부르지 않는지 확인한다.
func TestSnapshotAfterCloseDoesNotReinit(t *testing.T) {
	lib := &mock.Interface{}
	nvmlFuncs(lib)
	lib.InitFunc = func() gonvml.Return { return gonvml.SUCCESS }
	lib.DeviceGetCountFunc = func() (int, gonvml.Return) { return 0, gonvml.SUCCESS }
	p := newProber(lib, newClock().now, retryInterval)
	p.Snapshot(context.Background())
	p.Close()
	p.Snapshot(context.Background())
	if n := len(lib.InitCalls()); n != 1 {
		t.Fatalf("Init calls = %d, want 1", n)
	}
	if n := len(lib.ShutdownCalls()); n != 1 {
		t.Fatalf("Shutdown calls = %d, want 1", n)
	}
}

// TestNewWithoutLibraryDoesNotCrash 는 실제 go-nvml 을 쓰는 New 가 NVML 이 없는 환경에서 죽지 않고,
// 두 번 연속 호출해도 죽지 않으며, 쓸 수 없으면 이유를 돌려주는지 확인한다. NVIDIA 드라이버가 있는 호스트에서는
// Available 이 true 여도 된다.
func TestNewWithoutLibraryDoesNotCrash(t *testing.T) {
	p := New()
	defer p.Close()
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		snap := p.Snapshot(ctx)
		if !snap.Available && snap.Reason == "" {
			t.Fatalf("call %d: unavailable without a reason", i+1)
		}
		if !snap.Available && len(snap.Devices) != 0 {
			t.Fatalf("call %d: devices listed while unavailable", i+1)
		}
	}
}

// TestCUDAVersionAndArch 는 CUDA 버전 변환과 아키텍처 이름을 확인한다.
func TestCUDAVersionAndArch(t *testing.T) {
	versions := map[int]string{13020: "13.2", 12080: "12.8", 12000: "12.0", 11040: "11.4"}
	for v, want := range versions {
		if got := cudaVersion(v); got != want {
			t.Fatalf("cudaVersion(%d) = %q, want %q", v, got, want)
		}
	}
	arches := map[gonvml.DeviceArchitecture]string{
		gonvml.DEVICE_ARCH_AMPERE:    "ampere",
		gonvml.DEVICE_ARCH_ADA:       "ada",
		gonvml.DEVICE_ARCH_BLACKWELL: "blackwell",
		gonvml.DEVICE_ARCH_HOPPER:    "unknown(9)",
		gonvml.DEVICE_ARCH_TURING:    "unknown(6)",
	}
	for a, want := range arches {
		if got := archName(a); got != want {
			t.Fatalf("archName(%d) = %q, want %q", a, got, want)
		}
	}
}

// TestProcessMiB 는 프로세스 메모리 단위 변환과 알 수 없는 값을 확인한다.
func TestProcessMiB(t *testing.T) {
	if got := processMiB(3 << 20); got != 3 {
		t.Fatalf("processMiB(3 MiB) = %d, want 3", got)
	}
	if got := processMiB(math.MaxUint64); got != -1 {
		t.Fatalf("processMiB(NVML_VALUE_NOT_AVAILABLE) = %d, want -1", got)
	}
}

// fullDevice 는 모든 조회가 성공하는 mock 장치를 만든다. RTX 3060 을 흉내 낸 값이다.
func fullDevice() *mock.Device {
	return &mock.Device{
		GetNameFunc: func() (string, gonvml.Return) { return "NVIDIA GeForce RTX 3060", gonvml.SUCCESS },
		GetUUIDFunc: func() (string, gonvml.Return) { return "GPU-1234", gonvml.SUCCESS },
		GetArchitectureFunc: func() (gonvml.DeviceArchitecture, gonvml.Return) {
			return gonvml.DEVICE_ARCH_AMPERE, gonvml.SUCCESS
		},
		GetCudaComputeCapabilityFunc: func() (int, int, gonvml.Return) { return 8, 6, gonvml.SUCCESS },
		GetMemoryInfoFunc: func() (gonvml.Memory, gonvml.Return) {
			return gonvml.Memory{Total: 12288 << 20, Free: 10240 << 20, Used: 2048 << 20}, gonvml.SUCCESS
		},
		GetTemperatureFunc: func(gonvml.TemperatureSensors) (uint32, gonvml.Return) { return 45, gonvml.SUCCESS },
		GetPowerUsageFunc:  func() (uint32, gonvml.Return) { return 35000, gonvml.SUCCESS },
		GetUtilizationRatesFunc: func() (gonvml.Utilization, gonvml.Return) {
			return gonvml.Utilization{Gpu: 12, Memory: 7}, gonvml.SUCCESS
		},
		GetComputeRunningProcessesFunc: func() ([]gonvml.ProcessInfo, gonvml.Return) {
			return []gonvml.ProcessInfo{
				{Pid: 4242, UsedGpuMemory: 1024 << 20},
				{Pid: 77, UsedGpuMemory: math.MaxUint64},
			}, gonvml.SUCCESS
		},
	}
}

// equalDevice 는 두 장치 값이 같은지 비교한다. 프로세스 목록은 순서까지 같아야 한다.
func equalDevice(a, b platform.GPUDevice) bool {
	if a.Index != b.Index || a.Name != b.Name || a.UUID != b.UUID || a.Arch != b.Arch || a.ComputeCap != b.ComputeCap {
		return false
	}
	if a.MemTotalMiB != b.MemTotalMiB || a.MemFreeMiB != b.MemFreeMiB || a.MemUsedMiB != b.MemUsedMiB {
		return false
	}
	if a.TemperatureC != b.TemperatureC || a.PowerMilliwatt != b.PowerMilliwatt ||
		a.UtilGPUPercent != b.UtilGPUPercent || a.UtilMemPercent != b.UtilMemPercent {
		return false
	}
	if a.ProcessesKnown != b.ProcessesKnown || len(a.Processes) != len(b.Processes) {
		return false
	}
	for i := range a.Processes {
		if a.Processes[i] != b.Processes[i] {
			return false
		}
	}
	return true
}
