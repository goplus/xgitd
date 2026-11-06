package wire

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/goplus/xgitd/internal/pktline"
	"github.com/goplus/xgitd/internal/protocol"
)

// Services.
const (
	ServiceUploadPack  = "git-upload-pack"
	ServiceReceivePack = "git-receive-pack"
)

// Server dispatches decoded Git requests to a [Handler]. The zero value is
// usable but has no Handler, so every request reports ErrNotImplemented; set
// Handler to do useful work.
type Server struct {
	// Handler receives every decoded request. When nil, NopHandler is used.
	Handler Handler
	// Capabilities is the static v2 capability advertisement. When it is the
	// zero value, DefaultCapabilityAdvertisement is used.
	Capabilities protocol.CapabilityAdvertisement
	// Authorizer, when set, gates every session: the transport calls
	// [Server.Authorize] once before advertising or dispatching, and a rejected
	// session never reaches the Handler. When nil, every session is allowed.
	Authorizer Authorizer
}

// WriteV2Advertisement writes the static v2 capability advertisement to s.Out.
// Transports call it during discovery so a v2 client knows which commands it
// may issue.
func (srv *Server) WriteV2Advertisement(s *Session) error {
	return srv.Capabilities.WriteV2(s.Out)
}

// Dispatch decodes the request described by s and routes it to the handler.
//
// The flow depends on s.Service and s.Version:
//
//   - v2 (either service): read one command frame from s.In and route to
//     LsRefs, Fetch, or ObjectInfo. This is the stateless shape used by HTTP,
//     where each command arrives in its own POST. A stateful transport that may
//     receive several v2 commands on one connection uses [Server.DispatchV2]
//     instead.
//   - v0 git-upload-pack: decode a negotiation round and call UploadPack.
//   - v0 git-receive-pack: decode the push and call ReceivePack.
//
// The caller handles the v0 ref advertisement separately (via
// [Server.AdvertiseRefs]); Dispatch covers the request body that follows.
func (srv *Server) Dispatch(ctx context.Context, s *Session) error {
	if s.Version == protocol.VersionV2 {
		cmd, req, err := protocol.DecodeV2(s.In)
		if err != nil {
			return err
		}
		return srv.routeV2(ctx, s, cmd, req)
	}
	switch s.Service {
	case ServiceUploadPack:
		req, err := protocol.DecodeUploadPackV0(s.In)
		if err != nil {
			return err
		}
		return srv.Handler.UploadPack(ctx, s, req)
	case ServiceReceivePack:
		req, err := protocol.DecodeReceivePackV0(s.In)
		if err != nil {
			return err
		}
		return srv.Handler.ReceivePack(ctx, s, req)
	default:
		return fmt.Errorf("xgitd: unsupported service %q", s.Service)
	}
}

// DispatchV2 handles a v2 session on a stateful connection: it reads and routes
// successive commands from s.In until the client ends the sequence with a
// flush-pkt. This is what SSH needs, since a v2 client issues several commands
// (for example ls-refs then fetch) on the same channel.
//
// Each command's response is written to s.Out by the handler as it is routed,
// so the client sees the full request/response interleaving the protocol
// expects.
func (srv *Server) DispatchV2(ctx context.Context, s *Session) error {
	dec := protocol.NewV2Decoder(s.In)
	for {
		cmd, req, err := dec.Next()
		if errors.Is(err, protocol.ErrNoMoreCommands) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := srv.routeV2(ctx, s, cmd, req); err != nil {
			return err
		}
	}
}

// AdvertiseRefs invokes the handler's v0 reference advertisement.
func (srv *Server) AdvertiseRefs(ctx context.Context, s *Session) error {
	return srv.Handler.AdvertiseRefs(ctx, s)
}

// routeV2 routes a single decoded v2 command to the handler.
func (srv *Server) routeV2(ctx context.Context, s *Session, cmd string, req any) error {
	h := srv.Handler
	switch r := req.(type) {
	case *protocol.LsRefsRequest:
		return h.LsRefs(ctx, s, r)
	case *protocol.FetchRequest:
		return h.Fetch(ctx, s, r)
	case *protocol.ObjectInfoRequest:
		return h.ObjectInfo(ctx, s, r)
	default:
		return fmt.Errorf("xgitd: unroutable v2 command %q", cmd)
	}
}

// WriteError writes a protocol-level error to w as an ERR pkt-line, the form
// SSH and v0/v2 streams use to report a failure to the client. The message is
// prefixed with "ERR " per the Git convention.
func WriteError(w io.Writer, msg string) error {
	pw := pktline.NewWriter(w)
	return pw.WriteString("ERR " + msg + "\n")
}

// WriteErrorFor writes the client-facing ERR pkt-line that corresponds to err.
// It keeps the "not implemented" case recognizable and hides any other error
// behind a generic message so internal detail does not leak to clients.
func WriteErrorFor(w io.Writer, err error) error {
	return WriteError(w, ClientErrorMessage(err))
}

// ClientErrorMessage maps an error to the client-facing message that transports
// surface to the client. It keeps the "not implemented" and "unauthorized" cases
// recognizable and hides the detail of any other error behind a generic message,
// since internal errors should not leak to clients. Transports use it as the
// single source of truth for this wording (for example, the Smart HTTP 403 body
// and the SSH ERR pkt-line share it).
func ClientErrorMessage(err error) string {
	switch {
	case errors.Is(err, ErrNotImplemented):
		return "not implemented"
	case errors.Is(err, ErrUnauthorized):
		return "unauthorized"
	default:
		return "internal server error"
	}
}
