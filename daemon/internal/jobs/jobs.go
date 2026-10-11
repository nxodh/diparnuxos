// Package jobs 는 Hugging Face 저장소에서 GGUF 모델을 내려받는 작업을 관리한다. platform.Jobs 를 구현한다.
//
// StartPull 은 저장소 정보와 파일 목록을 확인해 작업을 만들고 바로 돌려준다. 실제 내려받기는 백그라운드 고루틴에서
// 한 번에 하나씩 진행한다. 내려받기는 StartPull 의 요청 컨텍스트와 무관하게 Cancel 이나 Close 까지 계속된다.
// 취소되면 .part 파일은 남겨 두어 나중에 이어 받을 수 있게 한다.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/nxodh/lightlixos/daemon/internal/hf"
	"github.com/nxodh/lightlixos/daemon/internal/models"
	"github.com/nxodh/lightlixos/daemon/internal/platform"
	"github.com/nxodh/lightlixos/daemon/internal/sysinfo"
)

// 기본값과 파일 이름 규칙이다.
const (
	// defaultMaxJobs 는 Options.MaxJobs 가 0 이하일 때 기억하는 끝난 작업 수다.
	defaultMaxJobs = 20
	// defaultRevision 은 Revision 이 비어 있을 때 쓰는 브랜치다.
	defaultRevision = "main"
	// defaultMetaTimeout 은 ModelInfo 와 ListFiles 각각의 시간 제한이다.
	defaultMetaTimeout = 30 * time.Second
	// ggufSuffix 는 모델 파일 확장자다.
	ggufSuffix = ".gguf"
	// maxLocalNameLen 은 모델 파일 이름의 최대 바이트 수다. models 패키지의 제한과 같다.
	maxLocalNameLen = 255
)

// Options 는 Manager 를 만드는 설정이다.
type Options struct {
	// HF 는 Hugging Face 클라이언트다. nil 이면 토큰 없는 기본 클라이언트를 쓴다.
	HF *hf.Client
	// Store 는 모델 디렉터리다.
	Store models.Store
	// FreeBytes 는 dir 의 여유 바이트를 돌려준다. nil 이면 sysinfo.DiskFree 를 쓴다.
	FreeBytes func(dir string) (uint64, error)
	// MinFreeMargin 은 내려받은 뒤에도 남겨 둘 여유 바이트다.
	MinFreeMargin uint64
	// Now 는 현재 시각을 돌려준다. nil 이면 time.Now 다.
	Now func() time.Time
	// MaxJobs 는 끝난 작업을 최근 몇 개까지 기억할지다. 0 이하이면 20 이다.
	MaxJobs int
}

// platform.Jobs 를 구현하는지 컴파일 시점에 확인한다.
var _ platform.Jobs = (*Manager)(nil)

// Manager 는 내려받기 작업 목록과 내려받기 고루틴을 관리한다. 모든 메서드는 여러 고루틴에서 불러도 안전하다.
type Manager struct {
	client      *hf.Client
	store       models.Store
	free        func(dir string) (uint64, error)
	margin      uint64
	now         func() time.Time
	maxJobs     int
	metaTimeout time.Duration

	// base 는 모든 작업 컨텍스트의 부모다. Close 가 취소한다.
	base context.Context
	stop context.CancelFunc

	// slot 은 내려받기 슬롯이다. 용량이 1 이므로 한 번에 하나만 내려받는다.
	slot chan struct{}
	// wg 는 작업 고루틴의 끝을 기다리는 데 쓴다.
	wg sync.WaitGroup
	// progressHook 은 테스트가 진행률을 관찰하려고 넣는다. nil 이면 아무 일도 하지 않는다.
	progressHook func(id string, done int64)
	// cacheHashHook 은 테스트가 캐시 해시 도중 취소를 유도하려고 넣는다. nil 이면 아무 일도 하지 않는다.
	cacheHashHook func()

	mu     sync.Mutex
	closed bool
	seq    uint64
	// order 는 만든 순서의 작업 목록이다. 끝난 작업을 오래된 것부터 지울 때 쓴다.
	order []*job
	byID  map[string]*job
}

// job 은 작업 하나의 내부 상태다. view 는 mu 로 보호하고, 나머지 필드는 만들 때 한 번만 쓴다.
type job struct {
	view platform.Job
	// file 은 내려받을 파일이다.
	file hf.File
	// dest 는 최종 파일 경로다.
	dest string
	// ctx 와 cancel 은 작업 고루틴이 쓴다. 끝난 작업이나 바로 끝난 작업에는 없다.
	ctx    context.Context
	cancel context.CancelFunc
	// done 은 작업이 끝나면 닫힌다.
	done chan struct{}
}

