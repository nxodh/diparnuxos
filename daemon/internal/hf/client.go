// Package hf is a small client for the Hugging Face Hub API and file resolver.
//
// The response shapes follow the observations recorded in docs/verification-phase-2-research.md (section D).
// The client uses only the standard library. Tests use net/http/httptest and make no real network calls.
package hf

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// Defaults and limits.
const (
	defaultBaseURL   = "https://huggingface.co"
	defaultUserAgent = "lightlixd"
	maxJSONBytes     = 16 << 20 // Largest JSON response body accepted.
	maxPages         = 100      // Most pages followed in one listing.
	maxSearchLimit   = 100      // Largest limit accepted by Search.
	maxResetSeconds  = 86400    // Largest reset time accepted from a RateLimit header.
	errorBodyDrain   = 4 << 10  // Bytes read from an error body before it is closed.
)

// Sentinel errors. Use errors.Is to test for them. Messages carry no secrets.
var (
	// ErrGated is returned when the repository or file is gated and the token has no access.
	ErrGated = errors.New("gated repository")
	// ErrNotFound is returned for HTTP 404.
	ErrNotFound = errors.New("not found")
	// ErrRateLimited is returned for HTTP 429. The returned error is a *RateLimitError.
	ErrRateLimited = errors.New("rate limited")
	// ErrChecksumMismatch is returned when the downloaded bytes do not match the expected SHA-256.
	ErrChecksumMismatch = errors.New("checksum mismatch")
	// ErrSizeMismatch is returned when the downloaded size or the server's stated size differs from the expected size.
	ErrSizeMismatch = errors.New("size mismatch")
	// ErrInsufficientSpace is returned when the target directory lacks room for the download plus the margin.
	ErrInsufficientSpace = errors.New("insufficient disk space")
	// ErrInvalidInput is returned for a repository, revision, path, or request field that fails validation.
	ErrInvalidInput = errors.New("invalid input")
)

// Client talks to the Hugging Face Hub. The zero value uses the public Hub without a token.
type Client struct {
	// BaseURL is the Hub origin. An empty value means https://huggingface.co.
	// An http URL is accepted only for a loopback host, so that a token is never sent in clear text.
	BaseURL string
	// HTTP is the client used for requests. nil means a new default client.
	HTTP *http.Client
	// Token is the access token. It is sent only in the Authorization header.
	Token string
	// UserAgent is sent with every request. An empty value means "lightlixd".
	UserAgent string
}

// StatusError reports an HTTP status that has no dedicated sentinel error.
type StatusError struct {
	// StatusCode is the HTTP status code.
	StatusCode int
}

// Error returns the status as text.
func (e *StatusError) Error() string {
	return fmt.Sprintf("unexpected HTTP status %d", e.StatusCode)
}

// RateLimitError reports an HTTP 429 response. errors.Is(err, ErrRateLimited) is true for it.
type RateLimitError struct {
	// ResetSeconds is the largest t= value in the RateLimit header. It is 0 when the header is missing or unreadable.
	ResetSeconds int
}

// Error returns a short description that includes the reset time when it is known.
func (e *RateLimitError) Error() string {
	if e.ResetSeconds > 0 {
		return fmt.Sprintf("rate limited, retry after %d seconds", e.ResetSeconds)
	}
	return ErrRateLimited.Error()
}

// Is reports whether target is ErrRateLimited.
func (e *RateLimitError) Is(target error) bool {
	return target == ErrRateLimited
}

// GateMode is the gating mode of a repository, as reported by the Hub: "false", "auto", or "manual".
// The empty value means the Hub did not report the field.
type GateMode string

// Gating modes reported by the Hub.
const (
	GateNone   GateMode = "false"
	GateAuto   GateMode = "auto"
	GateManual GateMode = "manual"
)

// GGUFSummary is the gguf object of a model, which the Hub derives from the GGUF header.
// Zero values mean that the Hub did not report the field.
type GGUFSummary struct {
	// Architecture is the general.architecture value.
	Architecture string
	// ContextLength is the context length of the model.
	ContextLength uint64
	// Total is the total number of parameters.
	Total uint64
}

// ModelInfo is the summary that GET /api/models/{repo} returns.
type ModelInfo struct {
	// ID is the repository name, such as "ggml-org/gemma-3-1b-it-GGUF".
	ID string
	// SHA is the commit SHA of the default revision.
	SHA string
	// Private reports whether the repository is private.
	Private bool
	// Gated is the gating mode.
	Gated GateMode
	// License comes from cardData.license, or else from the license:* tag. It is empty when neither is a string.
	License string
	// GGUF is the gguf summary. It is zero when the Hub reports none.
	GGUF GGUFSummary
}

