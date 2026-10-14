package dist

import (
	"os"
	"strings"
	"testing"

	"github.com/nxodh/operetaos/daemon/internal/firewall"
)

// readFile 은 이 디렉터리의 파일을 읽는다. 테스트는 패키지 디렉터리에서 실행되므로 상대 경로를 쓴다.
func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(b)
}

// activeLines 는 주석과 빈 줄을 뺀 설정 줄을 돌려준다. systemd 는 "#" 으로 시작하는 줄만 주석으로 본다.
func activeLines(text string) []string {
	var out []string
	for _, line := range strings.Split(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		out = append(out, trimmed)
	}
	return out
}

// hasLine 은 주석이 아닌 줄 중에 line 과 정확히 같은 줄이 있는지 본다.
func hasLine(text, line string) bool {
	for _, l := range activeLines(text) {
		if l == line {
			return true
		}
	}
	return false
}

// TestDefaultRulesetMatchesFirewallPackage 는 firewall-default.nft 가 Ruleset(APIPort 8080, 모두 닫힘)의 출력과
// 바이트 단위로 같은지 확인한다. 규칙을 바꾸면 이 파일을 함께 바꿔야 한다.
func TestDefaultRulesetMatchesFirewallPackage(t *testing.T) {
	want, err := firewall.Ruleset(firewall.Options{APIPort: 8080})
	if err != nil {
		t.Fatalf("Ruleset: %v", err)
	}
	got := readFile(t, "firewall-default.nft")
	if got != want {
		t.Fatalf("firewall-default.nft differs from Ruleset output.\n--- file ---\n%s\n--- Ruleset ---\n%s", got, want)
	}
}

// TestDaemonUnitHasSocketAndTarget 는 데몬 유닛이 소켓을 요구하고, 부팅 때 자동으로 뜨는 대상에 묶였는지 확인한다.
func TestDaemonUnitHasSocketAndTarget(t *testing.T) {
	svc := readFile(t, "operetad.service")
	for _, line := range []string{
		"Requires=operetad.socket opereta-firewall-default.service",
		"ExecStart=/usr/sbin/operetad",
		"Type=simple",
		"Restart=on-failure",
		"WantedBy=multi-user.target",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ProtectSystem=full",
		"NoNewPrivileges=yes",
	} {
		if !hasLine(svc, line) {
			t.Errorf("operetad.service lacks %q", line)
		}
	}
	if !hasLine(svc, "After=operetad.socket opereta-gpu-check.service opereta-firewall-default.service opereta-firewall.service network.target") {
		t.Errorf("operetad.service lacks the After= line with the socket, the GPU check, and network.target")
	}
	if hasLine(svc, "PrivateDevices=yes") {
		t.Error("operetad.service sets PrivateDevices=yes, which hides /dev/nvidia* from NVML")
	}
}

// TestSocketUnit 은 소켓의 경로, 모드, 그룹, 설치 대상을 확인한다.
func TestSocketUnit(t *testing.T) {
	sock := readFile(t, "operetad.socket")
	for _, line := range []string{
		"ListenStream=/run/opereta/operetad.sock",
		"SocketMode=0660",
		"SocketGroup=opereta",
		"WantedBy=sockets.target",
	} {
		if !hasLine(sock, line) {
			t.Errorf("operetad.socket lacks %q", line)
		}
	}
}

// TestLlamaUnitKeepsGPUAccess 는 llama 유닛이 GPU 접근을 막는 옵션을 쓰지 않고, 자동 재시작과 [Install] 이 없는지 확인한다.
func TestLlamaUnitKeepsGPUAccess(t *testing.T) {
	llama := readFile(t, "opereta-llama.service")
	for _, line := range []string{
		"User=opereta-llm",
		"Group=opereta-llm",
		"SupplementaryGroups=video render",
		"ExecStartPre=/usr/lib/opereta/check-gpu-permissions",
		"EnvironmentFile=/run/opereta/llama-server.env",
		"ExecStart=/usr/lib/opereta/llama/llama-server",
		"Restart=no",
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ReadOnlyPaths=/var/lib/opereta/models",
		"LimitMEMLOCK=infinity",
		"RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX",
	} {
		if !hasLine(llama, line) {
			t.Errorf("opereta-llama.service lacks %q", line)
		}
	}
	for _, bad := range []string{"PrivateDevices=yes", "MemoryDenyWriteExecute=yes"} {
		if hasLine(llama, bad) {
			t.Errorf("opereta-llama.service sets %q, which breaks CUDA device access", bad)
		}
	}
	for _, l := range activeLines(llama) {
		if strings.HasPrefix(l, "[Install]") || strings.HasPrefix(l, "WantedBy=") {
			t.Errorf("opereta-llama.service has an install section line %q; the daemon starts it", l)
		}
	}
}

