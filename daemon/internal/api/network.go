package api

import (
	"context"
	"net/http"
	"sync"

	"github.com/nxodh/lightlixos/daemon/internal/network"
)

// NetworkManager 는 인터넷 연결 상태와 Wi-Fi 연결이다. *network.Manager 가 만족한다.
type NetworkManager interface {
	Status(ctx context.Context) (network.Status, error)
	Scan(ctx context.Context) error
	Networks(ctx context.Context) ([]network.WifiNetwork, error)
	Connect(ctx context.Context, ssid, passphrase string) error
	Disconnect(ctx context.Context) error
	Forget(ctx context.Context, ssid string) error
	SetPowered(ctx context.Context, on bool) error
}

// networkStatus 는 네트워크 오류 코드를 HTTP 상태로 바꾼다.
func networkStatus(code string) int {
	switch code {
	case network.CodeInvalidSSID, network.CodeInvalidPass:
		return http.StatusBadRequest
	case network.CodeNotFound:
		return http.StatusNotFound
	case network.CodeNoWifi, network.CodeNotConnected:
		return http.StatusConflict
	case network.CodeIwdUnavailable:
		return http.StatusServiceUnavailable
	case network.CodeConnectFailed:
		return http.StatusBadGateway
	}
	return http.StatusInternalServerError
}

// netMu 는 Wi-Fi 변경 동작(연결 등)을 한 번에 하나만 하게 한다. 연결은 길 수 있어 다른 설정 변경을 막지 않도록 따로 둔다.
var netMu sync.Mutex

func (s *server) network(w http.ResponseWriter) (NetworkManager, bool) {
	if s.d.Network == nil {
		writeError(w, http.StatusServiceUnavailable, "network_unavailable", "network management is not configured")
		return nil, false
	}
	return s.d.Network, true
}

// getNetwork 는 인터페이스와 무선 상태를 돌려준다.
func (s *server) getNetwork(w http.ResponseWriter, r *http.Request) {
	m, ok := s.network(w)
	if !ok {
		return
	}
	st, err := m.Status(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

// wifiScan 은 무선 검색을 시작한다.
func (s *server) wifiScan(w http.ResponseWriter, r *http.Request) {
	m, ok := s.network(w)
	if !ok {
		return
	}
	if err := m.Scan(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"scanning": true})
}

// wifiNetworks 는 보이는 무선 네트워크 목록이다.
func (s *server) wifiNetworks(w http.ResponseWriter, r *http.Request) {
	m, ok := s.network(w)
	if !ok {
		return
	}
	nets, err := m.Networks(r.Context())
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"networks": nets})
}

// wifiConnect 는 SSID 에 연결한다. 본문은 {"ssid","passphrase"} 이다. 비밀번호는 응답이나 로그에 남기지 않는다.
func (s *server) wifiConnect(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SSID       string `json:"ssid"`
		Passphrase string `json:"passphrase"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	m, ok := s.network(w)
	if !ok {
		return
	}
	netMu.Lock()
	defer netMu.Unlock()
	err := m.Connect(r.Context(), body.SSID, body.Passphrase)
	body.Passphrase = ""
	if err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": true, "ssid": body.SSID})
}

// wifiDisconnect 는 현재 무선 연결을 끊는다.
func (s *server) wifiDisconnect(w http.ResponseWriter, r *http.Request) {
	m, ok := s.network(w)
	if !ok {
		return
	}
	netMu.Lock()
	defer netMu.Unlock()
	if err := m.Disconnect(r.Context()); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"connected": false})
}

// wifiForget 은 저장된 네트워크를 지운다. 본문은 {"ssid"} 이다.
func (s *server) wifiForget(w http.ResponseWriter, r *http.Request) {
	var body struct {
		SSID string `json:"ssid"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	m, ok := s.network(w)
	if !ok {
		return
	}
	netMu.Lock()
	defer netMu.Unlock()
	if err := m.Forget(r.Context(), body.SSID); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"forgotten": body.SSID})
}

// wifiPower 는 무선 장치를 켜거나 끈다. 본문은 {"on"} 이다.
func (s *server) wifiPower(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if !decodeBody(w, r, &body) {
		return
	}
	if body.On == nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "the field \"on\" is required")
		return
	}
	m, ok := s.network(w)
	if !ok {
		return
	}
	netMu.Lock()
	defer netMu.Unlock()
	if err := m.SetPowered(r.Context(), *body.On); err != nil {
		writeErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"powered": *body.On})
}
