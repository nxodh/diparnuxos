package client

import (
	"time"

	"github.com/nxodh/diparnuxos/daemon/internal/gateway"
)

// Status 는 GET /v1/status 의 응답이다.
type Status struct {
	Version string  `json:"version"`
	Mode    string  `json:"mode"`
	GPU     GPU     `json:"gpu"`
	Memory  *Memory `json:"memory"`
	Server  Server  `json:"server"`
}

// GPU 는 상태 파일과 NVML 스냅샷을 합친 GPU 정보다.
type GPU struct {
	// Status 는 상태 파일을 읽었을 때만 있다.
	Status *GPUFile `json:"status"`
	// StatusError 는 상태 파일을 읽지 못한 이유다. 진단이 아직 돌지 않았으면 "GPU check has not run" 이다.
	StatusError   string `json:"status_error,omitempty"`
	OpenModule    string `json:"open_module"`
	SecureBoot    string `json:"secure_boot"`
	DriverVersion string `json:"driver_version"`
	NVML          NVML   `json:"nvml"`
}

// GPUFile 은 GPU 진단 상태 파일의 요약이다.
type GPUFile struct {
	State         string    `json:"state"`
	Message       string    `json:"message"`
	Present       bool      `json:"present"`
	PCICount      int       `json:"pci_count"`
	Count         int       `json:"count"`
	UsableCount   int       `json:"usable_count"`
	UsableIndexes []int     `json:"usable_indexes"`
	SecureBoot    string    `json:"secure_boot"`
	DriverVersion string    `json:"driver_version"`
	GPUs          []GPUItem `json:"gpus"`
}

// GPUItem 은 진단 상태 파일의 GPU 한 장이다.
type GPUItem struct {
	Index      int    `json:"index"`
	Name       string `json:"name"`
	ComputeCap string `json:"compute_cap"`
	Supported  bool   `json:"supported"`
	Reason     string `json:"reason"`
}

// NVML 은 NVML 스냅샷의 요약이다.
type NVML struct {
	Available         bool     `json:"available"`
	Reason            string   `json:"reason,omitempty"`
	NVMLVersion       string   `json:"nvml_version,omitempty"`
	CUDADriverVersion string   `json:"cuda_driver_version,omitempty"`
	Devices           []Device `json:"devices"`
}

// Device 는 NVML 이 본 GPU 한 장이다. 읽지 못한 온도, 전력, 사용률은 -1 이다.
type Device struct {
	Index          int    `json:"index"`
	Name           string `json:"name"`
	Arch           string `json:"arch"`
	ComputeCap     string `json:"compute_cap"`
	MemTotalMiB    int64  `json:"memory_total_mib"`
	MemFreeMiB     int64  `json:"memory_free_mib"`
	MemUsedMiB     int64  `json:"memory_used_mib"`
	TemperatureC   int    `json:"temperature_c"`
	PowerMilliwatt int    `json:"power_mw"`
	UtilGPUPercent int    `json:"util_gpu_percent"`
	UtilMemPercent int    `json:"util_mem_percent"`
}

// Memory 는 시스템 메모리다. 읽지 못하면 Status.Memory 가 null 이다.
type Memory struct {
	TotalMiB     int64 `json:"total_mib"`
	AvailableMiB int64 `json:"available_mib"`
}

// Server 는 llama-server 의 상태와 그 계획이다.
type Server struct {
	State            string     `json:"state"`
	ModelID          string     `json:"model_id"`
	Host             string     `json:"host"`
	Port             int        `json:"port"`
	LANExposed       bool       `json:"lan_exposed"`
	PID              int        `json:"pid"`
	UnitUnverifiable bool       `json:"unit_unverifiable,omitempty"`
	Since            time.Time  `json:"since"`
	Offload          Offload    `json:"offload"`
	Warnings         []string   `json:"warnings"`
	LastError        *ErrorInfo `json:"last_error,omitempty"`
	Plan             *Plan      `json:"plan"`
}

