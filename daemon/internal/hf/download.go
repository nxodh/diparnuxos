package hf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"hash"
	"io"
	"io/fs"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Download transfer settings.
const (
	copyBufferSize = 64 << 10 // Size of the copy buffer.
	progressStep   = 1 << 20  // Progress is reported after every MiB, not after every read.
)

// DownloadRequest describes one file to download.
type DownloadRequest struct {
	// Repo is the repository, such as "ggml-org/gemma-3-1b-it-GGUF".
	Repo string
	// Revision is a branch, tag, or commit SHA. The Hub resolves it.
	Revision string
	// Path is the file path inside the repository.
	Path string
	// Dest is the final local path. The download is written to Dest + ".part" first and renamed to Dest after verification.
	Dest string
	// Size is the expected file size in bytes.
	Size int64
	// SHA256 is the expected SHA-256 in hex. It is required, and a download without it is refused.
	SHA256 string
	// FreeBytes returns the free bytes available to unprivileged writers in dir. It is required.
	FreeBytes func(dir string) (uint64, error)
	// MinFreeMargin is the extra free space, in bytes, that must remain after the download.
	MinFreeMargin uint64
	// Progress is called with the bytes done and the total, at the start, after every MiB, and at the end. It may be nil.
	Progress func(done, total int64)
}

// Download fetches one file to req.Dest, verifying its size and SHA-256.
//
// The request follows the resolve redirect. Go's default client drops the Authorization header when a redirect goes to
// another host, so the token is sent only to the Hub. An existing partial file is resumed with a Range request. If the
// server answers 200 instead of 206, the partial file is discarded and the download starts over. If ctx is cancelled,
// the partial file is kept so that a later call can resume it. A size or checksum mismatch removes the partial file.
// After a successful verification the file is synced and renamed over req.Dest, which replaces any existing file.
func (c *Client) Download(ctx context.Context, req DownloadRequest) error {
	if err := validateDownload(req); err != nil {
		return err
	}
	want := strings.ToLower(req.SHA256)
	part := req.Dest + ".part"

	have, err := partSize(part)
	if err != nil {
		return err
	}
	if have > req.Size {
		if err := removeIfExists(part); err != nil {
			return err
		}
		have = 0
	}

	// Check the space before any request is sent. Space for the partial file that already exists does not count again.
	remaining := uint64(req.Size - have)
	if remaining > math.MaxUint64-req.MinFreeMargin {
		return fmt.Errorf("hf: %w: required space overflows", ErrInsufficientSpace)
	}
	need := remaining + req.MinFreeMargin
	free, err := req.FreeBytes(filepath.Dir(req.Dest))
	if err != nil {
		return fmt.Errorf("hf: check free space: %w", err)
	}
	if free < need {
		return fmt.Errorf("hf: %w: need %d bytes, %d free", ErrInsufficientSpace, need, free)
	}

	if have > 0 && have == req.Size {
		// The whole file is already in the partial file. Only verify it, so no request is sent.
		digest, err := hashFile(part, req.Size)
		if err != nil {
			return err
		}
		return finish(part, req, want, digest)
	}

	fileURL, err := c.url(escapePath(req.Repo)+"/resolve/"+escapePath(req.Revision)+"/"+escapePath(req.Path), "")
	if err != nil {
		return err
	}
	// Identity encoding keeps the byte counts exact. Go's transparent gzip would break the Content-Length and Range checks.
	headers := map[string]string{"Accept-Encoding": "identity"}
	if have > 0 {
		headers["Range"] = fmt.Sprintf("bytes=%d-", have)
	}
	resp, err := c.get(ctx, fileURL, headers)
	if err != nil {
		return interrupted(ctx, req.Path, err)
	}
	defer resp.Body.Close()

	offset, err := checkStart(resp, have, req.Size, part)
	if err != nil {
		if errors.Is(err, ErrSizeMismatch) {
			if rmErr := removeIfExists(part); rmErr != nil {
				return rmErr
			}
		}
		return err
	}

	digest, copied, err := streamBody(ctx, resp, part, offset, req)
	if err != nil {
		return err
	}
	if copied != req.Size {
		if err := removeIfExists(part); err != nil {
			return err
		}
		return fmt.Errorf("hf: %w: expected %d bytes, got %d", ErrSizeMismatch, req.Size, copied)
	}
	return finish(part, req, want, digest)
}

// finish verifies the digest, installs the file, and reports the final progress.
func finish(part string, req DownloadRequest, want, digest string) error {
	if err := verifyAndInstall(part, req.Dest, want, digest); err != nil {
		return err
	}
	if req.Progress != nil {
		req.Progress(req.Size, req.Size)
	}
	return nil
}

