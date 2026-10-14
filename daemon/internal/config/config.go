// Package config는 관리자 설정 파일(/etc/diparnux/diparnuxd.json)을 읽고 쓴다.
//
// 알 수 없는 필드는 오류로 한다. 오타가 조용히 무시되어 기본값으로 도는 일을 막기 위해서다.
// 저장은 같은 디렉터리에 임시 파일을 만들고 fsync 후 rename한다. 토큰이 들어가므로 모드는 0600이다.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/nxodh/diparnuxos/daemon/internal/autoconf"
)

// Config는 관리자가 정하는 데몬 설정이다.
type Config struct {
	Port       int  `json:"port"`        // llama-server 의 API 포트, 기본 8080. LAN 공개 시 방화벽이 여는 포트다. 데몬 자신은 유닉스 소켓만 쓴다
	LANExposed bool `json:"lan_exposed"` // true이면 API를 LAN에 연다(API 키 필수)
	Autostart  bool `json:"autostart"`   // 부팅 때 모델 서버를 띄운다
	// AutoRestart 가 true 이면 서버가 예상 밖으로 멈췄을 때 데몬이 대기 시간을 늘려 가며 다시 시작한다.
	AutoRestart bool `json:"auto_restart"`
	SSHEnabled  bool `json:"ssh_enabled"` // true이면 SSH 포트를 방화벽에서 연다. 유닛 시작은 /v1/system/ssh가 함께 처리한다
	// SSHPort 는 SSH 서버 포트다. 0 이면 22 다. sshd 설정(Port)과 방화벽이 같은 값을 쓴다.
	SSHPort   int    `json:"ssh_port"`
	ModelsDir string `json:"models_dir"` // 절대 경로, 기본 /var/lib/diparnux/models
	HFToken   string `json:"hf_token"`   // Hugging Face 토큰, 진단 리포트에서는 가린다
	// Gateway 가 true 이면 데몬이 공개 포트(Port)를 받아 키를 확인하고 llama-server 로 넘긴다. llama-server 는
	// 127.0.0.1:InternalPort 에서만 연다. 키 변경이 서버 재시작 없이 반영되고 키별 사용량을 알 수 있다. false 이면
	// 예전처럼 llama-server 가 Port 를 직접 받는다. 바꾸면 서버를 다시 시작해야 반영된다.
	Gateway      bool `json:"gateway"`
	InternalPort int  `json:"internal_port"` // 게이트웨이를 쓸 때 llama-server 가 여는 포트(127.0.0.1), 기본 18080
	// LocalNoKey 가 true 이면 이 컴퓨터에서 온 요청은 키 없이 게이트웨이를 통과한다(기본 true). LAN 의 다른 컴퓨터는
	// 여전히 키가 필요하다. 내부 포트가 로컬 프로세스에 열려 있으므로 새 위험은 아니다.
	LocalNoKey bool      `json:"local_no_key"`
	Overrides  Overrides `json:"overrides"` // 자동 계획을 덮어쓰는 수동 값

	// 아래 TLS 설정은 켜지 않으면 파일에 쓰이지 않는다(omitempty). 켜지 않은 설정 파일은 0.5.3 과 같아서 되돌려도 읽힌다.

	// TLS 가 true 이고 LAN 공개와 게이트웨이가 켜져 있으면 데몬이 TLS 포트(TLSPort)에서 듣는다. 그동안 LAN 에는 평문
	// 포트(Port)를 열지 않는다(LANPlain 이 아니면). 같은 컴퓨터의 평문 주소(127.0.0.1:Port)는 그대로 쓴다.
	TLS bool `json:"tls,omitempty"`
	// TLSPort 는 TLS 포트다. 0 이면 DefaultTLSPort 다.
	TLSPort int `json:"tls_port,omitempty"`
	// LANPlain 이 true 이면 TLS 를 켠 뒤에도 평문 포트를 LAN 에 열어 둔다(옛 클라이언트용. 암호화되지 않는다).
	LANPlain bool `json:"lan_plain,omitempty"`
	// TLSNames 는 서버 인증서에 더 넣을 DNS 이름이다. CA 를 만들 때 포함된 이름만 인증서에 들어간다.
	TLSNames []string `json:"tls_names,omitempty"`
}

// DefaultTLSPort 는 TLS 포트의 기본값이다. 원격 관리 웹의 기본 포트(8443)와 구별한다.
const DefaultTLSPort = 8444

