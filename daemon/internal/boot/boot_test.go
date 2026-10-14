package boot

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/platform"
)

// 사용자 PC(MSI Stealth 15M, 2026-10-14)에서 읽은 efibootmgr -v 출력이다. 순서가 USB, Windows, opereta 다.
const msiOutput = `BootCurrent: 0000
Timeout: 1 seconds
BootOrder: 0002,0001,0000
Boot0000* opereta	HD(1,GPT,5890b213-6ef7-4998-a28a-e5858f496b65,0x800,0x32000)/File(\EFI\OPERETA\SHIMX64.EFI)
Boot0001* Windows Boot Manager	HD(1,GPT,5890b213-6ef7-4998-a28a-e5858f496b65,0x800,0x32000)/File(\EFI\MICROSOFT\BOOT\BOOTMGFW.EFI)WINDOWS.........x...L.......
Boot0002* UEFI: SanDisk, Partition 1	PciRoot(0x0)/Pci(0x14,0x0)/USB(2,0)/HD(1,MBR,0x3eb596,0x800,0x3a7f7e0)..BO
`

type fakeRunner struct {
	out   map[string]string // 명령 줄 -> stdout
	exit  map[string]int
	calls []string
}

func (f *fakeRunner) Run(_ context.Context, c platform.Command) (platform.Result, error) {
	line := c.Name + " " + strings.Join(c.Args, " ")
	f.calls = append(f.calls, strings.TrimSpace(line))
	for k, v := range f.out {
		if strings.HasPrefix(line, k) {
			return platform.Result{Stdout: []byte(v), ExitCode: f.exit[k]}, nil
		}
	}
	return platform.Result{}, nil
}

func (f *fakeRunner) called(prefix string) bool {
	for _, c := range f.calls {
		if strings.HasPrefix(c, prefix) {
			return true
		}
	}
	return false
}

