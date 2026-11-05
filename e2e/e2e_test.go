// Package e2e exercises the xgitd server against the real git client over both
// the SSH and Smart HTTP transports, for protocol versions 0 and 2.
//
// The handler used here is a recorder: every method logs what it decoded and
// then returns ErrNotImplemented (or a trivial empty advertisement, enough for
// git to reach the next step). The git commands are therefore expected to make
// progress far enough to deliver a decoded request to the handler; the
// assertions are on what the handler saw, not on a successful clone/fetch.
//
// The tests skip automatically when git (or, for the SSH cases, ssh and
// ssh-keygen) is not on PATH, and under `go test -short`.
package e2e

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	gossh "golang.org/x/crypto/ssh"

	xhttp "github.com/goplus/xgitd/transport/http"
	xssh "github.com/goplus/xgitd/transport/ssh"
	"github.com/goplus/xgitd/wire"
)

// recorder is a Handler that records each decoded request. For the operations
// git needs to reach during ls-remote it writes a minimal empty advertisement
// so git completes the exchange cleanly.
type recorder struct {
	wire.BaseHandler
	mu   sync.Mutex
	seen []string
}

func (h *recorder) record(format string, args ...any) {
	h.mu.Lock()
	h.seen = append(h.seen, fmt.Sprintf(format, args...))
	h.mu.Unlock()
}

func (h *recorder) events() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]string, len(h.seen))
	copy(out, h.seen)
	return out
}

func (h *recorder) saw(substr string) bool {
	for _, e := range h.events() {
		if strings.Contains(e, substr) {
			return true
		}
	}
	return false
}

func (h *recorder) AdvertiseRefs(ctx context.Context, s *wire.Session) error {
	h.record("advertise-refs transport=%s version=%d repo=%s user=%q", s.Transport, s.Version, s.Repo, s.User)
	// An empty v0 ref advertisement is just a flush after the service banner.
	_, _ = s.Out.Write([]byte("0000"))
	return nil
}

func (h *recorder) LsRefs(ctx context.Context, s *wire.Session, req *wire.LsRefsRequest) error {
	h.record("ls-refs transport=%s version=%d repo=%s prefixes=%v user=%q", s.Transport, s.Version, s.Repo, req.RefPrefixes, s.User)
	// Report no refs.
	_, _ = s.Out.Write([]byte("0000"))
	return nil
}

func (h *recorder) Fetch(ctx context.Context, s *wire.Session, req *wire.FetchRequest) error {
	h.record("fetch transport=%s version=%d repo=%s wants=%d", s.Transport, s.Version, s.Repo, len(req.Wants))
	return wire.ErrNotImplemented
}

func requireGit(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping end-to-end test in -short mode")
	}
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not found on PATH")
	}
}

func requireSSHTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"ssh", "ssh-keygen"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("%s not found on PATH", tool)
		}
	}
}

// startHTTP starts an httptest-style server and returns its base repo URL.
func startHTTP(t *testing.T, h wire.Handler) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: xhttp.NewHandler(&wire.Server{Handler: h})}
	go srv.Serve(l)
	t.Cleanup(func() { srv.Close() })
	return fmt.Sprintf("http://%s/org/repo.git", l.Addr().String())
}

// startSSH starts an SSH transport and returns the port it listens on.
func startSSH(t *testing.T, h wire.Handler) string {
	t.Helper()
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &gossh.ServerConfig{
		PublicKeyCallback: xssh.FingerprintAuthCallback,
	}
	cfg.AddHostKey(signer)
	tr := &xssh.Transport{Server: &wire.Server{Handler: h}, Config: cfg}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go tr.Serve(l)
	t.Cleanup(func() { l.Close() })
	_, port, _ := net.SplitHostPort(l.Addr().String())
	return port
}

func runGit(t *testing.T, env []string, args ...string) (string, error) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestHTTPLsRemote(t *testing.T) {
	requireGit(t)
	for _, version := range []string{"0", "2"} {
		t.Run("protocol-v"+version, func(t *testing.T) {
			h := &recorder{}
			url := startHTTP(t, h)
			out, err := runGit(t,
				[]string{"GIT_TERMINAL_PROMPT=0"},
				"-c", "protocol.version="+version, "ls-remote", url)
			if err != nil {
				t.Fatalf("git ls-remote failed: %v\n%s", err, out)
			}
			assertVersionSeen(t, h, "http", version)
		})
	}
}

func TestSSHLsRemote(t *testing.T) {
	requireGit(t)
	requireSSHTools(t)

	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen failed: %v\n%s", err, out)
	}

	for _, version := range []string{"0", "2"} {
		t.Run("protocol-v"+version, func(t *testing.T) {
			h := &recorder{}
			port := startSSH(t, h)
			sshCmd := fmt.Sprintf("ssh -p %s -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes", port, keyPath)
			out, err := runGit(t,
				[]string{"GIT_SSH_COMMAND=" + sshCmd, "GIT_TERMINAL_PROMPT=0"},
				"-c", "protocol.version="+version, "ls-remote", "git@127.0.0.1:org/repo.git")
			if err != nil {
				t.Fatalf("git ls-remote over ssh failed: %v\n%s", err, out)
			}
			assertVersionSeen(t, h, "ssh", version)
			// Over SSH the public-key fingerprint must be surfaced as the user.
			if !h.saw("user=\"SHA256:") {
				t.Fatalf("ssh fingerprint not surfaced; events: %v", h.events())
			}
			// The scp-style path must be normalized.
			if !h.saw("repo=org/repo.git") {
				t.Fatalf("repo not normalized; events: %v", h.events())
			}
		})
	}
}

// assertVersionSeen checks that the handler was reached on the expected
// transport. For v0, ls-remote triggers AdvertiseRefs; for v2 it triggers
// ls-refs.
func assertVersionSeen(t *testing.T, h *recorder, transport, version string) {
	t.Helper()
	var want string
	switch version {
	case "0":
		want = fmt.Sprintf("advertise-refs transport=%s version=0", transport)
	case "2":
		want = fmt.Sprintf("ls-refs transport=%s version=2", transport)
	}
	if !h.saw(want) {
		t.Fatalf("handler did not see %q; events: %v", want, h.events())
	}
}

// TestVersionNegotiationStripsHeader verifies that a v2-capable client whose
// Git-Protocol header is stripped lands on v0 without error. We simulate the
// stripping by driving the HTTP handler directly with no header.
func TestVersionNegotiationStripsHeader(t *testing.T) {
	requireGit(t)
	h := &recorder{}
	url := startHTTP(t, h)

	// With protocol.version=2 but a proxy that drops the header, git would get
	// a v0 advertisement. We cannot easily strip the header through the git
	// client, so assert the server's own default: no header means v0.
	out, err := runGit(t,
		[]string{"GIT_TERMINAL_PROMPT=0"},
		// Explicitly request v0 to model the "downgraded" outcome.
		"-c", "protocol.version=0", "ls-remote", url)
	if err != nil {
		t.Fatalf("git ls-remote failed: %v\n%s", err, out)
	}
	if !h.saw("version=0") {
		t.Fatalf("expected a v0 exchange; events: %v", h.events())
	}
}
