package jobs

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/nxodh/operetaos/daemon/internal/models"
	"github.com/nxodh/operetaos/daemon/internal/platform"
)

const testToken = "hf_test_secret_token_value"

// content 는 tag 를 n 번 반복한 테스트 내용을 돌려준다. tag 가 다르면 SHA-256 도 다르다.
func content(tag string, n int) []byte {
	return bytes.Repeat([]byte(tag), n)
}

// minimalGGUF 는 캐시 무결성 테스트에 쓸 최소 GGUF v3 헤더를 만든다.
func minimalGGUF(arch string) []byte {
	var b bytes.Buffer
	_ = binary.Write(&b, binary.LittleEndian, []byte("GGUF"))
	_ = binary.Write(&b, binary.LittleEndian, uint32(3))
	_ = binary.Write(&b, binary.LittleEndian, uint64(0))
	_ = binary.Write(&b, binary.LittleEndian, uint64(2))
	writeKey := func(key string) {
		_ = binary.Write(&b, binary.LittleEndian, uint64(len(key)))
		_, _ = b.WriteString(key)
	}
	writeString := func(value string) {
		_ = binary.Write(&b, binary.LittleEndian, uint64(len(value)))
		_, _ = b.WriteString(value)
	}
	writeKey("general.architecture")
	_ = binary.Write(&b, binary.LittleEndian, uint32(8))
	writeString(arch)
	writeKey(arch + ".block_count")
	_ = binary.Write(&b, binary.LittleEndian, uint32(4))
	_ = binary.Write(&b, binary.LittleEndian, uint32(1))
	return b.Bytes()
}

// TestPullInstallsModelAndRecordsSidecar 는 내려받기가 성공하면 파일, 사이드카, 기본 모델, 진행률이 맞는지 확인한다.
// 리비전은 조회한 커밋 SHA 로 고정되어야 한다.
func TestPullInstallsModelAndRecordsSidecar(t *testing.T) {
	data := content("g", 5000)
	h := newHub(t, "ggml-org/tiny-GGUF", hubFile{path: "tiny-Q4.gguf", data: data})
	m := newTestManager(t, h, "")

	started := mustPull(t, m, platform.PullRequest{Repo: h.repo})
	if started.ID == "" || started.Repo != h.repo || started.File != "tiny-Q4.gguf" {
		t.Fatalf("started job = %+v", started)
	}
	done := mustWait(t, m, started.ID)
	if done.State != platform.JobDone || done.ErrorCode != "" || done.Error != "" {
		t.Fatalf("final job = %+v", done)
	}
	if done.ModelID != "tiny-Q4.gguf" || done.License != "apache-2.0" || done.Revision != h.sha {
		t.Fatalf("job metadata = %+v", done)
	}
	if done.BytesDone != int64(len(data)) || done.BytesTotal != int64(len(data)) {
		t.Fatalf("progress = %d of %d", done.BytesDone, done.BytesTotal)
	}
	got, err := os.ReadFile(filepath.Join(m.store.Dir, "tiny-Q4.gguf"))
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("installed file: err=%v equal=%v", err, bytes.Equal(got, data))
	}
	if _, err := os.Stat(filepath.Join(m.store.Dir, "tiny-Q4.gguf.part")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("partial file left behind: %v", err)
	}
	if n := h.count("/" + h.repo + "/resolve/" + h.sha + "/"); n != 1 {
		t.Fatalf("resolve requests pinned to the commit = %d, want 1", n)
	}

	list, err := m.store.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("store list = %+v, %v", list, err)
	}
	mod := list[0]
	if mod.Repo != h.repo || mod.Revision != h.sha || mod.SHA256 != sha256Hex(data) || mod.License != "apache-2.0" {
		t.Fatalf("sidecar fields = %+v", mod)
	}
	if !mod.Default {
		t.Fatal("first model is not the default")
	}
}

