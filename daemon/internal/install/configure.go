package install

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/boot"
	"github.com/nxodh/operetaos/daemon/internal/update"
)

// protectedPackages 는 대상 마무리 뒤에도 남아 있어야 하는 패키지다. 하나라도 없으면 설치를 실패로 본다.
var protectedPackages = []string{"opereta-daemon", "opereta-nvidia", "opereta-base", "systemd", "grub-efi-amd64-bin", "grub-efi-amd64-signed", "shim-signed", "nvidia-kernel-open-dkms"}

// configure 는 복사가 끝난 대상 시스템을 설정하고 부트로더를 설치한다.
func (in *Installer) configure(ctx context.Context, o *Options, espDev, rootDev string) error {
	target := in.target()
	in.event("configure", "writing fstab, host name, locale, time zone and keyboard")

	rootUUID, err := in.uuid(ctx, rootDev)
	if err != nil {
		return err
	}
	espUUID, err := in.uuid(ctx, espDev)
	if err != nil {
		return err
	}
	fstab := "# /etc/fstab: written by opereta-install. Use 'blkid' to see the UUIDs.\n" +
		fmt.Sprintf("UUID=%s / ext4 defaults,noatime,errors=remount-ro 0 1\n", rootUUID) +
		fmt.Sprintf("UUID=%s /boot/efi vfat umask=0077 0 2\n", espUUID)
	if err := writeFile(target, "etc/fstab", fstab, 0o644); err != nil {
		return err
	}

	hosts := "127.0.0.1\tlocalhost\n127.0.1.1\t" + o.Hostname + "\n" +
		"::1\tlocalhost ip6-localhost ip6-loopback\nff02::1\tip6-allnodes\nff02::2\tip6-allrouters\n"
	for path, body := range map[string]string{
		"etc/hostname":         o.Hostname + "\n",
		"etc/hosts":            hosts,
		"etc/default/locale":   "LANG=" + o.Locale + "\n",
		"etc/timezone":         o.Timezone + "\n",
		"etc/default/keyboard": fmt.Sprintf("XKBMODEL=\"pc105\"\nXKBLAYOUT=%q\nXKBVARIANT=\"\"\nXKBOPTIONS=\"\"\nBACKSPACE=\"guess\"\n", o.Keyboard),
	} {
		if err := writeFile(target, path, body, 0o644); err != nil {
			return err
		}
	}
	localtime := filepath.Join(target, "etc/localtime")
	_ = os.Remove(localtime)
	if err := os.Symlink("/usr/share/zoneinfo/"+o.Timezone, localtime); err != nil {
		return errf(CodeConfigure, "cannot set the time zone: %v", err)
	}
	// 이미지에 박힌 machine-id 를 버리고 이 설치만의 새 ID 를 만든다. 빈 파일로 두면 첫 부팅 때 systemd-firstboot 가 콘솔에서 질문할 수 있다.
	if err := writeFile(target, "etc/machine-id", "", 0o644); err != nil {
		return err
	}
	if _, err := in.cmd(ctx, CodeConfigure, time.Minute, nil, "systemd-machine-id-setup", "--root="+target); err != nil {
		return err
	}

	if err := in.bindMounts(ctx); err != nil {
		return err
	}

	locales := map[string]bool{"en_US.UTF-8": true, o.Locale: true}
	var lines []string
	for _, l := range sortedLocales(locales) {
		lines = append(lines, l+" UTF-8")
	}
	if err := writeFile(target, "etc/locale.gen", strings.Join(lines, "\n")+"\n", 0o644); err != nil {
		return err
	}
	if _, err := in.chroot(ctx, CodeConfigure, 10*time.Minute, nil, "locale-gen"); err != nil {
		return err
	}

	in.event("account", "creating the account %s", o.User)
	args := []string{"add", o.User}
	if o.Admin {
		args = append(args, "--admin")
	}
	out, err := in.chroot(ctx, CodeAccount, 2*time.Minute, []byte(o.Password+"\n"), "/usr/lib/opereta/opereta-user", args...)
	if err != nil || !strings.Contains(out, "OPERETA_USER_RESULT=ok") {
		if err == nil {
			err = errf(CodeAccount, "the account helper did not report success")
		}
		return err
	}

	in.event("cleanup", "removing live-session packages and the installer")
	if err := in.PrepareTarget(ctx, target, PrepareOptions{Mode: o.Mode}); err != nil {
		return err
	}

	if err := in.InstallBootloader(ctx, target, o.Console); err != nil {
		return err
	}
	in.event("done", "installed; remove the installation medium and reboot")
	return nil
}

