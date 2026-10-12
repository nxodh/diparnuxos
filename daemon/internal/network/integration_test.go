//go:build integration

package network

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/runner"
)

// 이 시험은 실제 busctl 로 tests/network/fake_iwd.py 가 띄운 사설 D-Bus 서비스를 부른다.
// scripts/test-network-in-docker.sh 가 버스와 서비스를 만들고 LIGHTLIX_TEST_BUS 를 준다.
func liveManager(t *testing.T) *Manager {
	t.Helper()
	addr := os.Getenv("LIGHTLIX_TEST_BUS")
	if addr == "" {
		t.Skip("LIGHTLIX_TEST_BUS is not set")
	}
	dir := t.TempDir()
	sys := filepath.Join(dir, "sys")
	for _, p := range []string{"wlan0/wireless", "wlan0/device"} {
		if err := os.MkdirAll(filepath.Join(sys, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return &Manager{
		Runner: runner.Exec{}, BusctlArgs: []string{"--address=" + addr},
		IwdDir: filepath.Join(dir, "iwd"), SysClassNet: sys, KnownWait: 800 * time.Millisecond,
	}
}

func TestLiveBusctlStatusAndNetworks(t *testing.T) {
	m := liveManager(t)
	ctx := context.Background()
	w, tree := m.wifiState(ctx)
	if !w.Available || w.Device != "wlan0" || w.ConnectedSSID != "HomeWiFi" || !w.Powered || tree == nil {
		t.Fatalf("wifi state = %+v", w)
	}
	nets, err := m.Networks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nets) != 3 || nets[0].SSID != "HomeWiFi" || !nets[0].Connected || !nets[0].Known || nets[1].SSID != "CafeFree" || nets[1].Security != "open" {
		t.Fatalf("networks = %+v", nets)
	}
	if err := m.Scan(ctx); err != nil {
		t.Fatalf("scan: %v", err)
	}
}

func TestLiveBusctlConnectDisconnectForgetPower(t *testing.T) {
	m := liveManager(t)
	ctx := context.Background()
	if err := m.Connect(ctx, "CafeFree", ""); err != nil {
		t.Fatalf("connect open: %v", err)
	}
	if w, _ := m.wifiState(ctx); w.ConnectedSSID != "CafeFree" {
		t.Fatalf("connected = %q", w.ConnectedSSID)
	}
	// 새 보안 네트워크: 파일을 쓰지만 가짜 서비스는 파일을 읽지 않으므로 알려진 네트워크가 되지 않는다.
	err := m.Connect(ctx, "Office 5G", "s3cretpassw0rd")
	if err == nil {
		t.Fatal("expected a failure from the fake service")
	}
	if _, serr := os.Stat(filepath.Join(m.iwdDir(), "Office 5G.psk")); serr == nil {
		t.Error("the unconfirmed configuration must be removed")
	}
	if err := m.Disconnect(ctx); err != nil {
		t.Fatalf("disconnect: %v", err)
	}
	if err := m.Forget(ctx, "HomeWiFi"); err != nil {
		t.Fatalf("forget: %v", err)
	}
	if err := m.SetPowered(ctx, false); err != nil {
		t.Fatalf("power off: %v", err)
	}
	if w, _ := m.wifiState(ctx); w.Powered {
		t.Error("the device should be off")
	}
}