// TestPullAmbiguousFileListsCandidates 는 .gguf 가 둘 이상이고 File 이 없으면 ambiguous_file 을 돌려주는지 확인한다.
func TestPullAmbiguousFileListsCandidates(t *testing.T) {
	h := newHub(t, "a/b",
		hubFile{path: "a.gguf", data: content("a", 10)},
		hubFile{path: "b.gguf", data: content("b", 10)},
	)
	m := newTestManager(t, h, "")

	_, err := m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo})
	expectCode(t, err, CodeAmbiguousFile)
	var pe *PullError
	if !errors.As(err, &pe) || strings.Join(pe.Candidates, ",") != "a.gguf,b.gguf" {
		t.Fatalf("candidates = %+v", pe)
	}
	if jobCount(m) != 0 {
		t.Fatal("a job was created for an ambiguous request")
	}
	if n := h.count("/" + h.repo + "/resolve/"); n != 0 {
		t.Fatalf("resolve requests sent = %d, want 0", n)
	}
}

// TestPullExplicitFileInSubdirectory 는 File 로 고른 하위 디렉터리의 파일을 basename 으로 저장하는지 확인한다.
func TestPullExplicitFileInSubdirectory(t *testing.T) {
	h := newHub(t, "a/b",
		hubFile{path: "a.gguf", data: content("a", 10)},
		hubFile{path: "sub/b.gguf", data: content("b", 12)},
	)
	m := newTestManager(t, h, "")

	j := mustPull(t, m, platform.PullRequest{Repo: h.repo, File: "sub/b.gguf"})
	done := mustWait(t, m, j.ID)
	if done.State != platform.JobDone || done.ModelID != "b.gguf" {
		t.Fatalf("job = %+v", done)
	}
	if _, err := os.Stat(filepath.Join(m.store.Dir, "b.gguf")); err != nil {
		t.Fatalf("installed file missing: %v", err)
	}
}

// TestPullRejectsBadRequests 는 입력 검증과 파일 선택 오류가 작업 없이 거부되는지 확인한다.
func TestPullRejectsBadRequests(t *testing.T) {
	h := newHub(t, "a/b",
		hubFile{path: "m.gguf", data: content("m", 10)},
		hubFile{path: "nodigest.gguf", data: content("n", 10), noLFS: true},
		hubFile{path: "readme.md", data: content("r", 10)},
	)
	m := newTestManager(t, h, "")
	ctx := t.Context()

	_, err := m.StartPull(ctx, platform.PullRequest{Repo: ""})
	expectCode(t, err, CodeInvalidInput)
	_, err = m.StartPull(ctx, platform.PullRequest{Repo: h.repo, File: "readme.md"})
	expectCode(t, err, CodeInvalidInput)
	_, err = m.StartPull(ctx, platform.PullRequest{Repo: h.repo, File: "nodigest.gguf"})
	expectCode(t, err, CodeInvalidInput)
	_, err = m.StartPull(ctx, platform.PullRequest{Repo: h.repo, File: "missing.gguf"})
	expectCode(t, err, CodeNotFound)
	_, err = m.StartPull(ctx, platform.PullRequest{Repo: h.repo, File: "../m.gguf"})
	expectCode(t, err, CodeNotFound)
	if jobCount(m) != 0 {
		t.Fatalf("jobs created for rejected requests: %d", jobCount(m))
	}
}

// TestPullGatedWithoutTokenSendsNoFileRequest 는 gated 저장소를 토큰 없이 요청하면 파일 목록도 받지 않고 거부하는지 확인한다.
func TestPullGatedWithoutTokenSendsNoFileRequest(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	h.gated = true
	m := newTestManager(t, h, "")

	_, err := m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo})
	expectCode(t, err, CodeGated)
	if n := h.count("/api/models/" + h.repo + "/tree/"); n != 0 {
		t.Fatalf("tree requests for a gated repository without a token = %d", n)
	}
}