func sortedLocales(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	// 간단한 삽입 정렬이면 충분하다.
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j] < out[j-1]; j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

func (in *Installer) uuid(ctx context.Context, dev string) (string, error) {
	out, err := in.cmd(ctx, CodeConfigure, time.Minute, nil, "blkid", "-s", "UUID", "-o", "value", dev)
	if err != nil {
		return "", err
	}
	id := strings.TrimSpace(out)
	if id == "" {
		return "", errf(CodeConfigure, "no UUID was found on %s", dev)
	}
	return id, nil
}

// bindMounts 는 chroot 에 필요한 가상 파일 시스템을 대상에 연결한다.
// /run 은 호스트의 것을 쓰지 않고 새 tmpfs 로 둔다. 그래야 대상의 패키지 스크립트가 라이브 세션의 systemd 를 건드리지 않는다.
func (in *Installer) bindMounts(ctx context.Context) error {
	target := in.target()
	type mnt struct {
		dst  string
		args []string
	}
	list := []mnt{
		{"dev", []string{"--rbind", "/dev"}},
		{"proc", []string{"-t", "proc", "proc"}},
		{"sys", []string{"--rbind", "/sys"}},
		{"run", []string{"-t", "tmpfs", "tmpfs"}},
	}
	for _, m := range list {
		dst := filepath.Join(target, m.dst)
		if err := os.MkdirAll(dst, 0o755); err != nil {
			return errf(CodeMount, "%v", err)
		}
		if _, err := in.cmd(ctx, CodeMount, time.Minute, nil, "mount", append(m.args, dst)...); err != nil {
			return err
		}
		in.mounted = append(in.mounted, dst)
		if m.dst == "dev" || m.dst == "sys" {
			_, _ = in.cmd(ctx, CodeMount, time.Minute, nil, "mount", "--make-rslave", dst)
		}
	}
	// update-initramfs 와 udev 규칙을 쓰는 도구가 호스트의 udev 데이터를 읽을 수 있게 한다.
	udev := filepath.Join(target, "run", "udev")
	if err := os.MkdirAll(udev, 0o755); err == nil {
		if _, err := in.cmd(ctx, CodeMount, time.Minute, nil, "mount", "--bind", "/run/udev", udev); err == nil {
			in.mounted = append(in.mounted, udev)
		}
	}
	return nil
}

func writeFile(root, rel, body string, mode os.FileMode) error {
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return errf(CodeConfigure, "%v", err)
	}
	if err := os.WriteFile(path, []byte(body), mode); err != nil {
		return errf(CodeConfigure, "write %s: %v", rel, err)
	}
	return nil
}

// PrepareOptions 는 대상 마무리 옵션이다.
type PrepareOptions struct {
	// Mode 는 기본 모드(server 또는 desktop)다. 비어 있으면 바꾸지 않는다.
	Mode string
	// RegisterUser 는 Opereta 계정 목록(users.list)에 올릴 이미 만들어진 계정 이름이다. Calamares 가 만든 계정에 쓴다.
	RegisterUser string
	Admin        bool
	// KeepInstaller 가 참이면 설치기 패키지를 지우지 않는다.
	KeepInstaller bool
}

// exec 는 root 가 "/" 이면 직접, 아니면 chroot 로 명령을 실행한다.
func (in *Installer) exec(ctx context.Context, root, code string, timeout time.Duration, name string, args ...string) (string, error) {
	if root == "" || root == "/" {
		return in.cmd(ctx, code, timeout, nil, name, args...)
	}
	return in.cmd(ctx, code, timeout, nil, "chroot", append([]string{root, name}, args...)...)
}

