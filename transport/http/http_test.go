package http

import (
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

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
	wire.NopHandler
	advertised  bool
	version     int
	repo        string
	user        string
	uploadPack  *wire.UploadPackRequest
	receivePack *wire.ReceivePackRequest
	fetch       *wire.FetchRequest
}

func (h *recordHandler) AdvertiseRefs(ctx context.Context, s *wire.Session) error {
	h.advertised = true
	h.version = s.Version
	h.repo = s.Repo
	h.user = s.User
	// Write a plausible v0 ref advertisement so the transport has a body.
	_, _ = io.WriteString(s.Out, "0000")
	return nil
}
func (h *recordHandler) UploadPack(ctx context.Context, s *wire.Session, req *wire.UploadPackRequest) error {
	h.uploadPack = req
	h.repo = s.Repo
	return nil // pretend success; no body needed for the test
}
func (h *recordHandler) ReceivePack(ctx context.Context, s *wire.Session, req *wire.ReceivePackRequest) error {
	h.receivePack = req
	return nil
}
func (h *recordHandler) Fetch(ctx context.Context, s *wire.Session, req *wire.FetchRequest) error {
	h.fetch = req
	h.version = s.Version
	return nil
}

func newTestServer(h wire.Handler) *httptest.Server {
	return httptest.NewServer(NewHandler(&wire.Server{Handler: h}))
}

func TestInfoRefsV0(t *testing.T) {
	h := &recordHandler{}
	ts := newTestServer(h)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/org/repo.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-git-upload-pack-advertisement" {
		t.Fatalf("content-type = %q", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); cc != "no-cache" {
		t.Fatalf("cache-control = %q", cc)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.HasPrefix(string(body), "001e# service=git-upload-pack\n0000") {
		t.Fatalf("banner = %q", body[:min(40, len(body))])
	}
	if !h.advertised {
		t.Fatal("handler AdvertiseRefs not called")
	}
	if h.repo != "org/repo.git" {
		t.Fatalf("repo = %q", h.repo)
	}
}

func TestInfoRefsV2Advertisement(t *testing.T) {
	h := &recordHandler{}
	ts := newTestServer(h)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/org/repo.git/info/refs?service=git-upload-pack", nil)
	req.Header.Set("Git-Protocol", "version=2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	s := string(body)
	if !strings.Contains(s, "# service=git-upload-pack") {
		t.Fatalf("no banner in %q", s)
	}
	if !strings.Contains(s, "ls-refs") || !strings.Contains(s, "fetch") || !strings.Contains(s, "object-info") {
		t.Fatalf("v2 advertisement missing commands: %q", s)
	}
	if h.advertised {
		t.Fatal("v2 discovery should not call AdvertiseRefs")
	}
}

func TestInfoRefsNoServiceRejected(t *testing.T) {
	ts := newTestServer(&recordHandler{})
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/org/repo.git/info/refs")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

func TestInfoRefsV0NotImplementedReturns501(t *testing.T) {
	// A NopHandler's AdvertiseRefs returns ErrNotImplemented and writes nothing,
	// so a v0 discovery must yield 501 rather than a 200 with a half-formed
	// advertisement.
	ts := newTestServer(wire.NopHandler{})
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/org/repo.git/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestPostUploadPackV0(t *testing.T) {
	h := &recordHandler{}
	ts := newTestServer(h)
	defer ts.Close()

	oid := strings.Repeat("a", 40)
	body := pkt("want "+oid+" ofs-delta\n", "FLUSH", "done\n")
	resp, err := http.Post(ts.URL+"/org/repo.git/git-upload-pack", "application/x-git-upload-pack-request", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/x-git-upload-pack-result" {
		t.Fatalf("content-type = %q", ct)
	}
	if h.uploadPack == nil {
		t.Fatal("handler did not receive upload-pack request")
	}
	if len(h.uploadPack.Wants) != 1 || !h.uploadPack.Done {
		t.Fatalf("decoded = %+v", h.uploadPack)
	}
}

func TestPostFetchV2(t *testing.T) {
	h := &recordHandler{}
	ts := newTestServer(h)
	defer ts.Close()

	oid := strings.Repeat("a", 40)
	body := pkt("command=fetch\n", "DELIM", "want "+oid+"\n", "done\n", "FLUSH")
	req, _ := http.NewRequest("POST", ts.URL+"/org/repo.git/git-upload-pack", strings.NewReader(body))
	req.Header.Set("Git-Protocol", "version=2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if h.fetch == nil {
		t.Fatal("handler did not receive fetch request")
	}
	if h.version != 2 {
		t.Fatalf("version = %d", h.version)
	}
}

func TestPostGzipBody(t *testing.T) {
	h := &recordHandler{}
	ts := newTestServer(h)
	defer ts.Close()

	oid := strings.Repeat("a", 40)
	raw := pkt("want "+oid+"\n", "FLUSH", "done\n")
	var gz bytes.Buffer
	zw := gzip.NewWriter(&gz)
	_, _ = zw.Write([]byte(raw))
	_ = zw.Close()

	req, _ := http.NewRequest("POST", ts.URL+"/org/repo.git/git-upload-pack", &gz)
	req.Header.Set("Content-Encoding", "gzip")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if h.uploadPack == nil {
		t.Fatal("gzip body not decoded")
	}
	if len(h.uploadPack.Wants) != 1 {
		t.Fatalf("wants = %v", h.uploadPack.Wants)
	}
}

func TestPostNotImplementedReturns501(t *testing.T) {
	// NopHandler returns ErrNotImplemented and writes nothing.
	ts := newTestServer(wire.NopHandler{})
	defer ts.Close()

	oid := strings.Repeat("a", 40)
	body := pkt("want "+oid+"\n", "FLUSH", "done\n")
	resp, err := http.Post(ts.URL+"/org/repo.git/git-upload-pack", "application/x-git-upload-pack-request", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotImplemented {
		t.Fatalf("status = %d, want 501", resp.StatusCode)
	}
}

func TestDumbHTTPRejected(t *testing.T) {
	ts := newTestServer(&recordHandler{})
	defer ts.Close()

	for _, path := range []string{"/org/repo.git/HEAD", "/org/repo.git/objects/info/packs"} {
		resp, err := http.Get(ts.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Fatalf("%s: status = %d, want 403", path, resp.StatusCode)
		}
	}
}

func TestUnsafeRepoPathRejected(t *testing.T) {
	ts := newTestServer(&recordHandler{})
	defer ts.Close()
	resp, err := http.Get(ts.URL + "/..%2f..%2fetc/info/refs?service=git-upload-pack")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Go's net/http cleans the path; the key assertion is it is not 200 OK with
	// a served advertisement.
	if resp.StatusCode == http.StatusOK {
		t.Fatalf("unsafe path served with 200")
	}
}

func TestBasicAuthUserSurfaced(t *testing.T) {
	h := &recordHandler{}
	ts := newTestServer(h)
	defer ts.Close()

	req, _ := http.NewRequest("GET", ts.URL+"/org/repo.git/info/refs?service=git-upload-pack", nil)
	req.SetBasicAuth("alice", "hunter2")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if h.user != "alice" {
		t.Fatalf("user = %q, want alice", h.user)
	}
}

func TestParseGitProtocol(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"version=2", 2},
		{"version=0", 0},
		{"", 0},
		{"version=1", 0},
		{"key=value:version=2", 2},
	}
	for _, tt := range tests {
		if got := parseGitProtocol(tt.in); got != tt.want {
			t.Errorf("parseGitProtocol(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}