// TestPullGatedWithTokenProceeds 는 토큰이 있으면 gated 저장소도 요청을 진행하는지 확인한다.
// 권한이 없는 토큰은 허브가 내려받기 단계에서 거부하며, 이 테스트 서버는 권한을 확인하지 않는다.
func TestPullGatedWithTokenProceeds(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	h.gated = true
	m := newTestManager(t, h, testToken)

	done := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
	if done.State != platform.JobDone {
		t.Fatalf("job = %+v", done)
	}
}

// TestPullUnknownRepositoryIsUnauthorized 는 없는 저장소가 X-Error-Code 없는 401 로 답할 때 unauthorized 가 되는지 확인한다.
func TestPullUnknownRepositoryIsUnauthorized(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	m := newTestManager(t, h, "")

	_, err := m.StartPull(t.Context(), platform.PullRequest{Repo: "nobody/missing"})
	expectCode(t, err, CodeUnauthorized)
}

// TestDownloadEntryNotFoundIsNotFound 는 목록에는 있지만 내려받을 때 404 가 나면 job 이 not_found 로 실패하는지 확인한다.
func TestDownloadEntryNotFoundIsNotFound(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	h.onResolve = func(w http.ResponseWriter, r *http.Request, file string) bool {
		w.Header().Set("X-Error-Code", "EntryNotFound")
		w.WriteHeader(http.StatusNotFound)
		return true
	}
	m := newTestManager(t, h, "")

	done := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
	if done.State != platform.JobFailed || done.ErrorCode != CodeNotFound {
		t.Fatalf("job = %+v", done)
	}
	if _, err := os.Stat(filepath.Join(m.store.Dir, "m.gguf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("model file exists after a failed download")
	}
}

// TestDownloadChecksumMismatchRemovesPart 는 SHA-256 이 다르면 checksum_mismatch 로 실패하고 .part 를 지우는지 확인한다.
func TestDownloadChecksumMismatchRemovesPart(t *testing.T) {
	data := content("x", 2000)
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: data, digest: sha256Hex(content("y", 2000))})
	m := newTestManager(t, h, "")

	done := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
	if done.State != platform.JobFailed || done.ErrorCode != CodeChecksumMismatch {
		t.Fatalf("job = %+v", done)
	}
	for _, name := range []string{"m.gguf", "m.gguf.part"} {
		if _, err := os.Stat(filepath.Join(m.store.Dir, name)); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("%s exists after a checksum mismatch: %v", name, err)
		}
	}
}

// TestNameConflictWithDifferentModel 는 같은 이름의 다른 모델이 있으면 name_conflict 가 되는지 확인한다.
func TestNameConflictWithDifferentModel(t *testing.T) {
	local := minimalGGUF("gemma")
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: minimalGGUF("llama")})
	m := newTestManager(t, h, "")
	if err := os.WriteFile(filepath.Join(m.store.Dir, "m.gguf"), local, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := m.store.WriteSidecar("m.gguf", models.Sidecar{
		Repo: h.repo, Revision: h.sha, SHA256: sha256Hex(local),
	}); err != nil {
		t.Fatal(err)
	}

	_, err := m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo})
	expectCode(t, err, CodeNameConflict)
	if n := h.count("/" + h.repo + "/resolve/"); n != 0 {
		t.Fatalf("resolve requests sent on a name conflict = %d", n)
	}
}

// TestIncompleteUnrecordedModelIsPreserved 는 사이드카 없는 같은 이름 파일을 불완전 오류로 거부하고 보존하는지 확인한다.
func TestIncompleteUnrecordedModelIsPreserved(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	m := newTestManager(t, h, "")
	if err := os.WriteFile(filepath.Join(m.store.Dir, "m.gguf"), content("m", 10), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo})
	expectCode(t, err, CodeCorruptModel)
}

