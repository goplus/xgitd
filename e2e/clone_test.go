package e2e

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/goplus/xgitd/wire"
)

// repoHandler is a handler backed by a real on-disk git repository. It uses the
// xgitd response mechanisms to advertise the repo's refs and to stream a
// packfile (built with `git pack-objects`) for the wanted objects. It exists to
// prove, against the real git client, that the response writers frame a
// complete, clonable response.
type repoHandler struct {
	wire.BaseHandler
	dir string
}

func (h *repoHandler) git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", append([]string{"-C", h.dir}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("git %v: %v: %s", args, err, errb.String())
	}
	return out.Bytes(), nil
}

// refs returns the repository's refs, HEAD first with its symref target.
func (h *repoHandler) refs() ([]wire.Ref, error) {
	headOID, err := h.git("rev-parse", "HEAD")
	if err != nil {
		return nil, err
	}
	sym, _ := h.git("symbolic-ref", "HEAD")
	refs := []wire.Ref{{
		Name:         "HEAD",
		ID:           strings.TrimSpace(string(headOID)),
		SymrefTarget: strings.TrimSpace(string(sym)),
	}}
	showRef, err := h.git("show-ref")
	if err != nil {
		return nil, err
	}
	for _, line := range strings.Split(strings.TrimSpace(string(showRef)), "\n") {
		f := strings.Fields(line)
		if len(f) == 2 {
			refs = append(refs, wire.Ref{Name: f[1], ID: f[0]})
		}
	}
	return refs, nil
}

// buildPack builds a packfile containing everything reachable from wants.
func (h *repoHandler) buildPack(wants []string) ([]byte, error) {
	cmd := exec.Command("git", "-C", h.dir, "pack-objects", "--stdout", "--revs", "-q")
	cmd.Stdin = strings.NewReader(strings.Join(wants, "\n") + "\n")
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("pack-objects: %v: %s", err, errb.String())
	}
	return out.Bytes(), nil
}

func (h *repoHandler) AdvertiseRefs(ctx context.Context, s *wire.Session) error {
	refs, err := h.refs()
	if err != nil {
		return err
	}
	return wire.WriteRefAdvertisementV0(s, wire.RefAdvertisementV0{
		Service:      s.Service,
		Refs:         refs,
		Capabilities: []string{"side-band-64k", "ofs-delta", "agent=git/xgitd"},
	})
}

func (h *repoHandler) UploadPack(ctx context.Context, s *wire.Session, req *wire.UploadPackRequest) error {
	resp := wire.NewUploadPackResponder(s, req.Capabilities.Has("side-band-64k"))
	if err := resp.NAK(); err != nil {
		return err
	}
	pack, err := h.buildPack(req.Wants)
	if err != nil {
		return err
	}
	w, finish := resp.PackWriter()
	if _, err := w.Write(pack); err != nil {
		return err
	}
	return finish()
}

func (h *repoHandler) LsRefs(ctx context.Context, s *wire.Session, req *wire.LsRefsRequest) error {
	refs, err := h.refs()
	if err != nil {
		return err
	}
	return wire.WriteLsRefs(s, req, refs)
}

func (h *repoHandler) Fetch(ctx context.Context, s *wire.Session, req *wire.FetchRequest) error {
	resp := wire.NewFetchResponder(s)
	// When the client is negotiating (it sent haves but has not said done), the
	// protocol requires an acknowledgments section before the packfile. This
	// simple handler always has the full history, so it acknowledges the
	// client's haves and declares itself ready, then sends the pack.
	if len(req.Haves) > 0 && !req.Done {
		if err := resp.Acknowledgments(req.Haves, true); err != nil {
			return err
		}
	}
	pack, err := h.buildPack(req.Wants)
	if err != nil {
		return err
	}
	w, finish, err := resp.PackSection()
	if err != nil {
		return err
	}
	if _, err := w.Write(pack); err != nil {
		return err
	}
	return finish()
}

// makeSourceRepo creates a small repository with one commit and returns its
// path and the HEAD commit id.
func makeSourceRepo(t *testing.T) (dir, head string) {
	t.Helper()
	dir = t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello xgitd\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "a.txt")
	run("commit", "-qm", "first")

	out, err := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatal(err)
	}
	return dir, strings.TrimSpace(string(out))
}