// File is one entry of a repository tree.
type File struct {
	// Path is the path inside the repository.
	Path string
	// Size is the file size in bytes.
	Size int64
	// SHA256 is the lowercase SHA-256 from lfs.oid. It is empty when the file is not stored in LFS.
	SHA256 string
	// LFS reports whether the file is stored in LFS.
	LFS bool
}

// SearchResult is one model returned by Search.
type SearchResult struct {
	// ID is the repository name.
	ID string
	// Downloads is the download count that the Hub reports.
	Downloads uint64
	// Gated is the gating mode.
	Gated GateMode
	// License is the license, as in ModelInfo.
	License string
	// Architecture is gguf.architecture.
	Architecture string
	// ContextLength is gguf.context_length.
	ContextLength uint64
}

// wireModel is the JSON shape shared by the model info and search responses.
type wireModel struct {
	ID        string          `json:"id"`
	ModelID   string          `json:"modelId"`
	SHA       string          `json:"sha"`
	Private   bool            `json:"private"`
	Gated     json.RawMessage `json:"gated"`
	Tags      []string        `json:"tags"`
	CardData  json.RawMessage `json:"cardData"`
	GGUF      *wireGGUF       `json:"gguf"`
	Downloads uint64          `json:"downloads"`
}

// wireGGUF is the gguf object inside wireModel.
type wireGGUF struct {
	Total         uint64 `json:"total"`
	Architecture  string `json:"architecture"`
	ContextLength uint64 `json:"context_length"`
}

// wireEntry is one element of the tree listing.
type wireEntry struct {
	Type string `json:"type"`
	Path string `json:"path"`
	Size int64  `json:"size"`
	LFS  *struct {
		OID string `json:"oid"`
	} `json:"lfs"`
}

// ModelInfo returns the summary of repo. It calls GET /api/models/{repo}.
func (c *Client) ModelInfo(ctx context.Context, repo string) (ModelInfo, error) {
	if err := validateRepo(repo); err != nil {
		return ModelInfo{}, err
	}
	u, err := c.url("api/models/"+escapePath(repo), "")
	if err != nil {
		return ModelInfo{}, err
	}
	resp, err := c.get(ctx, u, nil)
	if err != nil {
		return ModelInfo{}, fmt.Errorf("hf: model info %s: %w", repo, err)
	}
	if !isOK(resp) {
		return ModelInfo{}, fmt.Errorf("hf: model info %s: %w", repo, failed(resp))
	}
	body, err := readBody(resp, maxJSONBytes)
	if err != nil {
		return ModelInfo{}, fmt.Errorf("hf: model info %s: %w", repo, err)
	}
	var w wireModel
	if err := json.Unmarshal(body, &w); err != nil {
		return ModelInfo{}, fmt.Errorf("hf: model info %s: decode: %w", repo, err)
	}
	info, err := w.modelInfo(repo)
	if err != nil {
		return ModelInfo{}, fmt.Errorf("hf: model info %s: %w", repo, err)
	}
	return info, nil
}

