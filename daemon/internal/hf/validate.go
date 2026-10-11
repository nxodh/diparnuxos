package hf

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
)

// Limits on caller-supplied strings.
const (
	maxPathLen  = 1024
	maxQueryLen = 200
)

// repoPattern accepts "owner/name" with ASCII letters, digits, '-', '_', and '.' only.
var repoPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

// validateRepo checks that repo has the owner/name form. The "." and ".." segments are rejected because they would change the URL path.
func validateRepo(repo string) error {
	if !repoPattern.MatchString(repo) {
		return fmt.Errorf("%w: repository must be owner/name", ErrInvalidInput)
	}
	for _, seg := range strings.Split(repo, "/") {
		if seg == "." || seg == ".." {
			return fmt.Errorf("%w: repository has a dot segment", ErrInvalidInput)
		}
	}
	return nil
}

// validateRef checks a revision such as "main", a commit SHA, or "refs/pr/1".
func validateRef(rev string) error {
	return validateSlashPath("revision", rev)
}

// validatePath checks a path inside a repository. It must be relative and must not contain
// empty, ".", or ".." segments, backslashes, or control characters.
func validatePath(p string) error {
	return validateSlashPath("path", p)
}

// validateSlashPath applies the shared rules for revisions and repository paths.
func validateSlashPath(what, p string) error {
	if p == "" {
		return fmt.Errorf("%w: %s is empty", ErrInvalidInput, what)
	}
	if len(p) > maxPathLen {
		return fmt.Errorf("%w: %s is longer than %d bytes", ErrInvalidInput, what, maxPathLen)
	}
	if !utf8.ValidString(p) {
		return fmt.Errorf("%w: %s is not valid UTF-8", ErrInvalidInput, what)
	}
	if hasControl(p) {
		return fmt.Errorf("%w: %s contains a control character", ErrInvalidInput, what)
	}
	if strings.HasPrefix(p, "/") {
		return fmt.Errorf("%w: %s is absolute", ErrInvalidInput, what)
	}
	if strings.Contains(p, `\`) {
		return fmt.Errorf("%w: %s contains a backslash", ErrInvalidInput, what)
	}
	for _, seg := range strings.Split(p, "/") {
		if seg == "" || seg == "." || seg == ".." {
			return fmt.Errorf("%w: %s has an empty or dot segment", ErrInvalidInput, what)
		}
	}
	return nil
}

// validateQuery checks a search string.
func validateQuery(q string) error {
	if len(q) > maxQueryLen {
		return fmt.Errorf("%w: search query is longer than %d bytes", ErrInvalidInput, maxQueryLen)
	}
	if !utf8.ValidString(q) || hasControl(q) {
		return fmt.Errorf("%w: search query contains a control character", ErrInvalidInput)
	}
	return nil
}

// hasControl reports whether s contains an ASCII control character or DEL.
func hasControl(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] < 0x20 || s[i] == 0x7f {
			return true
		}
	}
	return false
}

// isSHA256 reports whether s is 64 hexadecimal digits, in either case.
func isSHA256(s string) bool {
	if len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// escapePath escapes each segment of a slash-separated path. The slashes themselves are kept.
func escapePath(p string) string {
	segs := strings.Split(p, "/")
	for i, seg := range segs {
		segs[i] = url.PathEscape(seg)
	}
	return strings.Join(segs, "/")
}

// isLoopback reports whether host is a loopback name or address.
func isLoopback(host string) bool {
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}
