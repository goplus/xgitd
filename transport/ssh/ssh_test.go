package ssh

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"io"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	gossh "golang.org/x/crypto/ssh"

	"github.com/goplus/xgitd/wire"
)

func pkt(items ...string) string {
	var b strings.Builder
	for _, s := range items {
		switch s {
		case "FLUSH":
			b.WriteString("0000")
			continue
		case "DELIM":
			b.WriteString("0001")
			continue
		}
		n := len(s) + 4
		const hex = "0123456789abcdef"
		b.WriteByte(hex[(n>>12)&0xf])
		b.WriteByte(hex[(n>>8)&0xf])
		b.WriteByte(hex[(n>>4)&0xf])
		b.WriteByte(hex[n&0xf])
		b.WriteString(s)
	}
	return b.String()
}

type recordHandler struct {
	wire.BaseHandler
	mu          sync.Mutex
	advertised  bool
	session     wire.Session
	uploadPack  *wire.UploadPackRequest
	receivePack *wire.ReceivePackRequest
	fetch       *wire.FetchRequest
}

func (h *recordHandler) snapshot(s *wire.Session) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.session = *s
}

func (h *recordHandler) AdvertiseRefs(ctx context.Context, s *wire.Session) error {
	h.mu.Lock()
	h.advertised = true
	h.mu.Unlock()
	h.snapshot(s)
	_, _ = io.WriteString(s.Out, "0000") // minimal v0 advertisement body
	return nil
}
func (h *recordHandler) UploadPack(ctx context.Context, s *wire.Session, req *wire.UploadPackRequest) error {
	h.mu.Lock()
	h.uploadPack = req
	h.mu.Unlock()
	h.snapshot(s)
	return nil
}
func (h *recordHandler) ReceivePack(ctx context.Context, s *wire.Session, req *wire.ReceivePackRequest) error {
	h.mu.Lock()
	h.receivePack = req
	h.mu.Unlock()
	h.snapshot(s)
	return nil
}
func (h *recordHandler) Fetch(ctx context.Context, s *wire.Session, req *wire.FetchRequest) error {
	h.mu.Lock()
	h.fetch = req
	h.mu.Unlock()
	h.snapshot(s)
	return nil
}

// newHostKey generates an ephemeral ed25519 host key for tests.
func newHostKey(t *testing.T) gossh.Signer {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	return signer
}

// startServer wires a Transport onto an in-memory listener and returns its
// address.
func startServer(t *testing.T, h wire.Handler) (net.Listener, *Transport) {
	return startServerWithAuth(t, h, nil)
}

// startServerWithAuth is startServer plus an optional Authorizer on the Server.
func startServerWithAuth(t *testing.T, h wire.Handler, auth wire.Authorizer) (net.Listener, *Transport) {
	t.Helper()
	cfg := &gossh.ServerConfig{
		PublicKeyCallback: FingerprintAuthCallback,
	}
	cfg.AddHostKey(newHostKey(t))

	tr := &Transport{Server: &wire.Server{Handler: h, Authorizer: auth}, Config: cfg}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go tr.Serve(l)
	return l, tr
}

// dial opens an SSH client connection with an ephemeral client key and the
// given GIT_PROTOCOL env value.
func dial(t *testing.T, addr string) *gossh.Client {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	signer, err := gossh.NewSignerFromKey(priv)
	if err != nil {
		t.Fatal(err)
	}
	clientCfg := &gossh.ClientConfig{
		User:            "git",
		Auth:            []gossh.AuthMethod{gossh.PublicKeys(signer)},
		HostKeyCallback: gossh.InsecureIgnoreHostKey(),
		Timeout:         5 * time.Second,
	}
	client, err := gossh.Dial("tcp", addr, clientCfg)
	if err != nil {
		t.Fatal(err)
	}
	return client
}

// runExec opens a session, optionally sets GIT_PROTOCOL, writes stdin, runs the
// command, and returns the combined stdout.
func runExec(t *testing.T, client *gossh.Client, gitProtocol, cmd, stdin string) ([]byte, error) {
	t.Helper()
	sess, err := client.NewSession()
	if err != nil {
		t.Fatal(err)
	}
	defer sess.Close()
	if gitProtocol != "" {
		if err := sess.Setenv("GIT_PROTOCOL", gitProtocol); err != nil {
			// Some servers reject Setenv; our transport accepts it.
			t.Fatalf("setenv: %v", err)
		}
	}
	stdinPipe, err := sess.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	sess.Stdout = &out
	sess.Stderr = io.Discard
	if err := sess.Start(cmd); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(stdinPipe, stdin)
	_ = stdinPipe.Close()
	err = sess.Wait()
	return []byte(out.String()), err
}

func TestSSHUploadPackV0(t *testing.T) {
	h := &recordHandler{}
	l, _ := startServer(t, h)
	defer l.Close()
	client := dial(t, l.Addr().String())
	defer client.Close()

	oid := strings.Repeat("a", 40)
	stdin := pkt("want "+oid+" ofs-delta\n", "FLUSH", "done\n")
	_, err := runExec(t, client, "", "git-upload-pack 'org/repo.git'", stdin)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.advertised {
		t.Fatal("AdvertiseRefs not called for v0")
	}
	if h.uploadPack == nil {
		t.Fatal("UploadPack not called")
	}
	if h.session.Repo != "org/repo.git" {
		t.Fatalf("repo = %q", h.session.Repo)
	}
	if h.session.Transport != "ssh" {
		t.Fatalf("transport = %q", h.session.Transport)
	}
	if !strings.HasPrefix(h.session.User, "SHA256:") {
		t.Fatalf("user fingerprint = %q", h.session.User)
	}
}