// NewManager 는 Manager 를 만든다. 사용이 끝나면 Close 를 부른다.
func NewManager(o Options) *Manager {
	m := &Manager{
		client:      o.HF,
		store:       o.Store,
		free:        o.FreeBytes,
		margin:      o.MinFreeMargin,
		now:         o.Now,
		maxJobs:     o.MaxJobs,
		metaTimeout: defaultMetaTimeout,
		slot:        make(chan struct{}, 1),
		byID:        make(map[string]*job),
	}
	if m.client == nil {
		m.client = &hf.Client{}
	}
	if m.free == nil {
		m.free = sysinfo.DiskFree
	}
	if m.now == nil {
		m.now = time.Now
	}
	if m.maxJobs <= 0 {
		m.maxJobs = defaultMaxJobs
	}
	m.base, m.stop = context.WithCancel(context.Background())
	return m
}

// StartPull 은 저장소의 모델 파일을 내려받는 작업을 만들고 바로 돌려준다.
//
// 먼저 저장소 정보(ModelInfo)와 파일 목록(ListFiles)을 조회한다. 두 조회는 각각 시간 제한을 가진다.
// File 이 있으면 그 파일을, 없으면 .gguf 파일이 정확히 하나일 때만 그 파일을 고른다. 고른 파일에 SHA-256 이 없으면 거부한다.
// 모델 디렉터리에 같은 이름의 완전한 모델이 있으면 실제 파일 크기와 SHA-256 을 검증한 뒤 done 으로 돌려준다.
// 파일이나 메타데이터가 손상된 경우 명시적 오류를 돌려주고 기존 파일은 건드리지 않는다.
// 거부는 *PullError 로 돌려주며 작업은 만들지 않는다. 같은 대상을 이미 내려받는 작업이 있으면 그 작업을 돌려준다.
//
// 내려받기는 리비전을 조회한 커밋 SHA 로 고정해 받으므로, 기록하는 리비전은 실제로 받은 내용과 일치한다.
// 리비전이 "main"이 아니면 그 이름을 그대로 쓰고, 커밋 SHA 는 알 수 없다.
func (m *Manager) StartPull(ctx context.Context, req platform.PullRequest) (platform.Job, error) {
	if m.isClosed() {
		return platform.Job{}, ErrClosed
	}
	repo := req.Repo
	if repo == "" {
		return platform.Job{}, newPullError(CodeInvalidInput, "repository is empty", nil)
	}
	if req.File != "" && !strings.HasSuffix(req.File, ggufSuffix) {
		return platform.Job{}, newPullError(CodeInvalidInput, "file must be a .gguf path in the repository", nil)
	}
	rev := req.Revision
	if rev == "" {
		rev = defaultRevision
	}

	mctx, cancel := context.WithTimeout(ctx, m.metaTimeout)
	info, err := m.client.ModelInfo(mctx, repo)
	cancel()
	if err != nil {
		return platform.Job{}, m.pullError(err)
	}
	if isGated(info.Gated) && m.client.Token == "" {
		return platform.Job{}, newPullError(CodeGated, "repository is gated; set a Hugging Face token that has access", nil)
	}
	pinned := rev
	if rev == defaultRevision && info.SHA != "" {
		pinned = info.SHA
	}

	lctx, cancel := context.WithTimeout(ctx, m.metaTimeout)
	files, err := m.client.ListFiles(lctx, repo, pinned)
	cancel()
	if err != nil {
		return platform.Job{}, m.pullError(err)
	}
	f, err := selectFile(files, req.File)
	if err != nil {
		return platform.Job{}, err
	}
	if f.SHA256 == "" {
		return platform.Job{}, newPullError(CodeInvalidInput, "file has no SHA-256 digest in the repository: "+f.Path, nil)
	}
	name := path.Base(f.Path)
	if err := checkLocalName(name); err != nil {
		return platform.Job{}, err
	}

	present, err := m.localModel(name)
	if err != nil {
		return platform.Job{}, err
	}
	if present != nil {
		if present.Incomplete {
			return platform.Job{}, cachedModelError(name, models.ErrIncomplete)
		}
		if !strings.EqualFold(present.SHA256, f.SHA256) {
			return platform.Job{}, newPullError(CodeNameConflict, "a different model named "+name+" already exists", nil)
		}
		if err := m.store.Verify(ctx, *present, f.Size, f.SHA256, func() {
			if m.cacheHashHook != nil {
				m.cacheHashHook()
			}
		}); err != nil {
			return platform.Job{}, cachedModelError(name, err)
		}
		return m.installedJob(repo, pinned, f, info.License, name)
	}
	if _, err := os.Lstat(filepath.Join(m.store.Dir, name)); err == nil {
		return platform.Job{}, newPullError(CodeNameConflict, "a file named "+name+" already exists in the model directory", nil)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return platform.Job{}, newPullError(CodeInternal, "cannot check the model directory: "+sanitize(err.Error(), ""), err)
	}

	j := m.newJob(repo, pinned, f, info.License, name)
	j.view.State = platform.JobQueued
	return m.insert(j)
}