// validateDownload checks every field of req before any file or request is touched.
func validateDownload(req DownloadRequest) error {
	if err := validateRepo(req.Repo); err != nil {
		return err
	}
	if err := validateRef(req.Revision); err != nil {
		return err
	}
	if err := validatePath(req.Path); err != nil {
		return err
	}
	if req.Dest == "" || strings.HasSuffix(req.Dest, string(filepath.Separator)) || filepath.Base(req.Dest) == "." {
		return fmt.Errorf("%w: destination path is not a file path", ErrInvalidInput)
	}
	if req.Size < 0 {
		return fmt.Errorf("%w: size is negative", ErrInvalidInput)
	}
	if req.SHA256 == "" {
		return fmt.Errorf("%w: sha256 is required", ErrInvalidInput)
	}
	if !isSHA256(req.SHA256) {
		return fmt.Errorf("%w: sha256 must be 64 hex digits", ErrInvalidInput)
	}
	if req.FreeBytes == nil {
		return fmt.Errorf("%w: FreeBytes is required", ErrInvalidInput)
	}
	return nil
}

// checkStart checks the response status and headers. It returns the byte offset at which the body starts writing.
// For 200 the offset is 0 and the partial file is rewritten. For 206 the offset is have.
func checkStart(resp *http.Response, have, size int64, part string) (int64, error) {
	switch resp.StatusCode {
	case http.StatusOK:
		if resp.ContentLength >= 0 && resp.ContentLength != size {
			return 0, fmt.Errorf("hf: %w: server sent %d bytes, expected %d", ErrSizeMismatch, resp.ContentLength, size)
		}
		return 0, nil
	case http.StatusPartialContent:
		if have == 0 {
			return 0, errors.New("hf: unexpected 206 for a request without Range")
		}
		if err := checkContentRange(resp.Header.Get("Content-Range"), have, size); err != nil {
			return 0, err
		}
		if resp.ContentLength >= 0 && resp.ContentLength != size-have {
			return 0, fmt.Errorf("hf: %w: range body is %d bytes, expected %d", ErrSizeMismatch, resp.ContentLength, size-have)
		}
		return have, nil
	}
	if resp.StatusCode == http.StatusRequestedRangeNotSatisfiable && have > 0 {
		// The partial file no longer matches the server. Remove it so that the next attempt starts clean.
		if err := removeIfExists(part); err != nil {
			return 0, err
		}
	}
	return 0, failed(resp)
}

// checkContentRange checks "bytes <start>-<end>/<total>" against the expected start and total size.
func checkContentRange(v string, start, size int64) error {
	rest, ok := strings.CutPrefix(v, "bytes ")
	if !ok {
		return fmt.Errorf("hf: malformed Content-Range %q", v)
	}
	span, totalText, ok := strings.Cut(rest, "/")
	if !ok {
		return fmt.Errorf("hf: malformed Content-Range %q", v)
	}
	total, err := strconv.ParseInt(totalText, 10, 64)
	if err != nil {
		return fmt.Errorf("hf: %w: Content-Range total %q is not a number", ErrSizeMismatch, totalText)
	}
	if total != size {
		return fmt.Errorf("hf: %w: server total is %d, expected %d", ErrSizeMismatch, total, size)
	}
	startText, endText, ok := strings.Cut(span, "-")
	if !ok {
		return fmt.Errorf("hf: malformed Content-Range %q", v)
	}
	gotStart, err1 := strconv.ParseInt(startText, 10, 64)
	gotEnd, err2 := strconv.ParseInt(endText, 10, 64)
	if err1 != nil || err2 != nil {
		return fmt.Errorf("hf: malformed Content-Range %q", v)
	}
	if gotStart != start || gotEnd != size-1 {
		return fmt.Errorf("hf: unexpected Content-Range %q, expected bytes %d-%d", v, start, size-1)
	}
	return nil
}

// stream writes the response body to part from offset on. It returns the SHA-256 of the whole file and the byte count.
// On a body error it returns an error and keeps the partial file. A short body is not an error here.
func streamBody(ctx context.Context, resp *http.Response, part string, offset int64, req DownloadRequest) (string, int64, error) {
	h := sha256.New()
	f, err := openPart(part, offset, h)
	if err != nil {
		return "", 0, err
	}

	pw := &progressWriter{w: io.MultiWriter(f, h), done: offset, total: req.Size, report: req.Progress}
	pw.notify()
	// Limit the body to one byte more than expected, so that an oversized body is detected without being written in full.
	body := io.LimitReader(resp.Body, req.Size-offset+1)
	_, copyErr := io.CopyBuffer(pw, body, make([]byte, copyBufferSize))
	closeErr := f.Close()
	if copyErr != nil {
		return "", 0, interrupted(ctx, req.Path, copyErr)
	}
	if closeErr != nil {
		return "", 0, fmt.Errorf("hf: close partial file: %w", closeErr)
	}
	if pw.done > req.Size {
		if err := removeIfExists(part); err != nil {
			return "", 0, err
		}
		return "", 0, fmt.Errorf("hf: %w: body is longer than %d bytes", ErrSizeMismatch, req.Size)
	}
	return hex.EncodeToString(h.Sum(nil)), pw.done, nil
}