// TestLlamaUnitWaitsForExecBeforeStartReturns는 데몬의 즉시 MainPID 검증에 필요한 실행 장벽을 고정한다.
func TestLlamaUnitWaitsForExecBeforeStartReturns(t *testing.T) {
	llama := readFile(t, "opereta-llama.service")
	if !hasLine(llama, "Type=exec") {
		t.Fatal("opereta-llama.service must wait until ExecStart has executed before systemctl start succeeds")
	}
	if hasLine(llama, "Type=simple") {
		t.Fatal("Type=simple can report success before ExecStart calls execve")
	}
}

// TestFirewallDefaultsBeforeSaved 는 저장 파일이 깨져도 기본 차단을 먼저 적용하는 의존성을 확인한다.
func TestFirewallDefaultsBeforeSaved(t *testing.T) {
	saved := readFile(t, "opereta-firewall.service")
	def := readFile(t, "opereta-firewall-default.service")
	if !hasLine(saved, "ConditionPathExists=/var/lib/opereta/firewall.nft") {
		t.Error("saved-rules unit lacks its path condition")
	}
	for _, line := range activeLines(def) {
		if strings.HasPrefix(line, "Condition") {
			t.Errorf("default firewall must always apply, found %q", line)
		}
	}
	for _, line := range []string{"Requires=opereta-firewall-default.service", "After=local-fs.target opereta-firewall-default.service"} {
		if !hasLine(saved, line) {
			t.Errorf("saved firewall lacks %q", line)
		}
	}
	if !hasLine(def, "After=local-fs.target nftables.service") {
		t.Error("default firewall must follow any nftables.service ruleset flush")
	}
	if !hasLine(saved, "ExecStart=/usr/sbin/nft -f /var/lib/opereta/firewall.nft") {
		t.Error("saved-rules unit applies the wrong file")
	}
	if !hasLine(def, "ExecStart=/usr/sbin/nft -f /usr/share/opereta/firewall-default.nft") {
		t.Error("default-rules unit applies the wrong file")
	}
	for name, text := range map[string]string{"saved": saved, "default": def} {
		if !hasLine(text, "Before=network-pre.target") || !hasLine(text, "Wants=network-pre.target") {
			t.Errorf("%s firewall unit is not ordered before the network", name)
		}
		if !hasLine(text, "RemainAfterExit=yes") || !hasLine(text, "Type=oneshot") {
			t.Errorf("%s firewall unit is not a oneshot with RemainAfterExit", name)
		}
	}
}

// TestTmpfilesDoesNotRedefineRun 은 /run/opereta 를 이 설정에서 다시 정의하지 않는지 확인한다.
// 그 경로는 opereta-nvidia 패키지가 만든다.
func TestTmpfilesDoesNotRedefineRun(t *testing.T) {
	tf := readFile(t, "opereta-daemon.tmpfiles.conf")
	for _, l := range activeLines(tf) {
		if strings.Contains(l, "/run/opereta") {
			t.Errorf("tmpfiles line %q redefines /run/opereta", l)
		}
	}
	for _, want := range []string{
		"d /var/lib/opereta 0750 root opereta-llm -",
		"d /var/lib/opereta/models 2750 root opereta-llm -",
		"d /etc/opereta 0750 root root -",
	} {
		if !hasLine(tf, want) {
			t.Errorf("tmpfiles lacks %q", want)
		}
	}
}

// TestSysusersDefinesGroupAndUser 는 그룹과 사용자 선언을 확인한다.
func TestSysusersDefinesGroupAndUser(t *testing.T) {
	su := readFile(t, "opereta.sysusers.conf")
	if !hasLine(su, "g opereta - -") {
		t.Error("sysusers lacks the opereta group")
	}
	if !hasLine(su, `u opereta-llm - "Opereta llama-server" /nonexistent /usr/sbin/nologin`) {
		t.Error("sysusers lacks the opereta-llm user with no home and nologin shell")
	}
}