// cachedModelError 는 로컬 캐시 검증 오류를 명시적인 pull 오류로 바꾼다.
func cachedModelError(name string, err error) error {
	const recovery = "; stop any server using it, remove the model, then pull again"
	switch {
	case errors.Is(err, models.ErrIncomplete):
		return newPullError(CodeCorruptModel, "cached model "+name+" is incomplete or has invalid metadata"+recovery, err)
	case errors.Is(err, models.ErrSizeMismatch):
		return newPullError(CodeSizeMismatch, "cached model "+name+" has a size that does not match the repository"+recovery, err)
	case errors.Is(err, models.ErrChecksumMismatch):
		return newPullError(CodeChecksumMismatch, "cached model "+name+" does not match the repository SHA-256"+recovery, err)
	default:
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return err
		}
		return newPullError(CodeInternal, "cannot verify cached model "+name+": "+sanitize(err.Error(), ""), err)
	}
}

// selectFile 은 파일 목록에서 내려받을 파일을 고른다. want 가 비어 있으면 .gguf 가 정확히 하나여야 한다.
func selectFile(files []hf.File, want string) (hf.File, error) {
	if want != "" {
		for _, f := range files {
			if f.Path == want {
				return f, nil
			}
		}
		return hf.File{}, newPullError(CodeNotFound, "file is not in the repository: "+want, nil)
	}
	var found []hf.File
	for _, f := range files {
		if strings.HasSuffix(f.Path, ggufSuffix) {
			found = append(found, f)
		}
	}
	switch len(found) {
	case 0:
		return hf.File{}, newPullError(CodeNotFound, "repository has no GGUF file", nil)
	case 1:
		return found[0], nil
	}
	paths := make([]string, len(found))
	for i, f := range found {
		paths[i] = f.Path
	}
	pe := newPullError(CodeAmbiguousFile,
		"repository has more than one GGUF file; choose one with file: "+strings.Join(paths, ", "), nil)
	pe.Candidates = paths
	return hf.File{}, pe
}

// localModel 은 모델 디렉터리에서 name 과 같은 ID 의 모델을 찾는다. 없으면 nil 이다.
func (m *Manager) localModel(name string) (*models.Model, error) {
	list, err := m.store.List()
	if err != nil {
		return nil, newPullError(CodeInternal, "cannot read the model directory: "+sanitize(err.Error(), ""), err)
	}
	for i := range list {
		if list[i].ID == name {
			return &list[i], nil
		}
	}
	return nil, nil
}

// installedJob 은 같은 내용의 모델이 이미 있을 때 done 상태의 작업을 만들어 돌려준다. 내려받기는 하지 않는다.
func (m *Manager) installedJob(repo, rev string, f hf.File, license, name string) (platform.Job, error) {
	state, code, msg := platform.JobDone, "", ""
	if err := m.ensureDefault(name); err != nil {
		state, code, msg = platform.JobFailed, CodeInternal, "model is present but the default could not be set: "+sanitize(err.Error(), "")
	}
	j := m.newJob(repo, rev, f, license, name)
	now := m.now()
	j.view.State = state
	j.view.ErrorCode = code
	j.view.Error = msg
	j.view.ModelID = name
	j.view.BytesDone = f.Size
	j.view.Started = now
	j.view.Finished = now
	return m.insert(j)
}

// newJob 은 파일과 목록 정보로 작업을 만든다. ID 는 insert 에서 정한다.
func (m *Manager) newJob(repo, rev string, f hf.File, license, name string) *job {
	return &job{
		view: platform.Job{
			Repo:       repo,
			File:       f.Path,
			Revision:   rev,
			BytesTotal: f.Size,
			License:    license,
		},
		file: f,
		dest: filepath.Join(m.store.Dir, name),
		done: make(chan struct{}),
	}
}

