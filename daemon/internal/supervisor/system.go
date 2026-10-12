package supervisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/nxodh/lightlixos/daemon/internal/firewall"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

// 부팅 모드 값과 데스크톱에 필요한 디스플레이 매니저 유닛이다.
const (
	modeServer         = "server"
	modeDesktop        = "desktop"
	targetServer       = "multi-user.target"
	targetDesktop      = "graphical.target"
	displayManagerUnit = "lightdm.service"
)

// Mode는 기본 타깃으로 모드를 돌려준다. graphical.target 이면 desktop, 그 밖은 server 다.
func (s *Supervisor) Mode(ctx context.Context) (string, error) {
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{"get-default"},
		Timeout:   shortTimeout,
		MaxOutput: 1 << 10,
	})
	if err != nil {
		return "", fmt.Errorf("systemctl get-default: %w", err)
	}
	if res.ExitCode != 0 {
		return "", fmt.Errorf("systemctl get-default exited %d: %s", res.ExitCode, excerpt(string(res.Stderr), 256))
	}
	if strings.TrimSpace(string(res.Stdout)) == targetDesktop {
		return modeDesktop, nil
	}
	return modeServer, nil
}

// SetMode는 모드를 바꾼다. 기본 타깃을 바꾼 뒤 isolate 로 바로 전환한다.
// desktop 은 lightdm.service 가 설치되어 있어야 한다. 진행 중인 llama 작업이 있으면 busy 오류다.
func (s *Supervisor) SetMode(ctx context.Context, mode string) error {
	var target string
	switch mode {
	case modeServer:
		target = targetServer
	case modeDesktop:
		target = targetDesktop
	default:
		return errorf(CodeInvalidMode, "mode must be %q or %q, got %q", modeServer, modeDesktop, mode)
	}

	select {
	case s.sem <- struct{}{}:
	default:
		return errorf(CodeBusy, "a server operation is in progress; change the mode later")
	}
	defer func() { <-s.sem }()

	// 모드 전환은 클라이언트 연결이 끊겨도 끝까지 간다.
	ctx = context.WithoutCancel(ctx)
	if mode == modeDesktop {
		installed, err := s.unitInstalled(ctx, displayManagerUnit)
		if err != nil {
			return errorf(CodeModeChangeFailed, "check %s: %v", displayManagerUnit, err)
		}
		if !installed {
			return errorf(CodeDesktopNotInstalled, "%s is not installed; install the lightlix-desktop package", displayManagerUnit)
		}
	}
	steps := [][]string{
		{"set-default", target},
		{"isolate", target},
	}
	for _, args := range steps {
		res, err := s.exec(ctx, platform.Command{
			Name:      "systemctl",
			Args:      args,
			Timeout:   modeChangeTimeout,
			MaxOutput: maxCommandOutput,
		})
		if err != nil {
			return errorf(CodeModeChangeFailed, "systemctl %s: %v", strings.Join(args, " "), err)
		}
		if res.ExitCode != 0 {
			return errorf(CodeModeChangeFailed, "systemctl %s exited %d: %s", strings.Join(args, " "), res.ExitCode, excerpt(string(res.Stderr), 512))
		}
	}
	return nil
}

// unitInstalled는 유닛 파일이 설치되어 있는지 본다. systemctl list-unit-files 의 첫 열로 확인한다.
func (s *Supervisor) unitInstalled(ctx context.Context, unit string) (bool, error) {
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{"list-unit-files", unit, "--legend=no", "--no-pager"},
		Timeout:   shortTimeout,
		MaxOutput: 4 << 10,
	})
	if err != nil {
		return false, err
	}
	if res.ExitCode != 0 {
		return false, fmt.Errorf("exit %d: %s", res.ExitCode, excerpt(string(res.Stderr), 256))
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] == unit {
			return true, nil
		}
	}
	return false, nil
}

// ApplyFirewall은 현재 설정으로 nftables 규칙을 만들어 파일에 쓰고 nft -f 로 적용한다.
// API 포트는 설정의 port 다. LAN 공개를 켰을 때만 그 포트가 열린다. SSH 포트는 설정의 ssh_enabled 일 때만 열린다.
func (s *Supervisor) ApplyFirewall(ctx context.Context) error {
	s.firewallMu.Lock()
	defer s.firewallMu.Unlock()

	cfg := s.opts.Config()
	rules, err := firewall.Ruleset(firewall.Options{APIPort: cfg.Port, APIOpen: cfg.LANExposed, SSHOpen: cfg.SSHEnabled})
	if err != nil {
		return errorf(CodeFirewallFailed, "build rules: %v", err)
	}
	if err := writeFileAtomic(s.opts.FirewallPath, []byte(rules), 0o644); err != nil {
		return errorf(CodeFirewallFailed, "write %s: %v", s.opts.FirewallPath, err)
	}
	res, err := s.exec(ctx, platform.Command{
		Name:      "nft",
		Args:      []string{"-f", s.opts.FirewallPath},
		Timeout:   firewallTimeout,
		MaxOutput: maxCommandOutput,
	})
	if err != nil {
		return errorf(CodeFirewallFailed, "nft: %v", err)
	}
	if res.ExitCode != 0 {
		return errorf(CodeFirewallFailed, "nft -f exited %d: %s", res.ExitCode, excerpt(string(res.Stderr), 512))
	}
	return nil
}
