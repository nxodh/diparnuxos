package update

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

func TestCompareVersions(t *testing.T) {
	cases := []struct {
		a, b string
		want int
	}{
		{"6.12.111-1", "6.12.112-1", -1},
		{"6.12.112-1", "6.12.111-1", 1},
		{"6.12.9-1", "6.12.10-1", -1},
		{"595.91.07-1", "595.91.07-1", 0},
		{"595.71.05-1", "595.91.07-1", -1},
		{"1:1.0-1", "2.0-1", 1},
		{"1.0~rc1-1", "1.0-1", -1},
		{"1.0-1+b1", "1.0-1", 1},
		{"6.12.111-1", "6.12.111-1+deb13u1", -1},
	}
	for _, c := range cases {
		got := compareVersions(c.a, c.b)
		if (got < 0) != (c.want < 0) || (got > 0) != (c.want > 0) {
			t.Errorf("compareVersions(%q, %q) = %d, want sign %d", c.a, c.b, got, c.want)
		}
	}
}

func TestParseSimulation(t *testing.T) {
	out := `NOTE: This is only a simulation!
Inst linux-image-amd64 [6.12.111-1] (6.12.112-1 Debian-Security:13/stable-security [amd64])
Inst linux-image-6.12.112+deb13-amd64 (6.12.112-1 Debian-Security:13/stable-security [amd64])
Conf linux-image-amd64 (6.12.112-1 Debian-Security:13/stable-security [amd64])
Remv libfoo1 [1.0]
`
	changes, removals := parseSimulation(out)
	if len(changes) != 2 {
		t.Fatalf("changes = %v", changes)
	}
	if changes[0].Name != "linux-image-amd64" || changes[0].From != "6.12.111-1" || changes[0].To != "6.12.112-1" || changes[0].New {
		t.Errorf("first change = %+v", changes[0])
	}
	if !changes[1].New || changes[1].To != "6.12.112-1" {
		t.Errorf("second change = %+v", changes[1])
	}
	if len(removals) != 1 || removals[0] != "libfoo1" {
		t.Errorf("removals = %v", removals)
	}
}

func TestKernelRelease(t *testing.T) {
	for name, want := range map[string]string{
		"linux-image-6.12.112+deb13-amd64":          "6.12.112+deb13-amd64",
		"linux-image-amd64":                         "",
		"linux-image-6.12.112+deb13-amd64-unsigned": "",
		"linux-image-6.12.112+deb13-amd64-dbg":      "",
		"linux-headers-6.12.112+deb13-amd64":        "",
	} {
		if got := kernelRelease(name); got != want {
			t.Errorf("kernelRelease(%q) = %q, want %q", name, got, want)
		}
	}
	if isKernelVersioned("linux-headers-amd64") || !isKernelVersioned("linux-headers-6.12.1-common") || !isKernelVersioned("linux-kbuild-6.12.x") {
		t.Error("isKernelVersioned misclassified")
	}
}