// openPart opens the partial file for writing at offset. When offset is greater than zero, the bytes before it are hashed first.
// When offset is zero the file is truncated.
func openPart(part string, offset int64, h hash.Hash) (*os.File, error) {
	if offset == 0 {
		f, err := os.OpenFile(part, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
		if err != nil {
			return nil, fmt.Errorf("hf: create partial file: %w", err)
		}
		return f, nil
	}
	in, err := os.Open(part)
	if err != nil {
		return nil, fmt.Errorf("hf: open partial file: %w", err)
	}
	n, err := io.CopyN(h, in, offset)
	closeErr := in.Close()
	if err != nil || n != offset {
		return nil, fmt.Errorf("hf: read partial file: got %d of %d bytes: %v", n, offset, err)
	}
	if closeErr != nil {
		return nil, fmt.Errorf("hf: close partial file: %w", closeErr)
	}
	f, err := os.OpenFile(part, os.O_WRONLY, 0o644)
	if err != nil {
		return nil, fmt.Errorf("hf: open partial file for append: %w", err)
	}
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		f.Close()
		return nil, fmt.Errorf("hf: seek partial file: %w", err)
	}
	return f, nil
}

// interrupted wraps an error from a transfer. A cancelled context is reported as such, so errors.Is works for context.Canceled.
func interrupted(ctx context.Context, path string, err error) error {
	if ctxErr := ctx.Err(); ctxErr != nil {
		return fmt.Errorf("hf: download %s interrupted: %w", path, ctxErr)
	}
	return fmt.Errorf("hf: download %s: %w", path, err)
}

// verifyAndInstall compares the digest with want and then installs the partial file as dest.
// On a mismatch the partial file is removed.
func verifyAndInstall(part, dest, want, got string) error {
	if got != want {
		if err := removeIfExists(part); err != nil {
			return err
		}
		return fmt.Errorf("hf: %w: expected %s, got %s", ErrChecksumMismatch, want, got)
	}
	f, err := os.Open(part)
	if err != nil {
		return fmt.Errorf("hf: open for sync: %w", err)
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return fmt.Errorf("hf: sync: %w", err)
	}
	if err := f.Close(); err != nil {
		return fmt.Errorf("hf: close after sync: %w", err)
	}
	if err := os.Rename(part, dest); err != nil {
		return fmt.Errorf("hf: install file: %w", err)
	}
	return nil
}

// hashFile returns the SHA-256 of the first size bytes of path. It fails if the file is shorter.
func hashFile(path string, size int64) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("hf: open partial file: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.CopyN(h, f, size)
	if err != nil || n != size {
		return "", fmt.Errorf("hf: %w: partial file has %d bytes, expected %d", ErrSizeMismatch, n, size)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// partSize returns the size of the partial file, or 0 when it does not exist.
func partSize(path string) (int64, error) {
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return 0, nil
		}
		return 0, fmt.Errorf("hf: stat partial file: %w", err)
	}
	if !info.Mode().IsRegular() {
		return 0, fmt.Errorf("hf: partial path %s is not a regular file", path)
	}
	return info.Size(), nil
}

// removeIfExists removes path and ignores a missing file.
func removeIfExists(path string) error {
	if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("hf: remove %s: %w", filepath.Base(path), err)
	}
	return nil
}

// progressWriter counts the bytes written and calls report at most once per progressStep.
type progressWriter struct {
	w          io.Writer
	done       int64
	total      int64
	lastReport int64
	report     func(done, total int64)
}

// Write writes p to the underlying writer and reports progress.
func (p *progressWriter) Write(b []byte) (int, error) {
	n, err := p.w.Write(b)
	p.done += int64(n)
	if p.done-p.lastReport >= progressStep {
		p.notify()
	}
	return n, err
}

// notify calls report with the current count, if a report function is set.
func (p *progressWriter) notify() {
	p.lastReport = p.done
	if p.report != nil {
		p.report(p.done, p.total)
	}
}