// ListFiles returns the files of repo at revision rev. It calls GET /api/models/{repo}/tree/{rev}?recursive=true
// and follows the rel="next" Link headers. Directories are not included.
func (c *Client) ListFiles(ctx context.Context, repo, rev string) ([]File, error) {
	if err := validateRepo(repo); err != nil {
		return nil, err
	}
	if err := validateRef(rev); err != nil {
		return nil, err
	}
	first, err := c.url("api/models/"+escapePath(repo)+"/tree/"+escapePath(rev), "recursive=true")
	if err != nil {
		return nil, err
	}
	var files []File
	err = c.listPages(ctx, first, func(body []byte) error {
		var entries []wireEntry
		if err := json.Unmarshal(body, &entries); err != nil {
			return fmt.Errorf("decode tree: %w", err)
		}
		for _, e := range entries {
			if e.Type != "file" {
				continue
			}
			f, err := e.file()
			if err != nil {
				return fmt.Errorf("file %q: %w", e.Path, err)
			}
			files = append(files, f)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("hf: list files %s@%s: %w", repo, rev, err)
	}
	return files, nil
}

// file converts a tree entry into a File, checking the size and the LFS digest.
func (e wireEntry) file() (File, error) {
	if e.Size < 0 {
		return File{}, fmt.Errorf("%w: negative size", ErrInvalidInput)
	}
	f := File{Path: e.Path, Size: e.Size}
	if e.LFS != nil {
		f.LFS = true
		if e.LFS.OID != "" {
			if !isSHA256(e.LFS.OID) {
				return File{}, fmt.Errorf("lfs.oid is not a SHA-256 digest")
			}
			f.SHA256 = strings.ToLower(e.LFS.OID)
		}
	}
	return f, nil
}

// Search returns up to limit GGUF models whose IDs contain query, most downloaded first.
// It calls GET /api/models with filter=gguf and the expand[] parameters.
func (c *Client) Search(ctx context.Context, query string, limit int) ([]SearchResult, error) {
	if err := validateQuery(query); err != nil {
		return nil, err
	}
	if limit < 1 || limit > maxSearchLimit {
		return nil, fmt.Errorf("%w: limit must be between 1 and %d", ErrInvalidInput, maxSearchLimit)
	}
	params := url.Values{}
	params.Set("search", query)
	params.Set("filter", "gguf")
	params.Set("sort", "downloads")
	params.Set("direction", "-1")
	params.Set("limit", strconv.Itoa(limit))
	raw := params.Encode() + "&expand[]=gguf&expand[]=gated&expand[]=cardData&expand[]=tags&expand[]=downloads"

	u, err := c.url("api/models", raw)
	if err != nil {
		return nil, err
	}
	resp, err := c.get(ctx, u, nil)
	if err != nil {
		return nil, fmt.Errorf("hf: search: %w", err)
	}
	if !isOK(resp) {
		return nil, fmt.Errorf("hf: search: %w", failed(resp))
	}
	body, err := readBody(resp, maxJSONBytes)
	if err != nil {
		return nil, fmt.Errorf("hf: search: %w", err)
	}
	var items []wireModel
	if err := json.Unmarshal(body, &items); err != nil {
		return nil, fmt.Errorf("hf: search: decode: %w", err)
	}
	results := make([]SearchResult, 0, len(items))
	for _, item := range items {
		info, err := item.modelInfo("")
		if err != nil {
			return nil, fmt.Errorf("hf: search: %w", err)
		}
		r := SearchResult{
			ID:            info.ID,
			Downloads:     item.Downloads,
			Gated:         info.Gated,
			License:       info.License,
			Architecture:  info.GGUF.Architecture,
			ContextLength: info.GGUF.ContextLength,
		}
		if r.ID == "" {
			r.ID = item.ModelID
		}
		results = append(results, r)
	}
	if len(results) > limit {
		results = results[:limit]
	}
	return results, nil
}

// modelInfo converts the wire form into ModelInfo. fallbackID is used when the response has no id.
func (w wireModel) modelInfo(fallbackID string) (ModelInfo, error) {
	gated, err := parseGated(w.Gated)
	if err != nil {
		return ModelInfo{}, err
	}
	info := ModelInfo{
		ID:      w.ID,
		SHA:     w.SHA,
		Private: w.Private,
		Gated:   gated,
		License: cardLicense(w.CardData),
	}
	if info.ID == "" {
		info.ID = fallbackID
	}
	if info.License == "" {
		info.License = licenseTag(w.Tags)
	}
	if w.GGUF != nil {
		info.GGUF = GGUFSummary{
			Architecture:  w.GGUF.Architecture,
			ContextLength: w.GGUF.ContextLength,
			Total:         w.GGUF.Total,
		}
	}
	return info, nil
}

// parseGated reads the gated field, which is false, "auto", or "manual". A missing field gives the empty mode.
func parseGated(raw json.RawMessage) (GateMode, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", nil
	}
	var flag bool
	if err := json.Unmarshal(raw, &flag); err == nil {
		if flag {
			return "", errors.New("unexpected gated value true")
		}
		return GateNone, nil
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("gated: %w", err)
	}
	switch GateMode(s) {
	case GateAuto, GateManual:
		return GateMode(s), nil
	}
	return "", fmt.Errorf("unexpected gated value %q", s)
}

// cardLicense returns cardData.license when it is a string. Any other shape gives the empty string.
func cardLicense(raw json.RawMessage) string {
	if len(raw) == 0 {
		return ""
	}
	var card struct {
		License json.RawMessage `json:"license"`
	}
	if err := json.Unmarshal(raw, &card); err != nil {
		return ""
	}
	var s string
	if err := json.Unmarshal(card.License, &s); err != nil {
		return ""
	}
	return s
}

// licenseTag returns the value of the first "license:<name>" tag, or the empty string.
func licenseTag(tags []string) string {
	for _, tag := range tags {
		if name, ok := strings.CutPrefix(tag, "license:"); ok && name != "" {
			return name
		}
	}
	return ""
}

// listPages calls handle with the body of each page. It follows the rel="next" links and stops after maxPages.
func (c *Client) listPages(ctx context.Context, first string, handle func([]byte) error) error {
	base, err := c.baseURL()
	if err != nil {
		return err
	}
	seen := make(map[string]bool)
	next := first
	for page := 0; next != ""; page++ {
		if page >= maxPages {
			return fmt.Errorf("more than %d pages", maxPages)
		}
		if seen[next] {
			return errors.New("pagination loop")
		}
		seen[next] = true

		resp, err := c.get(ctx, next, nil)
		if err != nil {
			return err
		}
		if !isOK(resp) {
			return failed(resp)
		}
		body, err := readBody(resp, maxJSONBytes)
		if err != nil {
			return err
		}
		if err := handle(body); err != nil {
			return err
		}
		next, err = nextLink(resp.Header.Values("Link"), base)
		if err != nil {
			return err
		}
	}
	return nil
}

// nextLink finds the rel="next" target in Link header values. Its host must match the base URL,
// so that the token is never sent to another host. It returns the empty string when there is no next page.
func nextLink(values []string, base *url.URL) (string, error) {
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			part = strings.TrimSpace(part)
			if !strings.HasPrefix(part, "<") {
				continue
			}
			end := strings.IndexByte(part, '>')
			if end < 0 || !isNextRel(part[end+1:]) {
				continue
			}
			ref, err := url.Parse(part[1:end])
			if err != nil {
				return "", fmt.Errorf("bad pagination link: %w", err)
			}
			target := base.ResolveReference(ref)
			if target.Scheme != base.Scheme || target.Host != base.Host {
				return "", errors.New("pagination link points to another host")
			}
			return target.String(), nil
		}
	}
	return "", nil
}