func TestUpstreamVersion(t *testing.T) {
	for in, want := range map[string]string{"595.91.07-1": "595.91.07", "1:5.0-2": "5.0", "6.12.111-1+deb13u1": "6.12.111"} {
		if got := upstreamVersion(in); got != want {
			t.Errorf("upstreamVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

// newKernelUpdate 는 새 커널 6.12.112 가 올라온 상황을 만든다.
func newKernelUpdate(f *fakeSys) string {
	rel := "6.12.112+deb13-amd64"
	f.newKernels[rel] = "6.12.112-1"
	f.cand["linux-image-amd64"] = "6.12.112-1"
	f.cand["linux-headers-amd64"] = "6.12.112-1"
	return rel
}

func mustApply(t *testing.T, e *Engine) *State {
	t.Helper()
	st, err := e.Apply(context.Background(), ApplyOptions{})
	if err != nil {
		t.Fatalf("Apply: %v", err)
	}
	return st
}

func codeOf(err error) string {
	var ue *Error
	if errors.As(err, &ue) {
		return ue.Code
	}
	return "other:" + err.Error()
}

func TestApplyKernelUpdateThenVerifyCommits(t *testing.T) {
	f := newFakeSys(t)
	rel := newKernelUpdate(f)
	e := f.engine()

	st := mustApply(t, e)
	if st.Phase != PhasePendingBoot || st.NewKernel != rel || !st.RebootRequired {
		t.Fatalf("state = %+v", st)
	}
	// 새 커널을 설치하기 전에 지금 커널이 저장된 기본값이 되어야 한다. 이후 바뀌면 안 된다.
	if f.savedEntry != "gnulinux-6.12.111+deb13-amd64-advanced-uuid" {
		t.Errorf("saved default changed before verification: %q", f.savedEntry)
	}
	if f.nextEntry != "gnulinux-"+rel+"-advanced-uuid" {
		t.Errorf("next_entry = %q", f.nextEntry)
	}
	for _, n := range []string{"linux-image-" + rel, "linux-headers-" + rel} {
		if !f.held[n] {
			t.Errorf("%s is not held again after the update", n)
		}
	}
	if f.modules[rel] != "595.91.07" {
		t.Errorf("modules[%s] = %q", rel, f.modules[rel])
	}
	if st.PreviousKernel != "6.12.111+deb13-amd64" || st.PreviousDefault == "" {
		t.Errorf("previous = %q / %q", st.PreviousKernel, st.PreviousDefault)
	}
	// 부팅 뒤 검증 전에는 다시 적용할 수 없다.
	if _, err := e.Apply(context.Background(), ApplyOptions{}); codeOf(err) != CodePendingBoot {
		t.Errorf("second apply: %v", err)
	}

	// 새 커널로 부팅했다.
	f.running = rel
	f.writeCmdline()
	vst, err := e.Verify(context.Background())
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if vst.Phase != PhaseCommitted || vst.RebootRequired {
		t.Fatalf("verify state = %+v", vst)
	}
	if f.savedEntry != "gnulinux-"+rel+"-advanced-uuid" {
		t.Errorf("saved default = %q", f.savedEntry)
	}
	loaded, _ := e.LoadState()
	if loaded.Phase != PhaseCommitted {
		t.Errorf("persisted phase = %q", loaded.Phase)
	}
	// 이전 커널은 남아 있어야 한다.
	if f.pkgs["linux-image-6.12.111+deb13-amd64"] == nil {
		t.Error("the previous kernel was removed")
	}
}

func TestApplyPrunesOlderKernelsOnCommit(t *testing.T) {
	f := newFakeSys(t)
	// 이미 오래된 커널 6.12.100 이 있다.
	old := "6.12.100+deb13-amd64"
	f.pkgs["linux-image-"+old] = &fakePkg{version: "6.12.100-1", source: "linux"}
	f.pkgs["linux-headers-"+old] = &fakePkg{version: "6.12.100-1", source: "linux"}
	f.modules[old] = "595.91.07"
	f.writeInitrd(old)
	f.writeGrub()
	rel := newKernelUpdate(f)
	e := f.engine()
	mustApply(t, e)
	f.running = rel
	f.writeCmdline()
	if _, err := e.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.pkgs["linux-image-"+old] != nil || f.pkgs["linux-headers-"+old] != nil {
		t.Error("the oldest kernel should be pruned")
	}
	if f.pkgs["linux-image-6.12.111+deb13-amd64"] == nil || f.pkgs["linux-image-"+rel] == nil {
		t.Error("the previous and the new kernel must remain")
	}
}

func TestApplyDKMSFailureKeepsPreviousKernel(t *testing.T) {
	f := newFakeSys(t)
	rel := newKernelUpdate(f)
	f.dkmsFail[rel] = true
	e := f.engine()

	st, err := e.Apply(context.Background(), ApplyOptions{})
	if codeOf(err) != CodeDKMS {
		t.Fatalf("error = %v", err)
	}
	if st.Phase != PhaseFailed {
		t.Errorf("phase = %q", st.Phase)
	}
	if f.pkgs["linux-image-"+rel] != nil || f.pkgs["linux-headers-"+rel] != nil {
		t.Error("the failed kernel packages must be removed")
	}
	if f.nextEntry != "" || f.savedEntry != "gnulinux-6.12.111+deb13-amd64-advanced-uuid" {
		t.Errorf("boot selection changed: next=%q saved=%q", f.nextEntry, f.savedEntry)
	}
	if f.pkgs["linux-image-6.12.111+deb13-amd64"] == nil {
		t.Error("previous kernel missing")
	}
	// 메타 패키지가 새 버전으로 올라가지 않았다.
	if v := f.pkgs["linux-image-amd64"].version; v != "6.12.111-1" {
		t.Errorf("meta package version = %s", v)
	}
	if !f.held["linux-image-amd64"] {
		t.Error("hold was not restored")
	}
}

func TestApplyKernelModulesMissingAfterInstall(t *testing.T) {
	f := newFakeSys(t)
	rel := newKernelUpdate(f)
	e := f.engine()
	// 설치는 성공했지만 모듈이 없는 상황을 만든다.
	hook := &modulesDropper{fakeSys: f, rel: rel}
	e.Run = hook
	st, err := e.Apply(context.Background(), ApplyOptions{})
	if codeOf(err) != CodeDKMS {
		t.Fatalf("error = %v", err)
	}
	if st.Phase != PhaseFailed || f.pkgs["linux-image-"+rel] != nil {
		t.Errorf("state=%+v pkg=%v", st, f.pkgs["linux-image-"+rel])
	}
}

// modulesDropper 는 새 커널 설치 직후 모듈을 지워 검증 실패를 만든다.
type modulesDropper struct {
	*fakeSys
	rel string
}

func (m *modulesDropper) Run(ctx context.Context, cmd platform.Command) (platform.Result, error) {
	res, err := m.fakeSys.Run(ctx, cmd)
	if cmd.Name == "apt-get" && strings.Contains(strings.Join(cmd.Args, " "), "install") && strings.Contains(strings.Join(cmd.Args, " "), "linux-image-"+m.rel) {
		delete(m.fakeSys.modules, m.rel)
	}
	return res, err
}

func TestApplyDriverUpdateThroughPolicyPackage(t *testing.T) {
	f := newFakeSys(t)
	f.cand["lightlix-nvidia"] = "0.4.0"
	f.cand["nvidia-kernel-open-dkms"] = "595.100.01-1"
	f.cand["nvidia-driver"] = "595.100.01-1"
	e := f.engine()
	// 핀 파일이 있어도 임시 preferences 에서는 빠져야 한다.
	if err := os.WriteFile(f.path("etc/apt/preferences.d/00-lightlix-nvidia.pref"), []byte("Package: x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.path("etc/apt/preferences.d/nvidia-driver-pinning-595"), []byte("Package: y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st := mustApply(t, e)
	if st.Phase != PhasePendingBoot || st.NewKernel != "" || !st.RebootRequired {
		t.Fatalf("state = %+v", st)
	}
	if st.DriverFrom != "595.91.07-1" || st.DriverTo != "595.100.01-1" {
		t.Errorf("driver %q -> %q", st.DriverFrom, st.DriverTo)
	}
	if f.modules["6.12.111+deb13-amd64"] != "595.100.01" {
		t.Errorf("module version = %q", f.modules["6.12.111+deb13-amd64"])
	}
	if f.nextEntry != "" {
		t.Errorf("a driver-only update must not select another kernel: %q", f.nextEntry)
	}
	// 같은 커널로 다시 부팅하면 검증이 통과한다.
	vst, err := e.Verify(context.Background())
	if err != nil || vst.Phase != PhaseCommitted {
		t.Fatalf("verify = %+v, %v", vst, err)
	}
}

func TestPrefsOverrideDropsOnlyPolicyPin(t *testing.T) {
	f := newFakeSys(t)
	e := f.engine()
	for name, body := range map[string]string{"00-lightlix-nvidia.pref": "a", "nvidia-driver-pinning-595": "b", "other.pref": "c"} {
		if err := os.WriteFile(f.path("etc/apt/preferences.d/"+name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	dir, cleanup, err := e.prefsOverride()
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if _, err := os.Stat(dir + "/00-lightlix-nvidia.pref"); err == nil {
		t.Error("the exact driver pin must be left out")
	}
	for _, n := range []string{"nvidia-driver-pinning-595", "other.pref"} {
		if _, err := os.Stat(dir + "/" + n); err != nil {
			t.Errorf("%s was not copied", n)
		}
	}
}

func TestApplyDriverUpgradeFailureRestoresPreviousVersions(t *testing.T) {
	f := newFakeSys(t)
	f.cand["lightlix-nvidia"] = "0.4.0"
	f.cand["nvidia-kernel-open-dkms"] = "595.100.01-1"
	f.cand["nvidia-driver"] = "595.100.01-1"
	f.upgradeFail = true
	e := f.engine()
	st, err := e.Apply(context.Background(), ApplyOptions{})
	if codeOf(err) != CodeDKMS {
		t.Fatalf("error = %v", err)
	}
	if st.Phase != PhaseFailed {
		t.Errorf("phase = %q", st.Phase)
	}
	if v := f.pkgs["nvidia-kernel-open-dkms"].version; v != "595.91.07-1" {
		t.Errorf("driver package not restored: %s", v)
	}
	if !strings.Contains(st.Message, "restored") {
		t.Errorf("message does not say the driver was restored: %s", st.Message)
	}
}

func TestApplyUnexpectedRemovalIsRefusedBeforeInstalling(t *testing.T) {
	f := newFakeSys(t)
	newKernelUpdate(f)
	f.removeOnUpg = []string{"lightlix-nvidia"}
	e := f.engine()
	st, err := e.Apply(context.Background(), ApplyOptions{})
	if codeOf(err) != CodeUnexpectedRemoval {
		t.Fatalf("error = %v", err)
	}
	if st.Phase != PhaseFailed {
		t.Errorf("phase = %q", st.Phase)
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "apt-get") && strings.Contains(c, " install ") && !strings.Contains(c, " -s ") {
			t.Errorf("nothing may be installed after the refusal: %s", c)
		}
	}
}

func TestApplyAllowsUnrelatedRemoval(t *testing.T) {
	f := newFakeSys(t)
	f.cand["bash"] = "5.2.37-3"
	f.removeOnUpg = []string{"libfoo1"}
	e := f.engine()
	st := mustApply(t, e)
	if st.Phase == PhaseFailed {
		t.Fatalf("state = %+v", st)
	}
}

func TestApplyUpToDateKeepsState(t *testing.T) {
	f := newFakeSys(t)
	e := f.engine()
	// 이전 성공 기록이 있다.
	prior := &State{Phase: PhaseCommitted, Operation: "apply", PreviousKernel: "6.12.100+deb13-amd64"}
	if err := e.save(prior); err != nil {
		t.Fatal(err)
	}
	st := mustApply(t, e)
	if st.Message != "up to date" {
		t.Errorf("state = %+v", st)
	}
	loaded, _ := e.LoadState()
	if loaded.PreviousKernel != "6.12.100+deb13-amd64" || loaded.Phase != PhaseCommitted {
		t.Errorf("persisted state was overwritten: %+v", loaded)
	}
}

func TestApplyOtherUpdatesOnlyNeedNoReboot(t *testing.T) {
	f := newFakeSys(t)
	f.cand["bash"] = "5.2.37-3"
	e := f.engine()
	st := mustApply(t, e)
	if st.Phase != PhaseCommitted || st.RebootRequired {
		t.Errorf("state = %+v", st)
	}
	if f.pkgs["bash"].version != "5.2.37-3" {
		t.Error("bash was not upgraded")
	}
}

func TestApplyRefusals(t *testing.T) {
	t.Run("live session", func(t *testing.T) {
		f := newFakeSys(t)
		if err := os.MkdirAll(f.path("run/live"), 0o755); err != nil {
			t.Fatal(err)
		}
		_, err := f.engine().Apply(context.Background(), ApplyOptions{})
		if codeOf(err) != CodeLiveSession {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("disk space", func(t *testing.T) {
		f := newFakeSys(t)
		e := f.engine()
		e.FreeBytes = func(string) (uint64, error) { return 10 << 20, nil }
		_, err := e.Apply(context.Background(), ApplyOptions{})
		if codeOf(err) != CodeDiskSpace {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("network", func(t *testing.T) {
		f := newFakeSys(t)
		f.updateFail = true
		_, err := f.engine().Apply(context.Background(), ApplyOptions{})
		if codeOf(err) != CodeNetwork {
			t.Errorf("error = %v", err)
		}
	})
	t.Run("busy", func(t *testing.T) {
		f := newFakeSys(t)
		e := f.engine()
		unlock, err := e.lock()
		if err != nil {
			t.Fatal(err)
		}
		defer unlock()
		_, err = f.engine().Apply(context.Background(), ApplyOptions{})
		if codeOf(err) != CodeBusy {
			t.Errorf("error = %v", err)
		}
	})
}

func TestVerifyNewKernelDidNotBoot(t *testing.T) {
	f := newFakeSys(t)
	rel := newKernelUpdate(f)
	e := f.engine()
	mustApply(t, e)
	// 새 커널이 부팅하지 못해 이전 커널로 돌아왔다(running 은 그대로).
	st, err := e.Verify(context.Background())
	if codeOf(err) != CodeVerify {
		t.Fatalf("error = %v", err)
	}
	if st.Phase != PhaseFailed {
		t.Errorf("phase = %q", st.Phase)
	}
	if f.savedEntry != "gnulinux-6.12.111+deb13-amd64-advanced-uuid" {
		t.Errorf("saved default changed: %q", f.savedEntry)
	}
	if f.pkgs["linux-image-"+rel] == nil {
		t.Error("the new kernel stays installed so it can be examined")
	}
}

func TestVerifyGPUDiagnosticsFailure(t *testing.T) {
	f := newFakeSys(t)
	rel := newKernelUpdate(f)
	f.gpuExit = 2
	e := f.engine()
	mustApply(t, e)
	f.running = rel
	f.writeCmdline()
	st, err := e.Verify(context.Background())
	if codeOf(err) != CodeVerify {
		t.Fatalf("error = %v", err)
	}
	if st.Phase != PhaseFailed || f.savedEntry != "gnulinux-6.12.111+deb13-amd64-advanced-uuid" {
		t.Errorf("state = %+v saved = %q", st, f.savedEntry)
	}
}

func TestVerifyWithoutPendingUpdate(t *testing.T) {
	f := newFakeSys(t)
	_, err := f.engine().Verify(context.Background())
	if codeOf(err) != CodeNoPending {
		t.Errorf("error = %v", err)
	}
}

func TestRollbackAfterDriverAndKernelUpdate(t *testing.T) {
	f := newFakeSys(t)
	rel := newKernelUpdate(f)
	f.cand["lightlix-nvidia"] = "0.4.0"
	f.cand["nvidia-kernel-open-dkms"] = "595.100.01-1"
	f.cand["nvidia-driver"] = "595.100.01-1"
	e := f.engine()
	mustApply(t, e)
	f.running = rel
	f.writeCmdline()
	if _, err := e.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	if f.savedEntry != "gnulinux-"+rel+"-advanced-uuid" {
		t.Fatalf("saved default = %q", f.savedEntry)
	}

	st, err := e.Rollback(context.Background(), ApplyOptions{})
	if err != nil {
		t.Fatalf("Rollback: %v", err)
	}
	if st.Phase != PhaseRolledBack || !st.RebootRequired {
		t.Errorf("state = %+v", st)
	}
	if v := f.pkgs["nvidia-kernel-open-dkms"].version; v != "595.91.07-1" {
		t.Errorf("driver version = %s", v)
	}
	if f.savedEntry != "gnulinux-6.12.111+deb13-amd64-advanced-uuid" {
		t.Errorf("saved default = %q", f.savedEntry)
	}
	// 모든 커널의 모듈이 되돌린 드라이버 버전이다.
	for k, v := range f.modules {
		if v != "595.91.07" {
			t.Errorf("modules[%s] = %s", k, v)
		}
	}
	// 롤백 정보는 한 번 쓰면 사라진다.
	if _, err := e.Rollback(context.Background(), ApplyOptions{}); codeOf(err) != CodeRollbackNA {
		t.Errorf("second rollback: %v", err)
	}
}

func TestRollbackUnavailableWhenOldVersionIsGone(t *testing.T) {
	f := newFakeSys(t)
	f.cand["lightlix-nvidia"] = "0.4.0"
	f.cand["nvidia-kernel-open-dkms"] = "595.100.01-1"
	f.cand["nvidia-driver"] = "595.100.01-1"
	e := f.engine()
	mustApply(t, e)
	if _, err := e.Verify(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.oldUnavailable = true
	_, err := e.Rollback(context.Background(), ApplyOptions{})
	if codeOf(err) != CodeRollbackNA {
		t.Fatalf("error = %v", err)
	}
	if v := f.pkgs["nvidia-kernel-open-dkms"].version; v != "595.100.01-1" {
		t.Errorf("driver must stay at the new version: %s", v)
	}
}

func TestRollbackWithoutHistory(t *testing.T) {
	f := newFakeSys(t)
	_, err := f.engine().Rollback(context.Background(), ApplyOptions{})
	if codeOf(err) != CodeRollbackNA {
		t.Errorf("error = %v", err)
	}
}

func TestStatusReportsKernelsAndDefault(t *testing.T) {
	f := newFakeSys(t)
	st, err := f.engine().Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if st.RunningKernel != "6.12.111+deb13-amd64" || st.GrubDefault == "" || len(st.Kernels) != 1 || st.Live {
		t.Errorf("status = %+v", st)
	}
}

func TestApplyRebootFlag(t *testing.T) {
	f := newFakeSys(t)
	newKernelUpdate(f)
	e := f.engine()
	if _, err := e.Apply(context.Background(), ApplyOptions{Reboot: true}); err != nil {
		t.Fatal(err)
	}
	if !f.rebooted {
		t.Error("systemctl reboot was not called")
	}
}