// TLSListenPort 는 TLS 포트의 실제 값이다.
func (c Config) TLSListenPort() int {
	if c.TLSPort == 0 {
		return DefaultTLSPort
	}
	return c.TLSPort
}

// TLSActive 는 TLS 리스너가 떠야 하는지다: TLS 가 켜져 있고 LAN 에 공개하며 게이트웨이가 있다.
func (c Config) TLSActive() bool { return c.TLS && c.LANExposed && c.Gateway }

// PlainLANOpen 은 평문 포트를 LAN 에 열어야 하는지다. TLS 가 떠 있으면 LANPlain 일 때만 연다.
func (c Config) PlainLANOpen() bool {
	if !c.LANExposed {
		return false
	}
	return !c.TLSActive() || c.LANPlain
}

// Overrides는 autoconf의 재정의와 같은 항목이다. nil이면 자동 계획을 쓴다.
type Overrides struct {
	NGL       *string `json:"ngl,omitempty"`
	Ctx       *int    `json:"ctx,omitempty"`
	CacheType *string `json:"cache_type,omitempty"`
	Batch     *int    `json:"batch,omitempty"`
	LoadMode  *string `json:"load_mode,omitempty"`
	Parallel  *int    `json:"parallel,omitempty"`
	Profile   *string `json:"profile,omitempty"`
}

// Default는 설정 파일이 없을 때 쓰는 기본값이다.
func Default() Config {
	return Config{
		Port:         8080,
		Autostart:    true,
		AutoRestart:  true,
		ModelsDir:    "/var/lib/diparnux/models",
		Gateway:      true,
		InternalPort: 18080,
		LocalNoKey:   true,
	}
}

// BackendPort 는 llama-server 가 실제로 여는 포트다. 게이트웨이를 쓰면 InternalPort, 아니면 Port 다.
func (c Config) BackendPort() int {
	if c.Gateway {
		return c.InternalPort
	}
	return c.Port
}

