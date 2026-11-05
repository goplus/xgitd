package xgitd

import "github.com/goplus/xgitd/protocol"

// The decoded request types live in the protocol package so the decoders and
// the Handler interface can share them without an import cycle. They are
// re-exported here as aliases so handler implementations only need to import
// this package.
type (
	// UploadPackRequest is a decoded v0 fetch/clone negotiation.
	UploadPackRequest = protocol.UploadPackRequest
	// ReceivePackRequest is a decoded v0 push.
	ReceivePackRequest = protocol.ReceivePackRequest
	// LsRefsRequest is a decoded v2 ls-refs command.
	LsRefsRequest = protocol.LsRefsRequest
	// FetchRequest is a decoded v2 fetch command.
	FetchRequest = protocol.FetchRequest
	// ObjectInfoRequest is a decoded v2 object-info command.
	ObjectInfoRequest = protocol.ObjectInfoRequest

	// RefUpdate is one ref-update command in a push.
	RefUpdate = protocol.RefUpdate
	// Capabilities is a parsed capability set.
	Capabilities = protocol.Capabilities
)

// Response types and writers, re-exported from the protocol package so a
// handler can build protocol-correct responses using only this package. The
// Handler is still free to write raw bytes to Session.Out, but these cover the
// framing every response needs.
type (
	// Ref is one reference to advertise (v0 advertisement or v2 ls-refs).
	Ref = protocol.Ref
	// RefAdvertisementV0 builds a v0 reference advertisement.
	RefAdvertisementV0 = protocol.RefAdvertisementV0
	// UploadPackResponder writes v0 upload-pack ACK/NAK negotiation and the
	// packfile section.
	UploadPackResponder = protocol.UploadPackResponder
	// ReportStatus is the v0 receive-pack report-status response.
	ReportStatus = protocol.ReportStatus
	// RefStatus is the outcome of one ref update in a push.
	RefStatus = protocol.RefStatus
	// FetchResponder writes a v2 fetch response (acknowledgments, shallow-info,
	// wanted-refs, packfile sections).
	FetchResponder = protocol.FetchResponder
	// WantedRef pairs a want-ref name with the object it resolved to.
	WantedRef = protocol.WantedRef
	// ObjectInfoEntry is the server's answer for one object in an object-info
	// response.
	ObjectInfoEntry = protocol.ObjectInfoEntry
)

// ZeroID is the all-zero object ID used on the wire to mean "no object".
const ZeroID = protocol.ZeroID

// ZeroIDFor returns the all-zero object ID at the width of the given
// object-format name.
func ZeroIDFor(format string) string { return protocol.ZeroIDFor(format) }

// NewUploadPackResponder returns a responder for a v0 upload-pack response,
// writing to s.Out. Pass sideband = req.Capabilities.Has("side-band-64k").
func NewUploadPackResponder(s *Session, sideband bool) *UploadPackResponder {
	return protocol.NewUploadPackResponder(s.Out, sideband)
}

// NewFetchResponder returns a responder for a v2 fetch response, writing to
// s.Out.
func NewFetchResponder(s *Session) *FetchResponder {
	return protocol.NewFetchResponder(s.Out)
}

// WriteLsRefs writes a v2 ls-refs response to s.Out for the given refs.
func WriteLsRefs(s *Session, req *LsRefsRequest, refs []Ref) error {
	return protocol.WriteLsRefs(s.Out, req, refs)
}

// WriteObjectInfo writes a v2 object-info response to s.Out for the given
// entries.
func WriteObjectInfo(s *Session, req *ObjectInfoRequest, entries []ObjectInfoEntry) error {
	return protocol.WriteObjectInfo(s.Out, req, entries)
}

// WriteRefAdvertisementV0 writes a v0 reference advertisement to s.Out. It is a
// convenience for the common AdvertiseRefs case.
func WriteRefAdvertisementV0(s *Session, adv RefAdvertisementV0) error {
	return adv.WriteV0(s.Out)
}

// WriteReportStatus writes a v0 receive-pack report-status response to s.Out.
// Pass sideband = req.Capabilities.Has("side-band-64k").
func WriteReportStatus(s *Session, rs ReportStatus, sideband bool) error {
	return rs.WriteV0(s.Out, sideband)
}