// PrepareTarget 은 대상 시스템에서 라이브 전용 패키지와 설치기를 지우고 기본 모드와 계정 기록을 맞춘다.
// root 는 대상 루트 경로이며, Calamares 의 chroot 안에서 부를 때는 "/" 다.
func (in *Installer) PrepareTarget(ctx context.Context, root string, o PrepareOptions) error {
	list, err := in.exec(ctx, root, CodeCleanup, time.Minute, "dpkg-query", "-W", "-f", "${Package}\n", "live-boot*", "live-config*", "live-tools")
	if err != nil && strings.TrimSpace(list) == "" {
		// 패키지 패턴에 맞는 것이 없으면 dpkg-query 가 1 로 끝난다. 그때는 지울 것이 없다.
		list = ""
	}
	var live []string
	for _, p := range strings.Fields(list) {
		live = append(live, p)
	}
	if len(live) > 0 {
		in.event("cleanup", "purging %s", strings.Join(live, " "))
		if _, err := in.exec(ctx, root, CodeCleanup, 20*time.Minute, "apt-get", append([]string{"-y", "-q", "purge"}, live...)...); err != nil {
			return err
		}
	}
	if !o.KeepInstaller {
		var inst []string
		for _, p := range []string{"calamares", "opereta-installer"} {
			if _, err := in.exec(ctx, root, CodeCleanup, time.Minute, "dpkg-query", "-W", "-f", "${Status}", p); err == nil {
				inst = append(inst, p)
			}
		}
		if len(inst) > 0 {
			in.event("cleanup", "purging the installer %s", strings.Join(inst, " "))
			if _, err := in.exec(ctx, root, CodeCleanup, 30*time.Minute, "apt-get", append([]string{"-y", "-q", "purge", "--autoremove"}, inst...)...); err != nil {
				return err
			}
		}
	}
	for _, p := range protectedPackages {
		out, _ := in.exec(ctx, root, CodeDamagedSystem, time.Minute, "dpkg-query", "-W", "-f", "${db:Status-Status}", p)
		if strings.TrimSpace(out) != "installed" {
			return errf(CodeDamagedSystem, "the package %s is missing after the cleanup; the installation was stopped", p)
		}
	}
	// 루트 계정은 비밀번호 로그인을 막는다. 관리는 sudo 로 한다.
	_, _ = in.exec(ctx, root, CodeCleanup, time.Minute, "usermod", "--lock", "root")

	if o.Mode != "" {
		unit := "multi-user.target"
		if o.Mode == "desktop" {
			unit = "graphical.target"
		}
		rootArg := root
		if rootArg == "" {
			rootArg = "/"
		}
		if _, err := in.cmd(ctx, CodeConfigure, time.Minute, nil, "systemctl", "--root="+rootArg, "set-default", unit); err != nil {
			return err
		}
	}
	if o.RegisterUser != "" {
		admin := "0"
		if o.Admin {
			admin = "1"
		}
		line := fmt.Sprintf("%s %s %s\n", o.RegisterUser, admin, in.now().UTC().Format(time.RFC3339))
		dir := filepath.Join(rootOr(root), "var/lib/opereta")
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return errf(CodeConfigure, "%v", err)
		}
		f, err := os.OpenFile(filepath.Join(dir, "users.list"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o640)
		if err != nil {
			return errf(CodeConfigure, "%v", err)
		}
		_, werr := f.WriteString(line)
		_ = f.Close()
		if werr != nil {
			return errf(CodeConfigure, "%v", werr)
		}
	}
	return nil
}

func rootOr(root string) string {
	if root == "" {
		return "/"
	}
	return root
}

// BootDefault 는 grub.cfg 가 만들어진 뒤 가장 새로운 커널을 저장된 기본 부팅 항목으로 고정한다.
// 이후 opereta-update 는 이 값을 기준으로 새 커널을 한 번만 부팅해 본다.
func (in *Installer) BootDefault(ctx context.Context, root string) error {
	cfg := filepath.Join(rootOr(root), "boot/grub/grub.cfg")
	releases, err := update.GrubKernelReleases(cfg)
	if err != nil || len(releases) == 0 {
		return errf(CodeBootloader, "no kernel entry was found in %s", cfg)
	}
	id, err := update.GrubEntryID(cfg, releases[len(releases)-1])
	if err != nil {
		return errf(CodeBootloader, "%v", err)
	}
	if _, err := in.exec(ctx, root, CodeBootloader, time.Minute, "grub-set-default", id); err != nil {
		return err
	}
	in.event("bootloader", "the saved default boot entry is %s", id)
	return nil
}