// Offload 는 GPU 오프로드 검증 결과다.
type Offload struct {
	Requested string `json:"requested"`
	Offloaded int    `json:"offloaded"`
	Total     int    `json:"total"`
	Verified  bool   `json:"verified"`
	Note      string `json:"note,omitempty"`
}

// ErrorInfo 는 서버 상태에 남은 마지막 오류다.
type ErrorInfo struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Plan 은 llama-server 실행 계획이다.
type Plan struct {
	NGL            string   `json:"ngl"`
	Ctx            int      `json:"ctx"`
	CacheTypeK     string   `json:"cache_type_k"`
	CacheTypeV     string   `json:"cache_type_v"`
	FlashAttn      bool     `json:"flash_attn"`
	Batch          int      `json:"batch"`
	Parallel       int      `json:"parallel"`
	SlotCtx        int      `json:"slot_ctx"`
	CacheReuse     int      `json:"cache_reuse"`
	CacheRAMMiB    int      `json:"cache_ram_mib"`
	TensorSplit    string   `json:"tensor_split,omitempty"`
	VisibleDevices []int    `json:"visible_devices"`
	LoadMode       string   `json:"load_mode"`
	Fit            bool     `json:"fit"`
	CPUOnly        bool     `json:"cpu_only"`
	EstVRAMMiB     int64    `json:"est_vram_mib"`
	EstRAMMiB      int64    `json:"est_ram_mib"`
	Rationale      []string `json:"rationale"`
	Warnings       []string `json:"warnings"`
	Advice         []string `json:"advice"`
}

// PlanResult 는 GET /v1/server/plan 의 응답이다.
type PlanResult struct {
	ModelID string    `json:"model_id"`
	Plan    Plan      `json:"plan"`
	Input   PlanInput `json:"input"`
	GPU     PlanGPU   `json:"gpu"`
}

// PlanInput 은 계획에 쓴 입력의 요약이다.
type PlanInput struct {
	ModelBytes  int64     `json:"model_bytes"`
	Layers      int       `json:"layers"`
	CtxTrain    int       `json:"ctx_train"`
	Embedding   int       `json:"embedding"`
	GPUs        []GPUFree `json:"gpus"`
	RAMAvailMiB int64     `json:"ram_avail_mib"`
	RAMTotalMiB int64     `json:"ram_total_mib"`
	DesktopMode bool      `json:"desktop_mode"`
}

// GPUFree 는 계획에 쓴 GPU 한 장의 여유 메모리다.
type GPUFree struct {
	Index    int   `json:"index"`
	FreeMiB  int64 `json:"free_mib"`
	TotalMiB int64 `json:"total_mib"`
}

// PlanGPU 는 계획에 쓴 GPU 상태 요약이다.
type PlanGPU struct {
	StatusChecked bool   `json:"status_checked"`
	State         string `json:"state"`
	Message       string `json:"message"`
	UsableIndexes []int  `json:"usable_indexes"`
	NVMLAvailable bool   `json:"nvml_available"`
	NVMLReason    string `json:"nvml_reason,omitempty"`
}

// Overrides 는 계획의 수동 재정의다. nil 인 항목은 자동 계획을 쓴다.
// 설정 파일과 요청 본문, 쿼리가 같은 모양을 쓴다.
type Overrides struct {
	NGL       *string `json:"ngl,omitempty"`
	Ctx       *int    `json:"ctx,omitempty"`
	CacheType *string `json:"cache_type,omitempty"`
	Batch     *int    `json:"batch,omitempty"`
	LoadMode  *string `json:"load_mode,omitempty"`
	Parallel  *int    `json:"parallel,omitempty"`
	Profile   *string `json:"profile,omitempty"`
}

// StartRequest 는 POST /v1/server/start 의 본문이다.
type StartRequest struct {
	Model     string    `json:"model"`
	Overrides Overrides `json:"overrides"`
	Wait      bool      `json:"wait"`
}

// ModelList 는 GET /v1/models 의 응답이다.
type ModelList struct {
	Default string  `json:"default"`
	Models  []Model `json:"models"`
}