// isNextRel reports whether the parameters after a Link target contain rel="next".
func isNextRel(params string) bool {
	for _, p := range strings.Split(params, ";") {
		p = strings.TrimSpace(p)
		if p == `rel="next"` || p == "rel=next" {
			return true
		}
	}
	return false
}

// baseURL returns the parsed base URL and rejects a non-loopback http origin.
func (c *Client) baseURL() (*url.URL, error) {
	raw := c.BaseURL
	if raw == "" {
		raw = defaultBaseURL
	}
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("%w: base URL: %v", ErrInvalidInput, err.Error())
	}
	if u.Host == "" {
		return nil, fmt.Errorf("%w: base URL has no host", ErrInvalidInput)
	}
	switch u.Scheme {
	case "https":
	case "http":
		if !isLoopback(u.Host) {
			return nil, fmt.Errorf("%w: base URL must use https unless the host is loopback", ErrInvalidInput)
		}
	default:
		return nil, fmt.Errorf("%w: base URL scheme must be https", ErrInvalidInput)
	}
	u.Path = strings.TrimRight(u.Path, "/")
	u.RawQuery = ""
	u.Fragment = ""
	return u, nil
}

// url builds an absolute URL from an already escaped path and an optional raw query.
func (c *Client) url(escapedPath, rawQuery string) (string, error) {
	base, err := c.baseURL()
	if err != nil {
		return "", err
	}
	s := base.String() + "/" + escapedPath
	if rawQuery != "" {
		s += "?" + rawQuery
	}
	return s, nil
}

// httpClient returns the configured client, or a new default client.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{}
}

// userAgent returns the configured User-Agent or the default.
func (c *Client) userAgent() string {
	if c.UserAgent != "" {
		return c.UserAgent
	}
	return defaultUserAgent
}

// get sends a GET request with the token and the given extra headers. The caller closes the response body.
// The token is set only in the Authorization header and never appears in an error.
func (c *Client) get(ctx context.Context, rawURL string, headers map[string]string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", c.userAgent())
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	return resp, nil
}

// isOK reports whether the response status is 2xx.
func isOK(resp *http.Response) bool {
	return resp.StatusCode >= 200 && resp.StatusCode < 300
}

// failed drains and closes an unsuccessful response and returns the error for its status.
func failed(resp *http.Response) error {
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, errorBodyDrain))
	return statusError(resp)
}

// statusError maps an unsuccessful status to a sentinel error or a typed error.
func statusError(resp *http.Response) error {
	switch resp.StatusCode {
	case http.StatusUnauthorized, http.StatusForbidden:
		if resp.Header.Get("X-Error-Code") == "GatedRepo" {
			return ErrGated
		}
	case http.StatusNotFound:
		return ErrNotFound
	case http.StatusTooManyRequests:
		return &RateLimitError{ResetSeconds: resetSeconds(resp.Header.Get("RateLimit"))}
	}
	return &StatusError{StatusCode: resp.StatusCode}
}

// resetSeconds returns the largest t= value in a RateLimit header such as "api";r=0;t=141.
// It returns 0 when there is no valid value.
func resetSeconds(header string) int {
	best := 0
	for _, field := range strings.FieldsFunc(header, func(r rune) bool { return r == ';' || r == ',' }) {
		v, ok := strings.CutPrefix(strings.TrimSpace(field), "t=")
		if !ok {
			continue
		}
		n, err := strconv.Atoi(v)
		if err == nil && n >= 0 && n <= maxResetSeconds && n > best {
			best = n
		}
	}
	return best
}

// readBody reads a response body of at most limit bytes and closes it.
func readBody(resp *http.Response, limit int64) ([]byte, error) {
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, fmt.Errorf("read response: %w", err)
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("response is larger than %d bytes", limit)
	}
	return data, nil
}
