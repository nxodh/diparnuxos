package supervisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/nxodh/lightlixos/daemon/internal/platform"
)

// sshUnit은 Debian의 OpenSSH 서버 유닛 이름이다.
const sshUnit = "ssh.service"

// SSHStatus는 SSH 서버 유닛의 상태다.
type SSHStatus struct {
	// Installed는 유닛 파일이 있다는 뜻이다. 없으면 켤 수 없다.
	Installed bool `json:"installed"`
	// Active는 유닛이 지금 실행 중이라는 뜻이다.
	Active bool `json:"active"`
}

// SSHStatus는 SSH 유닛이 설치되어 있는지와 실행 중인지 본다.
func (s *Supervisor) SSHStatus(ctx context.Context) (SSHStatus, error) {
	installed, err := s.unitInstalled(ctx, sshUnit)
	if err != nil {
		return SSHStatus{}, fmt.Errorf("check %s: %w", sshUnit, err)
	}
	if !installed {
		return SSHStatus{}, nil
	}
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{"is-active", sshUnit},
		Timeout:   shortTimeout,
		MaxOutput: 1 << 10,
	})
	if err != nil {
		return SSHStatus{}, fmt.Errorf("systemctl is-active %s: %w", sshUnit, err)
	}
	// is-active는 active일 때만 종료 코드 0이고 inactive, failed 등은 0이 아니다. 출력도 함께 본다.
	active := res.ExitCode == 0 && strings.TrimSpace(string(res.Stdout)) == "active"
	return SSHStatus{Installed: true, Active: active}, nil
}

// SetSSHUnit은 SSH 유닛을 켜고 끈다. 켜면 부팅 때도 시작되도록 enable 하고, 끄면 disable 한다.
// 유닛이 설치되어 있지 않으면 켤 수 없다(끄는 것은 항상 시도한다).
// 방화벽은 호출한 쪽이 ApplyFirewall 로 따로 맞춘다.
func (s *Supervisor) SetSSHUnit(ctx context.Context, enable bool) error {
	// 클라이언트 연결이 끊겨도 상태가 반쯤 바뀐 채 남지 않게 끝까지 간다.
	ctx = context.WithoutCancel(ctx)
	if enable {
		installed, err := s.unitInstalled(ctx, sshUnit)
		if err != nil {
			return errorf(CodeSSHChangeFailed, "check %s: %v", sshUnit, err)
		}
		if !installed {
			return errorf(CodeSSHNotInstalled, "%s is not installed", sshUnit)
		}
	}
	verb := "disable"
	if enable {
		verb = "enable"
	}
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{verb, "--now", sshUnit},
		Timeout:   modeChangeTimeout,
		MaxOutput: maxCommandOutput,
	})
	if err != nil {
		return errorf(CodeSSHChangeFailed, "systemctl %s --now %s: %v", verb, sshUnit, err)
	}
	if res.ExitCode != 0 {
		return errorf(CodeSSHChangeFailed, "systemctl %s --now %s exited %d: %s", verb, sshUnit, res.ExitCode, excerpt(string(res.Stderr), 512))
	}
	return nil
}