// Model 은 설치된 모델 한 개다.
type Model struct {
	ID              string `json:"id"`
	SizeBytes       int64  `json:"size_bytes"`
	Repo            string `json:"repo,omitempty"`
	Revision        string `json:"revision,omitempty"`
	SHA256          string `json:"sha256,omitempty"`
	License         string `json:"license,omitempty"`
	Architecture    string `json:"architecture,omitempty"`
	Name            string `json:"name,omitempty"`
	FileType        string `json:"file_type,omitempty"`
	BlockCount      uint64 `json:"block_count"`
	ContextLength   uint64 `json:"context_length"`
	EmbeddingLength uint64 `json:"embedding_length"`
	Default         bool   `json:"default"`
	Incomplete      bool   `json:"incomplete"`
}

// ModelDeleted 는 DELETE /v1/models/{id} 의 응답이다.
type ModelDeleted struct {
	ID      string `json:"id"`
	Deleted bool   `json:"deleted"`
}

// DefaultModel 은 PUT /v1/models/default 의 응답이다.
type DefaultModel struct {
	Default string `json:"default"`
}

// PullRequest 는 POST /v1/models/pull 의 본문이다.
type PullRequest struct {
	Repo     string `json:"repo"`
	File     string `json:"file"`
	Revision string `json:"revision"`
}

// Job 은 내려받기 작업 하나다. GET /v1/jobs/{id} 와 POST /v1/models/pull 의 응답이다.
type Job struct {
	ID         string    `json:"id"`
	State      string    `json:"state"`
	Repo       string    `json:"repo"`
	File       string    `json:"file"`
	Revision   string    `json:"revision"`
	BytesDone  int64     `json:"bytes_done"`
	BytesTotal int64     `json:"bytes_total"`
	ModelID    string    `json:"model_id,omitempty"`
	License    string    `json:"license,omitempty"`
	ErrorCode  string    `json:"error_code,omitempty"`
	Error      string    `json:"error,omitempty"`
	Started    time.Time `json:"started"`
	Finished   time.Time `json:"finished"`
}

// JobCanceled 는 DELETE /v1/jobs/{id} 의 응답이다.
type JobCanceled struct {
	ID       string `json:"id"`
	Canceled bool   `json:"canceled"`
}

// Remote 는 GET /v1/models/remote 의 응답이다. files 는 GGUF 파일만 담는다.
type Remote struct {
	Repo     string       `json:"repo"`
	Revision string       `json:"revision"`
	License  string       `json:"license"`
	Gated    bool         `json:"gated"`
	GateMode string       `json:"gate_mode"`
	Files    []RemoteFile `json:"files"`
}

// RemoteFile 은 저장소의 GGUF 파일 한 개다.
type RemoteFile struct {
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256,omitempty"`
	LFS    bool   `json:"lfs"`
}

// SearchResults 는 GET /v1/models/search 의 응답이다.
type SearchResults struct {
	Results []SearchHit `json:"results"`
}

// SearchHit 은 검색 결과 한 줄이다.
type SearchHit struct {
	ID            string `json:"id"`
	Downloads     uint64 `json:"downloads"`
	Gated         bool   `json:"gated"`
	License       string `json:"license,omitempty"`
	Architecture  string `json:"architecture,omitempty"`
	ContextLength uint64 `json:"context_length"`
}

// ServerLogs 는 GET /v1/server/logs 의 응답이다.
type ServerLogs struct {
	Lines int    `json:"lines"`
	Log   string `json:"log"`
}

// StateResult 는 POST /v1/server/stop 의 응답이다.
type StateResult struct {
	State string `json:"state"`
}

// ChatMessage 는 대화 한 줄이다.
type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

// ChatRequest 는 POST /v1/server/chat 의 본문이다.
type ChatRequest struct {
	Messages  []ChatMessage `json:"messages"`
	MaxTokens int           `json:"max_tokens"`
}

// ChatReply 는 POST /v1/server/chat 의 응답이다.
type ChatReply struct {
	Content         string  `json:"content"`
	TokensPredicted int     `json:"tokens_predicted"`
	TokensPerSecond float64 `json:"tokens_per_second"`
}