func TestSSHFetchV2(t *testing.T) {
	h := &recordHandler{}
	l, _ := startServer(t, h)
	defer l.Close()
	client := dial(t, l.Addr().String())
	defer client.Close()

	oid := strings.Repeat("a", 40)
	stdin := pkt("command=fetch\n", "DELIM", "want "+oid+"\n", "done\n", "FLUSH")
	out, err := runExec(t, client, "version=2", "git-upload-pack 'org/repo.git'", stdin)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.fetch == nil {
		t.Fatal("Fetch not called for v2")
	}
	if h.session.Version != 2 {
		t.Fatalf("version = %d", h.session.Version)
	}
	// v2 begins with the capability advertisement.
	if !strings.Contains(string(out), "ls-refs") {
		t.Fatalf("v2 advertisement missing from output: %q", out)
	}
}

func TestSSHReceivePackV0(t *testing.T) {
	h := &recordHandler{}
	l, _ := startServer(t, h)
	defer l.Close()
	client := dial(t, l.Addr().String())
	defer client.Close()

	oidA := strings.Repeat("a", 40)
	oidB := strings.Repeat("b", 40)
	stdin := pkt(oidA+" "+oidB+" refs/heads/main\x00report-status\n", "FLUSH") + "PACKdata"
	_, err := runExec(t, client, "", "git-receive-pack 'org/repo.git'", stdin)
	if err != nil {
		t.Fatalf("exec: %v", err)
	}

	h.mu.Lock()
	defer h.mu.Unlock()
	if h.receivePack == nil {
		t.Fatal("ReceivePack not called")
	}
	if len(h.receivePack.Commands) != 1 {
		t.Fatalf("commands = %v", h.receivePack.Commands)
	}
}

func TestSSHUnsupportedCommand(t *testing.T) {
	h := &recordHandler{}
	l, _ := startServer(t, h)
	defer l.Close()
	client := dial(t, l.Addr().String())
	defer client.Close()

	out, err := runExec(t, client, "", "rm -rf /", "")
	// The command exits non-zero and reports an ERR pkt-line.
	if err == nil {
		t.Fatal("expected non-zero exit for unsupported command")
	}
	if !strings.Contains(string(out), "ERR") {
		t.Fatalf("expected ERR pkt-line, got %q", out)
	}
}

func TestSSHAuthorizeRejects(t *testing.T) {
	var seen *wire.Session
	auth := wire.AuthorizeFunc(func(ctx context.Context, s *wire.Session) error {
		seen = s
		return wire.ErrUnauthorized
	})
	h := &recordHandler{}
	l, _ := startServerWithAuth(t, h, auth)
	defer l.Close()
	client := dial(t, l.Addr().String())
	defer client.Close()

	oid := strings.Repeat("a", 40)
	stdin := pkt("want "+oid+" ofs-delta\n", "FLUSH", "done\n")
	out, err := runExec(t, client, "", "git-upload-pack 'org/repo.git'", stdin)
	// A rejected session exits non-zero with an ERR pkt-line, and the handler
	// must never be reached.
	if err == nil {
		t.Fatal("expected non-zero exit for unauthorized session")
	}
	if !strings.Contains(string(out), "ERR unauthorized") {
		t.Fatalf("expected ERR unauthorized pkt-line, got %q", out)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.advertised || h.uploadPack != nil {
		t.Fatal("handler was reached despite an unauthorized session")
	}
	if seen == nil || seen.Repo != "org/repo.git" {
		t.Fatalf("authorizer did not see the session: %+v", seen)
	}
}

func TestSSHAuthorizeAllows(t *testing.T) {
	auth := wire.AuthorizeFunc(func(ctx context.Context, s *wire.Session) error {
		return nil // allow
	})
	h := &recordHandler{}
	l, _ := startServerWithAuth(t, h, auth)
	defer l.Close()
	client := dial(t, l.Addr().String())
	defer client.Close()

	oid := strings.Repeat("a", 40)
	stdin := pkt("want "+oid+" ofs-delta\n", "FLUSH", "done\n")
	if _, err := runExec(t, client, "", "git-upload-pack 'org/repo.git'", stdin); err != nil {
		t.Fatalf("exec: %v", err)
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.advertised || h.uploadPack == nil {
		t.Fatal("handler not reached for an allowed session")
	}
}

func TestParseGitCommand(t *testing.T) {
	tests := []struct {
		cmdline     string
		wantService string
		wantRepo    string
		wantErr     bool
	}{
		{"git-upload-pack 'org/repo.git'", wire.ServiceUploadPack, "org/repo.git", false},
		{`git-receive-pack "org/repo.git"`, wire.ServiceReceivePack, "org/repo.git", false},
		{"git-upload-pack org/repo.git", wire.ServiceUploadPack, "org/repo.git", false},
		{"git-upload-pack", "", "", true},
		{"scp -t /tmp", "", "", true},
		{"", "", "", true},
	}
	for _, tt := range tests {
		svc, repo, err := parseGitCommand(tt.cmdline)
		if tt.wantErr {
			if err == nil {
				t.Errorf("parseGitCommand(%q): want error", tt.cmdline)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseGitCommand(%q): %v", tt.cmdline, err)
			continue
		}
		if svc != tt.wantService || repo != tt.wantRepo {
			t.Errorf("parseGitCommand(%q) = (%q,%q), want (%q,%q)", tt.cmdline, svc, repo, tt.wantService, tt.wantRepo)
		}
	}
}

func TestParseGitProtocolSSH(t *testing.T) {
	if parseGitProtocol("version=2") != 2 {
		t.Fatal("version=2 should be 2")
	}
	if parseGitProtocol("version=0") != 0 {
		t.Fatal("version=0 should be 0")
	}
	if parseGitProtocol("") != 0 {
		t.Fatal("empty should be 0")
	}
}
