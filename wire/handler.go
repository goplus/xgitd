// Package wire is a lightweight Git server skeleton: it decodes every
// supported Git wire-protocol request into typed Go values and dispatches to a
// single [Handler] interface.
//
// The design deliberately stops at the wire. There is no object storage, no
// pack parsing, and no ref updates. A push's packfile is handed to the handler
// as an opaque [io.Reader]; a fetch response is whatever the handler writes to
// [Session.Out]. This keeps the protocol surface small, testable, and reusable:
// later work can plug a real backend behind the same [Handler] without touching
// any wire-level code.
//
// The package carries no built-in authorization policy, but it offers an
// optional gate: set [Server.Authorizer] to accept or reject each session by its
// metadata before any handler runs. See [Authorizer].
package wire

import (
	"context"
	"errors"
	"io"
)

// ErrNotImplemented is returned by [BaseHandler] methods and by any handler that
// has not implemented a particular request. Transports translate it into the
// protocol's "not implemented" signal: an ERR pkt-line for SSH, and HTTP 501
// when nothing has been written to the client yet.
var ErrNotImplemented = errors.New("xgitd: not implemented")

// Session is the transport-independent view of a single Git request. Both the
// SSH and HTTP transports reduce a connection to a Session before any
// protocol-specific decoding happens.
type Session struct {
	// Transport is "ssh" or "http".
	Transport string
	// Service is "git-upload-pack" or "git-receive-pack".
	Service string
	// Repo is the normalized repository path, e.g. "org/repo.git". Leading
	// slashes and any scp-style "host:" prefix have been stripped, and paths
	// containing ".." are rejected before a Session is built.
	Repo string
	// Version is the negotiated protocol version, 0 or 2.
	Version int
	// User is the SSH public-key fingerprint or the HTTP basic-auth username.
	// It may be empty. The transport only surfaces it; it is the
	// [Server.Authorizer] or the handler, not the wire layer, that decides what
	// it means for access.
	User string
	// RemoteAddr is the client's network address, when the transport knows it.
	RemoteAddr string

	// In is the request stream. For HTTP it has already been gunzipped when the
	// client used Content-Encoding: gzip.
	In io.Reader
	// Out is the response stream.
	Out io.Writer
}

// Handler receives every decoded request. Each method corresponds to one
// protocol operation. A method should write its response to s.Out (or use the
// side-band writer from the pktline package) and return nil, or return
// [ErrNotImplemented] / another error to signal failure.
//
// Implementations should embed [BaseHandler] so that protocol operations added
// to this interface later do not break existing handlers.
type Handler interface {
	// AdvertiseRefs is the v0 reference advertisement. In v0 the advertised
	// refs are real refs, so producing them is the handler's job.
	AdvertiseRefs(ctx context.Context, s *Session) error
	// UploadPack handles a decoded v0 fetch/clone negotiation.
	UploadPack(ctx context.Context, s *Session, req *UploadPackRequest) error
	// ReceivePack handles a decoded v0 push.
	ReceivePack(ctx context.Context, s *Session, req *ReceivePackRequest) error

	// LsRefs handles a decoded v2 ls-refs command.
	LsRefs(ctx context.Context, s *Session, req *LsRefsRequest) error
	// Fetch handles a decoded v2 fetch command.
	Fetch(ctx context.Context, s *Session, req *FetchRequest) error
	// ObjectInfo handles a decoded v2 object-info command.
	ObjectInfo(ctx context.Context, s *Session, req *ObjectInfoRequest) error
}

// BaseHandler is the empty [Handler]: every method returns [ErrNotImplemented].
// Embed it to implement only the operations you care about.
type BaseHandler struct{}

// AdvertiseRefs implements [Handler].
func (BaseHandler) AdvertiseRefs(ctx context.Context, s *Session) error {
	return ErrNotImplemented
}

// UploadPack implements [Handler].
func (BaseHandler) UploadPack(ctx context.Context, s *Session, req *UploadPackRequest) error {
	return ErrNotImplemented
}

// ReceivePack implements [Handler].
func (BaseHandler) ReceivePack(ctx context.Context, s *Session, req *ReceivePackRequest) error {
	return ErrNotImplemented
}

// LsRefs implements [Handler].
func (BaseHandler) LsRefs(ctx context.Context, s *Session, req *LsRefsRequest) error {
	return ErrNotImplemented
}

// Fetch implements [Handler].
func (BaseHandler) Fetch(ctx context.Context, s *Session, req *FetchRequest) error {
	return ErrNotImplemented
}

// ObjectInfo implements [Handler].
func (BaseHandler) ObjectInfo(ctx context.Context, s *Session, req *ObjectInfoRequest) error {
	return ErrNotImplemented
}

// Ensure NopHandler satisfies Handler.
var _ Handler = BaseHandler{}