// TestInstalledSameDigestIsDoneWithoutDownload 는 실제 다운로드된 GGUF 캐시를 해시 검증 후 재사용하는지 확인한다.
func TestInstalledSameDigestIsDoneWithoutDownload(t *testing.T) {
	data := minimalGGUF("llama")
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: data})
	m := newTestManager(t, h, "")
	first := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
	if first.State != platform.JobDone {
		t.Fatalf("initial download job = %+v", first)
	}
	resolveCount := h.count("/" + h.repo + "/resolve/")

	j, err := m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo})
	if err != nil {
		t.Fatalf("StartPull cached model: %v", err)
	}
	if j.State != platform.JobDone || j.ModelID != "m.gguf" {
		t.Fatalf("job = %+v, want done with model m.gguf", j)
	}
	if n := h.count("/" + h.repo + "/resolve/"); n != resolveCount || n != 1 {
		t.Fatalf("resolve requests after cache reuse = %d, initial count %d", n, resolveCount)
	}
}

// TestCorruptCachedModelsFailClosedPreservingFiles 는 손상된 캐시 파일을 덮어쓰거나 지우지 않는지 확인한다.
func TestCorruptCachedModelsFailClosedPreservingFiles(t *testing.T) {
	tests := []struct {
		name     string
		mutate   func(t *testing.T, m *Manager, data []byte)
		wantCode string
	}{
		{
			name: "short file",
			mutate: func(t *testing.T, m *Manager, data []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(m.store.Dir, "m.gguf"), data[:len(data)-1], 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: CodeSizeMismatch,
		},
		{
			name: "same size corruption with matching sidecar",
			mutate: func(t *testing.T, m *Manager, data []byte) {
				t.Helper()
				corrupt := append([]byte(nil), data...)
				corrupt[len(corrupt)-1] ^= 0xff
				if err := os.WriteFile(filepath.Join(m.store.Dir, "m.gguf"), corrupt, 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: CodeChecksumMismatch,
		},
		{
			name: "missing sidecar",
			mutate: func(t *testing.T, m *Manager, _ []byte) {
				t.Helper()
				if err := os.Remove(filepath.Join(m.store.Dir, "m.gguf.json")); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: CodeCorruptModel,
		},
		{
			name: "incomplete sidecar metadata",
			mutate: func(t *testing.T, m *Manager, data []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(m.store.Dir, "m.gguf.json"), []byte(`{"sha256":"`+sha256Hex(data)+`"}`), 0o644); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: CodeCorruptModel,
		},
		{
			name: "partial download marker",
			mutate: func(t *testing.T, m *Manager, _ []byte) {
				t.Helper()
				if err := os.WriteFile(filepath.Join(m.store.Dir, "m.gguf.part"), []byte("active partial"), 0o600); err != nil {
					t.Fatal(err)
				}
			},
			wantCode: CodeCorruptModel,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			data := append(minimalGGUF("llama"), bytes.Repeat([]byte{0x41}, 16)...)
			h := newHub(t, "a/b", hubFile{path: "m.gguf", data: data})
			m := newTestManager(t, h, "")
			initial := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
			if initial.State != platform.JobDone {
				t.Fatalf("initial download job = %+v", initial)
			}
			resolveCount := h.count("/" + h.repo + "/resolve/")
			tc.mutate(t, m, data)

			modelPath := filepath.Join(m.store.Dir, "m.gguf")
			modelBefore, err := os.ReadFile(modelPath)
			if err != nil {
				t.Fatal(err)
			}
			sidecarPath := filepath.Join(m.store.Dir, "m.gguf.json")
			sidecarBefore, sidecarErr := os.ReadFile(sidecarPath)
			partPath := filepath.Join(m.store.Dir, "m.gguf.part")
			partBefore, partErr := os.ReadFile(partPath)
			defaultPath := filepath.Join(m.store.Dir, ".default")
			defaultBefore, err := os.ReadFile(defaultPath)
			if err != nil {
				t.Fatal(err)
			}
			activeFile, openErr := os.Open(modelPath)
			if openErr != nil {
				t.Fatal(openErr)
			}
			defer activeFile.Close()

			_, err = m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo})
			expectCode(t, err, tc.wantCode)
			if n := h.count("/" + h.repo + "/resolve/"); n != resolveCount {
				t.Fatalf("resolve requests after rejecting cache = %d, want unchanged count %d", n, resolveCount)
			}
			modelAfter, err := os.ReadFile(modelPath)
			if err != nil || !bytes.Equal(modelAfter, modelBefore) {
				t.Fatalf("model changed while rejecting cache: err=%v equal=%v", err, bytes.Equal(modelAfter, modelBefore))
			}
			if sidecarErr == nil {
				sidecarAfter, err := os.ReadFile(sidecarPath)
				if err != nil || !bytes.Equal(sidecarAfter, sidecarBefore) {
					t.Fatalf("sidecar changed while rejecting cache: err=%v equal=%v", err, bytes.Equal(sidecarAfter, sidecarBefore))
				}
			} else if !errors.Is(sidecarErr, os.ErrNotExist) {
				t.Fatal(sidecarErr)
			} else if _, err := os.Stat(sidecarPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("missing sidecar was recreated: %v", err)
			}
			if partErr == nil {
				partAfter, err := os.ReadFile(partPath)
				if err != nil || !bytes.Equal(partAfter, partBefore) {
					t.Fatalf("partial marker changed while rejecting cache: err=%v equal=%v", err, bytes.Equal(partAfter, partBefore))
				}
			} else if !errors.Is(partErr, os.ErrNotExist) {
				t.Fatal(partErr)
			} else if _, err := os.Stat(partPath); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("partial marker was created while rejecting cache: %v", err)
			}
			if _, err := activeFile.Stat(); err != nil {
				t.Fatalf("open active model file was disturbed: %v", err)
			}
			defaultAfter, err := os.ReadFile(defaultPath)
			if err != nil || !bytes.Equal(defaultAfter, defaultBefore) {
				t.Fatalf("default model changed while rejecting cache: err=%v equal=%v", err, bytes.Equal(defaultAfter, defaultBefore))
			}
			if got := jobCount(m); got != 1 {
				t.Fatalf("job count = %d, want only the original completed download", got)
			}
		})
	}
}

