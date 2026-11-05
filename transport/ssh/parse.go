package ssh

import (
	"encoding/binary"
	"fmt"
	"strings"

	"github.com/goplus/xgitd/wire"
)

// execCommand extracts the command string from an "exec" request payload. The
// payload is an SSH string: a 4-byte big-endian length followed by the bytes.
// A truncated or malformed payload yields the empty string, which
// parseGitCommand rejects.
func execCommand(payload []byte) string {
	s, _, ok := sshString(payload)
	if !ok {
		return ""
	}
	return s
}

// parseEnvRequest extracts the name and value from an "env" request payload,
// which is two SSH strings. A malformed payload yields empty strings.
func parseEnvRequest(payload []byte) (name, value string) {
	name, rest, ok := sshString(payload)
	if !ok {
		return "", ""
	}
	value, _, _ = sshString(rest)
	return name, value
}

// sshString decodes one length-prefixed SSH string from b, returning it, the
// remaining bytes, and whether the frame was well-formed (enough bytes for the
// declared length). A declared length longer than the available bytes is
// reported as malformed rather than silently truncated.
func sshString(b []byte) (string, []byte, bool) {
	if len(b) < 4 {
		return "", nil, false
	}
	n := binary.BigEndian.Uint32(b[:4])
	b = b[4:]
	if uint32(len(b)) < n {
		return "", nil, false
	}
	return string(b[:n]), b[n:], true
}

// parseGitCommand parses a Git service exec command line such as
//
//	git-upload-pack 'org/repo.git'
//	git-receive-pack "/org/repo.git"
//
// and returns the service name and the (still un-normalized) repository
// argument. Any other command is rejected.
func parseGitCommand(cmdline string) (service, repo string, err error) {
	cmdline = strings.TrimSpace(cmdline)
	name, arg, ok := strings.Cut(cmdline, " ")
	if !ok {
		return "", "", fmt.Errorf("unsupported command %q", cmdline)
	}
	switch name {
	case wire.ServiceUploadPack, wire.ServiceReceivePack:
		service = name
	default:
		return "", "", fmt.Errorf("unsupported command %q", name)
	}
	repo = unquoteArg(strings.TrimSpace(arg))
	if repo == "" {
		return "", "", fmt.Errorf("%s: missing repository argument", service)
	}
	return service, repo, nil
}

// unquoteArg removes a single surrounding pair of single or double quotes, the
// form git uses to pass the repository path over SSH.
func unquoteArg(s string) string {
	if len(s) >= 2 {
		if (s[0] == '\'' && s[len(s)-1] == '\'') || (s[0] == '"' && s[len(s)-1] == '"') {
			return s[1 : len(s)-1]
		}
	}
	return s
}
