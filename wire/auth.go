package wire

import (
	"context"
	"errors"
)

// ErrUnauthorized is returned by an [Authorizer] (and by [Server.Authorize]) to
// reject a session. Transports translate it into the protocol's access-denied
// signal: an ERR pkt-line for SSH, and HTTP 403 for Smart HTTP.
//
// An Authorizer may also return its own error wrapping ErrUnauthorized; use
// errors.Is to recognize it. Any non-nil error that is not ErrUnauthorized is
// treated as an internal failure and reported generically, so authorization
// internals never leak to the client.
var ErrUnauthorized = errors.New("xgitd: unauthorized")

// Authorizer decides whether a decoded [Session] may proceed. It runs once per
// session, after the transport has built the Session (so Service, Repo, User,
// RemoteAddr, and Version are populated) but before any advertisement or request
// dispatch. Returning nil allows the session; returning [ErrUnauthorized]
// rejects it; returning any other error aborts it as an internal failure.
//
// The Session's In/Out streams are present but must not be consumed by an
// Authorizer: reading In would steal bytes from the decoder, and writing Out
// would corrupt the response framing. Authorization decisions should rest on the
// metadata fields alone.
type Authorizer interface {
	Authorize(ctx context.Context, s *Session) error
}

// AuthorizeFunc adapts a plain function to the [Authorizer] interface.
type AuthorizeFunc func(ctx context.Context, s *Session) error

// Authorize implements [Authorizer].
func (f AuthorizeFunc) Authorize(ctx context.Context, s *Session) error {
	return f(ctx, s)
}

// Authorize runs the configured [Server.Authorizer] against s, or returns nil
// when no authorizer is set (the default: every session is allowed).
//
// Transports call it once per session, before writing any advertisement or
// dispatching any request, so an unauthorized session is rejected before the
// handler ever sees it. A caller driving [Server] directly should do the same.
func (srv *Server) Authorize(ctx context.Context, s *Session) error {
	if srv.Authorizer == nil {
		return nil
	}
	return srv.Authorizer.Authorize(ctx, s)
}
