// Package http implements the Smart HTTP transport for a Git server.
//
// It exposes three routes relative to a repository path:
//
//	GET  /<repo>/info/refs?service=git-upload-pack|git-receive-pack   discovery
//	POST /<repo>/git-upload-pack                                      fetch/clone
//	POST /<repo>/git-receive-pack                                     push
//
// The transport owns the details a handler should not have to care about:
// choosing the decoder from the Git-Protocol header, transparently gunzipping
// request bodies, setting the correct Content-Type and Cache-Control on
// responses, rejecting dumb-HTTP style access, and surfacing the basic-auth
// user as [xgitd.Session.User].
package http

import (
	"bytes"
	"compress/gzip"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/goplus/xgitd"
)

// Handler is an http.Handler that serves the Smart HTTP Git protocol for a
// given [xgitd.Server].
type Handler struct {
	// Server dispatches decoded requests. Required.
	Server *xgitd.Server
}

// NewHandler returns a [Handler] for srv.
func NewHandler(srv *xgitd.Server) *Handler {
	return &Handler{Server: srv}
}

// ServeHTTP implements http.Handler.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	path := r.URL.Path

	switch {
	case strings.HasSuffix(path, "/info/refs") && r.Method == http.MethodGet:
		h.serveInfoRefs(w, r, strings.TrimSuffix(path, "/info/refs"))
	case strings.HasSuffix(path, "/"+xgitd.ServiceUploadPack) && r.Method == http.MethodPost:
		h.servePost(w, r, strings.TrimSuffix(path, "/"+xgitd.ServiceUploadPack), xgitd.ServiceUploadPack)
	case strings.HasSuffix(path, "/"+xgitd.ServiceReceivePack) && r.Method == http.MethodPost:
		h.servePost(w, r, strings.TrimSuffix(path, "/"+xgitd.ServiceReceivePack), xgitd.ServiceReceivePack)
	default:
		// Anything else is dumb-HTTP style static access: HEAD, objects/...,
		// loose/packed object fetches. Reject it clearly.
		if isDumbHTTPPath(path) {
			http.Error(w, "dumb HTTP is not supported", http.StatusForbidden)
			return
		}
		http.NotFound(w, r)
	}
}

// serveInfoRefs handles the discovery request. It validates the service,
// writes the service banner, then either the v2 capability advertisement or
// (v0) the handler's ref advertisement.
//
// The response body is assembled in a buffer first, so that a v0 handler that
// fails (for example a NopHandler returning ErrNotImplemented) yields a clean
// 501/500 status instead of a 200 with a half-formed advertisement.
func (h *Handler) serveInfoRefs(w http.ResponseWriter, r *http.Request, rawRepo string) {
	service := r.URL.Query().Get("service")
	if service != xgitd.ServiceUploadPack && service != xgitd.ServiceReceivePack {
		// No service means a dumb-HTTP client asking for a static info/refs.
		http.Error(w, "dumb HTTP is not supported", http.StatusForbidden)
		return
	}

	repo, err := xgitd.NormalizeRepoPath(rawRepo)
	if err != nil {
		http.Error(w, "invalid repository path", http.StatusBadRequest)
		return
	}

	version := parseGitProtocol(r.Header.Get("Git-Protocol"))

	var body bytes.Buffer
	s := &xgitd.Session{
		Transport:  "http",
		Service:    service,
		Repo:       repo,
		Version:    version,
		User:       basicAuthUser(r),
		RemoteAddr: r.RemoteAddr,
		In:         r.Body,
		Out:        &body,
	}

	// The service banner is a pkt-line "# service=<name>\n" followed by a
	// flush, then the advertisement body.
	if err := writeServiceBanner(&body, service); err != nil {
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}

	if version == 2 {
		if err := h.Server.WriteV2Advertisement(s); err != nil {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	} else {
		// v0: the handler produces the real ref advertisement.
		if err := h.Server.AdvertiseRefs(r.Context(), s); err != nil {
			if errors.Is(err, xgitd.ErrNotImplemented) {
				http.Error(w, "not implemented", http.StatusNotImplemented)
				return
			}
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
	}

	setNoCache(w)
	w.Header().Set("Content-Type", "application/x-"+service+"-advertisement")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body.Bytes())
}

// servePost handles an upload-pack or receive-pack POST: the request body is
// the client's command/negotiation stream, and the response is whatever the
// handler writes.
func (h *Handler) servePost(w http.ResponseWriter, r *http.Request, rawRepo, service string) {
	repo, err := xgitd.NormalizeRepoPath(rawRepo)
	if err != nil {
		http.Error(w, "invalid repository path", http.StatusBadRequest)
		return
	}

	body, err := requestBody(r)
	if err != nil {
		http.Error(w, "bad request body", http.StatusBadRequest)
		return
	}
	defer body.Close()

	version := parseGitProtocol(r.Header.Get("Git-Protocol"))

	setNoCache(w)
	// Buffer the response so that, if the handler fails before writing
	// anything, we can still send a clean HTTP status instead of a half-formed
	// 200.
	rw := newResultWriter(w, service)

	s := &xgitd.Session{
		Transport:  "http",
		Service:    service,
		Repo:       repo,
		Version:    version,
		User:       basicAuthUser(r),
		RemoteAddr: r.RemoteAddr,
		In:         body,
		Out:        rw,
	}

	if err := h.Server.Dispatch(r.Context(), s); err != nil {
		h.reportPostError(rw, err)
		return
	}
	rw.flushHeaders() // ensure a 200 even if the handler wrote nothing.
}

// reportPostError translates a dispatch error into the right client response.
// If nothing has been written yet, it maps ErrNotImplemented to 501 and any
// other error to 500. If the response has already begun, the status is fixed,
// so the error is surfaced in-band as an ERR pkt-line instead.
func (h *Handler) reportPostError(rw *resultWriter, err error) {
	if rw.wroteBody() {
		_ = xgitd.WriteErrorFor(rw, err)
		return
	}
	if errors.Is(err, xgitd.ErrNotImplemented) {
		rw.httpError("not implemented", http.StatusNotImplemented)
		return
	}
	rw.httpError("internal server error", http.StatusInternalServerError)
}

// requestBody returns the request body, transparently decompressing it when the
// client set Content-Encoding: gzip.
func requestBody(r *http.Request) (io.ReadCloser, error) {
	if strings.EqualFold(r.Header.Get("Content-Encoding"), "gzip") {
		zr, err := gzip.NewReader(r.Body)
		if err != nil {
			return nil, err
		}
		return &gzipBody{zr: zr, underlying: r.Body}, nil
	}
	return r.Body, nil
}

// gzipBody closes both the gzip reader and the underlying body.
type gzipBody struct {
	zr         *gzip.Reader
	underlying io.ReadCloser
}

func (g *gzipBody) Read(p []byte) (int, error) { return g.zr.Read(p) }
func (g *gzipBody) Close() error {
	err := g.zr.Close()
	if cerr := g.underlying.Close(); cerr != nil && err == nil {
		err = cerr
	}
	return err
}

// basicAuthUser returns the HTTP basic-auth username, or "" if none was sent.
func basicAuthUser(r *http.Request) string {
	user, _, ok := r.BasicAuth()
	if !ok {
		return ""
	}
	return user
}

// setNoCache applies the cache headers Git expects on dynamic responses.
func setNoCache(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Expires", "Fri, 01 Jan 1980 00:00:00 GMT")
	w.Header().Set("Pragma", "no-cache")
}