// InstallBootloader 는 initramfs 를 다시 만들고 GRUB 을 설치한다. 대상 루트 root 에서 쓴다(Calamares 의 chroot 안에서는 "/").
// console 이 serial 이면 커널과 GRUB 콘솔을 ttyS0 에도 둔다.
func (in *Installer) InstallBootloader(ctx context.Context, root, console string) error {
	if console == "serial" {
		cfg := "# Written by opereta-install (--console serial).\n" +
			"GRUB_CMDLINE_LINUX=\"console=tty0 console=ttyS0,115200\"\n" +
			"GRUB_TERMINAL=\"console serial\"\n" +
			"GRUB_SERIAL_COMMAND=\"serial --speed=115200\"\n"
		if err := writeFile(rootOr(root), "etc/default/grub.d/30-opereta-console.cfg", cfg, 0o644); err != nil {
			return err
		}
	}

	in.event("initramfs", "rebuilding the initramfs for all kernels")
	if _, err := in.exec(ctx, root, CodeBootloader, 20*time.Minute, "update-initramfs", "-u", "-k", "all"); err != nil {
		return err
	}

	// UEFI 로 부팅하지 않았으면 NVRAM 항목을 만들 수 없고 ESP 도 없다. 부팅할 수 없는 시스템을 만들지 않는다.
	if fi, err := os.Stat(in.efiDir()); err != nil || !fi.IsDir() {
		return errf(CodeNotUEFI, "this computer was not started in UEFI mode, so a bootable system cannot be installed. Restart the installer in UEFI mode (turn off CSM or legacy boot in the firmware settings)")
	}

	// GRUB 이 Windows 같은 다른 시스템을 메뉴에 올리게 한다(os-prober 는 이미지에 들어 있다).
	if err := writeFile(rootOr(root), "etc/default/grub.d/40-opereta-os-prober.cfg",
		"# Written by opereta-install: list other operating systems (for example Windows) in the GRUB menu.\nGRUB_DISABLE_OS_PROBER=false\n", 0o644); err != nil {
		return err
	}

	in.event("bootloader", "installing GRUB for UEFI with Secure Boot support")
	grub := []string{"--target=x86_64-efi", "--efi-directory=/boot/efi", "--bootloader-id=opereta", "--uefi-secure-boot"}
	if _, err := in.exec(ctx, root, CodeBootloader, 5*time.Minute, "grub-install", grub...); err != nil {
		// 펌웨어가 NVRAM 쓰기를 거부하면(efibootmgr 실패) 항목 등록 없이 파일만 설치한다. 아래에서 항목을 직접 만들어 본다.
		in.event("bootloader", "registering the boot entry failed (%v); installing the files without it", err)
		if _, err := in.exec(ctx, root, CodeBootloader, 5*time.Minute, "grub-install", append(grub, "--no-nvram")...); err != nil {
			return err
		}
	}
	// 펌웨어가 부팅 항목을 잃어도 부팅되도록 대체 경로(EFI/BOOT/BOOTX64.EFI)에도 설치한다. 다른 시스템(예: Windows)이
	// 이미 그 경로를 쓰고 있으면 덮어쓰지 않는다.
	if in.fallbackIsForeign(root) {
		in.event("bootloader", "EFI/BOOT/BOOTX64.EFI belongs to another system; leaving it as it is")
	} else if _, err := in.exec(ctx, root, CodeBootloader, 5*time.Minute, "grub-install", "--target=x86_64-efi", "--efi-directory=/boot/efi", "--removable", "--uefi-secure-boot"); err != nil {
		return err
	}
	if _, err := in.exec(ctx, root, CodeBootloader, 5*time.Minute, "update-grub"); err != nil {
		return err
	}
	in.ensureBootEntry(ctx, root)
	in.noteLegacyBootFolders(root)
	if err := in.localRTCForWindows(root); err != nil {
		return err
	}
	return in.BootDefault(ctx, root)
}