// assertClone clones from url into a temp dir and checks HEAD and file content.
func assertClone(t *testing.T, env []string, url, wantHead string) {
	t.Helper()
	dst := filepath.Join(t.TempDir(), "clone")
	out, err := runGit(t, env, "clone", "--quiet", url, dst)
	if err != nil {
		t.Fatalf("git clone failed: %v\n%s", err, out)
	}
	head, err := exec.Command("git", "-C", dst, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("rev-parse in clone: %v", err)
	}
	if got := strings.TrimSpace(string(head)); got != wantHead {
		t.Fatalf("cloned HEAD = %s, want %s", got, wantHead)
	}
	content, err := exec.Command("git", "-C", dst, "show", "HEAD:a.txt").Output()
	if err != nil {
		t.Fatalf("show cloned file: %v", err)
	}
	if got := strings.TrimSpace(string(content)); got != "hello xgitd" {
		t.Fatalf("cloned content = %q, want %q", got, "hello xgitd")
	}
}

func TestHTTPClone(t *testing.T) {
	requireGit(t)
	dir, head := makeSourceRepo(t)
	for _, version := range []string{"0", "2"} {
		t.Run("protocol-v"+version, func(t *testing.T) {
			url := startHTTP(t, &repoHandler{dir: dir})
			assertClone(t,
				[]string{"GIT_TERMINAL_PROMPT=0", "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
					"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=" + version},
				url, head)
		})
	}
}

func TestSSHClone(t *testing.T) {
	requireGit(t)
	requireSSHTools(t)
	dir, head := makeSourceRepo(t)

	keyDir := t.TempDir()
	keyPath := filepath.Join(keyDir, "id_ed25519")
	if out, err := exec.Command("ssh-keygen", "-t", "ed25519", "-N", "", "-f", keyPath).CombinedOutput(); err != nil {
		t.Skipf("ssh-keygen failed: %v\n%s", err, out)
	}

	for _, version := range []string{"0", "2"} {
		t.Run("protocol-v"+version, func(t *testing.T) {
			port := startSSH(t, &repoHandler{dir: dir})
			sshCmd := fmt.Sprintf("ssh -p %s -i %s -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null -o BatchMode=yes", port, keyPath)
			assertClone(t,
				[]string{"GIT_SSH_COMMAND=" + sshCmd, "GIT_TERMINAL_PROMPT=0",
					"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=" + version},
				"git@127.0.0.1:org/repo.git", head)
		})
	}
}

func TestHTTPFetchIncremental(t *testing.T) {
	// After an initial clone, adding a commit and fetching exercises the
	// response path for a client that already has objects.
	requireGit(t)
	dir, _ := makeSourceRepo(t)
	h := &repoHandler{dir: dir}
	url := startHTTP(t, h)

	env := []string{"GIT_TERMINAL_PROMPT=0",
		"GIT_CONFIG_COUNT=1", "GIT_CONFIG_KEY_0=protocol.version", "GIT_CONFIG_VALUE_0=2"}
	dst := filepath.Join(t.TempDir(), "clone")
	if out, err := runGit(t, env, "clone", "--quiet", url, dst); err != nil {
		t.Fatalf("initial clone: %v\n%s", err, out)
	}

	// Add a second commit to the source repo.
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("second\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"add", "b.txt"}, {"commit", "-qm", "second"}} {
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t",
			"GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	newHead, _ := exec.Command("git", "-C", dir, "rev-parse", "HEAD").Output()

	// Fetch into the clone and verify the new commit arrives.
	fetchEnv := append(env, "GIT_DIR="+filepath.Join(dst, ".git"))
	if out, err := runGit(t, fetchEnv, "-C", dst, "fetch", "--quiet", "origin"); err != nil {
		t.Fatalf("fetch: %v\n%s", err, out)
	}
	got, err := exec.Command("git", "-C", dst, "rev-parse", "origin/main").Output()
	if err != nil {
		t.Fatalf("rev-parse origin/main: %v", err)
	}
	if strings.TrimSpace(string(got)) != strings.TrimSpace(string(newHead)) {
		t.Fatalf("origin/main = %s, want %s", strings.TrimSpace(string(got)), strings.TrimSpace(string(newHead)))
	}
}
