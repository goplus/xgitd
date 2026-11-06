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

// Authorizer decides whether a decoded [Session] may proceed. It runs after the
// transport has built the Session (so Service, Repo, User, RemoteAddr, and
// Version are populated) but before any advertisement or request dispatch.
// Returning nil allows the session; returning [ErrUnauthorized] rejects it;
// returning any other error aborts it as an internal failure.
//
// It runs before each advertisement/dispatch, not once per logical git
// operation. On the stateful SSH transport that is once per connection, but on
// the stateless Smart HTTP transport a single clone/fetch/push is two separate
// requests — GET /info/refs then the follow-up POST — each of which builds its
// own Session and calls the Authorizer. An Authorizer should therefore be
// idempotent and side-effect-free; one that, for example, records an audit entry
// or increments a rate-limit counter will fire twice per HTTP operation.
//
// Authorization decisions must rest on the metadata fields alone: s.In may be
// nil (the HTTP POST path sets it only after authorization) and must not be
// read, and s.Out must not be written (doing so would corrupt the response
// framing).
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
// Transports call it before writing any advertisement or dispatching any
// request, so an unauthorized session is rejected before the handler ever sees
// it. See [Authorizer] for the per-request (not per-operation) invocation
// contract. A caller driving [Server] directly should do the same.
func (srv *Server) Authorize(ctx context.Context, s *Session) error {
	if srv.Authorizer == nil {
		return nil
	}
	return srv.Authorizer.Authorize(ctx, s)
}