// insert 는 작업을 목록에 넣는다. 같은 대상을 쓰는 진행 중 작업이 있으면 그 작업을 돌려주거나 충돌 오류를 돌려준다.
// 큐에 들어갈 작업이면 내려받기 고루틴을 시작한다. 이미 끝난 작업이면 닫힌 done 으로 표시한다.
func (m *Manager) insert(j *job) (platform.Job, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.closed {
		return platform.Job{}, ErrClosed
	}
	for _, other := range m.order {
		if terminal(other.view.State) || other.dest != j.dest {
			continue
		}
		if other.view.Repo == j.view.Repo && other.file.Path == j.file.Path && other.file.SHA256 == j.file.SHA256 {
			return other.view, nil
		}
		return platform.Job{}, newPullError(CodeNameConflict,
			"another download is already writing "+filepath.Base(j.dest), nil)
	}
	m.seq++
	j.view.ID = fmt.Sprintf("job-%d", m.seq)
	m.byID[j.view.ID] = j
	m.order = append(m.order, j)
	if j.view.State == platform.JobQueued {
		j.ctx, j.cancel = context.WithCancel(m.base)
		m.wg.Add(1)
		go m.run(j)
	} else {
		close(j.done)
		m.trimLocked()
	}
	return j.view, nil
}

// run 은 작업 하나의 고루틴이다. 슬롯을 얻으면 내려받고, 취소되면 바로 끝난다.
func (m *Manager) run(j *job) {
	defer m.wg.Done()
	select {
	case m.slot <- struct{}{}:
	case <-j.ctx.Done():
		m.finish(j, platform.JobCanceled, "", "canceled before the download started", "")
		return
	}
	defer func() { <-m.slot }()
	if !m.markRunning(j) {
		return
	}
	m.download(j)
}

// markRunning 은 큐에 있던 작업을 실행 상태로 바꾼다. 이미 취소되었으면 false 를 돌려준다.
func (m *Manager) markRunning(j *job) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if j.view.State != platform.JobQueued {
		return false
	}
	if j.ctx.Err() != nil {
		m.finishLocked(j, platform.JobCanceled, "", "canceled before the download started", "")
		return false
	}
	j.view.State = platform.JobRunning
	j.view.Started = m.now()
	return true
}

// download 는 파일을 내려받고 검증한 뒤 사이드카와 기본 모델을 기록한다.
func (m *Manager) download(j *job) {
	name := filepath.Base(j.dest)
	err := m.client.Download(j.ctx, hf.DownloadRequest{
		Repo:          j.view.Repo,
		Revision:      j.view.Revision,
		Path:          j.file.Path,
		Dest:          j.dest,
		Size:          j.file.Size,
		SHA256:        j.file.SHA256,
		FreeBytes:     m.free,
		MinFreeMargin: m.margin,
		Progress:      func(done, total int64) { m.progress(j, done, total) },
	})
	if err != nil {
		if j.ctx.Err() != nil {
			m.finish(j, platform.JobCanceled, "", "canceled; the partial file is kept and can be resumed", "")
			return
		}
		code, msg := classify(err, m.client.Token)
		m.finish(j, platform.JobFailed, code, msg, "")
		return
	}
	sc := models.Sidecar{Repo: j.view.Repo, Revision: j.view.Revision, SHA256: j.file.SHA256, License: j.view.License}
	if err := m.store.WriteSidecar(name, sc); err != nil {
		m.finish(j, platform.JobFailed, CodeInternal,
			"model downloaded but its metadata could not be saved: "+sanitize(err.Error(), m.client.Token), name)
		return
	}
	if err := m.ensureDefault(name); err != nil {
		m.finish(j, platform.JobFailed, CodeInternal,
			"model downloaded but the default could not be set: "+sanitize(err.Error(), m.client.Token), name)
		return
	}
	m.finish(j, platform.JobDone, "", "", name)
}

// ensureDefault 는 기본 모델이 없으면 name 을 기본으로 정한다. 기본이 지정되어 있어도 그 모델 파일이 있으면 바꾸지 않는다.
// 기본 파일이 깨졌거나 가리키는 모델이 없으면 name 으로 바꾼다.
func (m *Manager) ensureDefault(name string) error {
	cur, err := m.store.Default()
	if err == nil && cur != "" {
		if _, statErr := os.Stat(filepath.Join(m.store.Dir, cur)); statErr == nil {
			return nil
		}
	}
	return m.store.SetDefault(name)
}

// progress 는 진행률을 작업에 반영한다. 끝난 작업은 바꾸지 않는다.
func (m *Manager) progress(j *job, done, total int64) {
	m.mu.Lock()
	if !terminal(j.view.State) {
		j.view.BytesDone = done
		j.view.BytesTotal = total
	}
	m.mu.Unlock()
	if m.progressHook != nil {
		m.progressHook(j.view.ID, done)
	}
}