// TestAlwaysOnDropinsNeverBlockUserShutdown 은 상시 가동 설정이 절전만 막고, 사용자가 직접 끄는 길(전원 버튼, poweroff 명령)을
// 막지 않는지 확인한다. 전원 키와 종료 관련 키는 이 설정에 나오면 안 된다.
func TestAlwaysOnDropinsNeverBlockUserShutdown(t *testing.T) {
	for _, name := range []string{"50-opereta-always-on.logind.conf", "50-opereta-always-on.sleep.conf"} {
		b, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(b), "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "#") || line == "" {
				continue
			}
			for _, forbidden := range []string{"HandlePowerKey", "HandleRebootKey", "PowerOff", "Reboot", "InhibitorsMax"} {
				if strings.HasPrefix(line, forbidden) {
					t.Errorf("%s must not set %s: %q", name, forbidden, line)
				}
			}
		}
	}
	logind, _ := os.ReadFile("50-opereta-always-on.logind.conf")
	for _, want := range []string{"HandleLidSwitch=ignore", "HandleLidSwitchExternalPower=ignore", "HandleLidSwitchDocked=ignore", "HandleSuspendKey=ignore", "HandleHibernateKey=ignore", "IdleAction=ignore"} {
		if !strings.Contains(string(logind), want+"\n") {
			t.Errorf("logind drop-in is missing %s", want)
		}
	}
	sleep, _ := os.ReadFile("50-opereta-always-on.sleep.conf")
	for _, want := range []string{"AllowSuspend=no", "AllowHibernation=no", "AllowHybridSleep=no", "AllowSuspendThenHibernate=no"} {
		if !strings.Contains(string(sleep), want+"\n") {
			t.Errorf("sleep drop-in is missing %s", want)
		}
	}
}

// 스킬은 Deberopa 가 frontmatter 의 설명이 있어야 목록에 올린다. 이름과 설명이 있고, 안내하는 명령이 실제 명령인지 본다.
func TestOperetaSkillIsListedByAgentsAndNamesRealCommands(t *testing.T) {
	text := readFile(t, "skills/opereta/SKILL.md")
	if !strings.HasPrefix(text, "---\n") {
		t.Fatal("SKILL.md must start with a frontmatter block")
	}
	end := strings.Index(text[4:], "\n---\n")
	if end < 0 {
		t.Fatal("the frontmatter block is not closed")
	}
	front := text[4 : 4+end]
	var name, desc string
	for _, line := range strings.Split(front, "\n") {
		switch {
		case strings.HasPrefix(line, "name:"):
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		case strings.HasPrefix(line, "description:"):
			desc = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}
	if name != "opereta" || len(desc) < 40 {
		t.Fatalf("name = %q description = %q", name, desc)
	}
	// 키나 개인 정보가 들어 있지 않아야 한다.
	for _, bad := range []string{"opr_", "sk-", "BEGIN PRIVATE KEY"} {
		if strings.Contains(text, bad) {
			t.Fatalf("SKILL.md contains %q", bad)
		}
	}
	for _, cmd := range []string{"opereta status", "opereta serve stats", "opereta connection", "opereta resources", "opereta exposure", "opereta serve toolcheck", "opereta serve logs"} {
		if !strings.Contains(text, "`"+cmd) {
			t.Fatalf("SKILL.md does not mention %q", cmd)
		}
	}
}

// TestDeberopaUpdateUnits 는 Deberopa 자동 갱신 유닛이 하루 한 번 타이머로 도는 자동 모드(--auto)를 부르고,
// 라이브 세션에서는 돌지 않으며, 쓸 수 있는 경로가 상태 디렉터리와 링크 위치로 제한되는지 확인한다.
func TestDeberopaUpdateUnits(t *testing.T) {
	svc := readFile(t, "opereta-deberopa-update.service")
	for _, line := range []string{
		"Type=oneshot",
		"ExecStart=/usr/lib/opereta/opereta-update deberopa apply --auto",
		"ConditionPathExists=!/run/live",
		"ProtectSystem=strict",
		"NoNewPrivileges=true",
	} {
		if !hasLine(svc, line) {
			t.Errorf("opereta-deberopa-update.service lacks %q", line)
		}
	}
	for _, l := range activeLines(svc) {
		if strings.HasPrefix(l, "ReadWritePaths=") {
			for _, p := range strings.Fields(strings.TrimPrefix(l, "ReadWritePaths=")) {
				switch p {
				case "/var/lib/opereta/deberopa", "/usr/local/lib/opereta", "/usr/local/bin":
				default:
					t.Errorf("ReadWritePaths has an unexpected path %q", p)
				}
			}
		}
	}
	timer := readFile(t, "opereta-deberopa-update.timer")
	for _, line := range []string{"OnBootSec=10min", "OnUnitActiveSec=1d", "Persistent=true", "WantedBy=timers.target"} {
		if !hasLine(timer, line) {
			t.Errorf("opereta-deberopa-update.timer lacks %q", line)
		}
	}
	tmp := readFile(t, "opereta-daemon.tmpfiles.conf")
	for _, line := range []string{
		"d /var/lib/opereta/deberopa 0755 root root -",
		"d /usr/local/lib/opereta/deberopa 0755 root root -",
	} {
		if !hasLine(tmp, line) {
			t.Errorf("tmpfiles lacks %q", line)
		}
	}
}
