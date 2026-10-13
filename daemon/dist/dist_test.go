package dist

import (
	"os"
	"strings"
	"testing"

	"github.com/nxodh/diparnuxos/daemon/internal/firewall"
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
	svc := readFile(t, "diparnuxd.service")
	for _, line := range []string{
		"Requires=diparnuxd.socket diparnux-firewall-default.service",
		"ExecStart=/usr/sbin/diparnuxd",
		"Type=simple",
		"Restart=on-failure",
		"WantedBy=multi-user.target",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ProtectSystem=full",
		"NoNewPrivileges=yes",
	} {
		if !hasLine(svc, line) {
			t.Errorf("diparnuxd.service lacks %q", line)
		}
	}
	if !hasLine(svc, "After=diparnuxd.socket diparnux-gpu-check.service diparnux-firewall-default.service diparnux-firewall.service network.target") {
		t.Errorf("diparnuxd.service lacks the After= line with the socket, the GPU check, and network.target")
	}
	if hasLine(svc, "PrivateDevices=yes") {
		t.Error("diparnuxd.service sets PrivateDevices=yes, which hides /dev/nvidia* from NVML")
	}
}

// TestSocketUnit 은 소켓의 경로, 모드, 그룹, 설치 대상을 확인한다.
func TestSocketUnit(t *testing.T) {
	sock := readFile(t, "diparnuxd.socket")
	for _, line := range []string{
		"ListenStream=/run/diparnux/diparnuxd.sock",
		"SocketMode=0660",
		"SocketGroup=diparnux",
		"WantedBy=sockets.target",
	} {
		if !hasLine(sock, line) {
			t.Errorf("diparnuxd.socket lacks %q", line)
		}
	}
}

// TestLlamaUnitKeepsGPUAccess 는 llama 유닛이 GPU 접근을 막는 옵션을 쓰지 않고, 자동 재시작과 [Install] 이 없는지 확인한다.
func TestLlamaUnitKeepsGPUAccess(t *testing.T) {
	llama := readFile(t, "diparnux-llama.service")
	for _, line := range []string{
		"User=diparnux-llm",
		"Group=diparnux-llm",
		"SupplementaryGroups=video render",
		"ExecStartPre=/usr/lib/diparnux/check-gpu-permissions",
		"EnvironmentFile=/run/diparnux/llama-server.env",
		"ExecStart=/usr/lib/diparnux/llama/llama-server",
		"Restart=no",
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ReadOnlyPaths=/var/lib/diparnux/models",
		"LimitMEMLOCK=infinity",
		"RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX",
	} {
		if !hasLine(llama, line) {
			t.Errorf("diparnux-llama.service lacks %q", line)
		}
	}
	for _, bad := range []string{"PrivateDevices=yes", "MemoryDenyWriteExecute=yes"} {
		if hasLine(llama, bad) {
			t.Errorf("diparnux-llama.service sets %q, which breaks CUDA device access", bad)
		}
	}
	for _, l := range activeLines(llama) {
		if strings.HasPrefix(l, "[Install]") || strings.HasPrefix(l, "WantedBy=") {
			t.Errorf("diparnux-llama.service has an install section line %q; the daemon starts it", l)
		}
	}
}

// TestLlamaUnitWaitsForExecBeforeStartReturns는 데몬의 즉시 MainPID 검증에 필요한 실행 장벽을 고정한다.
func TestLlamaUnitWaitsForExecBeforeStartReturns(t *testing.T) {
	llama := readFile(t, "diparnux-llama.service")
	if !hasLine(llama, "Type=exec") {
		t.Fatal("diparnux-llama.service must wait until ExecStart has executed before systemctl start succeeds")
	}
	if hasLine(llama, "Type=simple") {
		t.Fatal("Type=simple can report success before ExecStart calls execve")
	}
}

// TestFirewallDefaultsBeforeSaved 는 저장 파일이 깨져도 기본 차단을 먼저 적용하는 의존성을 확인한다.
func TestFirewallDefaultsBeforeSaved(t *testing.T) {
	saved := readFile(t, "diparnux-firewall.service")
	def := readFile(t, "diparnux-firewall-default.service")
	if !hasLine(saved, "ConditionPathExists=/var/lib/diparnux/firewall.nft") {
		t.Error("saved-rules unit lacks its path condition")
	}
	for _, line := range activeLines(def) {
		if strings.HasPrefix(line, "Condition") {
			t.Errorf("default firewall must always apply, found %q", line)
		}
	}
	for _, line := range []string{"Requires=diparnux-firewall-default.service", "After=local-fs.target diparnux-firewall-default.service"} {
		if !hasLine(saved, line) {
			t.Errorf("saved firewall lacks %q", line)
		}
	}
	if !hasLine(def, "After=local-fs.target nftables.service") {
		t.Error("default firewall must follow any nftables.service ruleset flush")
	}
	if !hasLine(saved, "ExecStart=/usr/sbin/nft -f /var/lib/diparnux/firewall.nft") {
		t.Error("saved-rules unit applies the wrong file")
	}
	if !hasLine(def, "ExecStart=/usr/sbin/nft -f /usr/share/diparnux/firewall-default.nft") {
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

// TestTmpfilesDoesNotRedefineRun 은 /run/diparnux 를 이 설정에서 다시 정의하지 않는지 확인한다.
// 그 경로는 diparnux-nvidia 패키지가 만든다.
func TestTmpfilesDoesNotRedefineRun(t *testing.T) {
	tf := readFile(t, "diparnux-daemon.tmpfiles.conf")
	for _, l := range activeLines(tf) {
		if strings.Contains(l, "/run/diparnux") {
			t.Errorf("tmpfiles line %q redefines /run/diparnux", l)
		}
	}
	for _, want := range []string{
		"d /var/lib/diparnux 0750 root diparnux-llm -",
		"d /var/lib/diparnux/models 2750 root diparnux-llm -",
		"d /etc/diparnux 0750 root root -",
	} {
		if !hasLine(tf, want) {
			t.Errorf("tmpfiles lacks %q", want)
		}
	}
}

// TestSysusersDefinesGroupAndUser 는 그룹과 사용자 선언을 확인한다.
func TestSysusersDefinesGroupAndUser(t *testing.T) {
	su := readFile(t, "diparnux.sysusers.conf")
	if !hasLine(su, "g diparnux - -") {
		t.Error("sysusers lacks the diparnux group")
	}
	if !hasLine(su, `u diparnux-llm - "Diparnux llama-server" /nonexistent /usr/sbin/nologin`) {
		t.Error("sysusers lacks the diparnux-llm user with no home and nologin shell")
	}
}
