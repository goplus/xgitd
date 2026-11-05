package http

import (
	"io"
	"net/http"
	"strings"

	"github.com/goplus/xgitd/internal/pktline"
)

// parseGitProtocol reads the protocol version from a Git-Protocol header value
// such as "version=2". Anything other than an explicit version=2 is v0, so a
// proxy that strips the header silently downgrades the client to v0 without
// error, as required.
func parseGitProtocol(v string) int {
	for _, field := range strings.Split(v, ":") {
		if ver, ok := strings.CutPrefix(field, "version="); ok {
			if strings.TrimSpace(ver) == "2" {
				return 2
			}
		}
	}
	return 0
}

// writeServiceBanner writes the Smart HTTP discovery banner:
//
//	<pkt>"# service=<name>\n"
//	0000
//
// which precedes the advertisement body.
func writeServiceBanner(w io.Writer, service string) error {
	pw := pktline.NewWriter(w)
	if err := pw.WriteString("# service=" + service + "\n"); err != nil {
		return err
	}
	return pw.Flush()
}

// isDumbHTTPPath reports whether path looks like a dumb-HTTP static request
// (direct access to HEAD, refs, or the object store), which the Smart HTTP
// transport refuses.
func isDumbHTTPPath(path string) bool {
	for _, marker := range []string{
		"/HEAD",
		"/objects/",
		"/info/packs",
		"/info/alternates",
		"/info/http-alternates",
		"/packed-refs",
	} {
		if strings.HasSuffix(path, marker) || strings.Contains(path, marker) {
			return true
		}
	}
	return false
}

// resultWriter wraps the http.ResponseWriter for a POST response. It defers
// writing the status line and Content-Type header until the first body byte, so
// a handler that fails before producing output can still be answered with a
// clean 4xx/5xx status.
type resultWriter struct {
	w       http.ResponseWriter
	service string
	wrote   bool
	// failed records an httpError already sent; after it, body writes are
	// dropped.
	failed bool
}

func newResultWriter(w http.ResponseWriter, service string) *resultWriter {
	return &resultWriter{w: w, service: service}
}

// Write implements io.Writer, emitting the success headers on first use.
func (rw *resultWriter) Write(p []byte) (int, error) {
	if rw.failed {
		return len(p), nil
	}
	rw.flushHeaders()
	return rw.w.Write(p)
}

// flushHeaders writes the 200 status and result Content-Type exactly once.
func (rw *resultWriter) flushHeaders() {
	if rw.wrote {
		return
	}
	rw.wrote = true
	rw.w.Header().Set("Content-Type", "application/x-"+rw.service+"-result")
	rw.w.WriteHeader(http.StatusOK)
}

// wroteBody reports whether any response bytes (or the success header) have
// been committed.
func (rw *resultWriter) wroteBody() bool { return rw.wrote }

// httpError sends an error status, provided nothing has been committed yet.
func (rw *resultWriter) httpError(msg string, code int) {
	if rw.wrote {
		return
	}
	rw.wrote = true
	rw.failed = true
	http.Error(rw.w, msg, code)
}