// Config 는 데몬 설정이다. GET /v1/config 의 응답이고, hf_token 은 가려져 있다.
type Config struct {
	Port        int       `json:"port"`
	LANExposed  bool      `json:"lan_exposed"`
	Autostart   bool      `json:"autostart"`
	SSHEnabled  bool      `json:"ssh_enabled"`
	ModelsDir   string    `json:"models_dir"`
	HFToken     string    `json:"hf_token"`
	Gateway     bool      `json:"gateway"`
	AutoRestart bool      `json:"auto_restart"`
	LocalNoKey  bool      `json:"local_no_key"`
	Overrides   Overrides `json:"overrides"`
}

// ConfigPatch 는 PUT /v1/config 의 부분 갱신 본문이다. nil 인 필드는 그대로 둔다.
type ConfigPatch struct {
	Port        *int       `json:"port,omitempty"`
	LANExposed  *bool      `json:"lan_exposed,omitempty"`
	Autostart   *bool      `json:"autostart,omitempty"`
	ModelsDir   *string    `json:"models_dir,omitempty"`
	HFToken     *string    `json:"hf_token,omitempty"`
	Gateway     *bool      `json:"gateway,omitempty"`
	AutoRestart *bool      `json:"auto_restart,omitempty"`
	LocalNoKey  *bool      `json:"local_no_key,omitempty"`
	Overrides   *Overrides `json:"overrides,omitempty"`
}

// ConfigUpdate 는 PUT /v1/config 의 응답이다.
type ConfigUpdate struct {
	Config          Config `json:"config"`
	RestartRequired bool   `json:"restart_required"`
}

// APIKey 는 키 목록의 항목이다. 평문 키는 없다.
type APIKey struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Created string `json:"created"`
	Prefix  string `json:"prefix"`
}

// APIKeyList 는 GET /v1/apikeys 의 응답이다.
type APIKeyList struct {
	Keys []APIKey `json:"keys"`
}

// APIKeyCreated 는 POST /v1/apikeys 의 응답이다. Key 는 이 응답에서만 나온다.
type APIKeyCreated struct {
	ID              string `json:"id"`
	Label           string `json:"label"`
	Prefix          string `json:"prefix"`
	Created         string `json:"created"`
	Key             string `json:"key"`
	Note            string `json:"note"`
	RestartRequired bool   `json:"restart_required"`
}

// APIKeyDeleted 는 DELETE /v1/apikeys/{id} 의 응답이다.
type APIKeyDeleted struct {
	ID              string `json:"id"`
	Deleted         bool   `json:"deleted"`
	RestartRequired bool   `json:"restart_required"`
}

// ModeResult 는 GET 이나 POST /v1/mode 의 응답이다.
type ModeResult struct {
	Mode string `json:"mode"`
}

// GPUCheckResult 는 POST /v1/gpu/check 의 응답이다.
type GPUCheckResult struct {
	ExitCode int     `json:"exit_code"`
	Status   GPUFile `json:"status"`
}

// User 는 Diparnux 로 만든 로그인 계정이다.
type User struct {
	Name    string `json:"name"`
	Admin   bool   `json:"admin"`
	Created string `json:"created"`
}

// UserList 는 GET /v1/users 의 응답이다.
type UserList struct {
	Users []User `json:"users"`
}

// UserCreateRequest 는 POST /v1/users 의 본문이다. 응답에는 비밀번호가 없다.
type UserCreateRequest struct {
	Name     string `json:"name"`
	Password string `json:"password"`
	Admin    bool   `json:"admin"`
}

// UserDeleted 는 DELETE /v1/users/{name} 의 응답이다.
type UserDeleted struct {
	Name     string `json:"name"`
	Deleted  bool   `json:"deleted"`
	HomeKept bool   `json:"home_kept"`
}

// SSH 는 GET 이나 PUT /v1/system/ssh 의 응답이다.
type SSH struct {
	Enabled      bool     `json:"enabled"`
	Installed    bool     `json:"installed"`
	Active       bool     `json:"active"`
	PasswordAuth string   `json:"password_auth"`
	LoginUsers   []string `json:"login_users"`
	Port         int      `json:"port"`
	Warning      string   `json:"warning,omitempty"`
}

