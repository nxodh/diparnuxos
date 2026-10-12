// Command lightlixd는 Lightlix 데몬이다. 유닉스 소켓으로 REST API 를 제공하고 llama-server 유닛을 관리한다.
//
// 시작 순서는 설정 로드, 의존성 조립, 방화벽 적용, 서버 상태 조정, API 시작, Watch 다.
// 종료는 SIGTERM 과 SIGINT 로 한다. 데몬이 끝나도 llama-server 유닛은 멈추지 않는다.
// 로그는 표준 오류로 쓰고 systemd 저널이 받는다. 비밀은 로그에 쓰지 않는다.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/accounts"
	"github.com/nxodh/lightlixos/daemon/internal/api"
	"github.com/nxodh/lightlixos/daemon/internal/apikeys"
	"github.com/nxodh/lightlixos/daemon/internal/config"
	"github.com/nxodh/lightlixos/daemon/internal/hf"
	"github.com/nxodh/lightlixos/daemon/internal/jobs"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/nvml"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
	"github.com/nxodh/lightlixos/daemon/internal/report"
	"github.com/nxodh/lightlixos/daemon/internal/runner"
	"github.com/nxodh/lightlixos/daemon/internal/supervisor"
	"github.com/nxodh/lightlixos/daemon/internal/sysinfo"
)

// version 은 빌드 때 -ldflags "-X main.version=..." 으로 넣는다.
var version = "dev"

// 기본 경로와 이름이다. 플래그로 바꿀 수 있다.
const (
	defaultConfigPath  = "/etc/lightlix/lightlixd.json"
	defaultStateDir    = "/var/lib/lightlix"
	defaultSocketPath  = "/run/lightlix/lightlixd.sock"
	defaultSocketGroup = "lightlix"
	keyGroup           = "lightlix-llm"
	llamaUnit          = "lightlix-llama.service"
	llamaBinary        = "/usr/lib/lightlix/llama/llama-server"
	gpuCheckBinary     = "/usr/sbin/lightlix-gpu-check"
	gpuStatusPath      = "/run/lightlix/gpu-status"
	envFilePath        = "/run/lightlix/llama-server.env"
	osReleasePath      = "/etc/os-release"
	meminfoPath        = "/proc/meminfo"
	shutdownTimeout    = 10 * time.Second
	// minFreeMargin 은 내려받기 뒤에 남겨 둘 디스크 여유다.
	minFreeMargin = 1 << 30
	maxJobs       = 2
)

func main() {
	log.SetFlags(log.LstdFlags)
	log.SetPrefix("lightlixd: ")
	if err := run(os.Args[1:]); err != nil {
		log.Printf("fatal: %v", err)
		os.Exit(1)
	}
}