// TestCancelDuringCachedModelHashPreventsDone 는 해시 계산 도중 취소되면 캐시 재사용 작업을 완료하지 않는지 확인한다.
func TestCancelDuringCachedModelHashPreventsDone(t *testing.T) {
	data := append(minimalGGUF("llama"), bytes.Repeat([]byte{0x5a}, 256<<10)...)
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: data})
	m := newTestManager(t, h, "")
	initial := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
	if initial.State != platform.JobDone {
		t.Fatalf("initial download job = %+v", initial)
	}
	resolveCount := h.count("/" + h.repo + "/resolve/")
	ctx, cancel := context.WithCancel(t.Context())
	m.cacheHashHook = cancel

	_, err := m.StartPull(ctx, platform.PullRequest{Repo: h.repo})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("StartPull after cancellation during cache hash = %v, want context canceled", err)
	}
	if got := jobCount(m); got != 1 {
		t.Fatalf("job count = %d, want no completed cache-reuse job", got)
	}
	if n := h.count("/" + h.repo + "/resolve/"); n != resolveCount {
		t.Fatalf("resolve requests after canceled cache hash = %d, want unchanged count %d", n, resolveCount)
	}
	if got, err := os.ReadFile(filepath.Join(m.store.Dir, "m.gguf")); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("cache changed during canceled hash: err=%v equal=%v", err, bytes.Equal(got, data))
	}
}

