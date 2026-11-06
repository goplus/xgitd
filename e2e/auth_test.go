package e2e

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goplus/xgitd/wire"
)

// denyReceivePack rejects pushes but allows fetches, a realistic read-only
// policy that exercises the Authorizer against the real git client.
func denyReceivePack() wire.Authorizer {
	return wire.AuthorizeFunc(func(ctx context.Context, s *wire.Session) error {
		if s.Service == wire.ServiceReceivePack {
			return fmt.Errorf("pushes are disabled: %w", wire.ErrUnauthorized)
		}
		return nil
	})
}

func TestHTTPCloneAuthorized(t *testing.T) {
	requireGit(t)
	dir, head := makeSourceRepo(t)

	// An authorizer that allows upload-pack must not break a clone.
	srv := &wire.Server{Handler: &repoHandler{dir: dir}, Authorizer: denyReceivePack()}
	url := startHTTPServer(t, srv)
	assertClone(t,
		[]string{"GIT_TERMINAL_PROMPT=0",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=2"},
		url, head)
}

func TestHTTPCloneUnauthorized(t *testing.T) {
	requireGit(t)
	dir, _ := makeSourceRepo(t)

	// Deny everything: the clone must fail and the handler must never run.
	denyAll := wire.AuthorizeFunc(func(ctx context.Context, s *wire.Session) error {
		return wire.ErrUnauthorized
	})
	srv := &wire.Server{Handler: &repoHandler{dir: dir}, Authorizer: denyAll}
	url := startHTTPServer(t, srv)

	dst := filepath.Join(t.TempDir(), "clone")
	out, err := runGit(t,
		[]string{"GIT_TERMINAL_PROMPT=0",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=2"},
		"clone", "--quiet", url, dst)
	if err == nil {
		t.Fatalf("expected clone to fail for an unauthorized session; output:\n%s", out)
	}
	// git surfaces the 403 to the user.
	if !strings.Contains(out, "403") && !strings.Contains(strings.ToLower(out), "forbidden") {
		t.Fatalf("expected a 403/forbidden error, got:\n%s", out)
	}
}

func TestSSHCloneAuthorized(t *testing.T) {
	requireGit(t)
	requireSSHTools(t)
	dir, head := makeSourceRepo(t)

	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen failed: %v\n%s", err, out)
	}

	srv := &wire.Server{Handler: &repoHandler{dir: dir}, Authorizer: denyReceivePack()}
	port := startSSHServer(t, srv)
	sshCmd := fmt.Sprintf("ssh -p %s -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes", port, keyPath)
	assertClone(t,
		[]string{"GIT_SSH_COMMAND=" + sshCmd, "GIT_TERMINAL_PROMPT=0",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=2"},
		"git@127.0.0.1:org/repo.git", head)
}

func TestSSHCloneUnauthorized(t *testing.T) {
	requireGit(t)
	requireSSHTools(t)
	dir, _ := makeSourceRepo(t)

	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen failed: %v\n%s", err, out)
	}

	denyAll := wire.AuthorizeFunc(func(ctx context.Context, s *wire.Session) error {
		return wire.ErrUnauthorized
	})
	srv := &wire.Server{Handler: &repoHandler{dir: dir}, Authorizer: denyAll}
	port := startSSHServer(t, srv)
	sshCmd := fmt.Sprintf("ssh -p %s -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes", port, keyPath)

	dst := filepath.Join(t.TempDir(), "clone")
	out, err := runGit(t,
		[]string{"GIT_SSH_COMMAND=" + sshCmd, "GIT_TERMINAL_PROMPT=0",
			"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=2"},
		"clone", "--quiet", "git@127.0.0.1:org/repo.git", dst)
	if err == nil {
		t.Fatalf("expected clone to fail for an unauthorized session; output:\n%s", out)
	}
	if !strings.Contains(strings.ToLower(out), "unauthorized") {
		t.Fatalf("expected an 'unauthorized' ERR from the server, got:\n%s", out)
	}
}
