// Command xgitd is an example binary that wires the xgitd protocol layers and
// transports together behind a handler that only logs what it decodes.
//
// It is intentionally a skeleton: the handler returns ErrNotImplemented for
// every operation, so real git clients talking to it will fail. The point is to
// show the intended embedding and to let you watch what the decoders produce
// when you point a real client at the server:
//
//	# Terminal 1
//	go run ./cmd/xgitd -http :8080 -ssh :2222
//
//	# Terminal 2
//	git -c protocol.version=2 ls-remote http://localhost:8080/org/repo.git
//	GIT_SSH_COMMAND='ssh -p 2222' git ls-remote git@localhost:org/repo.git
//
// The git commands are expected to fail; the server logs the decoded request.
//
// A handler that actually responds uses the response helpers on the xgitd
// package — WriteRefAdvertisementV0, NewUploadPackResponder, WriteLsRefs,
// NewFetchResponder, WriteObjectInfo, and WriteReportStatus — to frame its
// output on the wire. The e2e package contains a worked example (a handler
// backed by a real repository that git can clone from) in clone_test.go.
package main

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"flag"
	"log"
	"net/http"

	gossh "golang.org/x/crypto/ssh"

	"github.com/goplus/xgitd"
	xhttp "github.com/goplus/xgitd/transport/http"
	xssh "github.com/goplus/xgitd/transport/ssh"
)

// logHandler embeds NopHandler and logs each decoded request before deferring
// to the empty implementation (which returns ErrNotImplemented).
type logHandler struct {
	xgitd.NopHandler
}

func (logHandler) AdvertiseRefs(ctx context.Context, s *xgitd.Session) error {
	log.Printf("advertise-refs transport=%s service=%s repo=%s version=%d user=%q",
		s.Transport, s.Service, s.Repo, s.Version, s.User)
	return xgitd.ErrNotImplemented
}

func (logHandler) UploadPack(ctx context.Context, s *xgitd.Session, req *xgitd.UploadPackRequest) error {
	log.Printf("upload-pack repo=%s wants=%d haves=%d done=%v caps=%v",
		s.Repo, len(req.Wants), len(req.Haves), req.Done, req.Capabilities.Names())
	return xgitd.ErrNotImplemented
}

func (logHandler) ReceivePack(ctx context.Context, s *xgitd.Session, req *xgitd.ReceivePackRequest) error {
	log.Printf("receive-pack repo=%s commands=%d push-options=%v has-pack=%v",
		s.Repo, len(req.Commands), req.PushOptions, req.Packfile != nil)
	return xgitd.ErrNotImplemented
}

func (logHandler) LsRefs(ctx context.Context, s *xgitd.Session, req *xgitd.LsRefsRequest) error {
	log.Printf("ls-refs repo=%s prefixes=%v symrefs=%v peel=%v",
		s.Repo, req.RefPrefixes, req.Symrefs, req.Peel)
	return xgitd.ErrNotImplemented
}

func (logHandler) Fetch(ctx context.Context, s *xgitd.Session, req *xgitd.FetchRequest) error {
	log.Printf("fetch repo=%s wants=%d haves=%d done=%v filter=%q",
		s.Repo, len(req.Wants), len(req.Haves), req.Done, req.Filter)
	return xgitd.ErrNotImplemented
}

func (logHandler) ObjectInfo(ctx context.Context, s *xgitd.Session, req *xgitd.ObjectInfoRequest) error {
	log.Printf("object-info repo=%s attrs=%v oids=%d", s.Repo, req.Attributes, len(req.OIDs))
	return xgitd.ErrNotImplemented
}

func main() {
	httpAddr := flag.String("http", ":8080", "Smart HTTP listen address (empty to disable)")
	sshAddr := flag.String("ssh", ":2222", "SSH listen address (empty to disable)")
	flag.Parse()

	srv := &xgitd.Server{Handler: logHandler{}}

	errc := make(chan error, 2)

	if *sshAddr != "" {
		hostKey, err := generateHostKey()
		if err != nil {
			log.Fatalf("generate host key: %v", err)
		}
		cfg := &gossh.ServerConfig{
			// Accept any key and surface its fingerprint; a real deployment
			// supplies its own policy.
			PublicKeyCallback: func(conn gossh.ConnMetadata, key gossh.PublicKey) (*gossh.Permissions, error) {
				return xssh.FingerprintAuthCallback()(conn, key)
			},
		}
		cfg.AddHostKey(hostKey)
		tr := &xssh.Transport{Server: srv, Config: cfg}
		go func() {
			log.Printf("ssh listening on %s", *sshAddr)
			errc <- tr.ListenAndServe(*sshAddr)
		}()
	}

	if *httpAddr != "" {
		handler := xhttp.NewHandler(srv)
		go func() {
			log.Printf("http listening on %s", *httpAddr)
			errc <- http.ListenAndServe(*httpAddr, handler)
		}()
	}

	if *sshAddr == "" && *httpAddr == "" {
		log.Fatal("both -http and -ssh are empty; nothing to serve")
	}

	log.Fatal(<-errc)
}

// generateHostKey makes an ephemeral ed25519 host key for the example. A real
// server loads a persistent key from disk.
func generateHostKey() (gossh.Signer, error) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	return gossh.NewSignerFromKey(priv)
}
