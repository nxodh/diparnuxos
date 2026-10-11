package dist

import (
	"os"
	"strings"
	"testing"

	"github.com/nxodh/lightlixos/daemon/internal/firewall"
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
	svc := readFile(t, "lightlixd.service")
	for _, line := range []string{
		"Requires=lightlixd.socket",
		"ExecStart=/usr/sbin/lightlixd",
		"Type=simple",
		"Restart=on-failure",
		"WantedBy=multi-user.target",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ProtectSystem=full",
		"NoNewPrivileges=yes",
	} {
		if !hasLine(svc, line) {
			t.Errorf("lightlixd.service lacks %q", line)
		}
	}
	if !hasLine(svc, "After=lightlixd.socket lightlix-gpu-check.service network.target") {
		t.Errorf("lightlixd.service lacks the After= line with the socket, the GPU check, and network.target")
	}
	if hasLine(svc, "PrivateDevices=yes") {
		t.Error("lightlixd.service sets PrivateDevices=yes, which hides /dev/nvidia* from NVML")
	}
}

// TestSocketUnit 은 소켓의 경로, 모드, 그룹, 설치 대상을 확인한다.
func TestSocketUnit(t *testing.T) {
	sock := readFile(t, "lightlixd.socket")
	for _, line := range []string{
		"ListenStream=/run/lightlix/lightlixd.sock",
		"SocketMode=0660",
		"SocketGroup=lightlix",
		"WantedBy=sockets.target",
	} {
		if !hasLine(sock, line) {
			t.Errorf("lightlixd.socket lacks %q", line)
		}
	}
}

// TestLlamaUnitKeepsGPUAccess 는 llama 유닛이 GPU 접근을 막는 옵션을 쓰지 않고, 자동 재시작과 [Install] 이 없는지 확인한다.
func TestLlamaUnitKeepsGPUAccess(t *testing.T) {
	llama := readFile(t, "lightlix-llama.service")
	for _, line := range []string{
		"User=lightlix-llm",
		"Group=lightlix-llm",
		"EnvironmentFile=/run/lightlix/llama-server.env",
		"ExecStart=/usr/lib/lightlix/llama/llama-server",
		"Restart=no",
		"NoNewPrivileges=yes",
		"ProtectSystem=strict",
		"ProtectHome=yes",
		"PrivateTmp=yes",
		"ReadOnlyPaths=/var/lib/lightlix/models",
		"LimitMEMLOCK=infinity",
		"RestrictAddressFamilies=AF_INET AF_INET6 AF_UNIX",
	} {
		if !hasLine(llama, line) {
			t.Errorf("lightlix-llama.service lacks %q", line)
		}
	}
	for _, bad := range []string{"PrivateDevices=yes", "MemoryDenyWriteExecute=yes"} {
		if hasLine(llama, bad) {
			t.Errorf("lightlix-llama.service sets %q, which breaks CUDA device access", bad)
		}
	}
	for _, l := range activeLines(llama) {
		if strings.HasPrefix(l, "[Install]") || strings.HasPrefix(l, "WantedBy=") {
			t.Errorf("lightlix-llama.service has an install section line %q; the daemon starts it", l)
		}
	}
}

// TestFirewallUnitsAreExclusive 는 저장된 규칙과 기본 규칙 유닛이 서로 반대 조건으로만 시작하는지 확인한다.
func TestFirewallUnitsAreExclusive(t *testing.T) {
	saved := readFile(t, "lightlix-firewall.service")
	def := readFile(t, "lightlix-firewall-default.service")
	if !hasLine(saved, "ConditionPathExists=/var/lib/lightlix/firewall.nft") {
		t.Error("saved-rules unit lacks its path condition")
	}
	if !hasLine(def, "ConditionPathExists=!/var/lib/lightlix/firewall.nft") {
		t.Error("default-rules unit lacks the negated path condition")
	}
	if !hasLine(saved, "ExecStart=/usr/sbin/nft -f /var/lib/lightlix/firewall.nft") {
		t.Error("saved-rules unit applies the wrong file")
	}
	if !hasLine(def, "ExecStart=/usr/sbin/nft -f /usr/share/lightlix/firewall-default.nft") {
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

// TestTmpfilesDoesNotRedefineRun 은 /run/lightlix 를 이 설정에서 다시 정의하지 않는지 확인한다.
// 그 경로는 lightlix-nvidia 패키지가 만든다.
func TestTmpfilesDoesNotRedefineRun(t *testing.T) {
	tf := readFile(t, "lightlix-daemon.tmpfiles.conf")
	for _, l := range activeLines(tf) {
		if strings.Contains(l, "/run/lightlix") {
			t.Errorf("tmpfiles line %q redefines /run/lightlix", l)
		}
	}
	for _, want := range []string{
		"d /var/lib/lightlix 0755 root root -",
		"d /var/lib/lightlix/models 0755 root root -",
		"d /etc/lightlix 0755 root root -",
	} {
		if !hasLine(tf, want) {
			t.Errorf("tmpfiles lacks %q", want)
		}
	}
}

// TestSysusersDefinesGroupAndUser 는 그룹과 사용자 선언을 확인한다.
func TestSysusersDefinesGroupAndUser(t *testing.T) {
	su := readFile(t, "lightlix.sysusers.conf")
	if !hasLine(su, "g lightlix - -") {
		t.Error("sysusers lacks the lightlix group")
	}
	if !hasLine(su, `u lightlix-llm - "Lightlix llama-server" /nonexistent /usr/sbin/nologin`) {
		t.Error("sysusers lacks the lightlix-llm user with no home and nologin shell")
	}
}
