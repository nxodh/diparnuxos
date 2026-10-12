// Package config는 관리자 설정 파일(/etc/lightlix/lightlixd.json)을 읽고 쓴다.
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

	"github.com/nxodh/lightlixos/daemon/internal/autoconf"
)

// Config는 관리자가 정하는 데몬 설정이다.
type Config struct {
	Port       int       `json:"port"`        // llama-server 의 API 포트, 기본 8080. LAN 공개 시 방화벽이 여는 포트다. 데몬 자신은 유닉스 소켓만 쓴다
	LANExposed bool      `json:"lan_exposed"` // true이면 API를 LAN에 연다(API 키 필수)
	Autostart  bool      `json:"autostart"`   // 부팅 때 모델 서버를 띄운다
	SSHEnabled bool      `json:"ssh_enabled"` // true이면 SSH 포트를 방화벽에서 연다. 유닛 시작은 /v1/system/ssh가 함께 처리한다
	ModelsDir  string    `json:"models_dir"`  // 절대 경로, 기본 /var/lib/lightlix/models
	HFToken    string    `json:"hf_token"`    // Hugging Face 토큰, 진단 리포트에서는 가린다
	Overrides  Overrides `json:"overrides"`   // 자동 계획을 덮어쓰는 수동 값
}

// Overrides는 autoconf의 재정의와 같은 항목이다. nil이면 자동 계획을 쓴다.
type Overrides struct {
	NGL       *string `json:"ngl,omitempty"`
	Ctx       *int    `json:"ctx,omitempty"`
	CacheType *string `json:"cache_type,omitempty"`
	Batch     *int    `json:"batch,omitempty"`
	LoadMode  *string `json:"load_mode,omitempty"`
}

// Default는 설정 파일이 없을 때 쓰는 기본값이다.
func Default() Config {
	return Config{
		Port:      8080,
		Autostart: true,
		ModelsDir: "/var/lib/lightlix/models",
	}
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
	if !path.IsAbs(c.ModelsDir) {
		errs = append(errs, fmt.Errorf("models_dir %q must be an absolute path", c.ModelsDir))
	}
	if err := autoconf.ValidateOverrides(c.Overrides.toAutoconf()); err != nil {
		errs = append(errs, fmt.Errorf("overrides: %w", err))
	}
	return errors.Join(errs...)
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