// run은 데몬을 시작하고 신호를 받을 때까지 돈다.
func run(args []string) error {
	fs := flag.NewFlagSet("lightlixd", flag.ContinueOnError)
	configPath := fs.String("config", defaultConfigPath, "path of the daemon config file")
	stateDir := fs.String("state-dir", defaultStateDir, "state directory for models, API keys, and the firewall file")
	socketPath := fs.String("socket", defaultSocketPath, "unix socket path (ignored with systemd socket activation)")
	socketGroup := fs.String("socket-group", defaultSocketGroup, "group that may connect to the socket")
	showVersion := fs.Bool("version", false, "print the version and exit")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("lightlixd", version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	src := &configSource{path: *configPath, last: cfg}

	// 의존성 조립. 시스템에 닿는 것은 모두 여기서 만든다.
	gpu := nvml.New()
	defer gpu.Close()
	cmds := runner.Exec{}
	mem := func() (platform.MemInfo, error) { return sysinfo.ReadMemInfo(meminfoPath) }

	store := models.Store{Dir: cfg.ModelsDir}
	keyFile := *stateDir + "/api-keys"
	keys := &apikeys.Store{KeyFile: keyFile, MetaFile: *stateDir + "/api-keys.json"}
	keyGID, err := groupID(keyGroup)
	if err != nil {
		return fmt.Errorf("look up llama-server key group: %w", err)
	}

	jobMgr := jobs.NewManager(jobs.Options{
		HF:            &hf.Client{Token: cfg.HFToken},
		Store:         store,
		FreeBytes:     sysinfo.DiskFree,
		MinFreeMargin: minFreeMargin,
		Now:           time.Now,
		MaxJobs:       maxJobs,
	})
	defer jobMgr.Close()

	sup := supervisor.New(supervisor.Options{
		Runner:         cmds,
		GPU:            gpu,
		Store:          store,
		Keys:           keys,
		Config:         src.get,
		MemInfo:        mem,
		GPUStatusPath:  gpuStatusPath,
		EnvFilePath:    envFilePath,
		ServerBinary:   llamaBinary,
		Unit:           llamaUnit,
		FirewallPath:   *stateDir + "/firewall.nft",
		KeySnapshotDir: *stateDir,
		KeySnapshotGID: uint32(keyGID),
		ChownKeyFile:   chownKeyFile,
		Now:            time.Now,
	})

	rep := report.Deps{
		Version:       version,
		Now:           time.Now,
		Runner:        cmds,
		GPU:           gpu,
		MemInfo:       mem,
		Config:        func() (config.Config, error) { return config.Load(*configPath) },
		KeyCount:      keys.Count,
		Models:        store,
		Server:        sup.Status,
		Secrets:       secretsFunc(keyFile, *stateDir, src.get),
		GPUStatusPath: gpuStatusPath,
		EnvFilePath:   envFilePath,
		OSReleasePath: osReleasePath,
	}

	accountsMgr := &accounts.Manager{Runner: cmds, ListPath: *stateDir + "/users.list"}
	handler := api.New(api.Deps{
		Supervisor:    sup,
		Jobs:          jobMgr,
		Store:         store,
		Keys:          keys,
		HF:            hfProxy{cfg: src.get},
		Accounts:      accountsMgr,
		GPU:           gpu,
		Runner:        cmds,
		ConfigPath:    *configPath,
		FirstBootPath: *stateDir + "/firstboot.json",
		GPUStatusPath: gpuStatusPath,
		GPUCheckPath:  gpuCheckBinary,
		MemInfo:       mem,
		Report:        func(ctx context.Context) string { return report.Build(ctx, rep) },
		Version:       version,
		Now:           time.Now,
		ChownKeyFile:  chownKeyFile,
		HTTP:          &http.Client{},
	})

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	// 방화벽은 실패해도 데몬을 띄운다. 이전 규칙이 그대로 남으므로 LAN 공개가 꺼진 상태라면 포트는 닫혀 있다.
	if err := sup.ApplyFirewall(ctx); err != nil {
		log.Printf("firewall rules were not applied: %v", err)
	}
	if err := sup.Initialize(ctx); err != nil {
		log.Printf("llama startup reconciliation failed: %v", err)
	}
	restoreSSH(ctx, *configPath, sup, accountsMgr)

	ln, created, err := socketListener(*socketPath, *socketGroup)
	if err != nil {
		return fmt.Errorf("socket: %w", err)
	}
	if created {
		log.Printf("listening on %s (group %s, mode 0660)", *socketPath, *socketGroup)
	} else {
		log.Printf("using the socket passed by systemd")
	}

	srv := &http.Server{
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      180 * time.Second,
		IdleTimeout:       60 * time.Second,
		MaxHeaderBytes:    64 << 10,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(ln) }()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		sup.Autostart(ctx)
	}()
	go func() {
		defer wg.Done()
		sup.Watch(ctx)
	}()

	select {
	case <-ctx.Done():
		log.Printf("stop signal received; shutting down the API (llama-server keeps running)")
	case err := <-serveErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("API server stopped: %v", err)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		log.Printf("API shutdown: %v", err)
	}
	wg.Wait()
	log.Printf("stopped")
	return nil
}