func newCtl(t *testing.T, r *fakeRunner, esp bool) *Controller {
	t.Helper()
	dir := t.TempDir()
	efi := filepath.Join(dir, "efi")
	if err := os.MkdirAll(filepath.Join(efi, "efivars"), 0o755); err != nil {
		t.Fatal(err)
	}
	espDir := filepath.Join(dir, "esp")
	if esp {
		for _, d := range []string{"EFI/opereta", "EFI/BOOT", "EFI/diparnux", "EFI/Microsoft/Boot"} {
			if err := os.MkdirAll(filepath.Join(espDir, d), 0o755); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.WriteFile(filepath.Join(espDir, "EFI/BOOT/BOOTX64.EFI"), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &Controller{Runner: r, EFIDir: efi, ESPDir: espDir, StateDir: filepath.Join(dir, "state")}
}

func TestParseRealOutput(t *testing.T) {
	in := Parse(msiOutput)
	if in.Current != "0000" || strings.Join(in.Order, ",") != "0002,0001,0000" {
		t.Fatalf("current=%q order=%v", in.Current, in.Order)
	}
	if in.OperetaEntry != "0000" || in.WindowsEntry != "0001" {
		t.Fatalf("opereta=%q windows=%q", in.OperetaEntry, in.WindowsEntry)
	}
	kinds := map[string]string{}
	for _, e := range in.Entries {
		kinds[e.Num] = e.Kind
	}
	if kinds["0000"] != "opereta" || kinds["0001"] != "windows" || kinds["0002"] != "removable" {
		t.Fatalf("kinds = %v", kinds)
	}
	if in.Entries[0].Loader != `\EFI\OPERETA\SHIMX64.EFI` || !in.Entries[0].Active {
		t.Fatalf("entry 0: %+v", in.Entries[0])
	}
}

func TestParseToleratesSpacesInOrderAndLegacyEntries(t *testing.T) {
	in := Parse("BootNext: 0003\nBootOrder: 0003, 0000\nBoot0000* Diparnux\tHD(1,GPT,x)/File(\\EFI\\diparnux\\shimx64.efi)\nBoot0003  opereta\tHD(1,GPT,x)/File(\\EFI\\opereta\\shimx64.efi)\n")
	if in.Next != "0003" || strings.Join(in.Order, ",") != "0003,0000" {
		t.Fatalf("%+v", in)
	}
	if in.Entries[0].Kind != "legacy" || in.Entries[1].Kind != "opereta" || in.Entries[1].Active {
		t.Fatalf("%+v", in.Entries)
	}
}

func TestStatusFindsTheProblemsOfTheRealMachine(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"efibootmgr -v": msiOutput, "lsblk": "vfat\nBitLocker\next4\n"}}
	c := newCtl(t, r, true)
	// 이전에 순서를 바꿨는데 펌웨어가 되돌렸다.
	c.saveState(state{ExpectedOrder: []string{"0000", "0001", "0002"}})
	in := c.Status(context.Background())
	if !in.Available || !in.UEFI || !in.BitLocker {
		t.Fatalf("%+v", in)
	}
	codes := map[string]bool{}
	for _, p := range in.Problems {
		codes[p.Code] = true
	}
	// 지금 부팅한 것이 opereta 이므로 "먼저 뜨지 않음" 문제는 아니다.
	if codes[ProblemNotFirst] {
		t.Fatalf("opereta is the current entry, so not_first must not be reported: %v", codes)
	}
	if !codes[ProblemFirmwareReorders] || !codes[ProblemLegacyEntries] {
		t.Fatalf("problems = %v", codes)
	}
	if !in.ESP.Fallback || !in.ESP.OperetaFiles || len(in.ESP.LegacyFolders) != 1 || in.ESP.LegacyFolders[0] != "diparnux" {
		t.Fatalf("esp = %+v", in.ESP)
	}
}

func TestStatusNotFirstWhenBootedAnotherEntry(t *testing.T) {
	out := strings.Replace(msiOutput, "BootCurrent: 0000", "BootCurrent: 0001", 1)
	c := newCtl(t, &fakeRunner{out: map[string]string{"efibootmgr -v": out}}, true)
	in := c.Status(context.Background())
	found := false
	for _, p := range in.Problems {
		found = found || p.Code == ProblemNotFirst
	}
	if !found {
		t.Fatalf("problems = %+v", in.Problems)
	}
}

func TestStatusWithoutUEFI(t *testing.T) {
	c := &Controller{Runner: &fakeRunner{}, EFIDir: filepath.Join(t.TempDir(), "none"), ESPDir: t.TempDir(), StateDir: t.TempDir()}
	in := c.Status(context.Background())
	if in.UEFI || in.Available || len(in.Problems) != 1 || in.Problems[0].Code != ProblemNotUEFI {
		t.Fatalf("%+v", in)
	}
	if err := c.Next(context.Background(), "opereta"); err == nil || err.(*Error).Code != CodeNotUEFI {
		t.Fatalf("err = %v", err)
	}
}

func TestNextOrderAndTargets(t *testing.T) {
	r := &fakeRunner{out: map[string]string{"efibootmgr -v": msiOutput}}
	c := newCtl(t, r, true)
	ctx := context.Background()
	if err := c.Next(ctx, "windows"); err != nil || !r.called("efibootmgr -n 0001") {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
	if err := c.Order(ctx, "opereta"); err != nil || !r.called("efibootmgr -o 0000,0002,0001") {
		t.Fatalf("err=%v calls=%v", err, r.calls)
	}
	if got := c.loadState().ExpectedOrder; strings.Join(got, ",") != "0000,0002,0001" {
		t.Fatalf("expected order = %v", got)
	}
	if err := c.Next(ctx, "usb"); err != nil || !r.called("efibootmgr -n 0002") {
		t.Fatalf("usb: %v %v", err, r.calls)
	}
	if err := c.Next(ctx, "0001"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "ubuntu", "00zz", "0009", "../../etc"} {
		if err := c.Next(ctx, bad); err == nil {
			t.Fatalf("target %q was accepted", bad)
		}
	}
}

func TestValidTarget(t *testing.T) {
	for _, ok := range []string{"opereta", "Windows", "usb", "0001", "00aF"} {
		if !ValidTarget(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "x", "12345", "opereta;rm", "-n"} {
		if ValidTarget(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}

func TestCleanupRemovesOnlyLegacyNames(t *testing.T) {
	out := msiOutput + "Boot0003* Diparnux\tHD(1,GPT,x)/File(\\EFI\\diparnux\\shimx64.efi)\n"
	r := &fakeRunner{out: map[string]string{"efibootmgr -v": out}}
	c := newCtl(t, r, true)
	if err := c.Cleanup(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.ESPDir, "EFI/diparnux")); err == nil {
		t.Fatal("the legacy folder was not removed")
	}
	for _, keep := range []string{"EFI/opereta", "EFI/BOOT/BOOTX64.EFI", "EFI/Microsoft/Boot"} {
		if _, err := os.Stat(filepath.Join(c.ESPDir, keep)); err != nil {
			t.Fatalf("%s was removed", keep)
		}
	}
	if !r.called("efibootmgr -B -b 0003") {
		t.Fatalf("calls = %v", r.calls)
	}
	if r.called("efibootmgr -B -b 0000") || r.called("efibootmgr -B -b 0001") {
		t.Fatalf("a non-legacy entry was deleted: %v", r.calls)
	}
}

func TestRepairCreatesTheEntryWhenGrubInstallDoesNot(t *testing.T) {
	noEntry := "BootCurrent: 0001\nBootOrder: 0001\nBoot0001* Windows Boot Manager\tHD(1,GPT,x)/File(\\EFI\\MICROSOFT\\BOOT\\BOOTMGFW.EFI)\n"
	r := &fakeRunner{out: map[string]string{"efibootmgr -v": noEntry, "findmnt": "/dev/nvme0n1p1\n"}}
	c := newCtl(t, r, true)
	if err := os.WriteFile(filepath.Join(c.ESPDir, "EFI/opereta/shimx64.efi"), []byte("s"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := c.Repair(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !r.called("grub-install --target=x86_64-efi") || !r.called("update-grub") {
		t.Fatalf("calls = %v", r.calls)
	}
	if !r.called("efibootmgr --create --disk /dev/nvme0n1 --part 1 --loader") {
		t.Fatalf("the entry was not created: %v", r.calls)
	}
}

func TestSplitPartition(t *testing.T) {
	for dev, want := range map[string][2]string{
		"/dev/nvme0n1p1": {"/dev/nvme0n1", "1"},
		"/dev/sda2":      {"/dev/sda", "2"},
		"/dev/mmcblk0p3": {"/dev/mmcblk0", "3"},
	} {
		d, p, ok := SplitPartition(dev)
		if !ok || d != want[0] || p != want[1] {
			t.Errorf("%s -> %s %s %v", dev, d, p, ok)
		}
	}
	if _, _, ok := SplitPartition("/dev/mapper/x"); ok {
		t.Error("a mapper device was accepted")
	}
}
