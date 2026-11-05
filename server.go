package xgitd

import (
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/goplus/xgitd/pktline"
	"github.com/goplus/xgitd/protocol"
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
}

// handler returns the configured handler or a NopHandler.
func (srv *Server) handler() Handler {
	if srv.Handler == nil {
		return NopHandler{}
	}
	return srv.Handler
}

// advertisement returns the configured advertisement or the default.
func (srv *Server) advertisement() protocol.CapabilityAdvertisement {
	// A zero CapabilityAdvertisement advertises nothing, which is never useful,
	// so treat "no commands advertised" as "use the default".
	if !srv.Capabilities.LsRefs && !srv.Capabilities.Fetch && !srv.Capabilities.ObjectInfo {
		return protocol.DefaultCapabilityAdvertisement()
	}
	return srv.Capabilities
}

// WriteV2Advertisement writes the static v2 capability advertisement to s.Out.
// Transports call it during discovery so a v2 client knows which commands it
// may issue.
func (srv *Server) WriteV2Advertisement(s *Session) error {
	return srv.advertisement().WriteV2(s.Out)
}

// Dispatch decodes the request described by s and routes it to the handler.
//
// The flow depends on s.Service and s.Version:
//
//   - v2 (either service): read one command frame from s.In and route to
//     LsRefs, Fetch, or ObjectInfo.
//   - v0 git-upload-pack: decode a negotiation round and call UploadPack.
//   - v0 git-receive-pack: decode the push and call ReceivePack.
//
// The caller handles the v0 ref advertisement separately (via
// [Server.AdvertiseRefs]); Dispatch covers the request body that follows.
func (srv *Server) Dispatch(ctx context.Context, s *Session) error {
	if s.Version == protocol.VersionV2 {
		return srv.dispatchV2(ctx, s)
	}
	switch s.Service {
	case ServiceUploadPack:
		req, err := protocol.DecodeUploadPackV0(s.In)
		if err != nil {
			return err
		}
		return srv.handler().UploadPack(ctx, s, req)
	case ServiceReceivePack:
		req, err := protocol.DecodeReceivePackV0(s.In)
		if err != nil {
			return err
		}
		return srv.handler().ReceivePack(ctx, s, req)
	default:
		return fmt.Errorf("xgitd: unsupported service %q", s.Service)
	}
}

// AdvertiseRefs invokes the handler's v0 reference advertisement.
func (srv *Server) AdvertiseRefs(ctx context.Context, s *Session) error {
	return srv.handler().AdvertiseRefs(ctx, s)
}

// dispatchV2 reads one v2 command frame and routes it.
func (srv *Server) dispatchV2(ctx context.Context, s *Session) error {
	cmd, req, err := protocol.DecodeV2(s.In)
	if err != nil {
		return err
	}
	h := srv.handler()
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
	return WriteError(w, errorMessage(err))
}

// errorMessage maps a handler error to the client-facing message. It keeps the
// "not implemented" case recognizable and hides the detail of any other error
// behind a generic message, since internal errors should not leak to clients.
func errorMessage(err error) string {
	if errors.Is(err, ErrNotImplemented) {
		return "not implemented"
	}
	return "internal server error"
}