// TestSameTargetWhileDownloadingReturnsSameJob 는 같은 파일을 내려받는 중에 다시 요청하면 같은 작업을 돌려주는지 확인한다.
func TestSameTargetWhileDownloadingReturnsSameJob(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	arrived := make(chan struct{}, 4)
	release := make(chan struct{})
	h.onResolve = func(w http.ResponseWriter, r *http.Request, file string) bool {
		arrived <- struct{}{}
		select {
		case <-release:
			return false
		case <-r.Context().Done():
			return true
		}
	}
	m := newTestManager(t, h, "")

	first := mustPull(t, m, platform.PullRequest{Repo: h.repo})
	<-arrived
	second := mustPull(t, m, platform.PullRequest{Repo: h.repo})
	if second.ID != first.ID {
		t.Fatalf("second request got job %s, want %s", second.ID, first.ID)
	}
	close(release)
	if done := mustWait(t, m, first.ID); done.State != platform.JobDone {
		t.Fatalf("job = %+v", done)
	}
}

// TestQueueRunsOneAtATime 는 두 번째 작업이 첫 번째가 끝날 때까지 큐에 있는지, 취소하면 바로 canceled 가 되는지 확인한다.
func TestQueueRunsOneAtATime(t *testing.T) {
	h := newHub(t, "a/b",
		hubFile{path: "a.gguf", data: content("a", 10)},
		hubFile{path: "b.gguf", data: content("b", 10)},
	)
	arrived := make(chan struct{}, 4)
	release := make(chan struct{})
	h.onResolve = func(w http.ResponseWriter, r *http.Request, file string) bool {
		if file != "a.gguf" {
			return false
		}
		arrived <- struct{}{}
		select {
		case <-release:
			return false
		case <-r.Context().Done():
			return true
		}
	}
	m := newTestManager(t, h, "")

	a := mustPull(t, m, platform.PullRequest{Repo: h.repo, File: "a.gguf"})
	<-arrived
	if got, _ := m.Get(a.ID); got.State != platform.JobRunning {
		t.Fatalf("first job state = %s, want running", got.State)
	}
	b := mustPull(t, m, platform.PullRequest{Repo: h.repo, File: "b.gguf"})
	if b.State != platform.JobQueued {
		t.Fatalf("second job state = %s, want queued", b.State)
	}
	if n := h.count("/" + h.repo + "/resolve/" + h.sha + "/b.gguf"); n != 0 {
		t.Fatalf("second job started while the first was running")
	}
	if err := m.Cancel(b.ID); err != nil {
		t.Fatalf("Cancel queued job: %v", err)
	}
	if got := mustWait(t, m, b.ID); got.State != platform.JobCanceled {
		t.Fatalf("canceled queued job = %+v", got)
	}
	close(release)
	if got := mustWait(t, m, a.ID); got.State != platform.JobDone {
		t.Fatalf("first job = %+v", got)
	}
	if err := m.Cancel(a.ID); !errors.Is(err, ErrJobFinished) {
		t.Fatalf("Cancel finished job error = %v, want ErrJobFinished", err)
	}
	if n := h.count("/" + h.repo + "/resolve/" + h.sha + "/b.gguf"); n != 0 {
		t.Fatalf("canceled job still sent a request")
	}
}