// configSource 는 설정 파일을 매번 읽는다. 읽지 못하면 마지막으로 읽은 좋은 설정을 쓴다.
type configSource struct {
	path string
	mu   sync.Mutex
	last config.Config
}

// get 은 현재 설정을 돌려준다.
func (c *configSource) get() config.Config {
	cfg, err := config.Load(c.path)
	c.mu.Lock()
	defer c.mu.Unlock()
	if err != nil {
		log.Printf("config: keeping the last good config: %v", err)
		return c.last
	}
	c.last = cfg
	return cfg
}

// hfProxy 는 API 의 검색과 원격 조회에 쓰는 Hugging Face 클라이언트다. 호출마다 현재 토큰을 쓴다.
type hfProxy struct {
	cfg func() config.Config
}

func (p hfProxy) client() *hf.Client {
	return &hf.Client{Token: p.cfg().HFToken}
}

func (p hfProxy) Search(ctx context.Context, query string, limit int) ([]hf.SearchResult, error) {
	return p.client().Search(ctx, query, limit)
}

func (p hfProxy) ModelInfo(ctx context.Context, repo string) (hf.ModelInfo, error) {
	return p.client().ModelInfo(ctx, repo)
}

func (p hfProxy) ListFiles(ctx context.Context, repo, rev string) ([]hf.File, error) {
	return p.client().ListFiles(ctx, repo, rev)
}

// secretsFunc 은 리포트에서 가릴 HF 토큰과 저장·실행 중인 API 키를 돌려준다.
func secretsFunc(keyFile, snapshotDir string, cfg func() config.Config) func() []string {
	return func() []string {
		var out []string
		if t := cfg().HFToken; t != "" {
			out = append(out, t)
		}
		for _, file := range append([]string{keyFile}, snapshotFiles(snapshotDir)...) {
			data, err := os.ReadFile(file)
			if err != nil {
				continue
			}
			for _, line := range strings.Split(string(data), "\n") {
				if t := strings.TrimSpace(line); t != "" && !strings.HasPrefix(t, "#") {
					out = append(out, t)
				}
			}
		}
		return out
	}
}

func snapshotFiles(dir string) []string {
	if dir == "" {
		return nil
	}
	files, _ := filepath.Glob(filepath.Join(dir, "api-keys.start-*.key"))
	return files
}

// chownKeyFile 은 키 파일의 그룹을 llama-server 가 쓰는 lightlix-llm 으로 맞춘다.
func chownKeyFile(path string) error {
	gid, err := groupID(keyGroup)
	if err != nil {
		return err
	}
	if err := os.Chown(path, -1, gid); err != nil {
		return fmt.Errorf("set group of %s to %s: %w", path, keyGroup, err)
	}
	return nil
}

// restoreSSH 는 설정이 SSH 를 켜 둔 상태이면 부팅 뒤 SSH 서버를 다시 시작한다.
// 데몬은 /etc 에 쓸 수 없어 systemctl enable 을 못 하므로, 부팅 때의 시작은 설정의 ssh_enabled 가 맡는다.
// 실패해도 데몬은 계속 뜬다. 방화벽 규칙은 이미 같은 설정으로 적용됐다.
func restoreSSH(ctx context.Context, configPath string, sup *supervisor.Supervisor, acc *accounts.Manager) {
	cfg, err := config.Load(configPath)
	if err != nil || !cfg.SSHEnabled {
		return
	}
	if err := acc.PrepareSSH(ctx); err != nil {
		log.Printf("ssh was enabled in the config, but its host keys and policy could not be prepared: %v", err)
		return
	}
	if err := sup.SetSSHUnit(ctx, true); err != nil {
		log.Printf("ssh was enabled in the config, but the server did not start: %v", err)
	}
}