// finish 는 작업을 끝난 상태로 바꾼다. 이미 끝났으면 아무것도 하지 않는다.
func (m *Manager) finish(j *job, state platform.JobState, code, msg, modelID string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.finishLocked(j, state, code, msg, modelID)
}

// finishLocked 는 mu 를 쥔 상태에서 작업을 끝낸다. 끝난 작업이 MaxJobs 를 넘으면 오래된 것부터 지운다.
func (m *Manager) finishLocked(j *job, state platform.JobState, code, msg, modelID string) {
	if terminal(j.view.State) {
		return
	}
	j.view.State = state
	j.view.ErrorCode = code
	j.view.Error = msg
	if modelID != "" {
		j.view.ModelID = modelID
	}
	if state == platform.JobDone {
		j.view.BytesDone = j.view.BytesTotal
	}
	j.view.Finished = m.now()
	if j.cancel != nil {
		j.cancel()
	}
	close(j.done)
	m.trimLocked()
}

// trimLocked 은 끝난 작업이 MaxJobs 를 넘으면 가장 오래된 끝난 작업부터 지운다. 진행 중인 작업은 지우지 않는다.
func (m *Manager) trimLocked() {
	finished := 0
	for _, j := range m.order {
		if terminal(j.view.State) {
			finished++
		}
	}
	excess := finished - m.maxJobs
	if excess <= 0 {
		return
	}
	kept := m.order[:0]
	for _, j := range m.order {
		if excess > 0 && terminal(j.view.State) {
			delete(m.byID, j.view.ID)
			excess--
			continue
		}
		kept = append(kept, j)
	}
	m.order = kept
}

// Get 은 ID 가 id 인 작업의 현재 상태를 돌려준다. 없으면 false 다.
func (m *Manager) Get(id string) (platform.Job, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return platform.Job{}, false
	}
	return j.view, true
}

// List 는 만든 순서대로 모든 기억된 작업의 상태를 돌려준다.
func (m *Manager) List() []platform.Job {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]platform.Job, 0, len(m.order))
	for _, j := range m.order {
		out = append(out, j.view)
	}
	return out
}

// Cancel 은 작업 id 의 취소를 요청한다. 큐에 있던 작업은 바로 canceled 가 된다. 실행 중인 작업은 내려받기가
// 멈춘 뒤에 canceled 가 되므로, 최종 상태는 Wait 로 기다려 확인한다. 없는 ID 는 ErrNotFound,
// 이미 끝난 작업은 ErrJobFinished 를 감싼 오류다.
func (m *Manager) Cancel(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	j, ok := m.byID[id]
	if !ok {
		return fmt.Errorf("jobs: cancel %s: %w", id, ErrNotFound)
	}
	if terminal(j.view.State) {
		return fmt.Errorf("jobs: cancel %s: %w", id, ErrJobFinished)
	}
	j.cancel()
	if j.view.State == platform.JobQueued {
		m.finishLocked(j, platform.JobCanceled, "", "canceled before the download started", "")
	}
	return nil
}

// Wait 은 작업 id 가 끝날 때까지 기다리고 끝난 뒤의 상태를 돌려준다. 없는 ID 는 false 다.
// 작업이 끝나지 않으면 영원히 기다리므로, 쓰기 전에 Cancel 이나 Close 로 끝낼 수 있는지 확인한다.
func (m *Manager) Wait(id string) (platform.Job, bool) {
	m.mu.Lock()
	j, ok := m.byID[id]
	m.mu.Unlock()
	if !ok {
		return platform.Job{}, false
	}
	<-j.done
	m.mu.Lock()
	defer m.mu.Unlock()
	return j.view, true
}

// Close 는 진행 중인 내려받기를 모두 취소하고 작업 고루틴이 끝날 때까지 기다린다. 두 번 불러도 된다.
// Close 뒤의 StartPull 은 ErrClosed 를 돌려준다.
func (m *Manager) Close() {
	m.mu.Lock()
	m.closed = true
	m.mu.Unlock()
	m.stop()
	m.wg.Wait()
}

// pullError 는 hf 오류를 PullError 로 바꾼다.
func (m *Manager) pullError(err error) *PullError {
	code, msg := classify(err, m.client.Token)
	return newPullError(code, msg, err)
}

// isClosed 는 Close 가 불렸는지 알려 준다.
func (m *Manager) isClosed() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.closed
}

// isGated 는 저장소가 gated 인지 본다.
func isGated(g hf.GateMode) bool {
	return g == hf.GateAuto || g == hf.GateManual
}

// terminal 은 작업 상태가 끝난 상태인지 본다.
func terminal(s platform.JobState) bool {
	return s == platform.JobDone || s == platform.JobFailed || s == platform.JobCanceled
}