// fallbackIsForeign 은 EFI/BOOT/BOOTX64.EFI 가 이미 있고 이 시스템이 만든 것이 아닌지 본다. 우리가 --removable 로 설치하면
// 같은 폴더에 grubx64.efi 가 함께 놓이므로, 그것이 없는 BOOTX64.EFI 는 다른 시스템(Windows 의 복사본 등)의 것으로 본다.
func (in *Installer) fallbackIsForeign(root string) bool {
	dir := filepath.Join(rootOr(root), "boot/efi/EFI/BOOT")
	if fi, err := os.Stat(filepath.Join(dir, "BOOTX64.EFI")); err != nil || !fi.Mode().IsRegular() {
		return false
	}
	_, err := os.Stat(filepath.Join(dir, "grubx64.efi"))
	return err != nil
}

// ensureBootEntry 는 NVRAM 에 opereta 항목이 있는지 확인하고 없으면 직접 만든다. 만들지 못해도 설치는 계속한다
// (대체 경로나 일회성 부팅 메뉴로 부팅할 수 있다). 결과는 이벤트로 남기고 첫 부팅 때 제어 센터가 다시 점검한다.
func (in *Installer) ensureBootEntry(ctx context.Context, root string) {
	has := func() (bool, bool) {
		out, err := in.exec(ctx, root, CodeBootloader, time.Minute, "efibootmgr", "-v")
		if err != nil {
			return false, false
		}
		return boot.Parse(out).OperetaEntry != "", true
	}
	if ok, readable := has(); ok {
		in.event("bootloader", "the opereta boot entry is registered in the firmware")
		return
	} else if !readable {
		in.event("bootloader", "warning: the firmware boot entries could not be read; check them after the first start with 'opereta boot status'")
		return
	}
	src, err := in.exec(ctx, root, CodeBootloader, time.Minute, "findmnt", "-no", "SOURCE", "/boot/efi")
	if err != nil {
		in.event("bootloader", "warning: no opereta boot entry exists and the EFI partition could not be located; repair it later with 'opereta boot repair'")
		return
	}
	disk, part, ok := boot.SplitPartition(strings.TrimSpace(src))
	if !ok {
		in.event("bootloader", "warning: no opereta boot entry exists; repair it later with 'opereta boot repair'")
		return
	}
	loader := `\EFI\opereta\shimx64.efi`
	if _, err := in.exec(ctx, root, CodeBootloader, time.Minute, "efibootmgr", "--create", "--disk", disk, "--part", part, "--loader", loader, "--label", "opereta"); err != nil {
		in.event("bootloader", "warning: the firmware refused the opereta boot entry (%v). Start Opereta from the firmware boot menu once, then run 'opereta boot status'", err)
		return
	}
	if ok, _ := has(); ok {
		in.event("bootloader", "the opereta boot entry was created")
	} else {
		in.event("bootloader", "warning: the opereta boot entry was written but does not show up in the firmware list; use the firmware boot menu (for example F11) to start Opereta")
	}
}

// noteLegacyBootFolders 는 ESP 에 이전 이름의 폴더가 남아 있으면 알린다. 지우는 일은 사용자 확인 뒤 opereta boot cleanup 이 한다.
func (in *Installer) noteLegacyBootFolders(root string) {
	dir := filepath.Join(rootOr(root), "boot/efi/EFI")
	ents, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range ents {
		if e.IsDir() && boot.IsLegacyName(e.Name()) {
			in.event("bootloader", "the EFI partition has the folder %s from an earlier name of this system; remove it with 'opereta boot cleanup' once you have confirmed it is not needed", e.Name())
		}
	}
}

// localRTCForWindows 는 같은 컴퓨터에 Windows 가 있으면 하드웨어 시계를 현지 시간으로 쓰게 한다. Windows 는 RTC 를
// 현지 시간으로 읽고 Linux 는 기본이 UTC 라서, 그대로 두면 두 시스템을 오갈 때 시각이 시간대 차이(한국은 9시간)만큼 틀어진다.
func (in *Installer) localRTCForWindows(root string) error {
	if fi, err := os.Stat(filepath.Join(rootOr(root), "boot/efi/EFI/Microsoft")); err != nil || !fi.IsDir() {
		return nil
	}
	in.event("bootloader", "Windows was found on this computer; the hardware clock is set to local time so both systems agree")
	return writeFile(rootOr(root), "etc/adjtime", "0.0 0 0.0\n0\nLOCAL\n", 0o644)
}