// Load는 file의 설정 파일을 읽는다. 파일이 없으면 기본값을 돌려준다.
// 모르는 필드, 파일 뒤의 추가 데이터, 잘못된 값은 모두 오류다.
func Load(file string) (Config, error) {
	cfg := Default()
	f, err := os.Open(file)
	if errors.Is(err, fs.ErrNotExist) {
		return cfg, nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("config: open %s: %w", file, err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("config: decode %s: %w", file, err)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return Config{}, fmt.Errorf("config: %s has data after the JSON object", file)
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("config: %s: %w", file, err)
	}
	return cfg, nil
}

// Save는 설정을 원자적으로 쓴다. 잘못된 설정은 저장하지 않는다.
func Save(file string, cfg Config) error {
	if err := cfg.Validate(); err != nil {
		return fmt.Errorf("config: refusing to save invalid config: %w", err)
	}
	data, err := json.MarshalIndent(cfg, "", "  ")
	if err != nil {
		return fmt.Errorf("config: encode: %w", err)
	}
	data = append(data, '\n')

	dir := filepath.Dir(file)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(file)+".tmp-*")
	if err != nil {
		return fmt.Errorf("config: create temp file in %s: %w", dir, err)
	}
	tmpName := tmp.Name()
	committed := false
	defer func() {
		if !committed {
			_ = tmp.Close()
			_ = os.Remove(tmpName)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		return fmt.Errorf("config: chmod %s: %w", tmpName, err)
	}
	if _, err := tmp.Write(data); err != nil {
		return fmt.Errorf("config: write %s: %w", tmpName, err)
	}
	if err := tmp.Sync(); err != nil {
		return fmt.Errorf("config: sync %s: %w", tmpName, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close %s: %w", tmpName, err)
	}
	if err := os.Rename(tmpName, file); err != nil {
		return fmt.Errorf("config: rename %s to %s: %w", tmpName, file, err)
	}
	committed = true

	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("config: open dir %s: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("config: sync dir %s: %w", dir, err)
	}
	return nil
}

// Validate는 설정 값의 범위와 형식을 검사한다. 여러 문제는 한꺼번에 돌려준다.
func (c Config) Validate() error {
	var errs []error
	if c.Port < 1 || c.Port > 65535 {
		errs = append(errs, fmt.Errorf("port %d is out of range 1-65535", c.Port))
	}
	if c.Gateway {
		if c.InternalPort < 1 || c.InternalPort > 65535 {
			errs = append(errs, fmt.Errorf("internal_port %d is out of range 1-65535", c.InternalPort))
		} else if c.InternalPort == c.Port {
			errs = append(errs, fmt.Errorf("internal_port must differ from port %d", c.Port))
		}
	}
	if c.SSHPort < 0 || c.SSHPort > 65535 {
		errs = append(errs, fmt.Errorf("ssh_port %d is out of range 0-65535", c.SSHPort))
	}
	errs = append(errs, c.validateTLS()...)
	if !path.IsAbs(c.ModelsDir) {
		errs = append(errs, fmt.Errorf("models_dir %q must be an absolute path", c.ModelsDir))
	}
	if err := autoconf.ValidateOverrides(c.Overrides.toAutoconf()); err != nil {
		errs = append(errs, fmt.Errorf("overrides: %w", err))
	}
	return errors.Join(errs...)
}

// validateTLS 는 TLS 설정을 검사한다. TLS 를 끄면 포트와 이름은 값만 범위를 본다.
func (c Config) validateTLS() []error {
	var errs []error
	if c.TLSPort < 0 || c.TLSPort > 65535 {
		errs = append(errs, fmt.Errorf("tls_port %d is out of range 0-65535", c.TLSPort))
	}
	if len(c.TLSNames) > 32 {
		errs = append(errs, fmt.Errorf("tls_names has %d entries; at most 32", len(c.TLSNames)))
	}
	for _, n := range c.TLSNames {
		if !validDNSName(n) {
			errs = append(errs, fmt.Errorf("tls_names: %q is not a valid DNS name", n))
		}
	}
	if !c.TLS {
		return errs
	}
	if !c.Gateway {
		errs = append(errs, errors.New("tls needs the gateway: the daemon terminates TLS (turn gateway on)"))
	}
	p := c.TLSListenPort()
	if p >= 1 && p <= 65535 {
		switch {
		case p == c.Port:
			errs = append(errs, fmt.Errorf("tls_port must differ from port %d", c.Port))
		case c.Gateway && p == c.InternalPort:
			errs = append(errs, fmt.Errorf("tls_port must differ from internal_port %d", c.InternalPort))
		case p == sshPort(c):
			errs = append(errs, fmt.Errorf("tls_port must differ from the SSH port %d", p))
		}
	}
	return errs
}

func sshPort(c Config) int {
	if c.SSHPort == 0 {
		return 22
	}
	return c.SSHPort
}

// validDNSName 은 호스트 이름이나 도메인 이름 형식(라벨 1-63자, 영숫자와 '-', 전체 253자 이하)인지 본다.
func validDNSName(n string) bool {
	if n == "" || len(n) > 253 {
		return false
	}
	for _, label := range strings.Split(n, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for i := 0; i < len(label); i++ {
			c := label[i]
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return false
			}
		}
	}
	return true
}

// Redacted는 토큰을 가린 사본을 돌려준다. 진단 리포트에는 이 값만 쓴다.
func (c Config) Redacted() Config {
	out := c
	if out.HFToken != "" {
		out.HFToken = "<redacted>"
	}
	out.Overrides = copyOverrides(c.Overrides)
	return out
}

// toAutoconf는 설정의 재정의를 autoconf 타입으로 바꾼다. 포인터가 가리키는 값을 복사한다.
func (o Overrides) toAutoconf() autoconf.Overrides {
	return autoconf.Overrides{
		NGL:       copyStr(o.NGL),
		Ctx:       copyInt(o.Ctx),
		CacheType: copyStr(o.CacheType),
		Batch:     copyInt(o.Batch),
		LoadMode:  copyStr(o.LoadMode),
		Parallel:  copyInt(o.Parallel),
		Profile:   copyStr(o.Profile),
	}
}

// copyOverrides는 재정의 구조체를 깊이 복사한다.
func copyOverrides(o Overrides) Overrides {
	return Overrides{
		NGL:       copyStr(o.NGL),
		Ctx:       copyInt(o.Ctx),
		CacheType: copyStr(o.CacheType),
		Batch:     copyInt(o.Batch),
		LoadMode:  copyStr(o.LoadMode),
		Parallel:  copyInt(o.Parallel),
		Profile:   copyStr(o.Profile),
	}
}

func copyStr(p *string) *string {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func copyInt(p *int) *int {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
