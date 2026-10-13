package supervisor

import (
	"context"
	"fmt"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/platform"
)

// sshUnit은 Debian의 OpenSSH 서버 유닛 이름이다.
const sshUnit = "ssh.service"

// SSHStatus는 SSH 서버 유닛의 상태다.
type SSHStatus struct {
	// Installed는 유닛 파일이 있다는 뜻이다. 없으면 켤 수 없다.
	Installed bool `json:"installed"`
	// Active는 유닛이 지금 실행 중이라는 뜻이다.
	Active bool `json:"active"`
	// PasswordAuth는 sshd 유효 설정의 비밀번호 로그인 값이다. "yes", "no", "unknown" 중 하나다.
	// Debian live 이미지는 live-config가 이 값을 끄므로 키 없이는 접속할 수 없을 수 있다.
	PasswordAuth string `json:"password_auth"`
}

// SSHStatus는 SSH 유닛이 설치되어 있는지와 실행 중인지 본다.
func (s *Supervisor) SSHStatus(ctx context.Context) (SSHStatus, error) {
	installed, err := s.unitInstalled(ctx, sshUnit)
	if err != nil {
		return SSHStatus{}, fmt.Errorf("check %s: %w", sshUnit, err)
	}
	if !installed {
		return SSHStatus{PasswordAuth: "unknown"}, nil
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
	return SSHStatus{Installed: true, Active: active, PasswordAuth: s.sshPasswordAuth(ctx)}, nil
}

// sshPasswordAuth는 sshd -T 의 유효 설정에서 비밀번호 로그인 값을 읽는다. 읽지 못하면 "unknown"이다.
// sshd -T 는 호스트 키가 없으면 실패하므로 아직 키가 없는 이미지에서는 unknown 이 나온다.
func (s *Supervisor) sshPasswordAuth(ctx context.Context) string {
	res, err := s.exec(ctx, platform.Command{
		Name:      "sshd",
		Args:      []string{"-T"},
		Timeout:   shortTimeout,
		MaxOutput: 64 << 10,
	})
	if err != nil || res.ExitCode != 0 {
		return "unknown"
	}
	for _, line := range strings.Split(string(res.Stdout), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[0] == "passwordauthentication" {
			if fields[1] == "yes" || fields[1] == "no" {
				return fields[1]
			}
		}
	}
	return "unknown"
}

// SetSSHUnit은 SSH 유닛을 시작하거나 멈춘다.
//
// enable 과 disable 은 쓰지 않는다. diparnuxd 는 ProtectSystem 아래에서 돌아 /etc 에 링크를 만들 수 없고,
// Debian 의 ssh.service 는 SysV 스크립트 동기화(update-rc.d)까지 하려 해서 실패한다. 부팅 때의 시작은 데몬이 설정의
// ssh_enabled 를 보고 다시 시작해 주므로 enable 이 필요 없다. 호스트 키 생성과 정책 파일은 헬퍼(PrepareSSH)가 맡는다.
// 켤 때 유닛이 설치되어 있지 않으면 CodeSSHNotInstalled 다. 끄는 것은 항상 시도한다.
// 방화벽은 호출한 쪽이 ApplyFirewall 로 따로 맞춘다.
func (s *Supervisor) SetSSHUnit(ctx context.Context, enable bool) error {
	// 클라이언트 연결이 끊겨도 상태가 반쯤 바뀐 채 남지 않게 끝까지 간다.
	ctx = context.WithoutCancel(ctx)
	verb := "stop"
	if enable {
		installed, err := s.unitInstalled(ctx, sshUnit)
		if err != nil {
			return errorf(CodeSSHChangeFailed, "check %s: %v", sshUnit, err)
		}
		if !installed {
			return errorf(CodeSSHNotInstalled, "%s is not installed", sshUnit)
		}
		verb = "start"
	}
	res, err := s.exec(ctx, platform.Command{
		Name:      "systemctl",
		Args:      []string{verb, sshUnit},
		Timeout:   modeChangeTimeout,
		MaxOutput: maxCommandOutput,
	})
	if err != nil {
		return errorf(CodeSSHChangeFailed, "systemctl %s %s: %v", verb, sshUnit, err)
	}
	if res.ExitCode != 0 {
		return errorf(CodeSSHChangeFailed, "systemctl %s %s exited %d: %s", verb, sshUnit, res.ExitCode, excerpt(string(res.Stderr), 512))
	}
	return nil
}
