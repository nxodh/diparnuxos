package update

import (
	"os"
	"strings"
)

// DefaultChannelFile 은 opereta-archive-keyring 이 설치하는 Opereta 패키지 저장소 소스다.
const DefaultChannelFile = "/etc/apt/sources.list.d/opereta.sources"

// 업데이트 채널이 꺼져 있는 이유다.
const (
	// ChannelMissing 은 소스 파일이 없다.
	ChannelMissing = "missing"
	// ChannelDisabled 는 소스 파일에 Enabled: no 가 있다. 저장소 주소나 서명 키가 정해지지 않은 빌드다.
	ChannelDisabled = "disabled"
)

// Channel 은 Opereta 패키지 저장소(OS 업데이트 채널)가 켜져 있는지 보여 준다.
// 꺼져 있으면 갱신 확인이 "최신"이라고 해도 Opereta 의 새 버전은 볼 수 없다.
type Channel struct {
	Enabled bool   `json:"enabled"`
	URL     string `json:"url,omitempty"`
	// Reason 은 꺼져 있을 때의 이유(missing, disabled)다.
	Reason string `json:"reason,omitempty"`
}

// ReadChannel 은 deb822 소스 파일의 첫 문단에서 Enabled 와 URIs 를 읽는다. path 가 비어 있으면 기본 경로를 쓴다.
func ReadChannel(path string) Channel {
	if path == "" {
		path = DefaultChannelFile
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Channel{Reason: ChannelMissing}
	}
	ch := Channel{Enabled: true}
	seen := false
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			if seen {
				break
			}
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		seen = true
		val = strings.TrimSpace(val)
		switch strings.ToLower(strings.TrimSpace(key)) {
		case "enabled":
			switch strings.ToLower(val) {
			case "no", "false", "0":
				ch.Enabled = false
				ch.Reason = ChannelDisabled
			}
		case "uris":
			if f := strings.Fields(val); len(f) > 0 {
				ch.URL = f[0]
			}
		}
	}
	return ch
}
