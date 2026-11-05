package wire

import (
	"errors"
	"strings"
)

// ErrUnsafeRepoPath is returned when a requested repository path is unsafe (for
// example it contains a ".." path element).
var ErrUnsafeRepoPath = errors.New("xgitd: unsafe repository path")

// NormalizeRepoPath cleans a client-supplied repository path into the canonical
// "org/repo.git" form used throughout the server.
//
// It strips a leading scp-style "host:" prefix, removes leading and surrounding
// slashes, collapses repeated slashes, and rejects any path that would escape
// its root via a ".." element. The returned path never begins or ends with a
// slash.
//
// Normalization is deliberately syntactic: it does not consult the filesystem
// and makes no judgement about whether the repository exists.
func NormalizeRepoPath(raw string) (string, error) {
	p := raw

	// Strip an scp-style "host:" prefix (git@host:org/repo.git). Only treat a
	// colon before the first slash as the scp separator.
	if i := strings.IndexByte(p, ':'); i >= 0 {
		if slash := strings.IndexByte(p, '/'); slash < 0 || i < slash {
			p = p[i+1:]
		}
	}

	// Trim surrounding whitespace and slashes.
	p = strings.TrimSpace(p)
	p = strings.Trim(p, "/")

	if p == "" {
		return "", ErrUnsafeRepoPath
	}

	// Reject backslashes outright: the canonical form is POSIX, and treating a
	// backslash as an ordinary byte would let "..\..\etc" slip through the
	// ".." check below and become a traversal escape on any backend that later
	// interprets "\" as a separator.
	if strings.ContainsRune(p, '\\') {
		return "", ErrUnsafeRepoPath
	}

	// Collapse repeated slashes and reject ".." / "." segments and NUL bytes.
	var cleaned []string
	for _, seg := range strings.Split(p, "/") {
		switch seg {
		case "":
			// Skip empty segments from repeated slashes.
			continue
		case ".", "..":
			return "", ErrUnsafeRepoPath
		}
		if strings.ContainsRune(seg, 0) {
			return "", ErrUnsafeRepoPath
		}
		cleaned = append(cleaned, seg)
	}
	if len(cleaned) == 0 {
		return "", ErrUnsafeRepoPath
	}
	return strings.Join(cleaned, "/"), nil
}