// TestCancelRunningKeepsPartialFile 는 실행 중인 작업을 취소하면 canceled 가 되고 .part 는 남는지 확인한다.
// 진행률 훅으로 1 MiB 를 받은 뒤에 취소하므로 대기는 채널로만 한다.
func TestCancelRunningKeepsPartialFile(t *testing.T) {
	const size = 3 << 20
	data := content("p", size)
	h := newHub(t, "a/b", hubFile{path: "big.gguf", data: data})
	h.onResolve = func(w http.ResponseWriter, r *http.Request, file string) bool {
		w.Header().Set("Content-Length", strconv.Itoa(len(data)))
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data[:2<<20])
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		<-r.Context().Done()
		return true
	}
	m := newTestManager(t, h, "")
	progressed := make(chan struct{}, 1)
	m.progressHook = func(id string, done int64) {
		if done >= 1<<20 {
			select {
			case progressed <- struct{}{}:
			default:
			}
		}
	}

	j := mustPull(t, m, platform.PullRequest{Repo: h.repo})
	<-progressed
	if err := m.Cancel(j.ID); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	got := mustWait(t, m, j.ID)
	if got.State != platform.JobCanceled {
		t.Fatalf("job = %+v, want canceled", got)
	}
	part, err := os.Stat(filepath.Join(m.store.Dir, "big.gguf.part"))
	if err != nil || part.Size() < 1<<20 {
		t.Fatalf("partial file: %v, size %d; want at least 1 MiB", err, sizeOf(part))
	}
	if _, err := os.Stat(filepath.Join(m.store.Dir, "big.gguf")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("model file exists after cancel: %v", err)
	}
}

// sizeOf 는 FileInfo 가 nil 이면 -1 을 돌려준다. 실패 메시지에서만 쓴다.
func sizeOf(fi os.FileInfo) int64 {
	if fi == nil {
		return -1
	}
	return fi.Size()
}

// TestRequestContextDoesNotStopDownload 는 StartPull 의 요청 컨텍스트가 끝나도 내려받기가 계속되는지 확인한다.
func TestRequestContextDoesNotStopDownload(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	arrived := make(chan struct{}, 1)
	release := make(chan struct{})
	h.onResolve = func(w http.ResponseWriter, r *http.Request, file string) bool {
		arrived <- struct{}{}
		select {
		case <-release:
			return false
		case <-r.Context().Done():
			return true
		}
	}
	m := newTestManager(t, h, "")

	ctx, cancel := context.WithCancel(t.Context())
	j, err := m.StartPull(ctx, platform.PullRequest{Repo: h.repo})
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	<-arrived
	close(release)
	if got := mustWait(t, m, j.ID); got.State != platform.JobDone {
		t.Fatalf("job after the request context ended = %+v", got)
	}
}

// TestMaxJobsKeepsRecentFinishedJobs 는 끝난 작업을 MaxJobs 개만 기억하는지 확인한다.
func TestMaxJobsKeepsRecentFinishedJobs(t *testing.T) {
	h := newHub(t, "a/b",
		hubFile{path: "a.gguf", data: content("a", 10)},
		hubFile{path: "b.gguf", data: content("b", 10)},
		hubFile{path: "c.gguf", data: content("c", 10)},
	)
	m := newTestManager(t, h, "", func(o *Options) { o.MaxJobs = 2 })

	var ids []string
	for _, f := range []string{"a.gguf", "b.gguf", "c.gguf"} {
		j := mustPull(t, m, platform.PullRequest{Repo: h.repo, File: f})
		mustWait(t, m, j.ID)
		ids = append(ids, j.ID)
	}
	list := m.List()
	if len(list) != 2 || list[0].ID != ids[1] || list[1].ID != ids[2] {
		t.Fatalf("remembered jobs = %+v, want %v", list, ids[1:])
	}
	if _, ok := m.Get(ids[0]); ok {
		t.Fatal("oldest finished job is still remembered")
	}
}

// TestCloseCancelsRunningDownload 는 Close 가 진행 중인 내려받기를 취소하고 돌아오는지 확인한다.
func TestCloseCancelsRunningDownload(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	arrived := make(chan struct{}, 1)
	h.onResolve = func(w http.ResponseWriter, r *http.Request, file string) bool {
		arrived <- struct{}{}
		<-r.Context().Done()
		return true
	}
	m := newTestManager(t, h, "")

	j := mustPull(t, m, platform.PullRequest{Repo: h.repo})
	<-arrived
	m.Close()
	got, ok := m.Get(j.ID)
	if !ok || got.State != platform.JobCanceled {
		t.Fatalf("job after Close = %+v, %v", got, ok)
	}
	if _, err := m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo}); !errors.Is(err, ErrClosed) {
		t.Fatalf("StartPull after Close error = %v, want ErrClosed", err)
	}
}