// ConnectionAddress 는 다른 컴퓨터가 접속할 때 쓸 주소 하나다.
type ConnectionAddress struct {
	Interface string `json:"interface"`
	Address   string `json:"address"`
	Family    string `json:"family"`
	URL       string `json:"url"`
}

// Connection 은 GET /v1/connection 의 응답이다. 평문 API 키는 없다.
type Connection struct {
	ServerState string   `json:"server_state"`
	ModelID     string   `json:"model_id"`
	Models      []string `json:"models"`
	PublicPort  int      `json:"public_port"`
	LANExposed  bool     `json:"lan_exposed"`
	Gateway     bool     `json:"gateway"`
	KeyRequired bool     `json:"key_required"`
	KeyCount    int      `json:"key_count"`
	// LocalNoKey 는 이 컴퓨터의 요청이 키 없이 통과하는지, LocalKeyRequired 는 이 컴퓨터에서도 키가 필요한지다.
	LocalNoKey       bool `json:"local_no_key"`
	LocalKeyRequired bool `json:"local_key_required"`
	// SlotCtx 와 Parallel 은 실행 중인 서버의 계획 값이다. 멈춰 있으면 0 이다.
	SlotCtx   int                 `json:"slot_ctx"`
	Parallel  int                 `json:"parallel"`
	Hostname  string              `json:"hostname"`
	LocalURL  string              `json:"local_url"`
	Addresses []ConnectionAddress `json:"addresses"`
	MDNSURL   string              `json:"mdns_url"`
	Warnings  []string            `json:"warnings"`
	Examples  map[string]string   `json:"examples"`
}

// ToolCheck 는 POST /v1/server/toolcheck 의 응답이다.
type ToolCheck struct {
	OK           bool   `json:"ok"`
	Code         string `json:"code"`
	Message      string `json:"message"`
	FinishReason string `json:"finish_reason"`
	LatencyMS    int64  `json:"latency_ms"`
}

// ServerStats 는 GET /v1/server/stats 의 응답이다.
type ServerStats struct {
	Running       bool   `json:"running"`
	State         string `json:"state"`
	ModelID       string `json:"model_id"`
	UptimeSeconds int64  `json:"uptime_seconds"`
	Parallel      int    `json:"parallel"`
	SlotCtx       int    `json:"slot_ctx"`
	Slots         struct {
		Available bool   `json:"available"`
		Reason    string `json:"reason"`
		Total     int    `json:"total"`
		Busy      int    `json:"busy"`
	} `json:"slots"`
	Metrics struct {
		Available          bool    `json:"available"`
		Reason             string  `json:"reason"`
		PromptTokensTotal  float64 `json:"prompt_tokens_total"`
		GeneratedTotal     float64 `json:"generated_tokens_total"`
		PromptTokensPerSec float64 `json:"prompt_tokens_per_second"`
		GenTokensPerSec    float64 `json:"generated_tokens_per_second"`
		RequestsProcessing float64 `json:"requests_processing"`
		RequestsDeferred   float64 `json:"requests_deferred"`
		KVCacheUsageRatio  float64 `json:"kv_cache_usage_ratio"`
	} `json:"metrics"`
	Gateway *struct {
		Requests     int64 `json:"requests"`
		Errors       int64 `json:"errors"`
		Unauthorized int64 `json:"unauthorized"`
		InFlight     int64 `json:"in_flight"`
	} `json:"gateway"`
}

// Gateway 는 GET /v1/gateway 의 응답이다.
type Gateway struct {
	Enabled    bool                 `json:"enabled"`
	PublicPort int                  `json:"public_port"`
	LANExposed bool                 `json:"lan_exposed"`
	State      gateway.ManagerState `json:"state"`
	Stats      gateway.Snapshot     `json:"stats"`
	Note       string               `json:"note"`
}

// ImportResult 는 PUT /v1/models/import 의 응답이다.
type ImportResult struct {
	ModelID   string `json:"model_id"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
	Default   bool   `json:"default"`
}