// TestInsufficientSpaceFailsBeforeRequest 는 여유 공간이 부족하면 요청 없이 insufficient_space 로 실패하는지 확인한다.
func TestInsufficientSpaceFailsBeforeRequest(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	m := newTestManager(t, h, "", func(o *Options) {
		o.FreeBytes = func(string) (uint64, error) { return 0, nil }
	})

	done := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
	if done.State != platform.JobFailed || done.ErrorCode != CodeInsufficientSpace {
		t.Fatalf("job = %+v", done)
	}
	if n := h.count("/" + h.repo + "/resolve/"); n != 0 {
		t.Fatalf("resolve requests = %d, want 0", n)
	}
}

// TestErrorNeverContainsToken 는 토큰을 실은 요청이 실패해도 오류 설명과 Error 필드에 토큰이 들어가지 않는지 확인한다.
// 토큰은 Authorization 헤더로 허브에 실제로 전달되어야 한다.
func TestErrorNeverContainsToken(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	h.onResolve = func(w http.ResponseWriter, r *http.Request, file string) bool {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("server echoed " + testToken))
		return true
	}
	m := newTestManager(t, h, testToken)

	done := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo}).ID)
	if done.State != platform.JobFailed || done.ErrorCode != CodeNetwork {
		t.Fatalf("job = %+v, want failed with network", done)
	}
	if strings.Contains(done.Error, testToken) {
		t.Fatalf("job error contains the token: %q", done.Error)
	}
	if !h.authSeen("Bearer " + testToken) {
		t.Fatal("token was not sent in the Authorization header")
	}
}

// TestMetadataTimeoutIsNetworkError 는 모델 정보 조회가 시간 제한을 넘으면 network 오류가 되는지 확인한다.
func TestMetadataTimeoutIsNetworkError(t *testing.T) {
	h := newHub(t, "a/b", hubFile{path: "m.gguf", data: content("m", 10)})
	h.blockAPI = make(chan struct{})
	defer close(h.blockAPI)
	m := newTestManager(t, h, "")
	m.metaTimeout = 50 * time.Millisecond

	_, err := m.StartPull(t.Context(), platform.PullRequest{Repo: h.repo})
	expectCode(t, err, CodeNetwork)
	if jobCount(m) != 0 {
		t.Fatal("a job was created after a metadata timeout")
	}
}

// TestDefaultIsKeptWhenAnotherModelIsInstalled 는 기본 모델이 이미 있으면 새 모델이 기본을 바꾸지 않는지 확인한다.
func TestDefaultIsKeptWhenAnotherModelIsInstalled(t *testing.T) {
	h := newHub(t, "a/b",
		hubFile{path: "a.gguf", data: content("a", 10)},
		hubFile{path: "b.gguf", data: content("b", 10)},
	)
	m := newTestManager(t, h, "")
	for _, f := range []string{"a.gguf", "b.gguf"} {
		done := mustWait(t, m, mustPull(t, m, platform.PullRequest{Repo: h.repo, File: f}).ID)
		if done.State != platform.JobDone {
			t.Fatalf("%s: job = %+v", f, done)
		}
	}
	if def, err := m.store.Default(); err != nil || def != "a.gguf" {
		t.Fatalf("default = %q, %v; want a.gguf", def, err)
	}
}

// TestCancelUnknownAndWaitUnknown 는 없는 작업 ID 의 Cancel 과 Wait 를 확인한다.
func TestCancelUnknownAndWaitUnknown(t *testing.T) {
	m := newTestManager(t, newHub(t, "a/b"), "")
	if err := m.Cancel("job-999"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("Cancel unknown = %v, want ErrNotFound", err)
	}
	if _, ok := m.Wait("job-999"); ok {
		t.Fatal("Wait on an unknown ID reported a job")
	}
}
